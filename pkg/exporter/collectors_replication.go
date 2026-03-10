package exporter

import (
	"errors"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

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

	logMetricCollection(server, "replication", count)
}

// parseContextCSN parses an OpenLDAP contextCSN value.
// Format: YYYYMMDDHHmmss.ffffffZ#SSSSSS#SID#MMMMMM
// Returns the parsed timestamp and the server ID (SID).
func parseContextCSN(csn string) (time.Time, string, error) {
	// Split on '#' to get components
	parts := strings.SplitN(csn, "#", 4)
	if len(parts) < 3 {
		return time.Time{}, "", errors.New("invalid CSN format: expected at least 3 '#'-separated components")
	}

	// Parse timestamp (part 0): YYYYMMDDHHmmss.ffffffZ
	timestamp := parts[0]

	// Parse timestamp, handling variable microsecond precision
	// OpenLDAP may produce 1-6 fractional digits (e.g., .0Z, .00Z, .000000Z)
	var t time.Time
	var err error

	if dotIdx := strings.IndexByte(timestamp, '.'); dotIdx >= 0 {
		// Normalize fractional part to exactly 6 digits for consistent parsing
		zIdx := strings.IndexByte(timestamp, 'Z')
		if zIdx < 0 {
			return time.Time{}, "", errors.New("invalid CSN timestamp: missing Z suffix")
		}
		frac := timestamp[dotIdx+1 : zIdx]
		for len(frac) < 6 {
			frac += "0"
		}
		if len(frac) > 6 {
			frac = frac[:6]
		}
		normalized := timestamp[:dotIdx+1] + frac + "Z"
		t, err = time.Parse("20060102150405.000000Z", normalized)
	} else {
		t, err = time.Parse("20060102150405Z", timestamp)
	}
	if err != nil {
		return time.Time{}, "", err
	}

	// Server ID is part 2 (3 hex digits, e.g., "000", "001")
	serverID := parts[2]

	return t, serverID, nil
}
