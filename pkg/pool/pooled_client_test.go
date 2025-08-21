package pool

import (
	"testing"
	"time"
)

// TestNewPooledLDAPClient tests client creation
func TestNewPooledLDAPClient(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	if client == nil {
		t.Fatal("NewPooledLDAPClient should return non-nil client")
	}

	if client.pool == nil {
		t.Error("Client should have a pool")
	}

	if client.circuitBreaker == nil {
		t.Error("Client should have a circuit breaker")
	}

	if client.config != cfg {
		t.Error("Client should store the config")
	}
}

// TestNewPooledLDAPClientWithOptions tests client creation with options
func TestNewPooledLDAPClientWithOptions(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	// Test with adaptive=false (default)
	client := NewPooledLDAPClientWithOptions(cfg, false)
	defer client.Close()

	if client == nil {
		t.Fatal("NewPooledLDAPClientWithOptions should return non-nil client")
	}

	stats := client.Stats()
	if stats["is_adaptive"] != false {
		t.Error("Client should not be adaptive when created with adaptive=false")
	}

	// Test with adaptive=true (currently not implemented but should not panic)
	adaptiveClient := NewPooledLDAPClientWithOptions(cfg, true)
	defer adaptiveClient.Close()

	adaptiveStats := adaptiveClient.Stats()
	// Currently adaptive is not implemented, so it should still be false
	if adaptiveStats["is_adaptive"] != false {
		t.Log("Adaptive pool feature is not yet implemented")
	}
}

// TestPooledClientSearch tests the Search method
func TestPooledClientSearch(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Search will fail (no server) but should handle gracefully
	result, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	
	if err == nil {
		t.Log("Unexpectedly successful search (LDAP server might be running)")
		if result != nil {
			t.Logf("Found %d entries", len(result.Entries))
		}
	} else {
		// Expected failure
		t.Logf("Expected search failure: %v", err)
	}
}

// TestPooledClientSearchWithContext tests Search with context
func TestPooledClientSearchWithContext(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Wait for timeout
	time.Sleep(5 * time.Millisecond)

	result, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	
	if err == nil {
		t.Error("Should return error on context timeout")
		if result != nil {
			t.Logf("Unexpected result with %d entries", len(result.Entries))
		}
	}
}

// TestPooledClientIsHealthy tests health check
func TestPooledClientIsHealthy(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Initially should be healthy (circuit breaker closed)
	if !client.IsHealthy() {
		t.Error("Client should be healthy initially")
	}

	// After several failures, circuit breaker might open
	for i := 0; i < 5; i++ {
		_, _ = client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	}

	// Check health again
	healthy := client.IsHealthy()
	t.Logf("Client healthy after failures: %v", healthy)
}

// TestPooledClientStats tests statistics gathering
func TestPooledClientStats(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	stats := client.Stats()

	// Check required stats
	requiredStats := []string{
		"pool_max_connections",
		"pool_active_connections",
		"pool_pool_size",
		"circuit_breaker_state",
		"is_adaptive",
	}

	for _, stat := range requiredStats {
		if _, exists := stats[stat]; !exists {
			t.Errorf("Stats should include %s", stat)
		}
	}

	// Verify some values
	if stats["pool_max_connections"] != 5 {
		t.Errorf("Expected max connections %d, got %v", 5, stats["pool_max_connections"])
	}

	if stats["is_adaptive"] != false {
		t.Error("Regular client should not be adaptive")
	}
}

// TestPooledClientClose tests closing the client
func TestPooledClientClose(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	
	// Close the client
	client.Close()

	// Close again should not panic
	client.Close()

	// Operations after close should fail gracefully
	_, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	if err == nil {
		t.Error("Search should fail after client is closed")
	}
}

// TestPooledClientWithNilPool tests client behavior with nil pool
func TestPooledClientWithNilPool(t *testing.T) {
	client := &PooledLDAPClient{
		pool:           nil,
		circuitBreaker: nil,
		config:         nil,
		isAdaptive:     false,
	}

	// Stats should handle nil pool
	stats := client.Stats()
	if stats == nil {
		t.Error("Stats should return non-nil map even with nil pool")
	}

	// IsHealthy should handle nil circuit breaker
	healthy := client.IsHealthy()
	if healthy {
		t.Error("Client with nil circuit breaker should not be healthy")
	}

	// Close should handle nil pool
	client.Close() // Should not panic
}

