package server

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewResponseOptimizer(t *testing.T) {
	ro := NewResponseOptimizer()
	if ro == nil {
		t.Fatal("NewResponseOptimizer should not return nil")
	}

	if !ro.enableCompression {
		t.Error("Compression should be enabled by default")
	}

	if ro.minCompressionSize != 1024 {
		t.Errorf("Expected minCompressionSize 1024, got %d", ro.minCompressionSize)
	}

	if ro.compressionLevel != 6 {
		t.Errorf("Expected compressionLevel 6, got %d", ro.compressionLevel)
	}

	if ro.stats == nil {
		t.Error("Stats should be initialized")
	}

	if ro.stats.LastReset.IsZero() {
		t.Error("LastReset should be set")
	}

	if ro.gzipBuffer == nil {
		t.Error("GzipBuffer should be initialized")
	}

	if ro.deflateBuffer == nil {
		t.Error("DeflateBuffer should be initialized")
	}
}

func TestResponseOptimizer_SupportsCompression(t *testing.T) {
	ro := NewResponseOptimizer()

	testCases := []struct {
		name           string
		acceptEncoding string
		expected       bool
	}{
		{
			name:           "gzip support",
			acceptEncoding: "gzip",
			expected:       true,
		},
		{
			name:           "deflate support",
			acceptEncoding: "deflate",
			expected:       true,
		},
		{
			name:           "both gzip and deflate",
			acceptEncoding: "gzip, deflate",
			expected:       true,
		},
		{
			name:           "case insensitive gzip",
			acceptEncoding: "GZIP",
			expected:       true,
		},
		{
			name:           "case insensitive deflate",
			acceptEncoding: "DEFLATE",
			expected:       true,
		},
		{
			name:           "no compression support",
			acceptEncoding: "identity",
			expected:       false,
		},
		{
			name:           "empty header",
			acceptEncoding: "",
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Accept-Encoding", tc.acceptEncoding)

			result := ro.supportsCompression(req)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for Accept-Encoding: %s", tc.expected, result, tc.acceptEncoding)
			}
		})
	}
}

func TestResponseOptimizer_GetBestCompression(t *testing.T) {
	ro := NewResponseOptimizer()

	testCases := []struct {
		name           string
		acceptEncoding string
		expected       CompressionType
	}{
		{
			name:           "gzip preferred",
			acceptEncoding: "gzip, deflate",
			expected:       CompressionGzip,
		},
		{
			name:           "gzip only",
			acceptEncoding: "gzip",
			expected:       CompressionGzip,
		},
		{
			name:           "deflate only",
			acceptEncoding: "deflate",
			expected:       CompressionDeflate,
		},
		{
			name:           "case insensitive gzip",
			acceptEncoding: "GZIP",
			expected:       CompressionGzip,
		},
		{
			name:           "case insensitive deflate",
			acceptEncoding: "DEFLATE",
			expected:       CompressionDeflate,
		},
		{
			name:           "no compression support",
			acceptEncoding: "identity",
			expected:       CompressionNone,
		},
		{
			name:           "empty header",
			acceptEncoding: "",
			expected:       CompressionNone,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Accept-Encoding", tc.acceptEncoding)

			result := ro.getBestCompression(req)
			if result != tc.expected {
				t.Errorf("Expected %s, got %s for Accept-Encoding: %s", tc.expected, result, tc.acceptEncoding)
			}
		})
	}
}

func TestResponseOptimizer_CompressGzip(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test data that should compress well
	testData := []byte(strings.Repeat("This is test data that should compress well. ", 100))

	compressed, err := ro.compressGzip(testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(compressed) == 0 {
		t.Error("Compressed data should not be empty")
	}

	if len(compressed) >= len(testData) {
		t.Error("Compressed data should be smaller than original")
	}

	// Verify the compressed data can be decompressed
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("Failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to decompress data: %v", err)
	}

	if !bytes.Equal(decompressed, testData) {
		t.Error("Decompressed data should match original")
	}
}

