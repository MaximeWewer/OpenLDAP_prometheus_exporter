package pool

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/security"
)

// Configuration constants
const (
	DefaultPoolSize      = 5
	DefaultSearchTimeout = 30 * time.Second

	// EventsPoolType is the metrics component/pool_type label for the events
	// stream's dedicated client. EventsPoolSize keeps that pool small: the
	// events runner drives a single sequential ticker, so one connection plus
	// one spare for churn is plenty and it never competes with the scrape pool.
	EventsPoolType = "events"
	EventsPoolSize = 2

	// AccesslogTimeLimitMargin is subtracted from the configured LDAP timeout
	// to derive the server-side TimeLimit on accesslog scans, so slapd returns
	// timeLimitExceeded a beat before the client socket deadline would fire.
	AccesslogTimeLimitMargin = 1 * time.Second
)

// CircuitBreakerMonitoring defines the interface for circuit breaker monitoring.
// The component label separates the scrape breaker ("ldap") from the events
// stream breaker ("events").
type CircuitBreakerMonitoring interface {
	RecordCircuitBreakerState(server, component string, state circuitbreaker.State)
	RecordCircuitBreakerRequest(server, component, result string)
	RecordCircuitBreakerFailure(server, component string)
}

// PooledLDAPClient wraps the connection pool to provide a simple interface with circuit breaker protection
type PooledLDAPClient struct {
	pool           PoolInterface // Interface to support both regular and adaptive pools
	config         *config.Config
	circuitBreaker *circuitbreaker.CircuitBreaker
	isAdaptive     bool
	cbMonitoring   CircuitBreakerMonitoring // Optional circuit breaker monitoring
	serverName     string                   // Server name for monitoring
	component      string                   // Circuit breaker component label ("ldap" scrape, "events" stream)

	// baseCtx is the scrape-scoped context the Search* methods derive
	// their per-request timeout from. It is set by the exporter at the
	// top of Collect() via SetBaseContext so canceling a scrape (or
	// the process shutting down) propagates all the way down to any
	// in-flight LDAP round-trip, instead of each search running against
	// a fresh context.Background() that ignores the outer deadline.
	baseCtxMu sync.RWMutex
	baseCtx   context.Context
}

// PoolInterface defines the interface that both ConnectionPool and AdaptiveConnectionPool implement
type PoolInterface interface {
	Get(ctx context.Context) (*PooledConnection, error)
	Put(conn *PooledConnection)
	Discard(conn *PooledConnection)
	Close()
	Stats() map[string]interface{}
}

// SetBaseContext installs a scrape-scoped context that every subsequent
// Search* call will derive its per-request timeout from. Pass
// context.Background() when a scrape ends so background probes (e.g.
// the maintenance goroutine) do not end up waiting on a canceled
// context that belonged to the previous scrape. The exporter calls this
// at the top of Collect() under its own serialization lock so there is
// at most one in-flight scrape at a time.
func (c *PooledLDAPClient) SetBaseContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.baseCtxMu.Lock()
	c.baseCtx = ctx
	c.baseCtxMu.Unlock()
}

