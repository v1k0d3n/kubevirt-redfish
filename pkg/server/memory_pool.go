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
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// BufferPool manages reusable byte buffers to reduce memory allocations
type BufferPool struct {
	pool    chan *bytes.Buffer
	maxSize int
	stats   *PoolStats
	mutex   sync.RWMutex
}

// PoolStats tracks pool performance metrics
type PoolStats struct {
	TotalAllocated int64
	TotalReturned  int64
	CurrentInUse   int64
	MaxPoolSize    int64
	LastReset      time.Time
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(maxSize int) *BufferPool {
	return &BufferPool{
		pool:    make(chan *bytes.Buffer, maxSize),
		maxSize: maxSize,
		stats: &PoolStats{
			LastReset: time.Now(),
		},
	}
}

// Get retrieves a buffer from the pool or creates a new one
func (bp *BufferPool) Get() *bytes.Buffer {
	select {
	case buf := <-bp.pool:
		bp.updateStats(1, 0) // Buffer retrieved from pool
		return buf
	default:
		bp.updateStats(0, 1)                          // New buffer allocated
		return bytes.NewBuffer(make([]byte, 0, 1024)) // Pre-allocate 1KB
	}
}

// Put returns a buffer to the pool
func (bp *BufferPool) Put(buf *bytes.Buffer) {
	if buf == nil {
		return
	}

	// Reset the buffer for reuse
	buf.Reset()

	select {
	case bp.pool <- buf:
		bp.updateStats(-1, 0) // Buffer returned to pool
	default:
		// Pool is full, discard the buffer
		bp.updateStats(0, -1) // Buffer discarded
	}
}

// updateStats updates pool statistics
func (bp *BufferPool) updateStats(poolDelta, allocatedDelta int64) {
	bp.mutex.Lock()
	defer bp.mutex.Unlock()

	bp.stats.TotalAllocated += allocatedDelta
	bp.stats.TotalReturned += poolDelta
	bp.stats.CurrentInUse = bp.stats.TotalAllocated - bp.stats.TotalReturned
}

// GetStats returns pool statistics
func (bp *BufferPool) GetStats() map[string]interface{} {
	bp.mutex.RLock()
	defer bp.mutex.RUnlock()

	return map[string]interface{}{
		"total_allocated":   bp.stats.TotalAllocated,
		"total_returned":    bp.stats.TotalReturned,
		"current_in_use":    bp.stats.CurrentInUse,
		"max_pool_size":     bp.maxSize,
		"current_pool_size": len(bp.pool),
		"uptime":            time.Since(bp.stats.LastReset).String(),
	}
}

// JSONEncoderPool manages reusable JSON encoders
type JSONEncoderPool struct {
	pool    chan *json.Encoder
	maxSize int
	stats   *PoolStats
	mutex   sync.RWMutex
}

// NewJSONEncoderPool creates a new JSON encoder pool
func NewJSONEncoderPool(maxSize int) *JSONEncoderPool {
	return &JSONEncoderPool{
		pool:    make(chan *json.Encoder, maxSize),
		maxSize: maxSize,
		stats: &PoolStats{
			LastReset: time.Now(),
		},
	}
}

// Get retrieves a JSON encoder from the pool or creates a new one
func (jep *JSONEncoderPool) Get(buf *bytes.Buffer) *json.Encoder {
	select {
	case enc := <-jep.pool:
		jep.updateStats(1, 0) // Encoder retrieved from pool
		// Reset the encoder with the provided buffer
		enc.SetIndent("", "  ")
		return enc
	default:
		jep.updateStats(0, 1) // New encoder allocated
		encoder := json.NewEncoder(buf)
		encoder.SetIndent("", "  ")
		return encoder
	}
}

// Put returns a JSON encoder to the pool
func (jep *JSONEncoderPool) Put(enc *json.Encoder) {
	if enc == nil {
		return
	}

	select {
	case jep.pool <- enc:
		jep.updateStats(-1, 0) // Encoder returned to pool
	default:
		// Pool is full, discard the encoder
		jep.updateStats(0, -1) // Encoder discarded
	}
}

// updateStats updates pool statistics
func (jep *JSONEncoderPool) updateStats(poolDelta, allocatedDelta int64) {
	jep.mutex.Lock()
	defer jep.mutex.Unlock()

	jep.stats.TotalAllocated += allocatedDelta
	jep.stats.TotalReturned += poolDelta
	jep.stats.CurrentInUse = jep.stats.TotalAllocated - jep.stats.TotalReturned
}

// GetStats returns pool statistics
func (jep *JSONEncoderPool) GetStats() map[string]interface{} {
	jep.mutex.RLock()
	defer jep.mutex.RUnlock()

	return map[string]interface{}{
		"total_allocated":   jep.stats.TotalAllocated,
		"total_returned":    jep.stats.TotalReturned,
		"current_in_use":    jep.stats.CurrentInUse,
		"max_pool_size":     jep.maxSize,
		"current_pool_size": len(jep.pool),
		"uptime":            time.Since(jep.stats.LastReset).String(),
	}
}

// ResponsePool manages reusable HTTP response objects
type ResponsePool struct {
	pool    chan *ResponseData
	maxSize int
	stats   *PoolStats
	mutex   sync.RWMutex
}

// ResponseData represents a reusable HTTP response
type ResponseData struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
	ETag       string
	Timestamp  time.Time
}

