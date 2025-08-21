package exporter

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/metrics"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/monitoring"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/pool"
)

// Constants for validation thresholds
const (
	// MaxReasonableCounterValue represents the maximum reasonable value for a counter (1 billion operations/bytes)
	MaxReasonableCounterValue = 1e9
	// MaxReasonableCounterDelta represents the maximum reasonable delta between counter readings (1 million operations per interval)
	MaxReasonableCounterDelta = 1e6
	// MaxCounterEntries is the maximum number of counter entries to keep in memory
	MaxCounterEntries = 1000
	// CounterCleanupInterval is how often to clean up old counter entries
	CounterCleanupInterval = 10 * time.Minute
	// CounterEntryRetentionPeriod is how long to keep counter entries before cleanup
	CounterEntryRetentionPeriod = 2 * CounterCleanupInterval

	// Retry configuration constants
	MaxRetryAttempts   = 3
	InitialRetryDelay  = 100 * time.Millisecond
	MaxRetryDelay      = 2 * time.Second
	RetryBackoffFactor = 2.0
	RetryJitterFactor  = 0.1
)

// collectionTask represents a metric collection task
type collectionTask struct {
	name string
	fn   func(string)
}

// counterEntry tracks a counter value with last update time
type counterEntry struct {
	value    float64
	lastSeen time.Time
	// Atomic flag to prevent concurrent modifications during cleanup
	modifying int32
}

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
		return fmt.Errorf("empty key not allowed")
	}
	if len(key) > 512 { // Reasonable limit for key length
		return fmt.Errorf("key too long: %d characters (max 512)", len(key))
	}
	// Check for null bytes or other problematic characters
	for _, r := range key {
		if r == 0 || r == '\r' || r == '\n' {
			return fmt.Errorf("key contains invalid character: %q", r)
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

	// Cap at maximum delay
	if delay > float64(rc.MaxDelay) {
		delay = float64(rc.MaxDelay)
	}

	// Add jitter to prevent thundering herd
	jitter := delay * rc.JitterFactor * (2*float64(time.Now().UnixNano()%1000)/1000 - 1)
	finalDelay := time.Duration(delay + jitter)

	// Ensure minimum delay
	if finalDelay < rc.InitialDelay {
		finalDelay = rc.InitialDelay
	}

	return finalDelay
}

// retryOperation executes an operation with retry logic for transient failures
func (e *OpenLDAPExporter) retryOperation(ctx context.Context, operation string, fn func() error) error {
	retryConfig := DefaultRetryConfig()
	var lastErr error

	for attempt := 0; attempt < retryConfig.MaxAttempts; attempt++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			logger.SafeError("exporter", "Context cancelled during retry", ctx.Err(), map[string]interface{}{
				"operation":    operation,
				"attempt":      attempt,
				"max_attempts": retryConfig.MaxAttempts,
				"server":       e.config.ServerName,
			})
			return ctx.Err()
		default:
		}

		// Execute the operation
		err := fn()
		if err == nil {
			// Success
			if attempt > 0 {
				logger.SafeInfo("exporter", "Operation succeeded after retry", map[string]interface{}{
					"operation": operation,
					"attempt":   attempt + 1,
					"server":    e.config.ServerName,
				})
			}
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			logger.SafeError("exporter", "Non-retryable error encountered", err, map[string]interface{}{
				"operation": operation,
				"attempt":   attempt + 1,
				"retryable": false,
				"server":    e.config.ServerName,
			})
			return err
		}

		// Don't sleep on the last attempt
		if attempt < retryConfig.MaxAttempts-1 {
			delay := retryConfig.calculateDelay(attempt)
			logger.SafeWarn("exporter", "Retrying operation after failure", map[string]interface{}{
				"operation":    operation,
				"attempt":      attempt + 1,
				"max_attempts": retryConfig.MaxAttempts,
				"delay":        delay.String(),
				"error":        err.Error(),
				"server":       e.config.ServerName,
			})

			// Wait with context cancellation support
			select {
			case <-ctx.Done():
				logger.SafeError("exporter", "Context cancelled during retry delay", ctx.Err(), map[string]interface{}{
					"operation": operation,
					"attempt":   attempt + 1,
					"delay":     delay.String(),
					"server":    e.config.ServerName,
				})
				return ctx.Err()
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// All attempts failed
	logger.SafeError("exporter", "All retry attempts failed", lastErr, map[string]interface{}{
		"operation": operation,
		"attempts":  retryConfig.MaxAttempts,
		"server":    e.config.ServerName,
	})
	return lastErr
}

// OpenLDAPExporter implements the Prometheus collector interface for OpenLDAP metrics
type OpenLDAPExporter struct {
	client        *pool.PooledLDAPClient
	metrics       *metrics.OpenLDAPMetrics
	config        *config.Config
	monitoring    *monitoring.InternalMonitoring
	lastValues    map[string]*counterEntry // Track previous counter values with timestamps
	valueMutex    sync.RWMutex             // Protects lastValues map
	cleanupTicker *time.Ticker             // Ticker for cleanup routine
	cleanupStop   chan struct{}            // Channel to stop cleanup routine
	cleanupDone   chan struct{}            // Channel to signal cleanup goroutine completion
	closed        int32                    // Atomic flag to indicate if exporter is closed
	// Frequently accessed counters for atomic operations
	totalScrapes   atomicFloat64
	totalErrors    atomicFloat64
	lastScrapeTime atomicFloat64
}

// NewOpenLDAPExporter creates a new OpenLDAP exporter with the given configuration
func NewOpenLDAPExporter(cfg *config.Config) *OpenLDAPExporter {
	// Use pooled LDAP client for better performance
	client := pool.NewPooledLDAPClient(cfg)

	logger.SafeInfo("exporter", "OpenLDAP exporter created with connection pooling", map[string]interface{}{
		"server":       cfg.ServerName,
		"pool_enabled": true,
	})

	exporter := &OpenLDAPExporter{
		client:        client,
		metrics:       metrics.NewOpenLDAPMetrics(),
		config:        cfg,
		monitoring:    monitoring.NewInternalMonitoring(),
		lastValues:    make(map[string]*counterEntry),
		cleanupTicker: time.NewTicker(CounterCleanupInterval),
		cleanupStop:   make(chan struct{}),
		cleanupDone:   make(chan struct{}),
	}

	// Start cleanup routine for old counter entries
	go exporter.cleanupOldCounters()

	return exporter
}

// GetInternalMonitoring returns the internal monitoring instance for exposing metrics
func (e *OpenLDAPExporter) GetInternalMonitoring() *monitoring.InternalMonitoring {
	return e.monitoring
}

// GetAtomicCounterStats returns current atomic counter statistics
func (e *OpenLDAPExporter) GetAtomicCounterStats() map[string]float64 {
	return map[string]float64{
		"total_scrapes":    e.totalScrapes.Load(),
		"total_errors":     e.totalErrors.Load(),
		"last_scrape_time": e.lastScrapeTime.Load(),
	}
}

// Describe implements the prometheus.Collector interface by sending metric descriptions
func (e *OpenLDAPExporter) Describe(ch chan<- *prometheus.Desc) {
	e.metrics.Describe(ch)
}

// Collect implements the prometheus.Collector interface by gathering and sending metrics
func (e *OpenLDAPExporter) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	// Atomically increment total scrapes counter
	e.totalScrapes.Store(e.totalScrapes.Load() + 1)

	// Create context with timeout for the entire collection process
	ctx, cancel := context.WithTimeout(context.Background(), e.config.Timeout)
	defer cancel()

	if err := e.ensureConnection(); err != nil {
		logger.SafeError("exporter", "Failed to establish LDAP connection", err, map[string]interface{}{
			"server":  e.config.ServerName,
			"timeout": e.config.Timeout.String(),
		})
		// Atomically increment error counter
		e.totalErrors.Store(e.totalErrors.Load() + 1)
		e.metrics.ScrapeErrors.WithLabelValues(e.config.ServerName).Inc()
		e.metrics.Collect(ch)
		return
	}

	e.collectAllMetricsWithContext(ctx)
	e.metrics.Collect(ch)

	duration := time.Since(start)
	// Atomically store last scrape time
	e.lastScrapeTime.Store(float64(time.Now().Unix()))
	logger.SafeInfo("exporter", "Metrics collection completed successfully", map[string]interface{}{
		"server":        e.config.ServerName,
		"duration":      duration.String(),
		"total_scrapes": e.totalScrapes.Load(),
	})
}

// ensureConnection ensures the LDAP client connection is active, reconnecting if necessary
func (e *OpenLDAPExporter) ensureConnection() error {
	if e.client != nil {
		return nil
	}

	// Create new pooled LDAP client for reconnection
	client := pool.NewPooledLDAPClient(e.config)
	e.client = client

	logger.SafeInfo("exporter", "LDAP connection pool recreated for reconnection")
	return nil
}

// collectAllMetricsWithContext gathers all OpenLDAP monitoring metrics with context support
func (e *OpenLDAPExporter) collectAllMetricsWithContext(ctx context.Context) {
	serverLabel := e.config.ServerName

	// Define collection tasks with names for better logging
	allTasks := []collectionTask{
		{"connections", e.collectConnectionsMetrics},
		{"statistics", e.collectStatisticsMetrics},
		{"operations", e.collectOperationsMetrics},
		{"threads", e.collectThreadsMetrics},
		{"time", e.collectTimeMetrics},
		{"waiters", e.collectWaitersMetrics},
		{"overlays", e.collectOverlaysMetrics},
		{"tls", e.collectTLSMetrics},
		{"backends", e.collectBackendsMetrics},
		{"listeners", e.collectListenersMetrics},
		{"health", e.collectHealthMetrics},
		{"database", e.collectDatabaseMetrics},
	}

	// Apply metric filtering based on configuration
	var tasks []collectionTask
	for _, task := range allTasks {
		if e.config.ShouldCollectMetric(task.name) {
			tasks = append(tasks, task)
		} else {
			logger.SafeDebug("exporter", "Skipping metric group due to filtering", map[string]interface{}{
				"server": serverLabel,
				"task":   task.name,
			})
		}
	}

	if len(tasks) == 0 {
		logger.SafeWarn("exporter", "No metric groups selected for collection - check filtering configuration", map[string]interface{}{
			"server":         serverLabel,
			"include_filter": e.config.MetricsInclude,
			"exclude_filter": e.config.MetricsExclude,
		})
		return
	}

	// Run collectors in parallel with context checking
	var wg sync.WaitGroup
	errChan := make(chan error, len(tasks))

	for _, task := range tasks {
		wg.Add(1)
		go func(t collectionTask) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				logger.SafeWarn("exporter", "Metrics collection cancelled due to timeout", map[string]interface{}{
					"server": serverLabel,
					"task":   t.name,
					"error":  ctx.Err().Error(),
				})
				errChan <- ctx.Err()
				return
			default:
				logger.SafeDebug("exporter", "Collecting metrics", map[string]interface{}{
					"server": serverLabel,
					"task":   t.name,
				})

				// Execute collection with panic recovery
				func() {
					defer func() {
						if r := recover(); r != nil {
							logger.SafeError("exporter", "Panic in metrics collection", nil, map[string]interface{}{
								"server": serverLabel,
								"task":   t.name,
								"panic":  r,
							})
							errChan <- fmt.Errorf("panic in task %s: %v", t.name, r)
						}
					}()
					t.fn(serverLabel)
				}()
			}
		}(task)
	}

	// Wait for all collectors to complete or context to cancel
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All tasks completed successfully
		close(errChan)

		// Count any errors that occurred
		errorCount := 0
		for err := range errChan {
			if err != nil {
				errorCount++
			}
		}

		if errorCount > 0 {
			logger.SafeWarn("exporter", "Some metric collection tasks failed", map[string]interface{}{
				"server":      serverLabel,
				"error_count": errorCount,
				"total_tasks": len(tasks),
			})
			// Atomically add to total errors counter
			e.totalErrors.Store(e.totalErrors.Load() + float64(errorCount))
			e.metrics.ScrapeErrors.WithLabelValues(serverLabel).Add(float64(errorCount))
		}

	case <-ctx.Done():
		logger.SafeWarn("exporter", "Metrics collection timed out", map[string]interface{}{
			"server": serverLabel,
			"error":  ctx.Err().Error(),
		})
		// Atomically increment error counter for timeout
		e.totalErrors.Store(e.totalErrors.Load() + 1)
		e.metrics.ScrapeErrors.WithLabelValues(serverLabel).Inc()
		return
	}
}

