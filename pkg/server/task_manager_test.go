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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

func TestPriorityQueue_RemoveMatching(t *testing.T) {
	pq := NewPriorityQueue()

	// Push jobs with different types and payloads
	j1 := &Job{ID: "j1", TaskID: "t1", Type: TaskTypePowerResetWithWait, Payload: map[string]string{"namespace": "ns1", "vmName": "vm1"}}
	j2 := &Job{ID: "j2", TaskID: "t2", Type: TaskTypeVirtualMediaInsert, Payload: map[string]string{"namespace": "ns1", "vmName": "vm1"}}
	j3 := &Job{ID: "j3", TaskID: "t3", Type: TaskTypePowerResetWithWait, Payload: map[string]string{"namespace": "ns1", "vmName": "vm1"}}
	j4 := &Job{ID: "j4", TaskID: "t4", Type: TaskTypePowerResetWithWait, Payload: map[string]string{"namespace": "ns2", "vmName": "vm2"}}

	pq.Push(j1)
	pq.Push(j2)
	pq.Push(j3)
	pq.Push(j4)

	// Remove only PowerResetWithWait for ns1/vm1
	removed := pq.RemoveMatching(func(job *Job) bool {
		if job.Type != TaskTypePowerResetWithWait {
			return false
		}
		payload, ok := job.Payload.(map[string]string)
		if !ok {
			return false
		}
		return payload["namespace"] == "ns1" && payload["vmName"] == "vm1"
	})

	require.Len(t, removed, 2)
	assert.Contains(t, []string{removed[0].ID, removed[1].ID}, "j1")
	assert.Contains(t, []string{removed[0].ID, removed[1].ID}, "j3")
	assert.Equal(t, 2, pq.Size())

	// Remaining should be j2 (VirtualMediaInsert) and j4 (PowerResetWithWait for ns2/vm2)
	popped := pq.Pop()
	require.NotNil(t, popped)
	// Order may vary by priority; just ensure we have two jobs left
	other := pq.Pop()
	require.NotNil(t, other)
	assert.NotEqual(t, popped.ID, other.ID)
	assert.Nil(t, pq.Pop())
}

func TestCreatePowerResetTask_SupersedesPendingJob(t *testing.T) {
	// TaskManager with 0 workers so jobs are never consumed
	tm := NewTaskManager(0, nil)
	defer tm.Stop()

	// Call CreatePowerResetTask multiple times for the same VM. Each call must cancel any
	// pending power-reset job for that VM before creating a new one, so we never have more
	// than one such job in the queue. We don't rely on dispatcher timing.
	var taskIDs []string
	for i := 0; i < 3; i++ {
		id := tm.CreatePowerResetTask("Reset vm1", "default", "vm1", "On", "5m")
		require.NotEmpty(t, id)
		taskIDs = append(taskIDs, id)
	}

	// Exactly one task should still be Pending (the last one); the other two should be Exception (superseded)
	var pendingCount, exceptionCount int
	for _, id := range taskIDs {
		task, ok := tm.GetTask(id)
		require.True(t, ok)
		switch task.TaskState {
		case redfish.TaskStatePending:
			pendingCount++
		case redfish.TaskStateException:
			exceptionCount++
			assert.Contains(t, task.Messages[len(task.Messages)-1].Message, "Superseded by new reset request")
		}
	}
	assert.Equal(t, 1, pendingCount, "exactly one task should be Pending (the latest)")
	assert.Equal(t, 2, exceptionCount, "previous tasks should be superseded")

	// At most one job for this VM in the queue (dispatcher may be holding one with 0 workers)
	stats := tm.GetStats()
	queueSize, ok := stats["queue_size"].(int)
	require.True(t, ok)
	assert.LessOrEqual(t, queueSize, 1, "queue should have at most one job for this VM")
}

func TestCreatePowerResetTask_DoesNotSupersedeOtherVM(t *testing.T) {
	tm := NewTaskManager(0, nil)
	defer tm.Stop()

	taskID1 := tm.CreatePowerResetTask("Reset vm1", "default", "vm1", "On", "5m")
	taskID2 := tm.CreatePowerResetTask("Reset vm2", "default", "vm2", "On", "5m")

	require.NotEmpty(t, taskID1)
	require.NotEmpty(t, taskID2)

	// Both tasks should still be Pending (different VMs, neither superseded)
	task1, _ := tm.GetTask(taskID1)
	task2, _ := tm.GetTask(taskID2)
	assert.Equal(t, redfish.TaskStatePending, task1.TaskState)
	assert.Equal(t, redfish.TaskStatePending, task2.TaskState)

	// With 0 workers, both jobs stay queued or one may be in the dispatcher's hand
	stats := tm.GetStats()
	queueSize, ok := stats["queue_size"].(int)
	require.True(t, ok)
	assert.GreaterOrEqual(t, queueSize, 1, "at least one job should be queued")
}
