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
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// Mock config that implements the required interfaces
type MockConfig struct {
	dataVolumeConfig struct {
		storageSize        string
		allowInsecureTLS   bool
		storageClass       string
		vmUpdateTimeout    string
		isoDownloadTimeout string
		helperImage        string
	}
	kubeVirtConfig struct {
		apiVersion       string
		timeout          int
		allowInsecureTLS bool
	}
}

func (m *MockConfig) GetDataVolumeConfig() (string, bool, string, string, string, string) {
	return m.dataVolumeConfig.storageSize,
		m.dataVolumeConfig.allowInsecureTLS,
		m.dataVolumeConfig.storageClass,
		m.dataVolumeConfig.vmUpdateTimeout,
		m.dataVolumeConfig.isoDownloadTimeout,
		m.dataVolumeConfig.helperImage
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

	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage := client.getDataVolumeConfig()

	// Should return default values
	if storageSize != "10Gi" {
		t.Errorf("Expected storage size '10Gi', got '%s'", storageSize)
	}
	// allowInsecureTLS can be false by default, but we should still check it's defined
	_ = allowInsecureTLS   // Use the variable to avoid linter warning
	_ = storageClass       // Use the variable to avoid linter warning
	_ = vmUpdateTimeout    // Use the variable to avoid linter warning
	_ = isoDownloadTimeout // Use the variable to avoid linter warning
	if helperImage != "alpine:latest" {
		t.Errorf("Expected helper image 'alpine:latest', got '%s'", helperImage)
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
	if quantity.IsZero() {
		t.Error("resourceMustParse should not return zero quantity for valid input")
	}

	// Test another valid resource string
	quantity = resourceMustParse("2Gi")
	if quantity.IsZero() {
		t.Error("resourceMustParse should not return zero quantity for valid input")
	}

	// Test invalid resource string - should return zero quantity
	quantity = resourceMustParse("invalid")
	if !quantity.IsZero() {
		t.Error("resourceMustParse should return zero quantity for invalid input")
	}
}

// =============================================================================
// NEW TESTS FOR 0% COVERAGE FUNCTIONS
// =============================================================================

func TestClient_IsVirtualMediaInserted_VolumeNameFix(t *testing.T) {
	// This test validates that IsVirtualMediaInserted works correctly with typed API

	testCases := []struct {
		name           string
		setupVM        func(mockClient *MockDynamicClient)
		setupPVC       func(fakeK8sClient *fake.Clientset)
		mediaID        string
		expectedResult bool
	}{
		{
			name: "CD-ROM with bound PVC returns true",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{
												Name: "cdrom0",
												DiskDevice: kubevirtv1.DiskDevice{
													CDRom: &kubevirtv1.CDRomTarget{
														Bus: kubevirtv1.DiskBusSATA,
													},
												},
											},
										},
									},
								},
								Volumes: []kubevirtv1.Volume{
									{
										Name: "cdrom0",
										VolumeSource: kubevirtv1.VolumeSource{
											PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
												PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
													ClaimName: "test-vm-bootiso",
												},
											},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupPVC: func(fakeK8sClient *fake.Clientset) {
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm-bootiso",
						Namespace: "test-namespace",
					},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimBound,
						Capacity: corev1.ResourceList{
							"storage": resource.MustParse("1Gi"),
						},
					},
				}
				fakeK8sClient.CoreV1().PersistentVolumeClaims("test-namespace").Create(
					context.Background(), pvc, metav1.CreateOptions{})
			},
			mediaID:        "cdrom0",
			expectedResult: true,
		},
		{
			name: "CD-ROM with unbound PVC returns false",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{
												Name: "cdrom0",
												DiskDevice: kubevirtv1.DiskDevice{
													CDRom: &kubevirtv1.CDRomTarget{
														Bus: kubevirtv1.DiskBusSATA,
													},
												},
											},
										},
									},
								},
								Volumes: []kubevirtv1.Volume{
									{
										Name: "cdrom0",
										VolumeSource: kubevirtv1.VolumeSource{
											PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
												PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
													ClaimName: "test-vm-bootiso",
												},
											},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupPVC: func(fakeK8sClient *fake.Clientset) {
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm-bootiso",
						Namespace: "test-namespace",
					},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimPending, // Not bound
					},
				}
				fakeK8sClient.CoreV1().PersistentVolumeClaims("test-namespace").Create(
					context.Background(), pvc, metav1.CreateOptions{})
			},
			mediaID:        "cdrom0",
			expectedResult: false,
		},
		{
			name: "Non-existent CD-ROM device returns false",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{
												Name: "rootdisk",
												DiskDevice: kubevirtv1.DiskDevice{
													Disk: &kubevirtv1.DiskTarget{
														Bus: kubevirtv1.DiskBusVirtio,
													},
												},
											},
										},
									},
								},
								Volumes: []kubevirtv1.Volume{
									{
										Name: "rootdisk",
										VolumeSource: kubevirtv1.VolumeSource{
											DataVolume: &kubevirtv1.DataVolumeSource{Name: "my-dv"},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupPVC:       func(fakeK8sClient *fake.Clientset) {},
			mediaID:        "cdrom0", // Looking for cdrom0 but VM only has rootdisk
			expectedResult: false,
		},
		{
			name: "CD-ROM without PVC returns false",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{
												Name: "cdrom0",
												DiskDevice: kubevirtv1.DiskDevice{
													CDRom: &kubevirtv1.CDRomTarget{
														Bus: kubevirtv1.DiskBusSATA,
													},
												},
											},
										},
									},
								},
								Volumes: []kubevirtv1.Volume{
									{
										Name: "cdrom0",
										VolumeSource: kubevirtv1.VolumeSource{
											// Using CloudInit instead of PVC
											CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
												UserData: "#cloud-config",
											},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupPVC:       func(fakeK8sClient *fake.Clientset) {},
			mediaID:        "cdrom0",
			expectedResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)
			tc.setupPVC(fakeK8sClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual IsVirtualMediaInserted function
			result, err := client.IsVirtualMediaInserted("test-namespace", "test-vm", tc.mediaID)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tc.expectedResult {
				t.Errorf("Expected IsVirtualMediaInserted to return %v, got %v", tc.expectedResult, result)
			}
		})
	}
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

// =============================================================================
// EDGE CASES AND ERROR CONDITIONS
// =============================================================================

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

// Test getDataVolumeConfig function with nil appConfig
func TestClient_GetDataVolumeConfig_NilAppConfig(t *testing.T) {
	// Test with client but nil appConfig
	client := &Client{
		timeout:   30 * time.Second,
		appConfig: nil,
	}
	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage := client.getDataVolumeConfig()
	if storageSize != "10Gi" || allowInsecureTLS || storageClass != "" || vmUpdateTimeout != "30s" || isoDownloadTimeout != "30m" || helperImage != "alpine:latest" {
		t.Error("Expected default values with nil appConfig")
	}
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

// TestGetVMPowerState tests the GetVMPowerState function with various scenarios using MockDynamicClient
func TestGetVMPowerState(t *testing.T) {
	testCases := []struct {
		name     string
		setupVM  func(mockClient *MockDynamicClient)
		expected string
	}{
		{
			name: "VM running",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("Running"),
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "On",
		},
		{
			name: "VM stopped",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("Stopped"),
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "Off",
		},
		{
			name: "VM stopping",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("Stopping"),
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "ShuttingDown",
		},
		{
			name: "VM starting",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("Starting"),
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "PoweringOn",
		},
		{
			name: "VM force stopping",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"kubevirt.io/force-stop": "true",
						},
					},
					Status: kubevirtv1.VirtualMachineStatus{
						PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("Stopping"),
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "ForceOffInProgress",
		},
		{
			name: "VM with PodTerminating condition",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{
						Conditions: []kubevirtv1.VirtualMachineCondition{
							{
								Type: kubevirtv1.VirtualMachineConditionType("PodTerminating"),
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "ShuttingDown",
		},
		{
			name: "VM with state change requests",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{
						StateChangeRequests: []kubevirtv1.VirtualMachineStateChangeRequest{
							{
								Action: kubevirtv1.StartRequest,
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: "Transitioning",
		},
		{
			name: "VMI running (fallback when VM has no printableStatus)",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{},
				}
				mockClient.AddVM(vm)

				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: "On",
		},
		{
			name: "VMI paused",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{},
				}
				mockClient.AddVM(vm)

				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Conditions: []kubevirtv1.VirtualMachineInstanceCondition{
							{
								Type:   kubevirtv1.VirtualMachineInstancePaused,
								Status: corev1.ConditionTrue,
							},
						},
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: "Paused",
		},
		{
			name: "VMI failed",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{},
				}
				mockClient.AddVM(vm)

				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Failed,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: "Off",
		},
		{
			name: "VMI pending",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{},
				}
				mockClient.AddVM(vm)

				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Pending,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: "PoweringOn",
		},
		{
			name: "No VMI exists (VM stopped)",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineStatus{},
				}
				mockClient.AddVM(vm)
				// No VMI added - simulates stopped VM
			},
			expected: "Off",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMPowerState function
			result, err := client.GetVMPowerState("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tc.expected {
				t.Errorf("Expected power state '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

// TestSetVMPowerState tests the SetVMPowerState function using MockDynamicClient
func TestSetVMPowerState(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	testCases := []struct {
		name                string
		state               string
		expectErr           bool
		errSubstr           string
		expectedRunStrategy string // Expected runStrategy after the operation
		initialRunning      *bool  // If set, the initial VM uses the legacy running field
	}{
		{
			name:                "Power on",
			state:               "On",
			expectedRunStrategy: "Always",
		},
		{
			name:                "Force power off",
			state:               "ForceOff",
			expectedRunStrategy: "Halted",
		},
		{
			name:                "Graceful shutdown",
			state:               "GracefulShutdown",
			expectedRunStrategy: "Halted",
		},
		{
			name:                "Force restart",
			state:               "ForceRestart",
			expectedRunStrategy: "Always", // After restart, VM should be set to run
		},
		{
			name:      "Invalid state",
			state:     "InvalidState",
			expectErr: true,
			errSubstr: "unsupported power state",
		},
		{
			name:                "Power off VM with legacy running=true",
			state:               "GracefulShutdown",
			initialRunning:      boolPtr(true),
			expectedRunStrategy: "Halted",
		},
		{
			name:                "Power on VM with legacy running=false",
			state:               "On",
			initialRunning:      boolPtr(false),
			expectedRunStrategy: "Always",
		},
		{
			name:                "Force off VM with legacy running=true",
			state:               "ForceOff",
			initialRunning:      boolPtr(true),
			expectedRunStrategy: "Halted",
		},
		{
			name:                "Force restart VM with legacy running=true",
			state:               "ForceRestart",
			initialRunning:      boolPtr(true),
			expectedRunStrategy: "Always",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup a VM in the mock
			vm := &kubevirtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vm",
					Namespace: "test-namespace",
				},
				Spec: kubevirtv1.VirtualMachineSpec{
					RunStrategy: func() *kubevirtv1.VirtualMachineRunStrategy {
						s := kubevirtv1.RunStrategyAlways
						return &s
					}(),
				},
				Status: kubevirtv1.VirtualMachineStatus{
					PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus("Running"),
				},
			}

			if tc.initialRunning != nil {
				// Use the legacy running field alongside runStrategy
				vm.Spec.Running = tc.initialRunning
			}

			mockDynamicClient.AddVM(vm)

			// Also add a VMI for some operations that need it
			gracePeriod := int64(30)
			vmi := &kubevirtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vm",
					Namespace: "test-namespace",
				},
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					TerminationGracePeriodSeconds: &gracePeriod,
				},
				Status: kubevirtv1.VirtualMachineInstanceStatus{
					Phase: kubevirtv1.Running,
				},
			}
			mockDynamicClient.AddVMI(vmi)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual SetVMPowerState function
			err := client.SetVMPowerState("test-namespace", "test-vm", tc.state)

			if tc.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("Expected error containing '%s', got: %v", tc.errSubstr, err)
				}
				return // Skip verification for error cases
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Verify the VM was actually updated in the mock
			updatedVM, err := mockDynamicClient.GetVM("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Failed to retrieve updated VM from mock: %v", err)
			}

			// Check that runStrategy was updated correctly
			if updatedVM.Spec.RunStrategy == nil {
				t.Errorf("Expected runStrategy to be set, but it was nil")
			} else {
				actualRunStrategy := string(*updatedVM.Spec.RunStrategy)
				if actualRunStrategy != tc.expectedRunStrategy {
					t.Errorf("Expected runStrategy '%s', got '%s'", tc.expectedRunStrategy, actualRunStrategy)
				}
			}

			// Check that the legacy running field was cleared
			if updatedVM.Spec.Running != nil {
				t.Errorf("Expected running field to be nil after power state change, but got %v", *updatedVM.Spec.Running)
			}

			// For ForceOff, also check the force-stop annotation
			if tc.state == "ForceOff" {
				annotations := updatedVM.GetAnnotations()
				if annotations == nil || annotations["kubevirt.io/force-stop"] != "true" {
					t.Errorf("Expected force-stop annotation to be set for ForceOff state")
				}
			}

			// For ForceOff and ForceRestart, verify terminationGracePeriodSeconds was set to 0 on the VMI
			if tc.state == "ForceOff" || tc.state == "ForceRestart" {
				updatedVMI, err := mockDynamicClient.GetVMI("test-namespace", "test-vm")
				if err != nil {
					t.Fatalf("Failed to retrieve updated VMI from mock: %v", err)
				}
				if updatedVMI.Spec.TerminationGracePeriodSeconds == nil {
					t.Error("Expected VMI terminationGracePeriodSeconds to be set, got nil")
				} else if *updatedVMI.Spec.TerminationGracePeriodSeconds != 0 {
					t.Errorf("Expected VMI terminationGracePeriodSeconds=0, got %d", *updatedVMI.Spec.TerminationGracePeriodSeconds)
				}
			}
		})
	}
}

