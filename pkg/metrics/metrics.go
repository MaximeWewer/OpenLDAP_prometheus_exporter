package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// OpenLDAPMetrics holds all Prometheus metrics for OpenLDAP monitoring
type OpenLDAPMetrics struct {
	ConnectionsCurrent  *prometheus.GaugeVec
	ConnectionsTotal    *prometheus.CounterVec
	BytesTotal          *prometheus.CounterVec
	PduTotal            *prometheus.CounterVec
	ReferralsTotal      *prometheus.CounterVec
	EntriesTotal        *prometheus.CounterVec
	ThreadsMax          *prometheus.GaugeVec
	ThreadsMaxPending   *prometheus.GaugeVec
	ThreadsBackload     *prometheus.GaugeVec
	ThreadsActive       *prometheus.GaugeVec
	ThreadsOpen         *prometheus.GaugeVec
	ThreadsStarting     *prometheus.GaugeVec
	ThreadsPending      *prometheus.GaugeVec
	ThreadsState        *prometheus.GaugeVec
	OperationsInitiated *prometheus.CounterVec
	OperationsCompleted *prometheus.CounterVec
	WaitersRead         *prometheus.GaugeVec
	WaitersWrite        *prometheus.GaugeVec
	OverlaysInfo        *prometheus.GaugeVec
	ServerTime          *prometheus.GaugeVec
	ServerUptime        *prometheus.GaugeVec
	TlsInfo             *prometheus.GaugeVec
	BackendsInfo        *prometheus.GaugeVec
	ListenersInfo       *prometheus.GaugeVec
	DatabaseEntries     *prometheus.GaugeVec
	HealthStatus        *prometheus.GaugeVec
	ResponseTime        *prometheus.GaugeVec
	ScrapeErrors        *prometheus.CounterVec
	Up                  *prometheus.GaugeVec
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
				Help:      "Thread pool state (cn=State,cn=Threads,cn=Monitor)",
			},
			[]string{"server", "state"},
		),

		// Operation metrics
		OperationsInitiated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "operations_initiated_total",
				Help:      "Total operations initiated by type (cn=Operations,cn=Monitor)",
			},
			[]string{"server", "operation"},
		),
		OperationsCompleted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "operations_completed_total",
				Help:      "Total operations completed by type (cn=Operations,cn=Monitor)",
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
				Help:      "Information about loaded overlays (cn=Overlays,cn=Monitor)",
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
				Help:      "TLS configuration information (cn=TLS,cn=Monitor)",
			},
			[]string{"server", "component", "status"},
		),

		// Backend metrics
		BackendsInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "backends_info",
				Help:      "Information about available backends (cn=Backends,cn=Monitor)",
			},
			[]string{"server", "backend", "type"},
		),

		// Listener metrics
		ListenersInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "listeners_info",
				Help:      "Information about active listeners (cn=Listeners,cn=Monitor)",
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
		ResponseTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "response_time_seconds",
				Help:      "Response time for health checks in seconds",
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
				Help:      "Whether the OpenLDAP exporter is up (1) or down (0)",
			},
			[]string{"server"},
		),
	}
}

// Describe implements the prometheus.Collector interface
func (m *OpenLDAPMetrics) Describe(ch chan<- *prometheus.Desc) {
	// Connection metrics
	m.ConnectionsCurrent.Describe(ch)
	m.ConnectionsTotal.Describe(ch)

	// Statistics metrics
	m.BytesTotal.Describe(ch)
	m.PduTotal.Describe(ch)
	m.ReferralsTotal.Describe(ch)
	m.EntriesTotal.Describe(ch)

	// Thread metrics
	m.ThreadsMax.Describe(ch)
	m.ThreadsMaxPending.Describe(ch)
	m.ThreadsBackload.Describe(ch)
	m.ThreadsActive.Describe(ch)
	m.ThreadsOpen.Describe(ch)
	m.ThreadsStarting.Describe(ch)
	m.ThreadsPending.Describe(ch)
	m.ThreadsState.Describe(ch)

	// Operation metrics
	m.OperationsInitiated.Describe(ch)
	m.OperationsCompleted.Describe(ch)

	// Waiter metrics
	m.WaitersRead.Describe(ch)
	m.WaitersWrite.Describe(ch)

	// Overlay metrics
	m.OverlaysInfo.Describe(ch)

	// Time metrics
	m.ServerTime.Describe(ch)
	m.ServerUptime.Describe(ch)

	// TLS metrics
	m.TlsInfo.Describe(ch)

	// Backend metrics
	m.BackendsInfo.Describe(ch)

	// Listener metrics
	m.ListenersInfo.Describe(ch)

	// Database metrics
	m.DatabaseEntries.Describe(ch)

	// System health metrics
	m.HealthStatus.Describe(ch)
	m.ResponseTime.Describe(ch)
	m.ScrapeErrors.Describe(ch)
	m.Up.Describe(ch)
}

// Collect implements the prometheus.Collector interface
func (m *OpenLDAPMetrics) Collect(ch chan<- prometheus.Metric) {
	// Connection metrics
	m.ConnectionsCurrent.Collect(ch)
	m.ConnectionsTotal.Collect(ch)

	// Statistics metrics
	m.BytesTotal.Collect(ch)
	m.PduTotal.Collect(ch)
	m.ReferralsTotal.Collect(ch)
	m.EntriesTotal.Collect(ch)

	// Thread metrics
	m.ThreadsMax.Collect(ch)
	m.ThreadsMaxPending.Collect(ch)
	m.ThreadsBackload.Collect(ch)
	m.ThreadsActive.Collect(ch)
	m.ThreadsOpen.Collect(ch)
	m.ThreadsStarting.Collect(ch)
	m.ThreadsPending.Collect(ch)
	m.ThreadsState.Collect(ch)

	// Operation metrics
	m.OperationsInitiated.Collect(ch)
	m.OperationsCompleted.Collect(ch)

	// Waiter metrics
	m.WaitersRead.Collect(ch)
	m.WaitersWrite.Collect(ch)

	// Overlay metrics
	m.OverlaysInfo.Collect(ch)

	// Time metrics
	m.ServerTime.Collect(ch)
	m.ServerUptime.Collect(ch)

	// TLS metrics
	m.TlsInfo.Collect(ch)

	// Backend metrics
	m.BackendsInfo.Collect(ch)

	// Listener metrics
	m.ListenersInfo.Collect(ch)

	// Database metrics
	m.DatabaseEntries.Collect(ch)

	// System health metrics
	m.HealthStatus.Collect(ch)
	m.ResponseTime.Collect(ch)
	m.ScrapeErrors.Collect(ch)
	m.Up.Collect(ch)
}
