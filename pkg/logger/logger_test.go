package logger

import (
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