// NewResponsePool creates a new response pool
func NewResponsePool(maxSize int) *ResponsePool {
	return &ResponsePool{
		pool:    make(chan *ResponseData, maxSize),
		maxSize: maxSize,
		stats: &PoolStats{
			LastReset: time.Now(),
		},
	}
}

// Get retrieves a response object from the pool or creates a new one
func (rp *ResponsePool) Get() *ResponseData {
	select {
	case resp := <-rp.pool:
		rp.updateStats(1, 0) // Response retrieved from pool
		// Reset the response for reuse
		resp.StatusCode = 0
		resp.Headers = make(map[string]string)
		resp.Body = resp.Body[:0] // Reuse the slice
		resp.ETag = ""
		resp.Timestamp = time.Time{}
		return resp
	default:
		rp.updateStats(0, 1) // New response allocated
		return &ResponseData{
			Headers: make(map[string]string),
			Body:    make([]byte, 0, 1024),
		}
	}
}

// Put returns a response object to the pool
func (rp *ResponsePool) Put(resp *ResponseData) {
	if resp == nil {
		return
	}

	select {
	case rp.pool <- resp:
		rp.updateStats(-1, 0) // Response returned to pool
	default:
		// Pool is full, discard the response
		rp.updateStats(0, -1) // Response discarded
	}
}

// updateStats updates pool statistics
func (rp *ResponsePool) updateStats(poolDelta, allocatedDelta int64) {
	rp.mutex.Lock()
	defer rp.mutex.Unlock()

	rp.stats.TotalAllocated += allocatedDelta
	rp.stats.TotalReturned += poolDelta
	rp.stats.CurrentInUse = rp.stats.TotalAllocated - rp.stats.TotalReturned
}

// GetStats returns pool statistics
func (rp *ResponsePool) GetStats() map[string]interface{} {
	rp.mutex.RLock()
	defer rp.mutex.RUnlock()

	return map[string]interface{}{
		"total_allocated":   rp.stats.TotalAllocated,
		"total_returned":    rp.stats.TotalReturned,
		"current_in_use":    rp.stats.CurrentInUse,
		"max_pool_size":     rp.maxSize,
		"current_pool_size": len(rp.pool),
		"uptime":            time.Since(rp.stats.LastReset).String(),
	}
}

// MemoryManager coordinates all memory pools and provides memory optimization
type MemoryManager struct {
	bufferPool    *BufferPool
	encoderPool   *JSONEncoderPool
	responsePool  *ResponsePool
	stats         *MemoryStats
	statsMutex    sync.RWMutex
	cleanupTicker *time.Ticker
	stopChan      chan struct{}
}

// MemoryStats tracks overall memory usage
type MemoryStats struct {
	TotalAllocated int64
	TotalFreed     int64
	CurrentUsage   int64
	PeakUsage      int64
	LastReset      time.Time
}

// NewMemoryManager creates a new memory manager
func NewMemoryManager() *MemoryManager {
	mm := &MemoryManager{
		bufferPool:   NewBufferPool(100),     // 100 buffers
		encoderPool:  NewJSONEncoderPool(50), // 50 encoders
		responsePool: NewResponsePool(200),   // 200 responses
		stats: &MemoryStats{
			LastReset: time.Now(),
		},
		stopChan: make(chan struct{}),
	}

	// Start cleanup routine
	go mm.cleanupRoutine()

	return mm
}