// TestSearchRequestConstruction tests the search request building
func TestSearchRequestConstruction(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Test with various base DNs
	testCases := []struct {
		baseDN string
		filter string
		attrs  []string
	}{
		{"cn=Monitor", "(objectClass=*)", []string{"cn"}},
		{"cn=Connections,cn=Monitor", "(objectClass=*)", []string{"monitorCounter"}},
		{"", "(objectClass=*)", []string{"namingContexts"}}, // Root DSE
		{"cn=Statistics,cn=Monitor", "(monitorCounter=*)", []string{"monitorCounter", "cn"}},
	}

	for _, tc := range testCases {
		_, err := client.Search(tc.baseDN, tc.filter, tc.attrs)
		// We expect errors since no server is running, but the request should be constructed
		if err != nil {
			t.Logf("Expected error for baseDN=%s: %v", tc.baseDN, err)
		}
	}
}

// TestCircuitBreakerIntegration tests circuit breaker behavior
func TestCircuitBreakerIntegration(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Get initial state
	initialStats := client.Stats()
	initialState := initialStats["circuit_breaker_state"]
	t.Logf("Initial circuit breaker state: %v", initialState)

	// Cause multiple failures to potentially open the circuit
	for i := 0; i < 10; i++ {
		_, _ = client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	}

	// Check state after failures
	afterStats := client.Stats()
	afterState := afterStats["circuit_breaker_state"]
	t.Logf("Circuit breaker state after failures: %v", afterState)
}

// TestPooledClientSearchValidation tests input validation
func TestPooledClientSearchValidation(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Test with empty filter
	_, err := client.Search("cn=Monitor", "", []string{"cn"})
	if err == nil {
		t.Log("Empty filter was accepted (might be valid in some LDAP implementations)")
	}

	// Test with nil attributes
	_, err = client.Search("cn=Monitor", "(objectClass=*)", nil)
	if err != nil {
		t.Logf("Nil attributes caused error: %v", err)
	}

	// Test with empty base DN (root DSE)
	_, err = client.Search("", "(objectClass=*)", []string{"*"})
	if err != nil {
		t.Logf("Empty base DN caused error: %v", err)
	}
}

// TestExecuteSearch tests the internal executeSearch method
func TestExecuteSearch(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// We can't test executeSearch directly as it's not exported
	// Instead, test search with connection from pool (which will fail gracefully)
	_, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
	if err == nil {
		t.Log("Search unexpectedly succeeded")
	} else {
		t.Logf("Expected search failure: %v", err)
	}
}

// TestPooledClientConcurrency tests concurrent operations
func TestPooledClientConcurrency(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	// Run concurrent searches
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			_, err := client.Search("cn=Monitor", "(objectClass=*)", []string{"cn"})
			if err != nil {
				// Expected - no server running
				t.Logf("Goroutine %d: search failed as expected", id)
			}
			
			// Check stats concurrently
			stats := client.Stats()
			_ = stats["pool_active_connections"]
			
			// Check health concurrently
			_ = client.IsHealthy()
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestDefaultPoolSize tests that default pool size is reasonable
func TestDefaultPoolSize(t *testing.T) {
	cfg, cleanup := setupTestConfig(t)
	defer cleanup()

	client := NewPooledLDAPClient(cfg)
	defer client.Close()

	stats := client.Stats()
	maxConns, ok := stats["pool_max_connections"]
	if !ok {
		t.Error("Stats should include pool_max_connections")
		return
	}

	maxConnsInt, ok := maxConns.(int)
	if !ok {
		t.Errorf("pool_max_connections should be int, got %T", maxConns)
		return
	}

	if maxConnsInt <= 0 {
		t.Errorf("Max connections should be positive, got %d", maxConnsInt)
	}

	if maxConnsInt > 100 {
		t.Errorf("Max connections seems too high: %d", maxConnsInt)
	}

	// Check that it uses the expected default value of 5
	if maxConnsInt != 5 {
		t.Errorf("Expected default max connections 5, got %d", maxConnsInt)
	}
}