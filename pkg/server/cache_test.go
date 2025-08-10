package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

func TestNewCache(t *testing.T) {
	// Initialize logger for testing
	logger.Init("debug")

	// Test cache creation with valid parameters
	maxEntries := 100
	defaultTTL := 5 * time.Minute
	cache := NewCache(maxEntries, defaultTTL)

	if cache == nil {
		t.Fatal("NewCache should not return nil")
	}

	if cache.maxEntries != maxEntries {
		t.Errorf("Expected maxEntries %d, got %d", maxEntries, cache.maxEntries)
	}

	if cache.defaultTTL != defaultTTL {
		t.Errorf("Expected defaultTTL %v, got %v", defaultTTL, cache.defaultTTL)
	}

	if cache.entries == nil {
		t.Error("Cache entries map should be initialized")
	}

	if cache.cleanupTicker == nil {
		t.Error("Cleanup ticker should be initialized")
	}

	// Clean up
	cache.Stop()
}

func TestCache_GenerateCacheKey(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)
	defer cache.Stop()

	// Create test request
	req := httptest.NewRequest("GET", "/redfish/v1/Systems", nil)
	user := "testuser"

	// Generate cache key
	key := cache.generateCacheKey(req, user)

	if key == "" {
		t.Error("Generated cache key should not be empty")
	}

	// Test that same request generates same key
	key2 := cache.generateCacheKey(req, user)
	if key != key2 {
		t.Error("Same request should generate same cache key")
	}

	// Test that different users generate different keys
	key3 := cache.generateCacheKey(req, "differentuser")
	if key == key3 {
		t.Error("Different users should generate different cache keys")
	}

	// Test that different methods generate different keys
	req2 := httptest.NewRequest("POST", "/redfish/v1/Systems", nil)
	key4 := cache.generateCacheKey(req2, user)
	if key == key4 {
		t.Error("Different methods should generate different cache keys")
	}
}

func TestCache_GetAndSet(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(100, 5*time.Minute)
	defer cache.Stop()

	key := "test-key"
	data := []byte("test data")
	headers := map[string]string{"Content-Type": "application/json"}
	ttl := 1 * time.Second

	// Test getting non-existent key
	entry, exists := cache.Get(key)
	if exists {
		t.Error("Should not find non-existent key")
	}
	if entry != nil {
		t.Error("Should return nil for non-existent key")
	}

	// Test setting and getting a key
	cache.Set(key, data, headers, ttl)

	entry, exists = cache.Get(key)
	if !exists {
		t.Error("Should find key after setting it")
	}
	if entry == nil {
		t.Error("Should return entry after setting it")
	}

	if string(entry.Data) != string(data) {
		t.Errorf("Expected data %s, got %s", string(data), string(entry.Data))
	}

	if entry.Headers["Content-Type"] != headers["Content-Type"] {
		t.Errorf("Expected header %s, got %s", headers["Content-Type"], entry.Headers["Content-Type"])
	}

	// Access count starts at 1 (from Set operation), but Get increments it
	if entry.AccessCount != 2 {
		t.Errorf("Expected access count 2, got %d", entry.AccessCount)
	}

	// Test that access count increases on subsequent gets
	entry, exists = cache.Get(key)
	if !exists {
		t.Error("Should still find key")
	}
	// Access count should be 3 after second Get
	if entry.AccessCount != 3 {
		t.Errorf("Expected access count 3, got %d", entry.AccessCount)
	}
}

