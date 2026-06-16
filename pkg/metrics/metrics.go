package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// OpenLDAPMetrics holds all Prometheus metrics for OpenLDAP monitoring
type OpenLDAPMetrics struct {
	ConnectionsCurrent         *prometheus.GaugeVec
	ConnectionsTotal           *prometheus.CounterVec
	BytesTotal                 *prometheus.CounterVec
	PduTotal                   *prometheus.CounterVec
	ReferralsTotal             *prometheus.CounterVec
	EntriesTotal               *prometheus.CounterVec
	ThreadsMax                 *prometheus.GaugeVec
	ThreadsMaxPending          *prometheus.GaugeVec
	ThreadsBackload            *prometheus.GaugeVec
	ThreadsActive              *prometheus.GaugeVec
	ThreadsOpen                *prometheus.GaugeVec
	ThreadsStarting            *prometheus.GaugeVec
	ThreadsPending             *prometheus.GaugeVec
	ThreadsState               *prometheus.GaugeVec
	OperationsInitiated        *prometheus.CounterVec
	OperationsCompleted        *prometheus.CounterVec
	WaitersRead                *prometheus.GaugeVec
	WaitersWrite               *prometheus.GaugeVec
	OverlaysInfo               *prometheus.GaugeVec
	ServerTime                 *prometheus.GaugeVec
	ServerUptime               *prometheus.GaugeVec
	TlsInfo                    *prometheus.GaugeVec
	TlsCertNotAfter            *prometheus.GaugeVec
	BackendsInfo               *prometheus.GaugeVec
	ListenersInfo              *prometheus.GaugeVec
	DatabaseEntries            *prometheus.GaugeVec
	DatabaseInfo               *prometheus.GaugeVec
	HealthStatus               *prometheus.GaugeVec
	ResponseTime               *prometheus.HistogramVec
	ScrapeErrors               *prometheus.CounterVec
	Up                         *prometheus.GaugeVec
	ServerInfo                 *prometheus.GaugeVec
	LogLevels                  *prometheus.GaugeVec
	SaslInfo                   *prometheus.GaugeVec
	ReplicationCSN             *prometheus.GaugeVec
	ReplicationLag             *prometheus.GaugeVec
	ReplicationMultiProvider   *prometheus.GaugeVec
	ReplicationConfiguredPeers *prometheus.GaugeVec
	ReplicationPeerUp          *prometheus.GaugeVec
	ReplicationPeerCSN         *prometheus.GaugeVec
	ReplicationPeerLag         *prometheus.GaugeVec
	ConnectionsByProtocol      *prometheus.GaugeVec
	ConnectionOpsAggregate     *prometheus.GaugeVec
	SupportedControlInfo       *prometheus.GaugeVec

	// PPolicy metrics
	PpolicyPwdChangedTimestamp *prometheus.GaugeVec
	PpolicyPwdLastSuccess      *prometheus.GaugeVec
	PpolicyPwdGraceUseCount    *prometheus.GaugeVec
	PpolicyPwdReset            *prometheus.GaugeVec

	// Accesslog metrics (event-based counters, incrementally scanned by reqStart cursor)
	AccesslogBindTotal       *prometheus.CounterVec
	AccesslogWriteTotal      *prometheus.CounterVec
	AccesslogLockEventsTotal *prometheus.CounterVec
}

