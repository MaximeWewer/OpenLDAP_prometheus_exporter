package events

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// accesslogSearcher is the narrow interface the event collector needs from the
// LDAP client. The production implementation is *pool.PooledLDAPClient, but
// tests supply a fake so the mapping logic can be exercised in isolation.
type accesslogSearcher interface {
	SearchAccessLog(filter string, attributes []string) (*ldap.SearchResult, error)
}

// cursorState holds the per-stream reqStart bookmarks the event runner uses to
// query only newer entries on each tick. Each stream (bind / write / lock) has
// its own cursor so that an error on one does not freeze the others.
//
// The "ready" flag exists so that a stream which is empty on the first scan
// still transitions out of baseline mode — otherwise the very first real event
// on an idle stream would be silently dropped because cursor=="" would still
// look like "not yet initialised".
type cursorState struct {
	bind       string
	write      string
	lock       string
	bindReady  bool
	writeReady bool
	lockReady  bool
}

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

// scan performs a single tick: it queries each audit stream with its current
// cursor, hands the decoded events to emit, and advances the cursor to the
// highest reqStart seen. Baseline semantics mirror the metric collector: the
// first scan for a given stream (cursor == "") only establishes a starting
// point, it does not back-fill historical events.
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

func buildIncrementalFilter(base, cursor string) string {
	if cursor == "" {
		return base
	}
	return fmt.Sprintf("(&%s(reqStart>=%s))", base, cursor)
}

func (c *collector) scanBinds(cursor *cursorState, emit func(Event)) (int, error) {
	filter := buildIncrementalFilter("(objectClass=auditBind)", cursor.bind)
	result, err := c.client.SearchAccessLog(filter, []string{
		"reqStart", "reqDN", "reqResult", "reqSession", "reqAuthzID",
	})
	if err != nil {
		return 0, err
	}

	baseline := !cursor.bindReady
	maxSeen := cursor.bind
	count := 0

	for _, entry := range result.Entries {
		reqStart := attr(entry, "reqStart")
		if reqStart == "" {
			continue
		}
		if !baseline && reqStart == cursor.bind {
			maxSeen = maxString(maxSeen, reqStart)
			continue
		}
		maxSeen = maxString(maxSeen, reqStart)
		if baseline {
			continue
		}

		userDN := attr(entry, "reqDN")
		resultCode := attr(entry, "reqResult")
		eventType := TypeBindSuccess
		if resultCode != "0" {
			eventType = TypeBindFailure
		}

		emit(Event{
			Timestamp:  parseReqStart(reqStart),
			Event:      eventType,
			Server:     c.server,
			Source:     "accesslog",
			ReqStart:   reqStart,
			ReqSession: attr(entry, "reqSession"),
			Actor:      trimDNPrefix(attr(entry, "reqAuthzID")),
			UserDN:     userDN,
			Result:     classifyBindResult(resultCode),
			ResultCode: resultCode,
		})
		count++
	}

	cursor.bind = maxSeen
	cursor.bindReady = true
	return count, nil
}

func (c *collector) scanWrites(cursor *cursorState, emit func(Event)) (int, error) {
	base := "(|(objectClass=auditAdd)(objectClass=auditModify)(objectClass=auditDelete)(objectClass=auditModRDN))"
	filter := buildIncrementalFilter(base, cursor.write)
	result, err := c.client.SearchAccessLog(filter, []string{
		"reqStart", "reqType", "reqDN", "reqAuthzID", "reqSession", "reqMod", "reqResult",
	})
	if err != nil {
		return 0, err
	}

	baseline := !cursor.writeReady
	maxSeen := cursor.write
	count := 0

	for _, entry := range result.Entries {
		reqStart := attr(entry, "reqStart")
		if reqStart == "" {
			continue
		}
		if !baseline && reqStart == cursor.write {
			maxSeen = maxString(maxSeen, reqStart)
			continue
		}
		maxSeen = maxString(maxSeen, reqStart)
		if baseline {
			continue
		}

		// Skip modifications that the server rejected — accesslog is
		// configured with olcAccessLogSuccess=FALSE so failures are recorded
		// too. Emitting them would produce phantom events for operations
		// that never took effect (e.g. ACL denials).
		if rc := attr(entry, "reqResult"); rc != "" && rc != "0" {
			continue
		}

		opType := attr(entry, "reqType")
		targetDN := attr(entry, "reqDN")
		actor := trimDNPrefix(attr(entry, "reqAuthzID"))
		attrs := touchedAttributes(entry)

		eventType := mapWriteOp(opType)
		if eventType == "" {
			// Unknown subtype — skip rather than emit a noisy "write.unknown".
			continue
		}

		ts := parseReqStart(reqStart)

		emit(Event{
			Timestamp:  ts,
			Event:      eventType,
			Server:     c.server,
			Source:     "accesslog",
			ReqStart:   reqStart,
			ReqSession: attr(entry, "reqSession"),
			Actor:      actor,
			TargetDN:   targetDN,
			Operation:  strings.ToLower(opType),
			Attributes: attrs,
		})
		count++

		// Derived ppolicy events: a single auditModify can also indicate a
		// password change or a "must reset" marker. We emit them in addition
		// to the write.* event so downstream consumers can filter precisely.
		if eventType == TypeWriteModify {
			if containsAttr(attrs, "userPassword") || containsAttr(attrs, "pwdChangedTime") {
				emit(Event{
					Timestamp:  ts,
					Event:      TypePasswordChange,
					Server:     c.server,
					Source:     "accesslog",
					ReqStart:   reqStart,
					ReqSession: attr(entry, "reqSession"),
					Actor:      actor,
					UserDN:     targetDN,
				})
				count++
			}
			if hasReqModAddition(entry, "pwdReset") {
				emit(Event{
					Timestamp:  ts,
					Event:      TypePasswordResetRequire,
					Server:     c.server,
					Source:     "accesslog",
					ReqStart:   reqStart,
					ReqSession: attr(entry, "reqSession"),
					Actor:      actor,
					UserDN:     targetDN,
				})
				count++
			}
		}
	}

	cursor.write = maxSeen
	cursor.writeReady = true
	return count, nil
}

