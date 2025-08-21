//go:build integration
// +build integration

package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

// TestCircuitBreakerIntegration tests the circuit breaker with realistic scenarios
func TestCircuitBreakerIntegration(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:      3,
		Timeout:          1 * time.Second,
		ResetTimeout:     500 * time.Millisecond,
		SuccessThreshold: 2,
	}

	cb := NewCircuitBreaker(config)

	// Track state changes
	var stateChanges []string
	cb.SetStateChangeCallback(func(from, to State) {
		stateChanges = append(stateChanges, from.String()+"->"+to.String())
	})

	// Successful calls should keep circuit closed
	t.Run("Successful calls keep circuit closed", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			err := cb.Call(func() error {
				return nil // Success
			})
			if err != nil {
				t.Errorf("Expected success, got error: %v", err)
			}
		}

		if cb.GetState() != StateClosed {
			t.Errorf("Expected circuit to be closed, got %v", cb.GetState())
		}
	})

	// Sequential failures should open circuit
	t.Run("Sequential failures open circuit", func(t *testing.T) {
		failureErr := errors.New("test failure")

		for i := 0; i < 3; i++ {
			err := cb.Call(func() error {
				return failureErr
			})
			if err == nil {
				t.Errorf("Expected error on failure %d", i)
			}
		}

		if cb.GetState() != StateOpen {
			t.Errorf("Expected circuit to be open after failures, got %v", cb.GetState())
		}

		// Next call should fail immediately without executing function
		called := false
		err := cb.Call(func() error {
			called = true
			return nil
		})

		if called {
			t.Error("Function should not be called when circuit is open")
		}

		if err == nil {
			t.Error("Expected error when circuit is open")
		}
	})

	// Circuit should transition to half-open after timeout
	t.Run("Circuit transitions to half-open after timeout", func(t *testing.T) {
		// Wait for timeout
		time.Sleep(1100 * time.Millisecond) // Slightly longer than timeout

		// First call should put circuit in half-open state
		called := false
		err := cb.Call(func() error {
			called = true
			return nil // Success
		})

		if !called {
			t.Error("Function should be called in half-open state")
		}

		if err != nil {
			t.Errorf("Expected success in half-open state, got: %v", err)
		}

		if cb.GetState() != StateHalfOpen {
			t.Errorf("Expected circuit to be half-open, got %v", cb.GetState())
		}
	})

	// Successful calls in half-open should close circuit
	t.Run("Successful calls in half-open close circuit", func(t *testing.T) {
		// Need one more success to reach threshold
		err := cb.Call(func() error {
			return nil
		})

		if err != nil {
			t.Errorf("Expected success, got: %v", err)
		}

		if cb.GetState() != StateClosed {
			t.Errorf("Expected circuit to be closed after successful half-open calls, got %v", cb.GetState())
		}
	})

	// Verify state change tracking
	t.Run("State changes are tracked", func(t *testing.T) {
		if len(stateChanges) == 0 {
			t.Error("Expected state changes to be recorded")
		}

		t.Logf("State changes: %v", stateChanges)
	})
}

// TestCircuitBreakerStressTest performs stress testing
func TestCircuitBreakerStressTest(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:      5,
		Timeout:          100 * time.Millisecond,
		ResetTimeout:     50 * time.Millisecond,
		SuccessThreshold: 3,
	}

	cb := NewCircuitBreaker(config)

	// Run many concurrent operations
	concurrency := 10
	operations := 100
	results := make(chan error, concurrency*operations)

	for i := 0; i < concurrency; i++ {
		go func(workerID int) {
			for j := 0; j < operations; j++ {
				err := cb.Call(func() error {
					// Simulate mixed success/failure
					if (workerID+j)%7 == 0 {
						return errors.New("simulated failure")
					}
					return nil
				})
				results <- err
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Collect results
	successCount := 0
	failureCount := 0

	for i := 0; i < concurrency*operations; i++ {
		err := <-results
		if err != nil {
			failureCount++
		} else {
			successCount++
		}
	}

	t.Logf("Stress test results: %d successes, %d failures", successCount, failureCount)

	// Should have some successes and some failures
	if successCount == 0 {
		t.Error("Expected some successful calls")
	}

	if failureCount == 0 {
		t.Error("Expected some failed calls")
	}

	// Circuit breaker should be operational
	stats := cb.GetStats()
	if stats["total_calls"].(int64) != int64(concurrency*operations) {
		t.Errorf("Expected %d total calls, got %v", concurrency*operations, stats["total_calls"])
	}
}

// TestCircuitBreakerRecovery tests recovery after failures
func TestCircuitBreakerRecovery(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:      2,
		Timeout:          200 * time.Millisecond,
		ResetTimeout:     100 * time.Millisecond,
		SuccessThreshold: 1,
	}

	cb := NewCircuitBreaker(config)

	// Cause circuit to open
	for i := 0; i < 2; i++ {
		_ = cb.Call(func() error {
			return errors.New("failure")
		})
	}

	if cb.GetState() != StateOpen {
		t.Fatalf("Expected circuit to be open, got %v", cb.GetState())
	}

	// Wait for recovery opportunity
	time.Sleep(250 * time.Millisecond)

	// Successful call should recover circuit
	err := cb.Call(func() error {
		return nil
	})

	if err != nil {
		t.Errorf("Expected recovery call to succeed, got: %v", err)
	}

	if cb.GetState() != StateClosed {
		t.Errorf("Expected circuit to be closed after recovery, got %v", cb.GetState())
	}

	// Subsequent calls should work normally
	for i := 0; i < 5; i++ {
		err := cb.Call(func() error {
			return nil
		})

		if err != nil {
			t.Errorf("Expected normal operation after recovery, got: %v", err)
		}
	}
}
