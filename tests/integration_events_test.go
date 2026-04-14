//go:build integration
// +build integration

package tests

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/events"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/pool"
)

// TestEventsRunner_LiveAccesslog asserts that enabling the events runner
// against the running OpenLDAP instance captures — end to end — the
// events derived from real slapo-accesslog traffic:
//
//   - at least one bind.success event for a good password
//   - at least one account.lock event once we exceed pwdMaxFailure
//
// Account lock is used as the failure assertion rather than bind.failure
// because slapd does not always produce an auditBind entry for a rejected
// bind, but ppolicy deterministically writes pwdAccountLockedTime on the
// Nth wrong attempt — and that write IS always logged.
//
// The test resets bob's ppolicy state before running so it is idempotent
// across repeated invocations against the same stack.
func TestEventsRunner_LiveAccesslog(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	const bobDN = "cn=bob.tester,ou=users,dc=example,dc=org"
	resetPpolicyState(t, cfg, bobDN)

	// Emit to a JSON Lines file in a temp dir so the test can parse what
	// the runner produced. Daily rotation is disabled to keep the output
	// path predictable for the assertions.
	tmp := t.TempDir()
	outputPath := filepath.Join(tmp, "events.jsonl")
	cfg.EventsEnabled = true
	cfg.EventsInterval = 2 * time.Second
	cfg.EventsOutput = outputPath
	cfg.EventsRotation = config.EventsRotationNone

	client := pool.NewPooledLDAPClient(cfg)
	defer client.Close()

	runner, err := events.NewRunner(cfg, client)
	if err != nil {
		t.Fatalf("events.NewRunner: %v", err)
	}
	runner.Start()
	defer runner.Stop()

	// Give the runner its baseline scan before generating traffic so we
	// know every event produced afterwards is post-baseline.
	time.Sleep(3 * time.Second)

	// One known-good bind to produce a deterministic bind.success event.
	triggerBind(t, cfg.URL, bobDN, "BobCorrectHorse42")

	// Four wrong binds to push bob past pwdMaxFailure=3 so ppolicy locks
	// the account. pwdAccountLockedTime is written by the rootDN (ppolicy
	// internal) and is always recorded in cn=accesslog, which maps to
	// account.lock on the events stream.
	for i := 0; i < 4; i++ {
		triggerBind(t, cfg.URL, bobDN, "definitely-not-my-password")
	}

	// Poll up to 20s (pwdMaxFailure=3 → lock happens at the 3rd wrong
	// bind, plus a few extra events runner ticks at 2s interval to flush
	// the events through to the file).
	deadline := time.Now().Add(20 * time.Second)
	var seen map[events.Type]int
	for time.Now().Before(deadline) {
		seen = readEventFile(t, outputPath)
		if seen[events.TypeBindSuccess] >= 1 && seen[events.TypeAccountLock] >= 1 {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if seen[events.TypeBindSuccess] < 1 {
		t.Errorf("Expected at least one bind.success event, got: %+v", seen)
	}
	if seen[events.TypeAccountLock] < 1 {
		t.Errorf("Expected at least one account.lock event, got: %+v", seen)
	}
}

// triggerBind performs a single simple bind to the given URL. The result
// (success or failure) is logged but not asserted: the goal is to create
// an auditBind entry in cn=accesslog, which happens regardless.
func triggerBind(t *testing.T, url, bindDN, password string) {
	t.Helper()
	conn, err := ldap.DialURL(url)
	if err != nil {
		t.Fatalf("DialURL(%s): %v", url, err)
	}
	defer conn.Close()
	if err := conn.Bind(bindDN, password); err != nil {
		t.Logf("bind as %s failed (expected for wrong password): %v", bindDN, err)
		return
	}
	t.Logf("bind as %s succeeded", bindDN)
}

// resetPpolicyState binds as the data admin and removes pwdFailureTime
// and pwdAccountLockedTime from the target entry so a previous test run
// that left the account locked does not make the next run unable to
// produce fresh failure events (a locked account is rejected by ppolicy
// before accesslog sees the bind).
func resetPpolicyState(t *testing.T, cfg *config.Config, targetDN string) {
	t.Helper()
	conn, err := ldap.DialURL(cfg.URL)
	if err != nil {
		t.Fatalf("reset: DialURL(%s): %v", cfg.URL, err)
	}
	defer conn.Close()

	if err := conn.Bind("cn=admin,ou=users,dc=example,dc=org", "adminpassword"); err != nil {
		t.Fatalf("reset: data admin bind failed: %v", err)
	}

	// Delete each ppolicy operational attribute in its own modify request
	// so slapd's atomic handling does not abort the whole reset when the
	// attribute is already absent (clean entry → result code 16 "no such
	// attribute", expected and harmless).
	for _, attr := range []string{"pwdFailureTime", "pwdAccountLockedTime", "pwdReset"} {
		req := ldap.NewModifyRequest(targetDN, nil)
		req.Delete(attr, nil)
		if err := conn.Modify(req); err != nil {
			if !strings.Contains(err.Error(), "No Such Attribute") && !strings.Contains(err.Error(), "Result Code 16") {
				t.Logf("reset: delete %s on %s warning (non-fatal): %v", attr, targetDN, err)
			}
		}
	}
}

// readEventFile parses a JSON Lines event file and returns a histogram of
// event types encountered. An absent file yields an empty map so callers
// can poll safely while the runner is still warming up.
func readEventFile(t *testing.T, path string) map[events.Type]int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[events.Type]int{}
		}
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	counts := map[events.Type]int{}
	scanner := bufio.NewScanner(f)
	// Events lines can legitimately be long (reqMod list with many attrs),
	// so give the scanner a generous buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev events.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("malformed event line %q: %v", line, err)
			continue
		}
		counts[ev.Event]++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	return counts
}
