package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestNewOpenLDAPMetrics tests the creation of all metrics
func TestNewOpenLDAPMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	if metrics == nil {
		t.Fatal("NewOpenLDAPMetrics should return non-nil metrics")
	}

	// Test that all metric fields are initialized
	tests := []struct {
		name   string
		metric interface{}
	}{
		{"ConnectionsCurrent", metrics.ConnectionsCurrent},
		{"ConnectionsTotal", metrics.ConnectionsTotal},
		{"BytesTotal", metrics.BytesTotal},
		{"PduTotal", metrics.PduTotal},
		{"ReferralsTotal", metrics.ReferralsTotal},
		{"EntriesTotal", metrics.EntriesTotal},
		{"ThreadsMax", metrics.ThreadsMax},
		{"ThreadsMaxPending", metrics.ThreadsMaxPending},
		{"ThreadsBackload", metrics.ThreadsBackload},
		{"ThreadsActive", metrics.ThreadsActive},
		{"ThreadsOpen", metrics.ThreadsOpen},
		{"ThreadsStarting", metrics.ThreadsStarting},
		{"ThreadsPending", metrics.ThreadsPending},
		{"ThreadsState", metrics.ThreadsState},
		{"OperationsInitiated", metrics.OperationsInitiated},
		{"OperationsCompleted", metrics.OperationsCompleted},
		{"WaitersRead", metrics.WaitersRead},
		{"WaitersWrite", metrics.WaitersWrite},
		{"OverlaysInfo", metrics.OverlaysInfo},
		{"ServerTime", metrics.ServerTime},
		{"ServerUptime", metrics.ServerUptime},
		{"TlsInfo", metrics.TlsInfo},
		{"BackendsInfo", metrics.BackendsInfo},
		{"ListenersInfo", metrics.ListenersInfo},
		{"DatabaseEntries", metrics.DatabaseEntries},
		{"HealthStatus", metrics.HealthStatus},
		{"ResponseTime", metrics.ResponseTime},
		{"ScrapeErrors", metrics.ScrapeErrors},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.metric == nil {
				t.Errorf("Metric %s should not be nil", tt.name)
			}
		})
	}
}

// TestConnectionsCurrentMetric tests ConnectionsCurrent gauge
func TestConnectionsCurrentMetric(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test setting value
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Set(10)

	// Test getting value
	value := testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("test-server"))
	if value != 10 {
		t.Errorf("Expected ConnectionsCurrent to be 10, got %f", value)
	}

	// Test multiple labels
	metrics.ConnectionsCurrent.WithLabelValues("server1").Set(5)
	metrics.ConnectionsCurrent.WithLabelValues("server2").Set(15)

	value1 := testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("server1"))
	value2 := testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("server2"))

	if value1 != 5 {
		t.Errorf("Expected server1 ConnectionsCurrent to be 5, got %f", value1)
	}
	if value2 != 15 {
		t.Errorf("Expected server2 ConnectionsCurrent to be 15, got %f", value2)
	}
}

// TestConnectionsTotalMetric tests ConnectionsTotal counter
func TestConnectionsTotalMetric(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test adding to counter
	metrics.ConnectionsTotal.WithLabelValues("test-server").Add(100)

	value := testutil.ToFloat64(metrics.ConnectionsTotal.WithLabelValues("test-server"))
	if value != 100 {
		t.Errorf("Expected ConnectionsTotal to be 100, got %f", value)
	}

	// Test incrementing
	metrics.ConnectionsTotal.WithLabelValues("test-server").Add(50)

	value = testutil.ToFloat64(metrics.ConnectionsTotal.WithLabelValues("test-server"))
	if value != 150 {
		t.Errorf("Expected ConnectionsTotal to be 150 after increment, got %f", value)
	}
}

