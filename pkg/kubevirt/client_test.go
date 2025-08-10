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
)

type mockKubeVirtClient struct {
	ListVMsFunc func(namespace string) ([]string, error)
	GetVMFunc   func(namespace, name string) (string, error)
}

func (m *mockKubeVirtClient) ListVMs(namespace string) ([]string, error) {
	return m.ListVMsFunc(namespace)
}

func (m *mockKubeVirtClient) GetVM(namespace, name string) (string, error) {
	return m.GetVMFunc(namespace, name)
}

func TestListVMs_Success(t *testing.T) {
	client := &mockKubeVirtClient{
		ListVMsFunc: func(namespace string) ([]string, error) {
			if namespace == "test-ns" {
				return []string{"vm1", "vm2"}, nil
			}
			return nil, errors.New("namespace not found")
		},
	}
	vms, err := client.ListVMs("test-ns")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(vms) != 2 {
		t.Errorf("Expected 2 VMs, got %d", len(vms))
	}
}

func TestListVMs_Error(t *testing.T) {
	client := &mockKubeVirtClient{
		ListVMsFunc: func(namespace string) ([]string, error) {
			return nil, errors.New("API error")
		},
	}
	_, err := client.ListVMs("bad-ns")
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestGetVM_Success(t *testing.T) {
	client := &mockKubeVirtClient{
		GetVMFunc: func(namespace, name string) (string, error) {
			if namespace == "test-ns" && name == "vm1" {
				return "vm1-details", nil
			}
			return "", errors.New("not found")
		},
	}
	vm, err := client.GetVM("test-ns", "vm1")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if vm != "vm1-details" {
		t.Errorf("Expected vm1-details, got %s", vm)
	}
}

func TestGetVM_Error(t *testing.T) {
	client := &mockKubeVirtClient{
		GetVMFunc: func(namespace, name string) (string, error) {
			return "", errors.New("not found")
		},
	}
	_, err := client.GetVM("test-ns", "bad-vm")
	if err == nil {
		t.Error("Expected error, got nil")
	}
}
