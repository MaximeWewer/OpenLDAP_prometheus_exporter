package pool

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// maintainConnections periodically cleans up old/invalid connections.
// Each tick is executed inside runMaintenanceTick so that the per-tick
// context/timer cleanup runs on every iteration rather than being piled
// onto a single deferred stack for the goroutine's lifetime (the original
// "defer cancel() inside a for-loop" anti-pattern leaked one timer per
// tick and would eventually kill the goroutine on the first slow tick).
func (p *ConnectionPool) maintainConnections() {
	defer p.maintainWG.Done()
	ticker := time.NewTicker(DefaultMaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.shutdownChan:
			return
		case <-ticker.C:
			if atomic.LoadInt32(&p.closed) == 1 {
				return
			}
			p.runMaintenanceTick()
		}
	}
}

// runMaintenanceTick drains every currently-pooled connection, drops the
// invalid ones, and puts the valid ones back. It never returns from the
// outer maintenance loop on ctx timeout — it simply reinserts what it has
// collected so far and falls through, so a single slow tick cannot kill
// the goroutine forever.
func (p *ConnectionPool) runMaintenanceTick() {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultHealthCheckInterval)
	defer cancel()

	var validConns []*PooledConnection

drain:
	for {
		select {
		case <-ctx.Done():
			break drain
		case conn := <-p.pool:
			atomic.AddInt64(&p.poolSize, -1)
			if p.isConnectionValid(conn) {
				validConns = append(validConns, conn)
			} else {
				p.closeConnectionWithReason(conn, "timeout")
			}
		default:
			break drain
		}
	}

	for _, conn := range validConns {
		select {
		case p.pool <- conn:
			atomic.AddInt64(&p.poolSize, 1)
		default:
			p.closeConnectionWithReason(conn, "normal")
		}
	}

	logger.SafeDebug("pool", "Connection pool maintenance completed", map[string]interface{}{
		"active_connections": atomic.LoadInt64(&p.activeConns),
		"pool_size":          atomic.LoadInt64(&p.poolSize),
	})
}