// TestStatisticsMetrics tests statistics counters
func TestStatisticsMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test bytes counter
	metrics.BytesTotal.WithLabelValues("test-server").Add(1024)
	bytesValue := testutil.ToFloat64(metrics.BytesTotal.WithLabelValues("test-server"))
	if bytesValue != 1024 {
		t.Errorf("Expected BytesTotal to be 1024, got %f", bytesValue)
	}

	// Test PDU counter
	metrics.PduTotal.WithLabelValues("test-server").Add(100)
	pduValue := testutil.ToFloat64(metrics.PduTotal.WithLabelValues("test-server"))
	if pduValue != 100 {
		t.Errorf("Expected PduTotal to be 100, got %f", pduValue)
	}

	// Test referrals counter
	metrics.ReferralsTotal.WithLabelValues("test-server").Add(10)
	referralsValue := testutil.ToFloat64(metrics.ReferralsTotal.WithLabelValues("test-server"))
	if referralsValue != 10 {
		t.Errorf("Expected ReferralsTotal to be 10, got %f", referralsValue)
	}

	// Test entries counter
	metrics.EntriesTotal.WithLabelValues("test-server").Add(500)
	entriesValue := testutil.ToFloat64(metrics.EntriesTotal.WithLabelValues("test-server"))
	if entriesValue != 500 {
		t.Errorf("Expected EntriesTotal to be 500, got %f", entriesValue)
	}
}

// TestThreadMetrics tests thread gauges
func TestThreadMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test max threads
	metrics.ThreadsMax.WithLabelValues("test-server").Set(16)
	maxValue := testutil.ToFloat64(metrics.ThreadsMax.WithLabelValues("test-server"))
	if maxValue != 16 {
		t.Errorf("Expected ThreadsMax to be 16, got %f", maxValue)
	}

	// Test active threads
	metrics.ThreadsActive.WithLabelValues("test-server").Set(8)
	activeValue := testutil.ToFloat64(metrics.ThreadsActive.WithLabelValues("test-server"))
	if activeValue != 8 {
		t.Errorf("Expected ThreadsActive to be 8, got %f", activeValue)
	}

	// Test pending threads
	metrics.ThreadsPending.WithLabelValues("test-server").Set(2)
	pendingValue := testutil.ToFloat64(metrics.ThreadsPending.WithLabelValues("test-server"))
	if pendingValue != 2 {
		t.Errorf("Expected ThreadsPending to be 2, got %f", pendingValue)
	}

	// Test open threads
	metrics.ThreadsOpen.WithLabelValues("test-server").Set(10)
	openValue := testutil.ToFloat64(metrics.ThreadsOpen.WithLabelValues("test-server"))
	if openValue != 10 {
		t.Errorf("Expected ThreadsOpen to be 10, got %f", openValue)
	}

	// Test starting threads
	metrics.ThreadsStarting.WithLabelValues("test-server").Set(1)
	startingValue := testutil.ToFloat64(metrics.ThreadsStarting.WithLabelValues("test-server"))
	if startingValue != 1 {
		t.Errorf("Expected ThreadsStarting to be 1, got %f", startingValue)
	}

	// Test backload
	metrics.ThreadsBackload.WithLabelValues("test-server").Set(5)
	backloadValue := testutil.ToFloat64(metrics.ThreadsBackload.WithLabelValues("test-server"))
	if backloadValue != 5 {
		t.Errorf("Expected ThreadsBackload to be 5, got %f", backloadValue)
	}

	// Test max pending
	metrics.ThreadsMaxPending.WithLabelValues("test-server").Set(20)
	maxPendingValue := testutil.ToFloat64(metrics.ThreadsMaxPending.WithLabelValues("test-server"))
	if maxPendingValue != 20 {
		t.Errorf("Expected ThreadsMaxPending to be 20, got %f", maxPendingValue)
	}
}

