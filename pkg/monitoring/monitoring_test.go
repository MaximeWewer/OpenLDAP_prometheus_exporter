package monitoring

import (
	"testing"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
)

// TestInternalMonitoring tests the internal monitoring system
func TestInternalMonitoring(t *testing.T) {
	monitoring := NewInternalMonitoring()

	serverName := "test-server"

	// Test pool metrics recording
	monitoring.RecordPoolUtilization(serverName, "adaptive", 0.75)
	monitoring.RecordPoolConnections(serverName, "adaptive", "active", 3)
	monitoring.RecordPoolWaitTime(serverName, "adaptive", 50*time.Millisecond)
	monitoring.RecordPoolOperation(serverName, "adaptive", "get")

	// Test circuit breaker metrics
	monitoring.RecordCircuitBreakerState(serverName, circuitbreaker.StateClosed)
	monitoring.RecordCircuitBreakerRequest(serverName, "allowed")
	monitoring.RecordCircuitBreakerFailure(serverName)

	// Test collection metrics
	monitoring.RecordCollectionLatency(serverName, "connections", 25*time.Millisecond)
	monitoring.RecordCollectionSuccess(serverName, "connections")
	monitoring.RecordCollectionFailure(serverName, "operations")

	// Test cache metrics
	monitoring.RecordCacheOperation(serverName, "hit")
	monitoring.RecordCacheHitRatio(serverName, 0.85)
	monitoring.RecordCacheSize(serverName, "metrics", 1024)

	// Test rate limit metrics
	monitoring.RecordRateLimitRequest("192.168.1.100", "/metrics")
	monitoring.RecordRateLimitBlocked("192.168.1.101", "/metrics")

	// Test system metrics
	monitoring.UpdateSystemMetrics(serverName, 10, 1024*1024, 512*1024, 2048*1024)

	// Test event recording
	monitoring.RecordEvent("test_event")
	monitoring.RecordEvent("test_event")
	monitoring.RecordEvent("another_event")

	// Verify event stats
	eventStats := monitoring.GetEventStats()
	if len(eventStats) != 2 {
		t.Errorf("Expected 2 event types, got %d", len(eventStats))
	}

	if testEventStats, exists := eventStats["test_event"]; exists {
		if count := testEventStats["count"]; count != int64(2) {
			t.Errorf("Expected test_event count to be 2, got %v", count)
		}
	} else {
		t.Error("test_event should exist in event stats")
	}

	// Test system stats
	systemStats := monitoring.GetSystemStats()
	if uptime := systemStats["uptime"]; uptime == nil {
		t.Error("System stats should include uptime")
	}

	// Test reset
	monitoring.Reset()
	eventStatsAfterReset := monitoring.GetEventStats()
	if len(eventStatsAfterReset) != 0 {
		t.Errorf("Expected no events after reset, got %d", len(eventStatsAfterReset))
	}

	t.Log("Internal monitoring test completed successfully")
}
