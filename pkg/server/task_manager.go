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
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/kubevirt"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

// TaskPriority represents the priority level of a task
type TaskPriority int

const (
	PriorityLow TaskPriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

// TaskType represents the type of task
type TaskType string

const (
	TaskTypeVirtualMediaInsert TaskType = "virtual_media_insert"
	TaskTypeVirtualMediaEject  TaskType = "virtual_media_eject"
	TaskTypePowerAction        TaskType = "power_action"
	TaskTypePowerResetWithWait TaskType = "power_reset_with_wait" // Reset that waits for ISO to be ready
	TaskTypeBootUpdate         TaskType = "boot_update"
	TaskTypeSystemMaintenance  TaskType = "system_maintenance"
)

// Job represents a work item to be processed by a worker
type Job struct {
	ID         string
	TaskID     string
	Type       TaskType
	Priority   TaskPriority
	Payload    interface{}
	CreatedAt  time.Time
	RetryCount int
	MaxRetries int
	RetryDelay time.Duration
}

// TaskManager provides advanced task management with job queuing and worker pools
type TaskManager struct {
	// Task management
	tasks     map[string]*TaskInfo
	taskMutex sync.RWMutex

	// Job queue management
	jobNotify     chan struct{} // Notifies dispatcher when new jobs are available
	priorityQueue *PriorityQueue

	// Worker pool
	workers         []*Worker
	workerCount     int
	workerMutex     sync.RWMutex
	lastWorkerIndex int // For round-robin distribution
	lastWorkerMutex sync.Mutex

	// KubeVirt client for actual operations
	kubevirtClient *kubevirt.Client

	// Configuration
	ctx      context.Context
	cancel   context.CancelFunc
	cleanup  *time.Ticker
	stopChan chan struct{}

	// Statistics
	stats      *TaskStats
	statsMutex sync.RWMutex
}

// TaskStats tracks task manager performance statistics
type TaskStats struct {
	TotalTasksCreated     int64
	TotalTasksCompleted   int64
	TotalTasksFailed      int64
	ActiveTasks           int64
	QueueSize             int64
	AverageProcessingTime time.Duration
	TotalProcessingTime   time.Duration
	LastReset             time.Time
}

// PriorityQueue implements a priority queue for jobs
type PriorityQueue struct {
	jobs  []*Job
	mutex sync.RWMutex
}

// NewPriorityQueue creates a new priority queue
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		jobs: make([]*Job, 0),
	}
}

// Push adds a job to the priority queue
func (pq *PriorityQueue) Push(job *Job) {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()

	pq.jobs = append(pq.jobs, job)

	// Sort by priority (higher priority first)
	for i := len(pq.jobs) - 1; i > 0; i-- {
		if pq.jobs[i].Priority > pq.jobs[i-1].Priority {
			pq.jobs[i], pq.jobs[i-1] = pq.jobs[i-1], pq.jobs[i]
		}
	}
}

// Pop removes and returns the highest priority job
func (pq *PriorityQueue) Pop() *Job {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()

	if len(pq.jobs) == 0 {
		return nil
	}

	job := pq.jobs[0]
	pq.jobs = pq.jobs[1:]
	return job
}

// Size returns the number of jobs in the queue
func (pq *PriorityQueue) Size() int {
	pq.mutex.RLock()
	defer pq.mutex.RUnlock()
	return len(pq.jobs)
}

// RemoveMatching removes all jobs for which predicate returns true, and returns them.
// The remaining jobs keep their relative order (and thus priority ordering).
// Caller must hold no locks that could conflict with pq.mutex (e.g. do not hold taskMutex
// if another goroutine might lock queue then taskMutex).
func (pq *PriorityQueue) RemoveMatching(predicate func(*Job) bool) []*Job {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()

	kept := make([]*Job, 0, len(pq.jobs))
	var removed []*Job
	for _, j := range pq.jobs {
		if predicate(j) {
			removed = append(removed, j)
		} else {
			kept = append(kept, j)
		}
	}
	pq.jobs = kept
	return removed
}

