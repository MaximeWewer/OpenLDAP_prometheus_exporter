package exporter

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/prometheus/client_golang/prometheus"
)

// counterEntry tracks a counter value with last update time
type counterEntry struct {
	value    float64
	lastSeen time.Time
	// Atomic flag to prevent concurrent modifications during cleanup
	modifying int32
}

// updateCounter safely updates a counter metric with overflow detection
func (e *OpenLDAPExporter) updateCounter(counter *prometheus.CounterVec, server, key string, newValue float64) {
	e.updateCounterCommon("counter", server, key, "", newValue, func(delta float64, labels prometheus.Labels) {
		if delta > 0 {
			counter.With(labels).Add(delta)
		}
	})
}

// updateOperationCounter safely updates an operation counter metric
func (e *OpenLDAPExporter) updateOperationCounter(counter *prometheus.GaugeVec, server, operation, key string, newValue float64) {
	e.updateCounterCommon("operation_counter", server, key, operation, newValue, func(delta float64, labels prometheus.Labels) {
		counter.With(labels).Set(delta)
	})
}

// updateCounterCommon contains the common logic for updating counters to reduce code duplication
func (e *OpenLDAPExporter) updateCounterCommon(
	counterType, server, key, operation string,
	newValue float64,
	updateFunc func(delta float64, labels prometheus.Labels),
) {
	// Validate inputs
	if err := validateKey(key); err != nil {
		logFields := map[string]interface{}{
			"key":          key,
			"counter_type": counterType,
		}
		if operation != "" {
			logFields["operation"] = operation
		}
		logger.SafeError("exporter", "Invalid counter key", err, logFields)
		return
	}

	// Check for reasonable counter values
	if newValue < 0 {
		logFields := map[string]interface{}{
			"server":       server,
			"key":          key,
			"value":        newValue,
			"counter_type": counterType,
		}
		if operation != "" {
			logFields["operation"] = operation
		}
		logger.SafeWarn("exporter", "Negative counter value detected, skipping", logFields)
		return
	}

	if newValue > MaxReasonableCounterValue {
		logFields := map[string]interface{}{
			"server":       server,
			"key":          key,
			"value":        newValue,
			"counter_type": counterType,
		}
		if operation != "" {
			logFields["operation"] = operation
		}
		logger.SafeWarn("exporter", "Unreasonably large counter value detected", logFields)
	}

	// Get the map for this server
	e.counterMutex.RLock()
	serverMap, exists := e.counterValues[server]
	e.counterMutex.RUnlock()

	if !exists {
		// Create new server map if it doesn't exist
		e.counterMutex.Lock()
		// Double-check after acquiring write lock
		if serverMap, exists = e.counterValues[server]; !exists {
			serverMap = &sync.Map{}
			e.counterValues[server] = serverMap
		}
		e.counterMutex.Unlock()
	}

	// Load or create counter entry
	entryInterface, _ := serverMap.LoadOrStore(key, &counterEntry{
		value:    0,
		lastSeen: time.Now(),
	})
	entry := entryInterface.(*counterEntry)

	// Try to acquire modification lock
	if !atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
		// Another goroutine is modifying this entry, skip this update
		logFields := map[string]interface{}{
			"server":       server,
			"key":          key,
			"counter_type": counterType,
		}
		if operation != "" {
			logFields["operation"] = operation
		}
		logger.SafeDebug("exporter", "Counter update skipped due to concurrent modification", logFields)
		return
	}
	defer atomic.StoreInt32(&entry.modifying, 0)

	// Update last seen time
	entry.lastSeen = time.Now()

	// Calculate the delta
	oldValue := entry.value
	delta := newValue - oldValue

	// Debug logging for delta tracking
	logFields := map[string]interface{}{
		"server":       server,
		"key":          key,
		"old_value":    oldValue,
		"new_value":    newValue,
		"delta":        delta,
		"counter_type": counterType,
	}
	if operation != "" {
		logFields["operation"] = operation
	}
	logger.SafeDebug("exporter", "Counter delta calculation", logFields)

	// Check for counter reset or overflow
	if delta < 0 {
		// Counter has been reset or overflowed
		resetLogFields := map[string]interface{}{
			"server":       server,
			"key":          key,
			"old_value":    oldValue,
			"new_value":    newValue,
			"counter_type": counterType,
		}
		if operation != "" {
			resetLogFields["operation"] = operation
		}
		logger.SafeDebug("exporter", "Counter reset detected", resetLogFields)

		// If the new value is reasonable, assume it's a reset and use the new value as delta
		if newValue < MaxReasonableCounterDelta {
			delta = newValue
		} else {
			// Skip this update as it might be corrupted
			logger.SafeWarn("exporter", "Skipping counter update due to suspicious reset", resetLogFields)
			return
		}
	} else if delta > MaxReasonableCounterDelta {
		// Check for unreasonable delta
		deltaLogFields := map[string]interface{}{
			"server":       server,
			"key":          key,
			"old_value":    oldValue,
			"new_value":    newValue,
			"delta":        delta,
			"counter_type": counterType,
		}
		if operation != "" {
			deltaLogFields["operation"] = operation
		}
		logger.SafeWarn("exporter", "Unreasonably large counter delta detected", deltaLogFields)
		// Still update but log the warning
	}

	// Update the stored value
	entry.value = newValue

	// Create labels for prometheus update
	labels := prometheus.Labels{"server": server}
	if operation != "" {
		labels["operation"] = operation
	}

	// Call the update function
	updateFunc(delta, labels)
}


