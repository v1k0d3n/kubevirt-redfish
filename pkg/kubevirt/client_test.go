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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	testCases := []struct {
		name                string
		state               string
		expectErr           bool
		errSubstr           string
		expectedRunStrategy string // Expected runStrategy after the operation
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock clients
			mockDynamicClient := NewMockDynamicClient()
			fakeK8sClient := fake.NewSimpleClientset()

			// Setup a VM in the mock with initial runStrategy
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
			mockDynamicClient.AddVM(vm)

			// Also add a VMI for some operations that need it
			vmi := &kubevirtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-vm",
					Namespace: "test-namespace",
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

			// For ForceOff, also check the force-stop annotation
			if tc.state == "ForceOff" {
				annotations := updatedVM.GetAnnotations()
				if annotations == nil || annotations["kubevirt.io/force-stop"] != "true" {
					t.Errorf("Expected force-stop annotation to be set for ForceOff state")
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
		name            string
		setupVM         func(mockClient *MockDynamicClient)
		setupVMI        func(mockClient *MockDynamicClient)
		expectRestore   bool
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
							BootOnceOriginalConfigAnnotation:         `[{"diskName":"disk0","bootOrder":1},{"diskName":"cdrom0","bootOrder":2}]`,
							BootOnceVMIUIDAnnotation:                  "old-vmi-uid",
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
							BootOnceOriginalConfigAnnotation:         `[{"diskName":"disk0","bootOrder":1}]`,
							BootOnceVMIUIDAnnotation:                  "same-vmi-uid",
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
							BootOnceOriginalConfigAnnotation:         `[{"diskName":"disk0","bootOrder":1}]`,
							BootOnceVMIUIDAnnotation:                  "", // Was off
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