// TestOperationsMetrics tests operation counters
func TestOperationsMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test operations initiated
	operations := []string{"bind", "search", "add", "modify", "delete"}

	for i, op := range operations {
		expectedValue := float64((i + 1) * 10)
		metrics.OperationsInitiated.WithLabelValues("test-server", op).Add(expectedValue)

		value := testutil.ToFloat64(metrics.OperationsInitiated.WithLabelValues("test-server", op))
		if value != expectedValue {
			t.Errorf("Expected %s operations initiated to be %f, got %f", op, expectedValue, value)
		}
	}

	// Test operations completed
	for i, op := range operations {
		expectedValue := float64((i + 1) * 10)
		metrics.OperationsCompleted.WithLabelValues("test-server", op).Add(expectedValue)

		value := testutil.ToFloat64(metrics.OperationsCompleted.WithLabelValues("test-server", op))
		if value != expectedValue {
			t.Errorf("Expected %s operations completed to be %f, got %f", op, expectedValue, value)
		}
	}
}

// TestWaiterMetrics tests waiter gauges
func TestWaiterMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test read waiters
	metrics.WaitersRead.WithLabelValues("test-server").Set(3)
	readValue := testutil.ToFloat64(metrics.WaitersRead.WithLabelValues("test-server"))
	if readValue != 3 {
		t.Errorf("Expected WaitersRead to be 3, got %f", readValue)
	}

	// Test write waiters
	metrics.WaitersWrite.WithLabelValues("test-server").Set(2)
	writeValue := testutil.ToFloat64(metrics.WaitersWrite.WithLabelValues("test-server"))
	if writeValue != 2 {
		t.Errorf("Expected WaitersWrite to be 2, got %f", writeValue)
	}
}

// TestTimeMetrics tests time-related gauges
func TestTimeMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test server time
	serverTime := float64(1609459200) // 2021-01-01 00:00:00 UTC
	metrics.ServerTime.WithLabelValues("test-server").Set(serverTime)
	value := testutil.ToFloat64(metrics.ServerTime.WithLabelValues("test-server"))
	if value != serverTime {
		t.Errorf("Expected ServerTime to be %f, got %f", serverTime, value)
	}

	// Test server uptime
	uptime := float64(86400) // 1 day in seconds
	metrics.ServerUptime.WithLabelValues("test-server").Set(uptime)
	value = testutil.ToFloat64(metrics.ServerUptime.WithLabelValues("test-server"))
	if value != uptime {
		t.Errorf("Expected ServerUptime to be %f, got %f", uptime, value)
	}
}

// TestInfoMetrics tests info gauges
func TestInfoMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test overlays info
	metrics.OverlaysInfo.WithLabelValues("test-server", "syncprov", "enabled").Set(1)
	value := testutil.ToFloat64(metrics.OverlaysInfo.WithLabelValues("test-server", "syncprov", "enabled"))
	if value != 1 {
		t.Errorf("Expected OverlaysInfo to be 1, got %f", value)
	}

	// Test TLS info (3 labels: server, component, status)
	metrics.TlsInfo.WithLabelValues("test-server", "cipher", "TLS_AES_256_GCM_SHA384").Set(1)
	value = testutil.ToFloat64(metrics.TlsInfo.WithLabelValues("test-server", "cipher", "TLS_AES_256_GCM_SHA384"))
	if value != 1 {
		t.Errorf("Expected TlsInfo to be 1, got %f", value)
	}

	// Test backends info
	metrics.BackendsInfo.WithLabelValues("test-server", "mdb", "active").Set(1)
	value = testutil.ToFloat64(metrics.BackendsInfo.WithLabelValues("test-server", "mdb", "active"))
	if value != 1 {
		t.Errorf("Expected BackendsInfo to be 1, got %f", value)
	}

	// Test listeners info
	metrics.ListenersInfo.WithLabelValues("test-server", "ldap://0.0.0.0:389", "active").Set(1)
	value = testutil.ToFloat64(metrics.ListenersInfo.WithLabelValues("test-server", "ldap://0.0.0.0:389", "active"))
	if value != 1 {
		t.Errorf("Expected ListenersInfo to be 1, got %f", value)
	}
}

// TestDatabaseMetrics tests database metrics
func TestDatabaseMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test database entries (3 labels: server, base_dn, domain_component)
	metrics.DatabaseEntries.WithLabelValues("test-server", "dc=example,dc=com", "example.com").Set(10000)
	entries := testutil.ToFloat64(metrics.DatabaseEntries.WithLabelValues("test-server", "dc=example,dc=com", "example.com"))
	if entries != 10000 {
		t.Errorf("Expected DatabaseEntries to be 10000, got %f", entries)
	}
}

