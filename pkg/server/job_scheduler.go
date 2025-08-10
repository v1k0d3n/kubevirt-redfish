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

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// ScheduledJob represents a job that runs on a schedule
type ScheduledJob struct {
	ID         string
	Name       string
	Schedule   time.Duration
	LastRun    time.Time
	NextRun    time.Time
	Handler    func() error
	Enabled    bool
	RetryCount int
	MaxRetries int
	RetryDelay time.Duration
}

// JobScheduler manages scheduled background jobs
type JobScheduler struct {
	jobs       map[string]*ScheduledJob
	mutex      sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	ticker     *time.Ticker
	stopChan   chan struct{}
	stats      *SchedulerStats
	statsMutex sync.RWMutex
}

// SchedulerStats tracks scheduler performance
type SchedulerStats struct {
	TotalJobsScheduled int64
	TotalJobsExecuted  int64
	TotalJobsFailed    int64
	LastReset          time.Time
}

// NewJobScheduler creates a new job scheduler
func NewJobScheduler() *JobScheduler {
	ctx, cancel := context.WithCancel(context.Background())

	scheduler := &JobScheduler{
		jobs:     make(map[string]*ScheduledJob),
		ctx:      ctx,
		cancel:   cancel,
		stopChan: make(chan struct{}),
		stats: &SchedulerStats{
			LastReset: time.Now(),
		},
	}

	// Start the scheduler
	go scheduler.run()

	return scheduler
}

// AddJob adds a new scheduled job
func (js *JobScheduler) AddJob(id, name string, schedule time.Duration, handler func() error) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	if _, exists := js.jobs[id]; exists {
		return fmt.Errorf("job with ID %s already exists", id)
	}

	job := &ScheduledJob{
		ID:         id,
		Name:       name,
		Schedule:   schedule,
		NextRun:    time.Now().Add(schedule),
		Handler:    handler,
		Enabled:    true,
		MaxRetries: 3,
		RetryDelay: 5 * time.Second,
	}

	js.jobs[id] = job
	js.updateScheduledStats() // Job scheduled

	logger.Info("Added scheduled job %s (%s) with schedule %v", id, name, schedule)
	return nil
}

// RemoveJob removes a scheduled job
func (js *JobScheduler) RemoveJob(id string) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	if _, exists := js.jobs[id]; !exists {
		return fmt.Errorf("job with ID %s not found", id)
	}

	delete(js.jobs, id)
	logger.Info("Removed scheduled job %s", id)
	return nil
}

// EnableJob enables a scheduled job
func (js *JobScheduler) EnableJob(id string) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	job, exists := js.jobs[id]
	if !exists {
		return fmt.Errorf("job with ID %s not found", id)
	}

	job.Enabled = true
	job.NextRun = time.Now().Add(job.Schedule)
	logger.Info("Enabled scheduled job %s", id)
	return nil
}

// DisableJob disables a scheduled job
func (js *JobScheduler) DisableJob(id string) error {
	js.mutex.Lock()
	defer js.mutex.Unlock()

	job, exists := js.jobs[id]
	if !exists {
		return fmt.Errorf("job with ID %s not found", id)
	}

	job.Enabled = false
	logger.Info("Disabled scheduled job %s", id)
	return nil
}

// GetJob returns a scheduled job by ID
func (js *JobScheduler) GetJob(id string) (*ScheduledJob, bool) {
	js.mutex.RLock()
	defer js.mutex.RUnlock()

	job, exists := js.jobs[id]
	return job, exists
}

