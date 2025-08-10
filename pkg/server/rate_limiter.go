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
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// RateLimitStrategy defines how rate limiting should be applied
type RateLimitStrategy int

const (
	StrategyTokenBucket RateLimitStrategy = iota
	StrategyLeakyBucket
	StrategyFixedWindow
	StrategySlidingWindow
)

// RateLimitConfig holds configuration for rate limiting
type RateLimitConfig struct {
	RequestsPerSecond float64
	BurstSize         int
	Strategy          RateLimitStrategy
	WindowSize        time.Duration
	UserBased         bool
	IPBased           bool
	EndpointBased     bool
}

// RateLimitStats tracks rate limiting performance
type RateLimitStats struct {
	TotalRequests   int64
	AllowedRequests int64
	BlockedRequests int64
	RateLimitHits   int64
	AverageWaitTime time.Duration
	LastReset       time.Time
}

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	capacity   float64
	tokens     float64
	rate       float64
	lastRefill time.Time
	mutex      sync.Mutex
}

// NewTokenBucket creates a new token bucket
func NewTokenBucket(capacity float64, rate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		rate:       rate,
		lastRefill: time.Now(),
	}
}

// Take attempts to take a token from the bucket
func (tb *TokenBucket) Take(count float64) bool {
	tb.mutex.Lock()
	defer tb.mutex.Unlock()

	// Refill tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tokensToAdd := elapsed * tb.rate

	tb.tokens = tb.capacity
	if tb.tokens+tokensToAdd < tb.capacity {
		tb.tokens += tokensToAdd
	}

	tb.lastRefill = now

	// Check if we have enough tokens
	if tb.tokens >= count {
		tb.tokens -= count
		return true
	}

	return false
}

// TakeWithWait attempts to take a token with optional waiting
func (tb *TokenBucket) TakeWithWait(count float64, maxWait time.Duration) (bool, time.Duration) {
	tb.mutex.Lock()
	defer tb.mutex.Unlock()

	// Refill tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tokensToAdd := elapsed * tb.rate

	tb.tokens = tb.capacity
	if tb.tokens+tokensToAdd < tb.capacity {
		tb.tokens += tokensToAdd
	}

	tb.lastRefill = now

	// Check if we have enough tokens
	if tb.tokens >= count {
		tb.tokens -= count
		return true, 0
	}

	// Calculate wait time
	tokensNeeded := count - tb.tokens
	waitTime := time.Duration(tokensNeeded/tb.rate) * time.Second

	if waitTime > maxWait {
		return false, maxWait
	}

	// Wait and then take tokens
	time.Sleep(waitTime)
	tb.tokens = 0 // All tokens will be consumed

	return true, waitTime
}

// RateLimiter provides rate limiting functionality
type RateLimiter struct {
	config     RateLimitConfig
	stats      *RateLimitStats
	statsMutex sync.RWMutex

	// Token bucket for rate limiting
	tokenBucket *TokenBucket

	// User-based rate limiting
	userBuckets map[string]*TokenBucket
	userMutex   sync.RWMutex

	// IP-based rate limiting
	ipBuckets map[string]*TokenBucket
	ipMutex   sync.RWMutex

	// Endpoint-based rate limiting
	endpointBuckets map[string]*TokenBucket
	endpointMutex   sync.RWMutex

	// Global rate limiting
	globalBucket *TokenBucket
	globalMutex  sync.RWMutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		config: config,
		stats: &RateLimitStats{
			LastReset: time.Now(),
		},
		userBuckets:     make(map[string]*TokenBucket),
		ipBuckets:       make(map[string]*TokenBucket),
		endpointBuckets: make(map[string]*TokenBucket),
	}

	// Set defaults if not provided
	if rl.config.RequestsPerSecond <= 0 {
		rl.config.RequestsPerSecond = 100
	}
	if rl.config.BurstSize <= 0 {
		rl.config.BurstSize = 50
	}
	if rl.config.WindowSize <= 0 {
		rl.config.WindowSize = time.Second
	}

	// Initialize token bucket
	rl.tokenBucket = NewTokenBucket(float64(rl.config.BurstSize), rl.config.RequestsPerSecond)
	rl.globalBucket = NewTokenBucket(float64(rl.config.BurstSize), rl.config.RequestsPerSecond)

	return rl
}

