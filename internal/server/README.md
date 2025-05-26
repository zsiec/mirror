# Server Package

The `server` package implements a high-performance HTTP/3 server using QUIC protocol for the Mirror platform. It provides ultra-low latency communication, built-in middleware, and seamless integration with all platform components.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Architecture](#architecture)
- [Usage](#usage)
- [Middleware](#middleware)
- [Route Configuration](#route-configuration)
- [HTTP/3 & QUIC](#http3--quic)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The server package provides:

- **HTTP/3 server** with QUIC transport for improved performance
- **Comprehensive middleware** stack for common functionality
- **Route management** with path parameters and patterns
- **Graceful shutdown** with connection draining
- **TLS 1.3** with automatic certificate loading
- **Request context** propagation throughout the stack
- **Response helpers** for consistent API responses

## Features

### Core Capabilities

- **0-RTT connections** for returning clients
- **Stream multiplexing** without head-of-line blocking
- **Connection migration** for mobile clients
- **Built-in compression** with HPACK
- **Server push** capabilities
- **Fallback to HTTP/2** when needed
- **WebSocket over QUIC** support

### Performance Features

- **Connection pooling** for backend services
- **Request coalescing** for duplicate requests
- **Response caching** with ETags
- **Static file serving** with in-memory cache
- **Bandwidth throttling** per connection
- **Concurrent stream limits** for DoS protection

## Architecture

### Server Structure

```go
type Server struct {
    config      *config.ServerConfig
    router      *mux.Router
    http3Server *http3.Server
    quicConfig  *quic.Config
    tlsConfig   *tls.Config
    
    // Components
    logger      logger.Logger
    metrics     *metrics.Collector
    health      *health.Manager
    ingestion   *ingestion.Manager
    
    // Middleware
    middleware  []Middleware
    
    // Lifecycle
    ctx         context.Context
    cancel      context.CancelFunc
    wg          sync.WaitGroup
}
```

### Request Flow

```
Client Request
    ↓
QUIC Connection
    ↓
TLS Handshake
    ↓
HTTP/3 Stream
    ↓
Middleware Chain
    ├── Recovery (Panic Handler)
    ├── Request ID
    ├── Logging
    ├── Metrics
    ├── Rate Limiting
    ├── CORS
    └── Authentication
    ↓
Route Handler
    ↓
Response Writer
    ↓
HTTP/3 Response
```

## Usage

### Basic Server Setup

```go
import (
    "github.com/zsiec/mirror/internal/server"
    "github.com/zsiec/mirror/internal/config"
)

// Create server
srv, err := server.New(server.Config{
    HTTP3Port:      8443,
    TLSCertFile:    "certs/cert.pem",
    TLSKeyFile:     "certs/key.pem",
    MaxConnections: 10000,
})

// Add components
srv.SetHealth(healthManager)
srv.SetIngestion(ingestionManager)
srv.SetMetrics(metricsCollector)

// Configure routes
srv.SetupRoutes()

// Start server
if err := srv.Start(ctx); err != nil {
    log.Fatal(err)
}

// Graceful shutdown
srv.Shutdown(ctx)
```

### Custom Configuration

```go
srv := server.New(server.Config{
    // HTTP/3 Configuration
    HTTP3Port:           8443,
    HTTP2Port:           8442, // Fallback
    
    // TLS Configuration
    TLSCertFile:         "certs/cert.pem",
    TLSKeyFile:          "certs/key.pem",
    TLSMinVersion:       tls.TLS13,
    
    // QUIC Configuration
    MaxIncomingStreams:  1000,
    MaxIdleTimeout:      30 * time.Second,
    KeepAlivePeriod:     10 * time.Second,
    
    // Server Limits
    ReadTimeout:         30 * time.Second,
    WriteTimeout:        30 * time.Second,
    MaxHeaderBytes:      1 << 20, // 1MB
    MaxRequestBodySize:  10 << 20, // 10MB
    
    // Performance
    EnableServerPush:    true,
    Enable0RTT:          true,
})
```

## Middleware

### Built-in Middleware

#### Recovery Middleware
```go
// Handles panics gracefully
func RecoveryMiddleware(logger logger.Logger) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            defer func() {
                if err := recover(); err != nil {
                    logger.WithField("panic", err).Error("Panic recovered")
                    
                    // Send error response
                    w.WriteHeader(http.StatusInternalServerError)
                    json.NewEncoder(w).Encode(map[string]string{
                        "error": "Internal server error",
                    })
                }
            }()
            
            next.ServeHTTP(w, r)
        })
    }
}
```

#### Request ID Middleware
```go
// Adds unique request ID for tracing
func RequestIDMiddleware() Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            requestID := r.Header.Get("X-Request-ID")
            if requestID == "" {
                requestID = generateRequestID()
            }
            
            // Add to context
            ctx := context.WithValue(r.Context(), RequestIDKey, requestID)
            r = r.WithContext(ctx)
            
            // Add to response
            w.Header().Set("X-Request-ID", requestID)
            
            next.ServeHTTP(w, r)
        })
    }
}
```

#### Rate Limiting Middleware
```go
// Limits requests per client
func RateLimitMiddleware(limiter *rate.Limiter) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            clientIP := getClientIP(r)
            
            if !limiter.Allow(clientIP) {
                w.WriteHeader(http.StatusTooManyRequests)
                json.NewEncoder(w).Encode(map[string]string{
                    "error": "Rate limit exceeded",
                })
                return
            }
            
            next.ServeHTTP(w, r)
        })
    }
}
```

#### CORS Middleware
```go
// Handles Cross-Origin Resource Sharing
func CORSMiddleware(config CORSConfig) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            origin := r.Header.Get("Origin")
            
            // Check allowed origins
            if isAllowedOrigin(origin, config.AllowedOrigins) {
                w.Header().Set("Access-Control-Allow-Origin", origin)
                w.Header().Set("Access-Control-Allow-Credentials", "true")
            }
            
            // Handle preflight
            if r.Method == "OPTIONS" {
                w.Header().Set("Access-Control-Allow-Methods", 
                    strings.Join(config.AllowedMethods, ", "))
                w.Header().Set("Access-Control-Allow-Headers", 
                    strings.Join(config.AllowedHeaders, ", "))
                w.Header().Set("Access-Control-Max-Age", "86400")
                w.WriteHeader(http.StatusNoContent)
                return
            }
            
            next.ServeHTTP(w, r)
        })
    }
}
```

### Custom Middleware

```go
// Authentication middleware example
func AuthMiddleware(authService *auth.Service) Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Skip auth for public endpoints
            if isPublicEndpoint(r.URL.Path) {
                next.ServeHTTP(w, r)
                return
            }
            
            // Extract token
            token := extractToken(r)
            if token == "" {
                w.WriteHeader(http.StatusUnauthorized)
                json.NewEncoder(w).Encode(map[string]string{
                    "error": "Missing authentication token",
                })
                return
            }
            
            // Validate token
            user, err := authService.ValidateToken(r.Context(), token)
            if err != nil {
                w.WriteHeader(http.StatusUnauthorized)
                json.NewEncoder(w).Encode(map[string]string{
                    "error": "Invalid token",
                })
                return
            }
            
            // Add user to context
            ctx := context.WithValue(r.Context(), UserKey, user)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

## Route Configuration

### Basic Route Setup

```go
func (s *Server) SetupRoutes() {
    r := s.router
    
    // Health endpoints
    r.HandleFunc("/health", s.health.HandleHealth).Methods("GET")
    r.HandleFunc("/ready", s.health.HandleReady).Methods("GET")
    r.HandleFunc("/live", s.health.HandleLive).Methods("GET")
    
    // Metrics endpoint
    r.Handle("/metrics", promhttp.Handler()).Methods("GET")
    
    // API routes
    api := r.PathPrefix("/api/v1").Subrouter()
    
    // Apply API middleware
    api.Use(s.authMiddleware)
    api.Use(s.rateLimitMiddleware)
    
    // Stream routes
    api.HandleFunc("/streams", s.ingestion.HandleListStreams).Methods("GET")
    api.HandleFunc("/streams", s.ingestion.HandleCreateStream).Methods("POST")
    api.HandleFunc("/streams/{id}", s.ingestion.HandleGetStream).Methods("GET")
    api.HandleFunc("/streams/{id}", s.ingestion.HandleDeleteStream).Methods("DELETE")
    
    // Static files
    r.PathPrefix("/static/").Handler(
        http.StripPrefix("/static/", 
            http.FileServer(http.Dir("./static/"))))
}
```

### Advanced Routing

```go
// Route with middleware chain
api.Handle("/admin/streams", 
    Chain(
        s.adminAuth,
        s.audit,
        http.HandlerFunc(s.handleAdminStreams),
    )).Methods("GET")

// Subrouter with prefix
admin := api.PathPrefix("/admin").Subrouter()
admin.Use(s.adminAuthMiddleware)
admin.HandleFunc("/users", s.handleAdminUsers)
admin.HandleFunc("/config", s.handleAdminConfig)

// WebSocket endpoint
r.HandleFunc("/ws", s.handleWebSocket)

// Server-Sent Events
r.HandleFunc("/events", s.handleSSE)
```

## HTTP/3 & QUIC

### QUIC Configuration

```go
// Optimize QUIC for video streaming
quicConfig := &quic.Config{
    // Performance
    MaxIncomingStreams:      5000,
    MaxIncomingUniStreams:   1000,
    MaxStreamReceiveWindow:  6 * 1024 * 1024,  // 6MB
    MaxConnectionReceiveWindow: 15 * 1024 * 1024, // 15MB
    
    // Timeouts
    HandshakeIdleTimeout:    10 * time.Second,
    MaxIdleTimeout:          30 * time.Second,
    KeepAlivePeriod:         10 * time.Second,
    
    // Features
    EnableDatagrams:         true,  // For low-latency data
    Enable0RTT:              true,  // Fast reconnection
    
    // Congestion Control
    CongestionControl:       "bbr",  // Better for video
}
```

### 0-RTT Support

```go
// Enable 0-RTT for returning clients
func (s *Server) setup0RTT() {
    s.tlsConfig.SessionTicketsDisabled = false
    s.tlsConfig.SessionTicketKey = s.config.SessionKey
    
    // Session cache
    cache := tls.NewLRUClientSessionCache(10000)
    s.tlsConfig.ClientSessionCache = cache
    
    // Early data handler
    s.http3Server.SetEarlyDataFunc(func(conn quic.EarlyConnection) {
        // Process early data
        log.Info("0-RTT connection established")
    })
}
```

### Connection Migration

```go
// Handle connection migration for mobile clients
func (s *Server) handleMigration() {
    s.quicConfig.EnableConnectionMigration = true
    
    // Migration callback
    s.http3Server.OnConnectionMigration = func(conn quic.Connection) {
        log.WithFields(logger.Fields{
            "old_addr": conn.RemoteAddr(),
            "new_addr": conn.LocalAddr(),
        }).Info("Connection migrated")
        
        // Update connection tracking
        s.updateConnectionInfo(conn)
    }
}
```

## Best Practices

### 1. Error Handling

```go
// Consistent error responses
func (s *Server) respondError(w http.ResponseWriter, code int, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    
    response := ErrorResponse{
        Error:     http.StatusText(code),
        Message:   message,
        RequestID: getRequestID(r),
        Timestamp: time.Now(),
    }
    
    json.NewEncoder(w).Encode(response)
}

// Handler with error handling
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
    streamID := mux.Vars(r)["id"]
    
    stream, err := s.ingestion.GetStream(r.Context(), streamID)
    if err != nil {
        if errors.Is(err, ErrNotFound) {
            s.respondError(w, http.StatusNotFound, "Stream not found")
            return
        }
        s.respondError(w, http.StatusInternalServerError, "Internal error")
        return
    }
    
    s.respondJSON(w, http.StatusOK, stream)
}
```

### 2. Response Helpers

```go
// JSON response helper
func (s *Server) respondJSON(w http.ResponseWriter, code int, data interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    
    if err := json.NewEncoder(w).Encode(data); err != nil {
        s.logger.WithError(err).Error("Failed to encode response")
    }
}

// Streaming response
func (s *Server) streamResponse(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "Streaming not supported", http.StatusInternalServerError)
        return
    }
    
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    
    // Stream data
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-r.Context().Done():
            return
        case <-ticker.C:
            fmt.Fprintf(w, "data: %s\n\n", time.Now().Format(time.RFC3339))
            flusher.Flush()
        }
    }
}
```

### 3. Graceful Shutdown

```go
func (s *Server) Shutdown(ctx context.Context) error {
    s.logger.Info("Starting graceful shutdown")
    
    // Stop accepting new connections
    s.cancel()
    
    // Create shutdown context with timeout
    shutdownCtx, cancel := context.WithTimeout(ctx, s.config.ShutdownTimeout)
    defer cancel()
    
    // Shutdown HTTP/3 server
    if err := s.http3Server.Shutdown(shutdownCtx); err != nil {
        s.logger.WithError(err).Error("HTTP/3 shutdown error")
    }
    
    // Wait for ongoing requests
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        s.logger.Info("Graceful shutdown completed")
        return nil
    case <-shutdownCtx.Done():
        s.logger.Warn("Shutdown timeout exceeded")
        return shutdownCtx.Err()
    }
}
```

### 4. Security

```go
// Security headers middleware
func SecurityHeaders() Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Security headers
            w.Header().Set("X-Content-Type-Options", "nosniff")
            w.Header().Set("X-Frame-Options", "DENY")
            w.Header().Set("X-XSS-Protection", "1; mode=block")
            w.Header().Set("Strict-Transport-Security", 
                "max-age=31536000; includeSubDomains")
            w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
            w.Header().Set("Content-Security-Policy", 
                "default-src 'self'; script-src 'self'")
            
            next.ServeHTTP(w, r)
        })
    }
}
```

## Testing

### Unit Tests

```go
func TestServer_Start(t *testing.T) {
    // Create test server
    srv := server.New(server.Config{
        HTTP3Port:   0, // Random port
        TLSCertFile: "testdata/cert.pem",
        TLSKeyFile:  "testdata/key.pem",
    })
    
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    // Start server
    errCh := make(chan error, 1)
    go func() {
        errCh <- srv.Start(ctx)
    }()
    
    // Wait for startup
    time.Sleep(100 * time.Millisecond)
    
    // Test connection
    addr := srv.Addr()
    conn, err := quic.DialAddr(addr, &tls.Config{
        InsecureSkipVerify: true,
    }, nil)
    require.NoError(t, err)
    defer conn.Close()
    
    // Shutdown
    cancel()
    err = <-errCh
    assert.NoError(t, err)
}
```

### Integration Tests

```go
func TestServerIntegration(t *testing.T) {
    // Setup test server with all components
    srv := setupTestServer(t)
    defer srv.Shutdown(context.Background())
    
    // Create HTTP/3 client
    client := &http.Client{
        Transport: &http3.RoundTripper{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,
            },
        },
    }
    
    // Test health endpoint
    resp, err := client.Get(srv.URL() + "/health")
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)
    
    // Test API endpoint
    resp, err = client.Get(srv.URL() + "/api/v1/streams")
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)
}
```

### Benchmark Tests

```go
func BenchmarkServer_HandleRequest(b *testing.B) {
    srv := setupBenchmarkServer(b)
    defer srv.Shutdown(context.Background())
    
    client := &http.Client{
        Transport: &http3.RoundTripper{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,
            },
        },
    }
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            resp, err := client.Get(srv.URL() + "/api/v1/streams/test")
            if err != nil {
                b.Fatal(err)
            }
            resp.Body.Close()
        }
    })
}
```

## Examples

### Complete Server Setup

```go
// main.go
func main() {
    // Load configuration
    cfg, err := config.Load("config.yaml")
    if err != nil {
        log.Fatal(err)
    }
    
    // Initialize logger
    logger, err := logger.New(cfg.Logging)
    if err != nil {
        log.Fatal(err)
    }
    
    // Create components
    healthMgr := health.NewManager(logger)
    metricsMgr := metrics.NewManager()
    ingestionMgr := ingestion.NewManager(cfg.Ingestion, logger)
    
    // Create server
    srv := server.New(server.Config{
        ServerConfig: cfg.Server,
        Logger:       logger,
        Health:       healthMgr,
        Metrics:      metricsMgr,
        Ingestion:    ingestionMgr,
    })
    
    // Setup middleware
    srv.Use(
        server.RecoveryMiddleware(logger),
        server.RequestIDMiddleware(),
        server.LoggingMiddleware(logger),
        server.MetricsMiddleware(metricsMgr),
        server.RateLimitMiddleware(rateLimit),
        server.CORSMiddleware(corsConfig),
    )
    
    // Setup routes
    srv.SetupRoutes()
    
    // Start server
    ctx, stop := signal.NotifyContext(context.Background(), 
        os.Interrupt, syscall.SIGTERM)
    defer stop()
    
    if err := srv.Start(ctx); err != nil {
        logger.WithError(err).Fatal("Server failed")
    }
    
    // Wait for shutdown
    <-ctx.Done()
    
    // Graceful shutdown
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 
        30*time.Second)
    defer cancel()
    
    if err := srv.Shutdown(shutdownCtx); err != nil {
        logger.WithError(err).Error("Shutdown error")
    }
}
```

### Custom Handler with Streaming

```go
// Stream HLS segments
func (s *Server) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    streamID := vars["stream_id"]
    segmentID := vars["segment_id"]
    
    // Get segment
    segment, err := s.hls.GetSegment(r.Context(), streamID, segmentID)
    if err != nil {
        s.respondError(w, http.StatusNotFound, "Segment not found")
        return
    }
    
    // Set headers
    w.Header().Set("Content-Type", "video/mp2t")
    w.Header().Set("Content-Length", strconv.Itoa(len(segment.Data)))
    w.Header().Set("Cache-Control", "public, max-age=3600")
    
    // Enable HTTP/3 server push for next segments
    if pusher, ok := w.(http.Pusher); ok {
        nextSegments := s.hls.GetNextSegments(streamID, segmentID, 3)
        for _, next := range nextSegments {
            pusher.Push(next.URL, nil)
        }
    }
    
    // Stream segment
    if _, err := w.Write(segment.Data); err != nil {
        s.logger.WithError(err).Error("Failed to write segment")
    }
}
```

## HTTP/1.1 and HTTP/2 Fallback Support

The server supports running HTTP/1.1 and HTTP/2 alongside HTTP/3 for debugging and compatibility:

### Configuration

```yaml
server:
  # HTTP/3 (QUIC) - Primary protocol
  http3_port: 8443              # UDP port
  
  # HTTP/1.1 and HTTP/2 - Fallback for debugging
  http_port: 8443               # TCP port (same number, different protocol)
  enable_http: true             # Enable fallback server
  enable_http2: true            # Support HTTP/2 over TLS
  debug_endpoints: true         # Enable /debug/pprof/* endpoints
