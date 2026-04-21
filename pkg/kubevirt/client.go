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
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// VMSelectorConfig defines how to select VMs for a chassis
type VMSelectorConfig struct {
	Labels map[string]string `json:"labels,omitempty"`
	Names  []string          `json:"names,omitempty"`
}

// Client represents a KubeVirt client for interacting with KubeVirt resources
type Client struct {
	kubernetesClient kubernetes.Interface
	dynamicClient    dynamic.Interface
	config           *rest.Config
	timeout          time.Duration
	appConfig        interface{}  // Using interface{} to avoid import cycle
	httpClient       *http.Client // Pooled HTTP client for external requests

	// Performance metrics
	metrics struct {
		sync.RWMutex
		operationCounts map[string]int64
		operationTimes  map[string]time.Duration
	}

	// Boot-once watcher management
	watcherCtx    context.Context
	watcherCancel context.CancelFunc
	watcherWg     sync.WaitGroup

	// Import pod watcher management
	importWatcherCtx    context.Context
	importWatcherCancel context.CancelFunc
	importWatcherWg     sync.WaitGroup

	// CDI PVC watcher management
	pvcWatcherCtx    context.Context
	pvcWatcherCancel context.CancelFunc
	pvcWatcherWg     sync.WaitGroup
}

// init registers KubeVirt and CDI types with the runtime scheme for conversion
func init() {
	// Register KubeVirt types with the scheme for runtime conversion
	_ = kubevirtv1.AddToScheme(scheme.Scheme)
	// Register CDI types with the scheme for runtime conversion
	_ = cdiv1beta1.AddToScheme(scheme.Scheme)
}

// unstructuredToVM converts an unstructured object to a typed VirtualMachine
func unstructuredToVM(u *unstructured.Unstructured) (*kubevirtv1.VirtualMachine, error) {
	vm := &kubevirtv1.VirtualMachine{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, vm)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to VirtualMachine: %w", err)
	}
	return vm, nil
}

// unstructuredToVMI converts an unstructured object to a typed VirtualMachineInstance
func unstructuredToVMI(u *unstructured.Unstructured) (*kubevirtv1.VirtualMachineInstance, error) {
	vmi := &kubevirtv1.VirtualMachineInstance{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, vmi)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to VirtualMachineInstance: %w", err)
	}
	return vmi, nil
}

// vmToUnstructured converts a typed VirtualMachine to an unstructured object
func vmToUnstructured(vm *kubevirtv1.VirtualMachine) (*unstructured.Unstructured, error) {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vm)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VirtualMachine to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: obj}
	// Set the GVK as it's not preserved by the converter
	u.SetAPIVersion("kubevirt.io/v1")
	u.SetKind("VirtualMachine")
	return u, nil
}

// computeVMPatch compares original and modified VMs and returns a JSON Merge Patch.
// This preserves unknown fields that may exist in the cluster object but are not
// represented in the typed VirtualMachine struct.
func computeVMPatch(original, modified *kubevirtv1.VirtualMachine) ([]byte, error) {
	originalJSON, err := json.Marshal(original)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal original VM: %w", err)
	}
	modifiedJSON, err := json.Marshal(modified)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal modified VM: %w", err)
	}

	patch, err := jsonpatch.CreateMergePatch(originalJSON, modifiedJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to create merge patch: %w", err)
	}
	return patch, nil
}

// vmiToUnstructured converts a typed VirtualMachineInstance to an unstructured object
func vmiToUnstructured(vmi *kubevirtv1.VirtualMachineInstance) (*unstructured.Unstructured, error) {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vmi)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VirtualMachineInstance to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: obj}
	// Set the GVK as it's not preserved by the converter
	u.SetAPIVersion("kubevirt.io/v1")
	u.SetKind("VirtualMachineInstance")
	return u, nil
}

// volumeImportSourceToUnstructured converts a typed VolumeImportSource to an unstructured object
func volumeImportSourceToUnstructured(vis *cdiv1beta1.VolumeImportSource) (*unstructured.Unstructured, error) {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vis)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VolumeImportSource to unstructured: %w", err)
	}
	u := &unstructured.Unstructured{Object: obj}
	// Set the GVK as it's not preserved by the converter
	u.SetAPIVersion("cdi.kubevirt.io/v1beta1")
	u.SetKind("VolumeImportSource")
	return u, nil
}

// NewClient creates a new KubeVirt client with connection pooling
func NewClient(configPath string, timeout time.Duration, appConfig interface{}) (*Client, error) {
	var config *rest.Config
	var err error

	if configPath != "" {
		// Use kubeconfig file
		config, err = clientcmd.BuildConfigFromFlags("", configPath)
	} else {
		// Use in-cluster config
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create kubeconfig: %w", err)
	}

	// Initialize random seed for unique PVC name generation
	rand.Seed(time.Now().UnixNano())

	// Create Kubernetes client
	kubernetesClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create dynamic client for KubeVirt resources
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Create optimized HTTP client with connection pooling
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,              // Maximum number of idle connections
			MaxIdleConnsPerHost: 10,               // Maximum idle connections per host
			IdleConnTimeout:     90 * time.Second, // How long to keep idle connections
			TLSHandshakeTimeout: 10 * time.Second, // TLS handshake timeout
			DisableCompression:  false,            // Enable compression for better performance
			ForceAttemptHTTP2:   true,             // Enable HTTP/2 for better performance
		},
	}

	return &Client{
		kubernetesClient: kubernetesClient,
		dynamicClient:    dynamicClient,
		config:           config,
		timeout:          timeout,
		appConfig:        appConfig,
		httpClient:       httpClient,
	}, nil
}

// NewClientWithClients creates a Client with provided Kubernetes clients.
// This is useful for testing with mock/fake clients.
func NewClientWithClients(
	kubernetesClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	timeout time.Duration,
	appConfig interface{},
) *Client {
	return &Client{
		kubernetesClient: kubernetesClient,
		dynamicClient:    dynamicClient,
		timeout:          timeout,
		appConfig:        appConfig,
		config:           &rest.Config{},
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// trackOperation tracks the performance of an operation
func (c *Client) trackOperation(operation string, duration time.Duration) {
	c.metrics.Lock()
	defer c.metrics.Unlock()

	if c.metrics.operationCounts == nil {
		c.metrics.operationCounts = make(map[string]int64)
		c.metrics.operationTimes = make(map[string]time.Duration)
	}

	c.metrics.operationCounts[operation]++
	c.metrics.operationTimes[operation] += duration
}

// GetPerformanceMetrics returns current performance metrics
func (c *Client) GetPerformanceMetrics() map[string]interface{} {
	c.metrics.RLock()
	defer c.metrics.RUnlock()

	metrics := make(map[string]interface{})

	if c.metrics.operationCounts != nil {
		for operation, count := range c.metrics.operationCounts {
			avgTime := time.Duration(0)
			if count > 0 {
				avgTime = c.metrics.operationTimes[operation] / time.Duration(count)
			}

			metrics[operation] = map[string]interface{}{
				"count":     count,
				"totalTime": c.metrics.operationTimes[operation].String(),
				"avgTime":   avgTime.String(),
			}
		}
	}

	return metrics
}

// Close properly cleans up the client resources
func (c *Client) Close() error {
	c.stopBootOnceWatcher()
	c.stopImportPodWatcher()
	c.stopCDIPVCWatcher()

	if c.httpClient != nil && c.httpClient.Transport != nil {
		if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
			logger.Debug("Closed idle connections in HTTP client")
		}
	}
	return nil
}

// RestartWatchers stops all running watchers and starts new ones for the
// given namespaces. This is intended to be called on config reload so that
// namespace additions/removals are picked up.
func (c *Client) RestartWatchers(ctx context.Context, namespaces []string) {
	logger.Info("Restarting all watchers for namespaces: %v", namespaces)
	c.StartBootOnceWatcher(ctx, namespaces)
	c.StartImportPodWatcher(ctx, namespaces)
	c.StartCDIPVCWatcher(ctx, namespaces)
}

// retryWithBackoff executes a function with exponential backoff retry logic
func (c *Client) retryWithBackoff(operation string, fn func() error) error {
	maxRetries := 3
	baseDelay := 100 * time.Millisecond
	maxDelay := 5 * time.Second

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := fn()
		if err == nil {
			// Success, no need to retry
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			// Non-retryable error, return immediately
			return err
		}

		// If this is the last attempt, return the error
		if attempt == maxRetries {
			break
		}

		// Calculate delay with exponential backoff
		delay := time.Duration(float64(baseDelay) * float64(attempt) * 1.5)
		if delay > maxDelay {
			delay = maxDelay
		}

		logger.Debug("Retrying %s (attempt %d/%d) after %v: %v", operation, attempt+1, maxRetries, delay, err)

		// Wait before retrying
		time.Sleep(delay)
	}

	logger.Error("All retry attempts failed for %s: %v", operation, lastErr)
	return lastErr
}

// isRetryableError checks if an error is retryable
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for common retryable error patterns
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "server overloaded") ||
		strings.Contains(errStr, "rate limit exceeded") ||
		strings.Contains(errStr, "already exists") // Race condition - retry to handle gracefully
}

// ListVMs lists all VirtualMachines in a namespace
func (c *Client) ListVMs(namespace string) ([]string, error) {
	start := time.Now()
	defer func() {
		c.trackOperation("ListVMs", time.Since(start))
	}()

	var vmNames []string

	err := c.retryWithBackoff(fmt.Sprintf("ListVMs %s", namespace), func() error {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()

		logger.Info("Listing VMs in namespace: %s", namespace)

		// Use dynamic client to list VirtualMachines
		gvr := schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}

		vms, listErr := c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if listErr != nil {
			return listErr
		}

		vmNames = nil // Reset for retry
		for _, vm := range vms.Items {
			vmNames = append(vmNames, vm.GetName())
		}

		return nil
	})

	if err != nil {
		logger.Error("Failed to list VMs in namespace %s: %v", namespace, err)
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	logger.Info("Found %d VMs in namespace %s: %v", len(vmNames), namespace, vmNames)
	return vmNames, nil
}

// ListVMsWithSelector lists VirtualMachines in a namespace with optional label selector and name filtering
func (c *Client) ListVMsWithSelector(namespace string, vmSelector *VMSelectorConfig) ([]string, error) {
	start := time.Now()
	defer func() {
		c.trackOperation("ListVMsWithSelector", time.Since(start))
	}()

	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	logger.Info("Listing VMs in namespace %s with selector", namespace)

	// Use dynamic client to list VirtualMachines
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	// Build list options with label selector if provided
	listOptions := metav1.ListOptions{}
	if vmSelector != nil && len(vmSelector.Labels) > 0 {
		selector := labels.NewSelector()
		for key, value := range vmSelector.Labels {
			req, err := labels.NewRequirement(key, selection.Equals, []string{value})
			if err != nil {
				logger.Error("Invalid label selector %s=%s: %v", key, value, err)
				return nil, fmt.Errorf("invalid label selector: %w", err)
			}
			selector = selector.Add(*req)
		}
		listOptions.LabelSelector = selector.String()
		logger.Debug("Using label selector: %s", selector.String())
	}

	vms, err := c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, listOptions)
	if err != nil {
		logger.Error("Failed to list VMs in namespace %s: %v", namespace, err)
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var vmNames []string
	for _, vm := range vms.Items {
		vmName := vm.GetName()

		// If explicit names are specified, only include those VMs
		if vmSelector != nil && len(vmSelector.Names) > 0 {
			for _, allowedName := range vmSelector.Names {
				if vmName == allowedName {
					vmNames = append(vmNames, vmName)
					break
				}
			}
		} else {
			// Include all VMs if no explicit names specified
			vmNames = append(vmNames, vmName)
		}
	}

	logger.Info("Found %d VMs in namespace %s with selector: %v", len(vmNames), namespace, vmNames)
	return vmNames, nil
}

// GetVM gets details of a specific VirtualMachine
func (c *Client) GetVM(namespace, name string) (*kubevirtv1.VirtualMachine, error) {
	start := time.Now()
	defer func() {
		c.trackOperation("GetVM", time.Since(start))
	}()

	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client is not initialized")
	}

	var vmUnstructured *unstructured.Unstructured

	err := c.retryWithBackoff(fmt.Sprintf("GetVM %s/%s", namespace, name), func() error {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()

		gvr := schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}

		var getErr error
		vmUnstructured, getErr = c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		return getErr
	})

	if err != nil {
		logger.Error("Failed to get VM %s/%s: %v", namespace, name, err)
		return nil, fmt.Errorf("failed to get VM: %w", err)
	}

	// Convert unstructured to typed VirtualMachine
	vm, err := unstructuredToVM(vmUnstructured)
	if err != nil {
		logger.Error("Failed to convert VM %s/%s to typed object: %v", namespace, name, err)
		return nil, fmt.Errorf("failed to convert VM: %w", err)
	}

	return vm, nil
}

// VMMatchesSelector checks whether a VM matches the given selector criteria.
// A nil selector matches all VMs.
func VMMatchesSelector(vm *kubevirtv1.VirtualMachine, selector *VMSelectorConfig) bool {
	if selector == nil {
		return true
	}

	if len(selector.Names) > 0 {
		nameMatched := false
		for _, allowed := range selector.Names {
			if vm.Name == allowed {
				nameMatched = true
				break
			}
		}
		if !nameMatched {
			return false
		}
	}

	if len(selector.Labels) > 0 {
		vmLabels := vm.GetLabels()
		for key, value := range selector.Labels {
			if vmLabels[key] != value {
				return false
			}
		}
	}

	return true
}

// GetVMI gets details of a specific VirtualMachineInstance
func (c *Client) GetVMI(namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}

	vmiUnstructured, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get VMI %s: %w", name, err)
	}

	// Convert unstructured to typed VirtualMachineInstance
	vmi, err := unstructuredToVMI(vmiUnstructured)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VMI: %w", err)
	}

	return vmi, nil
}

// patchVMIGracePeriod sets terminationGracePeriodSeconds to 0 on the running VMI
// so that a subsequent force stop takes effect immediately. If no VMI is running,
// this is a no-op.
func (c *Client) patchVMIGracePeriod(ctx context.Context, namespace, name, correlationID string) {
	vmiGVR := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}

	gracePatch := []byte(`[{"op": "replace", "path": "/spec/terminationGracePeriodSeconds", "value": 0}]`)

	_, err := c.dynamicClient.Resource(vmiGVR).Namespace(namespace).Patch(
		ctx, name, types.JSONPatchType, gracePatch, metav1.PatchOptions{})
	if err != nil {
		logger.DebugStructured("Could not patch VMI terminationGracePeriodSeconds (VMI may not be running)", map[string]interface{}{
			"correlation_id": correlationID,
			"namespace":      namespace,
			"resource":       name,
			"error":          err.Error(),
		})
	} else {
		logger.Info("Set terminationGracePeriodSeconds=0 on VMI %s/%s", namespace, name)
	}
}

