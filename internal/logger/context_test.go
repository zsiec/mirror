package logger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextLogger(t *testing.T) {
	logger := logrus.New()
	entry := logger.WithField("test", "value")

	// Test WithLogger
	ctx := context.Background()
	ctx = WithLogger(ctx, entry)

	// Test FromContext
	retrieved := FromContext(ctx)
	assert.Equal(t, "value", retrieved.Data["test"])

	// Test with nil context
	nilEntry := FromContext(context.Background())
	assert.NotNil(t, nilEntry)
}

func TestContextRequestID(t *testing.T) {
	ctx := context.Background()

	// Test WithRequestID
	requestID := "test-request-123"
	ctx = WithRequestID(ctx, requestID)

	// Test GetRequestID
	retrieved := GetRequestID(ctx)
	assert.Equal(t, requestID, retrieved)

	// Test with no request ID
	emptyID := GetRequestID(context.Background())
	assert.Empty(t, emptyID)
}

func TestWithRequest(t *testing.T) {
	logger := logrus.New()

	tests := []struct {
		name          string
		setupRequest  func() *http.Request
		checkEntry    func(*testing.T, *logrus.Entry)
	}{
		{
			name: "with existing request ID",
			setupRequest: func() *http.Request {
				req := httptest.NewRequest("GET", "/test", nil)
				req.Header.Set("X-Request-ID", "existing-id")
				req.Header.Set("User-Agent", "test-agent")
				return req
			},
			checkEntry: func(t *testing.T, entry *logrus.Entry) {
				assert.Equal(t, "existing-id", entry.Data["request_id"])
				assert.Equal(t, "GET", entry.Data["method"])
				assert.Equal(t, "/test", entry.Data["path"])
				assert.Equal(t, "test-agent", entry.Data["user_agent"])
			},
		},
		{
			name: "without request ID",
			setupRequest: func() *http.Request {
				return httptest.NewRequest("POST", "/api/users", nil)
			},
			checkEntry: func(t *testing.T, entry *logrus.Entry) {
				assert.NotEmpty(t, entry.Data["request_id"])
				assert.Equal(t, "POST", entry.Data["method"])
				assert.Equal(t, "/api/users", entry.Data["path"])
			},
		},
		{
			name: "with X-Forwarded-For",
			setupRequest: func() *http.Request {
				req := httptest.NewRequest("GET", "/test", nil)
				req.Header.Set("X-Forwarded-For", "10.0.0.1")
				return req
			},
			checkEntry: func(t *testing.T, entry *logrus.Entry) {
				assert.Equal(t, "10.0.0.1", entry.Data["remote_ip"])
			},
		},
		{
			name: "with X-Real-IP",
			setupRequest: func() *http.Request {
				req := httptest.NewRequest("GET", "/test", nil)
				req.Header.Set("X-Real-IP", "10.0.0.2")
				return req
			},
			checkEntry: func(t *testing.T, entry *logrus.Entry) {
				assert.Equal(t, "10.0.0.2", entry.Data["remote_ip"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.setupRequest()
			entry := WithRequest(logger, req)
			tt.checkEntry(t, entry)
		})
	}
}

func TestRequestLoggerMiddleware(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that logger is in context
		entry := FromContext(r.Context())
		assert.NotNil(t, entry)

		// Check that request ID is in context
		requestID := GetRequestID(r.Context())
		assert.NotEmpty(t, requestID)

		w.WriteHeader(http.StatusOK)
	})

	// Apply middleware
	middleware := RequestLoggerMiddleware(logger)
	handler := middleware(testHandler)

	// Test request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	rw := NewResponseWriter(w)

	// Test default status code
	assert.Equal(t, http.StatusOK, rw.StatusCode())

	// Test WriteHeader
	rw.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, rw.StatusCode())

	// Test that WriteHeader can only be called once
	rw.WriteHeader(http.StatusBadRequest)
	assert.Equal(t, http.StatusCreated, rw.StatusCode())

	// Test Write
	data := []byte("test response")
	n, err := rw.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
}

func TestGetRemoteIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name: "X-Forwarded-For",
			headers: map[string]string{
				"X-Forwarded-For": "192.168.1.1",
			},
			expected: "192.168.1.1",
		},
		{
			name: "X-Real-IP",
			headers: map[string]string{
				"X-Real-IP": "192.168.1.2",
			},
			expected: "192.168.1.2",
		},
		{
			name:     "RemoteAddr fallback",
			headers:  map[string]string{},
			expected: "192.0.2.1:1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.0.2.1:1234"
			
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			ip := getRemoteIP(req)
			assert.Equal(t, tt.expected, ip)
		})
	}
}