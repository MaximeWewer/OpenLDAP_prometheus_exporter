package exporter

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Constants for helper functions
const (
	// Maximum allowed key length for counter entries
	MaxKeyLength = 512
	
	// Jitter calculation constants
	JitterOffset     = 0.5
	JitterMultiplier = 2.0
)

// atomicFloat64 provides atomic operations for float64 values
type atomicFloat64 struct {
	value uint64
}

// Load atomically loads the float64 value
func (af *atomicFloat64) Load() float64 {
	return math.Float64frombits(atomic.LoadUint64(&af.value))
}

// Store atomically stores the float64 value
func (af *atomicFloat64) Store(val float64) {
	atomic.StoreUint64(&af.value, math.Float64bits(val))
}

// CompareAndSwap atomically compares and swaps the float64 value
func (af *atomicFloat64) CompareAndSwap(old, new float64) bool {
	return atomic.CompareAndSwapUint64(&af.value, math.Float64bits(old), math.Float64bits(new))
}

// validateKey performs comprehensive key validation for map operations
func validateKey(key string) error {
	if key == "" {
		logger.SafeWarn("exporter", "Empty key validation failed", map[string]interface{}{
			"key": key,
		})
		return errors.New("empty key not allowed")
	}
	if len(key) > MaxKeyLength { // Reasonable limit for key length
		logger.SafeWarn("exporter", "Key too long", map[string]interface{}{
			"key_length": len(key),
			"max_length": MaxKeyLength,
		})
		return errors.New("key too long")
	}
	// Check for null bytes or other problematic characters
	for _, r := range key {
		if r == 0 || r == '\r' || r == '\n' {
			logger.SafeWarn("exporter", "Key contains invalid character", map[string]interface{}{
				"character": string(r),
			})
			return errors.New("key contains invalid character")
		}
	}
	return nil
}

// isRetryableError determines if an error should be retried
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for common retryable error patterns
	errorStr := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"connection refused",
		"timeout",
		"network is unreachable",
		"temporary failure",
		"server closed",
		"broken pipe",
		"no such host",
		"connection reset",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errorStr, pattern) {
			return true
		}
	}

	return false
}

// RetryConfig holds configuration for retry logic
type RetryConfig struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
	JitterFactor  float64
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:   MaxRetryAttempts,
		InitialDelay:  InitialRetryDelay,
		MaxDelay:      MaxRetryDelay,
		BackoffFactor: RetryBackoffFactor,
		JitterFactor:  RetryJitterFactor,
	}
}

// calculateDelay calculates the delay for a retry attempt with exponential backoff and jitter
func (rc *RetryConfig) calculateDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return rc.InitialDelay
	}

	// Calculate exponential backoff
	delay := float64(rc.InitialDelay)
	for i := 0; i < attempt; i++ {
		delay *= rc.BackoffFactor
	}

	// Cap at max delay
	if delay > float64(rc.MaxDelay) {
		delay = float64(rc.MaxDelay)
	}

	// Add jitter to prevent thundering herd
	jitter := (rand.Float64() - JitterOffset) * JitterMultiplier * rc.JitterFactor * delay
	finalDelay := time.Duration(delay + jitter)

	// Ensure we don't go below initial delay
	if finalDelay < rc.InitialDelay {
		finalDelay = rc.InitialDelay
	}

	return finalDelay
}

// retryOperation performs an operation with retry logic
func (e *OpenLDAPExporter) retryOperation(ctx context.Context, operation string, fn func() error) error {
	config := DefaultRetryConfig()
	var lastErr error

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Check context before attempting
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Execute the operation
		err := fn()
		if err == nil {
			// Success - reset any error counters if needed
			if attempt > 0 {
				logger.SafeInfo("exporter", "Operation succeeded after retry", map[string]interface{}{
					"operation": operation,
					"attempt":   attempt + 1,
				})
			}
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			logger.SafeDebug("exporter", "Non-retryable error", map[string]interface{}{
				"operation": operation,
				"error":     err.Error(),
			})
			return err
		}

		// Don't retry on last attempt
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate delay for next retry
		delay := config.calculateDelay(attempt)

		logger.SafeDebug("exporter", "Retrying operation", map[string]interface{}{
			"operation": operation,
			"attempt":   attempt + 1,
			"delay":     delay.String(),
			"error":     err.Error(),
		})

		// Wait before retrying
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// All retries exhausted
	logger.SafeError("exporter", "Operation failed after all retries", lastErr, map[string]interface{}{
		"operation":    operation,
		"max_attempts": config.MaxAttempts,
	})

	return lastErr
}

