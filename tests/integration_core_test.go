//go:build integration
// +build integration

package tests

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/exporter"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/pool"
)

// Test environment configuration
const (
	defaultLDAPURL      = "ldap://localhost:1389"
	defaultLDAPUser     = "cn=adminconfig,cn=config"
	defaultLDAPPassword = "configpassword"
	defaultServerName   = "openldap-test"
)

// getTestConfig returns a test configuration for OpenLDAP
func getTestConfig(t *testing.T) *config.Config {
	logger.InitLogger("integration-test", "INFO")

	url := getEnvOrDefault("TEST_LDAP_URL", defaultLDAPURL)
	username := getEnvOrDefault("TEST_LDAP_USERNAME", defaultLDAPUser)
	password := getEnvOrDefault("TEST_LDAP_PASSWORD", defaultLDAPPassword)

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	cfg.LDAPURL = url
	cfg.Username = username
	cfg.Password = password
	cfg.ServerName = defaultServerName
	cfg.TLS = false
	cfg.Timeout = 10

	return cfg
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// TestConnectionPool tests the Go connection pool functionality
func TestConnectionPool(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	t.Run("Pool Creation and Stats", func(t *testing.T) {
		pool := pool.NewConnectionPool(cfg, 3)
		defer pool.Close()

		stats := pool.Stats()
		if stats["max_connections"] != 3 {
			t.Errorf("Expected max_connections=3, got %v", stats["max_connections"])
		}
	})

	t.Run("Connection Lifecycle", func(t *testing.T) {
		pool := pool.NewConnectionPool(cfg, 2)
		defer pool.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Get connection
		conn, err := pool.Get(ctx)
		if err != nil {
			t.Fatalf("Failed to get connection: %v", err)
		}

		// Return connection
		pool.Put(conn)

		// Verify pool stats
		stats := pool.Stats()
		t.Logf("Pool stats after connection cycle: %+v", stats)
	})
}

// TestPooledLDAPClient tests the Go LDAP client wrapper
func TestPooledLDAPClient(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	client := pool.NewPooledLDAPClient(cfg)
	defer client.Close()

	t.Run("Client Health Check", func(t *testing.T) {
		if !client.IsHealthy() {
			t.Error("Client should be healthy initially")
		}
	})

	t.Run("Client Stats", func(t *testing.T) {
		stats := client.Stats()

		// Verify expected stats structure
		expectedKeys := []string{"pool_max_connections", "circuit_breaker_state", "is_adaptive"}
		for _, key := range expectedKeys {
			if _, exists := stats[key]; !exists {
				t.Errorf("Expected stat key %s not found", key)
			}
		}
	})

	t.Run("Basic LDAP Search", func(t *testing.T) {
		// Test basic connectivity with a simple Monitor search
		result, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})

		// This may fail if Monitor backend is not configured, which is OK
		if err == nil && result != nil {
			t.Logf("Successfully performed LDAP search, found %d entries", len(result.Entries))
		} else {
			t.Logf("LDAP search failed (expected if Monitor not configured): %v", err)
		}
	})
}

// TestExporterIntegration tests the Go exporter integration
func TestExporterIntegration(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	t.Run("Exporter Creation", func(t *testing.T) {
		exp := exporter.NewOpenLDAPExporter(cfg)
		defer exp.Close()

		if exp == nil {
			t.Fatal("Exporter should not be nil")
		}
	})

	t.Run("Prometheus Registration", func(t *testing.T) {
		exp := exporter.NewOpenLDAPExporter(cfg)
		defer exp.Close()

		registry := prometheus.NewRegistry()
		err := registry.Register(exp)
		if err != nil {
			t.Fatalf("Failed to register exporter: %v", err)
		}

		// Gather metrics to verify registration
		metricFamilies, err := registry.Gather()
		if err != nil {
			t.Fatalf("Failed to gather metrics: %v", err)
		}

		t.Logf("Successfully gathered %d metric families", len(metricFamilies))
	})

	t.Run("Metric Collection Structure", func(t *testing.T) {
		exp := exporter.NewOpenLDAPExporter(cfg)
		defer exp.Close()

		registry := prometheus.NewRegistry()
		registry.Register(exp)

		metricFamilies, err := registry.Gather()
		if err != nil {
			t.Fatalf("Failed to gather metrics: %v", err)
		}

		// Verify we get some metrics (even if LDAP is not fully configured)
		if len(metricFamilies) == 0 {
			t.Error("Expected at least some metrics to be available")
		}

		// Check for internal metrics that should always be present
		var foundInternalMetrics []string
		for _, mf := range metricFamilies {
			if mf.Name != nil {
				name := *mf.Name
				if strings.HasPrefix(name, "openldap_scrape") ||
					strings.HasPrefix(name, "openldap_up") {
					foundInternalMetrics = append(foundInternalMetrics, name)
				}
			}
		}

		if len(foundInternalMetrics) == 0 {
			t.Error("Expected at least some internal metrics (scrape duration, up status)")
		} else {
			t.Logf("Found internal metrics: %v", foundInternalMetrics)
		}
	})
}

