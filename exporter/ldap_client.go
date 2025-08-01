package main

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// LDAPClient wraps an LDAP connection with configuration and provides search methods
type LDAPClient struct {
	conn   *ldap.Conn
	config *Config
}

// NewLDAPClient creates a new LDAP client with the given configuration and establishes connection
func NewLDAPClient(config *Config) (*LDAPClient, error) {
	client := &LDAPClient{
		config: config,
	}

	Debug("Creating new LDAP client", map[string]interface{}{
		"url": config.URL,
		"server": config.ServerName,
		"tls": config.TLS,
	})

	if err := client.connect(); err != nil {
		Error("Failed to establish LDAP connection", err, map[string]interface{}{
			"url": config.URL,
		})
		return nil, err
	}

	Info("LDAP connection established successfully", map[string]interface{}{
		"url": config.URL,
		"server": config.ServerName,
	})

	return client, nil
}

// connect establishes a connection to the LDAP server with proper TLS and authentication
func (c *LDAPClient) connect() error {
	var conn *ldap.Conn
	var err error

	if c.config.TLS {
		Debug("Using TLS connection")
		tlsConfig, err := c.buildTLSConfig()
		if err != nil {
			Error("Failed to build TLS config", err)
			return err
		}

		// Use DialURL with TLS config for TLS connections
		conn, err = ldap.DialURL(c.config.URL, ldap.DialWithTLSConfig(tlsConfig))
		if err == nil {
			Debug("TLS connection established", map[string]interface{}{"url": c.config.URL})
		}
	} else {
		Debug("Using plain LDAP connection")
		conn, err = ldap.DialURL(c.config.URL)
		if err == nil {
			Debug("Plain LDAP connection established", map[string]interface{}{"url": c.config.URL})
		}
	}

	if err != nil {
		Error("Failed to dial LDAP server", err, map[string]interface{}{"url": c.config.URL})
		return err
	}

	conn.SetTimeout(c.config.Timeout)

	if c.config.Username != "" && c.config.Password != "" {
		Debug("Attempting LDAP bind", map[string]interface{}{"username": c.config.Username})
		err = conn.Bind(c.config.Username, c.config.Password)
		if err != nil {
			Error("LDAP authentication failed", err, map[string]interface{}{"username": c.config.Username})
			conn.Close()
			return err
		}
		Debug("LDAP bind successful", map[string]interface{}{"username": c.config.Username})
	}

	c.conn = conn
	return nil
}

// buildTLSConfig creates a TLS configuration based on the client's TLS settings
func (c *LDAPClient) buildTLSConfig() (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.config.TLSSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}

	if c.config.TLSCA != "" {
		caCert, err := os.ReadFile(c.config.TLSCA)
		if err != nil {
			Error("Failed to read CA certificate file", err, map[string]interface{}{"ca_file": c.config.TLSCA})
			return nil, err
		}
		
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			Error("Failed to parse CA certificate", nil, map[string]interface{}{"ca_file": c.config.TLSCA})
			return nil, nil
		}
		tlsConfig.RootCAs = caCertPool
	}

	if c.config.TLSCert != "" && c.config.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(c.config.TLSCert, c.config.TLSKey)
		if err != nil {
			Error("Failed to load client certificate", err, map[string]interface{}{
				"cert_file": c.config.TLSCert,
				"key_file": c.config.TLSKey,
			})
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// Search performs an LDAP search with the given base DN, filter and attributes
func (c *LDAPClient) Search(baseDN, filter string, attributes []string) (*ldap.SearchResult, error) {
	if c.conn == nil {
		Debug("Connection lost, attempting to reconnect")
		if err := c.connect(); err != nil {
			Error("Failed to reconnect to LDAP server", err)
			return nil, err
		}
		Debug("Reconnection successful")
	}

	if baseDN == "" || filter == "" {
		Error("Invalid search parameters", nil, map[string]interface{}{
			"baseDN": baseDN,
			"filter": filter,
		})
		return nil, nil
	}

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

	result, err := c.conn.Search(searchRequest)
	if err != nil {
		Error("LDAP search failed", err, map[string]interface{}{
			"baseDN": baseDN,
			"filter": filter,
		})
		c.Close()
		return nil, err
	}

	Debug("LDAP search completed", map[string]interface{}{
		"baseDN": baseDN,
		"filter": filter,
		"entries_found": len(result.Entries),
	})

	return result, nil
}

// GetDatabaseSuffixes retrieves database suffixes from the LDAP configuration
func (c *LDAPClient) GetDatabaseSuffixes() (map[string]string, error) {
	Debug("Querying database suffixes from LDAP config")
	result, err := c.Search("cn=config", "(objectClass=olcMdbConfig)", []string{"dn", "olcSuffix"})
	if err != nil {
		Error("Failed to query database suffixes", err)
		return nil, err
	}

	suffixes := make(map[string]string, len(result.Entries))
	for _, entry := range result.Entries {
		dn := entry.DN
		suffix := entry.GetAttributeValue("olcSuffix")

		if suffix == "" {
			continue
		}

		dbName := extractDatabaseName(dn)
		if dbName != "" {
			suffixes[dbName] = suffix
		}
	}

	Info("Database suffixes retrieved successfully", map[string]interface{}{"suffix_count": len(suffixes)})
	return suffixes, nil
}

// extractDatabaseName extracts the database number from an LDAP DN
func extractDatabaseName(dn string) string {
	const prefix = "olcDatabase={"
	const suffix = "}mdb,cn=config"

	startIdx := strings.Index(dn, prefix)
	if startIdx == -1 {
		return ""
	}

	startIdx += len(prefix)
	endIdx := strings.Index(dn[startIdx:], "}")
	if endIdx == -1 {
		return ""
	}

	dbNum := dn[startIdx : startIdx+endIdx]
	if dbNum == "" {
		return ""
	}

	return "Database " + dbNum
}

// Close closes the LDAP connection and cleans up resources
func (c *LDAPClient) Close() {
	if c.conn != nil {
		Debug("Closing LDAP connection")
		c.conn.Close()
		c.conn = nil
	}
}