package exporter

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// collectConnectionsMetrics collects connection-related metrics
func (e *OpenLDAPExporter) collectConnectionsMetrics(server string) {
	if !e.shouldCollectMetric("connections") {
		return
	}

	// Get all connection metrics in one request
	connectionCounters, err := e.getMonitorGroup("cn=Connections,cn=Monitor")
	if err != nil {
		logger.SafeError("exporter", "Failed to get connections group metrics", err)
		return
	}

	// Process the connection metrics
	for cnName, value := range connectionCounters {
		switch cnName {
		case "Current":
			e.metricsRegistry.ConnectionsCurrent.With(prometheus.Labels{"server": server}).Set(value)
		case "Total":
			e.updateCounter(e.metricsRegistry.ConnectionsTotal, server, "connections_total", value)
		}
	}

	logger.SafeDebug("exporter", "Collected connection metrics", map[string]interface{}{
		"server":         server,
		"counters_found": len(connectionCounters),
	})
}

// collectThreadsMetrics collects thread-related metrics
func (e *OpenLDAPExporter) collectThreadsMetrics(server string) {
	if !e.shouldCollectMetric("threads") {
		return
	}

	// Get all thread metrics in one request
	threadCounters, err := e.getMonitorGroup("cn=Threads,cn=Monitor")
	if err != nil {
		logger.SafeError("exporter", "Failed to get threads group metrics", err)
		return
	}

	// Map thread counter names to metrics
	threadMetrics := map[string]*prometheus.GaugeVec{
		"Max":         e.metricsRegistry.ThreadsMax,
		"Max Pending": e.metricsRegistry.ThreadsMaxPending,
		"Open":        e.metricsRegistry.ThreadsOpen,
		"Starting":    e.metricsRegistry.ThreadsStarting,
		"Active":      e.metricsRegistry.ThreadsActive,
		"Pending":     e.metricsRegistry.ThreadsPending,
		"Backload":    e.metricsRegistry.ThreadsBackload,
	}

	// Set the metrics from the retrieved counters
	for cnName, value := range threadCounters {
		if metric, exists := threadMetrics[cnName]; exists {
			metric.With(prometheus.Labels{"server": server}).Set(value)
			logger.SafeDebug("exporter", "Set thread metric", map[string]interface{}{
				"server": server,
				"metric": cnName,
				"value":  value,
			})
		}
	}

	// Thread pool state is handled within the main cn=Threads,cn=Monitor search above

	logger.SafeDebug("exporter", "Collected thread metrics", map[string]interface{}{
		"server":         server,
		"counters_found": len(threadCounters),
		"metrics_set":    len(threadMetrics),
	})
}

// collectTimeMetrics collects time-related metrics
func (e *OpenLDAPExporter) collectTimeMetrics(server string) {
	if !e.shouldCollectMetric("time") {
		return
	}

	// Get all time metrics in one request
	timeCounters, err := e.getMonitorGroup("cn=Time,cn=Monitor")
	if err != nil {
		logger.SafeError("exporter", "Failed to get time group metrics", err)
		return
	}

	// Process the time metrics
	for cnName, value := range timeCounters {
		switch cnName {
		case "Start":
			e.metricsRegistry.ServerTime.With(prometheus.Labels{"server": server}).Set(value)
		case "Uptime":
			e.metricsRegistry.ServerUptime.With(prometheus.Labels{"server": server}).Set(value)
		case "Current":
			// Current time updates the server time as well
			e.metricsRegistry.ServerTime.With(prometheus.Labels{"server": server}).Set(value)
		}
	}

	logger.SafeDebug("exporter", "Collected time metrics", map[string]interface{}{
		"server":         server,
		"counters_found": len(timeCounters),
	})
}

// collectWaitersMetrics collects waiter-related metrics
func (e *OpenLDAPExporter) collectWaitersMetrics(server string) {
	if !e.shouldCollectMetric("waiters") {
		return
	}

	// Get all waiter metrics in one request
	waiterCounters, err := e.getMonitorGroup("cn=Waiters,cn=Monitor")
	if err != nil {
		logger.SafeError("exporter", "Failed to get waiters group metrics", err)
		return
	}

	// Process the waiter metrics
	for cnName, value := range waiterCounters {
		switch cnName {
		case "Read":
			e.metricsRegistry.WaitersRead.With(prometheus.Labels{"server": server}).Set(value)
		case "Write":
			e.metricsRegistry.WaitersWrite.With(prometheus.Labels{"server": server}).Set(value)
		}
	}

	logger.SafeDebug("exporter", "Collected waiter metrics", map[string]interface{}{
		"server":         server,
		"counters_found": len(waiterCounters),
	})
}

// collectHealthMetrics collects health-related metrics
func (e *OpenLDAPExporter) collectHealthMetrics(server string) {
	if !e.shouldCollectMetric("health") {
		return
	}

	// Track connection time as health indicator
	start := time.Now()

	// Simple health check query
	_, err := e.client.Search(
		"cn=Monitor",
		"(objectClass=*)",
		[]string{"cn"},
	)

	responseTime := time.Since(start).Seconds()
	e.metricsRegistry.ResponseTime.With(prometheus.Labels{"server": server}).Set(responseTime)

	if err == nil {
		e.metricsRegistry.HealthStatus.With(prometheus.Labels{"server": server}).Set(1)
	} else {
		e.metricsRegistry.HealthStatus.With(prometheus.Labels{"server": server}).Set(0)
		logger.SafeError("exporter", "Health check failed", err)
	}
}
