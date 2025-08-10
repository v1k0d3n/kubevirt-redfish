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
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// CompressionType represents different compression algorithms
type CompressionType string

const (
	CompressionNone    CompressionType = "none"
	CompressionGzip    CompressionType = "gzip"
	CompressionDeflate CompressionType = "deflate"
)

// ResponseOptimizer provides response optimization and compression
type ResponseOptimizer struct {
	// Compression settings
	enableCompression  bool
	minCompressionSize int64
	compressionLevel   int

	// Statistics
	stats      *OptimizerStats
	statsMutex sync.RWMutex

	// Compression buffers
	gzipBuffer    *bytes.Buffer
	deflateBuffer *bytes.Buffer
	bufferMutex   sync.Mutex
}

// OptimizerStats tracks response optimization performance
type OptimizerStats struct {
	TotalResponses      int64
	CompressedResponses int64
	GzipResponses       int64
	DeflateResponses    int64
	BytesSaved          int64
	CompressionTime     time.Duration
	LastReset           time.Time
}

// NewResponseOptimizer creates a new response optimizer
func NewResponseOptimizer() *ResponseOptimizer {
	return &ResponseOptimizer{
		enableCompression:  true,
		minCompressionSize: 1024, // 1KB minimum for compression
		compressionLevel:   6,    // Default compression level

		stats: &OptimizerStats{
			LastReset: time.Now(),
		},

		gzipBuffer:    bytes.NewBuffer(make([]byte, 0, 4096)),
		deflateBuffer: bytes.NewBuffer(make([]byte, 0, 4096)),
	}
}

// OptimizeResponse optimizes a response based on content type and size
func (ro *ResponseOptimizer) OptimizeResponse(w http.ResponseWriter, r *http.Request, data []byte) error {
	startTime := time.Now()

	// Check if compression is enabled and supported
	if !ro.enableCompression || !ro.supportsCompression(r) {
		ro.writeUncompressedResponse(w, data)
		ro.updateStats(1, 0, 0, 0, 0, time.Since(startTime))
		return nil
	}

	// Check if response is large enough to compress
	if int64(len(data)) < ro.minCompressionSize {
		ro.writeUncompressedResponse(w, data)
		ro.updateStats(1, 0, 0, 0, 0, time.Since(startTime))
		return nil
	}

	// Determine best compression method
	compressionType := ro.getBestCompression(r)

	// Compress the response
	compressedData, err := ro.compressData(data, compressionType)
	if err != nil {
		logger.Warning("Failed to compress response: %v", err)
		ro.writeUncompressedResponse(w, data)
		ro.updateStats(1, 0, 0, 0, 0, time.Since(startTime))
		return nil
	}

	// Check if compression actually saved space
	if len(compressedData) >= len(data) {
		ro.writeUncompressedResponse(w, data)
		ro.updateStats(1, 0, 0, 0, 0, time.Since(startTime))
		return nil
	}

	// Write compressed response
	ro.writeCompressedResponse(w, compressedData, compressionType)

	// Update statistics
	bytesSaved := int64(len(data) - len(compressedData))
	ro.updateStats(1, 1, 0, 0, bytesSaved, time.Since(startTime))

	if compressionType == CompressionGzip {
		ro.updateStats(0, 0, 1, 0, 0, 0)
	} else if compressionType == CompressionDeflate {
		ro.updateStats(0, 0, 0, 1, 0, 0)
	}

	return nil
}

// supportsCompression checks if the client supports compression
func (ro *ResponseOptimizer) supportsCompression(r *http.Request) bool {
	acceptEncoding := r.Header.Get("Accept-Encoding")
	if acceptEncoding == "" {
		return false
	}

	acceptEncoding = strings.ToLower(acceptEncoding)
	return strings.Contains(acceptEncoding, "gzip") || strings.Contains(acceptEncoding, "deflate")
}

// getBestCompression determines the best compression method for the client
func (ro *ResponseOptimizer) getBestCompression(r *http.Request) CompressionType {
	acceptEncoding := strings.ToLower(r.Header.Get("Accept-Encoding"))

	// Check for gzip support (preferred)
	if strings.Contains(acceptEncoding, "gzip") {
		return CompressionGzip
	}

	// Check for deflate support
	if strings.Contains(acceptEncoding, "deflate") {
		return CompressionDeflate
	}

	return CompressionNone
}

// compressData compresses data using the specified method
func (ro *ResponseOptimizer) compressData(data []byte, compressionType CompressionType) ([]byte, error) {
	ro.bufferMutex.Lock()
	defer ro.bufferMutex.Unlock()

	switch compressionType {
	case CompressionGzip:
		return ro.compressGzip(data)
	case CompressionDeflate:
		return ro.compressDeflate(data)
	default:
		return data, nil
	}
}

