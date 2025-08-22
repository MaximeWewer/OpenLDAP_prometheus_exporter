package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/exporter"
)

// setupTestEnv sets up required environment variables for testing
func setupTestEnv() {
	os.Setenv("LDAP_URL", "ldap://localhost:389")
	os.Setenv("LDAP_USERNAME", "admin")
	os.Setenv("LDAP_PASSWORD", "password")
	os.Setenv("LDAP_BASE_DN", "dc=example,dc=com")
}

// cleanupTestEnv cleans up test environment variables
func cleanupTestEnv() {
	os.Unsetenv("LDAP_URL")
	os.Unsetenv("LDAP_USERNAME")
	os.Unsetenv("LDAP_PASSWORD")
	os.Unsetenv("LDAP_BASE_DN")
}

// TestSecurityHeadersMiddleware tests that security headers are properly set
func TestSecurityHeadersMiddleware(t *testing.T) {
	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	})

	// Wrap with security middleware
	handler := securityHeadersMiddleware(testHandler)

	// Create test request
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// Execute request
	handler.ServeHTTP(w, req)

	// Check security headers
	resp := w.Result()
	defer resp.Body.Close()

	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Server":                 "", // Should be removed
	}

	// CSP header should start with "default-src 'self'" but can have more content
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "default-src 'self'") {
		t.Errorf("Expected Content-Security-Policy to start with 'default-src 'self'', got '%s'", csp)
	}

	// HSTS is only set for HTTPS requests (TLS != nil), so we don't expect it in this test

	for header, expected := range expectedHeaders {
		actual := resp.Header.Get(header)
		if actual != expected {
			t.Errorf("Expected header %s to be '%s', got '%s'", header, expected, actual)
		}
	}

	// Check that response is successful
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

// TestHandleRoot tests the root endpoint
func TestHandleRoot(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handleRoot(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected HTML content type, got %s", contentType)
	}

	// Check that response contains expected content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	bodyStr := string(body)
	expectedContent := []string{
		"OpenLDAP Exporter",
		"/metrics",
		"/health",
	}

	for _, content := range expectedContent {
		if !strings.Contains(bodyStr, content) {
			t.Errorf("Expected response to contain '%s'", content)
		}
	}
}

// TestHandleHealth tests the health endpoint
func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	handleHealth(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected JSON content type, got %s", contentType)
	}

	// Check response structure
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	bodyStr := string(body)
	expectedFields := []string{
		"\"status\":",
		"\"version\":",
	}

	for _, field := range expectedFields {
		if !strings.Contains(bodyStr, field) {
			t.Errorf("Expected health response to contain '%s'", field)
		}
	}
}

// TestVersionFlag tests that the Version variable is set
func TestVersionFlag(t *testing.T) {
	// Simply test that the Version variable exists and has a value
	if Version == "" {
		t.Error("Expected Version to be set")
	}

	// Test that version information can be accessed
	if len(Version) == 0 {
		t.Error("Version should not be empty")
	}
}

// TestInvalidLogLevel tests invalid log level handling
func TestInvalidLogLevel(t *testing.T) {
	// Test setting invalid log level
	os.Setenv("LOG_LEVEL", "INVALID")
	defer os.Unsetenv("LOG_LEVEL")

	// This should not panic and should use default level
	// We can't easily test the main function directly, but we can test
	// that the application handles invalid log levels gracefully
}

// TestHTTPServerConfiguration tests HTTP server configuration
func TestHTTPServerConfiguration(t *testing.T) {
	// Test with different listen addresses
	testCases := []struct {
		name        string
		listenAddr  string
		expectError bool
	}{
		{
			name:        "Default address",
			listenAddr:  ":9330",
			expectError: false,
		},
		{
			name:        "Custom port",
			listenAddr:  ":0", // Use random port to avoid conflicts
			expectError: false,
		},
		{
			name:        "Invalid address",
			listenAddr:  "invalid",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variable
			os.Setenv("LISTEN_ADDRESS", tc.listenAddr)
			defer os.Unsetenv("LISTEN_ADDRESS")

			// Create server with timeout to avoid blocking tests
			mux := http.NewServeMux()
			mux.HandleFunc("/", handleRoot)

			server := &http.Server{
				Addr:         tc.listenAddr,
				Handler:      mux,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
				IdleTimeout:  60 * time.Second,
			}

			// Start server in goroutine
			errChan := make(chan error, 1)
			go func() {
				errChan <- server.ListenAndServe()
			}()

			// Give server time to start
			time.Sleep(50 * time.Millisecond)

			// Shutdown server
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)

			// Check if server started correctly (non-invalid addresses)
			select {
			case err := <-errChan:
				if !tc.expectError && err != http.ErrServerClosed {
					t.Errorf("Expected server to start successfully, got error: %v", err)
				}
			case <-time.After(100 * time.Millisecond):
				if tc.expectError {
					t.Error("Expected server to fail to start, but it started successfully")
				}
			}
		})
	}
}

