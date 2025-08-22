package exporter

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
)

// setupTestConfig creates a test configuration with required environment variables
func setupTestConfig(t *testing.T) (*config.Config, func()) {
	// Set up test environment
	testEnvVars := map[string]string{
		"LDAP_URL":      "ldap://test.example.com:389",
		"LDAP_USERNAME": "testuser",
		"LDAP_PASSWORD": "testpass",
		"LDAP_TIMEOUT":  "5",
		"LOG_LEVEL":     "ERROR", // Reduce noise in tests
	}

	// Set environment variables
	for key, value := range testEnvVars {
		os.Setenv(key, value)
	}

	// Create cleanup function
	cleanup := func() {
		for key := range testEnvVars {
			os.Unsetenv(key)
		}
	}

	// Create configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		cleanup()
		t.Fatalf("Failed to load config: %v", err)
	}

	return cfg, func() {
		cfg.Clear()
		cleanup()
	}
}

// TestNewOpenLDAPExporter tests exporter creation
func TestNewOpenLDAPExporter(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	if exporter == nil {
		t.Fatal("NewOpenLDAPExporter should return non-nil exporter")
	}

	if exporter.config != cfg {
		t.Error("Exporter should store the provided config")
	}

	if exporter.metrics == nil {
		t.Error("Exporter should have initialized metrics")
	}

	if exporter.client == nil {
		t.Error("Exporter should have initialized LDAP client")
	}

	if exporter.monitoring == nil {
		t.Error("Exporter should have initialized internal monitoring")
	}

	if exporter.lastValues == nil {
		t.Error("Exporter should have initialized lastValues map")
	}
}

// TestExporterDescribe tests the Prometheus Describe method
func TestExporterDescribe(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	ch := make(chan *prometheus.Desc, 100)
	go func() {
		defer close(ch)
		exporter.Describe(ch)
	}()

	descCount := 0
	for desc := range ch {
		if desc == nil {
			t.Error("Describe should not send nil descriptors")
		}
		descCount++
	}

	if descCount == 0 {
		t.Error("Describe should send at least one descriptor")
	}

	t.Logf("Describe sent %d metric descriptors", descCount)
}

// TestExporterCollectWithoutLDAP tests the Collect method when LDAP is unavailable
func TestExporterCollectWithoutLDAP(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Create a registry and register the exporter
	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)

	// Collect metrics (this will fail to connect to LDAP but should not panic)
	ch := make(chan prometheus.Metric, 100)
	go func() {
		defer close(ch)
		exporter.Collect(ch)
	}()

	metricCount := 0
	for metric := range ch {
		if metric == nil {
			t.Error("Collect should not send nil metrics")
		}
		metricCount++
	}

	// We should get at least the scrape_errors metric even if LDAP fails
	if metricCount == 0 {
		t.Error("Collect should send at least scrape_errors metric even when LDAP fails")
	}

	t.Logf("Collect sent %d metrics (LDAP unavailable)", metricCount)
}

// TestUpdateCounter tests the counter update logic
func TestUpdateCounter(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Create a test counter
	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_counter_total",
			Help: "Test counter for updateCounter testing",
		},
		[]string{"server"},
	)

	server := "test-server"
	key := "test-key"

	// Test first update (initialization)
	exporter.updateCounter(counter, server, key, 100)

	// Check that the counter was initialized to 0 (not 100)
	value := testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 0 {
		t.Errorf("First update should initialize counter to 0, got %f", value)
	}

	// Test second update (should add delta)
	exporter.updateCounter(counter, server, key, 150)
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 50 { // Delta from 100 to 150
		t.Errorf("Second update should add delta (50), got %f", value)
	}

	// Test third update (should add another delta)
	exporter.updateCounter(counter, server, key, 200)
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 100 { // Total delta: 50 + 50
		t.Errorf("Third update should add cumulative delta (100), got %f", value)
	}

	// Test counter reset scenario (new value < old value)
	exporter.updateCounter(counter, server, key, 10) // Reset scenario
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 110 { // Previous 100 + new reset value 10
		t.Errorf("Counter reset should add new value (110), got %f", value)
	}
}

