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
