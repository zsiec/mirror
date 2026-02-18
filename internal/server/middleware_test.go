package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/errors"
)

func TestRequestIDMiddleware(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that request ID was added
		requestID := r.Header.Get("X-Request-ID")
		assert.NotEmpty(t, requestID)
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with middleware
	handler := server.requestIDMiddleware(testHandler)

	// Test without existing request ID
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotEmpty(t, rr.Header().Get("X-Request-ID"))

	// Test with existing request ID
	existingID := "test-request-id"
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", existingID)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, existingID, rr.Header().Get("X-Request-ID"))
}

func TestCORSMiddleware(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with middleware
	handler := server.corsMiddleware(testHandler)

	// Test regular request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "*", rr.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "GET, POST, PUT, DELETE, OPTIONS", rr.Header().Get("Access-Control-Allow-Methods"))

	// Test OPTIONS request
	req = httptest.NewRequest("OPTIONS", "/test", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestRecoveryMiddleware(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Create test handler that panics
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	// Wrap with recovery middleware
	handler := server.recoveryMiddleware(panicHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Should not panic
	assert.NotPanics(t, func() {
		handler.ServeHTTP(rr, req)
	})
}

func TestRateLimitMiddleware(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with middleware (currently a no-op)
	handler := server.rateLimitMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAPIKeyMiddleware_NoKeyConfigured(t *testing.T) {
	// When no API key is configured, all requests should pass through
	cfg := &config.ServerConfig{
		APIKey: "", // no key configured
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

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := server.apiKeyMiddleware(testHandler)

	// POST without any key should still pass through
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "ok", rr.Body.String())

	// DELETE without any key should still pass through
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/streams/123", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAPIKeyMiddleware_ValidKey(t *testing.T) {
	cfg := &config.ServerConfig{
		APIKey: "test-secret-key-12345",
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

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := server.apiKeyMiddleware(testHandler)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/streams", nil)
			req.Header.Set("X-API-Key", "test-secret-key-12345")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "ok", rr.Body.String())
		})
	}
}

func TestAPIKeyMiddleware_InvalidKey(t *testing.T) {
	cfg := &config.ServerConfig{
		APIKey: "test-secret-key-12345",
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

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("should not reach here"))
	})

	handler := server.apiKeyMiddleware(testHandler)

	methods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	for _, method := range methods {
		t.Run(method+"_wrong_key", func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/streams", nil)
			req.Header.Set("X-API-Key", "wrong-key")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

			var body map[string]string
			err := json.Unmarshal(rr.Body.Bytes(), &body)
			require.NoError(t, err)
			assert.Contains(t, body["error"], "unauthorized")
		})

		t.Run(method+"_missing_key", func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/streams", nil)
			// No X-API-Key header
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusUnauthorized, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

			var body map[string]string
			err := json.Unmarshal(rr.Body.Bytes(), &body)
			require.NoError(t, err)
			assert.Contains(t, body["error"], "unauthorized")
		})
	}
}

func TestAPIKeyMiddleware_ReadOnlyMethodsPassThrough(t *testing.T) {
	cfg := &config.ServerConfig{
		APIKey: "test-secret-key-12345",
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

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := server.apiKeyMiddleware(testHandler)

	// GET and HEAD should pass through even without API key
	readMethods := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, method := range readMethods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/streams", nil)
			// No X-API-Key header
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, "ok", rr.Body.String())
		})
	}
}

func TestCORSMiddleware_IncludesAPIKeyHeader(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := server.corsMiddleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify X-API-Key is included in allowed headers
	allowedHeaders := rr.Header().Get("Access-Control-Allow-Headers")
	assert.Contains(t, allowedHeaders, "X-API-Key")
}