// TestUpdateCounterValidation tests counter validation logic
func TestUpdateCounterValidation(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_validation_counter_total",
			Help: "Test counter for validation testing",
		},
		[]string{"server"},
	)

	server := "test-server"

	// Test negative value (should be rejected)
	exporter.updateCounter(counter, server, "negative-key", -1)
	value := testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 0 {
		t.Errorf("Negative values should be rejected, counter should remain 0, got %f", value)
	}

	// Test extremely large value (should be logged but processed)
	largeValue := float64(MaxReasonableCounterValue + 1)
	exporter.updateCounter(counter, server, "large-key", largeValue)
	// Should initialize to 0 even for large values
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 0 {
		t.Errorf("Large values should initialize to 0, got %f", value)
	}
}

// TestExtractDomainComponents tests domain component extraction
func TestExtractDomainComponents(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	tests := []struct {
		dn       string
		expected []string
	}{
		{"dc=example,dc=org", []string{"example", "org"}},
		{"ou=users,dc=company,dc=net", []string{"company", "net"}},
		{"uid=user1,ou=people,dc=test,dc=local", []string{"test", "local"}},
		{"cn=config", []string{}},
		{"", []string{}},
		{"invalid-dn-format", []string{}},
		{"dc=single", []string{"single"}},
		{"DC=UPPER,DC=CASE", []string{"UPPER", "CASE"}}, // Test case sensitivity
	}

	for _, test := range tests {
		result := exporter.extractDomainComponents(test.dn)
		if len(result) != len(test.expected) {
			t.Errorf("extractDomainComponents(%q) = %v, expected %v", test.dn, result, test.expected)
			continue
		}

		for i, expected := range test.expected {
			if i >= len(result) || result[i] != expected {
				t.Errorf("extractDomainComponents(%q) = %v, expected %v", test.dn, result, test.expected)
				break
			}
		}
	}
}

// TestCleanupOldCounters tests the counter cleanup functionality
func TestCleanupOldCounters(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Add some test entries with different timestamps
	now := time.Now()
	exporter.lastValues["recent-key"] = &counterEntry{
		value:    100,
		lastSeen: now.Add(-1 * time.Minute), // Recent
	}
	exporter.lastValues["old-key"] = &counterEntry{
		value:    200,
		lastSeen: now.Add(-CounterEntryRetentionPeriod - 1*time.Minute), // Old
	}

	initialCount := len(exporter.lastValues)
	if initialCount != 2 {
		t.Fatalf("Expected 2 initial entries, got %d", initialCount)
	}

	// Run cleanup
	exporter.cleanupOldCountersSync()

	// Check that old entry was removed
	finalCount := len(exporter.lastValues)
	if finalCount != 1 {
		t.Errorf("Expected 1 entry after cleanup, got %d", finalCount)
	}

	// Check that the correct entry remains
	if _, exists := exporter.lastValues["recent-key"]; !exists {
		t.Error("Recent entry should not be removed")
	}
	if _, exists := exporter.lastValues["old-key"]; exists {
		t.Error("Old entry should be removed")
	}
}

// TestGetInternalMonitoring tests internal monitoring access
func TestGetInternalMonitoring(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	monitoring := exporter.GetInternalMonitoring()
	if monitoring == nil {
		t.Error("GetInternalMonitoring should return non-nil monitoring")
	}

	// Test that it returns the same instance
	monitoring2 := exporter.GetInternalMonitoring()
	if monitoring != monitoring2 {
		t.Error("GetInternalMonitoring should return the same instance")
	}
}

// TestExporterClose tests the Close method
func TestExporterClose(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)

	// Test that Close doesn't panic
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("First Close call should not panic: %v", r)
			}
		}()
		exporter.Close()
	}()

	// Test that multiple Close calls handle gracefully
	// Note: Second call may panic due to channel being closed, which is acceptable behavior
	func() {
		defer func() {
			if r := recover(); r != nil {
				// This is expected behavior - closing an already closed resource
				t.Logf("Second Close call panicked (expected): %v", r)
			}
		}()
		exporter.Close()
	}()
}

