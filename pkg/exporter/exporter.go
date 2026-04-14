package exporter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

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

	// MaxConcurrentCollectors limits the number of metric collection goroutines running in parallel
	MaxConcurrentCollectors = 4
)

// collectionTask represents a metric collection task
type collectionTask struct {
	name string
	fn   func(string)
}

// OpenLDAPExporter implements the Prometheus collector interface for OpenLDAP metrics
type OpenLDAPExporter struct {
	client                *pool.PooledLDAPClient
	metricsRegistry       *metrics.OpenLDAPMetrics
	config                *config.Config
	monitoring            *monitoring.InternalMonitoring
	counterValues         map[string]map[string]*counterEntry // Per-server counter tracking (typed map)
	counterMutex          sync.RWMutex                        // Protects counterValues map
	accesslogCursor       map[string]*accesslogCursorState    // Per-server accesslog incremental scan cursors
	accesslogMutex        sync.Mutex                          // Protects accesslogCursor map
	accesslogBindTracker  *accesslogSeriesTracker
	accesslogWriteTracker *accesslogSeriesTracker
	accesslogLockTracker  *accesslogSeriesTracker
	// collectMu serializes Collect() calls so two concurrent Prometheus
	// scrapes (e.g. Prometheus itself + a Grafana alerting evaluator
	// hitting /metrics at the same time) do not race on the delta-based
	// counter updates in counter_management.go — two readers observing
	// the same oldValue would each Add the same delta and lose one of
	// the increments.
	collectMu      sync.Mutex
	stopChan       chan struct{} // Signal to stop background goroutines
	stopped        int32         // Atomic flag to indicate if exporter is stopped
	closeOnce      sync.Once     // Ensures Close() is called only once
	totalScrapes   atomicFloat64
	totalErrors    atomicFloat64
	lastScrapeTime atomicFloat64
}

// NewOpenLDAPExporter creates a new OpenLDAP exporter with the given configuration
func NewOpenLDAPExporter(cfg *config.Config) *OpenLDAPExporter {
	// Create internal monitoring first
	internalMonitoring := monitoring.NewInternalMonitoring()

	// Initialize metrics with zero values for the server
	internalMonitoring.InitializeMetricsForServer(cfg.ServerName)

	// Use pooled LDAP client with monitoring support
	client := pool.NewPooledLDAPClientWithMonitoring(cfg, internalMonitoring, cfg.ServerName)

	registry := metrics.NewOpenLDAPMetrics()
	exporter := &OpenLDAPExporter{
		client:          client,
		metricsRegistry: registry,
		config:          cfg,
		monitoring:      internalMonitoring,
		counterValues:   make(map[string]map[string]*counterEntry),
		accesslogCursor: make(map[string]*accesslogCursorState),
		stopChan:        make(chan struct{}),
		accesslogBindTracker: newAccesslogSeriesTracker(
			"accesslog_bind_total", registry.AccesslogBindTotal, MaxAccesslogSeriesPerVec,
		),
		accesslogWriteTracker: newAccesslogSeriesTracker(
			"accesslog_write_total", registry.AccesslogWriteTotal, MaxAccesslogSeriesPerVec,
		),
		accesslogLockTracker: newAccesslogSeriesTracker(
			"accesslog_account_lock_events_total", registry.AccesslogLockEventsTotal, MaxAccesslogSeriesPerVec,
		),
	}

	// Initialize Up metric to indicate exporter is running
	exporter.metricsRegistry.Up.WithLabelValues(cfg.ServerName).Set(1)

	// Start cleanup routine for old counter entries
	go exporter.cleanupOldCounters()
	// Start sweeper that evicts stale accesslog series so a bad-actor
	// bind-DN rotation cannot grow the CounterVecs unbounded.
	go exporter.sweepAccesslogTrackers()

	logger.SafeInfo("exporter", "OpenLDAP exporter created", map[string]interface{}{
		"server":          cfg.ServerName,
		"metrics_include": cfg.MetricsInclude,
		"metrics_exclude": cfg.MetricsExclude,
		"dc_include":      cfg.DCInclude,
		"dc_exclude":      cfg.DCExclude,
	})

	return exporter
}

