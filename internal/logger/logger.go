package logger

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/pkg/version"
)

// New creates a new configured logger instance.
func New(cfg *config.LoggingConfig) (*logrus.Logger, error) {
	logger := logrus.New()

	// Set log level
	level, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}
	logger.SetLevel(level)

	// Set formatter
	if cfg.Format == "json" {
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyTime:  "timestamp",
				logrus.FieldKeyLevel: "level",
				logrus.FieldKeyMsg:   "message",
			},
		})
	} else {
		logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05.000",
			DisableColors:   false,
		})
	}

	// Set output
	switch cfg.Output {
	case "stdout":
		logger.SetOutput(os.Stdout)
	case "stderr":
		logger.SetOutput(os.Stderr)
	default:
		// File output with rotation
		// Ensure directory exists
		dir := filepath.Dir(cfg.Output)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		logger.SetOutput(&lumberjack.Logger{
			Filename:   cfg.Output,
			MaxSize:    cfg.MaxSize,    // megabytes
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge, // days
			Compress:   true,
		})
	}

	// Add default fields
	logger = logger.WithFields(logrus.Fields{
		"service": "mirror",
		"version": version.GetInfo().Short(),
	}).Logger

	return logger, nil
}

// WithComponent creates a logger entry with component field.
func WithComponent(logger *logrus.Logger, component string) *logrus.Entry {
	return logger.WithField("component", component)
}


// WithStream creates a logger entry with stream information.
func WithStream(logger *logrus.Logger, streamID string) *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"stream_id": streamID,
	})
}

// WithError creates a logger entry with error details.
func WithError(logger *logrus.Logger, err error) *logrus.Entry {
	return logger.WithError(err)
}

// Fields is a type alias for logrus.Fields for convenience
type Fields = logrus.Fields

// Entry is a type alias for logrus.Entry
type Entry = logrus.Entry

// Logger interface that both logrus.Logger and logrus.Entry implement
type Logger interface {
	WithField(key string, value interface{}) *logrus.Entry
	WithFields(fields logrus.Fields) *logrus.Entry
	WithError(err error) *logrus.Entry

	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Printf(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	Panicf(format string, args ...interface{})

	Debug(args ...interface{})
	Info(args ...interface{})
	Print(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Fatal(args ...interface{})
	Panic(args ...interface{})
}
