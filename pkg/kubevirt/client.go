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
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mime/multipart"
	"reflect"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// VMSelectorConfig defines how to select VMs for a chassis
type VMSelectorConfig struct {
	Labels map[string]string `json:"labels,omitempty"`
	Names  []string          `json:"names,omitempty"`
}

// Client represents a KubeVirt client for interacting with KubeVirt resources
type Client struct {
	kubernetesClient *kubernetes.Clientset
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
	if c.httpClient != nil && c.httpClient.Transport != nil {
		if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
			logger.Debug("Closed idle connections in HTTP client")
		}
	}
	return nil
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
func (c *Client) GetVM(namespace, name string) (*unstructured.Unstructured, error) {
	start := time.Now()
	defer func() {
		c.trackOperation("GetVM", time.Since(start))
	}()

	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return nil, fmt.Errorf("dynamic client is not initialized")
	}

	var vm *unstructured.Unstructured

	err := c.retryWithBackoff(fmt.Sprintf("GetVM %s/%s", namespace, name), func() error {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()

		gvr := schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}

		var getErr error
		vm, getErr = c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		return getErr
	})

	if err != nil {
		logger.Error("Failed to get VM %s/%s: %v", namespace, name, err)
		return nil, fmt.Errorf("failed to get VM: %w", err)
	}

	return vm, nil
}

