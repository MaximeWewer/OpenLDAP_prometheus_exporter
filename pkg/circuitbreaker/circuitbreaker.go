package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// State represents the current state of the circuit breaker
type State int

const (
	// StateClosed means the circuit breaker is allowing requests through
	StateClosed State = iota
	// StateOpen means the circuit breaker is blocking requests
	StateOpen
	// StateHalfOpen means the circuit breaker is testing if the service has recovered
	StateHalfOpen
)

// String returns the string representation of the state
func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF_OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreakerConfig holds configuration for the circuit breaker
type CircuitBreakerConfig struct {
	// MaxFailures is the maximum number of failures before opening the circuit
	MaxFailures int
	// Timeout is how long to wait before attempting to close the circuit
	Timeout time.Duration
	// ResetTimeout is how long to wait in half-open state before going back to closed
	ResetTimeout time.Duration
	// SuccessThreshold is the number of consecutive successes needed to close the circuit
	SuccessThreshold int
}

// DefaultCircuitBreakerConfig returns a default configuration
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		MaxFailures:      5,
		Timeout:          30 * time.Second,
		ResetTimeout:     10 * time.Second,
		SuccessThreshold: 2,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	config          CircuitBreakerConfig
	state           State
	failures        int
	successes       int
	lastFailureTime time.Time
	mutex           sync.RWMutex
	onStateChange   func(from, to State)

	// Callback management
	callbackCtx    context.Context
	callbackCancel context.CancelFunc
	callbackWg     sync.WaitGroup

	// Metrics
	totalRequests   int64
	failedRequests  int64
	blockedRequests int64
	stateChanges    int64
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.MaxFailures <= 0 {
		config.MaxFailures = 5
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.ResetTimeout <= 0 {
		config.ResetTimeout = 10 * time.Second
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 2
	}

	// Create context for managing callbacks
	ctx, cancel := context.WithCancel(context.Background())

	cb := &CircuitBreaker{
		config:         config,
		state:          StateClosed,
		callbackCtx:    ctx,
		callbackCancel: cancel,
	}

	logger.SafeInfo("circuitbreaker", "Circuit breaker created", map[string]interface{}{
		"max_failures":      config.MaxFailures,
		"timeout":           config.Timeout.String(),
		"reset_timeout":     config.ResetTimeout.String(),
		"success_threshold": config.SuccessThreshold,
	})

	return cb
}

// SetStateChangeCallback sets a callback function that will be called when the state changes
func (cb *CircuitBreaker) SetStateChangeCallback(callback func(from, to State)) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.onStateChange = callback
}

// Call executes the given function with circuit breaker protection
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mutex.Lock()
	cb.totalRequests++

	// Check if we should allow this request
	if !cb.canExecute() {
		cb.blockedRequests++
		// Capture values for logging while holding mutex
		state := cb.state.String()
		failures := cb.failures
		cb.mutex.Unlock()

		logger.SafeWarn("circuitbreaker", "Request blocked by circuit breaker", map[string]interface{}{
			"state":    state,
			"failures": failures,
		})

		return errors.New("circuit breaker is open")
	}

	cb.mutex.Unlock()

	// Execute the function
	err := fn()

	// Record the result
	cb.recordResult(err)

	return err
}

// canExecute determines if a request should be allowed based on current state
func (cb *CircuitBreaker) canExecute() bool {
	now := time.Now()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if enough time has passed to try half-open
		if now.Sub(cb.lastFailureTime) >= cb.config.Timeout {
			cb.setState(StateHalfOpen)
			return true
		}
		return false

	case StateHalfOpen:
		// In half-open state, allow a limited number of requests
		return true

	default:
		return false
	}
}

// recordResult records the success or failure of a request
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
}

// onFailure handles a failed request
func (cb *CircuitBreaker) onFailure() {
	cb.failures++
	cb.failedRequests++
	cb.successes = 0
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failures >= cb.config.MaxFailures {
			cb.setState(StateOpen)
			logger.SafeWarn("circuitbreaker", "Circuit breaker opened due to failures", map[string]interface{}{
				"failures":     cb.failures,
				"max_failures": cb.config.MaxFailures,
			})
		}

	case StateHalfOpen:
		// Any failure in half-open state immediately opens the circuit
		cb.setState(StateOpen)
		logger.SafeWarn("circuitbreaker", "Circuit breaker re-opened after half-open failure", map[string]interface{}{
			"failures": cb.failures,
		})
	}
}

