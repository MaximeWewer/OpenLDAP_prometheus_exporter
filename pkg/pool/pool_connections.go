package pool

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

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

	if time.Since(conn.createdAt) > p.maxIdleTime {
		return false
	}

	if !conn.inUse && time.Since(conn.lastUsed) > p.idleTimeout {
		return false
	}

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

	searchRequest := ldap.NewSearchRequest(
		"",
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		1,
		3,
		false,
		"(objectClass=*)",
		[]string{"objectClass"},
		nil,
	)

	_, err := conn.conn.Search(searchRequest)
	return err == nil
}

// closeConnection safely closes a pooled connection
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
			if p.monitoring != nil && p.serverName != "" {
				p.monitoring.RecordPoolConnectionClosed(p.serverName, "ldap", reason)
			}
		}
		atomic.AddInt64(&p.activeConns, -1)
	}

	logger.SafeDebug("pool", "LDAP connection closed", map[string]interface{}{
		"server":             p.config.ServerName,
		"active_connections": atomic.LoadInt64(&p.activeConns),
	})
}
