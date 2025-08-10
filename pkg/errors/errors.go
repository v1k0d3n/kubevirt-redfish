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

package errors

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// ErrorType represents the type of error
type ErrorType string

const (
	// ErrorTypeValidation represents validation errors
	ErrorTypeValidation ErrorType = "ValidationError"
	// ErrorTypeAuthentication represents authentication errors
	ErrorTypeAuthentication ErrorType = "AuthenticationError"
	// ErrorTypeAuthorization represents authorization errors
	ErrorTypeAuthorization ErrorType = "AuthorizationError"
	// ErrorTypeNotFound represents resource not found errors
	ErrorTypeNotFound ErrorType = "NotFoundError"
	// ErrorTypeConflict represents conflict errors (e.g., resource already exists)
	ErrorTypeConflict ErrorType = "ConflictError"
	// ErrorTypeInternal represents internal server errors
	ErrorTypeInternal ErrorType = "InternalError"
	// ErrorTypeKubeVirt represents KubeVirt-specific errors
	ErrorTypeKubeVirt ErrorType = "KubeVirtError"
	// ErrorTypeRedfish represents Redfish-specific errors
	ErrorTypeRedfish ErrorType = "RedfishError"
	// ErrorTypeNetwork represents network-related errors
	ErrorTypeNetwork ErrorType = "NetworkError"
	// ErrorTypeTimeout represents timeout errors
	ErrorTypeTimeout ErrorType = "TimeoutError"
	// ErrorTypeRetryable represents errors that can be retried
	ErrorTypeRetryable ErrorType = "RetryableError"
)

