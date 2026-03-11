package exporter

import (
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// collectAccesslogMetrics collects metrics from the accesslog database (cn=accesslog).
// The accesslog overlay records bind and write operations with their results.
// This collector aggregates counts per user DN and result code, providing a
// sliding window view (controlled by olcAccessLogPurge on the server side).
func (e *OpenLDAPExporter) collectAccesslogMetrics(server string) {
	if !e.shouldCollectMetric("accesslog") {
		return
	}

	count := 0

	// Collect bind operation metrics
	n, err := e.collectAccesslogBinds(server)
	if err != nil {
		logger.SafeDebug("exporter", "Accesslog bind collection skipped", map[string]interface{}{
			"error": err.Error(),
		})
	}
	count += n

	// Collect write operation metrics
	n, err = e.collectAccesslogWrites(server)
	if err != nil {
		logger.SafeDebug("exporter", "Accesslog write collection skipped", map[string]interface{}{
			"error": err.Error(),
		})
	}
	count += n

	logMetricCollection(server, "accesslog", count)
}

// collectAccesslogBinds queries bind operations from the accesslog and aggregates per user/result
func (e *OpenLDAPExporter) collectAccesslogBinds(server string) (int, error) {
	result, err := e.client.SearchAccessLog(
		"(objectClass=auditBind)",
		[]string{"reqDN", "reqResult"},
	)
	if err != nil {
		return 0, err
	}

	// Aggregate: map[userDN]map[resultLabel]count
	type bindKey struct {
		userDN string
		result string
	}
	counts := make(map[bindKey]float64)

	for _, entry := range result.Entries {
		userDN, resultCode := extractAccesslogBindInfo(entry)
		if userDN == "" {
			continue
		}

		// Classify result: 0 = success, anything else = failure with code
		resultLabel := classifyBindResult(resultCode)

		counts[bindKey{userDN: userDN, result: resultLabel}]++
	}

	// Record metrics
	count := 0
	for key, val := range counts {
		userName := extractUserNameFromDN(key.userDN)
		e.metricsRegistry.AccesslogBindTotal.With(prometheus.Labels{
			"server":  server,
			"user_dn": key.userDN,
			"user":    userName,
			"result":  key.result,
		}).Set(val)
		count++
	}

	return count, nil
}

// collectAccesslogWrites queries write operations from the accesslog and aggregates per user/type
func (e *OpenLDAPExporter) collectAccesslogWrites(server string) (int, error) {
	result, err := e.client.SearchAccessLog(
		"(|(objectClass=auditAdd)(objectClass=auditModify)(objectClass=auditDelete)(objectClass=auditModRDN))",
		[]string{"reqDN", "reqType", "reqAuthzID"},
	)
	if err != nil {
		return 0, err
	}

	// Aggregate: map[userDN]map[opType]count
	type writeKey struct {
		userDN    string
		operation string
	}
	counts := make(map[writeKey]float64)

	for _, entry := range result.Entries {
		authzID, opType := extractAccesslogWriteInfo(entry)
		if authzID == "" || opType == "" {
			continue
		}

		counts[writeKey{userDN: authzID, operation: strings.ToLower(opType)}]++
	}

	// Record metrics
	count := 0
	for key, val := range counts {
		userName := extractUserNameFromDN(key.userDN)
		e.metricsRegistry.AccesslogWriteTotal.With(prometheus.Labels{
			"server":    server,
			"user_dn":   key.userDN,
			"user":      userName,
			"operation": key.operation,
		}).Set(val)
		count++
	}

	return count, nil
}

// extractAccesslogBindInfo extracts reqDN and reqResult from an auditBind entry
func extractAccesslogBindInfo(entry *ldap.Entry) (string, string) {
	var userDN, resultCode string
	for _, attr := range entry.Attributes {
		if len(attr.Values) == 0 {
			continue
		}
		switch attr.Name {
		case "reqDN":
			userDN = attr.Values[0]
		case "reqResult":
			resultCode = attr.Values[0]
		}
	}
	return userDN, resultCode
}

// extractAccesslogWriteInfo extracts reqAuthzID and reqType from an audit write entry
func extractAccesslogWriteInfo(entry *ldap.Entry) (string, string) {
	var authzID, opType string
	for _, attr := range entry.Attributes {
		if len(attr.Values) == 0 {
			continue
		}
		switch attr.Name {
		case "reqAuthzID":
			// Format: "dn:cn=admin,dc=example,dc=org"
			authzID = strings.TrimPrefix(attr.Values[0], "dn:")
		case "reqType":
			opType = attr.Values[0]
		}
	}
	return authzID, opType
}

// classifyBindResult converts an LDAP result code into a human-readable label
func classifyBindResult(resultCode string) string {
	switch resultCode {
	case "0":
		return "success"
	case "49":
		return "invalid_credentials"
	case "50":
		return "insufficient_access"
	case "53":
		return "unwilling_to_perform"
	case "19":
		return "constraint_violation"
	default:
		if resultCode == "" {
			return "unknown"
		}
		return "error_" + resultCode
	}
}

// extractUserNameFromDN extracts a human-readable name from a DN string.
// It takes the first RDN value (e.g., "uid=john,ou=users,dc=example,dc=org" -> "john")
func extractUserNameFromDN(dn string) string {
	if dn == "" {
		return ""
	}
	parts := strings.SplitN(dn, ",", 2)
	if len(parts) > 0 {
		kv := strings.SplitN(parts[0], "=", 2)
		if len(kv) == 2 {
			return kv[1]
		}
	}
	return dn
}