// cleanupOldCounters runs periodically to clean up old counter entries
func (e *OpenLDAPExporter) cleanupOldCounters() {
	ticker := time.NewTicker(CounterCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.cleanupOldCountersSync()
		case <-e.stopChan:
			return
		}
	}
}

// cleanupOldCountersSync performs synchronous cleanup of old counter entries
func (e *OpenLDAPExporter) cleanupOldCountersSync() {
	cutoff := time.Now().Add(-CounterEntryRetentionPeriod)
	totalCleaned := 0

	e.counterMutex.RLock()
	servers := make([]string, 0, len(e.counterValues))
	for server := range e.counterValues {
		servers = append(servers, server)
	}
	e.counterMutex.RUnlock()

	for _, server := range servers {
		e.counterMutex.RLock()
		serverMap, exists := e.counterValues[server]
		e.counterMutex.RUnlock()

		if !exists {
			continue
		}

		keysToDelete := []string{}

		// Find entries to delete
		serverMap.Range(func(key, value interface{}) bool {
			entry := value.(*counterEntry)

			// Try to acquire modification lock
			if atomic.CompareAndSwapInt32(&entry.modifying, 0, 1) {
				if entry.lastSeen.Before(cutoff) {
					keysToDelete = append(keysToDelete, key.(string))
				}
				atomic.StoreInt32(&entry.modifying, 0)
			}

			// Check if we've collected too many entries
			if len(keysToDelete) > MaxCounterEntries {
				logger.SafeWarn("exporter", "Too many counter entries, forcing cleanup", map[string]interface{}{
					"server":  server,
					"entries": len(keysToDelete),
				})
				return false // Stop iteration
			}

			return true
		})

		// Delete old entries
		for _, key := range keysToDelete {
			serverMap.Delete(key)
			totalCleaned++
		}
	}

	if totalCleaned > 0 {
		logger.SafeDebug("exporter", "Cleaned up old counter entries", map[string]interface{}{
			"entries_removed": totalCleaned,
		})
	}
}

// GetAtomicCounterStats returns statistics about atomic counters
func (e *OpenLDAPExporter) GetAtomicCounterStats() map[string]float64 {
	stats := make(map[string]float64)
	totalEntries := 0

	// Add atomic stats from the exporter
	stats["total_scrapes"] = e.totalScrapes.Load()
	stats["total_errors"] = e.totalErrors.Load()
	stats["last_scrape_time"] = e.lastScrapeTime.Load()

	e.counterMutex.RLock()
	defer e.counterMutex.RUnlock()

	for server, serverMap := range e.counterValues {
		serverEntries := 0
		serverMap.Range(func(_, _ interface{}) bool {
			serverEntries++
			return true
		})
		stats["counter_entries_"+server] = float64(serverEntries)
		totalEntries += serverEntries
	}

	stats["counter_entries_total"] = float64(totalEntries)
	return stats
}