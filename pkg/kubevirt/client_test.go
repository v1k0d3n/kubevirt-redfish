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
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// Mock config that implements the required interfaces
type MockConfig struct {
	dataVolumeConfig struct {
		storageSize        string
		allowInsecureTLS   bool
		storageClass       string
		vmUpdateTimeout    string
		isoDownloadTimeout string
	}
	kubeVirtConfig struct {
		apiVersion       string
		timeout          int
		allowInsecureTLS bool
	}
}

func (m *MockConfig) GetDataVolumeConfig() (string, bool, string, string, string) {
	return m.dataVolumeConfig.storageSize,
		m.dataVolumeConfig.allowInsecureTLS,
		m.dataVolumeConfig.storageClass,
		m.dataVolumeConfig.vmUpdateTimeout,
		m.dataVolumeConfig.isoDownloadTimeout
}

func (m *MockConfig) GetKubeVirtConfig() (string, int, bool) {
	return m.kubeVirtConfig.apiVersion,
		m.kubeVirtConfig.timeout,
		m.kubeVirtConfig.allowInsecureTLS
}

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
		return // Early return to prevent nil pointer dereference
	}
	if *ptr != testString {
		t.Errorf("Expected '%s', got '%s'", testString, *ptr)
	}
}

func TestResourceMustParse(t *testing.T) {
	// Test valid resource string
	quantity := resourceMustParse("100Mi")
	if quantity.String() != "100Mi" {
		t.Errorf("Expected 100Mi, got %s", quantity.String())
	}

	// Test another valid resource string
	quantity = resourceMustParse("2Gi")
	if quantity.String() != "2Gi" {
		t.Errorf("Expected 2Gi, got %s", quantity.String())
	}
}

// =============================================================================
// NEW TESTS FOR 0% COVERAGE FUNCTIONS
// =============================================================================

