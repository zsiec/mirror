package errors

import (
	"fmt"
	"net/http"
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
	_, ok := err.(*AppError)
	return ok
}

// GetAppError extracts AppError from an error.
func GetAppError(err error) (*AppError, bool) {
	appErr, ok := err.(*AppError)
	return appErr, ok
}