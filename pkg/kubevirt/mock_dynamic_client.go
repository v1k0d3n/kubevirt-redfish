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
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1beta1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

// deepCopyUnstructured creates a deep copy of an unstructured object using JSON marshaling.
// This is necessary because DeepCopyJSON doesn't handle uint64 values properly.
func deepCopyUnstructured(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if obj == nil {
		return nil, nil
	}
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal unstructured object: %w", err)
	}
	var newObj map[string]interface{}
	if err := json.Unmarshal(data, &newObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal unstructured object: %w", err)
	}
	return &unstructured.Unstructured{Object: newObj}, nil
}

// MockDynamicClient implements dynamic.Interface for testing
type MockDynamicClient struct {
	mu        sync.RWMutex
	resources map[string]map[string]*unstructured.Unstructured // "gvr/namespace" -> name -> object
}

// NewMockDynamicClient creates a new mock dynamic client
func NewMockDynamicClient() *MockDynamicClient {
	return &MockDynamicClient{
		resources: make(map[string]map[string]*unstructured.Unstructured),
	}
}

// resourceKey returns the key for storing resources
func resourceKey(gvr schema.GroupVersionResource, namespace string) string {
	return fmt.Sprintf("%s/%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource, namespace)
}

// maxNameLength is the Kubernetes limit for object names (DNS subdomain)
const maxNameLength = 63

// validateName returns an error if the object name exceeds the Kubernetes 63 character limit.
func validateName(name string) error {
	if len(name) > maxNameLength {
		return fmt.Errorf("name %q is %d characters, exceeds the Kubernetes limit of %d", name, len(name), maxNameLength)
	}
	return nil
}

// addResource adds a resource to the mock client. It panics if the name exceeds 63 characters
// so that tests catch name length violations immediately.
func (m *MockDynamicClient) addResource(gvr schema.GroupVersionResource, namespace, name string, obj *unstructured.Unstructured) {
	if err := validateName(name); err != nil {
		panic(fmt.Sprintf("mock addResource: %v (resource: %s)", err, gvr.Resource))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(gvr, namespace)
	if m.resources[key] == nil {
		m.resources[key] = make(map[string]*unstructured.Unstructured)
	}
	m.resources[key][name] = obj
}

// getResource gets a resource from the mock client
func (m *MockDynamicClient) getResource(gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := resourceKey(gvr, namespace)
	if m.resources[key] == nil {
		return nil, false
	}
	obj, found := m.resources[key][name]
	return obj, found
}

// listResources lists resources from the mock client
func (m *MockDynamicClient) listResources(gvr schema.GroupVersionResource, namespace string) []*unstructured.Unstructured {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := resourceKey(gvr, namespace)
	if m.resources[key] == nil {
		return nil
	}

	var result []*unstructured.Unstructured
	for _, obj := range m.resources[key] {
		result = append(result, obj)
	}
	return result
}

// deleteResource deletes a resource from the mock client
func (m *MockDynamicClient) deleteResource(gvr schema.GroupVersionResource, namespace, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceKey(gvr, namespace)
	if m.resources[key] == nil {
		return false
	}
	if _, found := m.resources[key][name]; found {
		delete(m.resources[key], name)
		return true
	}
	return false
}

// AddVM adds a VirtualMachine to the mock client
func (m *MockDynamicClient) AddVM(vm *kubevirtv1.VirtualMachine) error {
	u, err := vmToUnstructured(vm)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}
	m.addResource(gvr, vm.Namespace, vm.Name, u)
	return nil
}

// AddVMI adds a VirtualMachineInstance to the mock client
func (m *MockDynamicClient) AddVMI(vmi *kubevirtv1.VirtualMachineInstance) error {
	u, err := vmiToUnstructured(vmi)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}
	m.addResource(gvr, vmi.Namespace, vmi.Name, u)
	return nil
}

// AddStorageProfile adds a CDI StorageProfile to the mock client.
// StorageProfiles are cluster-scoped and named after the StorageClass.
func (m *MockDynamicClient) AddStorageProfile(sp *cdiv1beta1.StorageProfile) error {
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sp)
	if err != nil {
		return fmt.Errorf("failed to convert StorageProfile to unstructured: %w", err)
	}
	obj := &unstructured.Unstructured{Object: u}
	obj.SetAPIVersion("cdi.kubevirt.io/v1beta1")
	obj.SetKind("StorageProfile")

	gvr := schema.GroupVersionResource{
		Group:    "cdi.kubevirt.io",
		Version:  "v1beta1",
		Resource: "storageprofiles",
	}
	// Cluster-scoped: namespace is empty
	m.addResource(gvr, "", sp.Name, obj)
	return nil
}

// AddVMWithStatus is a helper to quickly create a VM with common status
func (m *MockDynamicClient) AddVMWithStatus(namespace, name, status string) error {
	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: kubevirtv1.VirtualMachineStatus{
			PrintableStatus: kubevirtv1.VirtualMachinePrintableStatus(status),
		},
	}
	return m.AddVM(vm)
}

