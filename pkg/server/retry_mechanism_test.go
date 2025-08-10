package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

func init() {
	// Initialize logger for testing
	logger.Init("debug")
}

func TestNewRetryMechanism(t *testing.T) {
	// Test with custom config
	config := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 200 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   1.5,
		Jitter:       true,
		JitterFactor: 0.2,
		Strategy:     StrategyLinear,
	}

	rm := NewRetryMechanism(config)

	if rm == nil {
		t.Fatal("NewRetryMechanism should not return nil")
	}

	if rm.config.MaxAttempts != 5 {
		t.Errorf("Expected MaxAttempts 5, got %d", rm.config.MaxAttempts)
	}
	if rm.config.InitialDelay != 200*time.Millisecond {
		t.Errorf("Expected InitialDelay 200ms, got %v", rm.config.InitialDelay)
	}
	if rm.config.Strategy != StrategyLinear {
		t.Errorf("Expected Strategy Linear, got %v", rm.config.Strategy)
	}
	if rm.stats == nil {
		t.Error("Stats should be initialized")
	}
	if rm.rand == nil {
		t.Error("Random number generator should be initialized")
	}
}

func TestNewRetryMechanism_Defaults(t *testing.T) {
	// Test with empty config (should use defaults)
	config := RetryConfig{}
	rm := NewRetryMechanism(config)

	if rm.config.MaxAttempts != 3 {
		t.Errorf("Expected default MaxAttempts 3, got %d", rm.config.MaxAttempts)
	}
	if rm.config.InitialDelay != 100*time.Millisecond {
		t.Errorf("Expected default InitialDelay 100ms, got %v", rm.config.InitialDelay)
	}
	if rm.config.MaxDelay != 30*time.Second {
		t.Errorf("Expected default MaxDelay 30s, got %v", rm.config.MaxDelay)
	}
	if rm.config.Multiplier != 2.0 {
		t.Errorf("Expected default Multiplier 2.0, got %f", rm.config.Multiplier)
	}
	if rm.config.JitterFactor != 0.1 {
		t.Errorf("Expected default JitterFactor 0.1, got %f", rm.config.JitterFactor)
	}
}

