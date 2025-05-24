package logger

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const (
	// LoggerKey is the context key for the logger
	LoggerKey contextKey = "logger"
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request_id"
)

// WithLogger adds a logger to the context.
func WithLogger(ctx context.Context, logger *logrus.Entry) context.Context {
	return context.WithValue(ctx, LoggerKey, logger)
}

// FromContext retrieves the logger from context.
func FromContext(ctx context.Context) *logrus.Entry {
	if logger, ok := ctx.Value(LoggerKey).(*logrus.Entry); ok {
		return logger
	}
	// Return a default logger if none found
	return logrus.NewEntry(logrus.StandardLogger())
}

// WithRequestID adds a request ID to the context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// GetRequestID retrieves the request ID from context.
func GetRequestID(ctx context.Context) string {
	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		return requestID
	}
	return ""
}

// WithRequest creates a logger entry with request information.
func WithRequest(logger *logrus.Logger, r *http.Request) *logrus.Entry {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}

	return logger.WithFields(logrus.Fields{
		"request_id": requestID,
		"method":     r.Method,
		"path":       r.URL.Path,
		"remote_ip":  getRemoteIP(r),
		"user_agent": r.UserAgent(),
		"host":       r.Host,
	})
}

// RequestLoggerMiddleware creates a middleware that adds a logger to the request context.
func RequestLoggerMiddleware(logger *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get or create request ID
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
				r.Header.Set("X-Request-ID", requestID)
			}

			// Create logger entry with request information
			entry := WithRequest(logger, r)

			// Add logger to context
			ctx := WithLogger(r.Context(), entry)
			ctx = WithRequestID(ctx, requestID)

			// Log request start
			entry.Info("Request started")

			// Call next handler with updated context
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// getRemoteIP extracts the real client IP considering proxies
func getRemoteIP(r *http.Request) string {
	// Check X-Forwarded-For header
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		return forwarded
	}

	// Check X-Real-IP header
	realIP := r.Header.Get("X-Real-IP")
	if realIP != "" {
		return realIP
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// ResponseWriter wraps http.ResponseWriter to capture status code
type ResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// NewResponseWriter creates a new ResponseWriter.
func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

// WriteHeader captures the status code.
func (rw *ResponseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.ResponseWriter.WriteHeader(code)
		rw.written = true
	}
}

// Write captures that a write has occurred.
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// StatusCode returns the captured status code.
func (rw *ResponseWriter) StatusCode() int {
	return rw.statusCode
}