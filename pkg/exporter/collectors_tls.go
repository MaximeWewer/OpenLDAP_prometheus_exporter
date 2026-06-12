package exporter

import (
	"crypto/x509"
	"crypto/x509/pkix"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// collectTLSCertMetrics records the expiration (NotAfter) of every certificate
// the LDAP server presents on the live TLS handshake — the leaf server
// certificate and the intermediate/root CAs in the chain. This reflects what
// real clients actually negotiate, with no dependency on local certificate
// files. When TLS is disabled the server presents no chain and nothing is
// emitted.
//
// The gauge is reset on every scrape so that after a renewal the old
// serial-numbered series disappears instead of lingering forever.
func (e *OpenLDAPExporter) collectTLSCertMetrics(server string) {
	e.metricsRegistry.TlsCertNotAfter.Reset()

	certs, err := e.client.PeerCertificates()
	if err != nil {
		logger.SafeError("exporter", "Failed to read TLS peer certificates", err, map[string]interface{}{
			"server": server,
		})
		return
	}

	// TLS disabled or non-TLS connection: nothing to report.
	if len(certs) == 0 {
		return
	}

	e.recordCertChain(server, certs)
	logMetricCollection(server, "tls_cert", len(certs))
}

// recordCertChain sets one TlsCertNotAfter series per certificate in the
// presented chain. The first certificate is the server's leaf (usage="server");
// the rest are the issuing CAs (usage="ca"). Split out from collectTLSCertMetrics
// so it can be exercised directly with an in-memory chain in tests, without a
// live TLS connection.
func (e *OpenLDAPExporter) recordCertChain(server string, certs []*x509.Certificate) {
	for i, cert := range certs {
		usage := "ca"
		if i == 0 {
			usage = "server"
		}

		e.metricsRegistry.TlsCertNotAfter.With(prometheus.Labels{
			"server":  server,
			"usage":   usage,
			"subject": certName(cert.Subject),
			"issuer":  certName(cert.Issuer),
			"serial":  cert.SerialNumber.String(),
		}).Set(float64(cert.NotAfter.Unix()))
	}
}

// certName returns the certificate's Common Name, falling back to the full
// RDN sequence when the CN is empty (some CAs issue certs with only an O/OU).
func certName(name pkix.Name) string {
	if name.CommonName != "" {
		return name.CommonName
	}
	return name.String()
}