// Worker represents a background worker that processes jobs
type Worker struct {
	ID      int
	taskMgr *TaskManager
	jobChan chan *Job
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewWorker creates a new worker
func NewWorker(id int, taskMgr *TaskManager) *Worker {
	ctx, cancel := context.WithCancel(taskMgr.ctx)
	return &Worker{
		ID:      id,
		taskMgr: taskMgr,
		jobChan: make(chan *Job, 10), // Buffer for each worker
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start starts the worker
func (w *Worker) Start() {
	w.wg.Add(1)
	go w.work()
	logger.Debug("Started worker %d", w.ID)
}

// Stop stops the worker
func (w *Worker) Stop() {
	w.cancel()
	w.wg.Wait()
	logger.Debug("Stopped worker %d", w.ID)
}

// work is the main work loop for a worker
func (w *Worker) work() {
	logger.Debug("DEBUG: Worker %d starting work loop", w.ID)
	for {
		select {
		case <-w.ctx.Done():
			logger.Debug("DEBUG: Worker %d context cancelled, stopping work loop", w.ID)
			return
		case job := <-w.jobChan:
			if job == nil {
				logger.Debug("DEBUG: Worker %d received nil job, continuing", w.ID)
				continue
			}
			logger.Debug("DEBUG: Worker %d received job %s (task %s) from channel", w.ID, job.ID, job.TaskID)
			w.processJob(job)
		}
	}
}

// processJob processes a single job
func (w *Worker) processJob(job *Job) {
	// Check for nil job to prevent panic
	if job == nil {
		logger.Warning("Worker %d received nil job, skipping", w.ID)
		logger.Debug("DEBUG: Worker %d skipping nil job", w.ID)
		return
	}

	startTime := time.Now()
	logger.Debug("DEBUG: Worker %d starting to process job %s (task %s) at %v", w.ID, job.ID, job.TaskID, startTime)

	logger.Info("Worker %d processing job %s (task %s)", w.ID, job.ID, job.TaskID)

	// Update task state to running
	logger.Debug("DEBUG: Worker %d updating task %s state to Running", w.ID, job.TaskID)
	if err := w.taskMgr.UpdateTaskState(job.TaskID, redfish.TaskStateRunning, "OK", "Processing started"); err != nil {
		logger.Error("Failed to update task state for job %s: %v", job.ID, err)
	}

	// Process the job based on type
	var err error
	switch job.Type {
	case TaskTypeVirtualMediaInsert:
		logger.Debug("DEBUG: Worker %d processing VirtualMediaInsert job %s", w.ID, job.ID)
		err = w.processVirtualMediaInsert(job)
	case TaskTypeVirtualMediaEject:
		logger.Debug("DEBUG: Worker %d processing VirtualMediaEject job %s", w.ID, job.ID)
		err = w.processVirtualMediaEject(job)
	case TaskTypePowerAction:
		logger.Debug("DEBUG: Worker %d processing PowerAction job %s", w.ID, job.ID)
		err = w.processPowerAction(job)
	case TaskTypePowerResetWithWait:
		logger.Debug("DEBUG: Worker %d processing PowerResetWithWait job %s", w.ID, job.ID)
		err = w.processPowerResetWithWait(job)
	case TaskTypeBootUpdate:
		logger.Debug("DEBUG: Worker %d processing BootUpdate job %s", w.ID, job.ID)
		err = w.processBootUpdate(job)
	default:
		logger.Debug("DEBUG: Worker %d received unknown job type %s for job %s", w.ID, job.Type, job.ID)
		err = fmt.Errorf("unknown job type: %s", job.Type)
	}

	// Handle job completion
	duration := time.Since(startTime)
	logger.Debug("DEBUG: Worker %d job %s completed in %v with error: %v", w.ID, job.ID, duration, err)

	w.taskMgr.updateStats(duration, err == nil)

	if err != nil {
		logger.Error("Worker %d failed to process job %s: %v", w.ID, job.ID, err)
		logger.Debug("DEBUG: Worker %d job %s failed with error: %v", w.ID, job.ID, err)

		// Handle retries
		if job.RetryCount < job.MaxRetries {
			job.RetryCount++
			logger.Info("Retrying job %s (attempt %d/%d)", job.ID, job.RetryCount, job.MaxRetries)
			logger.Debug("DEBUG: Worker %d scheduling retry %d/%d for job %s", w.ID, job.RetryCount, job.MaxRetries, job.ID)

			// Schedule retry with exponential backoff
			retryDelay := job.RetryDelay * time.Duration(job.RetryCount)
			logger.Debug("DEBUG: Worker %d scheduling job %s retry in %v", w.ID, job.ID, retryDelay)
			time.AfterFunc(retryDelay, func() {
				logger.Debug("DEBUG: Worker %d retry timer fired for job %s, queueing for retry", w.ID, job.ID)
				w.taskMgr.QueueJob(job)
			})
		} else {
			logger.Debug("DEBUG: Worker %d job %s failed after %d retries, marking task as failed", w.ID, job.ID, job.MaxRetries)
			if taskErr := w.taskMgr.FailTask(job.TaskID, fmt.Sprintf("Job failed after %d retries: %v", job.MaxRetries, err)); taskErr != nil {
				logger.Error("Failed to mark task %s as failed: %v", job.TaskID, taskErr)
			}
		}
	} else {
		logger.Info("Worker %d completed job %s successfully in %v", w.ID, job.ID, duration)
		logger.Debug("DEBUG: Worker %d job %s completed successfully in %v", w.ID, job.ID, duration)
		if taskErr := w.taskMgr.CompleteTask(job.TaskID, "Job completed successfully"); taskErr != nil {
			logger.Error("Failed to mark task %s as completed: %v", job.TaskID, taskErr)
		}
	}
}

// processVirtualMediaInsert processes a virtual media insertion job
func (w *Worker) processVirtualMediaInsert(job *Job) error {
	logger.Debug("DEBUG: Worker %d starting virtual media insert job %s (task %s)", w.ID, job.ID, job.TaskID)

	payload, ok := job.Payload.(map[string]string)
	if !ok {
		logger.Debug("DEBUG: Worker %d received invalid payload type for job %s", w.ID, job.ID)
		return fmt.Errorf("invalid payload for virtual media insert job")
	}

	namespace := payload["namespace"]
	vmName := payload["vmName"]
	mediaID := payload["mediaID"]
	imageURL := payload["imageURL"]

	logger.Debug("DEBUG: Worker %d processing virtual media insert - namespace=%s, vmName=%s, mediaID=%s, imageURL=%s",
		w.ID, namespace, vmName, mediaID, imageURL)

	// Update progress
	logger.Debug("DEBUG: Worker %d updating task progress to 'Starting virtual media insertion'", w.ID)
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Starting virtual media insertion"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	// Perform the actual insertion using KubeVirt client
	logger.Debug("DEBUG: Worker %d calling kubevirtClient.InsertVirtualMedia", w.ID)
	err := w.taskMgr.kubevirtClient.InsertVirtualMedia(namespace, vmName, mediaID, imageURL)
	if err != nil {
		logger.Error("Failed to insert virtual media for VM %s/%s: %v", namespace, vmName, err)
		logger.Debug("DEBUG: Worker %d virtual media insertion failed for VM %s/%s: %v", w.ID, namespace, vmName, err)
		return fmt.Errorf("failed to insert virtual media: %w", err)
	}

	logger.Debug("DEBUG: Worker %d virtual media insertion completed successfully", w.ID)
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Virtual media insertion completed successfully"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	return nil
}

// processVirtualMediaEject processes a virtual media ejection job
func (w *Worker) processVirtualMediaEject(job *Job) error {
	payload, ok := job.Payload.(map[string]string)
	if !ok {
		return fmt.Errorf("invalid payload for virtual media eject job")
	}

	namespace := payload["namespace"]
	vmName := payload["vmName"]
	mediaID := payload["mediaID"]

	// Update progress
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Starting virtual media ejection"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	// Perform the actual ejection using KubeVirt client
	err := w.taskMgr.kubevirtClient.EjectVirtualMedia(namespace, vmName, mediaID)
	if err != nil {
		logger.Error("Failed to eject virtual media for VM %s/%s: %v", namespace, vmName, err)
		return fmt.Errorf("failed to eject virtual media: %w", err)
	}

	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Virtual media ejection completed successfully"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	return nil
}

// processPowerAction processes a power action job
func (w *Worker) processPowerAction(job *Job) error {
	payload, ok := job.Payload.(map[string]string)
	if !ok {
		return fmt.Errorf("invalid payload for power action job")
	}

	_ = payload["namespace"] // Will be used when integrating with KubeVirt client
	_ = payload["vmName"]    // Will be used when integrating with KubeVirt client
	_ = payload["action"]    // Will be used when integrating with KubeVirt client

	// Update progress
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, fmt.Sprintf("Executing power action: %s", payload["action"])); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	// Perform the power action
	time.Sleep(500 * time.Millisecond) // Simulate work

	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Power action completed"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	return nil
}

// processBootUpdate processes a boot update job
func (w *Worker) processBootUpdate(job *Job) error {
	payload, ok := job.Payload.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid payload for boot update job")
	}

	_ = payload // Will be used when integrating with KubeVirt client

	// Update progress
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Updating boot configuration"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	// Perform the boot update
	time.Sleep(1 * time.Second) // Simulate work

	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Boot configuration updated"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	return nil
}

// processPowerResetWithWait processes a power reset job that waits for virtual media (ISO) to be ready
// This is used when the client requests a Reset but the ISO is still being downloaded
func (w *Worker) processPowerResetWithWait(job *Job) error {
	payload, ok := job.Payload.(map[string]string)
	if !ok {
		return fmt.Errorf("invalid payload for power reset job")
	}

	namespace := payload["namespace"]
	vmName := payload["vmName"]
	resetType := payload["resetType"]
	isoDownloadTimeout := payload["isoDownloadTimeout"]

	// Parse the ISO download timeout from the payload
	maxWaitTime, err := time.ParseDuration(isoDownloadTimeout)
	if err != nil {
		return fmt.Errorf("invalid or missing isoDownloadTimeout in payload: %w", err)
	}
	retryInterval := 10 * time.Second

	// Update progress
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, "Waiting for virtual media (ISO) to be ready"); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	startTime := time.Now()

	// Wait for virtual media to be ready
	for {
		ready, message, err := w.taskMgr.kubevirtClient.IsVirtualMediaReady(namespace, vmName)
		if err != nil {
			// Log warning and continue retrying - transient API errors should not cause premature boots
			logger.Warning("Failed to check virtual media status for VM %s/%s: %v (will retry)", namespace, vmName, err)
		} else if ready {
			elapsed := time.Since(startTime)
			if elapsed > time.Second {
				logger.Info("Virtual media is now ready for VM %s/%s after waiting %v", namespace, vmName, elapsed)
				if err := w.taskMgr.UpdateTaskProgress(job.TaskID, fmt.Sprintf("Virtual media ready after %v", elapsed.Round(time.Second))); err != nil {
					logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
				}
			}
			break
		}

		elapsed := time.Since(startTime)

		// Check if we've exceeded the maximum wait time
		if elapsed >= maxWaitTime {
			logger.Error("Timeout waiting for virtual media to be ready for VM %s/%s after %v: %s", namespace, vmName, elapsed, message)
			return fmt.Errorf("timeout waiting for ISO download after %v: %s", elapsed, message)
		}

		// Log progress
		logger.Info("Waiting for virtual media to be ready for VM %s/%s (elapsed: %v, max: %v): %s",
			namespace, vmName, elapsed.Round(time.Second), maxWaitTime, message)

		if err := w.taskMgr.UpdateTaskProgress(job.TaskID, fmt.Sprintf("Waiting for ISO (elapsed: %v)", elapsed.Round(time.Second))); err != nil {
			logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
		}

		time.Sleep(retryInterval)
	}

	// Now execute the power action
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, fmt.Sprintf("Executing power action: %s", resetType)); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	err = w.taskMgr.kubevirtClient.SetVMPowerState(namespace, vmName, resetType)
	if err != nil {
		logger.Error("Failed to set power state %s for VM %s/%s: %v", resetType, namespace, vmName, err)
		return fmt.Errorf("failed to execute power action %s: %w", resetType, err)
	}

	logger.Info("Successfully executed power action %s for VM %s/%s", resetType, namespace, vmName)
	if err := w.taskMgr.UpdateTaskProgress(job.TaskID, fmt.Sprintf("Power action %s completed successfully", resetType)); err != nil {
		logger.Error("Failed to update task progress for job %s: %v", job.ID, err)
	}

	return nil
}

