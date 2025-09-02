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
)

// collectionTask represents a metric collection task
type collectionTask struct {
	name string
	fn   func(string)
}

// OpenLDAPExporter implements the Prometheus collector interface for OpenLDAP metrics
type OpenLDAPExporter struct {
	client          *pool.PooledLDAPClient
	metricsRegistry *metrics.OpenLDAPMetrics
	config          *config.Config
	monitoring      *monitoring.InternalMonitoring
	counterValues   map[string]*sync.Map // Per-server counter tracking
	counterMutex    sync.RWMutex         // Protects counterValues map
	stopChan        chan struct{}        // Signal to stop background goroutines
	stopped         int32                // Atomic flag to indicate if exporter is stopped
	closeOnce       sync.Once            // Ensures Close() is called only once
	totalScrapes    atomicFloat64
	totalErrors     atomicFloat64
	lastScrapeTime  atomicFloat64
}

// NewOpenLDAPExporter creates a new OpenLDAP exporter with the given configuration
func NewOpenLDAPExporter(cfg *config.Config) *OpenLDAPExporter {
	// Create internal monitoring first
	internalMonitoring := monitoring.NewInternalMonitoring()

	// Initialize metrics with zero values for the server
	internalMonitoring.InitializeMetricsForServer(cfg.ServerName)

	// Use pooled LDAP client with monitoring support
	client := pool.NewPooledLDAPClientWithMonitoring(cfg, internalMonitoring, cfg.ServerName)

	exporter := &OpenLDAPExporter{
		client:          client,
		metricsRegistry: metrics.NewOpenLDAPMetrics(),
		config:          cfg,
		monitoring:      internalMonitoring,
		counterValues:   make(map[string]*sync.Map),
		stopChan:        make(chan struct{}),
	}

	// Initialize Up metric to indicate exporter is running
	exporter.metricsRegistry.Up.WithLabelValues(cfg.ServerName).Set(1)

	// Start cleanup routine for old counter entries
	go exporter.cleanupOldCounters()

	logger.SafeInfo("exporter", "OpenLDAP exporter created", map[string]interface{}{
		"server":          cfg.ServerName,
		"metrics_include": cfg.MetricsInclude,
		"metrics_exclude": cfg.MetricsExclude,
		"dc_include":      cfg.DCInclude,
		"dc_exclude":      cfg.DCExclude,
	})

	return exporter
}

// getAllMetrics returns all metrics collectors for Describe and Collect operations
func (e *OpenLDAPExporter) getAllMetrics() []prometheus.Collector {
	return []prometheus.Collector{
		e.metricsRegistry.Up,
		e.metricsRegistry.ScrapeErrors,
		e.metricsRegistry.ConnectionsCurrent,
		e.metricsRegistry.ConnectionsTotal,
		e.metricsRegistry.BytesTotal,
		e.metricsRegistry.EntriesTotal,
		e.metricsRegistry.ReferralsTotal,
		e.metricsRegistry.PduTotal,
		e.metricsRegistry.OperationsInitiated,
		e.metricsRegistry.OperationsCompleted,
		e.metricsRegistry.ThreadsMax,
		e.metricsRegistry.ThreadsMaxPending,
		e.metricsRegistry.ThreadsOpen,
		e.metricsRegistry.ThreadsStarting,
		e.metricsRegistry.ThreadsActive,
		e.metricsRegistry.ThreadsPending,
		e.metricsRegistry.ThreadsBackload,
		e.metricsRegistry.ThreadsState,
		e.metricsRegistry.ServerTime,
		e.metricsRegistry.ServerUptime,
		e.metricsRegistry.WaitersRead,
		e.metricsRegistry.WaitersWrite,
		e.metricsRegistry.OverlaysInfo,
		e.metricsRegistry.TlsInfo,
		e.metricsRegistry.BackendsInfo,
		e.metricsRegistry.ListenersInfo,
		e.metricsRegistry.HealthStatus,
		e.metricsRegistry.ResponseTime,
		e.metricsRegistry.DatabaseEntries,
		e.metricsRegistry.DatabaseInfo,
		e.metricsRegistry.ServerInfo,
		e.metricsRegistry.LogLevels,
		e.metricsRegistry.SaslInfo,
	}
}

// GetInternalMonitoring returns the internal monitoring instance
func (e *OpenLDAPExporter) GetInternalMonitoring() *monitoring.InternalMonitoring {
	return e.monitoring
}

// GetConfig returns the configuration instance
func (e *OpenLDAPExporter) GetConfig() *config.Config {
	return e.config
}

// Describe sends metric descriptions to Prometheus
func (e *OpenLDAPExporter) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range e.getAllMetrics() {
		metric.Describe(ch)
	}
}

// Collect gathers metrics from OpenLDAP and sends them to Prometheus
func (e *OpenLDAPExporter) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), e.config.Timeout)
	defer cancel()

	// Update scrape counter
	e.totalScrapes.Store(e.totalScrapes.Load() + 1)
	e.lastScrapeTime.Store(float64(time.Now().Unix()))

	// Collect all metrics with error handling
	err := e.collectAllMetricsWithContext(ctx)

	if err != nil {
		logger.SafeError("exporter", "Failed to collect metrics", err)
		e.metricsRegistry.Up.WithLabelValues(e.config.ServerName).Set(0)
		e.metricsRegistry.ScrapeErrors.WithLabelValues(e.config.ServerName).Inc()
		e.totalErrors.Store(e.totalErrors.Load() + 1)
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
	}

	// Collect metrics in parallel with timeout protection
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
					logger.SafeWarn("exporter", "Metric collection cancelled", map[string]interface{}{
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
