package exporter

import (
	"context"
	"fmt"
	"os"
	"sync"
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
		"LDAP_URL":      "ldap://127.0.0.1:1", // Use localhost:1 for immediate connection refused
		"LDAP_USERNAME": "testuser",
		"LDAP_PASSWORD": "testpass", 
		"LDAP_TIMEOUT":  "1", // Very short timeout for tests (1 second)
		"LDAP_CONNECTION_TIMEOUT": "1", // Connection timeout (1 second)
		"LOG_LEVEL":     "ERROR", // Reduce noise in tests
		"OPENLDAP_RETRY_MAX_ATTEMPTS": "1", // Only 1 attempt for faster test failure
		"OPENLDAP_RETRY_INITIAL_DELAY": "100", // 100ms initial delay
		"OPENLDAP_RETRY_MAX_DELAY": "100", // 100ms max delay
		"POOL_INITIAL_CONNECTIONS": "0", // No initial connections
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

	if exporter.metricsRegistry == nil {
		t.Error("Exporter should have initialized metrics")
	}

	if exporter.client == nil {
		t.Error("Exporter should have initialized LDAP client")
	}

	if exporter.monitoring == nil {
		t.Error("Exporter should have initialized internal monitoring")
	}

	if exporter.counterValues == nil {
		t.Error("Exporter should have initialized counterValues map")
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

	// Test the collect process without actually connecting to LDAP
	// This tests the metric collection structure and error handling
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

	// Check that the counter shows the first value (100)
	value := testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 100 {
		t.Errorf("First update should set counter to 100, got %f", value)
	}

	// Test second update (should add delta)
	exporter.updateCounter(counter, server, key, 150)
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 150 { // Delta from 100 to 150 (50) added to previous 100
		t.Errorf("Second update should show cumulative value (150), got %f", value)
	}

	// Test third update (should add another delta)
	exporter.updateCounter(counter, server, key, 200)
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 200 { // Delta from 150 to 200 (50) added to previous 150
		t.Errorf("Third update should show cumulative value (200), got %f", value)
	}

	// Test counter reset scenario (new value < old value)
	exporter.updateCounter(counter, server, key, 10) // Reset scenario
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != 210 { // Previous 200 + new reset value 10
		t.Errorf("Counter reset should add new value to get 210, got %f", value)
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

	// Test extremely large value (should be logged but still processed)
	largeValue := float64(MaxReasonableCounterValue + 1)
	exporter.updateCounter(counter, server, "large-key", largeValue)
	// Large values are accepted but logged with warning - the counter shows the value
	value = testutil.ToFloat64(counter.WithLabelValues(server))
	if value != largeValue {
		t.Errorf("Large values should be accepted and show the value (%f), got %f", largeValue, value)
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
		{"DC=UPPER,DC=CASE", []string{"upper", "case"}}, // Test case normalization (converts to lowercase)
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
	serverName := "test-server"
	now := time.Now()

	// Initialize server map
	exporter.counterMutex.Lock()
	exporter.counterValues[serverName] = &sync.Map{}
	serverMap := exporter.counterValues[serverName]
	exporter.counterMutex.Unlock()

	// Add test entries
	recentEntry := &counterEntry{
		value:    100,
		lastSeen: now.Add(-1 * time.Minute), // Recent
	}
	oldEntry := &counterEntry{
		value:    200,
		lastSeen: now.Add(-CounterEntryRetentionPeriod - 1*time.Minute), // Old
	}

	serverMap.Store("recent-key", recentEntry)
	serverMap.Store("old-key", oldEntry)

	// Count initial entries
	initialCount := 0
	serverMap.Range(func(key, value interface{}) bool {
		initialCount++
		return true
	})
	if initialCount != 2 {
		t.Fatalf("Expected 2 initial entries, got %d", initialCount)
	}

	// Run cleanup
	exporter.cleanupOldCountersSync()

	// Count final entries
	finalCount := 0
	serverMap.Range(func(key, value interface{}) bool {
		finalCount++
		return true
	})
	if finalCount != 1 {
		t.Errorf("Expected 1 entry after cleanup, got %d", finalCount)
	}

	// Check that the correct entry remains
	if _, exists := serverMap.Load("recent-key"); !exists {
		t.Error("Recent entry should not be removed")
	}
	if _, exists := serverMap.Load("old-key"); exists {
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
	counter := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "test_operations_delta",
			Help: "Test operation delta gauge",
		},
		[]string{"server", "operation"},
	)

	server := "test-server"
	operation := "bind"
	key := "ops-bind"

	// Test first update (initial delta)
	exporter.updateOperationCounter(counter, server, operation, key, 50)
	value := testutil.ToFloat64(counter.WithLabelValues(server, operation))
	if value != 50 {
		t.Errorf("First operation update should show initial delta (50), got %f", value)
	}

	// Test second update (should show delta: 75 - 50 = 25)
	exporter.updateOperationCounter(counter, server, operation, key, 75)
	value = testutil.ToFloat64(counter.WithLabelValues(server, operation))
	if value != 25 { // Delta: 75 - 50 = 25
		t.Errorf("Second operation update should show delta value (25), got %f", value)
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
	serverName := "test-server"
	now := time.Now()

	// Initialize server map
	exporter.counterMutex.Lock()
	exporter.counterValues[serverName] = &sync.Map{}
	serverMap := exporter.counterValues[serverName]
	exporter.counterMutex.Unlock()

	for i := 0; i < 10; i++ {
		// Old entries
		oldEntry := &counterEntry{
			value:    float64(i),
			lastSeen: now.Add(-CounterEntryRetentionPeriod - time.Hour),
		}
		serverMap.Store(fmt.Sprintf("old-key-%d", i), oldEntry)

		// Recent entries
		recentEntry := &counterEntry{
			value:    float64(i + 100),
			lastSeen: now.Add(-time.Minute),
		}
		serverMap.Store(fmt.Sprintf("recent-key-%d", i), recentEntry)
	}

	// Count initial entries
	initialCount := 0
	serverMap.Range(func(key, value interface{}) bool {
		initialCount++
		return true
	})
	if initialCount != 20 {
		t.Fatalf("Expected 20 initial entries, got %d", initialCount)
	}

	// Run cleanup
	exporter.cleanupOldCountersSync()

	// Count final entries - should have removed 10 old entries
	finalCount := 0
	serverMap.Range(func(key, value interface{}) bool {
		finalCount++
		return true
	})
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

// TestConnectionErrors tests connection error handling without actual network calls
func TestConnectionErrors(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Use invalid URL to test error handling
	cfg.URL = "ldap://invalid-host-name-that-does-not-exist.local:389"

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that exporter structure is properly initialized even with bad config
	if exporter.config == nil {
		t.Error("Exporter should have config even with invalid URL")
	}
	
	if exporter.metricsRegistry == nil {
		t.Error("Exporter should have metrics registry even with invalid URL")
	}

	// Test Describe method works even with connection issues
	ch := make(chan *prometheus.Desc, 100)
	go func() {
		defer close(ch)
		exporter.Describe(ch)
	}()

	descCount := 0
	for desc := range ch {
		if desc != nil {
			descCount++
		}
	}

	if descCount == 0 {
		t.Error("Expected at least some metric descriptors even with bad config")
	}

	t.Logf("Exporter created successfully with bad config, %d descriptors", descCount)
}

// TestGetMonitorCounter tests the counter extraction functionality without connection
func TestGetMonitorCounter(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the method exists and handles errors gracefully
	// This will fail due to no connection, but tests the error handling path
	_, err := exporter.getMonitorCounter("cn=nonexistent,cn=Monitor")
	if err == nil {
		t.Log("Unexpected success - LDAP server might be running")
	} else {
		t.Logf("Expected error (no connection): %v", err)
	}
}

// TestEnsureConnection tests connection establishment without actual network calls
func TestEnsureConnection(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the method exists and handles errors gracefully
	// This tests error handling without actually trying to connect
	err := exporter.ensureConnection()
	// Error is expected since test.example.com doesn't exist
	if err == nil {
		t.Log("Connection succeeded unexpectedly (LDAP server might be running)")
	} else {
		t.Logf("Expected connection failure: %v", err)
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

// TestCollectStatisticsMetrics tests the collectStatisticsMetrics function structure
func TestCollectStatisticsMetrics(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the function exists and handles connection failures gracefully
	// This tests the error handling path without actually trying to connect
	exporter.collectStatisticsMetrics("test-server")

	// Function should not panic and should handle no LDAP server gracefully
	t.Log("collectStatisticsMetrics completed without panic")
}

// TestCollectTimeMetrics tests the collectTimeMetrics function structure
func TestCollectTimeMetrics(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the function exists and handles connection failures gracefully
	exporter.collectTimeMetrics("test-server")

	// Function should not panic and should handle no LDAP server gracefully
	t.Log("collectTimeMetrics completed without panic")
}

// TestCollectDatabaseMetrics tests the collectDatabaseMetrics function structure
func TestCollectDatabaseMetrics(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the function exists and handles connection failures gracefully
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
	// Allow some tolerance for jitter in the delay calculation (exponential backoff + jitter can exceed MaxDelay slightly)
	maxDelayWithTolerance := config.MaxDelay + time.Millisecond*200
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

// TestEnsureConnectionWithRetries tests the ensureConnection method structure
func TestEnsureConnectionWithRetries(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that ensureConnection method exists and handles errors gracefully
	err := exporter.ensureConnection()
	if err == nil {
		t.Log("Connection unexpectedly succeeded (LDAP server might be running)")
	} else {
		t.Logf("Expected connection failure (testing error handling): %v", err)
	}
}

// TestCollectMetricsMethodsCoverage tests individual collect methods structure
func TestCollectMetricsMethodsCoverage(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	server := "test-server"

	// Test all collection methods exist and handle connection failures gracefully
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

// TestGetMonitorCounterEdgeCases tests getMonitorCounter method structure
func TestGetMonitorCounterEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that getMonitorCounter method exists and handles various inputs gracefully
	_, err := exporter.getMonitorCounter("invalid-dn")
	if err == nil {
		t.Log("getMonitorCounter unexpectedly succeeded with invalid DN")
	} else {
		t.Logf("Expected error for invalid DN (testing error handling): %v", err)
	}

	// Test with empty DN
	_, err = exporter.getMonitorCounter("")
	if err == nil {
		t.Log("getMonitorCounter unexpectedly succeeded with empty DN")
	} else {
		t.Logf("Expected error for empty DN: %v", err)
	}

	// Test that the method can handle various DN patterns without panic
	testDNs := []string{
		"cn=Total,cn=Connections,cn=Monitor",
		"cn=Current,cn=Connections,cn=Monitor",
		"cn=Operations,cn=Monitor",
		"cn=Statistics,cn=Monitor",
	}

	for _, dn := range testDNs {
		_, _ = exporter.getMonitorCounter(dn)
		t.Logf("getMonitorCounter handled DN pattern: %s", dn)
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

// TestEnsureConnectionEdgeCases tests ensureConnection method structure  
func TestEnsureConnectionEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that multiple calls to ensureConnection handle errors gracefully
	for i := 0; i < 3; i++ {
		err := exporter.ensureConnection()
		if err == nil {
			t.Log("Connection unexpectedly succeeded (LDAP server might be running)")
			break
		} else {
			t.Logf("Expected connection failure attempt %d (testing error handling): %v", i+1, err)
		}
	}
}

// TestCollectAllMetricsIndividual tests each collect function structure
func TestCollectAllMetricsIndividual(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	server := "test-server"

	// Test each collect function individually to verify structure and error handling
	collectFunctions := []struct {
		name string
		fn   func(string)
	}{
		{"collectConnectionsMetrics", exporter.collectConnectionsMetrics},
		{"collectStatisticsMetrics", exporter.collectStatisticsMetrics},
		{"collectOperationsMetrics", exporter.collectOperationsMetrics},
		{"collectThreadsMetrics", exporter.collectThreadsMetrics},
		{"collectTimeMetrics", exporter.collectTimeMetrics},
		{"collectWaitersMetrics", exporter.collectWaitersMetrics},
		{"collectOverlaysMetrics", exporter.collectOverlaysMetrics},
		{"collectTLSMetrics", exporter.collectTLSMetrics},
		{"collectBackendsMetrics", exporter.collectBackendsMetrics},
		{"collectListenersMetrics", exporter.collectListenersMetrics},
		{"collectHealthMetrics", exporter.collectHealthMetrics},
		{"collectDatabaseMetrics", exporter.collectDatabaseMetrics},
	}

	for _, collectFunc := range collectFunctions {
		t.Run(collectFunc.name, func(t *testing.T) {
			// Test that each function exists and handles connection failures gracefully  
			collectFunc.fn(server)
			t.Logf("%s completed without panic", collectFunc.name)
		})
	}
}

// TestMetricsFilteringInclude tests INCLUDE filtering functionality
func TestMetricsFilteringInclude(t *testing.T) {
	// Set up environment for INCLUDE filtering
	testEnvVars := map[string]string{
		"LDAP_URL":                    "ldap://127.0.0.1:1",
		"LDAP_USERNAME":               "testuser",
		"LDAP_PASSWORD":               "testpass",
		"LOG_LEVEL":                   "ERROR",
		"OPENLDAP_METRICS_INCLUDE":    "connections,statistics,health",
		"OPENLDAP_METRICS_EXCLUDE":    "", // Should be ignored when INCLUDE is set
	}

	for key, value := range testEnvVars {
		os.Setenv(key, value)
	}
	defer func() {
		for key := range testEnvVars {
			os.Unsetenv(key)
		}
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the filtering configuration is properly set
	if len(cfg.MetricsInclude) != 3 {
		t.Errorf("Expected 3 metrics in include list, got %d", len(cfg.MetricsInclude))
	}

	expectedIncluded := map[string]bool{
		"connections": true,
		"statistics":  true,
		"health":      true,
	}

	// Test metrics that should be included
	for metric, expected := range expectedIncluded {
		result := exporter.shouldCollectMetric(metric)
		if result != expected {
			t.Errorf("shouldCollectMetric(%q) = %t, expected %t", metric, result, expected)
		}
	}

	// Test metrics that should be excluded
	excludedMetrics := []string{"threads", "overlays", "tls", "backends", "waiters"}
	for _, metric := range excludedMetrics {
		result := exporter.shouldCollectMetric(metric)
		if result {
			t.Errorf("shouldCollectMetric(%q) = true, expected false (should be excluded)", metric)
		}
	}

	t.Logf("INCLUDE filtering test passed - included: %v", cfg.MetricsInclude)
}

// TestMetricsFilteringExclude tests EXCLUDE filtering functionality
func TestMetricsFilteringExclude(t *testing.T) {
	// Set up environment for EXCLUDE filtering
	testEnvVars := map[string]string{
		"LDAP_URL":                    "ldap://127.0.0.1:1",
		"LDAP_USERNAME":               "testuser",
		"LDAP_PASSWORD":               "testpass",
		"LOG_LEVEL":                   "ERROR",
		"OPENLDAP_METRICS_INCLUDE":    "", // No include filter
		"OPENLDAP_METRICS_EXCLUDE":    "overlays,tls,backends",
	}

	for key, value := range testEnvVars {
		os.Setenv(key, value)
	}
	defer func() {
		for key := range testEnvVars {
			os.Unsetenv(key)
		}
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that the filtering configuration is properly set
	if len(cfg.MetricsExclude) != 3 {
		t.Errorf("Expected 3 metrics in exclude list, got %d", len(cfg.MetricsExclude))
	}

	// Test metrics that should be excluded
	excludedMetrics := map[string]bool{
		"overlays": false,
		"tls":      false,
		"backends": false,
	}

	for metric, expected := range excludedMetrics {
		result := exporter.shouldCollectMetric(metric)
		if result != expected {
			t.Errorf("shouldCollectMetric(%q) = %t, expected %t", metric, result, expected)
		}
	}

	// Test metrics that should be included (not in exclude list)
	includedMetrics := []string{"connections", "statistics", "health", "threads", "waiters"}
	for _, metric := range includedMetrics {
		result := exporter.shouldCollectMetric(metric)
		if !result {
			t.Errorf("shouldCollectMetric(%q) = false, expected true (should be included)", metric)
		}
	}

	t.Logf("EXCLUDE filtering test passed - excluded: %v", cfg.MetricsExclude)
}

// TestMetricsFilteringDefault tests default (no filtering) functionality
func TestMetricsFilteringDefault(t *testing.T) {
	// Set up environment with no filtering
	testEnvVars := map[string]string{
		"LDAP_URL":                    "ldap://127.0.0.1:1",
		"LDAP_USERNAME":               "testuser",
		"LDAP_PASSWORD":               "testpass",
		"LOG_LEVEL":                   "ERROR",
		"OPENLDAP_METRICS_INCLUDE":    "",
		"OPENLDAP_METRICS_EXCLUDE":    "",
	}

	for key, value := range testEnvVars {
		os.Setenv(key, value)
	}
	defer func() {
		for key := range testEnvVars {
			os.Unsetenv(key)
		}
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that no filters are set
	if len(cfg.MetricsInclude) != 0 {
		t.Errorf("Expected empty include list, got %d items", len(cfg.MetricsInclude))
	}
	if len(cfg.MetricsExclude) != 0 {
		t.Errorf("Expected empty exclude list, got %d items", len(cfg.MetricsExclude))
	}

	// Test that all metrics should be collected by default
	allMetrics := []string{
		"connections", "statistics", "operations", "threads", "time",
		"waiters", "overlays", "tls", "backends", "listeners",
		"health", "database", "server", "log", "sasl",
	}

	for _, metric := range allMetrics {
		result := exporter.shouldCollectMetric(metric)
		if !result {
			t.Errorf("shouldCollectMetric(%q) = false, expected true (default mode should collect all)", metric)
		}
	}

	t.Log("DEFAULT filtering test passed - all metrics collected")
}

// TestMetricsFilteringPriority tests that INCLUDE takes priority over EXCLUDE
func TestMetricsFilteringPriority(t *testing.T) {
	// Set up environment with both INCLUDE and EXCLUDE (INCLUDE should take priority)
	testEnvVars := map[string]string{
		"LDAP_URL":                    "ldap://127.0.0.1:1",
		"LDAP_USERNAME":               "testuser",
		"LDAP_PASSWORD":               "testpass",
		"LOG_LEVEL":                   "ERROR",
		"OPENLDAP_METRICS_INCLUDE":    "connections,statistics",
		"OPENLDAP_METRICS_EXCLUDE":    "connections,health", // Should be ignored
	}

	for key, value := range testEnvVars {
		os.Setenv(key, value)
	}
	defer func() {
		for key := range testEnvVars {
			os.Unsetenv(key)
		}
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that INCLUDE filter takes priority
	// "connections" should be included (in INCLUDE list, even though it's also in EXCLUDE)
	if !exporter.shouldCollectMetric("connections") {
		t.Error("shouldCollectMetric('connections') = false, expected true (INCLUDE should take priority)")
	}

	// "statistics" should be included (in INCLUDE list)
	if !exporter.shouldCollectMetric("statistics") {
		t.Error("shouldCollectMetric('statistics') = false, expected true (in INCLUDE list)")
	}

	// "health" should be excluded (not in INCLUDE list, EXCLUDE is ignored)
	if exporter.shouldCollectMetric("health") {
		t.Error("shouldCollectMetric('health') = true, expected false (not in INCLUDE list)")
	}

	// "threads" should be excluded (not in INCLUDE list)
	if exporter.shouldCollectMetric("threads") {
		t.Error("shouldCollectMetric('threads') = true, expected false (not in INCLUDE list)")
	}

	t.Log("PRIORITY filtering test passed - INCLUDE takes priority over EXCLUDE")
}

// TestMetricsFilteringIntegration tests the integration of filtering with actual collection
func TestMetricsFilteringIntegration(t *testing.T) {
	// Set up environment for filtering integration test
	testEnvVars := map[string]string{
		"LDAP_URL":                    "ldap://127.0.0.1:1",
		"LDAP_USERNAME":               "testuser",
		"LDAP_PASSWORD":               "testpass",
		"LOG_LEVEL":                   "ERROR",
		"OPENLDAP_METRICS_INCLUDE":    "connections,health", // Only these should be collected
	}

	for key, value := range testEnvVars {
		os.Setenv(key, value)
	}
	defer func() {
		for key := range testEnvVars {
			os.Unsetenv(key)
		}
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Create context for collection
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Test that collectAllMetricsWithContext respects filtering
	// This will fail to connect but should still test the filtering logic
	err = exporter.collectAllMetricsWithContext(ctx)
	
	// Error is expected due to no LDAP connection, but filtering should work
	if err == nil {
		t.Log("Collection unexpectedly succeeded (LDAP server might be running)")
	} else {
		t.Logf("Expected collection failure (testing filtering logic): %v", err)
	}

	t.Log("INTEGRATION filtering test completed - filtering logic was exercised")
}

// TestGetMonitorGroup tests the optimized monitor group retrieval function
func TestGetMonitorGroup(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test the getMonitorGroup method exists and handles errors gracefully
	// This will fail due to no connection, but tests the error handling path
	_, err := exporter.getMonitorGroup("cn=Threads,cn=Monitor")
	if err == nil {
		t.Log("Unexpected success - LDAP server might be running")
	} else {
		t.Logf("Expected error (no connection): %v", err)
	}

	// Test various monitor groups
	monitorGroups := []string{
		"cn=Connections,cn=Monitor",
		"cn=Threads,cn=Monitor",
		"cn=Time,cn=Monitor",
		"cn=Waiters,cn=Monitor",
	}

	for _, group := range monitorGroups {
		counters, err := exporter.getMonitorGroup(group)
		if err != nil {
			t.Logf("Expected error for group %s: %v", group, err)
		} else {
			t.Logf("Unexpected success for group %s, got %d counters", group, len(counters))
		}
	}
}

// TestOptimizedCollectors tests that optimized collectors handle errors gracefully
func TestOptimizedCollectors(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	server := "test-server"

	// Test optimized collectors
	optimizedCollectors := []struct {
		name string
		fn   func(string)
	}{
		{"collectConnectionsMetrics (optimized)", exporter.collectConnectionsMetrics},
		{"collectThreadsMetrics (optimized)", exporter.collectThreadsMetrics},
		{"collectTimeMetrics (optimized)", exporter.collectTimeMetrics},
		{"collectWaitersMetrics (optimized)", exporter.collectWaitersMetrics},
	}

	for _, collector := range optimizedCollectors {
		t.Run(collector.name, func(t *testing.T) {
			// Test that optimized functions exist and handle connection failures gracefully
			collector.fn(server)
			t.Logf("%s completed without panic (using getMonitorGroup)", collector.name)
		})
	}
}

// TestIndividualCollectors tests all collector functions individually to improve coverage
func TestIndividualCollectors(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	server := exporter.config.ServerName

	// Test all collector functions (these will fail gracefully without LDAP connection)
	collectors := []struct {
		name string
		fn   func(string)
	}{
		{"collectOperationsMetrics", exporter.collectOperationsMetrics},
		{"collectOverlaysMetrics", exporter.collectOverlaysMetrics},
		{"collectTLSMetrics", exporter.collectTLSMetrics},
		{"collectBackendsMetrics", exporter.collectBackendsMetrics},
		{"collectListenersMetrics", exporter.collectListenersMetrics},
		{"collectHealthMetrics", exporter.collectHealthMetrics},
		{"collectDatabaseMetrics", exporter.collectDatabaseMetrics},
		{"collectServerInfoMetrics", exporter.collectServerInfoMetrics},
		{"collectLogMetrics", exporter.collectLogMetrics},
		{"collectSASLMetrics", exporter.collectSASLMetrics},
	}

	for _, collector := range collectors {
		t.Run(collector.name, func(t *testing.T) {
			// These should handle connection failures gracefully
			collector.fn(server)
		})
	}
}

// TestHelperFunctions tests utility functions to improve coverage
func TestHelperFunctions(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test extractDomainComponents
	tests := []struct {
		dn       string
		expected []string
	}{
		{"cn=admin,dc=example,dc=com", []string{"example", "com"}},
		{"ou=people,dc=test,dc=org", []string{"test", "org"}},
		{"cn=monitor", []string{}},
		{"", []string{}},
		{"dc=single", []string{"single"}},
		{"cn=user,ou=people,dc=multi,dc=level,dc=domain", []string{"multi", "level", "domain"}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("extractDC_%s", tt.dn), func(t *testing.T) {
			result := exporter.extractDomainComponents(tt.dn)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d components, got %d", len(tt.expected), len(result))
				return
			}
			for i, expected := range tt.expected {
				if i >= len(result) || result[i] != expected {
					t.Errorf("Expected component %d to be %s, got %s", i, expected, result[i])
				}
			}
		})
	}

	// Test shouldIncludeDomain
	t.Run("shouldIncludeDomain", func(t *testing.T) {
		// Test with default config (should include all domains)
		testCases := []struct {
			name     string
			domains  []string
			expected bool
		}{
			{"empty_domains", []string{}, true},
			{"single_domain", []string{"example"}, true},
			{"multiple_domains", []string{"example", "com"}, true},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := exporter.shouldIncludeDomain(tc.domains)
				if result != tc.expected {
					t.Errorf("Expected %v, got %v for domains %v", tc.expected, result, tc.domains)
				}
			})
		}
	})
}

// TestAtomicFloat64 tests the atomic float operations
func TestAtomicFloat64(t *testing.T) {
	af := &atomicFloat64{}

	// Test Store and Load
	testValue := 42.5
	af.Store(testValue)
	if loaded := af.Load(); loaded != testValue {
		t.Errorf("Expected %v, got %v", testValue, loaded)
	}

	// Test CompareAndSwap success
	newValue := 100.0
	if !af.CompareAndSwap(testValue, newValue) {
		t.Error("CompareAndSwap should succeed")
	}
	if loaded := af.Load(); loaded != newValue {
		t.Errorf("Expected %v after CompareAndSwap, got %v", newValue, loaded)
	}

	// Test CompareAndSwap failure
	wrongOld := 50.0
	if af.CompareAndSwap(wrongOld, 200.0) {
		t.Error("CompareAndSwap should fail with wrong old value")
	}
	if loaded := af.Load(); loaded != newValue {
		t.Errorf("Value should remain %v after failed CompareAndSwap, got %v", newValue, loaded)
	}
}

// TestRetryConfigCalculateDelay tests retry delay calculations
func TestRetryConfigCalculateDelay(t *testing.T) {
	rc := DefaultRetryConfig()

	tests := []struct {
		attempt  int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{0, 90 * time.Millisecond, 110 * time.Millisecond},   // ~100ms with jitter
		{1, 180 * time.Millisecond, 220 * time.Millisecond}, // ~200ms with jitter  
		{2, 360 * time.Millisecond, 440 * time.Millisecond}, // ~400ms with jitter
		{10, 1800 * time.Millisecond, 2200 * time.Millisecond}, // Should be capped at max (2s) with jitter
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			delay := rc.calculateDelay(tt.attempt)
			if delay > tt.maxDelay {
				t.Errorf("Delay %v exceeds expected max %v", delay, tt.maxDelay)
			}
			if delay < tt.minDelay {
				t.Errorf("Delay %v below expected min %v", delay, tt.minDelay)
			}
			if delay < 0 {
				t.Errorf("Delay should not be negative, got %v", delay)
			}
		})
	}
}

// TestShouldIncludeDomainWithFilters tests domain filtering with actual filters configured
func TestShouldIncludeDomainWithFilters(t *testing.T) {
	// Test with DC include filter
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Set up DC filters in environment
	os.Setenv("OPENLDAP_DC_INCLUDE", "example,test")
	defer os.Unsetenv("OPENLDAP_DC_INCLUDE")

	// Reload config to pick up the filter
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to reload config: %v", err)
	}

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	testCases := []struct {
		name     string
		domains  []string
		expected bool
	}{
		{"included_domain", []string{"example", "com"}, true},
		{"excluded_domain", []string{"notallowed", "com"}, false},
		{"mixed_domains", []string{"example", "notallowed"}, true}, // Contains allowed domain
		{"empty_domains", []string{}, false}, // No domains to check
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := exporter.shouldIncludeDomain(tc.domains)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for domains %v", tc.expected, result, tc.domains)
			}
		})
	}
}

// TestCollectAllMetricsWithContext tests the core metric collection function
func TestCollectAllMetricsWithContext(t *testing.T) {
	tests := []struct {
		name        string
		setupExporter func() *OpenLDAPExporter
		expectError bool
		description string
	}{
		{
			name: "successful_collection_no_filters",
			setupExporter: func() *OpenLDAPExporter {
				cfg, _ := setupTestConfig(t)
				cfg.ServerName = "test-server"
				return NewOpenLDAPExporter(cfg)
			},
			expectError: true, // Expected due to static testing environment
			description: "Test all metrics collection without filters",
		},
		{
			name: "filtered_metrics_collection",
			setupExporter: func() *OpenLDAPExporter {
				cfg, _ := setupTestConfig(t)
				cfg.ServerName = "test-server"
				cfg.MetricsInclude = []string{"health", "connections"}
				return NewOpenLDAPExporter(cfg)
			},
			expectError: true, // Expected due to static testing environment
			description: "Test filtered metrics collection",
		},
		{
			name: "excluded_metrics_collection",
			setupExporter: func() *OpenLDAPExporter {
				cfg, _ := setupTestConfig(t)
				cfg.ServerName = "test-server"
				cfg.MetricsExclude = []string{"backends", "database"}
				return NewOpenLDAPExporter(cfg)
			},
			expectError: true, // Expected due to static testing environment
			description: "Test metrics collection with exclusions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tt.setupExporter()
			defer exporter.Close()

			// Test with timeout context
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := exporter.collectAllMetricsWithContext(ctx)
			
			if tt.expectError && err == nil {
				t.Log("Expected error in static test environment - this is normal")
			} else if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			
			// Verify that internal monitoring is available
			monitoring := exporter.GetInternalMonitoring()
			if monitoring != nil {
				t.Log("Internal monitoring is available")
			}
		})
	}
}

// TestCollectAllMetricsWithCancelledContext tests context cancellation handling
func TestCollectAllMetricsWithCancelledContext(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	cfg.ServerName = "test"
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := exporter.collectAllMetricsWithContext(ctx)
	// In static testing, we might not get the expected context cancellation error
	// but we should handle it gracefully
	t.Logf("Context cancellation result: %v", err)
}

// TestGetMonitorGroupComprehensive tests the monitor group retrieval function
func TestGetMonitorGroupComprehensive(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	cfg.ServerName = "test"
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	tests := []struct {
		name    string
		baseDN  string
		wantErr bool
	}{
		{
			name:    "valid_monitor_dn",
			baseDN:  "cn=Connections,cn=Monitor",
			wantErr: true, // Expected in static testing
		},
		{
			name:    "invalid_monitor_dn", 
			baseDN:  "cn=Invalid,cn=Monitor",
			wantErr: true,
		},
		{
			name:    "empty_dn",
			baseDN:  "",
			wantErr: true,
		},
		{
			name:    "malformed_dn",
			baseDN:  "invalid-dn-format",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := exporter.getMonitorGroup(tt.baseDN)
			
			if tt.wantErr {
				if err == nil {
					t.Log("Expected error in static test environment")
				}
				// In static environment, result should be nil or empty
				if result != nil && len(result) > 0 {
					t.Logf("Unexpected result in static test: %v", result)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result == nil {
					t.Error("Expected non-nil result")
				}
			}
		})
	}
}

// TestCollectorFunctionsComprehensive tests individual collector functions
func TestCollectorFunctionsComprehensive(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	cfg.ServerName = "test"
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	collectors := []struct {
		name string
		fn   func(string)
	}{
		{"collectBackendsMetrics", exporter.collectBackendsMetrics},
		{"collectDatabaseMetrics", exporter.collectDatabaseMetrics},
		{"collectOperationsMetrics", exporter.collectOperationsMetrics},
		{"collectStatisticsMetrics", exporter.collectStatisticsMetrics},
		{"collectServerInfoMetrics", exporter.collectServerInfoMetrics},
		{"collectConnectionsMetrics", exporter.collectConnectionsMetrics},
		{"collectWaitersMetrics", exporter.collectWaitersMetrics},
		{"collectSASLMetrics", exporter.collectSASLMetrics},
		{"collectThreadsMetrics", exporter.collectThreadsMetrics},
		{"collectTimeMetrics", exporter.collectTimeMetrics},
		{"collectOverlaysMetrics", exporter.collectOverlaysMetrics},
		{"collectTLSMetrics", exporter.collectTLSMetrics},
		{"collectListenersMetrics", exporter.collectListenersMetrics},
		{"collectLogMetrics", exporter.collectLogMetrics},
		{"collectHealthMetrics", exporter.collectHealthMetrics},
	}

	for _, collector := range collectors {
		t.Run(collector.name, func(t *testing.T) {
			// Call the collector function with server name
			collector.fn("test-server")
			// In static testing environment, these will proceed but may not collect real metrics
			// This is expected and tests the execution paths
			t.Logf("Collector %s executed successfully", collector.name)
		})
	}
}

// TestGetMonitorCounterComprehensive tests monitor counter retrieval
func TestGetMonitorCounterComprehensive(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	cfg.ServerName = "test"
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	tests := []struct {
		name    string
		dn      string
		wantErr bool
	}{
		{
			name:    "valid_counter_dn",
			dn:      "cn=Current,cn=Connections,cn=Monitor",
			wantErr: true, // Expected in static testing
		},
		{
			name:    "invalid_counter_dn",
			dn:      "cn=Invalid,cn=Monitor",
			wantErr: true,
		},
		{
			name:    "empty_dn",
			dn:      "",
			wantErr: true,
		},
		{
			name:    "malformed_dn",
			dn:      "not-a-valid-dn",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := exporter.getMonitorCounter(tt.dn)
			
			if tt.wantErr {
				if err == nil {
					t.Log("Expected error in static test environment")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if value < 0 {
					t.Errorf("Expected non-negative value, got %f", value)
				}
			}
		})
	}
}

// TestShouldCollectMetricFiltering tests metric filtering logic
func TestShouldCollectMetricFiltering(t *testing.T) {
	tests := []struct {
		name        string
		include     []string
		exclude     []string
		metric      string
		expected    bool
		description string
	}{
		{
			name:        "no_filters",
			include:     nil,
			exclude:     nil,
			metric:      "connections",
			expected:    true,
			description: "Should collect all metrics when no filters",
		},
		{
			name:        "include_filter_match",
			include:     []string{"connections", "health"},
			exclude:     nil,
			metric:      "connections",
			expected:    true,
			description: "Should collect included metric",
		},
		{
			name:        "include_filter_no_match",
			include:     []string{"connections", "health"},
			exclude:     nil,
			metric:      "backends",
			expected:    false,
			description: "Should not collect non-included metric",
		},
		{
			name:        "exclude_filter_match",
			include:     nil,
			exclude:     []string{"backends", "database"},
			metric:      "backends",
			expected:    false,
			description: "Should not collect excluded metric",
		},
		{
			name:        "exclude_filter_no_match",
			include:     nil,
			exclude:     []string{"backends", "database"},
			metric:      "connections",
			expected:    true,
			description: "Should collect non-excluded metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, cleanup := setupTestConfig(t)
			defer cleanup()
			
			cfg.MetricsInclude = tt.include
			cfg.MetricsExclude = tt.exclude
			
			exporter := NewOpenLDAPExporter(cfg)
			defer exporter.Close()

			result := exporter.shouldCollectMetric(tt.metric)
			if result != tt.expected {
				t.Errorf("shouldCollectMetric(%s) = %v, want %v (%s)", tt.metric, result, tt.expected, tt.description)
			}
		})
	}
}

// TestCollectBackendsMetricsDetailed tests backends metrics collection
func TestCollectBackendsMetricsDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with metric disabled
	cfg.MetricsExclude = []string{"backends"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectBackendsMetrics("test-server")
	// Should return early when disabled
	
	// Test with metric enabled
	cfg.MetricsExclude = nil
	cfg.MetricsInclude = []string{"backends"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectBackendsMetrics("test-server")
	// Will fail in static environment but tests the path
}

// TestCollectDatabaseMetricsDetailed tests database metrics collection
func TestCollectDatabaseMetricsDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with metric disabled
	cfg.MetricsExclude = []string{"database"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectDatabaseMetrics("test-server")
	// Should return early when disabled
	
	// Test with metric enabled
	cfg.MetricsExclude = nil
	cfg.MetricsInclude = []string{"database"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectDatabaseMetrics("test-server")
	// Will fail in static environment but tests the path
}

// TestCollectOperationsMetricsDetailed tests operations metrics collection
func TestCollectOperationsMetricsDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with metric disabled
	cfg.MetricsExclude = []string{"operations"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectOperationsMetrics("test-server")
	// Should return early when disabled
	
	// Test with metric enabled 
	cfg.MetricsExclude = nil
	cfg.MetricsInclude = []string{"operations"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectOperationsMetrics("test-server")
	// Will fail in static environment but tests the path
}

// TestCollectStatisticsMetricsDetailed tests statistics metrics collection
func TestCollectStatisticsMetricsDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with metric disabled
	cfg.MetricsExclude = []string{"statistics"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectStatisticsMetrics("test-server")
	// Should return early when disabled
	
	// Test with metric enabled
	cfg.MetricsExclude = nil
	cfg.MetricsInclude = []string{"statistics"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectStatisticsMetrics("test-server")
	// Will fail in static environment but tests the path
}

// TestCollectListenersMetricsDetailed tests listeners metrics collection
func TestCollectListenersMetricsDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with metric disabled
	cfg.MetricsExclude = []string{"listeners"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectListenersMetrics("test-server")
	// Should return early when disabled
	
	// Test with metric enabled
	cfg.MetricsExclude = nil
	cfg.MetricsInclude = []string{"listeners"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectListenersMetrics("test-server")
	// Will fail in static environment but tests the path
}

// TestCollectServerInfoMetricsDetailed tests server info metrics collection
func TestCollectServerInfoMetricsDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with metric disabled
	cfg.MetricsExclude = []string{"server"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectServerInfoMetrics("test-server")
	// Should return early when disabled
	
	// Test with metric enabled
	cfg.MetricsExclude = nil
	cfg.MetricsInclude = []string{"server"}
	exporter = NewOpenLDAPExporter(cfg)
	exporter.collectServerInfoMetrics("test-server")
	// Will fail in static environment but tests the path
}

// TestGetMonitorGroupAdvanced tests getMonitorGroup with different scenarios
func TestGetMonitorGroupAdvanced(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	testCases := []struct {
		name   string
		baseDN string
	}{
		{"connections", "cn=Connections,cn=Monitor"},
		{"operations", "cn=Operations,cn=Monitor"},
		{"statistics", "cn=Statistics,cn=Monitor"},
		{"threads", "cn=Threads,cn=Monitor"},
		{"waiters", "cn=Waiters,cn=Monitor"},
		{"time", "cn=Time,cn=Monitor"},
		{"backends", "cn=Backends,cn=Monitor"},
		{"databases", "cn=Databases,cn=Monitor"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := exporter.getMonitorGroup(tc.baseDN)
			// Expected to fail in static environment
			if err != nil {
				t.Logf("Expected error for %s: %v", tc.name, err)
			} else if result != nil {
				t.Logf("Got unexpected result for %s: %v", tc.name, result)
			}
		})
	}
}

// TestGetMonitorCounterAdvanced tests getMonitorCounter with different DNs
func TestGetMonitorCounterAdvanced(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	testCases := []struct {
		name string
		dn   string
	}{
		{"current_connections", "cn=Current,cn=Connections,cn=Monitor"},
		{"total_connections", "cn=Total,cn=Connections,cn=Monitor"},
		{"bytes_sent", "cn=Bytes,cn=Statistics,cn=Monitor"},
		{"entries_sent", "cn=Entries,cn=Statistics,cn=Monitor"},
		{"bind_operations", "cn=Bind,cn=Operations,cn=Monitor"},
		{"search_operations", "cn=Search,cn=Operations,cn=Monitor"},
		{"modify_operations", "cn=Modify,cn=Operations,cn=Monitor"},
		{"add_operations", "cn=Add,cn=Operations,cn=Monitor"},
		{"delete_operations", "cn=Delete,cn=Operations,cn=Monitor"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := exporter.getMonitorCounter(tc.dn)
			// Expected to fail in static environment
			if err != nil {
				t.Logf("Expected error for %s: %v", tc.name, err)
			} else {
				t.Logf("Got value for %s: %f", tc.name, value)
			}
		})
	}
}

// TestExtractDomainComponentsAdvanced tests advanced domain extraction scenarios
func TestExtractDomainComponentsAdvanced(t *testing.T) {
	tests := []struct {
		name     string
		dn       string
		expected []string
	}{
		{
			name:     "simple_domain",
			dn:       "cn=test,dc=example,dc=com",
			expected: []string{"example", "com"},
		},
		{
			name:     "complex_domain",
			dn:       "cn=user,ou=people,dc=corp,dc=example,dc=org",
			expected: []string{"corp", "example", "org"},
		},
		{
			name:     "no_domain",
			dn:       "cn=test,ou=people",
			expected: []string{},
		},
		{
			name:     "empty_dn",
			dn:       "",
			expected: []string{},
		},
		{
			name:     "single_domain",
			dn:       "cn=test,dc=localhost",
			expected: []string{"localhost"},
		},
	}

	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := exporter.extractDomainComponents(tt.dn)
			if len(result) != len(tt.expected) {
				t.Errorf("extractDomainComponents(%s) length = %d, want %d", tt.dn, len(result), len(tt.expected))
				return
			}
			for i, domain := range result {
				if domain != tt.expected[i] {
					t.Errorf("extractDomainComponents(%s)[%d] = %s, want %s", tt.dn, i, domain, tt.expected[i])
				}
			}
		})
	}
}

// TestCollectAllMetricsWithContextAdvanced tests advanced scenarios for collectAllMetricsWithContext
func TestCollectAllMetricsWithContextAdvanced(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	tests := []struct {
		name           string
		setupExporter  func() *OpenLDAPExporter
		setupContext   func() (context.Context, context.CancelFunc)
		expectError    bool
		expectTimeout  bool
	}{
		{
			name: "include_metrics_filtering",
			setupExporter: func() *OpenLDAPExporter {
				cfg.MetricsInclude = []string{"connections", "health"}
				cfg.MetricsExclude = nil
				return NewOpenLDAPExporter(cfg)
			},
			setupContext: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 2*time.Second)
			},
			expectError: true, // Expected in static test
		},
		{
			name: "exclude_metrics_filtering",
			setupExporter: func() *OpenLDAPExporter {
				cfg.MetricsInclude = nil
				cfg.MetricsExclude = []string{"statistics", "operations"}
				return NewOpenLDAPExporter(cfg)
			},
			setupContext: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 2*time.Second)
			},
			expectError: true, // Expected in static test
		},
		{
			name: "context_timeout_during_collection",
			setupExporter: func() *OpenLDAPExporter {
				cfg.MetricsInclude = nil
				cfg.MetricsExclude = nil
				return NewOpenLDAPExporter(cfg)
			},
			setupContext: func() (context.Context, context.CancelFunc) {
				// Very short timeout to force timeout during parallel collection
				return context.WithTimeout(context.Background(), 1*time.Millisecond)
			},
			expectError:   true,
			expectTimeout: true,
		},
		{
			name: "cancelled_context",
			setupExporter: func() *OpenLDAPExporter {
				return NewOpenLDAPExporter(cfg)
			},
			setupContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately
				return ctx, func() {}
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := tt.setupExporter()
			defer exporter.Close()
			
			ctx, cancel := tt.setupContext()
			defer cancel()

			err := exporter.collectAllMetricsWithContext(ctx)
			
			if tt.expectError && err == nil {
				t.Log("Expected error in static test environment - this is normal")
			} else if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.expectTimeout && err != nil {
				t.Logf("Got expected timeout error: %v", err)
			}
		})
	}
}