// NewTaskManager creates a new task manager
func NewTaskManager(workerCount int, kubevirtClient *kubevirt.Client) *TaskManager {
	ctx, cancel := context.WithCancel(context.Background())

	tm := &TaskManager{
		tasks:          make(map[string]*TaskInfo),
		jobNotify:      make(chan struct{}, 1), // Buffered channel to notify dispatcher
		priorityQueue:  NewPriorityQueue(),
		workerCount:    workerCount,
		kubevirtClient: kubevirtClient,
		ctx:            ctx,
		cancel:         cancel,
		stopChan:       make(chan struct{}),
		stats: &TaskStats{
			LastReset: time.Now(),
		},
	}

	// Start workers
	tm.startWorkers()

	// Start job dispatcher
	go tm.jobDispatcher()

	// Start cleanup routine
	tm.StartCleanupRoutine()

	return tm
}

// startWorkers starts the worker pool
func (tm *TaskManager) startWorkers() {
	tm.workerMutex.Lock()
	defer tm.workerMutex.Unlock()

	tm.workers = make([]*Worker, tm.workerCount)
	for i := 0; i < tm.workerCount; i++ {
		worker := NewWorker(i+1, tm)
		tm.workers[i] = worker
		worker.Start()
	}

	logger.Info("Started %d workers", tm.workerCount)
}

