package pool

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// Constants for pool configuration
const (
	// Connection management
	DefaultMaxRetries          = 3
	DefaultGetTimeout          = 30 * time.Second
	DefaultPoolTimeout         = 30 * time.Second
	DefaultMaintenanceInterval = 1 * time.Minute
	DefaultHealthCheckInterval = 30 * time.Second

	// Connection validation
	ConnectionValidationTimeout = 5 * time.Second
	MaxIdleTime                 = 10 * time.Minute

	// Pool sizing
	MinPoolSize            = 1
	MaxPoolSize            = 50
	DefaultInitialPoolSize = 5
)

// Error definitions for pool operations
var (
	ErrPoolClosed         = errors.New("connection pool is closed")
	ErrPoolTimeout        = errors.New("connection pool timeout")
	ErrConnectionInvalid  = errors.New("connection is invalid")
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
)

// PoolMonitoring defines the interface for pool monitoring
type PoolMonitoring interface {
	RecordPoolOperation(server, poolType, operation string)
	RecordPoolConnections(server, poolType, state string, count float64)
	RecordPoolUtilization(server, poolType string, utilization float64)
	RecordPoolWaitTime(server, poolType string, duration time.Duration)
	RecordPoolWaitTimeout(server, poolType string)
	RecordPoolConnectionCreated(server, poolType string)
	RecordPoolConnectionClosed(server, poolType, reason string)
	RecordPoolConnectionFailed(server, poolType, errorType string)
	RecordPoolConnectionReused(server, poolType string)
	RecordPoolGetRequest(server, poolType string)
	RecordPoolGetFailure(server, poolType, reason string)
	RecordPoolPutRequest(server, poolType string)
	RecordPoolPutRejection(server, poolType, reason string)
}

// ConnectionPool manages a pool of LDAP connections for improved performance
type ConnectionPool struct {
	config         *config.Config
	pool           chan *PooledConnection
	mutex          sync.RWMutex
	maxConnections int
	activeConns    int64 // Atomic counter for active connections
	poolSize       int64 // Atomic counter for pool size
	connTimeout    time.Duration
	idleTimeout    time.Duration
	maxIdleTime    time.Duration
	closed         int32          // Atomic flag for closed state
	shutdownChan   chan struct{}  // Channel to signal shutdown
	maintainWG     sync.WaitGroup // WaitGroup to track maintenance goroutine
	closeOnce      sync.Once      // Ensures Close() is called only once
	monitoring     PoolMonitoring // Interface for monitoring integration
	serverName     string         // Server name for metrics labeling
}

// PooledConnection wraps an LDAP connection with pool metadata
type PooledConnection struct {
	conn      *ldap.Conn
	createdAt time.Time
	lastUsed  time.Time
	inUse     bool
	mutex     sync.Mutex
}

// NewConnectionPool creates a new LDAP connection pool
func NewConnectionPool(cfg *config.Config, maxConnections int) *ConnectionPool {
	return NewConnectionPoolWithMonitoring(cfg, maxConnections, nil, "")
}

// NewConnectionPoolWithMonitoring creates a new LDAP connection pool with monitoring support
func NewConnectionPoolWithMonitoring(cfg *config.Config, maxConnections int, monitoring PoolMonitoring, serverName string) *ConnectionPool {
	pool := &ConnectionPool{
		config:         cfg,
		pool:           make(chan *PooledConnection, maxConnections),
		maxConnections: maxConnections,
		connTimeout:    30 * time.Second,
		idleTimeout:    5 * time.Minute,
		maxIdleTime:    10 * time.Minute,
		shutdownChan:   make(chan struct{}),
		monitoring:     monitoring,
		serverName:     serverName,
	}

	// Start connection maintenance goroutine with proper tracking
	pool.maintainWG.Add(1)
	go pool.maintainConnections()

	logger.SafeInfo("pool", "LDAP connection pool created", map[string]interface{}{
		"max_connections": maxConnections,
		"idle_timeout":    pool.idleTimeout.Truncate(10 * time.Millisecond).String(),
		"max_idle_time":   pool.maxIdleTime.Truncate(10 * time.Millisecond).String(),
	})

	return pool
}