// collectConnectionsMetrics collects connection-related metrics
func (e *OpenLDAPExporter) collectConnectionsMetrics(server string) {
	var success bool
	defer func() {
		if success {
			e.monitoring.RecordCollectionSuccess("openldap-exporter", "connections")
		} else {
			e.monitoring.RecordCollectionFailure("openldap-exporter", "connections")
		}
	}()

	if val, err := e.getMonitorCounter("cn=Current,cn=Connections,cn=Monitor"); err == nil {
		e.metrics.ConnectionsCurrent.WithLabelValues(server).Set(val)
	} else {
		return // Exit early on first failure
	}

	if val, err := e.getMonitorCounter("cn=Total,cn=Connections,cn=Monitor"); err == nil {
		key := server + ":connections_total"
		e.updateCounter(e.metrics.ConnectionsTotal, server, key, val)
	} else {
		return // Exit early on failure
	}

	success = true
}

// collectStatisticsMetrics collects statistics metrics using batch query optimization
func (e *OpenLDAPExporter) collectStatisticsMetrics(server string) {
	var success bool
	defer func() {
		if success {
			e.monitoring.RecordCollectionSuccess("openldap-exporter", "statistics")
		} else {
			e.monitoring.RecordCollectionFailure("openldap-exporter", "statistics")
		}
	}()

	// Use single query to get all statistics at once with retry
	var result *ldap.SearchResult
	err := e.retryOperation(context.Background(), "search_statistics", func() error {
		var searchErr error
		result, searchErr = e.client.Search("cn=Statistics,cn=Monitor", "(objectClass=*)", []string{"cn", "monitorCounter"})
		return searchErr
	})
	if err != nil {
		logger.SafeError("exporter", "Failed to query statistics metrics after retries", err, map[string]interface{}{
			"server":     server,
			"base_dn":    "cn=Statistics,cn=Monitor",
			"attributes": []string{"cn", "monitorCounter"},
		})
		return
	}

	// Map CN names to corresponding metrics
	statsMetricMap := map[string]*prometheus.CounterVec{
		"Bytes":     e.metrics.BytesTotal,
		"PDU":       e.metrics.PduTotal,
		"Entries":   e.metrics.EntriesTotal,
		"Referrals": e.metrics.ReferralsTotal,
	}

	// Bounds check for entries slice
	if len(result.Entries) > 1000 { // Reasonable limit for entries
		logger.SafeWarn("exporter", "Too many statistics entries, limiting processing", map[string]interface{}{
			"entries_count": len(result.Entries),
			"max_entries":   1000,
		})
		result.Entries = result.Entries[:1000]
	}

	for i, entry := range result.Entries {
		if i >= len(result.Entries) { // Additional bounds check
			break
		}

		if entry == nil { // Nil check for entry
			logger.SafeWarn("exporter", "Found nil entry in statistics results", map[string]interface{}{
				"entry_index": i,
			})
			continue
		}

		cn := entry.GetAttributeValue("cn")
		if cn == "" || cn == "Statistics" {
			continue
		}

		// Validate cn length
		if len(cn) > 100 {
			logger.SafeWarn("exporter", "CN too long in statistics entry", map[string]interface{}{
				"cn_length": len(cn),
			})
			continue
		}

		counterStr := entry.GetAttributeValue("monitorCounter")
		if counterStr == "" {
			continue
		}

		// Validate counter string length
		if len(counterStr) > 50 {
			logger.SafeWarn("exporter", "Counter string too long in statistics entry", map[string]interface{}{
				"counter_length": len(counterStr),
			})
			continue
		}

		if metric, exists := statsMetricMap[cn]; exists {
			if val, err := strconv.ParseFloat(counterStr, 64); err == nil {
				key := server + ":statistics:" + cn
				e.updateCounter(metric, server, key, val)
			}
		}
	}

	success = true
	logger.SafeDebug("exporter", "Statistics metrics collected", map[string]interface{}{
		"server":        server,
		"entries_found": len(result.Entries),
	})
}

