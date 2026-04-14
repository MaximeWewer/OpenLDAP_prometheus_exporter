package exporter

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func newTestBindVec() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "test",
			Name:      "bind_total",
			Help:      "test",
		},
		[]string{"server", "user", "result"},
	)
}

func TestAccesslogSeriesTracker_IncAndDedup(t *testing.T) {
	vec := newTestBindVec()
	tr := newAccesslogSeriesTracker("bind_total", vec, 100)

	labels := prometheus.Labels{"server": "srv", "user": "alice", "result": "success"}
	if !tr.Inc(labels) {
		t.Fatal("first Inc should be admitted")
	}
	if !tr.Inc(labels) {
		t.Fatal("second Inc on the same tuple should still be admitted")
	}
	if got := tr.Size(); got != 1 {
		t.Errorf("tracker should hold one entry, got %d", got)
	}
}

func TestAccesslogSeriesTracker_HardCap(t *testing.T) {
	vec := newTestBindVec()
	tr := newAccesslogSeriesTracker("bind_total", vec, 3)

	for i, user := range []string{"alice", "bob", "carol"} {
		labels := prometheus.Labels{"server": "srv", "user": user, "result": "success"}
		if !tr.Inc(labels) {
			t.Fatalf("inc %d should be admitted", i)
		}
	}
	if got := tr.Size(); got != 3 {
		t.Fatalf("tracker should hold 3 entries, got %d", got)
	}

	// 4th distinct tuple must be rejected by the cap.
	if tr.Inc(prometheus.Labels{"server": "srv", "user": "dave", "result": "success"}) {
		t.Error("cap should reject the fourth distinct tuple")
	}
	// But an already-admitted tuple should still succeed.
	if !tr.Inc(prometheus.Labels{"server": "srv", "user": "alice", "result": "success"}) {
		t.Error("known tuple should remain admitted after cap is hit")
	}
}

func TestAccesslogSeriesTracker_SweepEvictsStale(t *testing.T) {
	vec := newTestBindVec()
	tr := newAccesslogSeriesTracker("bind_total", vec, 10)

	fresh := prometheus.Labels{"server": "srv", "user": "alice", "result": "success"}
	stale := prometheus.Labels{"server": "srv", "user": "bob", "result": "success"}

	tr.Inc(fresh)
	tr.Inc(stale)

	// Backdate bob's lastSeen so the sweep evicts him but spares alice.
	tr.mu.Lock()
	for _, entry := range tr.seen {
		if entry.labels["user"] == "bob" {
			entry.lastSeen = time.Now().Add(-2 * time.Hour)
		}
	}
	tr.mu.Unlock()

	removed := tr.Sweep(1 * time.Hour)
	if removed != 1 {
		t.Errorf("expected 1 eviction, got %d", removed)
	}
	if tr.Size() != 1 {
		t.Errorf("tracker should hold 1 entry after sweep, got %d", tr.Size())
	}
}

func TestAccesslogSeriesTracker_NilSafe(t *testing.T) {
	var tr *accesslogSeriesTracker
	if tr.Inc(prometheus.Labels{"server": "srv", "user": "x", "result": "success"}) {
		t.Error("nil tracker should refuse Inc")
	}
	if got := tr.Sweep(time.Second); got != 0 {
		t.Errorf("nil tracker Sweep should return 0, got %d", got)
	}
	if got := tr.Size(); got != 0 {
		t.Errorf("nil tracker Size should return 0, got %d", got)
	}
}

func TestLabelKey_Deterministic(t *testing.T) {
	a := labelKey(prometheus.Labels{"server": "srv", "user": "alice", "result": "success"})
	b := labelKey(prometheus.Labels{"result": "success", "user": "alice", "server": "srv"})
	if a != b {
		t.Errorf("labelKey must be insensitive to map iteration order: %q vs %q", a, b)
	}
}
