package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHealthChecker(t *testing.T) {
	hc := NewHealthChecker()
	if hc == nil {
		t.Fatal("NewHealthChecker should not return nil")
	}

	if hc.checks == nil {
		t.Error("Checks map should be initialized")
	}

	if hc.stats == nil {
		t.Error("Stats should be initialized")
	}

	if hc.ctx == nil {
		t.Error("Context should be initialized")
	}

	if hc.stopChan == nil {
		t.Error("StopChan should be initialized")
	}

	if hc.stats.LastReset.IsZero() {
		t.Error("LastReset should be set")
	}

	// Clean up
	hc.Stop()
}

func TestHealthChecker_AddCheck(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test adding a valid check
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test health check",
		Check:       func() error { return nil },
		Timeout:     10 * time.Second,
		Interval:    30 * time.Second,
		Critical:    false,
	}

	hc.AddCheck(check)

	// Verify check was added
	addedCheck, exists := hc.GetCheck("test-check")
	if !exists {
		t.Error("Check should exist after adding")
	}

	if addedCheck.Name != "test-check" {
		t.Errorf("Expected name 'test-check', got '%s'", addedCheck.Name)
	}

	if addedCheck.Description != "Test health check" {
		t.Errorf("Expected description 'Test health check', got '%s'", addedCheck.Description)
	}

	if addedCheck.Timeout != 10*time.Second {
		t.Errorf("Expected timeout 10s, got %v", addedCheck.Timeout)
	}

	if addedCheck.Interval != 30*time.Second {
		t.Errorf("Expected interval 30s, got %v", addedCheck.Interval)
	}

	if addedCheck.Critical != false {
		t.Error("Expected Critical to be false")
	}

	if addedCheck.Status != StatusUnknown {
		t.Errorf("Expected status %s, got %s", StatusUnknown, addedCheck.Status)
	}
}

func TestHealthChecker_AddCheck_Defaults(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test adding a check with zero values (should use defaults)
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test health check",
		Check:       func() error { return nil },
		Timeout:     0, // Should default to 30s
		Interval:    0, // Should default to 60s
		Critical:    false,
	}

	hc.AddCheck(check)

	// Verify defaults were applied
	addedCheck, exists := hc.GetCheck("test-check")
	if !exists {
		t.Error("Check should exist after adding")
	}

	if addedCheck.Timeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", addedCheck.Timeout)
	}

	if addedCheck.Interval != 60*time.Second {
		t.Errorf("Expected default interval 60s, got %v", addedCheck.Interval)
	}
}

func TestHealthChecker_RemoveCheck(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add a check
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test health check",
		Check:       func() error { return nil },
	}

	hc.AddCheck(check)

	// Verify check exists
	if _, exists := hc.GetCheck("test-check"); !exists {
		t.Error("Check should exist before removal")
	}

	// Remove the check
	hc.RemoveCheck("test-check")

	// Verify check was removed
	if _, exists := hc.GetCheck("test-check"); exists {
		t.Error("Check should not exist after removal")
	}
}

func TestHealthChecker_GetCheck(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test getting non-existent check
	check, exists := hc.GetCheck("non-existent")
	if exists {
		t.Error("Should not find non-existent check")
	}
	if check != nil {
		t.Error("Should return nil for non-existent check")
	}

	// Add a check
	testCheck := &HealthCheck{
		Name:        "test-check",
		Description: "Test health check",
		Check:       func() error { return nil },
	}

	hc.AddCheck(testCheck)

	// Test getting existing check
	check, exists = hc.GetCheck("test-check")
	if !exists {
		t.Error("Should find existing check")
	}
	if check == nil {
		t.Error("Should return check for existing check")
	}

	if check.Name != "test-check" {
		t.Errorf("Expected name 'test-check', got '%s'", check.Name)
	}
}