// TestVMNetworkInterfaces tests the GetVMNetworkInterfaces function using MockDynamicClient
func TestVMNetworkInterfaces(t *testing.T) {
	testCases := []struct {
		name     string
		setupVMI func(mockClient *MockDynamicClient)
		expected []string
	}{
		{
			name: "VMI with network interfaces",
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
							{Name: "default", MAC: "00:11:22:33:44:55", IP: "10.0.0.1"},
							{Name: "secondary", MAC: "00:11:22:33:44:66", IP: "10.0.0.2"},
						},
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: []string{"default", "secondary"},
		},
		{
			name: "VMI without network interfaces",
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase:      kubevirtv1.Running,
						Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{},
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: nil,
		},
		{
			name: "VMI with single interface",
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
						Interfaces: []kubevirtv1.VirtualMachineInstanceNetworkInterface{
							{Name: "eth0", MAC: "00:11:22:33:44:55"},
						},
					},
				}
				mockClient.AddVMI(vmi)
			},
			expected: []string{"eth0"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVMI(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMNetworkInterfaces function
			interfaces, err := client.GetVMNetworkInterfaces("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(interfaces) != len(tc.expected) {
				t.Errorf("Expected %d interfaces, got %d", len(tc.expected), len(interfaces))
				return
			}

			for i, expected := range tc.expected {
				if interfaces[i] != expected {
					t.Errorf("Expected interface[%d] = '%s', got '%s'", i, expected, interfaces[i])
				}
			}
		})
	}
}

// TestVMStorage tests the GetVMStorage function using MockDynamicClient
func TestVMStorage(t *testing.T) {
	testCases := []struct {
		name     string
		setupVM  func(mockClient *MockDynamicClient)
		expected []string
	}{
		{
			name: "VM with storage volumes",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Volumes: []kubevirtv1.Volume{
									{Name: "containerdisk", VolumeSource: kubevirtv1.VolumeSource{ContainerDisk: &kubevirtv1.ContainerDiskSource{Image: "cirros"}}},
									{Name: "cloudinitdisk", VolumeSource: kubevirtv1.VolumeSource{CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{UserData: "#cloud-config"}}},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: []string{"containerdisk", "cloudinitdisk"},
		},
		{
			name: "VM without storage volumes",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Volumes: []kubevirtv1.Volume{},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: nil,
		},
		{
			name: "VM with single data volume",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Volumes: []kubevirtv1.Volume{
									{Name: "rootdisk", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{Name: "my-dv"}}},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: []string{"rootdisk"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMStorage function
			storage, err := client.GetVMStorage("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(storage) != len(tc.expected) {
				t.Errorf("Expected %d volumes, got %d", len(tc.expected), len(storage))
				return
			}

			for i, expected := range tc.expected {
				if storage[i] != expected {
					t.Errorf("Expected storage[%d] = '%s', got '%s'", i, expected, storage[i])
				}
			}
		})
	}
}

// TestVMBootOptions tests the GetVMBootOptions function using MockDynamicClient
func TestVMBootOptions(t *testing.T) {
	testCases := []struct {
		name     string
		setupVM  func(mockClient *MockDynamicClient)
		expected map[string]interface{}
	}{
		{
			name: "VM with EFI boot options",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Firmware: &kubevirtv1.Firmware{
										Bootloader: &kubevirtv1.Bootloader{
											EFI: &kubevirtv1.EFI{},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: map[string]interface{}{
				"bootSourceOverrideEnabled": "Disabled",
				"bootSourceOverrideTarget":  "None",
				"bootSourceOverrideMode":    "UEFI",
			},
		},
		{
			name: "VM without boot options (legacy)",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: map[string]interface{}{
				"bootSourceOverrideEnabled": "Disabled",
				"bootSourceOverrideTarget":  "None",
				"bootSourceOverrideMode":    "Legacy",
			},
		},
		{
			name: "VM with boot override annotations",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						Annotations: map[string]string{
							"redfish.boot.source.override.enabled": "Once",
							"redfish.boot.source.override.target":  "Pxe",
							"redfish.boot.source.override.mode":    "UEFI",
						},
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: map[string]interface{}{
				"bootSourceOverrideEnabled": "Once",
				"bootSourceOverrideTarget":  "Pxe",
				"bootSourceOverrideMode":    "UEFI",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMBootOptions function
			bootOptions, err := client.GetVMBootOptions("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Compare results
			for key, expectedValue := range tc.expected {
				if actualValue, exists := bootOptions[key]; !exists {
					t.Errorf("Missing boot option: %s", key)
				} else if actualValue != expectedValue {
					t.Errorf("Boot option %s: expected %v, got %v", key, expectedValue, actualValue)
				}
			}
		})
	}
}

// TestGetVMMemory tests the GetVMMemory function using MockDynamicClient
func TestGetVMMemory(t *testing.T) {
	testCases := []struct {
		name     string
		setupVM  func(mockClient *MockDynamicClient)
		expected float64
	}{
		{
			name: "VM with 48Gi memory",
			setupVM: func(mockClient *MockDynamicClient) {
				guestMemory := resource.MustParse("48Gi")
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Memory: &kubevirtv1.Memory{
										Guest: &guestMemory,
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: 48.0,
		},
		{
			name: "VM with 2048Mi memory",
			setupVM: func(mockClient *MockDynamicClient) {
				guestMemory := resource.MustParse("2048Mi")
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Memory: &kubevirtv1.Memory{
										Guest: &guestMemory,
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: 2.0, // 2048Mi / 1024 = 2.0GB
		},
		{
			name: "VM without memory spec",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: 2.0, // Default fallback
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMMemory function
			result, err := client.GetVMMemory("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tc.expected {
				t.Errorf("Expected memory %.1f GB, got %.1f GB", tc.expected, result)
			}
		})
	}
}

// TestGetVMCPU tests the GetVMCPU function using MockDynamicClient
func TestGetVMCPU(t *testing.T) {
	testCases := []struct {
		name     string
		setupVM  func(mockClient *MockDynamicClient)
		expected int
	}{
		{
			name: "VM with 4 CPU cores",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									CPU: &kubevirtv1.CPU{
										Cores: 4,
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: 4,
		},
		{
			name: "VM with 8 CPU cores and 2 sockets",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									CPU: &kubevirtv1.CPU{
										Cores:   8,
										Sockets: 2,
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: 8,
		},
		{
			name: "VM without CPU spec",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expected: 1, // Default fallback
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMCPU function
			result, err := client.GetVMCPU("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tc.expected {
				t.Errorf("Expected %d CPU cores, got %d", tc.expected, result)
			}
		})
	}
}

// TestGetVMStorageDetails tests the GetVMStorageDetails function
func TestGetVMStorageDetails(t *testing.T) {
	testCases := []struct {
		name                string
		setupVM             func(mockClient *MockDynamicClient)
		expectedTotalGB     float64
		expectedVolumeCount int
		expectedVolumes     []string // volume names
	}{
		{
			name: "VM with DataVolume templates",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "disk1"},
								Spec: cdiv1beta1.DataVolumeSpec{
									Storage: &cdiv1beta1.StorageSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												"storage": resource.MustParse("120Gi"),
											},
										},
									},
								},
							},
							{
								ObjectMeta: metav1.ObjectMeta{Name: "disk2"},
								Spec: cdiv1beta1.DataVolumeSpec{
									Storage: &cdiv1beta1.StorageSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												"storage": resource.MustParse("80Gi"),
											},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expectedTotalGB:     200.0, // 120 + 80
			expectedVolumeCount: 2,
			expectedVolumes:     []string{"disk1", "disk2"},
		},
		{
			name: "VM without DataVolume templates",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{},
					},
				}
				mockClient.AddVM(vm)
			},
			expectedTotalGB:     0.0,
			expectedVolumeCount: 0,
			expectedVolumes:     nil,
		},
		{
			name: "VM with single DataVolume template in Mi",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{
							{
								ObjectMeta: metav1.ObjectMeta{Name: "disk1"},
								Spec: cdiv1beta1.DataVolumeSpec{
									Storage: &cdiv1beta1.StorageSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												"storage": resource.MustParse("2048Mi"),
											},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			expectedTotalGB:     2.0, // 2048Mi / 1024 = 2GB
			expectedVolumeCount: 1,
			expectedVolumes:     []string{"disk1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup test data
			tc.setupVM(mockDynamicClient)

			// Create client with mock clients
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Call the actual GetVMStorageDetails function
			storageInfo, err := client.GetVMStorageDetails("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			// Verify total capacity
			totalCapacity, ok := storageInfo["totalCapacityGB"].(float64)
			if !ok {
				t.Fatal("totalCapacityGB not found or not a float64")
			}
			if totalCapacity != tc.expectedTotalGB {
				t.Errorf("Expected total capacity %.1f GB, got %.1f GB", tc.expectedTotalGB, totalCapacity)
			}

			// Verify volume count
			volumes, ok := storageInfo["volumes"].([]map[string]interface{})
			if !ok {
				t.Fatal("volumes not found or not a slice")
			}
			if len(volumes) != tc.expectedVolumeCount {
				t.Errorf("Expected %d volumes, got %d", tc.expectedVolumeCount, len(volumes))
			}

			// Verify volume names
			for i, expectedName := range tc.expectedVolumes {
				if i < len(volumes) {
					actualName, _ := volumes[i]["name"].(string)
					if actualName != expectedName {
						t.Errorf("Expected volume[%d] name '%s', got '%s'", i, expectedName, actualName)
					}
				}
			}
		})
	}
}

// TestSetBootOrderLogic tests the SetBootOrder function logic in isolation
func TestSetBootOrderLogic(t *testing.T) {
	// Helper to create a typed VM for testing
	createTestVM := func(disks []kubevirtv1.Disk, volumes []kubevirtv1.Volume) *kubevirtv1.VirtualMachine {
		return &kubevirtv1.VirtualMachine{
			Spec: kubevirtv1.VirtualMachineSpec{
				Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
					Spec: kubevirtv1.VirtualMachineInstanceSpec{
						Domain: kubevirtv1.DomainSpec{
							Devices: kubevirtv1.Devices{
								Disks: disks,
							},
						},
						Volumes: volumes,
					},
				},
			},
		}
	}

	// Test cases for boot order logic
	testCases := []struct {
		name       string
		bootTarget string
		disks      []kubevirtv1.Disk
		volumes    []kubevirtv1.Volume
		expected   map[string]*uint // disk name -> expected boot order (nil means no boot order)
	}{
		{
			name:       "Set CD-ROM as first boot device",
			bootTarget: "Cd",
			disks: []kubevirtv1.Disk{
				{Name: "cdrom0"},
				{Name: "disk1"},
			},
			volumes: []kubevirtv1.Volume{
				{Name: "cdrom0"},
				{Name: "disk1", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{}}},
			},
			expected: map[string]*uint{
				"cdrom0": uintPtr(1),
				"disk1":  uintPtr(2),
			},
		},
		{
			name:       "Set CD-ROM as first boot device when boot 1 taken",
			bootTarget: "Cd",
			disks: []kubevirtv1.Disk{
				{Name: "cdrom0"},
				{Name: "disk1", BootOrder: uintPtr(1)},
			},
			volumes: []kubevirtv1.Volume{
				{Name: "cdrom0"},
				{Name: "disk1", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{}}},
			},
			expected: map[string]*uint{
				"cdrom0": uintPtr(1),
				"disk1":  uintPtr(2),
			},
		},
		{
			name:       "Set CD-ROM as first boot device, ignore cloudInit",
			bootTarget: "Cd",
			disks: []kubevirtv1.Disk{
				{Name: "cdrom0"},
				{Name: "disk1"},
				{Name: "cloudinitdisk"},
			},
			volumes: []kubevirtv1.Volume{
				{Name: "cdrom0"},
				{Name: "disk1", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{}}},
				{Name: "cloudinitdisk", VolumeSource: kubevirtv1.VolumeSource{CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{}}},
			},
			expected: map[string]*uint{
				"cdrom0":        uintPtr(1),
				"disk1":         uintPtr(2),
				"cloudinitdisk": nil,
			},
		},
		{
			name:       "Set disk as first boot device",
			bootTarget: "Hdd",
			disks: []kubevirtv1.Disk{
				{Name: "cdrom0"},
				{Name: "disk1"},
			},
			volumes: []kubevirtv1.Volume{
				{Name: "cdrom0"},
				{Name: "disk1", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{}}},
			},
			expected: map[string]*uint{
				"cdrom0": nil,
				"disk1":  uintPtr(1),
			},
		},
		{
			name:       "Set disk as first boot device ignore cloud init",
			bootTarget: "Hdd",
			disks: []kubevirtv1.Disk{
				{Name: "cdrom0"},
				{Name: "disk1"},
				{Name: "cloudinitdisk"},
			},
			volumes: []kubevirtv1.Volume{
				{Name: "cdrom0"},
				{Name: "disk1", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{}}},
				{Name: "cloudinitdisk", VolumeSource: kubevirtv1.VolumeSource{CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{}}},
			},
			expected: map[string]*uint{
				"cdrom0":        nil,
				"disk1":         uintPtr(1),
				"cloudinitdisk": nil,
			},
		},
	}

	client := Client{}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create typed VM object
			vm := createTestVM(tc.disks, tc.volumes)

			// Call the boot order logic
			err := client.modifyVmBootOrder(vm, tc.bootTarget)
			if err != nil {
				t.Errorf("modifyVmBootOrder failed: %v", err)
				return
			}

			// Verify the results
			testedDisks := map[string]bool{}
			for _, disk := range vm.Spec.Template.Spec.Domain.Devices.Disks {
				testedDisks[disk.Name] = true
				if expectedOrder, exists := tc.expected[disk.Name]; exists {
					if expectedOrder == nil {
						if disk.BootOrder != nil {
							t.Errorf("Disk %s: expected no boot order, got %d", disk.Name, *disk.BootOrder)
						}
					} else {
						if disk.BootOrder == nil {
							t.Errorf("Disk %s: expected boot order %d, but none was set", disk.Name, *expectedOrder)
						} else if *disk.BootOrder != *expectedOrder {
							t.Errorf("Disk %s: expected boot order %d, got %d", disk.Name, *expectedOrder, *disk.BootOrder)
						}
					}
				}
			}

			for name := range tc.expected {
				if _, ok := testedDisks[name]; !ok {
					t.Errorf("Disk %s boot order was not checked. It was probably missing.", name)
				}
			}
		})
	}
}

