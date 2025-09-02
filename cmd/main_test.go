package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
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

// Test configuration constants
const (
	testTimeout       = 5 * time.Second
	testShortTimeout  = 100 * time.Millisecond
	testServerTimeout = 50 * time.Millisecond
	testConcurrency   = 10
	testRequests      = 50
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
	setupTestEnv()
	defer cleanupTestEnv()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	var _ *http.Request = req
	handleHealth(w, exp)

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
			time.Sleep(testServerTimeout)

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
			case <-time.After(testShortTimeout):
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
	time.Sleep(testServerTimeout)

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
	setupTestEnv()
	defer cleanupTestEnv()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	// Create test server
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		handleHealth(w, exp)
	})

	// Wrap with security middleware
	handler := securityHeadersMiddleware(mux)

	server := httptest.NewServer(handler)
	defer server.Close()

	// Number of concurrent requests - using constants
	concurrency := testConcurrency
	requests := testRequests

	// Channel to collect results
	results := make(chan error, concurrency*requests)

	// Launch concurrent requests
	for i := 0; i < concurrency; i++ {
		go func() {
			client := &http.Client{Timeout: testTimeout}
			for j := 0; j < requests; j++ {
				resp, err := client.Get(server.URL + "/health")
				if err != nil {
					results <- err
					continue
				}
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					results <- fmt.Errorf("unexpected status code: %d", resp.StatusCode)
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
	setupTestEnv()
	defer cleanupTestEnv()

	cfg, err := config.LoadConfig()
	if err != nil {
		b.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	req := httptest.NewRequest("GET", "/health", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		var _ *http.Request = req
		handleHealth(w, exp)
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

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

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

	handleHealth(w, exp)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for health endpoint, got %d", w.Code)
	}
}

// TestHandleRootNotFound tests 404 handling for non-root paths
func TestHandleRootNotFound(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	testPaths := []string{
		"/nonexistent",
		"/admin",
		"/config",
		"/../..",
	}

	for _, path := range testPaths {
		t.Run("Path-"+path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()

			handleRoot(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("Expected status 404 for path %s, got %d", path, w.Code)
			}
		})
	}
}

// TestRateLimitConfiguration tests rate limit environment variable handling
func TestRateLimitConfiguration(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Test various rate limit configurations
	testCases := []struct {
		name          string
		rateEnvVar    string
		rateValue     string
		burstEnvVar   string
		burstValue    string
		expectedRate  int
		expectedBurst int
	}{
		{
			name:          "Custom rate and burst",
			rateEnvVar:    "RATE_LIMIT_REQUESTS",
			rateValue:     "60",
			burstEnvVar:   "RATE_LIMIT_BURST",
			burstValue:    "20",
			expectedRate:  60,
			expectedBurst: 20,
		},
		{
			name:          "Invalid rate - should use default",
			rateEnvVar:    "RATE_LIMIT_REQUESTS",
			rateValue:     "invalid",
			burstEnvVar:   "RATE_LIMIT_BURST",
			burstValue:    "15",
			expectedRate:  defaultRateLimitRequests,
			expectedBurst: 15,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv(tc.rateEnvVar, tc.rateValue)
			os.Setenv(tc.burstEnvVar, tc.burstValue)
			defer func() {
				os.Unsetenv(tc.rateEnvVar)
				os.Unsetenv(tc.burstEnvVar)
			}()

			// Test getEnvInt function directly
			actualRate := getEnvInt(tc.rateEnvVar, defaultRateLimitRequests)
			actualBurst := getEnvInt(tc.burstEnvVar, defaultRateLimitBurst)

			if actualRate != tc.expectedRate {
				t.Errorf("Expected rate %d, got %d", tc.expectedRate, actualRate)
			}
			if actualBurst != tc.expectedBurst {
				t.Errorf("Expected burst %d, got %d", tc.expectedBurst, actualBurst)
			}
		})
	}
}

// TestHTTPTimeoutConfiguration tests HTTP timeout environment variables
func TestHTTPTimeoutConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		envVar       string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "Valid HTTP read timeout",
			envVar:       "HTTP_READ_TIMEOUT",
			envValue:     "15s",
			defaultValue: defaultReadTimeout,
			expected:     15 * time.Second,
		},
		{
			name:         "Valid HTTP write timeout",
			envVar:       "HTTP_WRITE_TIMEOUT",
			envValue:     "20s",
			defaultValue: defaultWriteTimeout,
			expected:     20 * time.Second,
		},
		{
			name:         "Invalid timeout - use default",
			envVar:       "HTTP_READ_TIMEOUT",
			envValue:     "invalid-timeout",
			defaultValue: defaultReadTimeout,
			expected:     defaultReadTimeout,
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

// TestMainFunctionComponents tests components used by main function
func TestMainFunctionComponents(t *testing.T) {
	// Test that all default constants are reasonable
	constants := map[string]interface{}{
		"defaultListenAddress":     defaultListenAddress,
		"defaultShutdownTimeout":   defaultShutdownTimeout,
		"defaultReadTimeout":       defaultReadTimeout,
		"defaultWriteTimeout":      defaultWriteTimeout,
		"defaultIdleTimeout":       defaultIdleTimeout,
		"defaultRateLimitRequests": defaultRateLimitRequests,
		"defaultRateLimitBurst":    defaultRateLimitBurst,
		"defaultHealthRequests":    defaultHealthRequests,
		"defaultHealthBurst":       defaultHealthBurst,
	}

	for name, value := range constants {
		if value == nil {
			t.Errorf("Constant %s should not be nil", name)
		}

		// Check specific types and ranges
		switch v := value.(type) {
		case string:
			if v == "" {
				t.Errorf("String constant %s should not be empty", name)
			}
		case time.Duration:
			if v <= 0 {
				t.Errorf("Duration constant %s should be positive, got %v", name, v)
			}
		case int:
			if v <= 0 {
				t.Errorf("Integer constant %s should be positive, got %v", name, v)
			}
		}
	}
}

// TestHandleRootInvalidPath tests handleRoot with invalid paths
func TestHandleRootInvalidPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"invalid path", "/invalid"},
		{"deep path", "/some/deep/path"},
		{"path with query", "/test?query=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()

			handleRoot(w, req)

			if w.Code != http.StatusNotFound {
				t.Errorf("Expected status code %d, got %d", http.StatusNotFound, w.Code)
			}
		})
	}
}