// compressGzip compresses data using gzip
func (ro *ResponseOptimizer) compressGzip(data []byte) ([]byte, error) {
	ro.gzipBuffer.Reset()

	gw, err := gzip.NewWriterLevel(ro.gzipBuffer, ro.compressionLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip writer: %w", err)
	}

	if _, err := gw.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write gzip data: %w", err)
	}

	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	result := make([]byte, ro.gzipBuffer.Len())
	copy(result, ro.gzipBuffer.Bytes())
	return result, nil
}

// compressDeflate compresses data using deflate
func (ro *ResponseOptimizer) compressDeflate(data []byte) ([]byte, error) {
	ro.deflateBuffer.Reset()

	dw, err := zlib.NewWriterLevel(ro.deflateBuffer, ro.compressionLevel)
	if err != nil {
		return nil, fmt.Errorf("failed to create deflate writer: %w", err)
	}

	if _, err := dw.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write deflate data: %w", err)
	}

	if err := dw.Close(); err != nil {
		return nil, fmt.Errorf("failed to close deflate writer: %w", err)
	}

	result := make([]byte, ro.deflateBuffer.Len())
	copy(result, ro.deflateBuffer.Bytes())
	return result, nil
}

// writeUncompressedResponse writes an uncompressed response
func (ro *ResponseOptimizer) writeUncompressedResponse(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// writeCompressedResponse writes a compressed response
func (ro *ResponseOptimizer) writeCompressedResponse(w http.ResponseWriter, data []byte, compressionType CompressionType) {
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	switch compressionType {
	case CompressionGzip:
		w.Header().Set("Content-Encoding", "gzip")
	case CompressionDeflate:
		w.Header().Set("Content-Encoding", "deflate")
	}

	w.Header().Set("Vary", "Accept-Encoding")
	w.Write(data)
}

// updateStats updates optimizer statistics
func (ro *ResponseOptimizer) updateStats(total, compressed, gzip, deflate int64, bytesSaved int64, compressionTime time.Duration) {
	ro.statsMutex.Lock()
	defer ro.statsMutex.Unlock()

	ro.stats.TotalResponses += total
	ro.stats.CompressedResponses += compressed
	ro.stats.GzipResponses += gzip
	ro.stats.DeflateResponses += deflate
	ro.stats.BytesSaved += bytesSaved
	ro.stats.CompressionTime += compressionTime
}

// GetStats returns optimizer statistics
func (ro *ResponseOptimizer) GetStats() map[string]interface{} {
	ro.statsMutex.RLock()
	defer ro.statsMutex.RUnlock()

	compressionRatio := float64(0)
	if ro.stats.TotalResponses > 0 {
		compressionRatio = float64(ro.stats.CompressedResponses) / float64(ro.stats.TotalResponses) * 100
	}

	avgCompressionTime := time.Duration(0)
	if ro.stats.CompressedResponses > 0 {
		avgCompressionTime = ro.stats.CompressionTime / time.Duration(ro.stats.CompressedResponses)
	}

	return map[string]interface{}{
		"total_responses":      ro.stats.TotalResponses,
		"compressed_responses": ro.stats.CompressedResponses,
		"gzip_responses":       ro.stats.GzipResponses,
		"deflate_responses":    ro.stats.DeflateResponses,
		"bytes_saved":          ro.stats.BytesSaved,
		"compression_ratio":    fmt.Sprintf("%.2f%%", compressionRatio),
		"avg_compression_time": avgCompressionTime.String(),
		"uptime":               time.Since(ro.stats.LastReset).String(),
	}
}

// SetCompressionSettings configures compression behavior
func (ro *ResponseOptimizer) SetCompressionSettings(enable bool, minSize int64, level int) {
	ro.enableCompression = enable
	ro.minCompressionSize = minSize
	ro.compressionLevel = level

	logger.Info("Response optimizer settings updated: enable=%v, minSize=%d, level=%d",
		enable, minSize, level)
}

// OptimizedResponseWriter provides optimized response writing
type OptimizedResponseWriter struct {
	http.ResponseWriter
	optimizer  *ResponseOptimizer
	request    *http.Request
	buffer     *bytes.Buffer
	statusCode int
	headers    map[string]string
}

// NewOptimizedResponseWriter creates a new optimized response writer
func NewOptimizedResponseWriter(w http.ResponseWriter, r *http.Request, optimizer *ResponseOptimizer) *OptimizedResponseWriter {
	return &OptimizedResponseWriter{
		ResponseWriter: w,
		optimizer:      optimizer,
		request:        r,
		buffer:         bytes.NewBuffer(make([]byte, 0, 4096)),
		statusCode:     http.StatusOK,
		headers:        make(map[string]string),
	}
}

// WriteHeader captures the status code
func (orw *OptimizedResponseWriter) WriteHeader(code int) {
	orw.statusCode = code
	orw.ResponseWriter.WriteHeader(code)
}

// Write captures the response body for optimization
func (orw *OptimizedResponseWriter) Write(data []byte) (int, error) {
	orw.buffer.Write(data)
	return len(data), nil
}

// Header returns the response headers and captures them
func (orw *OptimizedResponseWriter) Header() http.Header {
	originalHeaders := orw.ResponseWriter.Header()

	// Capture headers for caching
	for key, values := range originalHeaders {
		if len(values) > 0 {
			orw.headers[key] = values[0]
		}
	}

	return originalHeaders
}

// Flush optimizes and writes the captured response
func (orw *OptimizedResponseWriter) Flush() error {
	if orw.statusCode != http.StatusOK {
		// Don't optimize error responses
		orw.ResponseWriter.WriteHeader(orw.statusCode)
		orw.ResponseWriter.Write(orw.buffer.Bytes())
		return nil
	}

	// Optimize the response
	return orw.optimizer.OptimizeResponse(orw.ResponseWriter, orw.request, orw.buffer.Bytes())
}

// GetCapturedData returns the captured response data
func (orw *OptimizedResponseWriter) GetCapturedData() []byte {
	return orw.buffer.Bytes()
}

// GetCapturedHeaders returns the captured response headers
func (orw *OptimizedResponseWriter) GetCapturedHeaders() map[string]string {
	return orw.headers
}

// ResponseCacheOptimizer combines caching and optimization
type ResponseCacheOptimizer struct {
	cache      *AdvancedCache
	optimizer  *ResponseOptimizer
	stats      *CacheOptimizerStats
	statsMutex sync.RWMutex
}

// CacheOptimizerStats tracks combined cache and optimizer performance
type CacheOptimizerStats struct {
	CacheHits          int64
	CacheMisses        int64
	OptimizedResponses int64
	BytesSaved         int64
	LastReset          time.Time
}

// NewResponseCacheOptimizer creates a new response cache optimizer
func NewResponseCacheOptimizer() *ResponseCacheOptimizer {
	return &ResponseCacheOptimizer{
		cache:     NewAdvancedCache(),
		optimizer: NewResponseOptimizer(),
		stats: &CacheOptimizerStats{
			LastReset: time.Now(),
		},
	}
}

// Get retrieves an optimized cached response
func (rco *ResponseCacheOptimizer) Get(key string) (*AdvancedCacheEntry, bool) {
	entry, exists := rco.cache.Get(key)
	if exists {
		rco.updateStats(1, 0, 0, 0) // Cache hit
		return entry, true
	}

	rco.updateStats(0, 1, 0, 0) // Cache miss
	return nil, false
}

// Set stores an optimized response in the cache
func (rco *ResponseCacheOptimizer) Set(key string, data []byte, headers map[string]string, policy *CachePolicy) {
	// Optimize the response before caching
	optimizedData := data
	if rco.optimizer.enableCompression {
		// Create a mock request for optimization
		mockReq, _ := http.NewRequest("GET", "/", nil)
		mockReq.Header.Set("Accept-Encoding", "gzip, deflate")

		// Optimize the response
		optimizedData, _ = rco.optimizer.compressData(data, CompressionGzip)
		if len(optimizedData) >= len(data) {
			optimizedData = data // Use original if compression didn't help
		}
	}

	rco.cache.Set(key, optimizedData, headers, policy)
}

// OptimizeResponse optimizes a response using the optimizer
func (rco *ResponseCacheOptimizer) OptimizeResponse(w http.ResponseWriter, r *http.Request, data []byte) error {
	err := rco.optimizer.OptimizeResponse(w, r, data)
	if err == nil {
		rco.updateStats(0, 0, 1, 0) // Optimized response
	}
	return err
}

// Invalidate invalidates cache entries
func (rco *ResponseCacheOptimizer) Invalidate(tags []string, patterns []string) {
	rco.cache.Invalidate(tags, patterns)
}

// GetStats returns combined statistics
func (rco *ResponseCacheOptimizer) GetStats() map[string]interface{} {
	rco.statsMutex.RLock()
	defer rco.statsMutex.RUnlock()

	cacheStats := rco.cache.GetStats()
	optimizerStats := rco.optimizer.GetStats()

	return map[string]interface{}{
		"cache":     cacheStats,
		"optimizer": optimizerStats,
		"combined": map[string]interface{}{
			"cache_hits":          rco.stats.CacheHits,
			"cache_misses":        rco.stats.CacheMisses,
			"optimized_responses": rco.stats.OptimizedResponses,
			"bytes_saved":         rco.stats.BytesSaved,
			"uptime":              time.Since(rco.stats.LastReset).String(),
		},
	}
}

// updateStats updates combined statistics
func (rco *ResponseCacheOptimizer) updateStats(hits, misses, optimized, bytesSaved int64) {
	rco.statsMutex.Lock()
	defer rco.statsMutex.Unlock()

	rco.stats.CacheHits += hits
	rco.stats.CacheMisses += misses
	rco.stats.OptimizedResponses += optimized
	rco.stats.BytesSaved += bytesSaved
}

// Stop gracefully stops the cache optimizer
func (rco *ResponseCacheOptimizer) Stop() {
	logger.Info("Stopping response cache optimizer...")

	if rco.cache != nil {
		rco.cache.Stop()
	}

	logger.Info("Response cache optimizer stopped")
}