func TestRetryMechanism_ExecuteWithRetry_Success(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	attempts := 0

	operation := func() error {
		attempts++
		return nil // Success on first attempt
	}

	err := rm.ExecuteWithRetry(operation)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryMechanism_ExecuteWithRetry_EventuallySucceeds(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	attempts := 0

	operation := func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil // Success on third attempt
	}

	err := rm.ExecuteWithRetry(operation)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryMechanism_ExecuteWithRetry_MaxAttemptsReached(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  2,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	attempts := 0

	operation := func() error {
		attempts++
		return errors.New("persistent error")
	}

	err := rm.ExecuteWithRetry(operation)

	if err == nil {
		t.Error("Expected error after max attempts")
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
	if !strings.Contains(err.Error(), "operation failed after 2 attempts") {
		t.Errorf("Expected error message to contain attempt count, got: %v", err)
	}
}

func TestRetryMechanism_ExecuteWithRetry_NonRetryableError(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	attempts := 0

	operation := func() error {
		attempts++
		return errors.New("permanent error") // Not in retryable patterns
	}

	err := rm.ExecuteWithRetry(operation)

	if err == nil {
		t.Error("Expected error")
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt for non-retryable error, got %d", attempts)
	}
}

func TestRetryMechanism_ExecuteWithRetry_ContextCancelled(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	operation := func() error {
		attempts++
		if attempts == 2 {
			cancel() // Cancel context after second attempt
		}
		return errors.New("temporary error")
	}

	err := rm.ExecuteWithRetryContext(ctx, operation)

	if err == nil {
		t.Error("Expected error due to context cancellation")
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestRetryMechanism_ExecuteWithRetryAndBackoff_Success(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	attempts := 0

	operation := func() (interface{}, error) {
		attempts++
		return "success", nil
	}

	result, err := rm.ExecuteWithRetryAndBackoff(operation)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("Expected result 'success', got %v", result)
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetryMechanism_ExecuteWithRetryAndBackoff_EventuallySucceeds(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	attempts := 0

	operation := func() (interface{}, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("temporary error")
		}
		return "success", nil
	}

	result, err := rm.ExecuteWithRetryAndBackoff(operation)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("Expected result 'success', got %v", result)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetryMechanism_ExecuteWithRetryAndBackoff_ContextCancelled(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  5,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	operation := func() (interface{}, error) {
		attempts++
		if attempts == 2 {
			cancel() // Cancel context after second attempt
		}
		return nil, errors.New("temporary error")
	}

	result, err := rm.ExecuteWithRetryAndBackoffContext(ctx, operation)

	if err == nil {
		t.Error("Expected error due to context cancellation")
	}
	if result != nil {
		t.Errorf("Expected nil result, got %v", result)
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestRetryMechanism_isRetryableError(t *testing.T) {
	config := RetryConfig{
		RetryableErrors: []error{
			errors.New("custom retryable error"),
		},
	}

	rm := NewRetryMechanism(config)

	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "custom retryable error",
			err:      errors.New("custom retryable error"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "timeout error",
			err:      errors.New("timeout"),
			expected: true,
		},
		{
			name:     "temporary error",
			err:      errors.New("temporary"),
			expected: true,
		},
		{
			name:     "unavailable error",
			err:      errors.New("unavailable"),
			expected: true,
		},
		{
			name:     "rate limit error",
			err:      errors.New("rate limit"),
			expected: true,
		},
		{
			name:     "too many requests",
			err:      errors.New("too many requests"),
			expected: true,
		},
		{
			name:     "server error",
			err:      errors.New("server error"),
			expected: true,
		},
		{
			name:     "network error",
			err:      errors.New("network error"),
			expected: true,
		},
		{
			name:     "permanent error",
			err:      errors.New("permanent error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := rm.isRetryableError(tc.err)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for error: %v", tc.expected, result, tc.err)
			}
		})
	}
}

func TestRetryMechanism_containsIgnoreCase(t *testing.T) {
	config := RetryConfig{}
	rm := NewRetryMechanism(config)

	testCases := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{
			name:     "exact match",
			s:        "hello world",
			substr:   "hello",
			expected: true,
		},
		{
			name:     "case insensitive match",
			s:        "Hello World",
			substr:   "hello",
			expected: true,
		},
		{
			name:     "case insensitive match reverse",
			s:        "hello world",
			substr:   "Hello",
			expected: true,
		},
		{
			name:     "no match",
			s:        "hello world",
			substr:   "goodbye",
			expected: false,
		},
		{
			name:     "empty string",
			s:        "",
			substr:   "hello",
			expected: false,
		},
		{
			name:     "empty substring",
			s:        "hello world",
			substr:   "",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := rm.containsIgnoreCase(tc.s, tc.substr)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for s='%s', substr='%s'", tc.expected, result, tc.s, tc.substr)
			}
		})
	}
}

func TestRetryMechanism_toLower(t *testing.T) {
	config := RetryConfig{}
	rm := NewRetryMechanism(config)

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "all lowercase",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "mixed case",
			input:    "Hello World",
			expected: "hello world",
		},
		{
			name:     "all uppercase",
			input:    "HELLO WORLD",
			expected: "hello world",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "numbers and symbols",
			input:    "Hello123!@#",
			expected: "hello123!@#",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := rm.toLower(tc.input)
			if result != tc.expected {
				t.Errorf("Expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestRetryMechanism_calculateDelay(t *testing.T) {
	testCases := []struct {
		name        string
		config      RetryConfig
		attempt     int
		expectedMin time.Duration
		expectedMax time.Duration
	}{
		{
			name: "exponential strategy",
			config: RetryConfig{
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     1 * time.Second,
				Multiplier:   2.0,
				Strategy:     StrategyExponential,
				Jitter:       false,
			},
			attempt:     2,
			expectedMin: 200 * time.Millisecond,
			expectedMax: 200 * time.Millisecond,
		},
		{
			name: "linear strategy",
			config: RetryConfig{
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     1 * time.Second,
				Strategy:     StrategyLinear,
				Jitter:       false,
			},
			attempt:     3,
			expectedMin: 300 * time.Millisecond,
			expectedMax: 300 * time.Millisecond,
		},
		{
			name: "constant strategy",
			config: RetryConfig{
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     1 * time.Second,
				Strategy:     StrategyConstant,
				Jitter:       false,
			},
			attempt:     5,
			expectedMin: 100 * time.Millisecond,
			expectedMax: 100 * time.Millisecond,
		},
		{
			name: "fibonacci strategy",
			config: RetryConfig{
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     1 * time.Second,
				Strategy:     StrategyFibonacci,
				Jitter:       false,
			},
			attempt:     4,
			expectedMin: 300 * time.Millisecond, // fib(4) = 3
			expectedMax: 300 * time.Millisecond,
		},
		{
			name: "max delay limit",
			config: RetryConfig{
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     200 * time.Millisecond,
				Multiplier:   2.0,
				Strategy:     StrategyExponential,
				Jitter:       false,
			},
			attempt:     5,
			expectedMin: 200 * time.Millisecond, // Should be capped at MaxDelay
			expectedMax: 200 * time.Millisecond,
		},
		{
			name: "with jitter",
			config: RetryConfig{
				InitialDelay: 100 * time.Millisecond,
				MaxDelay:     1 * time.Second,
				Multiplier:   2.0,
				Strategy:     StrategyExponential,
				Jitter:       true,
				JitterFactor: 0.1,
			},
			attempt:     2,
			expectedMin: 180 * time.Millisecond, // 200ms - 10% jitter
			expectedMax: 220 * time.Millisecond, // 200ms + 10% jitter
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rm := NewRetryMechanism(tc.config)
			delay := rm.calculateDelay(tc.attempt)

			if delay < tc.expectedMin || delay > tc.expectedMax {
				t.Errorf("Expected delay between %v and %v, got %v", tc.expectedMin, tc.expectedMax, delay)
			}
		})
	}
}

func TestRetryMechanism_fibonacci(t *testing.T) {
	config := RetryConfig{}
	rm := NewRetryMechanism(config)

	testCases := []struct {
		name     string
		n        int
		expected int
	}{
		{name: "fib(0)", n: 0, expected: 0},
		{name: "fib(1)", n: 1, expected: 1},
		{name: "fib(2)", n: 2, expected: 1},
		{name: "fib(3)", n: 3, expected: 2},
		{name: "fib(4)", n: 4, expected: 3},
		{name: "fib(5)", n: 5, expected: 5},
		{name: "fib(6)", n: 6, expected: 8},
		{name: "fib(7)", n: 7, expected: 13},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := rm.fibonacci(tc.n)
			if result != tc.expected {
				t.Errorf("Expected fib(%d) = %d, got %d", tc.n, tc.expected, result)
			}
		})
	}
}