// GetVMPowerState gets the power state of a VirtualMachine
func (c *Client) GetVMPowerState(namespace, name string) (string, error) {
	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return "Unknown", fmt.Errorf("dynamic client is not initialized")
	}

	// Fetch the VM resource
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	vm, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return "Unknown", fmt.Errorf("failed to get VM %s: %w", name, err)
	}

	// Check for force-stop annotation
	annotations, found, _ := unstructured.NestedStringMap(vm.Object, "metadata", "annotations")
	forceStop := found && annotations["kubevirt.io/force-stop"] == "true"

	// Use printableStatus if available
	printableStatus, found, _ := unstructured.NestedString(vm.Object, "status", "printableStatus")
	if found {
		switch printableStatus {
		case "Running":
			return "On", nil
		case "Stopped":
			return "Off", nil
		case "Stopping", "Terminating":
			if forceStop {
				return "ForceOffInProgress", nil
			}
			return "ShuttingDown", nil
		case "Starting":
			return "PoweringOn", nil
		}
	}

	// Check for PodTerminating condition
	conditions, found, _ := unstructured.NestedSlice(vm.Object, "status", "conditions")
	if found {
		for _, cond := range conditions {
			if condMap, ok := cond.(map[string]interface{}); ok {
				typeStr, _ := condMap["type"].(string)
				if typeStr == "PodTerminating" {
					return "ShuttingDown", nil
				}
			}
		}
	}

	// Check for pending state change requests
	stateChangeRequests, found, _ := unstructured.NestedSlice(vm.Object, "status", "stateChangeRequests")
	if found && len(stateChangeRequests) > 0 {
		return "Transitioning", nil
	}

	// Fallback to VMI phase logic
	gvrVMI := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}
	vmi, err := c.dynamicClient.Resource(gvrVMI).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		// If VMI doesn't exist, VM is stopped
		return "Off", nil
	}

	// Check for Paused condition in VMI status
	vmiConditions, found, _ := unstructured.NestedSlice(vmi.Object, "status", "conditions")
	if found {
		for _, cond := range vmiConditions {
			if condMap, ok := cond.(map[string]interface{}); ok {
				typeStr, _ := condMap["type"].(string)
				statusStr, _ := condMap["status"].(string)
				if typeStr == "Paused" && statusStr == "True" {
					return "Paused", nil
				}
			}
		}
	}

	phase, found, _ := unstructured.NestedString(vmi.Object, "status", "phase")
	if found {
		switch phase {
		case "Running":
			return "On", nil
		case "Succeeded":
			return "On", nil
		case "Failed":
			return "Off", nil
		case "Pending":
			return "PoweringOn", nil
		}
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

		// Start the VM by setting runStrategy to Always
		patch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Always"}]`)

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
			"method":         "patch_runstrategy_annotation_and_grace_period",
			"target_state":   "Halted",
			"force_stop":     true,
			"grace_period":   0,
		})

		// Force stop the VM using runStrategy, force-stop annotation, and zero grace period
		// This mirrors the behavior of: virtctl stop --grace-period 0 --force <vm name>
		patch := []byte(`[
			 {"op": "replace", "path": "/spec/runStrategy", "value": "Halted"},
			 {"op": "add", "path": "/metadata/annotations/kubevirt.io~1force-stop", "value": "true"},
			 {"op": "replace", "path": "/spec/terminationGracePeriodSeconds", "value": 0}
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

		// Graceful stop the VM using runStrategy
		patch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Halted"}]`)

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
			"method":         "force_stop_then_start",
		})

		// Force restart the VM by force stopping (with zero grace period) and starting
		stopPatch := []byte(`[
			 {"op": "replace", "path": "/spec/runStrategy", "value": "Halted"},
			 {"op": "add", "path": "/metadata/annotations/kubevirt.io~1force-stop", "value": "true"},
			 {"op": "replace", "path": "/spec/terminationGracePeriodSeconds", "value": 0}
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

		startPatch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Always"}]`)

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

		// Graceful restart the VM by stopping and starting
		stopPatch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Halted"}]`)

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

		startPatch := []byte(`[{"op": "replace", "path": "/spec/runStrategy", "value": "Always"}]`)

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

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}

	vmi, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get VMI %s: %w", name, err)
	}

	var interfaces []string
	networkInterfaces, found, err := unstructured.NestedSlice(vmi.Object, "status", "interfaces")
	if err != nil || !found {
		return interfaces, nil
	}

	for _, iface := range networkInterfaces {
		if ifaceMap, ok := iface.(map[string]interface{}); ok {
			if name, found := ifaceMap["name"].(string); found && name != "" {
				interfaces = append(interfaces, name)
			}
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
	volumes, found, err := unstructured.NestedSlice(vm.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found {
		return storage, nil
	}

	for _, volume := range volumes {
		if volumeMap, ok := volume.(map[string]interface{}); ok {
			if name, found := volumeMap["name"].(string); found && name != "" {
				storage = append(storage, name)
			}
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
		"bootSourceOverrideMode":    "UEFI",
	}

	// Check for firmware configuration
	firmware, found, _ := unstructured.NestedMap(vm.Object, "spec", "template", "spec", "domain", "firmware")
	if found {
		if bootloader, found := firmware["bootloader"].(map[string]interface{}); found {
			if _, found := bootloader["efi"]; found {
				bootOptions["bootSourceOverrideMode"] = "UEFI"
			}
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

// SetVMBootOptions sets boot options of a VirtualMachine
func (c *Client) SetVMBootOptions(namespace, name string, options map[string]interface{}) error {
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

	// Update VM
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, vmCopy, metav1.UpdateOptions{})
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
	devices, found, err := unstructured.NestedMap(vm.Object, "spec", "template", "spec", "domain", "devices")
	if err == nil && found {
		if disks, found := devices["disks"].([]interface{}); found {
			for _, disk := range disks {
				if diskMap, ok := disk.(map[string]interface{}); ok {
					if diskName, found := diskMap["name"].(string); found {
						// Check if this is a CD-ROM device
						if cdrom, found := diskMap["cdrom"]; found && cdrom != nil {
							// Check if media is inserted (has volumeName)
							if volumeName, found := diskMap["volumeName"].(string); found && volumeName != "" {
								logger.Debug("Found CD-ROM device %s with inserted media (volume: %s) for VM %s/%s", diskName, volumeName, namespace, name)
							} else {
								logger.Debug("Found empty CD-ROM device %s for VM %s/%s", diskName, namespace, name)
							}
							mediaDevices = append(mediaDevices, diskName)
						}
					}
				}
			}
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

	// Step 1: Find the CD-ROM device in the VM spec
	var volumeRef string
	devices, found, err := unstructured.NestedMap(vm.Object, "spec", "template", "spec", "domain", "devices")
	if err != nil || !found {
		logger.Debug("No devices found in VM %s/%s", namespace, name)
		return false, nil
	}

	if disks, found := devices["disks"].([]interface{}); found {
		for _, disk := range disks {
			if diskMap, ok := disk.(map[string]interface{}); ok {
				if diskName, found := diskMap["name"].(string); found && diskName == mediaID {
					// Check if this is a CD-ROM device
					if cdrom, found := diskMap["cdrom"]; found && cdrom != nil {
						if vol, ok := diskMap["volumeName"].(string); ok && vol != "" {
							volumeRef = vol
						} else {
							volumeRef = diskName
						}
						break
					}
				}
			}
		}
	}

	if volumeRef == "" {
		logger.Debug("CD-ROM device %s not found in VM %s/%s", mediaID, namespace, name)
		return false, nil
	}

	// Step 2: Find the corresponding volume for the CD-ROM
	var pvcName string
	volumes, found, err := unstructured.NestedSlice(vm.Object, "spec", "template", "spec", "volumes")
	if err != nil || !found {
		logger.Debug("No volumes found in VM %s/%s", namespace, name)
		return false, nil
	}

	for _, volume := range volumes {
		if volumeMap, ok := volume.(map[string]interface{}); ok {
			if volumeName, found := volumeMap["name"].(string); found && volumeName == volumeRef {
				// Step 3: Get PVC name from persistentVolumeClaim.claimName
				if pvc, found := volumeMap["persistentVolumeClaim"].(map[string]interface{}); found {
					if claimName, found := pvc["claimName"].(string); found {
						pvcName = claimName
						break
					}
				}
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

// InsertVirtualMedia inserts virtual media into a VirtualMachine
func (c *Client) InsertVirtualMedia(namespace, name, mediaID, imageURL string) error {
	logger.Info("Inserting virtual media %s with image %s for VM %s/%s", mediaID, imageURL, namespace, name)
	logger.Debug("DEBUG: InsertVirtualMedia called - namespace=%s, name=%s, mediaID=%s, imageURL=%s", namespace, name, mediaID, imageURL)

	// Perform the actual insertion work
	logger.Debug("DEBUG: Calling insertVirtualMediaAsync for VM %s/%s", namespace, name)
	if err := c.insertVirtualMediaAsync(namespace, name, mediaID, imageURL); err != nil {
		logger.Error("Failed to insert virtual media %s for VM %s/%s: %v", mediaID, namespace, name, err)
		logger.Debug("DEBUG: insertVirtualMediaAsync failed for VM %s/%s: %v", namespace, name, err)
		return err
	}

	logger.Info("Successfully completed virtual media insertion %s for VM %s/%s", mediaID, namespace, name)
	logger.Debug("DEBUG: Virtual media insertion completed successfully for VM %s/%s", namespace, name)
	return nil
}

// insertVirtualMediaAsync performs the actual virtual media insertion work
func (c *Client) insertVirtualMediaAsync(namespace, name, mediaID, imageURL string) error {
	logger.Debug("DEBUG: insertVirtualMediaAsync called - namespace=%s, name=%s, mediaID=%s, imageURL=%s", namespace, name, mediaID, imageURL)

	// Get DataVolume configuration first to determine timeouts
	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage := c.getDataVolumeConfig()
	logger.Info("Using DataVolume config: storageSize=%s, allowInsecureTLS=%v, storageClass=%s, vmUpdateTimeout=%s, isoDownloadTimeout=%s, helperImage=%s", storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage)
	logger.Debug("DEBUG: DataVolume config - storageSize=%s, allowInsecureTLS=%v, storageClass=%s, vmUpdateTimeout=%s, isoDownloadTimeout=%s, helperImage=%s", storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout, helperImage)

	// Parse timeout for VM update
	vmUpdateDuration, err := time.ParseDuration(vmUpdateTimeout)
	if err != nil {
		logger.Warning("Invalid vm_update_timeout %s, using default 30s: %v", vmUpdateTimeout, err)
		vmUpdateDuration = 30 * time.Second
	}
	logger.Debug("DEBUG: Using VM update timeout: %v", vmUpdateDuration)

	// Use VM update timeout for this operation
	ctx, cancel := context.WithTimeout(context.Background(), vmUpdateDuration)
	defer cancel()

	logger.Info("Inserting virtual media %s with image %s for VM %s/%s", mediaID, imageURL, namespace, name)
	logger.Debug("DEBUG: Starting virtual media insertion process for VM %s/%s", namespace, name)

	// Parse URL to determine scheme
	u, parseErr := url.Parse(imageURL)
	if parseErr != nil {
		logger.Debug("DEBUG: Failed to parse URL %s: %v", imageURL, parseErr)
		return fmt.Errorf("failed to parse URL %s: %w", imageURL, parseErr)
	}
	logger.Debug("DEBUG: Parsed URL - scheme=%s, host=%s", u.Scheme, u.Host)

	// Generate unique PVC name with timestamp and random suffix to avoid conflicts
	dataVolumeName := c.generateUniquePVCName(name)
	logger.Debug("DEBUG: Generated unique dataVolumeName=%s", dataVolumeName)

	// First, create the CD-ROM device in the VM spec
	// Use lowercase device name for KubeVirt compatibility
	deviceName := "cdrom0"
	logger.Info("Creating CD-ROM device %s in VM spec first", deviceName)
	logger.Debug("DEBUG: Creating CD-ROM device %s in VM spec", deviceName)

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		logger.Debug("DEBUG: VM update attempt %d/%d", attempt, maxRetries)

		vm, err := c.GetVM(namespace, name)
		if err != nil {
			logger.Debug("DEBUG: Failed to get VM %s/%s: %v", namespace, name, err)
			return fmt.Errorf("failed to get VM %s: %w", name, err)
		}
		logger.Debug("DEBUG: Successfully retrieved VM %s/%s", namespace, name)

		vmCopy := vm.DeepCopy()

		devices, found, err := unstructured.NestedMap(vmCopy.Object, "spec", "template", "spec", "domain", "devices")
		if err != nil || !found {
			logger.Debug("DEBUG: No devices found in VM, creating new devices map")
			devices = map[string]interface{}{}
		}

		var disks []interface{}
		if existingDisks, found := devices["disks"].([]interface{}); found {
			disks = existingDisks
			logger.Debug("DEBUG: Found %d existing disks in VM", len(disks))
		} else {
			logger.Debug("DEBUG: No existing disks found, creating new disks slice")
		}

		var diskExists bool
		var existingDiskIndex int
		for i, disk := range disks {
			if diskMap, ok := disk.(map[string]interface{}); ok {
				if diskName, found := diskMap["name"].(string); found && diskName == deviceName {
					diskExists = true
					existingDiskIndex = i
					logger.Debug("DEBUG: Found existing disk %s at index %d", deviceName, i)
					break
				}
			}
		}

		if diskExists {
			if diskMap, ok := disks[existingDiskIndex].(map[string]interface{}); ok {
				// Ensure the CD-ROM device is connected to the PVC
				diskMap["volumeName"] = deviceName
				logger.Info("Updated existing CD-ROM device %s to connect to PVC", deviceName)
				logger.Debug("DEBUG: Updated existing CD-ROM device %s, connected to volume %s", deviceName, deviceName)
			}
		} else {
			newDisk := map[string]interface{}{
				"name":       deviceName,
				"volumeName": deviceName, // Connect to the volume
				"cdrom": map[string]interface{}{
					"bus": "sata",
				},
			}
			disks = append(disks, newDisk)
			logger.Info("Added new CD-ROM device %s connected to PVC", deviceName)
			logger.Debug("DEBUG: Added new CD-ROM device %s connected to volume %s", deviceName, deviceName)
		}
		devices["disks"] = disks

		var volumes []interface{}
		if existingVolumes, found, err := unstructured.NestedSlice(vmCopy.Object, "spec", "template", "spec", "volumes"); err == nil && found {
			volumes = existingVolumes
			logger.Debug("DEBUG: Found %d existing volumes in VM", len(volumes))
		} else {
			logger.Debug("DEBUG: No existing volumes found, creating new volumes slice")
		}

		var volumeExists bool
		for _, volume := range volumes {
			if volumeMap, ok := volume.(map[string]interface{}); ok {
				if volumeName, found := volumeMap["name"].(string); found && volumeName == deviceName {
					volumeExists = true
					logger.Debug("DEBUG: Found existing volume %s", deviceName)
					break
				}
			}
		}

		if !volumeExists {
			newVolume := map[string]interface{}{
				"name": deviceName,
				"persistentVolumeClaim": map[string]interface{}{
					"claimName": dataVolumeName,
				},
			}
			volumes = append(volumes, newVolume)
			logger.Info("Added new volume reference %s for PVC %s", deviceName, dataVolumeName)
			logger.Debug("DEBUG: Added new volume reference %s for PVC %s", deviceName, dataVolumeName)
		} else {
			logger.Info("Volume reference %s already exists", deviceName)
			logger.Debug("DEBUG: Volume reference %s already exists", deviceName)
		}

		err = unstructured.SetNestedSlice(vmCopy.Object, volumes, "spec", "template", "spec", "volumes")
		if err != nil {
			logger.Debug("DEBUG: Failed to update VM volumes: %v", err)
			return fmt.Errorf("failed to update VM volumes: %w", err)
		}

		err = unstructured.SetNestedMap(vmCopy.Object, devices, "spec", "template", "spec", "domain", "devices")
		if err != nil {
			logger.Debug("DEBUG: Failed to update VM devices: %v", err)
			return fmt.Errorf("failed to update VM devices: %w", err)
		}

		gvrVM := schema.GroupVersionResource{
			Group:    "kubevirt.io",
			Version:  "v1",
			Resource: "virtualmachines",
		}

		logger.Debug("DEBUG: Updating VM %s/%s with new CD-ROM device", namespace, name)
		_, err = c.dynamicClient.Resource(gvrVM).Namespace(namespace).Update(ctx, vmCopy, metav1.UpdateOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "the object has been modified") && attempt < maxRetries {
				logger.Info("Concurrent modification detected, retrying VM update (attempt %d/%d)", attempt, maxRetries)
				logger.Debug("DEBUG: Concurrent modification detected, retrying VM update (attempt %d/%d)", attempt, maxRetries)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			logger.Debug("DEBUG: Failed to update VM %s/%s: %v", namespace, name, err)
			return fmt.Errorf("failed to update VM: %w", err)
		}

		logger.Debug("DEBUG: Successfully updated VM %s/%s on attempt %d", namespace, name, attempt)
		break
	}

	logger.Info("Successfully created CD-ROM device %s in VM spec", deviceName)
	logger.Debug("DEBUG: Successfully created CD-ROM device %s in VM spec for VM %s/%s", deviceName, namespace, name)

	// Now create the PVC and import the ISO
	if allowInsecureTLS && u.Scheme == "https" {
		logger.Info("Using helper pod for ISO import due to allowInsecureTLS=true and HTTPS URL")
		logger.Debug("DEBUG: Using helper pod approach for HTTPS URL with allowInsecureTLS=true")

		// Create blank PVC with Block volume mode for ISO files
		volumeMode := corev1.PersistentVolumeBlock
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dataVolumeName,
				Namespace: namespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				VolumeMode: &volumeMode,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						"storage": resourceMustParse(storageSize),
					},
				},
			},
		}
		if storageClass != "" {
			pvc.Spec.StorageClassName = &storageClass
			logger.Info("Set storage class %s for PVC %s", storageClass, dataVolumeName)
			logger.Debug("DEBUG: Set storage class %s for PVC %s", storageClass, dataVolumeName)
		}

		logger.Debug("DEBUG: Creating PVC %s in namespace %s", dataVolumeName, namespace)

		// Check if PVC already exists and validate its state
		existingPVC, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, dataVolumeName, metav1.GetOptions{})
		if err == nil {
			// PVC exists, check its state
			if c.isPVCUsable(existingPVC) {
				logger.Info("PVC %s already exists and is usable, reusing it", dataVolumeName)
				logger.Debug("DEBUG: PVC %s already exists and is usable, reusing it", dataVolumeName)
			} else {
				logger.Info("PVC %s exists but is not usable (status: %s), deleting and recreating", dataVolumeName, existingPVC.Status.Phase)
				logger.Debug("DEBUG: PVC %s exists but is not usable, deleting and recreating", dataVolumeName)
				// Delete the unusable PVC
				err = c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, dataVolumeName, metav1.DeleteOptions{})
				if err != nil {
					logger.Debug("DEBUG: Failed to delete unusable PVC %s: %v", dataVolumeName, err)
					return fmt.Errorf("failed to delete unusable PVC: %w", err)
				}
				// Wait a moment for deletion to complete
				time.Sleep(2 * time.Second)
			}
		}

		// Create the PVC with retry logic
		err = c.retryWithBackoff(fmt.Sprintf("create PVC %s", dataVolumeName), func() error {
			var createErr error
			_, createErr = c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
			if createErr != nil {
				if strings.Contains(createErr.Error(), "already exists") {
					logger.Debug("DEBUG: PVC %s already exists (race condition), will use existing PVC", dataVolumeName)
					// PVC already exists, we can use it
					return nil // Success - we'll use the existing PVC
				}
				logger.Debug("DEBUG: Failed to create PVC %s: %v", dataVolumeName, createErr)
				return fmt.Errorf("failed to create PVC: %w", createErr)
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to create PVC after retries: %w", err)
		}

		logger.Info("Created new PVC %s for virtual media", dataVolumeName)
		logger.Debug("DEBUG: Successfully created new PVC %s for virtual media", dataVolumeName)

		// Use helper pod to copy ISO to PVC
		logger.Debug("DEBUG: Calling copyISOToPVC for PVC %s", dataVolumeName)
		if err := c.copyISOToPVC(namespace, dataVolumeName, imageURL, isoDownloadTimeout); err != nil {
			logger.Debug("DEBUG: copyISOToPVC failed for PVC %s: %v", dataVolumeName, err)
			return fmt.Errorf("failed to copy ISO to PVC: %w", err)
		}
		logger.Info("Helper pod completed ISO import for PVC %s", dataVolumeName)
		logger.Debug("DEBUG: Helper pod successfully completed ISO import for PVC %s", dataVolumeName)
	} else {
		logger.Debug("DEBUG: Using CDI HTTP import approach for URL scheme %s", u.Scheme)
		// CDI HTTP import for HTTP or valid HTTPS
		logger.Info("Using CDI HTTP import for ISO")
		volumeImportSourceName := fmt.Sprintf("%s-populator", dataVolumeName)
		logger.Debug("DEBUG: Generated volumeImportSourceName=%s", volumeImportSourceName)
		volumeImportSource := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cdi.kubevirt.io/v1beta1",
				"kind":       "VolumeImportSource",
				"metadata": map[string]interface{}{
					"name":      volumeImportSourceName,
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"source": map[string]interface{}{
						"http": map[string]interface{}{
							"url": imageURL,
						},
					},
				},
			},
		}
		gvrVIS := schema.GroupVersionResource{
			Group:    "cdi.kubevirt.io",
			Version:  "v1beta1",
			Resource: "volumeimportsources",
		}
		_, err := c.dynamicClient.Resource(gvrVIS).Namespace(namespace).Create(ctx, volumeImportSource, metav1.CreateOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				logger.Info("VolumeImportSource %s already exists, reusing it", volumeImportSourceName)
			} else {
				return fmt.Errorf("failed to create VolumeImportSource: %w", err)
			}
		} else {
			logger.Info("Created new VolumeImportSource %s for virtual media", volumeImportSourceName)
		}
		// Create PVC that references the VolumeImportSource with Block volume mode for ISO files
		volumeMode := corev1.PersistentVolumeBlock
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dataVolumeName,
				Namespace: namespace,
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
				VolumeMode: &volumeMode,
				Resources: corev1.ResourceRequirements{
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
		_, err = c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				logger.Info("PVC %s already exists, reusing it", dataVolumeName)
			} else {
				return fmt.Errorf("failed to create PVC: %w", err)
			}
		} else {
			logger.Info("Created new PVC %s for virtual media", dataVolumeName)
		}
		logger.Info("VolumeImportSource and PVC created for HTTP import")
		logger.Info("CDI will handle the ISO download and import")
	}

	logger.Info("Successfully inserted virtual media %s for VM %s/%s", mediaID, namespace, name)
	return nil
}

// uploadISOToDataVolume uploads an ISO file to a DataVolume using the CDI upload proxy
func (c *Client) uploadISOToDataVolume(namespace, dataVolumeName, filePath string) error {
	logger.Info("Uploading ISO file %s to DataVolume %s", filePath, dataVolumeName)

	// Get the upload proxy URL
	uploadProxyURL, err := c.getUploadProxyURL()
	if err != nil {
		return fmt.Errorf("failed to get upload proxy URL: %w", err)
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	// Create the upload URL
	uploadURL := fmt.Sprintf("%s/v1alpha1/upload", uploadProxyURL)

	// Create HTTP client
	client := &http.Client{
		Timeout: 30 * time.Minute, // Long timeout for large uploads
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // Allow self-signed certificates for internal CDI communication
			},
		},
	}

	// Create a pipe for streaming the multipart form
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Start writing the multipart form in a goroutine
	go func() {
		defer pw.Close()
		defer writer.Close()

		// Add the file
		part, err := writer.CreateFormFile("file", filepath.Base(filePath))
		if err != nil {
			logger.Error("Failed to create form file: %v", err)
			return
		}

		// Stream the file content
		if _, err := io.Copy(part, file); err != nil {
			logger.Error("Failed to copy file content: %v", err)
			return
		}

		// Add the DataVolume name
		if err := writer.WriteField("token", dataVolumeName); err != nil {
			logger.Error("Failed to write token field: %v", err)
			return
		}
	}()

	// Create the request with streaming body
	req, err := http.NewRequest("POST", uploadURL, pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	logger.Info("Successfully uploaded ISO to DataVolume %s", dataVolumeName)
	return nil
}

// copyISOToPVC copies an ISO file from the kubevirt-redfish pod to a PVC using a simpler approach
func (c *Client) copyISOToPVC(namespace, dataVolumeName, imageURL, isoDownloadTimeout string) error {
	logger.Info("Copying ISO from %s to PVC for DataVolume %s", imageURL, dataVolumeName)
	logger.Debug("DEBUG: Starting copyISOToPVC - namespace=%s, dataVolumeName=%s, imageURL=%s", namespace, dataVolumeName, imageURL)

	// Get DataVolume configuration to determine helper image and timeouts
	_, _, _, _, configISODownloadTimeout, helperImage := c.getDataVolumeConfig()

	// Use provided timeout if not empty, otherwise use config timeout
	if isoDownloadTimeout == "" {
		isoDownloadTimeout = configISODownloadTimeout
	}

	pvcName := dataVolumeName
	isoFileName := filepath.Base(imageURL)
	logger.Debug("DEBUG: Extracted ISO filename=%s from URL", isoFileName)
	logger.Debug("DEBUG: Using helper image=%s for ISO copy operation", helperImage)

	// Create a simple helper pod that will copy the file to block device
	// Use timestamp to make pod name unique and avoid race conditions
	timestamp := time.Now().Unix()
	helperPodName := fmt.Sprintf("copy-iso-%s-%d", dataVolumeName, timestamp)
	logger.Debug("DEBUG: Generated unique helper pod name=%s", helperPodName)

	// Check if helper pod already exists before creating
	existingPod, err := c.kubernetesClient.CoreV1().Pods(namespace).Get(context.Background(), helperPodName, metav1.GetOptions{})
	if err == nil {
		logger.Debug("DEBUG: Helper pod %s already exists with status: %s", helperPodName, existingPod.Status.Phase)
		if existingPod.Status.Phase == corev1.PodSucceeded {
			logger.Info("Helper pod %s already completed successfully, reusing result", helperPodName)
			return nil
		} else if existingPod.Status.Phase == corev1.PodFailed {
			logger.Debug("DEBUG: Helper pod %s exists but failed, will delete and recreate", helperPodName)
			// Delete the failed pod
			err = c.kubernetesClient.CoreV1().Pods(namespace).Delete(context.Background(), helperPodName, metav1.DeleteOptions{})
			if err != nil {
				logger.Debug("DEBUG: Failed to delete existing failed pod %s: %v", helperPodName, err)
			} else {
				logger.Debug("DEBUG: Successfully deleted existing failed pod %s", helperPodName)
			}
		} else {
			logger.Debug("DEBUG: Helper pod %s exists with status %s, will wait for completion", helperPodName, existingPod.Status.Phase)
		}
	} else {
		logger.Debug("DEBUG: Helper pod %s does not exist, will create new one", helperPodName)
	}

	helperPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helperPodName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "copy",
					Image:   helperImage,
					Command: []string{"sh", "-c", fmt.Sprintf("curl --fail --show-error --insecure --connect-timeout 30 --max-time 1800 --location -o /tmp/%s %s && [ -s /tmp/%s ] && dd if=/tmp/%s of=/dev/block bs=1M conv=fsync", isoFileName, imageURL, isoFileName, isoFileName)},
					VolumeDevices: []corev1.VolumeDevice{
						{Name: "iso-volume", DevicePath: "/dev/block"},
					},
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
				},
			},
			Volumes: []corev1.Volume{
				{Name: "iso-volume", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName}}},
			},
		},
	}

	logger.Debug("DEBUG: Creating helper pod %s with PVC %s", helperPodName, pvcName)
	// Create the helper pod with retry logic for race conditions
	var createdPod *corev1.Pod
	err = c.retryWithBackoff(fmt.Sprintf("create helper pod %s", helperPodName), func() error {
		var createErr error
		createdPod, createErr = c.kubernetesClient.CoreV1().Pods(namespace).Create(context.Background(), helperPod, metav1.CreateOptions{})
		if createErr != nil {
			// Check if this is an "already exists" error (race condition)
			if strings.Contains(createErr.Error(), "already exists") {
				logger.Debug("DEBUG: Helper pod %s already exists (race condition), will use existing pod", helperPodName)
				// Get the existing pod
				existingPod, getErr := c.kubernetesClient.CoreV1().Pods(namespace).Get(context.Background(), helperPodName, metav1.GetOptions{})
				if getErr != nil {
					logger.Debug("DEBUG: Failed to get existing helper pod %s: %v", helperPodName, getErr)
					return fmt.Errorf("failed to get existing helper pod: %w", getErr)
				}
				createdPod = existingPod
				return nil // Success - we'll use the existing pod
			}
			logger.Debug("DEBUG: Failed to create helper pod %s: %v", helperPodName, createErr)
			return fmt.Errorf("failed to create helper pod: %w", createErr)
		}
		return nil
	})

	if err != nil {
		logger.Debug("DEBUG: All attempts to create helper pod %s failed: %v", helperPodName, err)
		return fmt.Errorf("failed to create helper pod after retries: %w", err)
	}

	logger.Debug("DEBUG: Successfully created/retrieved helper pod %s with UID %s", helperPodName, createdPod.UID)

	defer func() {
		logger.Debug("DEBUG: Cleaning up helper pod %s", helperPodName)
		err := c.kubernetesClient.CoreV1().Pods(namespace).Delete(context.Background(), helperPodName, metav1.DeleteOptions{})
		if err != nil {
			logger.Debug("DEBUG: Failed to delete helper pod %s during cleanup: %v", helperPodName, err)
		} else {
			logger.Debug("DEBUG: Successfully deleted helper pod %s during cleanup", helperPodName)
		}
	}()

	// Parse timeout for ISO download
	isoDownloadDuration, err := time.ParseDuration(isoDownloadTimeout)
	if err != nil {
		logger.Warning("Invalid iso_download_timeout %s, using default 30m: %v", isoDownloadTimeout, err)
		isoDownloadDuration = 30 * time.Minute
	}
	logger.Debug("DEBUG: Using timeout duration: %v", isoDownloadDuration)

	// Wait for the pod to complete
	timeoutSeconds := int(isoDownloadDuration.Seconds())
	logger.Debug("DEBUG: Starting pod monitoring loop for %d seconds", timeoutSeconds)

	for i := 0; i < timeoutSeconds; i++ {
		if i%10 == 0 { // Log every 10 seconds
			logger.Debug("DEBUG: Pod monitoring iteration %d/%d", i, timeoutSeconds)
		}

		pod, err := c.kubernetesClient.CoreV1().Pods(namespace).Get(context.Background(), helperPodName, metav1.GetOptions{})
		if err != nil {
			logger.Debug("DEBUG: Failed to get pod %s status (iteration %d): %v", helperPodName, i, err)
			time.Sleep(1 * time.Second)
			continue
		}

		logger.Debug("DEBUG: Pod %s status: Phase=%s, PodIP=%s", helperPodName, pod.Status.Phase, pod.Status.PodIP)

		if pod.Status.Phase == corev1.PodSucceeded {
			logger.Info("Helper pod %s completed successfully", helperPodName)
			logger.Debug("DEBUG: Helper pod %s succeeded after %d seconds", helperPodName, i)
			return nil
		}
		if pod.Status.Phase == corev1.PodFailed {
			logger.Debug("DEBUG: Helper pod %s failed after %d seconds", helperPodName, i)
			// Try to get pod logs for debugging, but don't fail if we can't
			logs, logErr := c.kubernetesClient.CoreV1().Pods(namespace).GetLogs(helperPodName, &corev1.PodLogOptions{}).DoRaw(context.Background())
			if logErr != nil {
				logger.Error("Helper pod %s failed. Could not get logs: %v", helperPodName, logErr)
				logger.Debug("DEBUG: Failed to retrieve logs for pod %s: %v", helperPodName, logErr)
			} else {
				logger.Error("Helper pod %s failed. Logs: %s", helperPodName, string(logs))
				logger.Debug("DEBUG: Helper pod %s failed with logs: %s", helperPodName, string(logs))
			}
			return fmt.Errorf("helper pod %s failed", helperPodName)
		}

		// Log container status if available
		for _, containerStatus := range pod.Status.ContainerStatuses {
			logger.Debug("DEBUG: Container %s status: Ready=%v, State=%v", containerStatus.Name, containerStatus.Ready, containerStatus.State)
		}

		time.Sleep(1 * time.Second)
	}

	logger.Debug("DEBUG: Helper pod %s did not complete within %d seconds", helperPodName, timeoutSeconds)
	return fmt.Errorf("helper pod %s did not complete in time", helperPodName)
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

