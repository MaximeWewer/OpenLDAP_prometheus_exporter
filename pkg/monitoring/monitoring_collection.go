package monitoring

import (
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
)

// RecordCircuitBreakerState records circuit breaker state
func (im *InternalMonitoring) RecordCircuitBreakerState(server string, state circuitbreaker.State) {
	stateValue := float64(state)
	im.circuitBreakerState.WithLabelValues(server).Set(stateValue)
}

// RecordCircuitBreakerRequest records circuit breaker requests
func (im *InternalMonitoring) RecordCircuitBreakerRequest(server, result string) {
	im.circuitBreakerRequests.WithLabelValues(server, result).Inc()
}

// RecordCircuitBreakerFailure records circuit breaker failures
func (im *InternalMonitoring) RecordCircuitBreakerFailure(server string) {
	im.circuitBreakerFailures.WithLabelValues(server).Inc()
}

// RecordCollectionLatency records metric collection latency
func (im *InternalMonitoring) RecordCollectionLatency(server, metricType string, duration time.Duration) {
	im.collectionLatency.WithLabelValues(server, metricType).Observe(duration.Seconds())
}

// RecordCollectionSuccess records successful metric collection
func (im *InternalMonitoring) RecordCollectionSuccess(server, metricType string) {
	im.collectionSuccess.WithLabelValues(server, metricType).Inc()
}

// RecordCollectionFailure records failed metric collection
func (im *InternalMonitoring) RecordCollectionFailure(server, metricType string) {
	im.collectionFailures.WithLabelValues(server, metricType).Inc()
}

// RecordRateLimitRequest records rate limit requests
func (im *InternalMonitoring) RecordRateLimitRequest(clientIP, endpoint string) {
	im.rateLimitRequests.WithLabelValues(clientIP, endpoint).Inc()
}

// RecordRateLimitBlocked records blocked requests
func (im *InternalMonitoring) RecordRateLimitBlocked(clientIP, endpoint string) {
	im.rateLimitBlocked.WithLabelValues(clientIP, endpoint).Inc()
}

// UpdateSystemMetrics updates system-level metrics
func (im *InternalMonitoring) UpdateSystemMetrics(server string, goroutines int, heapBytes, stackBytes, sysBytes uint64) {
	im.goroutineCount.WithLabelValues(server).Set(float64(goroutines))
	im.memoryUsage.WithLabelValues(server, "heap").Set(float64(heapBytes))
	im.memoryUsage.WithLabelValues(server, "stack").Set(float64(stackBytes))
	im.memoryUsage.WithLabelValues(server, "sys").Set(float64(sysBytes))

	uptime := time.Since(im.startTime).Seconds()
	im.uptime.WithLabelValues(server).Set(uptime)
}
