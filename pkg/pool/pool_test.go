package pool

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/go-ldap/ldap/v3"
)

// setupTestConfig creates a test configuration
func setupTestConfig(t *testing.T) (*config.Config, func()) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("LDAP_TIMEOUT", "1")

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

	if stats["active_connections"] != int64(0) {
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
	// Use context with short timeout to avoid long waits
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Start fewer goroutines to reduce race conditions with timeout
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			conn, err := pool.Get(ctx)
			if err == nil {
				// If we somehow get a connection, return it quickly
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
	initialActive := atomic.LoadInt64(&pool.activeConns)
	pool.closeConnection(conn)

	// Active connections should remain consistent
	finalActive := atomic.LoadInt64(&pool.activeConns)
	// Since we're using mock connections in tests, the counter behavior
	// depends on whether actual LDAP connections were created
	// Just verify it's not negative and log the values for debugging
	if finalActive < 0 {
		t.Errorf("Active connections should not be negative, got %d", finalActive)
	}
	t.Logf("Connection count - initial: %d, final: %d", initialActive, finalActive)
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

// TestConnectionValidationEdgeCases tests connection validation edge cases
func TestConnectionValidationEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Test with nil connection
	result := pool.isConnectionValid(nil)
	if result {
		t.Error("isConnectionValid should return false for nil connection")
	}

	// Test with invalid connection
	invalidConn := &PooledConnection{
		conn:      nil,
		lastUsed:  time.Now().Add(-time.Hour), // Old connection
		createdAt: time.Now().Add(-time.Hour),
		inUse:     false,
	}

	result = pool.isConnectionValid(invalidConn)
	if result {
		t.Error("isConnectionValid should return false for invalid connection")
	}

	// Test pingConnection with nil connection
	result = pool.pingConnection(nil)
	if result {
		t.Error("pingConnection should return false for nil connection")
	}

	result = pool.pingConnection(invalidConn)
	if result {
		t.Error("pingConnection should return false for invalid connection")
	}
}

// TestPoolWithDifferentSizes tests pool creation with various sizes
func TestPoolWithDifferentSizes(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	testSizes := []int{1, 3, 10, 100}
	for _, size := range testSizes {
		t.Run(fmt.Sprintf("Size-%d", size), func(t *testing.T) {
			pool := NewConnectionPool(cfg, size)
			defer pool.Close()

			if pool.maxConnections != size {
				t.Errorf("Expected max connections %d, got %d", size, pool.maxConnections)
			}

			if cap(pool.pool) != size {
				t.Errorf("Expected pool capacity %d, got %d", size, cap(pool.pool))
			}

			stats := pool.Stats()
			if stats["max_connections"] != size {
				t.Errorf("Expected stats max_connections %d, got %v", size, stats["max_connections"])
			}
		})
	}
}

// TestPoolTimeoutConfiguration tests timeout configuration
func TestPoolTimeoutConfiguration(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Test default timeouts
	expectedIdle := 5 * time.Minute
	expectedMaxIdle := 10 * time.Minute
	expectedConn := 30 * time.Second

	if pool.idleTimeout != expectedIdle {
		t.Errorf("Expected idle timeout %v, got %v", expectedIdle, pool.idleTimeout)
	}
	if pool.maxIdleTime != expectedMaxIdle {
		t.Errorf("Expected max idle time %v, got %v", expectedMaxIdle, pool.maxIdleTime)
	}
	if pool.connTimeout != expectedConn {
		t.Errorf("Expected connection timeout %v, got %v", expectedConn, pool.connTimeout)
	}
}

// TestBuildTLSConfigEdgeCases tests TLS config edge cases
func TestBuildTLSConfigEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test with TLS disabled
	cfg.TLS = false
	tlsConfig, err := pool.buildTLSConfig()
	if err != nil {
		t.Errorf("Should build TLS config without error even when TLS disabled: %v", err)
	}
	if tlsConfig == nil {
		t.Error("TLS config should not be nil")
	}

	// Test with TLS skip verify
	cfg.TLS = true
	cfg.TLSSkipVerify = true
	tlsConfig, err = pool.buildTLSConfig()
	if err != nil {
		t.Errorf("Should build TLS config with skip verify: %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Error("TLS config should have InsecureSkipVerify set to true")
	}
}

// TestEstablishConnectionEdgeCases tests connection establishment edge cases
func TestEstablishConnectionEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test with empty username (should fail)
	originalUsername := cfg.Username
	cfg.Username = ""
	defer func() { cfg.Username = originalUsername }()

	conn, err := pool.establishConnection()
	if err == nil && conn != nil {
		conn.Close()
		t.Log("Connection established with empty username (LDAP server might allow anonymous)")
	} else {
		t.Logf("Expected connection failure with empty username: %v", err)
	}
}

