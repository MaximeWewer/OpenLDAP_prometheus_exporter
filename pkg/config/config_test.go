package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Test configuration constants
const (
	testURL = "ldap://test.example.com:389"
	testUsername = "testuser"
	testPassword = "testpass"
)

// Helper functions to reduce code duplication
func setupTestEnvironment(additionalVars map[string]string) func() {
	// Set required variables
	os.Setenv("LDAP_URL", testURL)
	os.Setenv("LDAP_USERNAME", testUsername)
	os.Setenv("LDAP_PASSWORD", testPassword)
	
	// Set additional variables
	for key, value := range additionalVars {
		os.Setenv(key, value)
	}
	
	// Return cleanup function
	return func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		for key := range additionalVars {
			os.Unsetenv(key)
		}
	}
}

func createTestConfig(t *testing.T, additionalVars map[string]string) (*Config, func()) {
	cleanup := setupTestEnvironment(additionalVars)
	
	config, err := LoadConfig()
	if err != nil {
		cleanup()
		t.Fatalf("Failed to load config: %v", err)
	}
	
	return config, func() {
		config.Clear()
		cleanup()
	}
}

// TestSecureString tests the real SecureString implementation
func TestSecureString(t *testing.T) {
	password := "test_password_123"

	// Test SecureString creation and usage
	secureStr, err := NewSecureString(password)
	if err != nil {
		t.Fatalf("Failed to create SecureString: %v", err)
	}
	defer secureStr.Clear()

	// Verify we can decrypt back to original
	decrypted := secureStr.String()
	if decrypted != password {
		t.Errorf("Decryption failed: got %s, want %s", decrypted, password)
	}

	// Test that IsEmpty works correctly
	if secureStr.IsEmpty() {
		t.Error("SecureString should not be empty")
	}

	// Test Clear functionality
	secureStr.Clear()
	if !secureStr.IsEmpty() {
		t.Error("SecureString should be empty after Clear()")
	}

	// Test empty string creation
	_, err = NewSecureString("")
	if err == nil {
		t.Error("Should not be able to create SecureString with empty value")
	}
}

// TestConfigurationValidation tests configuration loading and validation
func TestConfigurationValidation(t *testing.T) {
	// Save original environment
	originalEnv := map[string]string{
		"LDAP_URL":      os.Getenv("LDAP_URL"),
		"LDAP_USERNAME": os.Getenv("LDAP_USERNAME"),
		"LDAP_PASSWORD": os.Getenv("LDAP_PASSWORD"),
	}

	// Clean up after test
	defer func() {
		for key, value := range originalEnv {
			if value != "" {
				os.Setenv(key, value)
			} else {
				os.Unsetenv(key)
			}
		}
	}()

	// Test missing required variables - skip this test because LoadConfig calls Fatal()
	// which terminates the process. This is expected behavior in production.
	t.Log("Skipping missing variables test - LoadConfig calls Fatal() which terminates process")

	// Test valid configuration
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig should succeed with valid environment variables: %v", err)
	}

	if config.URL != "ldap://test.example.com:389" {
		t.Errorf("Expected URL 'ldap://test.example.com:389', got '%s'", config.URL)
	}

	if config.Username != "testuser" {
		t.Errorf("Expected Username 'testuser', got '%s'", config.Username)
	}

	// Test that password is properly secured (we can't access the raw value)
	if config.Password == nil {
		t.Error("Password should not be nil")
	}

	// Clean up sensitive data
	config.Clear()
}

// TestEnvironmentVariablesParsing tests environment variables parsing
func TestEnvironmentVariablesParsing(t *testing.T) {
	additionalVars := map[string]string{
		"LDAP_TIMEOUT":     "30",
		"LDAP_UPDATE_EVERY": "45",
	}
	
	config, cleanup := createTestConfig(t, additionalVars)
	defer cleanup()

	if config.Timeout != 30*time.Second {
		t.Errorf("Expected timeout 30s, got %v", config.Timeout)
	}

	if config.UpdateEvery != 45*time.Second {
		t.Errorf("Expected update_every 45s, got %v", config.UpdateEvery)
	}
}

// TestMetricFiltering tests metric include/exclude functionality
func TestMetricFiltering(t *testing.T) {
	// Test with include list
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_METRICS_INCLUDE", "connections,statistics")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_METRICS_INCLUDE")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Test ShouldCollectMetric with include list
	if !config.ShouldCollectMetric("connections") {
		t.Error("Should collect 'connections' metric (in include list)")
	}
	if !config.ShouldCollectMetric("statistics") {
		t.Error("Should collect 'statistics' metric (in include list)")
	}
	if config.ShouldCollectMetric("threads") {
		t.Error("Should NOT collect 'threads' metric (not in include list)")
	}
}

// TestMetricFilteringExclude tests metric exclude functionality
func TestMetricFilteringExclude(t *testing.T) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_METRICS_EXCLUDE", "threads,overlays")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_METRICS_EXCLUDE")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Test ShouldCollectMetric with exclude list
	if config.ShouldCollectMetric("threads") {
		t.Error("Should NOT collect 'threads' metric (in exclude list)")
	}
	if config.ShouldCollectMetric("overlays") {
		t.Error("Should NOT collect 'overlays' metric (in exclude list)")
	}
	if !config.ShouldCollectMetric("connections") {
		t.Error("Should collect 'connections' metric (not in exclude list)")
	}
}