// currentBaseContext returns the installed scrape context, or
// context.Background() when none has been set. Used internally by every
// Search* method so canceling the outer scrape propagates to any
// in-flight LDAP round-trip.
func (c *PooledLDAPClient) currentBaseContext() context.Context {
	c.baseCtxMu.RLock()
	ctx := c.baseCtx
	c.baseCtxMu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// createCircuitBreakerConfig builds the circuit breaker configuration from the
// operator-tunable CIRCUIT_BREAKER_* settings. config.LoadConfig already floors
// any non-positive value to the package default, so the values here are safe to
// pass through directly.
func createCircuitBreakerConfig(cfg *config.Config) circuitbreaker.CircuitBreakerConfig {
	return circuitbreaker.CircuitBreakerConfig{
		MaxFailures:      cfg.CBMaxFailures,
		Timeout:          cfg.CBTimeout,
		ResetTimeout:     cfg.CBResetTimeout,
		SuccessThreshold: cfg.CBSuccessThreshold,
	}
}

// accesslogTimeLimitSeconds returns the server-side LDAP TimeLimit (in whole
// seconds) applied to cn=accesslog scans. It is derived from the configured
// LDAP timeout less AccesslogTimeLimitMargin so slapd aborts the search with
// result code 3 before the client socket deadline closes the connection,
// floored at 1 second.
func (c *PooledLDAPClient) accesslogTimeLimitSeconds() int {
	secs := int((c.config.Timeout - AccesslogTimeLimitMargin) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return secs
}

// NewPooledLDAPClient creates a new pooled LDAP client with circuit breaker protection
func NewPooledLDAPClient(cfg *config.Config) *PooledLDAPClient {
	return NewPooledLDAPClientWithOptions(cfg, false)
}

// NewPooledLDAPClientWithMonitoring creates a new pooled LDAP client with monitoring support
// for the main metric-scrape path (pool_type / component = "ldap").
func NewPooledLDAPClientWithMonitoring(cfg *config.Config, monitoring PoolMonitoring, serverName string) *PooledLDAPClient {
	return newMonitoredClient(cfg, monitoring, serverName, DefaultPoolType, DefaultPoolSize)
}

// NewEventsLDAPClient creates a pooled LDAP client dedicated to the JSON events
// stream. It owns a separate connection pool and circuit breaker, labeled with
// component / pool_type = "events", so a slow or failing accesslog scan on the
// events ticker can never trip the scrape's breaker or drain the scrape's pool.
// This isolation is the fix for the events-vs-scrape contention that opened the
// shared breaker in a loop.
func NewEventsLDAPClient(cfg *config.Config, monitoring PoolMonitoring, serverName string) *PooledLDAPClient {
	return newMonitoredClient(cfg, monitoring, serverName, EventsPoolType, EventsPoolSize)
}

// newMonitoredClient builds a pooled client whose pool and circuit breaker
// metrics are labeled with the given component (also used as the pool_type) and
// whose pool is sized to poolSize.
func newMonitoredClient(cfg *config.Config, monitoring PoolMonitoring, serverName, component string, poolSize int) *PooledLDAPClient {
	// Create pool with monitoring support, labeled by component.
	pool := NewConnectionPoolWithType(cfg, poolSize, monitoring, serverName, component)

	// Create circuit breaker with standard configuration
	cb := circuitbreaker.NewCircuitBreaker(createCircuitBreakerConfig(cfg))

	// Check if monitoring supports circuit breaker monitoring
	var cbMonitoring CircuitBreakerMonitoring
	if cbMon, ok := monitoring.(CircuitBreakerMonitoring); ok {
		cbMonitoring = cbMon
	}

	// Set up circuit breaker monitoring callback if monitoring supports it
	if cbMonitoring != nil {
		// Record initial state
		cbMonitoring.RecordCircuitBreakerState(serverName, component, cb.GetState())

		// Set up state change callback
		cb.SetStateChangeCallback(func(from, to circuitbreaker.State) {
			cbMonitoring.RecordCircuitBreakerState(serverName, component, to)
			logger.SafeWarn("pooled_client", "LDAP circuit breaker state changed", map[string]interface{}{
				"server":    serverName,
				"component": component,
				"from":      from.String(),
				"to":        to.String(),
			})
		})
	}

	return &PooledLDAPClient{
		pool:           pool,
		config:         cfg,
		circuitBreaker: cb,
		isAdaptive:     false,
		cbMonitoring:   cbMonitoring,
		serverName:     serverName,
		component:      component,
	}
}

// NewPooledLDAPClientWithOptions creates a new pooled LDAP client with options
func NewPooledLDAPClientWithOptions(cfg *config.Config, _ bool) *PooledLDAPClient {
	// For now, always use regular pool (adaptive pool needs more work)
	pool := NewConnectionPool(cfg, DefaultPoolSize)
	useAdaptivePool := false

	// Create circuit breaker with standard configuration
	circuitBreakerInstance := circuitbreaker.NewCircuitBreaker(createCircuitBreakerConfig(cfg))

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
		component:      DefaultPoolType,
	}
}

// runSearch executes a search on a pooled connection with one retry on a
// dropped connection. A pooled connection can be silently closed by the server
// or a stateful firewall after a long idle period; its first reuse then fails
// with a network error ("connection closed"). Rather than failing the whole
// scrape, the broken connection is discarded (closed + de-counted) and the
// search replayed once on a fresh connection. Searches here are read-only and
// idempotent, so the replay is safe. The connection is always either returned
// to the pool (success / non-network error — still healthy) or discarded
// (network error), never both, which is what keeps the active-connection count
// accurate over long uptimes.
func (c *PooledLDAPClient) runSearch(ctx context.Context, req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		conn, err := c.pool.Get(ctx)
		if err != nil {
			return nil, err
		}

		res, err := conn.conn.Search(req)
		if err == nil {
			c.pool.Put(conn)
			return res, nil
		}

		if isNetworkError(err) {
			// Drop the broken connection and retry once with a fresh one.
			c.pool.Discard(conn)
			lastErr = err
			continue
		}

		// Non-network error (e.g. an LDAP result code): the connection is
		// still healthy, so return it for reuse and surface the error.
		c.pool.Put(conn)
		return nil, err
	}
	return nil, lastErr
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
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

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

		searchResult, err := c.runSearch(ctx, searchRequest)
		if err != nil {
			logger.SafeError("pooled_client", "LDAP search failed", err, map[string]interface{}{
				"baseDN": baseDN,
				"filter": filter,
			})
			return err
		}

		result = searchResult
		return nil
	})

	// Record circuit breaker monitoring
	if c.cbMonitoring != nil && c.serverName != "" {
		if err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitBreakerOpen) {
				// Request was blocked by circuit breaker
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "blocked")
			} else {
				// Request was allowed but failed
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
				c.cbMonitoring.RecordCircuitBreakerFailure(c.serverName, c.component)
			}
		} else {
			// Request was allowed and succeeded
			c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
		}
	}

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

