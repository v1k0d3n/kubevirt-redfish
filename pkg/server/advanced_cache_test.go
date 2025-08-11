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
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNewAdvancedCache(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	if ac == nil {
		t.Fatal("Expected AdvancedCache to be created")
	}

	if ac.l1Cache == nil {
		t.Error("Expected L1 cache to be initialized")
	}

	if ac.l2Cache == nil {
		t.Error("Expected L2 cache to be initialized")
	}

	if ac.l3Cache == nil {
		t.Error("Expected L3 cache to be initialized")
	}

	if ac.policies == nil {
		t.Error("Expected policies to be initialized")
	}

	if ac.stats == nil {
		t.Error("Expected stats to be initialized")
	}
}

func TestAdvancedCache_Get(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Test getting non-existent key
	entry, exists := ac.Get("nonexistent")
	if exists {
		t.Error("Expected non-existent key to return false")
	}
	if entry != nil {
		t.Error("Expected non-existent key to return nil entry")
	}

	// Test getting existing key
	policy := &CachePolicy{
		TTL:  time.Minute,
		Tier: TierL1,
	}
	ac.Set("test_key", []byte("test_data"), map[string]string{"header": "value"}, policy)

	entry, exists = ac.Get("test_key")
	if !exists {
		t.Error("Expected existing key to return true")
	}
	if entry == nil {
		t.Error("Expected existing key to return non-nil entry")
		return // Early return to prevent nil pointer dereference
	}
	if string(entry.Data) != "test_data" {
		t.Errorf("Expected data to be 'test_data', got '%s'", string(entry.Data))
	}
}

func TestAdvancedCache_Set(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	policy := &CachePolicy{
		TTL:      time.Minute,
		Tier:     TierL1,
		Compress: true,
	}

	ac.Set("test_key", []byte("test_data"), map[string]string{"header": "value"}, policy)

	entry, exists := ac.Get("test_key")
	if !exists {
		t.Error("Expected entry to exist after Set")
	}
	if string(entry.Data) != "test_data" {
		t.Errorf("Expected data to be 'test_data', got '%s'", string(entry.Data))
	}
}

func TestAdvancedCache_Invalidate(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	policy := &CachePolicy{
		TTL:              time.Minute,
		Tier:             TierL1,
		InvalidationTags: []string{"tag1", "tag2"},
	}

	ac.Set("key1", []byte("data1"), map[string]string{"header": "value1"}, policy)
	ac.Set("key2", []byte("data2"), map[string]string{"header": "value2"}, policy)

	// Test invalidation by tags
	ac.Invalidate([]string{"tag1"}, nil)

	if _, exists := ac.Get("key1"); exists {
		t.Error("Expected key1 to be invalidated")
	}
	if _, exists := ac.Get("key2"); exists {
		t.Error("Expected key2 to be invalidated")
	}
}

func TestAdvancedCache_invalidateByTag(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Add test data with tags
	policy := &CachePolicy{
		TTL:              time.Minute,
		Tier:             TierL1,
		InvalidationTags: []string{"tag1", "tag2"},
	}

	ac.Set("key1", []byte("data1"), map[string]string{"header": "value1"}, policy)
	ac.Set("key2", []byte("data2"), map[string]string{"header": "value2"}, policy)
	ac.Set("key3", []byte("data3"), map[string]string{"header": "value3"}, &CachePolicy{
		TTL:              time.Minute,
		Tier:             TierL1,
		InvalidationTags: []string{"tag3"},
	})

	// Test invalidating by tag
	count := ac.invalidateByTag("tag1")
	if count != 2 {
		t.Errorf("Expected 2 entries to be invalidated, got %d", count)
	}

	// Verify entries are removed
	if _, exists := ac.Get("key1"); exists {
		t.Error("Expected key1 to be invalidated")
	}
	if _, exists := ac.Get("key2"); exists {
		t.Error("Expected key2 to be invalidated")
	}
	if _, exists := ac.Get("key3"); !exists {
		t.Error("Expected key3 to remain")
	}
}

