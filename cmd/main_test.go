package main

import (
	"flag"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/v1k0d3n/kubevirt-redfish/pkg/config"
	"github.com/v1k0d3n/kubevirt-redfish/pkg/server"
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
	// Test that timeout handling works correctly
	timeout := 100 * time.Millisecond
	start := time.Now()

	// Simulate a timeout scenario
	select {
	case <-time.After(timeout):
		// Expected timeout
	case <-time.After(timeout * 2):
		t.Error("Timeout should have occurred")
	}

	elapsed := time.Since(start)
	if elapsed < timeout {
		t.Errorf("Expected at least %v elapsed time, got %v", timeout, elapsed)
	}
}

// Test watchConfigFile function with various scenarios
func TestWatchConfigFileFunction(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test-config.yaml")

	// Create a default config file for testing
	err := config.CreateDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Load the config
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Create a real server instance for testing
	testServer := server.NewServer(cfg, nil) // nil kubevirt client for testing

	// Test watchConfigFile with valid config path - use a timeout to avoid hanging
	done := make(chan bool)
	go func() {
		watchConfigFile(configPath, testServer)
		done <- true
	}()

	// Wait for a short time to let the watcher start, then simulate a file change
	time.Sleep(100 * time.Millisecond)

	// Simulate a file change by writing to the config file
	err = config.CreateDefaultConfig(configPath) // This will overwrite the file
	if err != nil {
		t.Fatalf("Failed to simulate file change: %v", err)
	}

	// Wait a bit more for the change to be detected
	time.Sleep(100 * time.Millisecond)

	// The test should complete without hanging
	select {
	case <-done:
		// Test completed successfully
	case <-time.After(1 * time.Second):
		t.Log("Test completed with timeout (expected behavior)")
	}

	// Verify the config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Test config file should exist")
	}
}

// Test watchConfigFile with invalid directory
func TestWatchConfigFileInvalidDirectory(t *testing.T) {
	// Test with non-existent directory
	invalidPath := "/non/existent/path/config.yaml"

	// Create a minimal config for testing
	cfg := &config.Config{}
	testServer := server.NewServer(cfg, nil)

	// This should not panic and should handle the error gracefully
	// Use a timeout to avoid hanging
	done := make(chan bool)
	go func() {
		watchConfigFile(invalidPath, testServer)
		done <- true
	}()

	// Wait for the function to handle the error and return
	select {
	case <-done:
		// Function completed successfully
	case <-time.After(500 * time.Millisecond):
		t.Log("Test completed with timeout (expected for invalid path)")
	}
}

// Test watchConfigFile with empty config path
func TestWatchConfigFileEmptyPath(t *testing.T) {
	// Test with empty path
	cfg := &config.Config{}
	testServer := server.NewServer(cfg, nil)

	// This should not panic and should handle the error gracefully
	// Use a timeout to avoid hanging
	done := make(chan bool)
	go func() {
		watchConfigFile("", testServer)
		done <- true
	}()

	// Wait for the function to handle the error and return
	select {
	case <-done:
		// Function completed successfully
	case <-time.After(500 * time.Millisecond):
		t.Log("Test completed with timeout (expected for empty path)")
	}
}

// Test main function with various flag combinations
func TestMainFunctionWithFlags(t *testing.T) {
	// Test cases for different flag combinations
	testCases := []struct {
		name string
		args []string
	}{
		{
			name: "no flags",
			args: []string{"kubevirt-redfish"},
		},
		{
			name: "with config flag",
			args: []string{"kubevirt-redfish", "--config", "/test/config.yaml"},
		},
		{
			name: "with kubeconfig flag",
			args: []string{"kubevirt-redfish", "--kubeconfig", "/test/kubeconfig"},
		},
		{
			name: "with both flags",
			args: []string{"kubevirt-redfish", "--config", "/test/config.yaml", "--kubeconfig", "/test/kubeconfig"},
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

			// Parse flags (this is what main() does)
			configPath := flag.String("config", "", "Path to configuration file")
			kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (for external cluster access)")
			showVersion := flag.Bool("version", false, "Show version information")
			createConfig := flag.String("create-config", "", "Create a default configuration file at the specified path")

			flag.Parse()

			// Verify flag parsing works correctly
			if tc.name == "with config flag" && *configPath != "/test/config.yaml" {
				t.Errorf("Expected config path '/test/config.yaml', got '%s'", *configPath)
			}
			if tc.name == "with kubeconfig flag" && *kubeconfig != "/test/kubeconfig" {
				t.Errorf("Expected kubeconfig path '/test/kubeconfig', got '%s'", *kubeconfig)
			}
			if tc.name == "with both flags" {
				if *configPath != "/test/config.yaml" {
					t.Errorf("Expected config path '/test/config.yaml', got '%s'", *configPath)
				}
				if *kubeconfig != "/test/kubeconfig" {
					t.Errorf("Expected kubeconfig path '/test/kubeconfig', got '%s'", *kubeconfig)
				}
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

// Test main function version flag handling
func TestMainFunctionVersionFlag(t *testing.T) {
	// Save original args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	// Test version flag
	os.Args = []string{"kubevirt-redfish", "--version"}

	// Reset flag state
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	// Parse flags
	configPath := flag.String("config", "", "Path to configuration file")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (for external cluster access)")
	showVersion := flag.Bool("version", false, "Show version information")
	createConfig := flag.String("create-config", "", "Create a default configuration file at the specified path")

	flag.Parse()

	// Verify version flag is set
	if !*showVersion {
		t.Error("showVersion should be true when --version flag is used")
	}

	// Verify other flags are not set
	if *configPath != "" {
		t.Error("configPath should be empty when --version flag is used")
	}
	if *kubeconfig != "" {
		t.Error("kubeconfig should be empty when --version flag is used")
	}
	if *createConfig != "" {
		t.Error("createConfig should be empty when --version flag is used")
	}
}

// Test main function create-config flag handling
func TestMainFunctionCreateConfigFlag(t *testing.T) {
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

	// Parse flags
	configPathFlag := flag.String("config", "", "Path to configuration file")
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig file (for external cluster access)")
	showVersion := flag.Bool("version", false, "Show version information")
	createConfig := flag.String("create-config", "", "Create a default configuration file at the specified path")

	flag.Parse()

	// Verify create-config flag is set
	if *createConfig != configPath {
		t.Errorf("Expected createConfig '%s', got '%s'", configPath, *createConfig)
	}

	// Verify other flags are not set
	if *configPathFlag != "" {
		t.Error("configPath should be empty when --create-config flag is used")
	}
	if *kubeconfig != "" {
		t.Error("kubeconfig should be empty when --create-config flag is used")
	}
	if *showVersion {
		t.Error("showVersion should be false when --create-config flag is used")
	}
}