// NewOpenLDAPMetrics creates and initializes all OpenLDAP Prometheus metrics
func NewOpenLDAPMetrics() *OpenLDAPMetrics {
	return &OpenLDAPMetrics{
		ConnectionsCurrent: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "connections_current",
				Help:      "Current number of connections (cn=Current,cn=Connections,cn=Monitor)",
			},
			[]string{"server"},
		),
		ConnectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "connections_total",
				Help:      "Total number of connections (cn=Total,cn=Connections,cn=Monitor)",
			},
			[]string{"server"},
		),

		BytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "bytes_total",
				Help:      "Total bytes sent (cn=Bytes,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),
		PduTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "pdu_total",
				Help:      "Total PDUs processed (cn=PDU,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),
		ReferralsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "referrals_total",
				Help:      "Total referrals sent (cn=Referrals,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),
		EntriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "entries_total",
				Help:      "Total entries sent (cn=Entries,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),

		// Thread metrics
		ThreadsMax: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_max",
				Help:      "Maximum number of threads (cn=Max,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsMaxPending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_max_pending",
				Help:      "Maximum number of pending threads (cn=Max Pending,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsBackload: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_backload",
				Help:      "Current thread backload (cn=Backload,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_active",
				Help:      "Number of active threads (cn=Active,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsOpen: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_open",
				Help:      "Number of open threads (cn=Open,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsStarting: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_starting",
				Help:      "Number of starting threads (cn=Starting,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsPending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_pending",
				Help:      "Number of pending threads (cn=Pending,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		ThreadsState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_state",
				Help:      "Number of threads in each state (cn=State,cn=Threads,cn=Monitor)",
			},
			[]string{"server", "state"},
		),

		// Operation metrics
		OperationsInitiated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "operations_initiated_total",
				Help:      "Total number of operations initiated (cn=Operations,cn=Monitor)",
			},
			[]string{"server", "operation"},
		),
		OperationsCompleted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "operations_completed_total",
				Help:      "Total number of operations completed (cn=Operations,cn=Monitor)",
			},
			[]string{"server", "operation"},
		),

		// Waiter metrics
		WaitersRead: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "waiters_read",
				Help:      "Number of read waiters (cn=Read,cn=Waiters,cn=Monitor)",
			},
			[]string{"server"},
		),
		WaitersWrite: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "waiters_write",
				Help:      "Number of write waiters (cn=Write,cn=Waiters,cn=Monitor)",
			},
			[]string{"server"},
		),

		// Overlay metrics
		OverlaysInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "overlays_info",
				Help:      "Loaded overlay modules (value is always 1, cn=Overlays,cn=Monitor)",
			},
			[]string{"server", "overlay", "status"},
		),

		// Time metrics
		ServerTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "server_time",
				Help:      "Current server time (cn=Current,cn=Time,cn=Monitor)",
			},
			[]string{"server"},
		),
		ServerUptime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "server_uptime_seconds",
				Help:      "Server uptime in seconds (cn=Start,cn=Time,cn=Monitor)",
			},
			[]string{"server"},
		),

		// TLS metrics
		TlsInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "tls_info",
				Help:      "TLS configuration components (value is always 1, cn=TLS,cn=Monitor)",
			},
			[]string{"server", "component", "status"},
		),

		// Expiration (NotAfter) of each certificate the LDAP server presents
		// on the live TLS handshake. Value is the Unix timestamp in seconds.
		// usage="server" is the leaf cert, usage="ca" covers the intermediate
		// and root CAs in the presented chain. Only emitted when TLS is on.
		TlsCertNotAfter: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "tls_cert_not_after_timestamp_seconds",
				Help:      "Expiration (NotAfter) of certificates in the server's live TLS chain, as a Unix timestamp",
			},
			[]string{"server", "usage", "subject", "issuer", "serial"},
		),

		// Backend metrics
		BackendsInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "backends_info",
				Help:      "Available database backends (value is always 1, cn=Backends,cn=Monitor)",
			},
			[]string{"server", "backend", "type"},
		),

		// Listener metrics
		ListenersInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "listeners_info",
				Help:      "Active network listeners with bind address (value is always 1, cn=Listeners,cn=Monitor)",
			},
			[]string{"server", "listener", "address"},
		),

		// Database metrics
		DatabaseEntries: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "database_entries",
				Help:      "Number of entries per database/base DN with domain component filtering",
			},
			[]string{"server", "base_dn", "domain_component"},
		),

		// System health metrics
		HealthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "health_status",
				Help:      "Health status of the OpenLDAP server (1=healthy, 0=unhealthy)",
			},
			[]string{"server"},
		),
		ResponseTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "openldap",
				Name:      "response_time_seconds",
				Help:      "Histogram of LDAP health check response times in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
			},
			[]string{"server"},
		),
		ScrapeErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "scrape_errors_total",
				Help:      "Total number of scrape errors",
			},
			[]string{"server"},
		),

		Up: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "up",
				Help:      "Whether the OpenLDAP server is reachable (1=up, 0=down)",
			},
			[]string{"server"},
		),

		// New metrics for comprehensive monitoring
		ServerInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "server_info",
				Help:      "OpenLDAP server and exporter version information (value is always 1)",
			},
			[]string{"server", "version", "exporter_version", "description"},
		),

		DatabaseInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "database_info",
				Help:      "Information about databases including shadow, context, and readonly status",
			},
			[]string{"server", "base_dn", "is_shadow", "context", "readonly"},
		),

		LogLevels: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "log_level_enabled",
				Help:      "Enabled log levels in cn=Log,cn=Monitor (1=enabled, 0=disabled)",
			},
			[]string{"server", "log_type"},
		),

		SaslInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "sasl_info",
				Help:      "SASL mechanism information (cn=SASL,cn=Monitor)",
			},
			[]string{"server", "mechanism", "status"},
		),

		// Replication metrics
		ReplicationCSN: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_csn_timestamp",
				Help:      "Unix timestamp extracted from contextCSN per database and server ID",
			},
			[]string{"server", "base_dn", "server_id"},
		),
		ReplicationLag: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_lag_seconds",
				Help:      "Seconds since the last contextCSN update per database and server ID",
			},
			[]string{"server", "base_dn", "server_id"},
		),
		ReplicationMultiProvider: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_multi_provider",
				Help:      "Replication topology per database: 1 = multi-provider (active-active, olcMultiProvider/olcMirrorMode TRUE), 0 = single-provider (active-passive). Read from cn=config; only populated when the bind identity can read the config tree",
			},
			[]string{"server", "base_dn"},
		),
		ReplicationConfiguredPeers: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_configured_peers",
				Help:      "Number of syncrepl providers (olcSyncrepl entries) configured on this database. Read from cn=config; only populated when the bind identity can read the config tree",
			},
			[]string{"server", "base_dn"},
		),
		ReplicationPeerUp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_peer_up",
				Help:      "Reachability of each replication peer as polled directly from this node: 1 = connected and bound, 0 = unreachable. Only populated when OPENLDAP_REPLICATION_POLL_ENABLED=true",
			},
			[]string{"peer"},
		),
		ReplicationPeerCSN: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_peer_csn_timestamp",
				Help:      "Unix timestamp of a peer's contextCSN per database and server ID, polled directly from this node",
			},
			[]string{"peer", "base_dn", "server_id"},
		),
		ReplicationPeerLag: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "replication_peer_lag_seconds",
				Help:      "Propagation lag of a peer in seconds: reference CSN (max of local and all peers for that server_id) minus the peer's CSN. The most up-to-date source reads 0",
			},
			[]string{"peer", "base_dn", "server_id"},
		),

		ConnectionsByProtocol: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "connections_by_protocol",
				Help:      "Number of individual connections by LDAP protocol version",
			},
			[]string{"server", "protocol"},
		),
		ConnectionOpsAggregate: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "connection_ops_aggregate",
				Help:      "Aggregate operation counts across all individual connections",
			},
			[]string{"server", "state"},
		),
		SupportedControlInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "supported_control_info",
				Help:      "LDAP controls supported by the server (from RootDSE)",
			},
			[]string{"server", "oid"},
		),

		// PPolicy metrics
		PpolicyPwdChangedTimestamp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "ppolicy_pwd_changed_timestamp",
				Help:      "Unix timestamp of last password change (from pwdChangedTime)",
			},
			[]string{"server", "user_dn", "user", "base_dn"},
		),
		PpolicyPwdLastSuccess: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "ppolicy_pwd_last_success_timestamp",
				Help:      "Unix timestamp of last successful bind (from pwdLastSuccess, requires ppolicy_hash_cleartext)",
			},
			[]string{"server", "user_dn", "user", "base_dn"},
		),
		PpolicyPwdGraceUseCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "ppolicy_pwd_grace_use_count",
				Help:      "Number of grace logins used after password expiration (from pwdGraceUseTime)",
			},
			[]string{"server", "user_dn", "user", "base_dn"},
		),
		PpolicyPwdReset: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "ppolicy_pwd_reset",
				Help:      "Whether user must change password on next bind (1=must change, 0=no, from pwdReset)",
			},
			[]string{"server", "user_dn", "user", "base_dn"},
		),

		// Accesslog metrics (event-based counters).
		AccesslogBindTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "accesslog_bind_total",
				Help:      "Cumulative bind operations per user and result observed via accesslog incremental scan (from cn=accesslog)",
			},
			[]string{"server", "user", "result"},
		),
		AccesslogWriteTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "accesslog_write_total",
				Help:      "Cumulative write operations per user and type observed via accesslog incremental scan (from cn=accesslog)",
			},
			[]string{"server", "user", "operation"},
		),
		AccesslogLockEventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "accesslog_account_lock_events_total",
				Help:      "Cumulative account lock events per user observed via accesslog (auditModify touching pwdAccountLockedTime)",
			},
			[]string{"server", "user"},
		),
	}
}

