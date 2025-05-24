package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		config  *config.LoggingConfig
		wantErr bool
		check   func(t *testing.T, logger *logrus.Logger)
	}{
		{
			name: "json format stdout",
			config: &config.LoggingConfig{
				Level:  "info",
				Format: "json",
				Output: "stdout",
			},
			wantErr: false,
			check: func(t *testing.T, logger *logrus.Logger) {
				assert.Equal(t, logrus.InfoLevel, logger.Level)
				_, ok := logger.Formatter.(*logrus.JSONFormatter)
				assert.True(t, ok)
			},
		},
		{
			name: "text format stderr",
			config: &config.LoggingConfig{
				Level:  "debug",
				Format: "text",
				Output: "stderr",
			},
			wantErr: false,
			check: func(t *testing.T, logger *logrus.Logger) {
				assert.Equal(t, logrus.DebugLevel, logger.Level)
				_, ok := logger.Formatter.(*logrus.TextFormatter)
				assert.True(t, ok)
			},
		},
		{
			name: "file output",
			config: &config.LoggingConfig{
				Level:      "warn",
				Format:     "json",
				Output:     filepath.Join(t.TempDir(), "test.log"),
				MaxSize:    10,
				MaxBackups: 3,
				MaxAge:     7,
			},
			wantErr: false,
			check: func(t *testing.T, logger *logrus.Logger) {
				assert.Equal(t, logrus.WarnLevel, logger.Level)
			},
		},
		{
			name: "invalid log level",
			config: &config.LoggingConfig{
				Level:  "invalid",
				Format: "json",
				Output: "stdout",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := New(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, logger)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, logger)
				if tt.check != nil {
					tt.check(t, logger)
				}
			}
		})
	}
}

func TestLoggerHelpers(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(&bytes.Buffer{})

	t.Run("WithComponent", func(t *testing.T) {
		entry := WithComponent(logger, "test-component")
		assert.Equal(t, "test-component", entry.Data["component"])
	})

	t.Run("WithRequestID", func(t *testing.T) {
		ctx := context.Background()
		ctx = WithRequestID(ctx, "req-123")
		assert.Equal(t, "req-123", GetRequestID(ctx))
	})

	t.Run("WithStream", func(t *testing.T) {
		entry := WithStream(logger, "stream-456")
		assert.Equal(t, "stream-456", entry.Data["stream_id"])
	})

	t.Run("WithError", func(t *testing.T) {
		err := assert.AnError
		entry := WithError(logger, err)
		assert.Equal(t, err, entry.Data[logrus.ErrorKey])
	})
}

func TestJSONLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&buf)
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
	})

	// Log a message
	logger.WithFields(logrus.Fields{
		"component": "test",
		"action":    "testing",
	}).Info("Test message")

	// Parse the output
	var logEntry map[string]interface{}
	err := json.Unmarshal(buf.Bytes(), &logEntry)
	require.NoError(t, err)

	assert.Equal(t, "info", logEntry["level"])
	assert.Equal(t, "Test message", logEntry["msg"])
	assert.Equal(t, "test", logEntry["component"])
	assert.Equal(t, "testing", logEntry["action"])
	assert.Contains(t, logEntry, "time")
}

func TestFileOutput(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	cfg := &config.LoggingConfig{
		Level:      "info",
		Format:     "text",
		Output:     logFile,
		MaxSize:    1,
		MaxBackups: 1,
		MaxAge:     1,
	}

	logger, err := New(cfg)
	require.NoError(t, err)

	// Write some logs
	logger.Info("Test log message")

	// Check file exists
	_, err = os.Stat(logFile)
	assert.NoError(t, err)
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		level    string
		expected logrus.Level
	}{
		{"panic", logrus.PanicLevel},
		{"fatal", logrus.FatalLevel},
		{"error", logrus.ErrorLevel},
		{"warn", logrus.WarnLevel},
		{"info", logrus.InfoLevel},
		{"debug", logrus.DebugLevel},
		{"trace", logrus.TraceLevel},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg := &config.LoggingConfig{
				Level:  tt.level,
				Format: "json",
				Output: "stdout",
			}

			logger, err := New(cfg)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, logger.Level)
		})
	}
}