// jobDispatcher dispatches jobs from the priority queue to available workers
// It uses channel-based notification instead of polling for better efficiency
func (tm *TaskManager) jobDispatcher() {
	logger.Debug("DEBUG: Starting job dispatcher")

	for {
		select {
		case <-tm.ctx.Done():
			logger.Debug("DEBUG: Job dispatcher context cancelled, stopping")
			return
		case <-tm.stopChan:
			logger.Debug("DEBUG: Job dispatcher stop signal received, stopping")
			return
		case <-tm.jobNotify:
			// Dispatch all available jobs when notified
			tm.dispatchAvailableJobs()
		}
	}
}

// dispatchAvailableJobs dispatches all available jobs to workers using round-robin
func (tm *TaskManager) dispatchAvailableJobs() {
	for {
		// Get next job from priority queue
		job := tm.priorityQueue.Pop()
		if job == nil {
			return // No more jobs in queue
		}

		logger.Debug("DEBUG: Job dispatcher popped job %s (task %s) from queue", job.ID, job.TaskID)

		// Find available worker using round-robin
		worker := tm.getAvailableWorker()
		if worker != nil {
			logger.Debug("DEBUG: Job dispatcher found available worker %d for job %s", worker.ID, job.ID)
			select {
			case worker.jobChan <- job:
				logger.Debug("DEBUG: Job dispatcher successfully assigned job %s to worker %d", job.ID, worker.ID)
				tm.updateQueueStats(-1)
			default:
				// Worker channel is full, put job back and schedule retry
				logger.Debug("DEBUG: Job dispatcher worker %d channel is full, scheduling retry for job %s", worker.ID, job.ID)
				tm.priorityQueue.Push(job)
				tm.scheduleDispatchRetry()
				return
			}
		} else {
			// No available workers, put job back and schedule retry
			logger.Debug("DEBUG: Job dispatcher no available workers, scheduling retry for job %s", job.ID)
			tm.priorityQueue.Push(job)
			tm.scheduleDispatchRetry()
			return
		}
	}
}

