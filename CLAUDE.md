# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mirror is a high-performance video streaming platform built in Go, designed to handle:
- 25 concurrent SRT/RTP streams at 50mbps with HEVC input
- Transcoding to HLS-compatible formats with hardware acceleration
- Broadcasting to 5,000 concurrent viewers using Low-Latency HLS (LL-HLS)
- Multi-stream viewing with up to 6 concurrent streams per viewer

## Current Status

### Phase 1 - COMPLETED
- Go project structure with modular architecture
- Configuration management (Viper-based YAML + env)
- Structured logging with rotation (logrus)
- Custom error handling framework
- Health check system (Redis, disk, memory)
- HTTP/3 server with QUIC protocol
- Docker environment with NVIDIA CUDA support
- GitHub Actions CI/CD

### Phase 2 - COMPLETED
- Stream ingestion with SRT and RTP protocols (pure Go SRT via adapter pattern)
- Video-aware buffering and GOP management
- Automatic codec detection (H.264, HEVC, AV1, JPEGXS)
- Frame assembly and validation with optimized B-frame reordering
- Video resolution detection from bitstream analysis
- A/V synchronization with enhanced timebase conversion and drift correction
- Intelligent backpressure control and memory management (8GB/400MB limits)
- Stream recovery and reconnection with circuit breakers
- Comprehensive metrics and monitoring with sampled logging
- Redis-based stream registry with race-condition-safe API
- Parameter set extraction and video bitstream processing
- MPEG-TS parsing and PES extraction for SRT streams
- Stream integrity validation (continuity counters, PTS/DTS, alignment)

### Phase 3 - IN PROGRESS
- FFmpeg integration for video decoding
- Hardware acceleration support (VideoToolbox on macOS, NVIDIA on Linux)
- Transcoding pipeline scaffolding (`internal/transcoding/`)
- Caption extraction support
- GPU resource management

### Remaining Phases
- Phase 4: HLS Packaging & Distribution
- Phase 5: Multi-stream Management
- Phase 6: CDN Integration
- Phase 7: Monitoring & Observability

## Build & Test Commands

SRT is implemented in pure Go — no C library or CGo flags needed. Only FFmpeg is required as an external dependency.

```bash
# Initial setup (installs FFmpeg, generates certs)
make setup

# Build
make build

# Run tests
make test

# Run tests with race detector
make test-race

# Run full integration test with Rich dashboard
make test-full-integration

# Run tests with coverage report
make test-coverage

# Run benchmarks
make bench

# Format and lint
make fmt
make lint
make vet

# Run all checks (fmt, vet, lint, test)
make check

# Docker
make docker              # Build image
make docker-compose      # Start all services
make docker-compose-logs # View logs
make docker-compose-down # Stop services

# Short aliases
make b   # docker build
make r   # docker-compose up
make l   # docker-compose logs
```

### SRT

SRT support uses a pure Go implementation (`github.com/zsiec/srtgo`) — no `libsrt` C library or CGo flags are needed. Just `go test ./...` works directly.

## URLs and Access

- HTTP/3 (QUIC, default): `https://localhost:443` (default.yaml) or `https://localhost:8443` (development.yaml)
- HTTP/1.1+HTTP/2 fallback: `http://localhost:8080` (when `enable_http: true`)
- Prometheus metrics: `http://localhost:9090/metrics`
- SRT ingestion: `srt://localhost:1234` (default) or `srt://localhost:30000` (development)
- RTP ingestion: `rtp://localhost:5004` (default) or `rtp://localhost:15004` (development)

## Project Structure

```
mirror/
├── cmd/                    # Executable entry points
│   ├── mirror/             # Main server binary
│   └── test-client/        # HTTP/3 test client
├── configs/                # YAML configuration files
│   ├── default.yaml        # Base configuration (port 443)
│   ├── development.yaml    # Dev overrides (port 8443)
│   ├── docker.yaml         # Docker-specific config
│   └── test.yaml           # Test configuration
├── docker/                 # Docker and compose files
├── docs/                   # Phase docs, architecture, OpenAPI specs
│   └── openapi/            # OpenAPI/Swagger specs
├── internal/               # Private application packages
│   ├── config/             # Viper-based configuration
│   ├── errors/             # Custom error types with HTTP mapping
│   ├── health/             # Health check system (Redis, disk, memory)
│   ├── ingestion/          # Stream ingestion system (SRT/RTP)
│   ├── logger/             # Structured logging (logrus)
│   ├── metrics/            # Prometheus metrics
│   ├── queue/              # Hybrid memory/disk queue
│   ├── server/             # HTTP/3 server (quic-go)
│   └── transcoding/        # Video transcoding (Phase 3, in progress)
│       ├── caption/        # Caption extraction
│       ├── ffmpeg/         # FFmpeg integration
│       ├── gpu/            # GPU resource management
│       └── pipeline/       # Transcoding pipeline
├── pkg/                    # Public packages
│   └── version/            # Version information
├── scripts/                # Helper scripts (srt-env.sh)
├── tests/                  # Integration tests and dashboard
└── web/                    # Web assets
```

## Key Architecture Patterns

- **Adapter pattern**: SRT (pure Go) and RTP connections implement a common `ConnectionAdapter` interface
- **Pipeline pattern**: Packets flow through: Protocol → Buffer → Frame Assembly → GOP → Output Queue
- **Backpressure**: Watermark-based (25/50/75/90%) with GOP-aware frame dropping
- **Memory management**: Global 8GB limit, 400MB per-stream limit, with eviction
- **Metrics**: Aggregate Prometheus metrics only (no per-stream labels to avoid cardinality bombs); use structured logging for per-stream diagnostics
- **Configuration**: Hierarchical YAML with env var overrides (`MIRROR_*` prefix)

## Testing Guidelines

- Target 80%+ test coverage per package
- Use table-driven tests
- No external SRT library needed (pure Go implementation)
- Integration tests in `tests/` directory, run with `make test-full-integration`
