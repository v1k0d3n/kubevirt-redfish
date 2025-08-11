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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/errors"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test-config.yaml")

	configContent := `
server:
  host: "127.0.0.1"
  port: 8443
  tls:
    enabled: false

chassis:
  - name: "test-cluster"
    namespace: "test-namespace"
    service_account: "test-sa"
    description: "Test cluster"

authentication:
  users:
    - username: "testuser"
      password: "testpass"
      chassis: ["test-cluster"]

kubevirt:
  api_version: "v1"
  timeout: 30
`

	err := os.WriteFile(configPath, []byte(configContent), 0600)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Test loading config
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify server config
	if config.Server.Host != "127.0.0.1" {
		t.Errorf("Expected host 127.0.0.1, got %s", config.Server.Host)
	}
	if config.Server.Port != 8443 {
		t.Errorf("Expected port 8443, got %d", config.Server.Port)
	}
	if config.Server.TLS.Enabled != false {
		t.Errorf("Expected TLS disabled, got %v", config.Server.TLS.Enabled)
	}

	// Verify chassis config
	if len(config.Chassis) != 1 {
		t.Errorf("Expected 1 chassis, got %d", len(config.Chassis))
	}
	if config.Chassis[0].Name != "test-cluster" {
		t.Errorf("Expected chassis name test-cluster, got %s", config.Chassis[0].Name)
	}

	// Verify auth config
	if len(config.Auth.Users) != 1 {
		t.Errorf("Expected 1 user, got %d", len(config.Auth.Users))
	}
	if config.Auth.Users[0].Username != "testuser" {
		t.Errorf("Expected username testuser, got %s", config.Auth.Users[0].Username)
	}

	// Verify KubeVirt config
	if config.KubeVirt.APIVersion != "v1" {
		t.Errorf("Expected API version v1, got %s", config.KubeVirt.APIVersion)
	}
	if config.KubeVirt.Timeout != 30 {
		t.Errorf("Expected timeout 30, got %d", config.KubeVirt.Timeout)
	}
}

func TestLoadConfigWithDefaults(t *testing.T) {
	// Create a temporary directory and config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test-config.yaml")

	// Create a minimal valid config file
	validConfig := `
server:
  host: "127.0.0.1"
  port: 8080
  tls:
    enabled: false

chassis:
  - name: "test-chassis"
    namespace: "test-namespace"
    service_account: "test-sa"
    description: "Test chassis"

authentication:
  users:
    - username: "admin"
      password: "admin123"
      chassis: ["test-chassis"]

kubevirt:
  api_version: "v1"
  timeout: 60
  allow_insecure_tls: true

datavolume:
  storage_size: "20Gi"
  allow_insecure_tls: true
  storage_class: "fast"
  vm_update_timeout: "60s"
  iso_download_timeout: "60m"
`

	err := os.WriteFile(configPath, []byte(validConfig), 0600)
	if err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load config using explicit path
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config with defaults: %v", err)
	}

	// Verify loaded values (not defaults)
	if config.Server.Host != "127.0.0.1" {
		t.Errorf("Expected host 127.0.0.1, got %s", config.Server.Host)
	}
	if config.Server.Port != 8080 {
		t.Errorf("Expected port 8080, got %d", config.Server.Port)
	}
	if config.KubeVirt.Timeout != 60 {
		t.Errorf("Expected timeout 60, got %d", config.KubeVirt.Timeout)
	}
	if config.DataVolume.StorageSize != "20Gi" {
		t.Errorf("Expected storage size 20Gi, got %s", config.DataVolume.StorageSize)
	}
}

