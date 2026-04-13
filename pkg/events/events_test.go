package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
)

// fakeSearcher queues canned LDAP search results. Each filter can have a
// sequence of responses (one per call) so a test can model how a stream
// reacts differently on the baseline tick and on subsequent ticks. If a
// filter only has one response, it is repeated for later calls.
type fakeSearcher struct {
	responses map[string][][]*ldap.Entry
	calls     map[string]int
}

func newFakeSearcher() *fakeSearcher {
	return &fakeSearcher{
		responses: map[string][][]*ldap.Entry{},
		calls:     map[string]int{},
	}
}

func (f *fakeSearcher) queue(filter string, entries ...*ldap.Entry) {
	f.responses[filter] = append(f.responses[filter], entries)
}

func (f *fakeSearcher) SearchAccessLog(filter string, _ []string) (*ldap.SearchResult, error) {
	seq := f.responses[filter]
	idx := f.calls[filter]
	f.calls[filter] = idx + 1
	if len(seq) == 0 {
		return &ldap.SearchResult{}, nil
	}
	var entries []*ldap.Entry
	if idx < len(seq) {
		entries = seq[idx]
	} else {
		entries = seq[len(seq)-1]
	}
	return &ldap.SearchResult{Entries: entries}, nil
}

func entry(attrs map[string][]string) *ldap.Entry {
	e := &ldap.Entry{}
	for k, v := range attrs {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: k, Values: v})
	}
	return e
}

func collectEvents(t *testing.T, f *fakeSearcher) []Event {
	t.Helper()
	c := newCollector(f, "srv")
	cur := &cursorState{}
	// First scan establishes baseline without emitting events.
	var baseline []Event
	if _, err := c.scan(cur, func(e Event) { baseline = append(baseline, e) }); err != nil {
		t.Fatalf("baseline scan: %v", err)
	}
	if len(baseline) != 0 {
		t.Fatalf("baseline scan must not emit events, got %d", len(baseline))
	}
	// Second scan: emit using the advanced cursor.
	var got []Event
	if _, err := c.scan(cur, func(e Event) { got = append(got, e) }); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return got
}

func TestCollector_BindSuccessAndFailure(t *testing.T) {
	f := newFakeSearcher()
	f.queue("(objectClass=auditBind)",
		entry(map[string][]string{
			"reqStart":   {"20260101120000.000000Z"},
			"reqDN":      {"uid=alice,dc=example,dc=org"},
			"reqResult":  {"0"},
			"reqAuthzID": {"dn:uid=alice,dc=example,dc=org"},
		}),
	)
	f.queue("(&(objectClass=auditBind)(reqStart>=20260101120000.000000Z))",
		// cursor value repeats → must be skipped as boundary
		entry(map[string][]string{
			"reqStart":  {"20260101120000.000000Z"},
			"reqDN":     {"uid=alice,dc=example,dc=org"},
			"reqResult": {"0"},
		}),
		entry(map[string][]string{
			"reqStart":  {"20260101120005.000000Z"},
			"reqDN":     {"uid=bob,dc=example,dc=org"},
			"reqResult": {"49"},
		}),
	)

	events := collectEvents(t, f)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (boundary skipped), got %d: %+v", len(events), events)
	}
	e := events[0]
	if e.Event != TypeBindFailure {
		t.Errorf("want %s, got %s", TypeBindFailure, e.Event)
	}
	if e.Result != "invalid_credentials" {
		t.Errorf("want invalid_credentials, got %s", e.Result)
	}
	if e.UserDN != "uid=bob,dc=example,dc=org" {
		t.Errorf("unexpected user_dn: %s", e.UserDN)
	}
}

func TestCollector_WriteDerivesPasswordChangeAndLock(t *testing.T) {
	writeBase := "(|(objectClass=auditAdd)(objectClass=auditModify)(objectClass=auditDelete)(objectClass=auditModRDN))"
	lockBase := "(&(objectClass=auditModify)(reqMod=pwdAccountLockedTime:*))"

	f := newFakeSearcher()
	// Baseline write entry (ignored but advances cursor.write).
	f.queue(writeBase, entry(map[string][]string{
		"reqStart": {"20260101100000.000000Z"},
		"reqType":  {"modify"},
		"reqDN":    {"uid=seed,dc=example,dc=org"},
	}))
	// Second tick: real write carrying password change + reset marker.
	f.queue("(&"+writeBase+"(reqStart>=20260101100000.000000Z))",
		entry(map[string][]string{
			"reqStart":   {"20260101100030.000000Z"},
			"reqType":    {"modify"},
			"reqDN":      {"uid=carol,dc=example,dc=org"},
			"reqAuthzID": {"dn:cn=admin,dc=example,dc=org"},
			"reqMod": {
				"userPassword:= {SSHA}abc",
				"pwdChangedTime:= 20260101100030Z",
				"pwdReset:+ TRUE",
			},
		}),
	)
	// Lock stream is empty on baseline (ready flag is what advances it),
	// then a real lock event appears on the second tick.
	f.queue(lockBase) // baseline empty
	f.queue(lockBase, // second tick — filter is still the base since cursor is still ""
		entry(map[string][]string{
			"reqStart": {"20260101100040.000000Z"},
			"reqDN":    {"uid=dan,dc=example,dc=org"},
			"reqMod":   {"pwdAccountLockedTime:+ 20260101100040Z"},
		}),
	)

	c := newCollector(f, "srv")
	cur := &cursorState{}

	if _, err := c.scan(cur, func(Event) {}); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if cur.write != "20260101100000.000000Z" {
		t.Fatalf("write cursor not advanced on baseline: %q", cur.write)
	}
	if !cur.lockReady {
		t.Fatalf("lock stream must be marked ready after baseline even with no entries")
	}

	var out []Event
	if _, err := c.scan(cur, func(e Event) { out = append(out, e) }); err != nil {
		t.Fatalf("scan: %v", err)
	}

	seen := map[Type]int{}
	for _, ev := range out {
		seen[ev.Event]++
	}
	if seen[TypeWriteModify] != 1 {
		t.Errorf("want 1 write.modify, got %d (events: %+v)", seen[TypeWriteModify], out)
	}
	if seen[TypePasswordChange] != 1 {
		t.Errorf("want 1 password.change, got %d", seen[TypePasswordChange])
	}
	if seen[TypePasswordResetRequire] != 1 {
		t.Errorf("want 1 password.reset_required, got %d", seen[TypePasswordResetRequire])
	}
	if seen[TypeAccountLock] != 1 {
		t.Errorf("want 1 account.lock, got %d", seen[TypeAccountLock])
	}
}

