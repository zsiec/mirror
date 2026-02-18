package errors

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrorType represents the type of error.
type ErrorType string

const (
	ErrorTypeValidation   ErrorType = "VALIDATION_ERROR"
	ErrorTypeNotFound     ErrorType = "NOT_FOUND"
	ErrorTypeUnauthorized ErrorType = "UNAUTHORIZED"
	ErrorTypeForbidden    ErrorType = "FORBIDDEN"
	ErrorTypeInternal     ErrorType = "INTERNAL_ERROR"
	ErrorTypeTimeout      ErrorType = "TIMEOUT"
	ErrorTypeConflict     ErrorType = "CONFLICT"
	ErrorTypeRateLimit    ErrorType = "RATE_LIMIT"
	ErrorTypeServiceDown  ErrorType = "SERVICE_DOWN"
)

// AppError represents an application error with additional context.
type AppError struct {
	Type       ErrorType              `json:"type"`
	Message    string                 `json:"message"`
	Code       string                 `json:"code,omitempty"`
	Details    map[string]interface{} `json:"details,omitempty"`
	HTTPStatus int                    `json:"-"`
	Err        error                  `json:"-"`
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (caused by: %v)", e.Type, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Unwrap returns the wrapped error.
func (e *AppError) Unwrap() error {
	return e.Err
}

// WithDetails adds details to the error.
func (e *AppError) WithDetails(details map[string]interface{}) *AppError {
	e.Details = details
	return e
}

// WithCode adds an error code.
func (e *AppError) WithCode(code string) *AppError {
	e.Code = code
	return e
}

// New creates a new AppError.
func New(errType ErrorType, message string, httpStatus int) *AppError {
	return &AppError{
		Type:       errType,
		Message:    message,
		HTTPStatus: httpStatus,
	}
}

// Wrap wraps an existing error.
func Wrap(err error, errType ErrorType, message string, httpStatus int) *AppError {
	return &AppError{
		Type:       errType,
		Message:    message,
		HTTPStatus: httpStatus,
		Err:        err,
	}
}

// Common error constructors.

// NewValidationError creates a validation error.
func NewValidationError(message string) *AppError {
	return New(ErrorTypeValidation, message, http.StatusBadRequest)
}

// NewNotFoundError creates a not found error.
func NewNotFoundError(resource string) *AppError {
	return New(ErrorTypeNotFound, fmt.Sprintf("%s not found", resource), http.StatusNotFound)
}

// NewUnauthorizedError creates an unauthorized error.
func NewUnauthorizedError(message string) *AppError {
	return New(ErrorTypeUnauthorized, message, http.StatusUnauthorized)
}

// NewForbiddenError creates a forbidden error.
func NewForbiddenError(message string) *AppError {
	return New(ErrorTypeForbidden, message, http.StatusForbidden)
}

// NewInternalError creates an internal server error.
func NewInternalError(message string) *AppError {
	return New(ErrorTypeInternal, message, http.StatusInternalServerError)
}

// WrapInternalError wraps an error as internal server error.
func WrapInternalError(err error, message string) *AppError {
	return Wrap(err, ErrorTypeInternal, message, http.StatusInternalServerError)
}

// NewTimeoutError creates a timeout error.
func NewTimeoutError(message string) *AppError {
	return New(ErrorTypeTimeout, message, http.StatusRequestTimeout)
}

// NewConflictError creates a conflict error.
func NewConflictError(message string) *AppError {
	return New(ErrorTypeConflict, message, http.StatusConflict)
}

// NewRateLimitError creates a rate limit error.
func NewRateLimitError(message string) *AppError {
	return New(ErrorTypeRateLimit, message, http.StatusTooManyRequests)
}

// NewServiceDownError creates a service down error.
func NewServiceDownError(service string) *AppError {
	return New(ErrorTypeServiceDown, fmt.Sprintf("%s service is currently unavailable", service), http.StatusServiceUnavailable)
}

// IsAppError checks if an error is an AppError.
func IsAppError(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr)
}

// GetAppError extracts AppError from an error.
func GetAppError(err error) (*AppError, bool) {
	var appErr *AppError
	ok := errors.As(err, &appErr)
	return appErr, ok
}

// StreamError represents a stream-specific error with additional context
type StreamError struct {
	StreamID  string                 `json:"stream_id"`
	Component string                 `json:"component"`
	Operation string                 `json:"operation"`
	Err       error                  `json:"-"`
	Timestamp time.Time              `json:"timestamp"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// Error implements the error interface
func (e *StreamError) Error() string {
	return fmt.Sprintf("[%s] %s: %s failed: %v", e.StreamID, e.Component, e.Operation, e.Err)
}

// Unwrap returns the wrapped error
func (e *StreamError) Unwrap() error {
	return e.Err
}

// NewStreamError creates a new stream error
func NewStreamError(streamID, component, operation string, err error) *StreamError {
	return &StreamError{
		StreamID:  streamID,
		Component: component,
		Operation: operation,
		Err:       err,
		Timestamp: time.Now(),
		Details:   make(map[string]interface{}),
	}
}

// WithDetails adds details to the stream error
func (e *StreamError) WithDetails(key string, value interface{}) *StreamError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// WithDetailsMap adds multiple details to the stream error
func (e *StreamError) WithDetailsMap(details map[string]interface{}) *StreamError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	for k, v := range details {
		e.Details[k] = v
	}
	return e
}

// IsStreamError checks if an error is a StreamError
func IsStreamError(err error) bool {
	var streamErr *StreamError
	return errors.As(err, &streamErr)
}

// GetStreamError extracts StreamError from an error
func GetStreamError(err error) (*StreamError, bool) {
	var streamErr *StreamError
	ok := errors.As(err, &streamErr)
	return streamErr, ok
}

// WrapWithStreamError wraps an error with stream context
func WrapWithStreamError(streamID, component, operation string, err error) error {
	if err == nil {
		return nil
	}
	return NewStreamError(streamID, component, operation, err)
}