// GetVMPowerState gets the power state of a VirtualMachine
func (c *Client) GetVMPowerState(namespace, name string) (string, error) {
	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return "Unknown", fmt.Errorf("dynamic client is not initialized")
	}

	// Fetch the VM resource using typed API
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return "Unknown", fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	// Check for force-stop annotation
	annotations := vm.GetAnnotations()
	forceStop := annotations != nil && annotations["kubevirt.io/force-stop"] == "true"

	// Use printableStatus if available
	printableStatus := vm.Status.PrintableStatus
	if printableStatus != "" {
		switch printableStatus {
		case kubevirtv1.VirtualMachinePrintableStatus("Running"):
			return "On", nil
		case kubevirtv1.VirtualMachinePrintableStatus("Stopped"):
			return "Off", nil
		case kubevirtv1.VirtualMachinePrintableStatus("Stopping"), kubevirtv1.VirtualMachinePrintableStatus("Terminating"):
			if forceStop {
				return "ForceOffInProgress", nil
			}
			return "ShuttingDown", nil
		case kubevirtv1.VirtualMachinePrintableStatus("Starting"):
			return "PoweringOn", nil
		}
	}

	// Check for PodTerminating condition
	for _, cond := range vm.Status.Conditions {
		if cond.Type == kubevirtv1.VirtualMachineConditionType("PodTerminating") {
			return "ShuttingDown", nil
		}
	}

	// Check for pending state change requests
	if len(vm.Status.StateChangeRequests) > 0 {
		return "Transitioning", nil
	}

	// Fallback to VMI phase logic
	vmi, err := c.GetVMI(namespace, name)
	if err != nil {
		// If VMI doesn't exist, VM is stopped
		return "Off", nil
	}

	// Check for Paused condition in VMI status
	for _, cond := range vmi.Status.Conditions {
		if cond.Type == kubevirtv1.VirtualMachineInstancePaused && cond.Status == corev1.ConditionTrue {
			return "Paused", nil
		}
	}

	// Check VMI phase
	switch vmi.Status.Phase {
	case kubevirtv1.Running:
		return "On", nil
	case kubevirtv1.Succeeded:
		return "On", nil
	case kubevirtv1.Failed:
		return "Off", nil
	case kubevirtv1.Pending, kubevirtv1.Scheduling, kubevirtv1.Scheduled:
		return "PoweringOn", nil
	}

	return "Off", nil
}

// SetVMPowerState sets the power state of a VirtualMachine using proper KubeVirt REST API calls
func (c *Client) SetVMPowerState(namespace, name, state string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Check if dynamic client is available
	if c.dynamicClient == nil {
		return fmt.Errorf("dynamic client is not initialized")
	}

	// Debug: Log power state change attempt
	logger.DebugStructured("Attempting power state change", map[string]interface{}{
		"operation":    "set_vm_power_state",
		"namespace":    namespace,
		"resource":     name,
		"target_state": state,
		"timeout":      c.timeout.String(),
	})

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	// Get correlation ID from context if available
	correlationID := logger.GetCorrelationID(context.Background())

	// Get VMI to check if it's running
	switch state {
	case "On":
		// Debug: Log start operation
		logger.DebugStructured("Executing start operation", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "start_vm",
			"namespace":      namespace,
			"resource":       name,
			"method":         "patch_runstrategy",
			"target_state":   "Always",
		})

		// Start the VM by setting runStrategy to Always and clearing the legacy running field
		patch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Always"},{"op": "add", "path": "/spec/running", "value": null}]`)

		// Debug: Log before making the API call
		logger.DebugStructured("Making start API call with dynamic client", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "start_vm",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(patch),
		})

		_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Start request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "start_vm",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to start VM %s: %w", name, err)
		}

		logger.Info("Successfully started VM %s/%s", namespace, name)

	case "ForceOff":
		// Debug: Log force stop operation
		logger.DebugStructured("Executing force stop operation", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "force_stop_vm",
			"namespace":      namespace,
			"resource":       name,
			"method":         "patch_vmi_grace_period_then_vm_runstrategy",
			"target_state":   "Halted",
			"force_stop":     true,
			"grace_period":   0,
		})

		// Set terminationGracePeriodSeconds=0 on the running VMI so the kill is immediate
		c.patchVMIGracePeriod(ctx, namespace, name, correlationID)

		// Force stop the VM using runStrategy and force-stop annotation
		// This mirrors the behavior of: virtctl stop --grace-period 0 --force <vm name>
		patch := []byte(`[
			 {"op": "replace", "path": "/spec/runStrategy", "value": "Halted"},
			 {"op": "add", "path": "/spec/running", "value": null},
			 {"op": "add", "path": "/metadata/annotations/kubevirt.io~1force-stop", "value": "true"}
		 ]`)

		// Debug: Log before making the API call
		logger.DebugStructured("Making force stop API call with dynamic client", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "force_stop_vm",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(patch),
		})

		_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Force stop request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "force_stop_vm",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to force stop VM %s: %w", name, err)
		}

		logger.Info("Successfully force stopped VM %s/%s", namespace, name)

	case "GracefulShutdown":
		// Debug: Log graceful stop operation
		logger.DebugStructured("Executing graceful stop operation", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "graceful_stop_vm",
			"namespace":      namespace,
			"resource":       name,
			"method":         "patch_runstrategy",
			"target_state":   "Halted",
		})

		// Graceful stop the VM using runStrategy and clearing the legacy running field
		patch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Halted"},{"op": "add", "path": "/spec/running", "value": null}]`)

		// Debug: Log before making the API call
		logger.DebugStructured("Making graceful stop API call with dynamic client", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "graceful_stop_vm",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(patch),
		})

		_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Graceful stop request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "graceful_stop_vm",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to gracefully stop VM %s: %w", name, err)
		}

		logger.Info("Successfully gracefully stopped VM %s/%s", namespace, name)

	case "ForceRestart":
		// Debug: Log force restart operation
		logger.DebugStructured("Executing force restart operation", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "force_restart_vm",
			"namespace":      namespace,
			"resource":       name,
			"method":         "patch_vmi_grace_period_then_force_stop_start",
		})

		// Set terminationGracePeriodSeconds=0 on the running VMI so the kill is immediate
		c.patchVMIGracePeriod(ctx, namespace, name, correlationID)

		// Force restart the VM by force stopping and starting
		stopPatch := []byte(`[
		     {"op": "replace", "path": "/spec/runStrategy", "value": "Halted"},
			 {"op": "add", "path": "/spec/running", "value": null},
			 {"op": "add", "path": "/metadata/annotations/kubevirt.io~1force-stop", "value": "true"}
		 ]`)

		// Debug: Log before making the stop API call
		logger.DebugStructured("Making force restart stop API call", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "force_restart_vm_stop",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(stopPatch),
		})

		_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, stopPatch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Force restart stop request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "force_restart_vm_stop",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to force stop VM %s for restart: %w", name, err)
		}

		// Wait a moment then start
		logger.DebugStructured("Waiting before force restart start", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "force_restart_vm_wait",
			"namespace":      namespace,
			"resource":       name,
			"wait_time":      "2s",
		})
		time.Sleep(2 * time.Second)

		startPatch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Always"},{"op": "add", "path": "/spec/running", "value": null}]`)

		// Debug: Log before making the start API call
		logger.DebugStructured("Making force restart start API call", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "force_restart_vm_start",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(startPatch),
		})

		_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, startPatch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Force restart start request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "force_restart_vm_start",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to restart VM %s: %w", name, err)
		}

		logger.Info("Successfully force restarted VM %s/%s", namespace, name)

	case "GracefulRestart":
		// Debug: Log graceful restart operation
		logger.DebugStructured("Executing graceful restart operation", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "graceful_restart_vm",
			"namespace":      namespace,
			"resource":       name,
			"method":         "graceful_stop_then_start",
		})

		// Graceful restart the VM by stopping and starting, clearing the legacy running field
		stopPatch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Halted"},{"op": "add", "path": "/spec/running", "value": null}]`)

		// Debug: Log before making the stop API call
		logger.DebugStructured("Making graceful restart stop API call", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "graceful_restart_vm_stop",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(stopPatch),
		})

		_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, stopPatch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Graceful restart stop request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "graceful_restart_vm_stop",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to stop VM %s for restart: %w", name, err)
		}

		// Wait a moment then start
		logger.DebugStructured("Waiting before graceful restart start", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "graceful_restart_vm_wait",
			"namespace":      namespace,
			"resource":       name,
			"wait_time":      "2s",
		})
		time.Sleep(2 * time.Second)

		startPatch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Always"},{"op": "add", "path": "/spec/running", "value": null}]`)

		// Debug: Log before making the start API call
		logger.DebugStructured("Making graceful restart start API call", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "graceful_restart_vm_start",
			"namespace":      namespace,
			"resource":       name,
			"gvr":            fmt.Sprintf("%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource),
			"patch_type":     "JSONPatch",
			"patch_data":     string(startPatch),
		})

		_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(
			ctx, name, types.JSONPatchType, startPatch, metav1.PatchOptions{})
		if err != nil {
			logger.DebugStructured("Graceful restart start request failed", map[string]interface{}{
				"correlation_id": correlationID,
				"operation":      "graceful_restart_vm_start",
				"namespace":      namespace,
				"resource":       name,
				"error":          err.Error(),
				"error_type":     fmt.Sprintf("%T", err),
			})
			return fmt.Errorf("failed to restart VM %s: %w", name, err)
		}

		logger.Info("Successfully gracefully restarted VM %s/%s", namespace, name)

	case "Pause":
		// Debug: Log pause operation
		logger.DebugStructured("Executing pause operation", map[string]interface{}{
			"operation": "pause_vmi",
			"namespace": namespace,
			"resource":  name,
			"method":    "pauseVMI",
		})
		// Use proper REST API call instead of curl workaround
		return c.pauseVMI(namespace, name)
	case "Resume":
		// Debug: Log resume operation
		logger.DebugStructured("Executing resume operation", map[string]interface{}{
			"operation": "resume_vmi",
			"namespace": namespace,
			"resource":  name,
			"method":    "unpauseVMI",
		})
		// Use proper REST API call instead of curl workaround
		return c.unpauseVMI(namespace, name)
	default:
		return fmt.Errorf("unsupported power state: %s", state)
	}

	return nil
}

// pauseVMI pauses a VirtualMachineInstance using the KubeVirt subresource API
func (c *Client) pauseVMI(namespace, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Get correlation ID from context if available
	correlationID := logger.GetCorrelationID(context.Background())

	fields := map[string]interface{}{
		"correlation_id": correlationID,
		"operation":      "pause_vmi",
		"namespace":      namespace,
		"resource":       name,
		"api":            "subresources.kubevirt.io/v1",
		"method":         "PUT",
	}

	logger.DebugStructured("Pausing VMI using KubeVirt subresource API", fields)

	// Use retry logic for the pause operation
	return errors.Retry(ctx, nil, func() error {
		return c.executePauseRequestWithDynamicClient(ctx, namespace, name, correlationID)
	})
}

// executePauseRequestWithDynamicClient executes the pause request using the dynamic client
func (c *Client) executePauseRequestWithDynamicClient(ctx context.Context, namespace, name, correlationID string) error {
	// Debug: Log authentication attempt
	logger.DebugStructured("Attempting pause operation with REST client", map[string]interface{}{
		"correlation_id": correlationID,
		"operation":      "pause_vmi",
		"namespace":      namespace,
		"resource":       name,
		"client_type":    "rest",
		"api_group":      "subresources.kubevirt.io",
		"api_version":    "v1",
		"resource_type":  "virtualmachineinstances/pause",
		"method":         "PUT",
	})

	// Use REST client for subresource operations with proper authentication
	config := *c.config
	config.GroupVersion = &schema.GroupVersion{
		Group:   "subresources.kubevirt.io",
		Version: "v1",
	}
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	restClient, err := rest.RESTClientFor(&config)
	if err != nil {
		logger.DebugStructured("Failed to create REST client", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "pause_vmi",
			"namespace":      namespace,
			"resource":       name,
			"error":          err.Error(),
		})
		return errors.NewKubeVirtError("pause_vmi", namespace, name,
			fmt.Sprintf("Failed to create REST client: %v", err), err).
			WithCorrelationID(correlationID)
	}

	// Build the subresource URL
	subresourceURL := fmt.Sprintf("/apis/subresources.kubevirt.io/v1/namespaces/%s/virtualmachineinstances/%s/pause", namespace, name)

	// Debug: Log before making the API call
	logger.DebugStructured("Making pause API call with REST client", map[string]interface{}{
		"correlation_id": correlationID,
		"operation":      "pause_vmi",
		"namespace":      namespace,
		"resource":       name,
		"url":            subresourceURL,
		"method":         "PUT",
	})

	// Make the REST call with proper authentication
	result := restClient.Put().
		AbsPath(subresourceURL).
		Do(ctx)

	if result.Error() != nil {
		err := result.Error()

		// Enhanced debug logging for authentication/authorization errors
		logger.DebugStructured("Pause request failed", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "pause_vmi",
			"namespace":      namespace,
			"resource":       name,
			"error":          err.Error(),
			"error_type":     fmt.Sprintf("%T", err),
		})

		// Check if it's a not found error
		if strings.Contains(err.Error(), "not found") {
			return errors.NewNotFoundError(fmt.Sprintf("VMI %s/%s", namespace, name),
				fmt.Sprintf("VMI not found: %v", err)).
				WithCorrelationID(correlationID).
				WithContext("pause_vmi", namespace, name)
		}

		// Check if it's a conflict (VMI not in running state)
		if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "409") {
			return errors.NewConflictError(fmt.Sprintf("VMI %s/%s", namespace, name),
				fmt.Sprintf("VMI is not in a state that can be paused: %v", err)).
				WithCorrelationID(correlationID).
				WithContext("pause_vmi", namespace, name)
		}

		// Check if it's an authentication error
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return errors.NewAuthenticationError("Unauthorized to pause VMI").
				WithCorrelationID(correlationID).
				WithContext("pause_vmi", namespace, name)
		}

		// Check if it's an authorization error
		if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "403") {
			return errors.NewAuthorizationError("Forbidden to pause VMI").
				WithCorrelationID(correlationID).
				WithContext("pause_vmi", namespace, name)
		}

		// Generic error
		return errors.NewKubeVirtError("pause_vmi", namespace, name,
			fmt.Sprintf("Failed to pause VMI: %v", err), err).
			WithCorrelationID(correlationID)
	}

	logger.Info("Successfully paused VMI %s/%s", namespace, name)
	return nil
}

