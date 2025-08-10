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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// HTTPClientPool manages reusable HTTP clients
type HTTPClientPool struct {
	clients       chan *http.Client
	maxSize       int
	timeout       time.Duration
	stats         *ConnectionStats
	mutex         sync.RWMutex
	cleanupTicker *time.Ticker
	stopChan      chan struct{}
}

// ConnectionStats tracks connection pool performance
type ConnectionStats struct {
	TotalCreated   int64
	TotalReused    int64
	TotalClosed    int64
	CurrentActive  int64
	MaxConnections int64
	LastReset      time.Time
}

// NewHTTPClientPool creates a new HTTP client pool
func NewHTTPClientPool(maxSize int, timeout time.Duration) *HTTPClientPool {
	pool := &HTTPClientPool{
		clients: make(chan *http.Client, maxSize),
		maxSize: maxSize,
		timeout: timeout,
		stats: &ConnectionStats{
			LastReset: time.Now(),
		},
		stopChan: make(chan struct{}),
	}

	// Pre-populate the pool with clients
	for i := 0; i < maxSize/2; i++ {
		client := pool.createClient()
		select {
		case pool.clients <- client:
			pool.updateStats(0, 1, 0) // Client created and added to pool
		default:
			// Pool is full, close the client
			pool.updateStats(0, 0, 1) // Client created but discarded
		}
	}

	// Start cleanup routine
	go pool.cleanupRoutine()

	return pool
}

// createClient creates a new HTTP client with optimized settings
func (hcp *HTTPClientPool) createClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   hcp.timeout,
	}
}

// Get retrieves an HTTP client from the pool or creates a new one
func (hcp *HTTPClientPool) Get() *http.Client {
	select {
	case client := <-hcp.clients:
		hcp.updateStats(1, 0, 0) // Client reused from pool
		return client
	default:
		hcp.updateStats(0, 1, 0) // New client created
		return hcp.createClient()
	}
}

// Put returns an HTTP client to the pool
func (hcp *HTTPClientPool) Put(client *http.Client) {
	if client == nil {
		return
	}

	select {
	case hcp.clients <- client:
		hcp.updateStats(-1, 0, 0) // Client returned to pool
	default:
		// Pool is full, close the client
		hcp.updateStats(0, 0, 1) // Client discarded
	}
}

// updateStats updates connection statistics
func (hcp *HTTPClientPool) updateStats(reusedDelta, createdDelta, closedDelta int64) {
	hcp.mutex.Lock()
	defer hcp.mutex.Unlock()

	hcp.stats.TotalReused += reusedDelta
	hcp.stats.TotalCreated += createdDelta
	hcp.stats.TotalClosed += closedDelta
	hcp.stats.CurrentActive = hcp.stats.TotalCreated - hcp.stats.TotalClosed
}

// GetStats returns connection pool statistics
func (hcp *HTTPClientPool) GetStats() map[string]interface{} {
	hcp.mutex.RLock()
	defer hcp.mutex.RUnlock()

	return map[string]interface{}{
		"total_created":   hcp.stats.TotalCreated,
		"total_reused":    hcp.stats.TotalReused,
		"total_closed":    hcp.stats.TotalClosed,
		"current_active":  hcp.stats.CurrentActive,
		"max_connections": hcp.maxSize,
		"pool_size":       len(hcp.clients),
		"timeout":         hcp.timeout.String(),
		"uptime":          time.Since(hcp.stats.LastReset).String(),
	}
}

// cleanupRoutine periodically cleans up idle connections
func (hcp *HTTPClientPool) cleanupRoutine() {
	hcp.cleanupTicker = time.NewTicker(2 * time.Minute)
	defer hcp.cleanupTicker.Stop()

	for {
		select {
		case <-hcp.cleanupTicker.C:
			hcp.performCleanup()
		case <-hcp.stopChan:
			logger.Info("HTTP client pool cleanup routine stopped")
			return
		}
	}
}

// performCleanup performs connection cleanup operations
func (hcp *HTTPClientPool) performCleanup() {
	// Close excess clients in the pool
	poolSize := len(hcp.clients)
	if poolSize > hcp.maxSize/2 {
		excess := poolSize - hcp.maxSize/2
		for i := 0; i < excess; i++ {
			select {
			case _ = <-hcp.clients:
				hcp.updateStats(0, 0, 1) // Client closed during cleanup
			default:
				break
			}
		}
		logger.Debug("Cleaned up %d excess HTTP clients", excess)
	}
}

// Stop gracefully stops the HTTP client pool
func (hcp *HTTPClientPool) Stop() {
	logger.Info("Stopping HTTP client pool...")

	if hcp.cleanupTicker != nil {
		hcp.cleanupTicker.Stop()
	}

	close(hcp.stopChan)

	// Close all clients in the pool
	closeCount := 0
	for {
		select {
		case _ = <-hcp.clients:
			hcp.updateStats(0, 0, 1) // Client closed
			closeCount++
		default:
			logger.Info("HTTP client pool stopped, closed %d clients", closeCount)
			return
		}
	}
}

