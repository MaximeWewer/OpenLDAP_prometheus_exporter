package pool

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// ConnectionPool manages a pool of LDAP connections for improved performance
type ConnectionPool struct {
	config         *config.Config
	pool           chan *PooledConnection
	mutex          sync.RWMutex
	maxConnections int
	activeConns    int
	poolSize       int64 // Atomic counter for pool size
	connTimeout    time.Duration
	idleTimeout    time.Duration
	maxIdleTime    time.Duration
	closed         bool
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
	pool := &ConnectionPool{
		config:         cfg,
		pool:           make(chan *PooledConnection, maxConnections),
		maxConnections: maxConnections,
		connTimeout:    30 * time.Second,
		idleTimeout:    5 * time.Minute,
		maxIdleTime:    10 * time.Minute,
	}

	// Start connection maintenance goroutine
	go pool.maintainConnections()

	logger.SafeInfo("pool", "LDAP connection pool created", map[string]interface{}{
		"max_connections": maxConnections,
		"idle_timeout":    pool.idleTimeout.String(),
		"max_idle_time":   pool.maxIdleTime.String(),
	})

	return pool
}

// Get retrieves a connection from the pool or creates a new one
func (p *ConnectionPool) Get(ctx context.Context) (*PooledConnection, error) {
	p.mutex.RLock()
	if p.closed {
		p.mutex.RUnlock()
		return nil, errors.New("connection pool is closed")
	}
	p.mutex.RUnlock()

	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Try to get a connection from the pool
		select {
		case conn := <-p.pool:
			atomic.AddInt64(&p.poolSize, -1)
			// Lock before validation to prevent race condition
			conn.mutex.Lock()
			if p.isConnectionValidLocked(conn) {
				conn.inUse = true
				conn.lastUsed = time.Now()
				conn.mutex.Unlock()
				return conn, nil
			}
			conn.mutex.Unlock()
			// Connection is invalid, close it and try again
			p.closeConnection(conn)
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			// Pool is empty, try to create a new connection
		}

		// Create new connection if we haven't reached the limit
		p.mutex.Lock()
		if p.activeConns < p.maxConnections {
			p.activeConns++
			p.mutex.Unlock()

			conn, err := p.createConnection()
			if err != nil {
				p.mutex.Lock()
				p.activeConns--
				p.mutex.Unlock()
				// Don't retry on creation errors, return immediately
				return nil, fmt.Errorf("failed to create new connection: %w", err)
			}

			conn.inUse = true
			return conn, nil
		}
		p.mutex.Unlock()

		// Wait for a connection to become available (only on final attempt)
		if attempt == maxRetries-1 {
			select {
			case conn := <-p.pool:
				atomic.AddInt64(&p.poolSize, -1)
				// Lock before validation to prevent race condition
				conn.mutex.Lock()
				if p.isConnectionValidLocked(conn) {
					conn.inUse = true
					conn.lastUsed = time.Now()
					conn.mutex.Unlock()
					return conn, nil
				}
				conn.mutex.Unlock()
				p.closeConnection(conn)
				return nil, errors.New("no valid connections available after retries")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// Small delay before retry to avoid tight loop
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, errors.New("max connection attempts exceeded")
}

// Put returns a connection to the pool
func (p *ConnectionPool) Put(conn *PooledConnection) {
	if conn == nil {
		return
	}

	conn.mutex.Lock()
	conn.inUse = false
	conn.lastUsed = time.Now()
	conn.mutex.Unlock()

	p.mutex.RLock()
	closed := p.closed
	p.mutex.RUnlock()

	if closed {
		p.closeConnection(conn)
		return
	}

	// Check connection validity with lock
	conn.mutex.Lock()
	valid := p.isConnectionValidLocked(conn)
	conn.mutex.Unlock()

	if !valid {
		p.closeConnection(conn)
		return
	}

	select {
	case p.pool <- conn:
		// Successfully returned to pool
		atomic.AddInt64(&p.poolSize, 1)
	default:
		// Pool is full, close the connection
		p.closeConnection(conn)
	}
}

// Close closes all connections in the pool
func (p *ConnectionPool) Close() {
	p.mutex.Lock()
	p.closed = true
	p.mutex.Unlock()

	// Close all connections in the pool
	for {
		select {
		case conn := <-p.pool:
			atomic.AddInt64(&p.poolSize, -1)
			p.closeConnection(conn)
		default:
			logger.SafeInfo("pool", "LDAP connection pool closed", map[string]interface{}{
				"connections_closed": p.activeConns,
			})
			return
		}
	}
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
		// Secure wipe password from local variable
		secureWipeString(password)
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
				"conn_age": time.Since(conn.createdAt).String(),
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

// closeConnection safely closes a pooled connection
func (p *ConnectionPool) closeConnection(conn *PooledConnection) {
	if conn == nil {
		return
	}

	if conn.conn != nil {
		if err := conn.conn.Close(); err != nil {
			logger.SafeError("pool", "Error closing LDAP connection", err)
		}
	}

	p.mutex.Lock()
	p.activeConns--
	p.mutex.Unlock()

	logger.SafeDebug("pool", "LDAP connection closed", map[string]interface{}{
		"server":             p.config.ServerName,
		"active_connections": p.activeConns,
	})
}

// maintainConnections periodically cleans up old/invalid connections
func (p *ConnectionPool) maintainConnections() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		p.mutex.RLock()
		if p.closed {
			p.mutex.RUnlock()
			return
		}
		p.mutex.RUnlock()

		// Clean up invalid connections
		var validConns []*PooledConnection

		// Use context with timeout to prevent deadlock
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

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
				cancel()
				return
			case conn := <-p.pool:
				atomic.AddInt64(&p.poolSize, -1)
				if p.isConnectionValid(conn) {
					validConns = append(validConns, conn)
				} else {
					p.closeConnection(conn)
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
				p.closeConnection(conn)
			}
		}

		logger.SafeDebug("pool", "Connection pool maintenance completed", map[string]interface{}{
			"active_connections": p.activeConns,
			"pool_size":          len(p.pool),
		})
	}
}

// Stats returns connection pool statistics
func (p *ConnectionPool) Stats() map[string]interface{} {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return map[string]interface{}{
		"max_connections":    p.maxConnections,
		"active_connections": p.activeConns,
		"pool_size":          atomic.LoadInt64(&p.poolSize),
		"closed":             p.closed,
	}
}

//go:noinline
//go:nosplit
func secureWipeString(s string) {
	// Get the string header to access underlying data
	if len(s) == 0 {
		return
	}

	// Convert string to []byte without copying (unsafe but necessary for secure wipe)
	data := unsafe.Slice((*byte)(unsafe.Pointer(unsafe.StringData(s))), len(s))

	// Multiple pass overwrite to defeat memory forensics
	// Pass 1: Zero bytes
	for i := range data {
		data[i] = 0x00
	}

	// Pass 2: 0xFF bytes
	for i := range data {
		data[i] = 0xFF
	}

	// Pass 3: Random pattern
	for i := range data {
		data[i] = byte(i) ^ 0xAA
	}

	// Final pass: Zero again
	for i := range data {
		data[i] = 0x00
	}

	// Force memory barrier to prevent compiler optimization
	runtime.KeepAlive(data)
}