// getUploadProxyURL gets the CDI upload proxy URL using configuration-driven service discovery
func (c *Client) getUploadProxyURL() (string, error) {
	// Check if kubernetesClient is initialized
	if c.kubernetesClient == nil {
		return "", fmt.Errorf("kubernetes client is not initialized")
	}

	correlationID := logger.GetCorrelationID(context.Background())

	// Get CDI namespaces from appConfig using reflection to avoid import cycle
	serviceName := "cdi-uploadproxy"
	namespaces := []string{"openshift-cnv", "cdi", "kubevirt-cdi"} // Default fallback

	// Try to extract CDI namespaces from appConfig if available
	if c.appConfig != nil {
		// Use reflection to safely extract CDI configuration without import cycle
		if reflect.TypeOf(c.appConfig).String() == "*config.Config" {
			configValue := reflect.ValueOf(c.appConfig).Elem()
			if cdiField := configValue.FieldByName("CDI"); cdiField.IsValid() {
				if uploadProxyField := cdiField.FieldByName("UploadProxy"); uploadProxyField.IsValid() {
					if namespacesField := uploadProxyField.FieldByName("Namespaces"); namespacesField.IsValid() && namespacesField.Kind() == reflect.Slice {
						if namespacesField.Len() > 0 {
							extractedNamespaces := make([]string, namespacesField.Len())
							for i := 0; i < namespacesField.Len(); i++ {
								extractedNamespaces[i] = namespacesField.Index(i).String()
							}
							namespaces = extractedNamespaces
						}
					}
					if serviceNameField := uploadProxyField.FieldByName("ServiceName"); serviceNameField.IsValid() && serviceNameField.String() != "" {
						serviceName = serviceNameField.String()
					}
				}
			}
		}
	}

	// Try to find the service in configured namespaces
	var svc *corev1.Service
	var err error
	var foundNamespace string

	for _, namespace := range namespaces {
		logger.DebugStructured("Searching for CDI upload proxy service", map[string]interface{}{
			"correlation_id": correlationID,
			"service_name":   serviceName,
			"namespace":      namespace,
			"operation":      "cdi_service_discovery",
		})

		svc, err = c.kubernetesClient.CoreV1().Services(namespace).Get(context.Background(), serviceName, metav1.GetOptions{})
		if err == nil {
			foundNamespace = namespace
			logger.InfoStructured("Found CDI upload proxy service", map[string]interface{}{
				"correlation_id": correlationID,
				"service_name":   serviceName,
				"namespace":      foundNamespace,
				"operation":      "cdi_service_discovery",
			})
			break
		}

		logger.DebugStructured("CDI upload proxy service not found in namespace", map[string]interface{}{
			"correlation_id": correlationID,
			"service_name":   serviceName,
			"namespace":      namespace,
			"error":          err.Error(),
			"operation":      "cdi_service_discovery",
		})
	}

	if svc == nil {
		logger.ErrorStructured("Failed to find CDI upload proxy service in any configured namespace", map[string]interface{}{
			"correlation_id":      correlationID,
			"service_name":        serviceName,
			"searched_namespaces": namespaces,
			"operation":           "cdi_service_discovery",
		})
		return "", fmt.Errorf("failed to find CDI upload proxy service '%s' in namespaces %v: %w", serviceName, namespaces, err)
	}

	// Use the service URL
	return fmt.Sprintf("https://%s.%s.svc.cluster.local:443", svc.Name, svc.Namespace), nil
}

