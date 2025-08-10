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

package server

import (
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

func TestTaskInfo_ToRedfishTask(t *testing.T) {
	// Test with all fields populated
	startTime := time.Now()
	endTime := startTime.Add(1 * time.Hour)

	taskInfo := &TaskInfo{
		ID:             "test-task-123",
		Name:           "Test Task",
		TaskState:      "Running",
		TaskStatus:     "OK",
		StartTime:      startTime,
		EndTime:        &endTime,
		Messages:       []redfish.Message{{Message: "Test message"}},
		Namespace:      "default",
		VMName:         "test-vm",
		MediaID:        "test-media",
		ImageURL:       "http://example.com/image.iso",
		DataVolumeName: "test-dv",
	}

	redfishTask := taskInfo.ToRedfishTask()

	// Verify all fields are correctly mapped
	if redfishTask.ID != "test-task-123" {
		t.Errorf("Expected ID 'test-task-123', got '%s'", redfishTask.ID)
	}
	if redfishTask.Name != "Test Task" {
		t.Errorf("Expected Name 'Test Task', got '%s'", redfishTask.Name)
	}
	if redfishTask.TaskState != "Running" {
		t.Errorf("Expected TaskState 'Running', got '%s'", redfishTask.TaskState)
	}
	if redfishTask.TaskStatus != "OK" {
		t.Errorf("Expected TaskStatus 'OK', got '%s'", redfishTask.TaskStatus)
	}
	if !redfishTask.StartTime.Equal(startTime) {
		t.Errorf("Expected StartTime %v, got %v", startTime, redfishTask.StartTime)
	}
	if !redfishTask.EndTime.Equal(endTime) {
		t.Errorf("Expected EndTime %v, got %v", endTime, redfishTask.EndTime)
	}
	if len(redfishTask.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(redfishTask.Messages))
	}
	if redfishTask.Messages[0].Message != "Test message" {
		t.Errorf("Expected message 'Test message', got '%s'", redfishTask.Messages[0].Message)
	}

	// Verify Redfish-specific fields
	if redfishTask.OdataContext != "/redfish/v1/$metadata#Task.Task" {
		t.Errorf("Expected OdataContext '/redfish/v1/$metadata#Task.Task', got '%s'", redfishTask.OdataContext)
	}
	if redfishTask.OdataID != "/redfish/v1/TaskService/Tasks/test-task-123" {
		t.Errorf("Expected OdataID '/redfish/v1/TaskService/Tasks/test-task-123', got '%s'", redfishTask.OdataID)
	}
	if redfishTask.OdataType != "#Task.v1_4_2.Task" {
		t.Errorf("Expected OdataType '#Task.v1_4_2.Task', got '%s'", redfishTask.OdataType)
	}
}

func TestTaskInfo_ToRedfishTask_WithNilEndTime(t *testing.T) {
	// Test with nil EndTime
	startTime := time.Now()

	taskInfo := &TaskInfo{
		ID:         "test-task-456",
		Name:       "Test Task No End",
		TaskState:  "Completed",
		TaskStatus: "OK",
		StartTime:  startTime,
		EndTime:    nil, // nil EndTime
		Messages:   []redfish.Message{},
	}

	redfishTask := taskInfo.ToRedfishTask()

	// Verify basic fields
	if redfishTask.ID != "test-task-456" {
		t.Errorf("Expected ID 'test-task-456', got '%s'", redfishTask.ID)
	}
	if redfishTask.Name != "Test Task No End" {
		t.Errorf("Expected Name 'Test Task No End', got '%s'", redfishTask.Name)
	}
	if redfishTask.TaskState != "Completed" {
		t.Errorf("Expected TaskState 'Completed', got '%s'", redfishTask.TaskState)
	}
	if !redfishTask.StartTime.Equal(startTime) {
		t.Errorf("Expected StartTime %v, got %v", startTime, redfishTask.StartTime)
	}

	// Verify EndTime is zero value when nil
	if !redfishTask.EndTime.IsZero() {
		t.Errorf("Expected EndTime to be zero value when nil, got %v", redfishTask.EndTime)
	}
}

func TestTaskInfo_ToRedfishTask_EmptyMessages(t *testing.T) {
	// Test with empty messages
	startTime := time.Now()

	taskInfo := &TaskInfo{
		ID:         "test-task-789",
		Name:       "Test Task Empty Messages",
		TaskState:  "Running",
		TaskStatus: "OK",
		StartTime:  startTime,
		EndTime:    nil,
		Messages:   []redfish.Message{}, // empty messages
	}

	redfishTask := taskInfo.ToRedfishTask()

	// Verify messages are empty
	if len(redfishTask.Messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(redfishTask.Messages))
	}
}

func TestTaskInfo_ToRedfishTask_MultipleMessages(t *testing.T) {
	// Test with multiple messages
	startTime := time.Now()

	taskInfo := &TaskInfo{
		ID:         "test-task-multi",
		Name:       "Test Task Multiple Messages",
		TaskState:  "Running",
		TaskStatus: "OK",
		StartTime:  startTime,
		EndTime:    nil,
		Messages: []redfish.Message{
			{Message: "First message"},
			{Message: "Second message"},
			{Message: "Third message"},
		},
	}

	redfishTask := taskInfo.ToRedfishTask()

	// Verify all messages are preserved
	if len(redfishTask.Messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(redfishTask.Messages))
	}
	if redfishTask.Messages[0].Message != "First message" {
		t.Errorf("Expected first message 'First message', got '%s'", redfishTask.Messages[0].Message)
	}
	if redfishTask.Messages[1].Message != "Second message" {
		t.Errorf("Expected second message 'Second message', got '%s'", redfishTask.Messages[1].Message)
	}
	if redfishTask.Messages[2].Message != "Third message" {
		t.Errorf("Expected third message 'Third message', got '%s'", redfishTask.Messages[2].Message)
	}
}
