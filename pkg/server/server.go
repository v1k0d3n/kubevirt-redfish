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

// Package server provides the HTTP server implementation for the KubeVirt Redfish API.
// It handles all HTTP requests, implements Redfish API endpoints, and manages
// the server lifecycle including startup, shutdown, and graceful termination.
//
// The package provides:
// - HTTP server with TLS support
// - Redfish API endpoint implementations
// - Request routing and middleware integration
// - Error handling and response formatting
// - Server configuration and lifecycle management
//
// The server implements the Redfish specification v1.22.1 and provides endpoints
// for service discovery, chassis management, system operations, and virtual media.
//
// Example usage:
//
//	// Create and configure server
//	server := server.NewServer(config, kubevirtClient)
//
//	// Start the server
//	go func() {
//		if err := server.Start(); err != nil {
//			log.Fatalf("Server failed: %v", err)
//		}
//	}()
//
//	// Graceful shutdown
//	defer server.Shutdown()
package server

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"sync"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/auth"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/errors"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/kubevirt"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/redfish"
)

// Server represents the HTTP server for the KubeVirt Redfish API.
// It handles all HTTP requests and provides Redfish API endpoints
// for managing KubeVirt virtual machines through the Redfish interface.
type Server struct {
	config                 *config.Config
	kubevirtClient         *kubevirt.Client
	enhancedAuthMiddleware *auth.EnhancedMiddleware // Enhanced authentication
	securityHandlers       *SecurityHandlers        // Security monitoring endpoints
	httpServer             *http.Server
	taskManager            *TaskManager
	useEnhancedAuth        bool // Flag to enable enhanced authentication
	jobScheduler           *JobScheduler
	memoryManager          *MemoryManager
	connectionManager      *ConnectionManager
	memoryMonitor          *MemoryMonitor
	advancedCache          *AdvancedCache
	responseOptimizer      *ResponseOptimizer
	responseCacheOptimizer *ResponseCacheOptimizer
	circuitBreakerManager  *CircuitBreakerManager
	retryManager           *RetryManager
	rateLimitManager       *RateLimitManager
	healthChecker          *HealthChecker
	selfHealingManager     *SelfHealingManager

	startTime     time.Time    // Added for uptime calculation
	responseCache *Cache       // Response cache for performance optimization
	configMutex   sync.RWMutex // Protects config for hot-reload
}

// NewServer creates a new HTTP server instance.
// It initializes the server with configuration, KubeVirt client, and authentication middleware.
//
// Parameters:
// - config: Application configuration for server settings
// - kubevirtClient: KubeVirt client for VM operations
//
// Returns:
// - *Server: Initialized HTTP server
func NewServer(config *config.Config, kubevirtClient *kubevirt.Client) *Server {
	// Initialize enhanced authentication middleware
	enhancedAuthMiddleware := auth.NewEnhancedMiddleware(config)

	// Initialize security handlers
	securityHandlers := NewSecurityHandlers(enhancedAuthMiddleware)

	var taskManager *TaskManager
	var jobScheduler *JobScheduler
	var memoryManager *MemoryManager
	var connectionManager *ConnectionManager
	var memoryMonitor *MemoryMonitor
	var advancedCache *AdvancedCache
	var responseOptimizer *ResponseOptimizer
	var responseCacheOptimizer *ResponseCacheOptimizer
	var circuitBreakerManager *CircuitBreakerManager
	var retryManager *RetryManager
	var rateLimitManager *RateLimitManager
	var healthChecker *HealthChecker
	var selfHealingManager *SelfHealingManager

	// Initialize background components only if not in test mode
	if !config.Server.TestMode {
		// Initialize enhanced task manager
		taskManager = NewTaskManager(4, kubevirtClient) // 4 workers for background processing

		// Initialize job scheduler
		jobScheduler = NewJobScheduler()

		// Initialize memory manager
		memoryManager = NewMemoryManager()

		// Initialize connection manager
		connectionManager = NewConnectionManager()

		// Initialize memory monitor
		memoryMonitor = NewMemoryMonitor()

		// Initialize advanced cache
		advancedCache = NewAdvancedCache()

		// Initialize response optimizer
		responseOptimizer = NewResponseOptimizer()

		// Initialize response cache optimizer
		responseCacheOptimizer = NewResponseCacheOptimizer()

		// Initialize circuit breaker manager
		circuitBreakerManager = NewCircuitBreakerManager()

		// Initialize retry manager
		retryManager = NewRetryManager()

		// Initialize rate limit manager
		rateLimitManager = NewRateLimitManager()

		// Initialize health checker
		healthChecker = NewHealthChecker()

		// Initialize self-healing manager
		selfHealingManager = NewSelfHealingManager(healthChecker, circuitBreakerManager, retryManager)
	} else {
		logger.Info("Test mode enabled - skipping background component initialization")
	}

	// Initialize response cache (skip in test mode)
	var responseCache *Cache
	if !config.Server.TestMode {
		responseCache = NewCache(1000, 5*time.Minute) // 1000 entries, 5 minute default TTL
	}

	server := &Server{
		config:                 config,
		kubevirtClient:         kubevirtClient,
		enhancedAuthMiddleware: enhancedAuthMiddleware,
		securityHandlers:       securityHandlers,
		taskManager:            taskManager,
		useEnhancedAuth:        true, // Enable enhanced authentication
		jobScheduler:           jobScheduler,
		memoryManager:          memoryManager,
		connectionManager:      connectionManager,
		memoryMonitor:          memoryMonitor,
		advancedCache:          advancedCache,
		responseOptimizer:      responseOptimizer,
		responseCacheOptimizer: responseCacheOptimizer,
		circuitBreakerManager:  circuitBreakerManager,
		retryManager:           retryManager,
		rateLimitManager:       rateLimitManager,
		healthChecker:          healthChecker,
		selfHealingManager:     selfHealingManager,

		startTime:     time.Now(), // Initialize start time
		responseCache: responseCache,
	}

	// Add default background jobs (skip in test mode)
	if !config.Server.TestMode {
		if err := jobScheduler.AddDefaultJobs(server); err != nil {
			logger.Warning("Failed to add default background jobs: %v", err)
		}
	} else {
		logger.Info("Test mode enabled - skipping background job initialization")
	}

	return server
}

// Start starts the HTTP server and begins listening for requests.
// It configures TLS if enabled and sets up all Redfish API endpoints.
// The server runs until explicitly stopped or an error occurs.
//
// Returns:
// - error: Any error that occurred during server startup or operation
func (s *Server) Start() error {
	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port),
		Handler:           s.createMux(),
		ReadHeaderTimeout: 10 * time.Second, // Protect against Slowloris attacks
	}

	// Configure TLS if enabled
	if s.config.Server.TLS.Enabled {
		log.Printf("Starting HTTPS server on %s", s.httpServer.Addr)
		return s.httpServer.ListenAndServeTLS(s.config.Server.TLS.CertFile, s.config.Server.TLS.KeyFile)
	} else {
		log.Printf("Starting HTTP server on %s", s.httpServer.Addr)
		return s.httpServer.ListenAndServe()
	}
}

