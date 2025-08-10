package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
)

func TestNewEnhancedMiddleware(t *testing.T) {
	// Create a minimal config for testing
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "testuser",
					Password: "testpass",
					Chassis:  []string{"chassis1"},
				},
			},
		},
		Chassis: []config.ChassisConfig{
			{
				Name:      "chassis1",
				Namespace: "default",
			},
		},
	}

	// Create enhanced middleware
	middleware := NewEnhancedMiddleware(cfg)

	// Verify middleware was created correctly
	if middleware == nil {
		t.Fatal("NewEnhancedMiddleware should not return nil")
	}

	if middleware.config == nil {
		t.Error("Config should not be nil")
	}

	if middleware.rateLimits == nil {
		t.Error("Rate limits map should be initialized")
	}

	if middleware.userRateLimits == nil {
		t.Error("User rate limits map should be initialized")
	}

	if middleware.securityEvents == nil {
		t.Error("Security events slice should be initialized")
	}

	// Verify default values
	if middleware.maxEvents != 1000 {
		t.Errorf("Expected maxEvents 1000, got %d", middleware.maxEvents)
	}

	if middleware.rateLimitWindow != 5*time.Minute {
		t.Errorf("Expected rateLimitWindow 5m, got %v", middleware.rateLimitWindow)
	}

	if middleware.maxAttempts != 10 {
		t.Errorf("Expected maxAttempts 10, got %d", middleware.maxAttempts)
	}

	if middleware.blockDuration != 15*time.Minute {
		t.Errorf("Expected blockDuration 15m, got %v", middleware.blockDuration)
	}
}

func TestEnhancedMiddleware_MaskPassword(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	testCases := []struct {
		name     string
		password string
		expected string
	}{
		{
			name:     "empty password",
			password: "",
			expected: "********",
		},
		{
			name:     "short password",
			password: "a",
			expected: "********",
		},
		{
			name:     "medium password",
			password: "password123",
			expected: "********",
		},
		{
			name:     "long password",
			password: "verylongpassword123",
			expected: "********",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := middleware.maskPassword(tc.password)
			if result != tc.expected {
				t.Errorf("Expected '%s', got '%s'", tc.expected, result)
			}
		})
	}
}

func TestEnhancedMiddleware_GetClientIP(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Test with X-Forwarded-For header
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.100, 10.0.0.1")

	clientIP := middleware.getClientIP(req)
	if clientIP != "192.168.1.100" {
		t.Errorf("Expected '192.168.1.100', got '%s'", clientIP)
	}

	// Test with X-Real-IP header
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Real-IP", "192.168.1.200")

	clientIP = middleware.getClientIP(req)
	if clientIP != "192.168.1.200" {
		t.Errorf("Expected '192.168.1.200', got '%s'", clientIP)
	}

	// Test with RemoteAddr
	req = httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.300:12345"

	clientIP = middleware.getClientIP(req)
	if clientIP != "192.168.1.300" {
		t.Errorf("Expected '192.168.1.300', got '%s'", clientIP)
	}

	// Test with no headers (should return "unknown")
	req = httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = ""

	clientIP = middleware.getClientIP(req)
	if clientIP != "unknown" {
		t.Errorf("Expected 'unknown', got '%s'", clientIP)
	}
}

func TestEnhancedMiddleware_ExtractChassisFromPath(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	testCases := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "valid chassis path",
			path:     "/redfish/v1/Chassis/chassis1",
			expected: "chassis1",
		},
		{
			name:     "chassis collection path",
			path:     "/redfish/v1/Chassis",
			expected: "",
		},
		{
			name:     "no chassis in path",
			path:     "/redfish/v1/Systems",
			expected: "",
		},
		{
			name:     "empty path",
			path:     "",
			expected: "",
		},
		{
			name:     "root path",
			path:     "/",
			expected: "",
		},
		{
			name:     "chassis with systems path",
			path:     "/redfish/v1/Chassis/chassis1/Systems",
			expected: "chassis1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := middleware.extractChassisFromPath(tc.path)
			if result != tc.expected {
				t.Errorf("Expected '%s', got '%s' for path '%s'", tc.expected, result, tc.path)
			}
		})
	}
}