// NewConnectionPoolWithMetrics creates a new LDAP connection pool (deprecated: use NewConnectionPool)
func NewConnectionPoolWithMetrics(cfg *config.Config, maxConnections int, metrics interface{}) *ConnectionPool {
	pool := NewConnectionPool(cfg, maxConnections)
	// Note: metrics field removed, legacy support maintained through monitoring interface
	return pool
}

// updateMetrics updates pool metrics if monitoring is enabled
func (p *ConnectionPool) updateMetrics() {
	if p.monitoring == nil || p.serverName == "" {
		return
	}

	// Update basic pool metrics
	activeConns := atomic.LoadInt64(&p.activeConns)
	poolSize := atomic.LoadInt64(&p.poolSize)
	idleConns := poolSize - activeConns
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

// Get retrieves a connection from the pool or creates a new one
func (p *ConnectionPool) Get(ctx context.Context) (*PooledConnection, error) {
	// Record get request
	p.recordGetRequest()

	start := time.Now()

	p.mutex.RLock()
	if atomic.LoadInt32(&p.closed) == 1 {
		p.mutex.RUnlock()
		p.recordGetFailure("pool_closed")
		return nil, ErrPoolClosed
	}
	p.mutex.RUnlock()

	const maxRetries = DefaultMaxRetries

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Try to get a connection from the pool
		select {
		case conn := <-p.pool:
			if conn == nil {
				// Nil connection in pool, skip it
				atomic.AddInt64(&p.poolSize, -1)
				continue
			}
			// Immediately lock the connection before any checks to prevent race condition
			conn.mutex.Lock()
			// Decrement pool size after acquiring the lock
			atomic.AddInt64(&p.poolSize, -1)
			if p.isConnectionValidLocked(conn) {
				conn.inUse = true
				conn.lastUsed = time.Now()
				conn.mutex.Unlock()
				// Record connection reuse
				p.recordConnectionReused()
				p.recordWaitTime(time.Since(start))
				return conn, nil
			}
			conn.mutex.Unlock()
			// Connection is invalid, close it and try again
			p.closeConnectionWithReason(conn, "error")
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			// Pool is empty, try to create a new connection
		}

		// Create new connection if we haven't reached the limit
		// Use atomic compare-and-swap to avoid race condition
		for {
			currentActive := atomic.LoadInt64(&p.activeConns)
			if int(currentActive) >= p.maxConnections {
				break
			}
			if atomic.CompareAndSwapInt64(&p.activeConns, currentActive, currentActive+1) {

				conn, err := p.createConnection()
				if err != nil {
					atomic.AddInt64(&p.activeConns, -1)
					// Record connection creation failure
					if p.monitoring != nil && p.serverName != "" {
						// Determine failure reason based on error
						reason := "network_error"
						if strings.Contains(strings.ToLower(err.Error()), "auth") ||
							strings.Contains(strings.ToLower(err.Error()), "invalid credentials") {
							reason = "auth_error"
						}
						p.monitoring.RecordPoolConnectionFailed(p.serverName, "ldap", reason)
						p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", "creation_failed")
					}
					// Don't retry on creation errors, return immediately
					return nil, fmt.Errorf("failed to create new connection: %w", err)
				}

				conn.inUse = true

				// Record successful new connection creation and wait time
				if p.monitoring != nil && p.serverName != "" {
					p.monitoring.RecordPoolConnectionCreated(p.serverName, "ldap")
					p.monitoring.RecordPoolWaitTime(p.serverName, "ldap", time.Since(start))
				}
				p.recordOperation("get")
				p.updateMetrics()

				return conn, nil
			}
		}

		// Wait for a connection to become available (only on final attempt)
		if attempt == maxRetries-1 {
			select {
			case conn := <-p.pool:
				if conn == nil {
					atomic.AddInt64(&p.poolSize, -1)
					return nil, errors.New("nil connection in pool")
				}
				// Immediately lock the connection before any checks to prevent race condition
				conn.mutex.Lock()
				// Decrement pool size after acquiring the lock
				atomic.AddInt64(&p.poolSize, -1)
				if p.isConnectionValidLocked(conn) {
					conn.inUse = true
					conn.lastUsed = time.Now()
					conn.mutex.Unlock()
					// Record connection reuse
					if p.monitoring != nil && p.serverName != "" {
						p.monitoring.RecordPoolConnectionReused(p.serverName, "ldap")
						p.monitoring.RecordPoolWaitTime(p.serverName, "ldap", time.Since(start))
					}
					return conn, nil
				}
				conn.mutex.Unlock()
				p.closeConnectionWithReason(conn, "error")
				return nil, errors.New("no valid connections available after retries")
			case <-ctx.Done():
				// Record timeout if monitoring is available
				if p.monitoring != nil && p.serverName != "" {
					if ctx.Err() == context.DeadlineExceeded {
						p.monitoring.RecordPoolWaitTimeout(p.serverName, "ldap")
						p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", "timeout")
					}
				}
				return nil, ctx.Err()
			}
		}

		// Small delay before retry to avoid tight loop
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			// Record timeout if monitoring is available
			if p.monitoring != nil && p.serverName != "" {
				if ctx.Err() == context.DeadlineExceeded {
					p.monitoring.RecordPoolWaitTimeout(p.serverName, "ldap")
					p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", "timeout")
				}
			}
			return nil, ctx.Err()
		}
	}

	// Record max attempts failure
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", "max_attempts")
	}
	return nil, errors.New("max connection attempts exceeded")
}