func TestLoadConfigInvalidFile(t *testing.T) {
	// Test loading non-existent config file
	_, err := LoadConfig("/nonexistent/config.yaml")
	if err == nil {
		t.Error("Expected error when loading non-existent config file")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	// Create a temporary invalid config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid-config.yaml")

	invalidContent := `
server:
  host: "127.0.0.1"
  port: "invalid-port"  # Invalid: port should be integer
`

	err := os.WriteFile(configPath, []byte(invalidContent), 0600)
	if err != nil {
		t.Fatalf("Failed to write invalid test config: %v", err)
	}

	// Test loading invalid config
	_, err = LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error when loading invalid config")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errType errors.ErrorType
	}{
		{
			name: "valid configuration",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
					TLS: TLSConfig{
						Enabled:  false,
						CertFile: "",
						KeyFile:  "",
					},
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
						Description:    "Test chassis",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion:       "v1",
					Timeout:          30,
					AllowInsecureTLS: false,
				},
				DataVolume: DataVolumeConfig{
					StorageSize:        "10Gi",
					AllowInsecureTLS:   false,
					StorageClass:       "",
					VMUpdateTimeout:    "30s",
					ISODownloadTimeout: "30m",
				},
			},
			wantErr: false,
		},
		{
			name: "missing server host",
			config: &Config{
				Server: ServerConfig{
					Host: "",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid server port",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 70000, // Invalid port
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "TLS enabled without certificate files",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
					TLS: TLSConfig{
						Enabled:  true,
						CertFile: "",
						KeyFile:  "",
					},
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "no chassis configuration",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid chassis name format",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test_chassis", // Invalid: contains underscore
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test_chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid namespace format",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "TestNamespace", // Invalid: contains uppercase
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "no users configuration",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "user with weak password",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "123", // Too short
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "user with access to non-existent chassis",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"non-existent-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "duplicate chassis names",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
					{
						Name:           "test-chassis", // Duplicate name
						Namespace:      "test-namespace-2",
						ServiceAccount: "test-sa-2",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "duplicate usernames",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
						{
							Username: "admin", // Duplicate username
							Password: "admin456",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid KubeVirt API version",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v2", // Invalid version
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid KubeVirt timeout",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    400, // Invalid: too high
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10Gi",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid storage size format",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize: "10GB", // Invalid: should be "10Gi"
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
		{
			name: "invalid timeout format",
			config: &Config{
				Server: ServerConfig{
					Host: "0.0.0.0",
					Port: 8443,
				},
				Chassis: []ChassisConfig{
					{
						Name:           "test-chassis",
						Namespace:      "test-namespace",
						ServiceAccount: "test-sa",
					},
				},
				Auth: AuthConfig{
					Users: []UserConfig{
						{
							Username: "admin",
							Password: "admin123",
							Chassis:  []string{"test-chassis"},
						},
					},
				},
				KubeVirt: KubeVirtConfig{
					APIVersion: "v1",
					Timeout:    30,
				},
				DataVolume: DataVolumeConfig{
					StorageSize:        "10Gi",
					VMUpdateTimeout:    "30x", // Invalid duration
					ISODownloadTimeout: "30m",
				},
			},
			wantErr: true,
			errType: errors.ErrorTypeValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != nil {
				if redfishErr, ok := err.(*errors.RedfishError); ok {
					if redfishErr.Type != tt.errType {
						t.Errorf("validateConfig() error type = %v, want %v", redfishErr.Type, tt.errType)
					}
				} else {
					t.Errorf("validateConfig() error is not a RedfishError: %T", err)
				}
			}
		})
	}
}

func TestEnvironmentVariableOverrides(t *testing.T) {
	// Test environment variable overrides
	os.Setenv("KUBEVIRT_REDFISH_SERVER_HOST", "127.0.0.1")
	os.Setenv("KUBEVIRT_REDFISH_SERVER_PORT", "8080")
	os.Setenv("KUBEVIRT_REDFISH_KUBEVIRT_TIMEOUT", "60")
	defer func() {
		os.Unsetenv("KUBEVIRT_REDFISH_SERVER_HOST")
		os.Unsetenv("KUBEVIRT_REDFISH_SERVER_PORT")
		os.Unsetenv("KUBEVIRT_REDFISH_KUBEVIRT_TIMEOUT")
	}()

	// Create a minimal valid config
	config := &Config{
		Server: ServerConfig{
			Host: "0.0.0.0", // This should be overridden
			Port: 8443,      // This should be overridden
		},
		Chassis: []ChassisConfig{
			{
				Name:           "test-chassis",
				Namespace:      "test-namespace",
				ServiceAccount: "test-sa",
			},
		},
		Auth: AuthConfig{
			Users: []UserConfig{
				{
					Username: "admin",
					Password: "admin123",
					Chassis:  []string{"test-chassis"},
				},
			},
		},
		KubeVirt: KubeVirtConfig{
			APIVersion: "v1",
			Timeout:    30, // This should be overridden
		},
		DataVolume: DataVolumeConfig{
			StorageSize: "10Gi",
		},
	}

	// Note: This test would require mocking viper to properly test environment variable overrides
	// For now, we just test that the validation still works with the config
	err := validateConfig(config)
	if err != nil {
		t.Errorf("validateConfig() with environment variables error = %v", err)
	}
}

func TestValidationHelperFunctions(t *testing.T) {
	// Test chassis name validation
	tests := []struct {
		name     string
		chassis  string
		expected bool
	}{
		{"valid chassis name", "test-chassis", true},
		{"valid chassis with numbers", "test-chassis-123", true},
		{"invalid chassis with underscore", "test_chassis", false},
		{"invalid chassis with uppercase", "Test-Chassis", false},
		{"empty chassis name", "", false},
		{"chassis name too long", "a" + string(make([]byte, 63)), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidChassisName(tt.chassis)
			if result != tt.expected {
				t.Errorf("isValidChassisName(%s) = %v, want %v", tt.chassis, result, tt.expected)
			}
		})
	}

	// Test namespace validation
	namespaceTests := []struct {
		name     string
		ns       string
		expected bool
	}{
		{"valid namespace", "test-namespace", true},
		{"valid namespace with numbers", "test-namespace-123", true},
		{"invalid namespace with uppercase", "Test-Namespace", false},
		{"invalid namespace with underscore", "test_namespace", false},
		{"empty namespace", "", false},
	}

	for _, tt := range namespaceTests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidNamespace(tt.ns)
			if result != tt.expected {
				t.Errorf("isValidNamespace(%s) = %v, want %v", tt.ns, result, tt.expected)
			}
		})
	}

	// Test username validation
	usernameTests := []struct {
		name     string
		username string
		expected bool
	}{
		{"valid username", "test-user", true},
		{"valid username with underscore", "test_user", true},
		{"valid username with uppercase", "TestUser", true},
		{"valid username with numbers", "test-user-123", true},
		{"invalid username with special chars", "test@user", false},
		{"empty username", "", false},
	}

	for _, tt := range usernameTests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidUsername(tt.username)
			if result != tt.expected {
				t.Errorf("isValidUsername(%s) = %v, want %v", tt.username, result, tt.expected)
			}
		})
	}

	// Test storage size validation
	storageTests := []struct {
		name     string
		size     string
		expected bool
	}{
		{"valid storage size Gi", "10Gi", true},
		{"valid storage size Mi", "100Mi", true},
		{"valid storage size Ki", "1024Ki", true},
		{"invalid storage size GB", "10GB", false},
		{"invalid storage size no unit", "10", false},
		{"invalid storage size empty", "", false},
	}

	for _, tt := range storageTests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidStorageSize(tt.size)
			if result != tt.expected {
				t.Errorf("isValidStorageSize(%s) = %v, want %v", tt.size, result, tt.expected)
			}
		})
	}
}

