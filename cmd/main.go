package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/exporter"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/security"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultListenAddress     = ":9330"
	defaultShutdownTimeout   = 30 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultWriteTimeout      = 10 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultRateLimitRequests = 30
	defaultRateLimitBurst    = 10
	defaultHealthRequests    = 60
	defaultHealthBurst       = 20
)

// getEnvString retrieves a string value from environment variable or returns default
func getEnvString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt retrieves an integer value from environment variable or returns default
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
		logger.Warn("main", "Invalid integer value for environment variable", map[string]interface{}{
			"key":           key,
			"value":         value,
			"using_default": defaultValue,
		})
	}
	return defaultValue
}

// getEnvDuration retrieves a duration value from environment variable or returns default
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
		logger.Warn("main", "Invalid duration value for environment variable", map[string]interface{}{
			"key":           key,
			"value":         value,
			"using_default": defaultValue.String(),
		})
	}
	return defaultValue
}

var (
	listenAddr = flag.String("web.listen-address", getEnvString("LISTEN_ADDRESS", defaultListenAddress), "Address to listen on for web interface and telemetry. Can also be set via LISTEN_ADDRESS environment variable")
	version    = flag.Bool("version", false, "Print version information and exit")
	logLevel   = flag.String("log.level", getEnvString("LOG_LEVEL", "INFO"), "Log level (DEBUG, INFO, WARN, ERROR, FATAL). Can also be set via LOG_LEVEL environment variable")
)

var Version = "dev"

// main is the entry point of the OpenLDAP exporter application
func main() {
	flag.Parse()

	// Initialize with parsed log level
	logger.InitLogger("openldap-exporter", *logLevel)

	if *version {
		logger.Info("main", "Version requested", map[string]interface{}{"version": Version})
		os.Exit(0)
	}

	configData, err := config.LoadConfig()
	if err != nil {
		logger.Fatal("main", "Failed to load configuration", err)
	}
	defer configData.Clear()

	exp := exporter.NewOpenLDAPExporter(configData)
	defer exp.Close()
	prometheus.MustRegister(exp)

	mux := setupHTTPRoutes(exp)

	// Configure HTTP server timeouts from environment variables
	readTimeout := getEnvDuration("HTTP_READ_TIMEOUT", defaultReadTimeout)
	writeTimeout := getEnvDuration("HTTP_WRITE_TIMEOUT", defaultWriteTimeout)
	idleTimeout := getEnvDuration("HTTP_IDLE_TIMEOUT", defaultIdleTimeout)
	shutdownTimeout := getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)

	server := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	go func() {
		logger.SafeInfo("main", "Starting OpenLDAP exporter", map[string]interface{}{
			"listen_address": *listenAddr,
			"version":        Version,
		})
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("main", "Failed to start HTTP server", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	logger.Info("main", "Received shutdown signal, starting graceful shutdown")

	// Close the exporter first to clean up LDAP connections
	exp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("main", "Server forced to shutdown", err)
	} else {
		logger.Info("main", "Server shutdown completed successfully")
	}
}

