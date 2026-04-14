package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Get retrieves a connection from the pool or creates a new one
func (p *ConnectionPool) Get(ctx context.Context) (*PooledConnection, error) {
	p.recordGetRequest()
	start := time.Now()

	if err := p.checkPoolClosed(); err != nil {
		p.recordGetFailure("pool_closed")
		return nil, err
	}

	const maxRetries = DefaultMaxRetries
	for attempt := 0; attempt < maxRetries; attempt++ {
		if conn := p.tryGetExistingConnection(ctx, start); conn != nil {
			return conn, nil
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if conn, err := p.tryCreateNewConnection(start); conn != nil || err != nil {
			return conn, err
		}

		if attempt == maxRetries-1 {
			if conn, err := p.waitForConnection(ctx, start); conn != nil || err != nil {
				return conn, err
			}
		}

		if err := p.retryDelay(ctx); err != nil {
			return nil, err
		}
	}

	p.recordGetFailure("max_attempts")
	return nil, errors.New("no available connections after max retries")
}

// Put returns a connection to the pool
func (p *ConnectionPool) Put(conn *PooledConnection) {
	if conn == nil {
		return
	}

	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolPutRequest(p.serverName, "ldap")
	}

	conn.mutex.Lock()
	conn.inUse = false
	conn.lastUsed = time.Now()
	conn.mutex.Unlock()

	if atomic.LoadInt32(&p.closed) == 1 {
		p.closeConnectionWithReason(conn, "shutdown")
		return
	}

	// Use the snapshot-based validity check so the (potentially blocking)
	// health-check ping does not run under conn.mutex — previously any
	// Put on a connection idle >30s serialized every concurrent Put on
	// the same pool entry behind a network round-trip.
	if !p.isConnectionUsable(conn) {
		if p.monitoring != nil && p.serverName != "" {
			p.monitoring.RecordPoolPutRejection(p.serverName, "ldap", "invalid_connection")
		}
		p.closeConnectionWithReason(conn, "error")
		return
	}

	select {
	case p.pool <- conn:
		atomic.AddInt64(&p.poolSize, 1)
		p.recordOperation("put")
		p.updateMetrics()
	default:
		if p.monitoring != nil && p.serverName != "" {
			p.monitoring.RecordPoolPutRejection(p.serverName, "ldap", "pool_full")
		}
		p.closeConnectionWithReason(conn, "normal")
	}
}

// checkPoolClosed checks if the pool is closed
func (p *ConnectionPool) checkPoolClosed() error {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if atomic.LoadInt32(&p.closed) == 1 {
		return ErrPoolClosed
	}
	return nil
}

// tryGetExistingConnection attempts to get a connection from the pool.
// The pool-size counter is decremented exactly once per channel receive,
// here at the call site, so validateAndPrepareConnection can be called
// from multiple code paths without worrying about duplicate accounting.
func (p *ConnectionPool) tryGetExistingConnection(ctx context.Context, start time.Time) *PooledConnection {
	select {
	case conn := <-p.pool:
		atomic.AddInt64(&p.poolSize, -1)
		if conn == nil {
			return nil
		}
		return p.validateAndPrepareConnection(conn, start)
	case <-ctx.Done():
		return nil
	default:
		return nil
	}
}

// validateAndPrepareConnection validates and prepares a connection for use.
// It never touches p.poolSize — the caller is responsible for decrementing
// it when consuming from the pool channel — so it is safe to call from any
// site that has already taken exclusive ownership of the connection.
//
// Validity is checked against a snapshot of the connection state taken
// under conn.mutex; the (potentially blocking) ping then runs without the
// lock so that Put on the scrape hot path never serializes itself behind
// a network round-trip.
func (p *ConnectionPool) validateAndPrepareConnection(conn *PooledConnection, start time.Time) *PooledConnection {
	if !p.isConnectionUsable(conn) {
		p.closeConnectionWithReason(conn, "error")
		return nil
	}

	conn.mutex.Lock()
	conn.inUse = true
	conn.lastUsed = time.Now()
	conn.mutex.Unlock()

	p.recordConnectionReused()
	p.recordWaitTime(time.Since(start))
	return conn
}