// unpauseVMI unpauses a VirtualMachineInstance using the KubeVirt subresource API
func (c *Client) unpauseVMI(namespace, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// Get correlation ID from context if available
	correlationID := logger.GetCorrelationID(context.Background())

	fields := map[string]interface{}{
		"correlation_id": correlationID,
		"operation":      "unpause_vmi",
		"namespace":      namespace,
		"resource":       name,
		"api":            "subresources.kubevirt.io/v1",
		"method":         "PUT",
	}

	logger.DebugStructured("Unpausing VMI using KubeVirt subresource API", fields)

	// Use retry logic for the unpause operation
	return errors.Retry(ctx, nil, func() error {
		return c.executeUnpauseRequestWithDynamicClient(ctx, namespace, name, correlationID)
	})
}

// executeUnpauseRequestWithDynamicClient executes the unpause request using the dynamic client
func (c *Client) executeUnpauseRequestWithDynamicClient(ctx context.Context, namespace, name, correlationID string) error {
	// Debug: Log authentication attempt
	logger.DebugStructured("Attempting unpause operation with REST client", map[string]interface{}{
		"correlation_id": correlationID,
		"operation":      "unpause_vmi",
		"namespace":      namespace,
		"resource":       name,
		"client_type":    "rest",
		"api_group":      "subresources.kubevirt.io",
		"api_version":    "v1",
		"resource_type":  "virtualmachineinstances/unpause",
		"method":         "PUT",
	})

	// Use REST client for subresource operations with proper authentication
	config := *c.config
	config.GroupVersion = &schema.GroupVersion{
		Group:   "subresources.kubevirt.io",
		Version: "v1",
	}
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	restClient, err := rest.RESTClientFor(&config)
	if err != nil {
		logger.DebugStructured("Failed to create REST client", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "unpause_vmi",
			"namespace":      namespace,
			"resource":       name,
			"error":          err.Error(),
		})
		return errors.NewKubeVirtError("unpause_vmi", namespace, name,
			fmt.Sprintf("Failed to create REST client: %v", err), err).
			WithCorrelationID(correlationID)
	}

	// Build the subresource URL
	subresourceURL := fmt.Sprintf("/apis/subresources.kubevirt.io/v1/namespaces/%s/virtualmachineinstances/%s/unpause", namespace, name)

	// Debug: Log before making the API call
	logger.DebugStructured("Making unpause API call with REST client", map[string]interface{}{
		"correlation_id": correlationID,
		"operation":      "unpause_vmi",
		"namespace":      namespace,
		"resource":       name,
		"url":            subresourceURL,
		"method":         "PUT",
	})

	// Make the REST call with proper authentication
	result := restClient.Put().
		AbsPath(subresourceURL).
		Do(ctx)

	if result.Error() != nil {
		err := result.Error()

		// Enhanced debug logging for authentication/authorization errors
		logger.DebugStructured("Unpause request failed", map[string]interface{}{
			"correlation_id": correlationID,
			"operation":      "unpause_vmi",
			"namespace":      namespace,
			"resource":       name,
			"error":          err.Error(),
			"error_type":     fmt.Sprintf("%T", err),
		})

		// Check if it's a not found error
		if strings.Contains(err.Error(), "not found") {
			return errors.NewNotFoundError(fmt.Sprintf("VMI %s/%s", namespace, name),
				fmt.Sprintf("VMI not found: %v", err)).
				WithCorrelationID(correlationID).
				WithContext("unpause_vmi", namespace, name)
		}

		// Check if it's a conflict (VMI not in paused state)
		if strings.Contains(err.Error(), "conflict") || strings.Contains(err.Error(), "409") {
			return errors.NewConflictError(fmt.Sprintf("VMI %s/%s", namespace, name),
				fmt.Sprintf("VMI is not in a state that can be unpaused: %v", err)).
				WithCorrelationID(correlationID).
				WithContext("unpause_vmi", namespace, name)
		}

		// Check if it's an authentication error
		if strings.Contains(err.Error(), "unauthorized") || strings.Contains(err.Error(), "401") {
			return errors.NewAuthenticationError("Unauthorized to unpause VMI").
				WithCorrelationID(correlationID).
				WithContext("unpause_vmi", namespace, name)
		}

		// Check if it's an authorization error
		if strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "403") {
			return errors.NewAuthorizationError("Forbidden to unpause VMI").
				WithCorrelationID(correlationID).
				WithContext("unpause_vmi", namespace, name)
		}

		// Generic error
		return errors.NewKubeVirtError("unpause_vmi", namespace, name,
			fmt.Sprintf("Failed to unpause VMI: %v", err), err).
			WithCorrelationID(correlationID)
	}

	logger.Info("Successfully unpaused VMI %s/%s", namespace, name)
	return nil
}

// GetVMNetworkInterfaces gets network interfaces of a VirtualMachine
func (c *Client) GetVMNetworkInterfaces(namespace, name string) ([]string, error) {
	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client is not initialized")
	}

	vmi, err := c.GetVMI(namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get VMI %s: %w", name, err)
	}

	var interfaces []string
	for _, iface := range vmi.Status.Interfaces {
		if iface.Name != "" {
			interfaces = append(interfaces, iface.Name)
		}
	}

	return interfaces, nil
}

// GetVMStorage gets storage information of a VirtualMachine
func (c *Client) GetVMStorage(namespace, name string) ([]string, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return nil, err
	}

	var storage []string
	if vm.Spec.Template == nil {
		return storage, nil
	}

	for _, volume := range vm.Spec.Template.Spec.Volumes {
		if volume.Name != "" {
			storage = append(storage, volume.Name)
		}
	}

	return storage, nil
}

// GetVMBootOptions gets boot options of a VirtualMachine
func (c *Client) GetVMBootOptions(namespace, name string) (map[string]interface{}, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return nil, err
	}

	bootOptions := map[string]interface{}{
		"bootSourceOverrideEnabled": "Disabled",
		"bootSourceOverrideTarget":  "None",
		"bootSourceOverrideMode":    "Legacy",
	}

	// Check for firmware configuration
	if vm.Spec.Template != nil && vm.Spec.Template.Spec.Domain.Firmware != nil {
		if vm.Spec.Template.Spec.Domain.Firmware.Bootloader != nil &&
			vm.Spec.Template.Spec.Domain.Firmware.Bootloader.EFI != nil {
			bootOptions["bootSourceOverrideMode"] = "UEFI"
		}
	}

	// Check for boot configuration in VM annotations
	annotations := vm.GetAnnotations()
	if annotations != nil {
		if enabled, found := annotations["redfish.boot.source.override.enabled"]; found {
			bootOptions["bootSourceOverrideEnabled"] = enabled
		}
		if target, found := annotations["redfish.boot.source.override.target"]; found {
			bootOptions["bootSourceOverrideTarget"] = target
		}
		if mode, found := annotations["redfish.boot.source.override.mode"]; found {
			bootOptions["bootSourceOverrideMode"] = mode
		}
	}

	return bootOptions, nil
}

// RecordVMBootOptionsAsAnnotations stores the boot options of a VirtualMachine
// This is needed to support Once and Next boot logic in the future.
func (c *Client) RecordVMBootOptionsAsAnnotations(namespace, name string, options map[string]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	logger.Info("Setting boot options for VM %s/%s: %v", namespace, name, options)

	// Get the current VM
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	// Update VM with boot options
	vmCopy := vm.DeepCopy()

	// Store boot options in VM annotations for persistence
	annotations := vmCopy.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Update annotations with boot options
	if bootSourceOverrideEnabled, found := options["bootSourceOverrideEnabled"]; found {
		if enabled, ok := bootSourceOverrideEnabled.(string); ok {
			annotations["redfish.boot.source.override.enabled"] = enabled
		}
	}
	if bootSourceOverrideTarget, found := options["bootSourceOverrideTarget"]; found {
		if target, ok := bootSourceOverrideTarget.(string); ok {
			annotations["redfish.boot.source.override.target"] = target
		}
	}
	if bootSourceOverrideMode, found := options["bootSourceOverrideMode"]; found {
		if mode, ok := bootSourceOverrideMode.(string); ok {
			annotations["redfish.boot.source.override.mode"] = mode
		}
	}

	vmCopy.SetAnnotations(annotations)

	// Compute merge patch from changes (preserves unknown fields)
	patch, err := computeVMPatch(vm, vmCopy)
	if err != nil {
		return fmt.Errorf("failed to compute VM patch: %w", err)
	}

	// Apply patch
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to update VM boot options: %w", err)
	}

	logger.Info("Successfully updated boot options for VM %s/%s", namespace, name)
	return nil
}

// GetVMVirtualMedia gets virtual media information of a VirtualMachine
func (c *Client) GetVMVirtualMedia(namespace, name string) ([]string, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	var mediaDevices []string

	// Check for CD-ROM devices in the VM spec
	if vm.Spec.Template == nil {
		return mediaDevices, nil
	}

	for _, disk := range vm.Spec.Template.Spec.Domain.Devices.Disks {
		// Check if this is a CD-ROM device
		if disk.CDRom != nil {
			logger.Debug("Found CD-ROM device %s for VM %s/%s", disk.Name, namespace, name)
			mediaDevices = append(mediaDevices, disk.Name)
		}
	}

	logger.Info("Found %d virtual media devices for VM %s/%s: %v", len(mediaDevices), namespace, name, mediaDevices)
	return mediaDevices, nil
}

// IsVirtualMediaInserted checks if a specific virtual media device has media inserted and ready
func (c *Client) IsVirtualMediaInserted(namespace, name, mediaID string) (bool, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return false, fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	if vm.Spec.Template == nil {
		logger.Debug("No template found in VM %s/%s", namespace, name)
		return false, nil
	}

	// Step 1: Find the CD-ROM device in the VM spec
	var volumeRef string
	for _, disk := range vm.Spec.Template.Spec.Domain.Devices.Disks {
		if disk.Name == mediaID && disk.CDRom != nil {
			// The disk name is the volume reference
			volumeRef = disk.Name
			break
		}
	}

	if volumeRef == "" {
		logger.Debug("CD-ROM device %s not found in VM %s/%s", mediaID, namespace, name)
		return false, nil
	}

	// Step 2: Find the corresponding volume for the CD-ROM
	var pvcName string
	for _, volume := range vm.Spec.Template.Spec.Volumes {
		if volume.Name == volumeRef {
			// Get PVC name from persistentVolumeClaim.claimName
			if volume.PersistentVolumeClaim != nil {
				pvcName = volume.PersistentVolumeClaim.ClaimName
				break
			}
		}
	}

	if pvcName == "" {
		logger.Debug("No PVC found for CD-ROM volume %s in VM %s/%s", volumeRef, namespace, name)
		return false, nil
	}

	// Step 4: Check if the PVC is bound and has non-zero size
	pvcObj, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), pvcName, metav1.GetOptions{})
	if err != nil {
		logger.Debug("PVC %s not found for VM %s/%s: %v", pvcName, namespace, name, err)
		return false, nil
	}

	// Check if PVC is bound and has data
	if pvcObj.Status.Phase == corev1.ClaimBound {
		// Additional check: verify the PVC has actual data by checking if it's not empty
		// This is important for DataVolume imports that may take time
		if len(pvcObj.Status.Capacity) > 0 {
			// Check if there's actual storage allocated
			if storage, found := pvcObj.Status.Capacity["storage"]; found && !storage.IsZero() {
				logger.Debug("Virtual media %s has ready media (PVC: %s, size: %s) for VM %s/%s", mediaID, pvcName, storage.String(), namespace, name)
				return true, nil
			}
		}
		logger.Debug("Virtual media %s has bound PVC %s but no storage capacity for VM %s/%s", mediaID, pvcName, namespace, name)
		return false, nil
	} else {
		logger.Debug("Virtual media %s has media but PVC %s is not bound (phase: %s) for VM %s/%s", mediaID, pvcName, pvcObj.Status.Phase, namespace, name)
		return false, nil
	}
}

