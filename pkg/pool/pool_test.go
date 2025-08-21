package pool

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
)

// setupTestConfig creates a test configuration
func setupTestConfig(t *testing.T) (*config.Config, func()) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("LDAP_TIMEOUT", "5")

	cleanup := func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("LDAP_TIMEOUT")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		cleanup()
		t.Fatalf("Failed to load config: %v", err)
	}

	return cfg, func() {
		cfg.Clear()
		cleanup()
	}
}

// TestNewConnectionPool tests pool creation
func TestNewConnectionPool(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 5)
	defer pool.Close()

	if pool == nil {
		t.Fatal("NewConnectionPool should return non-nil pool")
	}

	if pool.maxConnections != 5 {
		t.Errorf("Expected max connections 5, got %d", pool.maxConnections)
	}

	if pool.closed {
		t.Error("Pool should not be closed initially")
	}

	if pool.pool == nil {
		t.Error("Pool channel should be initialized")
	}

	if cap(pool.pool) != 5 {
		t.Errorf("Pool channel capacity should be 5, got %d", cap(pool.pool))
	}
}

// TestPoolGet tests getting connections from pool
func TestPoolGet(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	ctx := context.Background()

	// Test getting connection (will fail to connect but should handle gracefully)
	conn, err := pool.Get(ctx)
	if err == nil {
		// Unexpected success - close the connection
		pool.Put(conn)
		t.Log("Unexpectedly got a connection (LDAP server might be running)")
	} else {
		// Expected failure since test.example.com doesn't exist
		t.Logf("Expected connection failure: %v", err)
	}
}

// TestPoolGetWithClosedPool tests getting from closed pool
func TestPoolGetWithClosedPool(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	pool.Close()

	ctx := context.Background()
	conn, err := pool.Get(ctx)
	if err == nil {
		t.Error("Should return error when getting from closed pool")
		if conn != nil {
			pool.Put(conn)
		}
	}

	if err.Error() != "connection pool is closed" {
		t.Errorf("Expected 'connection pool is closed' error, got %v", err)
	}
}

// TestPoolPut tests returning connections to pool
func TestPoolPut(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 3)
	defer pool.Close()

	// Test putting nil connection (should be handled gracefully)
	pool.Put(nil)

	// Test putting connection to closed pool
	pool.Close()
	testConn := &PooledConnection{
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}
	pool.Put(testConn) // Should not panic
}

// TestPoolStats tests pool statistics
func TestPoolStats(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 3)
	defer pool.Close()

	stats := pool.Stats()

	// Check required stats
	requiredStats := []string{"max_connections", "active_connections", "pool_size", "closed"}
	for _, stat := range requiredStats {
		if _, exists := stats[stat]; !exists {
			t.Errorf("Stats should include %s", stat)
		}
	}

	// Verify initial values
	if stats["max_connections"] != 3 {
		t.Errorf("Expected max_connections 3, got %v", stats["max_connections"])
	}

	if stats["active_connections"] != 0 {
		t.Errorf("Expected active_connections 0, got %v", stats["active_connections"])
	}

	if stats["closed"] != false {
		t.Errorf("Expected closed false, got %v", stats["closed"])
	}
}

// TestPoolClose tests closing the pool
func TestPoolClose(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)

	// Close the pool
	pool.Close()

	if !pool.closed {
		t.Error("Pool should be marked as closed")
	}

	// Close again should not panic
	pool.Close()
}

// TestPoolConcurrency tests concurrent pool operations
func TestPoolConcurrency(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 5)
	defer pool.Close()

	var wg sync.WaitGroup
	ctx := context.Background()

	// Start multiple goroutines trying to get connections
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := pool.Get(ctx)
			if err == nil {
				// If we somehow get a connection, return it
				time.Sleep(10 * time.Millisecond)
				pool.Put(conn)
			}

			// Get stats (should not race)
			stats := pool.Stats()
			_ = stats["active_connections"]
		}(i)
	}

	wg.Wait()
}

// TestPooledConnection tests PooledConnection methods
func TestPooledConnection(t *testing.T) {
	conn := &PooledConnection{
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     false,
		conn:      nil,
	}

	// Test locking/unlocking (should not deadlock)
	conn.mutex.Lock()
	conn.inUse = true
	conn.mutex.Unlock()

	if !conn.inUse {
		t.Error("Connection should be marked in use")
	}
}

// TestConnectionValidation tests connection validation logic
func TestConnectionValidation(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Test nil connection
	if pool.isConnectionValid(nil) {
		t.Error("Nil connection should not be valid")
	}

	// Test connection with nil conn
	conn := &PooledConnection{
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		conn:      nil,
	}
	if pool.isConnectionValid(conn) {
		t.Error("Connection with nil conn should not be valid")
	}

	// Test old connection
	oldConn := &PooledConnection{
		createdAt: time.Now().Add(-pool.maxIdleTime - time.Hour),
		lastUsed:  time.Now(),
		conn:      nil,
	}
	if pool.isConnectionValid(oldConn) {
		t.Error("Old connection should not be valid")
	}

	// Test idle connection
	idleConn := &PooledConnection{
		createdAt: time.Now(),
		lastUsed:  time.Now().Add(-pool.idleTimeout - time.Hour),
		inUse:     false,
		conn:      nil,
	}
	if pool.isConnectionValid(idleConn) {
		t.Error("Idle connection should not be valid")
	}
}

