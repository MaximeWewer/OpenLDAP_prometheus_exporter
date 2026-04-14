package exporter

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Cardinality safety constants for the accesslog CounterVecs. Per-user
// counters are driven by LDAP bind DNs, which are trivially spoofable by
// an anonymous client, so without these guards an attacker could trigger
// an unbounded growth of Prometheus series and exhaust the exporter
// memory. The sweeper drops series that have not been touched for
// AccesslogSeriesTTL, and the hard cap refuses new label tuples once a
// single CounterVec holds more than MaxAccesslogSeriesPerVec distinct
// series — whichever fires first.
const (
	// MaxAccesslogSeriesPerVec caps the number of distinct label tuples
	// a single accesslog CounterVec may hold at any time. Hit by rotating
	// bind DNs, the exporter logs a warning and drops further additions
	// until the sweeper reclaims old entries.
	MaxAccesslogSeriesPerVec = 10000

	// AccesslogSeriesTTL is the idle time after which a series is
	// considered stale and removed by the sweeper.
	AccesslogSeriesTTL = 1 * time.Hour

	// AccesslogSeriesSweepInterval is how often the sweeper walks every
	// tracker and deletes expired series.
	AccesslogSeriesSweepInterval = 10 * time.Minute
)

// accesslogSeriesTracker bookkeeps the label tuples that have been
// touched on a single CounterVec so the set can be bounded and stale
// entries can be evicted. It is safe for concurrent use.
type accesslogSeriesTracker struct {
	mu         sync.Mutex
	name       string
	vec        *prometheus.CounterVec
	seen       map[string]*accesslogSeriesEntry
	maxEntries int
	// capLogged debounces the "series cap reached" warning so a sustained
	// attack does not flood the log with one line per Inc call.
	capLogged time.Time
}

type accesslogSeriesEntry struct {
	labels   prometheus.Labels
	lastSeen time.Time
}

// newAccesslogSeriesTracker builds a tracker for a CounterVec. `name` is
// only used in log fields so operators can tell which metric triggered a
// warning.
func newAccesslogSeriesTracker(name string, vec *prometheus.CounterVec, maxEntries int) *accesslogSeriesTracker {
	return &accesslogSeriesTracker{
		name:       name,
		vec:        vec,
		seen:       make(map[string]*accesslogSeriesEntry),
		maxEntries: maxEntries,
	}
}

// Inc admits a label tuple to the CounterVec only if it is already known
// or the tracker still has room for a new entry, then increments the
// Prometheus counter. Returns true on admission, false when the hard cap
// kicked in and the tuple was dropped.
func (t *accesslogSeriesTracker) Inc(labels prometheus.Labels) bool {
	if t == nil || t.vec == nil {
		return false
	}
	key := labelKey(labels)

	t.mu.Lock()
	entry, known := t.seen[key]
	if known {
		entry.lastSeen = time.Now()
		t.mu.Unlock()
		t.vec.With(labels).Inc()
		return true
	}
	if len(t.seen) >= t.maxEntries {
		// Debounce the warning to at most once per minute.
		shouldLog := time.Since(t.capLogged) > time.Minute
		if shouldLog {
			t.capLogged = time.Now()
		}
		t.mu.Unlock()
		if shouldLog {
			logger.SafeWarn("exporter", "Accesslog series cap reached, new label tuple dropped", map[string]interface{}{
				"metric": t.name,
				"cap":    t.maxEntries,
			})
		}
		return false
	}
	t.seen[key] = &accesslogSeriesEntry{
		labels:   copyLabels(labels),
		lastSeen: time.Now(),
	}
	t.mu.Unlock()

	t.vec.With(labels).Inc()
	return true
}

// Sweep removes every tracked series whose lastSeen is older than ttl.
// Returns the number of entries evicted. Called periodically by the
// exporter-level sweeper goroutine.
func (t *accesslogSeriesTracker) Sweep(ttl time.Duration) int {
	if t == nil {
		return 0
	}
	cutoff := time.Now().Add(-ttl)

	t.mu.Lock()
	var toDelete []*accesslogSeriesEntry
	for key, entry := range t.seen {
		if entry.lastSeen.Before(cutoff) {
			toDelete = append(toDelete, entry)
			delete(t.seen, key)
		}
	}
	t.mu.Unlock()

	for _, entry := range toDelete {
		t.vec.Delete(entry.labels)
	}
	if len(toDelete) > 0 {
		logger.SafeDebug("exporter", "Accesslog series swept", map[string]interface{}{
			"metric":  t.name,
			"removed": len(toDelete),
			"ttl":     ttl.String(),
		})
	}
	return len(toDelete)
}

// Size returns the number of currently tracked series. Exported for
// tests.
func (t *accesslogSeriesTracker) Size() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.seen)
}

// labelKey serializes a prometheus.Labels map into a deterministic
// string key so it can be used as a map index. Keys are sorted to keep
// the result independent of map iteration order.
func labelKey(labels prometheus.Labels) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('\x1f')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(labels[k])
	}
	return sb.String()
}

func copyLabels(in prometheus.Labels) prometheus.Labels {
	out := make(prometheus.Labels, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// sweepAccesslogTrackers runs in its own goroutine and periodically
// reaps stale series from every accesslog tracker. It terminates when
// the exporter's stopChan is closed.
func (e *OpenLDAPExporter) sweepAccesslogTrackers() {
	ticker := time.NewTicker(AccesslogSeriesSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if e.accesslogBindTracker != nil {
				e.accesslogBindTracker.Sweep(AccesslogSeriesTTL)
			}
			if e.accesslogWriteTracker != nil {
				e.accesslogWriteTracker.Sweep(AccesslogSeriesTTL)
			}
			if e.accesslogLockTracker != nil {
				e.accesslogLockTracker.Sweep(AccesslogSeriesTTL)
			}
		case <-e.stopChan:
			return
		}
	}
}