// Shutdown gracefully shuts down the HTTP server.
// It stops accepting new connections and waits for existing requests to complete.
// The shutdown has a timeout to prevent indefinite waiting.
//
// Returns:
// - error: Any error that occurred during shutdown
func (s *Server) Shutdown() error {
	logger.Info("Shutting down server...")

	// In test mode, components may be nil, so check before stopping
	if s.config.Server.TestMode {
		logger.Info("Test mode - skipping background component shutdown")
	} else {
		// Stop the enhanced task manager
		if s.taskManager != nil {
			s.taskManager.Stop()
		}

		// Stop the job scheduler
		if s.jobScheduler != nil {
			s.jobScheduler.Stop()
		}

		// Stop the memory manager
		if s.memoryManager != nil {
			s.memoryManager.Stop()
		}

		// Stop the connection manager
		if s.connectionManager != nil {
			s.connectionManager.Stop()
		}

		// Stop the memory monitor
		if s.memoryMonitor != nil {
			s.memoryMonitor.Stop()
		}

		// Stop the response cache
		if s.responseCache != nil {
			s.responseCache.Stop()
		}

		// Stop the advanced cache
		if s.advancedCache != nil {
			s.advancedCache.Stop()
		}

		// Stop the response cache optimizer
		if s.responseCacheOptimizer != nil {
			s.responseCacheOptimizer.Stop()
		}

		// Stop the circuit breaker manager
		if s.circuitBreakerManager != nil {
			s.circuitBreakerManager.Stop()
		}

		// Stop the retry manager (no Stop method, just log)
		if s.retryManager != nil {
			logger.Info("Retry manager stopped")
		}

		// Stop the rate limit manager (no Stop method, just log)
		if s.rateLimitManager != nil {
			logger.Info("Rate limit manager stopped")
		}

		// Stop the health checker
		if s.healthChecker != nil {
			s.healthChecker.Stop()
		}
	}

	// Stop the self-healing manager
	if s.selfHealingManager != nil {
		s.selfHealingManager.Stop()
	}

	// Close the KubeVirt client
	if s.kubevirtClient != nil {
		if err := s.kubevirtClient.Close(); err != nil {
			logger.Warning("Error closing KubeVirt client: %v", err)
		}
	}

	// Shutdown the HTTP server
	if s.httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

// UpdateConfig safely updates the server's configuration at runtime.
// This is used for hot-reloading configuration changes.
// Parameters:
// - newConfig: The new configuration to apply
func (s *Server) UpdateConfig(newConfig *config.Config) {
	s.configMutex.Lock()
	defer s.configMutex.Unlock()

	s.config = newConfig
	logger.Info("Configuration hot-reloaded successfully")
}

// createMux creates the HTTP request multiplexer with all Redfish API endpoints.
// It sets up routing for all Redfish API paths and applies authentication and logging middleware.
//
// Returns:
// - *http.ServeMux: Configured HTTP multiplexer with all endpoints
func (s *Server) createMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Register security monitoring endpoints
	s.securityHandlers.RegisterSecurityHandlers(mux)

	// Apply middleware chain: Logging -> Security -> Performance -> Compression -> Cache -> Authentication -> Handler
	// Service root endpoint
	mux.Handle("/redfish/v1/",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.CacheMiddleware(
							s.getAuthMiddleware().Authenticate(s.handleServiceRoot),
						),
					),
				),
			),
		),
	)

	// Chassis collection endpoint
	mux.Handle("/redfish/v1/Chassis",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.CacheMiddleware(
							s.getAuthMiddleware().Authenticate(s.handleChassisCollection),
						),
					),
				),
			),
		),
	)

	// Individual chassis endpoint
	mux.Handle("/redfish/v1/Chassis/",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.CacheMiddleware(
							s.getAuthMiddleware().Authenticate(s.handleChassis),
						),
					),
				),
			),
		),
	)

	// Systems collection endpoint
	mux.Handle("/redfish/v1/Systems",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.CacheMiddleware(
							s.getAuthMiddleware().Authenticate(s.handleSystemsCollection),
						),
					),
				),
			),
		),
	)

	// Individual system endpoint (handles both system and virtual media requests)
	mux.Handle("/redfish/v1/Systems/",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.CacheMiddleware(
							s.getAuthMiddleware().Authenticate(s.handleSystem),
						),
					),
				),
			),
		),
	)

	// Managers endpoint
	mux.Handle("/redfish/v1/Managers/",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.CacheMiddleware(
							s.getAuthMiddleware().Authenticate(s.handleManager),
						),
					),
				),
			),
		),
	)

	// Task endpoints (no caching for dynamic content)
	mux.Handle("/redfish/v1/TaskService/Tasks/",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.getAuthMiddleware().Authenticate(s.handleTask),
					),
				),
			),
		),
	)

	// Performance metrics endpoint (internal use, no caching)
	mux.Handle("/internal/metrics",
		LoggingMiddleware(
			SecurityMiddleware(
				PerformanceMiddleware(
					CompressionMiddleware(
						s.getAuthMiddleware().Authenticate(s.handleMetrics),
					),
				),
			),
		),
	)

	return mux
}

// getAuthMiddleware returns the appropriate authentication middleware based on configuration.
// It allows switching between basic and enhanced authentication.
func (s *Server) getAuthMiddleware() *auth.EnhancedMiddleware {
	if s.useEnhancedAuth {
		return s.enhancedAuthMiddleware
	}
	// For backward compatibility, we'll use enhanced auth for both cases
	// since it's backward compatible with basic auth
	return s.enhancedAuthMiddleware
}

// getTaskManager returns the enhanced task manager
func (s *Server) getTaskManager() interface{} {
	return s.taskManager
}

// getTaskManagerForCreation returns the enhanced task manager for creating tasks
func (s *Server) getTaskManagerForCreation() interface{} {
	return s.taskManager
}

// getTaskManagerForRetrieval returns the enhanced task manager for retrieving tasks
func (s *Server) getTaskManagerForRetrieval() interface{} {
	return s.taskManager
}

// handleServiceRoot handles the Redfish service root endpoint.
// It returns the service root information with links to main resource collections.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleServiceRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/redfish/v1/" {
		s.sendNotFound(w, "Service root not found")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	serviceRoot := redfish.ServiceRoot{
		OdataContext: "/redfish/v1/$metadata#ServiceRoot.ServiceRoot",
		OdataID:      "/redfish/v1/",
		OdataType:    "#ServiceRoot.v1_0_0.ServiceRoot",
		ID:           "RootService",
		Name:         "Root Service",
		Systems: redfish.Link{
			OdataID: "/redfish/v1/Systems",
		},
		Chassis: redfish.Link{
			OdataID: "/redfish/v1/Chassis",
		},
		Managers: redfish.Link{
			OdataID: "/redfish/v1/Managers",
		},
	}

	s.sendOptimizedJSON(w, r, serviceRoot)
}