// createCertConfigMap creates a ConfigMap with CA certificate for HTTPS imports
func (c *Client) createCertConfigMap(namespace, configMapName, imageURL string) error {
	// Extract hostname from URL
	u, err := url.Parse(imageURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL %s: %w", imageURL, err)
	}

	// Fetch the certificate from the server
	certPEM, err := c.fetchServerCertificate(u.Host)
	if err != nil {
		logger.Warning("Failed to fetch certificate from %s: %v", u.Host, err)
		// Create a ConfigMap with a comment explaining the issue
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: namespace,
			},
			Data: map[string]string{
				"ca.crt": fmt.Sprintf("# Failed to fetch certificate from %s\n# Error: %v\n# Please manually add the CA certificate here", u.Host, err),
			},
		}

		_, err = c.kubernetesClient.CoreV1().ConfigMaps(namespace).Create(context.Background(), configMap, metav1.CreateOptions{})
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to create cert ConfigMap %s: %w", configMapName, err)
		}
		return fmt.Errorf("certificate fetch failed: %w", err)
	}

	// Create ConfigMap with the actual certificate
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			"ca.crt": certPEM,
		},
	}

	_, err = c.kubernetesClient.CoreV1().ConfigMaps(namespace).Create(context.Background(), configMap, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			logger.Info("Cert ConfigMap %s already exists", configMapName)
			return nil
		}
		return fmt.Errorf("failed to create cert ConfigMap %s: %w", configMapName, err)
	}

	logger.Info("Created cert ConfigMap %s for host %s", configMapName, u.Host)
	return nil
}

