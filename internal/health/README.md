# Health Package

The `health` package provides a health checking system with concurrent execution, Kubernetes-compatible endpoints, and built-in checkers for Redis, disk, memory, and FFmpeg.

## Core Types

### Status

```go
type Status string

const (
    StatusOK       Status = "ok"
    StatusDegraded Status = "degraded"
    StatusDown     Status = "down"
)
```

### Check Result

```go
type Check struct {
    Name        string                 `json:"name"`
    Status      Status                 `json:"status"`
    Message     string                 `json:"message,omitempty"`
    LastChecked time.Time              `json:"last_checked"`
    Duration    time.Duration          `json:"-"`
    DurationMS  float64                `json:"duration_ms"`
    Details     map[string]interface{} `json:"details,omitempty"`
}
```

### Checker Interface

```go
type Checker interface {
    Name() string
    Check(ctx context.Context) error
}
```

## Manager

```go
mgr := health.NewManager(logger)          // takes *logrus.Logger only
mgr.Register(checker)                      // add a Checker implementation
results := mgr.RunChecks(ctx)              // run all checks concurrently (5s timeout per check)
results = mgr.GetResults()                 // get latest cached results
status := mgr.GetOverallStatus()           // StatusOK, StatusDegraded, or StatusDown
mgr.StartPeriodicChecks(ctx, interval)     // background loop (blocking, run in goroutine)
```

Overall status logic:
- Any check `StatusDown` → overall `StatusDown`
- Any check `StatusDegraded` → overall `StatusDegraded`
- All `StatusOK` → overall `StatusOK`
- No results → `StatusDown`

## HTTP Endpoints

The `Handler` provides three HTTP endpoints:

```go
handler := health.NewHandler(mgr)

// Register routes:
router.HandleFunc("/health", handler.HandleHealth)  // detailed check with all components
router.HandleFunc("/ready", handler.HandleReady)     // readiness probe (uses cached results)
router.HandleFunc("/live", handler.HandleLive)        // liveness probe (always 200)
```

### `/health` Response

```json
{
    "status": "ok",
    "timestamp": "2024-01-20T15:30:45Z",
    "version": "1.0.0",
    "uptime": "2 hours 15 minutes",
    "checks": {
        "redis": { "name": "redis", "status": "ok", "duration_ms": 12 },
        "disk": { "name": "disk", "status": "ok", "duration_ms": 3 },
        "memory": { "name": "memory", "status": "ok", "duration_ms": 0 },
        "ffmpeg": { "name": "ffmpeg", "status": "ok", "duration_ms": 45 }
    }
}
```

- `/health`: Returns 200 (ok/degraded) or 503 (down)
- `/ready`: Returns 200 or 503 based on overall status
- `/live`: Always returns 200 with `{"status": "alive"}`

## Built-in Checkers

### RedisChecker (`redis.go`)

```go
checker := health.NewRedisChecker(redisClient)  // *redis.Client
```

Performs `PING` and `INFO server` commands.

### DiskChecker (`redis.go`)

```go
checker := health.NewDiskChecker("/data", 0.9)  // path, threshold (0.0-1.0)
checker.SetMinFreeBytes(2 * 1024 * 1024 * 1024) // optional: 2GB minimum (default 1GB)
```

Uses `syscall.Statfs` to check disk usage. Fails if usage exceeds threshold or free space < minimum.

### MemoryChecker (`redis.go`)

```go
checker := health.NewMemoryChecker(0.8)  // threshold (0.0-1.0)
checker.SetMemoryLimit(16 * 1024 * 1024 * 1024)  // optional: 16GB (default 8GB)
```

Uses `runtime.MemStats.Alloc` to check Go heap usage against the configured memory limit.

### FFmpegChecker (`ffmpeg.go`)

```go
checker := health.NewFFmpegChecker("")  // empty string auto-discovers via PATH
checker := health.NewFFmpegChecker("/usr/local/bin/ffmpeg")
```

Verifies:
1. FFmpeg binary is available and executable
2. Required codecs are present (h264, hevc, av1)

Also provides `GetFFmpegInfo(ctx)` for detailed info (version, hardware accelerators, available codecs).

## Custom Checkers

Implement the `Checker` interface:

```go
type StreamIngestionChecker struct {
    manager *ingestion.Manager
}

func (c *StreamIngestionChecker) Name() string { return "stream_ingestion" }

func (c *StreamIngestionChecker) Check(ctx context.Context) error {
    if !c.manager.IsAcceptingConnections() {
        return fmt.Errorf("not accepting connections")
    }
    return nil
}
```

## Files

- `checker.go`: Status, Check, Checker interface, Manager
- `handlers.go`: Handler with HandleHealth, HandleReady, HandleLive
- `redis.go`: RedisChecker, DiskChecker, MemoryChecker
- `ffmpeg.go`: FFmpegChecker
- `*_test.go`: Tests