// getAllMetrics forwards to metricsRegistry.Collectors() so there is a
// single source of truth for the set of metrics the exporter exposes —
// previously an identical slice literal lived here AND in pkg/metrics,
// silently drifting apart every time a new metric was added.
func (e *OpenLDAPExporter) getAllMetrics() []prometheus.Collector {
	return e.metricsRegistry.Collectors()
}

// GetInternalMonitoring returns the internal monitoring instance
func (e *OpenLDAPExporter) GetInternalMonitoring() *monitoring.InternalMonitoring {
	return e.monitoring
}

// GetConfig returns the configuration instance
func (e *OpenLDAPExporter) GetConfig() *config.Config {
	return e.config
}

// Client returns the shared pooled LDAP client. It is exposed so auxiliary
// components (events runner, ad-hoc probes) can reuse the same connection pool
// instead of opening their own.
func (e *OpenLDAPExporter) Client() *pool.PooledLDAPClient {
	return e.client
}

// Describe sends metric descriptions to Prometheus
func (e *OpenLDAPExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range e.getAllMetrics() {
		metric.Describe(ch)
	}
}

// Collect gathers metrics from OpenLDAP and sends them to Prometheus.
//
// The entire body is serialized by collectMu: prometheus/client_golang
// invokes Collect on its own goroutine and nothing stops two independent
// HTTP handlers (for example /metrics for Prometheus and the same
// endpoint hit by Grafana alerting) from calling it concurrently. The
// delta-based counter update path in counter_management.go reads an old
// value, computes a delta, and stores the new value — if two callers
// read the same old value in parallel they both compute the same delta
// and one of the increments is lost. Serializing Collect closes that
// race at the cost of scrape concurrency (an acceptable tradeoff because
// the exporter's bottleneck is the LDAP round-trips, not CPU).
func (e *OpenLDAPExporter) Collect(ch chan<- prometheus.Metric) {
	e.collectMu.Lock()
	defer e.collectMu.Unlock()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), e.config.Timeout)
	defer cancel()

	// Install the scrape-scoped context on the pool client so every
	// LDAP round-trip issued by this Collect() honors the outer
	// deadline / cancellation instead of running under a detached
	// context.Background() internally. Cleared on exit so background
	// probes (e.g. the maintenance loop) fall back to a plain
	// background context.
	e.client.SetBaseContext(ctx)
	defer e.client.SetBaseContext(context.Background())

	// Update scrape counter atomically so concurrent scrapes (Prometheus
	// scraping /metrics while Grafana alerting hits it in parallel) do
	// not lose increments on a plain Load+Store race.
	e.totalScrapes.Add(1)
	e.lastScrapeTime.Store(float64(time.Now().Unix()))

	// Collect all metrics with error handling
	err := e.collectAllMetricsWithContext(ctx)

	if err != nil {
		logger.SafeError("exporter", "Failed to collect metrics", err)
		e.metricsRegistry.Up.WithLabelValues(e.config.ServerName).Set(0)
		e.metricsRegistry.ScrapeErrors.WithLabelValues(e.config.ServerName).Inc()
		e.totalErrors.Add(1)
	} else {
		e.metricsRegistry.Up.WithLabelValues(e.config.ServerName).Set(1)
	}

	// Collect all metrics to channel
	for _, metric := range e.getAllMetrics() {
		metric.Collect(ch)
	}

	// Update internal monitoring
	e.monitoring.RecordScrape(e.config.ServerName, time.Since(start), err == nil)

	logger.SafeDebug("exporter", "Metrics collection completed", map[string]interface{}{
		"duration": time.Since(start).Truncate(10 * time.Millisecond).String(),
		"success":  err == nil,
	})
}