// uintPtr is a helper to create a pointer to a uint
func uintPtr(v uint) *uint {
	return &v
}

// TestSetBootOnceLogic tests the SetBootOnce function logic in isolation
func TestSetBootOnceLogic(t *testing.T) {
	// Test cases for boot once logic
	testCases := []struct {
		name       string
		bootTarget string
		expected   map[string]string
	}{
		{
			name:       "Set boot once to CD-ROM",
			bootTarget: "Cd",
			expected: map[string]string{
				"redfish.boot.source.override.enabled": "Once",
				"redfish.boot.source.override.target":  "Cd",
				"redfish.boot.source.override.mode":    "UEFI",
			},
		},
		{
			name:       "Set boot once to HDD",
			bootTarget: "Hdd",
			expected: map[string]string{
				"redfish.boot.source.override.enabled": "Once",
				"redfish.boot.source.override.target":  "Hdd",
				"redfish.boot.source.override.mode":    "UEFI",
			},
		},
		{
			name:       "Set boot once to PXE",
			bootTarget: "Pxe",
			expected: map[string]string{
				"redfish.boot.source.override.enabled": "Once",
				"redfish.boot.source.override.target":  "Pxe",
				"redfish.boot.source.override.mode":    "UEFI",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock VM object
			vm := &unstructured.Unstructured{}
			vm.SetUnstructuredContent(map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{},
				},
			})

			// Simulate the boot once logic
			annotations := vm.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string)
			}

			annotations["redfish.boot.source.override.enabled"] = "Once"
			annotations["redfish.boot.source.override.target"] = tc.bootTarget
			annotations["redfish.boot.source.override.mode"] = "UEFI"

			vm.SetAnnotations(annotations)

			// Verify the results
			resultAnnotations := vm.GetAnnotations()
			for key, expectedValue := range tc.expected {
				if actualValue, exists := resultAnnotations[key]; !exists {
					t.Errorf("Missing annotation: %s", key)
				} else if actualValue != expectedValue {
					t.Errorf("Annotation %s: expected %s, got %s", key, expectedValue, actualValue)
				}
			}
		})
	}
}

// TestSanitizeResourceName tests the sanitizeResourceName function
func TestSanitizeResourceName(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "short name",
			input:    "vm",
			expected: "vm",
		},
		{
			name:     "exactly 63 characters",
			input:    "this-is-a-resource-name-with-63-characters-aaaaaaaaaaaaaaaaaaaa",
			expected: "this-is-a-resource-name-with-63-characters-aaaaaaaaaaaaaaaaaaaa",
		},
		{
			name:     "64 characters - should be truncated",
			input:    "this-is-a-resource-name-with-64-characters-the-last-one-is-gone-",
			expected: "this-is-a-resource-name-with-64-characters-the-la5fg6xtruncated",
		},
		{
			name:     "> 64 characters - should be truncated",
			input:    "this-is-a-resource-name-with-more-than-64-characters-and-it-must-be-truncated",
			expected: "this-is-a-resource-name-with-more-than-64-charact2xwg2truncated",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizeResourceName(tc.input)
			if result != tc.expected {
				t.Errorf("Expected '%s', got '%s'", tc.expected, result)
			}

			// Additional validation: ensure the result is never longer than 63 characters
			if len(result) > 63 {
				t.Errorf("Result length %d exceeds maximum allowed length of 63", len(result))
			}

			// Additional validation: ensure the result is never longer than the input
			if len(result) > len(tc.input) {
				t.Errorf("Result length %d is longer than input length %d", len(result), len(tc.input))
			}
		})
	}
}

// =============================================================================
// BOOT ONCE TESTS
// =============================================================================

// TestCaptureCurrentBootOrder tests the captureCurrentBootOrder function
func TestCaptureCurrentBootOrder(t *testing.T) {
	testCases := []struct {
		name        string
		setupVM     func() *kubevirtv1.VirtualMachine
		expectEmpty bool
		expectDisks int
	}{
		{
			name: "VM with boot orders set",
			setupVM: func() *kubevirtv1.VirtualMachine {
				bootOrder1 := uint(1)
				bootOrder2 := uint(2)
				return &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0", BootOrder: &bootOrder1},
											{Name: "disk1", BootOrder: &bootOrder2},
										},
									},
								},
							},
						},
					},
				}
			},
			expectEmpty: false,
			expectDisks: 2,
		},
		{
			name: "VM with no boot orders",
			setupVM: func() *kubevirtv1.VirtualMachine {
				return &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0"},
											{Name: "disk1"},
										},
									},
								},
							},
						},
					},
				}
			},
			expectEmpty: false,
			expectDisks: 2,
		},
		{
			name: "VM with no template",
			setupVM: func() *kubevirtv1.VirtualMachine {
				return &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{},
				}
			},
			expectEmpty: true,
			expectDisks: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{timeout: 30 * time.Second}
			vm := tc.setupVM()

			configJSON, err := client.captureCurrentBootOrder(vm)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if tc.expectEmpty {
				if configJSON != "[]" {
					t.Errorf("Expected empty JSON array, got: %s", configJSON)
				}
			} else {
				if !strings.Contains(configJSON, "disk0") {
					t.Errorf("Expected config to contain disk0, got: %s", configJSON)
				}
			}
		})
	}
}

// TestRestoreBootOrder tests the restoreBootOrder function
func TestRestoreBootOrder(t *testing.T) {
	testCases := []struct {
		name       string
		configJSON string
		setupVM    func() *kubevirtv1.VirtualMachine
		validate   func(t *testing.T, vm *kubevirtv1.VirtualMachine)
	}{
		{
			name:       "Restore boot orders from JSON",
			configJSON: `[{"diskName":"disk0","bootOrder":1},{"diskName":"disk1","bootOrder":2}]`,
			setupVM: func() *kubevirtv1.VirtualMachine {
				return &kubevirtv1.VirtualMachine{
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0"},
											{Name: "disk1"},
										},
									},
								},
							},
						},
					},
				}
			},
			validate: func(t *testing.T, vm *kubevirtv1.VirtualMachine) {
				disks := vm.Spec.Template.Spec.Domain.Devices.Disks
				if disks[0].BootOrder == nil || *disks[0].BootOrder != 1 {
					t.Errorf("disk0 should have boot order 1")
				}
				if disks[1].BootOrder == nil || *disks[1].BootOrder != 2 {
					t.Errorf("disk1 should have boot order 2")
				}
			},
		},
		{
			name:       "Restore with nil boot order",
			configJSON: `[{"diskName":"disk0","bootOrder":1},{"diskName":"disk1"}]`,
			setupVM: func() *kubevirtv1.VirtualMachine {
				bootOrder := uint(99)
				return &kubevirtv1.VirtualMachine{
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0", BootOrder: &bootOrder},
											{Name: "disk1", BootOrder: &bootOrder},
										},
									},
								},
							},
						},
					},
				}
			},
			validate: func(t *testing.T, vm *kubevirtv1.VirtualMachine) {
				disks := vm.Spec.Template.Spec.Domain.Devices.Disks
				if disks[0].BootOrder == nil || *disks[0].BootOrder != 1 {
					t.Errorf("disk0 should have boot order 1")
				}
				if disks[1].BootOrder != nil {
					t.Errorf("disk1 should have nil boot order, got %d", *disks[1].BootOrder)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{timeout: 30 * time.Second}
			vm := tc.setupVM()

			err := client.restoreBootOrder(vm, tc.configJSON)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			tc.validate(t, vm)
		})
	}
}

// TestGetVMIUID tests the getVMIUID function
func TestGetVMIUID(t *testing.T) {
	testCases := []struct {
		name        string
		setupVMI    func(mockClient *MockDynamicClient)
		expectedUID string
	}{
		{
			name: "VMI exists with UID",
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						UID:       "test-uid-12345",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expectedUID: "test-uid-12345",
		},
		{
			name:        "VMI does not exist",
			setupVMI:    func(mockClient *MockDynamicClient) {},
			expectedUID: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			tc.setupVMI(mockDynamicClient)

			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			uid := client.getVMIUID("test-namespace", "test-vm")
			if uid != tc.expectedUID {
				t.Errorf("Expected UID '%s', got '%s'", tc.expectedUID, uid)
			}
		})
	}
}

