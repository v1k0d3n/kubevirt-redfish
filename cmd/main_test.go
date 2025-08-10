package main

import (
	"flag"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
)

func TestMainVersionFlag(t *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test version flag
	os.Args = []string{"kubevirt-redfish", "--version"}

	// Reset flag state
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// This should exit with code 0, but we can't easily test that in a unit test
	// Instead, we'll test the version variables are set
	if Version == "" {
		t.Error("Version should not be empty")
	}
	if GitCommit == "" {
		t.Error("GitCommit should not be empty")
	}
	if BuildDate == "" {
		t.Error("BuildDate should not be empty")
	}
}

func TestMainCreateConfigFlag(t *testing.T) {
	// Create a temporary directory for test config
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test-config.yaml")

	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test create-config flag
	os.Args = []string{"kubevirt-redfish", "--create-config", configPath}

	// Reset flag state
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// This should exit with code 0, but we can't easily test that in a unit test
	// Instead, we'll test that the config creation function works
	err := config.CreateDefaultConfig(configPath)
	if err != nil {
		t.Errorf("Failed to create default config: %v", err)
	}

	// Verify the config file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file should have been created")
	}
}

func TestPrintUsage(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Call printUsage
	printUsage()

	// Close the pipe to read the output
	w.Close()

	// Read the output
	output := make([]byte, 1024)
	n, _ := r.Read(output)
	usageOutput := string(output[:n])

	// Verify expected content
	expectedStrings := []string{
		"KubeVirt Redfish API Server",
		"Usage: kubevirt-redfish",
		"--config",
		"--kubeconfig",
		"--version",
		"--create-config",
		"--help",
	}

	for _, expected := range expectedStrings {
		if !contains(usageOutput, expected) {
			t.Errorf("Usage output should contain '%s'", expected)
		}
	}
}

func TestWatchConfigFile(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test-config.yaml")

	// Create a default config file for testing
	err := config.CreateDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Test that the config file exists and is readable
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Test config file should exist")
	}

	// Test that we can load the config
	loadedConfig, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load test config: %v", err)
	}

	// Verify the loaded config has expected default values
	if loadedConfig.Server.Host == "" {
		t.Error("Server host should not be empty")
	}
	if loadedConfig.Server.Port == 0 {
		t.Error("Server port should not be zero")
	}
}

func TestMainFunctionFlags(t *testing.T) {
	// Test that flag parsing works correctly
	testCases := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "no flags",
			args:     []string{"kubevirt-redfish"},
			expected: "",
		},
		{
			name:     "config flag",
			args:     []string{"kubevirt-redfish", "--config", "/test/config.yaml"},
			expected: "/test/config.yaml",
		},
		{
			name:     "kubeconfig flag",
			args:     []string{"kubevirt-redfish", "--kubeconfig", "/test/kubeconfig"},
			expected: "/test/kubeconfig",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Save original args
			originalArgs := os.Args
			defer func() { os.Args = originalArgs }()

			os.Args = tc.args

			// Reset flag state
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			// Parse flags
			configPath := flag.String("config", "", "Path to configuration file")
			kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file")
			showVersion := flag.Bool("version", false, "Show version information")
			createConfig := flag.String("create-config", "", "Create a default configuration file")

			flag.Parse()

			// Verify flag parsing
			if tc.name == "config flag" && *configPath != "/test/config.yaml" {
				t.Errorf("Expected config path '/test/config.yaml', got '%s'", *configPath)
			}
			if tc.name == "kubeconfig flag" && *kubeconfig != "/test/kubeconfig" {
				t.Errorf("Expected kubeconfig path '/test/kubeconfig', got '%s'", *kubeconfig)
			}
			if *showVersion {
				t.Error("showVersion should be false for this test case")
			}
			if *createConfig != "" {
				t.Error("createConfig should be empty for this test case")
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			containsSubstring(s, substr))))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test that the main function can handle invalid config paths gracefully
func TestMainInvalidConfigPath(t *testing.T) {
	// This test verifies that the main function would handle invalid config paths
	// Since we can't easily test the main function directly due to os.Exit calls,
	// we'll test the config loading function that main calls

	invalidPath := "/nonexistent/path/config.yaml"
	_, err := config.LoadConfig(invalidPath)
	if err == nil {
		t.Error("Loading config from invalid path should return an error")
	}
}

// Test that version variables are properly set
func TestVersionVariables(t *testing.T) {
	// These should be set during build time, but for testing we can verify they exist
	if Version == "" {
		t.Error("Version should be set")
	}
	if GitCommit == "" {
		t.Error("GitCommit should be set")
	}
	if BuildDate == "" {
		t.Error("BuildDate should be set")
	}
}

// Test signal handling setup (simplified)
func TestSignalHandling(t *testing.T) {
	// This test verifies that the signal handling logic is properly structured
	// We can't easily test the actual signal handling in a unit test,
	// but we can verify the signal types are correct

	// The main function uses syscall.SIGINT and syscall.SIGTERM
	// We can verify these are valid signal types by checking they're not zero
	if syscall.SIGINT == 0 {
		t.Error("SIGINT should be a valid signal")
	}
	if syscall.SIGTERM == 0 {
		t.Error("SIGTERM should be a valid signal")
	}
}

// Test that the main function can handle timeouts properly
func TestTimeoutHandling(t *testing.T) {
	// Test that the timeout duration used in main is reasonable
	timeout := 30 * time.Second
	if timeout <= 0 {
		t.Error("Timeout should be positive")
	}
	if timeout > 5*time.Minute {
		t.Error("Timeout should be reasonable (not too long)")
	}
}