func (c *collector) scanLocks(cursor *cursorState, emit func(Event)) (int, error) {
	base := "(&(objectClass=auditModify)(reqMod=pwdAccountLockedTime:*))"
	filter := buildIncrementalFilter(base, cursor.lock)
	result, err := c.client.SearchAccessLog(filter, []string{
		"reqStart", "reqDN", "reqAuthzID", "reqSession", "reqMod", "reqResult",
	})
	if err != nil {
		return 0, err
	}

	baseline := !cursor.lockReady
	maxSeen := cursor.lock
	count := 0

	for _, entry := range result.Entries {
		reqStart := attr(entry, "reqStart")
		if reqStart == "" {
			continue
		}
		if !baseline && reqStart == cursor.lock {
			maxSeen = maxString(maxSeen, reqStart)
			continue
		}
		maxSeen = maxString(maxSeen, reqStart)
		if baseline {
			continue
		}

		// Same rationale as scanWrites: ignore rejected modifications so
		// denied unlocks/locks do not appear as successful events.
		if rc := attr(entry, "reqResult"); rc != "" && rc != "0" {
			continue
		}

		targetDN := attr(entry, "reqDN")
		actor := trimDNPrefix(attr(entry, "reqAuthzID"))
		ts := parseReqStart(reqStart)

		eventType := TypeAccountUnlock
		if hasReqModAddition(entry, "pwdAccountLockedTime") {
			eventType = TypeAccountLock
		}

		emit(Event{
			Timestamp:  ts,
			Event:      eventType,
			Server:     c.server,
			Source:     "accesslog",
			ReqStart:   reqStart,
			ReqSession: attr(entry, "reqSession"),
			Actor:      actor,
			UserDN:     targetDN,
		})
		count++
	}

	cursor.lock = maxSeen
	cursor.lockReady = true
	return count, nil
}

// attr returns the first value of the named attribute, or "" if absent.
func attr(entry *ldap.Entry, name string) string {
	for _, a := range entry.Attributes {
		if a.Name == name && len(a.Values) > 0 {
			return a.Values[0]
		}
	}
	return ""
}

// trimDNPrefix strips the "dn:" prefix slapd prepends to reqAuthzID values.
func trimDNPrefix(v string) string {
	return strings.TrimPrefix(v, "dn:")
}

func maxString(a, b string) string {
	if b > a {
		return b
	}
	return a
}

// parseReqStart converts an LDAP GeneralizedTime value into time.Time. On
// failure it falls back to the current wall clock so the emitted event still
// carries a usable timestamp instead of a zero value.
func parseReqStart(value string) time.Time {
	layouts := []string{
		"20060102150405.000000Z",
		"20060102150405.00000Z",
		"20060102150405.0000Z",
		"20060102150405.000Z",
		"20060102150405.00Z",
		"20060102150405.0Z",
		"20060102150405Z",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// touchedAttributes extracts the set of attribute names named in reqMod values.
// Slapd encodes each value as "<attr>:<op> <value>" where <op> is +, -, =, or #.
func touchedAttributes(entry *ldap.Entry) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, a := range entry.Attributes {
		if a.Name != "reqMod" {
			continue
		}
		for _, v := range a.Values {
			idx := strings.IndexByte(v, ':')
			if idx <= 0 {
				continue
			}
			name := v[:idx]
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func containsAttr(list []string, name string) bool {
	for _, v := range list {
		if strings.EqualFold(v, name) {
			return true
		}
	}
	return false
}

// hasReqModAddition reports whether any reqMod value encodes an add ("+") or
// replace ("=") operation on the requested attribute name. This distinguishes
// "lock the account" from "unlock the account" without re-reading the target
// entry after the fact.
func hasReqModAddition(entry *ldap.Entry, attrName string) bool {
	wantLower := strings.ToLower(attrName)
	for _, a := range entry.Attributes {
		if a.Name != "reqMod" {
			continue
		}
		for _, v := range a.Values {
			idx := strings.IndexByte(v, ':')
			if idx <= 0 {
				continue
			}
			if !strings.EqualFold(v[:idx], wantLower) {
				continue
			}
			rest := v[idx+1:]
			if len(rest) == 0 {
				continue
			}
			op := rest[0]
			if op == '+' || op == '=' {
				return true
			}
		}
	}
	return false
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

func classifyBindResult(code string) string {
	switch code {
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
	case "":
		return "unknown"
	default:
		return "error_" + code
	}
}