// TestHealthMetrics tests health and monitoring metrics
func TestHealthMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test health status
	metrics.HealthStatus.WithLabelValues("test-server").Set(1)
	health := testutil.ToFloat64(metrics.HealthStatus.WithLabelValues("test-server"))
	if health != 1 {
		t.Errorf("Expected HealthStatus to be 1, got %f", health)
	}

	// Test response time
	metrics.ResponseTime.WithLabelValues("test-server").Set(0.5)
	responseTime := testutil.ToFloat64(metrics.ResponseTime.WithLabelValues("test-server"))
	if responseTime != 0.5 {
		t.Errorf("Expected ResponseTime to be 0.5, got %f", responseTime)
	}

	// Test scrape errors
	metrics.ScrapeErrors.WithLabelValues("test-server").Add(5)
	errors := testutil.ToFloat64(metrics.ScrapeErrors.WithLabelValues("test-server"))
	if errors != 5 {
		t.Errorf("Expected ScrapeErrors to be 5, got %f", errors)
	}
}

// TestThreadStateMetrics tests thread state gauge with specific states
func TestThreadStateMetrics(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	states := []string{"active", "idle", "waiting", "running"}

	for i, state := range states {
		expectedValue := float64((i + 1) * 2)
		metrics.ThreadsState.WithLabelValues("test-server", state).Set(expectedValue)

		value := testutil.ToFloat64(metrics.ThreadsState.WithLabelValues("test-server", state))
		if value != expectedValue {
			t.Errorf("Expected thread state %s to be %f, got %f", state, expectedValue, value)
		}
	}
}

// TestMetricDescribe tests the Describe method
func TestMetricDescribe(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Create a channel to receive descriptions
	ch := make(chan *prometheus.Desc, 100)

	// Call Describe in a goroutine
	go func() {
		defer close(ch)
		metrics.Describe(ch)
	}()

	// Count the number of descriptions received
	descCount := 0
	for desc := range ch {
		if desc == nil {
			t.Error("Received nil description")
		}
		descCount++
	}

	// We should receive descriptions for all metrics
	// The struct has 28 metrics
	if descCount == 0 {
		t.Error("No descriptions received")
	}

	t.Logf("Received %d metric descriptions", descCount)
}

// TestMetricCollect tests the Collect method
func TestMetricCollect(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Set some test values
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Set(10)
	metrics.ConnectionsTotal.WithLabelValues("test-server").Add(100)
	metrics.BytesTotal.WithLabelValues("test-server").Add(1024)
	metrics.ThreadsActive.WithLabelValues("test-server").Set(5)
	metrics.HealthStatus.WithLabelValues("test-server").Set(1)

	// Create a channel to receive metrics
	ch := make(chan prometheus.Metric, 100)

	// Call Collect in a goroutine
	go func() {
		defer close(ch)
		metrics.Collect(ch)
	}()

	// Count the number of metrics received
	metricCount := 0
	for metric := range ch {
		if metric == nil {
			t.Error("Received nil metric")
		}
		metricCount++
	}

	// We should receive at least the metrics we set
	if metricCount < 5 {
		t.Errorf("Expected at least 5 metrics, got %d", metricCount)
	}

	t.Logf("Collected %d metrics", metricCount)
}

// TestMetricAsCollector tests that OpenLDAPMetrics implements prometheus.Collector
func TestMetricAsCollector(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test that metrics can be registered as a Collector
	registry := prometheus.NewRegistry()

	// Register the entire metrics struct as a collector
	err := registry.Register(metrics)
	if err != nil {
		t.Fatalf("Failed to register metrics as Collector: %v", err)
	}

	// Set some test values
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Set(20)
	metrics.HealthStatus.WithLabelValues("test-server").Set(1)

	// Gather metrics
	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	// We should get metric families for all the metrics in the struct
	if len(metricFamilies) == 0 {
		t.Error("No metric families gathered")
	}

	t.Logf("Gathered %d metric families", len(metricFamilies))
}