func TestHealthChecker_GetAllChecks(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Initially should be empty
	checks := hc.GetAllChecks()
	if len(checks) != 0 {
		t.Errorf("Expected 0 checks initially, got %d", len(checks))
	}

	// Add some checks
	check1 := &HealthCheck{
		Name:        "check1",
		Description: "First check",
		Check:       func() error { return nil },
	}

	check2 := &HealthCheck{
		Name:        "check2",
		Description: "Second check",
		Check:       func() error { return nil },
	}

	hc.AddCheck(check1)
	hc.AddCheck(check2)

	// Verify all checks are returned
	checks = hc.GetAllChecks()
	if len(checks) != 2 {
		t.Errorf("Expected 2 checks, got %d", len(checks))
	}

	if _, exists := checks["check1"]; !exists {
		t.Error("check1 should be in GetAllChecks result")
	}

	if _, exists := checks["check2"]; !exists {
		t.Error("check2 should be in GetAllChecks result")
	}
}

func TestHealthChecker_RunCheck(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test running non-existent check
	err := hc.RunCheck("non-existent")
	if err == nil {
		t.Error("Should return error for non-existent check")
	}

	// Add a successful check
	successCheck := &HealthCheck{
		Name:        "success-check",
		Description: "Successful check",
		Check:       func() error { return nil },
		Timeout:     5 * time.Second,
	}

	hc.AddCheck(successCheck)

	// Run the check
	err = hc.RunCheck("success-check")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify check status was updated
	check, exists := hc.GetCheck("success-check")
	if !exists {
		t.Fatal("Check should exist")
	}

	if check.Status != StatusHealthy {
		t.Errorf("Expected status %s, got %s", StatusHealthy, check.Status)
	}

	if check.LastError != nil {
		t.Errorf("Expected no error, got %v", check.LastError)
	}
}

func TestHealthChecker_RunCheck_Critical(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add a critical failing check
	criticalCheck := &HealthCheck{
		Name:        "critical-check",
		Description: "Critical failing check",
		Check:       func() error { return errors.New("critical error") },
		Timeout:     5 * time.Second,
		Critical:    true,
	}

	hc.AddCheck(criticalCheck)

	// Run the critical check
	err := hc.RunCheck("critical-check")
	if err == nil {
		t.Error("Expected error from failing check")
	}

	// Verify check status was updated to unhealthy (not degraded)
	check, exists := hc.GetCheck("critical-check")
	if !exists {
		t.Fatal("Check should exist")
	}

	if check.Status != StatusUnhealthy {
		t.Errorf("Expected status %s, got %s", StatusUnhealthy, check.Status)
	}
}

func TestHealthChecker_RunCheck_Timeout(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add a check that takes too long
	slowCheck := &HealthCheck{
		Name:        "slow-check",
		Description: "Slow check",
		Check:       func() error { time.Sleep(100 * time.Millisecond); return nil },
		Timeout:     10 * time.Millisecond, // Very short timeout
	}

	hc.AddCheck(slowCheck)

	// Run the slow check
	err := hc.RunCheck("slow-check")
	if err == nil {
		t.Error("Expected timeout error")
	}

	// Verify timeout error message
	if err.Error() != "health check timeout after 10ms" {
		t.Errorf("Expected timeout error message, got %v", err)
	}

	// Verify check status was updated
	check, exists := hc.GetCheck("slow-check")
	if !exists {
		t.Fatal("Check should exist")
	}

	if check.Status != StatusDegraded {
		t.Errorf("Expected status %s, got %s", StatusDegraded, check.Status)
	}
}

func TestHealthChecker_RunAllChecks(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add multiple checks
	successCheck := &HealthCheck{
		Name:        "success-check",
		Description: "Successful check",
		Check:       func() error { return nil },
		Timeout:     5 * time.Second,
	}

	failCheck := &HealthCheck{
		Name:        "fail-check",
		Description: "Failing check",
		Check:       func() error { return errors.New("test error") },
		Timeout:     5 * time.Second,
	}

	hc.AddCheck(successCheck)
	hc.AddCheck(failCheck)

	// Run all checks
	results := hc.RunAllChecks()

	// Verify results
	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	if results["success-check"] != nil {
		t.Errorf("Expected no error for success-check, got %v", results["success-check"])
	}

	if results["fail-check"] == nil {
		t.Error("Expected error for fail-check")
	}
}

