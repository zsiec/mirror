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

### Next Phases
- Phase 2: Stream Ingestion (SRT/RTP)
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
- **State Management**: Redis for session management
- **Metrics**: Prometheus for monitoring
- **Container**: Docker with multi-stage builds

### Service Architecture
```
HTTP/3 Server (QUIC)
    ├── Health Checks (/health, /ready, /live)
    ├── Version Info (/version)
    └── Stream Endpoints (Phase 2+)
        ├── Ingestion Service → 
        ├── Transcoding Service → 
        ├── HLS Packager → 
        └── CDN Distribution
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
│   ├── logger/           # Logging utilities
│   └── server/           # HTTP/3 server implementation
├── pkg/                   # Public packages
│   └── version/          # Version information
├── configs/              # Configuration files
├── docker/               # Docker-related files
├── docs/                 # Documentation
│   └── phase-*.md       # Implementation phase docs
├── .github/              # GitHub Actions workflows
└── tests/                # Integration tests (future)
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
MIRROR_SERVER_HTTP3_PORT=8443
MIRROR_REDIS_ADDR=localhost:6379
MIRROR_LOGGING_LEVEL=debug
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

## Future Considerations
When implementing video streaming phases:
- Use buffer pools for video data
- Implement backpressure for stream ingestion
- Consider memory-mapped files for segments
- Plan for horizontal scaling
- Design for CDN integration from the start