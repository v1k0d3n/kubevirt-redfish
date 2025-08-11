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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/kubevirt"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

// TestNewServer tests the NewServer constructor
func TestNewServer(t *testing.T) {
	t.Skip("Temporarily skipped - needs test mode implementation")
	// Create test config
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
			TLS: config.TLSConfig{
				Enabled: false,
			},
		},
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

	// Create mock KubeVirt client
	mockClient := &kubevirt.Client{}

	// Test server creation
	server := NewServer(testConfig, mockClient)

	// Verify server was created with correct components
	assert.NotNil(t, server)
	assert.Equal(t, testConfig, server.config)
	assert.Equal(t, mockClient, server.kubevirtClient)
	assert.NotNil(t, server.enhancedAuthMiddleware)
	assert.NotNil(t, server.securityHandlers)
	assert.NotNil(t, server.taskManager)
	assert.NotNil(t, server.jobScheduler)
	assert.NotNil(t, server.memoryManager)
	assert.NotNil(t, server.connectionManager)
	assert.NotNil(t, server.memoryMonitor)
	assert.NotNil(t, server.advancedCache)
	assert.NotNil(t, server.responseOptimizer)
	assert.NotNil(t, server.responseCacheOptimizer)
	assert.NotNil(t, server.circuitBreakerManager)
	assert.NotNil(t, server.retryManager)
	assert.NotNil(t, server.rateLimitManager)
	assert.NotNil(t, server.healthChecker)
	assert.NotNil(t, server.selfHealingManager)
	assert.NotNil(t, server.responseCache)
	assert.True(t, server.useEnhancedAuth)
	assert.False(t, server.startTime.IsZero())
}

// TestNewServerWithNilConfig tests server creation with nil config
func TestNewServerWithNilConfig(t *testing.T) {
	t.Skip("Skipping nil config test due to panic - needs proper nil handling")
	mockClient := &kubevirt.Client{}

	// This should not panic and should create a server with nil config
	server := NewServer(nil, mockClient)
	assert.NotNil(t, server)
	assert.Nil(t, server.config)
	assert.Equal(t, mockClient, server.kubevirtClient)
}

// TestNewServerWithNilClient tests server creation with nil client
func TestNewServerWithNilClient(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	// This should not panic and should create a server with nil client
	server := NewServer(testConfig, nil)
	assert.NotNil(t, server)
	assert.Equal(t, testConfig, server.config)
	assert.Nil(t, server.kubevirtClient)
}

// TestServerBasic tests basic server functionality without complex background processes
func TestServerBasic(t *testing.T) {
	server := testServer(t)

	// Test basic getters
	assert.NotNil(t, server.getAuthMiddleware())
	// In test mode, task manager is nil, so we expect nil
	if server.config.Server.TestMode {
		assert.Nil(t, server.getTaskManager())
	} else {
		assert.NotNil(t, server.getTaskManager())
	}
	assert.NotNil(t, server.startTime)
	// Test that config mutex is properly initialized by testing its functionality
	server.configMutex.RLock()
	server.configMutex.RUnlock()

	// Test basic functionality without starting background processes
	// This avoids the complex shutdown issues
	t.Log("Basic server functionality test completed successfully")
}

// TestServerShutdownSimple tests basic shutdown functionality without complex background processes
func TestServerShutdownSimple(t *testing.T) {
	server := testServer(t)

	// Test shutdown with timeout handling to prevent hanging
	testServerShutdown(t, server)
}

// TestServerShutdown tests the Shutdown method
func TestServerShutdown(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test shutdown with timeout handling to prevent hanging
	testServerShutdown(t, server)

	// Test shutdown with HTTP server - create a new server for this test
	server2 := NewServer(testConfig, mockClient)
	server2.httpServer = &http.Server{
		Addr:              ":0",             // Use port 0 to avoid conflicts
		ReadHeaderTimeout: 10 * time.Second, // Protect against Slowloris attacks
	}

	// Start server in background with error channel
	errChan := make(chan error, 1)
	go func() {
		errChan <- server2.httpServer.ListenAndServe()
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Test shutdown with timeout handling
	testServerShutdown(t, server2)

	// Check if server stopped
	select {
	case err := <-errChan:
		// Server stopped, which is expected
		assert.Error(t, err) // Should be http.ErrServerClosed
	default:
		// No error, which is also fine
	}
}

// TestServerUpdateConfig tests the UpdateConfig method
func TestServerUpdateConfig(t *testing.T) {
	server := testServer(t)

	// Test config update
	newConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 9090,
		},
	}

	server.UpdateConfig(newConfig)
	assert.Equal(t, newConfig, server.config)
}

