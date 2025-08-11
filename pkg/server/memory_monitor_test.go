package server

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

func init() {
	// Initialize logger for testing
	logger.Init("debug")
}

func TestNewMemoryMonitor(t *testing.T) {
	mm := NewMemoryMonitor()

	if mm == nil {
		t.Fatal("NewMemoryMonitor should not return nil")
	}

	if mm.stats == nil {
		t.Error("Stats should be initialized")
	}

	if mm.alerts == nil {
		t.Error("Alerts should be initialized")
	}

	if mm.stopChan == nil {
		t.Error("Stop channel should be initialized")
	}

	// Clean up
	mm.Stop()
}

func TestMemoryMonitor_collectStats(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Force stats collection
	mm.collectStats()

	stats := mm.GetStats()
	if stats == nil {
		t.Fatal("GetStats should not return nil")
	}

	current := stats["current"].(map[string]interface{})
	if current["total_alloc"] == nil {
		t.Error("TotalAlloc should be set")
	}
	if current["heap_alloc"] == nil {
		t.Error("HeapAlloc should be set")
	}
	if current["heap_objects"] == nil {
		t.Error("HeapObjects should be set")
	}
	if current["last_update"] == nil {
		t.Error("LastUpdate should be set")
	}

	peak := stats["peak"].(map[string]interface{})
	if peak["peak_heap_alloc"] == nil {
		t.Error("PeakHeapAlloc should be set")
	}
	if peak["peak_heap_sys"] == nil {
		t.Error("PeakHeapSys should be set")
	}
	if peak["peak_heap_objects"] == nil {
		t.Error("PeakHeapObjects should be set")
	}

	gc := stats["gc"].(map[string]interface{})
	if gc["next_gc"] == nil {
		t.Error("NextGC should be set")
	}
	if gc["enable_gc"] == nil {
		t.Error("EnableGC should be set")
	}

	// NumGC is in the current section, not gc section
	if current["num_gc"] == nil {
		t.Error("NumGC should be present in current stats")
	}
	// Verify it's a uint32 (even if 0)
	if _, ok := current["num_gc"].(uint32); !ok {
		t.Error("NumGC should be of type uint32")
	}
}

func TestMemoryMonitor_checkAlerts(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Initially there should be no alerts
	alerts := mm.GetAlerts()
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts initially, got %d", len(alerts))
	}

	// Force stats collection and alert checking
	mm.collectStats()
	mm.checkAlerts()

	// Check if any alerts were generated (depends on current memory usage)
	_ = mm.GetAlerts() // We can't predict if alerts will be generated as it depends on actual memory usage
	// But we can verify the function doesn't panic and processes correctly
}

func TestMemoryMonitor_addAlert(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Add a test alert
	mm.addAlert(AlertTypeHighMemoryUsage, SeverityMedium, "Test alert", 1000, 500)

	alerts := mm.GetAlerts()
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert, got %d", len(alerts))
	}

	alert := alerts[0]
	if alert.Type != string(AlertTypeHighMemoryUsage) {
		t.Errorf("Expected alert type %s, got %s", AlertTypeHighMemoryUsage, alert.Type)
	}
	if alert.Severity != string(SeverityMedium) {
		t.Errorf("Expected severity %s, got %s", SeverityMedium, alert.Severity)
	}
	if alert.Message != "Test alert" {
		t.Errorf("Expected message 'Test alert', got %s", alert.Message)
	}
	if alert.Value != 1000 {
		t.Errorf("Expected value 1000, got %d", alert.Value)
	}
	if alert.Threshold != 500 {
		t.Errorf("Expected threshold 500, got %d", alert.Threshold)
	}
}