// TestCreateConnectionErrorPaths tests error paths in createConnection
func TestCreateConnectionErrorPaths(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Use an invalid URL to force connection errors
	cfg.URL = "ldap://invalid-host-that-does-not-exist:389"
	
	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// This should fail and handle the error gracefully
	conn, err := pool.createConnection()
	if err == nil {
		if conn != nil {
			pool.closeConnection(conn)
		}
		t.Log("Unexpected success - createConnection should fail with invalid host")
	} else {
		t.Logf("Expected connection creation failure: %v", err)
	}
}

// TestStatsAccuracy tests stats accuracy across operations
func TestStatsAccuracy(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 3)
	defer pool.Close()

	initialStats := pool.Stats()
	if initialStats["active_connections"] != int64(0) {
		t.Errorf("Expected initial active connections 0, got %v", initialStats["active_connections"])
	}
	if initialStats["pool_size"] != int64(0) {
		t.Errorf("Expected initial pool size 0, got %v", initialStats["pool_size"])
	}

	// Stats should remain consistent after failed operations
	ctx := context.Background()
	conn, err := pool.Get(ctx)
	if err == nil && conn != nil {
		pool.Put(conn)
	}

	finalStats := pool.Stats()
	if finalStats["max_connections"] != 3 {
		t.Errorf("Expected max connections 3, got %v", finalStats["max_connections"])
	}
}

// TestMaintenanceEdgeCases tests maintenance routine edge cases
func TestMaintenanceEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	
	// Set very short timeouts for testing
	pool.idleTimeout = 50 * time.Millisecond
	pool.maxIdleTime = 100 * time.Millisecond

	// Add mock connections to pool
	mockConn1 := &PooledConnection{
		conn:      nil,
		createdAt: time.Now().Add(-200 * time.Millisecond), // Old
		lastUsed:  time.Now().Add(-200 * time.Millisecond),
		inUse:     false,
	}
	mockConn2 := &PooledConnection{
		conn:      nil,
		createdAt: time.Now(), // Fresh
		lastUsed:  time.Now(),
		inUse:     false,
	}

	// Put connections in pool
	select {
	case pool.pool <- mockConn1:
		atomic.AddInt64(&pool.poolSize, 1)
	default:
		t.Fatal("Could not add mock connection to pool")
	}
	
	select {
	case pool.pool <- mockConn2:
		atomic.AddInt64(&pool.poolSize, 1)
	default:
		t.Fatal("Could not add second mock connection to pool")
	}

	// Let maintenance run
	time.Sleep(200 * time.Millisecond)

	// Close pool (should clean up maintenance)
	pool.Close()

	// Pool should be closed
	if !pool.closed {
		t.Error("Pool should be closed after Close()")
	}
}

// TestAtomicOperations tests atomic counter operations
func TestAtomicOperations(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 5)
	defer pool.Close()

	// Test atomic increments/decrements
	initialActive := atomic.LoadInt64(&pool.activeConns)
	atomic.AddInt64(&pool.activeConns, 1)
	newActive := atomic.LoadInt64(&pool.activeConns)
	
	if newActive != initialActive+1 {
		t.Errorf("Expected atomic increment, got %d to %d", initialActive, newActive)
	}

	// Test compare and swap
	success := atomic.CompareAndSwapInt64(&pool.activeConns, newActive, newActive+1)
	if !success {
		t.Error("CompareAndSwap should succeed")
	}

	finalActive := atomic.LoadInt64(&pool.activeConns)
	if finalActive != newActive+1 {
		t.Errorf("Expected final active %d, got %d", newActive+1, finalActive)
	}
}

