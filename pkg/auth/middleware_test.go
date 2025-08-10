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

package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
)

func TestNewMiddleware(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "testuser",
					Password: "testpass",
					Chassis:  []string{"test-chassis"},
				},
			},
		},
	}

	middleware := NewMiddleware(cfg)
	if middleware == nil {
		t.Error("Expected middleware to be created")
	}
}

func TestExtractChassisFromPath(t *testing.T) {
	middleware := &Middleware{}

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "valid chassis path",
			path:     "/redfish/v1/Chassis/test-chassis/Systems",
			expected: "test-chassis",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := middleware.extractChassisFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestExtractAndValidateCredentials(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "testuser",
					Password: "testpass",
					Chassis:  []string{"test-chassis"},
				},
			},
		},
	}

	middleware := &Middleware{config: cfg}

	tests := []struct {
		name        string
		credentials string
		wantErr     bool
	}{
		{
			name:        "valid credentials",
			credentials: "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass")),
			wantErr:     false,
		},
		{
			name:        "invalid username",
			credentials: "Basic " + base64.StdEncoding.EncodeToString([]byte("wronguser:testpass")),
			wantErr:     true,
		},
		{
			name:        "invalid password",
			credentials: "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:wrongpass")),
			wantErr:     true,
		},
		{
			name:        "no authorization header",
			credentials: "",
			wantErr:     true,
		},
		{
			name:        "invalid auth method",
			credentials: "Bearer token",
			wantErr:     true,
		},
		{
			name:        "invalid base64",
			credentials: "Basic invalid-base64",
			wantErr:     true,
		},
		{
			name:        "malformed credentials",
			credentials: "Basic " + base64.StdEncoding.EncodeToString([]byte("justusername")),
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.credentials != "" {
				req.Header.Set("Authorization", tt.credentials)
			}

			_, err := middleware.extractAndValidateCredentials(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractAndValidateCredentials() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHasChassisAccess(t *testing.T) {
	middleware := &Middleware{}

	user := &User{
		Username: "testuser",
		Password: "testpass",
		Chassis:  []string{"chassis1", "chassis2"},
	}

	tests := []struct {
		name    string
		chassis string
		want    bool
	}{
		{
			name:    "has access to specific chassis",
			chassis: "chassis1",
			want:    true,
		},
		{
			name:    "has access to another chassis",
			chassis: "chassis2",
			want:    true,
		},
		{
			name:    "no access to chassis",
			chassis: "chassis3",
			want:    false,
		},
		{
			name:    "wildcard access",
			chassis: "*",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := middleware.hasChassisAccess(user, tt.chassis)
			if result != tt.want {
				t.Errorf("hasChassisAccess() = %v, want %v", result, tt.want)
			}
		})
	}
}

func TestAuthenticateMiddleware(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "testuser",
					Password: "testpass",
					Chassis:  []string{"test-chassis"},
				},
			},
		},
	}

	middleware := NewMiddleware(cfg)

	// Test handler that checks authentication context
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCtx := GetAuthContext(r)
		if authCtx == nil {
			http.Error(w, "No auth context", http.StatusInternalServerError)
			return
		}
		if authCtx.User.Username != "testuser" {
			http.Error(w, "Wrong user", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authenticated"))
	})

	tests := []struct {
		name           string
		authHeader     string
		path           string
		expectedStatus int
	}{
		{
			name:           "valid authentication",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass")),
			path:           "/redfish/v1/Chassis/test-chassis/Systems",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "invalid credentials",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:wrongpass")),
			path:           "/redfish/v1/Chassis/test-chassis/Systems",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "no authorization header",
			authHeader:     "",
			path:           "/redfish/v1/Chassis/test-chassis/Systems",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "access denied to chassis",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass")),
			path:           "/redfish/v1/Chassis/other-chassis/Systems",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()
			authenticatedHandler := middleware.Authenticate(testHandler)
			authenticatedHandler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestGetAuthContext(t *testing.T) {
	// Test with no auth context
	req := httptest.NewRequest("GET", "/test", nil)
	authCtx := GetAuthContext(req)
	if authCtx != nil {
		t.Error("Expected nil auth context when none is set")
	}
}

func TestGetUser(t *testing.T) {
	// Test with no auth context
	req := httptest.NewRequest("GET", "/test", nil)
	user := GetUser(req)
	if user != nil {
		t.Error("Expected nil user when no auth context is set")
	}
}

func TestGetChassis(t *testing.T) {
	// Test with no auth context
	req := httptest.NewRequest("GET", "/test", nil)
	chassis := GetChassis(req)
	if chassis != "" {
		t.Errorf("Expected empty chassis, got %s", chassis)
	}
}

func TestHasChassisAccessFromRequest(t *testing.T) {
	// Test with no auth context
	req := httptest.NewRequest("GET", "/test", nil)
	hasAccess := HasChassisAccess(req, "test-chassis")
	if hasAccess {
		t.Error("Expected no access when no auth context is set")
	}
}

func TestSendUnauthorizedResponse(t *testing.T) {
	middleware := &Middleware{}

	w := httptest.NewRecorder()
	middleware.sendUnauthorizedResponse(w, "Test error")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	authHeader := w.Header().Get("WWW-Authenticate")
	if authHeader != `Basic realm="KubeVirt Redfish API"` {
		t.Errorf("Expected WWW-Authenticate header, got %s", authHeader)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}
}

func TestSendForbiddenResponse(t *testing.T) {
	middleware := &Middleware{}

	w := httptest.NewRecorder()
	middleware.sendForbiddenResponse(w, "Test error")

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected status %d, got %d", http.StatusForbidden, w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, got %s", contentType)
	}
}

func TestMiddlewareWithEmptyConfig(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{},
		},
	}

	middleware := NewMiddleware(cfg)
	if middleware == nil {
		t.Error("Expected middleware to be created even with empty config")
	}

	// Test that any authentication attempt fails
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")))

	w := httptest.NewRecorder()
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	authenticatedHandler := middleware.Authenticate(testHandler)
	authenticatedHandler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected unauthorized status, got %d", w.Code)
	}
}

func TestMiddlewareWithMultipleUsers(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Users: []config.UserConfig{
				{
					Username: "user1",
					Password: "pass1",
					Chassis:  []string{"chassis1"},
				},
				{
					Username: "user2",
					Password: "pass2",
					Chassis:  []string{"chassis2"},
				},
			},
		},
	}

	middleware := NewMiddleware(cfg)

	tests := []struct {
		name           string
		username       string
		password       string
		chassis        string
		expectedStatus int
	}{
		{
			name:           "user1 access to chassis1",
			username:       "user1",
			password:       "pass1",
			chassis:        "chassis1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "user2 access to chassis2",
			username:       "user2",
			password:       "pass2",
			chassis:        "chassis2",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "user1 no access to chassis2",
			username:       "user1",
			password:       "pass1",
			chassis:        "chassis2",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "user2 no access to chassis1",
			username:       "user2",
			password:       "pass2",
			chassis:        "chassis1",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/redfish/v1/Chassis/"+tt.chassis+"/Systems", nil)
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(tt.username+":"+tt.password)))

			w := httptest.NewRecorder()
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			authenticatedHandler := middleware.Authenticate(testHandler)
			authenticatedHandler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}