func TestEnhancedMiddleware_HasChassisAccess(t *testing.T) {
	// Create config with test users
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "user1",
					Password: "pass1",
					Chassis:  []string{"chassis1", "chassis2"},
				},
				{
					Username: "user2",
					Password: "pass2",
					Chassis:  []string{"chassis2"},
				},
				{
					Username: "admin",
					Password: "admin",
					Chassis:  []string{"*"}, // Wildcard access
				},
			},
		},
	}

	middleware := NewEnhancedMiddleware(cfg)

	testCases := []struct {
		name     string
		username string
		chassis  string
		expected bool
	}{
		{
			name:     "user has access to specific chassis",
			username: "user1",
			chassis:  "chassis1",
			expected: true,
		},
		{
			name:     "user has access to another chassis",
			username: "user1",
			chassis:  "chassis2",
			expected: true,
		},
		{
			name:     "user has no access to chassis",
			username: "user1",
			chassis:  "chassis3",
			expected: false,
		},
		{
			name:     "user2 has access to chassis2",
			username: "user2",
			chassis:  "chassis2",
			expected: true,
		},
		{
			name:     "user2 has no access to chassis1",
			username: "user2",
			chassis:  "chassis1",
			expected: false,
		},
		{
			name:     "admin has wildcard access",
			username: "admin",
			chassis:  "*",
			expected: true,
		},
		{
			name:     "unknown user has no access",
			username: "unknown",
			chassis:  "chassis1",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create user with appropriate chassis access based on test case
			var user *User
			switch tc.username {
			case "user1":
				user = &User{Username: tc.username, Chassis: []string{"chassis1", "chassis2"}}
			case "user2":
				user = &User{Username: tc.username, Chassis: []string{"chassis2"}}
			case "admin":
				user = &User{Username: tc.username, Chassis: []string{"*"}}
			default:
				user = &User{Username: tc.username, Chassis: []string{}}
			}

			result := middleware.hasChassisAccess(user, tc.chassis)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v for user '%s' and chassis '%s'", tc.expected, result, tc.username, tc.chassis)
			}
		})
	}
}

func TestEnhancedMiddleware_GetSecurityMetrics(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Initially metrics should be zero
	metrics := middleware.GetSecurityMetrics()
	if metrics.TotalAttempts != 0 {
		t.Errorf("Expected TotalAttempts 0, got %d", metrics.TotalAttempts)
	}
	if metrics.SuccessfulLogins != 0 {
		t.Errorf("Expected SuccessfulLogins 0, got %d", metrics.SuccessfulLogins)
	}
	if metrics.FailedLogins != 0 {
		t.Errorf("Expected FailedLogins 0, got %d", metrics.FailedLogins)
	}
	if metrics.BlockedAttempts != 0 {
		t.Errorf("Expected BlockedAttempts 0, got %d", metrics.BlockedAttempts)
	}
	if metrics.RateLimitHits != 0 {
		t.Errorf("Expected RateLimitHits 0, got %d", metrics.RateLimitHits)
	}
	if metrics.SecurityIncidents != 0 {
		t.Errorf("Expected SecurityIncidents 0, got %d", metrics.SecurityIncidents)
	}
}

func TestEnhancedMiddleware_GetSecurityEvents(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Initially should have no events
	events := middleware.GetSecurityEvents(10)
	if len(events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(events))
	}

	// Test with limit parameter
	events = middleware.GetSecurityEvents(5)
	if len(events) != 0 {
		t.Errorf("Expected 0 events with limit 5, got %d", len(events))
	}
}

func TestEnhancedMiddleware_GetRateLimitInfo(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Initially should have no rate limit info
	rateLimits := middleware.GetRateLimitInfo()
	if len(rateLimits) != 0 {
		t.Errorf("Expected 0 rate limits, got %d", len(rateLimits))
	}
}