// fetchServerCertificate fetches the certificate from an HTTPS server
func (c *Client) fetchServerCertificate(host string) (string, error) {
	// Create a connection to get the certificate
	conn, err := tls.Dial("tcp", host, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // We're fetching the cert to verify it later
	})
	if err != nil {
		return "", fmt.Errorf("failed to connect to %s: %w", host, err)
	}
	defer conn.Close()

	// Get the certificate chain
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("no certificates received from %s", host)
	}

	// For now, use the server certificate itself as the CA
	// In production, you might want to extract the CA certificate from the chain
	cert := certs[0]

	// Convert certificate to PEM format
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})

	if certPEM == nil {
		return "", fmt.Errorf("failed to encode certificate to PEM")
	}

	return string(certPEM), nil
}

// IsDataVolumeReady checks if a DataVolume is in Ready state
func (c *Client) IsDataVolumeReady(namespace, name string) (bool, error) {
	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return false, fmt.Errorf("dynamic client is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	gvr := schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "datavolumes",
	}

	dv, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to get DataVolume %s: %w", name, err)
	}

	// Check if DataVolume is ready
	if phase, found, err := unstructured.NestedString(dv.Object, "status", "phase"); err == nil && found {
		return phase == "Succeeded", nil
	}

	return false, nil
}