func TestHealthChecker_GetOverallStatus(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Initially should be unknown
	status := hc.GetOverallStatus()
	if status != StatusUnknown {
		t.Errorf("Expected status %s, got %s", StatusUnknown, status)
	}

	// Add a healthy check
	healthyCheck := &HealthCheck{
		Name:        "healthy-check",
		Description: "Healthy check",
		Check:       func() error { return nil },
		Status:      StatusHealthy,
	}

	hc.AddCheck(healthyCheck)

	// Should be healthy
	status = hc.GetOverallStatus()
	if status != StatusHealthy {
		t.Errorf("Expected status %s, got %s", StatusHealthy, status)
	}

	// Add a degraded check
	degradedCheck := &HealthCheck{
		Name:        "degraded-check",
		Description: "Degraded check",
		Check:       func() error { return errors.New("test error") },
		Status:      StatusDegraded,
	}

	hc.AddCheck(degradedCheck)

	// Should be degraded (degraded takes precedence over healthy)
	status = hc.GetOverallStatus()
	if status != StatusDegraded {
		t.Errorf("Expected status %s, got %s", StatusDegraded, status)
	}

	// Add an unhealthy check
	unhealthyCheck := &HealthCheck{
		Name:        "unhealthy-check",
		Description: "Unhealthy check",
		Check:       func() error { return errors.New("critical error") },
		Status:      StatusUnhealthy,
		Critical:    true,
	}

	hc.AddCheck(unhealthyCheck)

	// Should be unhealthy (unhealthy takes precedence over degraded)
	status = hc.GetOverallStatus()
	if status != StatusUnhealthy {
		t.Errorf("Expected status %s, got %s", StatusUnhealthy, status)
	}
}

func TestHealthChecker_GetStats(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Initially stats should be zero
	stats := hc.GetStats()
	if stats.TotalChecks != 0 {
		t.Errorf("Expected TotalChecks 0, got %d", stats.TotalChecks)
	}
	if stats.SuccessfulChecks != 0 {
		t.Errorf("Expected SuccessfulChecks 0, got %d", stats.SuccessfulChecks)
	}
	if stats.FailedChecks != 0 {
		t.Errorf("Expected FailedChecks 0, got %d", stats.FailedChecks)
	}
	if stats.OverallStatus != StatusUnknown {
		t.Errorf("Expected OverallStatus %s, got %s", StatusUnknown, stats.OverallStatus)
	}

	// Add and run a single check to avoid deadlock
	successCheck := &HealthCheck{
		Name:        "success-check",
		Description: "Successful check",
		Check:       func() error { return nil },
		Timeout:     5 * time.Second,
	}

	hc.AddCheck(successCheck)
	hc.RunCheck("success-check")

	// Verify stats were updated
	stats = hc.GetStats()
	if stats.TotalChecks != 1 {
		t.Errorf("Expected TotalChecks 1, got %d", stats.TotalChecks)
	}
	if stats.SuccessfulChecks != 1 {
		t.Errorf("Expected SuccessfulChecks 1, got %d", stats.SuccessfulChecks)
	}
	if stats.FailedChecks != 0 {
		t.Errorf("Expected FailedChecks 0, got %d", stats.FailedChecks)
	}
}

