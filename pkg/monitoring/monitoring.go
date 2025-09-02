package monitoring

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Constants for monitoring configuration
const (
	// Event tracking limits
	MaxEvents = 1000
	EventTTL  = 24 * time.Hour
	
	// Rate calculation window
	RateWindow = 60 * time.Second
	
	// Placeholder for unknown client IP
	UnknownClientIP = "unknown"
)

// Histogram bucket definitions
var (
	DefaultLatencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
	ExtendedLatencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	ConnectionAgeBuckets   = []float64{1, 5, 10, 30, 60, 300, 600, 1800, 3600}
	ConnectionIdleBuckets  = []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300}
)

// InternalMonitoring provides internal monitoring capabilities for the exporter
type InternalMonitoring struct {
	// Pool metrics - basic
	poolUtilization *prometheus.GaugeVec
	poolConnections *prometheus.GaugeVec
	poolWaitTime    *prometheus.HistogramVec
	poolOperations  *prometheus.CounterVec

	// Pool metrics - detailed lifecycle
	poolConnectionsCreated *prometheus.CounterVec
	poolConnectionsClosed  *prometheus.CounterVec
	poolConnectionsFailed  *prometheus.CounterVec
	poolConnectionsReused  *prometheus.CounterVec

	// Pool metrics - connection quality
	poolConnectionAge      *prometheus.HistogramVec
	poolConnectionIdleTime *prometheus.HistogramVec
	poolWaitTimeouts       *prometheus.CounterVec

	// Pool metrics - health monitoring
	poolHealthChecks        *prometheus.CounterVec
	poolHealthCheckFailures *prometheus.CounterVec

	// Pool metrics - operation details
	poolGetRequests   *prometheus.CounterVec
	poolGetFailures   *prometheus.CounterVec
	poolPutRequests   *prometheus.CounterVec
	poolPutRejections *prometheus.CounterVec

	// Circuit breaker metrics
	circuitBreakerState    *prometheus.GaugeVec
	circuitBreakerRequests *prometheus.CounterVec
	circuitBreakerFailures *prometheus.CounterVec

	// Collection metrics
	collectionLatency  *prometheus.HistogramVec
	collectionSuccess  *prometheus.CounterVec
	collectionFailures *prometheus.CounterVec

	// Rate limiting metrics
	rateLimitRequests *prometheus.CounterVec
	rateLimitBlocked  *prometheus.CounterVec

	// System metrics
	goroutineCount *prometheus.GaugeVec
	memoryUsage    *prometheus.GaugeVec
	uptime         *prometheus.GaugeVec

	// Event tracking
	events     map[string]*EventCounter
	eventMutex sync.RWMutex

	// Start time for uptime calculation
	startTime time.Time
}

// EventCounter tracks events over time with cleanup capability
type EventCounter struct {
	Count     int64
	LastEvent time.Time
	FirstEvent time.Time
	Rate      float64 // Events per second
	mutex     sync.RWMutex
}

// SystemStats holds system-level metrics data
type SystemStats struct {
	Goroutines int
	HeapBytes  uint64
	StackBytes uint64
	SysBytes   uint64
}

