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

// Package config provides configuration management for the KubeVirt Redfish API server.
// It handles loading configuration from YAML files, environment variables, and provides
// validation for chassis configurations, authentication settings, and server parameters.
//
// The configuration system supports:
// - Multiple chassis configurations for namespace isolation
// - User authentication with chassis-based access control
// - Server settings including TLS configuration
// - KubeVirt-specific configuration parameters
//
// Example configuration:
//
//	server:
//	  host: "0.0.0.0"
//	  port: 8443
//	  tls:
//	    enabled: false
//
//	chassis:
//	  - name: "production-cluster"
//	    namespace: "kubevirt-redfish"
//	    service_account: "redfish-sa"
//	    description: "Production KubeVirt cluster"
//
//	authentication:
//	  users:
//	    - username: "admin"
//	      password: "admin123"
//	      chassis: ["production-cluster"]
//
//	kubevirt:
//	  api_version: "v1"
//	  timeout: 30
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/errors"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/kubevirt"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/logger"
)

// Config represents the complete application configuration structure.
// It contains all settings for the Redfish API server including server,
// chassis, authentication, and KubeVirt-specific configurations.
type Config struct {
	Server             ServerConfig     `mapstructure:"server"`
	Chassis            []ChassisConfig  `mapstructure:"chassis"`
	Auth               AuthConfig       `mapstructure:"authentication"`
	KubeVirt           KubeVirtConfig   `mapstructure:"kubevirt"`
	CDI                CDIConfig        `mapstructure:"cdi"`
	DataVolume         DataVolumeConfig `mapstructure:"datavolume"`
	SystemIDConvention string           `mapstructure:"system_id_convention"` // "legacy" or "enhanced"
}

// ServerConfig holds HTTP server configuration including host, port, and TLS settings.
// The server configuration determines how the Redfish API server listens for requests.
type ServerConfig struct {
	Host     string    `mapstructure:"host"`
	Port     int       `mapstructure:"port"`
	TLS      TLSConfig `mapstructure:"tls"`
	TestMode bool      `mapstructure:"test_mode"` // Disables background processes for testing
}

// TLSConfig holds TLS configuration for secure HTTPS connections.
// When enabled, the server will serve HTTPS traffic using the specified certificate files.
type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

// ChassisConfig represents a Redfish chassis configuration that maps to a Kubernetes namespace.
// Each chassis provides namespace isolation and can contain multiple virtual machines.
// The chassis name is used in Redfish URIs like /redfish/v1/Chassis/{chassis-name}.
type ChassisConfig struct {
	Name           string                     `mapstructure:"name"`
	Namespace      string                     `mapstructure:"namespace"`
	ServiceAccount string                     `mapstructure:"service_account"`
	Description    string                     `mapstructure:"description"`
	VMSelector     *kubevirt.VMSelectorConfig `mapstructure:"vm_selector"`
}

// AuthConfig holds authentication configuration for Redfish API access.
// It defines users and their access permissions to specific chassis.
type AuthConfig struct {
	Users []UserConfig `mapstructure:"users"`
}

// UserConfig represents a Redfish user with authentication credentials and chassis access.
// Users can be granted access to specific chassis, providing namespace-based access control.
type UserConfig struct {
	Username string   `mapstructure:"username"`
	Password string   `mapstructure:"password"`
	Chassis  []string `mapstructure:"chassis"`
}

// KubeVirtConfig holds KubeVirt-specific configuration parameters.
// It includes API version settings, timeout configurations, and TLS settings for KubeVirt operations.
type KubeVirtConfig struct {
	APIVersion       string `mapstructure:"api_version"`
	Timeout          int    `mapstructure:"timeout"`
	AllowInsecureTLS bool   `mapstructure:"allow_insecure_tls"`
}

// CDIConfig holds CDI (Containerized Data Importer) configuration parameters.
// It includes upload proxy service discovery settings and connection parameters.
type CDIConfig struct {
	UploadProxy CDIUploadProxyConfig `mapstructure:"upload_proxy"`
}

// CDIUploadProxyConfig holds CDI upload proxy service configuration.
// It defines how to discover and connect to the CDI upload proxy service.
type CDIUploadProxyConfig struct {
	ServiceName string   `mapstructure:"service_name"`
	Namespaces  []string `mapstructure:"namespaces"`
	Port        int      `mapstructure:"port"`
	Timeout     int      `mapstructure:"timeout"`
}