// handleChassisCollection handles the chassis collection endpoint.
// It returns a list of all available chassis (namespaces) that the user can access.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleChassisCollection(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/redfish/v1/Chassis" {
		s.sendNotFound(w, "Chassis collection not found")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	user := auth.GetUser(r)
	var members []redfish.Link

	for _, chassisName := range user.Chassis {
		members = append(members, redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Chassis/%s", chassisName),
		})
	}

	collection := redfish.ChassisCollection{
		OdataContext: "/redfish/v1/$metadata#ChassisCollection.ChassisCollection",
		OdataID:      "/redfish/v1/Chassis",
		OdataType:    "#ChassisCollection.ChassisCollection",
		Name:         "Chassis Collection",
		Members:      members,
		MembersCount: len(members),
	}

	// Set appropriate cache headers for collections
	s.setCacheHeaders(w, "collection")
	s.sendOptimizedJSON(w, r, collection)
}

// handleChassis handles individual chassis endpoints.
// It returns details of a specific chassis (namespace) that the user can access.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleChassis(w http.ResponseWriter, r *http.Request) {
	// Extract chassis name from path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		s.sendNotFound(w, "Invalid chassis path")
		return
	}

	chassisName := pathParts[3]
	if chassisName == "" {
		s.sendNotFound(w, "Chassis name required")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to chassis")
		return
	}

	// Get chassis configuration
	chassisConfig, err := s.config.GetChassisByName(chassisName)
	if err != nil {
		s.sendNotFound(w, "Chassis not found")
		return
	}

	// Get VMs in this chassis
	vms, err := s.kubevirtClient.ListVMsWithSelector(chassisConfig.Namespace, chassisConfig.VMSelector)
	if err != nil {
		logger.Error("Failed to list VMs for chassis %s: %v", chassisName, err)
		vms = []string{}
	}

	// Build computer system links
	var computerSystems []redfish.Link
	for _, vmName := range vms {
		computerSystems = append(computerSystems, redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s", vmName),
		})
	}

	chassis := redfish.Chassis{
		OdataContext: "/redfish/v1/$metadata#Chassis.Chassis",
		OdataID:      fmt.Sprintf("/redfish/v1/Chassis/%s", chassisName),
		OdataType:    "#Chassis.v1_0_0.Chassis",
		OdataEtag:    fmt.Sprintf("W/\"%d\"", time.Now().Unix()), // Simple ETag for versioning
		ID:           chassisName,
		Name:         chassisName,
		Description:  fmt.Sprintf("Kubernetes namespace: %s", chassisConfig.Namespace),
		Status: redfish.Status{
			State:  "Enabled",
			Health: "OK",
		},
		ChassisType: "RackMount",
		Links: redfish.Links{
			ComputerSystems: computerSystems,
			Managers:        []redfish.Link{},
		},
	}

	// Return the resource with proper Redfish headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", chassis.OdataEtag)
	s.setCacheHeaders(w, "resource")
	s.encodeJSONResponse(w, chassis)
}

// handleSystemsCollection handles the systems collection endpoint.
// It returns a list of all available computer systems (VMs) that the user can access.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleSystemsCollection(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/redfish/v1/Systems" {
		s.sendNotFound(w, "Systems collection not found")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	user := auth.GetUser(r)
	var members []redfish.Link

	for _, chassisName := range user.Chassis {
		config, err := s.config.GetChassisByName(chassisName)
		if err != nil {
			continue
		}

		vms, err := s.kubevirtClient.ListVMsWithSelector(config.Namespace, config.VMSelector)
		if err != nil {
			logger.Error("Failed to list VMs for chassis %s: %v", chassisName, err)
			continue
		}

		for _, vmName := range vms {
			members = append(members, redfish.Link{
				OdataID: fmt.Sprintf("/redfish/v1/Systems/%s", vmName),
			})
		}
	}

	collection := redfish.ComputerSystemCollection{
		OdataContext: "/redfish/v1/$metadata#ComputerSystemCollection.ComputerSystemCollection",
		OdataID:      "/redfish/v1/Systems",
		OdataType:    "#ComputerSystemCollection.ComputerSystemCollection",
		Name:         "Computer System Collection",
		Members:      members,
		MembersCount: len(members),
	}

	// Set appropriate cache headers for collections
	s.setCacheHeaders(w, "collection")
	s.sendOptimizedJSON(w, r, collection)
}

// handleSystem handles individual system (VM) endpoints.
// It returns detailed information about a specific virtual machine.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	// Extract system name from path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		s.sendNotFound(w, "Invalid system path")
		return
	}

	systemName := pathParts[4]
	if systemName == "" {
		s.sendNotFound(w, "System name required")
		return
	}

	// Log all incoming system requests for monitoring
	logger.Info("Received %s request for VM %s from %s", r.Method, systemName, r.RemoteAddr)
	logger.Debug("Request path: %s, Headers: %v", r.URL.Path, r.Header)

	// Handle power management actions
	if r.Method == "POST" && strings.Contains(r.URL.Path, "/Actions/ComputerSystem.Reset") {
		s.handlePowerAction(w, r, systemName)
		return
	}

	// Handle boot configuration updates
	if r.Method == "PATCH" {
		s.handleBootUpdate(w, r, systemName)
		return
	}

	// Check if this is a virtual media request
	if len(pathParts) >= 6 && pathParts[5] == "VirtualMedia" {
		s.handleVirtualMediaRequest(w, r, systemName, pathParts)
		return
	}

	// For GET requests to the system resource, validate method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Get real power state
	powerState, err := s.kubevirtClient.GetVMPowerState(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Error("Failed to get power state for VM %s: %v", systemName, err)
		powerState = "Unknown"
	}

	// Get real boot options
	bootOptions, err := s.kubevirtClient.GetVMBootOptions(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Error("Failed to get boot options for VM %s: %v", systemName, err)
		bootOptions = map[string]interface{}{
			"bootSourceOverrideEnabled": "Disabled",
			"bootSourceOverrideTarget":  "None",
			"bootSourceOverrideMode":    "UEFI",
		}
	}

	// Get real memory information
	memoryGB, err := s.kubevirtClient.GetVMMemory(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Warning("Failed to get memory for VM %s: %v", systemName, err)
		memoryGB = 2.0 // Low default fallback
	} else {
		logger.Debug("Retrieved memory for VM %s: %.1f GB", systemName, memoryGB)
	}

	// Get real CPU information
	cpuCount, err := s.kubevirtClient.GetVMCPU(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Warning("Failed to get CPU for VM %s: %v", systemName, err)
		cpuCount = 1 // Low default fallback
	} else {
		logger.Debug("Retrieved CPU count for VM %s: %d cores", systemName, cpuCount)
	}

	system := redfish.ComputerSystem{
		OdataContext: "/redfish/v1/$metadata#ComputerSystem.ComputerSystem",
		OdataID:      fmt.Sprintf("/redfish/v1/Systems/%s", systemName),
		OdataType:    "#ComputerSystem.v1_0_0.ComputerSystem",
		OdataEtag:    fmt.Sprintf("W/\"%d\"", time.Now().Unix()), // Simple ETag for versioning
		ID:           systemName,
		Name:         systemName,
		SystemType:   "Virtual",
		Status: redfish.Status{
			State:  "Enabled",
			Health: "OK",
		},
		PowerState: powerState,
		Memory: redfish.MemorySummary{
			OdataID:              fmt.Sprintf("/redfish/v1/Systems/%s/Memory", systemName),
			TotalSystemMemoryGiB: memoryGB,
		},
		ProcessorSummary: redfish.ProcessorSummary{
			Count: cpuCount,
		},
		Storage: redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/Storage", systemName),
		},
		EthernetInterfaces: redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/EthernetInterfaces", systemName),
		},
		VirtualMedia: redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia", systemName),
		},
		Boot: redfish.Boot{
			BootSourceOverrideEnabled:               bootOptions["bootSourceOverrideEnabled"].(string),
			BootSourceOverrideTarget:                bootOptions["bootSourceOverrideTarget"].(string),
			BootSourceOverrideTargetAllowableValues: []string{"Cd", "Hdd"},
			BootSourceOverrideMode:                  bootOptions["bootSourceOverrideMode"].(string),
			UefiTargetBootSourceOverride:            "/0x31/0x33/0x01/0x01",
		},
		Actions: redfish.Actions{
			Reset: redfish.ResetAction{
				Target: fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", systemName),
				ResetType: []string{
					"On", "ForceOff", "GracefulShutdown", "ForceRestart", "GracefulRestart", "Pause", "Resume",
				},
			},
		},
		Links: redfish.SystemLinks{
			ManagedBy: []redfish.Link{
				{
					OdataID: "/redfish/v1/Managers/1",
				},
			},
		},
	}

	// Return the resource with proper Redfish headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", system.OdataEtag)
	s.setCacheHeaders(w, "resource")
	s.encodeJSONResponse(w, system)
}

