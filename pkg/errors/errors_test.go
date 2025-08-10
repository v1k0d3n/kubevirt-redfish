package errors

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRedfishError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *RedfishError
		expected string
	}{
		{
			name: "error with details",
			err: &RedfishError{
				Code:    "Base.1.0.GeneralError",
				Message: "Test error",
				Details: "Additional details",
			},
			expected: "Base.1.0.GeneralError: Test error (Additional details)",
		},
		{
			name: "error without details",
			err: &RedfishError{
				Code:    "Base.1.0.GeneralError",
				Message: "Test error",
			},
			expected: "Base.1.0.GeneralError: Test error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()
			if result != tt.expected {
				t.Errorf("Error() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRedfishError_Unwrap(t *testing.T) {
	originalErr := errors.New("original error")
	redfishErr := &RedfishError{
		Code:    "Base.1.0.GeneralError",
		Message: "Test error",
		Err:     originalErr,
	}

	result := redfishErr.Unwrap()
	if result != originalErr {
		t.Errorf("Unwrap() = %v, want %v", result, originalErr)
	}
}

func TestRedfishError_IsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      *RedfishError
		expected bool
	}{
		{
			name: "retryable error",
			err: &RedfishError{
				Retryable: true,
			},
			expected: true,
		},
		{
			name: "non-retryable error",
			err: &RedfishError{
				Retryable: false,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.IsRetryable()
			if result != tt.expected {
				t.Errorf("IsRetryable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRedfishError_GetHTTPStatus(t *testing.T) {
	expectedStatus := http.StatusBadRequest
	err := &RedfishError{
		HTTPStatus: expectedStatus,
	}

	result := err.GetHTTPStatus()
	if result != expectedStatus {
		t.Errorf("GetHTTPStatus() = %v, want %v", result, expectedStatus)
	}
}

func TestNewValidationError(t *testing.T) {
	message := "Invalid input"
	details := "Field 'name' is required"

	err := NewValidationError(message, details)

	if err.Type != ErrorTypeValidation {
		t.Errorf("Expected type %s, got %s", ErrorTypeValidation, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %s, got %s", message, err.Message)
	}
	if err.Details != details {
		t.Errorf("Expected details %s, got %s", details, err.Details)
	}
	if err.HTTPStatus != http.StatusBadRequest {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusBadRequest, err.HTTPStatus)
	}
	if err.Retryable {
		t.Error("Validation errors should not be retryable")
	}
}

func TestNewAuthenticationError(t *testing.T) {
	message := "Invalid credentials"

	err := NewAuthenticationError(message)

	if err.Type != ErrorTypeAuthentication {
		t.Errorf("Expected type %s, got %s", ErrorTypeAuthentication, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %s, got %s", message, err.Message)
	}
	if err.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusUnauthorized, err.HTTPStatus)
	}
	if err.Retryable {
		t.Error("Authentication errors should not be retryable")
	}
}

func TestNewAuthorizationError(t *testing.T) {
	message := "Access denied"

	err := NewAuthorizationError(message)

	if err.Type != ErrorTypeAuthorization {
		t.Errorf("Expected type %s, got %s", ErrorTypeAuthorization, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %s, got %s", message, err.Message)
	}
	if err.HTTPStatus != http.StatusForbidden {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusForbidden, err.HTTPStatus)
	}
	if err.Retryable {
		t.Error("Authorization errors should not be retryable")
	}
}

func TestNewNotFoundError(t *testing.T) {
	resource := "VM"
	details := "VM 'test-vm' not found"

	err := NewNotFoundError(resource, details)

	if err.Type != ErrorTypeNotFound {
		t.Errorf("Expected type %s, got %s", ErrorTypeNotFound, err.Type)
	}
	if err.Resource != resource {
		t.Errorf("Expected resource %s, got %s", resource, err.Resource)
	}
	if err.Details != details {
		t.Errorf("Expected details %s, got %s", details, err.Details)
	}
	if err.HTTPStatus != http.StatusNotFound {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusNotFound, err.HTTPStatus)
	}
	if err.Retryable {
		t.Error("Not found errors should not be retryable")
	}
}

func TestNewConflictError(t *testing.T) {
	resource := "VM"
	details := "VM 'test-vm' already exists"

	err := NewConflictError(resource, details)

	if err.Type != ErrorTypeConflict {
		t.Errorf("Expected type %s, got %s", ErrorTypeConflict, err.Type)
	}
	if err.Resource != resource {
		t.Errorf("Expected resource %s, got %s", resource, err.Resource)
	}
	if err.Details != details {
		t.Errorf("Expected details %s, got %s", details, err.Details)
	}
	if err.HTTPStatus != http.StatusConflict {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusConflict, err.HTTPStatus)
	}
	if err.Retryable {
		t.Error("Conflict errors should not be retryable")
	}
}

func TestNewInternalError(t *testing.T) {
	message := "Internal server error"
	originalErr := errors.New("database connection failed")

	err := NewInternalError(message, originalErr)

	if err.Type != ErrorTypeInternal {
		t.Errorf("Expected type %s, got %s", ErrorTypeInternal, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %s, got %s", message, err.Message)
	}
	if err.Err != originalErr {
		t.Errorf("Expected original error %v, got %v", originalErr, err.Err)
	}
	if err.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusInternalServerError, err.HTTPStatus)
	}
	if err.Retryable {
		t.Error("Internal errors should not be retryable")
	}
}

func TestNewKubeVirtError(t *testing.T) {
	operation := "CreateVM"
	namespace := "default"
	resource := "VirtualMachine"
	message := "Failed to create VM"
	originalErr := errors.New("KubeVirt API error")

	err := NewKubeVirtError(operation, namespace, resource, message, originalErr)

	if err.Type != ErrorTypeKubeVirt {
		t.Errorf("Expected type %s, got %s", ErrorTypeKubeVirt, err.Type)
	}
	if err.Operation != operation {
		t.Errorf("Expected operation %s, got %s", operation, err.Operation)
	}
	if err.Namespace != namespace {
		t.Errorf("Expected namespace %s, got %s", namespace, err.Namespace)
	}
	if err.Resource != resource {
		t.Errorf("Expected resource %s, got %s", resource, err.Resource)
	}
	if err.Message != message {
		t.Errorf("Expected message %s, got %s", message, err.Message)
	}
	if err.Err != originalErr {
		t.Errorf("Expected original error %v, got %v", originalErr, err.Err)
	}
	if err.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusInternalServerError, err.HTTPStatus)
	}
	if !err.Retryable {
		t.Error("KubeVirt errors should be retryable")
	}
}

