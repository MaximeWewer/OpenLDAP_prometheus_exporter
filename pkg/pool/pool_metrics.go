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

	p.monitoring.RecordPoolConnections(p.serverName, p.poolType, "active", float64(activeConns))
	p.monitoring.RecordPoolConnections(p.serverName, p.poolType, "idle", float64(idleConns))
	p.monitoring.RecordPoolConnections(p.serverName, p.poolType, "total", float64(poolSize))
	p.monitoring.RecordPoolUtilization(p.serverName, p.poolType, utilization)
}

// recordOperation records a pool operation if monitoring is enabled
func (p *ConnectionPool) recordOperation(operation string) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolOperation(p.serverName, p.poolType, operation)
	}
}

// recordGetRequest records a get request metric
func (p *ConnectionPool) recordGetRequest() {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetRequest(p.serverName, p.poolType)
	}
}

// recordGetFailure records a get failure metric
func (p *ConnectionPool) recordGetFailure(reason string) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetFailure(p.serverName, p.poolType, reason)
	}
}

// recordConnectionReused records a connection reuse metric
func (p *ConnectionPool) recordConnectionReused() {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolConnectionReused(p.serverName, p.poolType)
	}
}

// recordWaitTime records connection wait time
func (p *ConnectionPool) recordWaitTime(duration time.Duration) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolWaitTime(p.serverName, p.poolType, duration)
	}
}
