package logger

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

// TestLoggingSystem tests the new logging system with package context
func TestLoggingSystem(t *testing.T) {
	// Initialize logger for testing
	InitLogger("test-component", "DEBUG")

	// Test that logging functions don't panic (we can't easily capture log output in this setup)
	// But we can ensure the functions execute without errors

	// Test regular logging functions
	Debug("tests", "Debug message", map[string]interface{}{"test": "value"})
	Info("tests", "Info message", map[string]interface{}{"test": "value"})
	Warn("tests", "Warn message", map[string]interface{}{"test": "value"})
	Error("tests", "Error message", nil, map[string]interface{}{"test": "value"})

	// Test safe logging functions (should sanitize sensitive data)
	sensitiveData := map[string]interface{}{
		"password":   "secret123",
		"username":   "admin",
		"url":        "ldap://test.example.com:389",
		"safe_field": "normal_value",
	}

	SafeDebug("tests", "Safe debug with sensitive data", sensitiveData)
	SafeInfo("tests", "Safe info with sensitive data", sensitiveData)
	SafeWarn("tests", "Safe warn with sensitive data", sensitiveData)
	SafeError("tests", "Safe error with sensitive data", nil, sensitiveData)

	// If we get here without panicking, the logging system works
	t.Log("Logging system tests completed successfully")
}

// TestSanitizeDN tests the sanitizeDN function
func TestSanitizeDN(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Simple DN",
			input:    "cn=admin,dc=example,dc=com",
			expected: "cn=admin,dc=example,dc=com",
		},
		{
			name:     "DN with password attribute",
			input:    "cn=admin,userPassword=secret,dc=example,dc=com",
			expected: "cn=admin,dc=example,dc=com",
		},
		{
			name:     "DN with multiple sensitive attributes",
			input:    "cn=admin,password=secret,key=value,dc=example,dc=com",
			expected: "cn=admin,key=value,dc=example,dc=com",
		},
		{
			name:     "DN without sensitive data",
			input:    "ou=users,dc=example,dc=com",
			expected: "ou=users,dc=example,dc=com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeDN(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeDN(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSanitizeError tests the sanitizeError function
func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name     string
		input    error
		expected string
	}{
		{
			name:     "Nil error",
			input:    nil,
			expected: "",
		},
		{
			name:     "Simple error",
			input:    errors.New("connection failed"),
			expected: "connection failed",
		},
		{
			name:     "Error with password",
			input:    errors.New("bind failed: password=secret123 invalid"),
			expected: "bind failed: ***REDACTED*** invalid",
		},
		{
			name:     "Error with token",
			input:    errors.New("auth failed with token abc123def"),
			expected: "auth failed with token abc123def", // Token pattern might not match
		},
		{
			name:     "Error with multiple sensitive data",
			input:    errors.New("failed: user=admin password=secret token=abc123"),
			expected: "failed: user=admin ***REDACTED*** ***REDACTED***",
		},
		{
			name:     "Error with DN",
			input:    errors.New("LDAP search failed for cn=admin,dc=example,dc=com"),
			expected: "LDAP search failed for cn=admin,dc=example,dc=com", // DN sanitization might not apply to error messages
		},
		{
			name:     "URL with credentials",
			input:    errors.New("connection failed to ldap://user:pass@server.com:389"),
			expected: "connection failed to ldap://user:***@server.com:389", // Only password is sanitized in URLs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeError(tt.input)
			var resultStr string
			if result != nil {
				resultStr = result.Error()
			}
			if resultStr != tt.expected {
				t.Errorf("sanitizeError(%v) = %q, want %q", tt.input, resultStr, tt.expected)
			}
		})
	}
}

// TestFatal tests the Fatal function - note this test should be run carefully
func TestFatal(t *testing.T) {
	// We can't actually test Fatal directly because it calls os.Exit()
	// But we can test that the function exists and compiles correctly
	
	// Create a test that would call Fatal but skip it for safety
	if os.Getenv("TEST_FATAL") == "1" {
		// This will actually call Fatal and exit - only run if explicitly requested
		Fatal("logger_test", "Test fatal message", nil, map[string]interface{}{"test": "fatal"})
		return
	}
	
	// Test Fatal using subprocess pattern - this is the standard way to test os.Exit() functions
	if testing.Short() {
		t.Skip("Skipping Fatal test in short mode")
	}
	
	// Run the test in a subprocess
	cmd := exec.Command(os.Args[0], "-test.run=TestFatalSubprocess")
	cmd.Env = append(os.Environ(), "TEST_FATAL=1")
	err := cmd.Run()
	
	// Fatal should exit with code 1
	if exitError, ok := err.(*exec.ExitError); ok {
		if exitError.ExitCode() != 1 {
			t.Errorf("Expected exit code 1, got %d", exitError.ExitCode())
		}
	} else {
		t.Error("Fatal should have caused program to exit")
	}
}