func TestRetryMechanism_applyJitter(t *testing.T) {
	config := RetryConfig{
		JitterFactor: 0.1,
	}
	rm := NewRetryMechanism(config)

	baseDelay := 100 * time.Millisecond
	jitteredDelay := rm.applyJitter(baseDelay)

	// Jitter should be within ±10% of base delay
	minDelay := baseDelay - time.Duration(float64(baseDelay)*0.1)
	maxDelay := baseDelay + time.Duration(float64(baseDelay)*0.1)

	if jitteredDelay < minDelay || jitteredDelay > maxDelay {
		t.Errorf("Expected jittered delay between %v and %v, got %v", minDelay, maxDelay, jitteredDelay)
	}
}

func TestRetryMechanism_GetStats(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)

	// Execute some operations to generate stats
	operation := func() error {
		return errors.New("temporary error")
	}

	rm.ExecuteWithRetry(operation)

	stats := rm.GetStats()

	if stats.TotalAttempts == 0 {
		t.Error("Expected TotalAttempts to be greater than 0")
	}
	if stats.FailedRetries == 0 {
		t.Error("Expected FailedRetries to be greater than 0")
	}
	if stats.LastRetry.IsZero() {
		t.Error("Expected LastRetry to be set")
	}
	if stats.LastReset.IsZero() {
		t.Error("Expected LastReset to be set")
	}
}

