package pool

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// maintainConnections periodically cleans up old/invalid connections
func (p *ConnectionPool) maintainConnections() {
	defer p.maintainWG.Done()
	ticker := time.NewTicker(DefaultMaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.shutdownChan:
			return
		case <-ticker.C:
			p.mutex.RLock()
			if atomic.LoadInt32(&p.closed) == 1 {
				p.mutex.RUnlock()
				return
			}
			p.mutex.RUnlock()

			// Clean up invalid connections
			var validConns []*PooledConnection

			ctx, cancel := context.WithTimeout(context.Background(), DefaultHealthCheckInterval)
			defer cancel()

			// Drain the pool
			for {
				select {
				case <-ctx.Done():
					for _, c := range validConns {
						select {
						case p.pool <- c:
							atomic.AddInt64(&p.poolSize, 1)
						default:
							p.closeConnection(c)
						}
					}
					return
				case conn := <-p.pool:
					atomic.AddInt64(&p.poolSize, -1)
					if p.isConnectionValid(conn) {
						validConns = append(validConns, conn)
					} else {
						p.closeConnectionWithReason(conn, "timeout")
					}
				default:
					cancel()
					goto done
				}
			}

		done:
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
	}
}