// scheduleDispatchRetry schedules a retry notification when workers are busy
func (tm *TaskManager) scheduleDispatchRetry() {
	time.AfterFunc(10*time.Millisecond, func() {
		select {
		case tm.jobNotify <- struct{}{}:
			logger.Debug("DEBUG: Scheduled dispatch retry notification sent")
		default:
			// Already notified
		}
	})
}

// getAvailableWorker returns an available worker using round-robin distribution
// This ensures jobs are distributed evenly across all workers instead of always
// assigning to the first available worker
func (tm *TaskManager) getAvailableWorker() *Worker {
	tm.workerMutex.RLock()
	defer tm.workerMutex.RUnlock()

	if len(tm.workers) == 0 {
		return nil
	}

	// Use round-robin: start from the last assigned worker index
	tm.lastWorkerMutex.Lock()
	startIndex := tm.lastWorkerIndex
	tm.lastWorkerMutex.Unlock()

	// Try all workers starting from the last assigned one (round-robin)
	for i := 0; i < len(tm.workers); i++ {
		index := (startIndex + i) % len(tm.workers)
		worker := tm.workers[index]

		// Check if worker's job channel has capacity
		if len(worker.jobChan) < cap(worker.jobChan) {
			// Update last assigned index for next round-robin
			tm.lastWorkerMutex.Lock()
			tm.lastWorkerIndex = (index + 1) % len(tm.workers)
			tm.lastWorkerMutex.Unlock()

			logger.Debug("DEBUG: Selected worker %d for round-robin distribution (started from index %d)", worker.ID, startIndex)
			return worker
		}
	}

	return nil
}