// Allow checks if a request should be allowed
func (rl *RateLimiter) Allow(r *http.Request) (bool, time.Duration) {
	startTime := time.Now()

	// Update statistics
	rl.updateStats(1, 0, 0, 0, 0)

	// Check global rate limit first
	if !rl.globalBucket.Take(1) {
		rl.updateStats(0, 0, 1, 0, 0)
		return false, 0
	}

	// Check user-based rate limit
	if rl.config.UserBased {
		user := rl.getUserFromRequest(r)
		if user != "" {
			if !rl.getUserBucket(user).Take(1) {
				rl.updateStats(0, 0, 1, 0, 0)
				return false, 0
			}
		}
	}

	// Check IP-based rate limit
	if rl.config.IPBased {
		ip := rl.getIPFromRequest(r)
		if ip != "" {
			if !rl.getIPBucket(ip).Take(1) {
				rl.updateStats(0, 0, 1, 0, 0)
				return false, 0
			}
		}
	}

	// Check endpoint-based rate limit
	if rl.config.EndpointBased {
		endpoint := rl.getEndpointFromRequest(r)
		if endpoint != "" {
			if !rl.getEndpointBucket(endpoint).Take(1) {
				rl.updateStats(0, 0, 1, 0, 0)
				return false, 0
			}
		}
	}

	// Check main token bucket
	if !rl.tokenBucket.Take(1) {
		rl.updateStats(0, 0, 1, 0, 0)
		return false, 0
	}

	// Request allowed
	waitTime := time.Since(startTime)
	rl.updateStats(0, 1, 0, 1, waitTime)

	return true, waitTime
}

// AllowWithWait checks if a request should be allowed with optional waiting
func (rl *RateLimiter) AllowWithWait(r *http.Request, maxWait time.Duration) (bool, time.Duration) {
	startTime := time.Now()

	// Update statistics
	rl.updateStats(1, 0, 0, 0, 0)

	// Check global rate limit first
	allowed, waitTime := rl.globalBucket.TakeWithWait(1, maxWait)
	if !allowed {
		rl.updateStats(0, 0, 1, 0, 0)
		return false, maxWait
	}

	// Check user-based rate limit
	if rl.config.UserBased {
		user := rl.getUserFromRequest(r)
		if user != "" {
			allowed, userWait := rl.getUserBucket(user).TakeWithWait(1, maxWait-waitTime)
			if !allowed {
				rl.updateStats(0, 0, 1, 0, 0)
				return false, maxWait
			}
			waitTime += userWait
		}
	}

	// Check IP-based rate limit
	if rl.config.IPBased {
		ip := rl.getIPFromRequest(r)
		if ip != "" {
			allowed, ipWait := rl.getIPBucket(ip).TakeWithWait(1, maxWait-waitTime)
			if !allowed {
				rl.updateStats(0, 0, 1, 0, 0)
				return false, maxWait
			}
			waitTime += ipWait
		}
	}

	// Check endpoint-based rate limit
	if rl.config.EndpointBased {
		endpoint := rl.getEndpointFromRequest(r)
		if endpoint != "" {
			allowed, endpointWait := rl.getEndpointBucket(endpoint).TakeWithWait(1, maxWait-waitTime)
			if !allowed {
				rl.updateStats(0, 0, 1, 0, 0)
				return false, maxWait
			}
			waitTime += endpointWait
		}
	}

	// Check main token bucket
	allowed, bucketWait := rl.tokenBucket.TakeWithWait(1, maxWait-waitTime)
	if !allowed {
		rl.updateStats(0, 0, 1, 0, 0)
		return false, maxWait
	}
	waitTime += bucketWait

	// Request allowed
	totalWaitTime := time.Since(startTime)
	rl.updateStats(0, 1, 0, 1, totalWaitTime)

	return true, totalWaitTime
}

