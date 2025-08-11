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

// Package auth provides authentication middleware for the KubeVirt Redfish API server.
// It implements basic authentication with chassis-based access control and provides
// middleware for protecting Redfish API endpoints.
//
// The package provides:
// - Basic authentication middleware
// - Chassis-based access control
// - User session management
// - Authentication context injection
// - Configurable authentication providers
//
// The middleware supports multiple authentication methods and can be extended
// to support additional authentication mechanisms like OAuth2 or certificates.
//
// Example usage:
//
//	// Create authentication middleware with configuration
//	authMiddleware := auth.NewMiddleware(config)
//
//	// Apply middleware to HTTP handlers
//	http.HandleFunc("/redfish/v1/", authMiddleware.Authenticate(handler))
//
//	// Check user access to specific chassis
//	if authMiddleware.HasChassisAccess(user, "production-cluster") {
//		// Allow access to production chassis
//	}
package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// User represents an authenticated user with access permissions.
// It contains user credentials and chassis access information for authorization.
type User struct {
	Username string   // User's username
	Password string   // User's password (hashed in production)
	Chassis  []string // List of chassis names the user can access
}

// AuthContext represents the authentication context for a request.
// It contains user information and is injected into the request context
// for downstream handlers to access authentication data.
type AuthContext struct {
	User    *User  // Authenticated user
	Chassis string // Current chassis being accessed
}

// Middleware represents the authentication middleware.
// It handles user authentication and chassis-based access control
// for all Redfish API requests.
type Middleware struct {
	config *config.Config
}

// NewMiddleware creates a new authentication middleware instance.
// It initializes the middleware with the provided configuration
// and sets up authentication providers.
//
// Parameters:
// - config: Application configuration containing user and chassis information
//
// Returns:
// - *Middleware: Initialized authentication middleware
func NewMiddleware(config *config.Config) *Middleware {
	return &Middleware{
		config: config,
	}
}

// Authenticate is a middleware function that performs authentication.
// It extracts credentials from the request, validates them against the configuration,
// and injects authentication context into the request for downstream handlers.
//
// The middleware allows unauthenticated access to the Redfish service root (/redfish/v1/)
// for health checks and service discovery, while protecting all other endpoints.
//
// Parameters:
// - handler: HTTP handler function to wrap with authentication
//
// Returns:
// - http.HandlerFunc: Wrapped handler with authentication middleware
func (m *Middleware) Authenticate(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow unauthenticated access to the Redfish service root for health checks
		if r.Method == "GET" && r.URL.Path == "/redfish/v1/" {
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

		// Extract chassis from URL path
		chassis := m.extractChassisFromPath(r.URL.Path)
		if chassis == "" {
			// No chassis specified, allow access to all chassis the user has access to
			chassis = "*"
		}

		// Extract and validate credentials
		user, err := m.extractAndValidateCredentials(r)
		if err != nil {
			m.sendUnauthorizedResponse(w, "Authentication failed")
			return
		}

		// Check chassis access
		if !m.hasChassisAccess(user, chassis) {
			m.sendForbiddenResponse(w, "Access denied to chassis")
			return
		}

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

// extractChassisFromPath extracts the chassis name from the request path.
// It parses Redfish API paths to identify which chassis is being accessed.
//
// Parameters:
// - path: HTTP request path
//
// Returns:
// - string: Chassis name extracted from path, or empty string if not found
func (m *Middleware) extractChassisFromPath(path string) string {
	// Parse path like /redfish/v1/Chassis/{chassis-name}/Systems
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "Chassis" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// extractAndValidateCredentials extracts and validates user credentials.
// It supports basic authentication and validates credentials against the configuration.
//
// Parameters:
// - r: HTTP request containing authentication headers
//
// Returns:
// - *User: Validated user information
// - error: Authentication error if credentials are invalid
func (m *Middleware) extractAndValidateCredentials(r *http.Request) (*User, error) {
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

	// Validate against configuration
	user, err := m.config.GetUserByCredentials(username, password)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &User{
		Username: user.Username,
		Password: user.Password,
		Chassis:  user.Chassis,
	}, nil
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
func (m *Middleware) hasChassisAccess(user *User, chassis string) bool {
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

// sendUnauthorizedResponse sends an HTTP 401 Unauthorized response.
// It provides a standardized error response for authentication failures.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (m *Middleware) sendUnauthorizedResponse(w http.ResponseWriter, message string) {
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
func (m *Middleware) sendForbiddenResponse(w http.ResponseWriter, message string) {
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

// GetAuthContext extracts authentication context from the request.
// It retrieves the authentication context that was injected by the middleware.
//
// Parameters:
// - r: HTTP request containing authentication context
//
// Returns:
// - *AuthContext: Authentication context from request, or nil if not found
func GetAuthContext(r *http.Request) *AuthContext {
	if authCtx, ok := logger.GetAuth(r.Context()).(*AuthContext); ok {
		return authCtx
	}
	return nil
}

// GetUser extracts user information from the request.
// It provides a convenient way to access user data in request handlers.
//
// Parameters:
// - r: HTTP request containing authentication context
//
// Returns:
// - *User: User information from request, or nil if not found
func GetUser(r *http.Request) *User {
	authCtx := GetAuthContext(r)
	if authCtx != nil {
		return authCtx.User
	}
	return nil
}

// GetChassis extracts chassis information from the request.
// It provides a convenient way to access chassis data in request handlers.
//
// Parameters:
// - r: HTTP request containing authentication context
//
// Returns:
// - string: Chassis name from request, or empty string if not found
func GetChassis(r *http.Request) string {
	authCtx := GetAuthContext(r)
	if authCtx != nil {
		return authCtx.Chassis
	}
	return ""
}

// HasChassisAccess checks if the current user has access to a specific chassis.
// It provides a convenient way to check permissions in request handlers.
//
// Parameters:
// - r: HTTP request containing authentication context
// - chassis: Chassis name to check access for
//
// Returns:
// - bool: True if user has access to the chassis, false otherwise
func HasChassisAccess(r *http.Request, chassis string) bool {
	user := GetUser(r)
	if user == nil {
		return false
	}

	for _, userChassis := range user.Chassis {
		if userChassis == chassis {
			return true
		}
	}

	return false
}
