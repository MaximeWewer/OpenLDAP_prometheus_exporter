package accesslog

import (
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

func TestShouldEmit_BaselineNeverEmits(t *testing.T) {
	emit, max := ShouldEmit("20260101120000.000001Z", "", true, "")
	if emit {
		t.Error("baseline must not emit")
	}
	if max != "20260101120000.000001Z" {
		t.Errorf("max not advanced: %q", max)
	}
}

func TestShouldEmit_SkipsBoundary(t *testing.T) {
	cur := "20260101120000.000001Z"
	emit, max := ShouldEmit(cur, cur, false, cur)
	if emit {
		t.Error("entry equal to cursor must be skipped")
	}
	if max != cur {
		t.Errorf("max should stay at cursor, got %q", max)
	}
}

func TestShouldEmit_EmitsNewer(t *testing.T) {
	cur := "20260101120000.000001Z"
	emit, max := ShouldEmit("20260101120000.000002Z", cur, false, cur)
	if !emit {
		t.Error("newer entry must emit")
	}
	if max != "20260101120000.000002Z" {
		t.Errorf("max not advanced: %q", max)
	}
}

func TestShouldEmit_EmptyIsDropped(t *testing.T) {
	emit, max := ShouldEmit("", "cursor", false, "old")
	if emit {
		t.Error("empty reqStart must not emit")
	}
	if max != "old" {
		t.Errorf("max should stay: %q", max)
	}
}

func TestCursorState_ReadyFlag(t *testing.T) {
	c := &CursorState{}
	_, ready := c.Get(StreamBind)
	if ready {
		t.Error("fresh cursor must not be ready")
	}
	c.Advance(StreamBind, "")
	_, ready = c.Get(StreamBind)
	if !ready {
		t.Error("Advance must set ready even with empty maxSeen")
	}
}

func TestBuildIncrementalFilter(t *testing.T) {
	if got := BuildIncrementalFilter(BindBaseFilter, ""); got != BindBaseFilter {
		t.Errorf("empty cursor should return base filter, got %q", got)
	}
	want := "(&(objectClass=auditBind)(reqStart>=20260101120000.000000Z))"
	if got := BuildIncrementalFilter(BindBaseFilter, "20260101120000.000000Z"); got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestClassifyBindResult(t *testing.T) {
	cases := map[string]string{
		"0":  "success",
		"49": "invalid_credentials",
		"50": "insufficient_access",
		"":   "unknown",
		"77": "error_77",
	}
	for in, want := range cases {
		if got := ClassifyBindResult(in); got != want {
			t.Errorf("ClassifyBindResult(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasReqModAddition(t *testing.T) {
	entry := &ldap.Entry{
		Attributes: []*ldap.EntryAttribute{
			{Name: "reqMod", Values: []string{"pwdAccountLockedTime:+ 20260101120000Z"}},
		},
	}
	if !HasReqModAddition(entry, "pwdAccountLockedTime") {
		t.Error("additive lock must be recognized")
	}
	unlock := &ldap.Entry{
		Attributes: []*ldap.EntryAttribute{
			{Name: "reqMod", Values: []string{"pwdAccountLockedTime:-"}},
		},
	}
	if HasReqModAddition(unlock, "pwdAccountLockedTime") {
		t.Error("deletion must not count as addition")
	}
}

func TestTouchedAttributes(t *testing.T) {
	entry := &ldap.Entry{
		Attributes: []*ldap.EntryAttribute{
			{Name: "reqMod", Values: []string{
				"userPassword:= {SSHA}xxx",
				"pwdChangedTime:= 20260101120000Z",
				"userPassword:# 1", // duplicate attr
			}},
		},
	}
	got := TouchedAttributes(entry)
	if len(got) != 2 {
		t.Fatalf("want 2 deduped attributes, got %v", got)
	}
}

func TestParseGeneralizedTime_NormalizesFraction(t *testing.T) {
	cases := []struct {
		in   string
		want string // ISO8601 representation
	}{
		{"20260101120000Z", "2026-01-01T12:00:00Z"},
		{"20260101120000.5Z", "2026-01-01T12:00:00.5Z"},
		{"20260101120000.500000Z", "2026-01-01T12:00:00.5Z"},
		// More than 6 fractional digits: must be truncated rather than
		// rejected — this was the original bug.
		{"20260101120000.0000007Z", "2026-01-01T12:00:00Z"},
		{"20260101120000.1234567Z", "2026-01-01T12:00:00.123456Z"},
	}
	for _, tc := range cases {
		got, err := ParseGeneralizedTime(tc.in)
		if err != nil {
			t.Errorf("ParseGeneralizedTime(%q): %v", tc.in, err)
			continue
		}
		want, _ := time.Parse(time.RFC3339Nano, tc.want)
		if !got.Equal(want) {
			t.Errorf("ParseGeneralizedTime(%q) = %v, want %v", tc.in, got, want)
		}
	}
}

func TestParseReqStart_FallsBackOnBadInput(t *testing.T) {
	if ParseReqStart("not-a-date").IsZero() {
		t.Error("fallback timestamp must not be zero")
	}
}

func TestExtractUserNameFromDN(t *testing.T) {
	cases := map[string]string{
		"uid=alice,ou=users,dc=x,dc=y": "alice",
		"cn=Bob Tester,ou=users":       "Bob Tester",
		"":                             "",
		"garbage":                      "garbage",
	}
	for in, want := range cases {
		if got := ExtractUserNameFromDN(in); got != want {
			t.Errorf("ExtractUserNameFromDN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaxString(t *testing.T) {
	if MaxString("a", "b") != "b" {
		t.Error("b > a")
	}
	if MaxString("b", "a") != "b" {
		t.Error("max should return b")
	}
}