// handleVirtualMediaRequest handles virtual media requests within the system handler.
// It routes virtual media requests to the appropriate handler based on the path.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system
// - pathParts: Parsed URL path components
func (s *Server) handleVirtualMediaRequest(w http.ResponseWriter, r *http.Request, systemName string, pathParts []string) {
	// Check if this is a virtual media action
	if len(pathParts) >= 7 {
		mediaID := pathParts[6]
		if mediaID == "" {
			s.sendNotFound(w, "Virtual media ID required")
			return
		}

		// Handle virtual media actions
		if r.Method == "POST" && len(pathParts) >= 8 {
			action := pathParts[7]
			switch action {
			case "Actions":
				if len(pathParts) >= 9 {
					actionType := pathParts[8]
					switch actionType {
					case "VirtualMedia.InsertMedia":
						s.handleInsertVirtualMedia(w, r, systemName, mediaID)
						return
					case "VirtualMedia.EjectMedia":
						s.handleEjectVirtualMedia(w, r, systemName, mediaID)
						return
					}
				}
			}
		}

		// Handle GET request for virtual media details
		if r.Method == "GET" {
			s.handleGetVirtualMedia(w, r, systemName, mediaID)
			return
		}
	} else if len(pathParts) >= 6 && pathParts[5] == "VirtualMedia" {
		// Handle VirtualMedia collection endpoint
		if r.Method == "GET" {
			s.handleVirtualMediaCollection(w, r, systemName)
			return
		}
	}

	// If not a recognized virtual media request, return not found
	s.sendNotFound(w, "Virtual media endpoint not found")
}

// handleVirtualMediaCollection handles GET requests for virtual media collection.
// It returns a list of all virtual media devices for a system.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system
func (s *Server) handleVirtualMediaCollection(w http.ResponseWriter, r *http.Request, systemName string) {
	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Get virtual media devices
	mediaDevices, err := s.kubevirtClient.GetVMVirtualMedia(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Error("Failed to get virtual media for VM %s: %v", systemName, err)
		// Don't fail, just return empty list
		mediaDevices = []string{}
	}

	// Standardize on Cd as the primary virtual media endpoint (Redfish standard)
	// Map Cd to the actual cdrom0 device for Metal3-Ironic compatibility
	hasCdrom0 := false
	for _, device := range mediaDevices {
		if device == "cdrom0" {
			hasCdrom0 = true
			break
		}
	}

	// If we have cdrom0, use Cd as the standard endpoint
	if hasCdrom0 {
		mediaDevices = []string{"Cd"}
	} else {
		// Fallback: include both for backward compatibility
		mediaDevices = append(mediaDevices, "Cd")
	}

	// Create collection response
	var members []redfish.Link
	for _, device := range mediaDevices {
		members = append(members, redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia/%s", systemName, device),
		})
	}

	collection := redfish.VirtualMediaCollection{
		OdataContext:      "/redfish/v1/$metadata#VirtualMediaCollection.VirtualMediaCollection",
		OdataID:           fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia", systemName),
		OdataType:         "#VirtualMediaCollection.VirtualMediaCollection",
		Name:              "Virtual Media Collection",
		Members:           members,
		MembersCount:      len(members),
		MembersIdentities: members,
	}

	// Set appropriate cache headers for collections
	s.setCacheHeaders(w, "collection")
	s.sendJSON(w, collection)
}

// handleGetVirtualMedia handles GET requests for virtual media details.
// It returns information about a specific virtual media device for a system.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system
// - mediaID: ID of the virtual media device
func (s *Server) handleGetVirtualMedia(w http.ResponseWriter, r *http.Request, systemName, mediaID string) {
	// Map Cd to cdrom0 for internal operations
	internalMediaID := mediaID
	if mediaID == "Cd" {
		internalMediaID = "cdrom0"
	}

	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Check if media is inserted using the internal media ID
	inserted, err := s.kubevirtClient.IsVirtualMediaInserted(chassisConfig.Namespace, systemName, internalMediaID)
	if err != nil {
		logger.Error("Failed to check virtual media status for VM %s: %v", systemName, err)
		s.sendInternalError(w, "Failed to get virtual media information")
		return
	}

	virtualMedia := redfish.VirtualMedia{
		OdataContext:   "/redfish/v1/$metadata#VirtualMedia.VirtualMedia",
		OdataID:        fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia/%s", systemName, mediaID),
		OdataType:      "#VirtualMedia.v1_0_0.VirtualMedia",
		OdataEtag:      fmt.Sprintf("W/\"%d\"", time.Now().Unix()), // Simple ETag for versioning
		ID:             mediaID,
		Name:           fmt.Sprintf("Virtual Media %s", mediaID),
		MediaTypes:     []string{"CD", "DVD"},
		ConnectedVia:   "Applet",
		Inserted:       inserted,
		WriteProtected: true,
		Actions: redfish.VirtualMediaActions{
			InsertMedia: redfish.InsertMediaAction{
				Target: fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia/%s/Actions/VirtualMedia.InsertMedia", systemName, mediaID),
			},
			EjectMedia: redfish.EjectMediaAction{
				Target: fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia/%s/Actions/VirtualMedia.EjectMedia", systemName, mediaID),
			},
		},
	}

	// Return the resource with proper Redfish headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", virtualMedia.OdataEtag)
	s.setCacheHeaders(w, "resource")
	s.encodeJSONResponse(w, virtualMedia)
}

