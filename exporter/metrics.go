package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

// OpenLDAPMetrics holds all Prometheus metrics for OpenLDAP monitoring
type OpenLDAPMetrics struct {
	connectionsCurrent  *prometheus.GaugeVec
	connectionsTotal    *prometheus.CounterVec
	bytesTotal          *prometheus.CounterVec
	pduTotal            *prometheus.CounterVec
	referralsTotal      *prometheus.CounterVec
	entriesTotal        *prometheus.CounterVec
	threadsMax          *prometheus.GaugeVec
	threadsMaxPending   *prometheus.GaugeVec
	threadsBackload     *prometheus.GaugeVec
	threadsActive       *prometheus.GaugeVec
	threadsOpen         *prometheus.GaugeVec
	threadsStarting     *prometheus.GaugeVec
	threadsPending      *prometheus.GaugeVec
	threadsState        *prometheus.GaugeVec
	operationsInitiated *prometheus.CounterVec
	operationsCompleted *prometheus.CounterVec
	waitersRead         *prometheus.GaugeVec
	waitersWrite        *prometheus.GaugeVec
	overlaysInfo        *prometheus.GaugeVec
	serverTime          *prometheus.GaugeVec
	serverUptime        *prometheus.GaugeVec
	tlsInfo             *prometheus.GaugeVec
	backendsInfo        *prometheus.GaugeVec
	listenersInfo       *prometheus.GaugeVec
	databaseEntries     *prometheus.GaugeVec
	healthStatus        *prometheus.GaugeVec
	responseTime        *prometheus.GaugeVec
	scrapeErrors        *prometheus.CounterVec
}

// NewOpenLDAPMetrics creates and initializes all OpenLDAP Prometheus metrics
func NewOpenLDAPMetrics() *OpenLDAPMetrics {
	return &OpenLDAPMetrics{
		connectionsCurrent: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "connections_current",
				Help:      "Current number of connections (cn=Current,cn=Connections,cn=Monitor)",
			},
			[]string{"server"},
		),
		connectionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "connections_total",
				Help:      "Total number of connections (cn=Total,cn=Connections,cn=Monitor)",
			},
			[]string{"server"},
		),

		bytesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "bytes_total",
				Help:      "Total bytes sent (cn=Bytes,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),
		pduTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "pdu_total",
				Help:      "Total PDUs processed (cn=PDU,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),
		referralsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "referrals_total",
				Help:      "Total referrals sent (cn=Referrals,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),
		entriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "entries_total",
				Help:      "Total entries sent (cn=Entries,cn=Statistics,cn=Monitor)",
			},
			[]string{"server"},
		),

		// Thread metrics
		threadsMax: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_max",
				Help:      "Maximum number of threads (cn=Max,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsMaxPending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_max_pending",
				Help:      "Maximum number of pending threads (cn=Max Pending,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsBackload: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_backload",
				Help:      "Current thread backload (cn=Backload,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_active",
				Help:      "Number of active threads (cn=Active,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsOpen: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_open",
				Help:      "Number of open threads (cn=Open,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsStarting: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_starting",
				Help:      "Number of starting threads (cn=Starting,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsPending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_pending",
				Help:      "Number of pending threads (cn=Pending,cn=Threads,cn=Monitor)",
			},
			[]string{"server"},
		),
		threadsState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "threads_state",
				Help:      "Thread pool state (cn=State,cn=Threads,cn=Monitor)",
			},
			[]string{"server", "state"},
		),

		// Operation metrics
		operationsInitiated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "operations_initiated_total",
				Help:      "Total operations initiated by type (cn=Operations,cn=Monitor)",
			},
			[]string{"server", "operation"},
		),
		operationsCompleted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "operations_completed_total",
				Help:      "Total operations completed by type (cn=Operations,cn=Monitor)",
			},
			[]string{"server", "operation"},
		),

		// Waiter metrics
		waitersRead: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "waiters_read",
				Help:      "Number of read waiters (cn=Read,cn=Waiters,cn=Monitor)",
			},
			[]string{"server"},
		),
		waitersWrite: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "waiters_write",
				Help:      "Number of write waiters (cn=Write,cn=Waiters,cn=Monitor)",
			},
			[]string{"server"},
		),

		// Overlay metrics
		overlaysInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "overlays_info",
				Help:      "Information about loaded overlays (cn=Overlays,cn=Monitor)",
			},
			[]string{"server", "overlay", "status"},
		),

		// Time metrics
		serverTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "server_time",
				Help:      "Current server time (cn=Current,cn=Time,cn=Monitor)",
			},
			[]string{"server"},
		),
		serverUptime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "server_uptime_seconds",
				Help:      "Server uptime in seconds (cn=Start,cn=Time,cn=Monitor)",
			},
			[]string{"server"},
		),

		// TLS metrics
		tlsInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "tls_info",
				Help:      "TLS configuration information (cn=TLS,cn=Monitor)",
			},
			[]string{"server", "component", "status"},
		),

		// Backend metrics
		backendsInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "backends_info",
				Help:      "Information about available backends (cn=Backends,cn=Monitor)",
			},
			[]string{"server", "backend", "type"},
		),

		// Listener metrics
		listenersInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "listeners_info",
				Help:      "Information about active listeners (cn=Listeners,cn=Monitor)",
			},
			[]string{"server", "listener", "address"},
		),

		// Database metrics
		databaseEntries: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "database_entries",
				Help:      "Number of entries per database/base DN",
			},
			[]string{"server", "base_dn"},
		),

		// System health metrics
		healthStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "health_status",
				Help:      "Health status of the OpenLDAP server (1=healthy, 0=unhealthy)",
			},
			[]string{"server"},
		),
		responseTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "openldap",
				Name:      "response_time_seconds",
				Help:      "Response time for health checks in seconds",
			},
			[]string{"server"},
		),
		scrapeErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "openldap",
				Name:      "scrape_errors_total",
				Help:      "Total number of scrape errors",
			},
			[]string{"server"},
		),
	}
}

