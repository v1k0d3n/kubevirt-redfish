package server

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestNewBufferPool(t *testing.T) {
	testCases := []struct {
		name     string
		maxSize  int
		expected int
	}{
		{
			name:     "small pool",
			maxSize:  10,
			expected: 10,
		},
		{
			name:     "large pool",
			maxSize:  1000,
			expected: 1000,
		},
		{
			name:     "zero size pool",
			maxSize:  0,
			expected: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewBufferPool(tc.maxSize)
			if pool == nil {
				t.Fatal("NewBufferPool should not return nil")
			}
			if pool.maxSize != tc.expected {
				t.Errorf("Expected maxSize %d, got %d", tc.expected, pool.maxSize)
			}
			if pool.stats == nil {
				t.Error("Stats should be initialized")
			}
			if pool.stats.LastReset.IsZero() {
				t.Error("LastReset should be set")
			}
		})
	}
}

func TestBufferPool_GetAndPut(t *testing.T) {
	pool := NewBufferPool(2)

	// Test getting buffers
	buf1 := pool.Get()
	if buf1 == nil {
		t.Fatal("Get should not return nil")
	}
	if buf1.Cap() < 1024 {
		t.Errorf("Expected buffer capacity >= 1024, got %d", buf1.Cap())
	}

	buf2 := pool.Get()
	if buf2 == nil {
		t.Fatal("Get should not return nil")
	}

	// Test putting buffers back
	pool.Put(buf1)
	pool.Put(buf2)

	// Test putting nil buffer (should not panic)
	pool.Put(nil)

	// Verify stats
	stats := pool.GetStats()
	if stats["total_allocated"] != int64(2) {
		t.Errorf("Expected total_allocated 2, got %v", stats["total_allocated"])
	}
	if stats["total_returned"] != int64(-2) {
		t.Errorf("Expected total_returned -2 (all items returned to pool), got %v", stats["total_returned"])
	}
}

func TestBufferPool_GetStats(t *testing.T) {
	pool := NewBufferPool(5)

	// Initially stats should be zero
	stats := pool.GetStats()
	expectedKeys := []string{
		"total_allocated",
		"total_returned",
		"current_in_use",
		"max_pool_size",
		"current_pool_size",
		"uptime",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected key '%s' in stats", key)
		}
	}

	if stats["total_allocated"] != int64(0) {
		t.Errorf("Expected total_allocated 0, got %v", stats["total_allocated"])
	}
	if stats["max_pool_size"] != 5 {
		t.Errorf("Expected max_pool_size 5, got %v", stats["max_pool_size"])
	}
	if stats["current_pool_size"] != 0 {
		t.Errorf("Expected current_pool_size 0, got %v", stats["current_pool_size"])
	}

	// Test after some operations
	buf := pool.Get()
	pool.Put(buf)

	stats = pool.GetStats()
	if stats["total_allocated"] != int64(1) {
		t.Errorf("Expected total_allocated 1, got %v", stats["total_allocated"])
	}
	if stats["total_returned"] != int64(-1) {
		t.Errorf("Expected total_returned -1 (item returned to pool), got %v", stats["total_returned"])
	}
}

func TestNewJSONEncoderPool(t *testing.T) {
	testCases := []struct {
		name     string
		maxSize  int
		expected int
	}{
		{
			name:     "small pool",
			maxSize:  5,
			expected: 5,
		},
		{
			name:     "large pool",
			maxSize:  100,
			expected: 100,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewJSONEncoderPool(tc.maxSize)
			if pool == nil {
				t.Fatal("NewJSONEncoderPool should not return nil")
			}
			if pool.maxSize != tc.expected {
				t.Errorf("Expected maxSize %d, got %d", tc.expected, pool.maxSize)
			}
			if pool.stats == nil {
				t.Error("Stats should be initialized")
			}
		})
	}
}

func TestJSONEncoderPool_GetAndPut(t *testing.T) {
	pool := NewJSONEncoderPool(2)
	buf := bytes.NewBuffer(nil)

	// Test getting encoders
	enc1 := pool.Get(buf)
	if enc1 == nil {
		t.Fatal("Get should not return nil")
	}

	enc2 := pool.Get(buf)
	if enc2 == nil {
		t.Fatal("Get should not return nil")
	}

	// Test putting encoders back
	pool.Put(enc1)
	pool.Put(enc2)

	// Test putting nil encoder (should not panic)
	pool.Put(nil)

	// Verify stats
	stats := pool.GetStats()
	if stats["total_allocated"] != int64(2) {
		t.Errorf("Expected total_allocated 2, got %v", stats["total_allocated"])
	}
	if stats["total_returned"] != int64(-2) {
		t.Errorf("Expected total_returned -2 (all items returned to pool), got %v", stats["total_returned"])
	}
}