// SearchContextCSN performs a base-scoped LDAP search for the contextCSN attribute
// on a suffix entry. This is used for replication monitoring and is restricted to
// only read the contextCSN operational attribute with base scope.
func (c *PooledLDAPClient) SearchContextCSN(suffixDN string) (*ldap.SearchResult, error) {
	if suffixDN == "" {
		return nil, errors.New("invalid search parameters: suffixDN cannot be empty")
	}

	// Security validation: validate DN is a valid suffix (not cn=config, etc.)
	if err := security.ValidateSuffixDN(suffixDN); err != nil {
		logger.SafeError("pooled_client", "Suffix DN validation failed", err, map[string]interface{}{
			"suffixDN": suffixDN,
		})
		return nil, err
	}

	var result *ldap.SearchResult

	err := c.circuitBreaker.Call(func() error {
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

		// Base-scoped search for contextCSN only
		searchRequest := ldap.NewSearchRequest(
			suffixDN,
			ldap.ScopeBaseObject,
			ldap.NeverDerefAliases,
			0,
			0,
			false,
			"(objectClass=*)",
			[]string{"contextCSN"},
			nil,
		)

		searchResult, err := c.runSearch(ctx, searchRequest)
		if err != nil {
			return err
		}

		result = searchResult
		return nil
	})

	if c.cbMonitoring != nil && c.serverName != "" {
		if err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitBreakerOpen) {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "blocked")
			} else {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
				c.cbMonitoring.RecordCircuitBreakerFailure(c.serverName, c.component)
			}
		} else {
			c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
		}
	}

	if err != nil {
		return nil, err
	}

	return result, nil
}