func TestGetChassisByName(t *testing.T) {
	config := &Config{
		Chassis: []ChassisConfig{
			{
				Name:           "chassis1",
				Namespace:      "namespace1",
				ServiceAccount: "sa1",
			},
			{
				Name:           "chassis2",
				Namespace:      "namespace2",
				ServiceAccount: "sa2",
			},
		},
	}

	// Test finding existing chassis
	chassis, err := config.GetChassisByName("chassis1")
	if err != nil {
		t.Errorf("Expected to find chassis1, got error: %v", err)
	}
	if chassis.Name != "chassis1" {
		t.Errorf("Expected chassis name chassis1, got %s", chassis.Name)
	}

	// Test finding non-existent chassis
	_, err = config.GetChassisByName("nonexistent")
	if err == nil {
		t.Error("Expected error when chassis not found")
	}
}

func TestGetUserByCredentials(t *testing.T) {
	config := &Config{
		Auth: AuthConfig{
			Users: []UserConfig{
				{
					Username: "user1",
					Password: "pass1",
				},
				{
					Username: "user2",
					Password: "pass2",
				},
			},
		},
	}

	// Test finding existing user
	user, err := config.GetUserByCredentials("user1", "pass1")
	if err != nil {
		t.Errorf("Expected to find user1, got error: %v", err)
	}
	if user.Username != "user1" {
		t.Errorf("Expected username user1, got %s", user.Username)
	}

	// Test finding user with wrong password
	_, err = config.GetUserByCredentials("user1", "wrongpass")
	if err == nil {
		t.Error("Expected error when password is wrong")
	}

	// Test finding non-existent user
	_, err = config.GetUserByCredentials("nonexistent", "pass")
	if err == nil {
		t.Error("Expected error when user not found")
	}
}

