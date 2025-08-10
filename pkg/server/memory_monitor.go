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
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// MemoryMonitor tracks memory usage and provides profiling capabilities
type MemoryMonitor struct {
	stats         *MonitorStats
	statsMutex    sync.RWMutex
	cleanupTicker *time.Ticker
	stopChan      chan struct{}
	alerts        []MemoryAlert
	alertsMutex   sync.RWMutex
}

// MonitorStats tracks memory monitoring statistics
type MonitorStats struct {
	TotalAlloc      uint64
	TotalSys        uint64
	HeapAlloc       uint64
	HeapSys         uint64
	HeapIdle        uint64
	HeapInuse       uint64
	HeapReleased    uint64
	HeapObjects     uint64
	StackInuse      uint64
	StackSys        uint64
	MSpanInuse      uint64
	MSpanSys        uint64
	MCacheInuse     uint64
	MCacheSys       uint64
	BuckHashSys     uint64
	GCSys           uint64
	OtherSys        uint64
	NextGC          uint64
	LastGC          uint64
	NumGC           uint32
	NumForcedGC     uint32
	GCCPUFraction   float64
	EnableGC        bool
	DebugGC         bool
	LastUpdate      time.Time
	PeakHeapAlloc   uint64
	PeakHeapSys     uint64
	PeakHeapObjects uint64
}

// MemoryAlert represents a memory usage alert
type MemoryAlert struct {
	Type      string
	Message   string
	Severity  string
	Timestamp time.Time
	Value     uint64
	Threshold uint64
}

// AlertType represents the type of memory alert
type AlertType string

const (
	AlertTypeHighMemoryUsage   AlertType = "high_memory_usage"
	AlertTypeMemoryLeak        AlertType = "memory_leak"
	AlertTypeGCPressure        AlertType = "gc_pressure"
	AlertTypeHeapFragmentation AlertType = "heap_fragmentation"
)

// AlertSeverity represents the severity of a memory alert
type AlertSeverity string

const (
	SeverityLow      AlertSeverity = "low"
	SeverityMedium   AlertSeverity = "medium"
	SeverityHigh     AlertSeverity = "high"
	SeverityCritical AlertSeverity = "critical"
)

// NewMemoryMonitor creates a new memory monitor
func NewMemoryMonitor() *MemoryMonitor {
	mm := &MemoryMonitor{
		stats: &MonitorStats{
			LastUpdate: time.Now(),
		},
		alerts:   make([]MemoryAlert, 0),
		stopChan: make(chan struct{}),
	}

	// Start monitoring routine
	go mm.monitoringRoutine()

	return mm
}

// monitoringRoutine periodically collects memory statistics
func (mm *MemoryMonitor) monitoringRoutine() {
	mm.cleanupTicker = time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer mm.cleanupTicker.Stop()

	for {
		select {
		case <-mm.cleanupTicker.C:
			mm.collectStats()
			mm.checkAlerts()
		case <-mm.stopChan:
			logger.Info("Memory monitor stopped")
			return
		}
	}
}

// collectStats collects current memory statistics
func (mm *MemoryMonitor) collectStats() {
	mm.statsMutex.Lock()
	defer mm.statsMutex.Unlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Update statistics
	mm.stats.TotalAlloc = m.TotalAlloc
	mm.stats.TotalSys = m.Sys
	mm.stats.HeapAlloc = m.HeapAlloc
	mm.stats.HeapSys = m.HeapSys
	mm.stats.HeapIdle = m.HeapIdle
	mm.stats.HeapInuse = m.HeapInuse
	mm.stats.HeapReleased = m.HeapReleased
	mm.stats.HeapObjects = m.HeapObjects
	mm.stats.StackInuse = m.StackInuse
	mm.stats.StackSys = m.StackSys
	mm.stats.MSpanInuse = m.MSpanInuse
	mm.stats.MSpanSys = m.MSpanSys
	mm.stats.MCacheInuse = m.MCacheInuse
	mm.stats.MCacheSys = m.MCacheSys
	mm.stats.BuckHashSys = m.BuckHashSys
	mm.stats.GCSys = m.GCSys
	mm.stats.OtherSys = m.OtherSys
	mm.stats.NextGC = m.NextGC
	mm.stats.LastGC = m.LastGC
	mm.stats.NumGC = m.NumGC
	mm.stats.NumForcedGC = m.NumForcedGC
	mm.stats.GCCPUFraction = m.GCCPUFraction
	mm.stats.EnableGC = m.EnableGC
	mm.stats.DebugGC = m.DebugGC
	mm.stats.LastUpdate = time.Now()

	// Track peak values
	if m.HeapAlloc > mm.stats.PeakHeapAlloc {
		mm.stats.PeakHeapAlloc = m.HeapAlloc
	}
	if m.HeapSys > mm.stats.PeakHeapSys {
		mm.stats.PeakHeapSys = m.HeapSys
	}
	if m.HeapObjects > mm.stats.PeakHeapObjects {
		mm.stats.PeakHeapObjects = m.HeapObjects
	}
}