// SearchSuffix performs a subtree-scoped LDAP search under a suffix DN.
// This is used for querying user entries (e.g., ppolicy attributes) and is restricted
// to valid naming contexts (dc=, o=, ou=, c=) to prevent access to sensitive trees.
func (c *PooledLDAPClient) SearchSuffix(suffixDN, filter string, attributes []string) (*ldap.SearchResult, error) {
	if suffixDN == "" || filter == "" {
		return nil, errors.New("invalid search parameters: suffixDN and filter cannot be empty")
	}

	// Security validation: validate DN is a valid suffix (not cn=config, etc.)
	if err := security.ValidateSuffixDN(suffixDN); err != nil {
		logger.SafeError("pooled_client", "Suffix DN validation failed", err, map[string]interface{}{
			"suffixDN": suffixDN,
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

	var result *ldap.SearchResult

	err := c.circuitBreaker.Call(func() error {
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

		searchRequest := ldap.NewSearchRequest(
			suffixDN,
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			0,
			0,
			false,
			filter,
			attributes,
			nil,
		)

		searchResult, err := c.runSearch(ctx, searchRequest)
		if err != nil {
			return err
		}

		result = searchResult
		return nil
	})

	if c.cbMonitoring != nil && c.serverName != "" {
		if err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitBreakerOpen) {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "blocked")
			} else {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
				c.cbMonitoring.RecordCircuitBreakerFailure(c.serverName, c.component)
			}
		} else {
			c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
		}
	}

	if err != nil {
		return nil, err
	}

	logger.SafeDebug("pooled_client", "Suffix search completed", map[string]interface{}{
		"suffixDN":      suffixDN,
		"filter":        filter,
		"entries_found": len(result.Entries),
	})

	return result, nil
}

// SearchAccessLog performs a subtree-scoped LDAP search on the accesslog database (cn=accesslog).
// This is used for querying bind operation logs and is restricted to the cn=accesslog tree.
func (c *PooledLDAPClient) SearchAccessLog(filter string, attributes []string) (*ldap.SearchResult, error) {
	if filter == "" {
		return nil, errors.New("invalid search parameters: filter cannot be empty")
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

	var result *ldap.SearchResult

	err := c.circuitBreaker.Call(func() error {
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

		searchRequest := ldap.NewSearchRequest(
			"cn=accesslog",
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			0,
			// Server-side time limit (seconds). Unlike the client socket
			// deadline (conn.SetTimeout = config.Timeout), which tears the
			// connection down when it fires, an LDAP TimeLimit makes slapd
			// return result code 3 (timeLimitExceeded) on the live
			// connection, so the conn is returned to the pool intact instead
			// of being invalidated and re-dialed. Set just under the socket
			// deadline so the server wins the race. cn=accesslog can be a
			// large/unindexed subtree, so this bounds an otherwise unbounded
			// scan rather than letting it hang until the socket dies.
			c.accesslogTimeLimitSeconds(),
			false,
			filter,
			attributes,
			nil,
		)

		searchResult, err := c.runSearch(ctx, searchRequest)
		if err != nil {
			return err
		}

		result = searchResult
		return nil
	})

	if c.cbMonitoring != nil && c.serverName != "" {
		if err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitBreakerOpen) {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "blocked")
			} else {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
				c.cbMonitoring.RecordCircuitBreakerFailure(c.serverName, c.component)
			}
		} else {
			c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
		}
	}

	if err != nil {
		return nil, err
	}

	logger.SafeDebug("pooled_client", "Accesslog search completed", map[string]interface{}{
		"filter":        filter,
		"entries_found": len(result.Entries),
	})

	return result, nil
}

