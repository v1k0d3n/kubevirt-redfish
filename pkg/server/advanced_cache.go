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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// CacheTier represents different cache tiers
type CacheTier int

const (
	TierL1 CacheTier = iota // Fastest, smallest (in-memory)
	TierL2                  // Medium speed, medium size (compressed)
	TierL3                  // Slowest, largest (persistent-like)
)

// AdvancedCacheEntry represents an advanced cached response
type AdvancedCacheEntry struct {
	Data             []byte            `json:"data"`
	CompressedData   []byte            `json:"compressed_data,omitempty"`
	Headers          map[string]string `json:"headers"`
	ETag             string            `json:"etag"`
	CreatedAt        time.Time         `json:"created_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
	AccessCount      int64             `json:"access_count"`
	LastAccess       time.Time         `json:"last_access"`
	Size             int64             `json:"size"`
	CompressedSize   int64             `json:"compressed_size"`
	Tier             CacheTier         `json:"tier"`
	Priority         int               `json:"priority"` // Higher = more important
	InvalidationTags []string          `json:"invalidation_tags"`
}

// CachePolicy defines caching behavior for different resource types
type CachePolicy struct {
	TTL              time.Duration
	Tier             CacheTier
	Priority         int
	Compress         bool
	InvalidationTags []string
}

// AdvancedCache provides multi-tier caching with advanced features
type AdvancedCache struct {
	// Multi-tier storage
	l1Cache map[string]*AdvancedCacheEntry // Fastest tier
	l2Cache map[string]*AdvancedCacheEntry // Compressed tier
	l3Cache map[string]*AdvancedCacheEntry // Persistent tier

	// Configuration
	maxL1Entries int
	maxL2Entries int
	maxL3Entries int
	defaultTTL   time.Duration

	// Statistics
	stats      *AdvancedCacheStats
	statsMutex sync.RWMutex

	// Background workers
	ctx           context.Context
	cancel        context.CancelFunc
	cleanupTicker *time.Ticker
	preloadTicker *time.Ticker
	stopChan      chan struct{}

	// Mutexes for each tier
	l1Mutex sync.RWMutex
	l2Mutex sync.RWMutex
	l3Mutex sync.RWMutex

	// Cache policies
	policies map[string]*CachePolicy
}

// AdvancedCacheStats tracks advanced cache performance
type AdvancedCacheStats struct {
	L1Hits         int64
	L2Hits         int64
	L3Hits         int64
	L1Misses       int64
	L2Misses       int64
	L3Misses       int64
	L1Evictions    int64
	L2Evictions    int64
	L3Evictions    int64
	Compressions   int64
	Decompressions int64
	Preloads       int64
	Invalidations  int64
	LastReset      time.Time
}

// NewAdvancedCache creates a new advanced cache instance
func NewAdvancedCache() *AdvancedCache {
	ctx, cancel := context.WithCancel(context.Background())

	ac := &AdvancedCache{
		l1Cache: make(map[string]*AdvancedCacheEntry),
		l2Cache: make(map[string]*AdvancedCacheEntry),
		l3Cache: make(map[string]*AdvancedCacheEntry),

		maxL1Entries: 1000,  // 1K entries in L1
		maxL2Entries: 5000,  // 5K entries in L2
		maxL3Entries: 10000, // 10K entries in L3
		defaultTTL:   5 * time.Minute,

		stats: &AdvancedCacheStats{
			LastReset: time.Now(),
		},

		ctx:      ctx,
		cancel:   cancel,
		stopChan: make(chan struct{}),

		policies: make(map[string]*CachePolicy),
	}

	// Initialize default policies
	ac.initializePolicies()

	// Start background workers
	go ac.cleanupRoutine()
	go ac.preloadRoutine()

	return ac
}

// initializePolicies sets up default cache policies
func (ac *AdvancedCache) initializePolicies() {
	// Service root - short cache, L1 tier
	ac.policies["/redfish/v1/"] = &CachePolicy{
		TTL:              30 * time.Second,
		Tier:             TierL1,
		Priority:         1,
		Compress:         false,
		InvalidationTags: []string{"service_root"},
	}

	// Chassis - moderate cache, L2 tier
	ac.policies["/redfish/v1/Chassis"] = &CachePolicy{
		TTL:              2 * time.Minute,
		Tier:             TierL2,
		Priority:         2,
		Compress:         true,
		InvalidationTags: []string{"chassis", "hardware"},
	}

	// Systems - short cache, L1 tier (frequently accessed)
	ac.policies["/redfish/v1/Systems"] = &CachePolicy{
		TTL:              1 * time.Minute,
		Tier:             TierL1,
		Priority:         3,
		Compress:         false,
		InvalidationTags: []string{"systems", "vms"},
	}

	// Managers - longer cache, L3 tier
	ac.policies["/redfish/v1/Managers"] = &CachePolicy{
		TTL:              5 * time.Minute,
		Tier:             TierL3,
		Priority:         1,
		Compress:         true,
		InvalidationTags: []string{"managers", "management"},
	}

	// Virtual Media - short cache, L1 tier
	ac.policies["/redfish/v1/Systems/.*/VirtualMedia"] = &CachePolicy{
		TTL:              30 * time.Second,
		Tier:             TierL1,
		Priority:         4,
		Compress:         false,
		InvalidationTags: []string{"virtual_media", "systems"},
	}
}

// Get retrieves a cached response from the appropriate tier
func (ac *AdvancedCache) Get(key string) (*AdvancedCacheEntry, bool) {
	// Try L1 cache first (fastest)
	ac.l1Mutex.RLock()
	if entry, exists := ac.l1Cache[key]; exists && !ac.isExpired(entry) {
		ac.l1Mutex.RUnlock()
		ac.updateAccessStats(entry)
		ac.updateHitStats(TierL1)
		return entry, true
	}
	ac.l1Mutex.RUnlock()
	ac.updateMissStats(TierL1)

	// Try L2 cache
	ac.l2Mutex.RLock()
	if entry, exists := ac.l2Cache[key]; exists && !ac.isExpired(entry) {
		ac.l2Mutex.RUnlock()
		ac.updateAccessStats(entry)
		ac.updateHitStats(TierL2)
		// Promote to L1 if frequently accessed
		if entry.AccessCount > 10 {
			ac.promoteToL1(key, entry)
		}
		return entry, true
	}
	ac.l2Mutex.RUnlock()
	ac.updateMissStats(TierL2)

	// Try L3 cache
	ac.l3Mutex.RLock()
	if entry, exists := ac.l3Cache[key]; exists && !ac.isExpired(entry) {
		ac.l3Mutex.RUnlock()
		ac.updateAccessStats(entry)
		ac.updateHitStats(TierL3)
		// Promote to L2 if frequently accessed
		if entry.AccessCount > 5 {
			ac.promoteToL2(key, entry)
		}
		return entry, true
	}
	ac.l3Mutex.RUnlock()
	ac.updateMissStats(TierL3)

	return nil, false
}

// Set stores a response in the appropriate cache tier
func (ac *AdvancedCache) Set(key string, data []byte, headers map[string]string, policy *CachePolicy) {
	if policy == nil {
		policy = ac.getDefaultPolicy()
	}

	entry := &AdvancedCacheEntry{
		Data:             data,
		Headers:          headers,
		CreatedAt:        time.Now(),
		ExpiresAt:        time.Now().Add(policy.TTL),
		AccessCount:      1,
		LastAccess:       time.Now(),
		Size:             int64(len(data)),
		Tier:             policy.Tier,
		Priority:         policy.Priority,
		InvalidationTags: policy.InvalidationTags,
	}

	// Generate ETag
	etagHash := sha256.Sum256(data)
	entry.ETag = fmt.Sprintf("W/\"%s\"", hex.EncodeToString(etagHash[:8]))

	// Compress data if policy requires it
	if policy.Compress {
		compressedData, err := ac.compressData(data)
		if err == nil {
			entry.CompressedData = compressedData
			entry.CompressedSize = int64(len(compressedData))
			ac.updateStats(1, 0, 0, 0, 0, 0) // Compression
		}
	}

	// Store in appropriate tier
	switch policy.Tier {
	case TierL1:
		ac.storeInL1(key, entry)
	case TierL2:
		ac.storeInL2(key, entry)
	case TierL3:
		ac.storeInL3(key, entry)
	}

	logger.Debug("Cached response in tier %d for key %s (TTL: %v, size: %d bytes)",
		policy.Tier, key, policy.TTL, len(data))
}

// Invalidate removes entries by tags or patterns
func (ac *AdvancedCache) Invalidate(tags []string, patterns []string) {
	count := 0

	// Invalidate by tags
	for _, tag := range tags {
		count += ac.invalidateByTag(tag)
	}

	// Invalidate by patterns
	for _, pattern := range patterns {
		count += ac.invalidateByPattern(pattern)
	}

	if count > 0 {
		ac.updateStats(0, 0, int64(count), 0, 0, 0) // Invalidations
		logger.Debug("Invalidated %d cache entries", count)
	}
}

// invalidateByTag removes entries with matching invalidation tags
func (ac *AdvancedCache) invalidateByTag(tag string) int {
	count := 0

	// Check L1 cache
	ac.l1Mutex.Lock()
	for key, entry := range ac.l1Cache {
		for _, entryTag := range entry.InvalidationTags {
			if entryTag == tag {
				delete(ac.l1Cache, key)
				count++
				break
			}
		}
	}
	ac.l1Mutex.Unlock()

	// Check L2 cache
	ac.l2Mutex.Lock()
	for key, entry := range ac.l2Cache {
		for _, entryTag := range entry.InvalidationTags {
			if entryTag == tag {
				delete(ac.l2Cache, key)
				count++
				break
			}
		}
	}
	ac.l2Mutex.Unlock()

	// Check L3 cache
	ac.l3Mutex.Lock()
	for key, entry := range ac.l3Cache {
		for _, entryTag := range entry.InvalidationTags {
			if entryTag == tag {
				delete(ac.l3Cache, key)
				count++
				break
			}
		}
	}
	ac.l3Mutex.Unlock()

	return count
}

// invalidateByPattern removes entries matching patterns
func (ac *AdvancedCache) invalidateByPattern(pattern string) int {
	count := 0

	// Check L1 cache
	ac.l1Mutex.Lock()
	for key := range ac.l1Cache {
		if strings.Contains(key, pattern) {
			delete(ac.l1Cache, key)
			count++
		}
	}
	ac.l1Mutex.Unlock()

	// Check L2 cache
	ac.l2Mutex.Lock()
	for key := range ac.l2Cache {
		if strings.Contains(key, pattern) {
			delete(ac.l2Cache, key)
			count++
		}
	}
	ac.l2Mutex.Unlock()

	// Check L3 cache
	ac.l3Mutex.Lock()
	for key := range ac.l3Cache {
		if strings.Contains(key, pattern) {
			delete(ac.l3Cache, key)
			count++
		}
	}
	ac.l3Mutex.Unlock()

	return count
}

// promoteToL1 promotes an entry from L2 to L1
func (ac *AdvancedCache) promoteToL1(key string, entry *AdvancedCacheEntry) {
	ac.l1Mutex.Lock()
	defer ac.l1Mutex.Unlock()

	// Check if L1 is full
	if len(ac.l1Cache) >= ac.maxL1Entries {
		ac.evictFromL1()
	}

	// Copy entry to L1
	ac.l1Cache[key] = entry

	// Remove from L2
	ac.l2Mutex.Lock()
	delete(ac.l2Cache, key)
	ac.l2Mutex.Unlock()

	logger.Debug("Promoted entry %s from L2 to L1", key)
}

// promoteToL2 promotes an entry from L3 to L2
func (ac *AdvancedCache) promoteToL2(key string, entry *AdvancedCacheEntry) {
	ac.l2Mutex.Lock()
	defer ac.l2Mutex.Unlock()

	// Check if L2 is full
	if len(ac.l2Cache) >= ac.maxL2Entries {
		ac.evictFromL2()
	}

	// Copy entry to L2
	ac.l2Cache[key] = entry

	// Remove from L3
	ac.l3Mutex.Lock()
	delete(ac.l3Cache, key)
	ac.l3Mutex.Unlock()

	logger.Debug("Promoted entry %s from L3 to L2", key)
}

// storeInL1 stores an entry in L1 cache
func (ac *AdvancedCache) storeInL1(key string, entry *AdvancedCacheEntry) {
	ac.l1Mutex.Lock()
	defer ac.l1Mutex.Unlock()

	if len(ac.l1Cache) >= ac.maxL1Entries {
		ac.evictFromL1()
	}

	ac.l1Cache[key] = entry
}

// storeInL2 stores an entry in L2 cache
func (ac *AdvancedCache) storeInL2(key string, entry *AdvancedCacheEntry) {
	ac.l2Mutex.Lock()
	defer ac.l2Mutex.Unlock()

	if len(ac.l2Cache) >= ac.maxL2Entries {
		ac.evictFromL2()
	}

	ac.l2Cache[key] = entry
}

// storeInL3 stores an entry in L3 cache
func (ac *AdvancedCache) storeInL3(key string, entry *AdvancedCacheEntry) {
	ac.l3Mutex.Lock()
	defer ac.l3Mutex.Unlock()

	if len(ac.l3Cache) >= ac.maxL3Entries {
		ac.evictFromL3()
	}

	ac.l3Cache[key] = entry
}

// evictFromL1 evicts the least recently used entry from L1
func (ac *AdvancedCache) evictFromL1() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range ac.l1Cache {
		if oldestKey == "" || entry.LastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.LastAccess
		}
	}

	if oldestKey != "" {
		delete(ac.l1Cache, oldestKey)
		ac.updateStats(0, 0, 0, 1, 0, 0) // L1 eviction
	}
}

// evictFromL2 evicts the least recently used entry from L2
func (ac *AdvancedCache) evictFromL2() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range ac.l2Cache {
		if oldestKey == "" || entry.LastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.LastAccess
		}
	}

	if oldestKey != "" {
		delete(ac.l2Cache, oldestKey)
		ac.updateStats(0, 0, 0, 0, 1, 0) // L2 eviction
	}
}

// evictFromL3 evicts the least recently used entry from L3
func (ac *AdvancedCache) evictFromL3() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range ac.l3Cache {
		if oldestKey == "" || entry.LastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.LastAccess
		}
	}

	if oldestKey != "" {
		delete(ac.l3Cache, oldestKey)
		ac.updateStats(0, 0, 0, 0, 0, 1) // L3 eviction
	}
}

// compressData compresses data using gzip
func (ac *AdvancedCache) compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)

	if _, err := gw.Write(data); err != nil {
		return nil, err
	}

	if err := gw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// decompressData decompresses gzipped data
func (ac *AdvancedCache) decompressData(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gr.Close()

	return io.ReadAll(gr)
}

// isExpired checks if an entry has expired
func (ac *AdvancedCache) isExpired(entry *AdvancedCacheEntry) bool {
	return time.Now().After(entry.ExpiresAt)
}

// updateAccessStats updates access statistics for an entry
func (ac *AdvancedCache) updateAccessStats(entry *AdvancedCacheEntry) {
	entry.AccessCount++
	entry.LastAccess = time.Now()
}

// updateHitStats updates hit statistics for a tier
func (ac *AdvancedCache) updateHitStats(tier CacheTier) {
	ac.statsMutex.Lock()
	defer ac.statsMutex.Unlock()

	switch tier {
	case TierL1:
		ac.stats.L1Hits++
	case TierL2:
		ac.stats.L2Hits++
	case TierL3:
		ac.stats.L3Hits++
	}
}

// updateMissStats updates miss statistics for a tier
func (ac *AdvancedCache) updateMissStats(tier CacheTier) {
	ac.statsMutex.Lock()
	defer ac.statsMutex.Unlock()

	switch tier {
	case TierL1:
		ac.stats.L1Misses++
	case TierL2:
		ac.stats.L2Misses++
	case TierL3:
		ac.stats.L3Misses++
	}
}

// updateStats updates general statistics
func (ac *AdvancedCache) updateStats(comps, decomps, invals, l1evict, l2evict, l3evict int64) {
	ac.statsMutex.Lock()
	defer ac.statsMutex.Unlock()

	ac.stats.Compressions += comps
	ac.stats.Decompressions += decomps
	ac.stats.Invalidations += invals
	ac.stats.L1Evictions += l1evict
	ac.stats.L2Evictions += l2evict
	ac.stats.L3Evictions += l3evict
}

// getDefaultPolicy returns the default cache policy
func (ac *AdvancedCache) getDefaultPolicy() *CachePolicy {
	return &CachePolicy{
		TTL:              ac.defaultTTL,
		Tier:             TierL2,
		Priority:         1,
		Compress:         true,
		InvalidationTags: []string{"default"},
	}
}

// GetPolicy returns the cache policy for a given path
func (ac *AdvancedCache) GetPolicy(path string) *CachePolicy {
	// Check for exact matches first
	if policy, exists := ac.policies[path]; exists {
		return policy
	}

	// Check for pattern matches
	for pattern, policy := range ac.policies {
		if strings.Contains(path, strings.TrimSuffix(pattern, ".*")) {
			return policy
		}
	}

	return ac.getDefaultPolicy()
}

// GetStats returns comprehensive cache statistics
func (ac *AdvancedCache) GetStats() map[string]interface{} {
	ac.statsMutex.RLock()
	defer ac.statsMutex.RUnlock()

	ac.l1Mutex.RLock()
	l1Count := len(ac.l1Cache)
	ac.l1Mutex.RUnlock()

	ac.l2Mutex.RLock()
	l2Count := len(ac.l2Cache)
	ac.l2Mutex.RUnlock()

	ac.l3Mutex.RLock()
	l3Count := len(ac.l3Cache)
	ac.l3Mutex.RUnlock()

	return map[string]interface{}{
		"tiers": map[string]interface{}{
			"l1": map[string]interface{}{
				"entries":     l1Count,
				"max_entries": ac.maxL1Entries,
				"hits":        ac.stats.L1Hits,
				"misses":      ac.stats.L1Misses,
				"evictions":   ac.stats.L1Evictions,
			},
			"l2": map[string]interface{}{
				"entries":     l2Count,
				"max_entries": ac.maxL2Entries,
				"hits":        ac.stats.L2Hits,
				"misses":      ac.stats.L2Misses,
				"evictions":   ac.stats.L2Evictions,
			},
			"l3": map[string]interface{}{
				"entries":     l3Count,
				"max_entries": ac.maxL3Entries,
				"hits":        ac.stats.L3Hits,
				"misses":      ac.stats.L3Misses,
				"evictions":   ac.stats.L3Evictions,
			},
		},
		"operations": map[string]interface{}{
			"compressions":   ac.stats.Compressions,
			"decompressions": ac.stats.Decompressions,
			"invalidations":  ac.stats.Invalidations,
			"preloads":       ac.stats.Preloads,
		},
		"uptime": time.Since(ac.stats.LastReset).String(),
	}
}

// cleanupRoutine periodically cleans up expired entries
func (ac *AdvancedCache) cleanupRoutine() {
	ac.cleanupTicker = time.NewTicker(2 * time.Minute)
	defer ac.cleanupTicker.Stop()

	for {
		select {
		case <-ac.cleanupTicker.C:
			ac.cleanupExpired()
		case <-ac.ctx.Done():
			return
		case <-ac.stopChan:
			return
		}
	}
}

// cleanupExpired removes expired entries from all tiers
func (ac *AdvancedCache) cleanupExpired() {
	now := time.Now()

	// Clean L1
	ac.l1Mutex.Lock()
	l1Count := 0
	for key, entry := range ac.l1Cache {
		if now.After(entry.ExpiresAt) {
			delete(ac.l1Cache, key)
			l1Count++
		}
	}
	ac.l1Mutex.Unlock()

	// Clean L2
	ac.l2Mutex.Lock()
	l2Count := 0
	for key, entry := range ac.l2Cache {
		if now.After(entry.ExpiresAt) {
			delete(ac.l2Cache, key)
			l2Count++
		}
	}
	ac.l2Mutex.Unlock()

	// Clean L3
	ac.l3Mutex.Lock()
	l3Count := 0
	for key, entry := range ac.l3Cache {
		if now.After(entry.ExpiresAt) {
			delete(ac.l3Cache, key)
			l3Count++
		}
	}
	ac.l3Mutex.Unlock()

	total := l1Count + l2Count + l3Count
	if total > 0 {
		logger.Debug("Cleaned up %d expired cache entries (L1: %d, L2: %d, L3: %d)",
			total, l1Count, l2Count, l3Count)
	}
}

// preloadRoutine periodically preloads frequently accessed resources
func (ac *AdvancedCache) preloadRoutine() {
	ac.preloadTicker = time.NewTicker(5 * time.Minute)
	defer ac.preloadTicker.Stop()

	for {
		select {
		case <-ac.preloadTicker.C:
			ac.performPreload()
		case <-ac.ctx.Done():
			return
		case <-ac.stopChan:
			return
		}
	}
}

// performPreload preloads frequently accessed resources
func (ac *AdvancedCache) performPreload() {
	// This would typically analyze access patterns and preload resources
	// For now, we'll just log that preload is happening
	logger.Debug("Performing cache preload...")
	ac.statsMutex.Lock()
	ac.stats.Preloads++
	ac.statsMutex.Unlock()
}

// Stop gracefully stops the advanced cache
func (ac *AdvancedCache) Stop() {
	logger.Info("Stopping advanced cache...")

	if ac.cleanupTicker != nil {
		ac.cleanupTicker.Stop()
	}

	if ac.preloadTicker != nil {
		ac.preloadTicker.Stop()
	}

	close(ac.stopChan)
	ac.cancel()

	// Clear all caches
	ac.l1Mutex.Lock()
	l1Count := len(ac.l1Cache)
	ac.l1Cache = make(map[string]*AdvancedCacheEntry)
	ac.l1Mutex.Unlock()

	ac.l2Mutex.Lock()
	l2Count := len(ac.l2Cache)
	ac.l2Cache = make(map[string]*AdvancedCacheEntry)
	ac.l2Mutex.Unlock()

	ac.l3Mutex.Lock()
	l3Count := len(ac.l3Cache)
	ac.l3Cache = make(map[string]*AdvancedCacheEntry)
	ac.l3Mutex.Unlock()

	total := l1Count + l2Count + l3Count
	logger.Info("Advanced cache stopped, cleared %d entries (L1: %d, L2: %d, L3: %d)",
		total, l1Count, l2Count, l3Count)
}
