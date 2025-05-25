package errors

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewErrorHandler(t *testing.T) {
	logger := logrus.New()
	handler := NewErrorHandler(logger)

	assert.NotNil(t, handler)
	assert.Equal(t, logger, handler.logger)
}

func TestHandleError(t *testing.T) {
	logger := logrus.New()
	handler := NewErrorHandler(logger)

	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedType   ErrorType
	}{
		{
			name:           "AppError",
			err:            NewValidationError("invalid input"),
			expectedStatus: http.StatusBadRequest,
			expectedType:   ErrorTypeValidation,
		},
		{
			name:           "Standard error",
			err:            errors.New("something went wrong"),
			expectedStatus: http.StatusInternalServerError,
			expectedType:   ErrorTypeInternal,
		},
		{
			name:           "Not found error",
			err:            NewNotFoundError("user"),
			expectedStatus: http.StatusNotFound,
			expectedType:   ErrorTypeNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("X-Request-ID", "test-123")
			rr := httptest.NewRecorder()

			handler.HandleError(rr, req, tt.err)

			assert.Equal(t, tt.expectedStatus, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

			var response ErrorResponse
			err := json.Unmarshal(rr.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedType, response.Error.Type)
			assert.NotEmpty(t, response.Error.Message)
			assert.Equal(t, "test-123", response.TraceID)
		})
	}
}

func TestHandleNotFound(t *testing.T) {
	logger := logrus.New()
	handler := NewErrorHandler(logger)

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	rr := httptest.NewRecorder()

	handler.HandleNotFound(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)

	var response ErrorResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ErrorTypeNotFound, response.Error.Type)
	assert.Contains(t, response.Error.Message, "endpoint")
}

func TestHandleMethodNotAllowed(t *testing.T) {
	logger := logrus.New()
	handler := NewErrorHandler(logger)

	req := httptest.NewRequest("POST", "/get-only-endpoint", nil)
	rr := httptest.NewRecorder()

	handler.HandleMethodNotAllowed(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)

	var response ErrorResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ErrorTypeValidation, response.Error.Type)
	assert.Contains(t, response.Error.Message, "Method not allowed")
}

func TestHandlePanic(t *testing.T) {
	logger := logrus.New()
	handler := NewErrorHandler(logger)

	req := httptest.NewRequest("GET", "/panic", nil)
	rr := httptest.NewRecorder()

	handler.HandlePanic(rr, req, "test panic")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	var response ErrorResponse
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, ErrorTypeInternal, response.Error.Type)
	assert.Contains(t, response.Error.Message, "unexpected error")
}

func TestMiddleware(t *testing.T) {
	logger := logrus.New()
	handler := NewErrorHandler(logger)

	// Test handler that panics
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("middleware test panic")
	})

	// Wrap with error handling middleware
	protected := handler.Middleware(panicHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Should recover from panic
	assert.NotPanics(t, func() {
		protected.ServeHTTP(rr, req)
	})

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}
