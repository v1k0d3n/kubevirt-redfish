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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/auth"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// CacheEntry represents a cached response
type CacheEntry struct {
	Data        []byte            `json:"data"`
	Headers     map[string]string `json:"headers"`
	ETag        string            `json:"etag"`
	CreatedAt   time.Time         `json:"created_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	AccessCount int64             `json:"access_count"`
	LastAccess  time.Time         `json:"last_access"`
}

// Cache provides response caching functionality
type Cache struct {
	entries map[string]*CacheEntry
	mutex   sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc

	// Configuration
	maxEntries    int
	defaultTTL    time.Duration
	cleanupTicker *time.Ticker
}

// NewCache creates a new cache instance
func NewCache(maxEntries int, defaultTTL time.Duration) *Cache {
	ctx, cancel := context.WithCancel(context.Background())

	cache := &Cache{
		entries:       make(map[string]*CacheEntry),
		maxEntries:    maxEntries,
		defaultTTL:    defaultTTL,
		ctx:           ctx,
		cancel:        cancel,
		cleanupTicker: time.NewTicker(5 * time.Minute), // Clean up every 5 minutes
	}

	// Start cleanup routine
	go cache.cleanupRoutine()

	return cache
}

// generateCacheKey generates a unique cache key for a request
func (c *Cache) generateCacheKey(r *http.Request, user string) string {
	// Create a hash of the request path, method, and user
	keyData := fmt.Sprintf("%s:%s:%s", r.Method, r.URL.Path, user)
	hash := sha256.Sum256([]byte(keyData))
	return hex.EncodeToString(hash[:])
}

// Get retrieves a cached response if available and not expired
func (c *Cache) Get(key string) (*CacheEntry, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	// Check if entry has expired
	if time.Now().After(entry.ExpiresAt) {
		// Entry has expired, remove it
		c.mutex.RUnlock()
		c.mutex.Lock()
		delete(c.entries, key)
		c.mutex.Unlock()
		c.mutex.RLock()
		return nil, false
	}

	// Update access statistics
	entry.AccessCount++
	entry.LastAccess = time.Now()

	return entry, true
}

// Set stores a response in the cache
func (c *Cache) Set(key string, data []byte, headers map[string]string, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	entry := &CacheEntry{
		Data:        data,
		Headers:     headers,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(ttl),
		AccessCount: 1,
		LastAccess:  time.Now(),
	}

	// Generate ETag for the response
	etagHash := sha256.Sum256(data)
	entry.ETag = fmt.Sprintf("W/\"%s\"", hex.EncodeToString(etagHash[:8]))

	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if we need to evict entries due to size limit
	if len(c.entries) >= c.maxEntries {
		c.evictOldest()
	}

	c.entries[key] = entry
	logger.Debug("Cached response for key %s (TTL: %v)", key, ttl)
}

// Invalidate removes entries matching a pattern
func (c *Cache) Invalidate(pattern string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	count := 0
	for key := range c.entries {
		if strings.Contains(key, pattern) {
			delete(c.entries, key)
			count++
		}
	}

	if count > 0 {
		logger.Debug("Invalidated %d cache entries matching pattern: %s", count, pattern)
	}
}

// evictOldest removes the oldest accessed entries when cache is full
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.entries {
		if oldestKey == "" || entry.LastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.LastAccess
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
		logger.Debug("Evicted oldest cache entry: %s", oldestKey)
	}
}

// cleanupRoutine periodically removes expired entries
func (c *Cache) cleanupRoutine() {
	for {
		select {
		case <-c.cleanupTicker.C:
			c.cleanupExpired()
		case <-c.ctx.Done():
			return
		}
	}
}

// cleanupExpired removes all expired entries from the cache
func (c *Cache) cleanupExpired() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	count := 0

	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
			count++
		}
	}

	if count > 0 {
		logger.Debug("Cleaned up %d expired cache entries", count)
	}
}

// GetStats returns cache statistics
func (c *Cache) GetStats() map[string]interface{} {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	totalAccess := int64(0)
	for _, entry := range c.entries {
		totalAccess += entry.AccessCount
	}

	return map[string]interface{}{
		"entries_count":  len(c.entries),
		"max_entries":    c.maxEntries,
		"total_accesses": totalAccess,
		"default_ttl":    c.defaultTTL.String(),
	}
}

// Stop gracefully stops the cache
func (c *Cache) Stop() {
	logger.Info("Stopping cache...")

	if c.cleanupTicker != nil {
		c.cleanupTicker.Stop()
	}

	c.cancel()

	c.mutex.Lock()
	entryCount := len(c.entries)
	c.entries = make(map[string]*CacheEntry)
	c.mutex.Unlock()

	logger.Info("Cache stopped, cleared %d entries", entryCount)
}

// CacheMiddleware provides caching functionality for HTTP handlers
func (s *Server) CacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip caching if responseCache is nil (test mode)
		if s.responseCache == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Skip caching for non-GET requests
		if r.Method != "GET" {
			next.ServeHTTP(w, r)
			return
		}

		// Skip caching for certain endpoints
		if s.shouldSkipCache(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Extract user for cache key generation
		user := "anonymous"
		if authCtx := auth.GetAuthContext(r); authCtx != nil && authCtx.User != nil {
			user = authCtx.User.Username
		}

		// Generate cache key
		cacheKey := s.responseCache.generateCacheKey(r, user)

		// Check for conditional requests
		if etag := r.Header.Get("If-None-Match"); etag != "" {
			if entry, exists := s.responseCache.Get(cacheKey); exists && entry.ETag == etag {
				// Return 304 Not Modified
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		// Check cache for existing response
		if entry, exists := s.responseCache.Get(cacheKey); exists {
			// Return cached response
			s.writeCachedResponse(w, entry)
			return
		}

		// Create response writer that captures the response
		cacheWriter := &CacheResponseWriter{
			ResponseWriter: w,
			headers:        make(map[string]string),
			statusCode:     http.StatusOK,
		}

		// Process request
		next.ServeHTTP(cacheWriter, r)

		// Cache successful responses
		if cacheWriter.statusCode == http.StatusOK {
			ttl := s.getCacheTTL(r.URL.Path)
			s.responseCache.Set(cacheKey, cacheWriter.body, cacheWriter.headers, ttl)
		}
	})
}

// CacheResponseWriter captures response data for caching
type CacheResponseWriter struct {
	http.ResponseWriter
	headers    map[string]string
	statusCode int
	body       []byte
}

// WriteHeader captures the status code
func (crw *CacheResponseWriter) WriteHeader(code int) {
	crw.statusCode = code
	crw.ResponseWriter.WriteHeader(code)
}

// Write captures the response body
func (crw *CacheResponseWriter) Write(data []byte) (int, error) {
	crw.body = append(crw.body, data...)
	return crw.ResponseWriter.Write(data)
}

// Header returns the response headers and captures them for caching
func (crw *CacheResponseWriter) Header() http.Header {
	// Capture headers from the original response writer
	originalHeaders := crw.ResponseWriter.Header()

	// Convert to map[string]string for caching
	if crw.headers == nil {
		crw.headers = make(map[string]string)
	}

	for key, values := range originalHeaders {
		if len(values) > 0 {
			crw.headers[key] = values[0] // Take the first value
		}
	}

	return originalHeaders
}

// writeCachedResponse writes a cached response to the client
func (s *Server) writeCachedResponse(w http.ResponseWriter, entry *CacheEntry) {
	// Set headers
	for key, value := range entry.Headers {
		w.Header().Set(key, value)
	}

	// Add cache hit indicator
	w.Header().Set("X-Cache", "HIT")
	w.Header().Set("X-Cache-Age", time.Since(entry.CreatedAt).String())

	// Write response
	w.Write(entry.Data)

	logger.Debug("Served cached response (age: %v, accesses: %d)",
		time.Since(entry.CreatedAt), entry.AccessCount)
}

// shouldSkipCache determines if a request should skip caching
func (s *Server) shouldSkipCache(path string) bool {
	// Skip caching for dynamic endpoints
	skipPaths := []string{
		"/internal/metrics",
		"/redfish/v1/TaskService/Tasks/",
		"/redfish/v1/Systems/", // Skip system endpoints as they change frequently
		"/redfish/v1/Systems",  // Skip Systems collection to see VM filtering logs
	}

	for _, skipPath := range skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}

	return false
}

// getCacheTTL returns the appropriate TTL for a given path
func (s *Server) getCacheTTL(path string) time.Duration {
	switch {
	case strings.HasPrefix(path, "/redfish/v1/"):
		return 30 * time.Second // Service root - short cache
	case strings.HasPrefix(path, "/redfish/v1/Chassis"):
		return 2 * time.Minute // Chassis - moderate cache
	case strings.HasPrefix(path, "/redfish/v1/Systems"):
		return 1 * time.Minute // Systems - short cache due to dynamic state
	case strings.HasPrefix(path, "/redfish/v1/Managers"):
		return 5 * time.Minute // Managers - longer cache
	default:
		return 1 * time.Minute // Default
	}
}