// downloadISO downloads an ISO file to a temporary directory
func (c *Client) downloadISO(imageURL string) (string, error) {
	logger.Info("Downloading ISO from %s to temporary directory", imageURL)

	// Create temp directory
	tempDir, err := os.MkdirTemp("/tmp", "kubevirt-redfish-iso-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Use the pooled HTTP client with extended timeout for large downloads
	downloadClient := &http.Client{
		Timeout: 10 * time.Minute, // Extended timeout for large ISOs
		Transport: &http.Transport{
			MaxIdleConns:        100,              // Reuse connection pool
			MaxIdleConnsPerHost: 10,               // Maximum idle connections per host
			IdleConnTimeout:     90 * time.Second, // How long to keep idle connections
			TLSHandshakeTimeout: 10 * time.Second, // TLS handshake timeout
			DisableCompression:  false,            // Enable compression for better performance
			ForceAttemptHTTP2:   true,             // Enable HTTP/2 for better performance
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // Allow self-signed certificates for external ISO downloads
			},
		},
	}

	// Download the file
	resp, err := downloadClient.Get(imageURL)
	if err != nil {
		os.RemoveAll(tempDir) // Clean up on error
		return "", fmt.Errorf("failed to download ISO: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.RemoveAll(tempDir) // Clean up on error
		return "", fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Extract filename from URL or use default
	filename := "boot.iso"
	if u, err := url.Parse(imageURL); err == nil {
		if path := filepath.Base(u.Path); path != "" && path != "." {
			filename = path
		}
	}

	// Create the file
	filePath := filepath.Join(tempDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		os.RemoveAll(tempDir) // Clean up on error
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Copy the data
	written, err := io.Copy(file, resp.Body)
	if err != nil {
		os.RemoveAll(tempDir) // Clean up on error
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	logger.Info("Downloaded ISO to %s (%d bytes)", filePath, written)
	return filePath, nil
}

// CheckInsertedMedia checks whether virtual media is already inserted for the
// given device and how it relates to the requested imageURL. The returned
// MediaState tells the caller whether to proceed with insertion, return an
// idempotent success, or reject with a conflict.
func (c *Client) CheckInsertedMedia(namespace, name, mediaID, imageURL string) (MediaState, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return MediaStateNone, fmt.Errorf("failed to get VM %s/%s: %w", namespace, name, err)
	}

	// Check if the device has a PVC volume attached
	hasPVC := false
	if vm.Spec.Template != nil {
		for _, vol := range vm.Spec.Template.Spec.Volumes {
			if vol.Name == mediaID && vol.PersistentVolumeClaim != nil {
				hasPVC = true
				break
			}
		}
	}
	if !hasPVC {
		return MediaStateNone, nil
	}

	// A PVC is attached. Compare the stored URL.
	annotationKey := VirtualMediaURLAnnotationPrefix + mediaID
	storedURL := vm.GetAnnotations()[annotationKey]

	if storedURL != imageURL {
		return MediaStateConflict, nil
	}

	// Same URL — check whether import is still in progress.
	labelKey := ImportingLabelPrefix + mediaID
	if vm.GetLabels()[labelKey] != "" {
		return MediaStateImporting, nil
	}

	return MediaStateReady, nil
}

// InsertVirtualMedia inserts virtual media into a VirtualMachine
func (c *Client) InsertVirtualMedia(namespace, name, mediaID, imageURL string) error {
	logger.Info("Inserting virtual media %s with image %s for VM %s/%s", mediaID, imageURL, namespace, name)
	logger.Debug("InsertVirtualMedia called - namespace=%s, name=%s, mediaID=%s, imageURL=%s", namespace, name, mediaID, imageURL)

	// Perform the actual insertion work
	logger.Debug("Calling insertVirtualMediaAsync for VM %s/%s", namespace, name)
	if err := c.insertVirtualMediaAsync(namespace, name, mediaID, imageURL); err != nil {
		logger.Error("Failed to insert virtual media %s for VM %s/%s: %v", mediaID, namespace, name, err)
		return err
	}

	logger.Info("Successfully completed virtual media insertion %s for VM %s/%s", mediaID, namespace, name)
	logger.Debug("Virtual media insertion completed successfully for VM %s/%s", namespace, name)
	return nil
}

// insertVirtualMediaAsync performs the actual virtual media insertion work
func (c *Client) insertVirtualMediaAsync(namespace, name, mediaID, imageURL string) error {
	logger.Debug("insertVirtualMediaAsync called - namespace=%s, name=%s, mediaID=%s, imageURL=%s", namespace, name, mediaID, imageURL)

	// Get DataVolume configuration first to determine timeouts
	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage := c.getDataVolumeConfig()
	logger.Info("Using DataVolume config: storageSize=%s, allowInsecureTLS=%v, storageClass=%s, vmUpdateTimeout=%s, isoDownloadTimeout=%s, helperImage=%s", storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage)
	logger.Debug("DataVolume config - storageSize=%s, allowInsecureTLS=%v, storageClass=%s, vmUpdateTimeout=%s, isoDownloadTimeout=%s, helperImage=%s", storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage)

	// Parse timeout for VM update
	vmUpdateDuration, err := time.ParseDuration(vmUpdateTimeout)
	if err != nil {
		logger.Warning("Invalid vm_update_timeout %s, using default 30s: %v", vmUpdateTimeout, err)
		vmUpdateDuration = 30 * time.Second
	}
	logger.Debug("Using VM update timeout: %v", vmUpdateDuration)

	// Use VM update timeout for this operation
	ctx, cancel := context.WithTimeout(context.Background(), vmUpdateDuration)
	defer cancel()

	logger.Info("Inserting virtual media %s with image %s for VM %s/%s", mediaID, imageURL, namespace, name)
	logger.Debug("Starting virtual media insertion process for VM %s/%s", namespace, name)

	// Parse URL to determine scheme
	u, parseErr := url.Parse(imageURL)
	if parseErr != nil {
		logger.Error("Failed to parse URL %s: %v", imageURL, parseErr)
		return fmt.Errorf("failed to parse URL %s: %w", imageURL, parseErr)
	}
	logger.Debug("Parsed URL - scheme=%s, host=%s", u.Scheme, u.Host)

	// Generate unique PVC name with timestamp and random suffix to avoid conflicts
	dataVolumeName := c.generateUniquePVCName(name)
	logger.Debug("Generated unique dataVolumeName=%s", dataVolumeName)

	// First, create the CD-ROM device in the VM spec
	// Use lowercase device name for KubeVirt compatibility
	deviceName := "cdrom0"
	logger.Info("Creating CD-ROM device %s in VM spec first", deviceName)
	logger.Debug("Creating CD-ROM device %s in VM spec", deviceName)

	var oldPVCName string

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		logger.Debug("VM update attempt %d/%d", attempt, maxRetries)

		vm, err := c.GetVM(namespace, name)
		if err != nil {
			logger.Error("Failed to get VM %s/%s: %v", namespace, name, err)
			return fmt.Errorf("failed to get VM %s: %w", name, err)
		}
		logger.Debug("Successfully retrieved VM %s/%s", namespace, name)

		vmCopy := vm.DeepCopy()

		if vmCopy.Spec.Template == nil {
			vmCopy.Spec.Template = &kubevirtv1.VirtualMachineInstanceTemplateSpec{}
		}

		diskExists := false
		for _, disk := range vmCopy.Spec.Template.Spec.Domain.Devices.Disks {
			if disk.Name == deviceName {
				diskExists = true
				break
			}
		}

		if !diskExists {
			newDisk := kubevirtv1.Disk{
				Name: deviceName,
				DiskDevice: kubevirtv1.DiskDevice{
					CDRom: &kubevirtv1.CDRomTarget{
						Bus: kubevirtv1.DiskBusSATA,
					},
				},
			}
			vmCopy.Spec.Template.Spec.Domain.Devices.Disks = append(vmCopy.Spec.Template.Spec.Domain.Devices.Disks, newDisk)
			logger.Info("Added new CD-ROM device %s", deviceName)
		}

		oldPVCName = ""
		volumeUpdated := false
		for i, volume := range vmCopy.Spec.Template.Spec.Volumes {
			if volume.Name == deviceName {
				if volume.PersistentVolumeClaim != nil {
					oldPVCName = volume.PersistentVolumeClaim.ClaimName
				}
				vmCopy.Spec.Template.Spec.Volumes[i] = kubevirtv1.Volume{
					Name: deviceName,
					VolumeSource: kubevirtv1.VolumeSource{
						PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
							PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: dataVolumeName,
							},
						},
					},
				}
				volumeUpdated = true
				logger.Info("Updated volume reference %s: %s -> %s", deviceName, oldPVCName, dataVolumeName)
				break
			}
		}

		if !volumeUpdated {
			newVolume := kubevirtv1.Volume{
				Name: deviceName,
				VolumeSource: kubevirtv1.VolumeSource{
					PersistentVolumeClaim: &kubevirtv1.PersistentVolumeClaimVolumeSource{
						PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: dataVolumeName,
						},
					},
				},
			}
			vmCopy.Spec.Template.Spec.Volumes = append(vmCopy.Spec.Template.Spec.Volumes, newVolume)
			logger.Info("Added new volume reference %s for PVC %s", deviceName, dataVolumeName)
		}

		annotations := vmCopy.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[VirtualMediaURLAnnotationPrefix+deviceName] = imageURL
		vmCopy.SetAnnotations(annotations)

		// Compute merge patch from changes (preserves unknown fields)
		patch, err := computeVMPatch(vm, vmCopy)
		if err != nil {
			logger.Error("Failed to compute VM patch: %v", err)
			return fmt.Errorf("failed to compute VM patch: %w", err)
		}

		gvrVM := schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}

		logger.Debug("Updating VM %s/%s with new CD-ROM device", namespace, name)
		_, err = c.dynamicClient.Resource(gvrVM).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "the object has been modified") && attempt < maxRetries {
				logger.Info("Concurrent modification detected, retrying VM update (attempt %d/%d)", attempt, maxRetries)
				logger.Debug("Concurrent modification detected, retrying VM update (attempt %d/%d)", attempt, maxRetries)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			logger.Error("Failed to update VM %s/%s: %v", namespace, name, err)
			return fmt.Errorf("failed to update VM: %w", err)
		}

		logger.Debug("Successfully updated VM %s/%s on attempt %d", namespace, name, attempt)
		break
	}

	logger.Info("Successfully created CD-ROM device %s in VM spec", deviceName)

	// Clean up old PVC and its VolumeImportSource when re-inserting media
	if oldPVCName != "" && oldPVCName != dataVolumeName {
		logger.Info("Cleaning up previous virtual media resources: PVC %s", oldPVCName)

		if err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, oldPVCName, metav1.DeleteOptions{}); err != nil {
			logger.Warning("Failed to delete old PVC %s: %v", oldPVCName, err)
		} else {
			logger.Info("Deleted old PVC %s", oldPVCName)
		}

		oldVISName := sanitizeResourceName(fmt.Sprintf("%s-populator", oldPVCName))
		gvrVIS := schema.GroupVersionResource{Group: "cdi.kubevirt.io", Version: "v1beta1", Resource: "volumeimportsources"}
		if err := c.dynamicClient.Resource(gvrVIS).Namespace(namespace).Delete(ctx, oldVISName, metav1.DeleteOptions{}); err != nil {
			logger.Warning("Failed to delete old VolumeImportSource %s: %v", oldVISName, err)
		} else {
			logger.Info("Deleted old VolumeImportSource %s", oldVISName)
		}
	}

	// Determine volume mode before choosing the import strategy.
	// CDI's volume populator (VolumeImportSource) is only used for Block storage
	// because its prime-PVC mechanism can stall with WaitForFirstConsumer
	// provisioners. Filesystem storage (including the default when no storage
	// class is configured) always uses a helper pod which directly mounts the
	// PVC and triggers provisioning reliably.
	volumeMode := c.getStorageProfileVolumeMode(storageClass)
	isBlockMode := volumeMode != nil && *volumeMode == corev1.PersistentVolumeBlock
	useHelperPod := (allowInsecureTLS && u.Scheme == "https") || !isBlockMode

	if useHelperPod {
		reason := "Filesystem volume mode"
		if allowInsecureTLS && u.Scheme == "https" {
			reason = "allowInsecureTLS=true with HTTPS URL"
		}
		logger.Info("Using helper pod for ISO import (%s)", reason)

		// Create blank PVC for ISO files
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dataVolumeName,
				Namespace: namespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						"storage": resourceMustParse(storageSize),
					},
				},
			},
		}
		if storageClass != "" {
			pvc.Spec.StorageClassName = &storageClass
			logger.Info("Set storage class %s for PVC %s", storageClass, dataVolumeName)
		}
		if volumeMode != nil {
			pvc.Spec.VolumeMode = volumeMode
		}

		if err := c.ensurePVC(ctx, namespace, pvc); err != nil {
			return err
		}

		// Create helper pod to copy ISO to PVC (non-blocking, pod watcher handles completion)
		if err := c.copyISOToPVC(namespace, dataVolumeName, imageURL, isoDownloadTimeout, name, deviceName); err != nil {
			logger.Error("copyISOToPVC failed for PVC %s: %v", dataVolumeName, err)
			return fmt.Errorf("failed to copy ISO to PVC: %w", err)
		}
		logger.Info("Helper pod created for ISO import to PVC %s", dataVolumeName)
	} else {
		logger.Info("Using CDI HTTP import for ISO (Block volume mode)")
		volumeImportSourceName := sanitizeResourceName(fmt.Sprintf("%s-populator", dataVolumeName))

		volumeImportSource := &cdiv1beta1.VolumeImportSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      volumeImportSourceName,
				Namespace: namespace,
			},
			Spec: cdiv1beta1.VolumeImportSourceSpec{
				Source: &cdiv1beta1.ImportSourceType{
					HTTP: &cdiv1beta1.DataVolumeSourceHTTP{
						URL: imageURL,
					},
				},
			},
		}

		visUnstructured, err := volumeImportSourceToUnstructured(volumeImportSource)
		if err != nil {
			return fmt.Errorf("failed to convert VolumeImportSource to unstructured: %w", err)
		}

		gvrVIS := schema.GroupVersionResource{
			Group:    "cdi.kubevirt.io",
			Version:  "v1beta1",
			Resource: "volumeimportsources",
		}
		_, err = c.dynamicClient.Resource(gvrVIS).Namespace(namespace).Create(ctx, visUnstructured, metav1.CreateOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				logger.Info("VolumeImportSource %s already exists, reusing it", volumeImportSourceName)
			} else {
				return fmt.Errorf("failed to create VolumeImportSource: %w", err)
			}
		} else {
			logger.Info("Created new VolumeImportSource %s for virtual media", volumeImportSourceName)
		}
		// Label the PVC so the PVC watcher can map it back to the VM when it becomes Bound.
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dataVolumeName,
				Namespace: namespace,
				Labels: map[string]string{
					ImportPodVMLabel:     name,
					ImportPodVolumeLabel: deviceName,
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				DataSourceRef: &corev1.TypedObjectReference{
					APIGroup: stringPtr("cdi.kubevirt.io"),
					Kind:     "VolumeImportSource",
					Name:     volumeImportSourceName,
				},
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						"storage": resourceMustParse(storageSize),
					},
				},
			},
		}
		if storageClass != "" {
			pvc.Spec.StorageClassName = &storageClass
			logger.Info("Set storage class %s for PVC %s", storageClass, dataVolumeName)
		}
		pvc.Spec.VolumeMode = volumeMode
		if err := c.ensurePVC(ctx, namespace, pvc); err != nil {
			return err
		}
		logger.Info("VolumeImportSource and PVC created for HTTP import")

		// Mark the VM as having a CDI import in progress so power-on is deferred
		// until the PVC becomes Bound. The PVC watcher removes this label later.
		if err := c.setImportingLabel(namespace, name, deviceName, CDIImportPrefix+dataVolumeName); err != nil {
			logger.Error("Failed to set CDI importing label on VM %s/%s: %v", namespace, name, err)
			return fmt.Errorf("failed to set CDI importing label: %w", err)
		}
		logger.Info("CDI will handle the ISO download and import")
	}

	logger.Info("Successfully inserted virtual media %s for VM %s/%s", mediaID, namespace, name)
	return nil
}

