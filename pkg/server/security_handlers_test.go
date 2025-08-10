package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/auth"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
)

// Test helper function to create a test config
func createTestConfig() *config.Config {
	return &config.Config{
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
}

// Test helper function to create test handlers
func createTestHandlers() *SecurityHandlers {
	cfg := createTestConfig()
	enhancedAuth := auth.NewEnhancedMiddleware(cfg)
	return NewSecurityHandlers(enhancedAuth)
}

func TestNewSecurityHandlers(t *testing.T) {
	cfg := createTestConfig()
	enhancedAuth := auth.NewEnhancedMiddleware(cfg)
	handlers := NewSecurityHandlers(enhancedAuth)

	if handlers == nil {
		t.Fatal("NewSecurityHandlers should not return nil")
	}

	if handlers.enhancedAuth == nil {
		t.Error("EnhancedAuth should be initialized")
	}

	if handlers.enhancedAuth != enhancedAuth {
		t.Error("EnhancedAuth should match the provided instance")
	}
}

func TestSecurityHandlers_RegisterSecurityHandlers(t *testing.T) {
	handlers := createTestHandlers()
	mux := http.NewServeMux()
	handlers.RegisterSecurityHandlers(mux)

	// Test that all expected endpoints are registered
	testEndpoints := []string{
		"/internal/security/metrics",
		"/internal/security/audit",
		"/internal/security/rate-limits",
		"/internal/security/events",
		"/internal/security/health",
	}

	for _, endpoint := range testEndpoints {
		req := httptest.NewRequest("GET", endpoint, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		// Should not return 404 (endpoint not found)
		if w.Code == http.StatusNotFound {
			t.Errorf("Endpoint %s should be registered", endpoint)
		}
	}
}

func TestSecurityHandlers_HandleSecurityMetrics(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request
	req := httptest.NewRequest("GET", "/internal/security/metrics", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response headers
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	if w.Header().Get("Cache-Control") != "no-cache, no-store, must-revalidate" {
		t.Error("Cache-Control should be set correctly")
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["timestamp"] == nil {
		t.Error("Response should contain timestamp")
	}

	if response["metrics"] == nil {
		t.Error("Response should contain metrics")
	}

	if response["calculated"] == nil {
		t.Error("Response should contain calculated metrics")
	}

	if response["summary"] == nil {
		t.Error("Response should contain summary")
	}

	// Test POST request (should fail)
	req = httptest.NewRequest("POST", "/internal/security/metrics", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityMetrics(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestSecurityHandlers_HandleSecurityAudit(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request with default limit
	req := httptest.NewRequest("GET", "/internal/security/audit", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityAudit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response headers
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["timestamp"] == nil {
		t.Error("Response should contain timestamp")
	}

	if response["limit"] == nil {
		t.Error("Response should contain limit")
	}

	if response["count"] == nil {
		t.Error("Response should contain count")
	}

	if response["events"] == nil {
		t.Error("Response should contain events")
	}

	if response["summary"] == nil {
		t.Error("Response should contain summary")
	}

	// Test GET request with custom limit
	req = httptest.NewRequest("GET", "/internal/security/audit?limit=50", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityAudit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Parse response with custom limit
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["limit"] != float64(50) {
		t.Errorf("Expected limit 50, got %v", response["limit"])
	}

	// Test GET request with invalid limit
	req = httptest.NewRequest("GET", "/internal/security/audit?limit=invalid", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityAudit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Should use default limit for invalid input
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["limit"] != float64(100) {
		t.Errorf("Expected default limit 100, got %v", response["limit"])
	}

	// Test POST request (should fail)
	req = httptest.NewRequest("POST", "/internal/security/audit", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityAudit(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestSecurityHandlers_HandleRateLimits(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request
	req := httptest.NewRequest("GET", "/internal/security/rate-limits", nil)
	w := httptest.NewRecorder()

	handlers.handleRateLimits(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response headers
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["timestamp"] == nil {
		t.Error("Response should contain timestamp")
	}

	if response["rate_limits"] == nil {
		t.Error("Response should contain rate_limits")
	}

	if response["summary"] == nil {
		t.Error("Response should contain summary")
	}

	if response["configuration"] == nil {
		t.Error("Response should contain configuration")
	}

	// Verify summary structure
	summary := response["summary"].(map[string]interface{})
	if summary["total_ips"] == nil {
		t.Error("Summary should contain total_ips")
	}

	if summary["total_attempts"] == nil {
		t.Error("Summary should contain total_attempts")
	}

	if summary["total_blocked"] == nil {
		t.Error("Summary should contain total_blocked")
	}

	if summary["active_blocks"] == nil {
		t.Error("Summary should contain active_blocks")
	}

	// Test POST request (should fail)
	req = httptest.NewRequest("POST", "/internal/security/rate-limits", nil)
	w = httptest.NewRecorder()

	handlers.handleRateLimits(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestSecurityHandlers_HandleSecurityEvents(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request with JSON format (default)
	req := httptest.NewRequest("GET", "/internal/security/events", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response headers
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	if w.Header().Get("Content-Disposition") != "attachment; filename=\"security-events.json\"" {
		t.Error("Content-Disposition should be set correctly")
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["export_timestamp"] == nil {
		t.Error("Response should contain export_timestamp")
	}

	if response["format"] != "json" {
		t.Error("Format should be json")
	}

	if response["event_count"] == nil {
		t.Error("Response should contain event_count")
	}

	if response["events"] == nil {
		t.Error("Response should contain events")
	}

	// Test GET request with explicit JSON format
	req = httptest.NewRequest("GET", "/internal/security/events?format=json", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Test GET request with custom limit
	req = httptest.NewRequest("GET", "/internal/security/events?format=json&limit=50", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Test POST request (should fail)
	req = httptest.NewRequest("POST", "/internal/security/events", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestSecurityHandlers_HandleSecurityEvents_CSV(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request with CSV format
	req := httptest.NewRequest("GET", "/internal/security/events?format=csv", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response headers
	if w.Header().Get("Content-Type") != "text/csv" {
		t.Error("Content-Type should be text/csv")
	}

	if w.Header().Get("Content-Disposition") != "attachment; filename=\"security-events.csv\"" {
		t.Error("Content-Disposition should be set correctly")
	}

	// Verify CSV content
	body := w.Body.String()
	if !strings.Contains(body, "Timestamp,EventType,Username,IPAddress,UserAgent,Path,Method,Status,CorrelationID,Details") {
		t.Error("CSV should contain header row")
	}

	// Test GET request with CSV format and custom limit
	req = httptest.NewRequest("GET", "/internal/security/events?format=csv&limit=50", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}
}

func TestSecurityHandlers_HandleSecurityEvents_InvalidFormat(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request with invalid format
	req := httptest.NewRequest("GET", "/internal/security/events?format=xml", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Unsupported format") {
		t.Error("Response should contain error message about unsupported format")
	}
}

func TestSecurityHandlers_HandleSecurityEvents_InvalidLimit(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request with invalid limit
	req := httptest.NewRequest("GET", "/internal/security/events?format=json&limit=invalid", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityEvents(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Should use default limit for invalid input
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// The response should still be valid JSON
	if response["format"] != "json" {
		t.Error("Format should still be json")
	}
}

func TestSecurityHandlers_HandleSecurityHealth(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request
	req := httptest.NewRequest("GET", "/internal/security/health", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Verify response headers
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("Content-Type should be application/json")
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response structure
	if response["timestamp"] == nil {
		t.Error("Response should contain timestamp")
	}

	if response["status"] == nil {
		t.Error("Response should contain status")
	}

	if response["components"] == nil {
		t.Error("Response should contain components")
	}

	if response["summary"] == nil {
		t.Error("Response should contain summary")
	}

	// Verify components structure
	components := response["components"].(map[string]interface{})
	if components["authentication"] == nil {
		t.Error("Components should contain authentication")
	}

	if components["rate_limiting"] == nil {
		t.Error("Components should contain rate_limiting")
	}

	if components["audit_logging"] == nil {
		t.Error("Components should contain audit_logging")
	}

	// Verify summary structure
	summary := response["summary"].(map[string]interface{})
	if summary["overall_health"] == nil {
		t.Error("Summary should contain overall_health")
	}

	if summary["last_check"] == nil {
		t.Error("Summary should contain last_check")
	}

	// Test POST request (should fail)
	req = httptest.NewRequest("POST", "/internal/security/health", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}
}

func TestSecurityHandlers_HandleSecurityHealth_Unhealthy(t *testing.T) {
	handlers := createTestHandlers()

	// Test GET request
	req := httptest.NewRequest("GET", "/internal/security/health", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Parse response
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify status is one of the expected values
	status := response["status"].(string)
	validStatuses := []string{"healthy", "degraded", "unhealthy"}
	found := false
	for _, validStatus := range validStatuses {
		if status == validStatus {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Status should be one of %v, got %s", validStatuses, status)
	}
}

func TestSecurityHandlers_ConcurrentAccess(t *testing.T) {
	handlers := createTestHandlers()

	// Test concurrent access to all endpoints
	endpoints := []string{
		"/internal/security/metrics",
		"/internal/security/audit",
		"/internal/security/rate-limits",
		"/internal/security/events",
		"/internal/security/health",
	}

	var wg sync.WaitGroup
	numGoroutines := 5

	for _, endpoint := range endpoints {
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(ep string) {
				defer wg.Done()
				req := httptest.NewRequest("GET", ep, nil)
				w := httptest.NewRecorder()

				switch ep {
				case "/internal/security/metrics":
					handlers.handleSecurityMetrics(w, req)
				case "/internal/security/audit":
					handlers.handleSecurityAudit(w, req)
				case "/internal/security/rate-limits":
					handlers.handleRateLimits(w, req)
				case "/internal/security/events":
					handlers.handleSecurityEvents(w, req)
				case "/internal/security/health":
					handlers.handleSecurityHealth(w, req)
				}

				if w.Code != http.StatusOK && w.Code != http.StatusBadRequest && w.Code != http.StatusInternalServerError {
					t.Errorf("Expected status code 200, 400, or 500, got %d for endpoint %s", w.Code, ep)
				}
			}(endpoint)
		}
	}

	wg.Wait()
}

func TestSecurityHandlers_EdgeCases(t *testing.T) {
	handlers := createTestHandlers()

	// Test with non-GET request method
	req := httptest.NewRequest("POST", "/test", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityMetrics(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status code %d, got %d", http.StatusMethodNotAllowed, w.Code)
	}

	// Test with very large limit values
	req = httptest.NewRequest("GET", "/internal/security/audit?limit=999999", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityAudit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Test with negative limit values
	req = httptest.NewRequest("GET", "/internal/security/audit?limit=-1", nil)
	w = httptest.NewRecorder()

	handlers.handleSecurityAudit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	// Should use default limit for negative values
	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if response["limit"] != float64(100) {
		t.Errorf("Expected default limit 100, got %v", response["limit"])
	}
}

func TestSecurityHandlers_ResponseStructure(t *testing.T) {
	handlers := createTestHandlers()

	// Test metrics response structure
	req := httptest.NewRequest("GET", "/internal/security/metrics", nil)
	w := httptest.NewRecorder()

	handlers.handleSecurityMetrics(w, req)

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify calculated metrics
	calculated := response["calculated"].(map[string]interface{})
	if calculated["success_rate"] == nil {
		t.Error("Calculated metrics should contain success_rate")
	}

	if calculated["failure_rate"] == nil {
		t.Error("Calculated metrics should contain failure_rate")
	}

	if calculated["block_rate"] == nil {
		t.Error("Calculated metrics should contain block_rate")
	}

	if calculated["rate_limit_rate"] == nil {
		t.Error("Calculated metrics should contain rate_limit_rate")
	}

	// Verify summary metrics
	summary := response["summary"].(map[string]interface{})
	if summary["total_requests"] == nil {
		t.Error("Summary should contain total_requests")
	}

	if summary["successful_logins"] == nil {
		t.Error("Summary should contain successful_logins")
	}

	if summary["failed_logins"] == nil {
		t.Error("Summary should contain failed_logins")
	}

	if summary["blocked_attempts"] == nil {
		t.Error("Summary should contain blocked_attempts")
	}

	if summary["rate_limit_hits"] == nil {
		t.Error("Summary should contain rate_limit_hits")
	}

	if summary["security_incidents"] == nil {
		t.Error("Summary should contain security_incidents")
	}
}

func TestSecurityHandlers_ErrorConditions(t *testing.T) {
	handlers := createTestHandlers()

	// Test all HTTP methods that should be rejected
	methods := []string{"POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	endpoints := []string{
		"/internal/security/metrics",
		"/internal/security/audit",
		"/internal/security/rate-limits",
		"/internal/security/events",
		"/internal/security/health",
	}

	for _, method := range methods {
		for _, endpoint := range endpoints {
			t.Run(fmt.Sprintf("method_not_allowed_%s_%s", method, endpoint), func(t *testing.T) {
				req := httptest.NewRequest(method, endpoint, nil)
				w := httptest.NewRecorder()

				switch endpoint {
				case "/internal/security/metrics":
					handlers.handleSecurityMetrics(w, req)
				case "/internal/security/audit":
					handlers.handleSecurityAudit(w, req)
				case "/internal/security/rate-limits":
					handlers.handleRateLimits(w, req)
				case "/internal/security/events":
					handlers.handleSecurityEvents(w, req)
				case "/internal/security/health":
					handlers.handleSecurityHealth(w, req)
				}

				if w.Code != http.StatusMethodNotAllowed {
					t.Errorf("Expected status code %d for %s %s, got %d",
						http.StatusMethodNotAllowed, method, endpoint, w.Code)
				}
			})
		}
	}
}

func TestSecurityHandlers_QueryParameterEdgeCases(t *testing.T) {
	handlers := createTestHandlers()

	// Test audit endpoint with various limit values
	testCases := []struct {
		name     string
		query    string
		expected int
	}{
		{"zero limit", "limit=0", 100},      // Should use default
		{"negative limit", "limit=-5", 100}, // Should use default
		{"very large limit", "limit=999999", 999999},
		{"float limit", "limit=50.5", 100}, // Should use default
		{"text limit", "limit=abc", 100},   // Should use default
		{"empty limit", "limit=", 100},     // Should use default
		{"no limit param", "", 100},        // Should use default
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/internal/security/audit"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			handlers.handleSecurityAudit(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
			}

			var response map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			if response["limit"] != float64(tc.expected) {
				t.Errorf("Expected limit %d, got %v", tc.expected, response["limit"])
			}
		})
	}

	// Test events endpoint with various format and limit combinations
	eventTestCases := []struct {
		name           string
		query          string
		expectedFormat string
		expectedLimit  int
		expectedStatus int
	}{
		{"default format", "", "json", 1000, http.StatusOK},
		{"explicit json", "format=json", "json", 1000, http.StatusOK},
		{"explicit csv", "format=csv", "csv", 1000, http.StatusOK},
		{"json with limit", "format=json&limit=50", "json", 50, http.StatusOK},
		{"csv with limit", "format=csv&limit=200", "csv", 200, http.StatusOK},
		{"limit only", "limit=75", "json", 75, http.StatusOK},
		{"invalid format", "format=xml", "xml", 1000, http.StatusBadRequest},
		{"zero limit", "limit=0", "json", 1000, http.StatusOK},
		{"negative limit", "limit=-1", "json", 1000, http.StatusOK},
	}

	for _, tc := range eventTestCases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/internal/security/events"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			handlers.handleSecurityEvents(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("Expected status code %d, got %d", tc.expectedStatus, w.Code)
			}

			if tc.expectedStatus == http.StatusBadRequest {
				body := w.Body.String()
				if !strings.Contains(body, "Unsupported format") {
					t.Error("Response should contain error message about unsupported format")
				}
				return
			}

			if tc.expectedFormat == "json" {
				var response map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("Failed to parse response: %v", err)
				}

				if response["format"] != tc.expectedFormat {
					t.Errorf("Expected format %s, got %v", tc.expectedFormat, response["format"])
				}

				if response["event_count"] != float64(0) { // No events in test
					t.Errorf("Expected event_count 0, got %v", response["event_count"])
				}
			} else if tc.expectedFormat == "csv" {
				contentType := w.Header().Get("Content-Type")
				if contentType != "text/csv" {
					t.Errorf("Expected Content-Type text/csv, got %s", contentType)
				}

				body := w.Body.String()
				if !strings.Contains(body, "Timestamp,EventType,Username,IPAddress,UserAgent,Path,Method,Status,CorrelationID,Details") {
					t.Error("CSV should contain header row")
				}
			}
		})
	}
}

func TestSecurityHandlers_ResponseHeaders(t *testing.T) {
	handlers := createTestHandlers()

	// Test all endpoints to verify response headers
	endpoints := []struct {
		name                 string
		path                 string
		query                string
		expectedContentType  string
		expectedCacheControl string
	}{
		{"metrics", "/internal/security/metrics", "", "application/json", "no-cache, no-store, must-revalidate"},
		{"audit", "/internal/security/audit", "", "application/json", "no-cache, no-store, must-revalidate"},
		{"rate-limits", "/internal/security/rate-limits", "", "application/json", "no-cache, no-store, must-revalidate"},
		{"events-json", "/internal/security/events", "format=json", "application/json", ""},
		{"events-csv", "/internal/security/events", "format=csv", "text/csv", ""},
		{"health", "/internal/security/health", "", "application/json", "no-cache, no-store, must-revalidate"},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			url := endpoint.path
			if endpoint.query != "" {
				url += "?" + endpoint.query
			}
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			switch endpoint.path {
			case "/internal/security/metrics":
				handlers.handleSecurityMetrics(w, req)
			case "/internal/security/audit":
				handlers.handleSecurityAudit(w, req)
			case "/internal/security/rate-limits":
				handlers.handleRateLimits(w, req)
			case "/internal/security/events":
				handlers.handleSecurityEvents(w, req)
			case "/internal/security/health":
				handlers.handleSecurityHealth(w, req)
			}

			if w.Code != http.StatusOK && w.Code != http.StatusBadRequest {
				t.Errorf("Expected status code 200 or 400, got %d", w.Code)
			}

			// Verify Content-Type
			contentType := w.Header().Get("Content-Type")
			if contentType != endpoint.expectedContentType {
				t.Errorf("Expected Content-Type %s, got %s", endpoint.expectedContentType, contentType)
			}

			// Verify Cache-Control for endpoints that should have it
			if endpoint.expectedCacheControl != "" {
				cacheControl := w.Header().Get("Cache-Control")
				if cacheControl != endpoint.expectedCacheControl {
					t.Errorf("Expected Cache-Control %s, got %s", endpoint.expectedCacheControl, cacheControl)
				}

				// Verify additional cache headers
				pragma := w.Header().Get("Pragma")
				if pragma != "no-cache" {
					t.Errorf("Expected Pragma no-cache, got %s", pragma)
				}

				expires := w.Header().Get("Expires")
				if expires != "0" {
					t.Errorf("Expected Expires 0, got %s", expires)
				}
			}
		})
	}
}