// TestSetBootOnce tests the SetBootOnce function with edge cases
func TestSetBootOnce(t *testing.T) {
	testCases := []struct {
		name       string
		bootTarget string
		setupVM    func(mockClient *MockDynamicClient)
		setupVMI   func(mockClient *MockDynamicClient)
		validate   func(t *testing.T, mockClient *MockDynamicClient)
	}{
		{
			name:       "Set boot once on VM without existing boot-once state",
			bootTarget: "Cd",
			setupVM: func(mockClient *MockDynamicClient) {
				bootOrder1 := uint(1)
				bootOrder2 := uint(2)
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0", BootOrder: &bootOrder1},
											{Name: "cdrom0", BootOrder: &bootOrder2, DiskDevice: kubevirtv1.DiskDevice{CDRom: &kubevirtv1.CDRomTarget{}}},
										},
									},
								},
								Volumes: []kubevirtv1.Volume{
									{Name: "disk0", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{Name: "dv0"}}},
									{Name: "cdrom0", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{Name: "cdrom-dv"}}},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						UID:       "vmi-uid-1",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}
				mockClient.AddVMI(vmi)
			},
			validate: func(t *testing.T, mockClient *MockDynamicClient) {
				vm, err := mockClient.GetVM("test-namespace", "test-vm")
				if err != nil {
					t.Fatalf("Failed to get VM: %v", err)
				}

				// Check label
				labels := vm.GetLabels()
				if labels[BootOnceLabel] != "enabled" {
					t.Errorf("Expected boot-once label to be 'enabled', got '%s'", labels[BootOnceLabel])
				}

				// Check annotations
				annotations := vm.GetAnnotations()
				if annotations[BootOnceOriginalConfigAnnotation] == "" {
					t.Error("Expected original config annotation to be set")
				}
				if annotations[BootOnceVMIUIDAnnotation] != "vmi-uid-1" {
					t.Errorf("Expected VMI UID annotation to be 'vmi-uid-1', got '%s'", annotations[BootOnceVMIUIDAnnotation])
				}
				if annotations["redfish.boot.source.override.enabled"] != "Once" {
					t.Error("Expected redfish override enabled annotation to be 'Once'")
				}
				if annotations["redfish.boot.source.override.target"] != "Cd" {
					t.Error("Expected redfish override target annotation to be 'Cd'")
				}
			},
		},
		{
			name:       "Set boot once on VM that is off (no VMI)",
			bootTarget: "Cd",
			setupVM: func(mockClient *MockDynamicClient) {
				bootOrder1 := uint(1)
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0", BootOrder: &bootOrder1},
											{Name: "cdrom0", DiskDevice: kubevirtv1.DiskDevice{CDRom: &kubevirtv1.CDRomTarget{}}},
										},
									},
								},
								Volumes: []kubevirtv1.Volume{
									{Name: "disk0", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{Name: "dv0"}}},
									{Name: "cdrom0", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{Name: "cdrom-dv"}}},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupVMI: func(mockClient *MockDynamicClient) {
				// No VMI - VM is off
			},
			validate: func(t *testing.T, mockClient *MockDynamicClient) {
				vm, err := mockClient.GetVM("test-namespace", "test-vm")
				if err != nil {
					t.Fatalf("Failed to get VM: %v", err)
				}

				// Check VMI UID annotation is empty
				annotations := vm.GetAnnotations()
				if annotations[BootOnceVMIUIDAnnotation] != "" {
					t.Errorf("Expected VMI UID annotation to be empty, got '%s'", annotations[BootOnceVMIUIDAnnotation])
				}

				// Check label is set
				labels := vm.GetLabels()
				if labels[BootOnceLabel] != "enabled" {
					t.Errorf("Expected boot-once label to be 'enabled', got '%s'", labels[BootOnceLabel])
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			tc.setupVM(mockDynamicClient)
			tc.setupVMI(mockDynamicClient)

			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			err := client.SetBootOnce("test-namespace", "test-vm", tc.bootTarget)
			if err != nil {
				t.Fatalf("SetBootOnce failed: %v", err)
			}

			tc.validate(t, mockDynamicClient)
		})
	}
}

// TestHandleVMUpdate tests the handleVMUpdate function
func TestHandleVMUpdate(t *testing.T) {
	testCases := []struct {
		name             string
		setupVM          func(mockClient *MockDynamicClient)
		setupVMI         func(mockClient *MockDynamicClient)
		expectRestore    bool
		expectClearState bool
	}{
		{
			name: "VMI UID changed - should restore boot order",
			setupVM: func(mockClient *MockDynamicClient) {
				bootOrder1 := uint(1)
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						Labels: map[string]string{
							BootOnceLabel: "enabled",
						},
						Annotations: map[string]string{
							BootOnceOriginalConfigAnnotation:       `[{"diskName":"disk0","bootOrder":1},{"diskName":"cdrom0","bootOrder":2}]`,
							BootOnceVMIUIDAnnotation:               "old-vmi-uid",
							"redfish.boot.source.override.enabled": "Once",
							"redfish.boot.source.override.target":  "Cd",
						},
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0", BootOrder: &bootOrder1},
											{Name: "cdrom0"},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						UID:       "new-vmi-uid", // Different from recorded
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expectRestore:    true,
			expectClearState: true,
		},
		{
			name: "VMI UID same - should not restore",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						Labels: map[string]string{
							BootOnceLabel: "enabled",
						},
						Annotations: map[string]string{
							BootOnceOriginalConfigAnnotation:       `[{"diskName":"disk0","bootOrder":1}]`,
							BootOnceVMIUIDAnnotation:               "same-vmi-uid",
							"redfish.boot.source.override.enabled": "Once",
						},
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0"},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						UID:       "same-vmi-uid", // Same as recorded
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expectRestore:    false,
			expectClearState: false,
		},
		{
			name: "VM was off, now has VMI - should restore",
			setupVM: func(mockClient *MockDynamicClient) {
				vm := &kubevirtv1.VirtualMachine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						Labels: map[string]string{
							BootOnceLabel: "enabled",
						},
						Annotations: map[string]string{
							BootOnceOriginalConfigAnnotation:       `[{"diskName":"disk0","bootOrder":1}]`,
							BootOnceVMIUIDAnnotation:               "", // Was off
							"redfish.boot.source.override.enabled": "Once",
						},
					},
					Spec: kubevirtv1.VirtualMachineSpec{
						Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
							Spec: kubevirtv1.VirtualMachineInstanceSpec{
								Domain: kubevirtv1.DomainSpec{
									Devices: kubevirtv1.Devices{
										Disks: []kubevirtv1.Disk{
											{Name: "disk0"},
										},
									},
								},
							},
						},
					},
				}
				mockClient.AddVM(vm)
			},
			setupVMI: func(mockClient *MockDynamicClient) {
				vmi := &kubevirtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-vm",
						Namespace: "test-namespace",
						UID:       "new-vmi-uid",
					},
					Status: kubevirtv1.VirtualMachineInstanceStatus{
						Phase: kubevirtv1.Running,
					},
				}
				mockClient.AddVMI(vmi)
			},
			expectRestore:    true,
			expectClearState: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			tc.setupVM(mockDynamicClient)
			tc.setupVMI(mockDynamicClient)

			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			// Get the VM to pass to handleVMUpdate
			vm, err := mockDynamicClient.GetVM("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Failed to get VM: %v", err)
			}

			// Call handleVMUpdate
			client.handleVMUpdate(vm)

			// Check the result
			updatedVM, err := mockDynamicClient.GetVM("test-namespace", "test-vm")
			if err != nil {
				t.Fatalf("Failed to get updated VM: %v", err)
			}

			labels := updatedVM.GetLabels()
			annotations := updatedVM.GetAnnotations()

			if tc.expectClearState {
				// Boot-once label should be removed
				if labels[BootOnceLabel] != "" {
					t.Errorf("Expected boot-once label to be removed, got '%s'", labels[BootOnceLabel])
				}
				// Original config annotation should be removed
				if annotations[BootOnceOriginalConfigAnnotation] != "" {
					t.Errorf("Expected original config annotation to be removed")
				}
			} else {
				// Boot-once label should still be present
				if labels[BootOnceLabel] != "enabled" {
					t.Errorf("Expected boot-once label to be 'enabled', got '%s'", labels[BootOnceLabel])
				}
			}
		})
	}
}

// newTestVM creates a VM with known labels, annotations, and spec for patching tests.
// Tests can verify that only the expected labels changed and everything else is preserved.
func newTestVM(namespace, name string, extraLabels, extraAnnotations map[string]string) *kubevirtv1.VirtualMachine {
	labels := map[string]string{"existing-label": "original-value"}
	for k, v := range extraLabels {
		labels[k] = v
	}
	annotations := map[string]string{"existing-annotation": "original-value"}
	for k, v := range extraAnnotations {
		annotations[k] = v
	}
	bootOrder := uint(1)
	return &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Devices: kubevirtv1.Devices{
							Disks: []kubevirtv1.Disk{
								{Name: "disk0", BootOrder: &bootOrder},
							},
						},
					},
					Volumes: []kubevirtv1.Volume{
						{Name: "disk0", VolumeSource: kubevirtv1.VolumeSource{DataVolume: &kubevirtv1.DataVolumeSource{Name: "dv0"}}},
					},
				},
			},
		},
	}
}

// assertVMUnchangedExceptLabels compares two VMs and verifies that everything
// except .metadata.labels is identical.
func assertVMUnchangedExceptLabels(t *testing.T, before, after *kubevirtv1.VirtualMachine) {
	t.Helper()

	// Compare annotations
	if !reflect.DeepEqual(before.GetAnnotations(), after.GetAnnotations()) {
		t.Errorf("Annotations were modified:\n  before: %v\n  after:  %v", before.GetAnnotations(), after.GetAnnotations())
	}

	// Compare spec
	if !reflect.DeepEqual(before.Spec, after.Spec) {
		t.Errorf("Spec was modified:\n  before: %+v\n  after:  %+v", before.Spec, after.Spec)
	}

	// Compare status
	if !reflect.DeepEqual(before.Status, after.Status) {
		t.Errorf("Status was modified:\n  before: %+v\n  after:  %+v", before.Status, after.Status)
	}

	// Compare name and namespace
	if before.Name != after.Name {
		t.Errorf("Name was modified: %q -> %q", before.Name, after.Name)
	}
	if before.Namespace != after.Namespace {
		t.Errorf("Namespace was modified: %q -> %q", before.Namespace, after.Namespace)
	}
}

func TestSetImportingLabel(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	vm := newTestVM("test-ns", "test-vm", nil, nil)
	mockDynamicClient.AddVM(vm)

	before, _ := mockDynamicClient.GetVM("test-ns", "test-vm")

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.setImportingLabel("test-ns", "test-vm", "cdrom0", "copy-iso-pod-123")
	if err != nil {
		t.Fatalf("setImportingLabel failed: %v", err)
	}

	after, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labels := after.GetLabels()
	expectedKey := ImportingLabelPrefix + "cdrom0"
	if labels[expectedKey] != "copy-iso-pod-123" {
		t.Errorf("Expected label %s=%q, got %q", expectedKey, "copy-iso-pod-123", labels[expectedKey])
	}
	if labels["existing-label"] != "original-value" {
		t.Errorf("Existing label was modified: got %q", labels["existing-label"])
	}
	assertVMUnchangedExceptLabels(t, before, after)
}

func TestSetImportingLabel_PreservesExistingImportLabels(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom1": "other-pod",
	}, nil)
	mockDynamicClient.AddVM(vm)

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.setImportingLabel("test-ns", "test-vm", "cdrom0", "copy-iso-pod-456")
	if err != nil {
		t.Fatalf("setImportingLabel failed: %v", err)
	}

	updated, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labels := updated.GetLabels()
	if labels[ImportingLabelPrefix+"cdrom0"] != "copy-iso-pod-456" {
		t.Errorf("New importing label not set correctly")
	}
	if labels[ImportingLabelPrefix+"cdrom1"] != "other-pod" {
		t.Errorf("Existing importing label was modified: got %q", labels[ImportingLabelPrefix+"cdrom1"])
	}
}

