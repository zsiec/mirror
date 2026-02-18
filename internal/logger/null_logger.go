package logger

import "github.com/sirupsen/logrus"

// NullLogger is a logger that discards all log messages
type NullLogger struct{}

// NewNullLogger creates a new null logger that discards all output
func NewNullLogger() Logger {
	return &NullLogger{}
}

// WithFields implements Logger interface
func (n *NullLogger) WithFields(fields map[string]interface{}) Logger {
	return n
}

// WithField implements Logger interface
func (n *NullLogger) WithField(key string, value interface{}) Logger {
	return n
}

// WithError implements Logger interface
func (n *NullLogger) WithError(err error) Logger {
	return n
}

// Debug implements Logger interface
func (n *NullLogger) Debug(args ...interface{}) {}

// Info implements Logger interface
func (n *NullLogger) Info(args ...interface{}) {}

// Warn implements Logger interface
func (n *NullLogger) Warn(args ...interface{}) {}

// Error implements Logger interface
func (n *NullLogger) Error(args ...interface{}) {}

// Log implements Logger interface
func (n *NullLogger) Log(level logrus.Level, args ...interface{}) {}

// Debugf implements Logger interface
func (n *NullLogger) Debugf(format string, args ...interface{}) {}

// Infof implements Logger interface
func (n *NullLogger) Infof(format string, args ...interface{}) {}

// Warnf implements Logger interface
func (n *NullLogger) Warnf(format string, args ...interface{}) {}

// Errorf implements Logger interface
func (n *NullLogger) Errorf(format string, args ...interface{}) {}

// Fatal implements Logger interface
func (n *NullLogger) Fatal(args ...interface{}) {
	// NullLogger should not exit on Fatal
}