// TestMetricsConcurrency tests concurrent access to metrics
func TestMetricsConcurrency(t *testing.T) {
	metrics := NewOpenLDAPMetrics()
	done := make(chan bool)

	// Simulate concurrent metric updates
	go func() {
		for i := 0; i < 100; i++ {
			metrics.ConnectionsCurrent.WithLabelValues("server1").Set(float64(i))
			metrics.ConnectionsTotal.WithLabelValues("server1").Add(1)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			metrics.ConnectionsCurrent.WithLabelValues("server2").Set(float64(i))
			metrics.ConnectionsTotal.WithLabelValues("server2").Add(1)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			metrics.BytesTotal.WithLabelValues("server1").Add(100)
			metrics.EntriesTotal.WithLabelValues("server2").Add(10)
		}
		done <- true
	}()

	// Wait for all goroutines to complete
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify final values
	total1 := testutil.ToFloat64(metrics.ConnectionsTotal.WithLabelValues("server1"))
	total2 := testutil.ToFloat64(metrics.ConnectionsTotal.WithLabelValues("server2"))

	if total1 != 100 {
		t.Errorf("Expected server1 ConnectionsTotal to be 100, got %f", total1)
	}
	if total2 != 100 {
		t.Errorf("Expected server2 ConnectionsTotal to be 100, got %f", total2)
	}

	// Verify bytes total
	bytes1 := testutil.ToFloat64(metrics.BytesTotal.WithLabelValues("server1"))
	if bytes1 != 10000 {
		t.Errorf("Expected server1 BytesTotal to be 10000, got %f", bytes1)
	}

	// Verify entries total
	entries2 := testutil.ToFloat64(metrics.EntriesTotal.WithLabelValues("server2"))
	if entries2 != 1000 {
		t.Errorf("Expected server2 EntriesTotal to be 1000, got %f", entries2)
	}
}

// TestCounterIncrement tests that counters can only increase
func TestCounterIncrement(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Initialize counter
	metrics.ConnectionsTotal.WithLabelValues("test-server").Add(100)

	// Try to add more (should work)
	metrics.ConnectionsTotal.WithLabelValues("test-server").Add(50)

	value := testutil.ToFloat64(metrics.ConnectionsTotal.WithLabelValues("test-server"))
	if value != 150 {
		t.Errorf("Expected ConnectionsTotal to be 150, got %f", value)
	}

	// Note: Prometheus counters cannot decrease, they only increase
	// Attempting to Add negative values would panic in real Prometheus client
}

// TestGaugeSetAndInc tests gauge set and inc/dec operations
func TestGaugeSetAndInc(t *testing.T) {
	metrics := NewOpenLDAPMetrics()

	// Test Set
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Set(10)
	value := testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("test-server"))
	if value != 10 {
		t.Errorf("Expected ConnectionsCurrent to be 10, got %f", value)
	}

	// Test Inc
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Inc()
	value = testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("test-server"))
	if value != 11 {
		t.Errorf("Expected ConnectionsCurrent to be 11 after Inc, got %f", value)
	}

	// Test Dec
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Dec()
	value = testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("test-server"))
	if value != 10 {
		t.Errorf("Expected ConnectionsCurrent to be 10 after Dec, got %f", value)
	}

	// Test Add (for gauge)
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Add(5)
	value = testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("test-server"))
	if value != 15 {
		t.Errorf("Expected ConnectionsCurrent to be 15 after Add(5), got %f", value)
	}

	// Test Sub (negative Add)
	metrics.ConnectionsCurrent.WithLabelValues("test-server").Add(-3)
	value = testutil.ToFloat64(metrics.ConnectionsCurrent.WithLabelValues("test-server"))
	if value != 12 {
		t.Errorf("Expected ConnectionsCurrent to be 12 after Add(-3), got %f", value)
	}
}
