package exporter

import (
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// This file contains shared utilities used by the collector files:
//   collectors_resource.go     - connections, threads, time, waiters, health
//   collectors_statistics.go   - statistics, operations
//   collectors_info.go         - overlays, TLS, backends, listeners, server, log, SASL
//   collectors_database.go     - database metrics
//   collectors_replication.go  - replication CSN and lag
//   collectors_ppolicy.go      - password policy per-user metrics
//   collectors_accesslog.go    - accesslog bind and write operation metrics

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
