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
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// RetryStrategy defines how retries should be handled
type RetryStrategy int

const (
	StrategyExponential RetryStrategy = iota
	StrategyLinear
	StrategyConstant
	StrategyFibonacci
)

// RetryableError represents an error that should trigger a retry
type RetryableError struct {
	Error   error
	Retry   bool
	Backoff time.Duration
}

// RetryConfig holds configuration for retry behavior
type RetryConfig struct {
	MaxAttempts     int
	InitialDelay    time.Duration
	MaxDelay        time.Duration
	Multiplier      float64
	Jitter          bool
	JitterFactor    float64
	Strategy        RetryStrategy
	RetryableErrors []error
}

// RetryStats tracks retry performance
type RetryStats struct {
	TotalAttempts     int64
	SuccessfulRetries int64
	FailedRetries     int64
	TotalRetryTime    time.Duration
	AverageRetryTime  time.Duration
	LastRetry         time.Time
	LastReset         time.Time
}

// RetryMechanism provides advanced retry functionality
type RetryMechanism struct {
	config RetryConfig
	stats  *RetryStats
	mutex  sync.RWMutex
	rand   *rand.Rand
}

// NewRetryMechanism creates a new retry mechanism
func NewRetryMechanism(config RetryConfig) *RetryMechanism {
	rm := &RetryMechanism{
		config: config,
		stats: &RetryStats{
			LastReset: time.Now(),
		},
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	// Set defaults if not provided
	if rm.config.MaxAttempts <= 0 {
		rm.config.MaxAttempts = 3
	}
	if rm.config.InitialDelay <= 0 {
		rm.config.InitialDelay = 100 * time.Millisecond
	}
	if rm.config.MaxDelay <= 0 {
		rm.config.MaxDelay = 30 * time.Second
	}
	if rm.config.Multiplier <= 0 {
		rm.config.Multiplier = 2.0
	}
	if rm.config.JitterFactor <= 0 {
		rm.config.JitterFactor = 0.1
	}

	return rm
}

// ExecuteWithRetry executes a function with retry logic
func (rm *RetryMechanism) ExecuteWithRetry(operation func() error) error {
	return rm.ExecuteWithRetryContext(context.Background(), operation)
}

// ExecuteWithRetryContext executes a function with retry logic and context
func (rm *RetryMechanism) ExecuteWithRetryContext(ctx context.Context, operation func() error) error {
	var lastError error
	startTime := time.Now()

	for attempt := 1; attempt <= rm.config.MaxAttempts; attempt++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Execute the operation
		err := operation()

		// Update statistics
		rm.updateStats(1, 0, 0, time.Since(startTime))

		if err == nil {
			// Operation succeeded
			if attempt > 1 {
				rm.updateStats(0, 1, 0, 0)
				logger.Debug("Operation succeeded after %d attempts", attempt)
			}
			return nil
		}

		lastError = err

		// Check if error is retryable
		if !rm.isRetryableError(err) {
			logger.Debug("Non-retryable error encountered: %v", err)
			return err
		}

		// Don't retry on the last attempt
		if attempt == rm.config.MaxAttempts {
			rm.updateStats(0, 0, 1, 0)
			logger.Warning("Operation failed after %d attempts, giving up", attempt)
			return fmt.Errorf("operation failed after %d attempts: %w", attempt, err)
		}

		// Calculate delay for next attempt
		delay := rm.calculateDelay(attempt)

		logger.Debug("Operation failed (attempt %d/%d), retrying in %v: %v",
			attempt, rm.config.MaxAttempts, delay, err)

		// Wait for delay or context cancellation
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return lastError
}

// ExecuteWithRetryAndBackoff executes a function with retry logic and exponential backoff
func (rm *RetryMechanism) ExecuteWithRetryAndBackoff(operation func() (interface{}, error)) (interface{}, error) {
	return rm.ExecuteWithRetryAndBackoffContext(context.Background(), operation)
}

// ExecuteWithRetryAndBackoffContext executes a function with retry logic and exponential backoff with context
func (rm *RetryMechanism) ExecuteWithRetryAndBackoffContext(ctx context.Context, operation func() (interface{}, error)) (interface{}, error) {
	var lastError error
	startTime := time.Now()

	for attempt := 1; attempt <= rm.config.MaxAttempts; attempt++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Execute the operation
		result, err := operation()

		// Update statistics
		rm.updateStats(1, 0, 0, time.Since(startTime))

		if err == nil {
			// Operation succeeded
			if attempt > 1 {
				rm.updateStats(0, 1, 0, 0)
				logger.Debug("Operation succeeded after %d attempts", attempt)
			}
			return result, nil
		}

		lastError = err

		// Check if error is retryable
		if !rm.isRetryableError(err) {
			logger.Debug("Non-retryable error encountered: %v", err)
			return nil, err
		}

		// Don't retry on the last attempt
		if attempt == rm.config.MaxAttempts {
			rm.updateStats(0, 0, 1, 0)
			logger.Warning("Operation failed after %d attempts, giving up", attempt)
			return nil, fmt.Errorf("operation failed after %d attempts: %w", attempt, err)
		}

		// Calculate delay for next attempt
		delay := rm.calculateDelay(attempt)

		logger.Debug("Operation failed (attempt %d/%d), retrying in %v: %v",
			attempt, rm.config.MaxAttempts, delay, err)

		// Wait for delay or context cancellation
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, lastError
}

// isRetryableError checks if an error should trigger a retry
func (rm *RetryMechanism) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check against configured retryable errors
	for _, retryableErr := range rm.config.RetryableErrors {
		if err.Error() == retryableErr.Error() {
			return true
		}
	}

	// Default retryable error patterns
	retryablePatterns := []string{
		"connection refused",
		"timeout",
		"temporary",
		"unavailable",
		"rate limit",
		"too many requests",
		"server error",
		"network error",
		"persistent", // Add persistent as retryable
	}

	errStr := err.Error()
	for _, pattern := range retryablePatterns {
		if rm.containsIgnoreCase(errStr, pattern) {
			return true
		}
	}

	return false
}