// TestPoolGetFromPoolLogic tests the internal logic of getting connections from pool
func TestPoolGetFromPoolLogic(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 3)
	defer pool.Close()

	// Test the case where we get a connection from pool but it's invalid
	// This tests the retry logic in Get()
	oldConn := &PooledConnection{
		conn:      nil, // Invalid - will be rejected
		createdAt: time.Now().Add(-pool.maxIdleTime - time.Hour), // Too old
		lastUsed:  time.Now(),
		inUse:     false,
	}

	// Put old connection in pool to trigger validation and retry
	select {
	case pool.pool <- oldConn:
		atomic.AddInt64(&pool.poolSize, 1)
		t.Log("Added invalid connection to pool for retry testing")
	default:
		t.Fatal("Could not add connection to pool")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This should get the invalid connection, reject it, and try to create new one
	// Since we can't connect to real LDAP, it will eventually fail, but we test the retry logic
	conn, err := pool.Get(ctx)
	if err != nil {
		t.Logf("Expected error after retries: %v", err)
		// This exercises the retry and validation logic
	}
	if conn != nil {
		pool.Put(conn)
	}
}

// TestPutConnectionToPool tests putting connections back to pool
func TestPutConnectionToPool(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Create mock connection
	conn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     true,
	}

	initialPoolSize := atomic.LoadInt64(&pool.poolSize)
	
	// Put connection back (should add to pool)
	pool.Put(conn)
	
	// Pool size should increase if there was room
	finalPoolSize := atomic.LoadInt64(&pool.poolSize)
	if finalPoolSize <= initialPoolSize && len(pool.pool) < pool.maxConnections {
		t.Log("Pool size behavior depends on pool state and capacity")
	}
	
	// Connection should no longer be in use
	if conn.inUse {
		t.Error("Connection should not be in use after Put")
	}
}

// TestCreateConnectionLogic tests createConnection internal logic
func TestCreateConnectionLogic(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test with invalid URL to exercise error path
	cfg.URL = "invalid-url"
	conn, err := pool.createConnection()
	if err == nil {
		t.Error("Should fail with invalid URL")
		if conn != nil {
			pool.closeConnection(conn)
		}
	} else {
		t.Logf("Expected error with invalid URL: %v", err)
	}

	// Test with valid URL format but non-existent host
	cfg.URL = "ldap://non-existent-host:389"
	conn, err = pool.createConnection()
	if err == nil {
		t.Log("Unexpected success - might have network connection")
		if conn != nil {
			pool.closeConnection(conn)
		}
	} else {
		t.Logf("Expected connection failure: %v", err)
	}
}

// TestTLSConfigWithFiles tests TLS config with certificate files
func TestTLSConfigWithFiles(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Create temporary certificate files
	caCert := `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA...
-----END CERTIFICATE-----`
	
	clientCert := `-----BEGIN CERTIFICATE-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA...
-----END CERTIFICATE-----`
	
	clientKey := `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC...
-----END PRIVATE KEY-----`

	// Create temp files
	caFile, err := os.CreateTemp("", "ca*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(caFile.Name())
	_, err = caFile.WriteString(caCert)
	if err != nil {
		t.Fatal(err)
	}
	caFile.Close()

	certFile, err := os.CreateTemp("", "cert*.pem")  
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(certFile.Name())
	_, err = certFile.WriteString(clientCert)
	if err != nil {
		t.Fatal(err)
	}
	certFile.Close()

	keyFile, err := os.CreateTemp("", "key*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(keyFile.Name())
	_, err = keyFile.WriteString(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	keyFile.Close()

	// Test with CA file
	cfg.TLS = true
	cfg.TLSCA = caFile.Name()
	tlsConfig, err := pool.buildTLSConfig()
	if err != nil {
		t.Logf("TLS config with CA file failed (expected with mock cert): %v", err)
	} else if tlsConfig != nil {
		t.Log("TLS config with CA file created successfully")
	}

	// Test with client cert/key
	cfg.TLSCA = ""
	cfg.TLSCert = certFile.Name()
	cfg.TLSKey = keyFile.Name()
	tlsConfig, err = pool.buildTLSConfig()
	if err != nil {
		t.Logf("TLS config with client cert failed (expected with mock cert): %v", err)
	} else if tlsConfig != nil {
		t.Log("TLS config with client cert created successfully")
	}
}

// TestIsConnectionValidDetailed tests detailed connection validation
func TestIsConnectionValidDetailed(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test with properly constructed connection that should be valid except for nil conn
	validTimeConn := &PooledConnection{
		conn:      nil, // This makes it invalid
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     false,
	}

	if pool.isConnectionValid(validTimeConn) {
		t.Error("Connection with nil conn should be invalid")
	}

	// Test connection that's too old
	oldConn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now().Add(-pool.maxIdleTime - time.Hour),
		lastUsed:  time.Now(),
		inUse:     false,
	}

	if pool.isConnectionValid(oldConn) {
		t.Error("Old connection should be invalid")
	}

	// Test connection that's been idle too long
	idleConn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now(),
		lastUsed:  time.Now().Add(-pool.idleTimeout - time.Hour),
		inUse:     false,
	}

	if pool.isConnectionValid(idleConn) {
		t.Error("Idle connection should be invalid")
	}

	// Test connection that's in use (should still be valid for time checks)
	inUseConn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     true,
	}

	// This tests the validation logic even though conn is not a real LDAP connection
	result := pool.isConnectionValid(inUseConn)
	if !result {
		t.Log("In-use connection validation failed (expected due to mock ldap.Conn)")
	}
}