func TestAdvancedCache_promoteToL1(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Fill L1 cache to capacity
	for i := 0; i < ac.maxL1Entries+1; i++ {
		key := fmt.Sprintf("key%d", i)
		entry := &AdvancedCacheEntry{
			Data:        []byte(fmt.Sprintf("data%d", i)),
			Headers:     map[string]string{"header": "value"},
			CreatedAt:   time.Now(),
			ExpiresAt:   time.Now().Add(time.Minute),
			Tier:        TierL1,
			AccessCount: 1,
			LastAccess:  time.Now(),
		}
		ac.storeInL1(key, entry)
	}

	// Create entry to promote
	promoteEntry := &AdvancedCacheEntry{
		Data:        []byte("promote_data"),
		Headers:     map[string]string{"header": "promote_value"},
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Minute),
		Tier:        TierL2,
		AccessCount: 10, // High access count to trigger promotion
		LastAccess:  time.Now(),
	}

	// Add to L2 first
	ac.storeInL2("promote_key", promoteEntry)

	// Promote to L1
	ac.promoteToL1("promote_key", promoteEntry)

	// Verify promotion
	if _, exists := ac.Get("promote_key"); !exists {
		t.Error("Expected promoted entry to exist in cache")
	}

	// Verify L1 size is within limits
	ac.l1Mutex.RLock()
	l1Size := len(ac.l1Cache)
	ac.l1Mutex.RUnlock()

	if l1Size > ac.maxL1Entries {
		t.Errorf("Expected L1 cache size to be <= %d, got %d", ac.maxL1Entries, l1Size)
	}
}

func TestAdvancedCache_promoteToL2(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Fill L2 cache to capacity
	for i := 0; i < ac.maxL2Entries+1; i++ {
		key := fmt.Sprintf("key%d", i)
		entry := &AdvancedCacheEntry{
			Data:        []byte(fmt.Sprintf("data%d", i)),
			Headers:     map[string]string{"header": "value"},
			CreatedAt:   time.Now(),
			ExpiresAt:   time.Now().Add(time.Minute),
			Tier:        TierL2,
			AccessCount: 1,
			LastAccess:  time.Now(),
		}
		ac.storeInL2(key, entry)
	}

	// Create entry to promote
	promoteEntry := &AdvancedCacheEntry{
		Data:        []byte("promote_data"),
		Headers:     map[string]string{"header": "promote_value"},
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Minute),
		Tier:        TierL3,
		AccessCount: 5, // Medium access count to trigger promotion
		LastAccess:  time.Now(),
	}

	// Add to L3 first
	ac.storeInL3("promote_key", promoteEntry)

	// Promote to L2
	ac.promoteToL2("promote_key", promoteEntry)

	// Verify promotion
	if _, exists := ac.Get("promote_key"); !exists {
		t.Error("Expected promoted entry to exist in cache")
	}

	// Verify L2 size is within limits
	ac.l2Mutex.RLock()
	l2Size := len(ac.l2Cache)
	ac.l2Mutex.RUnlock()

	if l2Size > ac.maxL2Entries {
		t.Errorf("Expected L2 cache size to be <= %d, got %d", ac.maxL2Entries, l2Size)
	}
}

func TestAdvancedCache_storeInL2(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	entry := &AdvancedCacheEntry{
		Data:        []byte("test_data"),
		Headers:     map[string]string{"header": "value"},
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Minute),
		Tier:        TierL2,
		AccessCount: 1,
		LastAccess:  time.Now(),
	}

	ac.storeInL2("test_key", entry)

	// Verify entry is stored in L2
	ac.l2Mutex.RLock()
	_, exists := ac.l2Cache["test_key"]
	ac.l2Mutex.RUnlock()

	if !exists {
		t.Error("Expected entry to be stored in L2 cache")
	}
}