// collectOperationsMetrics collects operation metrics using batch query optimization
func (e *OpenLDAPExporter) collectOperationsMetrics(server string) {
	var success bool
	defer func() {
		if success {
			e.monitoring.RecordCollectionSuccess("openldap-exporter", "operations")
		} else {
			e.monitoring.RecordCollectionFailure("openldap-exporter", "operations")
		}
	}()

	// Use single query to get all operation metrics at once with retry
	var result *ldap.SearchResult
	err := e.retryOperation(context.Background(), "search_operations", func() error {
		var searchErr error
		result, searchErr = e.client.Search("cn=Operations,cn=Monitor", "(objectClass=monitorOperation)", []string{"cn", "monitorOpInitiated", "monitorOpCompleted"})
		return searchErr
	})
	if err != nil {
		logger.SafeError("exporter", "Failed to query operations metrics after retries", err, map[string]interface{}{
			"server":     server,
			"base_dn":    "cn=Operations,cn=Monitor",
			"filter":     "(objectClass=monitorOperation)",
			"attributes": []string{"cn", "monitorOpInitiated", "monitorOpCompleted"},
		})
		return
	}

	for _, entry := range result.Entries {
		// Extract operation name from cn attribute
		opName := entry.GetAttributeValue("cn")
		if opName == "" || opName == "Operations" {
			continue
		}

		opLower := strings.ToLower(opName)

		// Operations initiated
		if initiated := entry.GetAttributeValue("monitorOpInitiated"); initiated != "" {
			if val, err := strconv.ParseFloat(initiated, 64); err == nil {
				key := server + ":" + opLower + ":initiated"
				e.updateOperationCounter(e.metrics.OperationsInitiated, server, opLower, key, val)
			}
		}

		// Operations completed
		if completed := entry.GetAttributeValue("monitorOpCompleted"); completed != "" {
			if val, err := strconv.ParseFloat(completed, 64); err == nil {
				key := server + ":" + opLower + ":completed"
				e.updateOperationCounter(e.metrics.OperationsCompleted, server, opLower, key, val)
			}
		}
	}

	success = true
	logger.SafeDebug("exporter", "Operations metrics collected", map[string]interface{}{
		"server":           server,
		"operations_found": len(result.Entries),
	})
}