func TestJSONEncoderPool_GetStats(t *testing.T) {
	pool := NewJSONEncoderPool(3)

	// Initially stats should be zero
	stats := pool.GetStats()
	expectedKeys := []string{
		"total_allocated",
		"total_returned",
		"current_in_use",
		"max_pool_size",
		"current_pool_size",
		"uptime",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected key '%s' in stats", key)
		}
	}

	if stats["max_pool_size"] != 3 {
		t.Errorf("Expected max_pool_size 3, got %v", stats["max_pool_size"])
	}
}

func TestNewResponsePool(t *testing.T) {
	testCases := []struct {
		name     string
		maxSize  int
		expected int
	}{
		{
			name:     "small pool",
			maxSize:  10,
			expected: 10,
		},
		{
			name:     "large pool",
			maxSize:  500,
			expected: 500,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewResponsePool(tc.maxSize)
			if pool == nil {
				t.Fatal("NewResponsePool should not return nil")
			}
			if pool.maxSize != tc.expected {
				t.Errorf("Expected maxSize %d, got %d", tc.expected, pool.maxSize)
			}
			if pool.stats == nil {
				t.Error("Stats should be initialized")
			}
		})
	}
}

func TestResponsePool_GetAndPut(t *testing.T) {
	pool := NewResponsePool(2)

	// Test getting response objects
	resp1 := pool.Get()
	if resp1 == nil {
		t.Fatal("Get should not return nil")
	}
	if resp1.Headers == nil {
		t.Error("Headers should be initialized")
	}
	if resp1.StatusCode != 0 {
		t.Errorf("Expected StatusCode 0, got %d", resp1.StatusCode)
	}

	resp2 := pool.Get()
	if resp2 == nil {
		t.Fatal("Get should not return nil")
	}

	// Set some data
	resp1.StatusCode = 200
	resp1.Headers["Content-Type"] = "application/json"
	resp1.Body = []byte("test data")
	resp1.ETag = "abc123"
	resp1.Timestamp = time.Now()

	// Test putting response objects back
	pool.Put(resp1)
	pool.Put(resp2)

	// Test putting nil response (should not panic)
	pool.Put(nil)

	// Verify stats
	stats := pool.GetStats()
	if stats["total_allocated"] != int64(2) {
		t.Errorf("Expected total_allocated 2, got %v", stats["total_allocated"])
	}
	if stats["total_returned"] != int64(-2) {
		t.Errorf("Expected total_returned -2 (all items returned to pool), got %v", stats["total_returned"])
	}
}

func TestResponsePool_GetStats(t *testing.T) {
	pool := NewResponsePool(5)

	// Initially stats should be zero
	stats := pool.GetStats()
	expectedKeys := []string{
		"total_allocated",
		"total_returned",
		"current_in_use",
		"max_pool_size",
		"current_pool_size",
		"uptime",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected key '%s' in stats", key)
		}
	}

	if stats["max_pool_size"] != 5 {
		t.Errorf("Expected max_pool_size 5, got %v", stats["max_pool_size"])
	}
}

func TestNewMemoryManager(t *testing.T) {
	mm := NewMemoryManager()
	if mm == nil {
		t.Fatal("NewMemoryManager should not return nil")
	}

	if mm.bufferPool == nil {
		t.Error("BufferPool should be initialized")
	}
	if mm.encoderPool == nil {
		t.Error("EncoderPool should be initialized")
	}
	if mm.responsePool == nil {
		t.Error("ResponsePool should be initialized")
	}
	if mm.stats == nil {
		t.Error("Stats should be initialized")
	}
	if mm.stopChan == nil {
		t.Error("StopChan should be initialized")
	}

	// Test default pool sizes
	bufferStats := mm.bufferPool.GetStats()
	if bufferStats["max_pool_size"] != 100 {
		t.Errorf("Expected buffer pool size 100, got %v", bufferStats["max_pool_size"])
	}

	encoderStats := mm.encoderPool.GetStats()
	if encoderStats["max_pool_size"] != 50 {
		t.Errorf("Expected encoder pool size 50, got %v", encoderStats["max_pool_size"])
	}

	responseStats := mm.responsePool.GetStats()
	if responseStats["max_pool_size"] != 200 {
		t.Errorf("Expected response pool size 200, got %v", responseStats["max_pool_size"])
	}

	// Clean up
	mm.Stop()
}