// CreateTask creates a new task and queues it for processing
func (tm *TaskManager) CreateTask(name, namespace, vmName, mediaID, imageURL string) string {
	logger.Debug("DEBUG: Creating task for virtual media insertion - name=%s, namespace=%s, vmName=%s, mediaID=%s, imageURL=%s",
		name, namespace, vmName, mediaID, imageURL)

	tm.taskMutex.Lock()
	defer tm.taskMutex.Unlock()

	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	dataVolumeName := fmt.Sprintf("%s-bootiso", vmName)

	logger.Debug("DEBUG: Generated taskID=%s, dataVolumeName=%s", taskID, dataVolumeName)

	task := &TaskInfo{
		ID:             taskID,
		Name:           name,
		TaskState:      redfish.TaskStatePending,
		TaskStatus:     "OK",
		StartTime:      time.Now(),
		Namespace:      namespace,
		VMName:         vmName,
		MediaID:        mediaID,
		ImageURL:       imageURL,
		DataVolumeName: dataVolumeName,
		Messages: []redfish.Message{
			{
				Message: fmt.Sprintf("Created task for virtual media insertion %s", mediaID),
			},
		},
	}

	tm.tasks[taskID] = task
	logger.Debug("DEBUG: Stored task %s in task map", taskID)

	// Create and queue job
	job := &Job{
		ID:         fmt.Sprintf("job-%d", time.Now().UnixNano()),
		TaskID:     taskID,
		Type:       TaskTypeVirtualMediaInsert,
		Priority:   PriorityNormal,
		CreatedAt:  time.Now(),
		MaxRetries: 3,
		RetryDelay: 5 * time.Second,
		Payload: map[string]string{
			"namespace": namespace,
			"vmName":    vmName,
			"mediaID":   mediaID,
			"imageURL":  imageURL,
		},
	}

	logger.Debug("DEBUG: Created job %s for task %s", job.ID, taskID)

	tm.QueueJob(job)
	logger.Debug("DEBUG: Queued job %s to priority queue", job.ID)

	tm.updateStats(0, true) // Task created

	logger.Debug("DEBUG: Updated stats for job %s", job.ID)

	logger.Info("Created task %s and queued job %s for virtual media insertion", taskID, job.ID)
	logger.Debug("DEBUG: Task creation complete - taskID=%s, jobID=%s", taskID, job.ID)

	return taskID
}