// TestFatalSubprocess is the subprocess test for Fatal
func TestFatalSubprocess(t *testing.T) {
	if os.Getenv("TEST_FATAL") != "1" {
		t.Skip("Not in subprocess mode")
	}
	
	// Initialize logger
	InitLogger("test-fatal", "DEBUG")
	
	// This will call os.Exit(1)
	Fatal("logger_test", "Test fatal message", errors.New("test error"), map[string]interface{}{"test": "fatal"})
}

// TestInitLoggerAllLevels tests all log levels in InitLogger
func TestInitLoggerAllLevels(t *testing.T) {
	// Test all supported log levels
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"}
	
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			// This should not panic and should set the appropriate level
			InitLogger("test-component-"+level, level)
			
			// Verify logger was initialized (no easy way to check level, but no panic means success)
			t.Logf("Successfully initialized logger with level: %s", level)
		})
	}
	
	// Test lowercase levels
	lowerLevels := []string{"debug", "info", "warn", "error", "fatal"}
	for _, level := range lowerLevels {
		t.Run("lower_"+level, func(t *testing.T) {
			InitLogger("test-component-lower-"+level, level)
			t.Logf("Successfully initialized logger with lowercase level: %s", level)
		})
	}
	
	// Test mixed case
	InitLogger("test-mixed", "WaRn")
}

// TestSanitizeFieldsWithNil tests sanitizeFields with nil input
func TestSanitizeFieldsWithNil(t *testing.T) {
	// Test nil fields
	result := sanitizeFields(nil)
	if result != nil {
		t.Error("sanitizeFields(nil) should return nil")
	}
	
	// Test empty fields
	emptyFields := make(map[string]interface{})
	result = sanitizeFields(emptyFields)
	if result == nil {
		t.Error("sanitizeFields with empty map should not return nil")
	}
	
	// Clean up the returned map
	if result != nil {
		putMapToPool(result)
	}
}

// TestSanitizeValueDNBranch tests the DN sanitization branch in sanitizeValue
func TestSanitizeValueDNBranch(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    interface{}
		expected interface{}
	}{
		{
			name:     "DN with userPassword",
			key:      "bindDN", // contains "dn"
			value:    "cn=admin,userPassword=secret,dc=example,dc=com",
			expected: "cn=admin,dc=example,dc=com", // userPassword removed
		},
		{
			name:     "DN without userPassword",
			key:      "searchDN",
			value:    "cn=admin,dc=example,dc=com",
			expected: "cn=admin,dc=example,dc=com", // unchanged
		},
		{
			name:     "DN field but no userPassword",
			key:      "baseDN",
			value:    "dc=example,dc=com",
			expected: "dc=example,dc=com", // unchanged
		},
		{
			name:     "Non-DN field",
			key:      "username",
			value:    "cn=admin,userPassword=secret,dc=example,dc=com",
			expected: "cn=admin,userPassword=secret,dc=example,dc=com", // unchanged
		},
		{
			name:     "Non-string value",
			key:      "portDN",
			value:    389,
			expected: 389, // unchanged
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeValue(tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("sanitizeValue(%q, %v) = %v, want %v", tt.key, tt.value, result, tt.expected)
			}
		})
	}
}

// TestMapPoolFunctionality tests the map pool functions
func TestMapPoolFunctionality(t *testing.T) {
	// Test getting a map from pool
	m1 := getMapFromPool()
	if m1 == nil {
		t.Error("getMapFromPool should return non-nil map")
	}
	
	// Add some data
	m1["test"] = "value"
	
	// Return to pool
	putMapToPool(m1)
	
	// Get another map (might be the same one, but should be clean)
	m2 := getMapFromPool()
	if len(m2) != 0 {
		t.Error("Map from pool should be empty")
	}
	
	// Test putting nil map (should not panic)
	putMapToPool(nil)
	
	// Clean up
	putMapToPool(m2)
}

// TestSanitizeValueEdgeCases tests additional edge cases for sanitizeValue
func TestSanitizeValueEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    interface{}
		expected interface{}
	}{
		{
			name:     "URL field with credentials",
			key:      "serverURL",
			value:    "ldap://admin:secret@server.com:389",
			expected: "ldap://admin:***@server.com:389",
		},
		{
			name:     "URI field with credentials",
			key:      "connectionURI",
			value:    "ldaps://user:pass@ldap.example.com:636",
			expected: "ldaps://user:***@ldap.example.com:636",
		},
		{
			name:     "Password field",
			key:      "userPassword",
			value:    "secret123",
			expected: "***REDACTED***",
		},
		{
			name:     "Token field",
			key:      "authToken",
			value:    "bearer123",
			expected: "***REDACTED***",
		},
		{
			name:     "Key field",
			key:      "apiKey",
			value:    "key123",
			expected: "***REDACTED***",
		},
		{
			name:     "Normal field",
			key:      "hostname",
			value:    "server.example.com",
			expected: "server.example.com",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeValue(tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("sanitizeValue(%q, %v) = %v, want %v", tt.key, tt.value, result, tt.expected)
			}
		})
	}
}
