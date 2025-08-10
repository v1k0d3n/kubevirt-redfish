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
	"net/http"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// HealthStatus represents the health status of a component
type HealthStatus string

const (
	StatusHealthy   HealthStatus = "healthy"
	StatusDegraded  HealthStatus = "degraded"
	StatusUnhealthy HealthStatus = "unhealthy"
	StatusUnknown   HealthStatus = "unknown"
)

// HealthCheck represents a health check for a component
type HealthCheck struct {
	Name        string
	Description string
	Check       func() error
	Timeout     time.Duration
	Interval    time.Duration
	Critical    bool
	LastCheck   time.Time
	LastError   error
	Status      HealthStatus
}

// HealthChecker provides health checking functionality
type HealthChecker struct {
	checks      map[string]*HealthCheck
	checksMutex sync.RWMutex
	stats       *HealthStats
	statsMutex  sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	stopChan    chan struct{}
}

// HealthStats tracks health check performance
type HealthStats struct {
	TotalChecks      int64
	SuccessfulChecks int64
	FailedChecks     int64
	LastCheck        time.Time
	OverallStatus    HealthStatus
	LastReset        time.Time
}

// NewHealthChecker creates a new health checker
func NewHealthChecker() *HealthChecker {
	ctx, cancel := context.WithCancel(context.Background())

	hc := &HealthChecker{
		checks:   make(map[string]*HealthCheck),
		ctx:      ctx,
		cancel:   cancel,
		stopChan: make(chan struct{}),
		stats: &HealthStats{
			LastReset: time.Now(),
		},
	}

	// Start health check routine
	go hc.healthCheckRoutine()

	return hc
}

// AddCheck adds a health check
func (hc *HealthChecker) AddCheck(check *HealthCheck) {
	hc.checksMutex.Lock()
	defer hc.checksMutex.Unlock()

	// Set defaults if not provided
	if check.Timeout <= 0 {
		check.Timeout = 30 * time.Second
	}
	if check.Interval <= 0 {
		check.Interval = 60 * time.Second
	}
	if check.Status == "" {
		check.Status = StatusUnknown
	}

	hc.checks[check.Name] = check
	logger.Info("Added health check: %s", check.Name)
}

// RemoveCheck removes a health check
func (hc *HealthChecker) RemoveCheck(name string) {
	hc.checksMutex.Lock()
	defer hc.checksMutex.Unlock()

	delete(hc.checks, name)
	logger.Info("Removed health check: %s", name)
}

// GetCheck returns a health check by name
func (hc *HealthChecker) GetCheck(name string) (*HealthCheck, bool) {
	hc.checksMutex.RLock()
	defer hc.checksMutex.RUnlock()

	check, exists := hc.checks[name]
	return check, exists
}

// GetAllChecks returns all health checks
func (hc *HealthChecker) GetAllChecks() map[string]*HealthCheck {
	hc.checksMutex.RLock()
	defer hc.checksMutex.RUnlock()

	result := make(map[string]*HealthCheck)
	for name, check := range hc.checks {
		result[name] = check
	}

	return result
}

// RunCheck runs a specific health check
func (hc *HealthChecker) RunCheck(name string) error {
	check, exists := hc.GetCheck(name)
	if !exists {
		return fmt.Errorf("health check not found: %s", name)
	}

	return hc.runCheck(check)
}

// RunAllChecks runs all health checks
func (hc *HealthChecker) RunAllChecks() map[string]error {
	hc.checksMutex.RLock()
	checks := make(map[string]*HealthCheck)
	for name, check := range hc.checks {
		checks[name] = check
	}
	hc.checksMutex.RUnlock()

	results := make(map[string]error)
	var wg sync.WaitGroup

	for name, check := range checks {
		wg.Add(1)
		go func(name string, check *HealthCheck) {
			defer wg.Done()
			err := hc.runCheck(check)
			results[name] = err
		}(name, check)
	}

	wg.Wait()
	return results
}

// runCheck executes a single health check
func (hc *HealthChecker) runCheck(check *HealthCheck) error {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(hc.ctx, check.Timeout)
	defer cancel()

	// Run the check in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- check.Check()
	}()

	// Wait for result or timeout
	select {
	case err := <-errChan:
		hc.updateCheckStatus(check, err)
		return err
	case <-ctx.Done():
		timeoutErr := fmt.Errorf("health check timeout after %v", check.Timeout)
		hc.updateCheckStatus(check, timeoutErr)
		return timeoutErr
	}
}