// CreatePowerResetTask creates a new task for power reset that waits for ISO to be ready
// This is used when a Reset is requested but the virtual media (ISO) is still downloading.
// Any existing pending power-reset job for the same VM is removed and its task marked as superseded.
func (tm *TaskManager) CreatePowerResetTask(name, namespace, vmName, resetType, isoDownloadTimeout string) string {
	logger.Debug("DEBUG: Creating power reset task - name=%s, namespace=%s, vmName=%s, resetType=%s, isoDownloadTimeout=%s",
		name, namespace, vmName, resetType, isoDownloadTimeout)

	tm.taskMutex.Lock()
	defer tm.taskMutex.Unlock()

	tm.cancelPendingPowerResetJobsForVM(namespace, vmName)

	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())

	logger.Debug("DEBUG: Generated taskID=%s for power reset", taskID)

	task := &TaskInfo{
		ID:         taskID,
		Name:       name,
		TaskState:  redfish.TaskStatePending,
		TaskStatus: "OK",
		StartTime:  time.Now(),
		Namespace:  namespace,
		VMName:     vmName,
		Messages: []redfish.Message{
			{
				Message: fmt.Sprintf("Created task for power reset %s (waiting for ISO)", resetType),
			},
		},
	}

	tm.tasks[taskID] = task
	logger.Debug("DEBUG: Stored power reset task %s in task map", taskID)

	// Create and queue job
	job := &Job{
		ID:         fmt.Sprintf("job-%d", time.Now().UnixNano()),
		TaskID:     taskID,
		Type:       TaskTypePowerResetWithWait,
		Priority:   PriorityHigh, // Power actions should have high priority
		CreatedAt:  time.Now(),
		MaxRetries: 1, // Don't retry power actions multiple times
		RetryDelay: 5 * time.Second,
		Payload: map[string]string{
			"namespace":          namespace,
			"vmName":             vmName,
			"resetType":          resetType,
			"isoDownloadTimeout": isoDownloadTimeout,
		},
	}

	logger.Debug("DEBUG: Created job %s for power reset task %s", job.ID, taskID)

	tm.QueueJob(job)
	logger.Debug("DEBUG: Queued power reset job %s to priority queue", job.ID)

	tm.updateStats(0, true) // Task created

	logger.Info("Created task %s and queued job %s for power reset (waiting for ISO)", taskID, job.ID)

	return taskID
}

// GetTask retrieves a task by ID
func (tm *TaskManager) GetTask(taskID string) (*TaskInfo, bool) {
	tm.taskMutex.RLock()
	defer tm.taskMutex.RUnlock()

	task, exists := tm.tasks[taskID]
	return task, exists
}

// UpdateTaskState updates the state of a task
func (tm *TaskManager) UpdateTaskState(taskID, state, status string, message string) error {
	tm.taskMutex.Lock()
	defer tm.taskMutex.Unlock()

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

		// Update active task count
		if state == redfish.TaskStateCompleted {
			tm.updateStats(0, true) // Task completed
		} else {
			tm.updateStats(0, false) // Task failed
		}
	}

	logger.Info("Updated task %s state to %s: %s", taskID, state, message)
	return nil
}

// UpdateTaskProgress updates the task with progress information
func (tm *TaskManager) UpdateTaskProgress(taskID, message string) error {
	tm.taskMutex.Lock()
	defer tm.taskMutex.Unlock()

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

// CompleteTask marks a task as completed
func (tm *TaskManager) CompleteTask(taskID, finalMessage string) error {
	return tm.UpdateTaskState(taskID, redfish.TaskStateCompleted, "OK", finalMessage)
}

// FailTask marks a task as failed
func (tm *TaskManager) FailTask(taskID, errorMessage string) error {
	return tm.UpdateTaskState(taskID, redfish.TaskStateException, "Warning", errorMessage)
}

// failTaskLocked marks a task as failed. Caller must hold tm.taskMutex.
func (tm *TaskManager) failTaskLocked(taskID, errorMessage string) error {
	task, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}
	task.TaskState = redfish.TaskStateException
	task.TaskStatus = "Warning"
	if errorMessage != "" {
		task.Messages = append(task.Messages, redfish.Message{Message: errorMessage})
	}
	now := time.Now()
	task.EndTime = &now
	tm.updateStats(0, false)
	logger.Info("Updated task %s state to Exception: %s", taskID, errorMessage)
	return nil
}

// cancelPendingPowerResetJobsForVM removes any pending PowerResetWithWait jobs for the given VM
// from the queue and marks their tasks as failed (superseded). Caller must hold tm.taskMutex.
func (tm *TaskManager) cancelPendingPowerResetJobsForVM(namespace, vmName string) {
	removed := tm.priorityQueue.RemoveMatching(func(job *Job) bool {
		if job.Type != TaskTypePowerResetWithWait {
			return false
		}
		payload, ok := job.Payload.(map[string]string)
		if !ok {
			return false
		}
		return payload["namespace"] == namespace && payload["vmName"] == vmName
	})
	for _, job := range removed {
		_ = tm.failTaskLocked(job.TaskID, "Superseded by new reset request")
		tm.updateQueueStats(-1)
	}
	if len(removed) > 0 {
		logger.Info("Cancelled %d pending power reset job(s) for VM %s/%s", len(removed), namespace, vmName)
	}
}

