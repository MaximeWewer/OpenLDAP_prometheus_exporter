package exporter

import (
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// collectPpolicyMetrics collects password policy metrics from user entries.
// It queries each naming context for users that have ppolicy operational attributes
// (pwdFailureTime, pwdAccountLockedTime, pwdChangedTime, pwdLastSuccess, etc.)
// and exposes per-user metrics for monitoring and alerting.
func (e *OpenLDAPExporter) collectPpolicyMetrics(server string) {
	if !e.shouldCollectMetric("ppolicy") {
		return
	}

	// Step 1: Get naming contexts from the monitor database entries
	result, err := e.searchLDAP(
		"cn=Databases,cn=Monitor",
		"(objectClass=*)",
		[]string{"namingContexts"},
		"ppolicy",
	)
	if err != nil {
		return
	}

	count := 0

	for _, entry := range result.Entries {
		if entry.DN == "cn=Databases,cn=Monitor" {
			continue
		}

		// Extract the naming context (suffix DN)
		var suffixDN string
		for _, attr := range entry.Attributes {
			if attr.Name == "namingContexts" && len(attr.Values) > 0 {
				suffixDN = attr.Values[0]
				break
			}
		}
		if suffixDN == "" {
			continue
		}

		// Skip non-data suffixes (cn=config, cn=accesslog, cn=monitor, etc.)
		suffixLower := strings.ToLower(suffixDN)
		if strings.HasPrefix(suffixLower, "cn=") {
			continue
		}

		// Apply domain component filtering
		domainComponents := e.extractDomainComponents(suffixDN)
		if !e.shouldIncludeDomain(domainComponents) {
			continue
		}

		// Step 2: Search for users with ppolicy attributes under this suffix
		n := e.collectPpolicyForSuffix(server, suffixDN)
		count += n
	}

	logMetricCollection(server, "ppolicy", count)
}

// ppolicyAttributes are the operational attributes we request from user entries
var ppolicyAttributes = []string{
	"pwdFailureTime",
	"pwdAccountLockedTime",
	"pwdChangedTime",
	"pwdLastSuccess",
	"pwdGraceUseTime",
	"pwdReset",
	"uid",
	"cn",
}

// collectPpolicyForSuffix searches a single naming context for users with ppolicy data
func (e *OpenLDAPExporter) collectPpolicyForSuffix(server, suffixDN string) int {
	// Search for entries that have at least one ppolicy operational attribute set
	filter := "(|(pwdFailureTime=*)(pwdAccountLockedTime=*)(pwdChangedTime=*)(pwdLastSuccess=*)(pwdGraceUseTime=*)(pwdReset=TRUE))"

	result, err := e.client.SearchSuffix(suffixDN, filter, ppolicyAttributes)
	if err != nil {
		logger.SafeDebug("exporter", "Could not query ppolicy attributes", map[string]interface{}{
			"base_dn": suffixDN,
			"error":   err.Error(),
		})
		return 0
	}

	count := 0
	for _, entry := range result.Entries {
		e.processPpolicyEntry(server, suffixDN, entry)
		count++
	}

	return count
}

// processPpolicyEntry processes a single user entry and records ppolicy metrics
func (e *OpenLDAPExporter) processPpolicyEntry(server, baseDN string, entry *ldap.Entry) {
	userDN := entry.DN
	userName := extractUserName(entry)

	labels := prometheus.Labels{
		"server":  server,
		"user_dn": userDN,
		"user":    userName,
		"base_dn": baseDN,
	}

	for _, attr := range entry.Attributes {
		if len(attr.Values) == 0 {
			continue
		}

		switch attr.Name {
		case "pwdFailureTime":
			// Multi-valued: each value is a GeneralizedTime of a failed attempt
			e.metricsRegistry.PpolicyPwdFailureCount.With(labels).Set(float64(len(attr.Values)))

		case "pwdAccountLockedTime":
			// If present, account is locked
			e.metricsRegistry.PpolicyAccountLocked.With(labels).Set(1)

		case "pwdChangedTime":
			if ts, err := parseGeneralizedTime(attr.Values[0]); err == nil {
				e.metricsRegistry.PpolicyPwdChangedTimestamp.With(labels).Set(float64(ts.Unix()))
			}

		case "pwdLastSuccess":
			if ts, err := parseGeneralizedTime(attr.Values[0]); err == nil {
				e.metricsRegistry.PpolicyPwdLastSuccess.With(labels).Set(float64(ts.Unix()))
			}

		case "pwdGraceUseTime":
			// Multi-valued: each value is a GeneralizedTime of a grace login
			e.metricsRegistry.PpolicyPwdGraceUseCount.With(labels).Set(float64(len(attr.Values)))

		case "pwdReset":
			if strings.EqualFold(attr.Values[0], "TRUE") {
				e.metricsRegistry.PpolicyPwdReset.With(labels).Set(1)
			} else {
				e.metricsRegistry.PpolicyPwdReset.With(labels).Set(0)
			}
		}
	}

	// Set default for account locked if not present (entry matched filter but may not have this attr)
	hasLockedAttr := false
	for _, attr := range entry.Attributes {
		if attr.Name == "pwdAccountLockedTime" {
			hasLockedAttr = true
			break
		}
	}
	if !hasLockedAttr {
		e.metricsRegistry.PpolicyAccountLocked.With(labels).Set(0)
	}

	logger.SafeDebug("exporter", "Collected ppolicy metric", map[string]interface{}{
		"server":  server,
		"user_dn": userDN,
		"user":    userName,
		"base_dn": baseDN,
	})
}

// extractUserName extracts a human-readable name from an LDAP entry.
// It prefers uid, then cn, then falls back to the first RDN component.
func extractUserName(entry *ldap.Entry) string {
	for _, attr := range entry.Attributes {
		if attr.Name == "uid" && len(attr.Values) > 0 {
			return attr.Values[0]
		}
	}
	for _, attr := range entry.Attributes {
		if attr.Name == "cn" && len(attr.Values) > 0 {
			return attr.Values[0]
		}
	}

	// Fall back to extracting from DN
	dn := entry.DN
	parts := strings.SplitN(dn, ",", 2)
	if len(parts) > 0 {
		kv := strings.SplitN(parts[0], "=", 2)
		if len(kv) == 2 {
			return kv[1]
		}
	}
	return dn
}

// parseGeneralizedTime parses an LDAP GeneralizedTime string.
// Format: YYYYMMDDHHmmssZ or YYYYMMDDHHmmss.fracZ
func parseGeneralizedTime(value string) (time.Time, error) {
	// Try common GeneralizedTime formats
	formats := []string{
		"20060102150405Z",
		"20060102150405.0Z",
		"20060102150405.00Z",
		"20060102150405.000Z",
		"20060102150405.0000Z",
		"20060102150405.00000Z",
		"20060102150405.000000Z",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, value); err == nil {
			return t, nil
		}
	}

	// Try without timezone suffix and assume UTC
	formats = []string{
		"20060102150405",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, value); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, &time.ParseError{
		Layout:     "20060102150405Z",
		Value:      value,
		LayoutElem: "",
		ValueElem:  "",
		Message:    "unrecognized GeneralizedTime format",
	}
}
