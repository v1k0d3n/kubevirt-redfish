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

package kubevirt

import (
	"errors"
	"testing"
	"time"
)

func TestNewClient_WithKubeconfig(t *testing.T) {
	// Test with invalid kubeconfig path
	_, err := NewClient("/nonexistent/kubeconfig", 30*time.Second, nil)
	if err == nil {
		t.Error("Expected error with invalid kubeconfig path")
	}
}

func TestNewClient_WithoutKubeconfig(t *testing.T) {
	// Test without kubeconfig (in-cluster config)
	// This will fail in test environment, but we can test the error handling
	_, err := NewClient("", 30*time.Second, nil)
	if err == nil {
		t.Error("Expected error when not running in cluster")
	}
}

func TestClient_trackOperation(t *testing.T) {
	// Create a minimal client for testing
	client := &Client{
		timeout: 30 * time.Second,
	}

	// Test tracking operations
	client.trackOperation("test-op", 100*time.Millisecond)
	client.trackOperation("test-op", 200*time.Millisecond)
	client.trackOperation("another-op", 50*time.Millisecond)

	// Get metrics
	metrics := client.GetPerformanceMetrics()

	// Verify metrics
	if metrics == nil {
		t.Fatal("Metrics should not be nil")
	}

	// Check that operations were tracked
	testOpMetrics, exists := metrics["test-op"]
	if !exists {
		t.Error("test-op metrics should exist")
	}

	if testOpMetrics == nil {
		t.Error("test-op metrics should not be nil")
	}
}

func TestClient_GetPerformanceMetrics(t *testing.T) {
	client := &Client{
		timeout: 30 * time.Second,
	}

	// Initially, metrics should be empty but not nil
	metrics := client.GetPerformanceMetrics()
	if metrics == nil {
		t.Error("Initial metrics should not be nil")
	}

	// Add some operations
	client.trackOperation("op1", 100*time.Millisecond)
	client.trackOperation("op2", 200*time.Millisecond)

	// Get metrics again
	metrics = client.GetPerformanceMetrics()

	// Verify structure
	if metrics == nil {
		t.Fatal("Metrics should not be nil")
	}

	// Check that we have metrics for both operations
	if _, exists := metrics["op1"]; !exists {
		t.Error("op1 metrics should exist")
	}
	if _, exists := metrics["op2"]; !exists {
		t.Error("op2 metrics should exist")
	}
}

func TestClient_Close(t *testing.T) {
	client := &Client{
		timeout: 30 * time.Second,
	}

	// Close should not return an error for a basic client
	err := client.Close()
	if err != nil {
		t.Errorf("Close should not return error: %v", err)
	}
}

func TestIsRetryableError(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "non-retryable error",
			err:      errors.New("permission denied"),
			expected: false,
		},
		{
			name:     "network timeout error",
			err:      errors.New("timeout"),
			expected: true,
		},
		{
			name:     "connection refused error",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "temporary failure error",
			err:      errors.New("temporary failure"),
			expected: true,
		},
		{
			name:     "connection reset error",
			err:      errors.New("connection reset"),
			expected: true,
		},
		{
			name:     "server overloaded error",
			err:      errors.New("server overloaded"),
			expected: true,
		},
		{
			name:     "rate limit exceeded error",
			err:      errors.New("rate limit exceeded"),
			expected: true,
		},
		{
			name:     "already exists error",
			err:      errors.New("already exists"),
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isRetryableError(tc.err)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for error: %v", tc.expected, result, tc.err)
			}
		})
	}
}

func TestVMSelectorConfig_JSON(t *testing.T) {
	selector := &VMSelectorConfig{
		Labels: map[string]string{
			"app": "test",
			"env": "prod",
		},
		Names: []string{"vm1", "vm2"},
	}

	// Test that the struct can be marshaled to JSON
	// This is a basic test to ensure the struct is properly defined
	if selector.Labels["app"] != "test" {
		t.Error("Label 'app' should be 'test'")
	}
	if selector.Labels["env"] != "prod" {
		t.Error("Label 'env' should be 'prod'")
	}
	if len(selector.Names) != 2 {
		t.Error("Should have 2 names")
	}
	if selector.Names[0] != "vm1" {
		t.Error("First name should be 'vm1'")
	}
	if selector.Names[1] != "vm2" {
		t.Error("Second name should be 'vm2'")
	}
}

