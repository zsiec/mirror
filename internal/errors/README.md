# Errors Package

The `errors` package provides a comprehensive error handling framework for the Mirror platform. It features typed errors, automatic HTTP status code mapping, context preservation, and consistent error responses across the application.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Error Types](#error-types)
- [Usage](#usage)
- [HTTP Error Handling](#http-error-handling)
- [Error Context](#error-context)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The error handling system provides:

- **Typed errors** for different error categories
- **Automatic HTTP status mapping** for API responses
- **Context preservation** with error wrapping
- **Structured error responses** with consistent format
- **Request ID tracking** for debugging
- **Stack trace capture** in development mode
- **Error aggregation** for validation errors

## Features

### Core Capabilities

- **Type-safe error handling** with predefined error types
- **HTTP status code mapping** for each error type
- **Error wrapping** with additional context
- **Structured JSON responses** for APIs
- **Development mode** with detailed stack traces
- **Production mode** with sanitized error messages
- **Error middleware** for automatic handling

### Error Response Format

```json
{
  "error": {
    "type": "validation_error",
    "message": "Invalid request parameters",
    "code": "VALIDATION_FAILED",
    "request_id": "req_123abc",
    "timestamp": "2024-01-20T15:30:45Z",
    "details": {
      "fields": [
        {
          "field": "stream_id",
          "message": "must be alphanumeric"
        }
      ]
    }
  }
}
```

## Error Types

### Base Error Type

```go
type AppError struct {
    Type       ErrorType              `json:"type"`
    Message    string                 `json:"message"`
    Code       string                 `json:"code,omitempty"`
    StatusCode int                    `json:"-"`
    Details    map[string]interface{} `json:"details,omitempty"`
    Internal   error                  `json:"-"`
    Stack      []byte                 `json:"-"`
    RequestID  string                 `json:"request_id,omitempty"`
    Timestamp  time.Time              `json:"timestamp"`
}
```

### Predefined Error Types

```go
const (
    // Client errors (4xx)
    ErrorTypeValidation    ErrorType = "validation_error"     // 400
    ErrorTypeUnauthorized  ErrorType = "unauthorized"         // 401
    ErrorTypeForbidden     ErrorType = "forbidden"            // 403
    ErrorTypeNotFound      ErrorType = "not_found"            // 404
    ErrorTypeConflict      ErrorType = "conflict"             // 409
    ErrorTypeRateLimit     ErrorType = "rate_limit_exceeded"  // 429

    // Server errors (5xx)
    ErrorTypeInternal      ErrorType = "internal_error"       // 500
    ErrorTypeUnavailable   ErrorType = "service_unavailable"  // 503
    ErrorTypeTimeout       ErrorType = "timeout"              // 504
    
    // Business logic errors
    ErrorTypeStreamOffline ErrorType = "stream_offline"       // 503
    ErrorTypeCapacityFull  ErrorType = "capacity_full"        // 503
    ErrorTypeInvalidCodec  ErrorType = "invalid_codec"        // 400
)
```

## Usage

### Creating Errors

```go
import "github.com/zsiec/mirror/internal/errors"

// Simple error
err := errors.New(errors.ErrorTypeNotFound, "Stream not found")

// Error with details
err := errors.NewWithDetails(
    errors.ErrorTypeValidation,
    "Invalid stream configuration",
    map[string]interface{}{
        "field": "bitrate",
        "value": 100000000,
        "max":   50000000,
    },
)

// Error with custom code
err := errors.NewWithCode(
    errors.ErrorTypeRateLimit,
    "RATE_LIMIT_STREAMS",
    "Too many stream creation requests",
)
```

### Wrapping Errors

```go
// Wrap external errors
dbErr := database.Query()
if dbErr != nil {
    return errors.Wrap(dbErr, errors.ErrorTypeInternal, 
        "Failed to query stream data")
}

// Wrap with additional context
err := errors.WrapWithContext(dbErr, errors.ErrorTypeInternal,
    "Database operation failed",
    map[string]interface{}{
        "operation": "get_stream",
        "stream_id": streamID,
    },
)
```

### Error Checking

```go
// Check error type
if errors.IsType(err, errors.ErrorTypeNotFound) {
    // Handle not found case
}

// Check multiple types
if errors.IsAnyType(err, errors.ErrorTypeNotFound, errors.ErrorTypeUnauthorized) {
    // Handle client errors
}

// Extract app error
if appErr, ok := errors.AsAppError(err); ok {
    log.Printf("Error code: %s", appErr.Code)
    log.Printf("Details: %v", appErr.Details)
}
```

## HTTP Error Handling

### Error Handler Middleware

```go
// handler.go
type ErrorHandler struct {
    logger     Logger
    isDev      bool
    requestID  func(*http.Request) string
}

func (h *ErrorHandler) Handle(w http.ResponseWriter, r *http.Request, err error) {
    appErr := h.toAppError(err)
    
    // Add request ID
    if h.requestID != nil {
        appErr.RequestID = h.requestID(r)
    }
    
    // Log error
    h.logError(r, appErr)
    
    // Send response
    h.sendErrorResponse(w, appErr)
}
```

### Using in HTTP Handlers

```go
func (s *Server) handleGetStream(w http.ResponseWriter, r *http.Request) {
    streamID := mux.Vars(r)["id"]
    
    stream, err := s.manager.GetStream(streamID)
    if err != nil {
        // Error handler will automatically set correct status code
        s.errorHandler.Handle(w, r, err)
        return
    }
    
    // Success response
    s.respondJSON(w, stream)
}
```

### Validation Errors

```go
// Aggregate multiple validation errors
func validateStreamConfig(cfg StreamConfig) error {
    var validationErrors []errors.ValidationError
    
    if cfg.Bitrate > MaxBitrate {
        validationErrors = append(validationErrors, errors.ValidationError{
            Field:   "bitrate",
            Message: fmt.Sprintf("exceeds maximum of %d", MaxBitrate),
        })
    }
    
    if cfg.BufferSize < MinBufferSize {
        validationErrors = append(validationErrors, errors.ValidationError{
            Field:   "buffer_size",
            Message: fmt.Sprintf("below minimum of %d", MinBufferSize),
        })
    }
    
    if len(validationErrors) > 0 {
        return errors.NewValidationError("Invalid configuration", validationErrors)
    }
    
    return nil
}
```

## Error Context

### Adding Context

```go
// Add context to errors for better debugging
func (m *Manager) StartStream(ctx context.Context, config StreamConfig) error {
    // Validate config
    if err := validateStreamConfig(config); err != nil {
        return errors.WrapWithContext(err, errors.ErrorTypeValidation,
            "Stream configuration validation failed",
            map[string]interface{}{
                "stream_id": config.ID,
                "source":    config.Source,
            },
        )
    }
    
    // Start stream
    if err := m.ingestion.Start(ctx, config); err != nil {
        return errors.WrapWithContext(err, errors.ErrorTypeInternal,
            "Failed to start stream ingestion",
            map[string]interface{}{
                "stream_id": config.ID,
                "protocol":  config.Protocol,
                "attempt":   1,
            },
        )
    }
    
    return nil
}
```

### Error Chain

```go
// Build error chain for complex operations
func (p *Pipeline) Process(data []byte) error {
    // Decode
    frame, err := p.decoder.Decode(data)
    if err != nil {
        return errors.Wrap(err, errors.ErrorTypeInvalidCodec,
            "Failed to decode frame")
    }
    
    // Process
    processed, err := p.processor.Process(frame)
    if err != nil {
        return errors.Wrap(err, errors.ErrorTypeInternal,
            "Failed to process frame")
    }
    
    // Encode
    encoded, err := p.encoder.Encode(processed)
    if err != nil {
        return errors.Wrap(err, errors.ErrorTypeInternal,
            "Failed to encode frame")
    }
    
    return nil
}
```

## Best Practices

### 1. Error Creation

```go
// DO: Use specific error types
err := errors.New(errors.ErrorTypeNotFound, "Stream not found")

// DON'T: Use generic errors
err := fmt.Errorf("stream not found")

// DO: Include relevant context
err := errors.NewWithDetails(errors.ErrorTypeValidation,
    "Invalid bitrate",
    map[string]interface{}{
        "provided": bitrate,
        "min":      MinBitrate,
        "max":      MaxBitrate,
    },
)

// DON'T: Lose context
err := errors.New(errors.ErrorTypeValidation, "Invalid bitrate")
```

### 2. Error Handling

```go
// DO: Handle specific error types
switch {
case errors.IsType(err, errors.ErrorTypeNotFound):
    // Return 404
case errors.IsType(err, errors.ErrorTypeValidation):
    // Return 400 with details
case errors.IsType(err, errors.ErrorTypeTimeout):
    // Retry operation
default:
    // Generic error handling
}

// DON'T: Ignore error types
if err != nil {
    return fmt.Errorf("operation failed: %w", err)
}
```

### 3. Error Messages

```go
// DO: User-friendly messages for client errors
err := errors.New(errors.ErrorTypeValidation,
    "The provided stream ID contains invalid characters. Use only letters and numbers.")

// DON'T: Expose internal details
err := errors.New(errors.ErrorTypeInternal,
    "pq: duplicate key value violates unique constraint \"streams_pkey\"")

// DO: Sanitize internal errors
if err != nil {
    if errors.IsInternalError(err) {
        return errors.New(errors.ErrorTypeInternal,
            "An internal error occurred. Please try again later.")
    }
    return err
}
```

### 4. Logging

```go
// DO: Log full error details internally
logger.WithError(err).WithFields(logrus.Fields{
    "stream_id": streamID,
    "operation": "transcode",
    "codec":     codec,
}).Error("Transcoding failed")

// DON'T: Log sensitive information
logger.Errorf("Failed to authenticate: %v", userCredentials)
```

## Testing

### Unit Tests

```go
func TestErrorCreation(t *testing.T) {
    tests := []struct {
        name       string
        errorType  errors.ErrorType
        message    string
        wantStatus int
    }{
        {
            name:       "not found error",
            errorType:  errors.ErrorTypeNotFound,
            message:    "Resource not found",
            wantStatus: http.StatusNotFound,
        },
        {
            name:       "validation error",
            errorType:  errors.ErrorTypeValidation,
            message:    "Invalid input",
            wantStatus: http.StatusBadRequest,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := errors.New(tt.errorType, tt.message)
            
            assert.Equal(t, tt.errorType, err.Type)
            assert.Equal(t, tt.message, err.Message)
            assert.Equal(t, tt.wantStatus, err.StatusCode)
        })
    }
}
```

### Testing Error Responses

```go
func TestErrorHandler(t *testing.T) {
    handler := errors.NewErrorHandler(logger, false)
    
    // Test 404 error
    req := httptest.NewRequest("GET", "/streams/123", nil)
    w := httptest.NewRecorder()
    
    err := errors.New(errors.ErrorTypeNotFound, "Stream not found")
    handler.Handle(w, req, err)
    
    assert.Equal(t, http.StatusNotFound, w.Code)
    
    var response map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &response)
    
    assert.Equal(t, "not_found", response["error"].(map[string]interface{})["type"])
}
```

## Examples

### Complete Error Handling Flow

```go
// Service layer
func (s *StreamService) CreateStream(ctx context.Context, req CreateStreamRequest) (*Stream, error) {
    // Validate request
    if err := req.Validate(); err != nil {
        return nil, errors.Wrap(err, errors.ErrorTypeValidation,
            "Invalid stream creation request")
    }
    
    // Check capacity
    if !s.hasCapacity() {
        return nil, errors.NewWithCode(
            errors.ErrorTypeCapacityFull,
            "STREAMS_AT_CAPACITY",
            "Maximum number of streams reached",
        )
    }
    
    // Create stream
    stream, err := s.repository.Create(ctx, req)
    if err != nil {
        if errors.IsUniqueViolation(err) {
            return nil, errors.New(errors.ErrorTypeConflict,
                "A stream with this ID already exists")
        }
        return nil, errors.Wrap(err, errors.ErrorTypeInternal,
            "Failed to create stream")
    }
    
    return stream, nil
}

// HTTP handler
func (h *Handler) CreateStream(w http.ResponseWriter, r *http.Request) {
    var req CreateStreamRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        h.errorHandler.Handle(w, r, errors.Wrap(err, 
            errors.ErrorTypeValidation, "Invalid JSON"))
        return
    }
    
    stream, err := h.service.CreateStream(r.Context(), req)
    if err != nil {
        h.errorHandler.Handle(w, r, err)
        return
    }
    
    h.respondJSON(w, http.StatusCreated, stream)
}
```

### Custom Error Types

```go
// Define domain-specific errors
const (
    ErrorTypeStreamOffline = "stream_offline"
    ErrorTypeCodecMismatch = "codec_mismatch"
)

// Register custom error mappings
func init() {
    errors.RegisterErrorType(ErrorTypeStreamOffline, http.StatusServiceUnavailable)
    errors.RegisterErrorType(ErrorTypeCodecMismatch, http.StatusBadRequest)
}

// Use custom errors
err := errors.NewWithDetails(ErrorTypeCodecMismatch,
    "Input codec does not match stream configuration",
    map[string]interface{}{
        "expected": "h264",
        "received": "hevc",
    },
)
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Logging Package](../logger/README.md) - Structured logging
- [Server Package](../server/README.md) - HTTP error handling
- [API Documentation](../../docs/api/README.md) - Error response formats