// ConnectionManager coordinates all connection pools
type ConnectionManager struct {
	httpPool      *HTTPClientPool
	stats         *ManagerStats
	statsMutex    sync.RWMutex
	cleanupTicker *time.Ticker
	stopChan      chan struct{}
}

// ManagerStats tracks overall connection management
type ManagerStats struct {
	TotalConnections  int64
	ActiveConnections int64
	PeakConnections   int64
	LastReset         time.Time
}

// NewConnectionManager creates a new connection manager
func NewConnectionManager() *ConnectionManager {
	cm := &ConnectionManager{
		httpPool: NewHTTPClientPool(50, 30*time.Second), // 50 clients, 30s timeout
		stats: &ManagerStats{
			LastReset: time.Now(),
		},
		stopChan: make(chan struct{}),
	}

	// Start cleanup routine
	go cm.cleanupRoutine()

	return cm
}

// GetHTTPClient retrieves an HTTP client from the pool
func (cm *ConnectionManager) GetHTTPClient() *http.Client {
	return cm.httpPool.Get()
}

// PutHTTPClient returns an HTTP client to the pool
func (cm *ConnectionManager) PutHTTPClient(client *http.Client) {
	cm.httpPool.Put(client)
}

// DoRequest performs an HTTP request using pooled client
func (cm *ConnectionManager) DoRequest(req *http.Request) (*http.Response, error) {
	client := cm.GetHTTPClient()
	defer cm.PutHTTPClient(client)

	return client.Do(req)
}

// DoRequestWithContext performs an HTTP request with context using pooled client
func (cm *ConnectionManager) DoRequestWithContext(ctx context.Context, req *http.Request) (*http.Response, error) {
	client := cm.GetHTTPClient()
	defer cm.PutHTTPClient(client)

	req = req.WithContext(ctx)
	return client.Do(req)
}

// cleanupRoutine periodically updates connection statistics
func (cm *ConnectionManager) cleanupRoutine() {
	cm.cleanupTicker = time.NewTicker(1 * time.Minute)
	defer cm.cleanupTicker.Stop()

	for {
		select {
		case <-cm.cleanupTicker.C:
			cm.updateStats()
		case <-cm.stopChan:
			logger.Info("Connection manager cleanup routine stopped")
			return
		}
	}
}

// updateStats updates connection manager statistics
func (cm *ConnectionManager) updateStats() {
	cm.statsMutex.Lock()
	defer cm.statsMutex.Unlock()

	httpStats := cm.httpPool.GetStats()
	activeConnections := httpStats["current_active"].(int64)

	cm.stats.ActiveConnections = activeConnections
	if activeConnections > cm.stats.PeakConnections {
		cm.stats.PeakConnections = activeConnections
	}
}

// GetStats returns comprehensive connection statistics
func (cm *ConnectionManager) GetStats() map[string]interface{} {
	cm.statsMutex.RLock()
	defer cm.statsMutex.RUnlock()

	return map[string]interface{}{
		"overall": map[string]interface{}{
			"total_connections":  cm.stats.TotalConnections,
			"active_connections": cm.stats.ActiveConnections,
			"peak_connections":   cm.stats.PeakConnections,
			"uptime":             time.Since(cm.stats.LastReset).String(),
		},
		"http_pool": cm.httpPool.GetStats(),
	}
}

// Stop gracefully stops the connection manager
func (cm *ConnectionManager) Stop() {
	logger.Info("Stopping connection manager...")

	if cm.cleanupTicker != nil {
		cm.cleanupTicker.Stop()
	}

	close(cm.stopChan)

	if cm.httpPool != nil {
		cm.httpPool.Stop()
	}

	logger.Info("Connection manager stopped")
}

// OptimizedHTTPClient provides optimized HTTP operations
type OptimizedHTTPClient struct {
	pool *HTTPClientPool
}

// NewOptimizedHTTPClient creates a new optimized HTTP client
func NewOptimizedHTTPClient(pool *HTTPClientPool) *OptimizedHTTPClient {
	return &OptimizedHTTPClient{
		pool: pool,
	}
}

// Get performs an HTTP GET request
func (ohc *OptimizedHTTPClient) Get(url string) (*http.Response, error) {
	client := ohc.pool.Get()
	defer ohc.pool.Put(client)

	return client.Get(url)
}

// Post performs an HTTP POST request
func (ohc *OptimizedHTTPClient) Post(url, contentType string, body interface{}) (*http.Response, error) {
	client := ohc.pool.Get()
	defer ohc.pool.Put(client)

	// Convert body to bytes if needed
	var bodyBytes []byte
	var err error

	switch v := body.(type) {
	case []byte:
		bodyBytes = v
	case string:
		bodyBytes = []byte(v)
	default:
		// Try to marshal as JSON
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	return client.Post(url, contentType, bytes.NewReader(bodyBytes))
}

// Do performs a custom HTTP request
func (ohc *OptimizedHTTPClient) Do(req *http.Request) (*http.Response, error) {
	client := ohc.pool.Get()
	defer ohc.pool.Put(client)

	return client.Do(req)
}
