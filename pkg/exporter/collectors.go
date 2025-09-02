package exporter

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// searchLDAP performs an LDAP search with error handling and logging
func (e *OpenLDAPExporter) searchLDAP(baseDN, filter string, attributes []string, metricType string) (*ldap.SearchResult, error) {
	result, err := e.client.Search(baseDN, filter, attributes)
	if err != nil {
		logger.SafeError("exporter", "LDAP search failed", err, map[string]interface{}{
			"metric_type": metricType,
			"base_dn":     baseDN,
			"filter":      filter,
		})
	}
	return result, err
}

// extractCNFromDN extracts the CN value from a DN with a specific suffix
func extractCNFromDN(dn, suffix string) string {
	cn := strings.TrimPrefix(dn, "cn=")
	return strings.TrimSuffix(cn, suffix)
}

// parseFloatAttribute parses a float value from an LDAP attribute
func parseFloatAttribute(entry *ldap.Entry, attrName string) (float64, bool) {
	for _, attr := range entry.Attributes {
		if attr.Name == attrName && len(attr.Values) > 0 {
			if value, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

// logMetricCollection logs the completion of metric collection
func logMetricCollection(server, metricType string, count int) {
	logger.SafeDebug("exporter", "Collected metrics", map[string]interface{}{
		"server":      server,
		"metric_type": metricType,
		"count":       count,
	})
}

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

// collectStatisticsMetrics collects statistics metrics
func (e *OpenLDAPExporter) collectStatisticsMetrics(server string) {
	if !e.shouldCollectMetric("statistics") {
		return
	}

	result, err := e.searchLDAP(
		"cn=Statistics,cn=Monitor",
		"(objectClass=*)",
		[]string{"monitorCounter"},
		"statistics",
	)
	if err != nil {
		return
	}

	count := 0
	for _, entry := range result.Entries {
		baseName := extractCNFromDN(entry.DN, ",cn=Statistics,cn=Monitor")

		if value, found := parseFloatAttribute(entry, "monitorCounter"); found {
			count++
			switch baseName {
			case "Bytes":
				e.updateCounter(e.metricsRegistry.BytesTotal, server, "bytes_total", value)
			case "Entries":
				e.updateCounter(e.metricsRegistry.EntriesTotal, server, "entries_total", value)
			case "Referrals":
				e.updateCounter(e.metricsRegistry.ReferralsTotal, server, "referrals_total", value)
			case "PDU":
				e.updateCounter(e.metricsRegistry.PduTotal, server, "pdu_total", value)
			}
		}
	}

	logMetricCollection(server, "statistics", count)
}

// collectOperationsMetrics collects operation-related metrics
func (e *OpenLDAPExporter) collectOperationsMetrics(server string) {
	if !e.shouldCollectMetric("operations") {
		return
	}

	// Standard OpenLDAP operations as per monitoring documentation
	standardOperations := []string{
		"bind", "unbind", "add", "delete", "modrdn", "modify",
		"compare", "search", "abandon", "extended",
	}

	// Initialize all standard operations to 0 first
	// For counters, we need to track what operations exist to set absent ones to 0
	foundOperations := make(map[string]bool)

	result, err := e.searchLDAP(
		"cn=Operations,cn=Monitor",
		"(objectClass=*)",
		[]string{"monitorOpInitiated", "monitorOpCompleted"},
		"operations",
	)
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		operation := strings.ToLower(extractCNFromDN(entry.DN, ",cn=Operations,cn=Monitor"))
		foundOperations[operation] = true

		for _, attr := range entry.Attributes {
			switch attr.Name {
			case "monitorOpInitiated":
				if len(attr.Values) > 0 {
					if value, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
						key := "operations_initiated_" + operation
						e.updateOperationCounter(e.metricsRegistry.OperationsInitiated, server, operation, key, value)
					}
				}
			case "monitorOpCompleted":
				if len(attr.Values) > 0 {
					if value, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
						key := "operations_completed_" + operation
						e.updateOperationCounter(e.metricsRegistry.OperationsCompleted, server, operation, key, value)
					}
				}
			}
		}
	}

	// Initialize missing standard operations to 0
	for _, operation := range standardOperations {
		if !foundOperations[operation] {
			keyInitiated := "operations_initiated_" + operation
			keyCompleted := "operations_completed_" + operation
			// Use updateOperationCounter with 0 to properly initialize and create metrics
			e.updateOperationCounter(e.metricsRegistry.OperationsInitiated, server, operation, keyInitiated, 0)
			e.updateOperationCounter(e.metricsRegistry.OperationsCompleted, server, operation, keyCompleted, 0)
		}
	}
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

// collectOverlaysMetrics collects overlay information metrics
func (e *OpenLDAPExporter) collectOverlaysMetrics(server string) {
	if !e.shouldCollectMetric("overlays") {
		return
	}

	result, err := e.searchLDAP(
		"cn=Overlays,cn=Monitor",
		"(objectClass=*)",
		[]string{"cn"},
		"overlays",
	)

	if err == nil {
		for _, entry := range result.Entries {
			overlay := strings.TrimPrefix(entry.DN, "cn=")
			overlay = strings.TrimSuffix(overlay, ",cn=Overlays,cn=Monitor")
			e.metricsRegistry.OverlaysInfo.With(prometheus.Labels{"server": server, "overlay": overlay, "status": "active"}).Set(1)
		}
	}
}

// collectTLSMetrics collects TLS information metrics
func (e *OpenLDAPExporter) collectTLSMetrics(server string) {
	if !e.shouldCollectMetric("tls") {
		return
	}

	result, err := e.searchLDAP(
		"cn=TLS,cn=Monitor",
		"(objectClass=*)",
		[]string{"cn"},
		"tls",
	)

	if err == nil {
		for _, entry := range result.Entries {
			component := strings.TrimPrefix(entry.DN, "cn=")
			component = strings.TrimSuffix(component, ",cn=TLS,cn=Monitor")
			e.metricsRegistry.TlsInfo.With(prometheus.Labels{"server": server, "component": component, "status": "configured"}).Set(1)
		}
	}
}

// collectBackendsMetrics collects backend information metrics
func (e *OpenLDAPExporter) collectBackendsMetrics(server string) {
	if !e.shouldCollectMetric("backends") {
		return
	}

	result, err := e.searchLDAP(
		"cn=Backends,cn=Monitor",
		"(objectClass=*)",
		[]string{"cn", "monitoredInfo", "monitorRuntimeConfig", "supportedControl", "seeAlso"},
		"backends",
	)

	if err == nil {
		for _, entry := range result.Entries {
			backend := strings.TrimPrefix(entry.DN, "cn=")
			backend = strings.TrimSuffix(backend, ",cn=Backends,cn=Monitor")
			backendType := "unknown"
			runtimeConfig := "unknown"
			supportedControls := "unknown"
			seeAlso := "unknown"

			for _, attr := range entry.Attributes {
				switch attr.Name {
				case "monitoredInfo":
					if len(attr.Values) > 0 {
						backendType = attr.Values[0]
					}
				case "monitorRuntimeConfig":
					if len(attr.Values) > 0 {
						runtimeConfig = attr.Values[0]
					}
				case "supportedControl":
					if len(attr.Values) > 0 {
						// Join multiple controls with comma
						supportedControls = strings.Join(attr.Values, ",")
					}
				case "seeAlso":
					if len(attr.Values) > 0 {
						seeAlso = attr.Values[0]
					}
				}
			}

			e.metricsRegistry.BackendsInfo.With(prometheus.Labels{
				"server":  server,
				"backend": backend,
				"type":    backendType,
			}).Set(1)

			logger.SafeDebug("exporter", "Collected backend info", map[string]interface{}{
				"server":             server,
				"backend":            backend,
				"type":               backendType,
				"runtime_config":     runtimeConfig,
				"supported_controls": supportedControls,
				"see_also":           seeAlso,
			})
		}
	}
}

// collectListenersMetrics collects listener information metrics
func (e *OpenLDAPExporter) collectListenersMetrics(server string) {
	if !e.shouldCollectMetric("listeners") {
		return
	}

	result, err := e.client.Search(
		"cn=Listeners,cn=Monitor",
		"(objectClass=*)",
		[]string{"cn", "labeledURI"},
	)

	if err == nil {
		for _, entry := range result.Entries {
			listener := strings.TrimPrefix(entry.DN, "cn=Listener ")
			listener = strings.TrimSuffix(listener, ",cn=Listeners,cn=Monitor")
			address := "unknown"
			for _, attr := range entry.Attributes {
				if attr.Name == "labeledURI" && len(attr.Values) > 0 {
					address = attr.Values[0]
				}
			}
			e.metricsRegistry.ListenersInfo.With(prometheus.Labels{"server": server, "listener": listener, "address": address}).Set(1)
		}
	}
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

// collectDatabaseMetrics collects database-related metrics
func (e *OpenLDAPExporter) collectDatabaseMetrics(server string) {
	if !e.shouldCollectMetric("database") {
		return
	}

	// Search for database entries in cn=Databases,cn=Monitor
	result, err := e.client.Search(
		"cn=Databases,cn=Monitor",
		"(objectClass=*)",
		[]string{"namingContexts", "monitorCounter", "monitoredInfo", "monitorIsShadow", "monitorContext", "readOnly"},
	)

	if err != nil {
		logger.SafeError("exporter", "Failed to search database metrics", err)
		return
	}

	for _, entry := range result.Entries {
		// Skip the root "cn=Databases,cn=Monitor" entry
		if entry.DN == "cn=Databases,cn=Monitor" {
			continue
		}

		// Extract base DN and other database attributes
		var baseDN string
		var entryCount float64
		var isShadow, context, readOnly string

		for _, attr := range entry.Attributes {
			switch attr.Name {
			case "namingContexts":
				if len(attr.Values) > 0 {
					baseDN = attr.Values[0]
				}
			case "monitorCounter":
				if len(attr.Values) > 0 {
					if val, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
						entryCount = val
					}
				}
			case "monitoredInfo":
				// If namingContexts is not available, try to extract from monitoredInfo
				if baseDN == "" && len(attr.Values) > 0 {
					baseDN = attr.Values[0]
				}
			case "monitorIsShadow":
				if len(attr.Values) > 0 {
					isShadow = attr.Values[0]
				} else {
					isShadow = "FALSE"
				}
			case "monitorContext":
				if len(attr.Values) > 0 {
					context = attr.Values[0]
				} else {
					context = ""
				}
			case "readOnly":
				if len(attr.Values) > 0 {
					readOnly = attr.Values[0]
				} else {
					readOnly = "FALSE"
				}
			}
		}

		// If we found a baseDN and it matches our filter, record the metric
		if baseDN != "" {
			// Check if this baseDN should be included based on DC filters
			domainComponents := e.extractDomainComponents(baseDN)
			if e.shouldIncludeDomain(domainComponents) {
				// Record database entries count
				e.metricsRegistry.DatabaseEntries.With(prometheus.Labels{
					"server":           server,
					"base_dn":          baseDN,
					"domain_component": strings.Join(domainComponents, ","),
				}).Set(entryCount)

				// Record database information
				e.metricsRegistry.DatabaseInfo.With(prometheus.Labels{
					"server":    server,
					"base_dn":   baseDN,
					"is_shadow": isShadow,
					"context":   context,
					"readonly":  readOnly,
				}).Set(1)

				logger.SafeDebug("exporter", "Collected database metric", map[string]interface{}{
					"server":    server,
					"base_dn":   baseDN,
					"entries":   entryCount,
					"is_shadow": isShadow,
					"context":   context,
					"readonly":  readOnly,
					"entry_dn":  entry.DN,
				})
			} else {
				logger.SafeDebug("exporter", "Skipped database metric (filtered)", map[string]interface{}{
					"base_dn": baseDN,
					"reason":  "DC filter",
				})
			}
		}
	}
}

// collectServerInfoMetrics collects server information from root Monitor entry
func (e *OpenLDAPExporter) collectServerInfoMetrics(server string) {
	if !e.shouldCollectMetric("server") {
		return
	}

	result, err := e.client.Search(
		"cn=Monitor",
		"(objectClass=*)",
		[]string{"monitoredInfo", "description"},
	)

	if err != nil {
		logger.SafeError("exporter", "Failed to search server info", err)
		return
	}

	for _, entry := range result.Entries {
		if entry.DN == "cn=Monitor" {
			version := "unknown"
			description := "unknown"

			for _, attr := range entry.Attributes {
				switch attr.Name {
				case "monitoredInfo":
					if len(attr.Values) > 0 {
						version = attr.Values[0]
					}
				case "description":
					if len(attr.Values) > 0 {
						description = attr.Values[0]
					}
				}
			}

			e.metricsRegistry.ServerInfo.With(prometheus.Labels{
				"server":      server,
				"version":     version,
				"description": description,
			}).Set(1)
		}
	}
}

// collectLogMetrics collects log level information
func (e *OpenLDAPExporter) collectLogMetrics(server string) {
	if !e.shouldCollectMetric("log") {
		return
	}

	// Common log types based on OpenLDAP documentation
	logTypes := []string{
		"Trace", "Packets", "Args", "Conns", "BER", "Filter",
		"Config", "ACL", "Stats", "Stats2", "Shell", "Parse", "Sync",
	}

	result, err := e.client.Search(
		"cn=Log,cn=Monitor",
		"(objectClass=*)",
		[]string{"cn", "description"},
	)

	if err != nil {
		logger.SafeError("exporter", "Failed to search log info", err)
		return
	}

	// Set all log types to 0 initially
	for _, logType := range logTypes {
		e.metricsRegistry.LogLevels.With(prometheus.Labels{
			"server":   server,
			"log_type": logType,
		}).Set(0)
	}

	// Process found log entries
	for _, entry := range result.Entries {
		if entry.DN != "cn=Log,cn=Monitor" {
			logName := strings.TrimPrefix(entry.DN, "cn=")
			logName = strings.TrimSuffix(logName, ",cn=Log,cn=Monitor")

			e.metricsRegistry.LogLevels.With(prometheus.Labels{
				"server":   server,
				"log_type": logName,
			}).Set(1)
		}
	}
}

// collectSASLMetrics collects SASL mechanism information
func (e *OpenLDAPExporter) collectSASLMetrics(server string) {
	if !e.shouldCollectMetric("sasl") {
		return
	}

	result, err := e.client.Search(
		"cn=SASL,cn=Monitor",
		"(objectClass=*)",
		[]string{"cn", "description", "supportedSASLMechanisms"},
	)

	if err != nil {
		logger.SafeError("exporter", "Failed to search SASL info", err)
		return
	}

	for _, entry := range result.Entries {
		if entry.DN != "cn=SASL,cn=Monitor" {
			mechanism := strings.TrimPrefix(entry.DN, "cn=")
			mechanism = strings.TrimSuffix(mechanism, ",cn=SASL,cn=Monitor")

			e.metricsRegistry.SaslInfo.With(prometheus.Labels{
				"server":    server,
				"mechanism": mechanism,
				"status":    "configured",
			}).Set(1)
		}
	}
}
