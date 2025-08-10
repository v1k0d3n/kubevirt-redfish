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
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// ResponseWriter wraps http.ResponseWriter to capture status code
type ResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code
func (rw *ResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Write implements the http.ResponseWriter interface
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	return rw.ResponseWriter.Write(b)
}

// generateCorrelationID generates a unique correlation ID for request tracking
func generateCorrelationID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// extractUser extracts username from request (from auth middleware)
func extractUser(r *http.Request) string {
	// This will be populated by the auth middleware
	if user := r.Header.Get("X-Redfish-User"); user != "" {
		return user
	}
	return "unknown"
}

// LoggingMiddleware adds correlation IDs and request/response logging
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Generate correlation ID
		correlationID := generateCorrelationID()

		// Extract user information
		user := extractUser(r)

		// Create context with correlation ID
		ctx := logger.WithCorrelationID(r.Context(), correlationID)
		ctx = logger.WithUser(ctx, user)
		ctx = logger.WithOperation(ctx, r.Method)
		ctx = logger.WithResource(ctx, r.URL.Path)

		// Set context for logging
		logger.SetContext(ctx)

		// Log incoming request
		logger.LogRequest(r.Method, r.URL.Path, user, correlationID)

		// Wrap response writer to capture status code
		wrappedWriter := &ResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default status code
		}

		// Process request
		next.ServeHTTP(wrappedWriter, r.WithContext(ctx))

		// Calculate duration
		duration := time.Since(start)

		// Log response
		logger.LogResponse(r.Method, r.URL.Path, user, correlationID, wrappedWriter.statusCode, duration)
	})
}

// PerformanceMiddleware tracks request performance and logs slow requests
func PerformanceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Process request
		next.ServeHTTP(w, r)

		// Calculate duration
		duration := time.Since(start)

		// Log slow requests (over 5 seconds)
		if duration > 5*time.Second {
			user := extractUser(r)
			correlationID := logger.GetCorrelationID(r.Context())
			if correlationID == "" {
				correlationID = "unknown"
			}

			fields := map[string]interface{}{
				"correlation_id": correlationID,
				"user":           user,
				"operation":      "performance_warning",
				"method":         r.Method,
				"path":           r.URL.Path,
				"duration":       duration.String(),
				"threshold":      "5s",
			}

			logger.WarningStructured("Slow request detected", fields)
		}
	})
}

// CompressionMiddleware adds gzip compression for JSON responses
func CompressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if client accepts gzip encoding
		acceptEncoding := r.Header.Get("Accept-Encoding")
		if !strings.Contains(acceptEncoding, "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		// Create gzip writer
		gzipWriter := gzip.NewWriter(w)
		defer gzipWriter.Close()

		// Set headers
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")

		// Create wrapped response writer
		compressedWriter := &CompressedResponseWriter{
			ResponseWriter: w,
			gzipWriter:     gzipWriter,
		}

		// Process request
		next.ServeHTTP(compressedWriter, r)
	})
}

// CompressedResponseWriter wraps http.ResponseWriter to provide compression
type CompressedResponseWriter struct {
	http.ResponseWriter
	gzipWriter *gzip.Writer
}

// Write compresses and writes data
func (crw *CompressedResponseWriter) Write(data []byte) (int, error) {
	return crw.gzipWriter.Write(data)
}

// WriteHeader sets the status code
func (crw *CompressedResponseWriter) WriteHeader(code int) {
	crw.ResponseWriter.WriteHeader(code)
}

// Header returns the response headers
func (crw *CompressedResponseWriter) Header() http.Header {
	return crw.ResponseWriter.Header()
}

// SecurityMiddleware logs security-relevant information
func SecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := extractUser(r)
		correlationID := logger.GetCorrelationID(r.Context())
		if correlationID == "" {
			correlationID = "unknown"
		}

		// Log authentication attempts
		if r.URL.Path == "/redfish/v1/" || r.URL.Path == "/redfish/v1/Systems" {
			fields := map[string]interface{}{
				"correlation_id": correlationID,
				"user":           user,
				"operation":      "authentication",
				"method":         r.Method,
				"path":           r.URL.Path,
				"user_agent":     r.UserAgent(),
				"remote_addr":    r.RemoteAddr,
			}

			logger.DebugStructured("Authentication attempt", fields)
		}

		// Process request
		next.ServeHTTP(w, r)
	})
}