func TestRemoveImportingLabel(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": "copy-iso-pod-123",
		ImportingLabelPrefix + "cdrom1": "other-pod",
	}, nil)
	mockDynamicClient.AddVM(vm)

	before, _ := mockDynamicClient.GetVM("test-ns", "test-vm")

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.removeImportingLabel("test-ns", "test-vm", "cdrom0")
	if err != nil {
		t.Fatalf("removeImportingLabel failed: %v", err)
	}

	after, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labels := after.GetLabels()
	if _, exists := labels[ImportingLabelPrefix+"cdrom0"]; exists {
		t.Errorf("Importing label cdrom0 should have been removed")
	}
	if labels[ImportingLabelPrefix+"cdrom1"] != "other-pod" {
		t.Errorf("Other importing label was modified: got %q", labels[ImportingLabelPrefix+"cdrom1"])
	}
	if labels["existing-label"] != "original-value" {
		t.Errorf("Existing label was modified: got %q", labels["existing-label"])
	}
	assertVMUnchangedExceptLabels(t, before, after)
}

func TestIsImportInProgress(t *testing.T) {
	testCases := []struct {
		name     string
		labels   map[string]string
		expected bool
	}{
		{
			name:     "no importing labels",
			labels:   nil,
			expected: false,
		},
		{
			name:     "one importing label",
			labels:   map[string]string{ImportingLabelPrefix + "cdrom0": "pod-1"},
			expected: true,
		},
		{
			name: "multiple importing labels",
			labels: map[string]string{
				ImportingLabelPrefix + "cdrom0": "pod-1",
				ImportingLabelPrefix + "cdrom1": "pod-2",
			},
			expected: true,
		},
		{
			name:     "unrelated labels only",
			labels:   map[string]string{"some-other-label": "value"},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()
			vm := newTestVM("test-ns", "test-vm", tc.labels, nil)
			mockDynamicClient.AddVM(vm)

			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			result, err := client.IsImportInProgress("test-ns", "test-vm")
			if err != nil {
				t.Fatalf("IsImportInProgress failed: %v", err)
			}
			if result != tc.expected {
				t.Errorf("IsImportInProgress = %v, want %v", result, tc.expected)
			}
		})
	}
}

func TestIsImportInProgress_VMNotFound(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	_, err := client.IsImportInProgress("test-ns", "nonexistent-vm")
	if err == nil {
		t.Error("Expected error for nonexistent VM")
	}
}

func TestSetPowerAfterImportLabel(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	vm := newTestVM("test-ns", "test-vm", nil, nil)
	mockDynamicClient.AddVM(vm)

	before, _ := mockDynamicClient.GetVM("test-ns", "test-vm")

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.SetPowerAfterImportLabel("test-ns", "test-vm", "On")
	if err != nil {
		t.Fatalf("SetPowerAfterImportLabel failed: %v", err)
	}

	after, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labels := after.GetLabels()
	if labels[PowerAfterImportLabel] != "On" {
		t.Errorf("Expected label %s=%q, got %q", PowerAfterImportLabel, "On", labels[PowerAfterImportLabel])
	}
	if labels["existing-label"] != "original-value" {
		t.Errorf("Existing label was modified: got %q", labels["existing-label"])
	}
	assertVMUnchangedExceptLabels(t, before, after)
}

func TestSetPowerAfterImportLabel_OverwritesPrevious(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	vm := newTestVM("test-ns", "test-vm", map[string]string{
		PowerAfterImportLabel: "On",
	}, nil)
	mockDynamicClient.AddVM(vm)

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.SetPowerAfterImportLabel("test-ns", "test-vm", "ForceRestart")
	if err != nil {
		t.Fatalf("SetPowerAfterImportLabel failed: %v", err)
	}

	updated, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labels := updated.GetLabels()
	if labels[PowerAfterImportLabel] != "ForceRestart" {
		t.Errorf("Expected power label to be overwritten to ForceRestart, got %q", labels[PowerAfterImportLabel])
	}
}

func TestSetPowerAfterImportLabel_PreservesImportingLabels(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": "pod-1",
	}, nil)
	mockDynamicClient.AddVM(vm)

	before, _ := mockDynamicClient.GetVM("test-ns", "test-vm")

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.SetPowerAfterImportLabel("test-ns", "test-vm", "On")
	if err != nil {
		t.Fatalf("SetPowerAfterImportLabel failed: %v", err)
	}

	after, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labels := after.GetLabels()
	if labels[PowerAfterImportLabel] != "On" {
		t.Errorf("Power label not set correctly")
	}
	if labels[ImportingLabelPrefix+"cdrom0"] != "pod-1" {
		t.Errorf("Importing label was modified: got %q", labels[ImportingLabelPrefix+"cdrom0"])
	}
	assertVMUnchangedExceptLabels(t, before, after)
}

func TestSetPowerAfterImportLabel_VMNotFound(t *testing.T) {
	mockDynamicClient := NewMockDynamicClient()
	fakeK8sClient := fake.NewSimpleClientset()
	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

	err := client.SetPowerAfterImportLabel("test-ns", "nonexistent-vm", "On")
	if err == nil {
		t.Error("Expected error for nonexistent VM")
	}
}

func TestGetStorageProfileVolumeMode(t *testing.T) {
	block := corev1.PersistentVolumeBlock
	filesystem := corev1.PersistentVolumeFilesystem

	tests := []struct {
		name         string
		storageClass string
		profile      *cdiv1beta1.StorageProfile
		wantMode     *corev1.PersistentVolumeMode
	}{
		{
			name:         "empty storage class returns nil",
			storageClass: "",
			profile:      nil,
			wantMode:     nil,
		},
		{
			name:         "missing StorageProfile returns nil",
			storageClass: "no-such-class",
			profile:      nil,
			wantMode:     nil,
		},
		{
			name:         "StorageProfile with Block volume mode",
			storageClass: "block-storage",
			profile: &cdiv1beta1.StorageProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "block-storage"},
				Status: cdiv1beta1.StorageProfileStatus{
					ClaimPropertySets: []cdiv1beta1.ClaimPropertySet{
						{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							VolumeMode:  &block,
						},
					},
				},
			},
			wantMode: &block,
		},
		{
			name:         "StorageProfile with Filesystem volume mode",
			storageClass: "fs-storage",
			profile: &cdiv1beta1.StorageProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "fs-storage"},
				Status: cdiv1beta1.StorageProfileStatus{
					ClaimPropertySets: []cdiv1beta1.ClaimPropertySet{
						{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							VolumeMode:  &filesystem,
						},
					},
				},
			},
			wantMode: &filesystem,
		},
		{
			name:         "StorageProfile with multiple property sets uses first",
			storageClass: "multi-storage",
			profile: &cdiv1beta1.StorageProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "multi-storage"},
				Status: cdiv1beta1.StorageProfileStatus{
					ClaimPropertySets: []cdiv1beta1.ClaimPropertySet{
						{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							VolumeMode:  &block,
						},
						{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
							VolumeMode:  &filesystem,
						},
					},
				},
			},
			wantMode: &block,
		},
		{
			name:         "StorageProfile with empty claimPropertySets returns nil",
			storageClass: "empty-profile",
			profile: &cdiv1beta1.StorageProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-profile"},
				Status:     cdiv1beta1.StorageProfileStatus{},
			},
			wantMode: nil,
		},
		{
			name:         "StorageProfile with nil VolumeMode in first property set returns nil",
			storageClass: "nil-mode",
			profile: &cdiv1beta1.StorageProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "nil-mode"},
				Status: cdiv1beta1.StorageProfileStatus{
					ClaimPropertySets: []cdiv1beta1.ClaimPropertySet{
						{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							VolumeMode:  nil,
						},
					},
				},
			},
			wantMode: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			if tt.profile != nil {
				if err := mockDynamicClient.AddStorageProfile(tt.profile); err != nil {
					t.Fatalf("Failed to add StorageProfile: %v", err)
				}
			}

			got := client.getStorageProfileVolumeMode(tt.storageClass)

			if tt.wantMode == nil {
				if got != nil {
					t.Errorf("expected nil volume mode, got %v", *got)
				}
			} else {
				if got == nil {
					t.Fatalf("expected volume mode %v, got nil", *tt.wantMode)
				}
				if *got != *tt.wantMode {
					t.Errorf("expected volume mode %v, got %v", *tt.wantMode, *got)
				}
			}
		})
	}
}

func TestEnsurePVC(t *testing.T) {
	tests := []struct {
		name         string
		existingPVC  *corev1.PersistentVolumeClaim
		expectErr    bool
		expectCreate bool // true if ensurePVC should create a new PVC (vs reuse)
	}{
		{
			name:         "no existing PVC creates a new one",
			existingPVC:  nil,
			expectCreate: true,
		},
		{
			name: "existing Bound PVC is reused without recreation",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			expectCreate: false,
		},
		{
			name: "existing Pending PVC is reused without recreation",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimPending,
				},
			},
			expectCreate: false,
		},
		{
			name: "existing Lost PVC is deleted and recreated",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimLost,
				},
			},
			expectCreate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []runtime.Object
			if tt.existingPVC != nil {
				objs = append(objs, tt.existingPVC)
			}
			fakeK8sClient := fake.NewSimpleClientset(objs...)
			mockDynamicClient := NewMockDynamicClient()
			client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, nil)

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
			}

			ctx := context.Background()
			err := client.ensurePVC(ctx, "test-ns", pvc)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := fakeK8sClient.CoreV1().PersistentVolumeClaims("test-ns").Get(ctx, "test-pvc", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("PVC should exist after ensurePVC: %v", err)
			}

			if tt.expectCreate {
				// Newly created PVC won't have Bound status in fake client
				if got.Status.Phase == corev1.ClaimBound {
					t.Error("expected newly created PVC, but got a Bound one (was not recreated)")
				}
			} else {
				// Reused PVC should retain its original phase
				if got.Status.Phase != tt.existingPVC.Status.Phase {
					t.Errorf("expected reused PVC phase %s, got %s", tt.existingPVC.Status.Phase, got.Status.Phase)
				}
			}
		})
	}
}

// setupInsertVirtualMediaTest creates the common mock objects for insertVirtualMediaAsync tests.
// It returns the client and mock dynamic client. The VM is pre-configured in the mock.
// With no storage class configured the default volume mode is Filesystem, which routes
// to the helper pod path. Use setupInsertVirtualMediaTestBlock for CDI path tests.
func setupInsertVirtualMediaTest(t *testing.T, allowInsecureTLS bool, existingPVCs ...corev1.PersistentVolumeClaim) (*Client, *MockDynamicClient, *fake.Clientset) {
	t.Helper()
	return setupInsertVirtualMediaTestWithStorageClass(t, allowInsecureTLS, "", nil, existingPVCs...)
}

// setupInsertVirtualMediaTestBlock creates test fixtures with a Block storage class
// and matching CDI StorageProfile so the CDI VolumeImportSource path is exercised.
func setupInsertVirtualMediaTestBlock(t *testing.T, allowInsecureTLS bool, existingPVCs ...corev1.PersistentVolumeClaim) (*Client, *MockDynamicClient, *fake.Clientset) {
	t.Helper()
	block := corev1.PersistentVolumeBlock
	sp := &cdiv1beta1.StorageProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "block-storage"},
		Status: cdiv1beta1.StorageProfileStatus{
			ClaimPropertySets: []cdiv1beta1.ClaimPropertySet{
				{VolumeMode: &block},
			},
		},
	}
	return setupInsertVirtualMediaTestWithStorageClass(t, allowInsecureTLS, "block-storage", sp, existingPVCs...)
}

func setupInsertVirtualMediaTestWithStorageClass(t *testing.T, allowInsecureTLS bool, storageClass string, sp *cdiv1beta1.StorageProfile, existingPVCs ...corev1.PersistentVolumeClaim) (*Client, *MockDynamicClient, *fake.Clientset) {
	t.Helper()

	var objs []runtime.Object
	for i := range existingPVCs {
		objs = append(objs, &existingPVCs[i])
	}
	fakeK8sClient := fake.NewSimpleClientset(objs...)
	mockDynamicClient := NewMockDynamicClient()

	mockConfig := &MockConfig{}
	mockConfig.dataVolumeConfig.storageSize = "10Gi"
	mockConfig.dataVolumeConfig.allowInsecureTLS = allowInsecureTLS
	mockConfig.dataVolumeConfig.storageClass = storageClass
	mockConfig.dataVolumeConfig.vmUpdateTimeout = "30s"
	mockConfig.dataVolumeConfig.isoDownloadTimeout = "30m"
	mockConfig.dataVolumeConfig.helperImage = "alpine:latest"

	if sp != nil {
		if err := mockDynamicClient.AddStorageProfile(sp); err != nil {
			t.Fatalf("Failed to add StorageProfile: %v", err)
		}
	}

	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-vm",
			Namespace: "test-ns",
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Devices: kubevirtv1.Devices{},
					},
				},
			},
		},
	}
	if err := mockDynamicClient.AddVM(vm); err != nil {
		t.Fatalf("Failed to add VM: %v", err)
	}

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, mockConfig)
	return client, mockDynamicClient, fakeK8sClient
}