// setupHTTPRoutes configures all HTTP routes for the exporter with rate limiting and security headers
func setupHTTPRoutes(exp *exporter.OpenLDAPExporter) http.Handler {
	mux := http.NewServeMux()

	// Configure rate limiting from environment variables
	rateLimitRequests := getEnvInt("RATE_LIMIT_REQUESTS", defaultRateLimitRequests)
	rateLimitBurst := getEnvInt("RATE_LIMIT_BURST", defaultRateLimitBurst)
	healthRequests := getEnvInt("HEALTH_RATE_LIMIT_REQUESTS", defaultHealthRequests)
	healthBurst := getEnvInt("HEALTH_RATE_LIMIT_BURST", defaultHealthBurst)

	// Create rate limiter with configurable values
	rateLimiter := security.NewRateLimiter(rateLimitRequests, rateLimitBurst)
	rateLimitMiddleware := security.RateLimitMiddleware(rateLimiter)

	// Security middleware
	securityMiddleware := securityHeadersMiddleware

	// Root endpoint with basic information (rate limited + security headers)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		securityMiddleware(rateLimitMiddleware(http.HandlerFunc(handleRoot))).ServeHTTP(w, r)
	})

	// Metrics endpoint (rate limited + security headers)
	mux.Handle("/metrics", securityMiddleware(rateLimitMiddleware(promhttp.Handler())))

	// Health check endpoint (rate limited but more generous + security headers)
	healthLimiter := security.NewRateLimiter(healthRequests, healthBurst)
	healthMiddleware := security.RateLimitMiddleware(healthLimiter)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		securityMiddleware(healthMiddleware(http.HandlerFunc(handleHealth))).ServeHTTP(w, r)
	})

	// Internal monitoring endpoint (rate limited + security headers)
	internalLimiter := security.NewRateLimiter(rateLimitRequests, rateLimitBurst) // Same as metrics endpoint
	internalMiddleware := security.RateLimitMiddleware(internalLimiter)
	mux.HandleFunc("/internal/metrics", func(w http.ResponseWriter, r *http.Request) {
		securityMiddleware(internalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleInternalMetrics(w, r, exp)
		}))).ServeHTTP(w, r)
	})

	// Add a simple JSON status endpoint for the web UI
	mux.HandleFunc("/internal/status", func(w http.ResponseWriter, r *http.Request) {
		securityMiddleware(internalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleInternalStatus(w, r, exp)
		}))).ServeHTTP(w, r)
	})

	logger.SafeInfo("main", "HTTP security and rate limiting configured", map[string]interface{}{
		"metrics_requests_per_min":          rateLimitRequests,
		"metrics_burst_size":                rateLimitBurst,
		"health_requests_per_min":           healthRequests,
		"health_burst_size":                 healthBurst,
		"internal_metrics_requests_per_min": healthRequests / 2,
		"internal_metrics_burst_size":       healthBurst / 2,
		"security_headers_enabled":          true,
		"endpoints":                         []string{"/", "/metrics", "/health", "/internal/metrics"},
	})

	return mux
}

// securityHeadersMiddleware adds security headers to HTTP responses
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent content type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent page from being rendered in frame/iframe (clickjacking protection)
		w.Header().Set("X-Frame-Options", "DENY")

		// Enable XSS filtering
		w.Header().Set("X-XSS-Protection", "1; mode=block")

		// Control referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy - restrict resource loading
		csp := "default-src 'self'; " +
			"script-src 'self' 'unsafe-inline'; " +
			"style-src 'self' 'unsafe-inline'; " +
			"img-src 'self' data:; " +
			"connect-src 'self'; " +
			"font-src 'self'; " +
			"object-src 'none'; " +
			"media-src 'none'; " +
			"frame-src 'none';"
		w.Header().Set("Content-Security-Policy", csp)

		// Strict Transport Security (HTTPS only) - only set if request is HTTPS
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		// Remove server information
		w.Header().Set("Server", "")

		// Prevent browsers from MIME-sniffing a response away from declared content-type
		w.Header().Set("X-Download-Options", "noopen")

		// Prevent Adobe Flash and PDF files from loading content
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")

		next.ServeHTTP(w, r)
	})
}