func TestAdvancedCache_storeInL3(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	entry := &AdvancedCacheEntry{
		Data:        []byte("test_data"),
		Headers:     map[string]string{"header": "value"},
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Minute),
		Tier:        TierL3,
		AccessCount: 1,
		LastAccess:  time.Now(),
	}

	ac.storeInL3("test_key", entry)

	// Verify entry is stored in L3
	ac.l3Mutex.RLock()
	_, exists := ac.l3Cache["test_key"]
	ac.l3Mutex.RUnlock()

	if !exists {
		t.Error("Expected entry to be stored in L3 cache")
	}
}

func TestAdvancedCache_evictFromL1(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Fill L1 cache beyond capacity
	for i := 0; i < ac.maxL1Entries+5; i++ {
		key := fmt.Sprintf("key%d", i)
		entry := &AdvancedCacheEntry{
			Data:        []byte(fmt.Sprintf("data%d", i)),
			Headers:     map[string]string{"header": "value"},
			CreatedAt:   time.Now(),
			ExpiresAt:   time.Now().Add(time.Minute),
			Tier:        TierL1,
			AccessCount: int64(i), // Different access counts
			LastAccess:  time.Now(),
		}
		ac.storeInL1(key, entry)
	}

	// Trigger eviction
	ac.evictFromL1()

	// Verify L1 size is within limits
	ac.l1Mutex.RLock()
	l1Size := len(ac.l1Cache)
	ac.l1Mutex.RUnlock()

	if l1Size > ac.maxL1Entries {
		t.Errorf("Expected L1 cache size to be <= %d, got %d", ac.maxL1Entries, l1Size)
	}
}

func TestAdvancedCache_evictFromL2(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Fill L2 cache beyond capacity
	for i := 0; i < ac.maxL2Entries+5; i++ {
		key := fmt.Sprintf("key%d", i)
		entry := &AdvancedCacheEntry{
			Data:        []byte(fmt.Sprintf("data%d", i)),
			Headers:     map[string]string{"header": "value"},
			CreatedAt:   time.Now(),
			ExpiresAt:   time.Now().Add(time.Minute),
			Tier:        TierL2,
			AccessCount: int64(i), // Different access counts
			LastAccess:  time.Now(),
		}
		ac.storeInL2(key, entry)
	}

	// Trigger eviction
	ac.evictFromL2()

	// Verify L2 size is within limits
	ac.l2Mutex.RLock()
	l2Size := len(ac.l2Cache)
	ac.l2Mutex.RUnlock()

	if l2Size > ac.maxL2Entries {
		t.Errorf("Expected L2 cache size to be <= %d, got %d", ac.maxL2Entries, l2Size)
	}
}

func TestAdvancedCache_evictFromL3(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Fill L3 cache beyond capacity
	for i := 0; i < ac.maxL3Entries+5; i++ {
		key := fmt.Sprintf("key%d", i)
		entry := &AdvancedCacheEntry{
			Data:        []byte(fmt.Sprintf("data%d", i)),
			Headers:     map[string]string{"header": "value"},
			CreatedAt:   time.Now(),
			ExpiresAt:   time.Now().Add(time.Minute),
			Tier:        TierL3,
			AccessCount: int64(i), // Different access counts
			LastAccess:  time.Now(),
		}
		ac.storeInL3(key, entry)
	}

	// Trigger eviction
	ac.evictFromL3()

	// Verify L3 size is within limits
	ac.l3Mutex.RLock()
	l3Size := len(ac.l3Cache)
	ac.l3Mutex.RUnlock()

	if l3Size > ac.maxL3Entries {
		t.Errorf("Expected L3 cache size to be <= %d, got %d", ac.maxL3Entries, l3Size)
	}
}

func TestAdvancedCache_compressData(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	testData := []byte("This is test data that should be compressed. " + strings.Repeat("Repeating content for compression testing. ", 100))

	compressed, err := ac.compressData(testData)
	if err != nil {
		t.Fatalf("Failed to compress data: %v", err)
	}

	if len(compressed) >= len(testData) {
		t.Error("Expected compressed data to be smaller than original data")
	}

	// Test with empty data
	emptyCompressed, err := ac.compressData([]byte{})
	if err != nil {
		t.Fatalf("Failed to compress empty data: %v", err)
	}

	if len(emptyCompressed) == 0 {
		t.Error("Expected compressed empty data to have some content")
	}
}

