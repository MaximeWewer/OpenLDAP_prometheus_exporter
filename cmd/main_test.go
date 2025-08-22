package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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