// collectAllMetricsWithContext collects all enabled metrics with context support
func (e *OpenLDAPExporter) collectAllMetricsWithContext(ctx context.Context) error {
	server := e.config.ServerName

	// Ensure we have a valid connection
	if err := e.ensureConnection(); err != nil {
		return err
	}

	// Create a wait group for parallel collection
	var wg sync.WaitGroup
	var collectErrors []error
	var errorMutex sync.Mutex

	tasks := []collectionTask{
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
		{"server", e.collectServerInfoMetrics},
		{"log", e.collectLogMetrics},
		{"sasl", e.collectSASLMetrics},
		{"replication", e.collectReplicationMetrics},
		{"ppolicy", e.collectPpolicyMetrics},
		{"accesslog", e.collectAccesslogMetrics},
	}

	// Semaphore to limit concurrent collection goroutines
	sem := make(chan struct{}, MaxConcurrentCollectors)

	// Collect metrics in parallel with concurrency limit and timeout protection
	for _, task := range tasks {
		if !e.shouldCollectMetric(task.name) {
			logger.SafeDebug("exporter", "Skipping metric collection", map[string]interface{}{
				"metric_group": task.name,
				"reason":       "filtered",
			})
			continue
		}

		wg.Add(1)
		go func(t collectionTask) {
			defer wg.Done()

			// Acquire semaphore slot
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				errorMutex.Lock()
				collectErrors = append(collectErrors, ctx.Err())
				errorMutex.Unlock()
				return
			}

			// Create a sub-context with timeout for this specific collection
			taskCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			// Run collection with panic recovery
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicErr := errors.New("panic during metric collection")
						errorMutex.Lock()
						collectErrors = append(collectErrors, panicErr)
						errorMutex.Unlock()
						logger.SafeError("exporter", "Panic during metric collection", panicErr, map[string]interface{}{
							"metric_group": t.name,
							"panic":        r,
						})
					}
				}()

				// Check context before starting
				select {
				case <-taskCtx.Done():
					errorMutex.Lock()
					collectErrors = append(collectErrors, taskCtx.Err())
					errorMutex.Unlock()
					logger.SafeWarn("exporter", "Metric collection canceled", map[string]interface{}{
						"metric_group": t.name,
					})
					return
				default:
					t.fn(server)
				}
			}()
		}(task)
	}

	// Wait for all collections to complete or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All collections completed
	case <-ctx.Done():
		logger.SafeWarn("exporter", "Metric collection timeout", map[string]interface{}{
			"timeout": e.config.Timeout.Truncate(10 * time.Millisecond).String(),
		})
		return ctx.Err()
	}

	// Return first error if any occurred
	if len(collectErrors) > 0 {
		return collectErrors[0]
	}

	return nil
}

// Close gracefully shuts down the exporter
func (e *OpenLDAPExporter) Close() {
	// Set the stopped flag atomically
	if !atomic.CompareAndSwapInt32(&e.stopped, 0, 1) {
		// Already stopped
		return
	}

	// Use sync.Once to ensure cleanup happens only once
	e.closeOnce.Do(func() {
		logger.SafeInfo("exporter", "Shutting down OpenLDAP exporter", map[string]interface{}{
			"server": e.config.ServerName,
		})

		// Signal all goroutines to stop
		close(e.stopChan)

		// Close the LDAP client
		if e.client != nil {
			e.client.Close()
		}

		// Set Up metric to 0 to indicate exporter is stopped
		e.metricsRegistry.Up.WithLabelValues(e.config.ServerName).Set(0)

		logger.SafeInfo("exporter", "OpenLDAP exporter shut down complete", map[string]interface{}{
			"server":        e.config.ServerName,
			"total_scrapes": e.totalScrapes.Load(),
			"total_errors":  e.totalErrors.Load(),
		})
	})
}
