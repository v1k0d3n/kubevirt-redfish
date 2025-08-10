package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

func TestNewHTTPClientPool(t *testing.T) {
	// Initialize logger for testing
	logger.Init("debug")

	maxSize := 10
	timeout := 30 * time.Second

	pool := NewHTTPClientPool(maxSize, timeout)

	if pool == nil {
		t.Fatal("NewHTTPClientPool should not return nil")
	}

	if pool.maxSize != maxSize {
		t.Errorf("Expected maxSize %d, got %d", maxSize, pool.maxSize)
	}

	if pool.timeout != timeout {
		t.Errorf("Expected timeout %v, got %v", timeout, pool.timeout)
	}

	if pool.clients == nil {
		t.Error("Expected clients channel to be initialized")
	}

	if pool.stats == nil {
		t.Error("Expected stats to be initialized")
	}

	if pool.stopChan == nil {
		t.Error("Expected stopChan to be initialized")
	}

	// Verify initial stats
	stats := pool.GetStats()
	if stats == nil {
		t.Fatal("GetStats should not return nil")
	}

	// Clean up
	pool.Stop()
}

func TestHTTPClientPool_Get(t *testing.T) {
	logger.Init("debug")

	pool := NewHTTPClientPool(5, 30*time.Second)
	defer pool.Stop()

	// Get a client
	client := pool.Get()
	if client == nil {
		t.Fatal("Get should not return nil")
	}

	// Verify client has timeout set
	if client.Timeout != 30*time.Second {
		t.Errorf("Expected timeout %v, got %v", 30*time.Second, client.Timeout)
	}

	// Verify transport is set
	if client.Transport == nil {
		t.Error("Expected transport to be set")
	}

	// Get multiple clients
	clients := make([]*http.Client, 0, 5)
	for i := 0; i < 5; i++ {
		client := pool.Get()
		if client != nil {
			clients = append(clients, client)
		}
	}

	// Should be able to get at least some clients
	if len(clients) == 0 {
		t.Error("Expected to be able to get at least one client")
	}
}

func TestHTTPClientPool_Put(t *testing.T) {
	logger.Init("debug")

	pool := NewHTTPClientPool(3, 30*time.Second)
	defer pool.Stop()

	// Get a client
	client := pool.Get()
	if client == nil {
		t.Fatal("Get should not return nil")
	}

	// Put it back
	pool.Put(client)

	// Get it again
	client2 := pool.Get()
	if client2 == nil {
		t.Fatal("Get should not return nil after Put")
	}

	// Put multiple clients
	for i := 0; i < 5; i++ {
		client := pool.Get()
		if client != nil {
			pool.Put(client)
		}
	}
}

func TestHTTPClientPool_GetStats(t *testing.T) {
	logger.Init("debug")

	pool := NewHTTPClientPool(5, 30*time.Second)
	defer pool.Stop()

	// Get initial stats
	stats := pool.GetStats()
	if stats == nil {
		t.Fatal("GetStats should not return nil")
	}

	// Verify stats structure
	expectedKeys := []string{
		"total_created",
		"total_reused",
		"total_closed",
		"current_active",
		"max_connections",
		"pool_size",
		"timeout",
		"uptime",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected stats to contain key '%s'", key)
		}
	}

	// Get and put some clients to exercise the pool
	client := pool.Get()
	if client != nil {
		pool.Put(client)
	}

	// Get stats again to ensure it still works
	stats2 := pool.GetStats()
	if stats2 == nil {
		t.Fatal("GetStats should not return nil after operations")
	}

	// Verify the structure is still correct
	for _, key := range expectedKeys {
		if _, exists := stats2[key]; !exists {
			t.Errorf("Expected stats to contain key '%s' after operations", key)
		}
	}
}

func TestHTTPClientPool_Stop(t *testing.T) {
	logger.Init("debug")

	pool := NewHTTPClientPool(5, 30*time.Second)

	// Stop the pool
	pool.Stop()

	// Verify stopChan is closed
	select {
	case <-pool.stopChan:
		// Expected
	default:
		t.Error("Expected stopChan to be closed after Stop")
	}
}

func TestNewConnectionManager(t *testing.T) {
	logger.Init("debug")

	cm := NewConnectionManager()

	if cm == nil {
		t.Fatal("NewConnectionManager should not return nil")
	}

	if cm.httpPool == nil {
		t.Error("Expected httpPool to be initialized")
	}

	if cm.stats == nil {
		t.Error("Expected stats to be initialized")
	}

	if cm.stopChan == nil {
		t.Error("Expected stopChan to be initialized")
	}

	// Clean up
	cm.Stop()
}

func TestConnectionManager_GetHTTPClient(t *testing.T) {
	logger.Init("debug")

	cm := NewConnectionManager()
	defer cm.Stop()

	// Get HTTP client
	client := cm.GetHTTPClient()
	if client == nil {
		t.Fatal("GetHTTPClient should not return nil")
	}

	// Verify client has timeout set
	if client.Timeout == 0 {
		t.Error("Expected client to have timeout set")
	}
}

func TestConnectionManager_PutHTTPClient(t *testing.T) {
	logger.Init("debug")

	cm := NewConnectionManager()
	defer cm.Stop()

	// Get and put HTTP client
	client := cm.GetHTTPClient()
	if client == nil {
		t.Fatal("GetHTTPClient should not return nil")
	}

	cm.PutHTTPClient(client)

	// Get another client
	client2 := cm.GetHTTPClient()
	if client2 == nil {
		t.Fatal("GetHTTPClient should not return nil after Put")
	}
}

