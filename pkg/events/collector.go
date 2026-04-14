package events

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/accesslog"
)

// parseEventTimestamp parses a GeneralizedTime reqStart value and
// truncates it to second precision so the marshaled "ts" field reads
// `2026-04-14T07:32:45Z` instead of `2026-04-14T07:32:45.000001Z`. The
// sub-second part is only an accesslog tie-breaker (slapd bumps it to
// keep reqStart unique when two operations land in the same second);
// the full-precision string stays available via the `req_start` field
// for anyone who needs to correlate back to the raw LDAP entry.
func parseEventTimestamp(reqStart string) time.Time {
	return accesslog.ParseReqStart(reqStart).Truncate(time.Second)
}

// accesslogSearcher is the narrow interface the event collector needs
// from the LDAP client. The production implementation is
// *pool.PooledLDAPClient, but tests supply a fake so the mapping logic
// can be exercised in isolation.
type accesslogSearcher interface {
	SearchAccessLog(filter string, attributes []string) (*ldap.SearchResult, error)
}

// cursorState is a thin alias over the shared cursor so existing test
// helpers can keep the short package-local name.
type cursorState = accesslog.CursorState

// collector turns accesslog entries into Event values. It is deliberately
// stateful only through the cursor it is given — the runner owns both the
// cursor and the emitter and passes them in on each tick.
type collector struct {
	client accesslogSearcher
	server string
}

func newCollector(client accesslogSearcher, server string) *collector {
	return &collector{client: client, server: server}
}

// scan performs a single tick: it queries each audit stream with its
// current cursor, hands the decoded events to emit, and advances the
// cursor to the highest reqStart seen. Baseline semantics mirror the
// metric collector via the shared accesslog.CursorState Ready flag: the
// first scan for a given stream only establishes a starting point, it
// does not back-fill historical events.
func (c *collector) scan(cursor *cursorState, emit func(Event)) (int, error) {
	total := 0

	n, err := c.scanBinds(cursor, emit)
	total += n
	if err != nil {
		return total, fmt.Errorf("bind stream: %w", err)
	}

	n, err = c.scanWrites(cursor, emit)
	total += n
	if err != nil {
		return total, fmt.Errorf("write stream: %w", err)
	}

	n, err = c.scanLocks(cursor, emit)
	total += n
	if err != nil {
		return total, fmt.Errorf("lock stream: %w", err)
	}

	return total, nil
}

func (c *collector) scanBinds(cursor *cursorState, emit func(Event)) (int, error) {
	cur, ready := cursor.Get(accesslog.StreamBind)
	filter := accesslog.BuildIncrementalFilter(accesslog.BindBaseFilter, cur)
	result, err := c.client.SearchAccessLog(filter, []string{
		"reqStart", "reqDN", "reqResult", "reqSession", "reqAuthzID",
	})
	if err != nil {
		return 0, err
	}

	baseline := !ready
	maxSeen := cur
	count := 0

	for _, entry := range result.Entries {
		reqStart := accesslog.Attr(entry, "reqStart")
		emitNow, next := accesslog.ShouldEmit(reqStart, cur, baseline, maxSeen)
		maxSeen = next
		if !emitNow {
			continue
		}

		resultCode := accesslog.Attr(entry, "reqResult")
		eventType := TypeBindSuccess
		if resultCode != "0" {
			eventType = TypeBindFailure
		}

		emit(Event{
			Timestamp:  parseEventTimestamp(reqStart),
			Event:      eventType,
			Server:     c.server,
			Source:     "accesslog",
			ReqStart:   reqStart,
			ReqSession: accesslog.Attr(entry, "reqSession"),
			Actor:      accesslog.TrimDNPrefix(accesslog.Attr(entry, "reqAuthzID")),
			UserDN:     accesslog.Attr(entry, "reqDN"),
			Result:     accesslog.ClassifyBindResult(resultCode),
			ResultCode: resultCode,
		})
		count++
	}

	cursor.Advance(accesslog.StreamBind, maxSeen)
	return count, nil
}