func TestResponseOptimizer_CompressDeflate(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test data that should compress well
	testData := []byte(strings.Repeat("This is test data that should compress well. ", 100))

	compressed, err := ro.compressDeflate(testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(compressed) == 0 {
		t.Error("Compressed data should not be empty")
	}

	if len(compressed) >= len(testData) {
		t.Error("Compressed data should be smaller than original")
	}

	// Verify the compressed data can be decompressed
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("Failed to create deflate reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("Failed to decompress data: %v", err)
	}

	if !bytes.Equal(decompressed, testData) {
		t.Error("Decompressed data should match original")
	}
}

func TestResponseOptimizer_CompressData(t *testing.T) {
	ro := NewResponseOptimizer()

	testData := []byte("test data")

	testCases := []struct {
		name            string
		compressionType CompressionType
		expectError     bool
	}{
		{
			name:            "gzip compression",
			compressionType: CompressionGzip,
			expectError:     false,
		},
		{
			name:            "deflate compression",
			compressionType: CompressionDeflate,
			expectError:     false,
		},
		{
			name:            "no compression",
			compressionType: CompressionNone,
			expectError:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := ro.compressData(testData, tc.compressionType)

			if tc.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}

			if tc.compressionType == CompressionNone {
				if !bytes.Equal(compressed, testData) {
					t.Error("No compression should return original data")
				}
			} else {
				if len(compressed) == 0 {
					t.Error("Compressed data should not be empty")
				}
			}
		})
	}
}

func TestResponseOptimizer_WriteUncompressedResponse(t *testing.T) {
	ro := NewResponseOptimizer()

	testData := []byte("test response data")
	w := httptest.NewRecorder()

	ro.writeUncompressedResponse(w, testData)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	if w.Header().Get("Content-Length") != "18" {
		t.Errorf("Expected Content-Length 18, got %s", w.Header().Get("Content-Length"))
	}

	if !bytes.Equal(w.Body.Bytes(), testData) {
		t.Error("Response body should match test data")
	}
}

func TestResponseOptimizer_WriteCompressedResponse(t *testing.T) {
	ro := NewResponseOptimizer()

	testData := []byte("compressed data")

	testCases := []struct {
		name             string
		compressionType  CompressionType
		expectedEncoding string
	}{
		{
			name:             "gzip compression",
			compressionType:  CompressionGzip,
			expectedEncoding: "gzip",
		},
		{
			name:             "deflate compression",
			compressionType:  CompressionDeflate,
			expectedEncoding: "deflate",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ro.writeCompressedResponse(w, testData, tc.compressionType)

			if w.Header().Get("Content-Length") != "15" {
				t.Errorf("Expected Content-Length 15, got %s", w.Header().Get("Content-Length"))
			}

			if w.Header().Get("Content-Encoding") != tc.expectedEncoding {
				t.Errorf("Expected Content-Encoding %s, got %s", tc.expectedEncoding, w.Header().Get("Content-Encoding"))
			}

			if w.Header().Get("Vary") != "Accept-Encoding" {
				t.Error("Expected Vary header to be set to Accept-Encoding")
			}

			if !bytes.Equal(w.Body.Bytes(), testData) {
				t.Error("Response body should match test data")
			}
		})
	}
}