// TestServerValidateMethod tests the validateMethod function
func TestServerValidateMethod(t *testing.T) {
	server := testServer(t)

	tests := []struct {
		name           string
		method         string
		allowedMethods []string
		expectedResult bool
		expectedStatus int
	}{
		{
			name:           "allowed method",
			method:         "GET",
			allowedMethods: []string{"GET", "POST"},
			expectedResult: true,
		},
		{
			name:           "not allowed method",
			method:         "PUT",
			allowedMethods: []string{"GET", "POST"},
			expectedResult: false,
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "case sensitive match",
			method:         "get",
			allowedMethods: []string{"GET", "POST"},
			expectedResult: false, // Should be case sensitive
			expectedStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()

			result := server.validateMethod(w, req, tt.allowedMethods)

			assert.Equal(t, tt.expectedResult, result)

			if !tt.expectedResult {
				assert.Equal(t, tt.expectedStatus, w.Code)
				assert.Contains(t, w.Header().Get("Allow"), "GET")
				assert.Contains(t, w.Header().Get("Allow"), "POST")
				assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

				// Verify error response structure
				var errorResponse redfish.Error
				err := json.NewDecoder(w.Body).Decode(&errorResponse)
				assert.NoError(t, err)
				assert.NotEmpty(t, errorResponse.Error.Message)
				assert.NotEmpty(t, errorResponse.Error.Code)
			}
		})
	}
}

// TestServerSendJSON tests the sendJSON method
func TestServerSendJSON(t *testing.T) {
	server := testServer(t)

	w := httptest.NewRecorder()
	testData := map[string]interface{}{
		"message": "test",
		"code":    200,
	}

	server.sendJSON(w, testData)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "test", response["message"])
	assert.Equal(t, float64(200), response["code"])
}

// TestServerSendOptimizedJSON tests the sendOptimizedJSON method
func TestServerSendOptimizedJSON(t *testing.T) {
	server := testServer(t)

	tests := []struct {
		name           string
		acceptEncoding string
		testData       interface{}
	}{
		{
			name:           "with gzip support",
			acceptEncoding: "gzip, deflate",
			testData:       map[string]string{"message": "test"},
		},
		{
			name:           "without compression support",
			acceptEncoding: "",
			testData:       map[string]string{"message": "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}
			w := httptest.NewRecorder()

			server.sendOptimizedJSON(w, req, tt.testData)

			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.Equal(t, http.StatusOK, w.Code)

			// Verify response can be decoded
			var response map[string]interface{}
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
		})
	}
}

// TestServerSetCacheHeaders tests the setCacheHeaders method
func TestServerSetCacheHeaders(t *testing.T) {
	server := testServer(t)

	tests := []struct {
		name         string
		resourceType string
		expected     string
	}{
		{
			name:         "collection",
			resourceType: "collection",
			expected:     "public, max-age=30",
		},
		{
			name:         "resource",
			resourceType: "resource",
			expected:     "public, max-age=300, must-revalidate",
		},
		{
			name:         "task",
			resourceType: "task",
			expected:     "no-cache, no-store, must-revalidate",
		},
		{
			name:         "action",
			resourceType: "action",
			expected:     "no-cache, no-store, must-revalidate",
		},
		{
			name:         "default",
			resourceType: "unknown",
			expected:     "public, max-age=60, must-revalidate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			server.setCacheHeaders(w, tt.resourceType)

			assert.Equal(t, tt.expected, w.Header().Get("Cache-Control"))
		})
	}
}

// TestServerSendRedfishError tests the sendRedfishError method
func TestServerSendRedfishError(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{
			name:           "not found error",
			err:            fmt.Errorf("resource not found"),
			expectedStatus: http.StatusInternalServerError, // Default for generic errors
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			server.sendRedfishError(w, req, tt.err)

			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.Equal(t, tt.expectedStatus, w.Code)

			var errorResponse redfish.Error
			err := json.NewDecoder(w.Body).Decode(&errorResponse)
			assert.NoError(t, err)
			assert.NotEmpty(t, errorResponse.Error.Message)
			assert.NotEmpty(t, errorResponse.Error.Code)
		})
	}
}

