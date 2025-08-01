package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultListenAddress   = ":9330"
	defaultShutdownTimeout = 30 * time.Second
)

// getLogLevel retrieves the log level from LOG_LEVEL environment variable or returns default
func getLogLevel() string {
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		return envLevel
	}
	return "INFO"
}

var (
	listenAddr = flag.String("web.listen-address", defaultListenAddress, "Address to listen on for web interface and telemetry")
	version    = flag.Bool("version", false, "Print version information and exit")
	logLevel   = flag.String("log.level", getLogLevel(), "Log level (DEBUG, INFO, WARN, ERROR, FATAL). Can also be set via LOG_LEVEL environment variable")
)

var (
	Version = "dev"
)

// main is the entry point of the OpenLDAP exporter application
func main() {
	flag.Parse()

	// Initialize with parsed log level
	InitLogger("openldap-exporter", *logLevel)

	if *version {
		Info("Version requested", map[string]interface{}{"version": Version})
		os.Exit(0)
	}

	config, err := LoadConfig()
	if err != nil {
		Fatal("Failed to load configuration", err)
	}

	exporter := NewOpenLDAPExporter(config)
	prometheus.MustRegister(exporter)

	mux := setupHTTPRoutes()

	server := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		Info("Starting OpenLDAP exporter", map[string]interface{}{
			"listen_address": *listenAddr,
			"version":        Version,
		})
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Fatal("Failed to start HTTP server", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	Info("Received shutdown signal, starting graceful shutdown")

	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		Error("Server forced to shutdown", err)
	} else {
		Info("Server shutdown completed successfully")
	}
}

// setupHTTPRoutes configures all HTTP routes for the exporter
func setupHTTPRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Root endpoint with basic information
	mux.HandleFunc("/", handleRoot)

	// Health check endpoint
	mux.HandleFunc("/health", handleHealth)

	return mux
}

// handleRoot serves the root page with basic exporter information
func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := `<!DOCTYPE html>
<html>
<head>
    <title>OpenLDAP Exporter</title>
    <meta charset="utf-8">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .container { max-width: 800px; margin: 0 auto; }
        .info { background-color: #f5f5f5; padding: 15px; border-radius: 5px; margin: 20px 0; }
        a { color: #007bff; text-decoration: none; }
        a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <div class="container">
        <h1>OpenLDAP Exporter</h1>
        <div class="info">
            <h3>Version Information</h3>
            <p><strong>Version:</strong> ` + Version + `</p>
        </div>
        <h3>Available Endpoints</h3>
        <ul>
            <li><a href="/metrics">Metrics</a> - Prometheus metrics endpoint</li>
            <li><a href="/health">Health</a> - Health check endpoint</li>
        </ul>
        <h3>Documentation</h3>
        <p>This exporter provides OpenLDAP monitoring metrics according to the official <a href="https://www.openldap.org/doc/admin26/monitoringslapd.html">OpenLDAP monitoring documentation</a>.</p>
    </div>
</body>
</html>`
	w.Write([]byte(html))
}

// handleHealth provides a simple health check endpoint
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := `{"status":"ok","version":"` + Version + `"}`
	w.Write([]byte(response))
}