// handleInsertVirtualMedia handles POST requests to insert virtual media.
// It mounts an ISO image to a virtual machine and returns a Task resource for monitoring.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system
// - mediaID: ID of the virtual media device
func (s *Server) handleInsertVirtualMedia(w http.ResponseWriter, r *http.Request, systemName, mediaID string) {
	// Map Cd to cdrom0 for internal operations
	internalMediaID := mediaID
	if mediaID == "Cd" {
		internalMediaID = "cdrom0"
	}

	// Parse the request body
	var insertRequest redfish.InsertMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&insertRequest); err != nil {
		s.sendValidationError(w, "Invalid request body", err.Error())
		return
	}

	if insertRequest.Image == "" {
		s.sendValidationError(w, "Image URL is required", "The 'Image' field must be provided in the request body.")
		return
	}

	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Create a task for this operation
	taskName := fmt.Sprintf("Insert Media %s for VM %s", mediaID, systemName)
	taskID := s.taskManager.CreateTask(taskName, chassisConfig.Namespace, systemName, internalMediaID, insertRequest.Image)

	// Return the task resource with 202 Accepted status
	task, exists := s.taskManager.GetTask(taskID)
	if !exists {
		logger.Error("Task %s not found after creation", taskID)
		s.sendInternalError(w, "Failed to create task")
		return
	}

	// Convert task to Redfish format
	redfishTask := task.ToRedfishTask()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	s.setCacheHeaders(w, "action")
	s.encodeJSONResponse(w, redfishTask)

	// Invalidate related cache entries after virtual media insertion
	s.responseCache.Invalidate(fmt.Sprintf("Systems/%s/VirtualMedia", systemName))
	s.responseCache.Invalidate(fmt.Sprintf("Systems/%s", systemName))
	logger.Debug("Invalidated cache for system %s virtual media after insertion", systemName)
}

// handleEjectVirtualMedia handles POST requests to eject virtual media.
// It unmounts an ISO image from a virtual machine.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system
// - mediaID: ID of the virtual media device
func (s *Server) handleEjectVirtualMedia(w http.ResponseWriter, r *http.Request, systemName, mediaID string) {
	// Map Cd to cdrom0 for internal operations
	internalMediaID := mediaID
	if mediaID == "Cd" {
		internalMediaID = "cdrom0"
	}

	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Eject virtual media using the internal media ID
	err := s.kubevirtClient.EjectVirtualMedia(chassisConfig.Namespace, systemName, internalMediaID)
	if err != nil {
		logger.Error("Failed to eject virtual media for VM %s: %v", systemName, err)
		s.sendInternalError(w, "Failed to eject virtual media")
		return
	}

	// Return success response with proper Redfish format
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	s.setCacheHeaders(w, "action")
	s.encodeJSONResponse(w, map[string]interface{}{
		"@odata.context": "/redfish/v1/$metadata#ActionResponse.ActionResponse",
		"@odata.type":    "#ActionResponse.v1_0_0.ActionResponse",
		"@odata.id":      fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia/%s/Actions/VirtualMedia.EjectMedia", systemName, mediaID),
		"Id":             "EjectMedia",
		"Name":           "Eject Media Action",
		"Status": map[string]string{
			"State":  "Completed",
			"Health": "OK",
		},
		"Messages": []map[string]string{
			{
				"Message": "Virtual media ejected successfully",
			},
		},
	})

	// Invalidate related cache entries after virtual media ejection
	s.responseCache.Invalidate(fmt.Sprintf("Systems/%s/VirtualMedia", systemName))
	s.responseCache.Invalidate(fmt.Sprintf("Systems/%s", systemName))
	logger.Debug("Invalidated cache for system %s virtual media after ejection", systemName)
}