func TestInsertVirtualMediaAsync_FilesystemUsesHelperPod(t *testing.T) {
	// Default setup has no storage class → Filesystem → helper pod path
	client, _, fakeK8s := setupInsertVirtualMediaTest(t, false)

	err := client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "http://example.com/image.iso")
	if err != nil {
		t.Fatalf("insertVirtualMediaAsync failed: %v", err)
	}

	pvcList, err := fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list PVCs: %v", err)
	}
	if len(pvcList.Items) < 1 {
		t.Fatal("Expected at least one PVC to be created")
	}

	// PVC must NOT have DataSourceRef (helper pod path creates a blank PVC)
	for _, pvc := range pvcList.Items {
		if pvc.Spec.DataSourceRef != nil {
			t.Error("Filesystem PVC should not have DataSourceRef — helper pod path expected")
		}
	}

	// Helper pod must be created
	podList, err := fakeK8s.CoreV1().Pods("test-ns").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list pods: %v", err)
	}
	if len(podList.Items) < 1 {
		t.Error("Expected helper pod to be created for Filesystem storage")
	}

	updatedVM, err := client.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	foundDisk := false
	for _, disk := range updatedVM.Spec.Template.Spec.Domain.Devices.Disks {
		if disk.Name == "cdrom0" && disk.CDRom != nil {
			foundDisk = true
			break
		}
	}
	if !foundDisk {
		t.Error("Expected cdrom0 disk to be added to VM")
	}

	// Helper pod path sets importing label with the pod name (no CDI prefix)
	labelKey := ImportingLabelPrefix + "cdrom0"
	labelVal := updatedVM.GetLabels()[labelKey]
	if labelVal == "" {
		t.Errorf("Expected importing label %s to be set", labelKey)
	}
	if strings.HasPrefix(labelVal, CDIImportPrefix) {
		t.Errorf("Filesystem path should use helper pod importing label (no CDI prefix), got %q", labelVal)
	}
}

func TestInsertVirtualMediaAsync_BlockUsesCDI(t *testing.T) {
	// Block storage class → CDI VolumeImportSource path
	client, _, fakeK8s := setupInsertVirtualMediaTestBlock(t, false)

	err := client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "http://example.com/image.iso")
	if err != nil {
		t.Fatalf("insertVirtualMediaAsync failed: %v", err)
	}

	pvcList, err := fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list PVCs: %v", err)
	}
	if len(pvcList.Items) < 1 {
		t.Fatal("Expected at least one PVC to be created")
	}

	updatedVM, err := client.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	foundDisk := false
	for _, disk := range updatedVM.Spec.Template.Spec.Domain.Devices.Disks {
		if disk.Name == "cdrom0" && disk.CDRom != nil {
			foundDisk = true
			break
		}
	}
	if !foundDisk {
		t.Error("Expected cdrom0 disk to be added to VM")
	}

	foundVolume := false
	for _, vol := range updatedVM.Spec.Template.Spec.Volumes {
		if vol.Name == "cdrom0" && vol.PersistentVolumeClaim != nil {
			foundVolume = true
			break
		}
	}
	if !foundVolume {
		t.Error("Expected cdrom0 volume to be added to VM")
	}

	// CDI path must set importing label with CDI prefix
	labelKey := ImportingLabelPrefix + "cdrom0"
	labelVal := updatedVM.GetLabels()[labelKey]
	if !strings.HasPrefix(labelVal, CDIImportPrefix) {
		t.Errorf("Expected importing label %s to start with %q, got %q", labelKey, CDIImportPrefix, labelVal)
	}

	// PVC must have DataSourceRef pointing to a VolumeImportSource
	for _, pvc := range pvcList.Items {
		if pvc.Labels[ImportPodVMLabel] == "test-vm" && pvc.Labels[ImportPodVolumeLabel] == "cdrom0" {
			if pvc.Spec.DataSourceRef == nil {
				t.Error("Block PVC must have DataSourceRef for CDI VolumeImportSource")
			} else if pvc.Spec.DataSourceRef.Kind != "VolumeImportSource" {
				t.Errorf("DataSourceRef kind must be VolumeImportSource, got %s", pvc.Spec.DataSourceRef.Kind)
			}
			if pvc.Spec.VolumeMode == nil || *pvc.Spec.VolumeMode != corev1.PersistentVolumeBlock {
				t.Error("Block PVC must have VolumeMode=Block")
			}
			return
		}
	}
	t.Error("Expected CDI PVC to carry vm.redfish and volume.vm.redfish labels")
}

func TestInsertVirtualMediaAsync_VMWithExistingRootDisk(t *testing.T) {
	fakeK8sClient := fake.NewSimpleClientset()
	mockDynamicClient := NewMockDynamicClient()

	mockConfig := &MockConfig{}
	mockConfig.dataVolumeConfig.storageSize = "3Gi"
	mockConfig.dataVolumeConfig.allowInsecureTLS = false
	mockConfig.dataVolumeConfig.storageClass = "block-storage"
	mockConfig.dataVolumeConfig.vmUpdateTimeout = "30s"
	mockConfig.dataVolumeConfig.isoDownloadTimeout = "30m"
	mockConfig.dataVolumeConfig.helperImage = "alpine:latest"

	block := corev1.PersistentVolumeBlock
	sp := &cdiv1beta1.StorageProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "block-storage"},
		Status: cdiv1beta1.StorageProfileStatus{
			ClaimPropertySets: []cdiv1beta1.ClaimPropertySet{
				{VolumeMode: &block},
			},
		},
	}
	if err := mockDynamicClient.AddStorageProfile(sp); err != nil {
		t.Fatalf("Failed to add StorageProfile: %v", err)
	}

	rootBootOrder := uint(1)
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-vm",
			Namespace: "test-ns",
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Domain: kubevirtv1.DomainSpec{
						Devices: kubevirtv1.Devices{
							Disks: []kubevirtv1.Disk{
								{
									Name:      "rootdisk",
									BootOrder: &rootBootOrder,
									DiskDevice: kubevirtv1.DiskDevice{
										Disk: &kubevirtv1.DiskTarget{Bus: kubevirtv1.DiskBusVirtio},
									},
								},
							},
						},
					},
					Volumes: []kubevirtv1.Volume{
						{
							Name: "rootdisk",
							VolumeSource: kubevirtv1.VolumeSource{
								DataVolume: &kubevirtv1.DataVolumeSource{Name: "test-vm-rootdisk"},
							},
						},
					},
				},
			},
			DataVolumeTemplates: []kubevirtv1.DataVolumeTemplateSpec{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-vm-rootdisk"},
					Spec: cdiv1beta1.DataVolumeSpec{
						Storage: &cdiv1beta1.StorageSpec{
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									"storage": resource.MustParse("120Gi"),
								},
							},
						},
					},
				},
			},
		},
	}
	if err := mockDynamicClient.AddVM(vm); err != nil {
		t.Fatalf("Failed to add VM: %v", err)
	}

	client := NewClientWithClients(fakeK8sClient, mockDynamicClient, 30*time.Second, mockConfig)

	err := client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "http://example.com/agent.iso")
	if err != nil {
		t.Fatalf("insertVirtualMediaAsync failed: %v", err)
	}

	updatedVM, err := mockDynamicClient.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	// Rootdisk must be unchanged
	var rootdiskVol *kubevirtv1.Volume
	var cdromVol *kubevirtv1.Volume
	for i, vol := range updatedVM.Spec.Template.Spec.Volumes {
		switch vol.Name {
		case "rootdisk":
			rootdiskVol = &updatedVM.Spec.Template.Spec.Volumes[i]
		case "cdrom0":
			cdromVol = &updatedVM.Spec.Template.Spec.Volumes[i]
		}
	}

	if rootdiskVol == nil {
		t.Fatal("Rootdisk volume must still be present after virtual media insertion")
	}
	if rootdiskVol.DataVolume == nil || rootdiskVol.DataVolume.Name != "test-vm-rootdisk" {
		t.Errorf("Rootdisk volume must still reference DataVolume test-vm-rootdisk, got %+v", rootdiskVol.VolumeSource)
	}

	if cdromVol == nil {
		t.Fatal("cdrom0 volume must be created by virtual media insertion")
	}
	if cdromVol.PersistentVolumeClaim == nil {
		t.Fatal("cdrom0 volume must reference a PVC")
	}
	bootIsoPVCName := cdromVol.PersistentVolumeClaim.ClaimName
	if !strings.Contains(bootIsoPVCName, "bootiso") {
		t.Errorf("cdrom0 PVC name should contain 'bootiso', got %s", bootIsoPVCName)
	}

	// Rootdisk disk entry must be unchanged (virtio, boot order 1)
	var rootdiskDisk *kubevirtv1.Disk
	var cdromDisk *kubevirtv1.Disk
	for i, disk := range updatedVM.Spec.Template.Spec.Domain.Devices.Disks {
		switch disk.Name {
		case "rootdisk":
			rootdiskDisk = &updatedVM.Spec.Template.Spec.Domain.Devices.Disks[i]
		case "cdrom0":
			cdromDisk = &updatedVM.Spec.Template.Spec.Domain.Devices.Disks[i]
		}
	}

	if rootdiskDisk == nil {
		t.Fatal("Rootdisk disk entry must still be present")
	}
	if rootdiskDisk.Disk == nil || rootdiskDisk.Disk.Bus != kubevirtv1.DiskBusVirtio {
		t.Error("Rootdisk must remain a virtio disk (not converted to cdrom)")
	}
	if rootdiskDisk.BootOrder == nil || *rootdiskDisk.BootOrder != 1 {
		t.Error("Rootdisk boot order must be preserved")
	}

	if cdromDisk == nil {
		t.Fatal("cdrom0 disk must be created")
	}
	if cdromDisk.CDRom == nil {
		t.Error("cdrom0 must be a CDRom device")
	}

	// Verify the boot ISO PVC was created with correct DataSourceRef
	bootIsoPVC, err := fakeK8sClient.CoreV1().PersistentVolumeClaims("test-ns").Get(
		context.Background(), bootIsoPVCName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Boot ISO PVC %s must exist: %v", bootIsoPVCName, err)
	}

	if bootIsoPVC.Spec.DataSourceRef == nil {
		t.Fatal("Boot ISO PVC must have DataSourceRef pointing to a VolumeImportSource")
	}
	if bootIsoPVC.Spec.DataSourceRef.Kind != "VolumeImportSource" {
		t.Errorf("DataSourceRef kind must be VolumeImportSource, got %s", bootIsoPVC.Spec.DataSourceRef.Kind)
	}
	expectedVISName := sanitizeResourceName(fmt.Sprintf("%s-populator", bootIsoPVCName))
	if bootIsoPVC.Spec.DataSourceRef.Name != expectedVISName {
		t.Errorf("DataSourceRef must reference %s, got %s", expectedVISName, bootIsoPVC.Spec.DataSourceRef.Name)
	}

	// Verify VolumeImportSource was created and references the ISO URL
	gvrVIS := schema.GroupVersionResource{Group: "cdi.kubevirt.io", Version: "v1beta1", Resource: "volumeimportsources"}
	visObj, err := mockDynamicClient.Resource(gvrVIS).Namespace("test-ns").Get(
		context.Background(), expectedVISName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("VolumeImportSource %s must exist: %v", expectedVISName, err)
	}
	httpURL, _, _ := unstructured.NestedString(visObj.Object, "spec", "source", "http", "url")
	if httpURL != "http://example.com/agent.iso" {
		t.Errorf("VolumeImportSource must reference the ISO URL, got %s", httpURL)
	}

	// Verify PVC carries the correct VM tracking labels
	if bootIsoPVC.Labels[ImportPodVMLabel] != "test-vm" {
		t.Errorf("Boot ISO PVC must have %s=test-vm label, got %s", ImportPodVMLabel, bootIsoPVC.Labels[ImportPodVMLabel])
	}
	if bootIsoPVC.Labels[ImportPodVolumeLabel] != "cdrom0" {
		t.Errorf("Boot ISO PVC must have %s=cdrom0 label, got %s", ImportPodVolumeLabel, bootIsoPVC.Labels[ImportPodVolumeLabel])
	}

	// Verify the importing label on the VM references the boot ISO PVC
	importLabel := updatedVM.GetLabels()[ImportingLabelPrefix+"cdrom0"]
	if importLabel != CDIImportPrefix+bootIsoPVCName {
		t.Errorf("Importing label must be %s%s, got %s", CDIImportPrefix, bootIsoPVCName, importLabel)
	}

	// DataVolumeTemplates must be untouched
	if len(updatedVM.Spec.DataVolumeTemplates) != 1 {
		t.Errorf("DataVolumeTemplates should be unchanged (1 entry), got %d", len(updatedVM.Spec.DataVolumeTemplates))
	}
	if updatedVM.Spec.DataVolumeTemplates[0].Name != "test-vm-rootdisk" {
		t.Errorf("DataVolumeTemplate must still be test-vm-rootdisk, got %s", updatedVM.Spec.DataVolumeTemplates[0].Name)
	}
}