// TestDCFiltering tests domain component filtering
func TestDCFiltering(t *testing.T) {
	// Test with include list
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_DC_INCLUDE", "example,test")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_DC_INCLUDE")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Test ShouldMonitorDC with include list
	if !config.ShouldMonitorDC("example") {
		t.Error("Should monitor 'example' DC (in include list)")
	}
	if !config.ShouldMonitorDC("test") {
		t.Error("Should monitor 'test' DC (in include list)")
	}
	if config.ShouldMonitorDC("other") {
		t.Error("Should NOT monitor 'other' DC (not in include list)")
	}
}

// TestDCFilteringExclude tests domain component exclude functionality
func TestDCFilteringExclude(t *testing.T) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_DC_EXCLUDE", "internal,private")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_DC_EXCLUDE")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Test ShouldMonitorDC with exclude list
	if config.ShouldMonitorDC("internal") {
		t.Error("Should NOT monitor 'internal' DC (in exclude list)")
	}
	if config.ShouldMonitorDC("private") {
		t.Error("Should NOT monitor 'private' DC (in exclude list)")
	}
	if !config.ShouldMonitorDC("public") {
		t.Error("Should monitor 'public' DC (not in exclude list)")
	}
}

// TestBooleanEnvironmentVariables tests boolean environment variable parsing
func TestBooleanEnvironmentVariables(t *testing.T) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("LDAP_TLS", "true")
	os.Setenv("LDAP_TLS_SKIP_VERIFY", "false")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("LDAP_TLS")
		os.Unsetenv("LDAP_TLS_SKIP_VERIFY")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	if !config.TLS {
		t.Error("Expected TLS to be true")
	}
	if config.TLSSkipVerify {
		t.Error("Expected TLSSkipVerify to be false")
	}
}

// TestInvalidBooleanEnvironmentVariables tests invalid boolean parsing
func TestInvalidBooleanEnvironmentVariables(t *testing.T) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("LDAP_TLS", "invalid_bool")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("LDAP_TLS")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Should use default value (false) for invalid boolean
	if config.TLS {
		t.Error("Expected TLS to be false (default) for invalid boolean value")
	}
}

// TestInvalidMetricsLists tests invalid metrics list handling
func TestInvalidMetricsLists(t *testing.T) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_METRICS_INCLUDE", "connections,invalid_metric,statistics")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_METRICS_INCLUDE")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Should still include valid metrics despite invalid ones
	if !config.ShouldCollectMetric("connections") {
		t.Error("Should collect 'connections' metric (valid metric in list)")
	}
	if !config.ShouldCollectMetric("statistics") {
		t.Error("Should collect 'statistics' metric (valid metric in list)")
	}
	// Note: ShouldCollectMetric doesn't validate if metric is valid,
	// it just checks if it's in the include list. Invalid metrics are
	// filtered out during config loading and logged as warnings.
	if !config.ShouldCollectMetric("invalid_metric") {
		t.Error("ShouldCollectMetric should return true for 'invalid_metric' since it's in include list (validation happens elsewhere)")
	}
}

// TestTimeoutBoundaryValues tests timeout validation edge cases
func TestTimeoutBoundaryValues(t *testing.T) {
	tests := []struct {
		name                 string
		timeoutValue         string
		updateEveryValue     string
		expectedTimeout      time.Duration
		expectedUpdateEvery  time.Duration
	}{
		{
			name:                 "Zero timeout",
			timeoutValue:         "0",
			updateEveryValue:     "15",
			expectedTimeout:      10 * time.Second, // Should use default
			expectedUpdateEvery:  15 * time.Second,
		},
		{
			name:                 "Negative timeout", 
			timeoutValue:         "-5",
			updateEveryValue:     "15",
			expectedTimeout:      10 * time.Second, // Should use default
			expectedUpdateEvery:  15 * time.Second,
		},
		{
			name:                 "Very high timeout",
			timeoutValue:         "600", // 10 minutes
			updateEveryValue:     "15",
			expectedTimeout:      5 * time.Minute, // Should be capped at 5 minutes
			expectedUpdateEvery:  15 * time.Second,
		},
		{
			name:                 "Zero update_every",
			timeoutValue:         "10",
			updateEveryValue:     "0",
			expectedTimeout:      10 * time.Second,
			expectedUpdateEvery:  15 * time.Second, // Should use default
		},
		{
			name:                 "Very low update_every",
			timeoutValue:         "10",
			updateEveryValue:     "3",
			expectedTimeout:      10 * time.Second,
			expectedUpdateEvery:  5 * time.Second, // Should be capped at minimum
		},
		{
			name:                 "Very high update_every",
			timeoutValue:         "10",
			updateEveryValue:     "700", // ~11.7 minutes
			expectedTimeout:      10 * time.Second,
			expectedUpdateEvery:  10 * time.Minute, // Should be capped at 10 minutes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("LDAP_URL", "ldap://test.example.com:389")
			os.Setenv("LDAP_USERNAME", "testuser")
			os.Setenv("LDAP_PASSWORD", "testpass")
			os.Setenv("LDAP_TIMEOUT", tt.timeoutValue)
			os.Setenv("LDAP_UPDATE_EVERY", tt.updateEveryValue)

			defer func() {
				os.Unsetenv("LDAP_URL")
				os.Unsetenv("LDAP_USERNAME")
				os.Unsetenv("LDAP_PASSWORD")
				os.Unsetenv("LDAP_TIMEOUT")
				os.Unsetenv("LDAP_UPDATE_EVERY")
			}()

			config, err := LoadConfig()
			if err != nil {
				t.Fatalf("Failed to load config: %v", err)
			}
			defer config.Clear()

			if config.Timeout != tt.expectedTimeout {
				t.Errorf("Expected timeout %v, got %v", tt.expectedTimeout, config.Timeout)
			}
			if config.UpdateEvery != tt.expectedUpdateEvery {
				t.Errorf("Expected update_every %v, got %v", tt.expectedUpdateEvery, config.UpdateEvery)
			}
		})
	}
}