// handleBootUpdate handles PATCH requests to update boot configuration.
// It allows setting boot source override options including CD boot.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system
func (s *Server) handleBootUpdate(w http.ResponseWriter, r *http.Request, systemName string) {
	// Log incoming boot update request for monitoring
	logger.Info("Received PATCH boot update request for VM %s from %s", systemName, r.RemoteAddr)
	logger.Debug("Boot update request headers: %v", r.Header)

	// Parse the request body
	var bootUpdate map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&bootUpdate); err != nil {
		logger.Error("Failed to parse boot update request body for VM %s: %v", systemName, err)
		s.sendValidationError(w, "Invalid request body", err.Error())
		return
	}

	// Log the boot update payload for debugging
	logger.Info("Boot update payload for VM %s: %+v", systemName, bootUpdate)

	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Extract boot configuration from request
	bootConfig := make(map[string]interface{})

	// Check if Boot field exists in the request
	if bootData, found := bootUpdate["Boot"]; found {
		if bootMap, ok := bootData.(map[string]interface{}); ok {
			if bootSourceOverrideEnabled, found := bootMap["BootSourceOverrideEnabled"]; found {
				bootConfig["bootSourceOverrideEnabled"] = bootSourceOverrideEnabled
			}
			if bootSourceOverrideTarget, found := bootMap["BootSourceOverrideTarget"]; found {
				bootConfig["bootSourceOverrideTarget"] = bootSourceOverrideTarget
			}
			if bootSourceOverrideMode, found := bootMap["BootSourceOverrideMode"]; found {
				bootConfig["bootSourceOverrideMode"] = bootSourceOverrideMode
			}
		}
	} else {
		// Fallback: check for direct fields (for backward compatibility)
		if bootSourceOverrideEnabled, found := bootUpdate["BootSourceOverrideEnabled"]; found {
			bootConfig["bootSourceOverrideEnabled"] = bootSourceOverrideEnabled
		}
		if bootSourceOverrideTarget, found := bootUpdate["BootSourceOverrideTarget"]; found {
			bootConfig["bootSourceOverrideTarget"] = bootSourceOverrideTarget
		}
		if bootSourceOverrideMode, found := bootUpdate["BootSourceOverrideMode"]; found {
			bootConfig["bootSourceOverrideMode"] = bootSourceOverrideMode
		}
	}

	// Update boot configuration
	err := s.kubevirtClient.SetVMBootOptions(chassisConfig.Namespace, systemName, bootConfig)
	if err != nil {
		logger.Error("Failed to update boot configuration for VM %s: %v", systemName, err)
		s.sendInternalError(w, "Failed to update boot configuration")
		return
	}

	// If boot target is CD, also set boot order
	if bootTarget, found := bootConfig["bootSourceOverrideTarget"]; found {
		if target, ok := bootTarget.(string); ok && target == "Cd" {
			// Use a recover mechanism to prevent panics from crashing the server
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("Panic recovered in SetBootOrder for VM %s: %v", systemName, r)
						// Don't fail the operation if boot order setting panics
					}
				}()

				err = s.kubevirtClient.SetBootOrder(chassisConfig.Namespace, systemName, target)
				if err != nil {
					logger.Error("Failed to set boot order for VM %s: %v", systemName, err)
					// Don't fail the operation if boot order setting fails
				}
			}()
		}
	}

	// Get updated boot options to return in response
	bootOptions, err := s.kubevirtClient.GetVMBootOptions(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Error("Failed to get updated boot options for VM %s: %v", systemName, err)
		bootOptions = map[string]interface{}{
			"bootSourceOverrideEnabled": "Disabled",
			"bootSourceOverrideTarget":  "None",
			"bootSourceOverrideMode":    "UEFI",
		}
	}

	// Get real power state
	powerState, err := s.kubevirtClient.GetVMPowerState(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Error("Failed to get power state for VM %s: %v", systemName, err)
		powerState = "Unknown"
	}

	// Get real memory information
	memoryGB, err := s.kubevirtClient.GetVMMemory(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Warning("Failed to get memory for VM %s: %v", systemName, err)
		memoryGB = 2.0 // Low default fallback
	}

	// Get real CPU information
	cpuCount, err := s.kubevirtClient.GetVMCPU(chassisConfig.Namespace, systemName)
	if err != nil {
		logger.Warning("Failed to get CPU for VM %s: %v", systemName, err)
		cpuCount = 1 // Low default fallback
	}

	// Return the updated ComputerSystem resource (Redfish spec requirement)
	system := redfish.ComputerSystem{
		OdataContext: "/redfish/v1/$metadata#ComputerSystem.ComputerSystem",
		OdataID:      fmt.Sprintf("/redfish/v1/Systems/%s", systemName),
		OdataType:    "#ComputerSystem.v1_0_0.ComputerSystem",
		OdataEtag:    fmt.Sprintf("W/\"%d\"", time.Now().Unix()), // Simple ETag for versioning
		ID:           systemName,
		Name:         systemName,
		SystemType:   "Virtual",
		Status: redfish.Status{
			State:  "Enabled",
			Health: "OK",
		},
		PowerState: powerState,
		Memory: redfish.MemorySummary{
			OdataID:              fmt.Sprintf("/redfish/v1/Systems/%s/Memory", systemName),
			TotalSystemMemoryGiB: memoryGB,
		},
		ProcessorSummary: redfish.ProcessorSummary{
			Count: cpuCount,
		},
		Storage: redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/Storage", systemName),
		},
		EthernetInterfaces: redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/EthernetInterfaces", systemName),
		},
		VirtualMedia: redfish.Link{
			OdataID: fmt.Sprintf("/redfish/v1/Systems/%s/VirtualMedia", systemName),
		},
		Boot: redfish.Boot{
			BootSourceOverrideEnabled:               bootOptions["bootSourceOverrideEnabled"].(string),
			BootSourceOverrideTarget:                bootOptions["bootSourceOverrideTarget"].(string),
			BootSourceOverrideTargetAllowableValues: []string{"Cd", "Hdd"},
			BootSourceOverrideMode:                  bootOptions["bootSourceOverrideMode"].(string),
			UefiTargetBootSourceOverride:            "/0x31/0x33/0x01/0x01",
		},
		Actions: redfish.Actions{
			Reset: redfish.ResetAction{
				Target: fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", systemName),
				ResetType: []string{
					"On", "ForceOff", "GracefulShutdown", "ForceRestart", "GracefulRestart", "Pause", "Resume",
				},
			},
		},
		Links: redfish.SystemLinks{
			ManagedBy: []redfish.Link{
				{
					OdataID: "/redfish/v1/Managers/1",
				},
			},
		},
	}

	// Return the updated resource with proper headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", system.OdataEtag)
	s.setCacheHeaders(w, "resource")
	w.WriteHeader(http.StatusOK)
	s.encodeJSONResponse(w, system)

	// Invalidate related cache entries
	s.responseCache.Invalidate(fmt.Sprintf("Systems/%s", systemName))
	s.responseCache.Invalidate("Systems") // Invalidate systems collection
	logger.Debug("Invalidated cache for system %s after boot update", systemName)
}

// handleManager handles individual manager endpoints.
// It returns details of a specific manager that manages the systems.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleManager(w http.ResponseWriter, r *http.Request) {
	// Extract manager ID from path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		s.sendNotFound(w, "Invalid manager path")
		return
	}

	managerID := pathParts[3]
	if managerID == "" {
		s.sendNotFound(w, "Manager ID required")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	// Get user and their accessible systems
	user := auth.GetUser(r)
	var managerForSystems []map[string]string

	// Build dynamic ManagerForSystems links based on user's accessible chassis
	for _, chassisName := range user.Chassis {
		config, err := s.config.GetChassisByName(chassisName)
		if err != nil {
			continue
		}

		vms, err := s.kubevirtClient.ListVMsWithSelector(config.Namespace, config.VMSelector)
		if err != nil {
			logger.Error("Failed to list VMs for chassis %s: %v", chassisName, err)
			continue
		}

		for _, vmName := range vms {
			managerForSystems = append(managerForSystems, map[string]string{
				"@odata.id": fmt.Sprintf("/redfish/v1/Systems/%s", vmName),
			})
		}
	}

	// Return a basic manager resource
	manager := map[string]interface{}{
		"@odata.context": "/redfish/v1/$metadata#Manager.Manager",
		"@odata.id":      fmt.Sprintf("/redfish/v1/Managers/%s", managerID),
		"@odata.type":    "#Manager.v1_0_0.Manager",
		"@odata.etag":    fmt.Sprintf("W/\"%d\"", time.Now().Unix()), // Simple ETag for versioning
		"Id":             managerID,
		"Name":           "KubeVirt Manager",
		"ManagerType":    "Service",
		"Status": map[string]string{
			"State":  "Enabled",
			"Health": "OK",
		},
		"Links": map[string]interface{}{
			"ManagerForSystems": managerForSystems,
		},
	}

	// Return the manager resource with proper headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", manager["@odata.etag"].(string))
	s.setCacheHeaders(w, "resource")
	s.encodeJSONResponse(w, manager)
}

// handleTask handles task endpoints for asynchronous operations.
// It provides task status and management for long-running operations.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 5 {
		s.sendNotFound(w, "Invalid task path")
		return
	}

	taskID := pathParts[4]
	if taskID == "" {
		s.sendNotFound(w, "Task ID required")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	// Get task from enhanced task manager
	task, exists := s.taskManager.GetTask(taskID)

	if !exists {
		s.sendNotFound(w, "Task not found")
		return
	}

	// Convert to Redfish Task and return
	redfishTask := task.ToRedfishTask()

	// Return the task resource with proper headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", redfishTask.OdataEtag)
	s.setCacheHeaders(w, "task")
	s.encodeJSONResponse(w, redfishTask)
}

