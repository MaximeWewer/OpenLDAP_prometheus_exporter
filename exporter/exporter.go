package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// OpenLDAPExporter implements the Prometheus collector interface for OpenLDAP metrics
type OpenLDAPExporter struct {
	client           *LDAPClient
	metrics          *OpenLDAPMetrics
	config           *Config
	databaseSuffixes map[string]string
}

// NewOpenLDAPExporter creates a new OpenLDAP exporter with the given configuration
func NewOpenLDAPExporter(config *Config) *OpenLDAPExporter {
	client, err := NewLDAPClient(config)
	if err != nil {
		Error("Failed to create LDAP client, will retry on first collection", err)
		client = nil
	}

	return &OpenLDAPExporter{
		client:           client,
		metrics:          NewOpenLDAPMetrics(),
		config:           config,
		databaseSuffixes: make(map[string]string),
	}
}

// Describe implements the prometheus.Collector interface by sending metric descriptions
func (e *OpenLDAPExporter) Describe(ch chan<- *prometheus.Desc) {
	e.metrics.Describe(ch)
}

// Collect implements the prometheus.Collector interface by gathering and sending metrics
func (e *OpenLDAPExporter) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()

	if err := e.ensureConnection(); err != nil {
		Error("Failed to establish LDAP connection", err, map[string]interface{}{
			"server": e.config.ServerName,
		})
		e.metrics.scrapeErrors.WithLabelValues(e.config.ServerName).Inc()
		e.metrics.Collect(ch)
		return
	}

	e.collectAllMetrics()
	e.metrics.Collect(ch)

	duration := time.Since(start)
	Info("Metrics collection completed successfully", map[string]interface{}{
		"server":   e.config.ServerName,
		"duration": duration.String(),
	})
}

// ensureConnection ensures the LDAP client connection is active, reconnecting if necessary
func (e *OpenLDAPExporter) ensureConnection() error {
	if e.client != nil {
		return nil
	}

	client, err := NewLDAPClient(e.config)
	if err != nil {
		return err
	}

	e.client = client
	return nil
}

// collectAllMetrics gathers all OpenLDAP monitoring metrics from the LDAP server
func (e *OpenLDAPExporter) collectAllMetrics() {
	serverLabel := e.config.ServerName

	e.collectConnectionsMetrics(serverLabel)
	e.collectStatisticsMetrics(serverLabel)
	e.collectOperationsMetrics(serverLabel)
	e.collectThreadsMetrics(serverLabel)
	e.collectTimeMetrics(serverLabel)
	e.collectWaitersMetrics(serverLabel)
	e.collectOverlaysMetrics(serverLabel)
	e.collectTLSMetrics(serverLabel)
	e.collectBackendsMetrics(serverLabel)
	e.collectListenersMetrics(serverLabel)
	e.collectHealthMetrics(serverLabel)
	e.collectDatabaseMetrics(serverLabel)
}

// collectConnectionsMetrics collects connection-related metrics
func (e *OpenLDAPExporter) collectConnectionsMetrics(server string) {
	if val, err := e.getMonitorCounter("cn=Current,cn=Connections,cn=Monitor"); err == nil {
		e.metrics.connectionsCurrent.WithLabelValues(server).Set(val)
	}

	if val, err := e.getMonitorCounter("cn=Total,cn=Connections,cn=Monitor"); err == nil {
		e.metrics.connectionsTotal.WithLabelValues(server).Add(val)
	}
}

// collectStatisticsMetrics collects statistics metrics
func (e *OpenLDAPExporter) collectStatisticsMetrics(server string) {
	stats := map[string]*prometheus.CounterVec{
		"cn=Bytes,cn=Statistics,cn=Monitor":     e.metrics.bytesTotal,
		"cn=PDU,cn=Statistics,cn=Monitor":       e.metrics.pduTotal,
		"cn=Entries,cn=Statistics,cn=Monitor":   e.metrics.entriesTotal,
		"cn=Referrals,cn=Statistics,cn=Monitor": e.metrics.referralsTotal,
	}

	for dn, metric := range stats {
		if val, err := e.getMonitorCounter(dn); err == nil {
			metric.WithLabelValues(server).Add(val)
		}
	}
}

// collectOperationsMetrics collects operation metrics
func (e *OpenLDAPExporter) collectOperationsMetrics(server string) {
	operations := []string{"Bind", "Unbind", "Add", "Delete", "Modrdn", "Modify", "Compare", "Search", "Abandon", "Extended"}

	for _, op := range operations {
		opDN := "cn=" + op + ",cn=Operations,cn=Monitor"
		opLower := strings.ToLower(op)

		result, err := e.client.Search(opDN, "(objectClass=*)", []string{"monitorOpInitiated", "monitorOpCompleted"})
		if err != nil || len(result.Entries) == 0 {
			continue
		}

		entry := result.Entries[0]

		// Operations initiated
		if initiated := entry.GetAttributeValue("monitorOpInitiated"); initiated != "" {
			if val, err := strconv.ParseFloat(initiated, 64); err == nil {
				e.metrics.operationsInitiated.WithLabelValues(server, opLower).Add(val)
			}
		}

		// Operations completed
		if completed := entry.GetAttributeValue("monitorOpCompleted"); completed != "" {
			if val, err := strconv.ParseFloat(completed, 64); err == nil {
				e.metrics.operationsCompleted.WithLabelValues(server, opLower).Add(val)
			}
		}
	}
}

