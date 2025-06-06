package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/server"
	"github.com/zsiec/mirror/pkg/version"
)

func main() {
	var (
		configPath     string
		showVersion    bool
		mutexProfiling bool
	)

	flag.StringVar(&configPath, "config", "configs/default.yaml", "Path to configuration file")
	flag.BoolVar(&showVersion, "version", false, "Show version information")
	flag.BoolVar(&mutexProfiling, "mutex-profiling", false, "Enable mutex profiling for debugging")
	flag.Parse()

	// Show version and exit if requested
	if showVersion {
		fmt.Println(version.GetInfo().String())
		os.Exit(0)
	}

	// Enable mutex profiling if requested
	if mutexProfiling {
		runtime.SetMutexProfileFraction(1)
	}

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logrusLogger, err := logger.New(&cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrusLogger))

	// Log startup information
	log.WithField("version", version.GetInfo().Short()).Info("Starting Mirror streaming server")
	log.WithField("config_path", configPath).Debug("Configuration loaded")

	// Connect to Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addresses[0],
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		MaxRetries:   cfg.Redis.MaxRetries,
		DialTimeout:  cfg.Redis.DialTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
		PoolSize:     cfg.Redis.PoolSize,
		MinIdleConns: cfg.Redis.MinIdleConns,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.WithError(err).Fatal("Failed to connect to Redis")
	}
	log.Info("Connected to Redis successfully")

	// Verify Redis is writable
	testKey := "mirror:startup:test"
	if err := redisClient.Set(ctx, testKey, "1", 0).Err(); err != nil {
		log.WithError(err).Fatal("Redis is not writable")
	}
	redisClient.Del(ctx, testKey)

	// Start metrics server if enabled
	if cfg.Metrics.Enabled {
		go startMetricsServer(cfg.Metrics, log)
	}

	// Create server
	srv := server.New(&cfg.Server, logrusLogger, redisClient)

	// Create ingestion manager if enabled
	var ingestionMgr *ingestion.Manager
	if cfg.Ingestion.SRT.Enabled || cfg.Ingestion.RTP.Enabled {
		var err error
		ingestionMgr, err = ingestion.NewManager(&cfg.Ingestion, log)
		if err != nil {
			log.WithError(err).Fatal("Failed to create ingestion manager")
		}

		// Register ingestion routes
		ingestionHandlers := ingestion.NewHandlers(ingestionMgr, log)
		srv.RegisterRoutes(ingestionHandlers.RegisterRoutes)

		// Start ingestion
		if err := ingestionMgr.Start(); err != nil {
			log.WithError(err).Fatal("Failed to start ingestion manager")
		}
		log.Info("Ingestion manager started")

		defer func() {
			if err := ingestionMgr.Stop(); err != nil {
				log.WithError(err).Error("Failed to stop ingestion manager")
			}
		}()
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		log.WithField("signal", sig).Info("Received shutdown signal")
		cancel()
	}()

	// Start server
	if err := srv.Start(ctx); err != nil {
		log.WithError(err).Fatal("Server error")
	}

	// Cleanup
	if err := redisClient.Close(); err != nil {
		log.WithError(err).Error("Failed to close Redis connection")
	}

	log.Info("Server shutdown complete")
}

// startMetricsServer starts the Prometheus metrics server.
func startMetricsServer(cfg config.MetricsConfig, log logger.Logger) {
	mux := http.NewServeMux()
	mux.Handle(cfg.Path, promhttp.Handler())

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.WithField("addr", addr).Info("Starting metrics server")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.WithError(err).Error("Metrics server error")
	}
}