// DataVolumeConfig holds configuration for DataVolume operations including ISO imports.
// It includes storage settings, TLS certificate handling, timeout settings, and helper image configuration for ISO imports.
type DataVolumeConfig struct {
	StorageSize        string `mapstructure:"storage_size"`
	AllowInsecureTLS   bool   `mapstructure:"allow_insecure_tls"`
	StorageClass       string `mapstructure:"storage_class"`
	VMUpdateTimeout    string `mapstructure:"vm_update_timeout"`
	ISODownloadTimeout string `mapstructure:"iso_download_timeout"`
	HelperImage        string `mapstructure:"helper_image"`
}

// LoadConfig loads configuration from file and environment variables.
// It supports loading from a specific config file or searching in default locations.
// Environment variables are automatically mapped using the pattern: CONFIG_KEY -> config.key
//
// Supported config file locations (in order of precedence):
// - Explicit config file path (if provided)
// - Current directory (./config.yaml)
// - Config directory (./config/config.yaml)
// - System config directory (/etc/kubevirt-redfish/config.yaml)
//
// Environment variables can override any config value using the pattern:
// KUBEVIRT_REDFISH_SERVER_HOST, KUBEVIRT_REDFISH_SERVER_PORT, etc.
//
// Returns:
// - *Config: Loaded and validated configuration
// - error: Configuration error if loading or validation fails
func LoadConfig(configPath string) (*Config, error) {
	logger.Info("Loading configuration...")

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	if configPath != "" {
		logger.Info("Using explicit config file: %s", configPath)
		viper.SetConfigFile(configPath)
	} else {
		logger.Info("Searching for config file in default locations")
		viper.AddConfigPath(".")
		viper.AddConfigPath("./config")
		viper.AddConfigPath("/etc/kubevirt-redfish")
	}

	// Set default values
	setDefaults()

	// Configure environment variable support
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.SetEnvPrefix("KUBEVIRT_REDFISH")

	// Log environment variable overrides
	logEnvironmentOverrides()

	// Read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, errors.NewInternalError("Failed to read config file", err)
		}
		logger.Warning("No config file found, using defaults and environment variables")
	} else {
		logger.Info("Config file loaded: %s", viper.ConfigFileUsed())
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, errors.NewInternalError("Failed to unmarshal config", err)
	}

	// Validate configuration
	logger.Info("Validating configuration...")
	if err := validateConfig(&config); err != nil {
		return nil, errors.WrapError(err, errors.ErrorTypeValidation, "Configuration validation failed")
	}

	// Log configuration summary
	logConfigurationSummary(&config)

	logger.Info("Configuration loaded successfully")
	return &config, nil
}

// setDefaults sets default configuration values for the application.
// These defaults provide sensible starting points for common deployment scenarios.
func setDefaults() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8443)
	viper.SetDefault("server.tls.enabled", true)
	viper.SetDefault("kubevirt.api_version", "v1")
	viper.SetDefault("kubevirt.timeout", 30)
	viper.SetDefault("kubevirt.allow_insecure_tls", false)
	viper.SetDefault("datavolume.storage_size", "10Gi")
	viper.SetDefault("datavolume.allow_insecure_tls", false)
	viper.SetDefault("datavolume.vm_update_timeout", "30s")
	viper.SetDefault("datavolume.iso_download_timeout", "30m")
	viper.SetDefault("datavolume.helper_image", "registry.access.redhat.com/ubi8/ubi-minimal:latest")
	viper.SetDefault("system_id_convention", "legacy") // Default to legacy for backward compatibility
}

// validateConfig validates the configuration to ensure all required fields are present
// and have valid values. It performs comprehensive checks on all configuration sections
// and provides detailed error messages for troubleshooting.
func validateConfig(config *Config) error {
	var validationErrors []string

	// Validate server configuration
	if err := validateServerConfig(&config.Server); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("server: %v", err))
	}

	// Validate chassis configurations
	if err := validateChassisConfigs(config.Chassis); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("chassis: %v", err))
	}

	// Validate authentication configuration
	if err := validateAuthConfig(&config.Auth, config.Chassis); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("authentication: %v", err))
	}

	// Validate KubeVirt configuration
	if err := validateKubeVirtConfig(&config.KubeVirt); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("kubevirt: %v", err))
	}

	// Validate DataVolume configuration
	if err := validateDataVolumeConfig(&config.DataVolume); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("datavolume: %v", err))
	}

	// Validate System ID convention
	if err := validateSystemIDConvention(config.SystemIDConvention); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("system_id_convention: %v", err))
	}

	// Check for duplicate chassis names
	if err := validateUniqueChassisNames(config.Chassis); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("chassis: %v", err))
	}

	// Check for duplicate usernames
	if err := validateUniqueUsernames(config.Auth.Users); err != nil {
		validationErrors = append(validationErrors, fmt.Sprintf("authentication: %v", err))
	}

	if len(validationErrors) > 0 {
		return errors.NewValidationError("Configuration validation failed", strings.Join(validationErrors, "; "))
	}

	return nil
}