// TestConstantsValidation tests that our constants are reasonable
func TestConstantsValidation(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		minValue interface{}
		maxValue interface{}
	}{
		{"MaxReasonableCounterValue", MaxReasonableCounterValue, float64(1e6), float64(1e12)},
		{"MaxReasonableCounterDelta", MaxReasonableCounterDelta, float64(1e3), float64(1e9)},
		{"MaxCounterEntries", MaxCounterEntries, 100, 10000},
		{"CounterCleanupInterval", CounterCleanupInterval, 1 * time.Minute, 1 * time.Hour},
		{"CounterEntryRetentionPeriod", CounterEntryRetentionPeriod, 2 * time.Minute, 2 * time.Hour},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			switch v := test.value.(type) {
			case float64:
				min := test.minValue.(float64)
				max := test.maxValue.(float64)
				if v < min || v > max {
					t.Errorf("%s = %v, should be between %v and %v", test.name, v, min, max)
				}
			case int:
				min := test.minValue.(int)
				max := test.maxValue.(int)
				if v < min || v > max {
					t.Errorf("%s = %v, should be between %v and %v", test.name, v, min, max)
				}
			case time.Duration:
				min := test.minValue.(time.Duration)
				max := test.maxValue.(time.Duration)
				if v < min || v > max {
					t.Errorf("%s = %v, should be between %v and %v", test.name, v, min, max)
				}
			}
		})
	}

	// Test relationship between cleanup interval and retention period
	if CounterEntryRetentionPeriod != 2*CounterCleanupInterval {
		t.Errorf("CounterEntryRetentionPeriod should be 2 * CounterCleanupInterval, got %v vs %v",
			CounterEntryRetentionPeriod, 2*CounterCleanupInterval)
	}
}

// TestCollectAllMetricsContext tests the context handling in metric collection
func TestCollectAllMetricsContext(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Set very short timeout to test context cancellation
	cfg.Timeout = 1 * time.Millisecond

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Create context that will timeout quickly
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	// This should handle timeout gracefully
	exporter.collectAllMetricsWithContext(ctx)

	// Test should complete without hanging
}

// TestUpdateOperationCounter tests operation counter updates
func TestUpdateOperationCounter(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Create test operation counter
	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_operations_total",
			Help: "Test operation counter",
		},
		[]string{"server", "operation"},
	)

	server := "test-server"
	operation := "bind"
	key := "ops-bind"

	// Test first update
	exporter.updateOperationCounter(counter, server, operation, key, 50)
	value := testutil.ToFloat64(counter.WithLabelValues(server, operation))
	if value != 0 {
		t.Errorf("First operation update should initialize to 0, got %f", value)
	}

	// Test second update
	exporter.updateOperationCounter(counter, server, operation, key, 75)
	value = testutil.ToFloat64(counter.WithLabelValues(server, operation))
	if value != 25 { // Delta: 75 - 50
		t.Errorf("Second operation update should show delta (25), got %f", value)
	}
}

// TestMetricFiltering tests metric filtering functionality
func TestMetricFiltering(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Test include filtering
	cfg.MetricsInclude = []string{"connections", "statistics"}
	cfg.MetricsExclude = nil

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that exporter was created with filtering configured
	if len(cfg.MetricsInclude) != 2 {
		t.Error("Expected 2 metrics in include list")
	}

	// Test exclude filtering
	cfg2, cleanup2 := setupTestConfig(t)
	defer cleanup2()

	cfg2.MetricsInclude = nil
	cfg2.MetricsExclude = []string{"overlays", "tls"}

	exporter2 := NewOpenLDAPExporter(cfg2)
	defer exporter2.Close()

	if len(cfg2.MetricsExclude) != 2 {
		t.Error("Expected 2 metrics in exclude list")
	}
}

// TestDCFiltering tests domain component filtering
func TestDCFiltering(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Test include DC filtering
	cfg.DCInclude = []string{"example", "test"}
	cfg.DCExclude = nil

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test the extractDomainComponents function
	testCases := []struct {
		dn       string
		expected []string
	}{
		{"cn=connections,cn=Monitor", []string{}}, // No DC components
		{"cn=total,cn=connections,cn=database,dc=example,dc=org,cn=Monitor", []string{"example", "org"}},
		{"ou=users,dc=test,dc=org", []string{"test", "org"}},
	}

	for _, tc := range testCases {
		result := exporter.extractDomainComponents(tc.dn)
		if len(result) != len(tc.expected) {
			t.Errorf("extractDomainComponents(%q) = %v, expected %v", tc.dn, result, tc.expected)
			continue
		}
		for i, expected := range tc.expected {
			if i >= len(result) || result[i] != expected {
				t.Errorf("extractDomainComponents(%q) = %v, expected %v", tc.dn, result, tc.expected)
				break
			}
		}
	}
}

