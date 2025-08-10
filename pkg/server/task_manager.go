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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

// TaskManager manages asynchronous tasks for the Redfish API.
// It provides task creation, status tracking, and cleanup functionality.
type TaskManager struct {
	tasks    map[string]*TaskInfo
	mutex    sync.RWMutex
	ctx      context.Context
	cancel   context.CancelFunc
	cleanup  *time.Ticker
	stopChan chan struct{}
}

// TaskInfo represents an individual task with its current state and metadata.
type TaskInfo struct {
	ID             string
	Name           string
	TaskState      string
	TaskStatus     string
	StartTime      time.Time
	EndTime        *time.Time
	Messages       []redfish.Message
	Namespace      string
	VMName         string
	MediaID        string
	ImageURL       string
	DataVolumeName string
}

// NewTaskManager creates a new TaskManager instance.
func NewTaskManager() *TaskManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskManager{
		tasks:    make(map[string]*TaskInfo),
		ctx:      ctx,
		cancel:   cancel,
		stopChan: make(chan struct{}),
	}
}

// CreateTask creates a new task and returns its ID.
func (tm *TaskManager) CreateTask(name, namespace, vmName, mediaID, imageURL string) string {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	dataVolumeName := fmt.Sprintf("%s-bootiso", vmName)

	task := &TaskInfo{
		ID:             taskID,
		Name:           name,
		TaskState:      redfish.TaskStateRunning,
		TaskStatus:     "OK",
		StartTime:      time.Now(),
		Namespace:      namespace,
		VMName:         vmName,
		MediaID:        mediaID,
		ImageURL:       imageURL,
		DataVolumeName: dataVolumeName,
		Messages: []redfish.Message{
			{
				Message: fmt.Sprintf("Started virtual media insertion for %s", mediaID),
			},
		},
	}

	tm.tasks[taskID] = task
	logger.Info("Created task %s for virtual media insertion %s for VM %s/%s", taskID, mediaID, namespace, vmName)

	return taskID
}

// GetTask retrieves a task by ID.
func (tm *TaskManager) GetTask(taskID string) (*TaskInfo, bool) {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	task, exists := tm.tasks[taskID]
	return task, exists
}

// UpdateTaskState updates the state of a task.
func (tm *TaskManager) UpdateTaskState(taskID, state, status string, message string) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.TaskState = state
	task.TaskStatus = status

	if message != "" {
		task.Messages = append(task.Messages, redfish.Message{
			Message: message,
		})
	}

	if state == redfish.TaskStateCompleted || state == redfish.TaskStateException {
		now := time.Now()
		task.EndTime = &now
	}

	logger.Info("Updated task %s state to %s: %s", taskID, state, message)
	return nil
}

// UpdateTaskProgress updates the task with progress information.
func (tm *TaskManager) UpdateTaskProgress(taskID, message string) error {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}

	task.Messages = append(task.Messages, redfish.Message{
		Message: message,
	})

	logger.Debug("Updated task %s progress: %s", taskID, message)
	return nil
}

// CompleteTask marks a task as completed.
func (tm *TaskManager) CompleteTask(taskID, finalMessage string) error {
	return tm.UpdateTaskState(taskID, redfish.TaskStateCompleted, "OK", finalMessage)
}

// FailTask marks a task as failed.
func (tm *TaskManager) FailTask(taskID, errorMessage string) error {
	return tm.UpdateTaskState(taskID, redfish.TaskStateException, "Warning", errorMessage)
}

// ToRedfishTask converts a TaskInfo to a Redfish Task resource.
func (ti *TaskInfo) ToRedfishTask() redfish.Task {
	task := redfish.Task{
		OdataContext: "/redfish/v1/$metadata#Task.Task",
		OdataID:      fmt.Sprintf("/redfish/v1/TaskService/Tasks/%s", ti.ID),
		OdataType:    "#Task.v1_0_0.Task",
		OdataEtag:    fmt.Sprintf("W/\"%d\"", time.Now().Unix()), // Simple ETag for versioning
		ID:           ti.ID,
		Name:         ti.Name,
		TaskState:    ti.TaskState,
		TaskStatus:   ti.TaskStatus,
		StartTime:    ti.StartTime,
		Messages:     ti.Messages,
	}

	// Only set EndTime if it's not nil
	if ti.EndTime != nil {
		task.EndTime = *ti.EndTime
	}

	return task
}

// CleanupOldTasks removes completed tasks older than the specified duration.
func (tm *TaskManager) CleanupOldTasks(maxAge time.Duration) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	now := time.Now()
	for taskID, task := range tm.tasks {
		if task.EndTime != nil && now.Sub(*task.EndTime) > maxAge {
			delete(tm.tasks, taskID)
			logger.Debug("Cleaned up old task %s", taskID)
		}
	}
}

// StartCleanupRoutine starts a background routine to clean up old tasks.
func (tm *TaskManager) StartCleanupRoutine() {
	tm.cleanup = time.NewTicker(1 * time.Hour) // Clean up every hour

	go func() {
		defer tm.cleanup.Stop()

		for {
			select {
			case <-tm.cleanup.C:
				tm.CleanupOldTasks(24 * time.Hour) // Keep tasks for 24 hours
			case <-tm.ctx.Done():
				logger.Info("Task manager cleanup routine stopped")
				return
			case <-tm.stopChan:
				logger.Info("Task manager cleanup routine stopped")
				return
			}
		}
	}()

	logger.Info("Started task manager cleanup routine")
}

// Stop gracefully stops the task manager and cleans up resources
func (tm *TaskManager) Stop() {
	logger.Info("Stopping task manager...")

	// Stop the cleanup routine
	if tm.cleanup != nil {
		tm.cleanup.Stop()
	}

	// Signal cleanup routine to stop
	close(tm.stopChan)

	// Cancel context
	tm.cancel()

	// Clean up all tasks
	tm.mutex.Lock()
	taskCount := len(tm.tasks)
	tm.tasks = make(map[string]*TaskInfo)
	tm.mutex.Unlock()

	logger.Info("Task manager stopped, cleaned up %d tasks", taskCount)
}