// collectThreadsMetrics collects thread-related metrics using batch query optimization
func (e *OpenLDAPExporter) collectThreadsMetrics(server string) {
	var success bool
	defer func() {
		if success {
			e.monitoring.RecordCollectionSuccess("openldap-exporter", "threads")
		} else {
			e.monitoring.RecordCollectionFailure("openldap-exporter", "threads")
		}
	}()

	// Use single query to get all thread metrics at once with retry
	var result *ldap.SearchResult
	err := e.retryOperation(context.Background(), "search_threads", func() error {
		var searchErr error
		result, searchErr = e.client.Search("cn=Threads,cn=Monitor", "(objectClass=*)", []string{"cn", "monitoredInfo"})
		return searchErr
	})
	if err != nil {
		logger.SafeError("exporter", "Failed to query thread metrics after retries", err, map[string]interface{}{
			"server":     server,
			"base_dn":    "cn=Threads,cn=Monitor",
			"attributes": []string{"cn", "monitoredInfo"},
		})
		return
	}

	// Map CN names to corresponding metrics
	threadMetricMap := map[string]*prometheus.GaugeVec{
		"Max":         e.metrics.ThreadsMax,
		"Max Pending": e.metrics.ThreadsMaxPending,
		"Backload":    e.metrics.ThreadsBackload,
		"Active":      e.metrics.ThreadsActive,
		"Open":        e.metrics.ThreadsOpen,
		"Starting":    e.metrics.ThreadsStarting,
		"Pending":     e.metrics.ThreadsPending,
	}

	for _, entry := range result.Entries {
		cn := entry.GetAttributeValue("cn")
		if cn == "" || cn == "Threads" {
			continue
		}

		monitoredInfo := entry.GetAttributeValue("monitoredInfo")
		if monitoredInfo == "" {
			continue
		}

		// Handle thread state (special case - text value)
		if cn == "State" {
			e.metrics.ThreadsState.WithLabelValues(server, monitoredInfo).Set(1)
			continue
		}

		// Handle numeric thread metrics
		if metric, exists := threadMetricMap[cn]; exists {
			if val, err := strconv.ParseFloat(monitoredInfo, 64); err == nil {
				metric.WithLabelValues(server).Set(val)
			}
		}
	}

	success = true
	logger.SafeDebug("exporter", "Thread metrics collected", map[string]interface{}{
		"server":        server,
		"entries_found": len(result.Entries),
	})
}

// collectTimeMetrics collects time-related metrics
func (e *OpenLDAPExporter) collectTimeMetrics(server string) {
	// Current time - cn=Current,cn=Time,cn=Monitor
	result, err := e.client.Search("cn=Current,cn=Time,cn=Monitor", "(objectClass=*)", []string{"monitorTimestamp"})
	if err == nil && len(result.Entries) > 0 {
		if timeStr := result.Entries[0].GetAttributeValue("monitorTimestamp"); timeStr != "" {
			if currentTime, err := time.Parse("20060102150405Z", timeStr); err == nil {
				e.metrics.ServerTime.WithLabelValues(server).Set(float64(currentTime.Unix()))
			}
		}
	}

	// Start time - cn=Start,cn=Time,cn=Monitor
	result, err = e.client.Search("cn=Start,cn=Time,cn=Monitor", "(objectClass=*)", []string{"monitorTimestamp"})
	if err == nil && len(result.Entries) > 0 {
		if startTimeStr := result.Entries[0].GetAttributeValue("monitorTimestamp"); startTimeStr != "" {
			if startTime, err := time.Parse("20060102150405Z", startTimeStr); err == nil {
				uptime := time.Since(startTime).Seconds()
				e.metrics.ServerUptime.WithLabelValues(server).Set(uptime)
			}
		}
	}
}

