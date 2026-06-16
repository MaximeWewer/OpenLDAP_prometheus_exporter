package exporter

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/csn"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// collectReplicationMetrics collects replication status via contextCSN.
// For each database with a naming context, queries the suffix entry for contextCSN.
// The CSN format is: YYYYMMDDHHmmss.ffffffZ#SSSSSS#SID#MMMMMM
// Multiple CSN values indicate multi-master replication (one per server ID).
func (e *OpenLDAPExporter) collectReplicationMetrics(server string) {
	if !e.shouldCollectMetric("replication") {
		return
	}

	// Step 1: Get naming contexts from the monitor database entries
	result, err := e.searchLDAP(
		"cn=Databases,cn=Monitor",
		"(objectClass=*)",
		[]string{"namingContexts"},
		"replication",
	)
	if err != nil {
		return
	}

	now := time.Now()
	count := 0

	for _, entry := range result.Entries {
		if entry.DN == "cn=Databases,cn=Monitor" {
			continue
		}

		// Extract the naming context (suffix DN)
		var suffixDN string
		for _, attr := range entry.Attributes {
			if attr.Name == "namingContexts" && len(attr.Values) > 0 {
				suffixDN = attr.Values[0]
				break
			}
		}
		if suffixDN == "" {
			continue
		}

		// Skip non-data suffixes (cn=config, cn=accesslog, cn=monitor, etc.)
		suffixLower := strings.ToLower(suffixDN)
		if strings.HasPrefix(suffixLower, "cn=") {
			continue
		}

		// Apply domain component filtering
		domainComponents := e.extractDomainComponents(suffixDN)
		if !e.shouldIncludeDomain(domainComponents) {
			continue
		}

		// Step 2: Query contextCSN on the suffix entry (base scope)
		csnResult, err := e.client.SearchContextCSN(suffixDN)
		if err != nil {
			logger.SafeDebug("exporter", "Could not query contextCSN", map[string]interface{}{
				"base_dn": suffixDN,
				"error":   err.Error(),
			})
			continue
		}

		if len(csnResult.Entries) == 0 {
			continue
		}

		// Step 3: Parse each contextCSN value
		for _, attr := range csnResult.Entries[0].Attributes {
			if attr.Name != "contextCSN" {
				continue
			}

			for _, csn := range attr.Values {
				csnTime, serverID, err := parseContextCSN(csn)
				if err != nil {
					logger.SafeWarn("exporter", "Failed to parse contextCSN", map[string]interface{}{
						"csn":     csn,
						"base_dn": suffixDN,
						"error":   err.Error(),
					})
					continue
				}

				labels := prometheus.Labels{
					"server":    server,
					"base_dn":   suffixDN,
					"server_id": serverID,
				}

				e.metricsRegistry.ReplicationCSN.With(labels).Set(float64(csnTime.Unix()))
				e.metricsRegistry.ReplicationLag.With(labels).Set(now.Sub(csnTime).Seconds())
				count++
			}
		}
	}

	// Topology (active-active vs active-passive, configured peer count) is read
	// from cn=config. It is best-effort: when the bind identity cannot read the
	// config tree the search simply yields nothing and the contextCSN metrics
	// above are unaffected.
	e.collectReplicationTopology(server)

	logMetricCollection(server, "replication", count)
}

// collectReplicationTopology reads each data database's replication
// configuration from cn=config and exposes whether it is multi-provider
// (active-active) and how many syncrepl providers are configured.
//
// olcMultiProvider is the OpenLDAP 2.6 attribute; olcMirrorMode is the legacy
// name for the same flag — either being TRUE means active-active. The raw
// olcSyncrepl values (which may embed bind credentials) are only counted here,
// never stored or logged.
func (e *OpenLDAPExporter) collectReplicationTopology(server string) {
	result, err := e.client.SearchConfig(
		"(olcSuffix=*)",
		[]string{"olcSuffix", "olcSyncrepl", "olcMultiProvider", "olcMirrorMode"},
	)
	if err != nil {
		logger.SafeDebug("exporter", "Could not read replication topology from cn=config", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	for _, entry := range result.Entries {
		var suffixDN string
		multiProvider := 0.0
		peers := 0.0

		for _, attr := range entry.Attributes {
			switch attr.Name {
			case "olcSuffix":
				if len(attr.Values) > 0 {
					suffixDN = attr.Values[0]
				}
			case "olcSyncrepl":
				peers = float64(len(attr.Values))
			case "olcMultiProvider", "olcMirrorMode":
				if len(attr.Values) > 0 && strings.EqualFold(attr.Values[0], "TRUE") {
					multiProvider = 1.0
				}
			}
		}

		if suffixDN == "" {
			continue
		}

		// Apply the same domain-component filtering as the contextCSN metrics.
		domainComponents := e.extractDomainComponents(suffixDN)
		if !e.shouldIncludeDomain(domainComponents) {
			continue
		}

		labels := prometheus.Labels{"server": server, "base_dn": suffixDN}
		e.metricsRegistry.ReplicationMultiProvider.With(labels).Set(multiProvider)
		e.metricsRegistry.ReplicationConfiguredPeers.With(labels).Set(peers)
	}
}

// parseContextCSN parses an OpenLDAP contextCSN value into a timestamp and
// server ID. It delegates to the shared csn package so the scrape collector and
// the replication peer poller use identical parsing rules.
func parseContextCSN(value string) (time.Time, string, error) {
	return csn.Parse(value)
}