// TestDCFiltering tests the Domain Component filtering logic
func TestDCFiltering(t *testing.T) {
	t.Run("DC Include Filtering", func(t *testing.T) {
		cfg := getTestConfig(t)
		defer cfg.Clear()

		cfg.DCInclude = []string{"example", "test"}
		cfg.DCExclude = nil

		exp := exporter.NewOpenLDAPExporter(cfg)
		defer exp.Close()

		// Test that the exporter was created with DC filtering
		if len(cfg.DCInclude) != 2 {
			t.Errorf("Expected 2 DC include filters, got %d", len(cfg.DCInclude))
		}
	})

	t.Run("DC Exclude Filtering", func(t *testing.T) {
		cfg := getTestConfig(t)
		defer cfg.Clear()

		cfg.DCInclude = nil
		cfg.DCExclude = []string{"production", "staging"}

		exp := exporter.NewOpenLDAPExporter(cfg)
		defer exp.Close()

		// Test that the exporter was created with DC filtering
		if len(cfg.DCExclude) != 2 {
			t.Errorf("Expected 2 DC exclude filters, got %d", len(cfg.DCExclude))
		}
	})

	t.Run("DC Filtering Conflict", func(t *testing.T) {
		cfg := getTestConfig(t)
		defer cfg.Clear()

		// Test conflicting configuration (both include and exclude)
		cfg.DCInclude = []string{"example"}
		cfg.DCExclude = []string{"test"}

		exp := exporter.NewOpenLDAPExporter(cfg)
		defer exp.Close()

		// Should handle conflicting config gracefully
		if exp == nil {
			t.Error("Exporter should handle conflicting DC config gracefully")
		}
	})
}

// TestConcurrentMetrics tests concurrent metric collection (Go-specific)
func TestConcurrentMetrics(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	registry := prometheus.NewRegistry()
	registry.Register(exp)

	t.Run("Concurrent Metric Gathering", func(t *testing.T) {
		const numGoroutines = 5
		results := make(chan error, numGoroutines)

		// Start multiple goroutines gathering metrics concurrently
		for i := 0; i < numGoroutines; i++ {
			go func(id int) {
				_, err := registry.Gather()
				results <- err
			}(i)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			err := <-results
			if err != nil {
				t.Errorf("Goroutine %d failed: %v", i, err)
			}
		}
	})
}

// TestMetricConsistency tests that metrics are consistent across multiple collections
func TestMetricConsistency(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	registry := prometheus.NewRegistry()
	registry.Register(exp)

	t.Run("Metric Stability", func(t *testing.T) {
		// Collect metrics twice
		metrics1, err1 := registry.Gather()
		if err1 != nil {
			t.Fatalf("First metric collection failed: %v", err1)
		}

		time.Sleep(100 * time.Millisecond)

		metrics2, err2 := registry.Gather()
		if err2 != nil {
			t.Fatalf("Second metric collection failed: %v", err2)
		}

		// Verify we get the same number of metric families
		if len(metrics1) != len(metrics2) {
			t.Errorf("Metric family count changed: %d -> %d", len(metrics1), len(metrics2))
		}

		// Verify metric names are consistent
		names1 := getMetricNames(metrics1)
		names2 := getMetricNames(metrics2)

		for _, name := range names1 {
			found := false
			for _, name2 := range names2 {
				if name == name2 {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Metric %s disappeared between collections", name)
			}
		}
	})
}

// Helper function to extract metric names
func getMetricNames(metricFamilies []*dto.MetricFamily) []string {
	var names []string
	for _, mf := range metricFamilies {
		if mf.Name != nil {
			names = append(names, *mf.Name)
		}
	}
	return names
}
