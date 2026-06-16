package replication

import (
	"sort"
	"strings"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/pool"
)

// discoverPeers builds the set of peer LDAP URIs to poll: the manually
// configured OPENLDAP_REPLICATION_PEERS plus any provider= URIs parsed from the
// local olcSyncrepl entries in cn=config. The result is de-duplicated and
// sorted for stable iteration/labels.
//
// Auto-discovery is best-effort: when the bind identity cannot read cn=config
// the manual list is used on its own.
func discoverPeers(cfg *config.Config, local *pool.PooledLDAPClient) []string {
	set := make(map[string]struct{})

	for _, p := range cfg.ReplicationPeers {
		if u := strings.TrimSpace(p); u != "" {
			set[u] = struct{}{}
		}
	}

	if local != nil {
		result, err := local.SearchConfig("(olcSyncrepl=*)", []string{"olcSyncrepl"})
		if err != nil {
			logger.SafeDebug("replication", "Peer auto-discovery skipped (cannot read cn=config)", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			for _, entry := range result.Entries {
				for _, attr := range entry.Attributes {
					if attr.Name != "olcSyncrepl" {
						continue
					}
					for _, v := range attr.Values {
						if uri := parseProviderURI(v); uri != "" {
							set[uri] = struct{}{}
						}
					}
				}
			}
		}
	}

	peers := make([]string, 0, len(set))
	for u := range set {
		peers = append(peers, u)
	}
	sort.Strings(peers)
	return peers
}

// parseProviderURI extracts the provider URI from a single olcSyncrepl value.
// The value looks like `{0}rid=001 provider=ldaps://host:636 searchbase=... ...`
// (token order is not guaranteed). The raw value can also contain
// credentials=... — never logged or returned here, only the provider URI is.
func parseProviderURI(syncreplValue string) string {
	for _, tok := range strings.Fields(syncreplValue) {
		if rest, ok := strings.CutPrefix(tok, "provider="); ok {
			return strings.Trim(rest, "\"")
		}
	}
	return ""
}

// buildPeerClient creates a dedicated pooled LDAP client (own pool + circuit
// breaker, no shared monitoring) for a single peer URI. TLS is enabled when the
// URI scheme is ldaps:// (or TLS is enabled globally); the CA and circuit
// breaker tuning are inherited from the main config, while bind identity and
// TLS server name use the peer-specific settings with sane fallbacks.
func buildPeerClient(cfg *config.Config, uri string) *pool.PooledLDAPClient {
	username := cfg.PeerUsername
	if username == "" {
		username = cfg.Username
	}
	password := cfg.PeerPassword
	if password == nil {
		password = cfg.Password
	}

	peerCfg := &config.Config{
		URL:           uri,
		ServerName:    uri,
		TLS:           isLDAPS(uri) || cfg.TLS,
		TLSSkipVerify: cfg.TLSSkipVerify,
		TLSCA:         cfg.TLSCA,
		TLSServerName: cfg.PeerServerName,
		Timeout:       cfg.Timeout,
		Username:      username,
		Password:      password,
		AuthMethod:    "simple",
		// Reuse the circuit breaker tuning so peer breakers behave like the rest.
		CBMaxFailures:      cfg.CBMaxFailures,
		CBTimeout:          cfg.CBTimeout,
		CBResetTimeout:     cfg.CBResetTimeout,
		CBSuccessThreshold: cfg.CBSuccessThreshold,
	}

	return pool.NewPooledLDAPClient(peerCfg)
}

func isLDAPS(uri string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(uri)), "ldaps://")
}
