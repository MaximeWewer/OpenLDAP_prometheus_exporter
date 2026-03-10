package pool

import (
	"errors"
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
	activeConns    int64          // Atomic counter for active connections
	poolSize       int64          // Atomic counter for pool size
	connTimeout    time.Duration
	idleTimeout    time.Duration
	maxIdleTime    time.Duration
	closed         int32          // Atomic flag for closed state
	shutdownChan   chan struct{}   // Channel to signal shutdown
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

// Close gracefully shuts down the connection pool
func (p *ConnectionPool) Close() {
	p.closeOnce.Do(func() {
		if !atomic.CompareAndSwapInt32(&p.closed, 0, 1) {
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
