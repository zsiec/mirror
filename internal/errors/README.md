# Errors Package

The `errors` package provides typed error handling with HTTP status code mapping for the Mirror platform.

## Error Types

### AppError

The primary error type with automatic HTTP status mapping:

```go
type AppError struct {
    Type       ErrorType              `json:"type"`
    Message    string                 `json:"message"`
    Code       string                 `json:"code,omitempty"`
    Details    map[string]interface{} `json:"details,omitempty"`
    HTTPStatus int                    `json:"-"`
    Err        error                  `json:"-"`
}
```

### Predefined Error Types

```go
const (
    ErrorTypeValidation   ErrorType = "VALIDATION_ERROR"   // 400
    ErrorTypeNotFound     ErrorType = "NOT_FOUND"           // 404
    ErrorTypeUnauthorized ErrorType = "UNAUTHORIZED"        // 401
    ErrorTypeForbidden    ErrorType = "FORBIDDEN"           // 403
    ErrorTypeInternal     ErrorType = "INTERNAL_ERROR"      // 500
    ErrorTypeTimeout      ErrorType = "TIMEOUT"             // 408
    ErrorTypeConflict     ErrorType = "CONFLICT"            // 409
    ErrorTypeRateLimit    ErrorType = "RATE_LIMIT"          // 429
    ErrorTypeServiceDown  ErrorType = "SERVICE_DOWN"        // 503
)
```

### StreamError

A stream-specific error type with additional context:

```go
type StreamError struct {
    StreamID  string
    Component string
    Operation string
    Err       error
    Timestamp time.Time
    Details   map[string]interface{}
}
```

## Creating Errors

### Constructor Functions

```go
// Generic constructors
errors.New(errType, message, httpStatus) *AppError
errors.Wrap(err, errType, message, httpStatus) *AppError

// Convenience constructors
errors.NewValidationError(message) *AppError          // 400
errors.NewNotFoundError(resource) *AppError            // 404 - formats "{resource} not found"
errors.NewUnauthorizedError(message) *AppError         // 401
errors.NewForbiddenError(message) *AppError            // 403
errors.NewInternalError(message) *AppError             // 500
errors.WrapInternalError(err, message) *AppError       // 500, wraps existing error
errors.NewTimeoutError(message) *AppError              // 408
errors.NewConflictError(message) *AppError             // 409
errors.NewRateLimitError(message) *AppError            // 429
errors.NewServiceDownError(service) *AppError          // 503 - formats "{service} service is currently unavailable"
```

### Fluent Methods

```go
err := errors.NewValidationError("Invalid bitrate").
    WithCode("INVALID_BITRATE").
    WithDetails(map[string]interface{}{
        "provided": bitrate,
        "max":      50000000,
    })
```

### Stream Errors

```go
errors.NewStreamError(streamID, component, operation, err) *StreamError
errors.WrapWithStreamError(streamID, component, operation, err) error  // nil-safe

streamErr.WithDetails(key, value) *StreamError
streamErr.WithDetailsMap(details) *StreamError
```

## Checking Errors

```go
errors.IsAppError(err) bool
errors.GetAppError(err) (*AppError, bool)

errors.IsStreamError(err) bool
errors.GetStreamError(err) (*StreamError, bool)
```

Both types implement `Unwrap()` for use with `errors.As()` and `errors.Is()`.

## HTTP Error Handler

The `ErrorHandler` (in `handler.go`) handles HTTP error responses:

```go
handler := errors.NewErrorHandler(logger)

// In HTTP handlers:
handler.HandleError(w, r, err)       // Converts any error to JSON response
handler.HandleNotFound(w, r)          // 404 response
handler.HandleMethodNotAllowed(w, r)  // 405 response
handler.HandlePanic(w, r, recovered)  // 500 response for panics
handler.Middleware(next)              // Panic recovery middleware
```

### Error Response Format

```json
{
    "error": {
        "type": "NOT_FOUND",
        "message": "Stream not found",
        "code": "STREAM_NOT_FOUND",
        "details": {}
    },
    "trace_id": "req_abc123"
}
```

The handler:
- Extracts trace ID from `X-Request-ID` header
- Logs at appropriate level (Error for 5xx, Warn for 4xx client errors, Info for others)
- Converts non-AppError errors to internal server errors automatically

## Files

- `errors.go`: AppError, StreamError types and all constructor functions
- `handler.go`: ErrorHandler, ErrorResponse, HTTP error handling and middleware
- `errors_test.go`, `handler_test.go`: Tests