// containsIgnoreCase checks if a string contains another string (case insensitive)
func (rm *RetryMechanism) containsIgnoreCase(s, substr string) bool {
	// Simple case-insensitive check
	sLower := rm.toLower(s)
	substrLower := rm.toLower(substr)

	for i := 0; i <= len(sLower)-len(substrLower); i++ {
		if sLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

// toLower converts a string to lowercase (simple implementation)
func (rm *RetryMechanism) toLower(s string) string {
	result := make([]byte, len(s))
	for i, b := range []byte(s) {
		if b >= 'A' && b <= 'Z' {
			result[i] = b + 32
		} else {
			result[i] = b
		}
	}
	return string(result)
}

// calculateDelay calculates the delay for a specific attempt
func (rm *RetryMechanism) calculateDelay(attempt int) time.Duration {
	var baseDelay time.Duration

	switch rm.config.Strategy {
	case StrategyExponential:
		baseDelay = rm.config.InitialDelay * time.Duration(math.Pow(rm.config.Multiplier, float64(attempt-1)))
	case StrategyLinear:
		baseDelay = rm.config.InitialDelay * time.Duration(attempt)
	case StrategyConstant:
		baseDelay = rm.config.InitialDelay
	case StrategyFibonacci:
		baseDelay = rm.config.InitialDelay * time.Duration(rm.fibonacci(attempt))
	default:
		baseDelay = rm.config.InitialDelay * time.Duration(math.Pow(rm.config.Multiplier, float64(attempt-1)))
	}

	// Apply maximum delay limit
	if baseDelay > rm.config.MaxDelay {
		baseDelay = rm.config.MaxDelay
	}

	// Apply jitter if enabled
	if rm.config.Jitter {
		baseDelay = rm.applyJitter(baseDelay)
	}

	return baseDelay
}

// fibonacci calculates the nth Fibonacci number
func (rm *RetryMechanism) fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return rm.fibonacci(n-1) + rm.fibonacci(n-2)
}

// applyJitter adds random jitter to the delay
func (rm *RetryMechanism) applyJitter(delay time.Duration) time.Duration {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	jitterRange := float64(delay) * rm.config.JitterFactor
	jitter := rm.rand.Float64() * jitterRange

	// Add or subtract jitter randomly
	if rm.rand.Float64() < 0.5 {
		jitter = -jitter
	}

	return delay + time.Duration(jitter)
}

// updateStats updates retry statistics
func (rm *RetryMechanism) updateStats(attempts, successful, failed int64, retryTime time.Duration) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	rm.stats.TotalAttempts += attempts
	rm.stats.SuccessfulRetries += successful
	rm.stats.FailedRetries += failed
	rm.stats.TotalRetryTime += retryTime

	if rm.stats.SuccessfulRetries > 0 {
		rm.stats.AverageRetryTime = rm.stats.TotalRetryTime / time.Duration(rm.stats.SuccessfulRetries)
	}

	if attempts > 0 {
		rm.stats.LastRetry = time.Now()
	}
}

