package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/quic-go/quic-go/http3"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/config"
)

func TestNew(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:             8443,
		TLSCertFile:           "test-cert.pem",
		TLSKeyFile:            "test-key.pem",
		MaxIncomingStreams:    100,
		MaxIncomingUniStreams: 50,
		MaxIdleTimeout:        30 * time.Second,
		ShutdownTimeout:       5 * time.Second,
	}

	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)

	assert.NotNil(t, server)
	assert.Equal(t, cfg, server.config)
	assert.Equal(t, logger, server.logger)
	assert.Equal(t, redisClient, server.redis)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.healthMgr)
	assert.NotNil(t, server.errorHandler)
}

func TestGetRouter(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)
	router := server.GetRouter()

	assert.NotNil(t, router)
	assert.IsType(t, &mux.Router{}, router)
}

func TestSetupRoutes(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port: 8443,
	}
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)
	
	// Manually call setupRoutes since it's normally called in Start()
	server.setupRoutes()

	// Routes should be set up
	req, _ := http.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	server.router.ServeHTTP(rr, req)

	// Should not return 404
	assert.NotEqual(t, http.StatusNotFound, rr.Code)
}

func TestShutdown(t *testing.T) {
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		ShutdownTimeout: 1 * time.Second,
	}
	logger := logrus.New()
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	server := New(cfg, logger, redisClient)
	
	// Create a minimal http3 server for testing
	server.http3Server = &http3.Server{
		Addr: ":8443",
	}

	err := server.Shutdown()
	assert.NoError(t, err)
}