// collectWaitersMetrics collects waiter metrics
func (e *OpenLDAPExporter) collectWaitersMetrics(server string) {
	// Read waiters - cn=Read,cn=Waiters,cn=Monitor
	if val, err := e.getMonitorCounter("cn=Read,cn=Waiters,cn=Monitor"); err == nil {
		e.metrics.WaitersRead.WithLabelValues(server).Set(val)
	}

	// Write waiters - cn=Write,cn=Waiters,cn=Monitor
	if val, err := e.getMonitorCounter("cn=Write,cn=Waiters,cn=Monitor"); err == nil {
		e.metrics.WaitersWrite.WithLabelValues(server).Set(val)
	}
}

// collectOverlaysMetrics collects overlay information
func (e *OpenLDAPExporter) collectOverlaysMetrics(server string) {
	result, err := e.client.Search("cn=Overlays,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "monitoredInfo"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		overlayName := entry.GetAttributeValue("cn")
		if overlayName == "" || overlayName == "Overlays" {
			continue
		}

		status := "loaded"
		if info := entry.GetAttributeValue("monitoredInfo"); info != "" {
			status = info
		}

		e.metrics.OverlaysInfo.WithLabelValues(server, overlayName, status).Set(1)
	}
}

// collectTLSMetrics collects TLS information
func (e *OpenLDAPExporter) collectTLSMetrics(server string) {
	result, err := e.client.Search("cn=TLS,cn=Monitor", "(objectClass=*)", []string{"cn", "monitoredInfo"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		component := entry.GetAttributeValue("cn")
		if component == "" {
			continue
		}

		status := "enabled"
		if info := entry.GetAttributeValue("monitoredInfo"); info != "" {
			status = info
		}

		e.metrics.TlsInfo.WithLabelValues(server, component, status).Set(1)
	}
}

// collectBackendsMetrics collects backend information
func (e *OpenLDAPExporter) collectBackendsMetrics(server string) {
	result, err := e.client.Search("cn=Backends,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "monitoredInfo"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		backendName := entry.GetAttributeValue("cn")
		if backendName == "" || backendName == "Backends" {
			continue
		}

		backendType := entry.GetAttributeValue("monitoredInfo")
		if backendType == "" {
			backendType = "unknown"
		}

		e.metrics.BackendsInfo.WithLabelValues(server, backendName, backendType).Set(1)
	}
}

// collectListenersMetrics collects listener information
func (e *OpenLDAPExporter) collectListenersMetrics(server string) {
	result, err := e.client.Search("cn=Listeners,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "monitorConnectionLocalAddress"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		listenerName := entry.GetAttributeValue("cn")
		address := entry.GetAttributeValue("monitorConnectionLocalAddress")

		if listenerName != "" && listenerName != "Listeners" {
			if address == "" {
				address = "unknown"
			}
			e.metrics.ListenersInfo.WithLabelValues(server, listenerName, address).Set(1)
		}
	}
}

// collectHealthMetrics collects health check metrics
func (e *OpenLDAPExporter) collectHealthMetrics(server string) {
	var success bool
	defer func() {
		if success {
			e.monitoring.RecordCollectionSuccess("openldap-exporter", "health")
		} else {
			e.monitoring.RecordCollectionFailure("openldap-exporter", "health")
		}
	}()

	start := time.Now()

	// Health check - try to read the monitor root
	_, err := e.client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	duration := time.Since(start).Seconds()

	e.metrics.ResponseTime.WithLabelValues(server).Set(duration)

	if err != nil {
		e.metrics.HealthStatus.WithLabelValues(server).Set(0)
		e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
		return // Failure case
	} else {
		e.metrics.HealthStatus.WithLabelValues(server).Set(1)
	}

	success = true
}

// collectDatabaseMetrics collects database-specific metrics from cn=Monitor
func (e *OpenLDAPExporter) collectDatabaseMetrics(server string) {
	// Query databases directly from Monitor tree - no need to access cn=config
	result, err := e.client.Search("cn=Databases,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "namingContexts", "monitoredInfo"})
	if err != nil {
		logger.SafeError("exporter", "Failed to get databases info", err)
		return
	}

	for _, entry := range result.Entries {
		dbName := entry.GetAttributeValue("cn")
		dbType := entry.GetAttributeValue("monitoredInfo")
		actualBaseDN := entry.GetAttributeValue("namingContexts")

		if actualBaseDN == "" || dbType != "mdb" {
			continue
		}

		// Extract domain components from the base DN for filtering
		domainComponents := e.extractDomainComponents(actualBaseDN)

		// Apply DC filtering - check if this database should be monitored
		shouldMonitor := false
		if len(domainComponents) == 0 {
			// If no DC found, use the full base DN for filtering
			shouldMonitor = e.config.ShouldMonitorDC(actualBaseDN)
		} else {
			// Check if any of the domain components should be monitored
			for _, dc := range domainComponents {
				if e.config.ShouldMonitorDC(dc) {
					shouldMonitor = true
					break
				}
			}
		}

		if !shouldMonitor {
			logger.SafeDebug("exporter", "Skipping database due to DC filtering", map[string]interface{}{
				"name":              dbName,
				"suffix":            actualBaseDN,
				"domain_components": domainComponents,
			})
			continue
		}

		logger.SafeDebug("exporter", "Found database", map[string]interface{}{
			"name":              dbName,
			"suffix":            actualBaseDN,
			"type":              dbType,
			"domain_components": domainComponents,
		})

		// Collect database metrics with DC labels
		for _, dc := range domainComponents {
			e.metrics.DatabaseEntries.WithLabelValues(server, actualBaseDN, dc).Set(1)
		}

		// If no DC found, still collect with the full base DN
		if len(domainComponents) == 0 {
			e.metrics.DatabaseEntries.WithLabelValues(server, actualBaseDN, actualBaseDN).Set(1)
		}
	}
}

// extractDomainComponents extracts DC (Domain Component) values from an LDAP DN
func (e *OpenLDAPExporter) extractDomainComponents(dn string) []string {
	if dn == "" {
		return nil
	}

	// Validate DN length to prevent memory issues
	if len(dn) > 2048 { // Reasonable limit for DN length
		logger.SafeWarn("exporter", "DN too long, truncating", map[string]interface{}{
			"dn_length":  len(dn),
			"max_length": 2048,
		})
		dn = dn[:2048]
	}

	var domainComponents []string

	// Split DN into components and look for DC= entries
	parts := strings.Split(dn, ",")

	// Bounds check for parts slice
	if len(parts) > 100 { // Reasonable limit for DN components
		logger.SafeWarn("exporter", "DN has too many components, limiting", map[string]interface{}{
			"components_count": len(parts),
			"max_components":   100,
		})
		parts = parts[:100]
	}

	for i, part := range parts {
		if i >= len(parts) { // Additional bounds check
			break
		}

		part = strings.TrimSpace(part)
		if len(part) < 3 { // Minimum "dc=" length
			continue
		}

		if strings.HasPrefix(strings.ToLower(part), "dc=") {
			// Extract the value after "dc=" with bounds checking
			if len(part) > 3 {
				dcValue := strings.TrimSpace(part[3:])
				if dcValue != "" && len(dcValue) <= 255 { // Reasonable limit for DC value
					domainComponents = append(domainComponents, dcValue)
				}
			}
		}
	}

	return domainComponents
}

// getMonitorCounter retrieves a numeric counter value from the specified LDAP DN with retry
func (e *OpenLDAPExporter) getMonitorCounter(dn string) (float64, error) {
	var result *ldap.SearchResult
	err := e.retryOperation(context.Background(), "get_monitor_counter", func() error {
		var searchErr error
		result, searchErr = e.client.Search(dn, "(objectClass=*)", []string{"monitorCounter"})
		return searchErr
	})
	if err != nil {
		logger.SafeError("exporter", "Failed to search monitor counter after retries", err, map[string]interface{}{
			"dn": dn,
		})
		return 0, err
	}

	if len(result.Entries) == 0 {
		logger.SafeDebug("exporter", "No entries found for DN", map[string]interface{}{"dn": dn})
		return 0, nil
	}

	counterStr := result.Entries[0].GetAttributeValue("monitorCounter")
	if counterStr == "" {
		logger.SafeDebug("exporter", "monitorCounter attribute not found", map[string]interface{}{"dn": dn})
		return 0, nil
	}

	val, parseErr := strconv.ParseFloat(counterStr, 64)
	if parseErr != nil {
		logger.SafeError("exporter", "Failed to parse counter value", parseErr, map[string]interface{}{
			"dn":          dn,
			"counter_str": counterStr,
		})
		return 0, parseErr
	}

	return val, nil
}

// updateCounter updates a Prometheus counter with the difference from last value
// This handles the fact that OpenLDAP counters are cumulative, but Prometheus counters should only increase by the delta
func (e *OpenLDAPExporter) updateCounter(counter *prometheus.CounterVec, server, key string, newValue float64) {
	e.valueMutex.Lock()
	defer e.valueMutex.Unlock()

	// Validate input value
	if newValue < 0 {
		logger.SafeError("exporter", "Invalid negative counter value detected", nil, map[string]interface{}{
			"server": server,
			"key":    key,
			"value":  newValue,
		})
		e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
		return
	}

	// Check for extremely large values that could indicate corruption
	if newValue > MaxReasonableCounterValue {
		logger.SafeWarn("exporter", "Suspiciously large counter value detected", map[string]interface{}{
			"server": server,
			"key":    key,
			"value":  newValue,
		})
		// Continue processing but log the warning
	}

	// Validate key before any operations
	if err := validateKey(key); err != nil {
		logger.SafeError("exporter", "Invalid counter key", err, map[string]interface{}{
			"server": server,
			"key":    key,
		})
		e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
		return
	}

	// Check if we've exceeded maximum entries (prevent unbounded growth)
	if len(e.lastValues) > MaxCounterEntries {
		logger.SafeWarn("exporter", "Counter entries exceeded maximum, triggering cleanup", map[string]interface{}{
			"current_entries": len(e.lastValues),
			"max_entries":     MaxCounterEntries,
		})
		// Unlock before cleanup to prevent deadlock
		e.valueMutex.Unlock()
		e.cleanupOldCountersSync()
		e.valueMutex.Lock()
		// Re-check if entry still doesn't exist after cleanup
		if entry, exists := e.lastValues[key]; exists && entry != nil {
			// Entry was created during cleanup, update it safely
			if atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
				entry.lastSeen = time.Now()
				entry.value = newValue
				atomic.StoreInt32(&entry.modifying, 0)
			}
			return
		}
	}

	// Safe map access with bounds checking
	entry, exists := e.lastValues[key]
	if !exists {
		// First time seeing this metric, initialize counter to current value and store it
		newEntry := &counterEntry{
			value:     newValue,
			lastSeen:  time.Now(),
			modifying: 0,
		}

		// Bounds check before assignment
		if e.lastValues == nil {
			logger.SafeError("exporter", "lastValues map is nil", nil, map[string]interface{}{
				"server": server,
				"key":    key,
			})
			e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
			return
		}

		e.lastValues[key] = newEntry

		// Initialize the counter to make it visible in metrics without adding the initial value
		// The counter will start at 0 and only track deltas from this point forward
		counter.WithLabelValues(server).Add(0)

		logger.SafeDebug("exporter", "First counter value initialized", map[string]interface{}{
			"server": server,
			"key":    key,
			"value":  newValue,
		})
		return
	}

	// Nil check for entry
	if entry == nil {
		logger.SafeError("exporter", "Found nil counter entry", nil, map[string]interface{}{
			"server":          server,
			"key":             key,
			"recovery_action": "replacing_with_new_entry",
		})
		// Replace with new entry
		e.lastValues[key] = &counterEntry{
			value:     newValue,
			lastSeen:  time.Now(),
			modifying: 0,
		}
		counter.WithLabelValues(server).Add(0)
		return
	}

	// Use atomic flag to prevent concurrent modification during cleanup
	if !atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
		// Entry is being modified by cleanup, skip this update
		logger.SafeDebug("exporter", "Skipping counter update due to concurrent cleanup", map[string]interface{}{
			"server": server,
			"key":    key,
		})
		return
	}
	defer atomic.StoreInt32(&entry.modifying, 0)

	// Update last seen time
	entry.lastSeen = time.Now()
	lastValue := entry.value

	// Calculate delta and add to counter
	if newValue >= lastValue {
		delta := newValue - lastValue
		if delta > 0 {
			// Sanity check: reject unreasonably large deltas (could indicate data corruption)
			if delta > MaxReasonableCounterDelta {
				logger.SafeWarn("exporter", "Suspiciously large counter delta detected, skipping update", map[string]interface{}{
					"server":     server,
					"key":        key,
					"delta":      delta,
					"new_value":  newValue,
					"last_value": lastValue,
				})
				// Don't update the counter but update the stored value to prevent repeated warnings
				entry.value = newValue
				return
			}

			counter.WithLabelValues(server).Add(delta)
			logger.SafeDebug("exporter", "Counter updated with delta", map[string]interface{}{
				"server": server,
				"key":    key,
				"delta":  delta,
			})
		}
	} else {
		// Counter was reset (server restart), validate the reset is reasonable
		if newValue > MaxReasonableCounterValue {
			logger.SafeError("exporter", "Counter reset with unreasonable value", nil, map[string]interface{}{
				"server":     server,
				"key":        key,
				"new_value":  newValue,
				"last_value": lastValue,
			})
			e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
			return
		}

		// Use the new value as delta (counter reset scenario)
		counter.WithLabelValues(server).Add(newValue)
		logger.SafeInfo("exporter", "Counter reset detected, using new value as delta", map[string]interface{}{
			"server":     server,
			"key":        key,
			"new_value":  newValue,
			"last_value": lastValue,
		})
	}

	// Update stored value
	entry.value = newValue
}