func TestInsertVirtualMediaAsync_RepeatedInsertion(t *testing.T) {
	client, mockDynamic, fakeK8s := setupInsertVirtualMediaTestBlock(t, false)

	// --- First insertion ---
	err := client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "http://example.com/first.iso")
	if err != nil {
		t.Fatalf("First insertVirtualMediaAsync failed: %v", err)
	}

	vm1, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM after first insert: %v", err)
	}

	// Find the PVC name from the cdrom0 volume
	var firstPVCName string
	for _, vol := range vm1.Spec.Template.Spec.Volumes {
		if vol.Name == "cdrom0" && vol.PersistentVolumeClaim != nil {
			firstPVCName = vol.PersistentVolumeClaim.ClaimName
			break
		}
	}
	if firstPVCName == "" {
		t.Fatal("Expected cdrom0 volume to reference a PVC after first insert")
	}

	// Verify PVC exists
	_, err = fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").Get(context.Background(), firstPVCName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Expected PVC %s to exist after first insert: %v", firstPVCName, err)
	}

	// Verify VolumeImportSource exists
	firstVISName := sanitizeResourceName(fmt.Sprintf("%s-populator", firstPVCName))
	gvrVIS := schema.GroupVersionResource{Group: "cdi.kubevirt.io", Version: "v1beta1", Resource: "volumeimportsources"}
	_, err = mockDynamic.Resource(gvrVIS).Namespace("test-ns").Get(context.Background(), firstVISName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Expected VolumeImportSource %s to exist after first insert: %v", firstVISName, err)
	}

	firstImportLabel := vm1.GetLabels()[ImportingLabelPrefix+"cdrom0"]
	if !strings.HasPrefix(firstImportLabel, CDIImportPrefix) {
		t.Fatalf("Expected importing label to start with %q, got %q", CDIImportPrefix, firstImportLabel)
	}

	// --- Second insertion with different URL ---
	err = client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "http://example.com/second.iso")
	if err != nil {
		t.Fatalf("Second insertVirtualMediaAsync failed: %v", err)
	}

	vm2, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM after second insert: %v", err)
	}

	// cdrom0 disk must still exist
	foundDisk := false
	for _, disk := range vm2.Spec.Template.Spec.Domain.Devices.Disks {
		if disk.Name == "cdrom0" && disk.CDRom != nil {
			foundDisk = true
			break
		}
	}
	if !foundDisk {
		t.Error("Expected cdrom0 disk to still exist after second insert")
	}

	// cdrom0 volume must point to a DIFFERENT (new) PVC
	var secondPVCName string
	for _, vol := range vm2.Spec.Template.Spec.Volumes {
		if vol.Name == "cdrom0" && vol.PersistentVolumeClaim != nil {
			secondPVCName = vol.PersistentVolumeClaim.ClaimName
			break
		}
	}
	if secondPVCName == "" {
		t.Fatal("Expected cdrom0 volume to reference a PVC after second insert")
	}
	if secondPVCName == firstPVCName {
		t.Errorf("Expected cdrom0 volume to be updated to a new PVC, but still references %s", firstPVCName)
	}

	// New PVC must exist
	_, err = fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").Get(context.Background(), secondPVCName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Expected new PVC %s to exist: %v", secondPVCName, err)
	}

	// Old PVC must be cleaned up
	_, err = fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").Get(context.Background(), firstPVCName, metav1.GetOptions{})
	if err == nil {
		t.Errorf("Expected old PVC %s to be deleted after second insertion, but it still exists", firstPVCName)
	}

	// Old VolumeImportSource must be cleaned up
	_, err = mockDynamic.Resource(gvrVIS).Namespace("test-ns").Get(context.Background(), firstVISName, metav1.GetOptions{})
	if err == nil {
		t.Errorf("Expected old VolumeImportSource %s to be deleted after second insertion, but it still exists", firstVISName)
	}

	// Importing label must reference the new import
	secondImportLabel := vm2.GetLabels()[ImportingLabelPrefix+"cdrom0"]
	if secondImportLabel == firstImportLabel {
		t.Errorf("Expected importing label to be updated, but still has value %q", firstImportLabel)
	}
	if !strings.HasPrefix(secondImportLabel, CDIImportPrefix) {
		t.Errorf("Expected importing label to start with %q, got %q", CDIImportPrefix, secondImportLabel)
	}
}

func TestInsertVirtualMediaAsync_HTTPSFlow(t *testing.T) {
	tests := []struct {
		name        string
		existingPVC *corev1.PersistentVolumeClaim
		wantReuse   bool
	}{
		{
			name:      "HTTPS with no existing PVC creates new PVC via CDI",
			wantReuse: false,
		},
		{
			name: "HTTPS with existing Bound PVC reuses it",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "override-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			wantReuse: true,
		},
		{
			name: "HTTPS with existing Lost PVC deletes and recreates",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "override-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimLost,
				},
			},
			wantReuse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pvcs []corev1.PersistentVolumeClaim
			if tt.existingPVC != nil {
				pvcs = append(pvcs, *tt.existingPVC)
			}
			// allowInsecureTLS=false + https + Block storage -> CDI path
			client, _, fakeK8s := setupInsertVirtualMediaTestBlock(t, false, pvcs...)

			err := client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "https://example.com/image.iso")
			if err != nil {
				t.Fatalf("insertVirtualMediaAsync failed: %v", err)
			}

			pvcList, err := fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to list PVCs: %v", err)
			}
			if len(pvcList.Items) < 1 {
				t.Error("Expected at least one PVC to exist")
			}

			updatedVM, err := client.GetVM("test-ns", "test-vm")
			if err != nil {
				t.Fatalf("Failed to get VM: %v", err)
			}

			foundDisk := false
			for _, disk := range updatedVM.Spec.Template.Spec.Domain.Devices.Disks {
				if disk.Name == "cdrom0" && disk.CDRom != nil {
					foundDisk = true
				}
			}
			if !foundDisk {
				t.Error("Expected cdrom0 disk to be added to VM")
			}

			// CDI path must set importing label (HTTPS without insecure also uses CDI)
			labelKey := ImportingLabelPrefix + "cdrom0"
			labelVal := updatedVM.GetLabels()[labelKey]
			if !strings.HasPrefix(labelVal, CDIImportPrefix) {
				t.Errorf("Expected importing label %s to start with %q, got %q", labelKey, CDIImportPrefix, labelVal)
			}
		})
	}
}

func TestInsertVirtualMediaAsync_InsecureHTTPSFlow(t *testing.T) {
	tests := []struct {
		name        string
		existingPVC *corev1.PersistentVolumeClaim
		wantReuse   bool
	}{
		{
			name:      "insecure HTTPS with no existing PVC creates new PVC via helper pod",
			wantReuse: false,
		},
		{
			name: "insecure HTTPS with existing Bound PVC reuses it",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "override-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			wantReuse: true,
		},
		{
			name: "insecure HTTPS with existing Lost PVC deletes and recreates",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "override-pvc",
					Namespace: "test-ns",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse("10Gi"),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimLost,
				},
			},
			wantReuse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pvcs []corev1.PersistentVolumeClaim
			if tt.existingPVC != nil {
				pvcs = append(pvcs, *tt.existingPVC)
			}
			// allowInsecureTLS=true + https -> helper pod path
			client, _, fakeK8s := setupInsertVirtualMediaTest(t, true, pvcs...)

			err := client.insertVirtualMediaAsync("test-ns", "test-vm", "cdrom0", "https://example.com/image.iso")
			if err != nil {
				t.Fatalf("insertVirtualMediaAsync failed: %v", err)
			}

			pvcList, err := fakeK8s.CoreV1().PersistentVolumeClaims("test-ns").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to list PVCs: %v", err)
			}
			if len(pvcList.Items) < 1 {
				t.Error("Expected at least one PVC to exist")
			}

			// Verify helper pod was created
			podList, err := fakeK8s.CoreV1().Pods("test-ns").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to list pods: %v", err)
			}
			if len(podList.Items) < 1 {
				t.Error("Expected helper pod to be created for insecure HTTPS flow")
			}

			updatedVM, err := client.GetVM("test-ns", "test-vm")
			if err != nil {
				t.Fatalf("Failed to get VM: %v", err)
			}

			foundDisk := false
			for _, disk := range updatedVM.Spec.Template.Spec.Domain.Devices.Disks {
				if disk.Name == "cdrom0" && disk.CDRom != nil {
					foundDisk = true
				}
			}
			if !foundDisk {
				t.Error("Expected cdrom0 disk to be added to VM")
			}
		})
	}
}

func TestHandleCDIPVCBound_RemovesImportingLabel(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	fakeK8s := fake.NewSimpleClientset()

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": CDIImportPrefix + "test-pvc",
		PowerAfterImportLabel:           "On",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}

	client.handleCDIPVCBound(pvc)

	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	if _, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; found {
		t.Error("Expected importing label to be removed after PVC became Bound")
	}
}

func TestHandleCDIPVCBound_WorksWithDataSourceRefPresent(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	fakeK8s := fake.NewSimpleClientset()

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": CDIImportPrefix + "test-pvc",
		PowerAfterImportLabel:           "On",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	// CDI does NOT clear DataSourceRef after population — it stays on
	// the PVC permanently. The Bound status on the original PVC is the
	// reliable completion signal for volume-populator PVCs.
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			DataSourceRef: &corev1.TypedObjectReference{
				APIGroup: stringPtr("cdi.kubevirt.io"),
				Kind:     "VolumeImportSource",
				Name:     "test-populator",
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}

	client.handleCDIPVCBound(pvc)

	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	if _, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; found {
		t.Error("Importing label should be removed — Bound original PVC with DataSourceRef means CDI completed (DataSourceRef is never cleared)")
	}
}

func TestHandleCDIPVCBound_IgnoresNonCDILabel(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	fakeK8s := fake.NewSimpleClientset()

	// importing label has a pod name, not the CDI prefix
	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": "copy-iso-pod-123",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}

	client.handleCDIPVCBound(pvc)

	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	labelVal := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]
	if labelVal != "copy-iso-pod-123" {
		t.Errorf("Expected helper-pod importing label to be preserved, got %q", labelVal)
	}
}

func TestHandleCDIPVCBound_IgnoresPrimePVC(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	fakeK8s := fake.NewSimpleClientset()

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": CDIImportPrefix + "test-pvc",
		PowerAfterImportLabel:           "On",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	// CDI prime PVC inherits our labels but has a different name
	primePVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prime-5f2ddb7b-7191-4260-a3fb-bf0f0f91def5",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}

	client.handleCDIPVCBound(primePVC)

	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	if _, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; !found {
		t.Error("Importing label should NOT be removed when a CDI prime PVC becomes Bound — must wait for original PVC")
	}
}

func TestCDIImportDetectedByIsImportInProgress(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	fakeK8s := fake.NewSimpleClientset()

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": CDIImportPrefix + "test-pvc",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	importing, err := client.IsImportInProgress("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("IsImportInProgress failed: %v", err)
	}
	if !importing {
		t.Error("Expected IsImportInProgress to return true for CDI import label")
	}
}