// NewInternalMonitoring creates a new internal monitoring instance
func NewInternalMonitoring() *InternalMonitoring {
	monitoring := &InternalMonitoring{
		events:    make(map[string]*EventCounter),
		startTime: time.Now(),

		// Pool metrics
		poolUtilization: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "utilization_ratio",
				Help:      "Connection pool utilization ratio (0-1)",
			},
			[]string{"server", "pool_type"},
		),

		poolConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connections",
				Help:      "Number of connections in pool by state",
			},
			[]string{"server", "pool_type", "state"}, // active, idle, total
		),

		poolWaitTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "wait_time_seconds",
				Help:      "Time spent waiting for a connection from the pool",
				Buckets:   DefaultLatencyBuckets,
			},
			[]string{"server", "pool_type"},
		),

		poolOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "operations_total",
				Help:      "Total number of pool operations",
			},
			[]string{"server", "pool_type", "operation"}, // get, put, create, close
		),

		// Pool metrics - detailed lifecycle
		poolConnectionsCreated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connections_created_total",
				Help:      "Total number of connections created",
			},
			[]string{"server", "pool_type"},
		),

		poolConnectionsClosed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connections_closed_total",
				Help:      "Total number of connections closed",
			},
			[]string{"server", "pool_type", "reason"}, // normal, timeout, error, shutdown
		),

		poolConnectionsFailed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connections_failed_total",
				Help:      "Total number of failed connection attempts",
			},
			[]string{"server", "pool_type", "error"},
		),

		poolConnectionsReused: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connections_reused_total",
				Help:      "Total number of connections reused from pool",
			},
			[]string{"server", "pool_type"},
		),

		// Pool metrics - connection quality
		poolConnectionAge: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connection_age_seconds",
				Help:      "Age of connections when closed",
				Buckets:   ConnectionAgeBuckets,
			},
			[]string{"server", "pool_type"},
		),

		poolConnectionIdleTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "connection_idle_seconds",
				Help:      "Time connections spent idle in pool",
				Buckets:   ConnectionIdleBuckets,
			},
			[]string{"server", "pool_type"},
		),

		poolWaitTimeouts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "wait_timeouts_total",
				Help:      "Total number of connection wait timeouts",
			},
			[]string{"server", "pool_type"},
		),

		// Pool metrics - health monitoring
		poolHealthChecks: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "health_checks_total",
				Help:      "Total number of connection health checks performed",
			},
			[]string{"server", "pool_type", "result"}, // success, failure
		),

		poolHealthCheckFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "health_check_failures_total",
				Help:      "Total number of failed connection health checks",
			},
			[]string{"server", "pool_type"},
		),

		// Pool metrics - operation details
		poolGetRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "get_requests_total",
				Help:      "Total number of connection get requests",
			},
			[]string{"server", "pool_type"},
		),

		poolGetFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "get_failures_total",
				Help:      "Total number of failed connection get requests",
			},
			[]string{"server", "pool_type", "reason"}, // timeout, creation_failed, max_attempts
		),

		poolPutRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "put_requests_total",
				Help:      "Total number of connection put (return) requests",
			},
			[]string{"server", "pool_type"},
		),

		poolPutRejections: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "pool",
				Name:      "put_rejections_total",
				Help:      "Total number of rejected connection put requests",
			},
			[]string{"server", "pool_type", "reason"}, // invalid_connection, pool_full
		),

		// Circuit breaker metrics
		circuitBreakerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "circuit_breaker",
				Name:      "state",
				Help:      "Circuit breaker state (0=closed, 1=half-open, 2=open)",
			},
			[]string{"server"},
		),

		circuitBreakerRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "circuit_breaker",
				Name:      "requests_total",
				Help:      "Total circuit breaker requests",
			},
			[]string{"server", "result"}, // allowed, blocked
		),

		circuitBreakerFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "circuit_breaker",
				Name:      "failures_total",
				Help:      "Total circuit breaker failures",
			},
			[]string{"server"},
		),

		// Collection metrics
		collectionLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "openldap_exporter",
				Subsystem: "collection",
				Name:      "latency_seconds",
				Help:      "Metric collection latency",
				Buckets:   ExtendedLatencyBuckets,
			},
			[]string{"server", "metric_type"},
		),

		collectionSuccess: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "collection",
				Name:      "success_total",
				Help:      "Total successful metric collections",
			},
			[]string{"server", "metric_type"},
		),

		collectionFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "collection",
				Name:      "failures_total",
				Help:      "Total failed metric collections",
			},
			[]string{"server", "metric_type"},
		),

		// Rate limiting metrics
		rateLimitRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "rate_limit",
				Name:      "requests_total",
				Help:      "Total rate limit requests",
			},
			[]string{"client_ip", "endpoint"},
		),

		rateLimitBlocked: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "rate_limit",
				Name:      "blocked_total",
				Help:      "Total blocked requests due to rate limiting",
			},
			[]string{"client_ip", "endpoint"},
		),

		// System metrics
		goroutineCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "system",
				Name:      "goroutines",
				Help:      "Number of goroutines",
			},
			[]string{"server"},
		),

		memoryUsage: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "system",
				Name:      "memory_bytes",
				Help:      "Memory usage in bytes",
			},
			[]string{"server", "type"}, // heap, stack, sys
		),

		uptime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "system",
				Name:      "uptime_seconds",
				Help:      "Uptime in seconds",
			},
			[]string{"server"},
		),
	}

	// Do not register with global registry to avoid conflicts
	// Metrics will be registered only when needed via RegisterMetrics()
	// Default values will be initialized when metrics are used

	return monitoring
}

// RecordPoolUtilization records connection pool utilization
func (im *InternalMonitoring) RecordPoolUtilization(server, poolType string, utilization float64) {
	im.poolUtilization.WithLabelValues(server, poolType).Set(utilization)
}

// RecordPoolConnections records connection pool connection counts
func (im *InternalMonitoring) RecordPoolConnections(server, poolType, state string, count float64) {
	im.poolConnections.WithLabelValues(server, poolType, state).Set(count)
}

// RecordPoolWaitTime records time spent waiting for a connection
func (im *InternalMonitoring) RecordPoolWaitTime(server, poolType string, duration time.Duration) {
	im.poolWaitTime.WithLabelValues(server, poolType).Observe(duration.Seconds())
}

