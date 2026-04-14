package exporter

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/accesslog"
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

	// Thread pool state is exposed as a separate "info" gauge whose
	// value is always 1 and whose `state` label carries the textual
	// status reported by slapd (e.g. "ready", "unknown"). The value
	// lives under cn=State,cn=Threads,cn=Monitor as a monitoredInfo
	// string, so getMonitorGroup (which only parses numeric counters)
	// cannot populate it — the gauge used to be declared but never
	// written.
	e.collectThreadsState(server)

	logger.SafeDebug("exporter", "Collected thread metrics", map[string]interface{}{
		"server":         server,
		"counters_found": len(threadCounters),
		"metrics_set":    len(threadMetrics),
	})
}

// collectThreadsState queries cn=State,cn=Threads,cn=Monitor, reads the
// textual monitoredInfo value, and exposes it as an info-style gauge on
// ThreadsState. The vector is Reset() first so a previous state label
// stops publishing the moment slapd moves to a new state.
func (e *OpenLDAPExporter) collectThreadsState(server string) {
	result, err := e.client.Search(
		"cn=State,cn=Threads,cn=Monitor",
		"(objectClass=*)",
		[]string{"monitoredInfo"},
	)
	if err != nil {
		logger.SafeDebug("exporter", "Thread state subtree not available (optional)", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	e.metricsRegistry.ThreadsState.Reset()
	for _, entry := range result.Entries {
		for _, attr := range entry.Attributes {
			if attr.Name != "monitoredInfo" || len(attr.Values) == 0 {
				continue
			}
			e.metricsRegistry.ThreadsState.With(prometheus.Labels{
				"server": server,
				"state":  attr.Values[0],
			}).Set(1)
		}
	}
}

// collectTimeMetrics collects time-related metrics.
//
// cn=Time,cn=Monitor is an odd subtree: cn=Uptime carries an integer
// seconds value in the regular `monitoredInfo` attribute, but cn=Start
// and cn=Current publish their values only as the operational
// `monitorTimestamp` attribute (a GeneralizedTime). getMonitorGroup
// reads neither of those as a time value, so the previous
// implementation — which relied exclusively on it — silently left
// ServerTime unset. The fix issues a dedicated search that requests
// `monitorTimestamp` explicitly and parses it via the shared accesslog
// helper.
func (e *OpenLDAPExporter) collectTimeMetrics(server string) {
	if !e.shouldCollectMetric("time") {
		return
	}

	result, err := e.client.Search(
		"cn=Time,cn=Monitor",
		"(objectClass=*)",
		[]string{"monitoredInfo", "monitorTimestamp"},
	)
	if err != nil {
		logger.SafeError("exporter", "Failed to get time metrics", err)
		return
	}

	entriesFound := 0
	for _, entry := range result.Entries {
		cn := extractCNFromFirstComponent(entry.DN)
		if cn == "" {
			continue
		}
		var info, ts string
		for _, attr := range entry.Attributes {
			if len(attr.Values) == 0 {
				continue
			}
			switch attr.Name {
			case "monitoredInfo":
				info = attr.Values[0]
			case "monitorTimestamp":
				ts = attr.Values[0]
			}
		}
		switch cn {
		case "Uptime":
			if info == "" {
				continue
			}
			if v, err := strconv.ParseFloat(info, 64); err == nil {
				e.metricsRegistry.ServerUptime.With(prometheus.Labels{"server": server}).Set(v)
				entriesFound++
			}
		case "Current", "Start":
			if ts == "" {
				continue
			}
			t, err := accesslog.ParseGeneralizedTime(ts)
			if err != nil {
				logger.SafeDebug("exporter", "Failed to parse monitorTimestamp", map[string]interface{}{
					"cn":    cn,
					"value": ts,
					"error": err.Error(),
				})
				continue
			}
			// Both Current and Start feed into ServerTime. Current wins
			// on the last iteration when both are returned because
			// callers typically want "now according to slapd" rather
			// than the startup timestamp.
			e.metricsRegistry.ServerTime.With(prometheus.Labels{"server": server}).Set(float64(t.Unix()))
			entriesFound++
		}
	}

	logger.SafeDebug("exporter", "Collected time metrics", map[string]interface{}{
		"server":         server,
		"counters_found": entriesFound,
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