// handlePowerAction handles power management actions for a computer system.
// It processes POST requests to /redfish/v1/Systems/{systemId}/Actions/ComputerSystem.Reset
// and performs the requested power operation on the virtual machine.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - systemName: Name of the system to perform the action on
func (s *Server) handlePowerAction(w http.ResponseWriter, r *http.Request, systemName string) {
	// Parse the request body
	var resetRequest redfish.ResetRequest
	if err := json.NewDecoder(r.Body).Decode(&resetRequest); err != nil {
		s.sendValidationError(w, "Invalid request body", err.Error())
		return
	}

	// Find the VM across all accessible chassis
	var vmFound bool
	var chassisName string
	var chassisConfig *config.ChassisConfig

	user := auth.GetUser(r)

	for _, chassis := range user.Chassis {
		config, err := s.config.GetChassisByName(chassis)
		if err != nil {
			continue
		}

		_, err = s.kubevirtClient.GetVM(config.Namespace, systemName)
		if err == nil {
			vmFound = true
			chassisName = chassis
			chassisConfig = config
			break
		}
	}

	if !vmFound {
		s.sendNotFound(w, "System not found")
		return
	}

	// Check if user has access to this chassis
	if !auth.HasChassisAccess(r, chassisName) {
		s.sendForbidden(w, "Access denied to system")
		return
	}

	// Map Redfish reset types to KubeVirt power states
	// Pass the actual ResetType to the client for proper handling
	var powerState string
	switch resetRequest.ResetType {
	case "On":
		powerState = "On"
	case "ForceOff":
		powerState = "ForceOff"
	case "GracefulShutdown":
		powerState = "GracefulShutdown"
	case "ForceRestart":
		powerState = "ForceRestart"
	case "GracefulRestart":
		powerState = "GracefulRestart"
	case "Pause":
		powerState = "Pause"
	case "Resume":
		powerState = "Resume"
	default:
		s.sendValidationError(w, "Unsupported reset type", fmt.Sprintf("Reset type '%s' is not supported. Supported types: On, ForceOff, GracefulShutdown, ForceRestart, GracefulRestart, Pause, Resume", resetRequest.ResetType))
		return
	}

	// Execute the power action
	err := s.kubevirtClient.SetVMPowerState(chassisConfig.Namespace, systemName, powerState)
	if err != nil {
		logger.Error("Failed to set power state for VM %s: %v", systemName, err)
		s.sendInternalError(w, fmt.Sprintf("Failed to execute power action: %v", err))
		return
	}

	// Return success response with proper Redfish format
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	s.setCacheHeaders(w, "action")
	s.encodeJSONResponse(w, map[string]interface{}{
		"@odata.context": "/redfish/v1/$metadata#ActionResponse.ActionResponse",
		"@odata.type":    "#ActionResponse.v1_0_0.ActionResponse",
		"@odata.id":      fmt.Sprintf("/redfish/v1/Systems/%s/Actions/ComputerSystem.Reset", systemName),
		"Id":             "Reset",
		"Name":           "Reset Action",
		"Status": map[string]string{
			"State":  "Completed",
			"Health": "OK",
		},
		"Messages": []map[string]string{
			{
				"Message": fmt.Sprintf("Power action %s executed successfully", resetRequest.ResetType),
			},
		},
	})

	// Invalidate related cache entries after power state change
	s.responseCache.Invalidate(fmt.Sprintf("Systems/%s", systemName))
	s.responseCache.Invalidate("Systems") // Invalidate systems collection
	logger.Debug("Invalidated cache for system %s after power action %s", systemName, resetRequest.ResetType)
}

// handleMetrics handles the performance metrics endpoint
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/internal/metrics" {
		s.sendNotFound(w, "Metrics endpoint not found")
		return
	}

	// Validate HTTP method
	if !s.validateMethod(w, r, []string{"GET"}) {
		return
	}

	// Get performance metrics from KubeVirt client
	metrics := s.kubevirtClient.GetPerformanceMetrics()

	// Get cache statistics
	cacheStats := s.responseCache.GetStats()

	// Get task manager statistics
	taskManagerStats := s.taskManager.GetStats()

	// Get job scheduler statistics
	schedulerStats := s.jobScheduler.GetStats()

	// Get memory manager statistics
	memoryStats := s.memoryManager.GetStats()

	// Get connection manager statistics
	connectionStats := s.connectionManager.GetStats()

	// Get memory monitor statistics
	memoryMonitorStats := s.memoryMonitor.GetStats()
	memoryAlerts := s.memoryMonitor.GetAlerts()

	// Get advanced cache statistics
	advancedCacheStats := s.advancedCache.GetStats()

	// Get response optimizer statistics
	responseOptimizerStats := s.responseOptimizer.GetStats()

	// Get response cache optimizer statistics
	responseCacheOptimizerStats := s.responseCacheOptimizer.GetStats()

	// Get circuit breaker statistics
	circuitBreakerStats := s.circuitBreakerManager.GetStats()

	// Get retry mechanism statistics
	retryStats := s.retryManager.GetStats()

	// Get rate limiter statistics
	rateLimitStats := s.rateLimitManager.GetStats()

	// Get health checker statistics
	healthStats := s.healthChecker.GetStats()

	// Add server information
	response := map[string]interface{}{
		"server": map[string]interface{}{
			"uptime": time.Since(s.startTime).String(),
		},
		"kubevirt_client":          metrics,
		"response_cache":           cacheStats,
		"task_manager":             taskManagerStats,
		"job_scheduler":            schedulerStats,
		"memory_manager":           memoryStats,
		"connection_manager":       connectionStats,
		"memory_monitor":           memoryMonitorStats,
		"memory_alerts":            memoryAlerts,
		"advanced_cache":           advancedCacheStats,
		"response_optimizer":       responseOptimizerStats,
		"response_cache_optimizer": responseCacheOptimizerStats,
		"circuit_breakers":         circuitBreakerStats,
		"retry_mechanisms":         retryStats,
		"rate_limiters":            rateLimitStats,
		"health_checker":           healthStats,
	}

	// Return metrics with no-cache headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	s.encodeJSONResponse(w, response)
}

// validateMethod validates that the HTTP method is supported for the given endpoint.
// It returns true if the method is valid, false otherwise.
// If the method is not valid, it sends an appropriate error response.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
// - allowedMethods: List of allowed HTTP methods for this endpoint
//
// Returns:
// - bool: True if method is valid, false otherwise
func (s *Server) validateMethod(w http.ResponseWriter, r *http.Request, allowedMethods []string) bool {
	for _, method := range allowedMethods {
		if r.Method == method {
			return true
		}
	}

	// Method not allowed - return 405 Method Not Allowed
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Allow", strings.Join(allowedMethods, ", "))
	w.WriteHeader(http.StatusMethodNotAllowed)

	errorResponse := redfish.Error{
		Error: redfish.ErrorInfo{
			Code:    redfish.ErrorCodeGeneralError,
			Message: fmt.Sprintf("Method %s not allowed", r.Method),
			ExtendedInfo: []redfish.ExtendedInfo{
				{
					MessageID:  "Base.1.0.GeneralError",
					Message:    fmt.Sprintf("Method %s not allowed", r.Method),
					Severity:   "Error",
					Resolution: fmt.Sprintf("Use one of the allowed methods: %s", strings.Join(allowedMethods, ", ")),
				},
			},
		},
	}

	s.encodeJSONResponse(w, errorResponse)
	return false
}

// sendJSON sends a JSON response with the specified status code.
// It sets appropriate headers and serializes the response data.
//
// Parameters:
// - w: HTTP response writer
// - data: Data to serialize as JSON
func (s *Server) sendJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	s.encodeJSONResponse(w, data)
}

