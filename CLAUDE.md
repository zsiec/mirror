# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mirror is a high-performance video streaming platform built in Go, designed to handle:
- 25 concurrent SRT/RTP streams at 50mbps with HEVC input
- Transcoding to HLS-compatible formats with hardware acceleration
- Broadcasting to 5,000 concurrent viewers using Low-Latency HLS (LL-HLS)
- Multi-stream viewing with up to 6 concurrent streams per viewer

## Current Status

### Phase 1 ✅ COMPLETED
- Go project structure with modular architecture
- Configuration management (Viper-based YAML + env)
- Structured logging with rotation (logrus)
- Custom error handling framework
- Health check system (Redis, disk, memory)
- HTTP/3 server with QUIC protocol
- Docker environment with NVIDIA CUDA support
- GitHub Actions CI/CD
- 71% test coverage

### Phase 2 ✅ COMPLETED
- Stream ingestion with SRT and RTP protocols
- Video-aware buffering and GOP management
- Automatic codec detection (H.264, HEVC, AV1, JPEGXS)
- Frame assembly and validation
- A/V synchronization with drift correction
- Backpressure control and memory management
- Stream recovery and reconnection
- Comprehensive metrics and monitoring
- Redis-based stream registry
- 85%+ test coverage for ingestion components

### Next Phases
- Phase 3: Video Processing & Transcoding
- Phase 4: HLS Packaging & Distribution
- Phase 5: Multi-stream Management
- Phase 6: CDN Integration
- Phase 7: Monitoring & Observability

## Architecture

### Core Technology Stack
- **Language**: Go 1.23+
- **HTTP/3 Server**: `quic-go/quic-go` for low-latency delivery
- **Configuration**: Viper for YAML + environment variable support
- **Logging**: Logrus with log rotation
- **State Management**: Redis for session management and stream registry
- **Metrics**: Prometheus for monitoring
- **Container**: Docker with multi-stage builds
- **Stream Protocols**: SRT (Haivision), RTP/RTCP
- **Video Codecs**: H.264, HEVC/H.265, AV1, JPEGXS
- **Container Formats**: MPEG-TS

### Service Architecture
```
HTTP/3 Server (QUIC)
    ├── Health Checks (/health, /ready, /live)
    ├── Version Info (/version)
    ├── Metrics (/metrics - Prometheus)
    └── Stream API (/api/v1/)
        ├── Ingestion Service
        │   ├── SRT Listener (port 30000)
        │   ├── RTP Listener (port 5004)
        │   ├── Stream Management (/streams)
        │   ├── Statistics (/stats)
        │   └── A/V Sync Control (/sync)
        ├── Transcoding Service (Phase 3)
        ├── HLS Packager (Phase 4)
        └── CDN Distribution (Phase 6)
```

## Repository Structure

```
mirror/
├── cmd/                    # Application entry points
│   ├── mirror/            # Main server application
│   └── test-client/       # HTTP/3 test client
├── internal/              # Private application code
│   ├── config/           # Configuration management
│   ├── errors/           # Error handling framework
│   ├── health/           # Health check system
│   ├── ingestion/        # Stream ingestion (Phase 2)
│   │   ├── buffer/       # Video-aware buffering
│   │   ├── codec/        # Codec detection & handling
│   │   ├── frame/        # Frame assembly & detection
│   │   ├── gop/          # GOP management
│   │   ├── rtp/          # RTP protocol implementation
│   │   ├── srt/          # SRT protocol implementation
│   │   ├── sync/         # A/V synchronization
│   │   └── ...           # Recovery, backpressure, etc.
│   ├── logger/           # Logging utilities
│   ├── metrics/          # Prometheus metrics
│   ├── queue/            # Hybrid memory/disk queue
│   └── server/           # HTTP/3 server implementation
├── pkg/                   # Public packages
│   └── version/          # Version information
├── configs/              # Configuration files
├── docker/               # Docker-related files
├── docs/                 # Documentation
│   ├── openapi/          # API specifications
│   └── phase-*.md       # Implementation phase docs
├── .github/              # GitHub Actions workflows
└── tests/                # Integration test helpers
```

## Development Guidelines

### Code Style
- Follow standard Go conventions
- Use structured logging with proper context
- Handle all errors explicitly
- Write tests for all new functionality
- Keep functions focused and under 50 lines
- Add godoc comments for all exported types/functions

