// Package accesslog contains the primitives shared by the two consumers
// of the slapo-accesslog stream: the Prometheus counter collectors in
// pkg/exporter and the JSON events runner in pkg/events. Both walk the
// same auditBind / auditModify / auditAdd / auditDelete / auditModRDN
// entries with the same reqStart cursor semantics, so the helper types
// and functions live in one place instead of being copy-pasted between
// the two packages.
package accesslog

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Stream identifies one of the three incremental streams we walk over
// cn=accesslog on every scan.
type Stream int

const (
	// StreamBind tracks auditBind entries.
	StreamBind Stream = iota
	// StreamWrite tracks auditAdd/auditModify/auditDelete/auditModRDN entries.
	StreamWrite
	// StreamLock tracks auditModify entries that touch pwdAccountLockedTime.
	StreamLock
)

// BindBaseFilter is the LDAP filter used to enumerate bind events.
const BindBaseFilter = "(objectClass=auditBind)"

// WriteBaseFilter is the LDAP filter used to enumerate every audit write
// objectClass in one search.
const WriteBaseFilter = "(|(objectClass=auditAdd)(objectClass=auditModify)(objectClass=auditDelete)(objectClass=auditModRDN))"

// LockBaseFilter is the LDAP filter used to enumerate auditModify entries
// that touch pwdAccountLockedTime, which the ppolicy overlay writes when
// it locks an account.
const LockBaseFilter = "(&(objectClass=auditModify)(reqMod=pwdAccountLockedTime:*))"

// CursorState holds per-stream bookmarks for the incremental accesslog
// scan. Each stream has its own reqStart value and a Ready flag so that
// an empty stream on the very first scan still transitions out of
// baseline mode — otherwise the first real event that ever lands on an
// idle stream would be silently dropped because the empty-string cursor
// would still look like "not yet initialized".
type CursorState struct {
	Bind       string
	BindReady  bool
	Write      string
	WriteReady bool
	Lock       string
	LockReady  bool
}

// NowGeneralizedTime returns the current UTC time formatted as an LDAP
// GeneralizedTime with microsecond precision, suitable for a reqStart bound.
func NowGeneralizedTime() string {
	return time.Now().UTC().Format("20060102150405.000000Z")
}

// NewCursorState returns a cursor whose three streams are seeded to the current
// time. This is critical: an empty (zero-value) cursor makes
// BuildIncrementalFilter fall back to the bare base filter, which matches the
// ENTIRE accesslog history — on a busy server that is hundreds of thousands of
// entries pulled into memory in a single SearchResult on the very first scan
// (and again on every restart), which both OOMs the exporter and hammers slapd.
// Seeding to "now" bounds every scan to reqStart >= start-up time. Ready is left
// false so the first scan still only establishes the high-water mark and emits
// no historical events.
func NewCursorState() *CursorState {
	now := NowGeneralizedTime()
	return &CursorState{
		Bind:  now,
		Write: now,
		Lock:  now,
	}
}

// Get returns the current cursor value and readiness flag for a stream.
func (c *CursorState) Get(s Stream) (value string, ready bool) {
	switch s {
	case StreamBind:
		return c.Bind, c.BindReady
	case StreamWrite:
		return c.Write, c.WriteReady
	case StreamLock:
		return c.Lock, c.LockReady
	}
	return "", false
}

// Advance records that a stream has been scanned up to `maxSeen`. The
// Ready flag is set unconditionally so the next scan is guaranteed to
// leave baseline mode even when maxSeen is still empty (the stream had
// no entries at all on the first tick).
func (c *CursorState) Advance(s Stream, maxSeen string) {
	switch s {
	case StreamBind:
		c.Bind = maxSeen
		c.BindReady = true
	case StreamWrite:
		c.Write = maxSeen
		c.WriteReady = true
	case StreamLock:
		c.Lock = maxSeen
		c.LockReady = true
	}
}

// BuildIncrementalFilter wraps a base objectClass filter with a reqStart
// lower bound so the LDAP server only returns entries newer than the
// cursor. The cursor is embedded verbatim because a GeneralizedTime only
// contains digits, '.', and 'Z' — it is safe inside an LDAP filter.
func BuildIncrementalFilter(base, cursor string) string {
	if cursor == "" {
		return base
	}
	return fmt.Sprintf("(&%s(reqStart>=%s))", base, cursor)
}

// ShouldEmit reports whether a given accesslog entry should produce a
// downstream event this tick and returns the updated maximum reqStart
// value. The caller is expected to track whether the stream has already
// been scanned once (the baseline flag) and the current cursor so this
// function can uniformly handle:
//
//   - baseline scans (cursor = "", baseline = true): never emit, always
//     advance maxSeen
//   - subsequent scans (baseline = false): emit every entry strictly
//     greater than the cursor; skip the boundary entry whose reqStart
//     equals the cursor to avoid double counting after `>=` returns the
//     cursor itself
//
// If reqStart is empty the function is a no-op — the entry is malformed
// and the caller should drop it.
func ShouldEmit(reqStart, cursor string, baseline bool, maxSeen string) (emit bool, newMax string) {
	if reqStart == "" {
		return false, maxSeen
	}
	next := maxSeen
	if reqStart > next {
		next = reqStart
	}
	if baseline {
		return false, next
	}
	if reqStart == cursor {
		return false, next
	}
	return true, next
}