// extractCNFromFirstComponent extracts the CN value from the first component of a DN
func extractCNFromFirstComponent(dn string) string {
	if !strings.Contains(dn, "cn=") {
		return ""
	}
	
	parts := strings.Split(dn, ",")
	if len(parts) == 0 {
		return ""
	}
	
	cnPart := parts[0]
	if strings.HasPrefix(cnPart, "cn=") {
		return strings.TrimPrefix(cnPart, "cn=")
	}
	
	return ""
}

// extractDomainComponents extracts DC components from a DN
func (e *OpenLDAPExporter) extractDomainComponents(dn string) []string {
	var components []string
	parts := strings.Split(strings.ToLower(dn), ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "dc=") {
			component := strings.TrimPrefix(part, "dc=")
			if component != "" {
				components = append(components, component)
			}
		}
	}

	return components
}

// shouldIncludeDomain checks if a domain should be included based on filters
func (e *OpenLDAPExporter) shouldIncludeDomain(domainComponents []string) bool {
	// If no filters are configured, include everything
	if len(e.config.DCInclude) == 0 && len(e.config.DCExclude) == 0 {
		return true
	}

	// Check include list first (if specified)
	if len(e.config.DCInclude) > 0 {
		for _, dc := range domainComponents {
			for _, include := range e.config.DCInclude {
				if strings.EqualFold(dc, include) {
					return true
				}
			}
		}
		// If include list is specified but no match, exclude
		return false
	}

	// Check exclude list
	for _, dc := range domainComponents {
		for _, exclude := range e.config.DCExclude {
			if strings.EqualFold(dc, exclude) {
				return false
			}
		}
	}

	return true
}

// shouldCollectMetric checks if a metric group should be collected based on filters
func (e *OpenLDAPExporter) shouldCollectMetric(metricGroup string) bool {
	return e.config.ShouldCollectMetric(metricGroup)
}

// getMonitorGroup retrieves all sub-entries and their counter values from a monitor group
func (e *OpenLDAPExporter) getMonitorGroup(baseDN string) (map[string]float64, error) {
	result, err := e.client.Search(
		baseDN,
		"(objectClass=*)",
		[]string{"monitorCounter", "monitoredInfo"},
	)

	if err != nil {
		return nil, err
	}

	counters := make(map[string]float64)
	
	for _, entry := range result.Entries {
		cnValue := extractCNFromFirstComponent(entry.DN)
		if cnValue == "" {
			continue
		}
		
		// Get the counter value from either monitorCounter or monitoredInfo
		for _, attr := range entry.Attributes {
			if (attr.Name == "monitorCounter" || attr.Name == "monitoredInfo") && len(attr.Values) > 0 {
				if value, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
					counters[cnValue] = value
					logger.SafeDebug("exporter", "Retrieved monitor counter from group", map[string]interface{}{
						"base_dn": baseDN,
						"cn":      cnValue,
						"value":   value,
					})
					break // Found the counter, no need to check other attributes
				}
			}
		}
	}

	return counters, nil
}

// getMonitorCounter retrieves a counter value from the monitor backend
func (e *OpenLDAPExporter) getMonitorCounter(dn string) (float64, error) {
	result, err := e.client.Search(
		dn,
		"(objectClass=*)",
		[]string{"monitorCounter"},
	)

	if err != nil {
		return 0, err
	}

	if len(result.Entries) == 0 {
		logger.SafeWarn("exporter", "No entries found for monitor counter", map[string]interface{}{
			"dn": dn,
		})
		return 0, errors.New("no entries found")
	}

	for _, attr := range result.Entries[0].Attributes {
		if attr.Name == "monitorCounter" && len(attr.Values) > 0 {
			value, err := strconv.ParseFloat(attr.Values[0], 64)
			if err != nil {
				logger.SafeError("exporter", "Failed to parse counter value", err, map[string]interface{}{
					"dn":    dn,
					"value": attr.Values[0],
				})
				return 0, err
			}
			return value, nil
		}
	}

	logger.SafeWarn("exporter", "MonitorCounter attribute not found", map[string]interface{}{
		"dn": dn,
	})
	return 0, errors.New("monitorCounter attribute not found")
}

// ensureConnection ensures we have a valid connection to LDAP
func (e *OpenLDAPExporter) ensureConnection() error {
	// Try a simple search to test the connection
	_, err := e.client.Search(
		"cn=Monitor",
		"(objectClass=*)",
		[]string{"cn"},
	)

	return err
}