// onSuccess handles a successful request
func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case StateClosed:
		// Reset failure count on success in closed state
		if cb.failures > 0 {
			cb.failures = 0
			logger.SafeDebug("circuitbreaker", "Failure count reset after success")
		}

	case StateHalfOpen:
		cb.successes++
		if cb.successes >= cb.config.SuccessThreshold {
			cb.setState(StateClosed)
			cb.failures = 0
			logger.SafeInfo("circuitbreaker", "Circuit breaker closed after successful recovery", map[string]interface{}{
				"successes":         cb.successes,
				"success_threshold": cb.config.SuccessThreshold,
			})
		}
	}
}

// setState changes the circuit breaker state and triggers callback if set
func (cb *CircuitBreaker) setState(newState State) {
	oldState := cb.state
	cb.state = newState
	cb.stateChanges++

	if cb.onStateChange != nil {
		// Call callback without holding the mutex to avoid deadlocks
		// Use managed goroutine with context for proper cleanup
		cb.callbackWg.Add(1)
		go cb.executeCallback(cb.onStateChange, oldState, newState)
	}

	logger.SafeInfo("circuitbreaker", "Circuit breaker state changed", map[string]interface{}{
		"from": oldState.String(),
		"to":   newState.String(),
	})
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() State {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

// GetStats returns statistics about the circuit breaker
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	return map[string]interface{}{
		"state":             cb.state.String(),
		"failures":          cb.failures,
		"successes":         cb.successes,
		"total_requests":    cb.totalRequests,
		"failed_requests":   cb.failedRequests,
		"blocked_requests":  cb.blockedRequests,
		"state_changes":     cb.stateChanges,
		"last_failure_time": cb.lastFailureTime,
	}
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	oldState := cb.state
	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.stateChanges++

	logger.SafeInfo("circuitbreaker", "Circuit breaker manually reset", map[string]interface{}{
		"from": oldState.String(),
		"to":   "CLOSED",
	})

	if cb.onStateChange != nil {
		cb.callbackWg.Add(1)
		go cb.executeCallback(cb.onStateChange, oldState, StateClosed)
	}
}

// IsHealthy returns true if the circuit breaker is in a healthy state
func (cb *CircuitBreaker) IsHealthy() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state != StateOpen
}

// executeCallback executes the state change callback in a managed goroutine
func (cb *CircuitBreaker) executeCallback(callback func(from, to State), oldState, newState State) {
	defer cb.callbackWg.Done()

	// Create a timeout context for the callback to prevent hanging
	ctx, cancel := context.WithTimeout(cb.callbackCtx, 5*time.Second)
	defer cancel()

	// Execute callback with timeout protection
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.SafeError("circuitbreaker", "Callback panic recovered", nil, map[string]interface{}{
					"panic": r,
					"from":  oldState.String(),
					"to":    newState.String(),
				})
			}
			close(done)
		}()
		callback(oldState, newState)
	}()

	select {
	case <-done:
		// Callback completed successfully
	case <-ctx.Done():
		logger.SafeWarn("circuitbreaker", "Callback timed out", map[string]interface{}{
			"from": oldState.String(),
			"to":   newState.String(),
		})
	}
}

// Close properly shuts down the circuit breaker and waits for all callbacks to complete
func (cb *CircuitBreaker) Close() {
	// Cancel context to signal all callbacks to stop
	cb.callbackCancel()

	// Wait for all callbacks to complete with timeout
	done := make(chan struct{})
	go func() {
		cb.callbackWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.SafeInfo("circuitbreaker", "Circuit breaker closed cleanly")
	case <-time.After(10 * time.Second):
		logger.SafeWarn("circuitbreaker", "Circuit breaker close timeout - some callbacks may still be running")
	}
}

// ForceOpen forces the circuit breaker to open state (useful for maintenance)
func (cb *CircuitBreaker) ForceOpen() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.state != StateOpen {
		oldState := cb.state
		cb.state = StateOpen
		cb.stateChanges++

		logger.SafeWarn("circuitbreaker", "Circuit breaker forced open", map[string]interface{}{
			"from": oldState.String(),
		})

		if cb.onStateChange != nil {
			cb.callbackWg.Add(1)
			go cb.executeCallback(cb.onStateChange, oldState, StateOpen)
		}
	}
}