// checkAlerts checks for memory-related alerts
func (mm *MemoryMonitor) checkAlerts() {
	mm.statsMutex.RLock()
	stats := *mm.stats // Copy for thread safety
	mm.statsMutex.RUnlock()

	// Check for high memory usage
	if stats.HeapAlloc > 100*1024*1024 { // 100MB threshold
		mm.addAlert(AlertTypeHighMemoryUsage, SeverityMedium,
			"High heap memory usage detected", stats.HeapAlloc, 100*1024*1024)
	}

	// Check for memory leak (heap objects growing continuously)
	if stats.HeapObjects > 100000 { // 100k objects threshold
		mm.addAlert(AlertTypeMemoryLeak, SeverityHigh,
			"High number of heap objects detected", stats.HeapObjects, 100000)
	}

	// Check for GC pressure
	if stats.GCCPUFraction > 0.1 { // 10% CPU time in GC
		mm.addAlert(AlertTypeGCPressure, SeverityHigh,
			"High garbage collection pressure detected", uint64(stats.GCCPUFraction*100), 10)
	}

	// Check for heap fragmentation
	if stats.HeapIdle > 0 && float64(stats.HeapInuse)/float64(stats.HeapIdle) < 0.1 {
		mm.addAlert(AlertTypeHeapFragmentation, SeverityMedium,
			"Heap fragmentation detected", stats.HeapIdle, stats.HeapInuse)
	}
}

// addAlert adds a new memory alert
func (mm *MemoryMonitor) addAlert(alertType AlertType, severity AlertSeverity, message string, value, threshold uint64) {
	mm.alertsMutex.Lock()
	defer mm.alertsMutex.Unlock()

	alert := MemoryAlert{
		Type:      string(alertType),
		Message:   message,
		Severity:  string(severity),
		Timestamp: time.Now(),
		Value:     value,
		Threshold: threshold,
	}

	mm.alerts = append(mm.alerts, alert)

	// Keep only the last 100 alerts
	if len(mm.alerts) > 100 {
		mm.alerts = mm.alerts[len(mm.alerts)-100:]
	}

	// Log the alert
	logger.Warning("Memory alert: %s - %s (value: %d, threshold: %d)",
		severity, message, value, threshold)
}

// GetStats returns current memory statistics
func (mm *MemoryMonitor) GetStats() map[string]interface{} {
	mm.statsMutex.RLock()
	defer mm.statsMutex.RUnlock()

	return map[string]interface{}{
		"current": map[string]interface{}{
			"total_alloc":     mm.stats.TotalAlloc,
			"total_sys":       mm.stats.TotalSys,
			"heap_alloc":      mm.stats.HeapAlloc,
			"heap_sys":        mm.stats.HeapSys,
			"heap_idle":       mm.stats.HeapIdle,
			"heap_inuse":      mm.stats.HeapInuse,
			"heap_released":   mm.stats.HeapReleased,
			"heap_objects":    mm.stats.HeapObjects,
			"stack_inuse":     mm.stats.StackInuse,
			"stack_sys":       mm.stats.StackSys,
			"gc_sys":          mm.stats.GCSys,
			"num_gc":          mm.stats.NumGC,
			"gc_cpu_fraction": mm.stats.GCCPUFraction,
			"last_update":     mm.stats.LastUpdate,
		},
		"peak": map[string]interface{}{
			"peak_heap_alloc":   mm.stats.PeakHeapAlloc,
			"peak_heap_sys":     mm.stats.PeakHeapSys,
			"peak_heap_objects": mm.stats.PeakHeapObjects,
		},
		"gc": map[string]interface{}{
			"next_gc":       mm.stats.NextGC,
			"last_gc":       mm.stats.LastGC,
			"num_forced_gc": mm.stats.NumForcedGC,
			"enable_gc":     mm.stats.EnableGC,
			"debug_gc":      mm.stats.DebugGC,
		},
	}
}