func TestRetryMechanism_Reset(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
	}

	rm := NewRetryMechanism(config)

	// Execute some operations to generate stats
	operation := func() error {
		return errors.New("temporary error")
	}

	rm.ExecuteWithRetry(operation)

	// Reset stats
	rm.Reset()

	stats := rm.GetStats()

	if stats.TotalAttempts != 0 {
		t.Errorf("Expected TotalAttempts 0 after reset, got %d", stats.TotalAttempts)
	}
	if stats.SuccessfulRetries != 0 {
		t.Errorf("Expected SuccessfulRetries 0 after reset, got %d", stats.SuccessfulRetries)
	}
	if stats.FailedRetries != 0 {
		t.Errorf("Expected FailedRetries 0 after reset, got %d", stats.FailedRetries)
	}
	if stats.TotalRetryTime != 0 {
		t.Errorf("Expected TotalRetryTime 0 after reset, got %v", stats.TotalRetryTime)
	}
	if stats.AverageRetryTime != 0 {
		t.Errorf("Expected AverageRetryTime 0 after reset, got %v", stats.AverageRetryTime)
	}
}

func TestNewRetryManager(t *testing.T) {
	rm := NewRetryManager()

	if rm == nil {
		t.Fatal("NewRetryManager should not return nil")
	}
	if rm.mechanisms == nil {
		t.Error("Mechanisms map should be initialized")
	}
}

func TestRetryManager_GetOrCreate(t *testing.T) {
	rm := NewRetryManager()
	config := RetryConfig{
		MaxAttempts: 3,
		Strategy:    StrategyExponential,
	}

	// Create new mechanism
	mechanism1 := rm.GetOrCreate("test", config)

	if mechanism1 == nil {
		t.Fatal("GetOrCreate should not return nil")
	}

	// Get existing mechanism
	mechanism2 := rm.GetOrCreate("test", config)

	if mechanism1 != mechanism2 {
		t.Error("GetOrCreate should return the same mechanism for the same name")
	}
}

func TestRetryManager_Get(t *testing.T) {
	rm := NewRetryManager()
	config := RetryConfig{
		MaxAttempts: 3,
		Strategy:    StrategyExponential,
	}

	// Create mechanism
	rm.GetOrCreate("test", config)

	// Get existing mechanism
	mechanism, exists := rm.Get("test")

	if !exists {
		t.Error("Expected mechanism to exist")
	}
	if mechanism == nil {
		t.Error("Expected mechanism to not be nil")
	}

	// Get non-existent mechanism
	mechanism, exists = rm.Get("nonexistent")

	if exists {
		t.Error("Expected mechanism to not exist")
	}
	if mechanism != nil {
		t.Error("Expected mechanism to be nil")
	}
}

func TestRetryManager_GetAll(t *testing.T) {
	rm := NewRetryManager()
	config := RetryConfig{
		MaxAttempts: 3,
		Strategy:    StrategyExponential,
	}

	// Create multiple mechanisms
	rm.GetOrCreate("test1", config)
	rm.GetOrCreate("test2", config)

	all := rm.GetAll()

	if len(all) != 2 {
		t.Errorf("Expected 2 mechanisms, got %d", len(all))
	}
	if all["test1"] == nil {
		t.Error("Expected test1 mechanism to exist")
	}
	if all["test2"] == nil {
		t.Error("Expected test2 mechanism to exist")
	}
}