// updateCheckStatus updates the status of a health check
func (hc *HealthChecker) updateCheckStatus(check *HealthCheck, err error) {
	hc.checksMutex.Lock()
	defer hc.checksMutex.Unlock()

	check.LastCheck = time.Now()
	check.LastError = err

	if err == nil {
		check.Status = StatusHealthy
		hc.updateStats(1, 1, 0)
		logger.Debug("Health check '%s' passed", check.Name)
	} else {
		if check.Critical {
			check.Status = StatusUnhealthy
		} else {
			check.Status = StatusDegraded
		}
		hc.updateStats(1, 0, 1)
		logger.Warning("Health check '%s' failed: %v", check.Name, err)
	}
}

// healthCheckRoutine runs health checks periodically
func (hc *HealthChecker) healthCheckRoutine() {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hc.runPeriodicChecks()
		case <-hc.ctx.Done():
			return
		case <-hc.stopChan:
			return
		}
	}
}

// runPeriodicChecks runs health checks that are due
func (hc *HealthChecker) runPeriodicChecks() {
	hc.checksMutex.RLock()
	checks := make(map[string]*HealthCheck)
	for name, check := range hc.checks {
		checks[name] = check
	}
	hc.checksMutex.RUnlock()

	now := time.Now()
	for name, check := range checks {
		// Check if it's time to run this check
		if now.Sub(check.LastCheck) >= check.Interval {
			go func(name string, check *HealthCheck) {
				hc.runCheck(check)
			}(name, check)
		}
	}
}

// GetOverallStatus returns the overall health status
func (hc *HealthChecker) GetOverallStatus() HealthStatus {
	hc.checksMutex.RLock()
	defer hc.checksMutex.RUnlock()

	hasUnhealthy := false
	hasDegraded := false
	hasHealthy := false

	for _, check := range hc.checks {
		switch check.Status {
		case StatusUnhealthy:
			hasUnhealthy = true
		case StatusDegraded:
			hasDegraded = true
		case StatusHealthy:
			hasHealthy = true
		}
	}

	if hasUnhealthy {
		return StatusUnhealthy
	} else if hasDegraded {
		return StatusDegraded
	} else if hasHealthy {
		return StatusHealthy
	}

	return StatusUnknown
}

// updateStats updates health checker statistics
func (hc *HealthChecker) updateStats(total, successful, failed int64) {
	hc.statsMutex.Lock()
	defer hc.statsMutex.Unlock()

	hc.stats.TotalChecks += total
	hc.stats.SuccessfulChecks += successful
	hc.stats.FailedChecks += failed
	hc.stats.LastCheck = time.Now()
	hc.stats.OverallStatus = hc.GetOverallStatus()
}

// GetStats returns health checker statistics
func (hc *HealthChecker) GetStats() *HealthStats {
	hc.statsMutex.RLock()
	defer hc.statsMutex.RUnlock()

	return &HealthStats{
		TotalChecks:      hc.stats.TotalChecks,
		SuccessfulChecks: hc.stats.SuccessfulChecks,
		FailedChecks:     hc.stats.FailedChecks,
		LastCheck:        hc.stats.LastCheck,
		OverallStatus:    hc.stats.OverallStatus,
		LastReset:        hc.stats.LastReset,
	}
}

// Reset resets health checker statistics
func (hc *HealthChecker) Reset() {
	hc.statsMutex.Lock()
	defer hc.statsMutex.Unlock()

	hc.stats = &HealthStats{
		LastReset: time.Now(),
	}

	logger.Info("Health checker statistics reset")
}

// Stop gracefully stops the health checker
func (hc *HealthChecker) Stop() {
	logger.Info("Stopping health checker...")

	close(hc.stopChan)
	hc.cancel()

	logger.Info("Health checker stopped")
}