func (c *collector) scanWrites(cursor *cursorState, emit func(Event)) (int, error) {
	cur, ready := cursor.Get(accesslog.StreamWrite)
	filter := accesslog.BuildIncrementalFilter(accesslog.WriteBaseFilter, cur)
	result, err := c.client.SearchAccessLog(filter, []string{
		"reqStart", "reqType", "reqDN", "reqAuthzID", "reqSession", "reqMod", "reqResult",
	})
	if err != nil {
		return 0, err
	}

	baseline := !ready
	maxSeen := cur
	count := 0

	for _, entry := range result.Entries {
		reqStart := accesslog.Attr(entry, "reqStart")
		emitNow, next := accesslog.ShouldEmit(reqStart, cur, baseline, maxSeen)
		maxSeen = next
		if !emitNow {
			continue
		}

		// Skip modifications that the server rejected — accesslog is
		// configured with olcAccessLogSuccess=FALSE so failures are
		// recorded too. Emitting them would produce phantom events for
		// operations that never took effect (e.g. ACL denials).
		if rc := accesslog.Attr(entry, "reqResult"); rc != "" && rc != "0" {
			continue
		}

		opType := accesslog.Attr(entry, "reqType")
		targetDN := accesslog.Attr(entry, "reqDN")
		actor := accesslog.TrimDNPrefix(accesslog.Attr(entry, "reqAuthzID"))
		attrs := accesslog.TouchedAttributes(entry)

		eventType := mapWriteOp(opType)
		if eventType == "" {
			// Unknown subtype — skip rather than emit a noisy "write.unknown".
			continue
		}

		ts := parseEventTimestamp(reqStart)

		emit(Event{
			Timestamp:  ts,
			Event:      eventType,
			Server:     c.server,
			Source:     "accesslog",
			ReqStart:   reqStart,
			ReqSession: accesslog.Attr(entry, "reqSession"),
			Actor:      actor,
			TargetDN:   targetDN,
			Operation:  strings.ToLower(opType),
			Attributes: attrs,
		})
		count++

		// Derived ppolicy events: a single auditModify can also indicate
		// a password change or a "must reset" marker. Emit them in
		// addition to the write.* event so downstream consumers can
		// filter precisely.
		if eventType == TypeWriteModify {
			if containsAttr(attrs, "userPassword") || containsAttr(attrs, "pwdChangedTime") {
				emit(Event{
					Timestamp:  ts,
					Event:      TypePasswordChange,
					Server:     c.server,
					Source:     "accesslog",
					ReqStart:   reqStart,
					ReqSession: accesslog.Attr(entry, "reqSession"),
					Actor:      actor,
					UserDN:     targetDN,
				})
				count++
			}
			if accesslog.HasReqModAddition(entry, "pwdReset") {
				emit(Event{
					Timestamp:  ts,
					Event:      TypePasswordResetRequire,
					Server:     c.server,
					Source:     "accesslog",
					ReqStart:   reqStart,
					ReqSession: accesslog.Attr(entry, "reqSession"),
					Actor:      actor,
					UserDN:     targetDN,
				})
				count++
			}
		}
	}

	cursor.Advance(accesslog.StreamWrite, maxSeen)
	return count, nil
}

func (c *collector) scanLocks(cursor *cursorState, emit func(Event)) (int, error) {
	cur, ready := cursor.Get(accesslog.StreamLock)
	filter := accesslog.BuildIncrementalFilter(accesslog.LockBaseFilter, cur)
	result, err := c.client.SearchAccessLog(filter, []string{
		"reqStart", "reqDN", "reqAuthzID", "reqSession", "reqMod", "reqResult",
	})
	if err != nil {
		return 0, err
	}

	baseline := !ready
	maxSeen := cur
	count := 0

	for _, entry := range result.Entries {
		reqStart := accesslog.Attr(entry, "reqStart")
		emitNow, next := accesslog.ShouldEmit(reqStart, cur, baseline, maxSeen)
		maxSeen = next
		if !emitNow {
			continue
		}

		// Same rationale as scanWrites: ignore rejected modifications
		// so denied unlocks/locks do not appear as successful events.
		if rc := accesslog.Attr(entry, "reqResult"); rc != "" && rc != "0" {
			continue
		}

		targetDN := accesslog.Attr(entry, "reqDN")
		actor := accesslog.TrimDNPrefix(accesslog.Attr(entry, "reqAuthzID"))
		ts := parseEventTimestamp(reqStart)

		eventType := TypeAccountUnlock
		if accesslog.HasReqModAddition(entry, "pwdAccountLockedTime") {
			eventType = TypeAccountLock
		}

		emit(Event{
			Timestamp:  ts,
			Event:      eventType,
			Server:     c.server,
			Source:     "accesslog",
			ReqStart:   reqStart,
			ReqSession: accesslog.Attr(entry, "reqSession"),
			Actor:      actor,
			UserDN:     targetDN,
		})
		count++
	}

	cursor.Advance(accesslog.StreamLock, maxSeen)
	return count, nil
}

func mapWriteOp(opType string) Type {
	switch strings.ToLower(opType) {
	case "add":
		return TypeWriteAdd
	case "modify":
		return TypeWriteModify
	case "delete":
		return TypeWriteDelete
	case "modrdn":
		return TypeWriteModRDN
	default:
		return ""
	}
}

func containsAttr(list []string, name string) bool {
	for _, v := range list {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}