// AddVMIWithPhase is a helper to quickly create a VMI with a phase
func (m *MockDynamicClient) AddVMIWithPhase(namespace, name string, phase kubevirtv1.VirtualMachineInstancePhase) error {
	vmi := &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: kubevirtv1.VirtualMachineInstanceStatus{
			Phase: phase,
		},
	}
	return m.AddVMI(vmi)
}

// GetVM retrieves a VirtualMachine from the mock client for verification purposes
func (m *MockDynamicClient) GetVM(namespace, name string) (*kubevirtv1.VirtualMachine, error) {
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachines",
	}
	obj, found := m.getResource(gvr, namespace, name)
	if !found {
		return nil, fmt.Errorf("VM %s/%s not found", namespace, name)
	}
	return unstructuredToVM(obj)
}

// GetVMI retrieves a VirtualMachineInstance from the mock client for verification purposes
func (m *MockDynamicClient) GetVMI(namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	gvr := schema.GroupVersionResource{
		Group:    "kubevirt.io",
		Version:  "v1",
		Resource: "virtualmachineinstances",
	}
	obj, found := m.getResource(gvr, namespace, name)
	if !found {
		return nil, fmt.Errorf("VMI %s/%s not found", namespace, name)
	}
	return unstructuredToVMI(obj)
}

// Resource implements dynamic.Interface
func (m *MockDynamicClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return &mockNamespaceableResource{
		client: m,
		gvr:    gvr,
	}
}

// mockNamespaceableResource implements dynamic.NamespaceableResourceInterface
type mockNamespaceableResource struct {
	client    *MockDynamicClient
	gvr       schema.GroupVersionResource
	namespace string
}

// Namespace implements dynamic.NamespaceableResourceInterface
func (r *mockNamespaceableResource) Namespace(namespace string) dynamic.ResourceInterface {
	return &mockNamespaceableResource{
		client:    r.client,
		gvr:       r.gvr,
		namespace: namespace,
	}
}

// Create implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Create(ctx context.Context, obj *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	name := obj.GetName()
	namespace := obj.GetNamespace()
	if namespace == "" {
		namespace = r.namespace
	}

	copied, err := deepCopyUnstructured(obj)
	if err != nil {
		return nil, err
	}
	r.client.addResource(r.gvr, namespace, name, copied)
	return deepCopyUnstructured(obj)
}

// Update implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Update(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	name := obj.GetName()
	namespace := obj.GetNamespace()
	if namespace == "" {
		namespace = r.namespace
	}

	copied, err := deepCopyUnstructured(obj)
	if err != nil {
		return nil, err
	}
	r.client.addResource(r.gvr, namespace, name, copied)
	return deepCopyUnstructured(obj)
}

// UpdateStatus implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) UpdateStatus(ctx context.Context, obj *unstructured.Unstructured, options metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return r.Update(ctx, obj, options)
}

// Delete implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	if r.client.deleteResource(r.gvr, r.namespace, name) {
		return nil
	}
	return fmt.Errorf("resource %s/%s not found", r.namespace, name)
}

// DeleteCollection implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) DeleteCollection(ctx context.Context, options metav1.DeleteOptions, listOptions metav1.ListOptions) error {
	return nil // No-op for mock
}

// Get implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Get(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	obj, found := r.client.getResource(r.gvr, r.namespace, name)
	if !found {
		return nil, fmt.Errorf("%s \"%s\" not found", r.gvr.Resource, name)
	}
	return deepCopyUnstructured(obj)
}

// List implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	resources := r.client.listResources(r.gvr, r.namespace)

	list := &unstructured.UnstructuredList{
		Object: map[string]interface{}{
			"apiVersion": r.gvr.Group + "/" + r.gvr.Version,
			"kind":       r.gvr.Resource + "List",
		},
	}

	for _, obj := range resources {
		copied, err := deepCopyUnstructured(obj)
		if err != nil {
			return nil, err
		}
		list.Items = append(list.Items, *copied)
	}

	return list, nil
}

// Watch implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
	return watch.NewFake(), nil
}

// Patch implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, options metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	obj, found := r.client.getResource(r.gvr, r.namespace, name)
	if !found {
		return nil, fmt.Errorf("%s \"%s\" not found", r.gvr.Resource, name)
	}

	// Make a copy to modify
	copied, err := deepCopyUnstructured(obj)
	if err != nil {
		return nil, err
	}

	// Apply JSON patch if it's a JSONPatchType
	if pt == types.JSONPatchType {
		var patches []jsonPatchOp
		if err := json.Unmarshal(data, &patches); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON patch: %w", err)
		}

		for _, patch := range patches {
			if err := applyJSONPatchOp(copied.Object, patch); err != nil {
				return nil, fmt.Errorf("failed to apply patch operation: %w", err)
			}
		}

		// Store the updated object
		r.client.addResource(r.gvr, r.namespace, name, copied)
	}

	// Apply Merge patch if it's a MergePatchType (RFC 7396)
	if pt == types.MergePatchType {
		var patchMap map[string]interface{}
		if err := json.Unmarshal(data, &patchMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal merge patch: %w", err)
		}

		// Deep merge patchMap into copied.Object
		mergeMaps(copied.Object, patchMap)

		// Store the updated object
		r.client.addResource(r.gvr, r.namespace, name, copied)
	}

	return deepCopyUnstructured(copied)
}