// rootPageTemplate is the HTML template for the root page
const rootPageTemplate = `<!DOCTYPE html>
<html>
	<head>
		<title>OpenLDAP Exporter</title>
		<meta charset="utf-8">
		<style>
			body{font-family:Arial,sans-serif;margin:40px;background-color:#f8f9fa}
			.container{max-width:800px;margin:0 auto;background-color:white;padding:30px;border-radius:8px;box-shadow:0 2px 10px rgba(0,0,0,0.1)}
			.info{background-color:#e9ecef;padding:15px;border-radius:5px;margin:20px 0;border-left:4px solid #007bff}
			.metrics-info{background-color:#d4edda;padding:15px;border-radius:5px;margin:20px 0;border-left:4px solid #28a745}
			.status-info{background-color:#fff3cd;padding:15px;border-radius:5px;margin:20px 0;border-left:4px solid #ffc107}
			a{color:#007bff;text-decoration:none}
			a:hover{text-decoration:underline}
			.metric-count{font-size:1.2em;font-weight:bold;color:#28a745}
			.timestamp{color:#6c757d;font-size:0.9em}
		</style>
	</head>
	<body>
		<div class="container">
			<h1>OpenLDAP Exporter</h1>
			<div class="info">
				<h3>Version Information</h3>
				<p><strong>Version:</strong> %s</p>
			</div>
			<div class="metrics-info">
				<h3>Metrics Overview</h3>
				<p><strong>Available Metric Groups:</strong> <span class="metric-count">12</span> (connections, statistics, operations, threads, time, waiters, overlays, tls, backends, listeners, health, database)</p>
				<p><strong>Total Metrics:</strong> <span class="metric-count">25+</span> OpenLDAP monitoring metrics</p>
				<p><strong>Last Scrape:</strong> <span class="timestamp" id="lastScrape">Available on first /metrics request</span></p>
			</div>
			<div class="status-info">
				<h3>Filtering Configuration</h3>
				<p><strong>Metrics Include:</strong> %s</p>
				<p><strong>Metrics Exclude:</strong> %s</p>
			</div>
			<div class="info">
				<h3>Available Endpoints</h3>
				<ul>
					<li><a href="/health">Health</a> - Health check endpoint</li>
					<li><a href="/metrics">Metrics</a> - OpenLDAP Prometheus metrics endpoint</li>
					<li><a href="/internal/metrics">Internal Metrics</a> - Internal exporter Prometheus metrics endpoint</li>
				</ul>
				<h3>Documentation</h3>
				<p>This exporter provides OpenLDAP monitoring metrics according to the official <a href="https://www.openldap.org/doc/admin26/monitoringslapd.html">OpenLDAP monitoring documentation</a>.</p>
			</div>
		</div>
		<script>
			// Update last scrape time every 30 seconds
			function updateLastScrape() {
				fetch('/internal/status')
					.then(response => response.json())
					.then(data => {
						if (data.timestamp) {
							const date = new Date(data.timestamp * 1000);
							document.getElementById('lastScrape').textContent = date.toLocaleString();
						}
					})
					.catch(() => {
						// Silently fail - not critical for functionality
					});
			}
			// Update immediately and then every 30 seconds
			updateLastScrape();
			setInterval(updateLastScrape, 30000);
		</script>
	</body>
</html>`

// handleRoot serves the root page with basic exporter information
func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Load current configuration to show filtering status
	configData, err := config.LoadConfig()
	if err != nil {
		logger.Error("main", "Failed to load configuration for root page", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer configData.Clear()

	// Format include/exclude filters for display
	includeFilter := "None (collect all metric groups)"
	if len(configData.MetricsInclude) > 0 {
		includeFilter = fmt.Sprintf("[%s]", strings.Join(configData.MetricsInclude, ", "))
	}

	excludeFilter := "None"
	if len(configData.MetricsExclude) > 0 {
		excludeFilter = fmt.Sprintf("[%s]", strings.Join(configData.MetricsExclude, ", "))
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(rootPageTemplate, Version, includeFilter, excludeFilter)))
}

// handleHealth provides a simple health check endpoint
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := `{"status":"ok","version":"` + Version + `"}`
	_, _ = w.Write([]byte(response))
}

// handleInternalMetrics provides internal monitoring metrics
func handleInternalMetrics(w http.ResponseWriter, r *http.Request, exp *exporter.OpenLDAPExporter) {
	monitoring := exp.GetInternalMonitoring()

	// Update system metrics before exposing them
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	monitoring.UpdateSystemMetrics("openldap-exporter", runtime.NumGoroutine(),
		m.HeapInuse, m.StackInuse, m.Sys)

	// Create a separate registry for internal metrics only
	internalRegistry := prometheus.NewRegistry()

	// Register internal monitoring metrics with the separate registry
	_ = monitoring.RegisterMetrics(internalRegistry)

	// Create a Prometheus handler for the internal registry
	handler := promhttp.HandlerFor(internalRegistry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})

	// Serve the internal metrics in Prometheus format
	handler.ServeHTTP(w, r)
}

// handleInternalStatus provides a simple JSON status for the web UI
func handleInternalStatus(w http.ResponseWriter, r *http.Request, exp *exporter.OpenLDAPExporter) {
	w.Header().Set("Content-Type", "application/json")

	monitoring := exp.GetInternalMonitoring()

	// Prepare simple status response for web UI
	response := map[string]interface{}{
		"version":   Version,
		"timestamp": time.Now().Unix(),
		"uptime":    time.Since(monitoring.GetStartTime()).String(),
		"status":    "running",
	}

	jsonData, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		logger.Error("main", "Failed to marshal internal status", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jsonData)
}
