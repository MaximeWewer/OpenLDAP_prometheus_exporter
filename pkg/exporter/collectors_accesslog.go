package exporter

import (
	"fmt"
	"strings"
	"sync"

	"github.com/go-ldap/ldap/v3"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// accesslogCursorState holds per-collector incremental scan cursors for a server.
// Each cursor is the last reqStart (GeneralizedTime) we processed for that stream.
// The format is lexicographically sortable, so string comparison is used for ordering.
// An empty cursor means the collector has not yet established a baseline — on the
// first scrape we record the current max reqStart without emitting counter increments
// to avoid back-filling historical events as a spike.
type accesslogCursorState struct {
	mu    sync.Mutex
	bind  string
	write string
	lock  string
}

// getAccesslogCursor returns the cursor state for the given server, creating it if needed.
func (e *OpenLDAPExporter) getAccesslogCursor(server string) *accesslogCursorState {
	e.accesslogMutex.Lock()
	defer e.accesslogMutex.Unlock()
	cur, ok := e.accesslogCursor[server]
	if !ok {
		cur = &accesslogCursorState{}
		e.accesslogCursor[server] = cur
	}
	return cur
}

// collectAccesslogMetrics collects event-based metrics from the accesslog database.
// It performs an incremental scan: on each call it queries only auditBind/auditModify
// entries with reqStart strictly greater than the last observed cursor. Matched entries
// are converted to counter increments so that PromQL rate()/increase() reflect the true
// moment of each event rather than a persistent state gauge.
func (e *OpenLDAPExporter) collectAccesslogMetrics(server string) {
	if !e.shouldCollectMetric("accesslog") {
		return
	}

	cursor := e.getAccesslogCursor(server)
	cursor.mu.Lock()
	defer cursor.mu.Unlock()

	count := 0

	if n, err := e.collectAccesslogBinds(server, cursor); err != nil {
		logger.SafeDebug("exporter", "Accesslog bind collection skipped", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		count += n
	}

	if n, err := e.collectAccesslogWrites(server, cursor); err != nil {
		logger.SafeDebug("exporter", "Accesslog write collection skipped", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		count += n
	}

	if n, err := e.collectAccesslogLockEvents(server, cursor); err != nil {
		logger.SafeDebug("exporter", "Accesslog lock event collection skipped", map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		count += n
	}

	logMetricCollection(server, "accesslog", count)
}

// buildIncrementalFilter wraps a base object-class filter with a reqStart lower bound
// when the cursor is set. The reqStart value is a GeneralizedTime which contains only
// digits, '.', and 'Z' — safe to embed directly in an LDAP filter.
func buildIncrementalFilter(base, cursor string) string {
	if cursor == "" {
		return base
	}
	return fmt.Sprintf("(&%s(reqStart>=%s))", base, cursor)
}

// collectAccesslogBinds queries bind operations newer than the cursor and increments counters.
func (e *OpenLDAPExporter) collectAccesslogBinds(server string, cursor *accesslogCursorState) (int, error) {
	filter := buildIncrementalFilter("(objectClass=auditBind)", cursor.bind)
	result, err := e.client.SearchAccessLog(filter, []string{"reqStart", "reqDN", "reqResult"})
	if err != nil {
		return 0, err
	}

	baseline := cursor.bind == ""
	maxSeen := cursor.bind
	count := 0

	for _, entry := range result.Entries {
		reqStart, userDN, resultCode := extractAccesslogBindInfo(entry)
		if reqStart == "" {
			continue
		}
		// reqStart>= is inclusive; skip the exact cursor value to avoid double counting
		if !baseline && reqStart == cursor.bind {
			if reqStart > maxSeen {
				maxSeen = reqStart
			}
			continue
		}
		if reqStart > maxSeen {
			maxSeen = reqStart
		}
		if baseline {
			continue
		}
		if userDN == "" {
			continue
		}
		userName := extractUserNameFromDN(userDN)
		e.metricsRegistry.AccesslogBindTotal.With(prometheus.Labels{
			"server":  server,
			"user_dn": userDN,
			"user":    userName,
			"result":  classifyBindResult(resultCode),
		}).Inc()
		count++
	}

	cursor.bind = maxSeen
	return count, nil
}

// collectAccesslogWrites queries write operations newer than the cursor and increments counters.
func (e *OpenLDAPExporter) collectAccesslogWrites(server string, cursor *accesslogCursorState) (int, error) {
	filter := buildIncrementalFilter(
		"(|(objectClass=auditAdd)(objectClass=auditModify)(objectClass=auditDelete)(objectClass=auditModRDN))",
		cursor.write,
	)
	result, err := e.client.SearchAccessLog(filter, []string{"reqStart", "reqType", "reqAuthzID"})
	if err != nil {
		return 0, err
	}

	baseline := cursor.write == ""
	maxSeen := cursor.write
	count := 0

	for _, entry := range result.Entries {
		reqStart, authzID, opType := extractAccesslogWriteInfo(entry)
		if reqStart == "" {
			continue
		}
		if !baseline && reqStart == cursor.write {
			if reqStart > maxSeen {
				maxSeen = reqStart
			}
			continue
		}
		if reqStart > maxSeen {
			maxSeen = reqStart
		}
		if baseline {
			continue
		}
		if authzID == "" || opType == "" {
			continue
		}
		userName := extractUserNameFromDN(authzID)
		e.metricsRegistry.AccesslogWriteTotal.With(prometheus.Labels{
			"server":    server,
			"user_dn":   authzID,
			"user":      userName,
			"operation": strings.ToLower(opType),
		}).Inc()
		count++
	}

	cursor.write = maxSeen
	return count, nil
}

// collectAccesslogLockEvents queries auditModify entries whose reqMod touches
// pwdAccountLockedTime and increments a per-user lock-event counter.
func (e *OpenLDAPExporter) collectAccesslogLockEvents(server string, cursor *accesslogCursorState) (int, error) {
	// reqMod values look like "pwdAccountLockedTime:+ 20250101120000Z"
	base := "(&(objectClass=auditModify)(reqMod=pwdAccountLockedTime:*))"
	filter := buildIncrementalFilter(base, cursor.lock)
	result, err := e.client.SearchAccessLog(filter, []string{"reqStart", "reqDN", "reqMod"})
	if err != nil {
		return 0, err
	}

	baseline := cursor.lock == ""
	maxSeen := cursor.lock
	count := 0

	for _, entry := range result.Entries {
		reqStart := getEntryValue(entry, "reqStart")
		if reqStart == "" {
			continue
		}
		if !baseline && reqStart == cursor.lock {
			if reqStart > maxSeen {
				maxSeen = reqStart
			}
			continue
		}
		if reqStart > maxSeen {
			maxSeen = reqStart
		}
		if baseline {
			continue
		}
		// Only count additions/replacements of pwdAccountLockedTime (lock events),
		// not deletions (unlocks) — a delete line reads "pwdAccountLockedTime:-".
		if !hasLockAddition(entry) {
			continue
		}
		userDN := getEntryValue(entry, "reqDN")
		if userDN == "" {
			continue
		}
		userName := extractUserNameFromDN(userDN)
		e.metricsRegistry.AccesslogLockEventsTotal.With(prometheus.Labels{
			"server":  server,
			"user_dn": userDN,
			"user":    userName,
		}).Inc()
		count++
	}

	cursor.lock = maxSeen
	return count, nil
}

// getEntryValue returns the first value of the named attribute, or "" if absent.
func getEntryValue(entry *ldap.Entry, name string) string {
	for _, attr := range entry.Attributes {
		if attr.Name == name && len(attr.Values) > 0 {
			return attr.Values[0]
		}
	}
	return ""
}

// hasLockAddition reports whether any reqMod value describes an add/replace of
// pwdAccountLockedTime (format: "pwdAccountLockedTime:+ ..." or ":="), which
// corresponds to the account being locked rather than unlocked.
func hasLockAddition(entry *ldap.Entry) bool {
	for _, attr := range entry.Attributes {
		if attr.Name != "reqMod" {
			continue
		}
		for _, v := range attr.Values {
			lower := strings.ToLower(v)
			if !strings.HasPrefix(lower, "pwdaccountlockedtime:") {
				continue
			}
			rest := lower[len("pwdaccountlockedtime:"):]
			if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "=") {
				return true
			}
		}
	}
	return false
}

// extractAccesslogBindInfo extracts reqStart, reqDN, reqResult from an auditBind entry
func extractAccesslogBindInfo(entry *ldap.Entry) (string, string, string) {
	var reqStart, userDN, resultCode string
	for _, attr := range entry.Attributes {
		if len(attr.Values) == 0 {
			continue
		}
		switch attr.Name {
		case "reqStart":
			reqStart = attr.Values[0]
		case "reqDN":
			userDN = attr.Values[0]
		case "reqResult":
			resultCode = attr.Values[0]
		}
	}
	return reqStart, userDN, resultCode
}

// extractAccesslogWriteInfo extracts reqStart, reqAuthzID, reqType from an audit write entry
func extractAccesslogWriteInfo(entry *ldap.Entry) (string, string, string) {
	var reqStart, authzID, opType string
	for _, attr := range entry.Attributes {
		if len(attr.Values) == 0 {
			continue
		}
		switch attr.Name {
		case "reqStart":
			reqStart = attr.Values[0]
		case "reqAuthzID":
			// Format: "dn:cn=admin,dc=example,dc=org"
			authzID = strings.TrimPrefix(attr.Values[0], "dn:")
		case "reqType":
			opType = attr.Values[0]
		}
	}
	return reqStart, authzID, opType
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