func TestMemoryMonitor_GetAlerts(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Add multiple alerts
	mm.addAlert(AlertTypeHighMemoryUsage, SeverityLow, "Alert 1", 100, 50)
	mm.addAlert(AlertTypeMemoryLeak, SeverityHigh, "Alert 2", 200, 150)
	mm.addAlert(AlertTypeGCPressure, SeverityCritical, "Alert 3", 300, 250)

	alerts := mm.GetAlerts()
	if len(alerts) != 3 {
		t.Errorf("Expected 3 alerts, got %d", len(alerts))
	}

	// Verify all alerts are present (order may vary due to timing)
	foundTypes := make(map[string]bool)
	for _, alert := range alerts {
		foundTypes[alert.Type] = true
	}

	expectedTypes := []string{
		string(AlertTypeHighMemoryUsage),
		string(AlertTypeMemoryLeak),
		string(AlertTypeGCPressure),
	}

	for _, expectedType := range expectedTypes {
		if !foundTypes[expectedType] {
			t.Errorf("Expected alert type %s not found", expectedType)
		}
	}
}

func TestMemoryMonitor_ClearAlerts(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Add some alerts
	mm.addAlert(AlertTypeHighMemoryUsage, SeverityMedium, "Test alert", 1000, 500)
	mm.addAlert(AlertTypeMemoryLeak, SeverityHigh, "Test alert 2", 2000, 1500)

	// Verify alerts exist
	alerts := mm.GetAlerts()
	if len(alerts) != 2 {
		t.Errorf("Expected 2 alerts before clearing, got %d", len(alerts))
	}

	// Clear alerts
	mm.ClearAlerts()

	// Verify alerts are cleared
	alerts = mm.GetAlerts()
	if len(alerts) != 0 {
		t.Errorf("Expected 0 alerts after clearing, got %d", len(alerts))
	}
}

func TestMemoryMonitor_ForceGC(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Force garbage collection
	mm.ForceGC()

	// Verify stats were updated after GC
	stats := mm.GetStats()
	current := stats["current"].(map[string]interface{})
	if current["num_gc"] == nil {
		t.Error("NumGC should be set after ForceGC")
	}
}

func TestMemoryMonitor_GetMemoryProfile(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	profile := mm.GetMemoryProfile()

	// Check basic profile fields
	if profile["goroutines"] == nil {
		t.Error("Goroutines count should be included")
	}
	if profile["cpu_count"] == nil {
		t.Error("CPU count should be included")
	}
	if profile["go_version"] == nil {
		t.Error("Go version should be included")
	}
	if profile["gc_settings"] == nil {
		t.Error("GC settings should be included")
	}

	// Verify goroutines count is reasonable
	goroutines := profile["goroutines"].(int)
	if goroutines < 1 {
		t.Errorf("Expected at least 1 goroutine, got %d", goroutines)
	}

	// Verify CPU count matches runtime
	cpuCount := profile["cpu_count"].(int)
	if cpuCount != runtime.NumCPU() {
		t.Errorf("Expected CPU count %d, got %d", runtime.NumCPU(), cpuCount)
	}

	// Verify Go version matches runtime
	goVersion := profile["go_version"].(string)
	if goVersion != runtime.Version() {
		t.Errorf("Expected Go version %s, got %s", runtime.Version(), goVersion)
	}
}

func TestMemoryMonitor_SetGCPercent(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Get current GC percent
	currentPercent := debug.SetGCPercent(-1) // Get current setting

	// Set a new GC percent
	newPercent := 200
	mm.SetGCPercent(newPercent)

	// Verify the setting was applied
	actualPercent := debug.SetGCPercent(-1)
	if actualPercent != newPercent {
		t.Errorf("Expected GC percent %d, got %d", newPercent, actualPercent)
	}

	// Restore original setting
	debug.SetGCPercent(currentPercent)
}

