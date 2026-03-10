package exporter

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

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