// Put returns a connection to the pool
func (p *ConnectionPool) Put(conn *PooledConnection) {
	if conn == nil {
		return
	}

	// Record put request
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

	// Check connection validity with lock
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
		// Successfully returned to pool
		atomic.AddInt64(&p.poolSize, 1)
		p.recordOperation("put")
		p.updateMetrics()
	default:
		// Pool is full, close the connection
		if p.monitoring != nil && p.serverName != "" {
			p.monitoring.RecordPoolPutRejection(p.serverName, "ldap", "pool_full")
		}
		p.closeConnectionWithReason(conn, "normal")
	}
}

// Close closes all connections in the pool and stops maintenance goroutine
// Close gracefully shuts down the connection pool
func (p *ConnectionPool) Close() {
	// Use sync.Once to ensure cleanup happens only once
	p.closeOnce.Do(func() {
		// Set the closed flag atomically first
		if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
			// Already closed
			return
		}

		logger.SafeInfo("pool", "Shutting down LDAP connection pool", map[string]interface{}{
			"active_connections": atomic.LoadInt64(&p.activeConns),
			"pool_size":          atomic.LoadInt64(&p.poolSize),
		})

		// Signal shutdown to maintenance goroutine
		close(p.shutdownChan)

		// Wait for maintenance goroutine to finish
		p.maintainWG.Wait()

		// Close all connections in the pool
		connectionsClosed := int64(0)
		for {
			select {
			case conn := <-p.pool:
				if conn != nil {
					atomic.AddInt64(&p.poolSize, -1)
					p.closeConnectionWithReason(conn, "shutdown")
					connectionsClosed++
				}
			default:
				logger.SafeInfo("pool", "LDAP connection pool closed", map[string]interface{}{
					"connections_closed": connectionsClosed,
					"final_active_count": atomic.LoadInt64(&p.activeConns),
				})
				return
			}
		}
	})
}

// createConnection creates a new LDAP connection
func (p *ConnectionPool) createConnection() (*PooledConnection, error) {
	conn, err := p.establishConnection()
	if err != nil {
		return nil, err
	}

	pooledConn := &PooledConnection{
		conn:      conn,
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     false,
	}

	logger.SafeDebug("pool", "New LDAP connection created", map[string]interface{}{
		"server": p.config.ServerName,
	})

	return pooledConn, nil
}