func TestResponseOptimizer_OptimizeResponse_NoCompression(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test with compression disabled
	ro.enableCompression = false

	testData := []byte("test data")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	err := ro.OptimizeResponse(w, r, testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Header().Get("Content-Encoding") != "" {
		t.Error("Should not have Content-Encoding header when compression is disabled")
	}

	if !bytes.Equal(w.Body.Bytes(), testData) {
		t.Error("Response body should match test data")
	}
}

func TestResponseOptimizer_OptimizeResponse_NoClientSupport(t *testing.T) {
	ro := NewResponseOptimizer()

	testData := []byte("test data")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	// No Accept-Encoding header

	err := ro.OptimizeResponse(w, r, testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Header().Get("Content-Encoding") != "" {
		t.Error("Should not have Content-Encoding header when client doesn't support compression")
	}

	if !bytes.Equal(w.Body.Bytes(), testData) {
		t.Error("Response body should match test data")
	}
}

func TestResponseOptimizer_OptimizeResponse_SmallData(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test with data smaller than minimum compression size
	testData := []byte("small")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	err := ro.OptimizeResponse(w, r, testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Header().Get("Content-Encoding") != "" {
		t.Error("Should not have Content-Encoding header for small data")
	}

	if !bytes.Equal(w.Body.Bytes(), testData) {
		t.Error("Response body should match test data")
	}
}

func TestResponseOptimizer_OptimizeResponse_Compression(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test with compressible data
	testData := []byte(strings.Repeat("This is test data that should compress well. ", 50))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	err := ro.OptimizeResponse(w, r, testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Should have gzip Content-Encoding header")
	}

	if w.Header().Get("Vary") != "Accept-Encoding" {
		t.Error("Should have Vary header set to Accept-Encoding")
	}

	// Verify the response is actually compressed
	if len(w.Body.Bytes()) >= len(testData) {
		t.Error("Compressed response should be smaller than original")
	}
}

func TestResponseOptimizer_GetStats(t *testing.T) {
	ro := NewResponseOptimizer()

	// Get initial stats
	stats := ro.GetStats()

	// Verify initial values
	if stats["total_responses"] != int64(0) {
		t.Errorf("Expected total_responses 0, got %v", stats["total_responses"])
	}

	if stats["compressed_responses"] != int64(0) {
		t.Errorf("Expected compressed_responses 0, got %v", stats["compressed_responses"])
	}

	if stats["gzip_responses"] != int64(0) {
		t.Errorf("Expected gzip_responses 0, got %v", stats["gzip_responses"])
	}

	if stats["deflate_responses"] != int64(0) {
		t.Errorf("Expected deflate_responses 0, got %v", stats["deflate_responses"])
	}

	if stats["bytes_saved"] != int64(0) {
		t.Errorf("Expected bytes_saved 0, got %v", stats["bytes_saved"])
	}

	if stats["compression_ratio"] != "0.00%" {
		t.Errorf("Expected compression_ratio 0.00%%, got %v", stats["compression_ratio"])
	}

	if stats["uptime"] == "" {
		t.Error("Uptime should not be empty")
	}

	// Test with some responses (large enough to trigger compression)
	testData := []byte(strings.Repeat("test data ", 200)) // 2000 bytes, above 1024 minimum
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	err := ro.OptimizeResponse(w, r, testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	stats = ro.GetStats()
	if stats["total_responses"] != int64(1) {
		t.Errorf("Expected total_responses 1, got %v", stats["total_responses"])
	}

	if stats["compressed_responses"] != int64(1) {
		t.Errorf("Expected compressed_responses 1, got %v", stats["compressed_responses"])
	}

	if stats["gzip_responses"] != int64(1) {
		t.Errorf("Expected gzip_responses 1, got %v", stats["gzip_responses"])
	}

	if stats["bytes_saved"] == int64(0) {
		t.Error("Should have saved some bytes")
	}

	if stats["compression_ratio"] != "100.00%" {
		t.Errorf("Expected compression_ratio 100.00%%, got %v", stats["compression_ratio"])
	}
}

func TestResponseOptimizer_SetCompressionSettings(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test default values
	if !ro.enableCompression {
		t.Error("Compression should be enabled by default")
	}

	if ro.minCompressionSize != 1024 {
		t.Errorf("Expected minCompressionSize 1024, got %d", ro.minCompressionSize)
	}

	if ro.compressionLevel != 6 {
		t.Errorf("Expected compressionLevel 6, got %d", ro.compressionLevel)
	}

	// Set new values
	ro.SetCompressionSettings(false, 2048, 9)

	if ro.enableCompression {
		t.Error("Compression should be disabled")
	}

	if ro.minCompressionSize != 2048 {
		t.Errorf("Expected minCompressionSize 2048, got %d", ro.minCompressionSize)
	}

	if ro.compressionLevel != 9 {
		t.Errorf("Expected compressionLevel 9, got %d", ro.compressionLevel)
	}
}

func TestNewOptimizedResponseWriter(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)
	if orw == nil {
		t.Fatal("NewOptimizedResponseWriter should not return nil")
	}

	if orw.ResponseWriter != w {
		t.Error("ResponseWriter should be set correctly")
	}

	if orw.optimizer != ro {
		t.Error("Optimizer should be set correctly")
	}

	if orw.request != r {
		t.Error("Request should be set correctly")
	}

	if orw.buffer == nil {
		t.Error("Buffer should be initialized")
	}

	if orw.statusCode != http.StatusOK {
		t.Errorf("Expected statusCode %d, got %d", http.StatusOK, orw.statusCode)
	}

	if orw.headers == nil {
		t.Error("Headers should be initialized")
	}
}

func TestOptimizedResponseWriter_WriteHeader(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)

	// Test WriteHeader
	orw.WriteHeader(http.StatusNotFound)

	if orw.statusCode != http.StatusNotFound {
		t.Errorf("Expected statusCode %d, got %d", http.StatusNotFound, orw.statusCode)
	}

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected response statusCode %d, got %d", http.StatusNotFound, w.Code)
	}
}

func TestOptimizedResponseWriter_Write(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)

	testData := []byte("test data")

	// Test Write
	n, err := orw.Write(testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if n != len(testData) {
		t.Errorf("Expected written bytes %d, got %d", len(testData), n)
	}

	if !bytes.Equal(orw.buffer.Bytes(), testData) {
		t.Error("Buffer should contain written data")
	}

	// Response should not be written yet
	if len(w.Body.Bytes()) != 0 {
		t.Error("Response should not be written until Flush")
	}
}

func TestOptimizedResponseWriter_Header(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)

	// Test Header - set headers first, then check capture
	headers := orw.Header()
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Custom", "value")

	// Call Header() again to trigger capture
	orw.Header()

	// Verify headers are captured
	if orw.headers["Content-Type"] != "application/json" {
		t.Error("Content-Type header should be captured")
	}

	if orw.headers["X-Custom"] != "value" {
		t.Error("X-Custom header should be captured")
	}
}

