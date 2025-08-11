package logger

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		expected LogLevel
	}{
		{"debug", "DEBUG", DEBUG},
		{"debug lowercase", "debug", DEBUG},
		{"info", "INFO", INFO},
		{"info lowercase", "info", INFO},
		{"warning", "WARNING", WARNING},
		{"warning lowercase", "warning", WARNING},
		{"error", "ERROR", ERROR},
		{"error lowercase", "error", ERROR},
		{"critical", "CRITICAL", CRITICAL},
		{"critical lowercase", "critical", CRITICAL},
		{"unknown", "UNKNOWN", INFO}, // Default to INFO
		{"empty", "", INFO},          // Default to INFO
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseLogLevel(tt.level)
			if result != tt.expected {
				t.Errorf("parseLogLevel(%s) = %v, want %v", tt.level, result, tt.expected)
			}
		})
	}
}

func TestLogger_ShouldLog(t *testing.T) {
	tests := []struct {
		name         string
		loggerLevel  LogLevel
		messageLevel LogLevel
		expected     bool
	}{
		{"debug at debug", DEBUG, DEBUG, true},
		{"info at debug", DEBUG, INFO, true},
		{"warning at debug", DEBUG, WARNING, true},
		{"error at debug", DEBUG, ERROR, true},
		{"critical at debug", DEBUG, CRITICAL, true},
		{"debug at info", INFO, DEBUG, false},
		{"info at info", INFO, INFO, true},
		{"warning at info", INFO, WARNING, true},
		{"error at info", INFO, ERROR, true},
		{"critical at info", INFO, CRITICAL, true},
		{"debug at warning", WARNING, DEBUG, false},
		{"info at warning", WARNING, INFO, false},
		{"warning at warning", WARNING, WARNING, true},
		{"error at warning", WARNING, ERROR, true},
		{"critical at warning", WARNING, CRITICAL, true},
		{"debug at error", ERROR, DEBUG, false},
		{"info at error", ERROR, INFO, false},
		{"warning at error", ERROR, WARNING, false},
		{"error at error", ERROR, ERROR, true},
		{"critical at error", ERROR, CRITICAL, true},
		{"debug at critical", CRITICAL, DEBUG, false},
		{"info at critical", CRITICAL, INFO, false},
		{"warning at critical", CRITICAL, WARNING, false},
		{"error at critical", CRITICAL, ERROR, false},
		{"critical at critical", CRITICAL, CRITICAL, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &Logger{level: tt.loggerLevel}
			result := logger.shouldLog(tt.messageLevel)
			if result != tt.expected {
				t.Errorf("shouldLog(%v) at level %v = %v, want %v", tt.messageLevel, tt.loggerLevel, result, tt.expected)
			}
		})
	}
}

func TestLogger_FormatStructuredMessage(t *testing.T) {
	logger := &Logger{level: DEBUG}
	fields := map[string]interface{}{
		"user":      "testuser",
		"operation": "test",
		"count":     42,
	}

	result := logger.formatStructuredMessage(INFO, "Test message", fields)

	// Check that the result is valid JSON
	if !strings.Contains(result, `"level":"INFO"`) {
		t.Error("Expected JSON to contain level INFO")
	}
	if !strings.Contains(result, `"message":"Test message"`) {
		t.Error("Expected JSON to contain message")
	}
	if !strings.Contains(result, `"user":"testuser"`) {
		t.Error("Expected JSON to contain user field")
	}
}

func TestLogger_FormatSimpleMessage(t *testing.T) {
	logger := &Logger{level: DEBUG}

	result := logger.formatSimpleMessage(INFO, "Test message %s", "value")

	if !strings.Contains(result, "INFO") {
		t.Error("Expected message to contain level")
	}
	if !strings.Contains(result, "Test message value") {
		t.Error("Expected message to contain formatted string")
	}
}

func TestLogger_Debug(t *testing.T) {
	logger := &Logger{level: DEBUG}

	// This should not panic
	logger.Debug("Debug message")
	logger.Debug("Debug message with %s", "format")
}

func TestLogger_DebugStructured(t *testing.T) {
	logger := &Logger{level: DEBUG}
	fields := map[string]interface{}{"key": "value"}

	// This should not panic
	logger.DebugStructured("Debug structured message", fields)
}

func TestLogger_Info(t *testing.T) {
	logger := &Logger{level: INFO}

	// This should not panic
	logger.Info("Info message")
	logger.Info("Info message with %s", "format")
}