func TestNewNetworkError(t *testing.T) {
	message := "Network connection failed"
	originalErr := errors.New("connection timeout")

	err := NewNetworkError(message, originalErr)

	if err.Type != ErrorTypeNetwork {
		t.Errorf("Expected type %s, got %s", ErrorTypeNetwork, err.Type)
	}
	if err.Message != message {
		t.Errorf("Expected message %s, got %s", message, err.Message)
	}
	if err.Err != originalErr {
		t.Errorf("Expected original error %v, got %v", originalErr, err.Err)
	}
	if err.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusServiceUnavailable, err.HTTPStatus)
	}
	if !err.Retryable {
		t.Error("Network errors should be retryable")
	}
}

func TestNewTimeoutError(t *testing.T) {
	operation := "CreateVM"
	timeout := 30 * time.Second

	err := NewTimeoutError(operation, timeout)

	if err.Type != ErrorTypeTimeout {
		t.Errorf("Expected type %s, got %s", ErrorTypeTimeout, err.Type)
	}
	if err.Operation != operation {
		t.Errorf("Expected operation %s, got %s", operation, err.Operation)
	}
	if err.HTTPStatus != http.StatusRequestTimeout {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusRequestTimeout, err.HTTPStatus)
	}
	if !err.Retryable {
		t.Error("Timeout errors should be retryable")
	}
}

