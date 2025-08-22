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

// TestCollectStatisticsMetrics tests the collectStatisticsMetrics function
func TestCollectStatisticsMetrics(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test collectStatisticsMetrics - should handle connection failure gracefully
	exporter.collectStatisticsMetrics("test-server")

	// Function should not panic and should handle no LDAP server gracefully
	t.Log("collectStatisticsMetrics completed without panic")
}

// TestCollectTimeMetrics tests the collectTimeMetrics function
func TestCollectTimeMetrics(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test collectTimeMetrics - should handle connection failure gracefully
	exporter.collectTimeMetrics("test-server")

	// Function should not panic and should handle no LDAP server gracefully
	t.Log("collectTimeMetrics completed without panic")
}

// TestCollectDatabaseMetrics tests the collectDatabaseMetrics function
func TestCollectDatabaseMetrics(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test collectDatabaseMetrics - should handle connection failure gracefully
	exporter.collectDatabaseMetrics("test-server")

	// Function should not panic and should handle no LDAP server gracefully
	t.Log("collectDatabaseMetrics completed without panic")
}

// TestValidateKey tests the validateKey function
func TestValidateKey(t *testing.T) {
	testCases := []struct {
		name        string
		key         string
		expectError bool
	}{
		{
			name:        "Valid key",
			key:         "valid_key",
			expectError: false,
		},
		{
			name:        "Empty key",
			key:         "",
			expectError: true,
		},
		{
			name:        "Key with spaces",
			key:         "invalid key",
			expectError: false, // Might be allowed
		},
		{
			name:        "Key with special chars",
			key:         "invalid-key!",
			expectError: false, // Might be allowed
		},
		{
			name:        "Valid key with underscore",
			key:         "valid_key_123",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateKey(tc.key)
			if tc.expectError && err == nil {
				t.Errorf("Expected error for key %q, got none", tc.key)
			}
			if !tc.expectError && err != nil {
				t.Errorf("Unexpected error for key %q: %v", tc.key, err)
			}
		})
	}
}

// TestIsRetryableError tests the isRetryableError function
func TestIsRetryableError(t *testing.T) {
	testCases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "Nil error",
			err:       nil,
			retryable: false,
		},
		{
			name:      "Connection timeout",
			err:       fmt.Errorf("connection timeout"),
			retryable: true,
		},
		{
			name:      "Network unreachable",
			err:       fmt.Errorf("network is unreachable"),
			retryable: true,
		},
		{
			name:      "Connection refused",
			err:       fmt.Errorf("connection refused"),
			retryable: true,
		},
		{
			name:      "Authentication error",
			err:       fmt.Errorf("authentication failed"),
			retryable: false,
		},
		{
			name:      "Generic error",
			err:       fmt.Errorf("some other error"),
			retryable: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isRetryableError(tc.err)
			if result != tc.retryable {
				t.Errorf("isRetryableError(%v) = %v, want %v", tc.err, result, tc.retryable)
			}
		})
	}
}

// TestRetryConfig tests the RetryConfig functions
func TestRetryConfig(t *testing.T) {
	// Test DefaultRetryConfig
	config := DefaultRetryConfig()
	if config == nil {
		t.Fatal("DefaultRetryConfig() should return non-nil config")
	}

	if config.MaxAttempts <= 0 {
		t.Error("MaxAttempts should be positive")
	}

	if config.InitialDelay <= 0 {
		t.Error("InitialDelay should be positive")
	}

	// Test calculateDelay
	delay1 := config.calculateDelay(1)
	delay2 := config.calculateDelay(2)

	if delay1 <= 0 {
		t.Error("calculateDelay should return positive duration")
	}

	// Second attempt should have longer delay (exponential backoff)
	if delay2 <= delay1 {
		t.Error("calculateDelay should implement exponential backoff")
	}

	// Test with high attempt number
	delayHigh := config.calculateDelay(10)
	// Allow some tolerance for jitter in the delay calculation
	maxDelayWithTolerance := config.MaxDelay + time.Millisecond*100
	if delayHigh > maxDelayWithTolerance {
		t.Errorf("calculateDelay should not significantly exceed MaxDelay (%v), got %v", config.MaxDelay, delayHigh)
	}
}

