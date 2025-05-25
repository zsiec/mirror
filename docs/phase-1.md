# Phase 1: Core Foundation and Project Setup

## Overview
This phase establishes the foundational architecture for the Mirror streaming platform. We'll create a single-binary Go application with proper configuration management, logging, error handling, and basic HTTP/3 server infrastructure.

## Goals
- Set up professional Go project structure with modular design
- Implement configuration management with environment variables and YAML
- Create core logging and error handling framework
- Establish HTTP/3 server with health check endpoints
- Integrate Redis for state management
- Set up Docker development environment with NVIDIA runtime

## Directory Structure
```
mirror/
├── cmd/
│   └── mirror/
│       └── main.go              # Application entry point
├── internal/
│   ├── config/
│   │   ├── config.go           # Configuration structs and loading
│   │   └── validate.go         # Config validation
│   ├── server/
│   │   ├── server.go           # HTTP/3 server implementation
│   │   ├── routes.go           # Route definitions
│   │   └── middleware.go       # HTTP middleware
│   ├── health/
│   │   ├── checker.go          # Health check interface
│   │   └── handlers.go         # Health endpoints
│   ├── logger/
│   │   ├── logger.go           # Structured logging setup
│   │   └── context.go          # Request context logging
│   └── errors/
│       ├── errors.go           # Custom error types
│       └── handler.go          # Error response handling
├── pkg/
│   └── version/
│       └── version.go          # Version information
├── configs/
│   ├── default.yaml            # Default configuration
│   └── development.yaml        # Development overrides
├── docker/
│   ├── Dockerfile              # Multi-stage build
│   └── docker-compose.yml      # Development environment
├── scripts/
│   ├── build.sh               # Build script
│   └── test.sh                # Test runner
├── go.mod                      # Go module definition
├── go.sum                      # Dependency checksums
├── Makefile                    # Build automation
└── .env.example               # Environment variables template
```

## Implementation Details

### 1. Go Module and Dependencies
```go
// go.mod
module github.com/yourusername/mirror

go 1.21

require (
    github.com/quic-go/quic-go v0.40.1
    github.com/redis/go-redis/v9 v9.5.1
    github.com/spf13/viper v1.18.2
    github.com/sirupsen/logrus v1.9.3
    github.com/prometheus/client_golang v1.19.0
    github.com/gorilla/mux v1.8.1
    github.com/stretchr/testify v1.9.0
)
```

### 2. Configuration Management
```go
// internal/config/config.go
package config

import (
    "time"
    "github.com/spf13/viper"
)

type Config struct {
    Server   ServerConfig   `mapstructure:"server"`
    Redis    RedisConfig    `mapstructure:"redis"`
    Logging  LoggingConfig  `mapstructure:"logging"`
    Metrics  MetricsConfig  `mapstructure:"metrics"`
}

type ServerConfig struct {
    // HTTP/3 Server
    HTTP3Port       int           `mapstructure:"http3_port"`
    TLSCertFile     string        `mapstructure:"tls_cert_file"`
    TLSKeyFile      string        `mapstructure:"tls_key_file"`
    ReadTimeout     time.Duration `mapstructure:"read_timeout"`
    WriteTimeout    time.Duration `mapstructure:"write_timeout"`
    ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
    
    // QUIC specific
    MaxIncomingStreams    int64 `mapstructure:"max_incoming_streams"`
    MaxIncomingUniStreams int64 `mapstructure:"max_incoming_uni_streams"`
    MaxIdleTimeout        time.Duration `mapstructure:"max_idle_timeout"`
}

type RedisConfig struct {
    Addresses       []string      `mapstructure:"addresses"`
    Password        string        `mapstructure:"password"`
    DB              int           `mapstructure:"db"`
    MaxRetries      int           `mapstructure:"max_retries"`
    DialTimeout     time.Duration `mapstructure:"dial_timeout"`
    ReadTimeout     time.Duration `mapstructure:"read_timeout"`
    WriteTimeout    time.Duration `mapstructure:"write_timeout"`
    PoolSize        int           `mapstructure:"pool_size"`
    MinIdleConns    int           `mapstructure:"min_idle_conns"`
}

type LoggingConfig struct {
    Level      string `mapstructure:"level"`
    Format     string `mapstructure:"format"`     // json or text
    Output     string `mapstructure:"output"`     // stdout, stderr, or file path
    MaxSize    int    `mapstructure:"max_size"`   // MB
    MaxBackups int    `mapstructure:"max_backups"`
    MaxAge     int    `mapstructure:"max_age"`    // days
}

type MetricsConfig struct {
    Enabled bool   `mapstructure:"enabled"`
    Path    string `mapstructure:"path"`
    Port    int    `mapstructure:"port"`
}

func Load(configPath string) (*Config, error) {
    viper.SetConfigType("yaml")
    viper.SetConfigFile(configPath)
    
    // Environment variable override
    viper.SetEnvPrefix("MIRROR")
    viper.AutomaticEnv()
    
    // Defaults
    setDefaults()
    
    if err := viper.ReadInConfig(); err != nil {
        return nil, fmt.Errorf("failed to read config: %w", err)
    }
    
    var cfg Config
    if err := viper.Unmarshal(&cfg); err != nil {
        return nil, fmt.Errorf("failed to unmarshal config: %w", err)
    }
    
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("invalid config: %w", err)
    }
    
    return &cfg, nil
}
```