// validateServerConfig validates server configuration settings
func validateServerConfig(server *ServerConfig) error {
	if server.Host == "" {
		return errors.NewValidationError("Server host is required", "server.host cannot be empty")
	}

	if server.Port < 1 || server.Port > 65535 {
		return errors.NewValidationError("Invalid server port", fmt.Sprintf("server.port must be between 1 and 65535, got %d", server.Port))
	}

	if server.TLS.Enabled {
		if server.TLS.CertFile == "" {
			return errors.NewValidationError("TLS certificate file is required when TLS is enabled", "server.tls.cert_file cannot be empty when server.tls.enabled is true")
		}
		if server.TLS.KeyFile == "" {
			return errors.NewValidationError("TLS key file is required when TLS is enabled", "server.tls.key_file cannot be empty when server.tls.enabled is true")
		}
	}

	return nil
}

// validateChassisConfigs validates chassis configuration settings
func validateChassisConfigs(chassis []ChassisConfig) error {
	if len(chassis) == 0 {
		return errors.NewValidationError("At least one chassis configuration is required", "chassis array cannot be empty")
	}

	for i, ch := range chassis {
		if ch.Name == "" {
			return errors.NewValidationError("Chassis name is required", fmt.Sprintf("chassis[%d].name cannot be empty", i))
		}

		// Validate chassis name format (alphanumeric and hyphens only)
		if !isValidChassisName(ch.Name) {
			return errors.NewValidationError("Invalid chassis name format", fmt.Sprintf("chassis[%d].name must contain only alphanumeric characters and hyphens, got '%s'", i, ch.Name))
		}

		if ch.Namespace == "" {
			return errors.NewValidationError("Chassis namespace is required", fmt.Sprintf("chassis[%d].namespace cannot be empty", i))
		}

		// Validate namespace format (RFC 1123)
		if !isValidNamespace(ch.Namespace) {
			return errors.NewValidationError("Invalid namespace format", fmt.Sprintf("chassis[%d].namespace must be a valid Kubernetes namespace name, got '%s'", i, ch.Namespace))
		}

		if ch.ServiceAccount == "" {
			return errors.NewValidationError("Chassis service account is required", fmt.Sprintf("chassis[%d].service_account cannot be empty", i))
		}
	}

	return nil
}

// validateAuthConfig validates authentication configuration settings
func validateAuthConfig(auth *AuthConfig, chassis []ChassisConfig) error {
	if len(auth.Users) == 0 {
		return errors.NewValidationError("At least one user configuration is required", "authentication.users array cannot be empty")
	}

	// Create a map of valid chassis names for quick lookup
	validChassis := make(map[string]bool)
	for _, ch := range chassis {
		validChassis[ch.Name] = true
	}

	for i, user := range auth.Users {
		if user.Username == "" {
			return errors.NewValidationError("Username is required", fmt.Sprintf("authentication.users[%d].username cannot be empty", i))
		}

		if user.Password == "" {
			return errors.NewValidationError("Password is required", fmt.Sprintf("authentication.users[%d].password cannot be empty", i))
		}

		// Validate username format
		if !isValidUsername(user.Username) {
			return errors.NewValidationError("Invalid username format", fmt.Sprintf("authentication.users[%d].username must contain only alphanumeric characters, hyphens, and underscores, got '%s'", i, user.Username))
		}

		// Validate password strength (basic check)
		if len(user.Password) < 6 {
			return errors.NewValidationError("Password is too short", fmt.Sprintf("authentication.users[%d].password must be at least 6 characters long", i))
		}

		// Validate chassis access
		if len(user.Chassis) == 0 {
			return errors.NewValidationError("User must have access to at least one chassis", fmt.Sprintf("authentication.users[%d].chassis array cannot be empty", i))
		}

		for j, chassisName := range user.Chassis {
			if !validChassis[chassisName] {
				return errors.NewValidationError("User has access to non-existent chassis", fmt.Sprintf("authentication.users[%d].chassis[%d] references non-existent chassis '%s'", i, j, chassisName))
			}
		}
	}

	return nil
}

