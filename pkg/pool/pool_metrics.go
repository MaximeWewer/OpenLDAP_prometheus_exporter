package pool

import (
	"sync/atomic"
	"time"
)

// updateMetrics updates pool metrics if monitoring is enabled
func (p *ConnectionPool) updateMetrics() {
	if p.monitoring == nil || p.serverName == "" {
		return
	}

	// Read both values under the pool mutex to get a consistent snapshot
	p.mutex.RLock()
	activeConns := atomic.LoadInt64(&p.activeConns)
	poolSize := atomic.LoadInt64(&p.poolSize)
	p.mutex.RUnlock()

	idleConns := poolSize - activeConns
	if idleConns < 0 {
		idleConns = 0
	}
	utilization := float64(activeConns) / float64(p.maxConnections)

	p.monitoring.RecordPoolConnections(p.serverName, "ldap", "active", float64(activeConns))
	p.monitoring.RecordPoolConnections(p.serverName, "ldap", "idle", float64(idleConns))
	p.monitoring.RecordPoolConnections(p.serverName, "ldap", "total", float64(poolSize))
	p.monitoring.RecordPoolUtilization(p.serverName, "ldap", utilization)
}

// recordOperation records a pool operation if monitoring is enabled
func (p *ConnectionPool) recordOperation(operation string) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolOperation(p.serverName, "ldap", operation)
	}
}

// recordGetRequest records a get request metric
func (p *ConnectionPool) recordGetRequest() {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetRequest(p.serverName, "ldap")
	}
}

// recordGetFailure records a get failure metric
func (p *ConnectionPool) recordGetFailure(reason string) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", reason)
	}
}

// recordConnectionReused records a connection reuse metric
func (p *ConnectionPool) recordConnectionReused() {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolConnectionReused(p.serverName, "ldap")
	}
}

// recordWaitTime records connection wait time
func (p *ConnectionPool) recordWaitTime(duration time.Duration) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolWaitTime(p.serverName, "ldap", duration)
	}
}