// RecordPoolOperation records pool operations
func (im *InternalMonitoring) RecordPoolOperation(server, poolType, operation string) {
	im.poolOperations.WithLabelValues(server, poolType, operation).Inc()
}

// Pool lifecycle recording methods

// RecordPoolConnectionCreated records connection creation
func (im *InternalMonitoring) RecordPoolConnectionCreated(server, poolType string) {
	im.poolConnectionsCreated.WithLabelValues(server, poolType).Inc()
}

// RecordPoolConnectionClosed records connection closure
func (im *InternalMonitoring) RecordPoolConnectionClosed(server, poolType, reason string) {
	im.poolConnectionsClosed.WithLabelValues(server, poolType, reason).Inc()
}

// RecordPoolConnectionFailed records connection failure
func (im *InternalMonitoring) RecordPoolConnectionFailed(server, poolType, errorType string) {
	im.poolConnectionsFailed.WithLabelValues(server, poolType, errorType).Inc()
}

// RecordPoolConnectionReused records connection reuse from pool
func (im *InternalMonitoring) RecordPoolConnectionReused(server, poolType string) {
	im.poolConnectionsReused.WithLabelValues(server, poolType).Inc()
}

// Pool quality recording methods

// RecordPoolConnectionAge records connection age when closed
func (im *InternalMonitoring) RecordPoolConnectionAge(server, poolType string, age time.Duration) {
	im.poolConnectionAge.WithLabelValues(server, poolType).Observe(age.Seconds())
}

// RecordPoolConnectionIdleTime records connection idle time
func (im *InternalMonitoring) RecordPoolConnectionIdleTime(server, poolType string, idleTime time.Duration) {
	im.poolConnectionIdleTime.WithLabelValues(server, poolType).Observe(idleTime.Seconds())
}

// RecordPoolWaitTimeout records connection wait timeout
func (im *InternalMonitoring) RecordPoolWaitTimeout(server, poolType string) {
	im.poolWaitTimeouts.WithLabelValues(server, poolType).Inc()
}

// Pool health recording methods

// RecordPoolHealthCheck records health check execution with result
func (im *InternalMonitoring) RecordPoolHealthCheck(server, poolType, result string) {
	im.poolHealthChecks.WithLabelValues(server, poolType, result).Inc()
}

// RecordPoolHealthCheckFailure records health check failure
func (im *InternalMonitoring) RecordPoolHealthCheckFailure(server, poolType string) {
	im.poolHealthCheckFailures.WithLabelValues(server, poolType).Inc()
}

// Pool operation recording methods

// RecordPoolGetRequest records connection get request
func (im *InternalMonitoring) RecordPoolGetRequest(server, poolType string) {
	im.poolGetRequests.WithLabelValues(server, poolType).Inc()
}

// RecordPoolGetFailure records connection get failure
func (im *InternalMonitoring) RecordPoolGetFailure(server, poolType, reason string) {
	im.poolGetFailures.WithLabelValues(server, poolType, reason).Inc()
}

// RecordPoolPutRequest records connection put (return) request
func (im *InternalMonitoring) RecordPoolPutRequest(server, poolType string) {
	im.poolPutRequests.WithLabelValues(server, poolType).Inc()
}

// RecordPoolPutRejection records connection put rejection
func (im *InternalMonitoring) RecordPoolPutRejection(server, poolType, reason string) {
	im.poolPutRejections.WithLabelValues(server, poolType, reason).Inc()
}

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