// Attr returns the first value of the named attribute on an LDAP entry,
// or the empty string when the attribute is absent or empty.
func Attr(entry *ldap.Entry, name string) string {
	for _, a := range entry.Attributes {
		if a.Name == name && len(a.Values) > 0 {
			return a.Values[0]
		}
	}
	return ""
}

// TrimDNPrefix strips the "dn:" prefix that slapd prepends to reqAuthzID
// values, turning "dn:cn=admin,dc=example,dc=org" into the raw DN.
func TrimDNPrefix(v string) string {
	return strings.TrimPrefix(v, "dn:")
}

// ExtractUserNameFromDN takes the first RDN value of a DN (e.g.
// "uid=alice,ou=users,dc=example,dc=org" → "alice") so a human-readable
// "user" label can be derived from a bind DN.
func ExtractUserNameFromDN(dn string) string {
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

// TouchedAttributes returns the set of attribute names referenced in an
// entry's reqMod values (de-duplicated). Each reqMod value is encoded by
// slapd as "<attr>:<op> <value>" where <op> is one of +, -, =, #.
func TouchedAttributes(entry *ldap.Entry) []string {
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

// HasReqModAddition reports whether any reqMod value encodes an add
// ("+") or replace ("=") operation on the requested attribute name. This
// is how we tell an "account locked" write (pwdAccountLockedTime:+ ...)
// from an "account unlocked" write (pwdAccountLockedTime:-) without
// re-reading the target entry after the fact.
func HasReqModAddition(entry *ldap.Entry, attrName string) bool {
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

// ClassifyBindResult converts an LDAP result code string into a stable
// human-readable label used as a Prometheus series dimension and as the
// "result" field on bind events.
func ClassifyBindResult(code string) string {
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

// ParseGeneralizedTime parses an LDAP GeneralizedTime string of the form
// YYYYMMDDHHmmss[.fffff]Z into time.Time. Unlike a naive enumeration of
// fixed-width layouts, it tolerates any number of fractional digits —
// RFC 4517 allows 1..N, and slapd emits between 1 and 7 depending on the
// build and the operation. When the fractional part has more than six
// digits (the maximum Go time.Parse can ingest with the standard layout)
// it is truncated rather than rejected, so a reqStart like
// `20260101120000.0000001Z` is parsed as 2026-01-01T12:00:00.000000Z
// instead of silently failing and falling back to the wall clock.
func ParseGeneralizedTime(value string) (time.Time, error) {
	normalized := normalizeGeneralizedTime(value)
	if normalized == "" {
		return time.Time{}, &time.ParseError{
			Layout:  "YYYYMMDDHHmmss[.frac]Z",
			Value:   value,
			Message: "empty generalized time",
		}
	}

	layouts := []string{
		"20060102150405.000000Z",
		"20060102150405Z",
		"20060102150405",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, normalized); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, &time.ParseError{
		Layout:  "YYYYMMDDHHmmss[.frac]Z",
		Value:   value,
		Message: "unrecognized GeneralizedTime format",
	}
}

// normalizeGeneralizedTime trims trailing Z, normalises the fractional
// part to exactly six digits (padding with zeros, truncating when
// longer), and reattaches Z. Inputs without a fractional part are
// returned unchanged.
func normalizeGeneralizedTime(value string) string {
	if value == "" {
		return ""
	}
	// Strip an optional trailing Z so string manipulation below does
	// not have to special-case it.
	hasZ := strings.HasSuffix(value, "Z")
	body := value
	if hasZ {
		body = strings.TrimSuffix(body, "Z")
	}
	dot := strings.IndexByte(body, '.')
	if dot < 0 {
		if hasZ {
			return body + "Z"
		}
		return body
	}
	head := body[:dot]
	frac := body[dot+1:]
	switch {
	case len(frac) >= 6:
		frac = frac[:6]
	default:
		frac = frac + strings.Repeat("0", 6-len(frac))
	}
	if hasZ {
		return head + "." + frac + "Z"
	}
	return head + "." + frac
}

// ParseReqStart is a thin alias over ParseGeneralizedTime that hides
// parse failures behind the current wall clock. Callers that need to
// distinguish parse errors should use ParseGeneralizedTime directly.
func ParseReqStart(value string) time.Time {
	t, err := ParseGeneralizedTime(value)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

// MaxString returns the lexicographically greater of a and b. Used to
// track the highest reqStart observed during a scan.
func MaxString(a, b string) string {
	if b > a {
		return b
	}
	return a
}