func TestGetChassisForUser(t *testing.T) {
	config := &Config{
		Auth: AuthConfig{
			Users: []UserConfig{
				{
					Username: "user1",
					Password: "pass1",
					Chassis:  []string{"chassis1", "chassis2"},
				},
				{
					Username: "user2",
					Password: "pass2",
					Chassis:  []string{"chassis3"},
				},
			},
		},
		Chassis: []ChassisConfig{
			{
				Name:           "chassis1",
				Namespace:      "namespace1",
				ServiceAccount: "sa1",
			},
			{
				Name:           "chassis2",
				Namespace:      "namespace2",
				ServiceAccount: "sa2",
			},
			{
				Name:           "chassis3",
				Namespace:      "namespace3",
				ServiceAccount: "sa3",
			},
		},
	}

	// Test getting chassis for existing user
	chassis, err := config.GetChassisForUser("user1")
	if err != nil {
		t.Errorf("Expected to get chassis for user1, got error: %v", err)
	}
	if len(chassis) != 2 {
		t.Errorf("Expected 2 chassis for user1, got %d", len(chassis))
	}

	// Test getting chassis for non-existent user
	_, err = config.GetChassisForUser("nonexistent")
	if err == nil {
		t.Error("Expected error when user not found")
	}

	// Test getting chassis for user with non-existent chassis
	config.Auth.Users[0].Chassis = []string{"nonexistent"}
	_, err = config.GetChassisForUser("user1")
	if err == nil {
		t.Error("Expected error when user has access to non-existent chassis")
	}
}

func TestCreateDefaultConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "default-config.yaml")

	// Test creating default config
	err := CreateDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to create default config: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Default config file was not created")
	}

	// Verify file content can be loaded
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load created default config: %v", err)
	}

	// Verify default values
	if config.Server.Host != "0.0.0.0" {
		t.Errorf("Expected default host 0.0.0.0, got %s", config.Server.Host)
	}
	if config.Server.Port != 8443 {
		t.Errorf("Expected default port 8443, got %d", config.Server.Port)
	}
}

// TestGetDataVolumeConfig tests the GetDataVolumeConfig method
func TestGetDataVolumeConfig(t *testing.T) {
	config := &Config{
		DataVolume: DataVolumeConfig{
			StorageSize:        "10Gi",
			AllowInsecureTLS:   true,
			StorageClass:       "fast-ssd",
			VMUpdateTimeout:    "5m",
			ISODownloadTimeout: "10m",
		},
	}

	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout := config.GetDataVolumeConfig()

	// Verify all returned values match the config
	if storageSize != "10Gi" {
		t.Errorf("Expected storageSize '10Gi', got '%s'", storageSize)
	}
	if !allowInsecureTLS {
		t.Error("Expected allowInsecureTLS to be true")
	}
	if storageClass != "fast-ssd" {
		t.Errorf("Expected storageClass 'fast-ssd', got '%s'", storageClass)
	}
	if vmUpdateTimeout != "5m" {
		t.Errorf("Expected vmUpdateTimeout '5m', got '%s'", vmUpdateTimeout)
	}
	if isoDownloadTimeout != "10m" {
		t.Errorf("Expected isoDownloadTimeout '10m', got '%s'", isoDownloadTimeout)
	}
}