// collectThreadsMetrics collects thread-related metrics
func (e *OpenLDAPExporter) collectThreadsMetrics(server string) {
	// All thread metrics using monitoredInfo attribute
	threadMetrics := map[string]*prometheus.GaugeVec{
		"cn=Max,cn=Threads,cn=Monitor":         e.metrics.threadsMax,
		"cn=Max Pending,cn=Threads,cn=Monitor": e.metrics.threadsMaxPending,
		"cn=Backload,cn=Threads,cn=Monitor":    e.metrics.threadsBackload,
		"cn=Active,cn=Threads,cn=Monitor":      e.metrics.threadsActive,
		"cn=Open,cn=Threads,cn=Monitor":        e.metrics.threadsOpen,
		"cn=Starting,cn=Threads,cn=Monitor":    e.metrics.threadsStarting,
		"cn=Pending,cn=Threads,cn=Monitor":     e.metrics.threadsPending,
	}

	for dn, metric := range threadMetrics {
		if val, err := e.getMonitoredInfo(dn); err == nil {
			metric.WithLabelValues(server).Set(val)
		}
	}

	// Thread state (returns text, not number)
	result, err := e.client.Search("cn=State,cn=Threads,cn=Monitor", "(objectClass=*)", []string{"monitoredInfo"})
	if err == nil && len(result.Entries) > 0 {
		if stateValue := result.Entries[0].GetAttributeValue("monitoredInfo"); stateValue != "" {
			e.metrics.threadsState.WithLabelValues(server, stateValue).Set(1)
		}
	}
}

// collectTimeMetrics collects time-related metrics
func (e *OpenLDAPExporter) collectTimeMetrics(server string) {
	// Current time - cn=Current,cn=Time,cn=Monitor
	result, err := e.client.Search("cn=Current,cn=Time,cn=Monitor", "(objectClass=*)", []string{"monitorTimestamp"})
	if err == nil && len(result.Entries) > 0 {
		if timeStr := result.Entries[0].GetAttributeValue("monitorTimestamp"); timeStr != "" {
			if currentTime, err := time.Parse("20060102150405Z", timeStr); err == nil {
				e.metrics.serverTime.WithLabelValues(server).Set(float64(currentTime.Unix()))
			}
		}
	}

	// Start time - cn=Start,cn=Time,cn=Monitor
	result, err = e.client.Search("cn=Start,cn=Time,cn=Monitor", "(objectClass=*)", []string{"monitorTimestamp"})
	if err == nil && len(result.Entries) > 0 {
		if startTimeStr := result.Entries[0].GetAttributeValue("monitorTimestamp"); startTimeStr != "" {
			if startTime, err := time.Parse("20060102150405Z", startTimeStr); err == nil {
				uptime := time.Since(startTime).Seconds()
				e.metrics.serverUptime.WithLabelValues(server).Set(uptime)
			}
		}
	}
}

// collectWaitersMetrics collects waiter metrics
func (e *OpenLDAPExporter) collectWaitersMetrics(server string) {
	// Read waiters - cn=Read,cn=Waiters,cn=Monitor
	if val, err := e.getMonitorCounter("cn=Read,cn=Waiters,cn=Monitor"); err == nil {
		e.metrics.waitersRead.WithLabelValues(server).Set(val)
	}

	// Write waiters - cn=Write,cn=Waiters,cn=Monitor
	if val, err := e.getMonitorCounter("cn=Write,cn=Waiters,cn=Monitor"); err == nil {
		e.metrics.waitersWrite.WithLabelValues(server).Set(val)
	}
}

// collectOverlaysMetrics collects overlay information
func (e *OpenLDAPExporter) collectOverlaysMetrics(server string) {
	result, err := e.client.Search("cn=Overlays,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "monitoredInfo"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		overlayName := entry.GetAttributeValue("cn")
		if overlayName == "" || overlayName == "Overlays" {
			continue
		}

		status := "loaded"
		if info := entry.GetAttributeValue("monitoredInfo"); info != "" {
			status = info
		}

		e.metrics.overlaysInfo.WithLabelValues(server, overlayName, status).Set(1)
	}
}