// GetAlerts returns current memory alerts
func (mm *MemoryMonitor) GetAlerts() []MemoryAlert {
	mm.alertsMutex.RLock()
	defer mm.alertsMutex.RUnlock()

	alerts := make([]MemoryAlert, len(mm.alerts))
	copy(alerts, mm.alerts)
	return alerts
}

// ClearAlerts clears all memory alerts
func (mm *MemoryMonitor) ClearAlerts() {
	mm.alertsMutex.Lock()
	defer mm.alertsMutex.Unlock()

	mm.alerts = make([]MemoryAlert, 0)
	logger.Info("Memory alerts cleared")
}

// ForceGC forces a garbage collection cycle
func (mm *MemoryMonitor) ForceGC() {
	logger.Info("Forcing garbage collection...")
	runtime.GC()

	// Collect stats after GC
	mm.collectStats()

	logger.Info("Garbage collection completed")
}

// GetMemoryProfile returns a memory profile for debugging
func (mm *MemoryMonitor) GetMemoryProfile() map[string]interface{} {
	// Get current memory stats
	stats := mm.GetStats()

	// Get goroutine count
	stats["goroutines"] = runtime.NumGoroutine()

	// Get CPU count
	stats["cpu_count"] = runtime.NumCPU()

	// Get Go version
	stats["go_version"] = runtime.Version()

	// Get GC settings
	stats["gc_settings"] = map[string]interface{}{
		"GOGC": debug.SetGCPercent(-1), // Get current setting
	}

	return stats
}

// SetGCPercent sets the garbage collection percentage
func (mm *MemoryMonitor) SetGCPercent(percent int) {
	oldPercent := debug.SetGCPercent(percent)
	logger.Info("GC percent changed from %d to %d", oldPercent, percent)
}

// GetMemoryUsage returns human-readable memory usage
func (mm *MemoryMonitor) GetMemoryUsage() map[string]string {
	mm.statsMutex.RLock()
	defer mm.statsMutex.RUnlock()

	return map[string]string{
		"heap_alloc":      formatBytes(mm.stats.HeapAlloc),
		"heap_sys":        formatBytes(mm.stats.HeapSys),
		"heap_idle":       formatBytes(mm.stats.HeapIdle),
		"heap_inuse":      formatBytes(mm.stats.HeapInuse),
		"heap_released":   formatBytes(mm.stats.HeapReleased),
		"total_alloc":     formatBytes(mm.stats.TotalAlloc),
		"total_sys":       formatBytes(mm.stats.TotalSys),
		"stack_inuse":     formatBytes(mm.stats.StackInuse),
		"stack_sys":       formatBytes(mm.stats.StackSys),
		"gc_sys":          formatBytes(mm.stats.GCSys),
		"heap_objects":    formatNumber(mm.stats.HeapObjects),
		"num_gc":          formatNumber(uint64(mm.stats.NumGC)),
		"gc_cpu_fraction": fmt.Sprintf("%.2f%%", mm.stats.GCCPUFraction*100),
	}
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// formatNumber formats large numbers with commas
func formatNumber(n uint64) string {
	return fmt.Sprintf("%d", n)
}

// Stop gracefully stops the memory monitor
func (mm *MemoryMonitor) Stop() {
	logger.Info("Stopping memory monitor...")

	if mm.cleanupTicker != nil {
		mm.cleanupTicker.Stop()
	}

	close(mm.stopChan)

	logger.Info("Memory monitor stopped")
}