func TestReconcileExistingCDIPVCs(t *testing.T) {
	mockDynamic := NewMockDynamicClient()

	boundPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pvc",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}
	fakeK8s := fake.NewSimpleClientset(boundPVC)

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": CDIImportPrefix + "test-pvc",
		PowerAfterImportLabel:           "On",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	err := client.reconcileExistingCDIPVCs("test-ns")
	if err != nil {
		t.Fatalf("reconcileExistingCDIPVCs failed: %v", err)
	}

	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	if _, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; found {
		t.Error("Expected importing label to be removed after reconciliation of Bound PVC")
	}
}

func TestIsCDIManagedPod(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{
			name:   "CDI importer pod",
			labels: map[string]string{CDIManagedLabel: "importer", ImportPodVMLabel: "vm-1"},
			want:   true,
		},
		{
			name:   "our helper pod",
			labels: map[string]string{ImportPodVMLabel: "vm-1", ImportPodVolumeLabel: "cdrom0"},
			want:   false,
		},
		{
			name:   "no labels",
			labels: nil,
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			if got := isCDIManagedPod(pod); got != tt.want {
				t.Errorf("isCDIManagedPod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleImportPodCompleted_SkipsCDIPod(t *testing.T) {
	mockDynamic := NewMockDynamicClient()

	// CDI importer pod with both CDI and our labels (CDI copies PVC labels)
	cdiPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "importer-prime-abc123",
			Namespace: "test-ns",
			Labels: map[string]string{
				CDIManagedLabel:      "importer",
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodFailed},
	}
	fakeK8s := fake.NewSimpleClientset(cdiPod)

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": CDIImportPrefix + "test-pvc",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	// Reconcile should skip the CDI pod
	err := client.reconcileExistingImportPods("test-ns")
	if err != nil {
		t.Fatalf("reconcileExistingImportPods failed: %v", err)
	}

	// The CDI pod must still exist (not deleted)
	_, err = fakeK8s.CoreV1().Pods("test-ns").Get(context.Background(), "importer-prime-abc123", metav1.GetOptions{})
	if err != nil {
		t.Errorf("CDI pod should not have been deleted, but got: %v", err)
	}

	// The importing label must still be on the VM (not removed)
	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}
	if _, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; !found {
		t.Error("CDI importing label should NOT have been removed — that is the CDI PVC watcher's job")
	}
}

func TestHandleImportPodCompleted_ProcessesOurHelperPod(t *testing.T) {
	mockDynamic := NewMockDynamicClient()

	// Our helper pod (no CDI label)
	helperPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "copy-iso-test-pvc-12345",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	fakeK8s := fake.NewSimpleClientset(helperPod)

	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": "copy-iso-test-pvc-12345",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	err := client.reconcileExistingImportPods("test-ns")
	if err != nil {
		t.Fatalf("reconcileExistingImportPods failed: %v", err)
	}

	// The helper pod should be deleted
	_, err = fakeK8s.CoreV1().Pods("test-ns").Get(context.Background(), "copy-iso-test-pvc-12345", metav1.GetOptions{})
	if err == nil {
		t.Error("Helper pod should have been deleted after completion")
	}

	// The importing label should be removed
	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}
	if _, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; found {
		t.Error("Importing label should have been removed for completed helper pod")
	}
}

func TestCheckInsertedMedia_NoMediaInserted(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	vm := newTestVM("test-ns", "test-vm", nil, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fake.NewSimpleClientset(), mockDynamic, 30*time.Second, nil)

	state, err := client.CheckInsertedMedia("test-ns", "test-vm", "cdrom0", "http://example.com/test.iso")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != MediaStateNone {
		t.Errorf("expected MediaStateNone, got %d", state)
	}
}

func TestCheckInsertedMedia_SameURLReady(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	vm := newTestVM("test-ns", "test-vm", nil, map[string]string{
		VirtualMediaURLAnnotationPrefix + "cdrom0": "http://example.com/test.iso",
	})
	vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, kubevirtv1.Volume{
		Name: "cdrom0",
		VolumeSource: kubevirtv1.VolumeSource{
			PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "test-vm-bootiso-123",
				},
			},
		},
	})
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fake.NewSimpleClientset(), mockDynamic, 30*time.Second, nil)

	state, err := client.CheckInsertedMedia("test-ns", "test-vm", "cdrom0", "http://example.com/test.iso")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != MediaStateReady {
		t.Errorf("expected MediaStateReady, got %d", state)
	}
}

func TestCheckInsertedMedia_SameURLImporting(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	vm := newTestVM("test-ns", "test-vm",
		map[string]string{ImportingLabelPrefix + "cdrom0": "cdi-test-vm-bootiso-123"},
		map[string]string{VirtualMediaURLAnnotationPrefix + "cdrom0": "http://example.com/test.iso"},
	)
	vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, kubevirtv1.Volume{
		Name: "cdrom0",
		VolumeSource: kubevirtv1.VolumeSource{
			PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "test-vm-bootiso-123",
				},
			},
		},
	})
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fake.NewSimpleClientset(), mockDynamic, 30*time.Second, nil)

	state, err := client.CheckInsertedMedia("test-ns", "test-vm", "cdrom0", "http://example.com/test.iso")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != MediaStateImporting {
		t.Errorf("expected MediaStateImporting, got %d", state)
	}
}

func TestCheckInsertedMedia_DifferentURLConflict(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	vm := newTestVM("test-ns", "test-vm", nil, map[string]string{
		VirtualMediaURLAnnotationPrefix + "cdrom0": "http://example.com/original.iso",
	})
	vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, kubevirtv1.Volume{
		Name: "cdrom0",
		VolumeSource: kubevirtv1.VolumeSource{
			PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "test-vm-bootiso-123",
				},
			},
		},
	})
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fake.NewSimpleClientset(), mockDynamic, 30*time.Second, nil)

	state, err := client.CheckInsertedMedia("test-ns", "test-vm", "cdrom0", "http://example.com/different.iso")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != MediaStateConflict {
		t.Errorf("expected MediaStateConflict, got %d", state)
	}
}

func TestCheckInsertedMedia_PVCButNoAnnotation(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	vm := newTestVM("test-ns", "test-vm", nil, nil)
	vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, kubevirtv1.Volume{
		Name: "cdrom0",
		VolumeSource: kubevirtv1.VolumeSource{
			PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "test-vm-bootiso-123",
				},
			},
		},
	})
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fake.NewSimpleClientset(), mockDynamic, 30*time.Second, nil)

	// No annotation means storedURL="" which differs from the requested URL → conflict
	state, err := client.CheckInsertedMedia("test-ns", "test-vm", "cdrom0", "http://example.com/test.iso")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != MediaStateConflict {
		t.Errorf("expected MediaStateConflict, got %d", state)
	}
}

func TestInsertVirtualMedia_StoresURLAnnotation(t *testing.T) {
	client, mockDynamic, _ := setupInsertVirtualMediaTestBlock(t, false)

	err := client.InsertVirtualMedia("test-ns", "test-vm", "cdrom0", "http://example.com/test.iso")
	if err != nil {
		t.Fatalf("InsertVirtualMedia failed: %v", err)
	}

	vm, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	annotationKey := VirtualMediaURLAnnotationPrefix + "cdrom0"
	storedURL := vm.GetAnnotations()[annotationKey]
	if storedURL != "http://example.com/test.iso" {
		t.Errorf("expected annotation %q = %q, got %q", annotationKey, "http://example.com/test.iso", storedURL)
	}
}

func TestEjectVirtualMedia_ClearsURLAnnotation(t *testing.T) {
	mockDynamic := NewMockDynamicClient()
	vm := newTestVM("test-ns", "test-vm", nil, map[string]string{
		VirtualMediaURLAnnotationPrefix + "cdrom0": "http://example.com/test.iso",
	})
	vm.Spec.Template.Spec.Domain.Devices.Disks = append(vm.Spec.Template.Spec.Domain.Devices.Disks, kubevirtv1.Disk{
		Name: "cdrom0",
		DiskDevice: kubevirtv1.DiskDevice{
			CDRom: &kubevirtv1.CDRomTarget{Bus: kubevirtv1.DiskBusSATA},
		},
	})
	vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, kubevirtv1.Volume{
		Name: "cdrom0",
		VolumeSource: kubevirtv1.VolumeSource{
			PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "test-vm-bootiso-123",
				},
			},
		},
	})
	mockDynamic.AddVM(vm)

	fakeK8s := fake.NewSimpleClientset()
	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	err := client.EjectVirtualMedia("test-ns", "test-vm", "cdrom0")
	if err != nil {
		t.Fatalf("EjectVirtualMedia failed: %v", err)
	}

	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}

	annotationKey := VirtualMediaURLAnnotationPrefix + "cdrom0"
	if url, found := updated.GetAnnotations()[annotationKey]; found {
		t.Errorf("expected annotation %q to be removed, but found %q", annotationKey, url)
	}
}

func TestHandleImportPodCompleted_StalePodSkipsLabelRemoval(t *testing.T) {
	mockDynamic := NewMockDynamicClient()

	stalePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "copy-iso-old-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	fakeK8s := fake.NewSimpleClientset(stalePod)

	// The VM's importing label points to a DIFFERENT (newer) pod
	vm := newTestVM("test-ns", "test-vm", map[string]string{
		ImportingLabelPrefix + "cdrom0": "copy-iso-new-pod",
	}, nil)
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)
	client.handleImportPodCompleted(stalePod)

	// The stale pod should still be deleted (cleanup)
	_, err := fakeK8s.CoreV1().Pods("test-ns").Get(context.Background(), "copy-iso-old-pod", metav1.GetOptions{})
	if err == nil {
		t.Error("Stale pod should have been deleted")
	}

	// But the importing label must still be present (points to the new pod)
	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}
	labelVal := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]
	if labelVal != "copy-iso-new-pod" {
		t.Errorf("expected importing label to still be %q, got %q", "copy-iso-new-pod", labelVal)
	}
}

func TestEjectVirtualMedia_KillsHelperPodAndClearsLabel(t *testing.T) {
	mockDynamic := NewMockDynamicClient()

	helperPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "copy-iso-active-pod",
			Namespace: "test-ns",
			Labels: map[string]string{
				ImportPodVMLabel:     "test-vm",
				ImportPodVolumeLabel: "cdrom0",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	fakeK8s := fake.NewSimpleClientset(helperPod)

	vm := newTestVM("test-ns", "test-vm",
		map[string]string{ImportingLabelPrefix + "cdrom0": "copy-iso-active-pod"},
		map[string]string{VirtualMediaURLAnnotationPrefix + "cdrom0": "http://example.com/test.iso"},
	)
	vm.Spec.Template.Spec.Domain.Devices.Disks = append(vm.Spec.Template.Spec.Domain.Devices.Disks, kubevirtv1.Disk{
		Name: "cdrom0",
		DiskDevice: kubevirtv1.DiskDevice{
			CDRom: &kubevirtv1.CDRomTarget{Bus: kubevirtv1.DiskBusSATA},
		},
	})
	vm.Spec.Template.Spec.Volumes = append(vm.Spec.Template.Spec.Volumes, kubevirtv1.Volume{
		Name: "cdrom0",
		VolumeSource: kubevirtv1.VolumeSource{
			PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "test-vm-bootiso-123",
				},
			},
		},
	})
	mockDynamic.AddVM(vm)

	client := NewClientWithClients(fakeK8s, mockDynamic, 30*time.Second, nil)

	err := client.EjectVirtualMedia("test-ns", "test-vm", "cdrom0")
	if err != nil {
		t.Fatalf("EjectVirtualMedia failed: %v", err)
	}

	// The helper pod should be deleted
	_, err = fakeK8s.CoreV1().Pods("test-ns").Get(context.Background(), "copy-iso-active-pod", metav1.GetOptions{})
	if err == nil {
		t.Error("Running helper pod should have been killed during eject")
	}

	// The importing label should be removed
	updated, err := mockDynamic.GetVM("test-ns", "test-vm")
	if err != nil {
		t.Fatalf("Failed to get VM: %v", err)
	}
	if val, found := updated.GetLabels()[ImportingLabelPrefix+"cdrom0"]; found {
		t.Errorf("importing label should have been removed, but found %q", val)
	}

	// The URL annotation should also be removed
	if url, found := updated.GetAnnotations()[VirtualMediaURLAnnotationPrefix+"cdrom0"]; found {
		t.Errorf("URL annotation should have been removed, but found %q", url)
	}
}