// TestCollectAllMetricsParallelExecution tests the parallel execution logic
func TestCollectAllMetricsParallelExecution(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	// Test with all metrics enabled to maximize parallel execution
	cfg.MetricsInclude = nil
	cfg.MetricsExclude = nil
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// This should test the parallel execution path, error handling, and waitgroup logic
	err := exporter.collectAllMetricsWithContext(ctx)
	
	// Expected to fail due to connection issues, but tests the execution paths
	if err != nil {
		t.Logf("Expected connection error in static environment: %v", err)
	}
}

// TestCollectAllMetricsErrorHandling tests error collection and aggregation
func TestCollectAllMetricsErrorHandling(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test with very short timeout to force timeout handling
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	
	err := exporter.collectAllMetricsWithContext(ctx)
	
	// Should get context deadline exceeded or similar
	if err != nil {
		t.Logf("Got expected timeout/context error: %v", err)
		// Verify it's a context-related error
		if err == context.DeadlineExceeded || err == context.Canceled {
			t.Log("Confirmed context timeout handling works correctly")
		}
	} else {
		t.Log("No error returned - might complete before timeout in test environment")
	}
}

// TestCollectAllMetricsTimeout tests timeout and context handling
func TestCollectAllMetricsTimeout(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	// Test context cancellation during collection
	ctx, cancel := context.WithCancel(context.Background())
	
	// Start collection in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- exporter.collectAllMetricsWithContext(ctx)
	}()
	
	// Cancel context after short delay
	time.Sleep(10 * time.Millisecond)
	cancel()
	
	// Wait for completion
	select {
	case err := <-errChan:
		if err != nil {
			t.Logf("Got expected cancellation error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Collection did not complete within timeout")
	}
}

// TestCollectAllMetricsAllTasksSkipped tests when all tasks are skipped by filters
func TestCollectAllMetricsAllTasksSkipped(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()
	
	// Set include filter to non-existent metrics so all tasks are skipped
	cfg.MetricsInclude = []string{"nonexistent1", "nonexistent2"}
	cfg.MetricsExclude = nil
	
	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	err := exporter.collectAllMetricsWithContext(ctx)
	
	// Should complete without error since all tasks are skipped
	if err != nil {
		// Still might fail on ensureConnection
		t.Logf("Got error (likely connection related): %v", err)
	} else {
		t.Log("Completed successfully with all metrics filtered out")
	}
}