func TestMemoryMonitor_GetMemoryUsage(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Force stats collection
	mm.collectStats()

	usage := mm.GetMemoryUsage()

	// Check that all expected fields are present
	expectedFields := []string{
		"heap_alloc", "heap_sys", "heap_idle", "heap_inuse", "heap_released",
		"total_alloc", "total_sys", "stack_inuse", "stack_sys", "gc_sys",
		"heap_objects", "num_gc", "gc_cpu_fraction",
	}

	for _, field := range expectedFields {
		if usage[field] == "" {
			t.Errorf("Expected field %s to be present and non-empty", field)
		}
	}

	// Verify specific format expectations
	if usage["gc_cpu_fraction"] == "" {
		t.Error("GC CPU fraction should be formatted as percentage")
	}
}

func TestFormatBytes(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    uint64
		expected string
	}{
		{
			name:     "zero bytes",
			bytes:    0,
			expected: "0 B",
		},
		{
			name:     "small bytes",
			bytes:    1023,
			expected: "1023 B",
		},
		{
			name:     "1 KB",
			bytes:    1024,
			expected: "1.0 KB",
		},
		{
			name:     "1.5 KB",
			bytes:    1536,
			expected: "1.5 KB",
		},
		{
			name:     "1 MB",
			bytes:    1024 * 1024,
			expected: "1.0 MB",
		},
		{
			name:     "1 GB",
			bytes:    1024 * 1024 * 1024,
			expected: "1.0 GB",
		},
		{
			name:     "large value",
			bytes:    1024*1024*1024 + 512*1024*1024, // 1.5 GB
			expected: "1.5 GB",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatBytes(tc.bytes)
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestFormatNumber(t *testing.T) {
	testCases := []struct {
		name     string
		number   uint64
		expected string
	}{
		{
			name:     "zero",
			number:   0,
			expected: "0",
		},
		{
			name:     "small number",
			number:   123,
			expected: "123",
		},
		{
			name:     "large number",
			number:   123456789,
			expected: "123456789",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatNumber(tc.number)
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestMemoryMonitor_AlertLimits(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Add more than 100 alerts to test the limit
	for i := uint(0); i < 150; i++ {
		mm.addAlert(AlertTypeHighMemoryUsage, SeverityLow,
			fmt.Sprintf("Alert %d", i), uint64(i), uint64(i/2))
	}

	alerts := mm.GetAlerts()
	if len(alerts) > 100 {
		t.Errorf("Expected at most 100 alerts, got %d", len(alerts))
	}

	// Verify the alert limit is respected
	if len(alerts) > 100 {
		t.Errorf("Expected at most 100 alerts, got %d", len(alerts))
	}

	// Verify that newer alerts are kept (the last 100)
	if len(alerts) > 0 {
		// The alerts should be from the last 100 added (50-149)
		// Since we added 150 alerts, the first 50 should be removed
		firstAlert := alerts[0]
		if firstAlert.Message == "Alert 0" {
			t.Errorf("Expected first alert to be newer than 'Alert 0', got %s", firstAlert.Message)
		}
	}
}

func TestMemoryMonitor_ConcurrentAccess(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Test concurrent access to stats and alerts
	var wg sync.WaitGroup
	numGoroutines := uint(10)

	for i := uint(0); i < numGoroutines; i++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()

			// Concurrent stats collection
			mm.collectStats()

			// Concurrent stats retrieval
			stats := mm.GetStats()
			if stats == nil {
				t.Errorf("GetStats returned nil in goroutine %d", id)
			}

			// Concurrent alert addition
			mm.addAlert(AlertTypeHighMemoryUsage, SeverityMedium,
				fmt.Sprintf("Concurrent alert %d", id), uint64(id), uint64(id/2))

			// Concurrent alert retrieval
			alerts := mm.GetAlerts()
			if alerts == nil {
				t.Errorf("GetAlerts returned nil in goroutine %d", id)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state is consistent
	stats := mm.GetStats()
	if stats == nil {
		t.Error("Final GetStats returned nil")
	}

	alerts := mm.GetAlerts()
	if alerts == nil {
		t.Error("Final GetAlerts returned nil")
	}
}

func TestMemoryMonitor_Stop(t *testing.T) {
	mm := NewMemoryMonitor()

	// Stop the monitor
	mm.Stop()

	// Verify the monitor can be stopped multiple times without panic
	mm.Stop()
	mm.Stop()
}

func TestMemoryMonitor_AlertTypes(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Test all alert types
	alertTypes := []AlertType{
		AlertTypeHighMemoryUsage,
		AlertTypeMemoryLeak,
		AlertTypeGCPressure,
		AlertTypeHeapFragmentation,
	}

	severities := []AlertSeverity{
		SeverityLow,
		SeverityMedium,
		SeverityHigh,
		SeverityCritical,
	}

	for i := uint(0); i < uint(len(alertTypes)); i++ {
		alertType := alertTypes[i]
		severity := severities[i%uint(len(severities))]
		mm.addAlert(alertType, severity,
			fmt.Sprintf("Test %s alert", alertType), uint64(i)*1000, uint64(i)*500)
	}

	alerts := mm.GetAlerts()
	if len(alerts) != len(alertTypes) {
		t.Errorf("Expected %d alerts, got %d", len(alertTypes), len(alerts))
	}

	// Verify all alert types are present
	foundTypes := make(map[string]bool)
	for _, alert := range alerts {
		foundTypes[alert.Type] = true
	}

	for _, alertType := range alertTypes {
		if !foundTypes[string(alertType)] {
			t.Errorf("Alert type %s not found in alerts", alertType)
		}
	}
}

func TestMemoryMonitor_StatsConsistency(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Collect stats multiple times
	for i := 0; i < 5; i++ {
		mm.collectStats()
		time.Sleep(10 * time.Millisecond) // Small delay between collections
	}

	stats := mm.GetStats()
	current := stats["current"].(map[string]interface{})

	// Verify stats are reasonable
	totalAlloc := current["total_alloc"].(uint64)
	if totalAlloc == 0 {
		t.Error("TotalAlloc should be greater than 0")
	}

	heapAlloc := current["heap_alloc"].(uint64)
	if heapAlloc == 0 {
		t.Error("HeapAlloc should be greater than 0")
	}

	// Verify peak values are tracked
	peak := stats["peak"].(map[string]interface{})
	peakHeapAlloc := peak["peak_heap_alloc"].(uint64)
	if peakHeapAlloc < heapAlloc {
		t.Errorf("PeakHeapAlloc (%d) should be >= current HeapAlloc (%d)", peakHeapAlloc, heapAlloc)
	}
}

func TestMemoryMonitor_AlertSeverityOrdering(t *testing.T) {
	mm := NewMemoryMonitor()
	defer mm.Stop()

	// Add alerts with different severities with small delays to ensure different timestamps
	mm.addAlert(AlertTypeHighMemoryUsage, SeverityLow, "Low severity", 100, 50)
	time.Sleep(1 * time.Millisecond)
	mm.addAlert(AlertTypeMemoryLeak, SeverityCritical, "Critical severity", 200, 150)
	time.Sleep(1 * time.Millisecond)
	mm.addAlert(AlertTypeGCPressure, SeverityMedium, "Medium severity", 300, 250)
	time.Sleep(1 * time.Millisecond)
	mm.addAlert(AlertTypeHeapFragmentation, SeverityHigh, "High severity", 400, 350)

	alerts := mm.GetAlerts()
	if len(alerts) != 4 {
		t.Errorf("Expected 4 alerts, got %d", len(alerts))
	}

	// Verify alerts are returned in chronological order (oldest first, as they were added)
	for i := 0; i < len(alerts)-1; i++ {
		if alerts[i].Timestamp.After(alerts[i+1].Timestamp) {
			t.Errorf("Alert %d timestamp (%v) should be before alert %d timestamp (%v)",
				i, alerts[i].Timestamp, i+1, alerts[i+1].Timestamp)
		}
	}
}