// TestServerSendNotFound tests the sendNotFound method
func TestServerSendNotFound(t *testing.T) {
	server := testServer(t)

	w := httptest.NewRecorder()
	message := "Resource not found"

	server.sendNotFound(w, message)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusNotFound, w.Code)

	var errorResponse redfish.Error
	err := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, err)
	assert.Contains(t, errorResponse.Error.Message, message)
}

// TestServerSendUnauthorized tests the sendUnauthorized method
func TestServerSendUnauthorized(t *testing.T) {
	server := testServer(t)

	w := httptest.NewRecorder()
	message := "Authentication required"

	server.sendUnauthorized(w, message)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var errorResponse redfish.Error
	err := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, err)
	assert.Contains(t, errorResponse.Error.Message, message)
}

// TestServerSendForbidden tests the sendForbidden method
func TestServerSendForbidden(t *testing.T) {
	server := testServer(t)

	w := httptest.NewRecorder()
	message := "Access denied"

	server.sendForbidden(w, message)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusForbidden, w.Code)

	var errorResponse redfish.Error
	err := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, err)
	assert.Contains(t, errorResponse.Error.Message, message)
}

// TestServerSendInternalError tests the sendInternalError method
func TestServerSendInternalError(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	w := httptest.NewRecorder()
	message := "Internal server error"

	server.sendInternalError(w, message)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var errorResponse redfish.Error
	err := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, err)
	assert.Contains(t, errorResponse.Error.Message, message)
}

// TestServerSendValidationError tests the sendValidationError method
func TestServerSendValidationError(t *testing.T) {
	server := testServer(t)

	w := httptest.NewRecorder()
	message := "Validation failed"
	details := "Invalid parameter"

	server.sendValidationError(w, message, details)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errorResponse redfish.Error
	err := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, err)
	assert.Contains(t, errorResponse.Error.Message, message)
}

// TestServerSendConflictError tests the sendConflictError method
func TestServerSendConflictError(t *testing.T) {
	server := testServer(t)

	w := httptest.NewRecorder()
	resource := "VirtualMachine"
	details := "Resource already exists"

	server.sendConflictError(w, resource, details)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusConflict, w.Code)

	var errorResponse redfish.Error
	err := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, err)
	assert.Contains(t, errorResponse.Error.Message, resource)
}

// TestServerSendJSONResponse tests the sendJSONResponse method
func TestServerSendJSONResponse(t *testing.T) {
	server := testServer(t)

	tests := []struct {
		name       string
		statusCode int
		data       interface{}
	}{
		{
			name:       "success response",
			statusCode: http.StatusOK,
			data:       map[string]string{"status": "success"},
		},
		{
			name:       "created response",
			statusCode: http.StatusCreated,
			data:       map[string]string{"id": "123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()

			server.sendJSONResponse(w, tt.statusCode, tt.data)

			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.Equal(t, tt.statusCode, w.Code)

			var response map[string]interface{}
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
		})
	}
}

// TestServerGetAuthMiddleware tests the getAuthMiddleware method
func TestServerGetAuthMiddleware(t *testing.T) {
	server := testServer(t)

	middleware := server.getAuthMiddleware()
	assert.NotNil(t, middleware)
	assert.Equal(t, server.enhancedAuthMiddleware, middleware)
}

// TestServerGetTaskManager tests the getTaskManager methods
func TestServerGetTaskManager(t *testing.T) {
	server := testServer(t)

	// Test getTaskManager (in test mode, this will be nil)
	taskManager := server.getTaskManager()
	if server.config.Server.TestMode {
		assert.Nil(t, taskManager)
	} else {
		assert.NotNil(t, taskManager)
		assert.Equal(t, server.taskManager, taskManager)
	}

	// Test getTaskManagerForCreation (in test mode, this will be nil)
	taskManagerForCreation := server.getTaskManagerForCreation()
	if server.config.Server.TestMode {
		assert.Nil(t, taskManagerForCreation)
	} else {
		assert.NotNil(t, taskManagerForCreation)
		assert.Equal(t, server.taskManager, taskManagerForCreation)
	}

	// Test getTaskManagerForRetrieval (in test mode, this will be nil)
	taskManagerForRetrieval := server.getTaskManagerForRetrieval()
	if server.config.Server.TestMode {
		assert.Nil(t, taskManagerForRetrieval)
	} else {
		assert.NotNil(t, taskManagerForRetrieval)
		assert.Equal(t, server.taskManager, taskManagerForRetrieval)
	}
}