func TestLogger_InfoStructured(t *testing.T) {
	logger := &Logger{level: INFO}
	fields := map[string]interface{}{"key": "value"}

	// This should not panic
	logger.InfoStructured("Info structured message", fields)
}

func TestLogger_Warning(t *testing.T) {
	logger := &Logger{level: WARNING}

	// This should not panic
	logger.Warning("Warning message")
	logger.Warning("Warning message with %s", "format")
}

func TestLogger_WarningStructured(t *testing.T) {
	logger := &Logger{level: WARNING}
	fields := map[string]interface{}{"key": "value"}

	// This should not panic
	logger.WarningStructured("Warning structured message", fields)
}

func TestLogger_Error(t *testing.T) {
	logger := &Logger{level: ERROR}

	// This should not panic
	logger.Error("Error message")
	logger.Error("Error message with %s", "format")
}

func TestLogger_ErrorStructured(t *testing.T) {
	logger := &Logger{level: ERROR}
	fields := map[string]interface{}{"key": "value"}

	// This should not panic
	logger.ErrorStructured("Error structured message", fields)
}

func TestLogger_Critical(t *testing.T) {
	logger := &Logger{level: CRITICAL}

	// This should not panic
	logger.Critical("Critical message")
	logger.Critical("Critical message with %s", "format")
}

func TestLogger_CriticalStructured(t *testing.T) {
	logger := &Logger{level: CRITICAL}
	fields := map[string]interface{}{"key": "value"}

	// This should not panic
	logger.CriticalStructured("Critical structured message", fields)
}

func TestInit(t *testing.T) {
	// Test initialization
	Init("DEBUG")
	if defaultLogger == nil {
		t.Error("Expected defaultLogger to be initialized")
	}
	if defaultLogger.level != DEBUG {
		t.Errorf("Expected level DEBUG, got %v", defaultLogger.level)
	}
}

func TestGlobalFunctions(t *testing.T) {
	// Initialize logger
	Init("DEBUG")

	// Test global functions - they should not panic
	Debug("Global debug message")
	DebugStructured("Global debug structured", map[string]interface{}{"key": "value"})

	Info("Global info message")
	InfoStructured("Global info structured", map[string]interface{}{"key": "value"})

	Warning("Global warning message")
	WarningStructured("Global warning structured", map[string]interface{}{"key": "value"})

	Error("Global error message")
	ErrorStructured("Global error structured", map[string]interface{}{"key": "value"})

	Critical("Global critical message")
	CriticalStructured("Global critical structured", map[string]interface{}{"key": "value"})
}

func TestSetContext(t *testing.T) {
	ctx := context.Background()

	// This should not panic
	SetContext(ctx)
}

func TestGetContext(t *testing.T) {
	// This should not panic and return a context
	ctx := getContext()
	if ctx == nil {
		t.Error("Expected context to be returned")
	}
}

func TestGetCorrelationID(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{"nil context", nil, ""},
		{"empty context", context.Background(), ""},
		{"context with correlation ID", context.WithValue(context.Background(), contextKey("correlation_id"), "test-id"), "test-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCorrelationID(tt.ctx)
			if result != tt.expected {
				t.Errorf("getCorrelationID() = %s, want %s", result, tt.expected)
			}
		})
	}
}

func TestGetCorrelationIDGlobal(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{"nil context", nil, ""},
		{"empty context", context.Background(), ""},
		{"context with correlation ID", context.WithValue(context.Background(), contextKey("correlation_id"), "test-id"), "test-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetCorrelationID(tt.ctx)
			if result != tt.expected {
				t.Errorf("GetCorrelationID() = %s, want %s", result, tt.expected)
			}
		})
	}
}

func TestWithCorrelationID(t *testing.T) {
	ctx := context.Background()
	correlationID := "test-correlation-id"

	result := WithCorrelationID(ctx, correlationID)

	// Check that the correlation ID was set
	value := result.Value(contextKey("correlation_id"))
	if value != correlationID {
		t.Errorf("Expected correlation ID %s, got %v", correlationID, value)
	}
}

func TestWithUser(t *testing.T) {
	ctx := context.Background()
	user := "testuser"

	result := WithUser(ctx, user)

	// Check that the user was set
	value := result.Value(contextKey("user"))
	if value != user {
		t.Errorf("Expected user %s, got %v", user, value)
	}
}

func TestWithOperation(t *testing.T) {
	ctx := context.Background()
	operation := "test-operation"

	result := WithOperation(ctx, operation)

	// Check that the operation was set
	value := result.Value(contextKey("operation"))
	if value != operation {
		t.Errorf("Expected operation %s, got %v", operation, value)
	}
}