```

### How It Works

The server can listen on the same port number for both HTTP/3 and HTTP/1.1/2 because they use different transport protocols:
- **HTTP/3**: Uses UDP (QUIC protocol)
- **HTTP/1.1/2**: Uses TCP (traditional protocols)

Clients automatically choose the appropriate protocol:
```bash
# Modern browsers and clients that support HTTP/3
curl --http3 https://localhost:8443/health

# Clients that only support HTTP/2
curl --http2 https://localhost:8443/health

# Legacy clients or debugging tools
curl --http1.1 https://localhost:8443/health
```

### Debug Endpoints

When `debug_endpoints: true`, the following endpoints are available:

```bash
# pprof profiling endpoints
/debug/pprof/              # Index of available profiles
/debug/pprof/profile       # CPU profile
/debug/pprof/heap          # Heap memory profile
/debug/pprof/goroutine     # Goroutine stack traces
/debug/pprof/allocs        # Memory allocations
/debug/pprof/block         # Blocking profile
/debug/pprof/mutex         # Mutex contention
/debug/pprof/trace         # Execution trace

# Server info endpoint
/debug/info                # Protocol and port information
```

### Usage Examples

```bash
# Test protocol negotiation
./scripts/test-protocols.sh

# CPU profiling
go tool pprof -http=:8081 https://localhost:8443/debug/pprof/profile?seconds=30