// RedfishError represents a structured error with Redfish-specific information
type RedfishError struct {
	Type          ErrorType `json:"type"`
	Code          string    `json:"code"`
	Message       string    `json:"message"`
	Details       string    `json:"details,omitempty"`
	HTTPStatus    int       `json:"http_status"`
	Retryable     bool      `json:"retryable"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Resource      string    `json:"resource,omitempty"`
	Operation     string    `json:"operation,omitempty"`
	Namespace     string    `json:"namespace,omitempty"`
	Err           error     `json:"-"` // Original error (not serialized)
}

// Error implements the error interface
func (e *RedfishError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying error
func (e *RedfishError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true if the error can be retried
func (e *RedfishError) IsRetryable() bool {
	return e.Retryable
}

// GetHTTPStatus returns the HTTP status code for this error
func (e *RedfishError) GetHTTPStatus() int {
	return e.HTTPStatus
}

// NewValidationError creates a new validation error
func NewValidationError(message, details string) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeValidation,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		Details:    details,
		HTTPStatus: http.StatusBadRequest,
		Retryable:  false,
	}
}

// NewAuthenticationError creates a new authentication error
func NewAuthenticationError(message string) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeAuthentication,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		HTTPStatus: http.StatusUnauthorized,
		Retryable:  false,
	}
}

// NewAuthorizationError creates a new authorization error
func NewAuthorizationError(message string) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeAuthorization,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		HTTPStatus: http.StatusForbidden,
		Retryable:  false,
	}
}

// NewNotFoundError creates a new not found error
func NewNotFoundError(resource, details string) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeNotFound,
		Code:       "Base.1.0.GeneralError",
		Message:    fmt.Sprintf("Resource not found: %s", resource),
		Details:    details,
		HTTPStatus: http.StatusNotFound,
		Retryable:  false,
		Resource:   resource,
	}
}

// NewConflictError creates a new conflict error
func NewConflictError(resource, details string) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeConflict,
		Code:       "Base.1.0.GeneralError",
		Message:    fmt.Sprintf("Resource conflict: %s", resource),
		Details:    details,
		HTTPStatus: http.StatusConflict,
		Retryable:  false,
		Resource:   resource,
	}
}

// NewInternalError creates a new internal error
func NewInternalError(message string, err error) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeInternal,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		Details:    err.Error(),
		HTTPStatus: http.StatusInternalServerError,
		Retryable:  false,
		Err:        err,
	}
}

// NewKubeVirtError creates a new KubeVirt-specific error
func NewKubeVirtError(operation, namespace, resource, message string, err error) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeKubeVirt,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		Details:    err.Error(),
		HTTPStatus: http.StatusInternalServerError,
		Retryable:  true, // KubeVirt errors are often retryable
		Operation:  operation,
		Namespace:  namespace,
		Resource:   resource,
		Err:        err,
	}
}

// NewNetworkError creates a new network error
func NewNetworkError(message string, err error) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeNetwork,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		Details:    err.Error(),
		HTTPStatus: http.StatusServiceUnavailable,
		Retryable:  true, // Network errors are retryable
		Err:        err,
	}
}

// NewTimeoutError creates a new timeout error
func NewTimeoutError(operation string, timeout time.Duration) *RedfishError {
	return &RedfishError{
		Type:       ErrorTypeTimeout,
		Code:       "Base.1.0.GeneralError",
		Message:    fmt.Sprintf("Operation timed out: %s", operation),
		Details:    fmt.Sprintf("Timeout after %v", timeout),
		HTTPStatus: http.StatusRequestTimeout,
		Retryable:  true, // Timeout errors are retryable
		Operation:  operation,
	}
}

// WithCorrelationID adds a correlation ID to an error
func (e *RedfishError) WithCorrelationID(correlationID string) *RedfishError {
	e.CorrelationID = correlationID
	return e
}

// WithContext adds context information to an error
func (e *RedfishError) WithContext(operation, namespace, resource string) *RedfishError {
	e.Operation = operation
	e.Namespace = namespace
	e.Resource = resource
	return e
}

// LogError logs an error with structured logging
func LogError(err error, correlationID string) {
	if redfishErr, ok := err.(*RedfishError); ok {
		fields := map[string]interface{}{
			"correlation_id": correlationID,
			"error_type":     string(redfishErr.Type),
			"error_code":     redfishErr.Code,
			"http_status":    redfishErr.HTTPStatus,
			"retryable":      redfishErr.Retryable,
		}

		if redfishErr.Operation != "" {
			fields["operation"] = redfishErr.Operation
		}
		if redfishErr.Namespace != "" {
			fields["namespace"] = redfishErr.Namespace
		}
		if redfishErr.Resource != "" {
			fields["resource"] = redfishErr.Resource
		}
		if redfishErr.Details != "" {
			fields["details"] = redfishErr.Details
		}

		logger.ErrorStructured(redfishErr.Message, fields)
	} else {
		fields := map[string]interface{}{
			"correlation_id": correlationID,
			"error_type":     "UnknownError",
			"details":        err.Error(),
		}
		logger.ErrorStructured("Unknown error occurred", fields)
	}
}

// RetryConfig defines retry behavior
type RetryConfig struct {
	MaxAttempts   int           `json:"max_attempts"`
	InitialDelay  time.Duration `json:"initial_delay"`
	MaxDelay      time.Duration `json:"max_delay"`
	BackoffFactor float64       `json:"backoff_factor"`
}

// DefaultRetryConfig returns a default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		BackoffFactor: 2.0,
	}
}

// RetryableFunc represents a function that can be retried
type RetryableFunc func() error

// Retry executes a function with retry logic
func Retry(ctx context.Context, config *RetryConfig, fn RetryableFunc) error {
	if config == nil {
		config = DefaultRetryConfig()
	}

	var lastErr error
	delay := config.InitialDelay

	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			return NewTimeoutError("Retry operation", 0*time.Second).WithCorrelationID(logger.GetCorrelationID(ctx))
		default:
		}

		// Execute the function
		err := fn()
		if err == nil {
			// Success, no need to retry
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if redfishErr, ok := err.(*RedfishError); ok && !redfishErr.IsRetryable() {
			// Non-retryable error, return immediately
			return err
		}

		// If this is the last attempt, return the error
		if attempt == config.MaxAttempts {
			break
		}

		// Log retry attempt
		fields := map[string]interface{}{
			"correlation_id": logger.GetCorrelationID(ctx),
			"attempt":        attempt,
			"max_attempts":   config.MaxAttempts,
			"delay":          delay.String(),
			"error":          err.Error(),
		}
		logger.WarningStructured("Retrying operation", fields)

		// Wait before retrying
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return NewTimeoutError("Retry operation", 0*time.Second).WithCorrelationID(logger.GetCorrelationID(ctx))
		}

		// Calculate next delay with exponential backoff
		delay = time.Duration(float64(delay) * config.BackoffFactor)
		if delay > config.MaxDelay {
			delay = config.MaxDelay
		}
	}

	// All attempts failed
	fields := map[string]interface{}{
		"correlation_id": logger.GetCorrelationID(ctx),
		"attempts":       config.MaxAttempts,
		"final_error":    lastErr.Error(),
	}
	logger.ErrorStructured("All retry attempts failed", fields)

	return lastErr
}

// IsRetryableError checks if an error is retryable
func IsRetryableError(err error) bool {
	if redfishErr, ok := err.(*RedfishError); ok {
		return redfishErr.IsRetryable()
	}
	return false
}

// GetHTTPStatus returns the HTTP status code for an error
func GetHTTPStatus(err error) int {
	if redfishErr, ok := err.(*RedfishError); ok {
		return redfishErr.GetHTTPStatus()
	}
	return http.StatusInternalServerError
}

// WrapError wraps an existing error with Redfish error information
func WrapError(err error, errorType ErrorType, message string) *RedfishError {
	redfishErr := &RedfishError{
		Type:       errorType,
		Code:       "Base.1.0.GeneralError",
		Message:    message,
		Details:    err.Error(),
		HTTPStatus: http.StatusInternalServerError,
		Retryable:  false,
		Err:        err,
	}

	// Set appropriate HTTP status based on error type
	switch errorType {
	case ErrorTypeValidation:
		redfishErr.HTTPStatus = http.StatusBadRequest
	case ErrorTypeAuthentication:
		redfishErr.HTTPStatus = http.StatusUnauthorized
	case ErrorTypeAuthorization:
		redfishErr.HTTPStatus = http.StatusForbidden
	case ErrorTypeNotFound:
		redfishErr.HTTPStatus = http.StatusNotFound
	case ErrorTypeConflict:
		redfishErr.HTTPStatus = http.StatusConflict
	case ErrorTypeTimeout:
		redfishErr.HTTPStatus = http.StatusRequestTimeout
		redfishErr.Retryable = true
	case ErrorTypeNetwork:
		redfishErr.HTTPStatus = http.StatusServiceUnavailable
		redfishErr.Retryable = true
	case ErrorTypeKubeVirt:
		redfishErr.Retryable = true
	}

	return redfishErr
}
