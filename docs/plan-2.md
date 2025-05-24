# Mirror Streaming Platform - Part 2: Core Implementation

## Table of Contents
1. [Main Application Entry](#main-application-entry)
2. [Configuration Management](#configuration-management)
3. [Server Implementation](#server-implementation)
4. [Service Orchestration](#service-orchestration)
5. [Error Handling and Logging](#error-handling-and-logging)
6. [Memory Management](#memory-management)

## Main Application Entry

### cmd/mirror/main.go
```go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/spf13/cobra"
    "github.com/rs/zerolog"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/internal/server"
)

var (
    // Build variables injected at compile time
    Version   = "dev"
    BuildTime = "unknown"
    GitCommit = "unknown"
)

func main() {
    var rootCmd = &cobra.Command{
        Use:   "mirror",
        Short: "High-performance streaming server",
        Long:  `Mirror is a single-binary streaming platform for ingesting, transcoding, and delivering live video streams.`,
        Run:   runServer,
    }

    // Global flags
    rootCmd.PersistentFlags().StringP("config", "c", "configs/mirror.yaml", "Configuration file path")
    rootCmd.PersistentFlags().StringP("log-level", "l", "info", "Log level (debug, info, warn, error)")
    rootCmd.PersistentFlags().BoolP("pretty-log", "p", false, "Enable pretty logging (for development)")

    // Version command
    rootCmd.AddCommand(&cobra.Command{
        Use:   "version",
        Short: "Print version information",
        Run: func(cmd *cobra.Command, args []string) {
            fmt.Printf("Mirror Streaming Server\n")
            fmt.Printf("  Version:    %s\n", Version)
            fmt.Printf("  Build Time: %s\n", BuildTime)
            fmt.Printf("  Git Commit: %s\n", GitCommit)
        },
    })

    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

func runServer(cmd *cobra.Command, args []string) {
    // Initialize logging
    initLogging(cmd)

    // Load configuration
    configPath, _ := cmd.Flags().GetString("config")
    cfg, err := config.Load(configPath)
    if err != nil {
        log.Fatal().Err(err).Msg("Failed to load configuration")
    }

    // Log startup information
    log.Info().
        Str("version", Version).
        Str("build_time", BuildTime).
        Str("git_commit", GitCommit).
        Msg("Starting Mirror Streaming Server")

    // Create server context
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Initialize server
    srv, err := server.New(ctx, cfg)
    if err != nil {
        log.Fatal().Err(err).Msg("Failed to initialize server")
    }

    // Start server in background
    errChan := make(chan error, 1)
    go func() {
        if err := srv.Start(); err != nil {
            errChan <- err
        }
    }()

    // Wait for interrupt signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

    select {
    case err := <-errChan:
        log.Error().Err(err).Msg("Server error")
    case sig := <-sigChan:
        log.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
    }

    // Graceful shutdown
    log.Info().Msg("Starting graceful shutdown...")
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()

    if err := srv.Shutdown(shutdownCtx); err != nil {
        log.Error().Err(err).Msg("Error during shutdown")
    }

    log.Info().Msg("Server stopped")
}

func initLogging(cmd *cobra.Command) {
    // Configure zerolog
    logLevel, _ := cmd.Flags().GetString("log-level")
    prettyLog, _ := cmd.Flags().GetBool("pretty-log")

    // Set log level
    switch logLevel {
    case "debug":
        zerolog.SetGlobalLevel(zerolog.DebugLevel)
    case "info":
        zerolog.SetGlobalLevel(zerolog.InfoLevel)
    case "warn":
        zerolog.SetGlobalLevel(zerolog.WarnLevel)
    case "error":
        zerolog.SetGlobalLevel(zerolog.ErrorLevel)
    default:
        zerolog.SetGlobalLevel(zerolog.InfoLevel)
    }

    // Configure output format
    if prettyLog {
        log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
    } else {
        // Production format with timestamp
        zerolog.TimeFieldFormat = time.RFC3339Nano
    }

    // Add caller information in debug mode
    if logLevel == "debug" {
        log.Logger = log.With().Caller().Logger()
    }
}
```

## Configuration Management

### internal/config/config.go
```go
package config

import (
    "fmt"
    "strings"
    "time"

    "github.com/spf13/viper"
)

// Config represents the complete application configuration
type Config struct {
    Server      ServerConfig      `mapstructure:"server"`
    Ingestion   IngestionConfig   `mapstructure:"ingestion"`
    Transcoding TranscodingConfig `mapstructure:"transcoding"`
    Storage     StorageConfig     `mapstructure:"storage"`
    Redis       RedisConfig       `mapstructure:"redis"`
    Monitoring  MonitoringConfig  `mapstructure:"monitoring"`
}

// ServerConfig defines HTTP/3 server settings
type ServerConfig struct {
    HTTP3Port       int           `mapstructure:"http3_port"`
    AdminPort       int           `mapstructure:"admin_port"`
    MaxConnections  int           `mapstructure:"max_connections"`
    ReadTimeout     time.Duration `mapstructure:"read_timeout"`
    WriteTimeout    time.Duration `mapstructure:"write_timeout"`
    ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// IngestionConfig defines stream ingestion settings
type IngestionConfig struct {
    SRT struct {
        Enabled     bool          `mapstructure:"enabled"`
        Port        int           `mapstructure:"port"`
        Latency     time.Duration `mapstructure:"latency"`
        MaxBitrate  int           `mapstructure:"max_bitrate"`
        BufferSize  int           `mapstructure:"buffer_size"`
    } `mapstructure:"srt"`
    
    RTP struct {
        Enabled    bool `mapstructure:"enabled"`
        Port       int  `mapstructure:"port"`
        BufferSize int  `mapstructure:"buffer_size"`
    } `mapstructure:"rtp"`
    
    MaxStreams      int `mapstructure:"max_streams"`
    ReconnectDelay  time.Duration `mapstructure:"reconnect_delay"`
}

// TranscodingConfig defines video processing settings
type TranscodingConfig struct {
    UseGPU          bool   `mapstructure:"use_gpu"`
    GPUDevices      []int  `mapstructure:"gpu_devices"`
    OutputWidth     int    `mapstructure:"output_width"`
    OutputHeight    int    `mapstructure:"output_height"`
    OutputBitrate   int    `mapstructure:"output_bitrate"`
    AudioBitrate    int    `mapstructure:"audio_bitrate"`
    WorkerThreads   int    `mapstructure:"worker_threads"`
    MaxQueueSize    int    `mapstructure:"max_queue_size"`
}

// StorageConfig defines storage settings
type StorageConfig struct {
    S3 struct {
        Enabled         bool   `mapstructure:"enabled"`
        Endpoint        string `mapstructure:"endpoint"`
        Region          string `mapstructure:"region"`
        Bucket          string `mapstructure:"bucket"`
        AccessKeyID     string `mapstructure:"access_key_id"`
        SecretAccessKey string `mapstructure:"secret_access_key"`
        UseSSL          bool   `mapstructure:"use_ssl"`
    } `mapstructure:"s3"`
    
    Local struct {
        CacheDir     string        `mapstructure:"cache_dir"`
        MaxCacheSize int64         `mapstructure:"max_cache_size"`
        TTL          time.Duration `mapstructure:"ttl"`
    } `mapstructure:"local"`
    
    DVR struct {
        Enabled       bool          `mapstructure:"enabled"`
        RetentionTime time.Duration `mapstructure:"retention_time"`
        SegmentLength time.Duration `mapstructure:"segment_length"`
    } `mapstructure:"dvr"`
}

// RedisConfig defines Redis connection settings
type RedisConfig struct {
    Addr         string        `mapstructure:"addr"`
    Password     string        `mapstructure:"password"`
    DB           int           `mapstructure:"db"`
    MaxRetries   int           `mapstructure:"max_retries"`
    DialTimeout  time.Duration `mapstructure:"dial_timeout"`
    ReadTimeout  time.Duration `mapstructure:"read_timeout"`
    WriteTimeout time.Duration `mapstructure:"write_timeout"`
    PoolSize     int           `mapstructure:"pool_size"`
}

// MonitoringConfig defines monitoring settings
type MonitoringConfig struct {
    MetricsPath     string        `mapstructure:"metrics_path"`
    HealthCheckPath string        `mapstructure:"health_check_path"`
    ScrapeInterval  time.Duration `mapstructure:"scrape_interval"`
}

// Load reads configuration from file and environment
func Load(configPath string) (*Config, error) {
    v := viper.New()

    // Set config file
    v.SetConfigFile(configPath)

    // Set defaults
    setDefaults(v)

    // Enable environment variable override
    v.SetEnvPrefix("MIRROR")
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
    v.AutomaticEnv()

    // Read config file
    if err := v.ReadInConfig(); err != nil {
        return nil, fmt.Errorf("failed to read config file: %w", err)
    }

    // Unmarshal to struct
    var cfg Config
    if err := v.Unmarshal(&cfg); err != nil {
        return nil, fmt.Errorf("failed to unmarshal config: %w", err)
    }

    // Validate configuration
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("invalid configuration: %w", err)
    }

    return &cfg, nil
}

func setDefaults(v *viper.Viper) {
    // Server defaults
    v.SetDefault("server.http3_port", 443)
    v.SetDefault("server.admin_port", 8080)
    v.SetDefault("server.max_connections", 10000)
    v.SetDefault("server.read_timeout", "30s")
    v.SetDefault("server.write_timeout", "30s")
    v.SetDefault("server.shutdown_timeout", "30s")

    // Ingestion defaults
    v.SetDefault("ingestion.srt.enabled", true)
    v.SetDefault("ingestion.srt.port", 6000)
    v.SetDefault("ingestion.srt.latency", "120ms")
    v.SetDefault("ingestion.srt.max_bitrate", 60000000)
    v.SetDefault("ingestion.srt.buffer_size", 2097152)
    v.SetDefault("ingestion.rtp.enabled", true)
    v.SetDefault("ingestion.rtp.port", 5004)
    v.SetDefault("ingestion.rtp.buffer_size", 1048576)
    v.SetDefault("ingestion.max_streams", 25)
    v.SetDefault("ingestion.reconnect_delay", "5s")

    // Transcoding defaults
    v.SetDefault("transcoding.use_gpu", true)
    v.SetDefault("transcoding.gpu_devices", []int{0})
    v.SetDefault("transcoding.output_width", 640)
    v.SetDefault("transcoding.output_height", 360)
    v.SetDefault("transcoding.output_bitrate", 400000)
    v.SetDefault("transcoding.audio_bitrate", 64000)
    v.SetDefault("transcoding.worker_threads", 4)
    v.SetDefault("transcoding.max_queue_size", 100)

    // Storage defaults
    v.SetDefault("storage.local.cache_dir", "/tmp/mirror-cache")
    v.SetDefault("storage.local.max_cache_size", 10737418240) // 10GB
    v.SetDefault("storage.local.ttl", "1h")
    v.SetDefault("storage.dvr.enabled", true)
    v.SetDefault("storage.dvr.retention_time", "24h")
    v.SetDefault("storage.dvr.segment_length", "500ms")

    // Redis defaults
    v.SetDefault("redis.addr", "localhost:6379")
    v.SetDefault("redis.db", 0)
    v.SetDefault("redis.max_retries", 3)
    v.SetDefault("redis.dial_timeout", "5s")
    v.SetDefault("redis.read_timeout", "3s")
    v.SetDefault("redis.write_timeout", "3s")
    v.SetDefault("redis.pool_size", 10)

    // Monitoring defaults
    v.SetDefault("monitoring.metrics_path", "/metrics")
    v.SetDefault("monitoring.health_check_path", "/health")
    v.SetDefault("monitoring.scrape_interval", "10s")
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
    if c.Server.HTTP3Port < 1 || c.Server.HTTP3Port > 65535 {
        return fmt.Errorf("invalid HTTP3 port: %d", c.Server.HTTP3Port)
    }

    if c.Ingestion.MaxStreams < 1 {
        return fmt.Errorf("max_streams must be at least 1")
    }

    if c.Transcoding.OutputBitrate < 100000 {
        return fmt.Errorf("output_bitrate too low: %d", c.Transcoding.OutputBitrate)
    }

    if c.Storage.S3.Enabled && c.Storage.S3.Bucket == "" {
        return fmt.Errorf("S3 bucket name required when S3 is enabled")
    }

    return nil
}
```

### configs/mirror.yaml
```yaml
# Mirror Streaming Server Configuration

server:
  http3_port: 443
  admin_port: 8080
  max_connections: 10000
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 30s

ingestion:
  srt:
    enabled: true
    port: 6000
    latency: 120ms
    max_bitrate: 60000000  # 60 Mbps
    buffer_size: 2097152   # 2 MB
  rtp:
    enabled: true
    port: 5004
    buffer_size: 1048576   # 1 MB
  max_streams: 25
  reconnect_delay: 5s

transcoding:
  use_gpu: true
  gpu_devices: [0]  # Use first GPU
  output_width: 640
  output_height: 360
  output_bitrate: 400000   # 400 kbps
  audio_bitrate: 64000     # 64 kbps
  worker_threads: 4
  max_queue_size: 100

storage:
  s3:
    enabled: true
    endpoint: ""  # Leave empty for AWS S3
    region: "us-east-1"
    bucket: "mirror-streams"
    access_key_id: ""      # Set via MIRROR_STORAGE_S3_ACCESS_KEY_ID env var
    secret_access_key: ""  # Set via MIRROR_STORAGE_S3_SECRET_ACCESS_KEY env var
    use_ssl: true
  local:
    cache_dir: "/var/lib/mirror/cache"
    max_cache_size: 10737418240  # 10 GB
    ttl: 1h
  dvr:
    enabled: true
    retention_time: 24h
    segment_length: 500ms

redis:
  addr: "localhost:6379"
  password: ""  # Set via MIRROR_REDIS_PASSWORD env var
  db: 0
  max_retries: 3
  dial_timeout: 5s
  read_timeout: 3s
  write_timeout: 3s
  pool_size: 10

monitoring:
  metrics_path: "/metrics"
  health_check_path: "/health"
  scrape_interval: 10s
```

## Server Implementation

### internal/server/server.go
```go
package server

import (
    "context"
    "fmt"
    "net/http"
    "sync"
    "time"

    "github.com/quic-go/quic-go/http3"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    "github.com/redis/go-redis/v9"
    "github.com/rs/zerolog/log"
    
    "github.com/zsiec/mirror/internal/api"
    "github.com/zsiec/mirror/internal/config"
    "github.com/zsiec/mirror/internal/ingestion"
    "github.com/zsiec/mirror/internal/packaging"
    "github.com/zsiec/mirror/internal/storage"
    "github.com/zsiec/mirror/internal/transcoding"
)

// Server represents the main application server
type Server struct {
    ctx    context.Context
    cancel context.CancelFunc
    cfg    *config.Config

    // Core services
    ingestionSvc   *ingestion.Service
    transcodingSvc *transcoding.Service
    packagingSvc   *packaging.Service
    storageSvc     *storage.Service

    // HTTP servers
    http3Server  *http3.Server
    adminServer  *http.Server

    // External clients
    redisClient *redis.Client

    // Synchronization
    wg      sync.WaitGroup
    errChan chan error
}

// New creates a new server instance
func New(ctx context.Context, cfg *config.Config) (*Server, error) {
    ctx, cancel := context.WithCancel(ctx)

    s := &Server{
        ctx:     ctx,
        cancel:  cancel,
        cfg:     cfg,
        errChan: make(chan error, 10),
    }

    // Initialize Redis client
    if err := s.initRedis(); err != nil {
        return nil, fmt.Errorf("failed to initialize Redis: %w", err)
    }

    // Initialize services
    if err := s.initServices(); err != nil {
        return nil, fmt.Errorf("failed to initialize services: %w", err)
    }

    // Initialize HTTP servers
    if err := s.initHTTPServers(); err != nil {
        return nil, fmt.Errorf("failed to initialize HTTP servers: %w", err)
    }

    // Register Prometheus metrics
    s.registerMetrics()

    return s, nil
}

// Start begins serving requests
func (s *Server) Start() error {
    log.Info().Msg("Starting server components")

    // Start core services
    services := []struct {
        name string
        fn   func() error
    }{
        {"ingestion", s.ingestionSvc.Start},
        {"transcoding", s.transcodingSvc.Start},
        {"packaging", s.packagingSvc.Start},
        {"storage", s.storageSvc.Start},
    }

    for _, svc := range services {
        s.wg.Add(1)
        go func(name string, startFn func() error) {
            defer s.wg.Done()
            log.Info().Str("service", name).Msg("Starting service")
            
            if err := startFn(); err != nil {
                log.Error().Err(err).Str("service", name).Msg("Service failed")
                s.errChan <- fmt.Errorf("%s service failed: %w", name, err)
            }
        }(svc.name, svc.fn)
    }

    // Start HTTP servers
    s.wg.Add(2)
    
    // HTTP/3 server
    go func() {
        defer s.wg.Done()
        addr := fmt.Sprintf(":%d", s.cfg.Server.HTTP3Port)
        log.Info().Str("addr", addr).Msg("Starting HTTP/3 server")
        
        if err := s.http3Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Error().Err(err).Msg("HTTP/3 server failed")
            s.errChan <- fmt.Errorf("HTTP/3 server failed: %w", err)
        }
    }()

    // Admin server
    go func() {
        defer s.wg.Done()
        addr := fmt.Sprintf(":%d", s.cfg.Server.AdminPort)
        log.Info().Str("addr", addr).Msg("Starting admin server")
        
        if err := s.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Error().Err(err).Msg("Admin server failed")
            s.errChan <- fmt.Errorf("admin server failed: %w", err)
        }
    }()

    // Monitor for errors
    select {
    case err := <-s.errChan:
        return err
    case <-s.ctx.Done():
        return s.ctx.Err()
    }
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown(ctx context.Context) error {
    log.Info().Msg("Shutting down server")

    // Cancel main context to signal shutdown
    s.cancel()

    // Shutdown HTTP servers
    var shutdownErr error
    
    if err := s.http3Server.Shutdown(ctx); err != nil {
        log.Error().Err(err).Msg("Error shutting down HTTP/3 server")
        shutdownErr = err
    }

    if err := s.adminServer.Shutdown(ctx); err != nil {
        log.Error().Err(err).Msg("Error shutting down admin server")
        if shutdownErr == nil {
            shutdownErr = err
        }
    }

    // Stop services
    services := []struct {
        name string
        svc  interface{ Stop(context.Context) error }
    }{
        {"packaging", s.packagingSvc},
        {"transcoding", s.transcodingSvc},
        {"ingestion", s.ingestionSvc},
        {"storage", s.storageSvc},
    }

    for _, svc := range services {
        log.Info().Str("service", svc.name).Msg("Stopping service")
        if err := svc.svc.Stop(ctx); err != nil {
            log.Error().Err(err).Str("service", svc.name).Msg("Error stopping service")
            if shutdownErr == nil {
                shutdownErr = err
            }
        }
    }

    // Close Redis connection
    if err := s.redisClient.Close(); err != nil {
        log.Error().Err(err).Msg("Error closing Redis connection")
    }

    // Wait for all goroutines
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        log.Info().Msg("All services stopped")
    case <-ctx.Done():
        log.Warn().Msg("Shutdown timeout exceeded")
        return ctx.Err()
    }

    return shutdownErr
}

func (s *Server) initRedis() error {
    s.redisClient = redis.NewClient(&redis.Options{
        Addr:         s.cfg.Redis.Addr,
        Password:     s.cfg.Redis.Password,
        DB:           s.cfg.Redis.DB,
        MaxRetries:   s.cfg.Redis.MaxRetries,
        DialTimeout:  s.cfg.Redis.DialTimeout,
        ReadTimeout:  s.cfg.Redis.ReadTimeout,
        WriteTimeout: s.cfg.Redis.WriteTimeout,
        PoolSize:     s.cfg.Redis.PoolSize,
    })

    // Test connection
    ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
    defer cancel()

    if err := s.redisClient.Ping(ctx).Err(); err != nil {
        return fmt.Errorf("failed to connect to Redis: %w", err)
    }

    log.Info().Str("addr", s.cfg.Redis.Addr).Msg("Connected to Redis")
    return nil
}

func (s *Server) initServices() error {
    var err error

    // Initialize storage service first (needed by others)
    s.storageSvc, err = storage.New(s.ctx, s.cfg.Storage, s.redisClient)
    if err != nil {
        return fmt.Errorf("failed to create storage service: %w", err)
    }

    // Initialize ingestion service
    s.ingestionSvc, err = ingestion.New(s.ctx, s.cfg.Ingestion, s.redisClient)
    if err != nil {
        return fmt.Errorf("failed to create ingestion service: %w", err)
    }

    // Initialize transcoding service
    s.transcodingSvc, err = transcoding.New(s.ctx, s.cfg.Transcoding, s.ingestionSvc, s.redisClient)
    if err != nil {
        return fmt.Errorf("failed to create transcoding service: %w", err)
    }

    // Initialize packaging service
    s.packagingSvc, err = packaging.New(s.ctx, s.cfg, s.transcodingSvc, s.storageSvc, s.redisClient)
    if err != nil {
        return fmt.Errorf("failed to create packaging service: %w", err)
    }

    return nil
}

func (s *Server) initHTTPServers() error {
    // Create HTTP/3 server
    mux := http.NewServeMux()
    
    // Add streaming endpoints
    mux.HandleFunc("/streams/", s.packagingSvc.HandleStreamRequest)
    mux.HandleFunc("/health", s.handleHealth)

    s.http3Server = &http3.Server{
        Addr:    fmt.Sprintf(":%d", s.cfg.Server.HTTP3Port),
        Handler: s.withMiddleware(mux),
        QUICConfig: &quic.Config{
            MaxIdleTimeout:        30 * time.Second,
            MaxIncomingStreams:    1000,
            MaxIncomingUniStreams: 1000,
        },
    }

    // Create admin server
    adminMux := http.NewServeMux()
    
    // Add admin endpoints
    adminAPI := api.NewAdminAPI(s.ingestionSvc, s.transcodingSvc, s.packagingSvc, s.storageSvc)
    adminAPI.RegisterRoutes(adminMux)
    
    // Add Prometheus metrics
    adminMux.Handle(s.cfg.Monitoring.MetricsPath, promhttp.Handler())
    
    s.adminServer = &http.Server{
        Addr:         fmt.Sprintf(":%d", s.cfg.Server.AdminPort),
        Handler:      adminMux,
        ReadTimeout:  s.cfg.Server.ReadTimeout,
        WriteTimeout: s.cfg.Server.WriteTimeout,
    }

    return nil
}

func (s *Server) withMiddleware(handler http.Handler) http.Handler {
    // Add request logging
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        
        // Wrap response writer to capture status
        wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
        
        // Process request
        handler.ServeHTTP(wrapped, r)
        
        // Log request
        log.Info().
            Str("method", r.Method).
            Str("path", r.URL.Path).
            Str("remote", r.RemoteAddr).
            Int("status", wrapped.statusCode).
            Dur("duration", time.Since(start)).
            Msg("HTTP request")
    })
}

type responseWriter struct {
    http.ResponseWriter
    statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
    w.statusCode = code
    w.ResponseWriter.WriteHeader(code)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    // Basic health check
    health := map[string]interface{}{
        "status": "ok",
        "time":   time.Now().Unix(),
        "version": map[string]string{
            "version":    Version,
            "build_time": BuildTime,
            "git_commit": GitCommit,
        },
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(health)
}

func (s *Server) registerMetrics() {
    // Register custom metrics
    prometheus.MustRegister(
        prometheus.NewBuildInfoCollector(),
    )
    
    // Service-specific metrics are registered by each service
}
```

## Service Orchestration

### Service Interface Pattern

All services follow a common interface pattern for consistency:

```go
// internal/common/service.go
package common

import "context"

// Service defines the common interface for all services
type Service interface {
    // Start begins the service operation
    Start() error
    
    // Stop gracefully shuts down the service
    Stop(ctx context.Context) error
    
    // Health returns the current health status
    Health() ServiceHealth
}

// ServiceHealth represents service health information
type ServiceHealth struct {
    Healthy bool              `json:"healthy"`
    Status  string           `json:"status"`
    Details map[string]interface{} `json:"details,omitempty"`
}
```

## Error Handling and Logging

### pkg/utils/errors.go
```go
package utils

import (
    "errors"
    "fmt"
)

// Common error types
var (
    ErrStreamNotFound     = errors.New("stream not found")
    ErrStreamExists       = errors.New("stream already exists")
    ErrCapacityExceeded   = errors.New("capacity exceeded")
    ErrInvalidInput       = errors.New("invalid input")
    ErrTranscodingFailed  = errors.New("transcoding failed")
    ErrStorageUnavailable = errors.New("storage unavailable")
)

// ErrorCode represents an error code for API responses
type ErrorCode string

const (
    CodeInternal      ErrorCode = "INTERNAL_ERROR"
    CodeInvalidInput  ErrorCode = "INVALID_INPUT"
    CodeNotFound      ErrorCode = "NOT_FOUND"
    CodeAlreadyExists ErrorCode = "ALREADY_EXISTS"
    CodeCapacityFull  ErrorCode = "CAPACITY_FULL"
    CodeUnavailable   ErrorCode = "SERVICE_UNAVAILABLE"
)

// AppError represents an application error with code and context
type AppError struct {
    Code    ErrorCode              `json:"code"`
    Message string                 `json:"message"`
    Details map[string]interface{} `json:"details,omitempty"`
    Err     error                  `json:"-"`
}

func (e *AppError) Error() string {
    if e.Err != nil {
        return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
    }
    return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
    return e.Err
}

// NewAppError creates a new application error
func NewAppError(code ErrorCode, message string, err error) *AppError {
    return &AppError{
        Code:    code,
        Message: message,
        Err:     err,
    }
}

// WithDetails adds details to the error
func (e *AppError) WithDetails(details map[string]interface{}) *AppError {
    e.Details = details
    return e
}
```

## Memory Management

### pkg/utils/buffer_pool.go
```go
package utils

import (
    "sync"
)

// BufferPool manages a pool of reusable byte buffers
type BufferPool struct {
    pool sync.Pool
    size int
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(bufferSize int) *BufferPool {
    return &BufferPool{
        size: bufferSize,
        pool: sync.Pool{
            New: func() interface{} {
                return make([]byte, bufferSize)
            },
        },
    }
}

// Get retrieves a buffer from the pool
func (p *BufferPool) Get() []byte {
    return p.pool.Get().([]byte)
}

// Put returns a buffer to the pool
func (p *BufferPool) Put(buf []byte) {
    if cap(buf) != p.size {
        // Don't put back buffers of wrong size
        return
    }
    
    // Reset buffer
    buf = buf[:p.size]
    
    p.pool.Put(buf)
}

// MediaBufferPool is optimized for video frame buffers
type MediaBufferPool struct {
    small  *BufferPool // 64KB - audio/metadata
    medium *BufferPool // 1MB - SD video frames
    large  *BufferPool // 4MB - HD video frames
}

// NewMediaBufferPool creates a pool optimized for media
func NewMediaBufferPool() *MediaBufferPool {
    return &MediaBufferPool{
        small:  NewBufferPool(64 * 1024),
        medium: NewBufferPool(1024 * 1024),
        large:  NewBufferPool(4 * 1024 * 1024),
    }
}

// GetBuffer returns an appropriately sized buffer
func (p *MediaBufferPool) GetBuffer(size int) []byte {
    switch {
    case size <= 64*1024:
        return p.small.Get()
    case size <= 1024*1024:
        return p.medium.Get()
    default:
        return p.large.Get()
    }
}

// PutBuffer returns a buffer to the appropriate pool
func (p *MediaBufferPool) PutBuffer(buf []byte) {
    switch cap(buf) {
    case 64 * 1024:
        p.small.Put(buf)
    case 1024 * 1024:
        p.medium.Put(buf)
    case 4 * 1024 * 1024:
        p.large.Put(buf)
    }
}

// RingBuffer provides a circular buffer for streaming data
type RingBuffer struct {
    data  []byte
    size  int
    read  int
    write int
    mu    sync.Mutex
    cond  *sync.Cond
}

// NewRingBuffer creates a new ring buffer
func NewRingBuffer(size int) *RingBuffer {
    rb := &RingBuffer{
        data: make([]byte, size),
        size: size,
    }
    rb.cond = sync.NewCond(&rb.mu)
    return rb
}

// Write adds data to the buffer
func (rb *RingBuffer) Write(p []byte) (n int, err error) {
    rb.mu.Lock()
    defer rb.mu.Unlock()

    n = len(p)
    for len(p) > 0 {
        // Wait for space
        for rb.available() == 0 {
            rb.cond.Wait()
        }

        // Write what we can
        writeSize := min(len(p), rb.available())
        writeSize = min(writeSize, rb.size-rb.write)

        copy(rb.data[rb.write:], p[:writeSize])
        rb.write = (rb.write + writeSize) % rb.size
        p = p[writeSize:]

        rb.cond.Signal()
    }

    return n, nil
}

// Read retrieves data from the buffer
func (rb *RingBuffer) Read(p []byte) (n int, err error) {
    rb.mu.Lock()
    defer rb.mu.Unlock()

    for rb.used() == 0 {
        rb.cond.Wait()
    }

    n = min(len(p), rb.used())
    n = min(n, rb.size-rb.read)

    copy(p, rb.data[rb.read:rb.read+n])
    rb.read = (rb.read + n) % rb.size

    rb.cond.Signal()
    return n, nil
}

func (rb *RingBuffer) available() int {
    if rb.write >= rb.read {
        return rb.size - rb.write + rb.read - 1
    }
    return rb.read - rb.write - 1
}

func (rb *RingBuffer) used() int {
    if rb.write >= rb.read {
        return rb.write - rb.read
    }
    return rb.size - rb.read + rb.write
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
```

## Next Steps

Continue to:
- [Part 3: Stream Ingestion](plan-3.md) - SRT/RTP implementation details
- [Part 4: HLS Packaging](plan-4.md) - LL-HLS and CMAF implementation
- [Part 5: Admin API](plan-5.md) - Control and monitoring endpoints
- [Part 6: Infrastructure](plan-6.md) - AWS deployment configuration
- [Part 7: Development](plan-7.md) - Local setup and CI/CD pipeline