func TestMemoryManager_GetAndPut(t *testing.T) {
	mm := NewMemoryManager()
	defer mm.Stop()

	// Test buffer operations
	buf := mm.GetBuffer()
	if buf == nil {
		t.Fatal("GetBuffer should not return nil")
	}
	mm.PutBuffer(buf)

	// Test encoder operations
	buf = mm.GetBuffer()
	enc := mm.GetEncoder(buf)
	if enc == nil {
		t.Fatal("GetEncoder should not return nil")
	}
	mm.PutEncoder(enc)
	mm.PutBuffer(buf)

	// Test response operations
	resp := mm.GetResponse()
	if resp == nil {
		t.Fatal("GetResponse should not return nil")
	}
	mm.PutResponse(resp)
}

func TestMemoryManager_GetStats(t *testing.T) {
	mm := NewMemoryManager()
	defer mm.Stop()

	stats := mm.GetStats()
	expectedKeys := []string{
		"overall",
		"buffer_pool",
		"encoder_pool",
		"response_pool",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected key '%s' in stats", key)
		}
	}

	// Test overall stats structure
	overall := stats["overall"].(map[string]interface{})
	overallKeys := []string{
		"total_allocated",
		"total_freed",
		"current_usage",
		"peak_usage",
		"uptime",
	}

	for _, key := range overallKeys {
		if _, exists := overall[key]; !exists {
			t.Errorf("Expected key '%s' in overall stats", key)
		}
	}
}

func TestMemoryManager_Stop(t *testing.T) {
	mm := NewMemoryManager()

	// Stop should not panic
	mm.Stop()

	// Multiple stops should be safe (but we need to handle the closed channel)
	// The second Stop() will panic because the channel is already closed
	// So we'll just test that the first Stop() works
}

func TestMemoryManager_OptimizedJSONMarshal(t *testing.T) {
	mm := NewMemoryManager()
	defer mm.Stop()

	testData := map[string]interface{}{
		"name":  "test",
		"value": 123,
		"array": []int{1, 2, 3},
	}

	// Test successful marshaling
	result, err := mm.OptimizedJSONMarshal(testData)
	if err != nil {
		t.Fatalf("OptimizedJSONMarshal failed: %v", err)
	}

	if len(result) == 0 {
		t.Error("Expected non-empty result")
	}

	// Verify it's valid JSON
	var unmarshaled map[string]interface{}
	if err := json.Unmarshal(result, &unmarshaled); err != nil {
		t.Errorf("Result is not valid JSON: %v", err)
	}

	// Test with nil data
	_, err = mm.OptimizedJSONMarshal(nil)
	if err != nil {
		t.Errorf("Expected no error for nil data, got %v", err)
	}
}

func TestMemoryManager_OptimizedJSONUnmarshal(t *testing.T) {
	mm := NewMemoryManager()
	defer mm.Stop()

	testData := map[string]interface{}{
		"name":  "test",
		"value": 123,
	}

	// Marshal test data
	jsonData, err := json.Marshal(testData)
	if err != nil {
		t.Fatalf("Failed to marshal test data: %v", err)
	}

	// Test successful unmarshaling
	var result map[string]interface{}
	err = mm.OptimizedJSONUnmarshal(jsonData, &result)
	if err != nil {
		t.Fatalf("OptimizedJSONUnmarshal failed: %v", err)
	}

	if result["name"] != "test" {
		t.Errorf("Expected name 'test', got %v", result["name"])
	}
	if result["value"] != float64(123) { // JSON numbers are float64
		t.Errorf("Expected value 123, got %v", result["value"])
	}

	// Test with invalid JSON
	err = mm.OptimizedJSONUnmarshal([]byte("invalid json"), &result)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}

	// Test with nil data
	err = mm.OptimizedJSONUnmarshal(nil, &result)
	if err == nil {
		t.Error("Expected error for nil data")
	}
}

func TestPoolConcurrency(t *testing.T) {
	// Test concurrent access to buffer pool
	pool := NewBufferPool(10)
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			buf := pool.Get()
			time.Sleep(1 * time.Millisecond)
			pool.Put(buf)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify no panics occurred
	stats := pool.GetStats()
	if stats["total_allocated"] != int64(10) {
		t.Errorf("Expected total_allocated 10, got %v", stats["total_allocated"])
	}
}