// sendOptimizedJSON sends a JSON response with optimized serialization.
// It uses the memory manager for pooled resources and response optimizer for compression.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request (for compression negotiation)
// - data: Data to serialize as JSON
func (s *Server) sendOptimizedJSON(w http.ResponseWriter, r *http.Request, data interface{}) {
	// Set content type header
	w.Header().Set("Content-Type", "application/json")

	// In test mode, use standard JSON marshaling
	if s.config.Server.TestMode || s.memoryManager == nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			logger.Error("Failed to marshal JSON response: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
		if _, err := w.Write(jsonData); err != nil {
			logger.Error("Failed to write JSON response: %v", err)
		}
		return
	}

	// Use optimized JSON marshaling from memory manager
	jsonData, err := s.memoryManager.OptimizedJSONMarshal(data)
	if err != nil {
		logger.Error("Failed to marshal JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Use response optimizer for compression
	if s.responseOptimizer != nil {
		if err := s.responseOptimizer.OptimizeResponse(w, r, jsonData); err != nil {
			logger.Error("Failed to optimize response: %v", err)
			// Fallback to uncompressed response
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
			if _, err := w.Write(jsonData); err != nil {
				logger.Error("Failed to write fallback JSON response: %v", err)
			}
		}
	} else {
		// Fallback to uncompressed response
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
		if _, err := w.Write(jsonData); err != nil {
			logger.Error("Failed to write uncompressed JSON response: %v", err)
		}
	}
}

// setCacheHeaders sets appropriate Cache-Control headers based on resource type.
// It helps clients understand how to cache different types of resources.
//
// Parameters:
// - w: HTTP response writer
// - resourceType: Type of resource (e.g., "collection", "resource", "task")
func (s *Server) setCacheHeaders(w http.ResponseWriter, resourceType string) {
	switch resourceType {
	case "collection":
		// Collections can be cached for a short time
		w.Header().Set("Cache-Control", "public, max-age=30")
	case "resource":
		// Individual resources can be cached longer but should revalidate
		w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
	case "task":
		// Tasks should not be cached as they change frequently
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	case "action":
		// Action responses should not be cached
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	default:
		// Default: moderate caching with revalidation
		w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
	}
}

// sendRedfishError sends a Redfish error response with proper logging
func (s *Server) sendRedfishError(w http.ResponseWriter, r *http.Request, err error) {
	// Get correlation ID from context
	var correlationID string
	if r != nil {
		correlationID = logger.GetCorrelationID(r.Context())
	}
	if correlationID == "" {
		correlationID = "unknown"
	}

	// Log the error with structured logging
	errors.LogError(err, correlationID)

	// Get HTTP status code from error
	statusCode := errors.GetHTTPStatus(err)

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	if statusCode == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="KubeVirt Redfish API"`)
	}
	w.WriteHeader(statusCode)

	// Create Redfish error response
	var errorCode string
	var resolution string

	switch statusCode {
	case http.StatusBadRequest:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "Check the request format and parameters."
	case http.StatusUnauthorized:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "Provide valid authentication credentials."
	case http.StatusForbidden:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "Contact the system administrator for access permissions."
	case http.StatusNotFound:
		errorCode = redfish.ErrorCodeResourceNotFound
		resolution = "Check the resource URI and try again."
	case http.StatusConflict:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "The resource is in a conflicting state."
	case http.StatusRequestTimeout:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "The operation timed out. Try again later."
	case http.StatusInternalServerError:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "Contact the system administrator for assistance."
	default:
		errorCode = redfish.ErrorCodeGeneralError
		resolution = "An unexpected error occurred."
	}

	// Extract error message
	var message string
	if redfishErr, ok := err.(*errors.RedfishError); ok {
		message = redfishErr.Message
	} else {
		message = err.Error()
	}

	errorResponse := redfish.Error{
		Error: redfish.ErrorInfo{
			Code:    errorCode,
			Message: message,
			ExtendedInfo: []redfish.ExtendedInfo{
				{
					MessageID:  "Base.1.0.GeneralError",
					Message:    message,
					Severity:   "Error",
					Resolution: resolution,
				},
			},
		},
	}

	s.encodeJSONResponse(w, errorResponse)
}

// sendNotFound sends a 404 Not Found response.
// It provides a standardized error response for missing resources.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (s *Server) sendNotFound(w http.ResponseWriter, message string) {
	err := errors.NewNotFoundError("Resource", message)
	s.sendRedfishError(w, nil, err)
}

// sendUnauthorized sends a 401 Unauthorized response.
// It provides a standardized error response for authentication failures.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (s *Server) sendUnauthorized(w http.ResponseWriter, message string) {
	err := errors.NewAuthenticationError(message)
	s.sendRedfishError(w, nil, err)
}

// sendForbidden sends a 403 Forbidden response.
// It provides a standardized error response for authorization failures.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (s *Server) sendForbidden(w http.ResponseWriter, message string) {
	err := errors.NewAuthorizationError(message)
	s.sendRedfishError(w, nil, err)
}

// sendInternalError sends a 500 Internal Server Error response.
// It provides a standardized error response for server errors.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
func (s *Server) sendInternalError(w http.ResponseWriter, message string) {
	err := errors.NewInternalError(message, stderrors.New(message))
	s.sendRedfishError(w, nil, err)
}

// sendValidationError sends a 400 Bad Request response.
// It provides a standardized error response for validation failures.
//
// Parameters:
// - w: HTTP response writer
// - message: Error message to include in response
// - details: Additional error details
func (s *Server) sendValidationError(w http.ResponseWriter, message, details string) {
	err := errors.NewValidationError(message, details)
	s.sendRedfishError(w, nil, err)
}

// sendConflictError sends a 409 Conflict response.
// It provides a standardized error response for resource conflicts.
//
// Parameters:
// - w: HTTP response writer
// - resource: Resource name that has a conflict
// - details: Additional error details
func (s *Server) sendConflictError(w http.ResponseWriter, resource, details string) {
	err := errors.NewConflictError(resource, details)
	s.sendRedfishError(w, nil, err)
}

// sendJSONResponse sends a JSON response with proper headers and status code.
// It uses the memory manager for optimized JSON marshaling.
func (s *Server) sendJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")

	// In test mode, use standard JSON marshaling
	if s.config.Server.TestMode || s.memoryManager == nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			logger.Error("Failed to marshal JSON response: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
		w.WriteHeader(statusCode)
		if _, err := w.Write(jsonData); err != nil {
			logger.Error("Failed to write JSON response: %v", err)
		}
		return
	}

	// Use optimized JSON marshaling from memory manager
	jsonData, err := s.memoryManager.OptimizedJSONMarshal(data)
	if err != nil {
		logger.Error("Failed to marshal JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(jsonData)))
	w.WriteHeader(statusCode)
	if _, err := w.Write(jsonData); err != nil {
		logger.Error("Failed to write optimized JSON response: %v", err)
	}
}

// encodeJSONResponse safely encodes JSON data to the response writer
func (s *Server) encodeJSONResponse(w http.ResponseWriter, data interface{}) {
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