func TestEnhancedMiddleware_ResetRateLimits(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Add some rate limit data
	middleware.rateLimits["192.168.1.1"] = &RateLimitInfo{
		Attempts: 5,
		Failures: 3,
	}
	middleware.userRateLimits["testuser"] = &RateLimitInfo{
		Attempts: 2,
		Failures: 1,
	}

	// Verify data was added
	if len(middleware.rateLimits) != 1 {
		t.Errorf("Expected 1 rate limit, got %d", len(middleware.rateLimits))
	}
	if len(middleware.userRateLimits) != 1 {
		t.Errorf("Expected 1 user rate limit, got %d", len(middleware.userRateLimits))
	}

	// Reset rate limits
	middleware.ResetRateLimits()

	// Verify data was cleared
	if len(middleware.rateLimits) != 0 {
		t.Errorf("Expected 0 rate limits after reset, got %d", len(middleware.rateLimits))
	}
	if len(middleware.userRateLimits) != 0 {
		t.Errorf("Expected 0 user rate limits after reset, got %d", len(middleware.userRateLimits))
	}
}

func TestEnhancedMiddleware_SetRateLimitConfig(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Test default values
	if middleware.rateLimitWindow != 5*time.Minute {
		t.Errorf("Expected default rateLimitWindow 5m, got %v", middleware.rateLimitWindow)
	}
	if middleware.maxAttempts != 10 {
		t.Errorf("Expected default maxAttempts 10, got %d", middleware.maxAttempts)
	}
	if middleware.blockDuration != 15*time.Minute {
		t.Errorf("Expected default blockDuration 15m, got %v", middleware.blockDuration)
	}

	// Set new values
	newWindow := 10 * time.Minute
	newMaxAttempts := 20
	newBlockDuration := 30 * time.Minute

	middleware.SetRateLimitConfig(newWindow, newMaxAttempts, newBlockDuration)

	// Verify new values
	if middleware.rateLimitWindow != newWindow {
		t.Errorf("Expected rateLimitWindow %v, got %v", newWindow, middleware.rateLimitWindow)
	}
	if middleware.maxAttempts != newMaxAttempts {
		t.Errorf("Expected maxAttempts %d, got %d", newMaxAttempts, middleware.maxAttempts)
	}
	if middleware.blockDuration != newBlockDuration {
		t.Errorf("Expected blockDuration %v, got %v", newBlockDuration, middleware.blockDuration)
	}
}

func TestEnhancedMiddleware_SendUnauthorizedResponse(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Create a test response writer
	w := httptest.NewRecorder()

	// Send unauthorized response
	middleware.sendUnauthorizedResponse(w, "Test unauthorized message")

	// Verify response
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, w.Code)
	}

	// Verify response body contains the message
	body := w.Body.String()
	if body == "" {
		t.Error("Response body should not be empty")
	}
}

func TestEnhancedMiddleware_SendForbiddenResponse(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Create a test response writer
	w := httptest.NewRecorder()

	// Send forbidden response
	middleware.sendForbiddenResponse(w, "Test forbidden message")

	// Verify response
	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status code %d, got %d", http.StatusForbidden, w.Code)
	}

	// Verify response body contains the message
	body := w.Body.String()
	if body == "" {
		t.Error("Response body should not be empty")
	}
}

func TestEnhancedMiddleware_SendRateLimitResponse(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Create a test response writer
	w := httptest.NewRecorder()

	// Send rate limit response
	middleware.sendRateLimitResponse(w, "Test rate limit message")

	// Verify response
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected status code %d, got %d", http.StatusTooManyRequests, w.Code)
	}

	// Verify response body contains the message
	body := w.Body.String()
	if body == "" {
		t.Error("Response body should not be empty")
	}
}

