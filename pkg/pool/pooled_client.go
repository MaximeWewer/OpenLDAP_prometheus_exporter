package pool

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/security"
)

// PooledLDAPClient wraps the connection pool to provide a simple interface with circuit breaker protection
type PooledLDAPClient struct {
	pool           PoolInterface // Interface to support both regular and adaptive pools
	config         *config.Config
	circuitBreaker *circuitbreaker.CircuitBreaker
	isAdaptive     bool
}

// PoolInterface defines the interface that both ConnectionPool and AdaptiveConnectionPool implement
type PoolInterface interface {
	Get(ctx context.Context) (*PooledConnection, error)
	Put(conn *PooledConnection)
	Close()
	Stats() map[string]interface{}
}

// NewPooledLDAPClient creates a new pooled LDAP client with circuit breaker protection
func NewPooledLDAPClient(cfg *config.Config) *PooledLDAPClient {
	return NewPooledLDAPClientWithOptions(cfg, false)
}

// NewPooledLDAPClientWithOptions creates a new pooled LDAP client with options
func NewPooledLDAPClientWithOptions(cfg *config.Config, _ bool) *PooledLDAPClient {
	// For now, always use regular pool (adaptive pool needs more work)
	pool := NewConnectionPool(cfg, 5)
	useAdaptivePool := false

	// Create circuit breaker with LDAP-specific configuration
	cbConfig := circuitbreaker.CircuitBreakerConfig{
		MaxFailures:      3,                // Open after 3 consecutive failures
		Timeout:          60 * time.Second, // Wait 1 minute before trying again
		ResetTimeout:     15 * time.Second, // Test for 15 seconds in half-open
		SuccessThreshold: 2,                // Need 2 successes to close
	}

	circuitBreakerInstance := circuitbreaker.NewCircuitBreaker(cbConfig)

	// Set up circuit breaker state change logging
	circuitBreakerInstance.SetStateChangeCallback(func(from, to circuitbreaker.State) {
		logger.SafeWarn("pooled_client", "LDAP circuit breaker state changed", map[string]interface{}{
			"server": cfg.ServerName,
			"from":   from.String(),
			"to":     to.String(),
		})
	})

	logger.SafeInfo("pooled_client", "LDAP client created", map[string]interface{}{
		"server":        cfg.ServerName,
		"adaptive_pool": useAdaptivePool,
	})

	return &PooledLDAPClient{
		pool:           pool,
		config:         cfg,
		circuitBreaker: circuitBreakerInstance,
		isAdaptive:     useAdaptivePool,
	}
}

// Search performs an LDAP search using a connection from the pool
func (c *PooledLDAPClient) Search(baseDN, filter string, attributes []string) (*ldap.SearchResult, error) {
	if baseDN == "" || filter == "" {
		logger.SafeError("pooled_client", "Invalid search parameters", nil, map[string]interface{}{
			"baseDN": baseDN,
			"filter": filter,
		})
		return nil, errors.New("invalid search parameters: baseDN and filter cannot be empty")
	}

	// Security validation: validate DN is within monitor tree
	if err := security.ValidateMonitorDN(baseDN); err != nil {
		logger.SafeError("pooled_client", "DN validation failed", err, map[string]interface{}{
			"baseDN": baseDN,
		})
		return nil, err
	}

	// Security validation: validate LDAP filter
	if err := security.ValidateLDAPFilter(filter); err != nil {
		logger.SafeError("pooled_client", "Filter validation failed", err, map[string]interface{}{
			"filter": filter,
		})
		return nil, err
	}

	// Security validation: validate attributes
	for _, attr := range attributes {
		if err := security.ValidateLDAPAttribute(attr); err != nil {
			logger.SafeError("pooled_client", "Attribute validation failed", err, map[string]interface{}{
				"attribute": attr,
			})
			return nil, err
		}
	}

	// Use circuit breaker to protect against cascading failures
	var result *ldap.SearchResult
	err := c.circuitBreaker.Call(func() error {
		// Get connection from pool with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		conn, err := c.pool.Get(ctx)
		if err != nil {
			logger.SafeError("pooled_client", "Failed to get connection from pool", err, map[string]interface{}{
				"server": c.config.ServerName,
			})
			return err
		}

		// Ensure connection is returned to pool
		defer c.pool.Put(conn)

		searchRequest := ldap.NewSearchRequest(
			baseDN,
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			0,
			0,
			false,
			filter,
			attributes,
			nil,
		)

		searchResult, err := conn.conn.Search(searchRequest)
		if err != nil {
			logger.SafeError("pooled_client", "LDAP search failed", err, map[string]interface{}{
				"baseDN": baseDN,
				"filter": filter,
			})

			// If connection error, invalidate the connection
			if isNetworkError(err) {
				// Don't return this connection to the pool
				c.invalidateConnection(conn)
			}

			return err
		}

		// Store result for return
		result = searchResult
		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.SafeDebug("pooled_client", "LDAP search completed", map[string]interface{}{
		"baseDN":                baseDN,
		"filter":                filter,
		"entries_found":         len(result.Entries),
		"circuit_breaker_state": c.circuitBreaker.GetState().String(),
	})

	return result, nil
}

// Close closes the connection pool
func (c *PooledLDAPClient) Close() {
	if c.pool != nil {
		c.pool.Close()
	}
}

// invalidateConnection marks a connection as invalid so it won't be returned to the pool
func (c *PooledLDAPClient) invalidateConnection(conn *PooledConnection) {
	if conn != nil {
		// Close the underlying connection directly instead of returning to pool
		if conn.conn != nil {
			if err := conn.conn.Close(); err != nil {
				logger.SafeError("pooled_client", "Error closing LDAP connection", err)
			}
		}

		// For interface abstraction, we can't directly access pool internals
		// The connection won't be returned to the pool anyway since we're not calling Put()
		logger.SafeDebug("pooled_client", "LDAP connection invalidated", map[string]interface{}{
			"server": c.config.ServerName,
		})
	}
}

// Stats returns connection pool and circuit breaker statistics
func (c *PooledLDAPClient) Stats() map[string]interface{} {
	stats := make(map[string]interface{})

	if c.pool != nil {
		poolStats := c.pool.Stats()
		for k, v := range poolStats {
			stats["pool_"+k] = v
		}

		// Adaptive pool support disabled for now
		// if c.isAdaptive && c.adaptivePool != nil {
		//     adaptiveStats := c.adaptivePool.GetAdaptiveStats()
		//     for k, v := range adaptiveStats {
		//         if k != "max_connections" && k != "active_connections" && k != "pool_size" && k != "closed" {
		//             stats["adaptive_"+k] = v
		//         }
		//     }
		// }
	} else {
		stats["pool_error"] = "no pool available"
	}

	if c.circuitBreaker != nil {
		cbStats := c.circuitBreaker.GetStats()
		for k, v := range cbStats {
			stats["circuit_breaker_"+k] = v
		}
	}

	stats["is_adaptive"] = c.isAdaptive

	return stats
}

// IsHealthy returns true if the client is healthy and can serve requests
func (c *PooledLDAPClient) IsHealthy() bool {
	return c.circuitBreaker != nil && c.circuitBreaker.IsHealthy()
}

// isNetworkError checks if an error is a network-related error that should invalidate the connection
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	networkErrors := []string{
		"connection reset",
		"connection closed",
		"broken pipe",
		"network",
		"timeout",
		"connection refused",
		"eof",
	}

	for _, netErr := range networkErrors {
		if strings.Contains(errStr, netErr) {
			return true
		}
	}

	return false
}
