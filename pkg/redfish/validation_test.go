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

package redfish

import (
	"testing"
)

func TestValidationError_Error(t *testing.T) {
	err := ValidationError{
		Field:   "test_field",
		Message: "test message",
	}

	expected := "validation error in field 'test_field': test message"
	if err.Error() != expected {
		t.Errorf("Expected '%s', got '%s'", expected, err.Error())
	}
}

func TestValidateServiceRoot(t *testing.T) {
	tests := []struct {
		name     string
		service  ServiceRoot
		expected bool
	}{
		{
			name: "valid service root",
			service: ServiceRoot{
				ID:        "RootService",
				Name:      "Root Service",
				OdataID:   "/redfish/v1/",
				OdataType: "#ServiceRoot.v1_0_0.ServiceRoot",
			},
			expected: true,
		},
		{
			name: "missing ID",
			service: ServiceRoot{
				Name:      "Root Service",
				OdataID:   "/redfish/v1/",
				OdataType: "#ServiceRoot.v1_0_0.ServiceRoot",
			},
			expected: false,
		},
		{
			name: "missing Name",
			service: ServiceRoot{
				ID:        "RootService",
				OdataID:   "/redfish/v1/",
				OdataType: "#ServiceRoot.v1_0_0.ServiceRoot",
			},
			expected: false,
		},
		{
			name: "missing OdataID",
			service: ServiceRoot{
				ID:        "RootService",
				Name:      "Root Service",
				OdataType: "#ServiceRoot.v1_0_0.ServiceRoot",
			},
			expected: false,
		},
		{
			name: "missing OdataType",
			service: ServiceRoot{
				ID:      "RootService",
				Name:    "Root Service",
				OdataID: "/redfish/v1/",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateServiceRoot(tt.service)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateComputerSystem(t *testing.T) {
	tests := []struct {
		name     string
		system   ComputerSystem
		expected bool
	}{
		{
			name: "valid computer system",
			system: ComputerSystem{
				ID:         "test-vm",
				Name:       "Test VM",
				OdataID:    "/redfish/v1/Systems/test-vm",
				OdataType:  "#ComputerSystem.v1_0_0.ComputerSystem",
				PowerState: PowerStateOn,
				SystemType: SystemTypeVirtual,
			},
			expected: true,
		},
		{
			name: "invalid power state",
			system: ComputerSystem{
				ID:         "test-vm",
				Name:       "Test VM",
				OdataID:    "/redfish/v1/Systems/test-vm",
				OdataType:  "#ComputerSystem.v1_0_0.ComputerSystem",
				PowerState: "InvalidState",
				SystemType: SystemTypeVirtual,
			},
			expected: false,
		},
		{
			name: "invalid system type",
			system: ComputerSystem{
				ID:         "test-vm",
				Name:       "Test VM",
				OdataID:    "/redfish/v1/Systems/test-vm",
				OdataType:  "#ComputerSystem.v1_0_0.ComputerSystem",
				PowerState: PowerStateOn,
				SystemType: "InvalidType",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateComputerSystem(tt.system)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   Status
		expected bool
	}{
		{
			name: "valid status",
			status: Status{
				State:  "Enabled",
				Health: HealthOK,
			},
			expected: true,
		},
		{
			name: "invalid health state",
			status: Status{
				State:  "Enabled",
				Health: "InvalidHealth",
			},
			expected: false,
		},
		{
			name: "empty state",
			status: Status{
				State:  "",
				Health: HealthOK,
			},
			expected: false,
		},
		{
			name: "empty health",
			status: Status{
				State:  "Enabled",
				Health: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateStatus(tt.status)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateResetRequest(t *testing.T) {
	tests := []struct {
		name     string
		request  ResetRequest
		expected bool
	}{
		{
			name: "valid reset request",
			request: ResetRequest{
				ResetType: ResetTypeOn,
			},
			expected: true,
		},
		{
			name: "invalid reset type",
			request: ResetRequest{
				ResetType: "InvalidReset",
			},
			expected: false,
		},
		{
			name: "empty reset type",
			request: ResetRequest{
				ResetType: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateResetRequest(tt.request)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateInsertMediaRequest(t *testing.T) {
	tests := []struct {
		name     string
		request  InsertMediaRequest
		expected bool
	}{
		{
			name: "valid HTTP URL",
			request: InsertMediaRequest{
				Image: "http://example.com/image.iso",
			},
			expected: true,
		},
		{
			name: "valid HTTPS URL",
			request: InsertMediaRequest{
				Image: "https://example.com/image.iso",
			},
			expected: true,
		},
		{
			name: "invalid URL",
			request: InsertMediaRequest{
				Image: "ftp://example.com/image.iso",
			},
			expected: false,
		},
		{
			name: "empty image URL",
			request: InsertMediaRequest{
				Image: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateInsertMediaRequest(tt.request)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateTask(t *testing.T) {
	tests := []struct {
		name     string
		task     Task
		expected bool
	}{
		{
			name: "valid task",
			task: Task{
				ID:        "task-123",
				Name:      "Test Task",
				OdataID:   "/redfish/v1/TaskService/Tasks/task-123",
				OdataType: "#Task.v1_0_0.Task",
				TaskState: TaskStateNew,
			},
			expected: true,
		},
		{
			name: "invalid task state",
			task: Task{
				ID:        "task-123",
				Name:      "Test Task",
				OdataID:   "/redfish/v1/TaskService/Tasks/task-123",
				OdataType: "#Task.v1_0_0.Task",
				TaskState: "InvalidState",
			},
			expected: false,
		},
		{
			name: "empty ID",
			task: Task{
				Name:      "Test Task",
				OdataID:   "/redfish/v1/TaskService/Tasks/task-123",
				OdataType: "#Task.v1_0_0.Task",
				TaskState: TaskStateNew,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateTask(tt.task)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateError(t *testing.T) {
	tests := []struct {
		name     string
		err      Error
		expected bool
	}{
		{
			name: "valid error",
			err: Error{
				Error: ErrorInfo{
					Code:    ErrorCodeGeneralError,
					Message: "Test error message",
				},
			},
			expected: true,
		},
		{
			name: "empty error code",
			err: Error{
				Error: ErrorInfo{
					Code:    "",
					Message: "Test error message",
				},
			},
			expected: false,
		},
		{
			name: "empty error message",
			err: Error{
				Error: ErrorInfo{
					Code:    ErrorCodeGeneralError,
					Message: "",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateError(tt.err)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidateVirtualMedia(t *testing.T) {
	tests := []struct {
		name     string
		media    VirtualMedia
		expected bool
	}{
		{
			name: "valid virtual media",
			media: VirtualMedia{
				ID:         "CD",
				Name:       "Virtual CD",
				OdataID:    "/redfish/v1/Systems/test-vm/VirtualMedia/CD",
				OdataType:  "#VirtualMedia.v1_0_0.VirtualMedia",
				MediaTypes: []string{"CD", "DVD"},
			},
			expected: true,
		},
		{
			name: "empty media types",
			media: VirtualMedia{
				ID:        "CD",
				Name:      "Virtual CD",
				OdataID:   "/redfish/v1/Systems/test-vm/VirtualMedia/CD",
				OdataType: "#VirtualMedia.v1_0_0.VirtualMedia",
			},
			expected: false,
		},
		{
			name: "empty ID",
			media: VirtualMedia{
				Name:       "Virtual CD",
				OdataID:    "/redfish/v1/Systems/test-vm/VirtualMedia/CD",
				OdataType:  "#VirtualMedia.v1_0_0.VirtualMedia",
				MediaTypes: []string{"CD", "DVD"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateVirtualMedia(tt.media)
			if result.IsValid != tt.expected {
				t.Errorf("Expected IsValid to be %v, got %v", tt.expected, result.IsValid)
			}
		})
	}
}

func TestValidationHelperFunctions(t *testing.T) {
	// Test power state validation
	t.Run("power state validation", func(t *testing.T) {
		validStates := []string{PowerStateOn, PowerStateOff, PowerStatePaused, PowerStateUnknown}
		for _, state := range validStates {
			if !isValidPowerState(state) {
				t.Errorf("Expected %s to be a valid power state", state)
			}
		}

		if isValidPowerState("InvalidState") {
			t.Error("Expected InvalidState to be an invalid power state")
		}
	})

	// Test system type validation
	t.Run("system type validation", func(t *testing.T) {
		validTypes := []string{SystemTypePhysical, SystemTypeVirtual}
		for _, systemType := range validTypes {
			if !isValidSystemType(systemType) {
				t.Errorf("Expected %s to be a valid system type", systemType)
			}
		}

		if isValidSystemType("InvalidType") {
			t.Error("Expected InvalidType to be an invalid system type")
		}
	})

	// Test health state validation
	t.Run("health state validation", func(t *testing.T) {
		validStates := []string{HealthOK, HealthWarning, HealthCritical}
		for _, health := range validStates {
			if !isValidHealthState(health) {
				t.Errorf("Expected %s to be a valid health state", health)
			}
		}

		if isValidHealthState("InvalidHealth") {
			t.Error("Expected InvalidHealth to be an invalid health state")
		}
	})

	// Test reset type validation
	t.Run("reset type validation", func(t *testing.T) {
		validTypes := []string{
			ResetTypeOn, ResetTypeForceOff, ResetTypeGracefulShutdown,
			ResetTypeForceRestart, ResetTypeGracefulRestart, ResetTypePause, ResetTypeResume,
		}
		for _, resetType := range validTypes {
			if !isValidResetType(resetType) {
				t.Errorf("Expected %s to be a valid reset type", resetType)
			}
		}

		if isValidResetType("InvalidReset") {
			t.Error("Expected InvalidReset to be an invalid reset type")
		}
	})

	// Test task state validation
	t.Run("task state validation", func(t *testing.T) {
		validStates := []string{
			TaskStateNew, TaskStateStarting, TaskStateRunning, TaskStateSuspended,
			TaskStateInterrupted, TaskStatePending, TaskStateStopping, TaskStateCompleted,
			TaskStateKilled, TaskStateException, TaskStateService,
		}
		for _, taskState := range validStates {
			if !isValidTaskState(taskState) {
				t.Errorf("Expected %s to be a valid task state", taskState)
			}
		}

		if isValidTaskState("InvalidState") {
			t.Error("Expected InvalidState to be an invalid task state")
		}
	})

	// Test image URL validation
	t.Run("image URL validation", func(t *testing.T) {
		validURLs := []string{
			"http://example.com/image.iso",
			"https://example.com/image.iso",
		}
		for _, url := range validURLs {
			if !isValidImageURL(url) {
				t.Errorf("Expected %s to be a valid image URL", url)
			}
		}

		invalidURLs := []string{
			"ftp://example.com/image.iso",
			"file:///path/to/image.iso",
			"not-a-url",
			"",
		}
		for _, url := range invalidURLs {
			if isValidImageURL(url) {
				t.Errorf("Expected %s to be an invalid image URL", url)
			}
		}
	})
}