func TestEnhancedMiddleware_LogSecurityEvent(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Initially should have no events
	if len(middleware.securityEvents) != 0 {
		t.Errorf("Expected 0 security events initially, got %d", len(middleware.securityEvents))
	}

	// Log a security event
	event := SecurityEvent{
		Timestamp: time.Now(),
		EventType: "test_event",
		Username:  "testuser",
		IPAddress: "192.168.1.1",
		Path:      "/test",
		Method:    "GET",
		Status:    "success",
	}

	middleware.logSecurityEvent(event)

	// Verify event was logged
	if len(middleware.securityEvents) != 1 {
		t.Errorf("Expected 1 security event, got %d", len(middleware.securityEvents))
	}

	// Verify event details
	loggedEvent := middleware.securityEvents[0]
	if loggedEvent.EventType != "test_event" {
		t.Errorf("Expected event type 'test_event', got '%s'", loggedEvent.EventType)
	}
	if loggedEvent.Username != "testuser" {
		t.Errorf("Expected username 'testuser', got '%s'", loggedEvent.Username)
	}
	if loggedEvent.IPAddress != "192.168.1.1" {
		t.Errorf("Expected IP address '192.168.1.1', got '%s'", loggedEvent.IPAddress)
	}
}

func TestEnhancedMiddleware_IsRateLimited(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Initially should not be rate limited
	if middleware.isRateLimited("192.168.1.1", "test-correlation") {
		t.Error("Should not be rate limited initially")
	}

	// Add rate limit data that would cause blocking
	middleware.rateLimits["192.168.1.1"] = &RateLimitInfo{
		Attempts:     middleware.maxAttempts + 1,
		LastAttempt:  time.Now(),
		BlockedUntil: time.Now().Add(1 * time.Hour), // Blocked for 1 hour
		Failures:     5,
	}

	// Should now be rate limited
	if !middleware.isRateLimited("192.168.1.1", "test-correlation") {
		t.Error("Should be rate limited after exceeding attempts")
	}

	// Test with unblocked IP
	if middleware.isRateLimited("192.168.1.2", "test-correlation") {
		t.Error("Should not be rate limited for different IP")
	}
}

func TestEnhancedMiddleware_UpdateRateLimit(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	clientIP := "192.168.1.1"

	// Initially no rate limit data
	if _, exists := middleware.rateLimits[clientIP]; exists {
		t.Error("Should not have rate limit data initially")
	}

	// Update rate limit for successful attempt
	middleware.updateRateLimit(clientIP, true)

	// Verify rate limit data was created
	rateLimit, exists := middleware.rateLimits[clientIP]
	if !exists {
		t.Error("Rate limit data should be created")
	}

	if rateLimit.Attempts != 1 {
		t.Errorf("Expected 1 attempt, got %d", rateLimit.Attempts)
	}

	if rateLimit.Failures != 0 {
		t.Errorf("Expected 0 failures, got %d", rateLimit.Failures)
	}

	// Update rate limit for failed attempt
	middleware.updateRateLimit(clientIP, false)

	// Verify failure was recorded
	rateLimit = middleware.rateLimits[clientIP]
	if rateLimit.Attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", rateLimit.Attempts)
	}

	if rateLimit.Failures != 1 {
		t.Errorf("Expected 1 failure, got %d", rateLimit.Failures)
	}
}

func TestEnhancedMiddleware_Authenticate(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "testuser",
					Password: "testpass",
					Chassis:  []string{"chassis1"},
				},
				{
					Username: "admin",
					Password: "adminpass",
					Chassis:  []string{"chassis1", "chassis2"},
				},
			},
		},
	}

	middleware := NewEnhancedMiddleware(cfg)

	testCases := []struct {
		name           string
		authHeader     string
		path           string
		expectedStatus int
	}{
		{
			name:           "service root access",
			authHeader:     "",
			path:           "/redfish/v1/",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "valid credentials",
			authHeader:     "Basic dGVzdHVzZXI6dGVzdHBhc3M=", // testuser:testpass
			path:           "/redfish/v1/Chassis/chassis1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "invalid credentials",
			authHeader:     "Basic d3Jvbmc6d3Jvbmc=", // wrong:wrong
			path:           "/redfish/v1/Chassis/chassis1",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "no auth header",
			authHeader:     "",
			path:           "/redfish/v1/Chassis/chassis1",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	// Test the Authenticate middleware function
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	authenticatedHandler := middleware.Authenticate(handler)

	authTestCases := []struct {
		name           string
		authHeader     string
		path           string
		expectedStatus int
	}{
		{
			name:           "service root access",
			authHeader:     "",
			path:           "/redfish/v1/",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "valid credentials",
			authHeader:     "Basic dGVzdHVzZXI6dGVzdHBhc3M=", // testuser:testpass
			path:           "/redfish/v1/Chassis/chassis1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "invalid credentials",
			authHeader:     "Basic d3Jvbmc6d3Jvbmc=", // wrong:wrong
			path:           "/redfish/v1/Chassis/chassis1",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "no auth header",
			authHeader:     "",
			path:           "/redfish/v1/Chassis/chassis1",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range authTestCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			w := httptest.NewRecorder()
			authenticatedHandler(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("Expected status %d, got %d", tc.expectedStatus, w.Code)
			}
		})
	}
}