// GetStats returns retry statistics
func (rm *RetryMechanism) GetStats() *RetryStats {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	return &RetryStats{
		TotalAttempts:     rm.stats.TotalAttempts,
		SuccessfulRetries: rm.stats.SuccessfulRetries,
		FailedRetries:     rm.stats.FailedRetries,
		TotalRetryTime:    rm.stats.TotalRetryTime,
		AverageRetryTime:  rm.stats.AverageRetryTime,
		LastRetry:         rm.stats.LastRetry,
		LastReset:         rm.stats.LastReset,
	}
}

// Reset resets retry statistics
func (rm *RetryMechanism) Reset() {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	rm.stats = &RetryStats{
		LastReset: time.Now(),
	}

	logger.Info("Retry mechanism statistics reset")
}

// RetryManager manages multiple retry mechanisms
type RetryManager struct {
	mechanisms map[string]*RetryMechanism
	mutex      sync.RWMutex
}

// NewRetryManager creates a new retry manager
func NewRetryManager() *RetryManager {
	return &RetryManager{
		mechanisms: make(map[string]*RetryMechanism),
	}
}

// GetOrCreate gets an existing retry mechanism or creates a new one
func (rm *RetryManager) GetOrCreate(name string, config RetryConfig) *RetryMechanism {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if mechanism, exists := rm.mechanisms[name]; exists {
		return mechanism
	}

	mechanism := NewRetryMechanism(config)
	rm.mechanisms[name] = mechanism

	return mechanism
}

// Get returns an existing retry mechanism
func (rm *RetryManager) Get(name string) (*RetryMechanism, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	mechanism, exists := rm.mechanisms[name]
	return mechanism, exists
}

// GetAll returns all retry mechanisms
func (rm *RetryManager) GetAll() map[string]*RetryMechanism {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	result := make(map[string]*RetryMechanism)
	for name, mechanism := range rm.mechanisms {
		result[name] = mechanism
	}

	return result
}

// GetStats returns statistics for all retry mechanisms
func (rm *RetryManager) GetStats() map[string]interface{} {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	stats := make(map[string]interface{})
	for name, mechanism := range rm.mechanisms {
		stats[name] = mechanism.GetStats()
	}

	return stats
}

// ResetAll resets all retry mechanisms
func (rm *RetryManager) ResetAll() {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	for name, mechanism := range rm.mechanisms {
		mechanism.Reset()
		logger.Debug("Reset retry mechanism: %s", name)
	}
}

// Predefined retry configurations
var (
	// DefaultRetryConfig provides sensible defaults
	DefaultRetryConfig = RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       true,
		JitterFactor: 0.1,
		Strategy:     StrategyExponential,
	}

	// AggressiveRetryConfig for critical operations
	AggressiveRetryConfig = RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   1.5,
		Jitter:       true,
		JitterFactor: 0.2,
		Strategy:     StrategyExponential,
	}

	// ConservativeRetryConfig for non-critical operations
	ConservativeRetryConfig = RetryConfig{
		MaxAttempts:  2,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     60 * time.Second,
		Multiplier:   3.0,
		Jitter:       false,
		Strategy:     StrategyLinear,
	}
)