// copyISOToPVC creates a helper pod that downloads an ISO and writes it to a PVC.
// The function returns immediately after creating the pod -- the import pod watcher
// handles completion detection, VM label cleanup, and pod deletion.
// The pod spec is built based on the PVC's volume mode: Block uses dd to a raw device,
// Filesystem uses a direct curl download to a mounted path.
func (c *Client) copyISOToPVC(namespace, dataVolumeName, imageURL, isoDownloadTimeout, vmName, deviceName string) error {
	logger.Info("Copying ISO from %s to PVC for DataVolume %s", imageURL, dataVolumeName)

	_, _, _, _, configISODownloadTimeout, helperImage := c.getDataVolumeConfig()

	if isoDownloadTimeout == "" {
		isoDownloadTimeout = configISODownloadTimeout
	}

	pvcName := dataVolumeName
	isoFileName := filepath.Base(imageURL)

	timestamp := time.Now().Unix()
	helperPodName := sanitizeResourceName(fmt.Sprintf("copy-iso-%s-%d", dataVolumeName, timestamp))

	// Check if helper pod already exists before creating
	existingPod, err := c.kubernetesClient.CoreV1().Pods(namespace).Get(context.Background(), helperPodName, metav1.GetOptions{})
	if err == nil {
		if existingPod.Status.Phase == corev1.PodSucceeded {
			logger.Info("Helper pod %s already completed successfully, reusing result", helperPodName)
			return nil
		} else if existingPod.Status.Phase == corev1.PodFailed {
			err = c.kubernetesClient.CoreV1().Pods(namespace).Delete(context.Background(), helperPodName, metav1.DeleteOptions{})
			if err != nil {
				logger.Warning("Failed to delete existing failed pod %s: %v", helperPodName, err)
			}
		} else {
			logger.Info("Helper pod %s already exists with status %s", helperPodName, existingPod.Status.Phase)
		}
	}

	// Read the PVC to determine its volume mode
	pvc, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), pvcName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get PVC %s to determine volume mode: %w", pvcName, err)
	}
	isBlock := pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode == corev1.PersistentVolumeBlock

	// Build container spec based on volume mode
	container := corev1.Container{
		Name:  "copy",
		Image: helperImage,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				"memory": resourceMustParse("128Mi"),
				"cpu":    resourceMustParse("50m"),
			},
			Limits: corev1.ResourceList{
				"memory": resourceMustParse("512Mi"),
				"cpu":    resourceMustParse("250m"),
			},
		},
	}

	if isBlock {
		logger.Info("PVC %s uses Block volume mode, helper pod will use dd", pvcName)
		container.Command = []string{"sh", "-c", fmt.Sprintf(
			"curl --fail --show-error --insecure --connect-timeout 30 --max-time 1800 --location -o /tmp/%s %s && [ -s /tmp/%s ] && dd if=/tmp/%s of=/dev/block bs=1M conv=fsync",
			isoFileName, imageURL, isoFileName, isoFileName)}
		container.VolumeDevices = []corev1.VolumeDevice{
			{Name: "iso-volume", DevicePath: "/dev/block"},
		}
	} else {
		logger.Info("PVC %s uses Filesystem volume mode, helper pod will download directly", pvcName)
		container.Command = []string{"sh", "-c", fmt.Sprintf(
			"curl --fail --show-error --insecure --connect-timeout 30 --max-time 1800 --location -o /mnt/iso/disk.img %s && [ -s /mnt/iso/disk.img ] && sync /mnt/iso/disk.img",
			imageURL)}
		container.VolumeMounts = []corev1.VolumeMount{
			{Name: "iso-volume", MountPath: "/mnt/iso"},
		}
	}

	helperPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helperPodName,
			Namespace: namespace,
			Labels: map[string]string{
				ImportPodVMLabel:     vmName,
				ImportPodVolumeLabel: deviceName,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{container},
			Volumes: []corev1.Volume{
				{Name: "iso-volume", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName}}},
			},
		},
	}

	var createdPod *corev1.Pod
	err = c.retryWithBackoff(fmt.Sprintf("create helper pod %s", helperPodName), func() error {
		var createErr error
		createdPod, createErr = c.kubernetesClient.CoreV1().Pods(namespace).Create(context.Background(), helperPod, metav1.CreateOptions{})
		if createErr != nil {
			if strings.Contains(createErr.Error(), "already exists") {
				existingPod, getErr := c.kubernetesClient.CoreV1().Pods(namespace).Get(context.Background(), helperPodName, metav1.GetOptions{})
				if getErr != nil {
					logger.Error("Failed to get existing helper pod %s: %v", helperPodName, getErr)
					return fmt.Errorf("failed to get existing helper pod: %w", getErr)
				}
				createdPod = existingPod
				return nil
			}
			logger.Error("Failed to create helper pod %s: %v", helperPodName, createErr)
			return fmt.Errorf("failed to create helper pod: %w", createErr)
		}
		return nil
	})

	if err != nil {
		logger.Error("All attempts to create helper pod %s failed: %v", helperPodName, err)
		return fmt.Errorf("failed to create helper pod after retries: %w", err)
	}

	logger.Info("Created helper pod %s (UID %s) for VM %s/%s volume %s", helperPodName, createdPod.UID, namespace, vmName, deviceName)

	// Set importing label on the VM so watchers and power management can detect the in-progress import
	if err := c.setImportingLabel(namespace, vmName, deviceName, helperPodName); err != nil {
		logger.Error("Failed to set importing label on VM %s/%s: %v", namespace, vmName, err)
		return fmt.Errorf("failed to set importing label: %w", err)
	}

	return nil
}

// setImportingLabel sets the importing.redfish/<deviceName> label on a VM
func (c *Client) setImportingLabel(namespace, vmName, deviceName, podName string) error {
	labelKey := ImportingLabelPrefix + deviceName
	patchJSON := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, labelKey, podName)

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, vmName, types.MergePatchType, []byte(patchJSON), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to patch VM %s/%s with importing label: %w", namespace, vmName, err)
	}
	logger.Info("Set importing label %s=%s on VM %s/%s", labelKey, podName, namespace, vmName)
	return nil
}

// IsImportInProgress checks if a VM has any active importing.redfish/* labels
func (c *Client) IsImportInProgress(namespace, name string) (bool, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return false, fmt.Errorf("failed to get VM %s/%s: %w", namespace, name, err)
	}

	for key := range vm.GetLabels() {
		if strings.HasPrefix(key, ImportingLabelPrefix) {
			return true, nil
		}
	}
	return false, nil
}

// SetPowerAfterImportLabel sets the power-after-import.redfish label on a VM
func (c *Client) SetPowerAfterImportLabel(namespace, name, powerState string) error {
	patchJSON := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, PowerAfterImportLabel, powerState)

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, []byte(patchJSON), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to set power-after-import label on VM %s/%s: %w", namespace, name, err)
	}
	logger.Info("Set power-after-import label %s=%s on VM %s/%s", PowerAfterImportLabel, powerState, namespace, name)
	return nil
}

// resourceMustParse is a helper for resource.Quantity
func resourceMustParse(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}

// stringPtr returns a pointer to the string value
func stringPtr(s string) *string {
	return &s
}

// SetBootOrder sets the boot order for a VM to prioritize the selected device.
// CD-ROM when boot target is CD, but this also keeps disk as the backup device so media ejection is handled
// The first disk when boot target is Hdd
func (c *Client) SetBootOrder(namespace, name, bootTarget string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	logger.Info("Setting boot order to %s for VM %s/%s", bootTarget, namespace, name)

	// Get the current VM
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	// Update VM with boot order configuration
	vmCopy := vm.DeepCopy()

	// Collect current volumes and detect their type
	// This is needed, because the device list does not provide enough
	// information about disk type and there are disks that are not bootable
	// like cloud init.
	err = c.modifyVmBootOrder(vmCopy, bootTarget)
	if err != nil {
		return err
	}

	// Compute merge patch from changes (preserves unknown fields)
	patch, err := computeVMPatch(vm, vmCopy)
	if err != nil {
		return fmt.Errorf("failed to compute VM patch: %w", err)
	}

	// Apply patch
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to update VM boot order: %w", err)
	}

	logger.Info("Successfully set boot order to %s for VM %s/%s", bootTarget, namespace, name)
	return nil
}

func (*Client) modifyVmBootOrder(vmCopy *kubevirtv1.VirtualMachine, bootTarget string) error {
	const (
		HDD   = "hdd"
		CDROM = "cd"
	)

	if vmCopy.Spec.Template == nil {
		return nil
	}

	// Collect current volumes and detect their type
	volumeTypes := map[string]string{}
	cdsFound := 0
	for _, volume := range vmCopy.Spec.Template.Spec.Volumes {
		volumeName := volume.Name

		// Heuristic for catching autoimported cdroms
		if strings.HasPrefix(volumeName, "cdrom") {
			cdsFound++
			volumeTypes[volumeName] = CDROM
			continue
		}

		// Two basic disk types are recognized: dataVolume and containerDisk
		if volume.DataVolume != nil {
			volumeTypes[volumeName] = HDD
		}
		if volume.ContainerDisk != nil {
			volumeTypes[volumeName] = HDD
		}
	}

	// Update disk boot order
	nextBootOrderPrimary := uint(1)
	nextBootOrderSecondary := uint(1)
	for i := range vmCopy.Spec.Template.Spec.Domain.Devices.Disks {
		disk := &vmCopy.Spec.Template.Spec.Domain.Devices.Disks[i]
		diskName := disk.Name

		if volType, found := volumeTypes[diskName]; found && volType == CDROM && bootTarget == "Cd" {
			// Set CD-ROMs as primary boot devices
			bootOrder := nextBootOrderPrimary
			disk.BootOrder = &bootOrder
			logger.Info("Set CD-ROM %s as primary boot device (order: %d)", diskName, nextBootOrderPrimary)
			nextBootOrderPrimary++
		} else if volType, found := volumeTypes[diskName]; found && volType == HDD && bootTarget == "Hdd" {
			// Set disk as primary boot device
			bootOrder := nextBootOrderPrimary
			disk.BootOrder = &bootOrder
			logger.Info("Set disk %s as primary boot device (order: %d)", diskName, nextBootOrderPrimary)
			nextBootOrderPrimary++
		} else if volType, found := volumeTypes[diskName]; found && volType == HDD && bootTarget == "Cd" {
			// Set main disk as secondary boot device
			bootOrder := uint(cdsFound) + nextBootOrderSecondary
			disk.BootOrder = &bootOrder
			logger.Info("Set disk %s as secondary boot device (order: %d)", diskName, uint(cdsFound)+nextBootOrderSecondary)
			nextBootOrderSecondary++
		} else {
			// All other devices should have their boot order removed
			disk.BootOrder = nil
		}
	}

	return nil
}

// SetBootOnce sets the boot source override to "Once" for the next boot
// It saves the original boot configuration and VMI UID so it can be restored after reboot
func (c *Client) SetBootOnce(namespace, name, bootTarget string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	logger.Info("Setting boot once to %s for VM %s/%s", bootTarget, namespace, name)

	// Get the current VM
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	// Update VM with boot once configuration
	vmCopy := vm.DeepCopy()

	// Get current annotations and labels
	annotations := vmCopy.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	vmLabels := vmCopy.GetLabels()
	if vmLabels == nil {
		vmLabels = make(map[string]string)
	}

	// Check if boot-once is already configured
	existingOriginalConfig := annotations[BootOnceOriginalConfigAnnotation]
	existingVMIUID := annotations[BootOnceVMIUIDAnnotation]
	hasExistingBootOnce := existingOriginalConfig != ""

	// Get current VMI UID
	currentVMIUID := c.getVMIUID(namespace, name)

	if hasExistingBootOnce {
		// Boot-once already configured - check if the recorded VMI is still running
		if existingVMIUID != "" && existingVMIUID == currentVMIUID {
			// Same VMI is still running - keep original config, just update boot order
			logger.Info("Boot-once already configured for VM %s/%s with same VMI, updating boot target only", namespace, name)
		} else {
			// VMI has changed or was empty - clear old state and capture new original config
			logger.Info("Boot-once state is stale for VM %s/%s (VMI changed), capturing new original config", namespace, name)

			// Restore the original boot order so we capture the real original state below
			if err := c.restoreBootOrder(vmCopy, existingOriginalConfig); err != nil {
				logger.Warning("Failed to restore original boot order before recapture: %v", err)
			}

			// Capture the (now restored) boot order as the new original config
			originalConfig, err := c.captureCurrentBootOrder(vmCopy)
			if err != nil {
				return fmt.Errorf("failed to capture boot order: %w", err)
			}
			annotations[BootOnceOriginalConfigAnnotation] = originalConfig
			annotations[BootOnceVMIUIDAnnotation] = currentVMIUID
		}
	} else {
		// No existing boot-once - capture current boot order
		originalConfig, err := c.captureCurrentBootOrder(vmCopy)
		if err != nil {
			return fmt.Errorf("failed to capture boot order: %w", err)
		}
		annotations[BootOnceOriginalConfigAnnotation] = originalConfig
		annotations[BootOnceVMIUIDAnnotation] = currentVMIUID
	}

	// Add boot-once label for watch selector
	vmLabels[BootOnceLabel] = "enabled"
	vmCopy.SetLabels(vmLabels)

	// Store Redfish boot override annotations
	annotations["redfish.boot.source.override.enabled"] = "Once"
	annotations["redfish.boot.source.override.target"] = bootTarget
	annotations["redfish.boot.source.override.mode"] = "UEFI"
	vmCopy.SetAnnotations(annotations)

	// Modify boot order to boot from target
	if err := c.modifyVmBootOrder(vmCopy, bootTarget); err != nil {
		return fmt.Errorf("failed to modify boot order: %w", err)
	}

	// Compute merge patch from changes (preserves unknown fields)
	patch, err := computeVMPatch(vm, vmCopy)
	if err != nil {
		return fmt.Errorf("failed to compute VM patch: %w", err)
	}

	// Apply patch
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to update VM boot once configuration: %w", err)
	}

	logger.Info("Successfully set boot once to %s for VM %s/%s (VMI UID: %s)", bootTarget, namespace, name, currentVMIUID)
	return nil
}