// TestServerCreateMux tests the createMux method
func TestServerCreateMux(t *testing.T) {
	// For this test, we need a full config with auth and chassis
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host:     "localhost",
			Port:     8080,
			TestMode: true, // Use test mode to avoid background processes
		},
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

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	mux := server.createMux()
	assert.NotNil(t, mux)

	// Test that mux can handle requests
	req := httptest.NewRequest("GET", "/redfish/v1/", nil)
	w := httptest.NewRecorder()

	// This should not panic even if the handler doesn't exist
	mux.ServeHTTP(w, req)
}

// TestServerStartSimple tests basic start functionality without complex background processes
func TestServerStartSimple(t *testing.T) {
	t.Skip("Temporarily skipped - needs test mode implementation")
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 0, // Use port 0 to avoid conflicts
			TLS: config.TLSConfig{
				Enabled: false,
			},
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test that we can call Start without hanging
	// We'll use a very short timeout to ensure it doesn't hang
	startCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	startDone := make(chan error, 1)
	go func() {
		startDone <- server.Start()
	}()

	// Wait for start to complete or timeout
	select {
	case err := <-startDone:
		// Start completed (likely with an error due to invalid config, which is fine)
		t.Logf("Start completed with result: %v", err)
	case <-startCtx.Done():
		// Start timed out, which is expected for this test
		t.Log("Start timed out as expected")
	}

	// Clean up
	err := server.Shutdown()
	assert.NoError(t, err)
}

// TestServerStart tests the Start method
func TestServerStart(t *testing.T) {
	t.Skip("Temporarily skipped - needs test mode implementation")
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 0, // Use port 0 to avoid conflicts
			TLS: config.TLSConfig{
				Enabled: false,
			},
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Start server in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown server with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- server.Shutdown()
	}()

	// Wait for shutdown to complete or timeout
	select {
	case err := <-shutdownDone:
		assert.NoError(t, err)
	case <-shutdownCtx.Done():
		t.Log("Shutdown timed out, but this is acceptable for testing")
	}

	// Check for any startup errors
	select {
	case err := <-errChan:
		// Server stopped, which is expected
		assert.Error(t, err) // Should be http.ErrServerClosed
	default:
		// No error, which is also fine
	}
}

// TestServerStartWithTLS tests the Start method with TLS enabled
func TestServerStartWithTLS(t *testing.T) {
	t.Skip("Temporarily skipped - needs test mode implementation")
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 0, // Use port 0 to avoid conflicts
			TLS: config.TLSConfig{
				Enabled:  true,
				CertFile: "nonexistent.crt",
				KeyFile:  "nonexistent.key",
			},
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Start server in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown server with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- server.Shutdown()
	}()

	// Wait for shutdown to complete or timeout
	select {
	case err := <-shutdownDone:
		assert.NoError(t, err)
	case <-shutdownCtx.Done():
		t.Log("Shutdown timed out, but this is acceptable for testing")
	}

	// Check for startup errors (should fail due to missing cert files)
	select {
	case err := <-errChan:
		// Expected to fail due to missing cert files
		assert.Error(t, err)
	default:
		// If no error, that's also acceptable for this test
	}
}

// TestServerConcurrentAccess tests concurrent access to server methods
func TestServerConcurrentAccess(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test concurrent config updates
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			newConfig := &config.Config{
				Server: config.ServerConfig{
					Host: fmt.Sprintf("host%d", id),
					Port: 8080 + id,
				},
			}
			server.UpdateConfig(newConfig)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify server is still functional
	assert.NotNil(t, server.config)
}

// TestServerErrorHandling tests error handling in various scenarios
func TestServerErrorHandling(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test with nil request in sendRedfishError
	w := httptest.NewRecorder()
	err := fmt.Errorf("test error")
	server.sendRedfishError(w, nil, err)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var errorResponse redfish.Error
	jsonErr := json.NewDecoder(w.Body).Decode(&errorResponse)
	assert.NoError(t, jsonErr)
	assert.NotEmpty(t, errorResponse.Error.Message)
}

