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

// Package auth provides enhanced authentication middleware for the KubeVirt Redfish API server.
// It implements advanced security features including password masking, rate limiting,
// comprehensive audit logging, and improved authentication mechanisms.
//
// The enhanced middleware provides:
// - Advanced authentication logging with password masking
// - Rate limiting to prevent brute force attacks
// - Audit trails for security compliance
// - Session tracking and monitoring
// - Enhanced error handling and security responses
// - Backward compatibility with existing basic auth
//
// Example usage:
//
//	// Create enhanced authentication middleware
//	enhancedAuth := auth.NewEnhancedMiddleware(config)
//
//	// Apply middleware to HTTP handlers
//	http.HandleFunc("/redfish/v1/", enhancedAuth.Authenticate(handler))
//
//	// Access audit logs and security metrics
//	metrics := enhancedAuth.GetSecurityMetrics()
package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// SecurityEvent represents a security-related event for audit logging.
// It captures authentication attempts, failures, and security incidents.
type SecurityEvent struct {
	Timestamp     time.Time         `json:"timestamp"`
	EventType     string            `json:"event_type"`
	Username      string            `json:"username"`
	IPAddress     string            `json:"ip_address"`
	UserAgent     string            `json:"user_agent"`
	Path          string            `json:"path"`
	Method        string            `json:"method"`
	Status        string            `json:"status"`
	Details       map[string]string `json:"details"`
	CorrelationID string            `json:"correlation_id"`
}

// RateLimitInfo tracks rate limiting information for IP addresses and users.
// It prevents brute force attacks and excessive authentication attempts.
type RateLimitInfo struct {
	Attempts     int       `json:"attempts"`
	LastAttempt  time.Time `json:"last_attempt"`
	BlockedUntil time.Time `json:"blocked_until"`
	Failures     int       `json:"failures"`
}

// SecurityMetrics tracks security-related metrics for monitoring and alerting.
// It provides insights into authentication patterns and security incidents.
type SecurityMetrics struct {
	TotalAttempts     int64 `json:"total_attempts"`
	SuccessfulLogins  int64 `json:"successful_logins"`
	FailedLogins      int64 `json:"failed_logins"`
	BlockedAttempts   int64 `json:"blocked_attempts"`
	RateLimitHits     int64 `json:"rate_limit_hits"`
	SecurityIncidents int64 `json:"security_incidents"`
}

// EnhancedMiddleware represents the enhanced authentication middleware.
// It provides advanced security features while maintaining backward compatibility.
type EnhancedMiddleware struct {
	config          *config.Config
	rateLimits      map[string]*RateLimitInfo // IP -> rate limit info
	userRateLimits  map[string]*RateLimitInfo // username -> rate limit info
	securityEvents  []SecurityEvent           // Audit trail
	metrics         SecurityMetrics           // Security metrics
	mutex           sync.RWMutex              // Thread safety
	maxEvents       int                       // Maximum events to keep in memory
	rateLimitWindow time.Duration             // Rate limit window
	maxAttempts     int                       // Maximum attempts per window
	blockDuration   time.Duration             // Block duration after max attempts
}

// NewEnhancedMiddleware creates a new enhanced authentication middleware instance.
// It initializes the middleware with advanced security features and monitoring.
//
// Parameters:
// - config: Application configuration containing user and chassis information
//
// Returns:
// - *EnhancedMiddleware: Initialized enhanced authentication middleware
func NewEnhancedMiddleware(config *config.Config) *EnhancedMiddleware {
	return &EnhancedMiddleware{
		config:          config,
		rateLimits:      make(map[string]*RateLimitInfo),
		userRateLimits:  make(map[string]*RateLimitInfo),
		securityEvents:  make([]SecurityEvent, 0),
		maxEvents:       1000, // Keep last 1000 security events
		rateLimitWindow: 5 * time.Minute,
		maxAttempts:     10, // Max 10 attempts per 5 minutes
		blockDuration:   15 * time.Minute,
	}
}

