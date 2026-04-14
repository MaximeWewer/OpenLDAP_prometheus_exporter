package exporter

import (
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/accesslog"
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

	// Reset the per-user ppolicy gauges at the start of every scrape so
	// users that disappeared from LDAP (account deleted, filter changed,
	// DC filter narrowed) stop showing up as flat lines in Grafana. The
	// set of active series is then fully rebuilt below from the entries
	// we actually observe on this scan.
	e.metricsRegistry.PpolicyPwdChangedTimestamp.Reset()
	e.metricsRegistry.PpolicyPwdLastSuccess.Reset()
	e.metricsRegistry.PpolicyPwdGraceUseCount.Reset()
	e.metricsRegistry.PpolicyPwdReset.Reset()

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

// ppolicyAttributes are the operational attributes we request from user entries.
// pwdFailureTime and pwdAccountLockedTime are intentionally excluded: they expose
// persistent state that skews time-series graphs (flat lines across scrapes).
// Failure and lock events are now derived from the accesslog collector instead.
var ppolicyAttributes = []string{
	"pwdChangedTime",
	"pwdLastSuccess",
	"pwdGraceUseTime",
	"pwdReset",
	"uid",
	"cn",
}

// collectPpolicyForSuffix searches a single naming context for users with ppolicy data
func (e *OpenLDAPExporter) collectPpolicyForSuffix(server, suffixDN string) int {
	// Search for entries that have at least one persisted ppolicy attribute set
	filter := "(|(pwdChangedTime=*)(pwdLastSuccess=*)(pwdGraceUseTime=*)(pwdReset=TRUE))"

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

// parseGeneralizedTime parses an LDAP GeneralizedTime string. It
// delegates to the shared accesslog helper so reqStart values with
// more than six fractional digits (slapd emits seven on some builds)
// are truncated rather than rejected, which used to drop ppolicy
// timestamps entirely.
func parseGeneralizedTime(value string) (time.Time, error) {
	return accesslog.ParseGeneralizedTime(value)
}
