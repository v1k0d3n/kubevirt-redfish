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

// Package main provides the entry point for the KubeVirt Redfish API server.
// It handles application initialization, configuration loading, and server startup.
//
// The main package provides:
// - Command-line argument parsing
// - Configuration file loading
// - KubeVirt client initialization
// - HTTP server startup and shutdown
// - Signal handling for graceful termination
//
// The application supports both in-cluster and external kubeconfig configurations
// and provides comprehensive logging and error handling.
//
// Example usage:
//
//	# Run with default configuration
//	./kubevirt-redfish
//
//	# Run with custom config file
//	./kubevirt-redfish --config /path/to/config.yaml
//
//	# Run with external kubeconfig
//	./kubevirt-redfish --kubeconfig /path/to/kubeconfig
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/kubevirt"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/server"
)

// Version information for the application.
// These values are set during the build process.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// main is the entry point for the KubeVirt Redfish API server.
// It initializes the application, loads configuration, starts the server,
// and handles graceful shutdown on termination signals.
func main() {
	// Parse command-line flags
	configPath := flag.String("config", "", "Path to configuration file")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (for external cluster access)")
	showVersion := flag.Bool("version", false, "Show version information")
	createConfig := flag.String("create-config", "", "Create a default configuration file at the specified path")

	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("KubeVirt Redfish API Server\n")
		fmt.Printf("Version: %s\n", Version)
		fmt.Printf("Git Commit: %s\n", GitCommit)
		fmt.Printf("Build Date: %s\n", BuildDate)
		os.Exit(0)
	}

	// Handle create-config flag
	if *createConfig != "" {
		if err := config.CreateDefaultConfig(*createConfig); err != nil {
			log.Fatalf("Failed to create default config: %v", err)
		}
		fmt.Printf("Default configuration created at: %s\n", *createConfig)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create KubeVirt client
	kubevirtClient, err := kubevirt.NewClient(*kubeconfig, 30*time.Second, cfg)
	if err != nil {
		log.Fatalf("Failed to create KubeVirt client: %v", err)
	}

	// Initialize logger
	logLevel := logger.GetLogLevelFromEnv()
	if logger.IsLoggingEnabled() {
		logger.Init(logLevel)
		logger.Info("KubeVirt Redfish server started with log level: %s", logLevel)
	} else {
		logger.Info("Logging disabled via REDFISH_LOGGING_ENABLED=false")
	}

	// Create and start server
	srv := server.NewServer(cfg, kubevirtClient)

	// Start server in a goroutine
	go func() {
		log.Printf("Starting KubeVirt Redfish API server on %s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := srv.Start(); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Start config file watcher for hot-reload
	if *configPath != "" {
		go watchConfigFile(*configPath, srv)
	}

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown
	if err := srv.Shutdown(); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Server stopped")
}

// watchConfigFile watches the config file for changes and hot-reloads configuration.
// Parameters:
// - configPath: Path to the config file to watch
// - srv: Pointer to the running Server instance
func watchConfigFile(configPath string, srv *server.Server) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[WARN] Failed to initialize config watcher: %v", err)
		return
	}
	defer watcher.Close()

	dir, _ := filepath.Split(configPath)
	if err := watcher.Add(dir); err != nil {
		log.Printf("[WARN] Failed to watch config directory: %v", err)
		return
	}

	log.Printf("Watching config file for changes: %s", configPath)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 && event.Name == configPath {
				log.Printf("Config file changed: %s, reloading...", event.Name)
				newConfig, err := config.LoadConfig(configPath)
				if err != nil {
					log.Printf("[WARN] Failed to reload config: %v", err)
					continue
				}
				srv.UpdateConfig(newConfig)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[WARN] Config watcher error: %v", err)
		}
	}
}

// printUsage prints the application usage information.
// It displays command-line options and examples for common usage patterns.
func printUsage() {
	fmt.Printf("KubeVirt Redfish API Server v%s\n\n", Version)
	fmt.Printf("Usage: kubevirt-redfish [options]\n\n")
	fmt.Printf("Options:\n")
	fmt.Printf("  --config <path>        Path to configuration file\n")
	fmt.Printf("  --kubeconfig <path>    Path to kubeconfig file (for external cluster access)\n")
	fmt.Printf("  --version              Show version information\n")
	fmt.Printf("  --create-config <path> Create a default configuration file\n")
	fmt.Printf("  --help                 Show this help message\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  # Run with default configuration\n")
	fmt.Printf("  kubevirt-redfish\n\n")
	fmt.Printf("  # Run with custom config file\n")
	fmt.Printf("  kubevirt-redfish --config /etc/kubevirt-redfish/config.yaml\n\n")
	fmt.Printf("  # Run with external kubeconfig\n")
	fmt.Printf("  kubevirt-redfish --kubeconfig ~/.kube/config\n\n")
	fmt.Printf("  # Create default configuration\n")
	fmt.Printf("  kubevirt-redfish --create-config config.yaml\n")
}
