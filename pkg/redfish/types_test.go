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
	"encoding/json"
	"testing"
)

func TestServiceRoot(t *testing.T) {
	serviceRoot := ServiceRoot{
		OdataContext: "/redfish/v1/$metadata#ServiceRoot.ServiceRoot",
		OdataID:      "/redfish/v1/",
		OdataType:    "#ServiceRoot.v1_0_0.ServiceRoot",
		ID:           "RootService",
		Name:         "Root Service",
		Systems: Link{
			OdataID: "/redfish/v1/Systems",
		},
		Chassis: Link{
			OdataID: "/redfish/v1/Chassis",
		},
		Managers: Link{
			OdataID: "/redfish/v1/Managers",
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(serviceRoot)
	if err != nil {
		t.Fatalf("Failed to marshal ServiceRoot: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled ServiceRoot
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal ServiceRoot: %v", err)
	}

	// Verify fields
	if unmarshaled.OdataContext != serviceRoot.OdataContext {
		t.Errorf("Expected OdataContext %s, got %s", serviceRoot.OdataContext, unmarshaled.OdataContext)
	}
	if unmarshaled.OdataID != serviceRoot.OdataID {
		t.Errorf("Expected OdataID %s, got %s", serviceRoot.OdataID, unmarshaled.OdataID)
	}
	if unmarshaled.ID != serviceRoot.ID {
		t.Errorf("Expected ID %s, got %s", serviceRoot.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != serviceRoot.Name {
		t.Errorf("Expected Name %s, got %s", serviceRoot.Name, unmarshaled.Name)
	}
}

func TestComputerSystem(t *testing.T) {
	system := ComputerSystem{
		OdataContext: "/redfish/v1/$metadata#ComputerSystem.ComputerSystem",
		OdataID:      "/redfish/v1/Systems/test-system",
		OdataType:    "#ComputerSystem.v1_0_0.ComputerSystem",
		ID:           "test-system",
		Name:         "Test System",
		SystemType:   SystemTypeVirtual,
		Status: Status{
			State:  "Enabled",
			Health: HealthOK,
		},
		PowerState: PowerStateOn,
		Memory: MemorySummary{
			OdataID:              "/redfish/v1/Systems/test-system/Memory",
			TotalSystemMemoryGiB: 8.0,
		},
		Storage: Link{
			OdataID: "/redfish/v1/Systems/test-system/Storage",
		},
		EthernetInterfaces: Link{
			OdataID: "/redfish/v1/Systems/test-system/EthernetInterfaces",
		},
		Boot: Boot{
			BootSourceOverrideEnabled: "Disabled",
			BootSourceOverrideTarget:  "None",
			BootSourceOverrideMode:    "UEFI",
		},
		Actions: Actions{
			Reset: ResetAction{
				Target: "/redfish/v1/Systems/test-system/Actions/ComputerSystem.Reset",
				ResetType: []string{
					ResetTypeOn,
					ResetTypeForceOff,
					ResetTypeGracefulShutdown,
				},
			},
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(system)
	if err != nil {
		t.Fatalf("Failed to marshal ComputerSystem: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled ComputerSystem
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal ComputerSystem: %v", err)
	}

	// Verify fields
	if unmarshaled.ID != system.ID {
		t.Errorf("Expected ID %s, got %s", system.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != system.Name {
		t.Errorf("Expected Name %s, got %s", system.Name, unmarshaled.Name)
	}
	if unmarshaled.SystemType != system.SystemType {
		t.Errorf("Expected SystemType %s, got %s", system.SystemType, unmarshaled.SystemType)
	}
	if unmarshaled.PowerState != system.PowerState {
		t.Errorf("Expected PowerState %s, got %s", system.PowerState, unmarshaled.PowerState)
	}
	if unmarshaled.Status.State != system.Status.State {
		t.Errorf("Expected Status.State %s, got %s", system.Status.State, unmarshaled.Status.State)
	}
	if unmarshaled.Status.Health != system.Status.Health {
		t.Errorf("Expected Status.Health %s, got %s", system.Status.Health, unmarshaled.Status.Health)
	}
}

func TestChassis(t *testing.T) {
	chassis := Chassis{
		OdataContext: "/redfish/v1/$metadata#Chassis.Chassis",
		OdataID:      "/redfish/v1/Chassis/test-chassis",
		OdataType:    "#Chassis.v1_0_0.Chassis",
		ID:           "test-chassis",
		Name:         "Test Chassis",
		Description:  "Test chassis description",
		Status: Status{
			State:  "Enabled",
			Health: HealthOK,
		},
		ChassisType: "RackMount",
		Links: Links{
			ComputerSystems: []Link{
				{OdataID: "/redfish/v1/Systems/system1"},
				{OdataID: "/redfish/v1/Systems/system2"},
			},
			Managers: []Link{
				{OdataID: "/redfish/v1/Managers/manager1"},
			},
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(chassis)
	if err != nil {
		t.Fatalf("Failed to marshal Chassis: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled Chassis
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal Chassis: %v", err)
	}

	// Verify fields
	if unmarshaled.ID != chassis.ID {
		t.Errorf("Expected ID %s, got %s", chassis.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != chassis.Name {
		t.Errorf("Expected Name %s, got %s", chassis.Name, unmarshaled.Name)
	}
	if unmarshaled.Description != chassis.Description {
		t.Errorf("Expected Description %s, got %s", chassis.Description, unmarshaled.Description)
	}
	if unmarshaled.ChassisType != chassis.ChassisType {
		t.Errorf("Expected ChassisType %s, got %s", chassis.ChassisType, unmarshaled.ChassisType)
	}
	if len(unmarshaled.Links.ComputerSystems) != len(chassis.Links.ComputerSystems) {
		t.Errorf("Expected %d computer systems, got %d", len(chassis.Links.ComputerSystems), len(unmarshaled.Links.ComputerSystems))
	}
}

func TestVirtualMedia(t *testing.T) {
	virtualMedia := VirtualMedia{
		OdataContext:   "/redfish/v1/$metadata#VirtualMedia.VirtualMedia",
		OdataID:        "/redfish/v1/Systems/test-system/VirtualMedia/CD",
		OdataType:      "#VirtualMedia.v1_0_0.VirtualMedia",
		ID:             "CD",
		Name:           "Virtual CD",
		MediaTypes:     []string{"CD", "DVD"},
		ConnectedVia:   "Applet",
		Inserted:       true,
		WriteProtected: true,
		Actions: VirtualMediaActions{
			InsertMedia: InsertMediaAction{
				Target: "/redfish/v1/Systems/test-system/VirtualMedia/CD/Actions/VirtualMedia.InsertMedia",
			},
			EjectMedia: EjectMediaAction{
				Target: "/redfish/v1/Systems/test-system/VirtualMedia/CD/Actions/VirtualMedia.EjectMedia",
			},
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(virtualMedia)
	if err != nil {
		t.Fatalf("Failed to marshal VirtualMedia: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled VirtualMedia
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal VirtualMedia: %v", err)
	}

	// Verify fields
	if unmarshaled.ID != virtualMedia.ID {
		t.Errorf("Expected ID %s, got %s", virtualMedia.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != virtualMedia.Name {
		t.Errorf("Expected Name %s, got %s", virtualMedia.Name, unmarshaled.Name)
	}
	if unmarshaled.ConnectedVia != virtualMedia.ConnectedVia {
		t.Errorf("Expected ConnectedVia %s, got %s", virtualMedia.ConnectedVia, unmarshaled.ConnectedVia)
	}
	if unmarshaled.Inserted != virtualMedia.Inserted {
		t.Errorf("Expected Inserted %v, got %v", virtualMedia.Inserted, unmarshaled.Inserted)
	}
	if unmarshaled.WriteProtected != virtualMedia.WriteProtected {
		t.Errorf("Expected WriteProtected %v, got %v", virtualMedia.WriteProtected, unmarshaled.WriteProtected)
	}
	if len(unmarshaled.MediaTypes) != len(virtualMedia.MediaTypes) {
		t.Errorf("Expected %d media types, got %d", len(virtualMedia.MediaTypes), len(unmarshaled.MediaTypes))
	}
}

func TestTask(t *testing.T) {
	task := Task{
		OdataContext: "/redfish/v1/$metadata#Task.Task",
		OdataID:      "/redfish/v1/TaskService/Tasks/task-123",
		OdataType:    "#Task.v1_0_0.Task",
		ID:           "task-123",
		Name:         "Test Task",
		TaskState:    TaskStateRunning,
		TaskStatus:   "OK",
		Messages: []Message{
			{
				Message: "Task started successfully",
			},
			{
				Message: "Processing request",
			},
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("Failed to marshal Task: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled Task
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal Task: %v", err)
	}

	// Verify fields
	if unmarshaled.ID != task.ID {
		t.Errorf("Expected ID %s, got %s", task.ID, unmarshaled.ID)
	}
	if unmarshaled.Name != task.Name {
		t.Errorf("Expected Name %s, got %s", task.Name, unmarshaled.Name)
	}
	if unmarshaled.TaskState != task.TaskState {
		t.Errorf("Expected TaskState %s, got %s", task.TaskState, unmarshaled.TaskState)
	}
	if unmarshaled.TaskStatus != task.TaskStatus {
		t.Errorf("Expected TaskStatus %s, got %s", task.TaskStatus, unmarshaled.TaskStatus)
	}
	if len(unmarshaled.Messages) != len(task.Messages) {
		t.Errorf("Expected %d messages, got %d", len(task.Messages), len(unmarshaled.Messages))
	}
}

func TestError(t *testing.T) {
	errorResponse := Error{
		Error: ErrorInfo{
			Code:    ErrorCodeGeneralError,
			Message: "Test error message",
			ExtendedInfo: []ExtendedInfo{
				{
					MessageID:  "Base.1.0.GeneralError",
					Message:    "Extended error information",
					Severity:   "Error",
					Resolution: "Check configuration",
				},
			},
		},
	}

	// Test JSON marshaling
	data, err := json.Marshal(errorResponse)
	if err != nil {
		t.Fatalf("Failed to marshal Error: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled Error
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal Error: %v", err)
	}

	// Verify fields
	if unmarshaled.Error.Code != errorResponse.Error.Code {
		t.Errorf("Expected Code %s, got %s", errorResponse.Error.Code, unmarshaled.Error.Code)
	}
	if unmarshaled.Error.Message != errorResponse.Error.Message {
		t.Errorf("Expected Message %s, got %s", errorResponse.Error.Message, unmarshaled.Error.Message)
	}
	if len(unmarshaled.Error.ExtendedInfo) != len(errorResponse.Error.ExtendedInfo) {
		t.Errorf("Expected %d extended info items, got %d", len(errorResponse.Error.ExtendedInfo), len(unmarshaled.Error.ExtendedInfo))
	}
}

func TestPowerStates(t *testing.T) {
	// Test that power states are defined
	if PowerStateOn == "" {
		t.Error("PowerStateOn should be defined")
	}
	if PowerStateOff == "" {
		t.Error("PowerStateOff should be defined")
	}
	if PowerStatePaused == "" {
		t.Error("PowerStatePaused should be defined")
	}
	if PowerStateUnknown == "" {
		t.Error("PowerStateUnknown should be defined")
	}
}

func TestResetTypes(t *testing.T) {
	// Test that reset types are defined
	if ResetTypeOn == "" {
		t.Error("ResetTypeOn should be defined")
	}
	if ResetTypeForceOff == "" {
		t.Error("ResetTypeForceOff should be defined")
	}
	if ResetTypeGracefulShutdown == "" {
		t.Error("ResetTypeGracefulShutdown should be defined")
	}
	if ResetTypeForceRestart == "" {
		t.Error("ResetTypeForceRestart should be defined")
	}
	if ResetTypeGracefulRestart == "" {
		t.Error("ResetTypeGracefulRestart should be defined")
	}
	if ResetTypePause == "" {
		t.Error("ResetTypePause should be defined")
	}
	if ResetTypeResume == "" {
		t.Error("ResetTypeResume should be defined")
	}
}

func TestSystemTypes(t *testing.T) {
	// Test that system types are defined
	if SystemTypePhysical == "" {
		t.Error("SystemTypePhysical should be defined")
	}
	if SystemTypeVirtual == "" {
		t.Error("SystemTypeVirtual should be defined")
	}
}

func TestHealthStates(t *testing.T) {
	// Test that health states are defined
	if HealthOK == "" {
		t.Error("HealthOK should be defined")
	}
	if HealthWarning == "" {
		t.Error("HealthWarning should be defined")
	}
	if HealthCritical == "" {
		t.Error("HealthCritical should be defined")
	}
}

func TestTaskStates(t *testing.T) {
	// Test that task states are defined
	if TaskStateNew == "" {
		t.Error("TaskStateNew should be defined")
	}
	if TaskStateRunning == "" {
		t.Error("TaskStateRunning should be defined")
	}
	if TaskStateCompleted == "" {
		t.Error("TaskStateCompleted should be defined")
	}
	if TaskStateException == "" {
		t.Error("TaskStateException should be defined")
	}
}

func TestErrorCodes(t *testing.T) {
	// Test that error codes are defined
	if ErrorCodeGeneralError == "" {
		t.Error("ErrorCodeGeneralError should be defined")
	}
	if ErrorCodePropertyValueNotInList == "" {
		t.Error("ErrorCodePropertyValueNotInList should be defined")
	}
	if ErrorCodeResourceNotFound == "" {
		t.Error("ErrorCodeResourceNotFound should be defined")
	}
	if ErrorCodePropertyMissing == "" {
		t.Error("ErrorCodePropertyMissing should be defined")
	}
}

func TestCollectionTypes(t *testing.T) {
	// Test ComputerSystemCollection
	systemCollection := ComputerSystemCollection{
		OdataContext: "/redfish/v1/$metadata#ComputerSystemCollection.ComputerSystemCollection",
		OdataID:      "/redfish/v1/Systems",
		OdataType:    "#ComputerSystemCollection.ComputerSystemCollection",
		Name:         "Computer System Collection",
		Members: []Link{
			{OdataID: "/redfish/v1/Systems/system1"},
			{OdataID: "/redfish/v1/Systems/system2"},
		},
		MembersCount: 2,
	}

	data, err := json.Marshal(systemCollection)
	if err != nil {
		t.Fatalf("Failed to marshal ComputerSystemCollection: %v", err)
	}

	var unmarshaled ComputerSystemCollection
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal ComputerSystemCollection: %v", err)
	}

	if unmarshaled.MembersCount != systemCollection.MembersCount {
		t.Errorf("Expected MembersCount %d, got %d", systemCollection.MembersCount, unmarshaled.MembersCount)
	}
	if len(unmarshaled.Members) != len(systemCollection.Members) {
		t.Errorf("Expected %d members, got %d", len(systemCollection.Members), len(unmarshaled.Members))
	}

	// Test ChassisCollection
	chassisCollection := ChassisCollection{
		OdataContext: "/redfish/v1/$metadata#ChassisCollection.ChassisCollection",
		OdataID:      "/redfish/v1/Chassis",
		OdataType:    "#ChassisCollection.ChassisCollection",
		Name:         "Chassis Collection",
		Members: []Link{
			{OdataID: "/redfish/v1/Chassis/chassis1"},
			{OdataID: "/redfish/v1/Chassis/chassis2"},
		},
		MembersCount: 2,
	}

	data, err = json.Marshal(chassisCollection)
	if err != nil {
		t.Fatalf("Failed to marshal ChassisCollection: %v", err)
	}

	var unmarshaledChassis ChassisCollection
	err = json.Unmarshal(data, &unmarshaledChassis)
	if err != nil {
		t.Fatalf("Failed to unmarshal ChassisCollection: %v", err)
	}

	if unmarshaledChassis.MembersCount != chassisCollection.MembersCount {
		t.Errorf("Expected MembersCount %d, got %d", chassisCollection.MembersCount, unmarshaledChassis.MembersCount)
	}
	if len(unmarshaledChassis.Members) != len(chassisCollection.Members) {
		t.Errorf("Expected %d members, got %d", len(chassisCollection.Members), len(unmarshaledChassis.Members))
	}
}
