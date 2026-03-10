package monitoring

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
	DefaultLatencyBuckets  = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
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
	Count      int64
	LastEvent  time.Time
	FirstEvent time.Time
	Rate       float64 // Events per second
	mutex      sync.RWMutex
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
			[]string{"server", "pool_type", "state"},
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
			[]string{"server", "pool_type", "operation"},
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
			[]string{"server", "pool_type", "reason"},
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
			[]string{"server", "pool_type", "result"},
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
			[]string{"server", "pool_type", "reason"},
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
			[]string{"server", "pool_type", "reason"},
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
			[]string{"server", "result"},
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
			[]string{"server", "type"},
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

	return monitoring
}