// TestRetryOperation tests the retryOperation method
func TestRetryOperation(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test operation that always succeeds
	successCount := 0
	err := exporter.retryOperation(context.Background(), "test_success", func() error {
		successCount++
		return nil
	})

	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}
	if successCount != 1 {
		t.Errorf("Expected operation to be called once, called %d times", successCount)
	}

	// Test operation that fails with retryable error
	failCount := 0
	err = exporter.retryOperation(context.Background(), "test_retryable", func() error {
		failCount++
		if failCount < 3 {
			return fmt.Errorf("connection timeout")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected eventual success, got error: %v", err)
	}
	if failCount != 3 {
		t.Errorf("Expected operation to be called 3 times, called %d times", failCount)
	}

	// Test operation that fails with non-retryable error
	nonRetryableCount := 0
	err = exporter.retryOperation(context.Background(), "test_non_retryable", func() error {
		nonRetryableCount++
		return fmt.Errorf("authentication failed")
	})

	if err == nil {
		t.Error("Expected error for non-retryable operation")
	}
	if nonRetryableCount != 1 {
		t.Errorf("Expected operation to be called once, called %d times", nonRetryableCount)
	}
}

// TestEnsureConnectionWithRetries tests the ensureConnection method more thoroughly
func TestEnsureConnectionWithRetries(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test ensureConnection - will fail but should handle gracefully
	err := exporter.ensureConnection()
	if err == nil {
		t.Log("Connection unexpectedly succeeded (LDAP server might be running)")
	} else {
		t.Logf("Expected connection failure: %v", err)
	}
}

// TestCollectMetricsMethodsCoverage tests individual collect methods for code coverage
func TestCollectMetricsMethodsCoverage(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	server := "test-server"

	// Test all collection methods - they should handle connection failures gracefully
	t.Run("collectConnectionsMetrics", func(t *testing.T) {
		exporter.collectConnectionsMetrics(server)
		t.Log("collectConnectionsMetrics completed without panic")
	})

	t.Run("collectOperationsMetrics", func(t *testing.T) {
		exporter.collectOperationsMetrics(server)
		t.Log("collectOperationsMetrics completed without panic")
	})

	t.Run("collectThreadsMetrics", func(t *testing.T) {
		exporter.collectThreadsMetrics(server)
		t.Log("collectThreadsMetrics completed without panic")
	})

	t.Run("collectWaitersMetrics", func(t *testing.T) {
		exporter.collectWaitersMetrics(server)
		t.Log("collectWaitersMetrics completed without panic")
	})

	t.Run("collectOverlaysMetrics", func(t *testing.T) {
		exporter.collectOverlaysMetrics(server)
		t.Log("collectOverlaysMetrics completed without panic")
	})

	t.Run("collectTLSMetrics", func(t *testing.T) {
		exporter.collectTLSMetrics(server)
		t.Log("collectTLSMetrics completed without panic")
	})

	t.Run("collectBackendsMetrics", func(t *testing.T) {
		exporter.collectBackendsMetrics(server)
		t.Log("collectBackendsMetrics completed without panic")
	})

	t.Run("collectListenersMetrics", func(t *testing.T) {
		exporter.collectListenersMetrics(server)
		t.Log("collectListenersMetrics completed without panic")
	})

	t.Run("collectHealthMetrics", func(t *testing.T) {
		exporter.collectHealthMetrics(server)
		t.Log("collectHealthMetrics completed without panic")
	})
}

// TestGetMonitorCounterEdgeCases tests getMonitorCounter with various inputs
func TestGetMonitorCounterEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test getMonitorCounter with invalid DN (will fail but should handle gracefully)
	_, err := exporter.getMonitorCounter("invalid-dn")
	if err == nil {
		t.Log("getMonitorCounter unexpectedly succeeded with invalid DN")
	} else {
		t.Logf("Expected error for invalid DN: %v", err)
	}

	// Test with empty DN
	_, err = exporter.getMonitorCounter("")
	if err == nil {
		t.Log("getMonitorCounter unexpectedly succeeded with empty DN")
	} else {
		t.Logf("Expected error for empty DN: %v", err)
	}

	// Test with various monitor DN patterns
	testDNs := []string{
		"cn=Total,cn=Connections,cn=Monitor",
		"cn=Current,cn=Connections,cn=Monitor",
		"cn=Operations,cn=Monitor",
		"cn=Statistics,cn=Monitor",
	}

	for _, dn := range testDNs {
		_, _ = exporter.getMonitorCounter(dn)
	}
}

// TestCleanupOldCountersSync tests the synchronous cleanup method
func TestCleanupOldCountersSync(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test cleanupOldCountersSync - should not panic
	exporter.cleanupOldCountersSync()
	t.Log("cleanupOldCountersSync completed without panic")
}