// SearchConfig performs a subtree-scoped search on the configuration backend
// (cn=config). It is used to read replication topology (olcSyncrepl,
// olcMultiProvider/olcMirrorMode) and is restricted to the cn=config tree.
//
// Security note: cn=config can hold secrets (rootDN password hashes, syncrepl
// bind credentials). Callers MUST request only a narrow, non-sensitive
// attribute allow-list, and this method deliberately never logs entry values —
// only the entry count — so an olcSyncrepl credentials= clause can never leak
// into logs. The search returns nothing (not an error to the scrape) when the
// bind identity lacks read access to cn=config, so deployments that bind only
// to the monitor tree simply do not get these metrics.
func (c *PooledLDAPClient) SearchConfig(filter string, attributes []string) (*ldap.SearchResult, error) {
	if filter == "" {
		return nil, errors.New("invalid search parameters: filter cannot be empty")
	}

	if err := security.ValidateLDAPFilter(filter); err != nil {
		logger.SafeError("pooled_client", "Filter validation failed", err, map[string]interface{}{"filter": filter})
		return nil, err
	}

	for _, attr := range attributes {
		if err := security.ValidateLDAPAttribute(attr); err != nil {
			logger.SafeError("pooled_client", "Attribute validation failed", err, map[string]interface{}{"attribute": attr})
			return nil, err
		}
	}

	var result *ldap.SearchResult

	err := c.circuitBreaker.Call(func() error {
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

		searchRequest := ldap.NewSearchRequest(
			"cn=config",
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			0,
			0,
			false,
			filter,
			attributes,
			nil,
		)

		searchResult, err := c.runSearch(ctx, searchRequest)
		if err != nil {
			return err
		}

		result = searchResult
		return nil
	})

	if c.cbMonitoring != nil && c.serverName != "" {
		if err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitBreakerOpen) {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "blocked")
			} else {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
				c.cbMonitoring.RecordCircuitBreakerFailure(c.serverName, c.component)
			}
		} else {
			c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
		}
	}

	if err != nil {
		return nil, err
	}

	// Never log entry values here — see the security note above.
	logger.SafeDebug("pooled_client", "Config search completed", map[string]interface{}{
		"filter":        filter,
		"entries_found": len(result.Entries),
	})

	return result, nil
}

// SearchRootDSE performs a base-scoped search on the RootDSE (empty base DN)
// to retrieve server capabilities like supportedControl, supportedExtension, etc.
func (c *PooledLDAPClient) SearchRootDSE(attributes []string) (*ldap.SearchResult, error) {
	// Validate attributes
	for _, attr := range attributes {
		if err := security.ValidateLDAPAttribute(attr); err != nil {
			return nil, err
		}
	}

	var result *ldap.SearchResult

	err := c.circuitBreaker.Call(func() error {
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

		searchRequest := ldap.NewSearchRequest(
			"",
			ldap.ScopeBaseObject,
			ldap.NeverDerefAliases,
			0,
			0,
			false,
			"(objectClass=*)",
			attributes,
			nil,
		)

		searchResult, err := c.runSearch(ctx, searchRequest)
		if err != nil {
			return err
		}

		result = searchResult
		return nil
	})

	if c.cbMonitoring != nil && c.serverName != "" {
		if err != nil {
			if errors.Is(err, circuitbreaker.ErrCircuitBreakerOpen) {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "blocked")
			} else {
				c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
				c.cbMonitoring.RecordCircuitBreakerFailure(c.serverName, c.component)
			}
		} else {
			c.cbMonitoring.RecordCircuitBreakerRequest(c.serverName, c.component, "allowed")
		}
	}

	if err != nil {
		return nil, err
	}

	return result, nil
}

// PeerCertificates returns the x509 chain the LDAP server presented on the
// live TLS handshake, leaf first. It returns (nil, nil) when TLS is disabled
// or the pooled connection is not TLS, so callers can simply skip emitting
// metrics in that case. The connection is borrowed from the pool and returned
// untouched — reading the handshake state issues no LDAP round-trip.
func (c *PooledLDAPClient) PeerCertificates() ([]*x509.Certificate, error) {
	if !c.config.TLS {
		return nil, nil
	}

	var certs []*x509.Certificate

	err := c.circuitBreaker.Call(func() error {
		ctx, cancel := context.WithTimeout(c.currentBaseContext(), DefaultSearchTimeout)
		defer cancel()

		conn, err := c.pool.Get(ctx)
		if err != nil {
			return err
		}

		state, ok := conn.conn.TLSConnectionState()
		if !ok {
			// Connection is not TLS despite TLS being configured; nothing to
			// report rather than an error. The connection is healthy, so return
			// it to the pool.
			c.pool.Put(conn)
			return nil
		}
		certs = state.PeerCertificates
		c.pool.Put(conn)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return certs, nil
}

// Close closes the connection pool
func (c *PooledLDAPClient) Close() {
	if c.pool != nil {
		c.pool.Close()
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