// updateOperationCounter updates operation counters that need server and operation labels
func (e *OpenLDAPExporter) updateOperationCounter(counter *prometheus.CounterVec, server, operation, key string, newValue float64) {
	e.valueMutex.Lock()
	defer e.valueMutex.Unlock()

	// Validate input value
	if newValue < 0 {
		logger.SafeError("exporter", "Invalid negative counter value detected", nil, map[string]interface{}{
			"server":    server,
			"operation": operation,
			"key":       key,
			"value":     newValue,
		})
		e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
		return
	}

	// Check for extremely large values that could indicate corruption
	if newValue > MaxReasonableCounterValue {
		logger.SafeWarn("exporter", "Suspiciously large operation counter value detected", map[string]interface{}{
			"server":    server,
			"operation": operation,
			"key":       key,
			"value":     newValue,
		})
	}

	// Validate key before any operations
	if err := validateKey(key); err != nil {
		logger.SafeError("exporter", "Invalid operation counter key", err, map[string]interface{}{
			"server":    server,
			"operation": operation,
			"key":       key,
		})
		e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
		return
	}

	// Check if we've exceeded maximum entries (prevent unbounded growth)
	if len(e.lastValues) > MaxCounterEntries {
		logger.SafeWarn("exporter", "Counter entries exceeded maximum, triggering cleanup", map[string]interface{}{
			"current_entries": len(e.lastValues),
			"max_entries":     MaxCounterEntries,
		})
		// Unlock before cleanup to prevent deadlock
		e.valueMutex.Unlock()
		e.cleanupOldCountersSync()
		e.valueMutex.Lock()
		// Re-check if entry still doesn't exist after cleanup
		if entry, exists := e.lastValues[key]; exists && entry != nil {
			// Entry was created during cleanup, update it safely
			if atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
				entry.lastSeen = time.Now()
				entry.value = newValue
				atomic.StoreInt32(&entry.modifying, 0)
			}
			return
		}
	}

	// Safe map access with bounds checking
	entry, exists := e.lastValues[key]
	if !exists {
		// First time seeing this metric, initialize counter to current value and store it
		newEntry := &counterEntry{
			value:     newValue,
			lastSeen:  time.Now(),
			modifying: 0,
		}

		// Bounds check before assignment
		if e.lastValues == nil {
			logger.SafeError("exporter", "lastValues map is nil", nil, map[string]interface{}{
				"server":    server,
				"operation": operation,
				"key":       key,
			})
			e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
			return
		}

		e.lastValues[key] = newEntry

		// Initialize the counter to make it visible in metrics without adding the initial value
		// The counter will start at 0 and only track deltas from this point forward
		counter.WithLabelValues(server, operation).Add(0)

		logger.SafeDebug("exporter", "First operation counter initialized", map[string]interface{}{
			"server":    server,
			"operation": operation,
			"key":       key,
			"value":     newValue,
		})
		return
	}

	// Nil check for entry
	if entry == nil {
		logger.SafeError("exporter", "Found nil operation counter entry", nil, map[string]interface{}{
			"server":    server,
			"operation": operation,
			"key":       key,
		})
		// Replace with new entry
		e.lastValues[key] = &counterEntry{
			value:     newValue,
			lastSeen:  time.Now(),
			modifying: 0,
		}
		counter.WithLabelValues(server, operation).Add(0)
		return
	}

	// Use atomic flag to prevent concurrent modification during cleanup
	if !atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
		// Entry is being modified by cleanup, skip this update
		logger.SafeDebug("exporter", "Skipping operation counter update due to concurrent cleanup", map[string]interface{}{
			"server":    server,
			"operation": operation,
			"key":       key,
		})
		return
	}
	defer atomic.StoreInt32(&entry.modifying, 0)

	// Update last seen time
	entry.lastSeen = time.Now()
	lastValue := entry.value

	// Calculate delta and add to counter
	if newValue >= lastValue {
		delta := newValue - lastValue
		if delta > 0 {
			// Sanity check: reject unreasonably large deltas
			if delta > MaxReasonableCounterDelta {
				logger.SafeWarn("exporter", "Suspiciously large operation counter delta detected, skipping update", map[string]interface{}{
					"server":     server,
					"operation":  operation,
					"key":        key,
					"delta":      delta,
					"new_value":  newValue,
					"last_value": lastValue,
				})
				// Don't update the counter but update the stored value to prevent repeated warnings
				entry.value = newValue
				return
			}

			counter.WithLabelValues(server, operation).Add(delta)
			logger.SafeDebug("exporter", "Operation counter updated with delta", map[string]interface{}{
				"server":    server,
				"operation": operation,
				"key":       key,
				"delta":     delta,
			})
		}
	} else {
		// Counter was reset (server restart)
		if newValue > MaxReasonableCounterValue {
			logger.SafeError("exporter", "Operation counter reset with unreasonable value", nil, map[string]interface{}{
				"server":     server,
				"operation":  operation,
				"key":        key,
				"new_value":  newValue,
				"last_value": lastValue,
			})
			e.metrics.ScrapeErrors.WithLabelValues(server).Inc()
			return
		}

		// Set counter to new value directly
		counter.WithLabelValues(server, operation).Add(newValue)
		logger.SafeInfo("exporter", "Operation counter reset detected, updated to new value", map[string]interface{}{
			"server":     server,
			"operation":  operation,
			"key":        key,
			"new_value":  newValue,
			"last_value": lastValue,
		})
	}

	// Update stored value
	entry.value = newValue
}