// EjectVirtualMedia ejects virtual media from a VirtualMachine
func (c *Client) EjectVirtualMedia(namespace, name, mediaID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	logger.Info("Ejecting virtual media %s for VM %s/%s", mediaID, namespace, name)

	// Get the current VM
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		logger.Error("Failed to get VM %s/%s for ejection: %v", namespace, name, err)
		return fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	logger.Debug("Current VM spec before ejection")

	// Get the actual PVC name from the volume BEFORE removing it from the VM
	// This is necessary because PVC names have unique identifiers appended
	var actualPVCName string
	if vm.Spec.Template != nil {
		for _, volume := range vm.Spec.Template.Spec.Volumes {
			if volume.Name == mediaID && volume.PersistentVolumeClaim != nil {
				actualPVCName = volume.PersistentVolumeClaim.ClaimName
				logger.Info("Found actual PVC name %s for media %s", actualPVCName, mediaID)
				break
			}
		}
	}

	// Update VM to remove CD-ROM disk and volume reference
	vmCopy := vm.DeepCopy()

	if vmCopy.Spec.Template == nil {
		logger.Warning("No template found in VM %s/%s when trying to eject media %s", namespace, name, mediaID)
		return fmt.Errorf("no template found in VM")
	}

	// Remove the specified disk from disks list
	var newDisks []kubevirtv1.Disk
	foundDisk := false
	for _, disk := range vmCopy.Spec.Template.Spec.Domain.Devices.Disks {
		if disk.Name != mediaID {
			newDisks = append(newDisks, disk)
		} else {
			logger.Info("Removing CD-ROM device %s from VM spec", mediaID)
			foundDisk = true
		}
	}
	if !foundDisk {
		logger.Warning("CD-ROM device %s not found in VM %s/%s disks list during ejection", mediaID, namespace, name)
	}
	vmCopy.Spec.Template.Spec.Domain.Devices.Disks = newDisks

	// Remove the volume reference
	var newVolumes []kubevirtv1.Volume
	foundVolume := false
	for _, volume := range vmCopy.Spec.Template.Spec.Volumes {
		if volume.Name != mediaID {
			newVolumes = append(newVolumes, volume)
		} else {
			logger.Info("Removing volume reference %s from VM spec", mediaID)
			foundVolume = true
		}
	}
	if !foundVolume {
		logger.Warning("Volume reference %s not found in VM %s/%s during ejection", mediaID, namespace, name)
	}
	vmCopy.Spec.Template.Spec.Volumes = newVolumes

	annotations := vmCopy.GetAnnotations()
	delete(annotations, VirtualMediaURLAnnotationPrefix+mediaID)
	vmCopy.SetAnnotations(annotations)

	// Compute merge patch from changes (preserves unknown fields)
	patch, err := computeVMPatch(vm, vmCopy)
	if err != nil {
		logger.Error("Failed to compute VM patch for %s/%s: %v", namespace, name, err)
		return fmt.Errorf("failed to compute VM patch: %w", err)
	}

	logger.Debug("VM spec after removing CD-ROM and volume")

	// Apply patch
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		logger.Error("Failed to update VM %s/%s after removing CD-ROM and volume: %v", namespace, name, err)
		return fmt.Errorf("failed to update VM: %w", err)
	}

	logger.Info("Successfully updated VM %s/%s to remove CD-ROM and volume for media %s", namespace, name, mediaID)

	// Kill any running helper pod for this device and remove the importing label
	// so a stale pod cannot trigger a deferred power-on after the media is gone.
	labelKey := ImportingLabelPrefix + mediaID
	helperPodName := vm.GetLabels()[labelKey]
	if helperPodName != "" {
		logger.Info("Cleaning up active import for device %s: deleting helper pod %s and removing importing label", mediaID, helperPodName)
		if delErr := c.kubernetesClient.CoreV1().Pods(namespace).Delete(ctx, helperPodName, metav1.DeleteOptions{}); delErr != nil {
			logger.Warning("Failed to delete helper pod %s during eject: %v", helperPodName, delErr)
		}
		if rmErr := c.removeImportingLabel(namespace, name, mediaID); rmErr != nil {
			logger.Warning("Failed to remove importing label during eject for VM %s/%s: %v", namespace, name, rmErr)
		}
	}

	// Clean up associated PVC and VolumeImportSource using the actual names
	// If we couldn't find the actual PVC name, try the fallback name
	pvcName := actualPVCName
	if pvcName == "" {
		pvcName = fmt.Sprintf("%s-bootiso", name)
		logger.Warning("Could not determine actual PVC name from VM spec, using fallback name %s - cleanup may be incomplete; check for orphaned PVCs with pattern '%s-bootiso-*'", pvcName, name)
	}

	// Generate VolumeImportSource name based on PVC name using the -populator suffix convention
	volumeImportSourceName := sanitizeResourceName(fmt.Sprintf("%s-populator", pvcName))

	// Log PVC state before deletion
	pvc, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err == nil {
		logger.Debug("PVC %s state before deletion: phase=%s, capacity=%v", pvcName, pvc.Status.Phase, pvc.Status.Capacity)
	} else {
		logger.Warning("PVC %s not found before deletion: %v", pvcName, err)
	}

	// Delete PVC
	logger.Info("Deleting PVC %s for ejected virtual media", pvcName)
	err = c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
	if err != nil {
		logger.Warning("Failed to delete PVC %s: %v", pvcName, err)
		// Don't fail the operation if PVC cleanup fails
	} else {
		logger.Info("Successfully deleted PVC %s", pvcName)
	}

	// Delete VolumeImportSource
	// Note: VolumeImportSource only exists when CDI import was used (HTTP, or HTTPS with valid certificate).
	// If helper pod was used (HTTPS with allowInsecureTLS=true), no VolumeImportSource was created.
	gvrVIS := schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "volumeimportsources",
	}

	logger.Info("Deleting VolumeImportSource %s for ejected virtual media", volumeImportSourceName)
	err = c.dynamicClient.Resource(gvrVIS).Namespace(namespace).Delete(ctx, volumeImportSourceName, metav1.DeleteOptions{})
	if err != nil {
		logger.Warning("Failed to delete VolumeImportSource %s: %v (this is expected if ISO was imported via helper pod instead of CDI)", volumeImportSourceName, err)
		// Don't fail the operation if VolumeImportSource cleanup fails
	} else {
		logger.Info("Successfully deleted VolumeImportSource %s", volumeImportSourceName)
	}

	logger.Info("Successfully ejected virtual media %s for VM %s/%s", mediaID, namespace, name)
	return nil
}

// TestConnection tests the connection to the Kubernetes cluster
func (c *Client) TestConnection() error {
	// Test by getting server version
	_, err := c.kubernetesClient.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to connect to kubernetes cluster: %w", err)
	}

	return nil
}

// GetNamespaceInfo gets information about a namespace
func (c *Client) GetNamespaceInfo(namespace string) (*corev1.Namespace, error) {
	return c.kubernetesClient.CoreV1().Namespaces().Get(context.Background(), namespace, metav1.GetOptions{})
}

// GetVMMemory gets memory information of a VirtualMachine
func (c *Client) GetVMMemory(namespace, name string) (float64, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		logger.Warning("Failed to get VM %s/%s for memory lookup: %v", namespace, name, err)
		return 2.0, nil // Low default fallback
	}

	// Get memory from VM spec using typed API
	if vm.Spec.Template == nil || vm.Spec.Template.Spec.Domain.Memory == nil ||
		vm.Spec.Template.Spec.Domain.Memory.Guest == nil {
		logger.Warning("Memory not found in VM spec for %s/%s, using default 2.0 GB", namespace, name)
		return 2.0, nil // Low default fallback
	}

	memory := vm.Spec.Template.Spec.Domain.Memory.Guest.String()
	logger.Debug("Found memory spec for %s/%s: %s", namespace, name, memory)

	// Parse memory string (e.g., "48Gi" -> 48.0)
	if strings.HasSuffix(memory, "Gi") {
		memoryStr := strings.TrimSuffix(memory, "Gi")
		if memoryGB, err := strconv.ParseFloat(memoryStr, 64); err == nil {
			logger.Debug("Successfully parsed memory for %s/%s: %.1f GB", namespace, name, memoryGB)
			return memoryGB, nil
		}
	} else if strings.HasSuffix(memory, "Mi") {
		memoryStr := strings.TrimSuffix(memory, "Mi")
		if memoryMB, err := strconv.ParseFloat(memoryStr, 64); err == nil {
			memoryGB := memoryMB / 1024.0
			logger.Debug("Successfully parsed memory for %s/%s: %.1f GB (from %s Mi)", namespace, name, memoryGB, memoryStr)
			return memoryGB, nil
		}
	}

	logger.Warning("Failed to parse memory for %s/%s: %s, using default 2.0 GB", namespace, name, memory)
	return 2.0, nil // Low default fallback
}

// GetVMCPU gets CPU information of a VirtualMachine
func (c *Client) GetVMCPU(namespace, name string) (int, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		logger.Warning("Failed to get VM %s/%s for CPU lookup: %v", namespace, name, err)
		return 1, nil // Low default fallback
	}

	// Get CPU cores from VM spec using typed API
	if vm.Spec.Template == nil || vm.Spec.Template.Spec.Domain.CPU == nil {
		logger.Warning("CPU cores not found in VM spec for %s/%s, using default 1 core", namespace, name)
		return 1, nil // Low default fallback
	}

	cpuCores := vm.Spec.Template.Spec.Domain.CPU.Cores
	if cpuCores == 0 {
		logger.Warning("CPU cores is 0 in VM spec for %s/%s, using default 1 core", namespace, name)
		return 1, nil // Low default fallback
	}

	logger.Debug("Successfully found CPU cores for %s/%s: %d", namespace, name, cpuCores)
	return int(cpuCores), nil
}

// GetVMStorageDetails gets detailed storage information of a VirtualMachine
func (c *Client) GetVMStorageDetails(namespace, name string) (map[string]interface{}, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return nil, err
	}

	storageInfo := map[string]interface{}{
		"totalCapacityGB": 0.0,
		"volumes":         []map[string]interface{}{},
	}

	// Get DataVolume templates for storage capacity using typed API
	totalCapacity := 0.0
	var volumes []map[string]interface{}

	for _, dv := range vm.Spec.DataVolumeTemplates {
		volumeName := dv.Name
		capacity := 0.0

		// Get storage capacity from DataVolume spec
		if dv.Spec.Storage != nil && dv.Spec.Storage.Resources.Requests != nil {
			if storageQty, found := dv.Spec.Storage.Resources.Requests["storage"]; found {
				storageStr := storageQty.String()
				// Parse storage string (e.g., "120Gi" -> 120.0)
				if strings.HasSuffix(storageStr, "Gi") {
					capacityStr := strings.TrimSuffix(storageStr, "Gi")
					if capacityGB, err := strconv.ParseFloat(capacityStr, 64); err == nil {
						capacity = capacityGB
					}
				} else if strings.HasSuffix(storageStr, "Mi") {
					capacityStr := strings.TrimSuffix(storageStr, "Mi")
					if capacityMB, err := strconv.ParseFloat(capacityStr, 64); err == nil {
						capacity = capacityMB / 1024.0
					}
				}
			}
		}

		totalCapacity += capacity
		volumes = append(volumes, map[string]interface{}{
			"name":     volumeName,
			"capacity": capacity,
		})
	}

	storageInfo["totalCapacityGB"] = totalCapacity
	storageInfo["volumes"] = volumes

	return storageInfo, nil
}

// GetVMNetworkDetails gets detailed network information of a VirtualMachine
func (c *Client) GetVMNetworkDetails(namespace, name string) ([]map[string]interface{}, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return nil, err
	}

	var interfaces []map[string]interface{}

	if vm.Spec.Template == nil {
		return interfaces, nil
	}

	// Get network interfaces from VM spec using typed API
	for _, iface := range vm.Spec.Template.Spec.Domain.Devices.Interfaces {
		interfaceInfo := map[string]interface{}{
			"name": iface.Name,
			"mac":  iface.MacAddress,
			"type": "bridge",
		}

		// Determine interface type
		if iface.Bridge != nil {
			interfaceInfo["type"] = "bridge"
		} else if iface.Masquerade != nil {
			interfaceInfo["type"] = "masquerade"
		} else if iface.SRIOV != nil {
			interfaceInfo["type"] = "sriov"
		}

		interfaces = append(interfaces, interfaceInfo)
	}

	return interfaces, nil
}

// getDataVolumeConfig returns DataVolume configuration from app config
func (c *Client) getDataVolumeConfig() (storageSize string, allowInsecureTLS bool, storageClass string, vmUpdateTimeout string, isoDownloadTimeout string, helperImage string) {
	// Default values
	storageSize = "10Gi"
	allowInsecureTLS = false
	storageClass = "" // Empty means use default storage class
	vmUpdateTimeout = "30s"
	isoDownloadTimeout = "30m"
	helperImage = "alpine:latest"

	// Try to get from app config if available
	if c.appConfig != nil {
		// Use type assertion to get config safely
		if config, ok := c.appConfig.(interface {
			GetDataVolumeConfig() (string, bool, string, string, string, string)
		}); ok {
			storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage = config.GetDataVolumeConfig()
			logger.Info("Read DataVolume config from app config: storageSize=%s, allowInsecureTLS=%v, storageClass=%s, vmUpdateTimeout=%s, isoDownloadTimeout=%s, helperImage=%s", storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage)
		} else {
			logger.Info("App config does not implement GetDataVolumeConfig method, using defaults")
		}
	} else {
		logger.Info("No app config available, using default DataVolume config")
	}

	logger.Info("Final DataVolume config: storageSize=%s, allowInsecureTLS=%v, storageClass=%s, vmUpdateTimeout=%s, isoDownloadTimeout=%s, helperImage=%s", storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage)
	return storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage
}

// getStorageProfileVolumeMode queries the CDI StorageProfile for the given storage class
// and returns the preferred volume mode. Falls back to Filesystem if the StorageProfile
// is not available (e.g. CDI not installed or storage class not recognized).
func (c *Client) getStorageProfileVolumeMode(storageClass string) *corev1.PersistentVolumeMode {
	if storageClass == "" {
		logger.Debug("No storage class specified, using default Filesystem volume mode")
		return nil
	}

	gvr := schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "storageprofiles",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// StorageProfiles are cluster-scoped and named after the StorageClass
	spUnstructured, err := c.dynamicClient.Resource(gvr).Get(ctx, storageClass, metav1.GetOptions{})
	if err != nil {
		logger.Debug("Could not fetch StorageProfile for %s (CDI may not be available): %v", storageClass, err)
		return nil
	}

	// Extract status.claimPropertySets[0].volumeMode
	sp := &cdiv1beta1.StorageProfile{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(spUnstructured.Object, sp); err != nil {
		logger.Debug("Could not convert StorageProfile for %s: %v", storageClass, err)
		return nil
	}

	if len(sp.Status.ClaimPropertySets) > 0 && sp.Status.ClaimPropertySets[0].VolumeMode != nil {
		mode := *sp.Status.ClaimPropertySets[0].VolumeMode
		logger.Info("StorageProfile for %s recommends volume mode: %s", storageClass, mode)
		return &mode
	}

	logger.Debug("StorageProfile for %s has no claimPropertySets, using default Filesystem volume mode", storageClass)
	return nil
}

// getKubeVirtConfig returns KubeVirt configuration from app config
func (c *Client) getKubeVirtConfig() (apiVersion string, timeout int, allowInsecureTLS bool) {
	// Default values
	apiVersion = "v1"
	timeout = 30
	allowInsecureTLS = false

	// Try to get from app config if available
	if c.appConfig != nil {
		// Use type assertion to get config safely
		if config, ok := c.appConfig.(interface {
			GetKubeVirtConfig() (string, int, bool)
		}); ok {
			apiVersion, timeout, allowInsecureTLS = config.GetKubeVirtConfig()
			logger.Info("Read KubeVirt config from app config: apiVersion=%s, timeout=%d, allowInsecureTLS=%v", apiVersion, timeout, allowInsecureTLS)
		} else {
			logger.Info("App config does not implement GetKubeVirtConfig method, using defaults")
		}
	} else {
		logger.Info("No app config available, using default KubeVirt config")
	}

	logger.Info("Final KubeVirt config: apiVersion=%s, timeout=%d, allowInsecureTLS=%v", apiVersion, timeout, allowInsecureTLS)
	return apiVersion, timeout, allowInsecureTLS
}

// generateUniquePVCName generates a unique PVC name with timestamp and random suffix
// to avoid conflicts when multiple operations target the same VM
func (c *Client) generateUniquePVCName(vmName string) string {
	timestamp := time.Now().Unix()
	randomSuffix := fmt.Sprintf("%06d", rand.Intn(1000000))
	name := fmt.Sprintf("%s-bootiso-%d-%s", vmName, timestamp, randomSuffix)
	return sanitizeResourceName(name)
}

// isPVCUsable checks if a PVC is in a usable state for mounting
func (c *Client) isPVCUsable(pvc *corev1.PersistentVolumeClaim) bool {
	// Check if PVC is bound and ready
	if pvc.Status.Phase == corev1.ClaimBound {
		return true
	}

	// Check if PVC is pending but not in a failed state
	if pvc.Status.Phase == corev1.ClaimPending {
		// Check if there are any conditions that indicate failure
		for _, condition := range pvc.Status.Conditions {
			if condition.Type == "Failed" && condition.Status == corev1.ConditionTrue {
				return false
			}
		}
		// Pending without failure conditions is considered usable (will be bound soon)
		return true
	}

	// Lost or other states are not usable
	return false
}

// ensurePVC checks whether a PVC with the given name already exists. If it exists
// and is usable (Bound or Pending without failure), it is reused. If it exists but
// is not usable (Lost, failed), it is deleted and recreated. If it does not exist,
// it is created.
func (c *Client) ensurePVC(ctx context.Context, namespace string, pvc *corev1.PersistentVolumeClaim) error {
	name := pvc.Name

	existing, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if c.isPVCUsable(existing) {
			logger.Info("PVC %s already exists and is usable, reusing it", name)
			return nil
		}
		logger.Info("PVC %s exists but is not usable (phase: %s), deleting and recreating", name, existing.Status.Phase)
		if delErr := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, metav1.DeleteOptions{}); delErr != nil {
			return fmt.Errorf("failed to delete unusable PVC %s: %w", name, delErr)
		}
		time.Sleep(2 * time.Second)
	}

	_, err = c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			logger.Info("PVC %s was created concurrently, reusing it", name)
			return nil
		}
		return fmt.Errorf("failed to create PVC %s: %w", name, err)
	}
	logger.Info("Created new PVC %s for virtual media", name)
	return nil
}

