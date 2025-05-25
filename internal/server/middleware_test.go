package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/config"
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