func TestEnhancedMiddleware_ExtractAndValidateCredentialsEnhanced(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "testuser",
					Password: "testpass",
					Chassis:  []string{"chassis1"},
				},
			},
		},
	}

	middleware := NewEnhancedMiddleware(cfg)

	credTestCases := []struct {
		name            string
		authHeader      string
		expectedUser    string
		expectedPass    string
		expectedChassis []string
		shouldSucceed   bool
	}{
		{
			name:            "valid basic auth",
			authHeader:      "Basic dGVzdHVzZXI6dGVzdHBhc3M=", // testuser:testpass
			expectedUser:    "testuser",
			expectedPass:    "testpass",
			expectedChassis: []string{"chassis1"},
			shouldSucceed:   true,
		},
		{
			name:            "invalid basic auth - wrong credentials",
			authHeader:      "Basic d3Jvbmc6d3Jvbmc=", // wrong:wrong
			expectedUser:    "",
			expectedPass:    "",
			expectedChassis: nil,
			shouldSucceed:   false,
		},
		{
			name:            "missing auth header",
			authHeader:      "",
			expectedUser:    "",
			expectedPass:    "",
			expectedChassis: nil,
			shouldSucceed:   false,
		},
		{
			name:            "invalid auth header format",
			authHeader:      "Invalid dGVzdHVzZXI6dGVzdHBhc3M=",
			expectedUser:    "",
			expectedPass:    "",
			expectedChassis: nil,
			shouldSucceed:   false,
		},
		{
			name:            "malformed base64",
			authHeader:      "Basic invalid-base64",
			expectedUser:    "",
			expectedPass:    "",
			expectedChassis: nil,
			shouldSucceed:   false,
		},
		{
			name:            "missing colon in credentials",
			authHeader:      "Basic dGVzdHVzZXI=", // testuser (no colon)
			expectedUser:    "",
			expectedPass:    "",
			expectedChassis: nil,
			shouldSucceed:   false,
		},
	}

	for _, tc := range credTestCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			user, err := middleware.extractAndValidateCredentialsEnhanced(req, "test-correlation-id")

			if tc.shouldSucceed {
				if err != nil {
					t.Errorf("Expected success, got error: %v", err)
				}
				if user == nil {
					t.Error("Expected user, got nil")
				} else {
					if user.Username != tc.expectedUser {
						t.Errorf("Expected user %s, got %s", tc.expectedUser, user.Username)
					}
					if len(user.Chassis) != len(tc.expectedChassis) {
						t.Errorf("Expected %d chassis, got %d", len(tc.expectedChassis), len(user.Chassis))
					}
					for i, expectedChassis := range tc.expectedChassis {
						if i >= len(user.Chassis) || user.Chassis[i] != expectedChassis {
							t.Errorf("Expected chassis[%d] %s, got %s", i, expectedChassis, user.Chassis[i])
						}
					}
				}
			} else {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				if user != nil {
					t.Errorf("Expected nil user, got %v", user)
				}
			}
		})
	}
}