// mergeMaps recursively merges src into dst according to RFC 7396 JSON Merge Patch.
// - If src has a key with value null, the key is deleted from dst
// - If src has a key with a non-null value, it replaces the value in dst
// - Objects are merged recursively
func mergeMaps(dst, src map[string]interface{}) {
	for key, srcValue := range src {
		if srcValue == nil {
			// null means delete the key
			delete(dst, key)
		} else if srcMap, ok := srcValue.(map[string]interface{}); ok {
			// Source value is a map - need to merge recursively
			if dstMap, ok := dst[key].(map[string]interface{}); ok {
				// Destination also has a map - merge recursively
				mergeMaps(dstMap, srcMap)
			} else {
				// Destination doesn't have a map - replace with a copy
				dst[key] = deepCopyMap(srcMap)
			}
		} else {
			// For all other values (including arrays), replace entirely
			dst[key] = srcValue
		}
	}
}

// deepCopyMap creates a deep copy of a map
func deepCopyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		if m, ok := v.(map[string]interface{}); ok {
			dst[k] = deepCopyMap(m)
		} else if s, ok := v.([]interface{}); ok {
			dst[k] = deepCopySlice(s)
		} else {
			dst[k] = v
		}
	}
	return dst
}

// deepCopySlice creates a deep copy of a slice
func deepCopySlice(src []interface{}) []interface{} {
	dst := make([]interface{}, len(src))
	for i, v := range src {
		if m, ok := v.(map[string]interface{}); ok {
			dst[i] = deepCopyMap(m)
		} else if s, ok := v.([]interface{}); ok {
			dst[i] = deepCopySlice(s)
		} else {
			dst[i] = v
		}
	}
	return dst
}

// jsonPatchOp represents a JSON patch operation
type jsonPatchOp struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// applyJSONPatchOp applies a single JSON patch operation to the object
func applyJSONPatchOp(obj map[string]interface{}, patch jsonPatchOp) error {
	// Parse the path into segments (e.g., "/spec/runStrategy" -> ["spec", "runStrategy"])
	path := strings.TrimPrefix(patch.Path, "/")
	segments := strings.Split(path, "/")

	// Unescape JSON Pointer escape sequences (RFC 6901)
	// ~1 means / and ~0 means ~
	for i, seg := range segments {
		seg = strings.ReplaceAll(seg, "~1", "/")
		seg = strings.ReplaceAll(seg, "~0", "~")
		segments[i] = seg
	}

	switch patch.Op {
	case "replace", "add":
		return setNestedField(obj, patch.Value, segments...)
	case "remove":
		return removeNestedField(obj, segments...)
	default:
		// For unsupported operations, just ignore (good enough for testing)
		return nil
	}
}

// setNestedField sets a value at the specified path, creating intermediate maps as needed
func setNestedField(obj map[string]interface{}, value interface{}, fields ...string) error {
	if len(fields) == 0 {
		return fmt.Errorf("no fields provided")
	}

	current := obj
	for i := 0; i < len(fields)-1; i++ {
		field := fields[i]
		if next, ok := current[field].(map[string]interface{}); ok {
			current = next
		} else {
			// Create intermediate map if it doesn't exist
			newMap := make(map[string]interface{})
			current[field] = newMap
			current = newMap
		}
	}

	current[fields[len(fields)-1]] = value
	return nil
}

// removeNestedField removes a field at the specified path
func removeNestedField(obj map[string]interface{}, fields ...string) error {
	if len(fields) == 0 {
		return fmt.Errorf("no fields provided")
	}

	current := obj
	for i := 0; i < len(fields)-1; i++ {
		field := fields[i]
		if next, ok := current[field].(map[string]interface{}); ok {
			current = next
		} else {
			// Path doesn't exist, nothing to remove
			return nil
		}
	}

	delete(current, fields[len(fields)-1])
	return nil
}

// Apply implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) Apply(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions, subresources ...string) (*unstructured.Unstructured, error) {
	return r.Update(ctx, obj, metav1.UpdateOptions{})
}

// ApplyStatus implements dynamic.ResourceInterface
func (r *mockNamespaceableResource) ApplyStatus(ctx context.Context, name string, obj *unstructured.Unstructured, options metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return r.UpdateStatus(ctx, obj, metav1.UpdateOptions{})
}

// Ensure MockDynamicClient implements dynamic.Interface
var _ dynamic.Interface = (*MockDynamicClient)(nil)