// validateKubeVirtConfig validates KubeVirt configuration settings
func validateKubeVirtConfig(kv *KubeVirtConfig) error {
	if kv.APIVersion == "" {
		return errors.NewValidationError("KubeVirt API version is required", "kubevirt.api_version cannot be empty")
	}

	// Validate API version format
	if !isValidAPIVersion(kv.APIVersion) {
		return errors.NewValidationError("Invalid KubeVirt API version", fmt.Sprintf("kubevirt.api_version must be 'v1', got '%s'", kv.APIVersion))
	}

	if kv.Timeout < 1 || kv.Timeout > 300 {
		return errors.NewValidationError("Invalid KubeVirt timeout", fmt.Sprintf("kubevirt.timeout must be between 1 and 300 seconds, got %d", kv.Timeout))
	}

	return nil
}

// validateDataVolumeConfig validates DataVolume configuration settings
func validateDataVolumeConfig(dv *DataVolumeConfig) error {
	if dv.StorageSize == "" {
		return errors.NewValidationError("DataVolume storage size is required", "datavolume.storage_size cannot be empty")
	}

	// Validate storage size format (Kubernetes resource format)
	if !isValidStorageSize(dv.StorageSize) {
		return errors.NewValidationError("Invalid storage size format", fmt.Sprintf("datavolume.storage_size must be in Kubernetes resource format (e.g., '10Gi'), got '%s'", dv.StorageSize))
	}

	// Validate helper image format
	if dv.HelperImage == "" {
		return errors.NewValidationError("DataVolume helper image is required", "datavolume.helper_image cannot be empty")
	}

	// Validate timeout formats
	if dv.VMUpdateTimeout != "" {
		if _, err := time.ParseDuration(dv.VMUpdateTimeout); err != nil {
			return errors.NewValidationError("Invalid VM update timeout format", fmt.Sprintf("datavolume.vm_update_timeout must be a valid duration (e.g., '30s'), got '%s'", dv.VMUpdateTimeout))
		}
	}

	if dv.ISODownloadTimeout != "" {
		if _, err := time.ParseDuration(dv.ISODownloadTimeout); err != nil {
			return errors.NewValidationError("Invalid ISO download timeout format", fmt.Sprintf("datavolume.iso_download_timeout must be a valid duration (e.g., '30m'), got '%s'", dv.ISODownloadTimeout))
		}
	}

	return nil
}

// validateSystemIDConvention validates the SystemIDConvention field.
func validateSystemIDConvention(convention string) error {
	if convention != "legacy" && convention != "enhanced" {
		return errors.NewValidationError("Invalid System ID convention", fmt.Sprintf("system_id_convention must be 'legacy' or 'enhanced', got '%s'", convention))
	}
	return nil
}

// GenerateSystemID generates the correct System ID based on the convention.
// - legacy: returns the VM name as-is (e.g., "ztp-jinkit-kvm-00")
// - enhanced: returns namespace.vmname (e.g., "jinkit-kvm.ztp-jinkit-kvm-00")
func GenerateSystemID(convention, namespace, vmName string) string {
	switch convention {
	case "enhanced":
		return fmt.Sprintf("%s.%s", namespace, vmName)
	case "legacy":
		fallthrough
	default:
		return vmName
	}
}

// validateUniqueChassisNames ensures all chassis names are unique
func validateUniqueChassisNames(chassis []ChassisConfig) error {
	seen := make(map[string]int)
	for i, ch := range chassis {
		if existingIndex, exists := seen[ch.Name]; exists {
			return errors.NewValidationError("Duplicate chassis name", fmt.Sprintf("chassis[%d].name '%s' duplicates chassis[%d].name", i, ch.Name, existingIndex))
		}
		seen[ch.Name] = i
	}
	return nil
}

// validateUniqueUsernames ensures all usernames are unique
func validateUniqueUsernames(users []UserConfig) error {
	seen := make(map[string]int)
	for i, user := range users {
		if existingIndex, exists := seen[user.Username]; exists {
			return errors.NewValidationError("Duplicate username", fmt.Sprintf("authentication.users[%d].username '%s' duplicates authentication.users[%d].username", i, user.Username, existingIndex))
		}
		seen[user.Username] = i
	}
	return nil
}

// Validation helper functions
func isValidChassisName(name string) bool {
	if len(name) < 1 || len(name) > 63 {
		return false
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return false
		}
	}
	return true
}

