/*
 * This file is part of the KubeVirt Redfish project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2025 KubeVirt Redfish project and its authors.
 *
 */

package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// CircuitState represents the current state of the circuit breaker
type CircuitState int

const (
	StateClosed   CircuitState = iota // Normal operation - requests pass through
	StateOpen                         // Circuit is open - requests are blocked
	StateHalfOpen                     // Testing if service has recovered
)

// CircuitBreaker provides circuit breaker functionality for external dependencies
type CircuitBreaker struct {
	// Configuration
	name             string
	failureThreshold int           // Number of failures before opening circuit
	successThreshold int           // Number of successes before closing circuit
	timeout          time.Duration // Time to wait before attempting recovery
	windowSize       time.Duration // Time window for failure counting

	// State management
	state           CircuitState
	lastFailureTime time.Time
	lastStateChange time.Time

	// Counters
	failureCount   int
	successCount   int
	totalRequests  int64
	totalFailures  int64
	totalSuccesses int64

	// Statistics
	stats      *CircuitBreakerStats
	statsMutex sync.RWMutex

	// Mutex for thread safety
	mutex sync.RWMutex

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// CircuitBreakerStats tracks circuit breaker performance
type CircuitBreakerStats struct {
	TotalRequests    int64
	TotalFailures    int64
	TotalSuccesses   int64
	CircuitOpens     int64
	CircuitCloses    int64
	CircuitHalfOpens int64
	LastStateChange  time.Time
	CurrentState     CircuitState
	FailureRate      float64
	Uptime           time.Duration
	LastReset        time.Time
}

// CircuitBreakerConfig holds configuration for a circuit breaker
type CircuitBreakerConfig struct {
	Name             string
	FailureThreshold int
	SuccessThreshold int
	Timeout          time.Duration
	WindowSize       time.Duration
}

// NewCircuitBreaker creates a new circuit breaker instance
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	ctx, cancel := context.WithCancel(context.Background())

	cb := &CircuitBreaker{
		name:             config.Name,
		failureThreshold: config.FailureThreshold,
		successThreshold: config.SuccessThreshold,
		timeout:          config.Timeout,
		windowSize:       config.WindowSize,
		state:            StateClosed,
		lastStateChange:  time.Now(),
		ctx:              ctx,
		cancel:           cancel,
		stats: &CircuitBreakerStats{
			LastReset: time.Now(),
		},
	}

	// Set defaults if not provided
	if cb.failureThreshold <= 0 {
		cb.failureThreshold = 5
	}
	if cb.successThreshold <= 0 {
		cb.successThreshold = 3
	}
	if cb.timeout <= 0 {
		cb.timeout = 30 * time.Second
	}
	if cb.windowSize <= 0 {
		cb.windowSize = 60 * time.Second
	}

	return cb
}

// Execute runs a function with circuit breaker protection
func (cb *CircuitBreaker) Execute(operation func() error) error {
	cb.mutex.RLock()
	currentState := cb.state
	cb.mutex.RUnlock()

	switch currentState {
	case StateClosed:
		return cb.executeClosed(operation)
	case StateOpen:
		return cb.executeOpen()
	case StateHalfOpen:
		return cb.executeHalfOpen(operation)
	default:
		return fmt.Errorf("unknown circuit breaker state")
	}
}

// ExecuteWithContext runs a function with circuit breaker protection and context
func (cb *CircuitBreaker) ExecuteWithContext(ctx context.Context, operation func() error) error {
	// Check if context is cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Combine with circuit breaker context
	combinedCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-cb.ctx.Done():
			cancel()
		case <-combinedCtx.Done():
			// Context already cancelled
		}
	}()

	return cb.Execute(operation)
}

// executeClosed handles execution when circuit is closed (normal operation)
func (cb *CircuitBreaker) executeClosed(operation func() error) error {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	// Check if we're still in closed state
	if cb.state != StateClosed {
		return cb.executeOpen()
	}

	// Execute the operation
	err := operation()

	// Update statistics
	cb.totalRequests++
	cb.updateStats(1, 0, 0, 0, 0, 0)

	if err != nil {
		// Operation failed
		cb.failureCount++
		cb.totalFailures++
		cb.lastFailureTime = time.Now()
		cb.updateStats(0, 1, 0, 0, 0, 0)

		// Check if we should open the circuit
		if cb.failureCount >= cb.failureThreshold {
			cb.openCircuit()
		}

		return err
	}

	// Operation succeeded
	cb.successCount++
	cb.totalSuccesses++
	cb.failureCount = 0 // Reset failure count on success
	cb.updateStats(0, 0, 1, 0, 0, 0)

	return nil
}

// executeOpen handles execution when circuit is open (blocking requests)
func (cb *CircuitBreaker) executeOpen() error {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	// Check if timeout has elapsed
	if time.Since(cb.lastFailureTime) >= cb.timeout {
		cb.halfOpenCircuit()
		return cb.executeHalfOpen(func() error {
			return fmt.Errorf("circuit breaker is testing recovery")
		})
	}

	return fmt.Errorf("circuit breaker is open for %s (last failure: %v ago)",
		cb.name, time.Since(cb.lastFailureTime))
}

// executeHalfOpen handles execution when circuit is half-open (testing recovery)
func (cb *CircuitBreaker) executeHalfOpen(operation func() error) error {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	// Check if we're still in half-open state
	if cb.state != StateHalfOpen {
		return cb.executeOpen()
	}

	// Execute the operation
	err := operation()

	// Update statistics
	cb.totalRequests++
	cb.updateStats(1, 0, 0, 0, 0, 0)

	if err != nil {
		// Operation failed - open circuit again
		cb.totalFailures++
		cb.lastFailureTime = time.Now()
		cb.openCircuit()
		cb.updateStats(0, 1, 0, 0, 0, 0)
		return err
	}

	// Operation succeeded - close circuit
	cb.totalSuccesses++
	cb.closeCircuit()
	cb.updateStats(0, 0, 1, 0, 0, 0)

	return nil
}