// cleanupOldCounters runs periodically to remove stale counter entries
func (e *OpenLDAPExporter) cleanupOldCounters() {
	defer close(e.cleanupDone)
	for {
		select {
		case <-e.cleanupTicker.C:
			// Check if exporter is closed before cleanup
			if atomic.LoadInt32(&e.closed) == 1 {
				return
			}
			e.cleanupOldCountersSync()
		case <-e.cleanupStop:
			return
		}
	}
}

// cleanupOldCountersSync performs synchronous cleanup of old counter entries
func (e *OpenLDAPExporter) cleanupOldCountersSync() {
	// Check bounds before acquiring lock
	if e.lastValues == nil {
		return
	}

	e.valueMutex.Lock()
	defer e.valueMutex.Unlock()

	// Double-check bounds after acquiring lock
	if e.lastValues == nil {
		return
	}

	now := time.Now()
	cutoff := now.Add(-CounterEntryRetentionPeriod) // Remove entries not seen in retention period
	removed := 0
	keysToRemove := make([]string, 0) // Collect keys to remove to avoid modifying map during iteration

	for key, entry := range e.lastValues {
		if entry == nil {
			// Safety check: remove nil entries
			keysToRemove = append(keysToRemove, key)
			continue
		}

		// Use atomic flag to ensure entry is not being modified
		if atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
			if entry.lastSeen.Before(cutoff) {
				keysToRemove = append(keysToRemove, key)
			} else {
				// Release the flag if not removing
				atomic.StoreInt32(&entry.modifying, 0)
			}
		}
	}

	// Remove collected keys
	for _, key := range keysToRemove {
		if entry, exists := e.lastValues[key]; exists {
			// Final safety check
			if entry != nil {
				atomic.StoreInt32(&entry.modifying, 0) // Ensure flag is cleared
			}
			delete(e.lastValues, key)
			removed++
		}
	}

	if removed > 0 {
		logger.SafeInfo("exporter", "Cleaned up old counter entries", map[string]interface{}{
			"removed_entries":   removed,
			"remaining_entries": len(e.lastValues),
		})
	}
}

// Close closes the LDAP connection pool and cleans up resources
func (e *OpenLDAPExporter) Close() {
	// Set closed flag atomically
	atomic.StoreInt32(&e.closed, 1)

	// Stop cleanup routine
	if e.cleanupTicker != nil {
		e.cleanupTicker.Stop()
	}
	if e.cleanupStop != nil {
		close(e.cleanupStop)
		// Wait for cleanup goroutine to finish
		select {
		case <-e.cleanupDone:
			// Cleanup completed
		case <-time.After(5 * time.Second):
			logger.SafeWarn("exporter", "Cleanup goroutine did not finish within timeout")
		}
	}

	// Clean up counter entries
	e.valueMutex.Lock()
	for key, entry := range e.lastValues {
		if entry != nil {
			// Wait for any ongoing modifications to complete
			for atomic.LoadInt32(&entry.modifying) == 1 {
				time.Sleep(time.Millisecond)
			}
		}
		delete(e.lastValues, key)
	}
	e.lastValues = nil
	e.valueMutex.Unlock()

	// Close LDAP client
	if e.client != nil {
		e.client.Close()
		logger.SafeInfo("exporter", "OpenLDAP exporter closed, connection pool terminated")
	}
}
