package exporter

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
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

	// Collect individual connection details (protocol, aggregate ops)
	e.collectIndividualConnections(server)
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
	e.metricsRegistry.ResponseTime.With(prometheus.Labels{"server": server}).Observe(responseTime)

	if err == nil {
		e.metricsRegistry.HealthStatus.With(prometheus.Labels{"server": server}).Set(1)
	} else {
		e.metricsRegistry.HealthStatus.With(prometheus.Labels{"server": server}).Set(0)
		logger.SafeError("exporter", "Health check failed", err)
	}
}

// collectIndividualConnections queries individual connection entries under cn=Connections,cn=Monitor
// and aggregates metrics by protocol version and operation state.
func (e *OpenLDAPExporter) collectIndividualConnections(server string) {
	// Reset so a protocol that no longer has any active connection (e.g.
	// the last LDAPv2 client disconnected) stops publishing its previous
	// value as a frozen ghost series.
	e.metricsRegistry.ConnectionsByProtocol.Reset()

	result, err := e.client.Search(
		"cn=Connections,cn=Monitor",
		"(objectClass=*)",
		[]string{"monitorConnectionProtocol", "monitorConnectionOpsExecuting", "monitorConnectionOpsPending", "monitorConnectionOpsReceived", "monitorConnectionOpsCompleted"},
	)
	if err != nil {
		logger.SafeDebug("exporter", "Failed to query individual connections", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	protocolCounts := make(map[string]float64)
	var totalExecuting, totalPending, totalReceived, totalCompleted float64

	for _, entry := range result.Entries {
		// Skip the parent entry cn=Connections,cn=Monitor itself.
		// Use ldap.ParseDN so we match by the first RDN regardless of
		// attribute-name case (RFC 4514 allows "CN=Connection 42"); the
		// previous strings.Contains missed any entry whose DN differed
		// from the literal "cn=Connection " prefix.
		if !hasConnectionRDN(entry.DN) {
			continue
		}

		for _, attr := range entry.Attributes {
			if len(attr.Values) == 0 {
				continue
			}
			switch attr.Name {
			case "monitorConnectionProtocol":
				protocolCounts[attr.Values[0]]++
			case "monitorConnectionOpsExecuting":
				if v, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
					totalExecuting += v
				}
			case "monitorConnectionOpsPending":
				if v, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
					totalPending += v
				}
			case "monitorConnectionOpsReceived":
				if v, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
					totalReceived += v
				}
			case "monitorConnectionOpsCompleted":
				if v, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
					totalCompleted += v
				}
			}
		}
	}

	for protocol, count := range protocolCounts {
		e.metricsRegistry.ConnectionsByProtocol.With(prometheus.Labels{
			"server":   server,
			"protocol": protocol,
		}).Set(count)
	}

	e.metricsRegistry.ConnectionOpsAggregate.With(prometheus.Labels{"server": server, "state": "executing"}).Set(totalExecuting)
	e.metricsRegistry.ConnectionOpsAggregate.With(prometheus.Labels{"server": server, "state": "pending"}).Set(totalPending)
	e.metricsRegistry.ConnectionOpsAggregate.With(prometheus.Labels{"server": server, "state": "received"}).Set(totalReceived)
	e.metricsRegistry.ConnectionOpsAggregate.With(prometheus.Labels{"server": server, "state": "completed"}).Set(totalCompleted)

	logger.SafeDebug("exporter", "Collected individual connection metrics", map[string]interface{}{
		"server":    server,
		"protocols": protocolCounts,
	})
}

// hasConnectionRDN returns true when the first RDN of dn is a cn=
// attribute whose value starts with "Connection " (case-insensitive).
// It tolerates any RFC-legal DN casing and escaping that slapd might
// emit on different builds.
func hasConnectionRDN(dn string) bool {
	parsed, err := ldap.ParseDN(dn)
	if err != nil || len(parsed.RDNs) == 0 {
		return false
	}
	for _, attr := range parsed.RDNs[0].Attributes {
		if strings.EqualFold(attr.Type, "cn") && strings.HasPrefix(strings.ToLower(attr.Value), "connection ") {
			return true
		}
	}
	return false
}