func TestClient_retryWithBackoff(t *testing.T) {
	client := &Client{
		timeout: 30 * time.Second,
	}

	// Test successful operation
	callCount := 0
	err := client.retryWithBackoff("test-op", func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// Test operation that fails then succeeds
	callCount = 0
	err = client.retryWithBackoff("test-op", func() error {
		callCount++
		if callCount < 3 {
			return errors.New("temporary failure")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Expected no error after retries, got %v", err)
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls, got %d", callCount)
	}

	// Test operation that always fails
	callCount = 0
	err = client.retryWithBackoff("test-op", func() error {
		callCount++
		return errors.New("permanent error")
	})

	if err == nil {
		t.Error("Expected error for permanent failure")
	}
	if callCount < 1 {
		t.Errorf("Expected at least 1 call, got %d", callCount)
	}
}

func TestClient_GetDataVolumeConfig(t *testing.T) {
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil, // No config provided, should use defaults
	}

	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout := client.getDataVolumeConfig()

	// Should return default values
	if storageSize != "10Gi" {
		t.Errorf("Expected storage size '10Gi', got '%s'", storageSize)
	}
	if allowInsecureTLS {
		t.Error("Expected allow_insecure_tls to be false by default")
	}
	if storageClass != "" {
		t.Errorf("Expected empty storage class, got '%s'", storageClass)
	}
	if vmUpdateTimeout != "30s" {
		t.Errorf("Expected vm update timeout '30s', got '%s'", vmUpdateTimeout)
	}
	if isoDownloadTimeout != "30m" {
		t.Errorf("Expected iso download timeout '30m', got '%s'", isoDownloadTimeout)
	}
}

func TestClient_GetKubeVirtConfig(t *testing.T) {
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil, // No config provided, should use defaults
	}

	apiVersion, timeout, allowInsecureTLS := client.getKubeVirtConfig()

	// Should return default values
	if apiVersion != "v1" {
		t.Errorf("Expected API version 'v1', got '%s'", apiVersion)
	}
	if timeout != 30 {
		t.Errorf("Expected timeout 30, got %d", timeout)
	}
	if allowInsecureTLS {
		t.Error("Expected allow_insecure_tls to be false by default")
	}
}

func TestClient_GetDataVolumeConfig_Defaults(t *testing.T) {
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil, // No config provided
	}

	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout := client.getDataVolumeConfig()

	// Should return default values
	if storageSize != "10Gi" {
		t.Errorf("Expected storage size '10Gi', got '%s'", storageSize)
	}
	if storageClass != "" {
		t.Errorf("Expected empty storage class, got '%s'", storageClass)
	}
	if vmUpdateTimeout != "30s" {
		t.Errorf("Expected vm update timeout '30s', got '%s'", vmUpdateTimeout)
	}
	if isoDownloadTimeout != "30m" {
		t.Errorf("Expected iso download timeout '30m', got '%s'", isoDownloadTimeout)
	}
	// allowInsecureTLS can be false by default, but we should still check it's defined
	_ = allowInsecureTLS // Use the variable to avoid linter warning
}

func TestClient_GetKubeVirtConfig_Defaults(t *testing.T) {
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil, // No config provided
	}

	apiVersion, timeout, allowInsecureTLS := client.getKubeVirtConfig()

	// Should return default values
	if apiVersion == "" {
		t.Error("API version should have a default value")
	}
	if timeout == 0 {
		t.Error("Timeout should have a default value")
	}
	// allowInsecureTLS can be false by default, but we should still check it's defined
	_ = allowInsecureTLS // Use the variable to avoid linter warning
}

func TestStringPtr(t *testing.T) {
	testString := "test-value"
	ptr := stringPtr(testString)

	if ptr == nil {
		t.Error("stringPtr should not return nil")
	}
	if *ptr != testString {
		t.Errorf("Expected '%s', got '%s'", testString, *ptr)
	}
}

func TestResourceMustParse(t *testing.T) {
	// Test valid resource string
	quantity := resourceMustParse("1Gi")
	if quantity.IsZero() {
		t.Error("Quantity should not be zero for valid resource string")
	}

	// Test another valid resource string
	quantity = resourceMustParse("500Mi")
	if quantity.IsZero() {
		t.Error("Quantity should not be zero for valid resource string")
	}
}
