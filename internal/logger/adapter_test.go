package logger

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogrusAdapter_Creation(t *testing.T) {
	logrusLogger := logrus.New()
	entry := logrus.NewEntry(logrusLogger)

	adapter := NewLogrusAdapter(entry)
	require.NotNil(t, adapter)

	logrusAdapter, ok := adapter.(*LogrusAdapter)
	require.True(t, ok)
	assert.Equal(t, entry, logrusAdapter.entry)
}

func TestLogrusAdapter_WithFields(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	fields := map[string]interface{}{
		"stream_id": "test-stream",
		"component": "test-component",
		"number":    42,
	}

	newAdapter := adapter.WithFields(fields)
	require.NotNil(t, newAdapter)

	// Test that the new adapter has the fields
	newAdapter.Info("test message")

	output := buf.String()
	assert.Contains(t, output, "stream_id")
	assert.Contains(t, output, "test-stream")
	assert.Contains(t, output, "component")
	assert.Contains(t, output, "test-component")
	assert.Contains(t, output, "number")
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "test message")
}

func TestLogrusAdapter_WithField(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	newAdapter := adapter.WithField("request_id", "req-123")
	require.NotNil(t, newAdapter)

	newAdapter.Info("processing request")

	output := buf.String()
	assert.Contains(t, output, "request_id")
	assert.Contains(t, output, "req-123")
	assert.Contains(t, output, "processing request")
}

func TestLogrusAdapter_WithError(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	testErr := errors.New("test error message")
	newAdapter := adapter.WithError(testErr)
	require.NotNil(t, newAdapter)

	newAdapter.Error("operation failed")

	output := buf.String()
	assert.Contains(t, output, "error")
	assert.Contains(t, output, "test error message")
	assert.Contains(t, output, "operation failed")
}

func TestLogrusAdapter_LogLevels(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
	})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	tests := []struct {
		name     string
		logFunc  func(args ...interface{})
		message  string
		expected string
	}{
		{
			name:     "Debug level",
			logFunc:  adapter.Debug,
			message:  "debug message",
			expected: "level=debug",
		},
		{
			name:     "Info level",
			logFunc:  adapter.Info,
			message:  "info message",
			expected: "level=info",
		},
		{
			name:     "Warn level",
			logFunc:  adapter.Warn,
			message:  "warn message",
			expected: "level=warning",
		},
		{
			name:     "Error level",
			logFunc:  adapter.Error,
			message:  "error message",
			expected: "level=error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			tt.logFunc(tt.message)

			output := buf.String()
			assert.Contains(t, output, tt.expected)
			assert.Contains(t, output, tt.message)
		})
	}
}

func TestLogrusAdapter_LogWithLevel(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
	})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	adapter.Log(logrus.WarnLevel, "warning via Log method")

	output := buf.String()
	assert.Contains(t, output, "level=warning")
	assert.Contains(t, output, "warning via Log method")
}

func TestLogrusAdapter_FormattedMethods(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
	})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	tests := []struct {
		name     string
		logFunc  func(format string, args ...interface{})
		format   string
		args     []interface{}
		expected string
		level    string
	}{
		{
			name:     "Debugf",
			logFunc:  adapter.Debugf,
			format:   "debug: %s = %d",
			args:     []interface{}{"count", 42},
			expected: "debug: count = 42",
			level:    "level=debug",
		},
		{
			name:     "Infof",
			logFunc:  adapter.Infof,
			format:   "info: processing %s",
			args:     []interface{}{"stream-123"},
			expected: "info: processing stream-123",
			level:    "level=info",
		},
		{
			name:     "Warnf",
			logFunc:  adapter.Warnf,
			format:   "warn: %s failed with code %d",
			args:     []interface{}{"operation", 500},
			expected: "warn: operation failed with code 500",
			level:    "level=warning",
		},
		{
			name:     "Errorf",
			logFunc:  adapter.Errorf,
			format:   "error: %s at line %d",
			args:     []interface{}{"syntax error", 42},
			expected: "error: syntax error at line 42",
			level:    "level=error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			tt.logFunc(tt.format, tt.args...)

			output := buf.String()
			assert.Contains(t, output, tt.level)
			assert.Contains(t, output, tt.expected)
		})
	}
}

func TestLogrusAdapter_Fatal(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
	})
	logrusLogger.SetLevel(logrus.DebugLevel)

	// Override the exit function to prevent actual exit
	originalExitFunc := logrusLogger.ExitFunc
	exitCalled := false
	logrusLogger.ExitFunc = func(code int) {
		exitCalled = true
		assert.Equal(t, 1, code)
	}
	defer func() {
		logrusLogger.ExitFunc = originalExitFunc
	}()

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	adapter.Fatal("fatal error occurred")

	output := buf.String()
	assert.Contains(t, output, "level=fatal")
	assert.Contains(t, output, "fatal error occurred")
	assert.True(t, exitCalled, "Exit function should have been called")
}

func TestLogrusAdapter_ChainedWithMethods(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	testErr := errors.New("chained error")

	// Chain multiple With methods
	adapter.WithField("component", "test").
		WithFields(map[string]interface{}{
			"stream_id": "stream-456",
			"operation": "test-op",
		}).
		WithError(testErr).
		Info("chained logging test")

	output := buf.String()
	assert.Contains(t, output, "component")
	assert.Contains(t, output, "test")
	assert.Contains(t, output, "stream_id")
	assert.Contains(t, output, "stream-456")
	assert.Contains(t, output, "operation")
	assert.Contains(t, output, "test-op")
	assert.Contains(t, output, "error")
	assert.Contains(t, output, "chained error")
	assert.Contains(t, output, "chained logging test")
}

func TestLogrusAdapter_ImmutableChaining(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	// Create a new adapter with a field
	adapter1 := adapter.WithField("step", 1)
	adapter2 := adapter.WithField("step", 2)

	// Log with each adapter
	buf.Reset()
	adapter1.Info("first step")
	output1 := buf.String()

	buf.Reset()
	adapter2.Info("second step")
	output2 := buf.String()

	buf.Reset()
	adapter.Info("original")
	output3 := buf.String()

	// Verify each adapter has its own fields
	assert.Contains(t, output1, `"step":1`)
	assert.Contains(t, output1, "first step")

	assert.Contains(t, output2, `"step":2`)
	assert.Contains(t, output2, "second step")

	// Original adapter should not have step field
	assert.NotContains(t, output3, "step")
	assert.Contains(t, output3, "original")
}

func TestLogrusAdapter_ComplexFields(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := logrus.NewEntry(logrusLogger)
	adapter := NewLogrusAdapter(entry)

	complexFields := map[string]interface{}{
		"string_field": "test_value",
		"int_field":    42,
		"float_field":  3.14159,
		"bool_field":   true,
		"slice_field":  []string{"a", "b", "c"},
		"nil_field":    nil,
	}

	adapter.WithFields(complexFields).Info("complex fields test")

	output := buf.String()
	assert.Contains(t, output, "string_field")
	assert.Contains(t, output, "test_value")
	assert.Contains(t, output, "int_field")
	assert.Contains(t, output, "42")
	assert.Contains(t, output, "float_field")
	assert.Contains(t, output, "3.14159")
	assert.Contains(t, output, "bool_field")
	assert.Contains(t, output, "true")
	assert.Contains(t, output, "complex fields test")
}