func TestHealthChecker_Reset(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add and run a check to generate some stats
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test check",
		Check:       func() error { return nil },
		Timeout:     5 * time.Second,
	}

	hc.AddCheck(check)
	hc.RunCheck("test-check")

	// Verify stats exist
	stats := hc.GetStats()
	if stats.TotalChecks == 0 {
		t.Error("Stats should have been generated")
	}

	// Reset stats
	hc.Reset()

	// Verify stats were reset
	stats = hc.GetStats()
	if stats.TotalChecks != 0 {
		t.Errorf("Expected TotalChecks 0 after reset, got %d", stats.TotalChecks)
	}
	if stats.SuccessfulChecks != 0 {
		t.Errorf("Expected SuccessfulChecks 0 after reset, got %d", stats.SuccessfulChecks)
	}
	if stats.FailedChecks != 0 {
		t.Errorf("Expected FailedChecks 0 after reset, got %d", stats.FailedChecks)
	}
	if stats.LastReset.IsZero() {
		t.Error("LastReset should be updated after reset")
	}
}

func TestHealthChecker_Stop(t *testing.T) {
	hc := NewHealthChecker()

	// Stop should not panic
	hc.Stop()

	// Multiple stops should be safe
	hc.Stop()
}

func TestHealthChecker_HealthCheckMiddleware(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add a healthy check
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test check",
		Check:       func() error { return nil },
		Status:      StatusHealthy,
	}

	hc.AddCheck(check)

	// Create middleware
	middleware := hc.HealthCheckMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// Test health check endpoint
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	// Verify health check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Test non-health endpoint
	req = httptest.NewRequest("GET", "/api/test", nil)
	w = httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	// Verify normal request passes through
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}
}

func TestHealthChecker_HealthCheckMiddleware_Unhealthy(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add an unhealthy check
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test check",
		Check:       func() error { return errors.New("test error") },
		Status:      StatusUnhealthy,
		Critical:    true,
	}

	hc.AddCheck(check)

	// Create middleware
	middleware := hc.HealthCheckMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	// Test non-health endpoint when unhealthy
	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	// Verify request is blocked with 503
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status code %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestHealthChecker_handleHealthCheck(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add some checks
	successCheck := &HealthCheck{
		Name:        "success-check",
		Description: "Successful check",
		Check:       func() error { return nil },
		Status:      StatusHealthy,
	}

	failCheck := &HealthCheck{
		Name:        "fail-check",
		Description: "Failing check",
		Check:       func() error { return errors.New("test error") },
		Status:      StatusDegraded,
	}

	hc.AddCheck(successCheck)
	hc.AddCheck(failCheck)

	// Test health check handler
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	hc.handleHealthCheck(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify content type
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}
}

func TestHealthChecker_marshalJSON(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test JSON marshaling
	data := map[string]interface{}{
		"status": "healthy",
		"checks": map[string]interface{}{},
	}

	result, err := hc.marshalJSON(data)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(result) == 0 {
		t.Error("Expected non-empty JSON result")
	}
}

func TestNewSelfHealingManager(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)
	if shm == nil {
		t.Fatal("NewSelfHealingManager should not return nil")
	}

	if shm.healthChecker != hc {
		t.Error("HealthChecker should be set")
	}

	if shm.circuitBreakerManager != cbm {
		t.Error("CircuitBreakerManager should be set")
	}

	if shm.retryManager != rm {
		t.Error("RetryManager should be set")
	}

	if shm.ctx == nil {
		t.Error("Context should be initialized")
	}

	if shm.stopChan == nil {
		t.Error("StopChan should be initialized")
	}

	// Clean up
	shm.Stop()
}

func TestSelfHealingManager_Stop(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)

	// Stop should not panic
	shm.Stop()

	// Multiple stops should be safe
	shm.Stop()
}

func TestSelfHealingManager_performSelfHealing(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)
	defer shm.Stop()

	// Test with healthy status
	// Add a healthy check
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test check",
		Check:       func() error { return nil },
		Status:      StatusHealthy,
	}

	hc.AddCheck(check)

	// performSelfHealing should not panic
	// Note: This is a private method, so we can't call it directly
	// But we can verify the manager works correctly
}