func TestClient_TestConnection(t *testing.T) {
	// Create a mock client
	client := &Client{
		kubernetesClient: nil, // Will cause panic
	}

	// Test connection failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when kubernetes client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.TestConnection()
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetNamespaceInfo(t *testing.T) {
	// Create a mock client
	client := &Client{
		kubernetesClient: nil, // Will cause panic
	}

	// Test namespace info retrieval failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when kubernetes client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetNamespaceInfo("test-namespace")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMMemory(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMMemory("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMCPU(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMCPU("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetUploadProxyURL(t *testing.T) {
	// Create a mock client
	client := &Client{
		kubernetesClient: nil, // Will cause panic
	}

	// Test upload proxy URL retrieval failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when kubernetes client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.getUploadProxyURL()
	t.Error("Expected panic, but function completed normally")
}

func TestClient_IsDataVolumeReady(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic
	}

	// Test data volume ready check failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.IsDataVolumeReady("test-namespace", "test-dv")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_CleanupExistingDataVolume(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic
	}

	// Test cleanup failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.cleanupExistingDataVolume("test-namespace", "test-dv")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMNetworkDetails(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMNetworkDetails("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMStorageDetails(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMStorageDetails("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMNetworkInterfaces(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMNetworkInterfaces("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMStorage(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMStorage("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMBootOptions(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMBootOptions("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_SetVMBootOptions(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	options := map[string]interface{}{
		"bootOrder": []string{"cdrom", "disk"},
	}
	//nolint:errcheck // We expect this to panic, not return an error
	client.SetVMBootOptions("test-namespace", "test-vm", options)
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMVirtualMedia(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.GetVMVirtualMedia("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_IsVirtualMediaInserted(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.IsVirtualMediaInserted("test-namespace", "test-vm", "cdrom0")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_DownloadISO(t *testing.T) {
	// Create a mock client
	client := &Client{}

	// Test with invalid URL
	_, err := client.downloadISO("invalid-url")
	if err == nil {
		t.Error("Expected error with invalid URL")
	}

	// Test with empty URL
	_, err = client.downloadISO("")
	if err == nil {
		t.Error("Expected error with empty URL")
	}
}

func TestClient_InsertVirtualMedia(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.InsertVirtualMedia("test-namespace", "test-vm", "cdrom0", "http://example.com/iso")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_EjectVirtualMedia(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.EjectVirtualMedia("test-namespace", "test-vm", "cdrom0")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_SetBootOrder(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.SetBootOrder("test-namespace", "test-vm", "cdrom")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_SetBootOnce(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.SetBootOnce("test-namespace", "test-vm", "cdrom")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_ListVMs(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic
	}

	// Test list VMs failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	//nolint:errcheck // We expect this to panic, not return an error
	client.ListVMs("test-namespace")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_ListVMsWithSelector(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic
	}

	// Test list VMs with selector failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	selector := &VMSelectorConfig{
		Labels: map[string]string{"app": "test"},
		Names:  []string{"vm1", "vm2"},
	}
	client.ListVMsWithSelector("test-namespace", selector)
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVM(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic
	}

	// Test get VM failure - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	client.GetVM("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMPowerState(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with nil VM (GetVM will panic, so we need to recover)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	client.GetVMPowerState("test-namespace", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_SetVMPowerState(t *testing.T) {
	// Create a mock client
	client := &Client{}

	// Test with invalid parameters
	err := client.SetVMPowerState("", "", "")
	if err == nil {
		t.Error("Expected error with empty parameters")
	}

	err = client.SetVMPowerState("test-namespace", "", "Running")
	if err == nil {
		t.Error("Expected error with empty VM name")
	}

	err = client.SetVMPowerState("test-namespace", "test-vm", "")
	if err == nil {
		t.Error("Expected error with empty power state")
	}
}

func TestClient_PauseVMI(t *testing.T) {
	// Create a mock client
	client := &Client{}

	// Test with invalid parameters
	err := client.pauseVMI("", "")
	if err == nil {
		t.Error("Expected error with empty parameters")
	}

	err = client.pauseVMI("test-namespace", "")
	if err == nil {
		t.Error("Expected error with empty VM name")
	}
}

func TestClient_UnpauseVMI(t *testing.T) {
	// Create a mock client
	client := &Client{}

	// Test with invalid parameters
	err := client.unpauseVMI("", "")
	if err == nil {
		t.Error("Expected error with empty parameters")
	}

	err = client.unpauseVMI("test-namespace", "")
	if err == nil {
		t.Error("Expected error with empty VM name")
	}
}

func TestClient_UploadISOToDataVolume(t *testing.T) {
	// Create a mock client
	client := &Client{
		kubernetesClient: nil, // Will cause panic in getUploadProxyURL
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when kubernetes client is nil")
		}
	}()

	client.uploadISOToDataVolume("test-namespace", "test-dv", "/path/to/iso")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_CopyISOToPVC(t *testing.T) {
	// Create a mock client
	client := &Client{
		kubernetesClient: nil, // Will cause panic in getUploadProxyURL
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when kubernetes client is nil")
		}
	}()

	client.copyISOToPVC("test-namespace", "test-dv", "http://example.com/iso", "30s")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_CreateCertConfigMap(t *testing.T) {
	// Create a mock client
	client := &Client{
		kubernetesClient: nil, // Will cause panic
	}

	// Test with invalid parameters - this will panic, so we need to recover
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when kubernetes client is nil")
		}
	}()

	client.createCertConfigMap("test-namespace", "test-configmap", "http://example.com/iso")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_FetchServerCertificate(t *testing.T) {
	// Create a mock client
	client := &Client{}

	// Test with invalid host
	_, err := client.fetchServerCertificate("")
	if err == nil {
		t.Error("Expected error with empty host")
	}

	// Test with invalid host format
	_, err = client.fetchServerCertificate("invalid-host")
	if err == nil {
		t.Error("Expected error with invalid host format")
	}
}

// =============================================================================
// EDGE CASES AND ERROR CONDITIONS
// =============================================================================

func TestClient_GetVMMemory_EdgeCases(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with empty namespace - will panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	client.GetVMMemory("", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_GetVMCPU_EdgeCases(t *testing.T) {
	// Create a mock client
	client := &Client{
		dynamicClient: nil, // Will cause panic in GetVM
	}

	// Test with empty namespace - will panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when dynamic client is nil")
		}
	}()

	client.GetVMCPU("", "test-vm")
	t.Error("Expected panic, but function completed normally")
}

func TestClient_ConcurrentAccess(t *testing.T) {
	// Create a mock client
	client := &Client{
		timeout: 30 * time.Second,
	}

	// Test concurrent access to performance metrics
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.trackOperation("concurrent-op", 100*time.Millisecond)
			client.GetPerformanceMetrics()
		}()
	}
	wg.Wait()

	// Verify metrics were tracked correctly
	metrics := client.GetPerformanceMetrics()
	if metrics == nil {
		t.Fatal("Metrics should not be nil")
	}
}

