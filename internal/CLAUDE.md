# Internal Packages

This directory contains the private application code that is not intended to be imported by external projects.

## Package Overview

### config/
Configuration management using Viper. Handles:
- YAML file loading (default.yaml, development.yaml, etc.)
- Environment variable overrides (MIRROR_* prefix)
- Configuration validation
- Hierarchical configuration merging

Key files:
- `config.go`: Main configuration structures and loader
- `validate.go`: Configuration validation logic

### errors/
Custom error handling framework providing:
- Typed errors with HTTP status mapping
- Error wrapping with context preservation
- Consistent error response format
- Error handler middleware

Key types:
- `AppError`: Base error type with Type, Message, Code, Details
- `ErrorHandler`: HTTP error response handler
- Error types: Validation, NotFound, Unauthorized, Internal, etc.

### health/
Health check system implementing:
- Multiple health check interfaces
- Concurrent health check execution
- Health status aggregation
- HTTP endpoints for health monitoring

Key components:
- `Checker` interface: For implementing custom health checks
- `Manager`: Orchestrates multiple health checkers
- Built-in checkers: Redis, Disk, Memory

### logger/
Structured logging utilities built on logrus:
- Context-aware logging
- Request ID propagation
- Log rotation support
- Performance-optimized field handling

Key features:
- Request logger middleware
- Context-based logger retrieval
- Automatic version injection
- Response writer wrapper for metrics

### server/
HTTP/3 server implementation using quic-go:
- QUIC protocol with 0-RTT support
- Middleware chain (request ID, CORS, recovery, etc.)
- Route configuration
- Graceful shutdown

Key components:
- `Server`: Main server struct with HTTP/3 configuration
- Middleware: RequestID, Metrics, CORS, Recovery, RateLimit
- Route handlers for health, version, and streaming APIs

### ingestion/ (Phase 2)
Comprehensive stream ingestion system supporting SRT and RTP protocols:
- Protocol adapters for unified stream handling
- Video-aware buffering with GOP management
- Automatic codec detection and frame assembly
- A/V synchronization with drift correction
- Backpressure control and memory management
- Stream recovery and reconnection
- Redis-based stream registry

Key subsystems:
- `buffer/`: Ring buffers with size limits and metrics
- `codec/`: Depacketizers for H.264, HEVC, AV1, JPEGXS
- `frame/`: Frame assembly and boundary detection
- `gop/`: GOP buffering and management
- `rtp/`, `srt/`: Protocol implementations
- `sync/`: A/V synchronization logic
- `pipeline/`: Video processing pipeline

### metrics/
Prometheus metrics collection and export:
- HTTP metrics (request duration, response size)
- Business metrics (streams, connections, codecs)
- Custom metric types and collectors
- Metric aggregation and labeling

Key features:
- Pre-defined metric names and labels
- Histogram buckets for latency tracking
- Counter and gauge implementations
- Thread-safe metric updates

### queue/
Hybrid memory/disk queue implementation:
- In-memory queue with configurable size
- Automatic disk overflow for large datasets
- Persistence across restarts
- Efficient serialization/deserialization
- Metrics for queue depth and throughput

Key features:
- Thread-safe operations
- Batch processing support
- Priority queue capabilities
- Recovery from disk on startup

## Testing Guidelines

Each package should maintain >80% test coverage. Use table-driven tests where appropriate:

```go
func TestSomething(t *testing.T) {
    tests := []struct {
        name    string
        input   interface{}
        want    interface{}
        wantErr bool
    }{
        // test cases
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test implementation
        })
    }
}
```

## Common Patterns

### Error Handling
```go
if err != nil {
    return errors.Wrap(err, errors.ErrorTypeInternal, "operation failed", http.StatusInternalServerError)
}
```

### Logging with Context
```go
logger := logger.FromContext(ctx)
logger.WithField("stream_id", streamID).Info("Processing stream")
```

### Configuration Access
```go
cfg, err := config.Load("configs/default.yaml")
if err != nil {
    return err
}
```