func TestEnhancedMiddleware_CleanupRateLimits(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	// Add some rate limit entries
	middleware.rateLimits["192.168.1.1"] = &RateLimitInfo{
		Attempts:     5,
		LastAttempt:  time.Now().Add(-10 * time.Minute), // Old entry
		BlockedUntil: time.Time{},
		Failures:     0,
	}

	middleware.rateLimits["192.168.1.2"] = &RateLimitInfo{
		Attempts:     3,
		LastAttempt:  time.Now(), // Recent entry
		BlockedUntil: time.Time{},
		Failures:     0,
	}

	middleware.userRateLimits["testuser"] = &RateLimitInfo{
		Attempts:     2,
		LastAttempt:  time.Now().Add(-20 * time.Minute), // Old entry
		BlockedUntil: time.Now().Add(5 * time.Minute),
		Failures:     5,
	}

	// Run cleanup
	middleware.cleanupRateLimits()

	// Check that old entries were removed
	if _, exists := middleware.rateLimits["192.168.1.1"]; exists {
		t.Error("Expected old IP rate limit to be cleaned up")
	}

	if _, exists := middleware.userRateLimits["testuser"]; exists {
		t.Error("Expected old user rate limit to be cleaned up")
	}

	// Check that recent entries remain
	if _, exists := middleware.rateLimits["192.168.1.2"]; !exists {
		t.Error("Expected recent IP rate limit to remain")
	}
}

func TestEnhancedMiddleware_LogSecurityEvent_Enhanced(t *testing.T) {
	middleware := NewEnhancedMiddleware(&config.Config{})

	testCases := []struct {
		name           string
		eventType      string
		username       string
		ip             string
		userAgent      string
		details        map[string]interface{}
		expectedEvents int
	}{
		{
			name:           "login success",
			eventType:      "login_success",
			username:       "testuser",
			ip:             "192.168.1.1",
			userAgent:      "test-agent",
			details:        map[string]interface{}{"chassis": "chassis1"},
			expectedEvents: 1,
		},
		{
			name:           "login failure",
			eventType:      "login_failure",
			username:       "invaliduser",
			ip:             "192.168.1.2",
			userAgent:      "test-agent",
			details:        map[string]interface{}{"reason": "invalid_credentials"},
			expectedEvents: 2,
		},
		{
			name:           "rate limit hit",
			eventType:      "rate_limit_hit",
			username:       "testuser",
			ip:             "192.168.1.3",
			userAgent:      "test-agent",
			details:        map[string]interface{}{"attempts": 10},
			expectedEvents: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			event := SecurityEvent{
				Timestamp:     time.Now(),
				EventType:     tc.eventType,
				Username:      tc.username,
				IPAddress:     tc.ip,
				UserAgent:     tc.userAgent,
				Path:          "/test",
				Method:        "GET",
				Status:        "success",
				Details:       make(map[string]string),
				CorrelationID: "test-correlation-id",
			}

			middleware.logSecurityEvent(event)

			// Check that event was added
			if len(middleware.securityEvents) != tc.expectedEvents {
				t.Errorf("Expected %d events, got %d", tc.expectedEvents, len(middleware.securityEvents))
			}

			// Check the latest event
			latestEvent := middleware.securityEvents[len(middleware.securityEvents)-1]
			if latestEvent.EventType != tc.eventType {
				t.Errorf("Expected event type %s, got %s", tc.eventType, latestEvent.EventType)
			}
			if latestEvent.Username != tc.username {
				t.Errorf("Expected username %s, got %s", tc.username, latestEvent.Username)
			}
			if latestEvent.IPAddress != tc.ip {
				t.Errorf("Expected IP %s, got %s", tc.ip, latestEvent.IPAddress)
			}
			if latestEvent.UserAgent != tc.userAgent {
				t.Errorf("Expected user agent %s, got %s", tc.userAgent, latestEvent.UserAgent)
			}
		})
	}

	// Test event limit enforcement
	for i := 0; i < 1100; i++ {
		event := SecurityEvent{
			Timestamp:     time.Now(),
			EventType:     "test",
			Username:      "user",
			IPAddress:     "ip",
			UserAgent:     "agent",
			Path:          "/test",
			Method:        "GET",
			Status:        "success",
			Details:       make(map[string]string),
			CorrelationID: "test-correlation-id",
		}
		middleware.logSecurityEvent(event)
	}

	if len(middleware.securityEvents) > middleware.maxEvents {
		t.Errorf("Expected events to be limited to %d, got %d", middleware.maxEvents, len(middleware.securityEvents))
	}
}