// TestPingConnection tests the ping functionality
func TestPingConnection(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test nil connection
	if pool.pingConnection(nil) {
		t.Error("Ping should fail for nil connection")
	}

	// Test connection with nil conn
	conn := &PooledConnection{
		conn: nil,
	}
	if pool.pingConnection(conn) {
		t.Error("Ping should fail for connection with nil conn")
	}
}

// TestCloseConnection tests closing individual connections
func TestCloseConnection(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Test closing nil connection (should not panic)
	pool.closeConnection(nil)

	// Test closing connection with nil conn
	conn := &PooledConnection{
		conn: nil,
	}
	initialActive := pool.activeConns
	pool.closeConnection(conn)

	// Active connections should decrease
	if pool.activeConns != initialActive-1 {
		t.Errorf("Active connections should decrease after close, got %d", pool.activeConns)
	}
}

// TestPoolGetWithTimeout tests getting connection with context timeout
func TestPoolGetWithTimeout(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Wait for timeout
	time.Sleep(5 * time.Millisecond)

	conn, err := pool.Get(ctx)
	if err == nil {
		pool.Put(conn)
		t.Error("Should return error on context timeout")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

// TestBuildTLSConfig tests TLS configuration building
func TestBuildTLSConfig(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test basic TLS config (no CA/cert)
	cfg.TLS = true
	tlsConfig, err := pool.buildTLSConfig()
	if err != nil {
		t.Errorf("Should build basic TLS config without error: %v", err)
	}

	if tlsConfig == nil {
		t.Fatal("TLS config should not be nil")
	}

	if tlsConfig.InsecureSkipVerify != cfg.TLSSkipVerify {
		t.Error("TLS config should respect InsecureSkipVerify setting")
	}

	// Test with invalid CA file
	cfg.TLSCA = "/nonexistent/ca.pem"
	_, err = pool.buildTLSConfig()
	if err == nil {
		t.Error("Should return error for nonexistent CA file")
	}

	// Test with invalid cert/key files
	cfg.TLSCA = ""
	cfg.TLSCert = "/nonexistent/cert.pem"
	cfg.TLSKey = "/nonexistent/key.pem"
	_, err = pool.buildTLSConfig()
	if err == nil {
		t.Error("Should return error for nonexistent cert/key files")
	}
}

// TestEstablishConnection tests connection establishment
func TestEstablishConnection(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test plain connection (will fail but should handle error)
	conn, err := pool.establishConnection()
	if err == nil {
		// Unexpected success
		conn.Close()
		t.Log("Unexpectedly established connection (LDAP server might be running)")
	} else {
		// Expected failure
		t.Logf("Expected connection failure: %v", err)
	}

	// Test TLS connection (will fail because test.example.com doesn't exist)
	cfg.TLS = true
	conn, err = pool.establishConnection()
	if err == nil {
		conn.Close()
		t.Log("Unexpectedly established TLS connection")
	} else {
		t.Logf("Expected TLS connection failure: %v", err)
	}
}

// TestSecureWipeString tests the secure string wiping function
func TestSecureWipeString(t *testing.T) {
	// Test with empty string (should not panic)
	secureWipeString("")

	// Note: We can't safely test secureWipeString with real strings since
	// it uses unsafe operations and string literals are immutable.
	// The function is designed to work with password strings obtained from
	// config.Password.String() which returns mutable string data.
	// We just test that empty string doesn't panic.
	t.Log("secureWipeString with empty string completed successfully")
}

// TestConnectionPoolMaintenance tests the maintenance routine
func TestConnectionPoolMaintenance(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Create pool with very short timeouts for testing
	pool := NewConnectionPool(cfg, 3)
	pool.idleTimeout = 100 * time.Millisecond
	pool.maxIdleTime = 200 * time.Millisecond

	// Let maintenance run
	time.Sleep(150 * time.Millisecond)

	// Close pool to stop maintenance
	pool.Close()
}

// TestIsConnectionValidLocked tests the locked validation method
func TestIsConnectionValidLocked(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test with nil connection
	if pool.isConnectionValidLocked(nil) {
		t.Error("Nil connection should not be valid")
	}

	// Test with connection that has nil conn field
	conn := &PooledConnection{
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     true,
		conn:      nil, // This will make the connection invalid
	}

	conn.mutex.Lock()
	valid := pool.isConnectionValidLocked(conn)
	conn.mutex.Unlock()

	// Should NOT be valid because conn.conn is nil
	if valid {
		t.Error("Connection with nil conn field should not be valid")
	}
}

// TestPoolRetries tests the retry logic in Get
func TestPoolRetries(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Fill the pool with an invalid connection
	invalidConn := &PooledConnection{
		createdAt: time.Now().Add(-pool.maxIdleTime - time.Hour), // Old connection
		lastUsed:  time.Now(),
		conn:      nil,
	}

	select {
	case pool.pool <- invalidConn:
		// Successfully added to pool
	default:
		t.Fatal("Failed to add test connection to pool")
	}

	ctx := context.Background()

	// Get should retry and eventually fail to create new connection
	conn, err := pool.Get(ctx)
	if err == nil {
		pool.Put(conn)
		t.Log("Unexpectedly got a connection")
	} else {
		t.Logf("Expected failure after retries: %v", err)
	}
}