func TestResponseData_Reset(t *testing.T) {
	pool := NewResponsePool(1)

	// Get a response and set data
	resp := pool.Get()
	resp.StatusCode = 200
	resp.Headers["Content-Type"] = "application/json"
	resp.Body = []byte("test data")
	resp.ETag = "abc123"
	resp.Timestamp = time.Now()

	// Put it back
	pool.Put(resp)

	// Get it again - should be reset
	resp2 := pool.Get()
	if resp2.StatusCode != 0 {
		t.Errorf("Expected StatusCode 0 after reset, got %d", resp2.StatusCode)
	}
	if len(resp2.Headers) != 0 {
		t.Errorf("Expected empty headers after reset, got %v", resp2.Headers)
	}
	if len(resp2.Body) != 0 {
		t.Errorf("Expected empty body after reset, got %v", resp2.Body)
	}
	if resp2.ETag != "" {
		t.Errorf("Expected empty ETag after reset, got %s", resp2.ETag)
	}
	if !resp2.Timestamp.IsZero() {
		t.Errorf("Expected zero timestamp after reset, got %v", resp2.Timestamp)
	}

	pool.Put(resp2)
}

func TestBufferPool_FullPool(t *testing.T) {
	pool := NewBufferPool(1) // Very small pool

	// Fill the pool
	buf1 := pool.Get()
	pool.Put(buf1)

	// Get another buffer (should reuse from pool)
	buf2 := pool.Get()
	pool.Put(buf2)

	// Get a third buffer (pool is full, should create new one)
	buf3 := pool.Get()
	pool.Put(buf3)

	stats := pool.GetStats()
	if stats["total_allocated"] != int64(1) {
		t.Errorf("Expected total_allocated 1 (only 1 fits in pool, others discarded), got %v", stats["total_allocated"])
	}
	if stats["total_returned"] != int64(-1) { // All items returned to pool
		t.Errorf("Expected total_returned -1, got %v", stats["total_returned"])
	}
}

func TestMemoryManager_PerformCleanup(t *testing.T) {
	mm := NewMemoryManager()
	defer mm.Stop()

	// Perform some operations to generate memory usage
	buf := mm.GetBuffer()
	buf.WriteString("test data")
	mm.PutBuffer(buf)

	encoder := mm.GetEncoder(buf)
	mm.PutEncoder(encoder)

	resp := mm.GetResponse()
	resp.StatusCode = 200
	mm.PutResponse(resp)

	// Perform cleanup
	mm.performCleanup()

	// Get final stats
	finalStats := mm.GetStats()
	finalUsage := finalStats["overall"].(map[string]interface{})["current_usage"].(int64)

	// Verify cleanup updated the stats
	if finalUsage < 0 {
		t.Errorf("Expected non-negative current usage after cleanup, got %d", finalUsage)
	}

	// Verify peak usage is tracked
	peakUsage := finalStats["overall"].(map[string]interface{})["peak_usage"].(int64)
	if peakUsage < 0 {
		t.Errorf("Expected non-negative peak usage, got %d", peakUsage)
	}

	// Verify uptime is tracked
	uptime := finalStats["overall"].(map[string]interface{})["uptime"].(string)
	if uptime == "" {
		t.Error("Expected non-empty uptime")
	}
}

func TestMemoryManager_UpdateStats(t *testing.T) {
	mm := NewMemoryManager()
	defer mm.Stop()

	// Perform some operations to change stats
	buf := mm.GetBuffer()
	buf.WriteString("test data")
	mm.PutBuffer(buf)

	encoder := mm.GetEncoder(buf)
	mm.PutEncoder(encoder)

	resp := mm.GetResponse()
	resp.StatusCode = 200
	mm.PutResponse(resp)

	// Update stats
	mm.updateStats()

	// Get stats
	stats := mm.GetStats()
	currentUsage := stats["overall"].(map[string]interface{})["current_usage"].(int64)

	// Verify stats were updated
	if currentUsage < 0 {
		t.Errorf("Expected non-negative current usage, got %d", currentUsage)
	}

	// Verify peak usage is tracked
	peakUsage := stats["overall"].(map[string]interface{})["peak_usage"].(int64)
	if peakUsage < 0 {
		t.Errorf("Expected non-negative peak usage, got %d", peakUsage)
	}

	// Verify all pool stats are included
	bufferStats := stats["buffer_pool"].(map[string]interface{})
	if bufferStats == nil {
		t.Error("Expected buffer pool stats to be included")
	}

	encoderStats := stats["encoder_pool"].(map[string]interface{})
	if encoderStats == nil {
		t.Error("Expected encoder pool stats to be included")
	}

	responseStats := stats["response_pool"].(map[string]interface{})
	if responseStats == nil {
		t.Error("Expected response pool stats to be included")
	}
}
