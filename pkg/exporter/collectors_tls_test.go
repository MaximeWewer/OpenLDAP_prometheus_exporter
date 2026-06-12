package exporter

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// makeCert builds a minimal in-memory certificate. recordCertChain only reads
// Subject, Issuer, SerialNumber and NotAfter, so no key material or signing is
// needed — a struct literal is enough to drive the metric.
func makeCert(cn, issuer string, serial int64, notAfter time.Time) *x509.Certificate {
	return &x509.Certificate{
		Subject:      pkix.Name{CommonName: cn},
		Issuer:       pkix.Name{CommonName: issuer},
		SerialNumber: big.NewInt(serial),
		NotAfter:     notAfter,
	}
}

// TestRecordCertChain verifies the leaf/CA split and that each certificate's
// NotAfter is exported as a Unix-timestamp gauge with the expected labels.
func TestRecordCertChain(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	const server = "test-server"
	leafExpiry := time.Unix(1800000000, 0) // leaf cert
	caExpiry := time.Unix(1900000000, 0)   // issuing CA

	chain := []*x509.Certificate{
		makeCert("ldap.example.com", "Example CA", 1001, leafExpiry),
		makeCert("Example CA", "Example CA", 1, caExpiry),
	}

	exporter.metricsRegistry.TlsCertNotAfter.Reset()
	exporter.recordCertChain(server, chain)

	// Leaf certificate: usage="server".
	leafGauge := exporter.metricsRegistry.TlsCertNotAfter.WithLabelValues(
		server, "server", "ldap.example.com", "Example CA", "1001",
	)
	if got := testutil.ToFloat64(leafGauge); got != float64(leafExpiry.Unix()) {
		t.Errorf("leaf NotAfter = %v, want %v", got, float64(leafExpiry.Unix()))
	}

	// CA certificate: usage="ca".
	caGauge := exporter.metricsRegistry.TlsCertNotAfter.WithLabelValues(
		server, "ca", "Example CA", "Example CA", "1",
	)
	if got := testutil.ToFloat64(caGauge); got != float64(caExpiry.Unix()) {
		t.Errorf("CA NotAfter = %v, want %v", got, float64(caExpiry.Unix()))
	}

	// Exactly two series should exist for the chain.
	if n := testutil.CollectAndCount(exporter.metricsRegistry.TlsCertNotAfter); n != 2 {
		t.Errorf("series count = %d, want 2", n)
	}
}

// TestCollectTLSCertMetricsTLSDisabled ensures that with TLS disabled (the
// test config uses a plain ldap:// URL) the collector emits no certificate
// series instead of erroring.
func TestCollectTLSCertMetricsTLSDisabled(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	exporter := NewOpenLDAPExporter(cfg)
	defer exporter.Close()

	exporter.collectTLSCertMetrics("test-server")

	if n := testutil.CollectAndCount(exporter.metricsRegistry.TlsCertNotAfter); n != 0 {
		t.Errorf("expected no cert series with TLS disabled, got %d", n)
	}
}

// TestCertName checks the Common Name extraction and its fallback to the full
// RDN sequence when the CN is empty.
func TestCertName(t *testing.T) {
	if got := certName(pkix.Name{CommonName: "ldap.example.com"}); got != "ldap.example.com" {
		t.Errorf("certName with CN = %q, want %q", got, "ldap.example.com")
	}

	// No CN: fall back to the RDN string (which includes the O).
	got := certName(pkix.Name{Organization: []string{"Example Org"}})
	if got == "" {
		t.Error("certName fallback returned empty string")
	}
}