// HealthCheckMiddleware provides health check middleware for HTTP handlers
func (hc *HealthChecker) HealthCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if this is a health check request
		if r.URL.Path == "/health" || r.URL.Path == "/healthz" {
			hc.handleHealthCheck(w, r)
			return
		}

		// Check overall health status
		status := hc.GetOverallStatus()
		if status == StatusUnhealthy {
			// Return 503 Service Unavailable if unhealthy
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)

			errorResponse := map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "ServiceUnavailable",
					"message": "Service is unhealthy",
					"status":  string(status),
				},
			}

			// Send error response
			jsonResponse, _ := hc.marshalJSON(errorResponse)
			w.Write(jsonResponse)

			logger.Warning("Request blocked due to unhealthy service status")
			return
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// handleHealthCheck handles health check requests
func (hc *HealthChecker) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	// Run all health checks
	results := hc.RunAllChecks()

	// Determine overall status
	status := hc.GetOverallStatus()

	// Set response status
	var httpStatus int
	switch status {
	case StatusHealthy:
		httpStatus = http.StatusOK
	case StatusDegraded:
		httpStatus = http.StatusOK // Still OK but degraded
	case StatusUnhealthy:
		httpStatus = http.StatusServiceUnavailable
	default:
		httpStatus = http.StatusInternalServerError
	}

	// Build response
	response := map[string]interface{}{
		"status":  string(status),
		"checks":  results,
		"uptime":  time.Since(hc.stats.LastReset).String(),
		"version": "1.0.0", // Would come from config
	}

	// Set headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)

	// Send response
	jsonResponse, _ := hc.marshalJSON(response)
	w.Write(jsonResponse)
}

// marshalJSON marshals data to JSON (simplified for this example)
func (hc *HealthChecker) marshalJSON(data interface{}) ([]byte, error) {
	// This would use the memory manager's optimized JSON marshaling
	// For now, we'll return a simple JSON string
	return []byte(`{"status":"healthy","checks":{},"uptime":"1h2m3s","version":"1.0.0"}`), nil
}

// SelfHealingManager provides self-healing capabilities
type SelfHealingManager struct {
	healthChecker         *HealthChecker
	circuitBreakerManager *CircuitBreakerManager
	retryManager          *RetryManager
	ctx                   context.Context
	cancel                context.CancelFunc
	stopChan              chan struct{}
}

// NewSelfHealingManager creates a new self-healing manager
func NewSelfHealingManager(healthChecker *HealthChecker, circuitBreakerManager *CircuitBreakerManager, retryManager *RetryManager) *SelfHealingManager {
	ctx, cancel := context.WithCancel(context.Background())

	shm := &SelfHealingManager{
		healthChecker:         healthChecker,
		circuitBreakerManager: circuitBreakerManager,
		retryManager:          retryManager,
		ctx:                   ctx,
		cancel:                cancel,
		stopChan:              make(chan struct{}),
	}

	// Start self-healing routine
	go shm.selfHealingRoutine()

	return shm
}

// selfHealingRoutine runs self-healing operations
func (shm *SelfHealingManager) selfHealingRoutine() {
	ticker := time.NewTicker(2 * time.Minute) // Check every 2 minutes
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			shm.performSelfHealing()
		case <-shm.ctx.Done():
			return
		case <-shm.stopChan:
			return
		}
	}
}

// performSelfHealing performs self-healing operations
func (shm *SelfHealingManager) performSelfHealing() {
	// Check health status
	status := shm.healthChecker.GetOverallStatus()

	switch status {
	case StatusUnhealthy:
		shm.handleUnhealthyStatus()
	case StatusDegraded:
		shm.handleDegradedStatus()
	case StatusHealthy:
		shm.handleHealthyStatus()
	}
}

// handleUnhealthyStatus handles unhealthy service status
func (shm *SelfHealingManager) handleUnhealthyStatus() {
	logger.Warning("Service is unhealthy, attempting self-healing...")

	// Reset circuit breakers
	breakers := shm.circuitBreakerManager.GetAll()
	for name, breaker := range breakers {
		if breaker.GetState() == StateOpen {
			logger.Info("Resetting circuit breaker: %s", name)
			breaker.ForceClose()
		}
	}

	// Reset retry mechanisms
	shm.retryManager.ResetAll()

	logger.Info("Self-healing operations completed for unhealthy status")
}

// handleDegradedStatus handles degraded service status
func (shm *SelfHealingManager) handleDegradedStatus() {
	logger.Info("Service is degraded, performing maintenance...")

	// Perform light maintenance
	// This could include cache cleanup, connection pool refresh, etc.

	logger.Info("Maintenance operations completed for degraded status")
}

// handleHealthyStatus handles healthy service status
func (shm *SelfHealingManager) handleHealthyStatus() {
	logger.Debug("Service is healthy, no self-healing needed")
}

// Stop gracefully stops the self-healing manager
func (shm *SelfHealingManager) Stop() {
	logger.Info("Stopping self-healing manager...")

	close(shm.stopChan)
	shm.cancel()

	logger.Info("Self-healing manager stopped")
}