// Authenticate is the enhanced middleware function that performs authentication.
// It includes advanced logging, rate limiting, and security monitoring.
//
// Parameters:
// - handler: HTTP handler function to wrap with authentication
//
// Returns:
// - http.HandlerFunc: Wrapped handler with enhanced authentication middleware
func (m *EnhancedMiddleware) Authenticate(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Generate correlation ID for request tracing
		correlationID := logger.GetCorrelationID(r.Context())

		// Extract client information
		clientIP := m.getClientIP(r)
		userAgent := r.UserAgent()

		// Allow unauthenticated access to the Redfish service root for health checks
		if r.Method == "GET" && r.URL.Path == "/redfish/v1/" {
			m.logSecurityEvent(SecurityEvent{
				Timestamp:     time.Now(),
				EventType:     "service_discovery",
				IPAddress:     clientIP,
				UserAgent:     userAgent,
				Path:          r.URL.Path,
				Method:        r.Method,
				Status:        "allowed",
				CorrelationID: correlationID,
			})

			// Create minimal auth context for service root
			authCtx := &AuthContext{
				User:    nil, // No user for unauthenticated access
				Chassis: "*", // Allow discovery of all chassis
			}

			// Inject context into request
			ctx := logger.WithAuth(r.Context(), authCtx)
			r = r.WithContext(ctx)

			// Call the original handler
			handler(w, r)
			return
		}

		// Check rate limiting before authentication
		if m.isRateLimited(clientIP, correlationID) {
			m.logSecurityEvent(SecurityEvent{
				Timestamp:     time.Now(),
				EventType:     "rate_limit_exceeded",
				IPAddress:     clientIP,
				UserAgent:     userAgent,
				Path:          r.URL.Path,
				Method:        r.Method,
				Status:        "blocked",
				Details:       map[string]string{"reason": "rate_limit_exceeded"},
				CorrelationID: correlationID,
			})

			m.sendRateLimitResponse(w, "Too many authentication attempts")
			return
		}

		// Extract chassis from URL path
		chassis := m.extractChassisFromPath(r.URL.Path)
		if chassis == "" {
			chassis = "*"
		}

		// Extract and validate credentials with enhanced logging
		user, err := m.extractAndValidateCredentialsEnhanced(r, correlationID)
		if err != nil {
			// Log failed authentication attempt
			m.logSecurityEvent(SecurityEvent{
				Timestamp:     time.Now(),
				EventType:     "authentication_failed",
				IPAddress:     clientIP,
				UserAgent:     userAgent,
				Path:          r.URL.Path,
				Method:        r.Method,
				Status:        "failed",
				Details:       map[string]string{"error": err.Error()},
				CorrelationID: correlationID,
			})

			// Update rate limiting
			m.updateRateLimit(clientIP, false)

			m.sendUnauthorizedResponse(w, "Authentication failed")
			return
		}

		// Check chassis access
		if !m.hasChassisAccess(user, chassis) {
			m.logSecurityEvent(SecurityEvent{
				Timestamp:     time.Now(),
				EventType:     "authorization_failed",
				Username:      user.Username,
				IPAddress:     clientIP,
				UserAgent:     userAgent,
				Path:          r.URL.Path,
				Method:        r.Method,
				Status:        "forbidden",
				Details:       map[string]string{"requested_chassis": chassis, "user_chassis": strings.Join(user.Chassis, ",")},
				CorrelationID: correlationID,
			})

			m.sendForbiddenResponse(w, "Access denied to chassis")
			return
		}

		// Log successful authentication
		m.logSecurityEvent(SecurityEvent{
			Timestamp:     time.Now(),
			EventType:     "authentication_success",
			Username:      user.Username,
			IPAddress:     clientIP,
			UserAgent:     userAgent,
			Path:          r.URL.Path,
			Method:        r.Method,
			Status:        "success",
			Details:       map[string]string{"chassis": chassis},
			CorrelationID: correlationID,
		})

		// Update rate limiting (successful login)
		m.updateRateLimit(clientIP, true)

		// Create authentication context
		authCtx := &AuthContext{
			User:    user,
			Chassis: chassis,
		}

		// Inject context into request
		ctx := logger.WithAuth(r.Context(), authCtx)
		r = r.WithContext(ctx)

		// Set user header for logging middleware
		r.Header.Set("X-Redfish-User", user.Username)

		// Call the original handler
		handler(w, r)
	}
}