func isValidNamespace(name string) bool {
	if len(name) < 1 || len(name) > 63 {
		return false
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return false
		}
	}
	return true
}

func isValidUsername(name string) bool {
	if len(name) < 1 || len(name) > 63 {
		return false
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func isValidAPIVersion(version string) bool {
	return version == "v1"
}

func isValidStorageSize(size string) bool {
	// Basic validation for Kubernetes resource format
	if len(size) < 2 {
		return false
	}

	// Check if it ends with a valid unit
	validUnits := []string{"Ki", "Mi", "Gi", "Ti", "Pi", "Ei", "K", "M", "G", "T", "P", "E"}
	for _, unit := range validUnits {
		if strings.HasSuffix(size, unit) {
			// Check if the prefix is a valid number
			number := strings.TrimSuffix(size, unit)
			if _, err := strconv.Atoi(number); err == nil {
				return true
			}
		}
	}
	return false
}

// GetChassisByName returns a chassis configuration by name.
// It searches through all configured chassis and returns the first match.
// Returns an error if no chassis with the given name is found.
func (c *Config) GetChassisByName(name string) (*ChassisConfig, error) {
	for _, chassis := range c.Chassis {
		if chassis.Name == name {
			return &chassis, nil
		}
	}
	return nil, fmt.Errorf("chassis not found: %s", name)
}

// GetUserByCredentials returns a user configuration by username and password.
// It performs exact matching of both username and password for authentication.
// Returns an error if no matching user is found.
func (c *Config) GetUserByCredentials(username, password string) (*UserConfig, error) {
	for _, user := range c.Auth.Users {
		if user.Username == username && user.Password == password {
			return &user, nil
		}
	}
	return nil, fmt.Errorf("invalid credentials")
}

// GetChassisForUser returns chassis configurations accessible to a user.
// It looks up the user by username and returns all chassis that the user
// has access to. Returns an error if the user is not found or if the user
// has access to non-existent chassis.
func (c *Config) GetChassisForUser(username string) ([]*ChassisConfig, error) {
	var userChassis []*ChassisConfig

	for _, user := range c.Auth.Users {
		if user.Username == username {
			for _, chassisName := range user.Chassis {
				chassis, err := c.GetChassisByName(chassisName)
				if err != nil {
					return nil, fmt.Errorf("user %s has access to non-existent chassis %s: %w", username, chassisName, err)
				}
				userChassis = append(userChassis, chassis)
			}
			return userChassis, nil
		}
	}
	return nil, fmt.Errorf("user not found: %s", username)
}

// GetDataVolumeConfig returns the DataVolume configuration settings.
// This provides a safe way to access DataVolume settings without reflection.
func (c *Config) GetDataVolumeConfig() (storageSize string, allowInsecureTLS bool, storageClass string, vmUpdateTimeout string, isoDownloadTimeout string, helperImage string) {
	return c.DataVolume.StorageSize, c.DataVolume.AllowInsecureTLS, c.DataVolume.StorageClass, c.DataVolume.VMUpdateTimeout, c.DataVolume.ISODownloadTimeout, c.DataVolume.HelperImage
}

// GetKubeVirtConfig returns the KubeVirt configuration settings.
// This provides a safe way to access KubeVirt settings without reflection.
func (c *Config) GetKubeVirtConfig() (apiVersion string, timeout int, allowInsecureTLS bool) {
	return c.KubeVirt.APIVersion, c.KubeVirt.Timeout, c.KubeVirt.AllowInsecureTLS
}

// CreateDefaultConfig creates a default configuration file at the specified path.
// This function generates a complete configuration file with example values
// that can be used as a starting point for customization.
func CreateDefaultConfig(path string) error {
	defaultConfig := `# KubeVirt Redfish Configuration
#
# This configuration file defines the settings for the KubeVirt Redfish API server.
# Each section configures different aspects of the server behavior.
#
# Server Configuration
# -------------------
# The server section defines HTTP server settings including host, port, and TLS.
server:
  host: "0.0.0.0"  # Listen on all interfaces
  port: 8443        # HTTPS port for Redfish API
  tls:
    enabled: true    # Enable TLS for secure connections
    cert_file: "/etc/kubevirt-redfish/tls/tls.crt"
    key_file: "/etc/kubevirt-redfish/tls/tls.key"

# Chassis Configuration
# --------------------
# Chassis provide namespace isolation in the Redfish API.
# Each chassis maps to a Kubernetes namespace and can contain multiple VMs.
chassis:
  - name: "production-cluster"     # Redfish chassis identifier
    namespace: "kubevirt-redfish"  # Kubernetes namespace
    service_account: "redfish-sa"  # K8s service account for RBAC
    description: "Production KubeVirt cluster"
  - name: "development-cluster"
    namespace: "dev-vms"
    service_account: "redfish-dev-sa"
    description: "Development KubeVirt cluster"

# Authentication Configuration
# ---------------------------
# Users define who can access the Redfish API and which chassis they can access.
# Each user can be granted access to specific chassis for namespace isolation.
authentication:
  users:
    - username: "admin"
      password: "admin123"
      chassis: ["production-cluster", "development-cluster"]
    - username: "dev-user"
      password: "dev123"
      chassis: ["development-cluster"]

# KubeVirt Configuration
# ----------------------
# KubeVirt-specific settings for API version and operation timeouts.
kubevirt:
  api_version: "v1"  # KubeVirt API version to use
  timeout: 30        # Timeout in seconds for KubeVirt operations

# DataVolume Configuration
# ------------------------
# DataVolume settings for ISO imports and storage operations.
datavolume:
  storage_size: "10Gi"           # Default storage size for DataVolumes
  allow_insecure_tls: false      # Allow insecure TLS for ISO downloads
  storage_class: ""              # Storage class (empty = default)
  vm_update_timeout: "30s"       # Timeout for VM updates
  iso_download_timeout: "30m"    # Timeout for ISO downloads
  helper_image: "alpine:latest"  # Container image for ISO copy operations
`

	return os.WriteFile(path, []byte(defaultConfig), 0600)
}

// logEnvironmentOverrides logs which environment variables are set
func logEnvironmentOverrides() {
	envVars := []string{
		"KUBEVIRT_REDFISH_SERVER_HOST",
		"KUBEVIRT_REDFISH_SERVER_PORT",
		"KUBEVIRT_REDFISH_SERVER_TLS_ENABLED",
		"KUBEVIRT_REDFISH_KUBEVIRT_API_VERSION",
		"KUBEVIRT_REDFISH_KUBEVIRT_TIMEOUT",
		"KUBEVIRT_REDFISH_KUBEVIRT_ALLOW_INSECURE_TLS",
		"KUBEVIRT_REDFISH_DATAVOLUME_STORAGE_SIZE",
		"KUBEVIRT_REDFISH_DATAVOLUME_ALLOW_INSECURE_TLS",
		"KUBEVIRT_REDFISH_DATAVOLUME_VM_UPDATE_TIMEOUT",
		"KUBEVIRT_REDFISH_DATAVOLUME_ISO_DOWNLOAD_TIMEOUT",
		"KUBEVIRT_REDFISH_DATAVOLUME_HELPER_IMAGE",
	}

	var overrides []string
	for _, envVar := range envVars {
		if value := os.Getenv(envVar); value != "" {
			overrides = append(overrides, fmt.Sprintf("%s=%s", envVar, value))
		}
	}

	if len(overrides) > 0 {
		logger.Info("Environment variable overrides detected: %s", strings.Join(overrides, ", "))
	} else {
		logger.Debug("No environment variable overrides detected")
	}
}

// logConfigurationSummary logs a summary of the loaded configuration
func logConfigurationSummary(config *Config) {
	fields := map[string]interface{}{
		"server_host":                 config.Server.Host,
		"server_port":                 config.Server.Port,
		"tls_enabled":                 config.Server.TLS.Enabled,
		"chassis_count":               len(config.Chassis),
		"user_count":                  len(config.Auth.Users),
		"kubevirt_api_version":        config.KubeVirt.APIVersion,
		"kubevirt_timeout":            config.KubeVirt.Timeout,
		"kubevirt_allow_insecure_tls": config.KubeVirt.AllowInsecureTLS,
	}

	logger.InfoStructured("Configuration summary", fields)

	// Log chassis details
	for i, chassis := range config.Chassis {
		chassisFields := map[string]interface{}{
			"chassis_index":   i,
			"chassis_name":    chassis.Name,
			"namespace":       chassis.Namespace,
			"service_account": chassis.ServiceAccount,
		}
		logger.DebugStructured("Chassis configuration", chassisFields)
	}

	// Log user details (without passwords)
	for i, user := range config.Auth.Users {
		userFields := map[string]interface{}{
			"user_index":    i,
			"username":      user.Username,
			"chassis_count": len(user.Chassis),
			"chassis":       user.Chassis,
		}
		logger.DebugStructured("User configuration", userFields)
	}
}