### Testing
```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Run specific package tests
go test ./internal/health/...

# Run with race detector
go test -race ./...
```

### Building & Running
```bash
# Build the application
make build

# Run locally
make run

# Run with Docker
make docker-run

# Run with hot reload (development)
make dev
```

### Linting
```bash
# Run linters
make lint

# Auto-fix some issues
make fmt
```

### Common Commands
```bash
# Generate self-signed certificates
make certs

# Clean build artifacts
make clean

# View logs
docker-compose logs -f mirror

# Check HTTP/3 endpoints
./bin/test-client -url https://localhost:8443/health
```

## Configuration

The application uses a hierarchical configuration system:
1. Default values (configs/default.yaml)
2. Environment-specific (configs/development.yaml, configs/production.yaml)
3. Environment variables (MIRROR_* prefix)
4. Command-line flags

Example environment variables:
```bash
# Server configuration
MIRROR_SERVER_HTTP3_PORT=8443
MIRROR_REDIS_ADDR=localhost:6379
MIRROR_LOGGING_LEVEL=debug

# Ingestion configuration
MIRROR_INGESTION_SRT_PORT=30000
MIRROR_INGESTION_RTP_PORT=5004
MIRROR_INGESTION_MAX_CONNECTIONS=25
MIRROR_INGESTION_STREAM_TIMEOUT=30s
MIRROR_INGESTION_BUFFER_SIZE=1048576
MIRROR_INGESTION_GOP_BUFFER_SIZE=3
```

## Error Handling

The application uses a custom error framework with typed errors:
- `AppError`: Base error type with HTTP status mapping
- Error types: Validation, NotFound, Unauthorized, Internal, etc.
- Consistent error responses with trace IDs
- Panic recovery middleware

## Health Checks

Three health endpoints:
- `/health`: Detailed health status with all checks
- `/ready`: Simple readiness check
- `/live`: Basic liveness check

Health checks include:
- Redis connectivity
- Disk space availability
- Memory usage
- Custom service checks (future)

## Security Considerations
- TLS 1.3 minimum for all connections
- Request ID tracking for audit trails
- Rate limiting on all endpoints
- CORS configuration for browser clients
- No sensitive data in logs
- Environment-based secrets management

## Performance Optimization
- HTTP/3 with 0-RTT support
- Connection pooling for Redis
- Structured logging with minimal allocation
- Graceful shutdown with timeout
- Memory-efficient error handling

## Monitoring
- Prometheus metrics endpoint (:9090/metrics)
- Request duration histograms
- Active connection gauges
- Error rate counters
- Custom business metrics (future)

## Video Streaming Components (Phase 2)

### Stream Ingestion
- **Protocol Support**: SRT (primary) and RTP with automatic detection
- **Codec Support**: H.264, HEVC, AV1, JPEGXS with automatic detection
- **Buffer Management**: Ring buffers with size limits and backpressure
- **Frame Assembly**: Codec-aware frame reconstruction from packets
- **GOP Buffering**: Maintains complete GOPs for clean switching
- **A/V Sync**: Automatic drift detection and correction

### API Endpoints
- `GET /api/v1/streams` - List active streams
- `GET /api/v1/streams/{id}` - Get stream details
- `DELETE /api/v1/streams/{id}` - Stop a stream
- `POST /api/v1/streams/{id}/pause` - Pause ingestion
- `POST /api/v1/streams/{id}/resume` - Resume ingestion
- `GET /api/v1/stats` - System-wide statistics
- `GET /api/v1/video/preview/{id}` - Stream preview data
- `GET /api/v1/sync/status/{id}` - A/V sync status

### Key Design Patterns
- **Adapter Pattern**: Unified interface for SRT/RTP protocols
- **Pipeline Pattern**: Packet → Frame → GOP → Output processing
- **Observer Pattern**: Metrics and monitoring throughout
- **Circuit Breaker**: Automatic recovery and reconnection
- **Backpressure**: Flow control at every stage

## Future Considerations
When implementing remaining phases:
- Leverage existing buffer pools and memory management
- Extend GOP buffer for transcoding decisions
- Use hybrid queue for HLS segment storage
- Build on metrics infrastructure for CDN routing
- Consider CUDA integration points in pipeline