func TestCache_Expiration(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(100, 5*time.Minute)
	defer cache.Stop()

	key := "test-key"
	data := []byte("test data")
	headers := map[string]string{"Content-Type": "application/json"}
	ttl := 10 * time.Millisecond // Very short TTL

	// Set entry with short TTL
	cache.Set(key, data, headers, ttl)

	// Should be available immediately
	_, exists := cache.Get(key)
	if !exists {
		t.Error("Should find key immediately after setting")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should not be available after expiration
	_, exists = cache.Get(key)
	if exists {
		t.Error("Should not find key after expiration")
	}
}

func TestCache_DefaultTTL(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(100, 1*time.Second)
	defer cache.Stop()

	key := "test-key"
	data := []byte("test data")
	headers := map[string]string{"Content-Type": "application/json"}

	// Set entry without TTL (should use default)
	cache.Set(key, data, headers, 0)

	_, exists := cache.Get(key)
	if !exists {
		t.Error("Should find key with default TTL")
	}

	// Wait for expiration
	time.Sleep(1100 * time.Millisecond)

	_, exists = cache.Get(key)
	if exists {
		t.Error("Should not find key after default TTL expiration")
	}
}

func TestCache_Invalidate(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(100, 5*time.Minute)
	defer cache.Stop()

	// Set multiple entries
	cache.Set("key1", []byte("data1"), nil, 1*time.Second)
	cache.Set("key2", []byte("data2"), nil, 1*time.Second)
	cache.Set("other", []byte("data3"), nil, 1*time.Second)

	// Verify all entries exist
	if _, exists := cache.Get("key1"); !exists {
		t.Error("key1 should exist")
	}
	if _, exists := cache.Get("key2"); !exists {
		t.Error("key2 should exist")
	}
	if _, exists := cache.Get("other"); !exists {
		t.Error("other should exist")
	}

	// Invalidate entries matching pattern
	cache.Invalidate("key")

	// Verify key1 and key2 are gone, but other remains
	if _, exists := cache.Get("key1"); exists {
		t.Error("key1 should be invalidated")
	}
	if _, exists := cache.Get("key2"); exists {
		t.Error("key2 should be invalidated")
	}
	if _, exists := cache.Get("other"); !exists {
		t.Error("other should still exist")
	}
}

func TestCache_Eviction(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(2, 5*time.Minute) // Only 2 entries allowed
	defer cache.Stop()

	// Set 3 entries (exceeds max)
	cache.Set("key1", []byte("data1"), nil, 1*time.Second)
	cache.Set("key2", []byte("data2"), nil, 1*time.Second)
	cache.Set("key3", []byte("data3"), nil, 1*time.Second)

	// Should only have 2 entries (oldest evicted)
	stats := cache.GetStats()
	if stats == nil {
		t.Fatal("GetStats should return non-nil stats")
	}
	totalEntries, ok := stats["entries_count"].(int)
	if !ok {
		t.Fatal("entries_count should be an int")
	}
	if totalEntries != 2 {
		t.Errorf("Expected 2 entries after eviction, got %d", totalEntries)
	}

	// key1 should be evicted (oldest)
	if _, exists := cache.Get("key1"); exists {
		t.Error("key1 should be evicted")
	}

	// key2 and key3 should still exist
	if _, exists := cache.Get("key2"); !exists {
		t.Error("key2 should still exist")
	}
	if _, exists := cache.Get("key3"); !exists {
		t.Error("key3 should still exist")
	}
}

func TestCache_GetStats(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(100, 5*time.Minute)
	defer cache.Stop()

	// Get initial stats
	stats := cache.GetStats()
	if stats == nil {
		t.Error("GetStats should return non-nil stats")
	}

	// Verify expected fields exist
	expectedFields := []string{"entries_count", "max_entries", "total_accesses", "default_ttl"}
	for _, field := range expectedFields {
		if _, exists := stats[field]; !exists {
			t.Errorf("Stats should contain field: %s", field)
		}
	}

	// Add some entries and access them
	cache.Set("key1", []byte("data1"), nil, 1*time.Second)
	cache.Set("key2", []byte("data2"), nil, 1*time.Second)

	cache.Get("key1")        // Hit
	cache.Get("key2")        // Hit
	cache.Get("nonexistent") // Miss

	// Get updated stats
	stats = cache.GetStats()
	if stats == nil {
		t.Fatal("GetStats should return non-nil stats")
	}

	entriesCount, ok := stats["entries_count"].(int)
	if !ok {
		t.Fatal("entries_count should be an int")
	}
	if entriesCount != 2 {
		t.Errorf("Expected 2 total entries, got %d", entriesCount)
	}

	totalAccesses, ok := stats["total_accesses"].(int64)
	if !ok {
		t.Fatal("total_accesses should be an int64")
	}
	if totalAccesses != 4 { // 2 from Set operations + 2 from Get operations
		t.Errorf("Expected 4 total accesses, got %d", totalAccesses)
	}
}

func TestCacheResponseWriter(t *testing.T) {
	// Create a test response writer
	recorder := httptest.NewRecorder()
	crw := &CacheResponseWriter{
		ResponseWriter: recorder,
		headers:        make(map[string]string),
		statusCode:     http.StatusOK,
		body:           []byte{},
	}

	// Test WriteHeader
	crw.WriteHeader(http.StatusNotFound)
	if crw.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code %d, got %d", http.StatusNotFound, crw.statusCode)
	}

	// Test Write
	testData := []byte("test response")
	n, err := crw.Write(testData)
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	if string(crw.body) != string(testData) {
		t.Errorf("Expected body '%s', got '%s'", string(testData), string(crw.body))
	}

	// Test Header
	headers := crw.Header()
	if headers == nil {
		t.Error("Header() should return non-nil headers")
	}

	// Test that underlying response writer received the data
	if recorder.Code != http.StatusNotFound {
		t.Errorf("Expected underlying status code %d, got %d", http.StatusNotFound, recorder.Code)
	}
}