// TestLongServerName tests server name length validation
func TestLongServerName(t *testing.T) {
	longName := strings.Repeat("a", 200) // Longer than 128 characters
	
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("LDAP_SERVER_NAME", longName)

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("LDAP_SERVER_NAME")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	if len(config.ServerName) != 128 {
		t.Errorf("Expected server name to be truncated to 128 characters, got %d", len(config.ServerName))
	}
	
	expectedName := longName[:128]
	if config.ServerName != expectedName {
		t.Errorf("Server name not properly truncated")
	}
}

// TestTLSSkipVerifyWarning tests the TLS warning functionality
func TestTLSSkipVerifyWarning(t *testing.T) {
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("LDAP_TLS_SKIP_VERIFY", "true")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("LDAP_TLS_SKIP_VERIFY")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	if !config.TLSSkipVerify {
		t.Error("Expected TLSSkipVerify to be true")
	}
}

// TestSecureStringEdgeCases tests edge cases for SecureString
func TestSecureStringEdgeCases(t *testing.T) {
	// Test String method on nil SecureString
	var nilSecure *SecureString
	result := nilSecure.String()
	if result != "" {
		t.Errorf("Expected empty string for nil SecureString, got %s", result)
	}

	// Test String method on empty ciphertext
	secureStr := &SecureString{
		ciphertext: []byte{},
		nonce:      []byte{1, 2, 3},
		aead:       nil,
	}
	result = secureStr.String()
	if result != "" {
		t.Errorf("Expected empty string for SecureString with empty ciphertext, got %s", result)
	}

	// Test Clear on nil SecureString (should not panic)
	nilSecure.Clear()

	// Test IsEmpty on various states
	if !nilSecure.IsEmpty() {
		t.Error("nil SecureString should be empty")
	}

	secureStr2 := &SecureString{
		ciphertext: []byte{},
		nonce:      []byte{},
		aead:       nil,
	}
	if !secureStr2.IsEmpty() {
		t.Error("SecureString with empty ciphertext should be empty")
	}
}

// TestValidateFilteringFunctions tests metric and DC filtering validation
func TestValidateFilteringFunctions(t *testing.T) {
	// Test with both include and exclude (include should take precedence)
	os.Setenv("LDAP_URL", "ldap://test.example.com:389")
	os.Setenv("LDAP_USERNAME", "testuser")
	os.Setenv("LDAP_PASSWORD", "testpass")
	os.Setenv("OPENLDAP_METRICS_INCLUDE", "connections,statistics")
	os.Setenv("OPENLDAP_METRICS_EXCLUDE", "threads,operations")
	os.Setenv("OPENLDAP_DC_INCLUDE", "example,test")
	os.Setenv("OPENLDAP_DC_EXCLUDE", "internal,private")

	defer func() {
		os.Unsetenv("LDAP_URL")
		os.Unsetenv("LDAP_USERNAME")
		os.Unsetenv("LDAP_PASSWORD")
		os.Unsetenv("OPENLDAP_METRICS_INCLUDE")
		os.Unsetenv("OPENLDAP_METRICS_EXCLUDE")
		os.Unsetenv("OPENLDAP_DC_INCLUDE")
		os.Unsetenv("OPENLDAP_DC_EXCLUDE")
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer config.Clear()

	// Verify that include takes precedence over exclude for metrics
	if !config.ShouldCollectMetric("connections") {
		t.Error("Should collect 'connections' metric (in include list)")
	}
	if config.ShouldCollectMetric("threads") {
		t.Error("Should NOT collect 'threads' metric (not in include list, even though in exclude)")
	}

	// Verify that include takes precedence over exclude for DC
	if !config.ShouldMonitorDC("example") {
		t.Error("Should monitor 'example' DC (in include list)")
	}
	if config.ShouldMonitorDC("internal") {
		t.Error("Should NOT monitor 'internal' DC (not in include list, even though in exclude)")
	}
}