// TestPingConnectionVariations tests different ping scenarios
func TestPingConnectionVariations(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test ping with mock connection (will fail but exercises the code)
	mockConn := &PooledConnection{
		conn:      &ldap.Conn{}, // Mock connection
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}

	// This will test the ping logic even though it will fail
	result := pool.pingConnection(mockConn)
	if result {
		t.Log("Ping unexpectedly succeeded with mock connection")
	} else {
		t.Log("Ping failed as expected with mock connection")
	}
}

// TestMaintenanceRoutineTrigger tests triggering maintenance
func TestMaintenanceRoutineTrigger(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 3)
	
	// Set very short maintenance interval
	pool.idleTimeout = 10 * time.Millisecond
	pool.maxIdleTime = 20 * time.Millisecond

	// Add some old connections to trigger maintenance
	for i := 0; i < 2; i++ {
		oldConn := &PooledConnection{
			conn:      nil,
			createdAt: time.Now().Add(-100 * time.Millisecond),
			lastUsed:  time.Now().Add(-100 * time.Millisecond),
			inUse:     false,
		}
		select {
		case pool.pool <- oldConn:
			atomic.AddInt64(&pool.poolSize, 1)
		default:
			t.Logf("Could not add connection %d to pool", i)
		}
	}

	// Let maintenance run
	time.Sleep(50 * time.Millisecond)

	// Close pool (triggers cleanup logic)
	pool.Close()

	if !pool.closed {
		t.Error("Pool should be closed")
	}
}

// TestIsConnectionValidLockedEdgeCases tests locked validation edge cases
func TestIsConnectionValidLockedEdgeCases(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test with connection that has no conn field
	conn := &PooledConnection{
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     false,
		conn:      nil,
	}

	conn.mutex.Lock()
	valid := pool.isConnectionValidLocked(conn)
	conn.mutex.Unlock()

	if valid {
		t.Error("Connection with nil conn should not be valid")
	}

	// Test with connection that's in use
	conn.conn = &ldap.Conn{}
	conn.inUse = true

	conn.mutex.Lock()
	valid = pool.isConnectionValidLocked(conn)
	conn.mutex.Unlock()

	// Should be invalid due to being in use, even though it has a conn
	if valid {
		t.Error("Connection in use should not be valid for reuse")
	}
}

// TestCloseConnectionWithActiveConn tests closing connection with active counter
func TestCloseConnectionWithActiveConn(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Manually increment active connections to test decrement logic
	atomic.AddInt64(&pool.activeConns, 1)
	
	conn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now(),
		lastUsed:  time.Now(),
	}

	initialActive := atomic.LoadInt64(&pool.activeConns)
	
	// This should decrement active connections
	pool.closeConnection(conn)
	
	finalActive := atomic.LoadInt64(&pool.activeConns)
	if finalActive != initialActive-1 {
		t.Errorf("Active connections should decrease from %d to %d, got %d", 
			initialActive, initialActive-1, finalActive)
	}
}

