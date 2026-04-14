//go:build integration
// +build integration

// Package tests contains the integration tests exercised by the CI
// Quality Assurance pipeline. They assume a running OpenLDAP server
// provisioned exactly like tests/docker/ — i.e. cleanstart/openldap:2.6.13
// with the slapo-accesslog and slapo-ppolicy overlays pre-seeded and the
// users / policies defined under init-ldifs/. The same stack is used
// locally by developers, so what CI runs is what the dev sees on their
// laptop.
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

// Defaults mirror tests/docker/docker-compose.yml and
// tests/docker/init-config/slapd-config.ldif so developers can run
// `go test -tags=integration ./tests/...` against the local stack without
// setting any environment variable.
const (
	defaultLDAPURL      = "ldap://localhost:1389"
	defaultLDAPUser     = "cn=adminconfig,cn=config"
	defaultLDAPPassword = "adminpasswordconfig"
	defaultServerName   = "openldap-events-test"
)

// getTestConfig returns a Config populated from the TEST_LDAP_* environment
// variables with a fall-through to the local tests/docker/ defaults.
func getTestConfig(t *testing.T) *config.Config {
	t.Helper()
	logger.InitLogger("integration-test", "INFO")

	url := getEnvOrDefault("TEST_LDAP_URL", defaultLDAPURL)
	username := getEnvOrDefault("TEST_LDAP_USERNAME", defaultLDAPUser)
	password := getEnvOrDefault("TEST_LDAP_PASSWORD", defaultLDAPPassword)

	// config.LoadConfig() reads from the real LDAP_* environment; set them
	// for the duration of the call and restore afterwards so concurrent
	// tests on the same process do not stomp on each other.
	saved := map[string]string{
		"LDAP_URL":      os.Getenv("LDAP_URL"),
		"LDAP_USERNAME": os.Getenv("LDAP_USERNAME"),
		"LDAP_PASSWORD": os.Getenv("LDAP_PASSWORD"),
	}
	os.Setenv("LDAP_URL", url)
	os.Setenv("LDAP_USERNAME", username)
	os.Setenv("LDAP_PASSWORD", password)
	defer func() {
		for k, v := range saved {
			if v == "" {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, v)
			}
		}
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	cfg.ServerName = defaultServerName
	cfg.TLS = false
	cfg.Timeout = 10 * time.Second

	return cfg
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// TestConnectionPool exercises the Go connection pool against a live slapd.
func TestConnectionPool(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	t.Run("Pool Creation and Stats", func(t *testing.T) {
		p := pool.NewConnectionPool(cfg, 3)
		defer p.Close()

		stats := p.Stats()
		if stats["max_connections"] != 3 {
			t.Errorf("Expected max_connections=3, got %v", stats["max_connections"])
		}
	})

	t.Run("Connection Lifecycle", func(t *testing.T) {
		p := pool.NewConnectionPool(cfg, 2)
		defer p.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		conn, err := p.Get(ctx)
		if err != nil {
			t.Fatalf("Failed to get connection: %v", err)
		}
		p.Put(conn)

		t.Logf("Pool stats after connection cycle: %+v", p.Stats())
	})
}

// TestPooledLDAPClient exercises the pooled client wrapper.
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
		expectedKeys := []string{"pool_max_connections", "circuit_breaker_state", "is_adaptive"}
		for _, key := range expectedKeys {
			if _, exists := stats[key]; !exists {
				t.Errorf("Expected stat key %s not found", key)
			}
		}
	})

	t.Run("Monitor Search", func(t *testing.T) {
		// tests/docker seeds a monitor database accessible only to
		// cn=adminconfig,cn=config; any successful result with >= 1 entry
		// confirms the client can talk to slapd end-to-end.
		result, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
		if err != nil {
			t.Fatalf("Monitor search failed: %v", err)
		}
		if len(result.Entries) == 0 {
			t.Error("Expected at least one entry under cn=Monitor")
		}
	})
}

// TestExporterIntegration registers the exporter against a Prometheus
// registry and verifies that a gather returns both internal housekeeping
// metrics and at least one openldap_* family sourced from the real slapd.
func TestExporterIntegration(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	registry := prometheus.NewRegistry()
	if err := registry.Register(exp); err != nil {
		t.Fatalf("Failed to register exporter: %v", err)
	}

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}
	if len(metricFamilies) == 0 {
		t.Fatal("Expected metric families from registry.Gather()")
	}

	var sawUp, sawMonitor bool
	for _, mf := range metricFamilies {
		if mf.Name == nil {
			continue
		}
		name := *mf.Name
		switch {
		case name == "openldap_up":
			sawUp = true
		case strings.HasPrefix(name, "openldap_connections") ||
			strings.HasPrefix(name, "openldap_threads") ||
			strings.HasPrefix(name, "openldap_operations"):
			sawMonitor = true
		}
	}
	if !sawUp {
		t.Error("openldap_up metric missing from gather output")
	}
	if !sawMonitor {
		t.Error("No metric sourced from cn=Monitor was gathered — accesslog DB might be missing or ACLs are wrong")
	}
}

// TestConcurrentMetrics ensures that running multiple Gather() calls in
// parallel does not corrupt internal state or deadlock.
func TestConcurrentMetrics(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	registry := prometheus.NewRegistry()
	if err := registry.Register(exp); err != nil {
		t.Fatalf("Failed to register exporter: %v", err)
	}

	const numGoroutines = 5
	results := make(chan error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := registry.Gather()
			results <- err
		}()
	}
	for i := 0; i < numGoroutines; i++ {
		if err := <-results; err != nil {
			t.Errorf("Goroutine %d failed: %v", i, err)
		}
	}
}

// TestMetricConsistency checks that two successive Gather() calls expose
// the same set of metric family names.
func TestMetricConsistency(t *testing.T) {
	cfg := getTestConfig(t)
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	registry := prometheus.NewRegistry()
	if err := registry.Register(exp); err != nil {
		t.Fatalf("Failed to register exporter: %v", err)
	}

	metrics1, err := registry.Gather()
	if err != nil {
		t.Fatalf("First metric collection failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	metrics2, err := registry.Gather()
	if err != nil {
		t.Fatalf("Second metric collection failed: %v", err)
	}

	if len(metrics1) != len(metrics2) {
		t.Errorf("Metric family count changed: %d -> %d", len(metrics1), len(metrics2))
	}

	names1 := metricNames(metrics1)
	names2 := metricNames(metrics2)
	for _, name := range names1 {
		if !contains(names2, name) {
			t.Errorf("Metric %s disappeared between collections", name)
		}
	}
}

func metricNames(metricFamilies []*dto.MetricFamily) []string {
	names := make([]string, 0, len(metricFamilies))
	for _, mf := range metricFamilies {
		if mf.Name != nil {
			names = append(names, *mf.Name)
		}
	}
	return names
}

func contains(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}