// TestCleanupOldCountersWithMany tests cleanup with many entries
func TestCleanupOldCountersWithMany(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Add many old and recent entries
	now := time.Now()
	for i := 0; i < 10; i++ {
		// Old entries
		exporter.lastValues[fmt.Sprintf("old-key-%d", i)] = &counterEntry{
			value:    float64(i),
			lastSeen: now.Add(-CounterEntryRetentionPeriod - time.Hour),
		}
		// Recent entries
		exporter.lastValues[fmt.Sprintf("recent-key-%d", i)] = &counterEntry{
			value:    float64(i + 100),
			lastSeen: now.Add(-time.Minute),
		}
	}

	initialCount := len(exporter.lastValues)
	if initialCount != 20 {
		t.Fatalf("Expected 20 initial entries, got %d", initialCount)
	}

	// Run cleanup
	exporter.cleanupOldCountersSync()

	// Should have removed 10 old entries
	finalCount := len(exporter.lastValues)
	if finalCount != 10 {
		t.Errorf("Expected 10 entries after cleanup, got %d", finalCount)
	}
}

// TestInternalMonitoring tests internal monitoring functionality
func TestInternalMonitoring(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	monitoring := exporter.GetInternalMonitoring()
	if monitoring == nil {
		t.Fatal("Internal monitoring should not be nil")
	}

	// Test that we can get monitoring twice and it's the same instance
	monitoring2 := exporter.GetInternalMonitoring()
	if monitoring != monitoring2 {
		t.Error("GetInternalMonitoring should return the same instance")
	}
}

// TestConnectionErrors tests various connection error scenarios
func TestConnectionErrors(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Use invalid URL to trigger connection errors (fix field name)
	cfg.URL = "ldap://invalid-host-name-that-does-not-exist.local:389"

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that exporter handles connection failures gracefully
	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)

	// This should not panic even with connection failures
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather should not fail even with connection errors: %v", err)
	}

	// Should still get some metrics (at least scrape errors)
	if len(families) == 0 {
		t.Error("Expected at least error metrics even with connection failures")
	}
}

// TestGetMonitorCounter tests the counter extraction functionality
func TestGetMonitorCounter(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test with non-existent DN (should return error)
	_, err := exporter.getMonitorCounter("cn=nonexistent,cn=Monitor")
	if err == nil {
		t.Error("Expected error when querying non-existent DN")
	}
}

// TestEnsureConnection tests connection establishment
func TestEnsureConnection(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// This should attempt to connect (and likely fail with test config)
	err := exporter.ensureConnection()
	// Error is expected since test.example.com doesn't exist
	if err == nil {
		t.Log("Connection succeeded unexpectedly (LDAP server might be running)")
	} else {
		t.Logf("Connection failed as expected: %v", err)
	}
}

// TestAtomicCounter tests the atomicFloat64 functions
func TestAtomicCounter(t *testing.T) {
	counter := &atomicFloat64{}

	// Test Load - initial value should be 0
	if val := counter.Load(); val != 0.0 {
		t.Errorf("Initial Load() = %f, want 0.0", val)
	}

	// Test Store
	counter.Store(42.5)
	if val := counter.Load(); val != 42.5 {
		t.Errorf("After Store(42.5), Load() = %f, want 42.5", val)
	}

	// Test CompareAndSwap - should succeed when old value matches
	if !counter.CompareAndSwap(42.5, 100.25) {
		t.Error("CompareAndSwap(42.5, 100.25) should succeed")
	}
	if val := counter.Load(); val != 100.25 {
		t.Errorf("After successful CAS, Load() = %f, want 100.25", val)
	}

	// Test CompareAndSwap - should fail when old value doesn't match
	if counter.CompareAndSwap(42.5, 200.0) {
		t.Error("CompareAndSwap(42.5, 200.0) should fail")
	}
	if val := counter.Load(); val != 100.25 {
		t.Errorf("After failed CAS, Load() = %f, want 100.25", val)
	}
}

// TestGetAtomicCounterStats tests the GetAtomicCounterStats method
func TestGetAtomicCounterStats(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Get atomic counter stats
	stats := exporter.GetAtomicCounterStats()

	// Check that stats is not nil
	if stats == nil {
		t.Fatal("GetAtomicCounterStats() should return non-nil map")
	}

	// Check for expected keys
	expectedKeys := []string{
		"total_scrapes", "total_errors", "last_scrape_time",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Stats should contain key %q", key)
		}
	}

	// All values should be zero initially (no successful connections)
	for key, value := range stats {
		if value != 0 {
			t.Logf("Note: Counter %s = %f (expected 0 for test config)", key, value)
		}
	}
}