// Collectors returns every Prometheus collector managed by this registry.
// It is the single source of truth the exporter's Describe / Collect
// loops iterate over, so adding a new metric in one place is enough —
// there is no parallel list to keep in sync.
func (m *OpenLDAPMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		// Connection metrics
		m.ConnectionsCurrent,
		m.ConnectionsTotal,

		// Statistics metrics
		m.BytesTotal,
		m.PduTotal,
		m.ReferralsTotal,
		m.EntriesTotal,

		// Thread metrics
		m.ThreadsMax,
		m.ThreadsMaxPending,
		m.ThreadsBackload,
		m.ThreadsActive,
		m.ThreadsOpen,
		m.ThreadsStarting,
		m.ThreadsPending,
		m.ThreadsState,

		// Operation metrics
		m.OperationsInitiated,
		m.OperationsCompleted,

		// Waiter metrics
		m.WaitersRead,
		m.WaitersWrite,

		// Overlay metrics
		m.OverlaysInfo,

		// Time metrics
		m.ServerTime,
		m.ServerUptime,

		// TLS metrics
		m.TlsInfo,
		m.TlsCertNotAfter,

		// Backend metrics
		m.BackendsInfo,

		// Listener metrics
		m.ListenersInfo,

		// Database metrics
		m.DatabaseEntries,
		m.DatabaseInfo,

		// System health metrics
		m.HealthStatus,
		m.ResponseTime,
		m.ScrapeErrors,
		m.Up,

		// Additional metrics
		m.ServerInfo,
		m.LogLevels,
		m.SaslInfo,

		// Replication metrics
		m.ReplicationCSN,
		m.ReplicationLag,
		m.ReplicationMultiProvider,
		m.ReplicationConfiguredPeers,
		m.ReplicationPeerUp,
		m.ReplicationPeerCSN,
		m.ReplicationPeerLag,

		// Individual connection metrics
		m.ConnectionsByProtocol,
		m.ConnectionOpsAggregate,

		// RootDSE metrics
		m.SupportedControlInfo,

		// PPolicy metrics
		m.PpolicyPwdChangedTimestamp,
		m.PpolicyPwdLastSuccess,
		m.PpolicyPwdGraceUseCount,
		m.PpolicyPwdReset,

		// Accesslog metrics
		m.AccesslogBindTotal,
		m.AccesslogWriteTotal,
		m.AccesslogLockEventsTotal,
	}
}

// Describe implements the prometheus.Collector interface
func (m *OpenLDAPMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range m.Collectors() {
		metric.Describe(ch)
	}
}

// Collect implements the prometheus.Collector interface
func (m *OpenLDAPMetrics) Collect(ch chan<- prometheus.Metric) {
	for _, metric := range m.Collectors() {
		metric.Collect(ch)
	}
}