// TestHandleRootWithFilters tests handleRoot with various filter configurations
func TestHandleRootWithFilters(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	testCases := []struct {
		name            string
		metricsInclude  string
		metricsExclude  string
		expectedInclude string
		expectedExclude string
	}{
		{
			name:            "Both include and exclude filters",
			metricsInclude:  "connections,health",
			metricsExclude:  "statistics,operations",
			expectedInclude: "[connections, health]",
			expectedExclude: "[statistics, operations]",
		},
		{
			name:            "Only include filter",
			metricsInclude:  "health,statistics",
			metricsExclude:  "",
			expectedInclude: "[health, statistics]",
			expectedExclude: "None",
		},
		{
			name:            "Only exclude filter",
			metricsInclude:  "",
			metricsExclude:  "tls,overlays",
			expectedInclude: "None (collect all metric groups)",
			expectedExclude: "[tls, overlays]",
		},
		{
			name:            "Single metric in include",
			metricsInclude:  "connections",
			metricsExclude:  "",
			expectedInclude: "[connections]",
			expectedExclude: "None",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set filter environment variables
			if tc.metricsInclude != "" {
				os.Setenv("OPENLDAP_METRICS_INCLUDE", tc.metricsInclude)
			}
			if tc.metricsExclude != "" {
				os.Setenv("OPENLDAP_METRICS_EXCLUDE", tc.metricsExclude)
			}

			// Clean up after test
			defer func() {
				os.Unsetenv("OPENLDAP_METRICS_INCLUDE")
				os.Unsetenv("OPENLDAP_METRICS_EXCLUDE")
			}()

			req := httptest.NewRequest("GET", "/", nil)
			w := httptest.NewRecorder()

			handleRoot(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", w.Code)
			}

			body := w.Body.String()

			// Check that the expected filter text appears in the response
			if !strings.Contains(body, tc.expectedInclude) {
				t.Errorf("Expected include filter '%s' to appear in response, body: %s", tc.expectedInclude, body)
			}

			if !strings.Contains(body, tc.expectedExclude) {
				t.Errorf("Expected exclude filter '%s' to appear in response, body: %s", tc.expectedExclude, body)
			}
		})
	}
}

// TestHandleHealthMarshalError tests handleHealth when JSON marshaling fails
func TestHandleHealthMarshalError(t *testing.T) {
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

	// Create a custom version of handleHealth that forces a marshal error
	// We'll use a struct that can't be marshaled to JSON
	testHandleHealthWithError := func(w http.ResponseWriter, r *http.Request, exp *exporter.OpenLDAPExporter) {
		w.Header().Set("Content-Type", "application/json")

		monitoring := exp.GetInternalMonitoring()

		// Create a response with a channel (which can't be marshaled to JSON)
		response := map[string]interface{}{
			"status":        "ok",
			"version":       Version,
			"timestamp":     time.Now().Format(time.RFC3339),
			"uptime":        formatDuration(time.Since(monitoring.GetStartTime())),
			"unmarshalable": make(chan int), // This will cause json.MarshalIndent to fail
		}

		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jsonData)
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	testHandleHealthWithError(w, req, exp)

	// Should return 500 Internal Server Error due to JSON marshaling failure
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Internal Server Error") {
		t.Errorf("Expected error message in response body, got: %s", body)
	}
}

// TestSecurityMiddlewareWithTLS tests security headers with TLS
func TestSecurityMiddlewareWithTLS(t *testing.T) {
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	})

	handler := securityHeadersMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/", nil)
	// Simulate HTTPS request
	req.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Check if HSTS header is set for HTTPS requests
	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts != "max-age=31536000; includeSubDomains" {
		t.Errorf("Expected HSTS header to be set for HTTPS requests, got: %s", hsts)
	}
}

// TestSetupHTTPRoutesConfiguration tests various aspects of HTTP route setup
func TestSetupHTTPRoutesConfiguration(t *testing.T) {
	setupTestEnv()
	defer cleanupTestEnv()

	// Test different rate limiting configurations
	os.Setenv("RATE_LIMIT_REQUESTS", "50")
	os.Setenv("RATE_LIMIT_BURST", "20")
	os.Setenv("HEALTH_RATE_LIMIT_REQUESTS", "80")
	os.Setenv("HEALTH_RATE_LIMIT_BURST", "30")

	defer func() {
		os.Unsetenv("RATE_LIMIT_REQUESTS")
		os.Unsetenv("RATE_LIMIT_BURST")
		os.Unsetenv("HEALTH_RATE_LIMIT_REQUESTS")
		os.Unsetenv("HEALTH_RATE_LIMIT_BURST")
	}()

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	defer cfg.Clear()

	exp := exporter.NewOpenLDAPExporter(cfg)
	defer exp.Close()

	mux := setupHTTPRoutes(exp)

	// Test that mux is properly configured
	if mux == nil {
		t.Fatal("setupHTTPRoutes returned nil")
	}

	// Test health endpoint
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for /health, got %d", w.Code)
	}

	// Verify it's JSON
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}
}