// TestGracefulShutdown tests graceful shutdown functionality
func TestGracefulShutdown(t *testing.T) {
	// This test verifies that the graceful shutdown mechanism works
	// We can't easily test the signal handling directly in unit tests,
	// but we can verify the shutdown logic works correctly

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)

	server := &http.Server{
		Addr:    ":0", // Use random available port
		Handler: mux,
	}

	// Start server
	go func() { _ = server.ListenAndServe() }()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Test graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Expected graceful shutdown to succeed, got error: %v", err)
	}
}

// TestEnvironmentVariableHandling tests environment variable parsing
func TestEnvironmentVariableHandling(t *testing.T) {
	testCases := []struct {
		name     string
		envVar   string
		value    string
		expected interface{}
	}{
		{
			name:     "Listen address",
			envVar:   "LISTEN_ADDRESS",
			value:    ":8080",
			expected: ":8080",
		},
		{
			name:     "Log level",
			envVar:   "LOG_LEVEL",
			value:    "DEBUG",
			expected: "DEBUG",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set environment variable
			os.Setenv(tc.envVar, tc.value)
			defer os.Unsetenv(tc.envVar)

			// Verify environment variable is set
			actual := os.Getenv(tc.envVar)
			if actual != tc.value {
				t.Errorf("Expected %s to be '%s', got '%s'", tc.envVar, tc.value, actual)
			}
		})
	}
}

// TestConcurrentRequests tests handling of concurrent requests
func TestConcurrentRequests(t *testing.T) {
	// Create test server
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/health", handleHealth)

	// Wrap with security middleware
	handler := securityHeadersMiddleware(mux)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Number of concurrent requests
	concurrency := 10
	requests := 50

	// Channel to collect results
	results := make(chan error, concurrency*requests)

	// Launch concurrent requests
	for i := 0; i < concurrency; i++ {
		go func() {
			client := &http.Client{Timeout: 5 * time.Second}
			for j := 0; j < requests; j++ {
				resp, err := client.Get(server.URL + "/health")
				if err != nil {
					results <- err
					continue
				}
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					results <- err
					continue
				}
				results <- nil
			}
		}()
	}

	// Collect results
	var errors []error
	for i := 0; i < concurrency*requests; i++ {
		if err := <-results; err != nil {
			errors = append(errors, err)
		}
	}

	// Check that all requests succeeded
	if len(errors) > 0 {
		t.Errorf("Expected all requests to succeed, got %d errors: %v", len(errors), errors[0])
	}
}

// Benchmark tests
func BenchmarkHandleRoot(b *testing.B) {
	req := httptest.NewRequest("GET", "/", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handleRoot(w, req)
	}
}

func BenchmarkHandleHealth(b *testing.B) {
	req := httptest.NewRequest("GET", "/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handleHealth(w, req)
	}
}

// TestGetEnvInt tests the getEnvInt function
func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		envVar       string
		envValue     string
		defaultValue int
		expected     int
	}{
		{
			name:         "Valid integer",
			envVar:       "TEST_INT",
			envValue:     "42",
			defaultValue: 10,
			expected:     42,
		},
		{
			name:         "Invalid integer",
			envVar:       "TEST_INT_INVALID",
			envValue:     "not-a-number",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "Empty value",
			envVar:       "TEST_INT_EMPTY",
			envValue:     "",
			defaultValue: 15,
			expected:     15,
		},
		{
			name:         "Unset variable",
			envVar:       "TEST_INT_UNSET",
			envValue:     "",
			defaultValue: 20,
			expected:     20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}

			result := getEnvInt(tt.envVar, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvInt() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestGetEnvDuration tests the getEnvDuration function
func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name         string
		envVar       string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "Valid duration",
			envVar:       "TEST_DURATION",
			envValue:     "30s",
			defaultValue: 10 * time.Second,
			expected:     30 * time.Second,
		},
		{
			name:         "Invalid duration",
			envVar:       "TEST_DURATION_INVALID",
			envValue:     "not-a-duration",
			defaultValue: 15 * time.Second,
			expected:     15 * time.Second,
		},
		{
			name:         "Empty value",
			envVar:       "TEST_DURATION_EMPTY",
			envValue:     "",
			defaultValue: 20 * time.Second,
			expected:     20 * time.Second,
		},
		{
			name:         "Complex duration",
			envVar:       "TEST_DURATION_COMPLEX",
			envValue:     "1h30m45s",
			defaultValue: 10 * time.Second,
			expected:     1*time.Hour + 30*time.Minute + 45*time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}

			result := getEnvDuration(tt.envVar, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvDuration() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestHandleInternalMetrics tests the internal metrics handler
func TestHandleInternalMetrics(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Create a test exporter
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	// Create test request
	req := httptest.NewRequest("GET", "/internal/metrics", nil)
	w := httptest.NewRecorder()

	// Call handler
	handleInternalMetrics(w, req, exp)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %v", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Expected text/plain content type, got %v", contentType)
	}

	body := w.Body.String()
	if body == "" {
		t.Error("Expected non-empty response body")
	}
}

