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

package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// LogLevel represents the logging level
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARNING
	ERROR
	CRITICAL
)

var levelNames = map[LogLevel]string{
	DEBUG:    "DEBUG",
	INFO:     "INFO",
	WARNING:  "WARNING",
	ERROR:    "ERROR",
	CRITICAL: "CRITICAL",
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp     string                 `json:"timestamp"`
	Level         string                 `json:"level"`
	Message       string                 `json:"message"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	User          string                 `json:"user,omitempty"`
	Operation     string                 `json:"operation,omitempty"`
	Resource      string                 `json:"resource,omitempty"`
	Duration      string                 `json:"duration,omitempty"`
	Status        string                 `json:"status,omitempty"`
	Error         string                 `json:"error,omitempty"`
	Fields        map[string]interface{} `json:"fields,omitempty"`
}

// Logger provides structured logging functionality
type Logger struct {
	level LogLevel
	mu    sync.Mutex
}

var defaultLogger *Logger

// Init initializes the default logger with the specified level
func Init(level string) {
	defaultLogger = &Logger{
		level: parseLogLevel(level),
	}
}

// parseLogLevel converts a string level to LogLevel
func parseLogLevel(level string) LogLevel {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARNING":
		return WARNING
	case "ERROR":
		return ERROR
	case "CRITICAL":
		return CRITICAL
	default:
		return INFO
	}
}

// shouldLog checks if the message should be logged at the current level
func (l *Logger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

// formatStructuredMessage formats a log message as structured JSON
func (l *Logger) formatStructuredMessage(level LogLevel, message string, fields map[string]interface{}) string {
	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level:     levelNames[level],
		Message:   message,
		Fields:    fields,
	}

	// Add correlation ID if present in context
	if ctx := getContext(); ctx != nil {
		if correlationID := getCorrelationID(ctx); correlationID != "" {
			entry.CorrelationID = correlationID
		}
	}

	jsonData, err := json.Marshal(entry)
	if err != nil {
		// Fallback to simple format if JSON marshaling fails
		return fmt.Sprintf("%s %s: %s", entry.Timestamp, entry.Level, message)
	}

	return string(jsonData)
}

// formatSimpleMessage formats a log message with timestamp and level (legacy format)
func (l *Logger) formatSimpleMessage(level LogLevel, message string, args ...interface{}) string {
	timestamp := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	levelStr := levelNames[level]

	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}

	return fmt.Sprintf("%s %s: %s", timestamp, levelStr, message)
}

// Debug logs a debug message
func (l *Logger) Debug(message string, args ...interface{}) {
	if l.shouldLog(DEBUG) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Print(l.formatSimpleMessage(DEBUG, message, args...))
	}
}

// DebugStructured logs a structured debug message
func (l *Logger) DebugStructured(message string, fields map[string]interface{}) {
	if l.shouldLog(DEBUG) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Print(l.formatStructuredMessage(DEBUG, message, fields))
	}
}

// Info logs an info message
func (l *Logger) Info(message string, args ...interface{}) {
	if l.shouldLog(INFO) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Print(l.formatSimpleMessage(INFO, message, args...))
	}
}

// InfoStructured logs a structured info message
func (l *Logger) InfoStructured(message string, fields map[string]interface{}) {
	if l.shouldLog(INFO) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatStructuredMessage(INFO, message, fields))
	}
}

// Warning logs a warning message
func (l *Logger) Warning(message string, args ...interface{}) {
	if l.shouldLog(WARNING) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatSimpleMessage(WARNING, message, args...))
	}
}

// WarningStructured logs a structured warning message
func (l *Logger) WarningStructured(message string, fields map[string]interface{}) {
	if l.shouldLog(WARNING) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatStructuredMessage(WARNING, message, fields))
	}
}

// Error logs an error message
func (l *Logger) Error(message string, args ...interface{}) {
	if l.shouldLog(ERROR) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatSimpleMessage(ERROR, message, args...))
	}
}

// ErrorStructured logs a structured error message
func (l *Logger) ErrorStructured(message string, fields map[string]interface{}) {
	if l.shouldLog(ERROR) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatStructuredMessage(ERROR, message, fields))
	}
}

// Critical logs a critical message
func (l *Logger) Critical(message string, args ...interface{}) {
	if l.shouldLog(CRITICAL) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatSimpleMessage(CRITICAL, message, args...))
	}
}

// CriticalStructured logs a structured critical message
func (l *Logger) CriticalStructured(message string, fields map[string]interface{}) {
	if l.shouldLog(CRITICAL) {
		l.mu.Lock()
		defer l.mu.Unlock()
		log.Printf(l.formatStructuredMessage(CRITICAL, message, fields))
	}
}

// Package-level convenience functions
func Debug(message string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Debug(message, args...)
	}
}

func DebugStructured(message string, fields map[string]interface{}) {
	if defaultLogger != nil {
		defaultLogger.DebugStructured(message, fields)
	}
}

func Info(message string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Info(message, args...)
	}
}

func InfoStructured(message string, fields map[string]interface{}) {
	if defaultLogger != nil {
		defaultLogger.InfoStructured(message, fields)
	}
}

func Warning(message string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Warning(message, args...)
	}
}

func WarningStructured(message string, fields map[string]interface{}) {
	if defaultLogger != nil {
		defaultLogger.WarningStructured(message, fields)
	}
}

func Error(message string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Error(message, args...)
	}
}

func ErrorStructured(message string, fields map[string]interface{}) {
	if defaultLogger != nil {
		defaultLogger.ErrorStructured(message, fields)
	}
}

func Critical(message string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Critical(message, args...)
	}
}

func CriticalStructured(message string, fields map[string]interface{}) {
	if defaultLogger != nil {
		defaultLogger.CriticalStructured(message, fields)
	}
}

// Context and correlation ID management
type contextKey string

const (
	correlationIDKey contextKey = "correlation_id"
	userKey          contextKey = "user"
	operationKey     contextKey = "operation"
	resourceKey      contextKey = "resource"
)

var currentContext context.Context

// SetContext sets the current context for logging
func SetContext(ctx context.Context) {
	currentContext = ctx
}

// getContext gets the current context
func getContext() context.Context {
	return currentContext
}

// getCorrelationID extracts correlation ID from context
func getCorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(correlationIDKey).(string); ok {
		return id
	}
	return ""
}

// GetCorrelationID extracts correlation ID from context (public function)
func GetCorrelationID(ctx context.Context) string {
	return getCorrelationID(ctx)
}

// WithCorrelationID creates a new context with correlation ID
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationIDKey, correlationID)
}

// WithUser creates a new context with user information
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey, user)
}

// WithOperation creates a new context with operation information
func WithOperation(ctx context.Context, operation string) context.Context {
	return context.WithValue(ctx, operationKey, operation)
}

// WithResource creates a new context with resource information
func WithResource(ctx context.Context, resource string) context.Context {
	return context.WithValue(ctx, resourceKey, resource)
}

// LogRequest logs an incoming request with correlation ID
func LogRequest(method, path, user string, correlationID string) {
	fields := map[string]interface{}{
		"correlation_id": correlationID,
		"user":           user,
		"operation":      "request",
		"method":         method,
		"path":           path,
		"status":         "started",
	}

	InfoStructured("Redfish API request received", fields)
}

// LogResponse logs a response with correlation ID and duration
func LogResponse(method, path, user string, correlationID string, statusCode int, duration time.Duration) {
	fields := map[string]interface{}{
		"correlation_id": correlationID,
		"user":           user,
		"operation":      "response",
		"method":         method,
		"path":           path,
		"status":         "completed",
		"status_code":    statusCode,
		"duration":       duration.String(),
	}

	InfoStructured("Redfish API response completed", fields)
}

// LogKubeVirtOperation logs a KubeVirt operation with correlation ID
func LogKubeVirtOperation(operation, namespace, resource string, correlationID string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}

	fields["correlation_id"] = correlationID
	fields["operation"] = operation
	fields["namespace"] = namespace
	fields["resource"] = resource
	fields["component"] = "kubevirt"

	DebugStructured("KubeVirt operation executed", fields)
}

// GetLogLevelFromEnv gets the log level from environment variable
func GetLogLevelFromEnv() string {
	level := os.Getenv("REDFISH_LOG_LEVEL")
	if level == "" {
		level = "INFO"
	}
	return level
}

// IsLoggingEnabled checks if logging is enabled via environment variable
func IsLoggingEnabled() bool {
	enabled := os.Getenv("REDFISH_LOGGING_ENABLED")
	if enabled == "" {
		return true
	}
	return strings.ToLower(enabled) == "true"
}
