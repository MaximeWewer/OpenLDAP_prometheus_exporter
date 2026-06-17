package exporter

import (
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/accesslog"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// accesslogCursorState wraps the shared accesslog.CursorState with a
// mutex so a single scrape does not overlap with itself while walking
// the same server's streams. Both the metric collector here and the
// JSON events runner in pkg/events use the identical underlying cursor
// type, so the baseline semantics (per-stream Ready flag that survives
// an empty first scan) stay consistent across the two consumers.
type accesslogCursorState struct {
	mu sync.Mutex
	accesslog.CursorState
}

// getAccesslogCursor returns the cursor state for the given server, creating it if needed.
func (e *OpenLDAPExporter) getAccesslogCursor(server string) *accesslogCursorState {
	e.accesslogMutex.Lock()
	defer e.accesslogMutex.Unlock()
	cur, ok := e.accesslogCursor[server]
	if !ok {
		// Seed the cursor to "now" so the first scan is bounded to new entries
		// instead of matching the entire accesslog history (see
		// accesslog.NewCursorState — an empty cursor pulls every entry into one
		// SearchResult, which OOMs the exporter and hammers slapd).
		cur = &accesslogCursorState{CursorState: *accesslog.NewCursorState()}
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

// collectAccesslogBinds queries bind operations newer than the cursor and increments counters.
func (e *OpenLDAPExporter) collectAccesslogBinds(server string, cursor *accesslogCursorState) (int, error) {
	cur, ready := cursor.Get(accesslog.StreamBind)
	filter := accesslog.BuildIncrementalFilter(accesslog.BindBaseFilter, cur)
	result, err := e.client.SearchAccessLog(filter, []string{"reqStart", "reqDN", "reqResult"})
	if err != nil {
		return 0, err
	}

	baseline := !ready
	maxSeen := cur
	count := 0

	for _, entry := range result.Entries {
		reqStart := accesslog.Attr(entry, "reqStart")
		emit, next := accesslog.ShouldEmit(reqStart, cur, baseline, maxSeen)
		maxSeen = next
		if !emit {
			continue
		}

		userDN := accesslog.Attr(entry, "reqDN")
		if userDN == "" {
			continue
		}
		userName := accesslog.ExtractUserNameFromDN(userDN)
		if userName == "" {
			continue
		}
		e.accesslogBindTracker.Inc(prometheus.Labels{
			"server": server,
			"user":   userName,
			"result": accesslog.ClassifyBindResult(accesslog.Attr(entry, "reqResult")),
		})
		count++
	}

	cursor.Advance(accesslog.StreamBind, maxSeen)
	return count, nil
}

// collectAccesslogWrites queries write operations newer than the cursor and increments counters.
func (e *OpenLDAPExporter) collectAccesslogWrites(server string, cursor *accesslogCursorState) (int, error) {
	cur, ready := cursor.Get(accesslog.StreamWrite)
	filter := accesslog.BuildIncrementalFilter(accesslog.WriteBaseFilter, cur)
	result, err := e.client.SearchAccessLog(filter, []string{"reqStart", "reqType", "reqAuthzID"})
	if err != nil {
		return 0, err
	}

	baseline := !ready
	maxSeen := cur
	count := 0

	for _, entry := range result.Entries {
		reqStart := accesslog.Attr(entry, "reqStart")
		emit, next := accesslog.ShouldEmit(reqStart, cur, baseline, maxSeen)
		maxSeen = next
		if !emit {
			continue
		}

		opType := accesslog.Attr(entry, "reqType")
		authzID := accesslog.TrimDNPrefix(accesslog.Attr(entry, "reqAuthzID"))
		if authzID == "" || opType == "" {
			continue
		}
		userName := accesslog.ExtractUserNameFromDN(authzID)
		if userName == "" {
			continue
		}
		e.accesslogWriteTracker.Inc(prometheus.Labels{
			"server":    server,
			"user":      userName,
			"operation": strings.ToLower(opType),
		})
		count++
	}

	cursor.Advance(accesslog.StreamWrite, maxSeen)
	return count, nil
}

// collectAccesslogLockEvents queries auditModify entries whose reqMod touches
// pwdAccountLockedTime and increments a per-user lock-event counter.
func (e *OpenLDAPExporter) collectAccesslogLockEvents(server string, cursor *accesslogCursorState) (int, error) {
	cur, ready := cursor.Get(accesslog.StreamLock)
	filter := accesslog.BuildIncrementalFilter(accesslog.LockBaseFilter, cur)
	result, err := e.client.SearchAccessLog(filter, []string{"reqStart", "reqDN", "reqMod"})
	if err != nil {
		return 0, err
	}

	baseline := !ready
	maxSeen := cur
	count := 0

	for _, entry := range result.Entries {
		reqStart := accesslog.Attr(entry, "reqStart")
		emit, next := accesslog.ShouldEmit(reqStart, cur, baseline, maxSeen)
		maxSeen = next
		if !emit {
			continue
		}

		// Only count additions/replacements of pwdAccountLockedTime
		// (lock events), not deletions (unlocks) — a delete line reads
		// "pwdAccountLockedTime:-".
		if !accesslog.HasReqModAddition(entry, "pwdAccountLockedTime") {
			continue
		}
		userDN := accesslog.Attr(entry, "reqDN")
		if userDN == "" {
			continue
		}
		userName := accesslog.ExtractUserNameFromDN(userDN)
		if userName == "" {
			continue
		}
		e.accesslogLockTracker.Inc(prometheus.Labels{
			"server": server,
			"user":   userName,
		})
		count++
	}

	cursor.Advance(accesslog.StreamLock, maxSeen)
	return count, nil
}
