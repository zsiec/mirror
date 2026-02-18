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
- HTTP/1.1 and HTTP/2 fallback server
- Middleware chain (request ID, CORS, recovery, etc.)
- Route configuration
- Graceful shutdown

Key components:
- `Server`: Main server struct with HTTP/3 configuration
- Middleware: RequestID, Metrics, CORS, Recovery, RateLimit
- Route handlers for health, version, streaming APIs, and debug endpoints

### ingestion/ (Phase 2)
Comprehensive stream ingestion system supporting SRT and RTP protocols. See `ingestion/CLAUDE.md` for full details.

Key subsystems (24 subdirectories):
- `backpressure/`: Watermark-based backpressure control
- `buffer/`: Ring buffers with size limits and metrics
- `codec/`: Depacketizers for H.264, HEVC, AV1, JPEGXS
- `diagnostics/`: Stream diagnostics
- `frame/`: Frame assembly and boundary detection
- `gop/`: GOP buffering and management
- `integrity/`: Stream integrity (checksums, health scoring, validation)
- `memory/`: Memory controller with eviction
- `monitoring/`: Health monitoring, alerts, corruption detection
- `mpegts/`: MPEG-TS parser for SRT streams
- `pipeline/`: Video processing pipeline
- `ratelimit/`: Connection and bandwidth rate limiting
- `reconnect/`: Automatic reconnection logic
- `recovery/`: Error recovery, smart recovery, adaptive quality
- `registry/`: Redis-based stream registry
- `resolution/`: Video resolution detection (H.264/HEVC SPS parsing)
- `rtp/`: RTP protocol implementation
- `security/`: LEB128 parsing, size limits
- `srt/`: SRT protocol implementation
- `sync/`: A/V synchronization with drift correction
- `testdata/`: Test helpers and generators
- `timestamp/`: Timestamp mapping utilities
- `types/`: Shared types (codecs, frames, GOPs, packets, parameter sets)
- `validation/`: PES validation, PTS/DTS, continuity, alignment, fragments

### metrics/
Prometheus metrics collection. See `metrics/CLAUDE.md` for full details.
- Aggregate stream ingestion metrics (no per-stream labels)
- SRT/RTP specific counters
- Debug metrics (goroutines, lock contention, memory)
- Generic Counter/Gauge/Histogram wrapper types

### queue/
Hybrid memory/disk queue. See `queue/CLAUDE.md` for full details.
- In-memory channel with disk overflow
- Background pump goroutine moves disk data back to memory
- Rate limiting, pressure tracking
- Per-stream queue instances

### transcoding/ (Phase 3 - In Progress)
Video transcoding system with FFmpeg integration:
- `caption/`: Caption extraction from video streams
- `ffmpeg/`: FFmpeg library integration for decoding/encoding
- `gpu/`: GPU resource management and scheduling
- `pipeline/`: Transcoding pipeline orchestration

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

## Package Documentation

Each package has its own README.md:
- [config/README.md](config/README.md)
- [errors/README.md](errors/README.md)
- [health/README.md](health/README.md)
- [logger/README.md](logger/README.md)
- [server/README.md](server/README.md)
- [ingestion/README.md](ingestion/README.md)
- [metrics/README.md](metrics/README.md)
- [queue/README.md](queue/README.md)

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
