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

// Logger defines the interface for structured logging
type Logger interface {
	WithFields(fields map[string]interface{}) Logger
	WithField(key string, value interface{}) Logger
	WithError(err error) Logger
	Debug(args ...interface{})
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Log(level logrus.Level, args ...interface{})
	// Printf-style methods for backward compatibility
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
}

// LogrusAdapter wraps logrus.Entry to implement Logger interface
type LogrusAdapter struct {
	entry *logrus.Entry
}

// NewLogrusAdapter creates a new LogrusAdapter
func NewLogrusAdapter(entry *logrus.Entry) Logger {
	return &LogrusAdapter{entry: entry}
}

// WithFields implements Logger interface
func (l *LogrusAdapter) WithFields(fields map[string]interface{}) Logger {
	return &LogrusAdapter{entry: l.entry.WithFields(fields)}
}

// WithField implements Logger interface
func (l *LogrusAdapter) WithField(key string, value interface{}) Logger {
	return &LogrusAdapter{entry: l.entry.WithField(key, value)}
}

// WithError implements Logger interface
func (l *LogrusAdapter) WithError(err error) Logger {
	return &LogrusAdapter{entry: l.entry.WithError(err)}
}

// Debug implements Logger interface
func (l *LogrusAdapter) Debug(args ...interface{}) {
	l.entry.Debug(args...)
}

// Info implements Logger interface
func (l *LogrusAdapter) Info(args ...interface{}) {
	l.entry.Info(args...)
}

// Warn implements Logger interface
func (l *LogrusAdapter) Warn(args ...interface{}) {
	l.entry.Warn(args...)
}

// Error implements Logger interface
func (l *LogrusAdapter) Error(args ...interface{}) {
	l.entry.Error(args...)
}

// Log implements Logger interface
func (l *LogrusAdapter) Log(level logrus.Level, args ...interface{}) {
	l.entry.Log(level, args...)
}

// Printf-style methods for backward compatibility
func (l *LogrusAdapter) Debugf(format string, args ...interface{}) {
	l.entry.Debugf(format, args...)
}

func (l *LogrusAdapter) Infof(format string, args ...interface{}) {
	l.entry.Infof(format, args...)
}

func (l *LogrusAdapter) Warnf(format string, args ...interface{}) {
	l.entry.Warnf(format, args...)
}

func (l *LogrusAdapter) Errorf(format string, args ...interface{}) {
	l.entry.Errorf(format, args...)
}

func (l *LogrusAdapter) Fatal(args ...interface{}) {
	l.entry.Log(logrus.FatalLevel, args...)
	l.entry.Logger.Exit(1)
}

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
			MaxSize:    cfg.MaxSize, // megabytes
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