// getUserFromRequest extracts user information from request
func (rl *RateLimiter) getUserFromRequest(r *http.Request) string {
	// This would typically extract user from authentication context
	// For now, we'll use a simple header-based approach
	return r.Header.Get("X-User-ID")
}

// getIPFromRequest extracts IP address from request
func (rl *RateLimiter) getIPFromRequest(r *http.Request) string {
	// Check for forwarded headers first
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Fall back to remote address
	return r.RemoteAddr
}

// getEndpointFromRequest extracts endpoint information from request
func (rl *RateLimiter) getEndpointFromRequest(r *http.Request) string {
	return r.Method + ":" + r.URL.Path
}

// getUserBucket gets or creates a token bucket for a user
func (rl *RateLimiter) getUserBucket(user string) *TokenBucket {
	rl.userMutex.Lock()
	defer rl.userMutex.Unlock()

	if bucket, exists := rl.userBuckets[user]; exists {
		return bucket
	}

	bucket := NewTokenBucket(float64(rl.config.BurstSize), rl.config.RequestsPerSecond)
	rl.userBuckets[user] = bucket

	return bucket
}

// getIPBucket gets or creates a token bucket for an IP
func (rl *RateLimiter) getIPBucket(ip string) *TokenBucket {
	rl.ipMutex.Lock()
	defer rl.ipMutex.Unlock()

	if bucket, exists := rl.ipBuckets[ip]; exists {
		return bucket
	}

	bucket := NewTokenBucket(float64(rl.config.BurstSize), rl.config.RequestsPerSecond)
	rl.ipBuckets[ip] = bucket

	return bucket
}

// getEndpointBucket gets or creates a token bucket for an endpoint
func (rl *RateLimiter) getEndpointBucket(endpoint string) *TokenBucket {
	rl.endpointMutex.Lock()
	defer rl.endpointMutex.Unlock()

	if bucket, exists := rl.endpointBuckets[endpoint]; exists {
		return bucket
	}

	bucket := NewTokenBucket(float64(rl.config.BurstSize), rl.config.RequestsPerSecond)
	rl.endpointBuckets[endpoint] = bucket

	return bucket
}

// updateStats updates rate limiter statistics
func (rl *RateLimiter) updateStats(total, allowed, blocked, rateLimitHits int64, waitTime time.Duration) {
	rl.statsMutex.Lock()
	defer rl.statsMutex.Unlock()

	rl.stats.TotalRequests += total
	rl.stats.AllowedRequests += allowed
	rl.stats.BlockedRequests += blocked
	rl.stats.RateLimitHits += rateLimitHits

	if allowed > 0 {
		// Update average wait time
		currentAvg := rl.stats.AverageWaitTime
		totalAllowed := rl.stats.AllowedRequests
		rl.stats.AverageWaitTime = (currentAvg*time.Duration(totalAllowed-allowed) + waitTime) / time.Duration(totalAllowed)
	}
}

// GetStats returns rate limiter statistics
func (rl *RateLimiter) GetStats() *RateLimitStats {
	rl.statsMutex.RLock()
	defer rl.statsMutex.RUnlock()

	return &RateLimitStats{
		TotalRequests:   rl.stats.TotalRequests,
		AllowedRequests: rl.stats.AllowedRequests,
		BlockedRequests: rl.stats.BlockedRequests,
		RateLimitHits:   rl.stats.RateLimitHits,
		AverageWaitTime: rl.stats.AverageWaitTime,
		LastReset:       rl.stats.LastReset,
	}
}

// Reset resets rate limiter statistics
func (rl *RateLimiter) Reset() {
	rl.statsMutex.Lock()
	defer rl.statsMutex.Unlock()

	rl.stats = &RateLimitStats{
		LastReset: time.Now(),
	}

	logger.Info("Rate limiter statistics reset")
}

