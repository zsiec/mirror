# Health Package

The `health` package provides a comprehensive health checking system for the Mirror platform. It supports multiple health check types, concurrent execution, and Kubernetes-compatible endpoints for liveness and readiness probes.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Health Check Types](#health-check-types)
- [Usage](#usage)
- [HTTP Endpoints](#http-endpoints)
- [Built-in Checkers](#built-in-checkers)
- [Custom Checkers](#custom-checkers)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The health check system provides:

- **Extensible health checks** with a simple interface
- **Concurrent execution** of multiple health checks
- **Detailed status reporting** with timing information
- **Kubernetes compatibility** with liveness/readiness endpoints
- **Graceful degradation** for non-critical components
- **Caching** to prevent overwhelming dependencies
- **Configurable timeouts** for each check

## Features

### Core Capabilities

- **Multiple endpoints** - `/health`, `/ready`, `/live`
- **Aggregated status** - Overall system health from all checks
- **Component details** - Individual status for each component
- **Performance metrics** - Check duration and last check time
- **Graceful handling** - Timeouts and error recovery
- **JSON responses** - Structured health information
- **HTTP status codes** - 200 (healthy), 503 (unhealthy)

### Health Status Levels

```go
type Status string

const (
    StatusOK       Status = "ok"       // Component is healthy
    StatusDegraded Status = "degraded" // Partial functionality
    StatusDown     Status = "down"     // Component is unavailable
)
```

## Health Check Types

### Check Result Structure

```go
type Check struct {
    Name        string        `json:"name"`
    Status      Status        `json:"status"`
    Message     string        `json:"message,omitempty"`
    LastChecked time.Time     `json:"last_checked"`
    Duration    time.Duration `json:"duration_ms"`
    Metadata    interface{}   `json:"metadata,omitempty"`
}
```

### Health Response Format

```json
{
  "status": "ok",
  "timestamp": "2024-01-20T15:30:45Z",
  "duration_ms": 45,
  "checks": {
    "redis": {
      "name": "redis",
      "status": "ok",
      "last_checked": "2024-01-20T15:30:45Z",
      "duration_ms": 12
    },
    "disk": {
      "name": "disk",
      "status": "ok",
      "message": "Disk usage: 45%",
      "last_checked": "2024-01-20T15:30:45Z",
      "duration_ms": 3,
      "metadata": {
        "path": "/data",
        "used_bytes": 48318382080,
        "total_bytes": 107374182400,
        "usage_percent": 45
      }
    }
  }
}
```

## Usage

### Basic Setup

```go
import (
    "github.com/zsiec/mirror/internal/health"
    "github.com/redis/go-redis/v9"
)

// Create health manager
healthMgr := health.NewManager(logger, health.Config{
    CheckTimeout:    5 * time.Second,
    CacheDuration:   10 * time.Second,
})

// Register checkers
healthMgr.Register(health.NewRedisChecker(redisClient))
healthMgr.Register(health.NewDiskChecker("/data", 0.9)) // 90% threshold
healthMgr.Register(health.NewMemoryChecker(0.8))         // 80% threshold

// Run health checks
results := healthMgr.RunChecks(ctx)
status := healthMgr.GetOverallStatus()
```

### HTTP Handler Integration

```go
// Setup health endpoints
router.HandleFunc("/health", healthMgr.HandleHealth).Methods("GET")
router.HandleFunc("/ready", healthMgr.HandleReady).Methods("GET")
router.HandleFunc("/live", healthMgr.HandleLive).Methods("GET")
```

## HTTP Endpoints

### `/health` - Detailed Health Status

Returns comprehensive health information for all components:

```bash
curl http://localhost:8080/health

# Response:
{
  "status": "ok",
  "timestamp": "2024-01-20T15:30:45Z",
  "duration_ms": 45,
  "checks": {
    "redis": { "status": "ok", ... },
    "disk": { "status": "ok", ... },
    "memory": { "status": "ok", ... }
  }
}
```

### `/ready` - Readiness Probe

Simple endpoint for Kubernetes readiness checks:

```bash
curl http://localhost:8080/ready

# Healthy response: 200 OK
{
  "status": "ready",
  "timestamp": "2024-01-20T15:30:45Z"
}

# Unhealthy response: 503 Service Unavailable
{
  "status": "not_ready",
  "timestamp": "2024-01-20T15:30:45Z",
  "reason": "redis check failed"
}
```

### `/live` - Liveness Probe

Minimal endpoint for Kubernetes liveness checks:

```bash
curl http://localhost:8080/live

# Response: 200 OK
{
  "status": "alive",
  "timestamp": "2024-01-20T15:30:45Z"
}
```

## Built-in Checkers

### Redis Checker

```go
type RedisChecker struct {
    client *redis.Client
    config RedisCheckerConfig
}

// Usage
checker := health.NewRedisChecker(redisClient, health.RedisCheckerConfig{
    Timeout: 3 * time.Second,
    // Optional: Test specific operations
    TestWrite: true,
    TestRead:  true,
})
```

### Disk Space Checker

```go
type DiskChecker struct {
    path      string
    threshold float64 // Usage percentage threshold
}

// Usage
checker := health.NewDiskChecker("/data", 0.9) // Alert if >90% full

// Check multiple paths
for _, path := range []string{"/data", "/logs", "/tmp"} {
    healthMgr.Register(health.NewDiskChecker(path, 0.9))
}
```

### Memory Checker

```go
type MemoryChecker struct {
    threshold float64 // Memory usage threshold
}

// Usage
checker := health.NewMemoryChecker(0.8) // Alert if >80% used
```

### HTTP Endpoint Checker

```go
type HTTPChecker struct {
    name     string
    url      string
    timeout  time.Duration
    expected int // Expected status code
}

// Usage
checker := health.NewHTTPChecker("api", "http://api:8080/health", 
    5*time.Second, http.StatusOK)
```

### Database Checker

```go
type DatabaseChecker struct {
    db      *sql.DB
    timeout time.Duration
}

// Usage
checker := health.NewDatabaseChecker(db, 3*time.Second)
```

## Custom Checkers

### Implementing the Checker Interface

```go
// Checker interface
type Checker interface {
    Name() string
    Check(ctx context.Context) error
}

// Custom implementation
type StreamIngestionChecker struct {
    manager *ingestion.Manager
}

func (c *StreamIngestionChecker) Name() string {
    return "stream_ingestion"
}

func (c *StreamIngestionChecker) Check(ctx context.Context) error {
    stats := c.manager.GetStats()
    
    // Check if ingestion is accepting connections
    if !stats.AcceptingConnections {
        return fmt.Errorf("not accepting new connections")
    }
    
    // Check error rate
    errorRate := float64(stats.Errors) / float64(stats.Total)
    if errorRate > 0.1 { // 10% error threshold
        return fmt.Errorf("high error rate: %.2f%%", errorRate*100)
    }
    
    return nil
}
```

### Advanced Custom Checker

```go
// Checker with detailed metadata
type GPUChecker struct {
    monitor *gpu.Monitor
}

func (c *GPUChecker) Name() string {
    return "gpu"
}

func (c *GPUChecker) Check(ctx context.Context) error {
    info, err := c.monitor.GetInfo(ctx)
    if err != nil {
        return health.CheckError{
            Err: err,
            Metadata: map[string]interface{}{
                "available": false,
            },
        }
    }
    
    // Return error with metadata
    if info.Temperature > 85 {
        return health.CheckError{
            Err: fmt.Errorf("GPU temperature too high: %d°C", info.Temperature),
            Metadata: map[string]interface{}{
                "temperature": info.Temperature,
                "usage":       info.Usage,
                "memory_used": info.MemoryUsed,
                "memory_total": info.MemoryTotal,
            },
        }
    }
    
    // Healthy - include metadata
    return health.CheckError{
        Metadata: map[string]interface{}{
            "temperature": info.Temperature,
            "usage":       info.Usage,
            "memory_used": info.MemoryUsed,
            "memory_total": info.MemoryTotal,
        },
    }
}
```

## Best Practices

### 1. Check Design

```go
// DO: Quick checks with timeouts
func (c *ServiceChecker) Check(ctx context.Context) error {
    ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
    defer cancel()
    
    return c.service.Ping(ctx)
}

// DON'T: Long-running checks
func (c *BadChecker) Check(ctx context.Context) error {
    // This could take minutes!
    return c.service.FullSystemScan()
}

// DO: Return meaningful errors
func (c *Checker) Check(ctx context.Context) error {
    if err := c.service.Ping(); err != nil {
        return fmt.Errorf("service unreachable: %w", err)
    }
    return nil
}
```

### 2. Critical vs Non-Critical

```go
// Configure critical checks for readiness
readinessChecks := health.NewManager(logger, health.Config{
    Checkers: []health.Checker{
        redisChecker,    // Critical - needed for operation
        databaseChecker, // Critical - needed for operation
    },
})

// Include all checks for detailed health
healthChecks := health.NewManager(logger, health.Config{
    Checkers: []health.Checker{
        redisChecker,
        databaseChecker,
        diskChecker,     // Non-critical - monitoring only
        memoryChecker,   // Non-critical - monitoring only
    },
})
```

### 3. Caching Results

```go
// Configure caching to prevent overwhelming services
manager := health.NewManager(logger, health.Config{
    CacheDuration: 10 * time.Second, // Cache results for 10s
    CheckTimeout:  5 * time.Second,  // Individual check timeout
})

// Force fresh check if needed
results := manager.RunChecks(ctx, health.WithNoCache())
```

### 4. Graceful Degradation

```go
// Handle degraded state
type ServiceChecker struct {
    service     Service
    degradedErr error
}

func (c *ServiceChecker) Check(ctx context.Context) error {
    // Try primary endpoint
    if err := c.service.PingPrimary(ctx); err == nil {
        return nil
    }
    
    // Try fallback
    if err := c.service.PingSecondary(ctx); err == nil {
        c.degradedErr = errors.New("running on fallback endpoint")
        return health.DegradedError{
            Message: "Primary endpoint down, using fallback",
        }
    }
    
    return errors.New("all endpoints unavailable")
}
```

## Testing

### Unit Tests

```go
func TestRedisChecker(t *testing.T) {
    // Mock redis client
    mock := redisMock.NewClient()
    checker := health.NewRedisChecker(mock)
    
    // Test healthy state
    mock.On("Ping", ctx).Return(redis.NewStatusResult("PONG", nil))
    
    err := checker.Check(context.Background())
    assert.NoError(t, err)
    
    // Test unhealthy state
    mock.On("Ping", ctx).Return(redis.NewStatusResult("", errors.New("connection refused")))
    
    err = checker.Check(context.Background())
    assert.Error(t, err)
}
```

### Integration Tests

```go
func TestHealthEndpoints(t *testing.T) {
    // Setup test server
    manager := health.NewManager(logger, health.Config{})
    manager.Register(&MockChecker{healthy: true})
    
    router := mux.NewRouter()
    router.HandleFunc("/health", manager.HandleHealth)
    
    srv := httptest.NewServer(router)
    defer srv.Close()
    
    // Test health endpoint
    resp, err := http.Get(srv.URL + "/health")
    require.NoError(t, err)
    assert.Equal(t, http.StatusOK, resp.StatusCode)
    
    var result map[string]interface{}
    json.NewDecoder(resp.Body).Decode(&result)
    assert.Equal(t, "ok", result["status"])
}
```

## Examples

### Complete Health Setup

```go
// health_setup.go
func SetupHealth(
    logger *logrus.Logger,
    redis *redis.Client,
    db *sql.DB,
    config Config,
) *health.Manager {
    manager := health.NewManager(logger, health.Config{
        CheckTimeout:  5 * time.Second,
        CacheDuration: 10 * time.Second,
    })
    
    // Critical infrastructure
    manager.Register(health.NewRedisChecker(redis))
    manager.Register(health.NewDatabaseChecker(db, 3*time.Second))
    
    // Resource monitoring
    manager.Register(health.NewDiskChecker("/data", 0.9))
    manager.Register(health.NewMemoryChecker(0.8))
    
    // Service dependencies
    if config.TranscodingEnabled {
        manager.Register(health.NewHTTPChecker(
            "transcoding_service",
            config.TranscodingURL + "/health",
            5*time.Second,
            http.StatusOK,
        ))
    }
    
    // Custom business logic checks
    manager.Register(&StreamCapacityChecker{
        maxStreams:     config.MaxStreams,
        currentStreams: streamManager.GetActiveCount,
    })
    
    return manager
}
```

### Kubernetes Configuration

```yaml
# kubernetes/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mirror
spec:
  template:
    spec:
      containers:
      - name: mirror
        image: mirror:latest
        livenessProbe:
          httpGet:
            path: /live
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 10
          timeoutSeconds: 1
          failureThreshold: 3
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
          timeoutSeconds: 1
          failureThreshold: 1
```

### Monitoring Integration

```go
// Export health metrics to Prometheus
type HealthMetricsExporter struct {
    manager *health.Manager
    
    healthGauge *prometheus.GaugeVec
    checkDuration *prometheus.HistogramVec
}

func (e *HealthMetricsExporter) Export() {
    results := e.manager.GetLastResults()
    
    for name, check := range results {
        status := 1.0 // healthy
        if check.Status != health.StatusOK {
            status = 0.0
        }
        
        e.healthGauge.WithLabelValues(name).Set(status)
        e.checkDuration.WithLabelValues(name).Observe(
            check.Duration.Seconds(),
        )
    }
}
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Server Package](../server/README.md) - HTTP endpoint setup
- [Metrics Package](../metrics/README.md) - Prometheus integration
- [Kubernetes Guide](../../docs/kubernetes.md) - Deployment configuration