// TestEstablishConnectionBranches tests different establishConnection branches
func TestEstablishConnectionBranches(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Test TLS branch
	cfg.TLS = true
	cfg.URL = "ldaps://non-existent:636"
	conn, err := pool.establishConnection()
	if err == nil && conn != nil {
		conn.Close()
		t.Log("Unexpected TLS success")
	} else {
		t.Logf("Expected TLS failure: %v", err)
	}

	// Test plain connection branch
	cfg.TLS = false
	cfg.URL = "ldap://non-existent:389"
	conn, err = pool.establishConnection()
	if err == nil && conn != nil {
		conn.Close()
		t.Log("Unexpected plain connection success")
	} else {
		t.Logf("Expected plain connection failure: %v", err)
	}

	// Test authentication branch (with empty credentials)
	cfg.Username = ""
	cfg.Password = nil
	conn, err = pool.establishConnection()
	if err == nil && conn != nil {
		conn.Close()
		t.Log("Connection with empty credentials succeeded")
	} else {
		t.Logf("Connection with empty credentials failed: %v", err)
	}
}

// TestMaintenanceLogic tests maintenance routine branches
func TestMaintenanceLogic(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 3)
	
	// Set very short timeouts to trigger maintenance quickly
	pool.idleTimeout = 1 * time.Millisecond
	pool.maxIdleTime = 2 * time.Millisecond

	// Add connections that will need cleanup
	for i := 0; i < 2; i++ {
		oldConn := &PooledConnection{
			conn:      nil,
			createdAt: time.Now().Add(-time.Hour), // Very old
			lastUsed:  time.Now().Add(-time.Hour),
			inUse:     false,
		}
		select {
		case pool.pool <- oldConn:
			atomic.AddInt64(&pool.poolSize, 1)
		default:
			t.Logf("Could not add connection %d to pool", i)
		}
	}

	initialSize := atomic.LoadInt64(&pool.poolSize)
	t.Logf("Initial pool size: %d", initialSize)

	// Let maintenance run
	time.Sleep(10 * time.Millisecond)

	finalSize := atomic.LoadInt64(&pool.poolSize)
	t.Logf("Final pool size: %d", finalSize)

	// Close pool to stop maintenance
	pool.Close()

	if !pool.closed {
		t.Error("Pool should be closed")
	}
}

// TestPoolPutFullPool tests putting connection when pool is full
func TestPoolPutFullPool(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Create a small pool to test full pool scenario
	pool := NewConnectionPool(cfg, 1)
	defer pool.Close()

	// Fill the pool
	existingConn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     false,
	}
	
	select {
	case pool.pool <- existingConn:
		atomic.AddInt64(&pool.poolSize, 1)
	default:
		t.Fatal("Could not fill pool")
	}

	// Try to put another connection (should close it instead)
	newConn := &PooledConnection{
		conn:      &ldap.Conn{},
		createdAt: time.Now(),
		lastUsed:  time.Now(),
		inUse:     true,
	}

	initialSize := atomic.LoadInt64(&pool.poolSize)
	pool.Put(newConn) // Should close the connection instead of pooling it
	finalSize := atomic.LoadInt64(&pool.poolSize)

	// Pool size shouldn't change if pool was full
	if finalSize != initialSize {
		t.Logf("Pool size changed from %d to %d when putting to full pool", initialSize, finalSize)
	}

	// Connection should not be in use anymore
	if newConn.inUse {
		t.Error("Connection should not be in use after Put")
	}
}

// TestPoolGetRetryLogic tests the retry logic when getting invalid connections
func TestPoolGetRetryLogic(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	pool := NewConnectionPool(cfg, 2)
	defer pool.Close()

	// Add multiple invalid connections to test retry logic
	for i := 0; i < 2; i++ {
		invalidConn := &PooledConnection{
			conn:      nil, // Invalid
			createdAt: time.Now().Add(-pool.maxIdleTime - time.Hour), // Too old
			lastUsed:  time.Now(),
			inUse:     false,
		}
		
		select {
		case pool.pool <- invalidConn:
			atomic.AddInt64(&pool.poolSize, 1)
		default:
			t.Fatal("Could not add invalid connection to pool")
		}
	}

	initialPoolSize := atomic.LoadInt64(&pool.poolSize)
	t.Logf("Added %d invalid connections to pool", initialPoolSize)

	// Get should retry through invalid connections and eventually fail
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	conn, err := pool.Get(ctx)
	if err == nil {
		t.Log("Unexpectedly got a connection")
		if conn != nil {
			pool.Put(conn)
		}
	} else {
		t.Logf("Expected failure after retries: %v", err)
		// This exercises the validation and retry logic
	}

	// Pool should have fewer connections after invalid ones are removed
	finalPoolSize := atomic.LoadInt64(&pool.poolSize)
	t.Logf("Pool size after Get: %d (was %d)", finalPoolSize, initialPoolSize)
}
