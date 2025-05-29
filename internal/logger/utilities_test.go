package logger

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithComponent(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := WithComponent(logrusLogger, "test-component")
	require.NotNil(t, entry)

	entry.Info("component test message")

	output := buf.String()
	assert.Contains(t, output, "component")
	assert.Contains(t, output, "test-component")
	assert.Contains(t, output, "component test message")
}

func TestWithStream(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	entry := WithStream(logrusLogger, "stream-123")
	require.NotNil(t, entry)

	entry.Info("stream test message")

	output := buf.String()
	assert.Contains(t, output, "stream_id")
	assert.Contains(t, output, "stream-123")
	assert.Contains(t, output, "stream test message")
}

func TestWithError(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	testErr := errors.New("test error for utility function")
	entry := WithError(logrusLogger, testErr)
	require.NotNil(t, entry)

	entry.Error("error test message")

	output := buf.String()
	assert.Contains(t, output, "error")
	assert.Contains(t, output, "test error for utility function")
	assert.Contains(t, output, "error test message")
}

func TestUtilityFunctionChaining(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	testErr := errors.New("chained error")

	// Test chaining utility functions
	entry := WithComponent(logrusLogger, "ingestion").
		WithField("stream_id", "stream-456").
		WithError(testErr)

	entry.Warn("chained utility test")

	output := buf.String()
	assert.Contains(t, output, "component")
	assert.Contains(t, output, "ingestion")
	assert.Contains(t, output, "stream_id")
	assert.Contains(t, output, "stream-456")
	assert.Contains(t, output, "error")
	assert.Contains(t, output, "chained error")
	assert.Contains(t, output, "chained utility test")
}

func TestUtilityFunctions_EmptyValues(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	tests := []struct {
		name     string
		setupFn  func() *logrus.Entry
		expected []string
	}{
		{
			name: "empty component",
			setupFn: func() *logrus.Entry {
				return WithComponent(logrusLogger, "")
			},
			expected: []string{"component", `"component":""`},
		},
		{
			name: "empty stream ID",
			setupFn: func() *logrus.Entry {
				return WithStream(logrusLogger, "")
			},
			expected: []string{"stream_id", `"stream_id":""`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			entry := tt.setupFn()
			entry.Info("empty value test")

			output := buf.String()
			for _, expected := range tt.expected {
				assert.Contains(t, output, expected)
			}
			assert.Contains(t, output, "empty value test")
		})
	}
}

func TestUtilityFunctions_SpecialCharacters(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	tests := []struct {
		name      string
		component string
		streamID  string
	}{
		{
			name:      "special characters in component",
			component: "test-component@domain.com",
			streamID:  "normal-stream",
		},
		{
			name:      "unicode in stream ID",
			component: "normal-component",
			streamID:  "stream-测试-🎥",
		},
		{
			name:      "symbols and spaces",
			component: "comp with spaces & symbols!",
			streamID:  "stream/with/slashes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			entry := WithComponent(logrusLogger, tt.component).
				WithField("stream_id", tt.streamID)
			entry.Info("special characters test")

			output := buf.String()
			assert.Contains(t, output, "component")
			assert.Contains(t, output, "stream_id")
			assert.Contains(t, output, "special characters test")
			// JSON should properly escape special characters
			assert.Contains(t, output, `"component"`)
			assert.Contains(t, output, `"stream_id"`)
		})
	}
}

func TestFields_TypeAlias(t *testing.T) {
	// Test that Fields type alias works correctly
	fields := Fields{
		"key1": "value1",
		"key2": 42,
		"key3": true,
	}

	// Should be assignable to logrus.Fields
	var logrusFields logrus.Fields = fields
	assert.Equal(t, fields["key1"], logrusFields["key1"])
	assert.Equal(t, fields["key2"], logrusFields["key2"])
	assert.Equal(t, fields["key3"], logrusFields["key3"])
}

func TestEntry_TypeAlias(t *testing.T) {
	// Test that Entry type alias works correctly
	logrusLogger := logrus.New()
	logrusEntry := logrus.NewEntry(logrusLogger)

	// Should be assignable to Entry
	var entry *Entry = logrusEntry
	assert.NotNil(t, entry)
	assert.Equal(t, logrusEntry, entry)
}

func TestUtilityFunctions_Integration(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	// Simulate a real-world logging scenario
	component := "video-processor"
	streamID := "live-stream-001"
	processingErr := errors.New("frame decode failed")

	// Use all utility functions together
	entry := WithComponent(logrusLogger, component)
	streamEntry := WithStream(logrusLogger, streamID)
	errorEntry := WithError(logrusLogger, processingErr)

	// Test each utility function produces expected output
	buf.Reset()
	entry.Info("component logging")
	output1 := buf.String()
	assert.Contains(t, output1, "video-processor")
	assert.Contains(t, output1, "component logging")

	buf.Reset()
	streamEntry.Info("stream logging")
	output2 := buf.String()
	assert.Contains(t, output2, "live-stream-001")
	assert.Contains(t, output2, "stream logging")

	buf.Reset()
	errorEntry.Error("error logging")
	output3 := buf.String()
	assert.Contains(t, output3, "frame decode failed")
	assert.Contains(t, output3, "error logging")

	// Test combined usage
	buf.Reset()
	WithComponent(logrusLogger, component).
		WithField("stream_id", streamID).
		WithError(processingErr).
		Error("processing failed")

	combinedOutput := buf.String()
	assert.Contains(t, combinedOutput, "video-processor")
	assert.Contains(t, combinedOutput, "live-stream-001")
	assert.Contains(t, combinedOutput, "frame decode failed")
	assert.Contains(t, combinedOutput, "processing failed")
}

func TestUtilityFunctions_NilError(t *testing.T) {
	var buf bytes.Buffer
	logrusLogger := logrus.New()
	logrusLogger.SetOutput(&buf)
	logrusLogger.SetFormatter(&logrus.JSONFormatter{})
	logrusLogger.SetLevel(logrus.DebugLevel)

	// Test WithError with nil error
	entry := WithError(logrusLogger, nil)
	require.NotNil(t, entry)

	entry.Info("nil error test")

	output := buf.String()
	// Should still log, but error field should be null/empty
	assert.Contains(t, output, "nil error test")
}