// extractAndValidateCredentialsEnhanced performs enhanced credential validation.
// It includes password masking in logs and improved error handling.
//
// Parameters:
// - r: HTTP request containing authentication headers
// - correlationID: Request correlation ID for tracing
//
// Returns:
// - *User: Validated user information
// - error: Authentication error if credentials are invalid
func (m *EnhancedMiddleware) extractAndValidateCredentialsEnhanced(r *http.Request, correlationID string) (*User, error) {
	// Extract Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("no authorization header")
	}

	// Parse Basic authentication
	if !strings.HasPrefix(authHeader, "Basic ") {
		return nil, fmt.Errorf("unsupported authentication method")
	}

	// Decode credentials
	encoded := strings.TrimPrefix(authHeader, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials encoding")
	}

	// Parse username and password
	credentials := strings.SplitN(string(decoded), ":", 2)
	if len(credentials) != 2 {
		return nil, fmt.Errorf("invalid credentials format")
	}

	username := credentials[0]
	password := credentials[1]

	// Log authentication attempt with masked password
	maskedPassword := m.maskPassword(password)
	logger.DebugStructured("Authentication attempt", map[string]interface{}{
		"correlation_id": correlationID,
		"username":       username,
		"password_mask":  maskedPassword,
		"ip_address":     m.getClientIP(r),
		"user_agent":     r.UserAgent(),
		"path":           r.URL.Path,
		"method":         r.Method,
	})

	// Validate against configuration
	user, err := m.config.GetUserByCredentials(username, password)
	if err != nil {
		// Log failed authentication with masked password
		logger.DebugStructured("Authentication failed", map[string]interface{}{
			"correlation_id": correlationID,
			"username":       username,
			"password_mask":  maskedPassword,
			"error":          err.Error(),
			"ip_address":     m.getClientIP(r),
		})
		return nil, fmt.Errorf("invalid credentials")
	}

	// Log successful authentication with masked password
	logger.DebugStructured("Authentication successful", map[string]interface{}{
		"correlation_id": correlationID,
		"username":       username,
		"password_mask":  maskedPassword,
		"chassis":        user.Chassis,
		"ip_address":     m.getClientIP(r),
	})

	return &User{
		Username: user.Username,
		Password: user.Password,
		Chassis:  user.Chassis,
	}, nil
}

// maskPassword masks a password for secure logging.
// It shows only 8 asterisks regardless of password length for maximum security.
//
// Parameters:
// - password: The password to mask
//
// Returns:
// - string: Masked password safe for logging
func (m *EnhancedMiddleware) maskPassword(password string) string {
	return "********"
}

// getClientIP extracts the real client IP address from the request.
// It handles proxy headers and various deployment scenarios.
//
// Parameters:
// - r: HTTP request
//
// Returns:
// - string: Client IP address
func (m *EnhancedMiddleware) getClientIP(r *http.Request) string {
	// Check for proxy headers
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if commaIndex := strings.Index(ip, ","); commaIndex != -1 {
			return strings.TrimSpace(ip[:commaIndex])
		}
		return strings.TrimSpace(ip)
	}

	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}

	if ip := r.Header.Get("X-Client-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}

	// Fall back to remote address
	if r.RemoteAddr != "" {
		// Remove port if present
		if colonIndex := strings.LastIndex(r.RemoteAddr, ":"); colonIndex != -1 {
			return r.RemoteAddr[:colonIndex]
		}
		return r.RemoteAddr
	}

	return "unknown"
}

// isRateLimited checks if an IP address is currently rate limited.
// It prevents brute force attacks by limiting authentication attempts.
//
// Parameters:
// - clientIP: Client IP address to check
// - correlationID: Request correlation ID for logging
//
// Returns:
// - bool: True if rate limited, false otherwise
func (m *EnhancedMiddleware) isRateLimited(clientIP, correlationID string) bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Clean up old rate limit entries
	m.cleanupRateLimits()

	// Check if IP is blocked
	if info, exists := m.rateLimits[clientIP]; exists {
		if time.Now().Before(info.BlockedUntil) {
			logger.DebugStructured("Rate limit hit", map[string]interface{}{
				"correlation_id": correlationID,
				"ip_address":     clientIP,
				"blocked_until":  info.BlockedUntil,
				"attempts":       info.Attempts,
				"failures":       info.Failures,
			})
			return true
		}

		// Reset if block period has expired
		if time.Since(info.LastAttempt) > m.rateLimitWindow {
			info.Attempts = 0
			info.Failures = 0
		}
	}

	return false
}

