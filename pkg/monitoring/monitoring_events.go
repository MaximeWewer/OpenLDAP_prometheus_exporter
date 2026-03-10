package monitoring

import (
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// RecordEvent records a custom event with proper rate calculation and memory management
func (im *InternalMonitoring) RecordEvent(eventType string) {
	im.eventMutex.Lock()
	defer im.eventMutex.Unlock()

	if len(im.events) >= MaxEvents {
		im.cleanupOldEventsUnsafe()
	}

	now := time.Now()

	if counter, exists := im.events[eventType]; exists {
		counter.mutex.Lock()
		counter.Count++

		if !counter.LastEvent.IsZero() {
			elapsed := now.Sub(counter.LastEvent)
			if elapsed < RateWindow {
				counter.Rate = 1.0 / elapsed.Seconds()
			} else {
				counter.Rate = 0
			}
		} else {
			counter.Rate = 0
		}

		counter.LastEvent = now
		counter.mutex.Unlock()
	} else {
		im.events[eventType] = &EventCounter{
			Count:      1,
			FirstEvent: now,
			LastEvent:  now,
			Rate:       0,
		}
	}
}

// GetEventStats returns statistics for all recorded events
func (im *InternalMonitoring) GetEventStats() map[string]map[string]interface{} {
	im.eventMutex.RLock()
	defer im.eventMutex.RUnlock()

	stats := make(map[string]map[string]interface{})

	for eventType, counter := range im.events {
		counter.mutex.RLock()
		stats[eventType] = map[string]interface{}{
			"count":      counter.Count,
			"last_event": counter.LastEvent,
			"rate":       counter.Rate,
		}
		counter.mutex.RUnlock()
	}

	return stats
}

// GetSystemStats returns general system statistics
func (im *InternalMonitoring) GetSystemStats() map[string]interface{} {
	return map[string]interface{}{
		"start_time": im.startTime.Format(time.RFC3339),
		"uptime":     time.Since(im.startTime).Truncate(10 * time.Millisecond).String(),
	}
}

// GetStartTime returns the start time of the exporter
func (im *InternalMonitoring) GetStartTime() time.Time {
	return im.startTime
}

// RecordScrape records a scrape operation with its duration and success status
func (im *InternalMonitoring) RecordScrape(server string, duration time.Duration, success bool) {
	im.collectionLatency.WithLabelValues(server, "scrape").Observe(duration.Seconds())

	if success {
		im.collectionSuccess.WithLabelValues(server, "scrape").Inc()
	} else {
		im.collectionFailures.WithLabelValues(server, "scrape").Inc()
	}

	if success {
		im.RecordEvent("scrape_success")
	} else {
		im.RecordEvent("scrape_failure")
	}
}

// Reset resets all event counters
func (im *InternalMonitoring) Reset() {
	im.eventMutex.Lock()
	defer im.eventMutex.Unlock()

	for eventType := range im.events {
		delete(im.events, eventType)
	}

	logger.SafeInfo("monitoring", "Internal monitoring metrics reset")
}

// CleanupEvents manually triggers cleanup of old events
func (im *InternalMonitoring) CleanupEvents() {
	im.eventMutex.Lock()
	defer im.eventMutex.Unlock()
	im.cleanupOldEventsUnsafe()
}

// cleanupOldEventsUnsafe removes old events to prevent memory leaks
// This method assumes the caller already holds the eventMutex lock
func (im *InternalMonitoring) cleanupOldEventsUnsafe() {
	now := time.Now()
	cleanedCount := 0

	for eventType, counter := range im.events {
		counter.mutex.RLock()
		age := now.Sub(counter.LastEvent)
		counter.mutex.RUnlock()

		if age > EventTTL {
			delete(im.events, eventType)
			cleanedCount++
		}
	}

	if cleanedCount > 0 {
		logger.SafeDebug("monitoring", "Cleaned up old events", map[string]interface{}{
			"events_removed": cleanedCount,
			"total_events":   len(im.events),
		})
	}
}
