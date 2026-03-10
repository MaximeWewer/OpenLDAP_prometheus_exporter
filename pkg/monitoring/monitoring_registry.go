package monitoring

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// RegisterMetrics registers all internal monitoring metrics with a Prometheus registry
func (im *InternalMonitoring) RegisterMetrics(registry *prometheus.Registry) error {
	collectors := []prometheus.Collector{
		// Pool metrics - basic
		im.poolUtilization,
		im.poolConnections,
		im.poolWaitTime,
		im.poolOperations,

		// Pool metrics - detailed lifecycle
		im.poolConnectionsCreated,
		im.poolConnectionsClosed,
		im.poolConnectionsFailed,
		im.poolConnectionsReused,

		// Pool metrics - connection quality
		im.poolConnectionAge,
		im.poolConnectionIdleTime,
		im.poolWaitTimeouts,

		// Pool metrics - health monitoring
		im.poolHealthChecks,
		im.poolHealthCheckFailures,

		// Pool metrics - operation details
		im.poolGetRequests,
		im.poolGetFailures,
		im.poolPutRequests,
		im.poolPutRejections,

		// Circuit breaker metrics
		im.circuitBreakerState,
		im.circuitBreakerRequests,
		im.circuitBreakerFailures,

		// Collection metrics
		im.collectionLatency,
		im.collectionSuccess,
		im.collectionFailures,

		// Rate limiting metrics
		im.rateLimitRequests,
		im.rateLimitBlocked,

		// System metrics
		im.goroutineCount,
		im.memoryUsage,
		im.uptime,
	}

	var (
		firstError                 error
		successCount, failureCount int
	)

	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			failureCount++
			logger.SafeWarn("monitoring", "Failed to register internal metric collector", map[string]interface{}{
				"error":          err.Error(),
				"collector_type": fmt.Sprintf("%T", collector),
			})
			if firstError == nil {
				firstError = err
			}
		} else {
			successCount++
		}
	}

	logger.SafeInfo("monitoring", "Metric registration completed", map[string]interface{}{
		"success_count":    successCount,
		"failure_count":    failureCount,
		"total_collectors": len(collectors),
	})

	return firstError
}

// InitializeMetricsForServer initializes all metrics with zero values for a specific server
// This ensures metrics appear in Prometheus even before any events occur
func (im *InternalMonitoring) InitializeMetricsForServer(serverName string) {
	const poolType = "ldap"

	// Initialize pool lifecycle metrics
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "normal").Add(0)
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "timeout").Add(0)
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "error").Add(0)
	im.poolConnectionsClosed.WithLabelValues(serverName, poolType, "shutdown").Add(0)

	im.poolConnectionsFailed.WithLabelValues(serverName, poolType, "network_error").Add(0)
	im.poolConnectionsFailed.WithLabelValues(serverName, poolType, "auth_error").Add(0)

	// Initialize pool operation metrics
	im.poolGetFailures.WithLabelValues(serverName, poolType, "timeout").Add(0)
	im.poolGetFailures.WithLabelValues(serverName, poolType, "creation_failed").Add(0)
	im.poolGetFailures.WithLabelValues(serverName, poolType, "max_attempts").Add(0)

	im.poolPutRejections.WithLabelValues(serverName, poolType, "invalid_connection").Add(0)
	im.poolPutRejections.WithLabelValues(serverName, poolType, "pool_full").Add(0)

	// Initialize pool quality metrics
	im.poolWaitTimeouts.WithLabelValues(serverName, poolType).Add(0)

	// Initialize pool health metrics
	im.poolHealthChecks.WithLabelValues(serverName, poolType, "success").Add(0)
	im.poolHealthChecks.WithLabelValues(serverName, poolType, "failure").Add(0)
	im.poolHealthCheckFailures.WithLabelValues(serverName, poolType).Add(0)

	// Initialize circuit breaker metrics
	im.circuitBreakerFailures.WithLabelValues(serverName).Add(0)
	im.circuitBreakerRequests.WithLabelValues(serverName, "blocked").Add(0)

	// Initialize collection failure metrics
	im.collectionFailures.WithLabelValues(serverName, "scrape").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "connections").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "statistics").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "operations").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "threads").Add(0)
	im.collectionFailures.WithLabelValues(serverName, "health").Add(0)

	// Initialize rate limiting metrics for common endpoints
	commonEndpoints := []string{"/metrics", "/health", "/internal/metrics"}
	for _, endpoint := range commonEndpoints {
		im.rateLimitBlocked.WithLabelValues(UnknownClientIP, endpoint).Add(0)
	}

	logger.SafeDebug("monitoring", "Initialized metrics for server", map[string]interface{}{
		"server": serverName,
	})
}
