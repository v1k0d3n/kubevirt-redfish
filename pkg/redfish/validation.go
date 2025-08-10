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
	"fmt"
	"strings"
)

// ValidationError represents a validation error with field and message information.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface for ValidationError.
func (e ValidationError) Error() string {
	return fmt.Sprintf("validation error in field '%s': %s", e.Field, e.Message)
}

// ValidationResult represents the result of a validation operation.
type ValidationResult struct {
	IsValid bool              `json:"isValid"`
	Errors  []ValidationError `json:"errors,omitempty"`
}

// ValidateServiceRoot validates a ServiceRoot object.
func ValidateServiceRoot(sr ServiceRoot) ValidationResult {
	var errors []ValidationError

	if sr.ID == "" {
		errors = append(errors, ValidationError{Field: "Id", Message: "ID cannot be empty"})
	}

	if sr.Name == "" {
		errors = append(errors, ValidationError{Field: "Name", Message: "Name cannot be empty"})
	}

	if sr.OdataID == "" {
		errors = append(errors, ValidationError{Field: "@odata.id", Message: "OdataID cannot be empty"})
	}

	if sr.OdataType == "" {
		errors = append(errors, ValidationError{Field: "@odata.type", Message: "OdataType cannot be empty"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// ValidateComputerSystem validates a ComputerSystem object.
func ValidateComputerSystem(cs ComputerSystem) ValidationResult {
	var errors []ValidationError

	if cs.ID == "" {
		errors = append(errors, ValidationError{Field: "Id", Message: "ID cannot be empty"})
	}

	if cs.Name == "" {
		errors = append(errors, ValidationError{Field: "Name", Message: "Name cannot be empty"})
	}

	if cs.OdataID == "" {
		errors = append(errors, ValidationError{Field: "@odata.id", Message: "OdataID cannot be empty"})
	}

	if cs.OdataType == "" {
		errors = append(errors, ValidationError{Field: "@odata.type", Message: "OdataType cannot be empty"})
	}

	if !isValidPowerState(cs.PowerState) {
		errors = append(errors, ValidationError{Field: "PowerState", Message: "Invalid power state"})
	}

	if !isValidSystemType(cs.SystemType) {
		errors = append(errors, ValidationError{Field: "SystemType", Message: "Invalid system type"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// ValidateStatus validates a Status object.
func ValidateStatus(status Status) ValidationResult {
	var errors []ValidationError

	if status.State == "" {
		errors = append(errors, ValidationError{Field: "State", Message: "State cannot be empty"})
	}

	if status.Health == "" {
		errors = append(errors, ValidationError{Field: "Health", Message: "Health cannot be empty"})
	}

	if !isValidHealthState(status.Health) {
		errors = append(errors, ValidationError{Field: "Health", Message: "Invalid health state"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// ValidateResetRequest validates a ResetRequest object.
func ValidateResetRequest(req ResetRequest) ValidationResult {
	var errors []ValidationError

	if req.ResetType == "" {
		errors = append(errors, ValidationError{Field: "ResetType", Message: "ResetType cannot be empty"})
	}

	if !isValidResetType(req.ResetType) {
		errors = append(errors, ValidationError{Field: "ResetType", Message: "Invalid reset type"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// ValidateInsertMediaRequest validates an InsertMediaRequest object.
func ValidateInsertMediaRequest(req InsertMediaRequest) ValidationResult {
	var errors []ValidationError

	if req.Image == "" {
		errors = append(errors, ValidationError{Field: "Image", Message: "Image URL cannot be empty"})
	}

	if !isValidImageURL(req.Image) {
		errors = append(errors, ValidationError{Field: "Image", Message: "Invalid image URL format"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// ValidateTask validates a Task object.
func ValidateTask(task Task) ValidationResult {
	var errors []ValidationError

	if task.ID == "" {
		errors = append(errors, ValidationError{Field: "Id", Message: "ID cannot be empty"})
	}

	if task.Name == "" {
		errors = append(errors, ValidationError{Field: "Name", Message: "Name cannot be empty"})
	}

	if task.OdataID == "" {
		errors = append(errors, ValidationError{Field: "@odata.id", Message: "OdataID cannot be empty"})
	}

	if task.OdataType == "" {
		errors = append(errors, ValidationError{Field: "@odata.type", Message: "OdataType cannot be empty"})
	}

	if !isValidTaskState(task.TaskState) {
		errors = append(errors, ValidationError{Field: "TaskState", Message: "Invalid task state"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// Helper functions for validation

func isValidPowerState(state string) bool {
	validStates := []string{PowerStateOn, PowerStateOff, PowerStatePaused, PowerStateUnknown}
	for _, validState := range validStates {
		if state == validState {
			return true
		}
	}
	return false
}

func isValidSystemType(systemType string) bool {
	validTypes := []string{SystemTypePhysical, SystemTypeVirtual}
	for _, validType := range validTypes {
		if systemType == validType {
			return true
		}
	}
	return false
}

func isValidHealthState(health string) bool {
	validStates := []string{HealthOK, HealthWarning, HealthCritical}
	for _, validState := range validStates {
		if health == validState {
			return true
		}
	}
	return false
}

func isValidResetType(resetType string) bool {
	validTypes := []string{
		ResetTypeOn, ResetTypeForceOff, ResetTypeGracefulShutdown,
		ResetTypeForceRestart, ResetTypeGracefulRestart, ResetTypePause, ResetTypeResume,
	}
	for _, validType := range validTypes {
		if resetType == validType {
			return true
		}
	}
	return false
}

func isValidTaskState(taskState string) bool {
	validStates := []string{
		TaskStateNew, TaskStateStarting, TaskStateRunning, TaskStateSuspended,
		TaskStateInterrupted, TaskStatePending, TaskStateStopping, TaskStateCompleted,
		TaskStateKilled, TaskStateException, TaskStateService,
	}
	for _, validState := range validStates {
		if taskState == validState {
			return true
		}
	}
	return false
}

func isValidImageURL(url string) bool {
	// Basic URL validation - should start with http:// or https://
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// ValidateError validates an Error object.
func ValidateError(err Error) ValidationResult {
	var errors []ValidationError

	if err.Error.Code == "" {
		errors = append(errors, ValidationError{Field: "error.code", Message: "Error code cannot be empty"})
	}

	if err.Error.Message == "" {
		errors = append(errors, ValidationError{Field: "error.message", Message: "Error message cannot be empty"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}

// ValidateVirtualMedia validates a VirtualMedia object.
func ValidateVirtualMedia(vm VirtualMedia) ValidationResult {
	var errors []ValidationError

	if vm.ID == "" {
		errors = append(errors, ValidationError{Field: "Id", Message: "ID cannot be empty"})
	}

	if vm.Name == "" {
		errors = append(errors, ValidationError{Field: "Name", Message: "Name cannot be empty"})
	}

	if vm.OdataID == "" {
		errors = append(errors, ValidationError{Field: "@odata.id", Message: "OdataID cannot be empty"})
	}

	if vm.OdataType == "" {
		errors = append(errors, ValidationError{Field: "@odata.type", Message: "OdataType cannot be empty"})
	}

	if len(vm.MediaTypes) == 0 {
		errors = append(errors, ValidationError{Field: "MediaTypes", Message: "MediaTypes cannot be empty"})
	}

	return ValidationResult{
		IsValid: len(errors) == 0,
		Errors:  errors,
	}
}