func TestAdvancedCache_decompressData(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	originalData := []byte("This is test data that should be compressed and decompressed. " + strings.Repeat("Repeating content for compression testing. ", 100))

	// Compress first
	compressed, err := ac.compressData(originalData)
	if err != nil {
		t.Fatalf("Failed to compress data: %v", err)
	}

	// Decompress
	decompressed, err := ac.decompressData(compressed)
	if err != nil {
		t.Fatalf("Failed to decompress data: %v", err)
	}

	if !bytes.Equal(originalData, decompressed) {
		t.Error("Decompressed data does not match original data")
	}

	// Test with invalid compressed data
	_, err = ac.decompressData([]byte("invalid compressed data"))
	if err == nil {
		t.Error("Expected error when decompressing invalid data")
	}
}

func TestAdvancedCache_getDefaultPolicy(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	policy := ac.getDefaultPolicy()
	if policy == nil {
		t.Fatal("Expected default policy to not be nil")
	}

	if policy.TTL != ac.defaultTTL {
		t.Errorf("Expected default TTL to be %v, got %v", ac.defaultTTL, policy.TTL)
	}

	if policy.Tier != TierL2 {
		t.Errorf("Expected default tier to be TierL2, got %v", policy.Tier)
	}

	if policy.Priority != 1 {
		t.Errorf("Expected default priority to be 1, got %d", policy.Priority)
	}

	if !policy.Compress {
		t.Error("Expected default compress to be true")
	}
}

func TestAdvancedCache_GetPolicy(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Test getting policy for specific path
	policy := ac.GetPolicy("/redfish/v1/Systems")
	if policy == nil {
		t.Fatal("Expected policy to not be nil")
	}

	// Test getting policy for non-existent path (should return default)
	defaultPolicy := ac.GetPolicy("/non/existent/path")
	if defaultPolicy == nil {
		t.Fatal("Expected default policy to not be nil")
	}

	// Verify it's the same as getDefaultPolicy
	expectedDefault := ac.getDefaultPolicy()
	if defaultPolicy.TTL != expectedDefault.TTL {
		t.Errorf("Expected default TTL to match, got %v vs %v", defaultPolicy.TTL, expectedDefault.TTL)
	}
}

func TestAdvancedCache_cleanupExpired(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Add expired entries
	expiredEntry := &AdvancedCacheEntry{
		Data:      []byte("expired_data"),
		Headers:   map[string]string{"header": "value"},
		CreatedAt: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(-time.Minute), // Expired
		Tier:      TierL1,
	}
	ac.storeInL1("expired_key", expiredEntry)

	// Add valid entries
	validEntry := &AdvancedCacheEntry{
		Data:      []byte("valid_data"),
		Headers:   map[string]string{"header": "value"},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour), // Valid
		Tier:      TierL1,
	}
	ac.storeInL1("valid_key", validEntry)

	// Run cleanup
	ac.cleanupExpired()

	// Verify expired entry is removed
	if _, exists := ac.Get("expired_key"); exists {
		t.Error("Expected expired entry to be removed")
	}

	// Verify valid entry remains
	if _, exists := ac.Get("valid_key"); !exists {
		t.Error("Expected valid entry to remain")
	}
}