func TestRedfishError_WithCorrelationID(t *testing.T) {
	err := &RedfishError{
		Code:    "Base.1.0.GeneralError",
		Message: "Test error",
	}

	correlationID := "test-correlation-id"
	result := err.WithCorrelationID(correlationID)

	if result.CorrelationID != correlationID {
		t.Errorf("Expected correlation ID %s, got %s", correlationID, result.CorrelationID)
	}
	// Original error should be modified (method modifies in place)
	if err.CorrelationID != correlationID {
		t.Error("Original error should be modified")
	}
}

func TestRedfishError_WithContext(t *testing.T) {
	err := &RedfishError{
		Code:    "Base.1.0.GeneralError",
		Message: "Test error",
	}

	operation := "CreateVM"
	namespace := "default"
	resource := "VirtualMachine"

	result := err.WithContext(operation, namespace, resource)

	if result.Operation != operation {
		t.Errorf("Expected operation %s, got %s", operation, result.Operation)
	}
	if result.Namespace != namespace {
		t.Errorf("Expected namespace %s, got %s", namespace, result.Namespace)
	}
	if result.Resource != resource {
		t.Errorf("Expected resource %s, got %s", resource, result.Resource)
	}
	// Original error should be modified (method modifies in place)
	if err.Operation != operation || err.Namespace != namespace || err.Resource != resource {
		t.Error("Original error should be modified")
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	if config.MaxAttempts != 3 {
		t.Errorf("Expected MaxAttempts %d, got %d", 3, config.MaxAttempts)
	}
	if config.InitialDelay != 100*time.Millisecond {
		t.Errorf("Expected InitialDelay %v, got %v", 100*time.Millisecond, config.InitialDelay)
	}
	if config.MaxDelay != 5*time.Second {
		t.Errorf("Expected MaxDelay %v, got %v", 5*time.Second, config.MaxDelay)
	}
	if config.BackoffFactor != 2.0 {
		t.Errorf("Expected BackoffFactor %f, got %f", 2.0, config.BackoffFactor)
	}
}

func TestRetry_Success(t *testing.T) {
	config := &RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
	}

	attempts := 0
	fn := func() error {
		attempts++
		return nil // Success on first attempt
	}

	ctx := context.Background()
	err := Retry(ctx, config, fn)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", attempts)
	}
}

func TestRetry_EventuallySucceeds(t *testing.T) {
	config := &RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
	}

	attempts := 0
	fn := func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil // Success on third attempt
	}

	ctx := context.Background()
	err := Retry(ctx, config, fn)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestRetry_ContextCancelled(t *testing.T) {
	config := &RetryConfig{
		MaxAttempts:   5,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      1 * time.Second,
		BackoffFactor: 2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	fn := func() error {
		attempts++
		if attempts == 2 {
			cancel() // Cancel context after second attempt
		}
		return errors.New("temporary error")
	}

	err := Retry(ctx, config, fn)

	if err == nil {
		t.Error("Expected error due to context cancellation")
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "retryable error",
			err:      &RedfishError{Retryable: true},
			expected: true,
		},
		{
			name:     "non-retryable error",
			err:      &RedfishError{Retryable: false},
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "standard error",
			err:      errors.New("standard error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryableError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetHTTPStatus(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "redfish error with status",
			err:      &RedfishError{HTTPStatus: http.StatusBadRequest},
			expected: http.StatusBadRequest,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: http.StatusInternalServerError,
		},
		{
			name:     "standard error",
			err:      errors.New("standard error"),
			expected: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetHTTPStatus(tt.err)
			if result != tt.expected {
				t.Errorf("GetHTTPStatus() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestWrapError(t *testing.T) {
	originalErr := errors.New("original error")
	message := "wrapped error"

	result := WrapError(originalErr, ErrorTypeInternal, message)

	if result.Type != ErrorTypeInternal {
		t.Errorf("Expected type %s, got %s", ErrorTypeInternal, result.Type)
	}
	if result.Message != message {
		t.Errorf("Expected message %s, got %s", message, result.Message)
	}
	if result.Err != originalErr {
		t.Errorf("Expected original error %v, got %v", originalErr, result.Err)
	}
	if result.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusInternalServerError, result.HTTPStatus)
	}
}