func TestOptimizedResponseWriter_Flush_ErrorResponse(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)

	testData := []byte("error data")
	orw.WriteHeader(http.StatusInternalServerError)
	if _, err := orw.Write(testData); err != nil {
		t.Errorf("Failed to write response: %v", err)
	}

	err := orw.Flush()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, w.Code)
	}

	if !bytes.Equal(w.Body.Bytes(), testData) {
		t.Error("Response body should match test data")
	}

	// Error responses should not be optimized
	if w.Header().Get("Content-Encoding") != "" {
		t.Error("Error responses should not be compressed")
	}
}

func TestOptimizedResponseWriter_Flush_SuccessResponse(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	orw := NewOptimizedResponseWriter(w, r, ro)

	testData := []byte(strings.Repeat("test data ", 200)) // Large enough to trigger compression
	if _, err := orw.Write(testData); err != nil {
		t.Errorf("Failed to write response: %v", err)
	}

	err := orw.Flush()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Success responses should be optimized
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Success responses should be compressed")
	}

	if len(w.Body.Bytes()) == 0 {
		t.Error("Response body should not be empty")
	}
}

func TestOptimizedResponseWriter_GetCapturedData(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)

	testData := []byte("test data")
	if _, err := orw.Write(testData); err != nil {
		t.Errorf("Failed to write response: %v", err)
	}

	captured := orw.GetCapturedData()
	if !bytes.Equal(captured, testData) {
		t.Error("Captured data should match written data")
	}
}

func TestOptimizedResponseWriter_GetCapturedHeaders(t *testing.T) {
	ro := NewResponseOptimizer()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	orw := NewOptimizedResponseWriter(w, r, ro)

	orw.Header().Set("Content-Type", "application/json")
	orw.Header().Set("X-Custom", "value")

	// Call Header() again to trigger capture
	orw.Header()

	headers := orw.GetCapturedHeaders()
	if headers["Content-Type"] != "application/json" {
		t.Error("Content-Type header should be captured")
	}

	if headers["X-Custom"] != "value" {
		t.Error("X-Custom header should be captured")
	}
}

func TestNewResponseCacheOptimizer(t *testing.T) {
	rco := NewResponseCacheOptimizer()
	if rco == nil {
		t.Fatal("NewResponseCacheOptimizer should not return nil")
	}

	if rco.cache == nil {
		t.Error("Cache should be initialized")
	}

	if rco.optimizer == nil {
		t.Error("Optimizer should be initialized")
	}

	if rco.stats == nil {
		t.Error("Stats should be initialized")
	}

	if rco.stats.LastReset.IsZero() {
		t.Error("LastReset should be set")
	}
}

func TestResponseCacheOptimizer_Get(t *testing.T) {
	rco := NewResponseCacheOptimizer()
	defer rco.Stop()

	// Test cache miss
	entry, exists := rco.Get("test-key")
	if exists {
		t.Error("Should not exist initially")
	}
	if entry != nil {
		t.Error("Should return nil for cache miss")
	}

	// Test cache hit
	testData := []byte("test data")
	headers := map[string]string{"Content-Type": "application/json"}
	policy := &CachePolicy{TTL: 1 * time.Hour}

	rco.Set("test-key", testData, headers, policy)

	entry, exists = rco.Get("test-key")
	if !exists {
		t.Error("Should exist after setting")
	}
	if entry == nil {
		t.Error("Should return entry for cache hit")
	}
}