// establishConnection creates and configures an LDAP connection
func (p *ConnectionPool) establishConnection() (*ldap.Conn, error) {
	var conn *ldap.Conn
	var err error

	if p.config.TLS {
		logger.SafeDebug("pool", "Using TLS connection")
		var tlsConfig *tls.Config
		tlsConfig, err = p.buildTLSConfig()
		if err != nil {
			logger.SafeError("pool", "Failed to build TLS config", err)
			return nil, err
		}

		// Use DialURL with TLS config for TLS connections
		conn, err = ldap.DialURL(p.config.URL, ldap.DialWithTLSConfig(tlsConfig))
		if err == nil {
			logger.SafeDebug("pool", "TLS connection established", map[string]interface{}{"url": p.config.URL})
		}
	} else {
		logger.SafeDebug("pool", "Using plain LDAP connection")
		conn, err = ldap.DialURL(p.config.URL)
		if err == nil {
			logger.SafeDebug("pool", "Plain LDAP connection established", map[string]interface{}{"url": p.config.URL})
		}
	}

	if err != nil {
		logger.SafeError("pool", "Failed to dial LDAP server", err, map[string]interface{}{"url": p.config.URL})
		return nil, err
	}

	if conn == nil {
		logger.SafeError("pool", "Connection is nil after dial", nil, map[string]interface{}{"url": p.config.URL})
		return nil, fmt.Errorf("connection is nil after dial")
	}

	conn.SetTimeout(p.config.Timeout)

	if p.config.Username != "" && p.config.Password != nil && !p.config.Password.IsEmpty() {
		logger.SafeDebug("pool", "Attempting LDAP bind", map[string]interface{}{"username": p.config.Username})
		password := p.config.Password.String()
		err = conn.Bind(p.config.Username, password)
		// Password is handled securely by SecureString
		if err != nil {
			logger.SafeError("pool", "LDAP authentication failed", err, map[string]interface{}{"username": p.config.Username})
			if closeErr := conn.Close(); closeErr != nil {
				logger.SafeError("pool", "Failed to close connection after bind failure", closeErr)
			}
			return nil, fmt.Errorf("LDAP bind failed: %w", err)
		}
		logger.SafeDebug("pool", "LDAP bind successful", map[string]interface{}{"username": p.config.Username})
	}

	return conn, nil
}

