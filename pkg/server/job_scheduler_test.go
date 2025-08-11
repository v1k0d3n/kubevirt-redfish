package server

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewJobScheduler(t *testing.T) {
	js := NewJobScheduler()
	if js == nil {
		t.Fatal("NewJobScheduler should not return nil")
	}

	if js.jobs == nil {
		t.Error("Jobs map should be initialized")
	}

	if js.ctx == nil {
		t.Error("Context should be initialized")
	}

	if js.stopChan == nil {
		t.Error("StopChan should be initialized")
	}

	if js.stats == nil {
		t.Error("Stats should be initialized")
	}

	if js.stats.LastReset.IsZero() {
		t.Error("LastReset should be set")
	}

	// Clean up
	js.Stop()
}

func TestJobScheduler_AddJob(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Test successful job addition
	err := js.AddJob("test-job", "Test Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify job was added
	job, exists := js.GetJob("test-job")
	if !exists {
		t.Error("Job should exist after adding")
	}

	if job.ID != "test-job" {
		t.Errorf("Expected job ID 'test-job', got '%s'", job.ID)
	}

	if job.Name != "Test Job" {
		t.Errorf("Expected job name 'Test Job', got '%s'", job.Name)
	}

	if job.Schedule != 1*time.Minute {
		t.Errorf("Expected schedule 1m, got %v", job.Schedule)
	}

	if !job.Enabled {
		t.Error("Job should be enabled by default")
	}

	if job.MaxRetries != 3 {
		t.Errorf("Expected max retries 3, got %d", job.MaxRetries)
	}

	if job.RetryDelay != 5*time.Second {
		t.Errorf("Expected retry delay 5s, got %v", job.RetryDelay)
	}

	// Test adding duplicate job
	err = js.AddJob("test-job", "Duplicate Job", 2*time.Minute, func() error { return nil })
	if err == nil {
		t.Error("Expected error when adding duplicate job")
	}
}

func TestJobScheduler_RemoveJob(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Add a job first
	err := js.AddJob("test-job", "Test Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Verify job exists
	if _, exists := js.GetJob("test-job"); !exists {
		t.Error("Job should exist before removal")
	}

	// Remove the job
	err = js.RemoveJob("test-job")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify job was removed
	if _, exists := js.GetJob("test-job"); exists {
		t.Error("Job should not exist after removal")
	}

	// Test removing non-existent job
	err = js.RemoveJob("non-existent")
	if err == nil {
		t.Error("Expected error when removing non-existent job")
	}
}

func TestJobScheduler_EnableDisableJob(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Add a job
	err := js.AddJob("test-job", "Test Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Verify job is enabled by default
	job, _ := js.GetJob("test-job")
	if !job.Enabled {
		t.Error("Job should be enabled by default")
	}

	// Disable the job
	err = js.DisableJob("test-job")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	job, _ = js.GetJob("test-job")
	if job.Enabled {
		t.Error("Job should be disabled")
	}

	// Enable the job
	err = js.EnableJob("test-job")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	job, _ = js.GetJob("test-job")
	if !job.Enabled {
		t.Error("Job should be enabled")
	}

	// Test enabling non-existent job
	err = js.EnableJob("non-existent")
	if err == nil {
		t.Error("Expected error when enabling non-existent job")
	}

	// Test disabling non-existent job
	err = js.DisableJob("non-existent")
	if err == nil {
		t.Error("Expected error when disabling non-existent job")
	}
}

func TestJobScheduler_GetJob(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Test getting non-existent job
	job, exists := js.GetJob("non-existent")
	if exists {
		t.Error("Should not exist")
	}
	if job != nil {
		t.Error("Should return nil for non-existent job")
	}

	// Add a job
	err := js.AddJob("test-job", "Test Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Get the job
	job, exists = js.GetJob("test-job")
	if !exists {
		t.Error("Job should exist")
	}
	if job == nil {
		t.Error("Should return job")
		return // Early return to prevent nil pointer dereference
	}

	if job.ID != "test-job" {
		t.Errorf("Expected job ID 'test-job', got '%s'", job.ID)
	}
}

func TestJobScheduler_ListJobs(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Initially should have no jobs
	jobs := js.ListJobs()
	if len(jobs) != 0 {
		t.Errorf("Expected 0 jobs, got %d", len(jobs))
	}

	// Add some jobs
	err := js.AddJob("job1", "Job 1", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job1: %v", err)
	}

	err = js.AddJob("job2", "Job 2", 2*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job2: %v", err)
	}

	// List jobs
	jobs = js.ListJobs()
	if len(jobs) != 2 {
		t.Errorf("Expected 2 jobs, got %d", len(jobs))
	}

	// Verify job IDs
	jobIDs := make(map[string]bool)
	for _, job := range jobs {
		jobIDs[job.ID] = true
	}

	if !jobIDs["job1"] {
		t.Error("Job1 should be in list")
	}
	if !jobIDs["job2"] {
		t.Error("Job2 should be in list")
	}
}

func TestJobScheduler_GetStats(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Get initial stats
	stats := js.GetStats()

	// Verify initial values
	if stats["total_jobs_scheduled"] != int64(0) {
		t.Errorf("Expected total_jobs_scheduled 0, got %v", stats["total_jobs_scheduled"])
	}

	if stats["total_jobs_executed"] != int64(0) {
		t.Errorf("Expected total_jobs_executed 0, got %v", stats["total_jobs_executed"])
	}

	if stats["total_jobs_failed"] != int64(0) {
		t.Errorf("Expected total_jobs_failed 0, got %v", stats["total_jobs_failed"])
	}

	if stats["active_jobs"] != 0 {
		t.Errorf("Expected active_jobs 0, got %v", stats["active_jobs"])
	}

	if stats["uptime"] == "" {
		t.Error("Uptime should not be empty")
	}

	// Add a job and check stats
	err := js.AddJob("test-job", "Test Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	stats = js.GetStats()
	if stats["active_jobs"] != 1 {
		t.Errorf("Expected active_jobs 1, got %v", stats["active_jobs"])
	}
}

func TestJobScheduler_Stop(t *testing.T) {
	js := NewJobScheduler()

	// Add a job
	err := js.AddJob("test-job", "Test Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Verify job exists
	if _, exists := js.GetJob("test-job"); !exists {
		t.Error("Job should exist before stop")
	}

	// Stop the scheduler
	js.Stop()

	// Verify jobs were cleared
	if _, exists := js.GetJob("test-job"); exists {
		t.Error("Job should not exist after stop")
	}

	// Test stopping again (should not panic)
	js.Stop()
}

func TestJobScheduler_ExecuteJob_Success(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	var executed bool
	var mu sync.Mutex

	// Add a job that succeeds
	err := js.AddJob("success-job", "Success Job", 1*time.Minute, func() error {
		mu.Lock()
		executed = true
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Get the job and manually execute it
	job, _ := js.GetJob("success-job")
	if job == nil {
		t.Fatal("Job should exist")
	}

	// Execute the job
	js.executeJob(job)

	// Wait a bit for execution
	time.Sleep(100 * time.Millisecond)

	// Verify job was executed
	mu.Lock()
	if !executed {
		t.Error("Job should have been executed")
	}
	mu.Unlock()

	// Verify retry count was reset
	if job.RetryCount != 0 {
		t.Errorf("Expected retry count 0, got %d", job.RetryCount)
	}

	// Check stats - should have 1 scheduled and 1 executed
	stats := js.GetStats()
	if stats["total_jobs_scheduled"] != int64(1) {
		t.Errorf("Expected total_jobs_scheduled 1, got %v", stats["total_jobs_scheduled"])
	}
	if stats["total_jobs_executed"] != int64(1) {
		t.Errorf("Expected total_jobs_executed 1, got %v", stats["total_jobs_executed"])
	}
}

func TestJobScheduler_ExecuteJob_Failure(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	testError := errors.New("test error")

	// Add a job that fails
	err := js.AddJob("fail-job", "Fail Job", 1*time.Minute, func() error {
		return testError
	})
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Get the job and manually execute it
	job, _ := js.GetJob("fail-job")
	if job == nil {
		t.Fatal("Job should exist")
	}

	// Execute the job
	js.executeJob(job)

	// Wait a bit for execution
	time.Sleep(100 * time.Millisecond)

	// Verify retry count was incremented
	if job.RetryCount != 1 {
		t.Errorf("Expected retry count 1, got %d", job.RetryCount)
	}

	// Check stats - should have 1 scheduled and 1 failed
	stats := js.GetStats()
	if stats["total_jobs_scheduled"] != int64(1) {
		t.Errorf("Expected total_jobs_scheduled 1, got %v", stats["total_jobs_scheduled"])
	}
	if stats["total_jobs_failed"] != int64(1) {
		t.Errorf("Expected total_jobs_failed 1, got %v", stats["total_jobs_failed"])
	}
}

func TestJobScheduler_ExecuteJob_MaxRetries(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	testError := errors.New("test error")

	// Add a job that always fails
	err := js.AddJob("retry-job", "Retry Job", 1*time.Minute, func() error {
		return testError
	})
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Get the job
	job, _ := js.GetJob("retry-job")
	if job == nil {
		t.Fatal("Job should exist")
	}

	// Execute the job multiple times to reach max retries
	for i := 0; i < job.MaxRetries+1; i++ {
		js.executeJob(job)
		time.Sleep(10 * time.Millisecond) // Small delay between executions
	}

	// Verify retry count reached max
	if job.RetryCount != job.MaxRetries {
		t.Errorf("Expected retry count %d, got %d", job.MaxRetries, job.RetryCount)
	}

	// Check stats - should have 1 scheduled and maxRetries+1 failed
	stats := js.GetStats()
	if stats["total_jobs_scheduled"] != int64(1) {
		t.Errorf("Expected total_jobs_scheduled 1, got %v", stats["total_jobs_scheduled"])
	}
	if stats["total_jobs_failed"] != int64(job.MaxRetries+1) {
		t.Errorf("Expected total_jobs_failed %d, got %v", job.MaxRetries+1, stats["total_jobs_failed"])
	}
}

func TestJobScheduler_CheckAndRunJobs(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	var executed bool
	var mu sync.Mutex

	// Add a job that should run immediately (past due)
	err := js.AddJob("immediate-job", "Immediate Job", 1*time.Millisecond, func() error {
		mu.Lock()
		executed = true
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to add job: %v", err)
	}

	// Get the job and set it to be due
	job, _ := js.GetJob("immediate-job")
	if job == nil {
		t.Fatal("Job should exist")
	}

	// Set the job to be due in the past
	job.NextRun = time.Now().Add(-1 * time.Second)

	// Manually trigger job check
	js.checkAndRunJobs()

	// Wait a bit for execution
	time.Sleep(100 * time.Millisecond)

	// Verify job was executed
	mu.Lock()
	if !executed {
		t.Error("Job should have been executed")
	}
	mu.Unlock()
}

func TestJobScheduler_AddDefaultJobs(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Create a mock server
	server := &Server{
		responseCache:  NewCache(100, 1*time.Hour),
		taskManager:    NewTaskManager(5, nil), // 5 workers, nil client for test
		kubevirtClient: nil,                    // Will be nil for this test
	}

	// Add default jobs
	err := js.AddDefaultJobs(server)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify default jobs were added
	expectedJobs := []string{"cache-cleanup", "task-cleanup", "health-check"}
	for _, jobID := range expectedJobs {
		if _, exists := js.GetJob(jobID); !exists {
			t.Errorf("Expected job %s to exist", jobID)
		}
	}

	// Verify job count
	jobs := js.ListJobs()
	if len(jobs) != len(expectedJobs) {
		t.Errorf("Expected %d jobs, got %d", len(expectedJobs), len(jobs))
	}
}

func TestJobScheduler_ConcurrentAccess(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	var wg sync.WaitGroup
	numGoroutines := 10

	// Test concurrent job additions
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			jobID := fmt.Sprintf("concurrent-job-%d", id)
			err := js.AddJob(jobID, "Concurrent Job", 1*time.Minute, func() error { return nil })
			if err != nil {
				t.Errorf("Failed to add job %s: %v", jobID, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all jobs were added
	jobs := js.ListJobs()
	if len(jobs) != numGoroutines {
		t.Errorf("Expected %d jobs, got %d", numGoroutines, len(jobs))
	}
}

func TestJobScheduler_EdgeCases(t *testing.T) {
	js := NewJobScheduler()
	defer js.Stop()

	// Test adding job with empty ID (should be allowed)
	err := js.AddJob("", "Empty ID Job", 1*time.Minute, func() error { return nil })
	if err != nil {
		t.Errorf("Expected no error for empty ID, got %v", err)
	}

	// Test adding job with nil handler (should be allowed)
	err = js.AddJob("nil-handler", "Nil Handler Job", 1*time.Minute, nil)
	if err != nil {
		t.Errorf("Expected no error for nil handler, got %v", err)
	}

	// Test adding job with zero schedule (should be allowed)
	err = js.AddJob("zero-schedule", "Zero Schedule Job", 0, func() error { return nil })
	if err != nil {
		t.Errorf("Expected no error for zero schedule, got %v", err)
	}

	// Test getting job with empty ID (should exist since we added it)
	job, exists := js.GetJob("")
	if !exists {
		t.Error("Should exist for empty ID since we added it")
	}
	if job == nil {
		t.Error("Should return job for empty ID")
	}
}
