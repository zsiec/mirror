package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/errors"
	"github.com/zsiec/mirror/internal/health"
	"github.com/zsiec/mirror/internal/logger"
)

// Server represents the HTTP/3 server.
type Server struct {
	config       *config.ServerConfig
	router       *mux.Router
	http3Server  *http3.Server
	logger       *logrus.Logger
	redis        *redis.Client
	healthMgr    *health.Manager
	errorHandler *errors.ErrorHandler
	
	// Additional handlers can be registered
	additionalRoutes []func(*mux.Router)
}

// New creates a new server instance.
func New(cfg *config.ServerConfig, log *logrus.Logger, redisClient *redis.Client) *Server {
	router := mux.NewRouter()
	healthMgr := health.NewManager(log)
	errorHandler := errors.NewErrorHandler(log)

	s := &Server{
		config:           cfg,
		router:           router,
		logger:           log,
		redis:            redisClient,
		healthMgr:        healthMgr,
		errorHandler:     errorHandler,
		additionalRoutes: make([]func(*mux.Router), 0),
	}

	// Register health checkers
	s.registerHealthCheckers()

	return s
}

// Start starts the HTTP/3 server.
func (s *Server) Start(ctx context.Context) error {
	// TLS configuration
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h3"},
	}

	// Load certificates
	cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificates: %w", err)
	}
	tlsConfig.Certificates = []tls.Certificate{cert}

	// QUIC configuration
	quicConfig := &quic.Config{
		MaxIncomingStreams:    s.config.MaxIncomingStreams,
		MaxIncomingUniStreams: s.config.MaxIncomingUniStreams,
		MaxIdleTimeout:        s.config.MaxIdleTimeout,
		EnableDatagrams:       true,
	}

	// HTTP/3 server
	s.http3Server = &http3.Server{
		Addr:       fmt.Sprintf(":%d", s.config.HTTP3Port),
		Handler:    s.router,
		QuicConfig: quicConfig,
		TLSConfig:  tlsConfig,
	}

	// Setup routes
	s.setupRoutes()

	// Start periodic health checks
	healthCtx := ctx
	go s.healthMgr.StartPeriodicChecks(healthCtx, 30*time.Second)

	// Start server
	s.logger.WithField("port", s.config.HTTP3Port).Info("Starting HTTP/3 server")

	errCh := make(chan error, 1)
	go func() {
		if err := s.http3Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server failed to start: %w", err)
	case <-ctx.Done():
		return s.Shutdown()
	}
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	s.logger.Info("Shutting down HTTP/3 server")

	// Note: http3.Server.Close() doesn't support context-based shutdown
	// The timeout is handled at the application level
	if err := s.http3Server.Close(); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	s.logger.Info("HTTP/3 server shutdown complete")
	return nil
}

// setupRoutes configures all routes
func (s *Server) setupRoutes() {
	// Apply global middleware
	s.router.Use(s.requestIDMiddleware)
	s.router.Use(logger.RequestLoggerMiddleware(s.logger))
	s.router.Use(s.recoveryMiddleware)
	s.router.Use(s.errorHandler.Middleware)
	s.router.Use(s.metricsMiddleware)
	s.router.Use(s.corsMiddleware)

	// Health endpoints
	healthHandler := health.NewHandler(s.healthMgr)
	s.router.HandleFunc("/health", healthHandler.HandleHealth).Methods("GET")
	s.router.HandleFunc("/ready", healthHandler.HandleReady).Methods("GET")
	s.router.HandleFunc("/live", healthHandler.HandleLive).Methods("GET")

	// Version endpoint
	s.router.HandleFunc("/version", s.handleVersion).Methods("GET")

	// API routes
	api := s.router.PathPrefix("/api/v1").Subrouter()
	// Placeholder endpoint - replaced when ingestion is enabled via RegisterRoutes
	api.HandleFunc("/streams", s.handleStreamsPlaceholder).Methods("GET")

	// Register any additional routes
	for _, registerFunc := range s.additionalRoutes {
		registerFunc(s.router)
	}
	
	// 404 handler
	s.router.NotFoundHandler = http.HandlerFunc(s.errorHandler.HandleNotFound)
	s.router.MethodNotAllowedHandler = http.HandlerFunc(s.errorHandler.HandleMethodNotAllowed)
}

// registerHealthCheckers registers all health checkers
func (s *Server) registerHealthCheckers() {
	// Register Redis health checker
	redisChecker := health.NewRedisChecker(s.redis)
	s.healthMgr.Register(redisChecker)

	// Register disk space checker
	diskChecker := health.NewDiskChecker("/", 0.9)
	s.healthMgr.Register(diskChecker)

	// Register memory checker
	memChecker := health.NewMemoryChecker(0.9)
	s.healthMgr.Register(memChecker)
}

// RegisterRoutes adds additional route handlers to the server
func (s *Server) RegisterRoutes(registerFunc func(*mux.Router)) {
	s.additionalRoutes = append(s.additionalRoutes, registerFunc)
}

// GetRouter returns the router for testing.
func (s *Server) GetRouter() *mux.Router {
	return s.router
}
