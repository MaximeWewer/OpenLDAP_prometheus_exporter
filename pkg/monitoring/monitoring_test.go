package monitoring

import (
	"testing"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/circuitbreaker"
	"github.com/prometheus/client_golang/prometheus"
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

// TestRecordEventEdgeCases tests edge cases for RecordEvent to achieve 100% coverage
func TestRecordEventEdgeCases(t *testing.T) {
	monitoring := NewInternalMonitoring()

	// Test first event (branch where event doesn't exist)
	monitoring.RecordEvent("new_event")
	
	eventStats := monitoring.GetEventStats()
	if stats, exists := eventStats["new_event"]; !exists {
		t.Error("new_event should exist after first RecordEvent")
	} else if count := stats["count"]; count != int64(1) {
		t.Errorf("Expected count 1, got %v", count)
	}

	// Test immediate second event (duration will be very small, testing duration > 0 branch)
	monitoring.RecordEvent("new_event")
	
	// Test multiple rapid events to trigger rate calculation
	for i := 0; i < 5; i++ {
		monitoring.RecordEvent("rapid_event")
		time.Sleep(1 * time.Millisecond) // Small delay to ensure duration > 0
	}
	
	eventStats = monitoring.GetEventStats()
	if stats, exists := eventStats["rapid_event"]; !exists {
		t.Error("rapid_event should exist")
	} else if count := stats["count"]; count != int64(5) {
		t.Errorf("Expected rapid_event count 5, got %v", count)
	}
	
	// Test rate calculation
	if stats, exists := eventStats["rapid_event"]; exists {
		if rate, hasRate := stats["rate"]; hasRate && rate.(float64) <= 0 {
			t.Error("Rate should be positive for rapid events")
		}
	}
}

// TestGetStartTime tests the GetStartTime method
func TestGetStartTime(t *testing.T) {
	monitoring := NewInternalMonitoring()

	startTime := monitoring.GetStartTime()
	if startTime.IsZero() {
		t.Error("Start time should not be zero")
	}

	// Verify start time is recent (within last second)
	if time.Since(startTime) > time.Second {
		t.Error("Start time should be recent")
	}
}

// TestRegisterMetrics tests the RegisterMetrics method
func TestRegisterMetrics(t *testing.T) {
	monitoring := NewInternalMonitoring()

	// Create a proper registry
	registry := prometheus.NewRegistry()

	// Register metrics should succeed
	err := monitoring.RegisterMetrics(registry)
	if err != nil {
		t.Errorf("RegisterMetrics() failed: %v", err)
	}

	// Second registration should fail (already registered)
	err = monitoring.RegisterMetrics(registry)
	if err == nil {
		t.Error("Second RegisterMetrics() should fail due to duplicate registration")
	}
}
