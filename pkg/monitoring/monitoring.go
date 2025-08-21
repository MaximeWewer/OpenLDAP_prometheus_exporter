package monitoring

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// InternalMonitoring provides internal monitoring capabilities for the exporter
type InternalMonitoring struct {
	// Pool metrics
	poolUtilization *prometheus.GaugeVec
	poolConnections *prometheus.GaugeVec
	poolWaitTime    *prometheus.HistogramVec
	poolOperations  *prometheus.CounterVec

	// Circuit breaker metrics
	circuitBreakerState    *prometheus.GaugeVec
	circuitBreakerRequests *prometheus.CounterVec
	circuitBreakerFailures *prometheus.CounterVec

	// Collection metrics
	collectionLatency  *prometheus.HistogramVec
	collectionSuccess  *prometheus.CounterVec
	collectionFailures *prometheus.CounterVec

	// Cache metrics
	cacheOperations *prometheus.CounterVec
	cacheHitRatio   *prometheus.GaugeVec
	cacheSize       *prometheus.GaugeVec

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

// EventCounter tracks events over time
type EventCounter struct {
	Count     int64
	LastEvent time.Time
	Rate      float64 // Events per second
	mutex     sync.RWMutex
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
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
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
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
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

		// Cache metrics
		cacheOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap_exporter",
				Subsystem: "cache",
				Name:      "operations_total",
				Help:      "Total cache operations",
			},
			[]string{"server", "operation"}, // hit, miss, set, delete
		),

		cacheHitRatio: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "cache",
				Name:      "hit_ratio",
				Help:      "Cache hit ratio (0-1)",
			},
			[]string{"server"},
		),

		cacheSize: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap_exporter",
				Subsystem: "cache",
				Name:      "size_bytes",
				Help:      "Cache size in bytes",
			},
			[]string{"server", "cache_type"},
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
	// But initialize default values to ensure they appear
	monitoring.initializeDefaultValues()

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

// RecordCacheOperation records cache operations
func (im *InternalMonitoring) RecordCacheOperation(server, operation string) {
	im.cacheOperations.WithLabelValues(server, operation).Inc()
}

// RecordCacheHitRatio records cache hit ratio
func (im *InternalMonitoring) RecordCacheHitRatio(server string, ratio float64) {
	im.cacheHitRatio.WithLabelValues(server).Set(ratio)
}

// RecordCacheSize records cache size
func (im *InternalMonitoring) RecordCacheSize(server, cacheType string, sizeBytes float64) {
	im.cacheSize.WithLabelValues(server, cacheType).Set(sizeBytes)
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

// RecordEvent records a custom event
func (im *InternalMonitoring) RecordEvent(eventType string) {
	im.eventMutex.Lock()
	defer im.eventMutex.Unlock()

	now := time.Now()

	if counter, exists := im.events[eventType]; exists {
		counter.mutex.Lock()
		counter.Count++

		// Calculate rate (events per second over last minute)
		if !counter.LastEvent.IsZero() {
			duration := now.Sub(counter.LastEvent).Seconds()
			if duration > 0 {
				counter.Rate = float64(counter.Count) / duration
			}
		}

		counter.LastEvent = now
		counter.mutex.Unlock()
	} else {
		im.events[eventType] = &EventCounter{
			Count:     1,
			LastEvent: now,
			Rate:      0,
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
		"start_time": im.startTime,
		"uptime":     time.Since(im.startTime).String(),
	}
}

// GetStartTime returns the start time of the exporter
func (im *InternalMonitoring) GetStartTime() time.Time {
	return im.startTime
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
		// Pool metrics
		im.poolUtilization,
		im.poolConnections,
		im.poolWaitTime,
		im.poolOperations,

		// Circuit breaker metrics
		im.circuitBreakerState,
		im.circuitBreakerRequests,
		im.circuitBreakerFailures,

		// Collection metrics
		im.collectionLatency,
		im.collectionSuccess,
		im.collectionFailures,

		// Cache metrics
		im.cacheOperations,
		im.cacheHitRatio,
		im.cacheSize,

		// Rate limiting metrics
		im.rateLimitRequests,
		im.rateLimitBlocked,

		// System metrics
		im.goroutineCount,
		im.memoryUsage,
		im.uptime,
	}

	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			logger.SafeWarn("monitoring", "Failed to register internal metric collector", map[string]interface{}{
				"error": err.Error(),
			})
			// Continue registering other collectors even if one fails
		}
	}

	// Initialize metrics with default values (0) to ensure they appear in Prometheus output
	im.initializeDefaultValues()

	logger.SafeDebug("monitoring", "Internal monitoring metrics registered with registry", map[string]interface{}{
		"collectors_count": len(collectors),
	})

	return nil
}

