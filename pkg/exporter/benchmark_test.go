package exporter

import (
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/metrics"
)

// setupBenchConfig sets up a minimal config for benchmarks
func setupBenchConfig(b *testing.B) (*config.Config, func()) {
	b.Helper()

	envVars := map[string]string{
		"LDAP_URL":      "ldap://localhost:389",
		"LDAP_USERNAME": "cn=admin,dc=test",
		"LDAP_PASSWORD": "test",
	}

	for k, v := range envVars {
		os.Setenv(k, v)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		b.Fatalf("Failed to load config: %v", err)
	}

	cleanup := func() {
		cfg.Clear()
		for k := range envVars {
			os.Unsetenv(k)
		}
	}

	return cfg, cleanup
}

// BenchmarkMetricsCreation benchmarks metric registry creation
func BenchmarkMetricsCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = metrics.NewOpenLDAPMetrics()
	}
}

// BenchmarkMetricsDescribe benchmarks the Describe method
func BenchmarkMetricsDescribe(b *testing.B) {
	m := metrics.NewOpenLDAPMetrics()
	ch := make(chan *prometheus.Desc, 200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Describe(ch)
		// Drain channel
		for len(ch) > 0 {
			<-ch
		}
	}
}

// BenchmarkMetricsCollect benchmarks the Collect method
func BenchmarkMetricsCollect(b *testing.B) {
	m := metrics.NewOpenLDAPMetrics()
	// Pre-populate some metrics
	m.ConnectionsCurrent.WithLabelValues("bench-server").Set(42)
	m.ThreadsActive.WithLabelValues("bench-server").Set(8)
	m.HealthStatus.WithLabelValues("bench-server").Set(1)

	ch := make(chan prometheus.Metric, 200)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Collect(ch)
		for len(ch) > 0 {
			<-ch
		}
	}
}

// BenchmarkCounterUpdate benchmarks counter delta tracking
func BenchmarkCounterUpdate(b *testing.B) {
	cfg, cleanup := setupBenchConfig(b)
	defer cleanup()

	exp := NewOpenLDAPExporter(cfg)
	defer exp.Close()

	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bench_counter_total",
			Help: "Benchmark counter",
		},
		[]string{"server"},
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		exp.updateCounter(counter, "bench-server", "bench_key", float64(i))
	}
}

// BenchmarkValidateKey benchmarks key validation
func BenchmarkValidateKey(b *testing.B) {
	keys := []string{
		"connections_total",
		"operations_initiated_total_bind",
		"bytes_total",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validateKey(keys[i%len(keys)])
	}
}

// BenchmarkExtractCN benchmarks CN extraction from DN
func BenchmarkExtractCN(b *testing.B) {
	dns := []string{
		"cn=Current,cn=Connections,cn=Monitor",
		"cn=Bind,cn=Operations,cn=Monitor",
		"cn=Connection 42,cn=Connections,cn=Monitor",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractCNFromFirstComponent(dns[i%len(dns)])
	}
}