// RecordEvent records a custom event with proper rate calculation and memory management
func (im *InternalMonitoring) RecordEvent(eventType string) {
	im.eventMutex.Lock()
	defer im.eventMutex.Unlock()

	// Check if we need to cleanup old events
	if len(im.events) >= MaxEvents {
		im.cleanupOldEventsUnsafe()
	}

	now := time.Now()

	if counter, exists := im.events[eventType]; exists {
		counter.mutex.Lock()
		counter.Count++

		// Calculate rate using a sliding window approach
		if !counter.LastEvent.IsZero() {
			elapsed := now.Sub(counter.LastEvent)
			if elapsed < RateWindow {
				counter.Rate = 1.0 / elapsed.Seconds() // Current event rate
			} else {
				counter.Rate = 0 // Reset rate if too much time has passed
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
	// Record the latency
	im.collectionLatency.WithLabelValues(server, "scrape").Observe(duration.Seconds())

	// Record success or failure
	if success {
		im.collectionSuccess.WithLabelValues(server, "scrape").Inc()
	} else {
		im.collectionFailures.WithLabelValues(server, "scrape").Inc()
	}

	// Record as an event for general tracking
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

// RegisterMetrics registers all internal monitoring metrics with a Prometheus registry
func (im *InternalMonitoring) RegisterMetrics(registry *prometheus.Registry) error {
	collectors := []prometheus.Collector{
		// Pool metrics - basic
		im.poolUtilization,
		im.poolConnections,
		im.poolWaitTime,
		im.poolOperations,

		// Pool metrics - detailed lifecycle
		im.poolConnectionsCreated,
		im.poolConnectionsClosed,
		im.poolConnectionsFailed,
		im.poolConnectionsReused,

		// Pool metrics - connection quality
		im.poolConnectionAge,
		im.poolConnectionIdleTime,
		im.poolWaitTimeouts,

		// Pool metrics - health monitoring
		im.poolHealthChecks,
		im.poolHealthCheckFailures,

		// Pool metrics - operation details
		im.poolGetRequests,
		im.poolGetFailures,
		im.poolPutRequests,
		im.poolPutRejections,

		// Circuit breaker metrics
		im.circuitBreakerState,
		im.circuitBreakerRequests,
		im.circuitBreakerFailures,

		// Collection metrics
		im.collectionLatency,
		im.collectionSuccess,
		im.collectionFailures,

		// Rate limiting metrics
		im.rateLimitRequests,
		im.rateLimitBlocked,

		// System metrics
		im.goroutineCount,
		im.memoryUsage,
		im.uptime,
	}

	var (
		firstError                   error
		successCount, failureCount int
	)

	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			failureCount++
			logger.SafeWarn("monitoring", "Failed to register internal metric collector", map[string]interface{}{
				"error":          err.Error(),
				"collector_type": fmt.Sprintf("%T", collector),
			})
			if firstError == nil {
				firstError = err
			}
		} else {
			successCount++
		}
	}

	logger.SafeInfo("monitoring", "Metric registration completed", map[string]interface{}{
		"success_count":    successCount,
		"failure_count":    failureCount,
		"total_collectors": len(collectors),
	})

	return firstError
}

// InitializeMetricsForServer initializes all metrics with zero values for a specific server
// This ensures metrics appear in Prometheus even before any events occur
func (im *InternalMonitoring) InitializeMetricsForServer(serverName string) {
	const poolType = "ldap"
	
	// Initialize pool lifecycle metrics
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "normal").Add(0)
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "timeout").Add(0)
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "error").Add(0)
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "shutdown").Add(0)
	
	im.poolConnectionsFailed.WithLabelValues(serverName, poolType, "network_error").Add(0)
	im.poolConnectionsFailed.WithLabelValues(serverName, poolType, "auth_error").Add(0)
	
	// Initialize pool operation metrics
	im.poolGetFailures.WithLabelValues(serverName, poolType, "timeout").Add(0)
	im.poolGetFailures.WithLabelValues(serverName, poolType, "creation_failed").Add(0)
	im.poolGetFailures.WithLabelValues(serverName, poolType, "max_attempts").Add(0)
	
	im.poolPutRejections.WithLabelValues(serverName, poolType, "invalid_connection").Add(0)
	im.poolPutRejections.WithLabelValues(serverName, poolType, "pool_full").Add(0)
	
	// Initialize pool quality metrics
	im.poolWaitTimeouts.WithLabelValues(serverName, poolType).Add(0)
	
	// Initialize pool health metrics
	im.poolHealthChecks.WithLabelValues(serverName, poolType, "success").Add(0)
	im.poolHealthChecks.WithLabelValues(serverName, poolType, "failure").Add(0)
	im.poolHealthCheckFailures.WithLabelValues(serverName, poolType).Add(0)
	
	// Initialize circuit breaker metrics
	im.circuitBreakerFailures.WithLabelValues(serverName).Add(0)
	im.circuitBreakerRequests.WithLabelValues(serverName, "blocked").Add(0)
	
	// Initialize collection failure metrics
	im.collectionFailures.WithLabelValues(serverName, "scrape").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "connections").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "statistics").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "operations").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "threads").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "health").Add(0)
	
	// Initialize rate limiting metrics for common endpoints
	commonEndpoints := []string{"/metrics", "/health", "/internal/metrics"}
	for _, endpoint := range commonEndpoints {
		im.rateLimitBlocked.WithLabelValues(UnknownClientIP, endpoint).Add(0)
	}
	
	logger.SafeDebug("monitoring", "Initialized metrics for server", map[string]interface{}{
		"server": serverName,
	})
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

// CleanupEvents manually triggers cleanup of old events
func (im *InternalMonitoring) CleanupEvents() {
	im.eventMutex.Lock()
	defer im.eventMutex.Unlock()
	im.cleanupOldEventsUnsafe()
}

