package errors

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppError(t *testing.T) {
	t.Run("New creates error correctly", func(t *testing.T) {
		err := New(ErrorTypeValidation, "Invalid input", http.StatusBadRequest)
		
		assert.Equal(t, ErrorTypeValidation, err.Type)
		assert.Equal(t, "Invalid input", err.Message)
		assert.Equal(t, http.StatusBadRequest, err.HTTPStatus)
		assert.Equal(t, "VALIDATION_ERROR: Invalid input", err.Error())
	})

	t.Run("Wrap wraps error correctly", func(t *testing.T) {
		originalErr := errors.New("original error")
		err := Wrap(originalErr, ErrorTypeInternal, "Something went wrong", http.StatusInternalServerError)
		
		assert.Equal(t, ErrorTypeInternal, err.Type)
		assert.Equal(t, "Something went wrong", err.Message)
		assert.Equal(t, http.StatusInternalServerError, err.HTTPStatus)
		assert.Equal(t, originalErr, err.Unwrap())
		assert.Contains(t, err.Error(), "original error")
	})

	t.Run("WithDetails adds details", func(t *testing.T) {
		err := New(ErrorTypeValidation, "Invalid input", http.StatusBadRequest)
		details := map[string]interface{}{
			"field": "email",
			"value": "invalid-email",
		}
		_ = err.WithDetails(details)
		
		assert.Equal(t, details, err.Details)
	})

	t.Run("WithCode adds code", func(t *testing.T) {
		err := New(ErrorTypeValidation, "Invalid input", http.StatusBadRequest)
		_ = err.WithCode("ERR_001")
		
		assert.Equal(t, "ERR_001", err.Code)
	})
}

func TestErrorConstructors(t *testing.T) {
	tests := []struct {
		name       string
		fn         func() *AppError
		wantType   ErrorType
		wantStatus int
	}{
		{
			name: "NewValidationError",
			fn: func() *AppError {
				return NewValidationError("Invalid field")
			},
			wantType:   ErrorTypeValidation,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "NewNotFoundError",
			fn: func() *AppError {
				return NewNotFoundError("User")
			},
			wantType:   ErrorTypeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "NewUnauthorizedError",
			fn: func() *AppError {
				return NewUnauthorizedError("Invalid token")
			},
			wantType:   ErrorTypeUnauthorized,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "NewForbiddenError",
			fn: func() *AppError {
				return NewForbiddenError("Access denied")
			},
			wantType:   ErrorTypeForbidden,
			wantStatus: http.StatusForbidden,
		},
		{
			name: "NewInternalError",
			fn: func() *AppError {
				return NewInternalError("Server error")
			},
			wantType:   ErrorTypeInternal,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "NewTimeoutError",
			fn: func() *AppError {
				return NewTimeoutError("Request timeout")
			},
			wantType:   ErrorTypeTimeout,
			wantStatus: http.StatusRequestTimeout,
		},
		{
			name: "NewConflictError",
			fn: func() *AppError {
				return NewConflictError("Resource conflict")
			},
			wantType:   ErrorTypeConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name: "NewRateLimitError",
			fn: func() *AppError {
				return NewRateLimitError("Too many requests")
			},
			wantType:   ErrorTypeRateLimit,
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name: "NewServiceDownError",
			fn: func() *AppError {
				return NewServiceDownError("Database")
			},
			wantType:   ErrorTypeServiceDown,
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			assert.Equal(t, tt.wantType, err.Type)
			assert.Equal(t, tt.wantStatus, err.HTTPStatus)
			assert.NotEmpty(t, err.Message)
		})
	}
}

func TestIsAppError(t *testing.T) {
	t.Run("returns true for AppError", func(t *testing.T) {
		err := NewValidationError("test")
		assert.True(t, IsAppError(err))
	})

	t.Run("returns false for standard error", func(t *testing.T) {
		err := errors.New("standard error")
		assert.False(t, IsAppError(err))
	})
}

func TestGetAppError(t *testing.T) {
	t.Run("extracts AppError successfully", func(t *testing.T) {
		originalErr := NewValidationError("test")
		appErr, ok := GetAppError(originalErr)
		
		assert.True(t, ok)
		assert.Equal(t, originalErr, appErr)
	})

	t.Run("returns false for non-AppError", func(t *testing.T) {
		err := errors.New("standard error")
		appErr, ok := GetAppError(err)
		
		assert.False(t, ok)
		assert.Nil(t, appErr)
	})
}

func TestWrapInternalError(t *testing.T) {
	originalErr := errors.New("database connection failed")
	wrappedErr := WrapInternalError(originalErr, "Failed to fetch data")
	
	assert.Equal(t, ErrorTypeInternal, wrappedErr.Type)
	assert.Equal(t, "Failed to fetch data", wrappedErr.Message)
	assert.Equal(t, http.StatusInternalServerError, wrappedErr.HTTPStatus)
	assert.Equal(t, originalErr, wrappedErr.Unwrap())
}