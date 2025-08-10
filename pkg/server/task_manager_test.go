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

// Package server provides the HTTP server implementation for the KubeVirt Redfish API.
// It handles all HTTP requests, implements Redfish API endpoints, and manages
// the server lifecycle including startup, shutdown, and graceful termination.
//
// The package provides:
// - HTTP server with TLS support
// - Redfish API endpoint implementations
// - Request routing and middleware integration
// - Error handling and response formatting
// - Server configuration and lifecycle management
//
// The server implements the Redfish specification v1.22.1 and provides endpoints
// for service discovery, chassis management, system operations, and virtual media.
package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

func TestTaskManager(t *testing.T) {
	tm := NewTaskManager()

	// Test task creation
	taskID := tm.CreateTask("Test Task", "test-namespace", "test-vm", "cdrom0", "https://example.com/test.iso")
	if taskID == "" {
		t.Fatal("Expected non-empty task ID")
	}

	// Test task retrieval
	task, exists := tm.GetTask(taskID)
	if !exists {
		t.Fatal("Expected task to exist")
	}

	if task.ID != taskID {
		t.Errorf("Expected task ID %s, got %s", taskID, task.ID)
	}

	if task.TaskState != redfish.TaskStateRunning {
		t.Errorf("Expected task state %s, got %s", redfish.TaskStateRunning, task.TaskState)
	}

	// Test task state updates
	err := tm.UpdateTaskState(taskID, redfish.TaskStateCompleted, "OK", "Task completed successfully")
	if err != nil {
		t.Fatalf("Failed to update task state: %v", err)
	}

	task, exists = tm.GetTask(taskID)
	if !exists {
		t.Fatal("Expected task to still exist")
	}

	if task.TaskState != redfish.TaskStateCompleted {
		t.Errorf("Expected task state %s, got %s", redfish.TaskStateCompleted, task.TaskState)
	}

	if task.EndTime == nil {
		t.Error("Expected EndTime to be set for completed task")
	}

	// Test Redfish Task conversion
	redfishTask := task.ToRedfishTask()
	if redfishTask.ID != taskID {
		t.Errorf("Expected Redfish task ID %s, got %s", taskID, redfishTask.ID)
	}

	if redfishTask.TaskState != redfish.TaskStateCompleted {
		t.Errorf("Expected Redfish task state %s, got %s", redfish.TaskStateCompleted, redfishTask.TaskState)
	}

	// Test task progress updates
	err = tm.UpdateTaskProgress(taskID, "Processing step 1")
	if err != nil {
		t.Fatalf("Failed to update task progress: %v", err)
	}

	task, _ = tm.GetTask(taskID)
	if len(task.Messages) < 2 {
		t.Error("Expected at least 2 messages in task")
	}

	// Test task failure
	err = tm.FailTask(taskID, "Task failed due to error")
	if err != nil {
		t.Fatalf("Failed to fail task: %v", err)
	}

	task, _ = tm.GetTask(taskID)
	if task.TaskState != redfish.TaskStateException {
		t.Errorf("Expected task state %s, got %s", redfish.TaskStateException, task.TaskState)
	}

	// Test cleanup
	// Make the task older by setting EndTime to the past
	oldTime := time.Now().Add(-10 * time.Millisecond)
	task.EndTime = &oldTime

	// Use a longer duration to ensure the task is old enough to be cleaned up
	tm.CleanupOldTasks(5 * time.Millisecond)
	time.Sleep(10 * time.Millisecond) // Wait longer for cleanup

	task, exists = tm.GetTask(taskID)
	if exists {
		t.Error("Expected task to be cleaned up")
	}
}

func TestTaskManager_CompleteTask(t *testing.T) {
	tm := NewTaskManager()

	// Create a task
	taskID := tm.CreateTask("Complete Test Task", "test-namespace", "test-vm", "cdrom0", "https://example.com/test.iso")

	// Complete the task
	err := tm.CompleteTask(taskID, "Task completed successfully")
	if err != nil {
		t.Fatalf("Failed to complete task: %v", err)
	}

	// Verify task is completed
	task, exists := tm.GetTask(taskID)
	if !exists {
		t.Fatal("Expected task to exist")
	}

	if task.TaskState != redfish.TaskStateCompleted {
		t.Errorf("Expected task state %s, got %s", redfish.TaskStateCompleted, task.TaskState)
	}

	if task.TaskStatus != "OK" {
		t.Errorf("Expected task status 'OK', got %s", task.TaskStatus)
	}

	if task.EndTime == nil {
		t.Error("Expected EndTime to be set for completed task")
	}

	// Test completing non-existent task
	err = tm.CompleteTask("non-existent-task", "This should fail")
	if err == nil {
		t.Error("Expected error when completing non-existent task")
	}
}

func TestTaskManager_StartCleanupRoutine(t *testing.T) {
	tm := NewTaskManager()

	// Start cleanup routine
	tm.StartCleanupRoutine()

	// Verify cleanup ticker is created
	if tm.cleanup == nil {
		t.Error("Expected cleanup ticker to be created")
	}

	// Stop the task manager to clean up
	tm.Stop()
}