func TestWithResource(t *testing.T) {
	ctx := context.Background()
	resource := "test-resource"

	result := WithResource(ctx, resource)

	// Check that the resource was set
	value := result.Value(contextKey("resource"))
	if value != resource {
		t.Errorf("Expected resource %s, got %v", resource, value)
	}
}

func TestWithAuth(t *testing.T) {
	ctx := context.Background()
	authData := map[string]string{"user": "testuser", "role": "admin"}

	result := WithAuth(ctx, authData)

	// Check that the auth data was set
	value := result.Value(contextKey("auth"))
	if !reflect.DeepEqual(value, authData) {
		t.Errorf("Expected auth data %v, got %v", authData, value)
	}
}

func TestGetAuth(t *testing.T) {
	ctx := context.Background()
	authData := map[string]string{"user": "testuser", "role": "admin"}

	// Test with auth data in context
	ctxWithAuth := WithAuth(ctx, authData)
	result := GetAuth(ctxWithAuth)
	if !reflect.DeepEqual(result, authData) {
		t.Errorf("Expected auth data %v, got %v", authData, result)
	}

	// Test with nil context
	result = GetAuth(context.TODO())
	if result != nil {
		t.Errorf("Expected nil for TODO context, got %v", result)
	}

	// Test with context without auth data
	result = GetAuth(ctx)
	if result != nil {
		t.Errorf("Expected nil for context without auth data, got %v", result)
	}
}

func TestLogRequest(t *testing.T) {
	// This should not panic
	LogRequest("GET", "/test", "testuser", "test-correlation-id")
}

func TestLogResponse(t *testing.T) {
	// This should not panic
	LogResponse("GET", "/test", "testuser", "test-correlation-id", 200, 100*time.Millisecond)
}

func TestLogKubeVirtOperation(t *testing.T) {
	fields := map[string]interface{}{"key": "value"}

	// This should not panic
	LogKubeVirtOperation("CreateVM", "default", "VirtualMachine", "test-correlation-id", fields)
}

func TestGetLogLevelFromEnv(t *testing.T) {
	// Test with no environment variable set
	result := GetLogLevelFromEnv()
	if result != "INFO" {
		t.Errorf("Expected default level INFO, got %s", result)
	}

	// Test with environment variable set
	os.Setenv("REDFISH_LOG_LEVEL", "DEBUG")
	defer os.Unsetenv("REDFISH_LOG_LEVEL")

	result = GetLogLevelFromEnv()
	if result != "DEBUG" {
		t.Errorf("Expected level DEBUG, got %s", result)
	}
}

func TestIsLoggingEnabled(t *testing.T) {
	// Test with no environment variable set
	result := IsLoggingEnabled()
	if !result {
		t.Error("Expected logging to be enabled by default")
	}

	// Test with environment variable set to false
	os.Setenv("REDFISH_LOGGING_ENABLED", "false")
	defer os.Unsetenv("REDFISH_LOGGING_ENABLED")

	result = IsLoggingEnabled()
	if result {
		t.Error("Expected logging to be disabled when REDFISH_LOGGING_ENABLED=false")
	}

	// Test with environment variable set to true
	os.Setenv("REDFISH_LOGGING_ENABLED", "true")

	result = IsLoggingEnabled()
	if !result {
		t.Error("Expected logging to be enabled when REDFISH_LOGGING_ENABLED=true")
	}
}

func TestLogEntry_JSON(t *testing.T) {
	entry := LogEntry{
		Timestamp:     "2025-01-01T00:00:00Z",
		Level:         "INFO",
		Message:       "Test message",
		CorrelationID: "test-id",
		User:          "testuser",
		Operation:     "test",
		Resource:      "test-resource",
		Duration:      "100ms",
		Status:        "success",
		Error:         "test error",
		Fields:        map[string]interface{}{"key": "value"},
	}

	// Test that the entry can be marshaled to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		t.Errorf("Failed to marshal LogEntry to JSON: %v", err)
	}

	// Test that the JSON contains expected fields
	jsonStr := string(data)
	if !strings.Contains(jsonStr, `"level":"INFO"`) {
		t.Error("Expected JSON to contain level")
	}
	if !strings.Contains(jsonStr, `"message":"Test message"`) {
		t.Error("Expected JSON to contain message")
	}
	if !strings.Contains(jsonStr, `"correlation_id":"test-id"`) {
		t.Error("Expected JSON to contain correlation_id")
	}
}
