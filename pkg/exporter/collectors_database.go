package exporter

import (
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// collectDatabaseMetrics collects database-related metrics
func (e *OpenLDAPExporter) collectDatabaseMetrics(server string) {
	if !e.shouldCollectMetric("database") {
		return
	}

	result, err := e.searchDatabaseEntries()
	if err != nil {
		logger.SafeError("exporter", "Failed to search database metrics", err)
		return
	}

	for _, entry := range result.Entries {
		if entry.DN == "cn=Databases,cn=Monitor" {
			continue // Skip root entry
		}
		// Skip overlay entries - they don't have their own entry counts
		if strings.Contains(entry.DN, "cn=Overlay") {
			continue
		}
		e.processDatabaseEntry(server, entry)
	}
}

// searchDatabaseEntries searches for database entries in monitor
func (e *OpenLDAPExporter) searchDatabaseEntries() (*ldap.SearchResult, error) {
	return e.client.Search(
		"cn=Databases,cn=Monitor",
		"(objectClass=*)",
		[]string{"namingContexts", "monitorCounter", "monitoredInfo", "monitorIsShadow", "monitorContext", "readOnly", "olmMDBEntries"},
	)
}

// processDatabaseEntry processes a single database entry
func (e *OpenLDAPExporter) processDatabaseEntry(server string, entry *ldap.Entry) {
	dbInfo := e.extractDatabaseInfo(entry)

	if dbInfo.baseDN == "" {
		return
	}

	domainComponents := e.extractDomainComponents(dbInfo.baseDN)
	if !e.shouldIncludeDomain(domainComponents) {
		logger.SafeDebug("exporter", "Skipped database metric (filtered)", map[string]interface{}{
			"base_dn": dbInfo.baseDN,
			"reason":  "DC filter",
		})
		return
	}

	e.recordDatabaseMetrics(server, dbInfo, domainComponents)
}

// databaseInfo holds extracted database information
type databaseInfo struct {
	baseDN     string
	entryCount float64
	isShadow   string
	context    string
	readOnly   string
	entryDN    string
}

// extractDatabaseInfo extracts database information from LDAP entry
func (e *OpenLDAPExporter) extractDatabaseInfo(entry *ldap.Entry) *databaseInfo {
	info := &databaseInfo{
		isShadow: "FALSE",
		context:  "",
		readOnly: "FALSE",
		entryDN:  entry.DN,
	}

	for _, attr := range entry.Attributes {
		e.processDatabaseAttribute(attr, info)
	}

	return info
}

// processDatabaseAttribute processes a single database attribute
func (e *OpenLDAPExporter) processDatabaseAttribute(attr *ldap.EntryAttribute, info *databaseInfo) {
	if len(attr.Values) == 0 {
		return
	}

	switch attr.Name {
	case "namingContexts":
		info.baseDN = attr.Values[0]
	case "monitorCounter", "olmMDBEntries":
		if val, err := strconv.ParseFloat(attr.Values[0], 64); err == nil {
			info.entryCount = val
		}
	case "monitoredInfo":
		// Fallback if namingContexts is not available
		if info.baseDN == "" {
			info.baseDN = attr.Values[0]
		}
	case "monitorIsShadow":
		info.isShadow = attr.Values[0]
	case "monitorContext":
		info.context = attr.Values[0]
	case "readOnly":
		info.readOnly = attr.Values[0]
	}
}

// recordDatabaseMetrics records database metrics to Prometheus
func (e *OpenLDAPExporter) recordDatabaseMetrics(server string, info *databaseInfo, domainComponents []string) {
	// Record database entries count
	e.metricsRegistry.DatabaseEntries.With(prometheus.Labels{
		"server":           server,
		"base_dn":          info.baseDN,
		"domain_component": strings.Join(domainComponents, ","),
	}).Set(info.entryCount)

	// Record database information
	e.metricsRegistry.DatabaseInfo.With(prometheus.Labels{
		"server":    server,
		"base_dn":   info.baseDN,
		"is_shadow": info.isShadow,
		"context":   info.context,
		"readonly":  info.readOnly,
	}).Set(1)

	logger.SafeDebug("exporter", "Collected database metric", map[string]interface{}{
		"server":    server,
		"base_dn":   info.baseDN,
		"entries":   info.entryCount,
		"is_shadow": info.isShadow,
		"context":   info.context,
		"readonly":  info.readOnly,
		"entry_dn":  info.entryDN,
	})
}