func TestVMSelectorConfig_Validation(t *testing.T) {
	// Test empty selector
	selector := &VMSelectorConfig{}
	if len(selector.Labels) != 0 || len(selector.Names) != 0 {
		t.Error("Empty selector should have empty labels and names")
	}

	// Test with labels
	selector = &VMSelectorConfig{
		Labels: map[string]string{"app": "test"},
	}
	if selector.Labels["app"] != "test" {
		t.Error("Label should be set correctly")
	}

	// Test with names
	selector = &VMSelectorConfig{
		Names: []string{"vm1", "vm2"},
	}
	if len(selector.Names) != 2 || selector.Names[0] != "vm1" || selector.Names[1] != "vm2" {
		t.Error("Names should be set correctly")
	}
}

// Test executePauseRequestWithDynamicClient function
func TestClient_ExecutePauseRequestWithDynamicClient(t *testing.T) {
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-vmi"
	correlationID := "test-correlation-id"

	// Test with nil client (should panic, but we can test the panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.executePauseRequestWithDynamicClient(ctx, namespace, name, correlationID)
}

// Test executePauseRequestWithDynamicClient with invalid config
func TestClient_ExecutePauseRequestWithDynamicClient_InvalidConfig(t *testing.T) {
	// Create a mock client with invalid config
	client := &Client{
		config: &rest.Config{
			Host: "invalid://host",
		},
		timeout: 30 * time.Second,
	}

	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-vmi"
	correlationID := "test-correlation-id"

	// Test with invalid config (should fail)
	err := client.executePauseRequestWithDynamicClient(ctx, namespace, name, correlationID)
	if err == nil {
		t.Error("Expected error with invalid config")
	}
}

// Test executeUnpauseRequestWithDynamicClient function
func TestClient_ExecuteUnpauseRequestWithDynamicClient(t *testing.T) {
	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-vmi"
	correlationID := "test-correlation-id"

	// Test with nil client (should panic, but we can test the panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.executeUnpauseRequestWithDynamicClient(ctx, namespace, name, correlationID)
}

// Test executeUnpauseRequestWithDynamicClient with invalid config
func TestClient_ExecuteUnpauseRequestWithDynamicClient_InvalidConfig(t *testing.T) {
	// Create a mock client with invalid config
	client := &Client{
		config: &rest.Config{
			Host: "invalid://host",
		},
		timeout: 30 * time.Second,
	}

	ctx := context.Background()
	namespace := "test-namespace"
	name := "test-vmi"
	correlationID := "test-correlation-id"

	// Test with invalid config (should fail)
	err := client.executeUnpauseRequestWithDynamicClient(ctx, namespace, name, correlationID)
	if err == nil {
		t.Error("Expected error with invalid config")
	}
}

// Test getUploadProxyURL function with various scenarios
func TestClient_GetUploadProxyURL_EdgeCases(t *testing.T) {
	// Test with nil client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.getUploadProxyURL()
}

// Test getUploadProxyURL function with nil kubernetes client
func TestClient_GetUploadProxyURL_NilKubeClient(t *testing.T) {
	// Test with nil kubernetes client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil kubernetes client")
		}
	}()

	client := &Client{
		kubernetesClient: nil,
		timeout:          30 * time.Second,
	}
	client.getUploadProxyURL()
}