// SetBootOrder sets the boot order for a VM to prioritize CD-ROM when boot target is CD
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

	// Get current devices
	devices, found, err := unstructured.NestedMap(vmCopy.Object, "spec", "template", "spec", "domain", "devices")
	if err == nil && found {
		// Update disk boot order
		if disks, found := devices["disks"].([]interface{}); found {
			for i, disk := range disks {
				if diskMap, ok := disk.(map[string]interface{}); ok {
					if diskName, found := diskMap["name"].(string); found {
						if bootTarget == "Cd" && diskName == "cdrom0" {
							// Set CD-ROM as first boot device
							diskMap["bootOrder"] = int64(1)
							logger.Info("Set CD-ROM %s as first boot device", diskName)
						} else if diskName == "disk1" {
							// Set main disk as second boot device
							diskMap["bootOrder"] = int64(2)
							logger.Info("Set disk %s as second boot device", diskName)
						}
					}
				}
				// Update the disk in the slice
				disks[i] = disk
			}
			devices["disks"] = disks
		}

		// Update devices in VM
		err = unstructured.SetNestedMap(vmCopy.Object, devices, "spec", "template", "spec", "domain", "devices")
		if err != nil {
			return fmt.Errorf("failed to update VM devices: %w", err)
		}
	}

	// Update VM
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, vmCopy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update VM boot order: %w", err)
	}

	logger.Info("Successfully set boot order to %s for VM %s/%s", bootTarget, namespace, name)
	return nil
}