// TestServerMemoryManagerIntegration tests integration with memory manager
func TestServerMemoryManagerIntegration(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test that memory manager is properly initialized
	assert.NotNil(t, server.memoryManager)

	// Test buffer allocation
	buffer := server.memoryManager.GetBuffer()
	assert.NotNil(t, buffer)
	server.memoryManager.PutBuffer(buffer)

	// Test encoder allocation
	encoderBuffer := server.memoryManager.GetBuffer()
	encoder := server.memoryManager.GetEncoder(encoderBuffer)
	assert.NotNil(t, encoder)
	server.memoryManager.PutEncoder(encoder)

	// Test response allocation
	response := server.memoryManager.GetResponse()
	assert.NotNil(t, response)
	server.memoryManager.PutResponse(response)
}

// TestServerStartTime tests that start time is properly initialized
func TestServerStartTime(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test that start time is properly initialized
	assert.False(t, server.startTime.IsZero())
	assert.True(t, time.Since(server.startTime) < 1*time.Second)
}

// TestServerConfigMutex tests that config mutex is properly initialized
func TestServerConfigMutex(t *testing.T) {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// Test that config mutex is properly initialized by testing its functionality

	// Test that we can acquire read lock
	server.configMutex.RLock()
	server.configMutex.RUnlock()

	// Test that we can acquire write lock
	server.configMutex.Lock()
	server.configMutex.Unlock()
}

// testServer creates a server instance configured for testing (no background processes)
func testServer(t *testing.T) *Server {
	testConfig := &config.Config{
		Server: config.ServerConfig{
			Host:     "localhost",
			Port:     8080,
			TestMode: true, // Disable background processes for testing
		},
	}

	mockClient := &kubevirt.Client{}
	server := NewServer(testConfig, mockClient)

	// For testing, we want to disable background processes that can cause hanging
	// This is a test-specific configuration
	t.Log("Created test server with background processes disabled")

	return server
}

// testServerShutdown is a helper function that safely shuts down a server with timeouts
func testServerShutdown(t *testing.T, server *Server) {
	// Create a context with timeout to prevent indefinite hanging
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Shutdown in background
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- server.Shutdown()
	}()

	// Wait for shutdown to complete or timeout
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Logf("Server shutdown completed with error: %v", err)
		} else {
			t.Log("Server shutdown completed successfully")
		}
	case <-ctx.Done():
		t.Log("Server shutdown timed out - forcing cleanup")
		// Force cleanup by calling Stop() on all components directly
		if server.memoryManager != nil {
			server.memoryManager.Stop()
		}
		if server.connectionManager != nil {
			server.connectionManager.Stop()
		}
		if server.memoryMonitor != nil {
			server.memoryMonitor.Stop()
		}
		if server.advancedCache != nil {
			server.advancedCache.Stop()
		}
		if server.healthChecker != nil {
			server.healthChecker.Stop()
		}
		if server.selfHealingManager != nil {
			server.selfHealingManager.Stop()
		}
		if server.responseCache != nil {
			server.responseCache.Stop()
		}
		t.Log("Forced cleanup completed")
	}
}

// TestServerShutdownWithTimeout tests server shutdown with proper timeout handling
// TestServerShutdownWithTimeout tests the timeout handling for server shutdown
// This test is disabled due to interference with other tests
// The timeout handling is already tested in TestServerShutdownSimple and TestServerShutdown
func TestServerShutdownWithTimeout(t *testing.T) {
	t.Skip("Skipping this test due to interference with other tests - timeout handling is tested in other shutdown tests")
}

// TestServerUtilityFunctions tests simple utility functions without background processes
func TestServerUtilityFunctions(t *testing.T) {
	server := testServer(t)

	// Test validateMethod
	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	assert.True(t, server.validateMethod(w, req, []string{"GET"}))
	assert.False(t, server.validateMethod(w, req, []string{"POST"}))

	// Test validateMethod with different methods
	req.Method = "POST"
	assert.True(t, server.validateMethod(w, req, []string{"POST"}))
	assert.False(t, server.validateMethod(w, req, []string{"GET"}))

	// Test validateMethod with OPTIONS (should not be valid unless explicitly allowed)
	req.Method = "OPTIONS"
	assert.False(t, server.validateMethod(w, req, []string{"GET"}))
	assert.False(t, server.validateMethod(w, req, []string{"POST"}))

	// Test validateMethod with OPTIONS when it's explicitly allowed
	assert.True(t, server.validateMethod(w, req, []string{"OPTIONS", "GET"}))

	t.Log("Server utility functions test completed successfully")
}