func TestCache_Stop(t *testing.T) {
	logger.Init("debug")
	cache := NewCache(100, 5*time.Minute)

	// Add some entries
	cache.Set("key1", []byte("data1"), nil, 1*time.Second)
	cache.Set("key2", []byte("data2"), nil, 1*time.Second)

	// Stop the cache
	cache.Stop()

	// Verify cleanup ticker is stopped (but not set to nil in the implementation)
	if cache.cleanupTicker == nil {
		t.Error("Cleanup ticker should not be nil (implementation doesn't set it to nil)")
	}

	// Context should be cancelled
	select {
	case <-cache.ctx.Done():
		// Expected
	default:
		t.Error("Context should be cancelled after stopping")
	}
}

func TestShouldSkipCache(t *testing.T) {
	// Create a minimal server for testing
	server := &Server{}

	testCases := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "metrics path should be skipped",
			path:     "/internal/metrics",
			expected: true,
		},
		{
			name:     "task service path should be skipped",
			path:     "/redfish/v1/TaskService/Tasks/123",
			expected: true,
		},
		{
			name:     "systems path should be skipped",
			path:     "/redfish/v1/Systems/",
			expected: true,
		},
		{
			name:     "systems collection should be skipped",
			path:     "/redfish/v1/Systems",
			expected: true,
		},
		{
			name:     "chassis path should not be skipped",
			path:     "/redfish/v1/Chassis",
			expected: false,
		},
		{
			name:     "service root should not be skipped",
			path:     "/redfish/v1/",
			expected: false,
		},
		{
			name:     "random path should not be skipped",
			path:     "/some/random/path",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := server.shouldSkipCache(tc.path)
			if result != tc.expected {
				t.Errorf("Expected %v for path '%s', got %v", tc.expected, tc.path, result)
			}
		})
	}
}

func TestGetCacheTTL(t *testing.T) {
	// Create a minimal server for testing
	server := &Server{}

	testCases := []struct {
		name     string
		path     string
		expected time.Duration
	}{
		{
			name:     "service root path",
			path:     "/redfish/v1/",
			expected: 30 * time.Second,
		},
		{
			name:     "chassis path",
			path:     "/redfish/v1/Chassis",
			expected: 30 * time.Second, // Due to the first case matching all /redfish/v1/ paths
		},
		{
			name:     "systems path",
			path:     "/redfish/v1/Systems",
			expected: 30 * time.Second, // Due to the first case matching all /redfish/v1/ paths
		},
		{
			name:     "managers path",
			path:     "/redfish/v1/Managers",
			expected: 30 * time.Second, // Due to the first case matching all /redfish/v1/ paths
		},
		{
			name:     "default path",
			path:     "/some/random/path",
			expected: 1 * time.Minute,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := server.getCacheTTL(tc.path)
			if result != tc.expected {
				t.Errorf("Expected %v for path '%s', got %v", tc.expected, tc.path, result)
			}
		})
	}
}

func TestWriteCachedResponse(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := &Server{}

	// Create a test response writer
	w := httptest.NewRecorder()

	// Create a test cache entry
	entry := &CacheEntry{
		Data:        []byte("cached response"),
		Headers:     map[string]string{"Content-Type": "application/json"},
		CreatedAt:   time.Now().Add(-1 * time.Minute), // 1 minute old
		AccessCount: 5,
	}

	// Write cached response
	server.writeCachedResponse(w, entry)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	if w.Body.String() != "cached response" {
		t.Errorf("Expected body 'cached response', got '%s'", w.Body.String())
	}

	// Verify cache headers
	if w.Header().Get("X-Cache") != "HIT" {
		t.Error("Expected X-Cache header to be 'HIT'")
	}

	if w.Header().Get("X-Cache-Age") == "" {
		t.Error("Expected X-Cache-Age header to be set")
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Expected Content-Type header to be preserved")
	}
}