# Memory profiling
go tool pprof https://localhost:8443/debug/pprof/heap

# Live goroutine inspection
curl -s https://localhost:8443/debug/pprof/goroutine?debug=2 | less

# Server protocol info
curl -s https://localhost:8443/debug/info | jq .
```

### Security Considerations

1. **Production Deployment**: Disable HTTP/1.1/2 fallback and debug endpoints:
   ```yaml
   server:
     enable_http: false
     debug_endpoints: false
   ```

2. **Access Control**: If debug endpoints must be enabled in production:
   ```go
   // Add authentication middleware for debug routes
   debugRouter := s.router.PathPrefix("/debug").Subrouter()
   debugRouter.Use(s.debugAuthMiddleware)
   ```

3. **Network Isolation**: Use firewall rules to restrict access to debug endpoints

### Troubleshooting

1. **Port Already in Use**:
   - HTTP/3 (UDP) and HTTP/1.1/2 (TCP) can share the same port number
   - If you get "address already in use", check for conflicting TCP services

2. **Protocol Not Working**:
   ```bash
   # Check if both servers are running
   netstat -an | grep 8443
   # Should show both udp and tcp listeners
   ```

3. **Client Compatibility**:
   - Not all clients support HTTP/3
   - Use `--http2` or `--http1.1` flags to force specific protocols
   - Check client capabilities with `curl --version`

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Configuration Package](../config/README.md) - Server configuration
- [Health Package](../health/README.md) - Health check endpoints
- [Metrics Package](../metrics/README.md) - Metrics collection
- [HTTP/3 Guide](../../docs/http3.md) - HTTP/3 implementation details