// collectTLSMetrics collects TLS information
func (e *OpenLDAPExporter) collectTLSMetrics(server string) {
	result, err := e.client.Search("cn=TLS,cn=Monitor", "(objectClass=*)", []string{"cn", "monitoredInfo"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		component := entry.GetAttributeValue("cn")
		if component == "" {
			continue
		}

		status := "enabled"
		if info := entry.GetAttributeValue("monitoredInfo"); info != "" {
			status = info
		}

		e.metrics.tlsInfo.WithLabelValues(server, component, status).Set(1)
	}
}

// collectBackendsMetrics collects backend information
func (e *OpenLDAPExporter) collectBackendsMetrics(server string) {
	result, err := e.client.Search("cn=Backends,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "monitoredInfo"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		backendName := entry.GetAttributeValue("cn")
		if backendName == "" || backendName == "Backends" {
			continue
		}

		backendType := entry.GetAttributeValue("monitoredInfo")
		if backendType == "" {
			backendType = "unknown"
		}

		e.metrics.backendsInfo.WithLabelValues(server, backendName, backendType).Set(1)
	}
}

// collectListenersMetrics collects listener information
func (e *OpenLDAPExporter) collectListenersMetrics(server string) {
	result, err := e.client.Search("cn=Listeners,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "monitorConnectionLocalAddress"})
	if err != nil {
		return
	}

	for _, entry := range result.Entries {
		listenerName := entry.GetAttributeValue("cn")
		address := entry.GetAttributeValue("monitorConnectionLocalAddress")

		if listenerName != "" && listenerName != "Listeners" {
			if address == "" {
				address = "unknown"
			}
			e.metrics.listenersInfo.WithLabelValues(server, listenerName, address).Set(1)
		}
	}
}

// collectHealthMetrics collects health check metrics
func (e *OpenLDAPExporter) collectHealthMetrics(server string) {
	start := time.Now()

	// Health check - try to read the monitor root
	_, err := e.client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	duration := time.Since(start).Seconds()

	e.metrics.responseTime.WithLabelValues(server).Set(duration)

	if err != nil {
		e.metrics.healthStatus.WithLabelValues(server).Set(0)
		e.metrics.scrapeErrors.WithLabelValues(server).Inc()
	} else {
		e.metrics.healthStatus.WithLabelValues(server).Set(1)
	}
}

// collectDatabaseMetrics collects database-specific metrics and suffixes
func (e *OpenLDAPExporter) collectDatabaseMetrics(server string) {
	if len(e.databaseSuffixes) == 0 {
		suffixes, err := e.client.GetDatabaseSuffixes()
		if err != nil {
			Error("Failed to get database suffixes dynamically", err)
			return
		}
		e.databaseSuffixes = suffixes
		Debug("Retrieved database suffixes", map[string]interface{}{
			"suffixes": suffixes,
		})
	}

	result, err := e.client.Search("cn=Databases,cn=Monitor", "(objectClass=monitoredObject)", []string{"cn", "namingContexts", "monitoredInfo"})
	if err != nil {
		Error("Failed to get databases info", err)
		return
	}

	for _, entry := range result.Entries {
		dbName := entry.GetAttributeValue("cn")
		dbType := entry.GetAttributeValue("monitoredInfo")
		actualBaseDN := entry.GetAttributeValue("namingContexts")

		if actualBaseDN == "" || dbType != "mdb" {
			continue
		}

		if dynamicBaseDN, exists := e.databaseSuffixes[dbName]; exists && dynamicBaseDN != actualBaseDN {
			Warn("Database mapping differs from actual", map[string]interface{}{
				"database":        dbName,
				"dynamic_mapping": dynamicBaseDN,
				"actual_mapping":  actualBaseDN,
			})
		}

		Debug("Found database", map[string]interface{}{
			"name":   dbName,
			"suffix": actualBaseDN,
			"type":   dbType,
		})

		e.metrics.databaseEntries.WithLabelValues(server, actualBaseDN).Set(1)
	}
}

// getMonitorCounter retrieves a numeric counter value from the specified LDAP DN
func (e *OpenLDAPExporter) getMonitorCounter(dn string) (float64, error) {
	result, err := e.client.Search(dn, "(objectClass=*)", []string{"monitorCounter"})
	if err != nil {
		return 0, err
	}

	if len(result.Entries) == 0 {
		Debug("No entries found for DN", map[string]interface{}{"dn": dn})
		return 0, nil
	}

	counterStr := result.Entries[0].GetAttributeValue("monitorCounter")
	if counterStr == "" {
		Debug("monitorCounter attribute not found", map[string]interface{}{"dn": dn})
		return 0, nil
	}

	return strconv.ParseFloat(counterStr, 64)
}

// getMonitoredInfo retrieves numeric information from the monitoredInfo attribute
func (e *OpenLDAPExporter) getMonitoredInfo(dn string) (float64, error) {
	result, err := e.client.Search(dn, "(objectClass=*)", []string{"monitoredInfo"})
	if err != nil {
		return 0, err
	}

	if len(result.Entries) == 0 {
		Debug("No entries found for DN", map[string]interface{}{"dn": dn})
		return 0, nil
	}

	infoStr := result.Entries[0].GetAttributeValue("monitoredInfo")
	if infoStr == "" {
		Debug("monitoredInfo attribute not found", map[string]interface{}{"dn": dn})
		return 0, nil
	}

	return strconv.ParseFloat(infoStr, 64)
}