// TestGetDataVolumeConfigWithEmptyValues tests GetDataVolumeConfig with empty/default values
func TestGetDataVolumeConfigWithEmptyValues(t *testing.T) {
	config := &Config{
		DataVolume: DataVolumeConfig{
			StorageSize:        "",
			AllowInsecureTLS:   false,
			StorageClass:       "",
			VMUpdateTimeout:    "",
			ISODownloadTimeout: "",
		},
	}

	storageSize, allowInsecureTLS, storageClass, vmUpdateTimeout, isoDownloadTimeout := config.GetDataVolumeConfig()

	// Verify all returned values match the config
	if storageSize != "" {
		t.Errorf("Expected empty storageSize, got '%s'", storageSize)
	}
	if allowInsecureTLS {
		t.Error("Expected allowInsecureTLS to be false")
	}
	if storageClass != "" {
		t.Errorf("Expected empty storageClass, got '%s'", storageClass)
	}
	if vmUpdateTimeout != "" {
		t.Errorf("Expected empty vmUpdateTimeout, got '%s'", vmUpdateTimeout)
	}
	if isoDownloadTimeout != "" {
		t.Errorf("Expected empty isoDownloadTimeout, got '%s'", isoDownloadTimeout)
	}
}

// TestGetKubeVirtConfig tests the GetKubeVirtConfig method
func TestGetKubeVirtConfig(t *testing.T) {
	config := &Config{
		KubeVirt: KubeVirtConfig{
			APIVersion:       "v1",
			Timeout:          30,
			AllowInsecureTLS: true,
		},
	}

	apiVersion, timeout, allowInsecureTLS := config.GetKubeVirtConfig()

	// Verify all returned values match the config
	if apiVersion != "v1" {
		t.Errorf("Expected apiVersion 'v1', got '%s'", apiVersion)
	}
	if timeout != 30 {
		t.Errorf("Expected timeout 30, got %d", timeout)
	}
	if !allowInsecureTLS {
		t.Error("Expected allowInsecureTLS to be true")
	}
}

// TestGetKubeVirtConfigWithDefaultValues tests GetKubeVirtConfig with default values
func TestGetKubeVirtConfigWithDefaultValues(t *testing.T) {
	config := &Config{
		KubeVirt: KubeVirtConfig{
			APIVersion:       "",
			Timeout:          0,
			AllowInsecureTLS: false,
		},
	}

	apiVersion, timeout, allowInsecureTLS := config.GetKubeVirtConfig()

	// Verify all returned values match the config
	if apiVersion != "" {
		t.Errorf("Expected empty apiVersion, got '%s'", apiVersion)
	}
	if timeout != 0 {
		t.Errorf("Expected timeout 0, got %d", timeout)
	}
	if allowInsecureTLS {
		t.Error("Expected allowInsecureTLS to be false")
	}
}

// TestGetKubeVirtConfigWithNilConfig tests GetKubeVirtConfig with nil config
func TestGetKubeVirtConfigWithNilConfig(t *testing.T) {
	var config *Config

	// This should panic due to nil pointer dereference
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when calling GetKubeVirtConfig on nil config")
		}
	}()

	config.GetKubeVirtConfig()
}

// TestGetDataVolumeConfigWithNilConfig tests GetDataVolumeConfig with nil config
func TestGetDataVolumeConfigWithNilConfig(t *testing.T) {
	var config *Config

	// This should panic due to nil pointer dereference
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when calling GetDataVolumeConfig on nil config")
		}
	}()

	config.GetDataVolumeConfig()
}