// isConnectionUsable checks if a connection is still valid without holding
// conn.mutex across network I/O. It snapshots the mutable fields, then
// pings outside the critical section when the connection has been idle
// long enough to warrant a health check.
func (p *ConnectionPool) isConnectionUsable(conn *PooledConnection) bool {
	if conn == nil || conn.conn == nil {
		return false
	}

	conn.mutex.Lock()
	inUse := conn.inUse
	lastUsed := conn.lastUsed
	createdAt := conn.createdAt
	conn.mutex.Unlock()

	if time.Since(createdAt) > p.maxIdleTime {
		return false
	}
	if !inUse && time.Since(lastUsed) > p.idleTimeout {
		return false
	}
	// Only ping connections that have been idle for more than 30 seconds
	// to avoid adding latency to every Get/Put. The ping runs without the
	// per-connection mutex held so concurrent scrapes are not serialized
	// behind network round-trips.
	if !inUse && time.Since(lastUsed) > 30*time.Second {
		if !p.pingConnection(conn) {
			logger.SafeDebug("pool", "Connection failed health check", map[string]interface{}{
				"server":   p.config.ServerName,
				"conn_age": time.Since(createdAt).Truncate(10 * time.Millisecond).String(),
			})
			return false
		}
	}
	return true
}

// tryCreateNewConnection attempts to reserve a slot in the active-connection
// budget and then dial a fresh LDAP connection. The reservation is a retry
// loop on CompareAndSwap: without the loop a lost CAS would return nil to
// the caller (signaling "pool full") even when capacity was in fact free,
// and a racing decrement could briefly admit more than maxConnections
// connections in flight.
func (p *ConnectionPool) tryCreateNewConnection(start time.Time) (*PooledConnection, error) {
	for {
		currentActive := atomic.LoadInt64(&p.activeConns)
		if int(currentActive) >= p.maxConnections {
			return nil, nil
		}
		if atomic.CompareAndSwapInt64(&p.activeConns, currentActive, currentActive+1) {
			break
		}
	}

	return p.createAndRecordConnection(start)
}

// createAndRecordConnection creates a new connection and records metrics
func (p *ConnectionPool) createAndRecordConnection(start time.Time) (*PooledConnection, error) {
	conn, err := p.createConnection()
	if err != nil {
		atomic.AddInt64(&p.activeConns, -1)
		p.recordConnectionCreationFailure(err)
		return nil, fmt.Errorf("failed to create new connection: %w", err)
	}

	conn.inUse = true
	p.recordConnectionCreationSuccess(start)
	return conn, nil
}

// waitForConnection waits for a connection to become available. As with
// tryGetExistingConnection, poolSize is decremented exactly once here at
// the channel-receive site so validateAndPrepareConnection stays
// accounting-free.
func (p *ConnectionPool) waitForConnection(ctx context.Context, start time.Time) (*PooledConnection, error) {
	select {
	case conn := <-p.pool:
		atomic.AddInt64(&p.poolSize, -1)
		if conn == nil {
			return nil, errors.New("nil connection in pool")
		}

		validConn := p.validateAndPrepareConnection(conn, start)
		if validConn != nil {
			if p.monitoring != nil && p.serverName != "" {
				p.monitoring.RecordPoolConnectionReused(p.serverName, "ldap")
				p.monitoring.RecordPoolWaitTime(p.serverName, "ldap", time.Since(start))
			}
			return validConn, nil
		}
		return nil, errors.New("no valid connections available after retries")

	case <-ctx.Done():
		p.recordContextTimeout(ctx)
		return nil, ctx.Err()
	}
}

// retryDelay implements a small delay before retry. time.NewTimer +
// explicit Stop is used instead of time.After so a canceled ctx does
// not leak a timer for 10 ms each call — under a cancel storm that
// adds up to real pressure on the scheduler's timer heap.
func (p *ConnectionPool) retryDelay(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
		p.recordContextTimeout(ctx)
		return ctx.Err()
	}
}

// recordConnectionCreationFailure records metrics for connection creation failure
func (p *ConnectionPool) recordConnectionCreationFailure(err error) {
	if p.monitoring == nil || p.serverName == "" {
		return
	}

	reason := "network_error"
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "auth") || strings.Contains(errStr, "invalid credentials") {
		reason = "auth_error"
	}

	p.monitoring.RecordPoolConnectionFailed(p.serverName, "ldap", reason)
	p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", "creation_failed")
}

// recordConnectionCreationSuccess records metrics for successful connection creation
func (p *ConnectionPool) recordConnectionCreationSuccess(start time.Time) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolConnectionCreated(p.serverName, "ldap")
		p.monitoring.RecordPoolWaitTime(p.serverName, "ldap", time.Since(start))
	}
	p.recordOperation("get")
	p.updateMetrics()
}

// recordContextTimeout records timeout metrics
func (p *ConnectionPool) recordContextTimeout(ctx context.Context) {
	if p.monitoring == nil || p.serverName == "" {
		return
	}

	if ctx.Err() == context.DeadlineExceeded {
		p.monitoring.RecordPoolWaitTimeout(p.serverName, "ldap")
		p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", "timeout")
	}
}