// Describe implements the prometheus.Collector interface
func (m *OpenLDAPMetrics) Describe(ch chan<- *prometheus.Desc) {
	// Connection metrics
	m.connectionsCurrent.Describe(ch)
	m.connectionsTotal.Describe(ch)

	// Statistics metrics
	m.bytesTotal.Describe(ch)
	m.pduTotal.Describe(ch)
	m.referralsTotal.Describe(ch)
	m.entriesTotal.Describe(ch)

	// Thread metrics
	m.threadsMax.Describe(ch)
	m.threadsMaxPending.Describe(ch)
	m.threadsBackload.Describe(ch)
	m.threadsActive.Describe(ch)
	m.threadsOpen.Describe(ch)
	m.threadsStarting.Describe(ch)
	m.threadsPending.Describe(ch)
	m.threadsState.Describe(ch)

	// Operation metrics
	m.operationsInitiated.Describe(ch)
	m.operationsCompleted.Describe(ch)

	// Waiter metrics
	m.waitersRead.Describe(ch)
	m.waitersWrite.Describe(ch)

	// Overlay metrics
	m.overlaysInfo.Describe(ch)

	// Time metrics
	m.serverTime.Describe(ch)
	m.serverUptime.Describe(ch)

	// TLS metrics
	m.tlsInfo.Describe(ch)

	// Backend metrics
	m.backendsInfo.Describe(ch)

	// Listener metrics
	m.listenersInfo.Describe(ch)

	// Database metrics
	m.databaseEntries.Describe(ch)

	// System health metrics
	m.healthStatus.Describe(ch)
	m.responseTime.Describe(ch)
	m.scrapeErrors.Describe(ch)
}

// Collect implements the prometheus.Collector interface
func (m *OpenLDAPMetrics) Collect(ch chan<- prometheus.Metric) {
	// Connection metrics
	m.connectionsCurrent.Collect(ch)
	m.connectionsTotal.Collect(ch)

	// Statistics metrics
	m.bytesTotal.Collect(ch)
	m.pduTotal.Collect(ch)
	m.referralsTotal.Collect(ch)
	m.entriesTotal.Collect(ch)

	// Thread metrics
	m.threadsMax.Collect(ch)
	m.threadsMaxPending.Collect(ch)
	m.threadsBackload.Collect(ch)
	m.threadsActive.Collect(ch)
	m.threadsOpen.Collect(ch)
	m.threadsStarting.Collect(ch)
	m.threadsPending.Collect(ch)
	m.threadsState.Collect(ch)

	// Operation metrics
	m.operationsInitiated.Collect(ch)
	m.operationsCompleted.Collect(ch)

	// Waiter metrics
	m.waitersRead.Collect(ch)
	m.waitersWrite.Collect(ch)

	// Overlay metrics
	m.overlaysInfo.Collect(ch)

	// Time metrics
	m.serverTime.Collect(ch)
	m.serverUptime.Collect(ch)

	// TLS metrics
	m.tlsInfo.Collect(ch)

	// Backend metrics
	m.backendsInfo.Collect(ch)

	// Listener metrics
	m.listenersInfo.Collect(ch)

	// Database metrics
	m.databaseEntries.Collect(ch)

	// System health metrics
	m.healthStatus.Collect(ch)
	m.responseTime.Collect(ch)
	m.scrapeErrors.Collect(ch)
}