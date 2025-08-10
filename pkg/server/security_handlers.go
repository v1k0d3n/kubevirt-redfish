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

// Package server provides security handlers for the KubeVirt Redfish API server.
// It implements endpoints for security monitoring, audit trails, and compliance reporting.
//
// The security handlers provide:
// - Security metrics endpoint for monitoring
// - Audit trail access for compliance
// - Rate limiting information for security analysis
// - Security event export for SIEM integration
//
// Example usage:
//
//	// Register security handlers
//	server.RegisterSecurityHandlers(enhancedAuth)
//
//	// Access security metrics
//	GET /internal/security/metrics
//
//	// Access audit trail
//	GET /internal/security/audit?limit=100
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/auth"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// SecurityHandlers provides HTTP handlers for security monitoring and audit.
// It exposes security metrics, audit trails, and compliance information.
type SecurityHandlers struct {
	enhancedAuth *auth.EnhancedMiddleware
}

// NewSecurityHandlers creates a new security handlers instance.
// It initializes handlers for security monitoring and audit endpoints.
//
// Parameters:
// - enhancedAuth: Enhanced authentication middleware for security data
//
// Returns:
// - *SecurityHandlers: Initialized security handlers
func NewSecurityHandlers(enhancedAuth *auth.EnhancedMiddleware) *SecurityHandlers {
	return &SecurityHandlers{
		enhancedAuth: enhancedAuth,
	}
}

// RegisterSecurityHandlers registers security monitoring endpoints.
// It sets up HTTP handlers for security metrics, audit trails, and compliance.
//
// Parameters:
// - mux: HTTP serve mux to register handlers with
func (h *SecurityHandlers) RegisterSecurityHandlers(mux *http.ServeMux) {
	// Security metrics endpoint
	mux.HandleFunc("/internal/security/metrics", h.handleSecurityMetrics)

	// Audit trail endpoint
	mux.HandleFunc("/internal/security/audit", h.handleSecurityAudit)

	// Rate limiting information endpoint
	mux.HandleFunc("/internal/security/rate-limits", h.handleRateLimits)

	// Security events export endpoint
	mux.HandleFunc("/internal/security/events", h.handleSecurityEvents)

	// Security health check endpoint
	mux.HandleFunc("/internal/security/health", h.handleSecurityHealth)

	logger.Info("Security handlers registered: /internal/security/*")
}

// handleSecurityMetrics handles requests for security metrics.
// It returns comprehensive security statistics and monitoring data.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (h *SecurityHandlers) handleSecurityMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get security metrics
	metrics := h.enhancedAuth.GetSecurityMetrics()

	// Calculate additional metrics
	successRate := float64(0)
	if metrics.TotalAttempts > 0 {
		successRate = float64(metrics.SuccessfulLogins) / float64(metrics.TotalAttempts) * 100
	}

	failureRate := float64(0)
	if metrics.TotalAttempts > 0 {
		failureRate = float64(metrics.FailedLogins) / float64(metrics.TotalAttempts) * 100
	}

	// Calculate additional rates with zero division protection
	blockRate := float64(0)
	if metrics.TotalAttempts > 0 {
		blockRate = float64(metrics.BlockedAttempts) / float64(metrics.TotalAttempts) * 100
	}

	rateLimitRate := float64(0)
	if metrics.TotalAttempts > 0 {
		rateLimitRate = float64(metrics.RateLimitHits) / float64(metrics.TotalAttempts) * 100
	}

	// Create response with enhanced metrics
	response := map[string]interface{}{
		"timestamp": time.Now().UTC(),
		"metrics":   metrics,
		"calculated": map[string]interface{}{
			"success_rate":    successRate,
			"failure_rate":    failureRate,
			"block_rate":      blockRate,
			"rate_limit_rate": rateLimitRate,
		},
		"summary": map[string]interface{}{
			"total_requests":     metrics.TotalAttempts,
			"successful_logins":  metrics.SuccessfulLogins,
			"failed_logins":      metrics.FailedLogins,
			"blocked_attempts":   metrics.BlockedAttempts,
			"rate_limit_hits":    metrics.RateLimitHits,
			"security_incidents": metrics.SecurityIncidents,
		},
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Encode and send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode security metrics: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.DebugStructured("Security metrics requested", map[string]interface{}{
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
		"method":      r.Method,
		"path":        r.URL.Path,
	})
}

// handleSecurityAudit handles requests for security audit trail.
// It returns recent security events for compliance and monitoring.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (h *SecurityHandlers) handleSecurityAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 100 // Default limit

	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Get security events
	events := h.enhancedAuth.GetSecurityEvents(limit)

	// Create response
	response := map[string]interface{}{
		"timestamp": time.Now().UTC(),
		"limit":     limit,
		"count":     len(events),
		"events":    events,
		"summary": map[string]interface{}{
			"total_events": len(events),
			"time_range": map[string]interface{}{
				"oldest": func() interface{} {
					if len(events) > 0 {
						return events[0].Timestamp
					}
					return nil
				}(),
				"newest": func() interface{} {
					if len(events) > 0 {
						return events[len(events)-1].Timestamp
					}
					return nil
				}(),
			},
		},
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Encode and send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode security audit: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.DebugStructured("Security audit requested", map[string]interface{}{
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
		"method":      r.Method,
		"path":        r.URL.Path,
		"limit":       limit,
		"event_count": len(events),
	})
}