func TestEmitter_Filter(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "events.jsonl")
	cfg := &config.Config{
		EventsOutput:   path,
		EventsRotation: config.EventsRotationNone,
		EventsTypes:    []string{"bind.failure"},
	}
	em, err := NewEmitter(cfg)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer em.Close()

	em.Emit(Event{Event: TypeBindSuccess})
	em.Emit(Event{Event: TypeBindFailure, Server: "srv"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), string(data))
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Event != TypeBindFailure {
		t.Errorf("want %s, got %s", TypeBindFailure, ev.Event)
	}
}

func TestRotator_DailyRollsOverUTCDay(t *testing.T) {
	tmp := t.TempDir()
	template := filepath.Join(tmp, "events-{date}.jsonl")

	r, err := newFileRotator(template, true)
	if err != nil {
		t.Fatalf("newFileRotator: %v", err)
	}
	defer r.Close()

	day1 := time.Date(2026, 1, 1, 23, 59, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 0, 0, 1, 0, time.UTC)

	r.clock = func() time.Time { return day1 }
	if _, err := r.Write([]byte("one\n")); err != nil {
		t.Fatalf("write day1: %v", err)
	}

	r.clock = func() time.Time { return day2 }
	if _, err := r.Write([]byte("two\n")); err != nil {
		t.Fatalf("write day2: %v", err)
	}

	paths := []string{
		filepath.Join(tmp, "events-2026-01-01.jsonl"),
		filepath.Join(tmp, "events-2026-01-02.jsonl"),
	}
	for i, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		want := []string{"one\n", "two\n"}[i]
		if string(data) != want {
			t.Errorf("%s: want %q got %q", p, want, string(data))
		}
	}
}

func TestRotator_NoneAppendsIndefinitely(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "events.jsonl")

	r, err := newFileRotator(path, false)
	if err != nil {
		t.Fatalf("newFileRotator: %v", err)
	}
	defer r.Close()

	r.clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if _, err := r.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	r.clock = func() time.Time { return time.Date(2026, 1, 2, 0, 0, 1, 0, time.UTC) }
	if _, err := r.Write([]byte("two\n")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Errorf("want appended, got %q", string(data))
	}
}

func TestCollector_SkipsFailedWritesAndLocks(t *testing.T) {
	writeBase := "(|(objectClass=auditAdd)(objectClass=auditModify)(objectClass=auditDelete)(objectClass=auditModRDN))"
	lockBase := "(&(objectClass=auditModify)(reqMod=pwdAccountLockedTime:*))"

	f := newFakeSearcher()
	// Baseline — empty streams so the "ready" flag flips without emitting.
	f.queue(writeBase)
	f.queue(lockBase)

	// Second tick — all entries have reqResult=50 (insufficient access).
	// The collector must skip them entirely rather than emit phantom events
	// for operations the server rejected.
	f.queue(writeBase,
		entry(map[string][]string{
			"reqStart":  {"20260101120000.000000Z"},
			"reqType":   {"modify"},
			"reqDN":     {"uid=victim,dc=example,dc=org"},
			"reqResult": {"50"},
			"reqMod":    {"userPassword:= {SSHA}xxx"},
		}),
	)
	f.queue(lockBase,
		entry(map[string][]string{
			"reqStart":  {"20260101120001.000000Z"},
			"reqDN":     {"uid=victim,dc=example,dc=org"},
			"reqResult": {"50"},
			"reqMod":    {"pwdAccountLockedTime:- 20260101120001Z"},
		}),
	)

	c := newCollector(f, "srv")
	cur := &cursorState{}

	if _, err := c.scan(cur, func(Event) {}); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	var out []Event
	if _, err := c.scan(cur, func(e Event) { out = append(out, e) }); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no events from failed operations, got %d: %+v", len(out), out)
	}

	// Cursors must still advance so the same failed entries are not
	// reconsidered on the next tick.
	if cur.write != "20260101120000.000000Z" {
		t.Errorf("write cursor not advanced: %q", cur.write)
	}
	if cur.lock != "20260101120001.000000Z" {
		t.Errorf("lock cursor not advanced: %q", cur.lock)
	}
}

func TestParseReqStart_FallsBackOnBadInput(t *testing.T) {
	ts := parseReqStart("not-a-date")
	if ts.IsZero() {
		t.Error("expected non-zero fallback timestamp")
	}
	ts = parseReqStart("20260413142201Z")
	if ts.Year() != 2026 || ts.Month() != 4 || ts.Day() != 13 {
		t.Errorf("unexpected parsed time: %v", ts)
	}
}
