package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/errors"
	"github.com/zsiec/mirror/internal/logger"
)

// TestServer_timeoutMiddleware tests the timeout middleware functionality
func TestServer_timeoutMiddleware(t *testing.T) {
	// Create test server
	cfg := &config.ServerConfig{
		ReadTimeout: 100 * time.Millisecond,
	}
	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.ErrorLevel)
	errorHandler := errors.NewErrorHandler(logrusLogger)

	server := &Server{
		config:       cfg,
		errorHandler: errorHandler,
		logger:       logrusLogger,
		router:       mux.NewRouter(),
	}

	tests := []struct {
		name           string
		path           string
		handler        http.HandlerFunc
		expectTimeout  bool
		expectedStatus int
	}{
		{
			name: "streaming endpoint - no timeout",
			path: "/api/v1/stream/test",
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(200 * time.Millisecond) // Longer than timeout
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("success"))
			},
			expectTimeout:  false,
			expectedStatus: http.StatusOK,
		},
		{
			name: "non-streaming endpoint - fast response",
			path: "/api/v1/health",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			},
			expectTimeout:  false,
			expectedStatus: http.StatusOK,
		},
		{
			name: "non-streaming endpoint - timeout",
			path: "/api/v1/slow",
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(200 * time.Millisecond) // Longer than timeout
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("should not reach here"))
			},
			expectTimeout:  true,
			expectedStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create middleware
			timeoutMw := server.timeoutMiddleware(cfg.ReadTimeout)
			handler := timeoutMw(tt.handler)

			// Create request
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			// Execute
			handler.ServeHTTP(w, req)

			// Verify
			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectTimeout {
				// Timeout responses contain "Request timeout" message
				assert.Contains(t, w.Body.String(), "Request timeout")
			} else if tt.expectedStatus == http.StatusOK {
				// Successful responses should contain expected content
				body := w.Body.String()
				assert.True(t, body == "success" || body == "ok")
			}
		})
	}
}

// TestServer_timeoutMiddleware_StreamingPaths tests various streaming path patterns
func TestServer_timeoutMiddleware_StreamingPaths(t *testing.T) {
	cfg := &config.ServerConfig{
		ReadTimeout: 50 * time.Millisecond,
	}
	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.ErrorLevel)
	errorHandler := errors.NewErrorHandler(logrusLogger)

	server := &Server{
		config:       cfg,
		errorHandler: errorHandler,
		logger:       logrusLogger,
		router:       mux.NewRouter(),
	}

	streamingPaths := []string{
		"/stream",
		"/api/stream/test",
		"/v1/stream/live",
		"/stream/12345",
		"/api/v1/stream",
	}

	timeoutMw := server.timeoutMiddleware(cfg.ReadTimeout)

	for _, path := range streamingPaths {
		t.Run("streaming_path_"+path, func(t *testing.T) {
			slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(100 * time.Millisecond) // Longer than timeout
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("streaming response"))
			})

			handler := timeoutMw(slowHandler)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			// Should not timeout for streaming endpoints
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "streaming response", w.Body.String())
		})
	}
}

// TestServer_metricsMiddleware_Coverage tests additional metrics middleware coverage
func TestServer_metricsMiddleware_Coverage(t *testing.T) {
	cfg := &config.ServerConfig{}
	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.ErrorLevel)
	errorHandler := errors.NewErrorHandler(logrusLogger)

	server := &Server{
		config:       cfg,
		errorHandler: errorHandler,
		logger:       logrusLogger,
		router:       mux.NewRouter(),
	}

	tests := []struct {
		name           string
		method         string
		path           string
		handler        http.HandlerFunc
		expectedStatus int
	}{
		{
			name:   "POST request with body",
			method: http.MethodPost,
			path:   "/api/v1/test",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{"success": true}`))
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name:   "PUT request",
			method: http.MethodPut,
			path:   "/api/v1/update",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:   "DELETE request",
			method: http.MethodDelete,
			path:   "/api/v1/delete",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:   "Error response",
			method: http.MethodGet,
			path:   "/api/v1/error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error": "test error"}`))
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create metrics middleware
			handler := server.metricsMiddleware(tt.handler)

			// Create request with body for POST/PUT
			var body string
			if tt.method == http.MethodPost || tt.method == http.MethodPut {
				body = `{"test": "data"}`
			}

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(body))
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			w := httptest.NewRecorder()

			// Execute
			handler.ServeHTTP(w, req)

			// Verify
			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

// createTestLogger creates a logger for testing
func createTestLogger() logger.Logger {
	// Create a logrus logger with error level to minimize noise in tests
	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.ErrorLevel)
	return logger.NewLogrusAdapter(logrus.NewEntry(logrusLogger))
}
