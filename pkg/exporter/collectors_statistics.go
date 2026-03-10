package exporter

import (
	"strconv"
	"strings"
)

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
}