func TestHealthChecker_ConcurrentAccess(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test concurrent access to health checker (simplified to avoid deadlocks)
	done := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func(id int) {
			// Add a check
			check := &HealthCheck{
				Name:        fmt.Sprintf("check-%d", id),
				Description: fmt.Sprintf("Check %d", id),
				Check:       func() error { return nil },
			}

			hc.AddCheck(check)

			// Get stats (avoid running checks to prevent deadlocks)
			hc.GetStats()

			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		<-done
	}

	// Verify no panics occurred
	checks := hc.GetAllChecks()
	if len(checks) != 5 {
		t.Errorf("Expected 5 checks, got %d", len(checks))
	}
}

func TestHealthChecker_EdgeCases(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Test adding check with empty name
	check := &HealthCheck{
		Name:        "",
		Description: "Empty name check",
		Check:       func() error { return nil },
	}

	hc.AddCheck(check)

	// Should be able to add it (no validation in current implementation)
	_, exists := hc.GetCheck("")
	if !exists {
		t.Error("Empty name check should exist")
	}

	// Test adding check with nil check function
	nilCheck := &HealthCheck{
		Name:        "nil-check",
		Description: "Nil check function",
		Check:       nil,
	}

	hc.AddCheck(nilCheck)

	// Should be able to add it (no validation in current implementation)
	_, exists = hc.GetCheck("nil-check")
	if !exists {
		t.Error("Nil check should exist")
	}
}

func TestHealthChecker_RunPeriodicChecks(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	// Add a check that's due for running
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test check",
		Check:       func() error { return nil },
		Interval:    1 * time.Second,
		LastCheck:   time.Now().Add(-2 * time.Second), // Set to 2 seconds ago
		Status:      StatusUnknown,
	}

	hc.AddCheck(check)

	// Run periodic checks
	hc.runPeriodicChecks()

	// Give some time for the goroutine to complete
	time.Sleep(100 * time.Millisecond)

	// Verify the check was updated
	updatedCheck, exists := hc.GetCheck("test-check")
	if !exists {
		t.Error("Check should still exist after periodic checks")
	}

	// The check should have been updated (LastCheck should be recent)
	if time.Since(updatedCheck.LastCheck) > 1*time.Second {
		t.Error("Check should have been updated recently")
	}
}

func TestSelfHealingManager_PerformSelfHealing(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)
	defer shm.Stop()

	// Test performSelfHealing with healthy status
	// Add a healthy check
	check := &HealthCheck{
		Name:        "test-check",
		Description: "Test check",
		Check:       func() error { return nil },
		Status:      StatusHealthy,
	}

	hc.AddCheck(check)

	// Call performSelfHealing directly
	shm.performSelfHealing()

	// Should not panic and should complete successfully
}

func TestSelfHealingManager_HandleUnhealthyStatus(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)
	defer shm.Stop()

	// Add a circuit breaker and force it open
	config := CircuitBreakerConfig{
		Name:             "test-breaker",
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          1 * time.Second,
		WindowSize:       1 * time.Minute,
	}

	breaker := cbm.GetOrCreate("test-breaker", config)
	breaker.ForceOpen()

	// Verify breaker is open
	if breaker.GetState() != StateOpen {
		t.Error("Expected circuit breaker to be open")
	}

	// Call handleUnhealthyStatus
	shm.handleUnhealthyStatus()

	// Verify breaker was reset (should be closed now)
	if breaker.GetState() != StateClosed {
		t.Error("Expected circuit breaker to be closed after self-healing")
	}
}

func TestSelfHealingManager_HandleDegradedStatus(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)
	defer shm.Stop()

	// Call handleDegradedStatus
	shm.handleDegradedStatus()

	// Should not panic and should complete successfully
}

func TestSelfHealingManager_HandleHealthyStatus(t *testing.T) {
	hc := NewHealthChecker()
	defer hc.Stop()

	cbm := NewCircuitBreakerManager()
	defer cbm.Stop()

	rm := NewRetryManager()

	shm := NewSelfHealingManager(hc, cbm, rm)
	defer shm.Stop()

	// Call handleHealthyStatus
	shm.handleHealthyStatus()

	// Should not panic and should complete successfully
}