// sanitizeResourceName sanitizes a resource name to ensure it is a valid Kubernetes resource name
// It truncates the resourceName to 63 characters if it's longer that that and ensures it ends with
// alphanumeric character by appending "truncated" string, it also avoids name collisions by using a hash of the name
func sanitizeResourceName(resourceName string) string {
	if len(resourceName) <= 63 {
		return resourceName
	}
	hash := sha256.Sum256([]byte(resourceName))
	shortHash := new(big.Int).SetBytes(hash[:]).Text(36)[:5]

	return resourceName[:49] + shortHash + "truncated"
}

// =============================================================================
// BOOT ONCE HELPER FUNCTIONS
// =============================================================================

// BootOnceLabel is the label used to identify VMs with boot-once configuration
const BootOnceLabel = "redfish.boot.once"

// BootOnceAnnotations are the annotation keys used for boot-once state
const (
	BootOnceOriginalConfigAnnotation = "redfish.boot.once.original-config"
	BootOnceVMIUIDAnnotation         = "redfish.boot.once.vmi-uid"
)

// Import tracking labels
const (
	ImportingLabelPrefix  = "importing.redfish/"
	PowerAfterImportLabel = "power-after-import.redfish"
	ImportPodVMLabel      = "vm.redfish"
	ImportPodVolumeLabel  = "volume.vm.redfish"
	CDIImportPrefix       = "cdi-"
	CDIManagedLabel       = "cdi.kubevirt.io"
)

// reconcileInterval is the period between periodic sweeps of pending objects.
// This catches race conditions where a watch event was missed or an import
// completed between the IsImportInProgress check and the label write.
const reconcileInterval = time.Minute

// VirtualMediaURLAnnotationPrefix is used to record the image URL of currently
// inserted virtual media. The device name is appended (e.g. "virtual-media.redfish/cdrom0").
const VirtualMediaURLAnnotationPrefix = "virtual-media.redfish/"

// MediaState describes the state of an already-inserted virtual media device.
type MediaState int

const (
	MediaStateNone      MediaState = iota // no media inserted
	MediaStateImporting                   // media inserted, import in progress
	MediaStateReady                       // media inserted and ready
	MediaStateConflict                    // different media already inserted
)

// isCDIManagedPod returns true when the pod was created by CDI (e.g. the
// importer-prime pod for volume population). Our import pod watcher must
// never delete or otherwise interfere with these pods — CDI manages their
// lifecycle and has its own retry logic (restartPolicy: OnFailure).
func isCDIManagedPod(pod *corev1.Pod) bool {
	_, hasCDI := pod.GetLabels()[CDIManagedLabel]
	return hasCDI
}

// BootOrderConfig represents the boot order configuration for a disk
type BootOrderConfig struct {
	DiskName  string `json:"diskName"`
	BootOrder *uint  `json:"bootOrder,omitempty"` // nil means no boot order was set
}

// captureCurrentBootOrder returns JSON-encoded slice of disk boot orders
func (c *Client) captureCurrentBootOrder(vm *kubevirtv1.VirtualMachine) (string, error) {
	if vm.Spec.Template == nil {
		return "[]", nil
	}

	var bootOrders []BootOrderConfig
	for _, disk := range vm.Spec.Template.Spec.Domain.Devices.Disks {
		config := BootOrderConfig{
			DiskName: disk.Name,
		}
		if disk.BootOrder != nil {
			order := *disk.BootOrder
			config.BootOrder = &order
		}
		bootOrders = append(bootOrders, config)
	}

	jsonData, err := json.Marshal(bootOrders)
	if err != nil {
		return "", fmt.Errorf("failed to marshal boot order config: %w", err)
	}

	return string(jsonData), nil
}

// restoreBootOrder restores boot order from JSON-encoded config
func (c *Client) restoreBootOrder(vm *kubevirtv1.VirtualMachine, configJSON string) error {
	if vm.Spec.Template == nil {
		return nil
	}

	var bootOrders []BootOrderConfig
	if err := json.Unmarshal([]byte(configJSON), &bootOrders); err != nil {
		return fmt.Errorf("failed to unmarshal boot order config: %w", err)
	}

	// Create a map for quick lookup
	bootOrderMap := make(map[string]*uint)
	for _, config := range bootOrders {
		if config.BootOrder != nil {
			order := *config.BootOrder
			bootOrderMap[config.DiskName] = &order
		} else {
			bootOrderMap[config.DiskName] = nil
		}
	}

	// Restore boot orders to disks
	for i := range vm.Spec.Template.Spec.Domain.Devices.Disks {
		disk := &vm.Spec.Template.Spec.Domain.Devices.Disks[i]
		if order, found := bootOrderMap[disk.Name]; found {
			if order != nil {
				orderCopy := *order
				disk.BootOrder = &orderCopy
			} else {
				disk.BootOrder = nil
			}
		}
	}

	return nil
}

// getVMIUID returns the UID of the current VMI, or empty string if not running
func (c *Client) getVMIUID(namespace, name string) string {
	vmi, err := c.GetVMI(namespace, name)
	if err != nil {
		// VMI doesn't exist or error getting it - return empty string
		return ""
	}
	return string(vmi.GetUID())
}

// =============================================================================
// BOOT ONCE WATCHER
// =============================================================================

// StartBootOnceWatcher starts the Kubernetes watch for labeled VMs.
// If an existing watcher is running it is stopped first, making this
// safe to call on config reload.
func (c *Client) StartBootOnceWatcher(ctx context.Context, namespaces []string) {
	c.stopBootOnceWatcher()

	c.watcherCtx, c.watcherCancel = context.WithCancel(ctx)

	logger.Info("Starting boot-once watcher for namespaces: %v", namespaces)

	for _, namespace := range namespaces {
		c.watcherWg.Add(1)
		go func(ns string) {
			defer c.watcherWg.Done()
			c.runNamespaceWatcher(c.watcherCtx, ns)
		}(namespace)
	}
}

// stopBootOnceWatcher stops the watcher (called from Close)
func (c *Client) stopBootOnceWatcher() {
	if c.watcherCancel != nil {
		logger.Info("Stopping boot-once watcher")
		c.watcherCancel()
		c.watcherWg.Wait()
		c.watcherCancel = nil
		logger.Info("Boot-once watcher stopped")
	}
}

// runNamespaceWatcher runs a watch for a single namespace with automatic reconnection
func (c *Client) runNamespaceWatcher(ctx context.Context, namespace string) {
	logger.Info("Starting boot-once watcher for namespace: %s", namespace)

	// First, reconcile any existing boot-once VMs to clean up stale state
	if err := c.reconcileExistingBootOnceVMs(namespace); err != nil {
		logger.Error("Failed to reconcile existing boot-once VMs in namespace %s: %v", namespace, err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			logger.Info("VM watcher for namespace %s shutting down", namespace)
			return
		default:
		}

		// Watch all VMs -- filtering is done in the event handler
		watcher, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Watch(ctx, metav1.ListOptions{})
		if err != nil {
			logger.Error("Failed to create watch for namespace %s: %v, retrying in %v", namespace, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = min(backoff*2, maxBackoff)
				continue
			}
		}

		// Reset backoff on successful watch creation
		backoff = time.Second

		logger.Info("Watch for namespace %s created.", namespace)

		// Process watch events
		c.processWatchEvents(ctx, namespace, watcher)

		// If we get here, the watch ended - will reconnect
		logger.Info("Watch for namespace %s ended, reconnecting...", namespace)
	}
}

// processWatchEvents handles events from a watch channel.
// In addition to reacting to individual events it runs a periodic sweep
// every reconcileInterval to catch any objects that fell through the cracks
// (e.g. the race between IsImportInProgress and SetPowerAfterImportLabel).
func (c *Client) processWatchEvents(ctx context.Context, namespace string, watcher watch.Interface) {
	defer watcher.Stop()

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.reconcileExistingBootOnceVMs(namespace); err != nil {
				logger.Error("Periodic VM reconciliation failed for namespace %s: %v", namespace, err)
			}
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}

			logger.Debug("Watch event received: %s for namespace %s", event.Type, namespace)

			switch event.Type {
			case watch.Added, watch.Modified:
				u, ok := event.Object.(*unstructured.Unstructured)
				if !ok {
					logger.Warning("Unexpected object type in watch event: %T", event.Object)
					continue
				}

				vm, err := unstructuredToVM(u)
				if err != nil {
					logger.Error("Failed to convert watch event to VM: %v", err)
					continue
				}

				c.handleVMUpdate(vm)

			case watch.Deleted:
				logger.Debug("VM deleted from boot-once watch")

			case watch.Error:
				logger.Error("Watch error event received for namespace %s", namespace)
				return
			}
		}
	}
}

// handleVMUpdate processes a VM update event from the watch
func (c *Client) handleVMUpdate(vm *kubevirtv1.VirtualMachine) {
	namespace := vm.GetNamespace()
	name := vm.GetName()

	logger.Debug("Handling VM update event for VM %s/%s", namespace, name)

	// Get fresh VM to ensure we have current state (watch event may be stale)
	currentVM, err := c.GetVM(namespace, name)
	if err != nil {
		logger.Error("Failed to get current VM %s/%s: %v", namespace, name, err)
		return
	}

	vmLabels := currentVM.GetLabels()

	// Handle power-after-import if applicable
	if vmLabels != nil && vmLabels[PowerAfterImportLabel] != "" {
		c.handlePowerAfterImport(currentVM)
	}

	// Check if boot-once label is still present
	if vmLabels == nil || vmLabels[BootOnceLabel] != "enabled" {
		return
	}

	// Get the boot-once annotations from the current VM
	annotations := currentVM.GetAnnotations()
	if annotations == nil {
		return
	}

	originalConfig := annotations[BootOnceOriginalConfigAnnotation]
	recordedVMIUID := annotations[BootOnceVMIUIDAnnotation]

	if originalConfig == "" {
		// No original config, nothing to restore
		logger.Debug("VM %s/%s has boot-once label but no original config annotation", namespace, name)
		return
	}

	// Get current VMI UID
	currentVMIUID := c.getVMIUID(namespace, name)

	// Check if VMI has changed
	vmiChanged := false
	if recordedVMIUID == "" {
		// Recorded UID was empty (VM was off) - check if VMI now exists
		vmiChanged = currentVMIUID != ""
	} else {
		// Recorded UID was set - check if it's different
		vmiChanged = currentVMIUID != "" && currentVMIUID != recordedVMIUID
	}

	if vmiChanged {
		logger.Info("VMI UID changed for VM %s/%s (recorded: %s, current: %s), restoring boot order",
			namespace, name, recordedVMIUID, currentVMIUID)

		vmCopy := currentVM.DeepCopy()

		// Restore the original boot order
		if err := c.restoreBootOrder(vmCopy, originalConfig); err != nil {
			logger.Error("Failed to restore boot order for VM %s/%s: %v", namespace, name, err)
			return
		}

		// Remove boot-once label and annotations
		vmLabels := vmCopy.GetLabels()
		if vmLabels != nil {
			delete(vmLabels, BootOnceLabel)
			vmCopy.SetLabels(vmLabels)
		}

		vmAnnotations := vmCopy.GetAnnotations()
		if vmAnnotations != nil {
			delete(vmAnnotations, BootOnceOriginalConfigAnnotation)
			delete(vmAnnotations, BootOnceVMIUIDAnnotation)
			delete(vmAnnotations, "redfish.boot.source.override.enabled")
			delete(vmAnnotations, "redfish.boot.source.override.target")
			delete(vmAnnotations, "redfish.boot.source.override.mode")
			vmCopy.SetAnnotations(vmAnnotations)
		}

		// Compute merge patch from changes (preserves unknown fields)
		patch, err := computeVMPatch(currentVM, vmCopy)
		if err != nil {
			logger.Error("Failed to compute VM patch: %v", err)
			return
		}

		// Apply patch
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()

		gvr := schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}

		_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			logger.Error("Failed to update VM %s/%s with restored boot order: %v", namespace, name, err)
			return
		}

		logger.Info("Successfully restored boot order for VM %s/%s after VMI change", namespace, name)
	}
}

// handlePowerAfterImport checks if a VM's imports are all done and reissues a deferred power command
func (c *Client) handlePowerAfterImport(vm *kubevirtv1.VirtualMachine) {
	namespace := vm.GetNamespace()
	name := vm.GetName()
	vmLabels := vm.GetLabels()

	powerCommand := vmLabels[PowerAfterImportLabel]
	if powerCommand == "" {
		return
	}

	// Check if any importing labels remain
	for key := range vmLabels {
		if strings.HasPrefix(key, ImportingLabelPrefix) {
			logger.Info("VM %s/%s still has importing label %s, deferring power command %s", namespace, name, key, powerCommand)
			return
		}
	}

	logger.Info("All imports complete for VM %s/%s, executing deferred power command: %s", namespace, name, powerCommand)

	// Remove the power-after-import label first to avoid re-triggering
	patchJSON := fmt.Sprintf(`{"metadata":{"labels":{%q:null}}}`, PowerAfterImportLabel)
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, name, types.MergePatchType, []byte(patchJSON), metav1.PatchOptions{})
	if err != nil {
		logger.Error("Failed to remove power-after-import label from VM %s/%s: %v", namespace, name, err)
		return
	}

	if err := c.SetVMPowerState(namespace, name, powerCommand); err != nil {
		logger.Error("Failed to execute deferred power command %s for VM %s/%s: %v", powerCommand, namespace, name, err)
		return
	}

	logger.Info("Successfully executed deferred power command %s for VM %s/%s", powerCommand, namespace, name)
}