// updateRateLimit updates rate limiting information for an IP address.
// It tracks successful and failed authentication attempts.
//
// Parameters:
// - clientIP: Client IP address
// - success: Whether the authentication was successful
func (m *EnhancedMiddleware) updateRateLimit(clientIP string, success bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	now := time.Now()

	// Get or create rate limit info
	info, exists := m.rateLimits[clientIP]
	if !exists {
		info = &RateLimitInfo{
			Attempts:    0,
			LastAttempt: now,
			Failures:    0,
		}
		m.rateLimits[clientIP] = info
	}

	// Update attempts
	info.Attempts++
	info.LastAttempt = now

	if !success {
		info.Failures++

		// Block if too many failures
		if info.Failures >= m.maxAttempts {
			info.BlockedUntil = now.Add(m.blockDuration)
			logger.Warning("IP address %s blocked due to too many failed authentication attempts", clientIP)
		}
	} else {
		// Reset failures on successful authentication
		info.Failures = 0
	}
}

// cleanupRateLimits removes old rate limit entries to prevent memory leaks.
// It cleans up entries older than the rate limit window.
func (m *EnhancedMiddleware) cleanupRateLimits() {
	cutoff := time.Now().Add(-m.rateLimitWindow)

	for ip, info := range m.rateLimits {
		if info.LastAttempt.Before(cutoff) && time.Now().After(info.BlockedUntil) {
			delete(m.rateLimits, ip)
		}
	}

	for username, info := range m.userRateLimits {
		if info.LastAttempt.Before(cutoff) && time.Now().After(info.BlockedUntil) {
			delete(m.userRateLimits, username)
		}
	}
}

// logSecurityEvent logs a security event for audit purposes.
// It maintains an in-memory audit trail of security-related events.
//
// Parameters:
// - event: Security event to log
func (m *EnhancedMiddleware) logSecurityEvent(event SecurityEvent) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Add event to audit trail
	m.securityEvents = append(m.securityEvents, event)

	// Limit the number of events kept in memory
	if len(m.securityEvents) > m.maxEvents {
		m.securityEvents = m.securityEvents[len(m.securityEvents)-m.maxEvents:]
	}

	// Update metrics
	switch event.EventType {
	case "authentication_success":
		m.metrics.SuccessfulLogins++
	case "authentication_failed", "authorization_failed":
		m.metrics.FailedLogins++
	case "rate_limit_exceeded":
		m.metrics.RateLimitHits++
		m.metrics.BlockedAttempts++
	}
	m.metrics.TotalAttempts++

	// Log the event
	logger.InfoStructured("Security event", map[string]interface{}{
		"event_type":     event.EventType,
		"username":       event.Username,
		"ip_address":     event.IPAddress,
		"status":         event.Status,
		"path":           event.Path,
		"method":         event.Method,
		"correlation_id": event.CorrelationID,
		"details":        event.Details,
	})
}

// sendRateLimitResponse sends an HTTP 429 Too Many Requests response.
// It provides a standardized response for rate limit violations.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (m *EnhancedMiddleware) sendRateLimitResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "900") // 15 minutes
	w.WriteHeader(http.StatusTooManyRequests)

	errorResponse := fmt.Sprintf(`{
		"error": {
			"code": "Base.1.0.GeneralError",
			"message": "%s",
			"@Message.ExtendedInfo": [
				{
					"@odata.type": "#Message.v1_0_0.Message",
					"MessageId": "Base.1.0.RateLimitExceeded",
					"Message": "Too many authentication attempts. Please try again later."
				}
			]
		}
	}`, message)

	w.Write([]byte(errorResponse))
}