// ListJobs returns all scheduled jobs
func (js *JobScheduler) ListJobs() []*ScheduledJob {
	js.mutex.RLock()
	defer js.mutex.RUnlock()

	jobs := make([]*ScheduledJob, 0, len(js.jobs))
	for _, job := range js.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

// run is the main scheduler loop
func (js *JobScheduler) run() {
	js.ticker = time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer js.ticker.Stop()

	for {
		select {
		case <-js.ticker.C:
			js.checkAndRunJobs()
		case <-js.ctx.Done():
			logger.Info("Job scheduler stopped")
			return
		case <-js.stopChan:
			logger.Info("Job scheduler stopped")
			return
		}
	}
}

// checkAndRunJobs checks for jobs that need to run and executes them
func (js *JobScheduler) checkAndRunJobs() {
	js.mutex.RLock()
	jobs := make([]*ScheduledJob, 0, len(js.jobs))
	for _, job := range js.jobs {
		if job.Enabled && time.Now().After(job.NextRun) {
			jobs = append(jobs, job)
		}
	}
	js.mutex.RUnlock()

	// Execute jobs that are due
	for _, job := range jobs {
		go js.executeJob(job)
	}
}

// executeJob executes a single job
func (js *JobScheduler) executeJob(job *ScheduledJob) {
	startTime := time.Now()

	logger.Info("Executing scheduled job %s (%s)", job.ID, job.Name)

	// Execute the job
	err := job.Handler()

	// Update job state
	js.mutex.Lock()
	job.LastRun = startTime
	job.NextRun = time.Now().Add(job.Schedule)

	if err != nil {
		logger.Error("Scheduled job %s failed: %v", job.ID, err)

		// Update failure stats immediately
		js.updateStats(time.Since(startTime), false)

		// Handle retries
		if job.RetryCount < job.MaxRetries {
			job.RetryCount++
			logger.Info("Retrying scheduled job %s (attempt %d/%d)", job.ID, job.RetryCount, job.MaxRetries)

			// Schedule retry
			retryDelay := job.RetryDelay * time.Duration(job.RetryCount)
			job.NextRun = time.Now().Add(retryDelay)
		} else {
			logger.Error("Scheduled job %s failed after %d retries", job.ID, job.MaxRetries)
		}
	} else {
		job.RetryCount = 0 // Reset retry count on success
		logger.Info("Scheduled job %s completed successfully in %v", job.ID, time.Since(startTime))
		js.updateStats(time.Since(startTime), true)
	}
	js.mutex.Unlock()
}

// updateStats updates scheduler statistics
func (js *JobScheduler) updateStats(duration time.Duration, success bool) {
	js.statsMutex.Lock()
	defer js.statsMutex.Unlock()

	if success {
		js.stats.TotalJobsExecuted++
	} else {
		js.stats.TotalJobsFailed++
	}
}

// updateScheduledStats updates the scheduled jobs count
func (js *JobScheduler) updateScheduledStats() {
	js.statsMutex.Lock()
	defer js.statsMutex.Unlock()
	js.stats.TotalJobsScheduled++
}

// GetStats returns scheduler statistics
func (js *JobScheduler) GetStats() map[string]interface{} {
	js.statsMutex.RLock()
	defer js.statsMutex.RUnlock()

	js.mutex.RLock()
	activeJobs := len(js.jobs)
	js.mutex.RUnlock()

	return map[string]interface{}{
		"total_jobs_scheduled": js.stats.TotalJobsScheduled,
		"total_jobs_executed":  js.stats.TotalJobsExecuted,
		"total_jobs_failed":    js.stats.TotalJobsFailed,
		"active_jobs":          activeJobs,
		"uptime":               time.Since(js.stats.LastReset).String(),
	}
}

// Stop gracefully stops the job scheduler
func (js *JobScheduler) Stop() {
	logger.Info("Stopping job scheduler...")

	if js.ticker != nil {
		js.ticker.Stop()
	}

	// Use select to avoid closing already closed channel
	select {
	case <-js.stopChan:
		// Channel already closed
	default:
		close(js.stopChan)
	}

	js.cancel()

	js.mutex.Lock()
	jobCount := len(js.jobs)
	js.jobs = make(map[string]*ScheduledJob)
	js.mutex.Unlock()

	logger.Info("Job scheduler stopped, cleared %d jobs", jobCount)
}

// AddDefaultJobs adds common background jobs for the Redfish API
func (js *JobScheduler) AddDefaultJobs(server *Server) error {
	// Cache cleanup job
	err := js.AddJob(
		"cache-cleanup",
		"Cache Cleanup",
		10*time.Minute, // Every 10 minutes
		func() error {
			if server.responseCache != nil {
				// Trigger cache cleanup
				server.responseCache.cleanupExpired()
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add cache cleanup job: %w", err)
	}

	// Task cleanup job
	err = js.AddJob(
		"task-cleanup",
		"Task Cleanup",
		1*time.Hour, // Every hour
		func() error {
			if server.enhancedTaskManager != nil {
				server.enhancedTaskManager.CleanupOldTasks(24 * time.Hour)
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add task cleanup job: %w", err)
	}

	// Health check job
	err = js.AddJob(
		"health-check",
		"Health Check",
		5*time.Minute, // Every 5 minutes
		func() error {
			// Perform health checks
			if server.kubevirtClient != nil {
				if err := server.kubevirtClient.TestConnection(); err != nil {
					logger.Warning("Health check failed: KubeVirt connection error: %v", err)
					return err
				}
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to add health check job: %w", err)
	}

	logger.Info("Added default background jobs")
	return nil
}