func TestTaskManager_Stop(t *testing.T) {
	tm := NewTaskManager()

	// Create some tasks
	taskID1 := tm.CreateTask("Task 1", "test-namespace", "test-vm1", "cdrom0", "https://example.com/test1.iso")
	taskID2 := tm.CreateTask("Task 2", "test-namespace", "test-vm2", "cdrom1", "https://example.com/test2.iso")

	// Verify tasks exist
	if _, exists := tm.GetTask(taskID1); !exists {
		t.Error("Expected task 1 to exist")
	}
	if _, exists := tm.GetTask(taskID2); !exists {
		t.Error("Expected task 2 to exist")
	}

	// Stop the task manager
	tm.Stop()

	// Verify all tasks are cleaned up
	if _, exists := tm.GetTask(taskID1); exists {
		t.Error("Expected task 1 to be cleaned up")
	}
	if _, exists := tm.GetTask(taskID2); exists {
		t.Error("Expected task 2 to be cleaned up")
	}

	// Verify stop channel is closed
	select {
	case <-tm.stopChan:
		// Expected - channel should be closed
	default:
		t.Error("Expected stop channel to be closed")
	}

	// Verify context is cancelled
	select {
	case <-tm.ctx.Done():
		// Expected - context should be cancelled
	default:
		t.Error("Expected context to be cancelled")
	}
}

func TestTaskManager_EdgeCases(t *testing.T) {
	tm := NewTaskManager()

	// Test getting non-existent task
	task, exists := tm.GetTask("non-existent-task")
	if exists {
		t.Error("Expected non-existent task to not exist")
	}
	if task != nil {
		t.Error("Expected non-existent task to be nil")
	}

	// Test updating state of non-existent task
	err := tm.UpdateTaskState("non-existent-task", redfish.TaskStateCompleted, "OK", "This should fail")
	if err == nil {
		t.Error("Expected error when updating non-existent task")
	}

	// Test updating progress of non-existent task
	err = tm.UpdateTaskProgress("non-existent-task", "This should fail")
	if err == nil {
		t.Error("Expected error when updating progress of non-existent task")
	}

	// Test completing non-existent task
	err = tm.CompleteTask("non-existent-task", "This should fail")
	if err == nil {
		t.Error("Expected error when completing non-existent task")
	}

	// Test failing non-existent task
	err = tm.FailTask("non-existent-task", "This should fail")
	if err == nil {
		t.Error("Expected error when failing non-existent task")
	}
}

func TestTaskManager_CleanupOldTasks(t *testing.T) {
	tm := NewTaskManager()

	// Create tasks with different ages
	now := time.Now()

	// Recent task (should not be cleaned up)
	recentTaskID := tm.CreateTask("Recent Task", "test-namespace", "test-vm", "cdrom0", "https://example.com/test.iso")
	recentTask, _ := tm.GetTask(recentTaskID)
	recentTask.EndTime = &now

	// Old task (should be cleaned up)
	oldTaskID := tm.CreateTask("Old Task", "test-namespace", "test-vm", "cdrom1", "https://example.com/test.iso")
	oldTask, _ := tm.GetTask(oldTaskID)
	oldTime := now.Add(-2 * time.Hour) // 2 hours old
	oldTask.EndTime = &oldTime

	// Very old task (should be cleaned up)
	veryOldTaskID := tm.CreateTask("Very Old Task", "test-namespace", "test-vm", "cdrom2", "https://example.com/test.iso")
	veryOldTask, _ := tm.GetTask(veryOldTaskID)
	veryOldTime := now.Add(-3 * time.Hour) // 3 hours old
	veryOldTask.EndTime = &veryOldTime

	// Clean up tasks older than 1 hour
	tm.CleanupOldTasks(1 * time.Hour)

	// Verify recent task still exists
	if _, exists := tm.GetTask(recentTaskID); !exists {
		t.Error("Expected recent task to still exist")
	}

	// Verify old tasks are cleaned up
	if _, exists := tm.GetTask(oldTaskID); exists {
		t.Error("Expected old task to be cleaned up")
	}

	if _, exists := tm.GetTask(veryOldTaskID); exists {
		t.Error("Expected very old task to be cleaned up")
	}
}

func TestTaskManager_ToRedfishTask(t *testing.T) {
	tm := NewTaskManager()

	// Create a task
	taskID := tm.CreateTask("Redfish Test Task", "test-namespace", "test-vm", "cdrom0", "https://example.com/test.iso")
	task, _ := tm.GetTask(taskID)

	// Convert to Redfish task
	redfishTask := task.ToRedfishTask()

	// Verify Redfish task properties
	if redfishTask.ID != taskID {
		t.Errorf("Expected Redfish task ID %s, got %s", taskID, redfishTask.ID)
	}

	if redfishTask.Name != "Redfish Test Task" {
		t.Errorf("Expected Redfish task name 'Redfish Test Task', got %s", redfishTask.Name)
	}

	if redfishTask.TaskState != redfish.TaskStateRunning {
		t.Errorf("Expected Redfish task state %s, got %s", redfish.TaskStateRunning, redfishTask.TaskState)
	}

	if redfishTask.TaskStatus != "OK" {
		t.Errorf("Expected Redfish task status 'OK', got %s", redfishTask.TaskStatus)
	}

	expectedOdataID := fmt.Sprintf("/redfish/v1/TaskService/Tasks/%s", taskID)
	if redfishTask.OdataID != expectedOdataID {
		t.Errorf("Expected Redfish task OdataID %s, got %s", expectedOdataID, redfishTask.OdataID)
	}

	if redfishTask.OdataType != "#Task.v1_0_0.Task" {
		t.Errorf("Expected Redfish task OdataType '#Task.v1_0_0.Task', got %s", redfishTask.OdataType)
	}

	// Test with completed task
	tm.CompleteTask(taskID, "Task completed")
	task, _ = tm.GetTask(taskID)
	redfishTask = task.ToRedfishTask()

	if redfishTask.TaskState != redfish.TaskStateCompleted {
		t.Errorf("Expected completed Redfish task state %s, got %s", redfish.TaskStateCompleted, redfishTask.TaskState)
	}

	// Verify EndTime is set for completed task
	if task.EndTime == nil {
		t.Error("Expected EndTime to be set for completed task")
	}
}
