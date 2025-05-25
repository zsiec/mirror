# Logger Package

The `logger` package provides structured, context-aware logging for the Mirror platform. Built on [logrus](https://github.com/sirupsen/logrus), it offers high-performance logging with automatic log rotation, context propagation, and request tracing.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Usage](#usage)
- [Context Logging](#context-logging)
- [Log Formats](#log-formats)
- [Log Rotation](#log-rotation)
- [Performance](#performance)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The logging system provides:

- **Structured logging** with fields for better parsing
- **Context propagation** for request tracing
- **Multiple output formats** (JSON, text)
- **Log rotation** with size and age limits
- **Performance optimized** with minimal allocations
- **Level-based filtering** for different environments
- **Request correlation** with trace IDs

## Features

### Core Capabilities

- **Zero allocation** for disabled log levels
- **Field reuse** to minimize garbage collection
- **Buffered writes** for better throughput
- **Async logging** option for critical paths
- **Log sampling** for high-frequency events
- **Error tracking** with stack traces
- **Metrics integration** for log volume monitoring

### Log Levels

```go
const (
    TraceLevel Level = iota  // Detailed debugging
    DebugLevel              // Debug information
    InfoLevel               // General information
    WarnLevel               // Warning messages
    ErrorLevel              // Error conditions
    FatalLevel              // Fatal errors (exits program)
    PanicLevel              // Panic conditions
)
```

## Usage

### Basic Setup

```go
import "github.com/zsiec/mirror/internal/logger"

// Create logger from config
log, err := logger.New(&config.LoggingConfig{
    Level:      "info",
    Format:     "json",
    Output:     "stdout",
    MaxSize:    100,      // MB
    MaxBackups: 5,
    MaxAge:     30,       // days
})

// Basic logging
log.Info("Server started")
log.WithField("port", 8080).Info("Listening on port")
log.WithError(err).Error("Failed to connect to database")
```

### Structured Logging

```go
// Log with multiple fields
log.WithFields(logger.Fields{
    "stream_id": "abc123",
    "protocol":  "srt",
    "bitrate":   5000000,
    "client_ip": "192.168.1.100",
}).Info("Stream started")

// Reuse fields for performance
fields := logger.Fields{
    "component": "ingestion",
    "version":   "1.0.0",
}

entry := log.WithFields(fields)
entry.Info("Component initialized")
entry.Debug("Processing started")
```

## Context Logging

### Request Context

```go
// context.go
type contextKey string

const (
    RequestIDKey   contextKey = "request_id"
    UserIDKey      contextKey = "user_id"
    StreamIDKey    contextKey = "stream_id"
    ComponentKey   contextKey = "component"
)

// Add logger to context
func WithLogger(ctx context.Context, logger logger.Logger) context.Context {
    return context.WithValue(ctx, loggerKey, logger)
}

// Get logger from context
func FromContext(ctx context.Context) logger.Logger {
    if l, ok := ctx.Value(loggerKey).(logger.Logger); ok {
        return l
    }
    return logger.Default()
}
```

### Using Context Logger

```go
// HTTP middleware example
func LoggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Create request logger
        requestID := generateRequestID()
        log := logger.WithFields(logger.Fields{
            "request_id": requestID,
            "method":     r.Method,
            "path":       r.URL.Path,
            "remote_ip":  r.RemoteAddr,
        })
        
        // Add to context
        ctx := logger.WithLogger(r.Context(), log)
        r = r.WithContext(ctx)
        
        // Log request
        log.Info("Request started")
        
        // Track response
        wrapped := &responseWriter{ResponseWriter: w}
        start := time.Now()
        
        next.ServeHTTP(wrapped, r)
        
        // Log response
        log.WithFields(logger.Fields{
            "status":      wrapped.status,
            "bytes":       wrapped.bytes,
            "duration_ms": time.Since(start).Milliseconds(),
        }).Info("Request completed")
    })
}
```

### Service Layer Logging

```go
func (s *StreamService) StartStream(ctx context.Context, config StreamConfig) error {
    log := logger.FromContext(ctx).WithField("stream_id", config.ID)
    
    log.Info("Starting stream")
    
    // Validate configuration
    if err := s.validate(config); err != nil {
        log.WithError(err).Error("Configuration validation failed")
        return err
    }
    
    // Start ingestion
    log.Debug("Initializing ingestion")
    if err := s.ingestion.Start(ctx, config); err != nil {
        log.WithError(err).Error("Failed to start ingestion")
        return err
    }
    
    log.WithField("protocol", config.Protocol).Info("Stream started successfully")
    return nil
}
```

## Log Formats

### JSON Format

```json
{
  "time": "2024-01-20T15:30:45.123Z",
  "level": "info",
  "msg": "Stream started",
  "request_id": "req_abc123",
  "stream_id": "stream_xyz",
  "protocol": "srt",
  "bitrate": 5000000,
  "duration_ms": 1250
}
```

### Text Format

```
2024-01-20 15:30:45.123 [INFO] Stream started request_id=req_abc123 stream_id=stream_xyz protocol=srt bitrate=5000000 duration_ms=1250
```

### Custom Formatters

```go
// Custom formatter for development
type DevFormatter struct{}

func (f *DevFormatter) Format(entry *logrus.Entry) ([]byte, error) {
    timestamp := entry.Time.Format("15:04:05.000")
    level := strings.ToUpper(entry.Level.String())[0:4]
    
    // Color coding for terminals
    var levelColor string
    switch entry.Level {
    case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
        levelColor = "\033[31m" // Red
    case logrus.WarnLevel:
        levelColor = "\033[33m" // Yellow
    default:
        levelColor = "\033[36m" // Cyan
    }
    
    msg := fmt.Sprintf("%s %s%s\033[0m %s",
        timestamp, levelColor, level, entry.Message)
    
    // Add fields
    for key, value := range entry.Data {
        msg += fmt.Sprintf(" %s=%v", key, value)
    }
    
    return []byte(msg + "\n"), nil
}
```

## Log Rotation

### File Rotation with Lumberjack

```go
// Automatic rotation based on size and age
import "gopkg.in/natefinch/lumberjack.v2"

func NewRotatingLogger(config *LoggingConfig) (logger.Logger, error) {
    log := logrus.New()
    
    if config.Output != "stdout" && config.Output != "stderr" {
        log.SetOutput(&lumberjack.Logger{
            Filename:   config.Output,
            MaxSize:    config.MaxSize,    // MB
            MaxBackups: config.MaxBackups,
            MaxAge:     config.MaxAge,      // days
            Compress:   config.Compress,
            LocalTime:  true,
        })
    }
    
    return log, nil
}
```

### Configuration Example

```yaml
logging:
  level: info
  format: json
  output: /var/log/mirror/app.log
  max_size: 100        # Rotate at 100MB
  max_backups: 5       # Keep 5 old files
  max_age: 30          # Delete after 30 days
  compress: true       # Gzip old files
```

## Performance

### Zero Allocation Logging

```go
// Pre-allocate fields for hot paths
type StreamLogger struct {
    logger logger.Logger
    fields logger.Fields
}

func NewStreamLogger(streamID string) *StreamLogger {
    return &StreamLogger{
        logger: logger.Default(),
        fields: logger.Fields{
            "stream_id": streamID,
            "component": "stream",
        },
    }
}

func (sl *StreamLogger) LogPacket(seq uint32, size int) {
    if !sl.logger.IsDebugEnabled() {
        return // Zero allocation if disabled
    }
    
    // Reuse fields
    sl.fields["seq"] = seq
    sl.fields["size"] = size
    sl.logger.WithFields(sl.fields).Debug("Packet received")
}
```

### Log Sampling

```go
// Sample high-frequency logs
type SampledLogger struct {
    logger   logger.Logger
    rate     int64
    counter  int64
}

func (sl *SampledLogger) Debug(msg string, fields logger.Fields) {
    // Log every Nth message
    count := atomic.AddInt64(&sl.counter, 1)
    if count%sl.rate == 0 {
        fields["sample_rate"] = sl.rate
        fields["dropped_logs"] = sl.rate - 1
        sl.logger.WithFields(fields).Debug(msg)
    }
}
```

### Async Logging

```go
// Async logger for critical paths
type AsyncLogger struct {
    logger  logger.Logger
    entries chan *logEntry
    done    chan struct{}
}

type logEntry struct {
    level   logger.Level
    message string
    fields  logger.Fields
}

func NewAsyncLogger(logger logger.Logger, bufferSize int) *AsyncLogger {
    al := &AsyncLogger{
        logger:  logger,
        entries: make(chan *logEntry, bufferSize),
        done:    make(chan struct{}),
    }
    go al.worker()
    return al
}

func (al *AsyncLogger) Info(msg string, fields logger.Fields) {
    select {
    case al.entries <- &logEntry{
        level:   logger.InfoLevel,
        message: msg,
        fields:  fields,
    }:
    default:
        // Buffer full, drop log
        al.logger.Warn("Async log buffer full, dropping logs")
    }
}
```

## Best Practices

### 1. Structured Fields

```go
// DO: Use structured fields
log.WithFields(logger.Fields{
    "user_id": userID,
    "action":  "login",
    "success": true,
}).Info("User logged in")

// DON'T: Concatenate strings
log.Info(fmt.Sprintf("User %s logged in successfully", userID))

// DO: Consistent field names
log.WithField("stream_id", id).Info("Stream started")
log.WithField("stream_id", id).Info("Stream stopped")

// DON'T: Inconsistent naming
log.WithField("streamId", id).Info("Stream started")
log.WithField("stream_ID", id).Info("Stream stopped")
```

### 2. Error Logging

```go
// DO: Use WithError
log.WithError(err).Error("Database query failed")

// DON'T: Include error in message
log.Errorf("Database query failed: %v", err)

// DO: Add context to errors
log.WithFields(logger.Fields{
    "query":    query,
    "duration": duration,
}).WithError(err).Error("Database query failed")

// DO: Check error before logging
if err != nil {
    log.WithError(err).WithField("path", path).Error("File operation failed")
    return err
}
```

### 3. Performance Considerations

```go
// DO: Check log level before expensive operations
if log.IsDebugEnabled() {
    log.WithField("payload", hex.Dump(data)).Debug("Packet contents")
}

// DON'T: Always compute expensive fields
log.WithField("payload", hex.Dump(data)).Debug("Packet contents")

// DO: Reuse loggers with common fields
streamLog := log.WithField("stream_id", streamID)
streamLog.Info("Stream started")
streamLog.Debug("Processing frame")
streamLog.Info("Stream ended")
```

### 4. Security

```go
// DO: Sanitize sensitive data
log.WithFields(logger.Fields{
    "user_id": userID,
    "email":   maskEmail(email),
}).Info("Password reset requested")

// DON'T: Log sensitive information
log.WithFields(logger.Fields{
    "password": password,
    "token":    authToken,
}).Info("User authenticated")

// DO: Use structured fields for PII
type User struct {
    ID       string
    Email    string
    Password string
}

func (u User) LogValue() logger.Fields {
    return logger.Fields{
        "user_id": u.ID,
        "email":   maskEmail(u.Email),
        // Never log passwords
    }
}
```

## Testing

### Mock Logger

```go
// Mock logger for tests
type MockLogger struct {
    entries []MockEntry
    mu      sync.Mutex
}

type MockEntry struct {
    Level   string
    Message string
    Fields  map[string]interface{}
}

func (m *MockLogger) Info(msg string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.entries = append(m.entries, MockEntry{
        Level:   "info",
        Message: msg,
    })
}

func TestServiceLogging(t *testing.T) {
    mockLog := &MockLogger{}
    service := NewService(mockLog)
    
    service.Process()
    
    assert.Len(t, mockLog.entries, 2)
    assert.Equal(t, "Processing started", mockLog.entries[0].Message)
    assert.Equal(t, "Processing completed", mockLog.entries[1].Message)
}
```

### Test Helpers

```go
// Capture logs in tests
func CaptureLogOutput(f func()) string {
    var buf bytes.Buffer
    log := logrus.New()
    log.SetOutput(&buf)
    log.SetLevel(logrus.DebugLevel)
    
    // Replace global logger
    oldLogger := logger.Default()
    logger.SetDefault(log)
    defer logger.SetDefault(oldLogger)
    
    f()
    
    return buf.String()
}

func TestLogOutput(t *testing.T) {
    output := CaptureLogOutput(func() {
        logger.Info("Test message")
    })
    
    assert.Contains(t, output, "Test message")
    assert.Contains(t, output, "level=info")
}
```

## Examples

### Complete Logger Setup

```go
// logger_setup.go
func SetupLogger(config *config.LoggingConfig) (logger.Logger, error) {
    // Create base logger
    log := logrus.New()
    
    // Set log level
    level, err := logrus.ParseLevel(config.Level)
    if err != nil {
        return nil, fmt.Errorf("invalid log level: %w", err)
    }
    log.SetLevel(level)
    
    // Set formatter
    switch config.Format {
    case "json":
        log.SetFormatter(&logrus.JSONFormatter{
            TimestampFormat:   time.RFC3339Nano,
            DisableHTMLEscape: true,
            DataKey:           "fields",
            FieldMap: logrus.FieldMap{
                logrus.FieldKeyTime:  "timestamp",
                logrus.FieldKeyLevel: "level",
                logrus.FieldKeyMsg:   "message",
            },
        })
    case "text":
        log.SetFormatter(&logrus.TextFormatter{
            FullTimestamp:   true,
            TimestampFormat: "2006-01-02 15:04:05.000",
            DisableColors:   config.Output != "stdout",
        })
    default:
        log.SetFormatter(&DevFormatter{})
    }
    
    // Set output
    switch config.Output {
    case "stdout":
        log.SetOutput(os.Stdout)
    case "stderr":
        log.SetOutput(os.Stderr)
    default:
        // File output with rotation
        log.SetOutput(&lumberjack.Logger{
            Filename:   config.Output,
            MaxSize:    config.MaxSize,
            MaxBackups: config.MaxBackups,
            MaxAge:     config.MaxAge,
            Compress:   config.Compress,
            LocalTime:  true,
        })
    }
    
    // Add default fields
    log = log.WithFields(logrus.Fields{
        "service": "mirror",
        "version": version.Version,
        "env":     config.Environment,
    })
    
    // Add hooks
    if config.ErrorTracking {
        log.AddHook(&ErrorTrackingHook{
            Threshold: logrus.ErrorLevel,
            Reporter:  errorReporter,
        })
    }
    
    return log, nil
}
```

### Stream Processing Logger

```go
// Specialized logger for stream processing
type StreamProcessingLogger struct {
    base      logger.Logger
    streamID  string
    startTime time.Time
    
    // Metrics
    frames    int64
    errors    int64
    warnings  int64
}

func NewStreamProcessingLogger(streamID string) *StreamProcessingLogger {
    return &StreamProcessingLogger{
        base:      logger.Default().WithField("stream_id", streamID),
        streamID:  streamID,
        startTime: time.Now(),
    }
}

func (l *StreamProcessingLogger) LogFrame(frame Frame) {
    atomic.AddInt64(&l.frames, 1)
    
    if l.frames%1000 == 0 { // Log every 1000 frames
        l.base.WithFields(logger.Fields{
            "frames_processed": l.frames,
            "fps":             l.calculateFPS(),
            "frame_type":      frame.Type,
            "frame_size":      frame.Size,
        }).Info("Processing milestone")
    }
}

func (l *StreamProcessingLogger) LogError(err error, context string) {
    atomic.AddInt64(&l.errors, 1)
    
    l.base.WithError(err).WithFields(logger.Fields{
        "context":      context,
        "total_errors": l.errors,
        "uptime":       time.Since(l.startTime),
    }).Error("Processing error")
}

func (l *StreamProcessingLogger) Summary() {
    l.base.WithFields(logger.Fields{
        "total_frames":   l.frames,
        "total_errors":   l.errors,
        "total_warnings": l.warnings,
        "duration":       time.Since(l.startTime),
        "avg_fps":        l.calculateFPS(),
    }).Info("Stream processing completed")
}
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Context Package](../context/README.md) - Context propagation
- [Metrics Package](../metrics/README.md) - Metrics integration
- [Error Package](../errors/README.md) - Error handling