// openCircuit opens the circuit breaker
func (cb *CircuitBreaker) openCircuit() {
	if cb.state != StateOpen {
		cb.state = StateOpen
		cb.lastStateChange = time.Now()
		cb.failureCount = 0
		cb.successCount = 0
		cb.updateStats(0, 0, 0, 1, 0, 0)

		logger.Warning("Circuit breaker '%s' opened after %d failures",
			cb.name, cb.failureThreshold)
	}
}

// closeCircuit closes the circuit breaker
func (cb *CircuitBreaker) closeCircuit() {
	if cb.state != StateClosed {
		cb.state = StateClosed
		cb.lastStateChange = time.Now()
		cb.failureCount = 0
		cb.successCount = 0
		cb.updateStats(0, 0, 0, 0, 1, 0)

		logger.Info("Circuit breaker '%s' closed after successful recovery", cb.name)
	}
}

// halfOpenCircuit sets the circuit to half-open state
func (cb *CircuitBreaker) halfOpenCircuit() {
	if cb.state != StateHalfOpen {
		cb.state = StateHalfOpen
		cb.lastStateChange = time.Now()
		cb.failureCount = 0
		cb.successCount = 0
		cb.updateStats(0, 0, 0, 0, 0, 1)

		logger.Info("Circuit breaker '%s' half-opened for recovery testing", cb.name)
	}
}

// ForceOpen manually opens the circuit breaker
func (cb *CircuitBreaker) ForceOpen() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.openCircuit()
}

// ForceClose manually closes the circuit breaker
func (cb *CircuitBreaker) ForceClose() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.closeCircuit()
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	return cb.state
}

// GetStats returns circuit breaker statistics
func (cb *CircuitBreaker) GetStats() *CircuitBreakerStats {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	cb.statsMutex.RLock()
	defer cb.statsMutex.RUnlock()

	// Calculate failure rate
	failureRate := float64(0)
	if cb.totalRequests > 0 {
		failureRate = float64(cb.totalFailures) / float64(cb.totalRequests) * 100
	}

	return &CircuitBreakerStats{
		TotalRequests:    cb.totalRequests,
		TotalFailures:    cb.totalFailures,
		TotalSuccesses:   cb.totalSuccesses,
		CircuitOpens:     cb.stats.CircuitOpens,
		CircuitCloses:    cb.stats.CircuitCloses,
		CircuitHalfOpens: cb.stats.CircuitHalfOpens,
		LastStateChange:  cb.lastStateChange,
		CurrentState:     cb.state,
		FailureRate:      failureRate,
		Uptime:           time.Since(cb.stats.LastReset),
		LastReset:        cb.stats.LastReset,
	}
}

// updateStats updates circuit breaker statistics
func (cb *CircuitBreaker) updateStats(requests, failures, successes, opens, closes, halfOpens int64) {
	cb.statsMutex.Lock()
	defer cb.statsMutex.Unlock()

	cb.stats.TotalRequests += requests
	cb.stats.TotalFailures += failures
	cb.stats.TotalSuccesses += successes
	cb.stats.CircuitOpens += opens
	cb.stats.CircuitCloses += closes
	cb.stats.CircuitHalfOpens += halfOpens
}

// Reset resets the circuit breaker to initial state
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.lastFailureTime = time.Time{}
	cb.lastStateChange = time.Now()

	cb.statsMutex.Lock()
	cb.stats.LastReset = time.Now()
	cb.statsMutex.Unlock()

	logger.Info("Circuit breaker '%s' reset to initial state", cb.name)
}

// Stop gracefully stops the circuit breaker
func (cb *CircuitBreaker) Stop() {
	logger.Info("Stopping circuit breaker '%s'", cb.name)

	cb.cancel()

	// Reset to closed state
	cb.ForceClose()
}

// CircuitBreakerManager manages multiple circuit breakers
type CircuitBreakerManager struct {
	breakers map[string]*CircuitBreaker
	mutex    sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager() *CircuitBreakerManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// GetOrCreate gets an existing circuit breaker or creates a new one
func (cbm *CircuitBreakerManager) GetOrCreate(name string, config CircuitBreakerConfig) *CircuitBreaker {
	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()

	if breaker, exists := cbm.breakers[name]; exists {
		return breaker
	}

	config.Name = name
	breaker := NewCircuitBreaker(config)
	cbm.breakers[name] = breaker

	return breaker
}

// Get returns an existing circuit breaker
func (cbm *CircuitBreakerManager) Get(name string) (*CircuitBreaker, bool) {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	breaker, exists := cbm.breakers[name]
	return breaker, exists
}

// GetAll returns all circuit breakers
func (cbm *CircuitBreakerManager) GetAll() map[string]*CircuitBreaker {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	result := make(map[string]*CircuitBreaker)
	for name, breaker := range cbm.breakers {
		result[name] = breaker
	}

	return result
}

// GetStats returns statistics for all circuit breakers
func (cbm *CircuitBreakerManager) GetStats() map[string]interface{} {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	stats := make(map[string]interface{})
	for name, breaker := range cbm.breakers {
		stats[name] = breaker.GetStats()
	}

	return stats
}

// Stop stops all circuit breakers
func (cbm *CircuitBreakerManager) Stop() {
	logger.Info("Stopping circuit breaker manager...")

	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()

	for name, breaker := range cbm.breakers {
		breaker.Stop()
		delete(cbm.breakers, name)
	}

	cbm.cancel()

	logger.Info("Circuit breaker manager stopped")
}