// initializeDefaultValues initializes all internal metrics with default values (0)
// This ensures they appear in Prometheus output even before any actual events occur
func (im *InternalMonitoring) initializeDefaultValues() {
	// Use const strings to avoid any memory corruption issues
	const serverLabel = "openldap-exporter"
	const poolType = "ldap"

	// Initialize pool metrics (requires server and pool_type labels)
	im.poolUtilization.WithLabelValues(serverLabel, poolType).Set(0)
	im.poolConnections.WithLabelValues(serverLabel, poolType, "active").Set(0)
	im.poolConnections.WithLabelValues(serverLabel, poolType, "idle").Set(0)
	im.poolConnections.WithLabelValues(serverLabel, poolType, "total").Set(0)

	// Initialize pool operations
	im.poolOperations.WithLabelValues(serverLabel, poolType, "get").Add(0)
	im.poolOperations.WithLabelValues(serverLabel, poolType, "put").Add(0)
	im.poolOperations.WithLabelValues(serverLabel, poolType, "create").Add(0)
	im.poolOperations.WithLabelValues(serverLabel, poolType, "close").Add(0)

	// Initialize circuit breaker metrics (requires server label only)
	im.circuitBreakerState.WithLabelValues(serverLabel).Set(0) // 0=closed state
	im.circuitBreakerRequests.WithLabelValues(serverLabel, "allowed").Add(0)
	im.circuitBreakerRequests.WithLabelValues(serverLabel, "blocked").Add(0)
	im.circuitBreakerFailures.WithLabelValues(serverLabel).Add(0)

	// Initialize collection metrics
	im.collectionSuccess.WithLabelValues(serverLabel, "connections").Add(0)
	im.collectionSuccess.WithLabelValues(serverLabel, "statistics").Add(0)
	im.collectionSuccess.WithLabelValues(serverLabel, "operations").Add(0)
	im.collectionSuccess.WithLabelValues(serverLabel, "threads").Add(0)
	im.collectionSuccess.WithLabelValues(serverLabel, "health").Add(0)

	im.collectionFailures.WithLabelValues(serverLabel, "connections").Add(0)
	im.collectionFailures.WithLabelValues(serverLabel, "statistics").Add(0)
	im.collectionFailures.WithLabelValues(serverLabel, "operations").Add(0)
	im.collectionFailures.WithLabelValues(serverLabel, "threads").Add(0)
	im.collectionFailures.WithLabelValues(serverLabel, "health").Add(0)

	// Initialize cache metrics
	im.cacheOperations.WithLabelValues(serverLabel, "hit").Add(0)
	im.cacheOperations.WithLabelValues(serverLabel, "miss").Add(0)
	im.cacheOperations.WithLabelValues(serverLabel, "set").Add(0)
	im.cacheOperations.WithLabelValues(serverLabel, "delete").Add(0)
	im.cacheHitRatio.WithLabelValues(serverLabel).Set(0)
	im.cacheSize.WithLabelValues(serverLabel, "connection").Set(0)

	// Initialize rate limiting metrics
	im.rateLimitRequests.WithLabelValues("0.0.0.0", "/metrics").Add(0)
	im.rateLimitRequests.WithLabelValues("0.0.0.0", "/health").Add(0)
	im.rateLimitRequests.WithLabelValues("0.0.0.0", "/internal/metrics").Add(0)

	im.rateLimitBlocked.WithLabelValues("0.0.0.0", "/metrics").Add(0)
	im.rateLimitBlocked.WithLabelValues("0.0.0.0", "/health").Add(0)
	im.rateLimitBlocked.WithLabelValues("0.0.0.0", "/internal/metrics").Add(0)

	// System metrics are updated in real-time, so we don't initialize them to 0
	// They will be populated by UpdateSystemMetrics() calls

	logger.SafeDebug("monitoring", "Internal metrics initialized with default values", map[string]interface{}{
		"server":    serverLabel,
		"pool_type": poolType,
	})
}