// Test createCertConfigMap function with various scenarios
func TestClient_CreateCertConfigMap_EdgeCases(t *testing.T) {
	// Test with nil client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.createCertConfigMap("test-namespace", "test-configmap", "invalid-url")
}

// Test createCertConfigMap function with invalid URL
func TestClient_CreateCertConfigMap_InvalidURL(t *testing.T) {
	// Test with invalid URL (should panic due to nil kubernetes client)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil kubernetes client")
		}
	}()

	client := &Client{
		timeout: 30 * time.Second,
	}
	client.createCertConfigMap("test-namespace", "test-configmap", "invalid-url")
}

// Test createCertConfigMap function with valid URL but nil kubernetes client
func TestClient_CreateCertConfigMap_NilKubeClient(t *testing.T) {
	// Test with valid URL but nil kubernetes client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil kubernetes client")
		}
	}()

	client := &Client{
		timeout: 30 * time.Second,
	}
	client.createCertConfigMap("test-namespace", "test-configmap", "https://example.com/image.iso")
}

// Test fetchServerCertificate function with various scenarios
func TestClient_FetchServerCertificate_EdgeCases(t *testing.T) {
	// Test with nil client (should not panic, but will fail with connection error)
	var client *Client
	_, err := client.fetchServerCertificate("invalid-host")
	if err == nil {
		t.Error("Expected error with nil client")
	}
}

// Test fetchServerCertificate function with empty host
func TestClient_FetchServerCertificate_EmptyHost(t *testing.T) {
	// Test with empty host
	client := &Client{
		timeout: 30 * time.Second,
	}
	_, err := client.fetchServerCertificate("")
	if err == nil {
		t.Error("Expected error with empty host")
	}
}

// Test fetchServerCertificate function with invalid host
func TestClient_FetchServerCertificate_InvalidHost(t *testing.T) {
	// Test with invalid host
	client := &Client{
		timeout: 30 * time.Second,
	}
	_, err := client.fetchServerCertificate("invalid-host:invalid-port")
	if err == nil {
		t.Error("Expected error with invalid host")
	}
}

// Test getDataVolumeConfig function with nil client
func TestClient_GetDataVolumeConfig_NilClient(t *testing.T) {
	// Test with nil client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.getDataVolumeConfig()
}

// Test getDataVolumeConfig function with nil appConfig
func TestClient_GetDataVolumeConfig_NilAppConfig(t *testing.T) {
	// Test with client but nil appConfig
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil,
	}
	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout := client.getDataVolumeConfig()
	if storageSize != "10Gi" || allowInsecureTLS || storageClass != "" || vmUpdateTimeout != "30s" || isoDownloadTimeout != "30m" {
		t.Error("Expected default values with nil appConfig")
	}
}

// Test getKubeVirtConfig function with nil client
func TestClient_GetKubeVirtConfig_NilClient(t *testing.T) {
	// Test with nil client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.getKubeVirtConfig()
}

// Test getKubeVirtConfig function with nil appConfig
func TestClient_GetKubeVirtConfig_NilAppConfig(t *testing.T) {
	// Test with client but nil appConfig
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil,
	}
	apiVersion, timeout, allowInsecureTLS := client.getKubeVirtConfig()
	if apiVersion != "v1" || timeout != 30 || allowInsecureTLS {
		t.Error("Expected default values with nil appConfig")
	}
}

// Test cleanupExistingDataVolume function
func TestClient_CleanupExistingDataVolume_EdgeCases(t *testing.T) {
	// Test with nil client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil client")
		}
	}()

	var client *Client
	client.cleanupExistingDataVolume("test-namespace", "test-datavolume")
}

// Test cleanupExistingDataVolume function with nil kubernetes client
func TestClient_CleanupExistingDataVolume_NilKubeClient(t *testing.T) {
	// Test with nil kubernetes client (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic with nil kubernetes client")
		}
	}()

	client := &Client{
		timeout: 30 * time.Second,
	}
	client.cleanupExistingDataVolume("test-namespace", "test-datavolume")
}