// buildTLSConfig creates a TLS configuration based on the config's TLS settings
func (p *ConnectionPool) buildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: p.config.TLSSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}

	if p.config.TLSCA != "" {
		caCert, err := os.ReadFile(p.config.TLSCA)
		if err != nil {
			logger.SafeError("pool", "Failed to read CA certificate file", err, map[string]interface{}{"ca_file": p.config.TLSCA})
			return nil, err
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			logger.SafeError("pool", "Failed to parse CA certificate", nil, map[string]interface{}{"ca_file": p.config.TLSCA})
			return nil, errors.New("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	if p.config.TLSCert != "" && p.config.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(p.config.TLSCert, p.config.TLSKey)
		if err != nil {
			logger.SafeError("pool", "Failed to load client certificate", err, map[string]interface{}{
				"cert_file": p.config.TLSCert,
				"key_file":  p.config.TLSKey,
			})
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// isConnectionValid checks if a connection is still valid (acquires lock)
func (p *ConnectionPool) isConnectionValid(conn *PooledConnection) bool {
	if conn == nil || conn.conn == nil {
		return false
	}

	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	return p.isConnectionValidLocked(conn)
}

// isConnectionValidLocked checks if a connection is still valid (lock must be held by caller)
func (p *ConnectionPool) isConnectionValidLocked(conn *PooledConnection) bool {
	if conn == nil || conn.conn == nil {
		return false
	}

	// Check if connection is too old
	if time.Since(conn.createdAt) > p.maxIdleTime {
		return false
	}

	// Check if connection has been idle too long
	if !conn.inUse && time.Since(conn.lastUsed) > p.idleTimeout {
		return false
	}

	// Perform a simple health check on idle connections
	// Only ping connections that have been idle for more than 30 seconds to avoid overhead
	if !conn.inUse && time.Since(conn.lastUsed) > 30*time.Second {
		if !p.pingConnection(conn) {
			logger.SafeDebug("pool", "Connection failed health check", map[string]interface{}{
				"server":   p.config.ServerName,
				"conn_age": time.Since(conn.createdAt).Truncate(10 * time.Millisecond).String(),
			})
			return false
		}
	}

	return true
}

// pingConnection performs a simple health check on an LDAP connection
func (p *ConnectionPool) pingConnection(conn *PooledConnection) bool {
	if conn == nil || conn.conn == nil {
		return false
	}

	// Perform a simple search on the root DSE to test connectivity
	// This is a lightweight operation that most LDAP servers support
	searchRequest := ldap.NewSearchRequest(
		"", // Empty base DN for root DSE
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		1, // Size limit: we only need to know if it responds
		3, // Time limit: 3 seconds max
		false,
		"(objectClass=*)",       // Simple filter
		[]string{"objectClass"}, // Request a basic attribute
		nil,
	)

	// Perform the search - this will quickly tell us if the connection is alive
	_, err := conn.conn.Search(searchRequest)

	if err != nil {
		// Connection is not responsive
		return false
	}

	return true
}

// closeConnection safely closes a pooled connection with a reason
func (p *ConnectionPool) closeConnection(conn *PooledConnection) {
	p.closeConnectionWithReason(conn, "normal")
}

// closeConnectionWithReason safely closes a pooled connection with a specific reason
func (p *ConnectionPool) closeConnectionWithReason(conn *PooledConnection, reason string) {
	if conn == nil {
		return
	}

	if conn.conn != nil {
		if err := conn.conn.Close(); err != nil {
			logger.SafeError("pool", "Error closing LDAP connection", err)
			if p.monitoring != nil && p.serverName != "" {
				p.monitoring.RecordPoolConnectionClosed(p.serverName, "ldap", "error")
			}
		} else {
			// Record successful close with the provided reason
			if p.monitoring != nil && p.serverName != "" {
				p.monitoring.RecordPoolConnectionClosed(p.serverName, "ldap", reason)
			}
		}
		// Only decrement if we actually had a connection
		atomic.AddInt64(&p.activeConns, -1)
	}

	logger.SafeDebug("pool", "LDAP connection closed", map[string]interface{}{
		"server":             p.config.ServerName,
		"active_connections": atomic.LoadInt64(&p.activeConns),
	})
}

// maintainConnections periodically cleans up old/invalid connections
func (p *ConnectionPool) maintainConnections() {
	defer p.maintainWG.Done()
	ticker := time.NewTicker(DefaultMaintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.shutdownChan:
			// Shutdown signal received
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

			// Use context with timeout to prevent deadlock
			ctx, cancel := context.WithTimeout(context.Background(), DefaultHealthCheckInterval)
			defer cancel() // Ensure context is always cancelled

			// Drain the pool
			for {
				select {
				case <-ctx.Done():
					// Timeout reached, put back valid connections
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
			// Put valid connections back
			for _, conn := range validConns {
				select {
				case p.pool <- conn:
					atomic.AddInt64(&p.poolSize, 1)
				default:
					// Pool is full, close excess connections
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

// Stats returns connection pool statistics
func (p *ConnectionPool) Stats() map[string]interface{} {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return map[string]interface{}{
		"max_connections":    p.maxConnections,
		"active_connections": atomic.LoadInt64(&p.activeConns),
		"pool_size":          atomic.LoadInt64(&p.poolSize),
		"closed":             atomic.LoadInt32(&p.closed) == 1,
	}
}

// Helper methods for monitoring to reduce code duplication
func (p *ConnectionPool) recordGetRequest() {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetRequest(p.serverName, "ldap")
	}
}

func (p *ConnectionPool) recordGetFailure(reason string) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolGetFailure(p.serverName, "ldap", reason)
	}
}

func (p *ConnectionPool) recordConnectionReused() {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolConnectionReused(p.serverName, "ldap")
	}
}

func (p *ConnectionPool) recordWaitTime(duration time.Duration) {
	if p.monitoring != nil && p.serverName != "" {
		p.monitoring.RecordPoolWaitTime(p.serverName, "ldap", duration)
	}
}