// updateStats updates task statistics
func (tm *TaskManager) updateStats(duration time.Duration, success bool) {
	tm.statsMutex.Lock()
	defer tm.statsMutex.Unlock()

	if duration > 0 {
		tm.stats.TotalProcessingTime += duration
		tm.stats.AverageProcessingTime = tm.stats.TotalProcessingTime / time.Duration(tm.stats.TotalTasksCompleted+tm.stats.TotalTasksFailed)
	}

	if success {
		tm.stats.TotalTasksCompleted++
	} else {
		tm.stats.TotalTasksFailed++
	}
}

// updateQueueStats updates queue statistics
func (tm *TaskManager) updateQueueStats(delta int64) {
	tm.statsMutex.Lock()
	defer tm.statsMutex.Unlock()
	tm.stats.QueueSize += delta
}

// QueueJob adds a job to the priority queue and notifies the dispatcher
func (tm *TaskManager) QueueJob(job *Job) {
	tm.priorityQueue.Push(job)
	tm.updateQueueStats(1)
	// Notify dispatcher without blocking (using select with default)
	select {
	case tm.jobNotify <- struct{}{}:
		logger.Debug("DEBUG: Notified dispatcher about new job %s", job.ID)
	default:
		// Channel already has a pending notification, dispatcher will process all available jobs
		logger.Debug("DEBUG: Dispatcher already notified, job %s will be processed", job.ID)
	}
}

// GetStats returns task manager statistics
func (tm *TaskManager) GetStats() map[string]interface{} {
	tm.statsMutex.RLock()
	defer tm.statsMutex.RUnlock()

	tm.taskMutex.RLock()
	activeTasks := int64(len(tm.tasks))
	tm.taskMutex.RUnlock()

	return map[string]interface{}{
		"total_tasks_created":     tm.stats.TotalTasksCreated,
		"total_tasks_completed":   tm.stats.TotalTasksCompleted,
		"total_tasks_failed":      tm.stats.TotalTasksFailed,
		"active_tasks":            activeTasks,
		"queue_size":              tm.priorityQueue.Size(),
		"worker_count":            tm.workerCount,
		"average_processing_time": tm.stats.AverageProcessingTime.String(),
		"uptime":                  time.Since(tm.stats.LastReset).String(),
	}
}

// CleanupOldTasks removes completed tasks older than the specified duration
func (tm *TaskManager) CleanupOldTasks(maxAge time.Duration) {
	tm.taskMutex.Lock()
	defer tm.taskMutex.Unlock()

	now := time.Now()
	count := 0
	for taskID, task := range tm.tasks {
		if task.EndTime != nil && now.Sub(*task.EndTime) > maxAge {
			delete(tm.tasks, taskID)
			count++
		}
	}

	if count > 0 {
		logger.Debug("Cleaned up %d old tasks", count)
	}
}

// StartCleanupRoutine starts a background routine to clean up old tasks
func (tm *TaskManager) StartCleanupRoutine() {
	tm.cleanup = time.NewTicker(1 * time.Hour)

	go func() {
		defer tm.cleanup.Stop()

		for {
			select {
			case <-tm.cleanup.C:
				tm.CleanupOldTasks(24 * time.Hour)
			case <-tm.ctx.Done():
				logger.Info("Enhanced task manager cleanup routine stopped")
				return
			case <-tm.stopChan:
				logger.Info("Enhanced task manager cleanup routine stopped")
				return
			}
		}
	}()

	logger.Info("Started enhanced task manager cleanup routine")
}

// Stop gracefully stops the enhanced task manager
func (tm *TaskManager) Stop() {
	logger.Info("Stopping enhanced task manager...")

	// Stop cleanup routine
	if tm.cleanup != nil {
		tm.cleanup.Stop()
	}

	// Signal stop
	close(tm.stopChan)

	// Cancel context
	tm.cancel()

	// Stop all workers
	tm.workerMutex.Lock()
	for _, worker := range tm.workers {
		worker.Stop()
	}
	tm.workerMutex.Unlock()

	// Clean up tasks
	tm.taskMutex.Lock()
	taskCount := len(tm.tasks)
	tm.tasks = make(map[string]*TaskInfo)
	tm.taskMutex.Unlock()

	logger.Info("Enhanced task manager stopped, cleaned up %d tasks", taskCount)
}