### 3. HTTP/3 Server Implementation
```go
// internal/server/server.go
package server

import (
    "context"
    "crypto/tls"
    "net/http"
    
    "github.com/quic-go/quic-go"
    "github.com/quic-go/quic-go/http3"
    "github.com/gorilla/mux"
)

type Server struct {
    config     *config.ServerConfig
    router     *mux.Router
    http3Server *http3.Server
    logger     *logrus.Logger
    redis      *redis.Client
}

func New(cfg *config.ServerConfig, logger *logrus.Logger, redis *redis.Client) *Server {
    router := mux.NewRouter()
    
    return &Server{
        config: cfg,
        router: router,
        logger: logger,
        redis:  redis,
    }
}

func (s *Server) Start(ctx context.Context) error {
    // TLS configuration
    tlsConfig := &tls.Config{
        MinVersion: tls.TLS13,
        NextProtos: []string{"h3", "h3-29"},
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
        QUICConfig: quicConfig,
        TLSConfig:  tlsConfig,
    }
    
    // Setup routes
    s.setupRoutes()
    
    // Start server
    s.logger.Infof("Starting HTTP/3 server on port %d", s.config.HTTP3Port)
    
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

func (s *Server) setupRoutes() {
    // Middleware
    s.router.Use(s.loggingMiddleware)
    s.router.Use(s.recoveryMiddleware)
    s.router.Use(s.metricsMiddleware)
    
    // Health checks
    s.router.HandleFunc("/health", s.handleHealth).Methods("GET")
    s.router.HandleFunc("/ready", s.handleReady).Methods("GET")
    
    // API version
    s.router.HandleFunc("/version", s.handleVersion).Methods("GET")
}
```

### 4. Health Check Implementation
```go
// internal/health/checker.go
package health

import (
    "context"
    "sync"
    "time"
)

type Status string

const (
    StatusOK       Status = "ok"
    StatusDegraded Status = "degraded"
    StatusDown     Status = "down"
)

type Check struct {
    Name        string        `json:"name"`
    Status      Status        `json:"status"`
    Message     string        `json:"message,omitempty"`
    LastChecked time.Time     `json:"last_checked"`
    Duration    time.Duration `json:"duration_ms"`
}

type Checker interface {
    Name() string
    Check(ctx context.Context) error
}

type Manager struct {
    checkers []Checker
    results  map[string]*Check
    mu       sync.RWMutex
    logger   *logrus.Logger
}

func NewManager(logger *logrus.Logger) *Manager {
    return &Manager{
        checkers: make([]Checker, 0),
        results:  make(map[string]*Check),
        logger:   logger,
    }
}

func (m *Manager) Register(checker Checker) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.checkers = append(m.checkers, checker)
}

func (m *Manager) RunChecks(ctx context.Context) map[string]*Check {
    var wg sync.WaitGroup
    results := make(map[string]*Check)
    
    for _, checker := range m.checkers {
        wg.Add(1)
        go func(c Checker) {
            defer wg.Done()
            
            start := time.Now()
            err := c.Check(ctx)
            duration := time.Since(start)
            
            check := &Check{
                Name:        c.Name(),
                LastChecked: time.Now(),
                Duration:    duration,
            }
            
            if err != nil {
                check.Status = StatusDown
                check.Message = err.Error()
            } else {
                check.Status = StatusOK
            }
            
            m.mu.Lock()
            m.results[c.Name()] = check
            results[c.Name()] = check
            m.mu.Unlock()
        }(checker)
    }
    
    wg.Wait()
    return results
}

// Redis health checker
type RedisChecker struct {
    client *redis.Client
}

func (r *RedisChecker) Name() string {
    return "redis"
}

func (r *RedisChecker) Check(ctx context.Context) error {
    return r.client.Ping(ctx).Err()
}
```

### 5. Structured Logging
```go
// internal/logger/logger.go
package logger

import (
    "os"
    "github.com/sirupsen/logrus"
    "github.com/natefinch/lumberjack"
)

func New(cfg *config.LoggingConfig) (*logrus.Logger, error) {
    logger := logrus.New()
    
    // Set log level
    level, err := logrus.ParseLevel(cfg.Level)
    if err != nil {
        return nil, fmt.Errorf("invalid log level: %w", err)
    }
    logger.SetLevel(level)
    
    // Set formatter
    if cfg.Format == "json" {
        logger.SetFormatter(&logrus.JSONFormatter{
            TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
        })
    } else {
        logger.SetFormatter(&logrus.TextFormatter{
            FullTimestamp:   true,
            TimestampFormat: "2006-01-02 15:04:05.000",
        })
    }
    
    // Set output
    switch cfg.Output {
    case "stdout":
        logger.SetOutput(os.Stdout)
    case "stderr":
        logger.SetOutput(os.Stderr)
    default:
        // File output with rotation
        logger.SetOutput(&lumberjack.Logger{
            Filename:   cfg.Output,
            MaxSize:    cfg.MaxSize,
            MaxBackups: cfg.MaxBackups,
            MaxAge:     cfg.MaxAge,
            Compress:   true,
        })
    }
    
    return logger, nil
}

// Request context logging
func WithRequest(logger *logrus.Logger, r *http.Request) *logrus.Entry {
    return logger.WithFields(logrus.Fields{
        "request_id": r.Header.Get("X-Request-ID"),
        "method":     r.Method,
        "path":       r.URL.Path,
        "remote_ip":  r.RemoteAddr,
        "user_agent": r.UserAgent(),
    })
}
```

