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
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

// TaskInfo represents an individual task with its current state and metadata.
// This type is shared between different task management implementations.
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

// ToRedfishTask converts a TaskInfo to a Redfish Task
func (t *TaskInfo) ToRedfishTask() redfish.Task {
	task := redfish.Task{
		OdataContext: "/redfish/v1/$metadata#Task.Task",
		OdataID:      "/redfish/v1/TaskService/Tasks/" + t.ID,
		OdataType:    "#Task.v1_4_2.Task",
		ID:           t.ID,
		Name:         t.Name,
		TaskState:    t.TaskState,
		TaskStatus:   t.TaskStatus,
		StartTime:    t.StartTime,
		Messages:     t.Messages,
	}

	// Only set EndTime if it's not nil
	if t.EndTime != nil {
		task.EndTime = *t.EndTime
	}

	return task
}