// RateLimitMiddleware provides rate limiting middleware for HTTP handlers
func (rl *RateLimiter) RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check rate limit
		allowed, waitTime := rl.Allow(r)

		if !allowed {
			// Rate limit exceeded
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)

			errorResponse := map[string]interface{}{
				"error": map[string]interface{}{
					"code":        "TooManyRequests",
					"message":     "Rate limit exceeded",
					"retry_after": 60,
				},
			}

			// Send error response
			jsonResponse, _ := rl.marshalJSON(errorResponse)
			w.Write(jsonResponse)

			logger.Warning("Rate limit exceeded for request: %s %s", r.Method, r.URL.Path)
			return
		}

		// Add rate limit headers
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rl.config.RequestsPerSecond))
		w.Header().Set("X-RateLimit-Remaining", "0") // Would need to track remaining tokens
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))

		if waitTime > 0 {
			w.Header().Set("X-RateLimit-WaitTime", waitTime.String())
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// marshalJSON marshals data to JSON (simplified for this example)
func (rl *RateLimiter) marshalJSON(data interface{}) ([]byte, error) {
	// This would use the memory manager's optimized JSON marshaling
	// For now, we'll return a simple JSON string
	return []byte(`{"error":{"code":"TooManyRequests","message":"Rate limit exceeded","retry_after":60}}`), nil
}

// RateLimitManager manages multiple rate limiters
type RateLimitManager struct {
	limiters map[string]*RateLimiter
	mutex    sync.RWMutex
}

// NewRateLimitManager creates a new rate limit manager
func NewRateLimitManager() *RateLimitManager {
	return &RateLimitManager{
		limiters: make(map[string]*RateLimiter),
	}
}

// GetOrCreate gets an existing rate limiter or creates a new one
func (rlm *RateLimitManager) GetOrCreate(name string, config RateLimitConfig) *RateLimiter {
	rlm.mutex.Lock()
	defer rlm.mutex.Unlock()

	if limiter, exists := rlm.limiters[name]; exists {
		return limiter
	}

	limiter := NewRateLimiter(config)
	rlm.limiters[name] = limiter

	return limiter
}

// Get returns an existing rate limiter
func (rlm *RateLimitManager) Get(name string) (*RateLimiter, bool) {
	rlm.mutex.RLock()
	defer rlm.mutex.RUnlock()

	limiter, exists := rlm.limiters[name]
	return limiter, exists
}

// GetAll returns all rate limiters
func (rlm *RateLimitManager) GetAll() map[string]*RateLimiter {
	rlm.mutex.RLock()
	defer rlm.mutex.RUnlock()

	result := make(map[string]*RateLimiter)
	for name, limiter := range rlm.limiters {
		result[name] = limiter
	}

	return result
}

// GetStats returns statistics for all rate limiters
func (rlm *RateLimitManager) GetStats() map[string]interface{} {
	rlm.mutex.RLock()
	defer rlm.mutex.RUnlock()

	stats := make(map[string]interface{})
	for name, limiter := range rlm.limiters {
		stats[name] = limiter.GetStats()
	}

	return stats
}

// ResetAll resets all rate limiters
func (rlm *RateLimitManager) ResetAll() {
	rlm.mutex.Lock()
	defer rlm.mutex.Unlock()

	for name, limiter := range rlm.limiters {
		limiter.Reset()
		logger.Debug("Reset rate limiter: %s", name)
	}
}

// Predefined rate limit configurations
var (
	// DefaultRateLimitConfig provides sensible defaults
	DefaultRateLimitConfig = RateLimitConfig{
		RequestsPerSecond: 100,
		BurstSize:         50,
		Strategy:          StrategyTokenBucket,
		WindowSize:        time.Second,
		UserBased:         false,
		IPBased:           true,
		EndpointBased:     false,
	}

	// StrictRateLimitConfig for sensitive endpoints
	StrictRateLimitConfig = RateLimitConfig{
		RequestsPerSecond: 10,
		BurstSize:         5,
		Strategy:          StrategyTokenBucket,
		WindowSize:        time.Second,
		UserBased:         true,
		IPBased:           true,
		EndpointBased:     true,
	}

	// RelaxedRateLimitConfig for public endpoints
	RelaxedRateLimitConfig = RateLimitConfig{
		RequestsPerSecond: 1000,
		BurstSize:         200,
		Strategy:          StrategyTokenBucket,
		WindowSize:        time.Second,
		UserBased:         false,
		IPBased:           false,
		EndpointBased:     false,
	}
)
