// Package csn parses OpenLDAP Change Sequence Numbers (contextCSN values).
// It is shared by the metric scrape collector and the replication peer poller
// so the two never drift on the parsing rules.
package csn

import (
	"errors"
	"strings"
	"time"
)

// Parse parses an OpenLDAP contextCSN value.
// Format: YYYYMMDDHHmmss.ffffffZ#SSSSSS#SID#MMMMMM
// It returns the parsed timestamp and the server ID (SID).
func Parse(csn string) (time.Time, string, error) {
	// Split on '#' to get components.
	parts := strings.SplitN(csn, "#", 4)
	if len(parts) < 3 {
		return time.Time{}, "", errors.New("invalid CSN format: expected at least 3 '#'-separated components")
	}

	// Parse timestamp (part 0): YYYYMMDDHHmmss.ffffffZ
	timestamp := parts[0]

	// Parse timestamp, handling variable microsecond precision.
	// OpenLDAP may produce 1-6 fractional digits (e.g., .0Z, .00Z, .000000Z).
	var t time.Time
	var err error

	if dotIdx := strings.IndexByte(timestamp, '.'); dotIdx >= 0 {
		// Normalize fractional part to exactly 6 digits for consistent parsing.
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

	// Server ID is part 2 (3 hex digits, e.g., "000", "001").
	serverID := parts[2]

	return t, serverID, nil
}