// reconcileExistingBootOnceVMs processes all labeled VMs on startup to clean stale state.
// Also reconciles VMs with power-after-import labels.
func (c *Client) reconcileExistingBootOnceVMs(namespace string) error {
	logger.Info("Reconciling existing labeled VMs in namespace %s", namespace)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	processed := make(map[string]bool)

	// Reconcile boot-once VMs
	bootOnceSelector := fmt.Sprintf("%s=enabled", BootOnceLabel)
	vmList, err := c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: bootOnceSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list boot-once VMs: %w", err)
	}

	logger.Info("Found %d VMs with boot-once label in namespace %s", len(vmList.Items), namespace)

	for _, item := range vmList.Items {
		vm, err := unstructuredToVM(&item)
		if err != nil {
			logger.Error("Failed to convert VM from list: %v", err)
			continue
		}
		processed[string(vm.GetUID())] = true
		c.handleVMUpdate(vm)
	}

	// Reconcile power-after-import VMs
	powerPendingList, err := c.dynamicClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: PowerAfterImportLabel,
	})
	if err != nil {
		return fmt.Errorf("failed to list power-after-import VMs: %w", err)
	}

	logger.Info("Found %d VMs with power-after-import label in namespace %s", len(powerPendingList.Items), namespace)

	for _, item := range powerPendingList.Items {
		if processed[string(item.GetUID())] {
			continue
		}
		vm, err := unstructuredToVM(&item)
		if err != nil {
			logger.Error("Failed to convert VM from list: %v", err)
			continue
		}
		c.handleVMUpdate(vm)
	}

	return nil
}

// =============================================================================
// IMPORT POD WATCHER
// =============================================================================

// StartImportPodWatcher starts watching for import helper pods across configured namespaces.
// If an existing watcher is running it is stopped first, making this
// safe to call on config reload.
func (c *Client) StartImportPodWatcher(ctx context.Context, namespaces []string) {
	c.stopImportPodWatcher()

	c.importWatcherCtx, c.importWatcherCancel = context.WithCancel(ctx)

	logger.Info("Starting import pod watcher for namespaces: %v", namespaces)

	for _, namespace := range namespaces {
		c.importWatcherWg.Add(1)
		go func(ns string) {
			defer c.importWatcherWg.Done()
			c.runImportPodWatcher(c.importWatcherCtx, ns)
		}(namespace)
	}
}

// stopImportPodWatcher stops the import pod watcher
func (c *Client) stopImportPodWatcher() {
	if c.importWatcherCancel != nil {
		logger.Info("Stopping import pod watcher")
		c.importWatcherCancel()
		c.importWatcherWg.Wait()
		c.importWatcherCancel = nil
		logger.Info("Import pod watcher stopped")
	}
}

// runImportPodWatcher watches import pods in a single namespace with automatic reconnection
func (c *Client) runImportPodWatcher(ctx context.Context, namespace string) {
	logger.Info("Starting import pod watcher for namespace: %s", namespace)

	// Reconcile any existing terminal pods on startup
	if err := c.reconcileExistingImportPods(namespace); err != nil {
		logger.Error("Failed to reconcile existing import pods in namespace %s: %v", namespace, err)
	}

	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			logger.Info("Import pod watcher for namespace %s shutting down", namespace)
			return
		default:
		}

		watcher, err := c.kubernetesClient.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector: ImportPodVMLabel,
		})
		if err != nil {
			logger.Error("Failed to create pod watch for namespace %s: %v, retrying in %v", namespace, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = min(backoff*2, maxBackoff)
				continue
			}
		}

		backoff = time.Second
		logger.Info("Import pod watch for namespace %s created", namespace)

		c.processImportPodEvents(ctx, namespace, watcher)

		logger.Info("Import pod watch for namespace %s ended, reconnecting...", namespace)
	}
}

// processImportPodEvents handles events from the import pod watch channel.
// A periodic sweep every reconcileInterval catches terminal pods whose
// events were missed.
func (c *Client) processImportPodEvents(ctx context.Context, namespace string, watcher watch.Interface) {
	defer watcher.Stop()

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.reconcileExistingImportPods(namespace); err != nil {
				logger.Error("Periodic import pod reconciliation failed for namespace %s: %v", namespace, err)
			}
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}

			if event.Type != watch.Modified {
				continue
			}

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				u, ok := event.Object.(*unstructured.Unstructured)
				if !ok {
					logger.Warning("Unexpected object type in import pod watch event: %T", event.Object)
					continue
				}
				p := &corev1.Pod{}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, p); err != nil {
					logger.Error("Failed to convert unstructured to Pod: %v", err)
					continue
				}
				pod = p
			}

			if isCDIManagedPod(pod) {
				continue
			}

			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				c.handleImportPodCompleted(pod)
			}
		}
	}
}

// handleImportPodCompleted processes a completed/failed import pod.
// It only removes the importing label when this pod is still the active
// import for the device. A stale pod from a previous insert cycle (whose
// PVC was already ejected and replaced) must not clear the label that now
// tracks the newer import.
func (c *Client) handleImportPodCompleted(pod *corev1.Pod) {
	// Re-fetch the pod from the API server to get fresh data
	freshPod, err := c.kubernetesClient.CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	if err != nil {
		// Pod disappeared, nothing to do
		return
	}
	pod = freshPod

	podLabels := pod.GetLabels()
	vmName := podLabels[ImportPodVMLabel]
	deviceName := podLabels[ImportPodVolumeLabel]
	vmNamespace := pod.Namespace

	if vmName == "" || deviceName == "" {
		logger.Warning("Import pod %s missing required labels (vm.redfish=%q, volume.vm.redfish=%q)", pod.Name, vmName, deviceName)
		return
	}

	if pod.Status.Phase == corev1.PodFailed {
		logs, logErr := c.kubernetesClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(context.Background())
		if logErr != nil {
			logger.Error("Import pod %s failed for VM %s/%s. Could not get logs: %v", pod.Name, vmNamespace, vmName, logErr)
		} else {
			logger.Error("Import pod %s failed for VM %s/%s. Logs: %s", pod.Name, vmNamespace, vmName, string(logs))
		}
	} else {
		logger.Info("Import pod %s succeeded for VM %s/%s volume %s", pod.Name, vmNamespace, vmName, deviceName)
	}

	// Delete the completed pod first so the RWO volume is fully released before
	// the importing label removal triggers a deferred power-on that would need
	// to mount the same PVC from a potentially different node.
	err = c.kubernetesClient.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	if err != nil {
		logger.Warning("Failed to delete completed import pod %s: %v", pod.Name, err)
	} else {
		logger.Info("Deleted completed import pod %s", pod.Name)
	}

	// Only clear the importing label if this pod is the one currently tracked.
	// After an eject+re-insert cycle the label points to the new pod, and a
	// stale pod completing must not clear it.
	vm, vmErr := c.GetVM(vmNamespace, vmName)
	if vmErr != nil {
		logger.Warning("Cannot verify importing label owner for pod %s: %v", pod.Name, vmErr)
		return
	}
	labelKey := ImportingLabelPrefix + deviceName
	currentPodName := vm.GetLabels()[labelKey]
	if currentPodName != pod.Name {
		logger.Info("Stale import pod %s completed (current label value is %q) — skipping label removal for VM %s/%s", pod.Name, currentPodName, vmNamespace, vmName)
		return
	}

	if err := c.removeImportingLabel(vmNamespace, vmName, deviceName); err != nil {
		logger.Error("Failed to remove importing label from VM %s/%s: %v", vmNamespace, vmName, err)
	}
}

// removeImportingLabel removes the importing.redfish/<deviceName> label from a VM
func (c *Client) removeImportingLabel(namespace, vmName, deviceName string) error {
	labelKey := ImportingLabelPrefix + deviceName
	// A null value in a merge patch removes the key
	patchJSON := fmt.Sprintf(`{"metadata":{"labels":{%q:null}}}`, labelKey)

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	_, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Patch(ctx, vmName, types.MergePatchType, []byte(patchJSON), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to remove importing label from VM %s/%s: %w", namespace, vmName, err)
	}
	logger.Info("Removed importing label %s from VM %s/%s", labelKey, namespace, vmName)
	return nil
}

// reconcileExistingImportPods handles import pods that reached terminal state while the watcher was not running
func (c *Client) reconcileExistingImportPods(namespace string) error {
	logger.Info("Reconciling existing import pods in namespace %s", namespace)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	podList, err := c.kubernetesClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: ImportPodVMLabel,
	})
	if err != nil {
		return fmt.Errorf("failed to list import pods: %w", err)
	}

	logger.Info("Found %d import pods in namespace %s", len(podList.Items), namespace)

	for i := range podList.Items {
		pod := &podList.Items[i]
		if isCDIManagedPod(pod) {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			c.handleImportPodCompleted(pod)
		}
	}

	return nil
}

// =============================================================================
// CDI PVC WATCHER
// =============================================================================

// StartCDIPVCWatcher starts watching labeled PVCs for CDI import completion
// StartCDIPVCWatcher starts watching for CDI PVC status changes.
// If an existing watcher is running it is stopped first, making this
// safe to call on config reload.
func (c *Client) StartCDIPVCWatcher(ctx context.Context, namespaces []string) {
	c.stopCDIPVCWatcher()

	c.pvcWatcherCtx, c.pvcWatcherCancel = context.WithCancel(ctx)

	logger.Info("Starting CDI PVC watcher for namespaces: %v", namespaces)

	for _, namespace := range namespaces {
		c.pvcWatcherWg.Add(1)
		go func(ns string) {
			defer c.pvcWatcherWg.Done()
			c.runCDIPVCWatcher(c.pvcWatcherCtx, ns)
		}(namespace)
	}
}

func (c *Client) stopCDIPVCWatcher() {
	if c.pvcWatcherCancel != nil {
		logger.Info("Stopping CDI PVC watcher")
		c.pvcWatcherCancel()
		c.pvcWatcherWg.Wait()
		c.pvcWatcherCancel = nil
		logger.Info("CDI PVC watcher stopped")
	}
}

func (c *Client) runCDIPVCWatcher(ctx context.Context, namespace string) {
	logger.Info("Starting CDI PVC watcher for namespace: %s", namespace)

	if err := c.reconcileExistingCDIPVCs(namespace); err != nil {
		logger.Error("Failed to reconcile existing CDI PVCs in namespace %s: %v", namespace, err)
	}

	backoff := time.Second
	maxBackoff := time.Minute

	for {
		select {
		case <-ctx.Done():
			logger.Info("CDI PVC watcher for namespace %s shutting down", namespace)
			return
		default:
		}

		watcher, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector: ImportPodVMLabel,
		})
		if err != nil {
			logger.Error("Failed to create PVC watch for namespace %s: %v, retrying in %v", namespace, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				backoff = min(backoff*2, maxBackoff)
				continue
			}
		}

		backoff = time.Second
		logger.Info("CDI PVC watch for namespace %s created", namespace)

		c.processCDIPVCEvents(ctx, namespace, watcher)

		logger.Info("CDI PVC watch for namespace %s ended, reconnecting...", namespace)
	}
}

func (c *Client) processCDIPVCEvents(ctx context.Context, namespace string, watcher watch.Interface) {
	defer watcher.Stop()

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.reconcileExistingCDIPVCs(namespace); err != nil {
				logger.Error("Periodic CDI PVC reconciliation failed for namespace %s: %v", namespace, err)
			}
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}

			if event.Type != watch.Modified {
				continue
			}

			pvc, ok := event.Object.(*corev1.PersistentVolumeClaim)
			if !ok {
				u, ok := event.Object.(*unstructured.Unstructured)
				if !ok {
					logger.Warning("Unexpected object type in CDI PVC watch event: %T", event.Object)
					continue
				}
				p := &corev1.PersistentVolumeClaim{}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, p); err != nil {
					logger.Error("Failed to convert unstructured to PVC: %v", err)
					continue
				}
				pvc = p
			}

			if pvc.Status.Phase == corev1.ClaimBound {
				c.handleCDIPVCBound(pvc)
			}
		}
	}
}

// handleCDIPVCBound is called when a CDI-managed PVC becomes Bound.
//
// CDI's volume populator creates a "prime" PVC that inherits our labels.
// The prime PVC becomes Bound early (before CDI finishes rebinding the PV
// to the original PVC), so we verify the PVC name matches the one stored
// in the importing label ("cdi-<pvcname>") to filter out prime PVCs.
//
// For volume-populator PVCs the Bound status on the original PVC is a
// reliable completion signal: the storage provisioner defers to CDI
// ("Assuming an external populator will provision the volume") and CDI
// only rebinds the PV after the import finishes. Note that CDI does NOT
// clear DataSourceRef after population — it stays on the PVC permanently.
func (c *Client) handleCDIPVCBound(pvc *corev1.PersistentVolumeClaim) {
	labels := pvc.GetLabels()
	vmName := labels[ImportPodVMLabel]
	deviceName := labels[ImportPodVolumeLabel]

	if vmName == "" || deviceName == "" {
		return
	}

	namespace := pvc.Namespace

	vm, err := c.GetVM(namespace, vmName)
	if err != nil {
		logger.Warning("CDI PVC %s bound but VM %s/%s not found: %v", pvc.Name, namespace, vmName, err)
		return
	}

	labelKey := ImportingLabelPrefix + deviceName
	labelVal := vm.GetLabels()[labelKey]
	if !strings.HasPrefix(labelVal, CDIImportPrefix) {
		return
	}

	// Verify this is our original PVC, not a CDI prime PVC. CDI copies
	// labels to the prime PVC it creates internally. The prime PVC becomes
	// Bound before CDI rebinds the PV to the original, so reacting to it
	// would prematurely clear the importing label.
	expectedPVCName := strings.TrimPrefix(labelVal, CDIImportPrefix)
	if pvc.Name != expectedPVCName {
		logger.Debug("Ignoring PVC %s — expected %s for VM %s/%s volume %s (likely a CDI prime PVC)", pvc.Name, expectedPVCName, namespace, vmName, deviceName)
		return
	}

	logger.Info("CDI PVC %s populated and bound for VM %s/%s volume %s, removing importing label", pvc.Name, namespace, vmName, deviceName)
	if err := c.removeImportingLabel(namespace, vmName, deviceName); err != nil {
		logger.Error("Failed to remove CDI importing label from VM %s/%s: %v", namespace, vmName, err)
	}
}

func (c *Client) reconcileExistingCDIPVCs(namespace string) error {
	logger.Info("Reconciling existing CDI PVCs in namespace %s", namespace)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	pvcList, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: ImportPodVMLabel,
	})
	if err != nil {
		return fmt.Errorf("failed to list CDI PVCs: %w", err)
	}

	logger.Info("Found %d labeled PVCs in namespace %s", len(pvcList.Items), namespace)

	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if pvc.Status.Phase == corev1.ClaimBound {
			c.handleCDIPVCBound(pvc)
		}
	}

	return nil
}