func TestRetryManager_GetStats(t *testing.T) {
	rm := NewRetryManager()
	config := RetryConfig{
		MaxAttempts: 3,
		Strategy:    StrategyExponential,
	}

	// Create mechanism and execute some operations
	mechanism := rm.GetOrCreate("test", config)
	operation := func() error {
		return errors.New("temporary error")
	}
	mechanism.ExecuteWithRetry(operation)

	stats := rm.GetStats()

	if len(stats) != 1 {
		t.Errorf("Expected 1 mechanism in stats, got %d", len(stats))
	}
	if stats["test"] == nil {
		t.Error("Expected test mechanism stats to exist")
	}
}

func TestRetryManager_ResetAll(t *testing.T) {
	rm := NewRetryManager()
	config := RetryConfig{
		MaxAttempts: 3,
		Strategy:    StrategyExponential,
	}

	// Create mechanism and execute some operations
	mechanism := rm.GetOrCreate("test", config)
	operation := func() error {
		return errors.New("temporary error")
	}
	mechanism.ExecuteWithRetry(operation)

	// Reset all mechanisms
	rm.ResetAll()

	// Check that stats were reset
	stats := rm.GetStats()
	if len(stats) != 1 {
		t.Errorf("Expected 1 mechanism in stats, got %d", len(stats))
	}

	mechanismStats := stats["test"].(*RetryStats)
	if mechanismStats.TotalAttempts != 0 {
		t.Errorf("Expected TotalAttempts 0 after reset, got %d", mechanismStats.TotalAttempts)
	}
}

func TestPredefinedRetryConfigs(t *testing.T) {
	// Test DefaultRetryConfig
	if DefaultRetryConfig.MaxAttempts != 3 {
		t.Errorf("Expected DefaultRetryConfig.MaxAttempts 3, got %d", DefaultRetryConfig.MaxAttempts)
	}
	if DefaultRetryConfig.Strategy != StrategyExponential {
		t.Errorf("Expected DefaultRetryConfig.Strategy Exponential, got %v", DefaultRetryConfig.Strategy)
	}

	// Test AggressiveRetryConfig
	if AggressiveRetryConfig.MaxAttempts != 5 {
		t.Errorf("Expected AggressiveRetryConfig.MaxAttempts 5, got %d", AggressiveRetryConfig.MaxAttempts)
	}
	if AggressiveRetryConfig.Strategy != StrategyExponential {
		t.Errorf("Expected AggressiveRetryConfig.Strategy Exponential, got %v", AggressiveRetryConfig.Strategy)
	}

	// Test ConservativeRetryConfig
	if ConservativeRetryConfig.MaxAttempts != 2 {
		t.Errorf("Expected ConservativeRetryConfig.MaxAttempts 2, got %d", ConservativeRetryConfig.MaxAttempts)
	}
	if ConservativeRetryConfig.Strategy != StrategyLinear {
		t.Errorf("Expected ConservativeRetryConfig.Strategy Linear, got %v", ConservativeRetryConfig.Strategy)
	}
}

func TestRetryMechanism_ConcurrentAccess(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Strategy:     StrategyExponential,
		Jitter:       true,
	}

	rm := NewRetryMechanism(config)

	// Test concurrent access to stats
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			operation := func() error {
				return errors.New("temporary error")
			}
			rm.ExecuteWithRetry(operation)
		}()
	}

	wg.Wait()

	// Verify stats were updated correctly
	stats := rm.GetStats()
	if stats.TotalAttempts == 0 {
		t.Error("Expected TotalAttempts to be greater than 0")
	}
}

func TestRetryManager_ConcurrentAccess(t *testing.T) {
	rm := NewRetryManager()
	config := RetryConfig{
		MaxAttempts: 3,
		Strategy:    StrategyExponential,
	}

	// Test concurrent access to manager
	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("test-%d", id)
			mechanism := rm.GetOrCreate(name, config)
			mechanism.ExecuteWithRetry(func() error {
				return errors.New("temporary error")
			})
		}(i)
	}

	wg.Wait()

	// Verify all mechanisms were created
	all := rm.GetAll()
	if len(all) != numGoroutines {
		t.Errorf("Expected %d mechanisms, got %d", numGoroutines, len(all))
	}
}