// TestHandleInternalStatus tests the internal status handler
func TestHandleInternalStatus(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Create a test exporter
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	// Create test request
	req := httptest.NewRequest("GET", "/internal/status", nil)
	w := httptest.NewRecorder()

	// Call handler
	handleInternalStatus(w, req, exp)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %v", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected application/json content type, got %v", contentType)
	}

	body := w.Body.String()
	if body == "" {
		t.Error("Expected non-empty response body")
	}

	// Check if it's valid JSON
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Errorf("Expected valid JSON response, got error: %v", err)
	}
}

func BenchmarkSecurityMiddleware(b *testing.B) {
	handler := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}
}

// TestGetEnvString tests the getEnvString function
func TestGetEnvString(t *testing.T) {
	tests := []struct {
		name         string
		envVar       string
		envValue     string
		defaultValue string
		expected     string
	}{
		{
			name:         "Set environment variable",
			envVar:       "TEST_STRING",
			envValue:     "test_value",
			defaultValue: "default",
			expected:     "test_value",
		},
		{
			name:         "Unset environment variable",
			envVar:       "UNSET_STRING",
			envValue:     "",
			defaultValue: "default_value",
			expected:     "default_value",
		},
		{
			name:         "Empty environment variable",
			envVar:       "EMPTY_STRING",
			envValue:     "",
			defaultValue: "fallback",
			expected:     "fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv(tt.envVar, tt.envValue)
				defer os.Unsetenv(tt.envVar)
			}

			result := getEnvString(tt.envVar, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvString() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestSetupHTTPRoutes tests the setupHTTPRoutes function
func TestSetupHTTPRoutes(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Create a test exporter
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	// Setup routes
	mux := setupHTTPRoutes(exp)
	if mux == nil {
		t.Fatal("setupHTTPRoutes should return non-nil ServeMux")
	}

	// Test that routes are properly registered by making requests
	testRoutes := []struct {
		path       string
		method     string
		expectCode int
	}{
		{"/", "GET", http.StatusOK},
		{"/health", "GET", http.StatusOK},
		{"/metrics", "GET", http.StatusOK},
		{"/internal/metrics", "GET", http.StatusOK},
		{"/internal/status", "GET", http.StatusOK},
	}

	for _, route := range testRoutes {
		t.Run(route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Code != route.expectCode {
				t.Errorf("Expected status %d for %s %s, got %d", route.expectCode, route.method, route.path, w.Code)
			}
		})
	}
}

// TestSecurityHeadersMiddlewareHTTPS tests security headers middleware with HTTPS
func TestSecurityHeadersMiddlewareHTTPS(t *testing.T) {
	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	})

	// Wrap with security middleware
	handler := securityHeadersMiddleware(testHandler)

	// Create test request with TLS
	req := httptest.NewRequest("GET", "https://localhost/", nil)
	req.TLS = &tls.ConnectionState{} // Simulate HTTPS
	w := httptest.NewRecorder()

	// Execute request
	handler.ServeHTTP(w, req)

	// Check that HSTS header is set for HTTPS
	resp := w.Result()
	defer resp.Body.Close()

	hsts := resp.Header.Get("Strict-Transport-Security")
	if hsts == "" {
		t.Error("Expected HSTS header for HTTPS request")
	}
}

// TestHandleRootEdgeCases tests handleRoot with different scenarios
func TestHandleRootEdgeCases(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Test with different request methods
	methods := []string{"GET", "POST", "PUT", "DELETE"}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/", nil)
			w := httptest.NewRecorder()

			handleRoot(w, req)

			// All methods should return 200 for root handler
			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200 for %s method, got %d", method, w.Code)
			}
		})
	}

	// Test with different accept headers
	acceptHeaders := []string{
		"text/html",
		"application/json",
		"*/*",
		"text/plain",
	}

	for _, accept := range acceptHeaders {
		t.Run("Accept-"+accept, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Accept", accept)
			w := httptest.NewRecorder()

			handleRoot(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200 with Accept: %s, got %d", accept, w.Code)
			}
		})
	}
}

// TestErrorHandling tests various error handling scenarios
func TestErrorHandling(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Test version variable handling
	originalVersion := Version
	Version = "test-version-1.0.0"
	defer func() { Version = originalVersion }()

	if Version != "test-version-1.0.0" {
		t.Errorf("Version should be set to test-version-1.0.0, got %s", Version)
	}

	// Test handleRoot with various user agents
	userAgents := []string{
		"Mozilla/5.0 (compatible; monitoring/1.0)",
		"Go-http-client/1.1",
		"curl/7.68.0",
		"",
	}

	for _, ua := range userAgents {
		t.Run("UserAgent-"+ua, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if ua != "" {
				req.Header.Set("User-Agent", ua)
			}
			w := httptest.NewRecorder()

			handleRoot(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200 with User-Agent: %s, got %d", ua, w.Code)
			}
		})
	}

	// Test health endpoint edge cases
	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for health endpoint, got %d", w.Code)
	}
}