// handleRateLimits handles requests for rate limiting information.
// It returns current rate limiting status and blocked IPs.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (h *SecurityHandlers) handleRateLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get rate limit information
	rateLimits := h.enhancedAuth.GetRateLimitInfo()

	// Calculate statistics
	var totalBlocked int
	var totalAttempts int
	var activeBlocks int

	now := time.Now()
	for _, info := range rateLimits {
		totalAttempts += info.Attempts
		if now.Before(info.BlockedUntil) {
			totalBlocked++
			activeBlocks++
		}
	}

	// Create response
	response := map[string]interface{}{
		"timestamp":   time.Now().UTC(),
		"rate_limits": rateLimits,
		"summary": map[string]interface{}{
			"total_ips":      len(rateLimits),
			"total_attempts": totalAttempts,
			"total_blocked":  totalBlocked,
			"active_blocks":  activeBlocks,
		},
		"configuration": map[string]interface{}{
			"rate_limit_window": "5m",
			"max_attempts":      10,
			"block_duration":    "15m",
		},
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Encode and send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode rate limits: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.DebugStructured("Rate limits requested", map[string]interface{}{
		"remote_addr":   r.RemoteAddr,
		"user_agent":    r.UserAgent(),
		"method":        r.Method,
		"path":          r.URL.Path,
		"total_ips":     len(rateLimits),
		"active_blocks": activeBlocks,
	})
}

// handleSecurityEvents handles requests for security events export.
// It provides security events in various formats for SIEM integration.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (h *SecurityHandlers) handleSecurityEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 1000 // Default limit for export

	if limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Get security events
	events := h.enhancedAuth.GetSecurityEvents(limit)

	// Set response headers based on format
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=\"security-events.json\"")

		response := map[string]interface{}{
			"export_timestamp": time.Now().UTC(),
			"format":           "json",
			"event_count":      len(events),
			"events":           events,
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("Failed to encode security events JSON: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=\"security-events.csv\"")

		// Write CSV header
		w.Write([]byte("Timestamp,EventType,Username,IPAddress,UserAgent,Path,Method,Status,CorrelationID,Details\n"))

		// Write CSV data
		for _, event := range events {
			details := ""
			if len(event.Details) > 0 {
				if detailsBytes, err := json.Marshal(event.Details); err == nil {
					details = string(detailsBytes)
				}
			}

			line := fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
				event.Timestamp.Format(time.RFC3339),
				event.EventType,
				event.Username,
				event.IPAddress,
				event.UserAgent,
				event.Path,
				event.Method,
				event.Status,
				event.CorrelationID,
				details,
			)
			w.Write([]byte(line))
		}

	default:
		http.Error(w, "Unsupported format. Use 'json' or 'csv'", http.StatusBadRequest)
		return
	}

	logger.DebugStructured("Security events export requested", map[string]interface{}{
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
		"method":      r.Method,
		"path":        r.URL.Path,
		"format":      format,
		"limit":       limit,
		"event_count": len(events),
	})
}

// handleSecurityHealth handles security health check requests.
// It provides a quick health status of security components.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (h *SecurityHandlers) handleSecurityHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get basic metrics for health check
	metrics := h.enhancedAuth.GetSecurityMetrics()
	rateLimits := h.enhancedAuth.GetRateLimitInfo()

	// Determine health status
	health := "healthy"
	if metrics.FailedLogins > metrics.SuccessfulLogins*2 {
		health = "degraded"
	}
	if metrics.SecurityIncidents > 0 {
		health = "unhealthy"
	}

	// Create response
	response := map[string]interface{}{
		"timestamp": time.Now().UTC(),
		"status":    health,
		"components": map[string]interface{}{
			"authentication": map[string]interface{}{
				"status": "operational",
				"metrics": map[string]interface{}{
					"total_attempts":    metrics.TotalAttempts,
					"successful_logins": metrics.SuccessfulLogins,
					"failed_logins":     metrics.FailedLogins,
				},
			},
			"rate_limiting": map[string]interface{}{
				"status": "operational",
				"metrics": map[string]interface{}{
					"active_ips":       len(rateLimits),
					"blocked_attempts": metrics.BlockedAttempts,
					"rate_limit_hits":  metrics.RateLimitHits,
				},
			},
			"audit_logging": map[string]interface{}{
				"status": "operational",
				"metrics": map[string]interface{}{
					"security_incidents": metrics.SecurityIncidents,
				},
			},
		},
		"summary": map[string]interface{}{
			"overall_health": health,
			"last_check":     time.Now().UTC(),
		},
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// Set status code based on health
	if health == "unhealthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else if health == "degraded" {
		w.WriteHeader(http.StatusOK) // Still operational but degraded
	}

	// Encode and send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("Failed to encode security health: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.DebugStructured("Security health check requested", map[string]interface{}{
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
		"method":      r.Method,
		"path":        r.URL.Path,
		"health":      health,
	})
}