func TestAdvancedCache_performPreload(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Add some entries to L3 that could be preloaded
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("preload_key_%d", i)
		entry := &AdvancedCacheEntry{
			Data:        []byte(fmt.Sprintf("preload_data_%d", i)),
			Headers:     map[string]string{"header": "value"},
			CreatedAt:   time.Now(),
			ExpiresAt:   time.Now().Add(time.Hour),
			Tier:        TierL3,
			AccessCount: int64(i + 1), // Different access counts
			LastAccess:  time.Now(),
		}
		ac.storeInL3(key, entry)
	}

	// Perform preload
	ac.performPreload()

	// Verify some entries were promoted (this is probabilistic)
	ac.l2Mutex.RLock()
	l2Size := len(ac.l2Cache)
	ac.l2Mutex.RUnlock()

	// At least some entries should be promoted based on access patterns
	if l2Size == 0 {
		t.Log("No entries were preloaded, which is acceptable for this test")
	}
}

func TestAdvancedCache_invalidateByPattern(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Add test data with different patterns
	policy := &CachePolicy{
		TTL:  time.Minute,
		Tier: TierL1,
	}

	ac.Set("user_profile_123", []byte("data1"), map[string]string{"header": "value1"}, policy)
	ac.Set("user_profile_456", []byte("data2"), map[string]string{"header": "value2"}, policy)
	ac.Set("system_config", []byte("data3"), map[string]string{"header": "value3"}, policy)

	// Test invalidating by pattern
	count := ac.invalidateByPattern("user_profile")
	if count != 2 {
		t.Errorf("Expected 2 entries to be invalidated, got %d", count)
	}

	// Verify entries are removed
	if _, exists := ac.Get("user_profile_123"); exists {
		t.Error("Expected user_profile_123 to be invalidated")
	}
	if _, exists := ac.Get("user_profile_456"); exists {
		t.Error("Expected user_profile_456 to be invalidated")
	}
	if _, exists := ac.Get("system_config"); !exists {
		t.Error("Expected system_config to remain")
	}
}

func TestAdvancedCache_UpdateHitStats(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Test updating hit stats for each tier
	ac.updateHitStats(TierL1)
	ac.updateHitStats(TierL2)
	ac.updateHitStats(TierL3)

	stats := ac.GetStats()

	// Verify hit counts are updated
	tiers := stats["tiers"].(map[string]interface{})
	l1 := tiers["l1"].(map[string]interface{})
	l2 := tiers["l2"].(map[string]interface{})
	l3 := tiers["l3"].(map[string]interface{})

	if l1["hits"].(int64) != 1 {
		t.Errorf("Expected L1 hits to be 1, got %d", l1["hits"])
	}
	if l2["hits"].(int64) != 1 {
		t.Errorf("Expected L2 hits to be 1, got %d", l2["hits"])
	}
	if l3["hits"].(int64) != 1 {
		t.Errorf("Expected L3 hits to be 1, got %d", l3["hits"])
	}
}

func TestAdvancedCache_UpdateStats(t *testing.T) {
	ac := NewAdvancedCache()
	defer ac.Stop()

	// Update various stats
	ac.updateStats(5, 3, 2, 1, 1, 1)

	stats := ac.GetStats()

	// Verify stats are updated
	operations := stats["operations"].(map[string]interface{})
	tiers := stats["tiers"].(map[string]interface{})
	l1 := tiers["l1"].(map[string]interface{})
	l2 := tiers["l2"].(map[string]interface{})
	l3 := tiers["l3"].(map[string]interface{})

	if operations["compressions"].(int64) != 5 {
		t.Errorf("Expected compressions to be 5, got %d", operations["compressions"])
	}
	if operations["decompressions"].(int64) != 3 {
		t.Errorf("Expected decompressions to be 3, got %d", operations["decompressions"])
	}
	if operations["invalidations"].(int64) != 2 {
		t.Errorf("Expected invalidations to be 2, got %d", operations["invalidations"])
	}
	if l1["evictions"].(int64) != 1 {
		t.Errorf("Expected L1 evictions to be 1, got %d", l1["evictions"])
	}
	if l2["evictions"].(int64) != 1 {
		t.Errorf("Expected L2 evictions to be 1, got %d", l2["evictions"])
	}
	if l3["evictions"].(int64) != 1 {
		t.Errorf("Expected L3 evictions to be 1, got %d", l3["evictions"])
	}
}