// GetBuffer retrieves a buffer from the pool
func (mm *MemoryManager) GetBuffer() *bytes.Buffer {
	return mm.bufferPool.Get()
}

// PutBuffer returns a buffer to the pool
func (mm *MemoryManager) PutBuffer(buf *bytes.Buffer) {
	mm.bufferPool.Put(buf)
}

// GetEncoder retrieves a JSON encoder from the pool
func (mm *MemoryManager) GetEncoder(buf *bytes.Buffer) *json.Encoder {
	return mm.encoderPool.Get(buf)
}

// PutEncoder returns a JSON encoder to the pool
func (mm *MemoryManager) PutEncoder(enc *json.Encoder) {
	mm.encoderPool.Put(enc)
}

// GetResponse retrieves a response object from the pool
func (mm *MemoryManager) GetResponse() *ResponseData {
	return mm.responsePool.Get()
}

// PutResponse returns a response object to the pool
func (mm *MemoryManager) PutResponse(resp *ResponseData) {
	mm.responsePool.Put(resp)
}

// cleanupRoutine periodically cleans up memory pools
func (mm *MemoryManager) cleanupRoutine() {
	mm.cleanupTicker = time.NewTicker(5 * time.Minute)
	defer mm.cleanupTicker.Stop()

	for {
		select {
		case <-mm.cleanupTicker.C:
			mm.performCleanup()
		case <-mm.stopChan:
			logger.Info("Memory manager cleanup routine stopped")
			return
		}
	}
}

// performCleanup performs memory cleanup operations
func (mm *MemoryManager) performCleanup() {
	// Force garbage collection
	// Note: In production, you might want to be more conservative with GC
	// and rely on Go's automatic GC instead

	// Update memory statistics
	mm.updateStats()

	logger.Debug("Memory cleanup performed")
}

// updateStats updates memory statistics
func (mm *MemoryManager) updateStats() {
	mm.statsMutex.Lock()
	defer mm.statsMutex.Unlock()

	// Calculate current usage from all pools
	bufferStats := mm.bufferPool.GetStats()
	encoderStats := mm.encoderPool.GetStats()
	responseStats := mm.responsePool.GetStats()

	currentUsage := bufferStats["current_in_use"].(int64) +
		encoderStats["current_in_use"].(int64) +
		responseStats["current_in_use"].(int64)

	mm.stats.CurrentUsage = currentUsage
	if currentUsage > mm.stats.PeakUsage {
		mm.stats.PeakUsage = currentUsage
	}
}

// GetStats returns comprehensive memory statistics
func (mm *MemoryManager) GetStats() map[string]interface{} {
	mm.statsMutex.RLock()
	defer mm.statsMutex.RUnlock()

	return map[string]interface{}{
		"overall": map[string]interface{}{
			"total_allocated": mm.stats.TotalAllocated,
			"total_freed":     mm.stats.TotalFreed,
			"current_usage":   mm.stats.CurrentUsage,
			"peak_usage":      mm.stats.PeakUsage,
			"uptime":          time.Since(mm.stats.LastReset).String(),
		},
		"buffer_pool":   mm.bufferPool.GetStats(),
		"encoder_pool":  mm.encoderPool.GetStats(),
		"response_pool": mm.responsePool.GetStats(),
	}
}

// Stop gracefully stops the memory manager
func (mm *MemoryManager) Stop() {
	logger.Info("Stopping memory manager...")

	if mm.cleanupTicker != nil {
		mm.cleanupTicker.Stop()
	}

	close(mm.stopChan)

	logger.Info("Memory manager stopped")
}

// OptimizedJSONMarshal marshals JSON using pooled resources
func (mm *MemoryManager) OptimizedJSONMarshal(data interface{}) ([]byte, error) {
	// Get buffer and encoder from pools
	buf := mm.GetBuffer()
	defer mm.PutBuffer(buf)

	encoder := mm.GetEncoder(buf)
	defer mm.PutEncoder(encoder)

	// Encode the data
	if err := encoder.Encode(data); err != nil {
		return nil, fmt.Errorf("failed to encode JSON: %w", err)
	}

	// Copy the result to avoid buffer reuse issues
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())

	return result, nil
}

// OptimizedJSONUnmarshal unmarshals JSON using pooled resources
func (mm *MemoryManager) OptimizedJSONUnmarshal(data []byte, v interface{}) error {
	// For unmarshaling, we can reuse the input data directly
	return json.Unmarshal(data, v)
}