### 6. Main Application Entry Point
```go
// cmd/mirror/main.go
package main

import (
    "context"
    "flag"
    "os"
    "os/signal"
    "syscall"
    
    "github.com/yourusername/mirror/internal/config"
    "github.com/yourusername/mirror/internal/server"
    "github.com/yourusername/mirror/internal/logger"
    "github.com/redis/go-redis/v9"
)

func main() {
    var configPath string
    flag.StringVar(&configPath, "config", "configs/default.yaml", "Path to configuration file")
    flag.Parse()
    
    // Load configuration
    cfg, err := config.Load(configPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
        os.Exit(1)
    }
    
    // Initialize logger
    log, err := logger.New(&cfg.Logging)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
        os.Exit(1)
    }
    
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
        log.Fatalf("Failed to connect to Redis: %v", err)
    }
    log.Info("Connected to Redis successfully")
    
    // Create server
    srv := server.New(&cfg.Server, log, redisClient)
    
    // Setup graceful shutdown
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
    
    go func() {
        <-sigCh
        log.Info("Received shutdown signal")
        cancel()
    }()
    
    // Start server
    if err := srv.Start(ctx); err != nil {
        log.Fatalf("Server error: %v", err)
    }
    
    log.Info("Server shutdown complete")
}
```

### 7. Docker Development Environment
```dockerfile
# docker/Dockerfile
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache git make

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make build

FROM nvidia/cuda:12.3.1-runtime-ubuntu22.04

RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /build/bin/mirror /usr/local/bin/mirror
COPY configs /etc/mirror/configs

EXPOSE 443/udp

CMD ["mirror", "-config", "/etc/mirror/configs/default.yaml"]
```

### 8. Makefile
```makefile
# Makefile
.PHONY: build test clean run docker

BINARY_NAME=mirror
BUILD_DIR=./bin
GO_FILES=$(shell find . -name '*.go' -type f)

build:
	@echo "Building..."
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/mirror

test:
	@echo "Running tests..."
	@go test -v -race -cover ./...

clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)

run: build
	@echo "Running..."
	@$(BUILD_DIR)/$(BINARY_NAME) -config configs/development.yaml

docker:
	@echo "Building Docker image..."
	@docker build -f docker/Dockerfile -t mirror:latest .

fmt:
	@echo "Formatting code..."
	@go fmt ./...

lint:
	@echo "Linting code..."
	@golangci-lint run

generate-certs:
	@echo "Generating self-signed certificates..."
	@openssl req -x509 -newkey rsa:4096 -nodes -keyout certs/key.pem -out certs/cert.pem -days 365 -subj "/CN=localhost"
```

### 9. Default Configuration
```yaml
# configs/default.yaml
server:
  http3_port: 443
  tls_cert_file: "./certs/cert.pem"
  tls_key_file: "./certs/key.pem"
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
  max_incoming_streams: 5000
  max_incoming_uni_streams: 1000
  max_idle_timeout: 30s

redis:
  addresses:
    - "localhost:6379"
  password: ""
  db: 0
  max_retries: 3
  dial_timeout: 5s
  read_timeout: 3s
  write_timeout: 3s
  pool_size: 100
  min_idle_conns: 10

logging:
  level: "info"
  format: "json"
  output: "stdout"
  max_size: 100
  max_backups: 5
  max_age: 30

metrics:
  enabled: true
  path: "/metrics"
  port: 9090
```

## Testing Requirements

### Unit Tests
- Configuration loading and validation
- Health check mechanisms
- Error handling
- Middleware functionality

### Integration Tests
- HTTP/3 server startup and shutdown
- Redis connectivity
- Health endpoint responses
- Graceful shutdown handling

## Deliverables
1. Complete project structure with all directories
2. Working HTTP/3 server with health endpoints
3. Redis integration with connection pooling
4. Structured logging with rotation
5. Docker development environment
6. Makefile for common tasks
7. Unit and integration tests
8. Generated self-signed certificates for development

## Success Criteria
- Server starts successfully on HTTPS/3
- Health endpoints return proper status
- Redis connection is established and monitored
- Logs are structured and include request context
- Graceful shutdown works correctly
- All tests pass with >80% coverage
- Docker image builds and runs successfully

## Next Phase Preview
Phase 2 will build upon this foundation to implement the stream ingestion layer with SRT and RTP listeners, stream registry, and buffer management.
