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

// Package redfish provides data types and structures for the Redfish API specification.
// It defines all the JSON structures used by the KubeVirt Redfish API server to
// communicate with Redfish clients like Metal3/Ironic and OpenShift ZTP.
//
// The package implements the Redfish specification v1.22.1 and provides:
// - Service root and resource collection types
// - Computer system (VM) representation
// - Chassis and manager resource types
// - Virtual media and boot configuration types
// - Task and error response structures
// - Power management action types
//
// All types follow the Redfish JSON schema specification and include proper
// OData annotations for compatibility with Redfish clients.
//
// Example usage:
//
//	// Create a service root response
//	serviceRoot := redfish.ServiceRoot{
//		OdataContext: "/redfish/v1/$metadata#ServiceRoot.ServiceRoot",
//		OdataID:      "/redfish/v1/",
//		OdataType:    "#ServiceRoot.v1_0_0.ServiceRoot",
//		ID:           "RootService",
//		Name:         "Root Service",
//		Systems: redfish.Link{
//			OdataID: "/redfish/v1/Systems",
//		},
//		Chassis: redfish.Link{
//			OdataID: "/redfish/v1/Chassis",
//		},
//	}
package redfish

import (
	"time"
)

// ServiceRoot represents the Redfish service root endpoint.
// This is the entry point for all Redfish API interactions and provides
// links to the main resource collections (Systems, Chassis, Managers).
type ServiceRoot struct {
	OdataContext string `json:"@odata.context"`
	OdataID      string `json:"@odata.id"`
	OdataType    string `json:"@odata.type"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	Systems      Link   `json:"Systems"`
	Chassis      Link   `json:"Chassis"`
	Managers     Link   `json:"Managers"`
}

// Link represents a Redfish link to another resource.
// Links are used throughout Redfish to provide navigation between resources.
type Link struct {
	OdataID string `json:"@odata.id"`
}

// ComputerSystemCollection represents a collection of computer systems (VMs).
// This is returned when querying /redfish/v1/Systems and contains links
// to all available virtual machines.
type ComputerSystemCollection struct {
	OdataContext string `json:"@odata.context"`
	OdataID      string `json:"@odata.id"`
	OdataType    string `json:"@odata.type"`
	Name         string `json:"Name"`
	Members      []Link `json:"Members"`
	MembersCount int    `json:"Members@odata.count"`
}

// ComputerSystem represents a computer system (virtual machine).
// This is the main resource type for representing KubeVirt VMs in the Redfish API.
// It includes power state, status, memory information, CPU details, and available actions.
type ComputerSystem struct {
	OdataContext       string           `json:"@odata.context"`
	OdataID            string           `json:"@odata.id"`
	OdataType          string           `json:"@odata.type"`
	OdataEtag          string           `json:"@odata.etag,omitempty"`
	ID                 string           `json:"Id"`
	Name               string           `json:"Name"`
	SystemType         string           `json:"SystemType"`
	Status             Status           `json:"Status"`
	PowerState         string           `json:"PowerState"`
	Memory             MemorySummary    `json:"Memory"`
	ProcessorSummary   ProcessorSummary `json:"ProcessorSummary"`
	Storage            Link             `json:"Storage"`
	EthernetInterfaces Link             `json:"EthernetInterfaces"`
	VirtualMedia       Link             `json:"VirtualMedia"`
	Boot               Boot             `json:"Boot"`
	Actions            Actions          `json:"Actions"`
	Links              SystemLinks      `json:"Links"`
}

// Status represents the status of a Redfish resource.
// It includes both the operational state and health status of the resource.
type Status struct {
	State  string `json:"State"`
	Health string `json:"Health"`
}

// MemorySummary represents memory information for a computer system.
// It provides details about the total system memory available to the VM.
type MemorySummary struct {
	OdataID              string  `json:"@odata.id"`
	TotalSystemMemoryGiB float64 `json:"TotalSystemMemoryGiB"`
}

// ProcessorSummary represents processor information for a computer system.
// It provides details about the CPU configuration of the VM.
type ProcessorSummary struct {
	Count int `json:"Count"`
}

// Boot represents boot configuration for a computer system.
// It includes settings for boot source override and boot mode configuration.
type Boot struct {
	BootSourceOverrideEnabled               string   `json:"BootSourceOverrideEnabled"`
	BootSourceOverrideTarget                string   `json:"BootSourceOverrideTarget"`
	BootSourceOverrideTargetAllowableValues []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
	BootSourceOverrideMode                  string   `json:"BootSourceOverrideMode"`
	UefiTargetBootSourceOverride            string   `json:"UefiTargetBootSourceOverride,omitempty"`
}

// Actions represents available actions for a Redfish resource.
// Actions are operations that can be performed on the resource, such as
// power management operations for computer systems.
type Actions struct {
	Reset ResetAction `json:"#ComputerSystem.Reset"`
}

// ResetAction represents the reset action for a computer system.
// It defines the target URI and allowable reset types for power management.
type ResetAction struct {
	Target    string   `json:"target"`
	ResetType []string `json:"ResetType@Redfish.AllowableValues"`
}

// ChassisCollection represents a collection of chassis.
// This is returned when querying /redfish/v1/Chassis and contains links
// to all available chassis (namespace groupings).
type ChassisCollection struct {
	OdataContext string `json:"@odata.context"`
	OdataID      string `json:"@odata.id"`
	OdataType    string `json:"@odata.type"`
	Name         string `json:"Name"`
	Members      []Link `json:"Members"`
	MembersCount int    `json:"Members@odata.count"`
}

// Chassis represents a Redfish chassis resource.
// In the KubeVirt Redfish implementation, chassis represent Kubernetes namespaces
// and provide namespace isolation for virtual machines.
type Chassis struct {
	OdataContext string `json:"@odata.context"`
	OdataID      string `json:"@odata.id"`
	OdataType    string `json:"@odata.type"`
	OdataEtag    string `json:"@odata.etag,omitempty"`
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	Description  string `json:"Description"`
	Status       Status `json:"Status"`
	ChassisType  string `json:"ChassisType"`
	Links        Links  `json:"Links"`
}

// Links represents links to related resources.
// It provides navigation to computer systems and managers associated with a chassis.
type Links struct {
	ComputerSystems []Link `json:"ComputerSystems"`
	Managers        []Link `json:"Managers"`
}

// SystemLinks represents links for a computer system.
// It provides navigation to managers that manage this system.
type SystemLinks struct {
	ManagedBy []Link `json:"ManagedBy"`
}

// VirtualMediaCollection represents a collection of virtual media devices.
// This is returned when querying /redfish/v1/Systems/{systemId}/VirtualMedia
// and contains links to all available virtual media devices for a system.
type VirtualMediaCollection struct {
	OdataContext      string `json:"@odata.context"`
	OdataID           string `json:"@odata.id"`
	OdataType         string `json:"@odata.type"`
	Name              string `json:"Name"`
	Members           []Link `json:"Members"`
	MembersCount      int    `json:"Members@odata.count"`
	MembersIdentities []Link `json:"members_identities"`
}

// VirtualMedia represents virtual media (CD/DVD) for a computer system.
// It provides information about virtual media devices and their current state.
type VirtualMedia struct {
	OdataContext   string              `json:"@odata.context"`
	OdataID        string              `json:"@odata.id"`
	OdataType      string              `json:"@odata.type"`
	OdataEtag      string              `json:"@odata.etag,omitempty"`
	ID             string              `json:"Id"`
	Name           string              `json:"Name"`
	MediaTypes     []string            `json:"MediaTypes"`
	ConnectedVia   string              `json:"ConnectedVia"`
	Inserted       bool                `json:"Inserted"`
	WriteProtected bool                `json:"WriteProtected"`
	Actions        VirtualMediaActions `json:"Actions"`
}

// VirtualMediaActions represents available actions for virtual media.
// It includes actions for inserting and ejecting media from virtual devices.
type VirtualMediaActions struct {
	InsertMedia InsertMediaAction `json:"#VirtualMedia.InsertMedia"`
	EjectMedia  EjectMediaAction  `json:"#VirtualMedia.EjectMedia"`
}

// InsertMediaAction represents the insert media action.
// It defines the target URI for inserting media into a virtual device.
type InsertMediaAction struct {
	Target string `json:"target"`
}

// EjectMediaAction represents the eject media action.
// It defines the target URI for ejecting media from a virtual device.
type EjectMediaAction struct {
	Target string `json:"target"`
}

// Task represents an asynchronous task in the Redfish API.
// Tasks are used for operations that may take time to complete,
// such as media insertion or power operations.
type Task struct {
	OdataContext string    `json:"@odata.context"`
	OdataID      string    `json:"@odata.id"`
	OdataType    string    `json:"@odata.type"`
	OdataEtag    string    `json:"@odata.etag,omitempty"`
	ID           string    `json:"Id"`
	Name         string    `json:"Name"`
	TaskState    string    `json:"TaskState"`
	TaskStatus   string    `json:"TaskStatus"`
	StartTime    time.Time `json:"StartTime"`
	EndTime      time.Time `json:"EndTime,omitempty"`
	Messages     []Message `json:"Messages"`
}

// Message represents a task message.
// Messages provide additional information about task progress and status.
type Message struct {
	Message string `json:"Message"`
}

// Error represents a Redfish error response.
// It follows the Redfish error specification for consistent error handling.
type Error struct {
	Error ErrorInfo `json:"error"`
}

// ErrorInfo represents error information in a Redfish error response.
// It includes error codes, messages, and extended information for debugging.
type ErrorInfo struct {
	Code         string         `json:"code"`
	Message      string         `json:"message"`
	ExtendedInfo []ExtendedInfo `json:"@Message.ExtendedInfo,omitempty"`
}

// ExtendedInfo represents extended error information.
// It provides additional details about errors including severity and resolution.
type ExtendedInfo struct {
	MessageID  string `json:"MessageId"`
	Message    string `json:"Message"`
	Severity   string `json:"Severity"`
	Resolution string `json:"Resolution"`
}

// ResetRequest represents a reset request for a computer system.
// It specifies the type of reset operation to perform on a VM.
type ResetRequest struct {
	ResetType string `json:"ResetType"`
}

// InsertMediaRequest represents an insert media request.
// It specifies the image URL to mount as virtual media.
type InsertMediaRequest struct {
	Image string `json:"Image"`
}

// PowerStates define the possible power states for computer systems.
const (
	PowerStateOn      = "On"
	PowerStateOff     = "Off"
	PowerStatePaused  = "Paused"
	PowerStateUnknown = "Unknown"
)

// ResetTypes define the possible reset operations for computer systems.
const (
	ResetTypeOn               = "On"
	ResetTypeForceOff         = "ForceOff"
	ResetTypeGracefulShutdown = "GracefulShutdown"
	ResetTypeForceRestart     = "ForceRestart"
	ResetTypeGracefulRestart  = "GracefulRestart"
	ResetTypePause            = "Pause"
	ResetTypeResume           = "Resume"
)

// SystemTypes define the possible system types for computer systems.
const (
	SystemTypePhysical = "Physical"
	SystemTypeVirtual  = "Virtual"
)

// HealthStates define the possible health states for Redfish resources.
const (
	HealthOK       = "OK"
	HealthWarning  = "Warning"
	HealthCritical = "Critical"
)

// TaskStates define the possible states for asynchronous tasks.
const (
	TaskStateNew         = "New"
	TaskStateStarting    = "Starting"
	TaskStateRunning     = "Running"
	TaskStateSuspended   = "Suspended"
	TaskStateInterrupted = "Interrupted"
	TaskStatePending     = "Pending"
	TaskStateStopping    = "Stopping"
	TaskStateCompleted   = "Completed"
	TaskStateKilled      = "Killed"
	TaskStateException   = "Exception"
	TaskStateService     = "Service"
)

// ErrorCodes define the standard Redfish error codes.
const (
	ErrorCodeGeneralError           = "Base.1.0.GeneralError"
	ErrorCodePropertyValueNotInList = "Base.1.0.PropertyValueNotInList"
	ErrorCodeResourceNotFound       = "Base.1.0.ResourceNotFound"
	ErrorCodePropertyMissing        = "Base.1.0.PropertyMissing"
)
