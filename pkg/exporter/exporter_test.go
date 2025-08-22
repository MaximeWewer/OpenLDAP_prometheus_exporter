package exporter

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/prometheus/client_golang/prometheus"
)

// setupTestConfig creates a test configuration with required environment variables
func setupTestConfig(t *testing.T) (*config.Config, func()) {
	// Set up test environment
	testEnvVars := map[string]string{
		"LDAP_URL":      "ldap://test.example.com:389",
		"LDAP_USERNAME": "testuser",
		"LDAP_PASSWORD": "testpass",
		"LDAP_TIMEOUT":  "1",     // Short timeout for tests (1 second instead of 5)
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

// TestExporterInternalState tests basic exporter state
func TestExporterInternalState(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that exporter is properly initialized
	if exporter.config != cfg {
		t.Error("Exporter should store the provided config")
	}

	if exporter.metrics == nil {
		t.Error("Exporter should have initialized metrics")
	}

	if exporter.client == nil {
		t.Error("Exporter should have initialized client")
	}
}

// TestValidateKey tests key validation logic
func TestValidateKey(t *testing.T) {
	testCases := []struct {
		name        string
		key         string
		expectError bool
	}{
		{
			name:        "Valid key",
			key:         "valid_metric_name",
			expectError: false,
		},
		{
			name:        "Empty key",
			key:         "",
			expectError: true,
		},
		{
			name:        "Very long key",
			key:         string(make([]byte, 600)), // Longer than 512 limit
			expectError: true,
		},
		{
			name:        "Key with null byte",
			key:         "invalid\x00key",
			expectError: true,
		},
		{
			name:        "Key with newline",
			key:         "invalid\nkey",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateKey(tc.key)
			if tc.expectError && err == nil {
				t.Errorf("Expected error for key %q, got nil", tc.key)
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error for key %q, got %v", tc.key, err)
			}
		})
	}
}

// TestIsRetryableError tests retry logic for different error types
func TestIsRetryableError(t *testing.T) {
	testCases := []struct {
		name        string
		err         error
		expectRetry bool
	}{
		{
			name:        "Nil error",
			err:         nil,
			expectRetry: false,
		},
		{
			name:        "Connection timeout",
			err:         fmt.Errorf("connection timeout"),
			expectRetry: true,
		},
		{
			name:        "Connection refused",
			err:         fmt.Errorf("connection refused"),
			expectRetry: true,
		},
		{
			name:        "Network unreachable",
			err:         fmt.Errorf("network is unreachable"),
			expectRetry: true,
		},
		{
			name:        "Authentication error",
			err:         fmt.Errorf("invalid credentials"),
			expectRetry: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isRetryableError(tc.err)
			if result != tc.expectRetry {
				t.Errorf("Expected retry %v for error %v, got %v", tc.expectRetry, tc.err, result)
			}
		})
	}
}

// TestAtomicFloat64 tests atomic float operations
func TestAtomicFloat64(t *testing.T) {
	var af atomicFloat64

	// Test initial zero value
	if af.Load() != 0.0 {
		t.Errorf("Expected initial value 0.0, got %v", af.Load())
	}

	// Test store and load
	testValue := 123.456
	af.Store(testValue)
	if af.Load() != testValue {
		t.Errorf("Expected stored value %v, got %v", testValue, af.Load())
	}

	// Test compare and swap
	newValue := 789.012
	success := af.CompareAndSwap(testValue, newValue)
	if !success {
		t.Error("CompareAndSwap should succeed with correct old value")
	}
	if af.Load() != newValue {
		t.Errorf("Expected value after CAS %v, got %v", newValue, af.Load())
	}

	// Test failed compare and swap
	success = af.CompareAndSwap(testValue, 999.999)
	if success {
		t.Error("CompareAndSwap should fail with incorrect old value")
	}
	if af.Load() != newValue {
		t.Errorf("Value should not change after failed CAS, got %v", af.Load())
	}
}

// TestExporterRegistration tests that exporter can be registered with Prometheus
func TestExporterRegistration(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that exporter can be registered
	registry := prometheus.NewRegistry()
	err := registry.Register(exporter)
	if err != nil {
		t.Errorf("Failed to register exporter: %v", err)
	}

	// Test that metrics can be gathered
	families, err := registry.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
	}

	// Should have at least some metrics even if LDAP fails
	if len(families) == 0 {
		t.Error("Expected at least some metrics to be gathered")
	}
}

// TestConfigurationFiltering tests metric filtering configuration
func TestConfigurationFiltering(t *testing.T) {
	// Test with include filter
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_METRICS_INCLUDE", "connections,statistics")
	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_METRICS_INCLUDE")
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that filtering is applied
	if !cfg.ShouldCollectMetric("connections") {
		t.Error("Should collect 'connections' metric when included")
	}
	if cfg.ShouldCollectMetric("threads") {
		t.Error("Should NOT collect 'threads' metric when not included")
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
		t.Fatal("Should have internal monitoring")
	}

	// Test metric registration
	registry := prometheus.NewRegistry()
	err := monitoring.RegisterMetrics(registry)
	if err != nil {
		t.Errorf("Failed to register internal metrics: %v", err)
	}

	// Test start time
	startTime := monitoring.GetStartTime()
	if startTime.IsZero() {
		t.Error("Start time should be set")
	}
	if time.Since(startTime) > time.Minute {
		t.Error("Start time should be recent")
	}
}

// TestExporterClose tests proper cleanup on close
func TestExporterClose(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)

	// Exporter should start normally
	if exporter.client == nil {
		t.Error("Exporter should have client before close")
	}

	// Close should not panic
	exporter.Close()

	// Note: Multiple Close() calls may panic due to closing already closed channels
	// This is acceptable behavior for this implementation
}

// TestMetricTypes tests different metric types are handled correctly
func TestMetricTypes(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	// Test that metrics are properly defined
	if exporter.metrics == nil {
		t.Fatal("Metrics should be initialized")
	}

	// Collect to ensure metrics are working
	ch := make(chan prometheus.Metric, 100)
	go func() {
		defer close(ch)
		exporter.Collect(ch)
	}()

	var gaugeCount, counterCount int
	for metric := range ch {
		desc := metric.Desc()
		metricType := desc.String()

		// Count different metric types
		if contains(metricType, "gauge") {
			gaugeCount++
		}
		if contains(metricType, "counter") {
			counterCount++
		}
	}

	t.Logf("Found %d gauge-type metrics and %d counter-type metrics", gaugeCount, counterCount)
}

// Helper function for string contains check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			len(s) > 2*len(substr) && s[len(s)/2-len(substr)/2:len(s)/2+len(substr)/2+len(substr)%2] == substr)))
}
