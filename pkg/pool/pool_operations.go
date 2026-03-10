package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
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

	conn.mutex.Lock()
	valid := p.isConnectionValidLocked(conn)
	conn.mutex.Unlock()

	if !valid {
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

// tryGetExistingConnection attempts to get a connection from the pool
func (p *ConnectionPool) tryGetExistingConnection(ctx context.Context, start time.Time) *PooledConnection {
	select {
	case conn := <-p.pool:
		if conn == nil {
			atomic.AddInt64(&p.poolSize, -1)
			return nil
		}
		return p.validateAndPrepareConnection(conn, start)
	case <-ctx.Done():
		return nil
	default:
		return nil
	}
}

// validateAndPrepareConnection validates and prepares a connection for use
func (p *ConnectionPool) validateAndPrepareConnection(conn *PooledConnection, start time.Time) *PooledConnection {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()

	atomic.AddInt64(&p.poolSize, -1)

	if !p.isConnectionValidLocked(conn) {
		p.closeConnectionWithReason(conn, "error")
		return nil
	}

	conn.inUse = true
	conn.lastUsed = time.Now()
	p.recordConnectionReused()
	p.recordWaitTime(time.Since(start))
	return conn
}

// tryCreateNewConnection attempts to create a new connection
func (p *ConnectionPool) tryCreateNewConnection(start time.Time) (*PooledConnection, error) {
	currentActive := atomic.LoadInt64(&p.activeConns)
	if int(currentActive) >= p.maxConnections {
		return nil, nil
	}

	if !atomic.CompareAndSwapInt64(&p.activeConns, currentActive, currentActive+1) {
		return nil, nil
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

// waitForConnection waits for a connection to become available
func (p *ConnectionPool) waitForConnection(ctx context.Context, start time.Time) (*PooledConnection, error) {
	select {
	case conn := <-p.pool:
		if conn == nil {
			atomic.AddInt64(&p.poolSize, -1)
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

// retryDelay implements a small delay before retry
func (p *ConnectionPool) retryDelay(ctx context.Context) error {
	select {
	case <-time.After(10 * time.Millisecond):
		return nil
	case <-ctx.Done():
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
