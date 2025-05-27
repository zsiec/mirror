package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
)

func TestRouteRegistrationNoDuplicates(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  false,
		ShutdownTimeout: 5 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise during tests
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Track route registrations
	routeCallCount := 0
	testRoute := func(r *mux.Router) {
		routeCallCount++
		// Register a test route that should only be called once
		r.HandleFunc("/test-route", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("test"))
		}).Methods("GET")
	}

	// Register the same route function multiple times
	server.RegisterRoutes(testRoute)
	server.RegisterRoutes(testRoute)

	// Setup routes (this is where the bug would manifest)
	server.setupRoutes()

	// Verify the route function was called the expected number of times
	assert.Equal(t, 2, routeCallCount, "Route registration function should be called for each RegisterRoutes call")

	// Test that the route works
	req, err := http.NewRequest("GET", "/test-route", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "test", rr.Body.String())
}

func TestMultipleRouteRegistrationWithoutConflicts(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  false,
		ShutdownTimeout: 5 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Register multiple different route handlers
	route1Called := false
	route2Called := false

	server.RegisterRoutes(func(r *mux.Router) {
		route1Called = true
		r.HandleFunc("/route1", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("route1"))
		}).Methods("GET")
	})

	server.RegisterRoutes(func(r *mux.Router) {
		route2Called = true
		r.HandleFunc("/route2", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("route2"))
		}).Methods("GET")
	})

	// Setup routes
	server.setupRoutes()

	// Verify both registration functions were called
	assert.True(t, route1Called, "First route registration should be called")
	assert.True(t, route2Called, "Second route registration should be called")

	// Test both routes work
	tests := []struct {
		path     string
		expected string
	}{
		{"/route1", "route1"},
		{"/route2", "route2"},
	}

	for _, tt := range tests {
		req, err := http.NewRequest("GET", tt.path, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, tt.expected, rr.Body.String())
	}
}

func TestPlaceholderRouteOnlyWhenNoAdditionalRoutes(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  false,
		ShutdownTimeout: 5 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	t.Run("PlaceholderRegisteredWhenNoRoutes", func(t *testing.T) {
		server := New(cfg, logger, redisClient)
		server.setupRoutes()

		// Test placeholder route
		req, err := http.NewRequest("GET", "/api/v1/streams", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		// Should not be 404 (placeholder should be registered)
		assert.NotEqual(t, http.StatusNotFound, rr.Code)
	})

	t.Run("PlaceholderNotRegisteredWhenRoutesExist", func(t *testing.T) {
		server := New(cfg, logger, redisClient)

		// Register a route first
		server.RegisterRoutes(func(r *mux.Router) {
			api := r.PathPrefix("/api/v1").Subrouter()
			api.HandleFunc("/streams", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("custom streams handler"))
			}).Methods("GET")
		})

		server.setupRoutes()

		// Test that our custom route works (not placeholder)
		req, err := http.NewRequest("GET", "/api/v1/streams", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "custom streams handler", rr.Body.String())
	})
}

func TestDefaultRoutesAreRegistered(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  false,
		ShutdownTimeout: 5 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)
	server.setupRoutes()

	// Test default routes exist
	defaultRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/health"},
		{"GET", "/ready"},
		{"GET", "/live"},
		{"GET", "/version"},
		{"GET", "/"},
	}

	for _, route := range defaultRoutes {
		req, err := http.NewRequest(route.method, route.path, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		// Should not be 404
		assert.NotEqual(t, http.StatusNotFound, rr.Code, 
			"Route %s %s should be registered", route.method, route.path)
	}
}

func TestDebugEndpointsConditionalRegistration(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	t.Run("DebugEndpointsEnabled", func(t *testing.T) {
		cfg := &config.ServerConfig{
			HTTP3Port:       8443,
			TLSCertFile:     "test-cert.pem",
			TLSKeyFile:      "test-key.pem",
			DebugEndpoints:  true,
			ShutdownTimeout: 5 * time.Second,
		}

		server := New(cfg, logger, redisClient)
		server.setupRoutes()

		// Test debug endpoint exists
		req, err := http.NewRequest("GET", "/debug/info", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Contains(t, rr.Header().Get("Content-Type"), "application/json")
	})

	t.Run("DebugEndpointsDisabled", func(t *testing.T) {
		cfg := &config.ServerConfig{
			HTTP3Port:       8443,
			TLSCertFile:     "test-cert.pem",
			TLSKeyFile:      "test-key.pem",
			DebugEndpoints:  false,
			ShutdownTimeout: 5 * time.Second,
		}

		server := New(cfg, logger, redisClient)
		server.setupRoutes()

		// Test debug endpoint does not exist
		req, err := http.NewRequest("GET", "/debug/info", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

// TestConcurrentRouteRegistration tests that concurrent route registration doesn't cause issues
func TestConcurrentRouteRegistration(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  false,
		ShutdownTimeout: 5 * time.Second,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	// Register routes concurrently
	const numRoutes = 10
	results := make(chan bool, numRoutes)

	for i := 0; i < numRoutes; i++ {
		go func(index int) {
			routePath := fmt.Sprintf("/test-route-%d", index)
			server.RegisterRoutes(func(r *mux.Router) {
				r.HandleFunc(routePath, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(fmt.Sprintf("route-%d", index)))
				}).Methods("GET")
			})
			results <- true
		}(i)
	}

	// Wait for all registrations to complete
	for i := 0; i < numRoutes; i++ {
		<-results
	}

	// Setup routes after all registrations
	server.setupRoutes()

	// Give a small delay to ensure all goroutines have completed registration
	time.Sleep(10 * time.Millisecond)

	// Test that all routes work
	for i := 0; i < numRoutes; i++ {
		routePath := fmt.Sprintf("/test-route-%d", i)
		req, err := http.NewRequest("GET", routePath, nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		server.router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, fmt.Sprintf("route-%d", i), rr.Body.String())
	}
}