func TestResponseCacheOptimizer_Set(t *testing.T) {
	rco := NewResponseCacheOptimizer()
	defer rco.Stop()

	testData := []byte("test data")
	headers := map[string]string{"Content-Type": "application/json"}
	policy := &CachePolicy{TTL: 1 * time.Hour}

	rco.Set("test-key", testData, headers, policy)

	entry, exists := rco.Get("test-key")
	if !exists {
		t.Error("Should exist after setting")
	}
	if entry == nil {
		t.Error("Should return entry")
	}
}

func TestResponseCacheOptimizer_OptimizeResponse(t *testing.T) {
	rco := NewResponseCacheOptimizer()
	defer rco.Stop()

	testData := []byte(strings.Repeat("test data ", 200)) // Large enough to trigger compression
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	err := rco.OptimizeResponse(w, r, testData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Should have gzip Content-Encoding header")
	}

	if len(w.Body.Bytes()) == 0 {
		t.Error("Response body should not be empty")
	}
}

func TestResponseCacheOptimizer_Invalidate(t *testing.T) {
	rco := NewResponseCacheOptimizer()
	defer rco.Stop()

	testData := []byte("test data")
	headers := map[string]string{"Content-Type": "application/json"}
	policy := &CachePolicy{TTL: 1 * time.Hour}

	rco.Set("test-key", testData, headers, policy)

	// Verify it exists
	_, exists := rco.Get("test-key")
	if !exists {
		t.Error("Should exist before invalidation")
	}

	// Invalidate using the exact key pattern
	rco.Invalidate([]string{}, []string{"test-key"})

	// Verify it's gone
	_, exists = rco.Get("test-key")
	if exists {
		t.Error("Should not exist after invalidation")
	}
}

func TestResponseCacheOptimizer_GetStats(t *testing.T) {
	rco := NewResponseCacheOptimizer()
	defer rco.Stop()

	stats := rco.GetStats()

	// Verify structure
	if stats["cache"] == nil {
		t.Error("Cache stats should be present")
	}

	if stats["optimizer"] == nil {
		t.Error("Optimizer stats should be present")
	}

	if stats["combined"] == nil {
		t.Error("Combined stats should be present")
	}

	combined := stats["combined"].(map[string]interface{})
	if combined["cache_hits"] != int64(0) {
		t.Errorf("Expected cache_hits 0, got %v", combined["cache_hits"])
	}

	if combined["cache_misses"] != int64(0) {
		t.Errorf("Expected cache_misses 0, got %v", combined["cache_misses"])
	}

	if combined["optimized_responses"] != int64(0) {
		t.Errorf("Expected optimized_responses 0, got %v", combined["optimized_responses"])
	}

	if combined["bytes_saved"] != int64(0) {
		t.Errorf("Expected bytes_saved 0, got %v", combined["bytes_saved"])
	}

	if combined["uptime"] == "" {
		t.Error("Uptime should not be empty")
	}
}

func TestResponseCacheOptimizer_Stop(t *testing.T) {
	rco := NewResponseCacheOptimizer()

	// Should not panic
	rco.Stop()
}

func TestResponseOptimizer_ConcurrentAccess(t *testing.T) {
	ro := NewResponseOptimizer()

	var wg sync.WaitGroup
	numGoroutines := 10

	// Test concurrent optimization
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			testData := []byte(strings.Repeat("test data ", 50))
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/test", nil)
			r.Header.Set("Accept-Encoding", "gzip")

			err := ro.OptimizeResponse(w, r, testData)
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
		}(i)
	}

	wg.Wait()

	// Verify stats
	stats := ro.GetStats()
	if stats["total_responses"] != int64(numGoroutines) {
		t.Errorf("Expected total_responses %d, got %v", numGoroutines, stats["total_responses"])
	}
}

func TestResponseOptimizer_EdgeCases(t *testing.T) {
	ro := NewResponseOptimizer()

	// Test with empty data
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept-Encoding", "gzip")

	err := ro.OptimizeResponse(w, r, []byte{})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test with nil data
	err = ro.OptimizeResponse(w, r, nil)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Test with very large data
	largeData := make([]byte, 1024*1024) // 1MB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	w = httptest.NewRecorder()
	err = ro.OptimizeResponse(w, r, largeData)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Error("Large data should be compressed")
	}
}
