package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	
	"github.com/zsiec/mirror/internal/logger"
)

var (
	// Prometheus metrics
	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "Duration of HTTP requests in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests",
	}, []string{"method", "path", "status"})

	httpRequestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Number of HTTP requests currently being processed",
	})
)

// requestIDMiddleware adds a unique request ID to each request
func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		
		// Set request ID in response header
		w.Header().Set("X-Request-ID", requestID)
		
		// Add to request header for downstream use
		r.Header.Set("X-Request-ID", requestID)
		
		next.ServeHTTP(w, r)
	})
}

// metricsMiddleware tracks request metrics
func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := r.URL.Path
		
		// Don't track metrics for health endpoints
		if strings.HasPrefix(path, "/health") || strings.HasPrefix(path, "/ready") || strings.HasPrefix(path, "/live") {
			next.ServeHTTP(w, r)
			return
		}
		
		// Track in-flight requests
		httpRequestsInFlight.Inc()
		defer httpRequestsInFlight.Dec()
		
		// Wrap response writer to capture status code
		rw := logger.NewResponseWriter(w)
		
		// Process request
		next.ServeHTTP(rw, r)
		
		// Record metrics
		duration := time.Since(start).Seconds()
		status := fmt.Sprintf("%d", rw.StatusCode())
		
		httpRequestDuration.WithLabelValues(r.Method, path, status).Observe(duration)
		httpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		
		// Log request completion
		log := logger.FromContext(r.Context())
		log.WithFields(logger.Fields{
			"status":      rw.StatusCode(),
			"duration_ms": duration * 1000,
			"bytes":       rw.Header().Get("Content-Length"),
		}).Info("Request completed")
	})
}

// corsMiddleware handles CORS headers
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")
		
		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// recoveryMiddleware recovers from panics
func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				s.logger.WithFields(logger.Fields{
					"error":      err,
					"request_id": r.Header.Get("X-Request-ID"),
					"method":     r.Method,
					"path":       r.URL.Path,
				}).Error("Panic recovered")
				
				s.errorHandler.HandlePanic(w, r, err)
			}
		}()
		
		next.ServeHTTP(w, r)
	})
}

// timeoutMiddleware adds request timeout
func (s *Server) timeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip timeout for streaming endpoints
			if strings.Contains(r.URL.Path, "/stream") {
				next.ServeHTTP(w, r)
				return
			}
			
			http.TimeoutHandler(next, timeout, "Request timeout").ServeHTTP(w, r)
		})
	}
}

// rateLimitMiddleware implements rate limiting (placeholder for now)
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO: Implement rate limiting in a future phase
		next.ServeHTTP(w, r)
	})
}