// sendUnauthorizedResponse sends an HTTP 401 Unauthorized response.
// It provides a standardized error response for authentication failures.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (m *EnhancedMiddleware) sendUnauthorizedResponse(w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="KubeVirt Redfish API"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	errorResponse := fmt.Sprintf(`{
		"error": {
			"code": "Base.1.0.GeneralError",
			"message": "%s"
		}
	}`, message)

	w.Write([]byte(errorResponse))
}

// sendForbiddenResponse sends an HTTP 403 Forbidden response.
// It provides a standardized error response for authorization failures.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (m *EnhancedMiddleware) sendForbiddenResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)

	errorResponse := fmt.Sprintf(`{
		"error": {
			"code": "Base.1.0.GeneralError",
			"message": "%s"
		}
	}`, message)

	w.Write([]byte(errorResponse))
}

// extractChassisFromPath extracts the chassis name from the request path.
// It parses Redfish API paths to identify which chassis is being accessed.
//
// Parameters:
// - path: HTTP request path
//
// Returns:
// - string: Chassis name extracted from path, or empty string if not found
func (m *EnhancedMiddleware) extractChassisFromPath(path string) string {
	// Parse path like /redfish/v1/Chassis/{chassis-name}/Systems
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "Chassis" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// hasChassisAccess checks if a user has access to a specific chassis.
// It validates user permissions against the requested chassis.
//
// Parameters:
// - user: Authenticated user information
// - chassis: Chassis name to check access for
//
// Returns:
// - bool: True if user has access to the chassis, false otherwise
func (m *EnhancedMiddleware) hasChassisAccess(user *User, chassis string) bool {
	// If no specific chassis requested, check if user has any chassis access
	if chassis == "*" {
		return len(user.Chassis) > 0
	}

	// Check if user has access to the specific chassis
	for _, userChassis := range user.Chassis {
		if userChassis == chassis {
			return true
		}
	}

	return false
}

// GetSecurityMetrics returns current security metrics for monitoring.
// It provides insights into authentication patterns and security incidents.
//
// Returns:
// - SecurityMetrics: Current security metrics
func (m *EnhancedMiddleware) GetSecurityMetrics() SecurityMetrics {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.metrics
}

// GetSecurityEvents returns recent security events for audit purposes.
// It provides access to the security audit trail.
//
// Parameters:
// - limit: Maximum number of events to return
//
// Returns:
// - []SecurityEvent: Recent security events
func (m *EnhancedMiddleware) GetSecurityEvents(limit int) []SecurityEvent {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if limit <= 0 || limit > len(m.securityEvents) {
		limit = len(m.securityEvents)
	}

	// Return the most recent events
	start := len(m.securityEvents) - limit
	if start < 0 {
		start = 0
	}

	events := make([]SecurityEvent, limit)
	copy(events, m.securityEvents[start:])

	return events
}

// GetRateLimitInfo returns rate limiting information for monitoring.
// It provides insights into rate limiting patterns and blocked IPs.
//
// Returns:
// - map[string]*RateLimitInfo: Rate limiting information by IP
func (m *EnhancedMiddleware) GetRateLimitInfo() map[string]*RateLimitInfo {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Create a copy to avoid race conditions
	info := make(map[string]*RateLimitInfo)
	for ip, rateInfo := range m.rateLimits {
		info[ip] = &RateLimitInfo{
			Attempts:     rateInfo.Attempts,
			LastAttempt:  rateInfo.LastAttempt,
			BlockedUntil: rateInfo.BlockedUntil,
			Failures:     rateInfo.Failures,
		}
	}

	return info
}

// ResetRateLimits clears all rate limiting information.
// This is useful for testing or manual intervention.
func (m *EnhancedMiddleware) ResetRateLimits() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.rateLimits = make(map[string]*RateLimitInfo)
	m.userRateLimits = make(map[string]*RateLimitInfo)

	logger.Info("Rate limits have been reset")
}

// SetRateLimitConfig updates rate limiting configuration.
// It allows dynamic adjustment of rate limiting parameters.
//
// Parameters:
// - window: Rate limit window duration
// - maxAttempts: Maximum attempts per window
// - blockDuration: Block duration after max attempts
func (m *EnhancedMiddleware) SetRateLimitConfig(window time.Duration, maxAttempts int, blockDuration time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.rateLimitWindow = window
	m.maxAttempts = maxAttempts
	m.blockDuration = blockDuration

	logger.Info("Rate limit configuration updated: window=%v, max_attempts=%d, block_duration=%v",
		window, maxAttempts, blockDuration)
}