// SetBootOnce sets the boot source override to "Once" for the next boot
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

	// Store boot once configuration in VM annotations
	annotations := vmCopy.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations["redfish.boot.source.override.enabled"] = "Once"
	annotations["redfish.boot.source.override.target"] = bootTarget
	annotations["redfish.boot.source.override.mode"] = "UEFI"

	vmCopy.SetAnnotations(annotations)

	// Update VM
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, vmCopy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update VM boot once configuration: %w", err)
	}

	logger.Info("Successfully set boot once to %s for VM %s/%s", bootTarget, namespace, name)
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

	logger.Debug("Current VM spec before ejection: %v", vm.Object)

	// Update VM to remove CD-ROM disk and volume reference
	vmCopy := vm.DeepCopy()

	// Get current disks and remove the specified disk
	devices, found, err := unstructured.NestedMap(vmCopy.Object, "spec", "template", "spec", "domain", "devices")
	if err != nil || !found {
		logger.Warning("No devices found in VM %s/%s when trying to eject media %s", namespace, name, mediaID)
		return fmt.Errorf("no devices found in VM")
	}

	// Get current disks list and remove the specified disk
	var disks []interface{}
	var foundDisk bool
	if existingDisks, found := devices["disks"].([]interface{}); found {
		for _, disk := range existingDisks {
			if diskMap, ok := disk.(map[string]interface{}); ok {
				if diskName, found := diskMap["name"].(string); found {
					if diskName != mediaID {
						disks = append(disks, disk)
					} else {
						logger.Info("Removing CD-ROM device %s from VM spec", mediaID)
						foundDisk = true
					}
				}
			}
		}
	}
	if !foundDisk {
		logger.Warning("CD-ROM device %s not found in VM %s/%s disks list during ejection", mediaID, namespace, name)
	}
	devices["disks"] = disks

	// Get current volumes and remove the volume reference
	var volumes []interface{}
	var foundVolume bool
	if existingVolumes, found, err := unstructured.NestedSlice(vmCopy.Object, "spec", "template", "spec", "volumes"); err == nil && found {
		for _, volume := range existingVolumes {
			if volumeMap, ok := volume.(map[string]interface{}); ok {
				if volumeName, found := volumeMap["name"].(string); found {
					if volumeName != mediaID {
						volumes = append(volumes, volume)
					} else {
						logger.Info("Removing volume reference %s from VM spec", mediaID)
						foundVolume = true
					}
				}
			}
		}
	}
	if !foundVolume {
		logger.Warning("Volume reference %s not found in VM %s/%s during ejection", mediaID, namespace, name)
	}

	// Update volumes in VM
	err = unstructured.SetNestedSlice(vmCopy.Object, volumes, "spec", "template", "spec", "volumes")
	if err != nil {
		logger.Error("Failed to update VM volumes for %s/%s: %v", namespace, name, err)
		return fmt.Errorf("failed to update VM volumes: %w", err)
	}

	// Update devices in VM
	err = unstructured.SetNestedMap(vmCopy.Object, devices, "spec", "template", "spec", "domain", "devices")
	if err != nil {
		logger.Error("Failed to update VM devices for %s/%s: %v", namespace, name, err)
		return fmt.Errorf("failed to update VM devices: %w", err)
	}

	logger.Debug("VM spec after removing CD-ROM and volume: %v", vmCopy.Object)

	// Update VM
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}

	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, vmCopy, metav1.UpdateOptions{})
	if err != nil {
		logger.Error("Failed to update VM %s/%s after removing CD-ROM and volume: %v", namespace, name, err)
		return fmt.Errorf("failed to update VM: %w", err)
	}

	logger.Info("Successfully updated VM %s/%s to remove CD-ROM and volume for media %s", namespace, name, mediaID)

	// Clean up associated PVC and VolumeImportSource
	pvcName := fmt.Sprintf("%s-bootiso", name)
	volumeImportSourceName := fmt.Sprintf("%s-populator", pvcName)

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
	gvrVIS := schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "volumeimportsources",
	}

	logger.Info("Deleting VolumeImportSource %s for ejected virtual media", volumeImportSourceName)
	err = c.dynamicClient.Resource(gvrVIS).Namespace(namespace).Delete(ctx, volumeImportSourceName, metav1.DeleteOptions{})
	if err != nil {
		logger.Warning("Failed to delete VolumeImportSource %s: %v", volumeImportSourceName, err)
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

	// Get memory from VM spec
	memory, found, err := unstructured.NestedString(vm.Object, "spec", "template", "spec", "domain", "memory", "guest")
	if err != nil || !found {
		logger.Warning("Memory not found in VM spec for %s/%s, using default 2.0 GB", namespace, name)
		return 2.0, nil // Low default fallback
	}

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

	// Get CPU cores from VM spec
	cpuCores, found, err := unstructured.NestedInt64(vm.Object, "spec", "template", "spec", "domain", "cpu", "cores")
	if err != nil || !found {
		logger.Warning("CPU cores not found in VM spec for %s/%s, using default 1 core", namespace, name)
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

	// Get DataVolume templates for storage capacity
	dataVolumeTemplates, found, err := unstructured.NestedSlice(vm.Object, "spec", "dataVolumeTemplates")
	if err == nil && found {
		totalCapacity := 0.0
		var volumes []map[string]interface{}

		for _, dv := range dataVolumeTemplates {
			if dvMap, ok := dv.(map[string]interface{}); ok {
				// Get volume name
				volumeName := ""
				if metadata, found := dvMap["metadata"].(map[string]interface{}); found {
					if name, found := metadata["name"].(string); found {
						volumeName = name
					}
				}

				// Get storage capacity
				capacity := 0.0
				if spec, found := dvMap["spec"].(map[string]interface{}); found {
					if storage, found := spec["storage"].(map[string]interface{}); found {
						if resources, found := storage["resources"].(map[string]interface{}); found {
							if requests, found := resources["requests"].(map[string]interface{}); found {
								if storageStr, found := requests["storage"].(string); found {
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
						}
					}
				}

				totalCapacity += capacity
				volumes = append(volumes, map[string]interface{}{
					"name":     volumeName,
					"capacity": capacity,
				})
			}
		}

		storageInfo["totalCapacityGB"] = totalCapacity
		storageInfo["volumes"] = volumes
	}

	return storageInfo, nil
}

// GetVMNetworkDetails gets detailed network information of a VirtualMachine
func (c *Client) GetVMNetworkDetails(namespace, name string) ([]map[string]interface{}, error) {
	vm, err := c.GetVM(namespace, name)
	if err != nil {
		return nil, err
	}

	var interfaces []map[string]interface{}

	// Get network interfaces from VM spec
	devices, found, err := unstructured.NestedMap(vm.Object, "spec", "template", "spec", "domain", "devices")
	if err == nil && found {
		if networkInterfaces, found := devices["interfaces"].([]interface{}); found {
			for _, iface := range networkInterfaces {
				if ifaceMap, ok := iface.(map[string]interface{}); ok {
					interfaceInfo := map[string]interface{}{
						"name": "",
						"mac":  "",
						"type": "bridge",
					}

					if name, found := ifaceMap["name"].(string); found {
						interfaceInfo["name"] = name
					}

					if macAddress, found := ifaceMap["macAddress"].(string); found {
						interfaceInfo["mac"] = macAddress
					}

					// Determine interface type
					if _, found := ifaceMap["bridge"]; found {
						interfaceInfo["type"] = "bridge"
					} else if _, found := ifaceMap["masquerade"]; found {
						interfaceInfo["type"] = "masquerade"
					} else if _, found := ifaceMap["sriov"]; found {
						interfaceInfo["type"] = "sriov"
					}

					interfaces = append(interfaces, interfaceInfo)
				}
			}
		}
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

// cleanupExistingDataVolume removes an existing DataVolume if it exists and is in a failed state
func (c *Client) cleanupExistingDataVolume(namespace, dataVolumeName string) error {
	// Check if dynamicClient is initialized
	if c.dynamicClient == nil {
		return fmt.Errorf("dynamic client is not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	gvr := schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "datavolumes",
	}

	// Check if DataVolume exists
	dv, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, dataVolumeName, metav1.GetOptions{})
	if err != nil {
		// DataVolume doesn't exist, nothing to clean up
		return nil
	}

	// Check if DataVolume is in a failed state
	if phase, found, err := unstructured.NestedString(dv.Object, "status", "phase"); err == nil && found {
		if phase == "ImportInProgress" || phase == "Failed" {
			logger.Info("Cleaning up existing DataVolume %s in state %s", dataVolumeName, phase)

			// Delete the DataVolume
			err = c.dynamicClient.Resource(gvr).Namespace(namespace).Delete(ctx, dataVolumeName, metav1.DeleteOptions{})
			if err != nil {
				return fmt.Errorf("failed to delete existing DataVolume %s: %w", dataVolumeName, err)
			}

			logger.Info("Successfully cleaned up existing DataVolume %s", dataVolumeName)
		}
	}

	return nil
}

// generateUniquePVCName generates a unique PVC name with timestamp and random suffix
// to avoid conflicts when multiple operations target the same VM
func (c *Client) generateUniquePVCName(vmName string) string {
	timestamp := time.Now().Unix()
	// Generate a random 6-character suffix to further reduce collision probability
	randomSuffix := fmt.Sprintf("%06d", rand.Intn(1000000))
	return fmt.Sprintf("%s-bootiso-%d-%s", vmName, timestamp, randomSuffix)
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
