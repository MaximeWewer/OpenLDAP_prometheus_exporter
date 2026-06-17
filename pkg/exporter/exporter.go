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
	// MaxReasonableCounterValue caps a single absolute counter reading.
	// uint64 monitorCounter wraps at ~1.8e19, so a real overflow would
	// be far above this. The previous 1e9 cap was triggered on every
	// busy slapd whose bytes_total / pdu_total naturally exceed 1 GB /
	// 1 G PDUs after a few days of uptime — a normal value, not a bug.
	MaxReasonableCounterValue = 1e15
	// MaxReasonableCounterDelta caps the per-scrape jump on a counter.
	// At a 15s scrape interval, 1e9 ops/scrape ≈ 66 M ops/s — orders of
	// magnitude above what slapd can actually serve, so anything above
	// that range still indicates wraparound or corruption rather than
	// real traffic.
	MaxReasonableCounterDelta = 1e9
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
	collecting     int32         // Atomic guard so background collection cycles never overlap
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

	// Start background collection so /metrics serves a cached snapshot instead
	// of running live LDAP I/O on the scrape path (see Collect / collectLoop).
	go exporter.collectLoop()
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

// Metrics returns the shared OpenLDAP metrics registry. It is exposed so
// auxiliary components (e.g. the replication peer poller) can publish into the
// same metric vectors that Collect() emits on each scrape.
func (e *OpenLDAPExporter) Metrics() *metrics.OpenLDAPMetrics {
	return e.metricsRegistry
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

// Collect serves the most recent cached metric snapshot to Prometheus. It does
// NO live LDAP I/O and never blocks on the connection pool or the circuit
// breaker: collection runs in the background (collectLoop) on the
// LDAP_UPDATE_EVERY ticker and writes into these metric vectors, so a /metrics
// scrape just emits their current values and returns immediately.
//
// This is the fix for "no metrics for hours": the previous design ran the full
// live collection inside Collect under collectMu, so whenever the LDAP server
// (or a dead pooled connection) was slow, the scrape handler hung for the whole
// collection — up to LDAP_TIMEOUT — and every subsequent scrape piled up behind
// it on collectMu. Decoupling the scrape from collection means a slow backend
// degrades metric freshness, never scrape availability.
func (e *OpenLDAPExporter) Collect(ch chan<- prometheus.Metric) {
	e.totalScrapes.Add(1)
	e.lastScrapeTime.Store(float64(time.Now().Unix()))

	for _, metric := range e.getAllMetrics() {
		metric.Collect(ch)
	}
}

// CollectNow runs one collection cycle synchronously and returns once the
// metric cache has been refreshed. Collection normally runs in the background
// (see Collect); this forces an immediate, blocking cycle — used by tests and
// available as an optional warm-up so the very first scrape already has data.
// It serializes with the background cycle via collectMu.
func (e *OpenLDAPExporter) CollectNow() {
	e.runCollection()
}

// collectLoop runs background collection on the LDAP_UPDATE_EVERY interval. It
// collects once immediately so the first scrape has data, then on each tick —
// skipping a tick when the previous cycle is still running so a slow LDAP
// server cannot pile up overlapping collections.
func (e *OpenLDAPExporter) collectLoop() {
	interval := e.config.UpdateEvery
	if interval <= 0 {
		interval = config.DefaultUpdateEvery
	}

	e.collectOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.collectOnce()
		case <-e.stopChan:
			return
		}
	}
}

// collectOnce runs a single background collection cycle unless one is already
// in flight, in which case the tick is skipped (the in-flight cycle will
// refresh the cache when it finishes).
func (e *OpenLDAPExporter) collectOnce() {
	if !atomic.CompareAndSwapInt32(&e.collecting, 0, 1) {
		logger.SafeWarn("exporter", "Skipping collection tick; previous cycle still running", nil)
		return
	}
	defer atomic.StoreInt32(&e.collecting, 0)
	e.runCollection()
}

// runCollection performs one live collection cycle against LDAP and updates the
// metric vectors that Collect serves. It is invoked only by collectLoop, never
// by the scrape handler, so its latency (or a hang up to LDAP_TIMEOUT) never
// affects /metrics availability.
func (e *OpenLDAPExporter) runCollection() {
	e.collectMu.Lock()
	defer e.collectMu.Unlock()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), e.config.Timeout)
	defer cancel()

	// Install the collection-scoped context on the pool client so every LDAP
	// round-trip honors the outer deadline / cancellation. Cleared on exit so
	// background probes fall back to a plain background context.
	e.client.SetBaseContext(ctx)
	defer e.client.SetBaseContext(context.Background())

	err := e.collectAllMetricsWithContext(ctx)
	if err != nil {
		logger.SafeError("exporter", "Failed to collect metrics", err)
		e.metricsRegistry.Up.WithLabelValues(e.config.ServerName).Set(0)
		e.metricsRegistry.ScrapeErrors.WithLabelValues(e.config.ServerName).Inc()
		e.totalErrors.Add(1)
	} else {
		e.metricsRegistry.Up.WithLabelValues(e.config.ServerName).Set(1)
	}

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