func TestConnectionManager_DoRequest(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	cm := NewConnectionManager()
	defer cm.Stop()

	// Create a request
	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Do the request
	resp, err := cm.DoRequest(req)
	if err != nil {
		t.Fatalf("DoRequest failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestConnectionManager_DoRequestWithContext(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	cm := NewConnectionManager()
	defer cm.Stop()

	// Create a request with context
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Do the request
	resp, err := cm.DoRequestWithContext(ctx, req)
	if err != nil {
		t.Fatalf("DoRequestWithContext failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Test with cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	req2, err := http.NewRequestWithContext(cancelledCtx, "GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	_, err = cm.DoRequestWithContext(cancelledCtx, req2)
	if err == nil {
		t.Error("Expected error when context is cancelled")
	}
}

func TestConnectionManager_GetStats(t *testing.T) {
	logger.Init("debug")

	cm := NewConnectionManager()
	defer cm.Stop()

	// Get stats
	stats := cm.GetStats()
	if stats == nil {
		t.Fatal("GetStats should not return nil")
	}

	// Verify stats structure
	expectedKeys := []string{
		"overall",
		"http_pool",
	}

	for _, key := range expectedKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected stats to contain key '%s'", key)
		}
	}

	// Verify overall stats structure
	if overall, exists := stats["overall"]; exists {
		if overallMap, ok := overall.(map[string]interface{}); ok {
			expectedOverallKeys := []string{
				"total_connections",
				"active_connections",
				"peak_connections",
				"uptime",
			}

			for _, key := range expectedOverallKeys {
				if _, exists := overallMap[key]; !exists {
					t.Errorf("Expected overall stats to contain key '%s'", key)
				}
			}
		}
	}
}

func TestConnectionManager_Stop(t *testing.T) {
	logger.Init("debug")

	cm := NewConnectionManager()

	// Stop the manager
	cm.Stop()

	// Verify stopChan is closed
	select {
	case <-cm.stopChan:
		// Expected
	default:
		t.Error("Expected stopChan to be closed after Stop")
	}
}

func TestNewOptimizedHTTPClient(t *testing.T) {
	logger.Init("debug")

	pool := NewHTTPClientPool(5, 30*time.Second)
	defer pool.Stop()

	ohc := NewOptimizedHTTPClient(pool)

	if ohc == nil {
		t.Fatal("NewOptimizedHTTPClient should not return nil")
	}

	if ohc.pool != pool {
		t.Error("Expected pool to be set correctly")
	}
}

func TestOptimizedHTTPClient_Get(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	pool := NewHTTPClientPool(5, 30*time.Second)
	defer pool.Stop()

	ohc := NewOptimizedHTTPClient(pool)

	// Test GET request
	resp, err := ohc.Get(server.URL)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestOptimizedHTTPClient_Post(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST method, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	pool := NewHTTPClientPool(5, 30*time.Second)
	defer pool.Stop()

	ohc := NewOptimizedHTTPClient(pool)

	// Test POST request with string body
	resp, err := ohc.Post(server.URL, "text/plain", "test data")
	if err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Test POST request with map body
	body := map[string]string{"key": "value"}
	resp2, err := ohc.Post(server.URL, "application/json", body)
	if err != nil {
		t.Fatalf("Post with map failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp2.StatusCode)
	}
}

func TestOptimizedHTTPClient_Do(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	pool := NewHTTPClientPool(5, 30*time.Second)
	defer pool.Stop()

	ohc := NewOptimizedHTTPClient(pool)

	// Create a request
	req, err := http.NewRequest("GET", server.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Do the request
	resp, err := ohc.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestHTTPClientPool_ConcurrentAccess(t *testing.T) {
	logger.Init("debug")

	pool := NewHTTPClientPool(10, 30*time.Second)
	defer pool.Stop()

	// Test concurrent access
	const numGoroutines = 20
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				client := pool.Get()
				if client != nil {
					pool.Put(client)
				}
			}
		}()
	}

	wg.Wait()

	// Verify pool is still functional
	client := pool.Get()
	if client == nil {
		t.Error("Pool should still be functional after concurrent access")
	} else {
		pool.Put(client)
	}
}

func TestConnectionManager_ConcurrentAccess(t *testing.T) {
	logger.Init("debug")

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	cm := NewConnectionManager()
	defer cm.Stop()

	// Test concurrent access
	const numGoroutines = 10
	const numOperations = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				client := cm.GetHTTPClient()
				if client != nil {
					cm.PutHTTPClient(client)
				}
			}
		}()
	}

	wg.Wait()

	// Verify manager is still functional
	client := cm.GetHTTPClient()
	if client == nil {
		t.Error("Manager should still be functional after concurrent access")
	} else {
		cm.PutHTTPClient(client)
	}
}

func TestHTTPClientPool_EdgeCases(t *testing.T) {
	logger.Init("debug")

	// Test with zero size pool
	pool := NewHTTPClientPool(0, 30*time.Second)
	defer pool.Stop()

	client := pool.Get()
	if client == nil {
		t.Error("Should be able to get client even with zero size pool")
	}

	// Test with very small timeout
	pool2 := NewHTTPClientPool(5, 1*time.Millisecond)
	defer pool2.Stop()

	client2 := pool2.Get()
	if client2 == nil {
		t.Error("Should be able to get client with small timeout")
	}

	// Test putting nil client
	pool2.Put(nil)
}

func TestConnectionManager_EdgeCases(t *testing.T) {
	logger.Init("debug")

	cm := NewConnectionManager()
	defer cm.Stop()

	// Test putting nil client
	cm.PutHTTPClient(nil)

	// Test DoRequest with nil request - this will panic, so we need to recover
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when request is nil")
			}
		}()
		cm.DoRequest(nil)
	}()

	// Test DoRequestWithContext with nil request - this will panic, so we need to recover
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when request is nil")
			}
		}()
		cm.DoRequestWithContext(context.Background(), nil)
	}()
}
