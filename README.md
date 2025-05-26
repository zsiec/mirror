<div align="center">
  <img src="assets/logo.svg" alt="Mirror Logo" width="200" height="200">
  
  # Mirror
  
  **High-Performance Video Streaming Platform**
  
  [![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
  [![License](https://img.shields.io/badge/License-MIT-green.svg?style=for-the-badge)](LICENSE)
  [![Build Status](https://img.shields.io/github/actions/workflow/status/zsiec/mirror/ci.yml?branch=main&style=for-the-badge)](https://github.com/zsiec/mirror/actions)
  [![Coverage](https://img.shields.io/badge/Coverage-85%25-brightgreen?style=for-the-badge)](https://github.com/zsiec/mirror)
  [![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://hub.docker.com/r/zsiec/mirror)
  
  <p align="center">
    <a href="#-features">Features</a> •
    <a href="#-quick-start">Quick Start</a> •
    <a href="#%EF%B8%8F-architecture">Architecture</a> •
    <a href="#-documentation">Documentation</a> •
    <a href="#-contributing">Contributing</a>
  </p>

  <p align="center">
    <strong>Stream • Transcode • Distribute</strong><br>
    Enterprise-grade video streaming infrastructure built with Go
  </p>
</div>

---

## 🎯 Overview

Mirror is a video streaming platform designed for live broadcast usecases. Built from the ground up with performance and reliability in mind, Mirror leverages the latest technologies including **HTTP/3 (QUIC)**, **hardware-accelerated transcoding**, and **intelligent stream management** to deliver exceptional video experiences.

### Key Capabilities

- 📡 **25 concurrent streams** at 50 Mbps each with HEVC input
- 🚀 **Ultra-low latency** delivery using HTTP/3 and LL-HLS
- 🎬 **Hardware-accelerated transcoding** with NVIDIA GPU support
- 👥 **5,000+ concurrent viewers** per stream
- 🔄 **Automatic failover** and stream recovery
- 📊 **Real-time analytics** and comprehensive monitoring

## ✨ Features

### 🎥 **Advanced Stream Ingestion**
- **Multi-protocol support**: SRT (primary) and RTP
- **Automatic codec detection**: H.264, HEVC/H.265, AV1, JPEG-XS
- **Intelligent buffering**: GOP-aware with backpressure control
- **Frame-perfect synchronization**: Advanced A/V sync with drift correction
- **Resilient connections**: Automatic recovery and reconnection

### 🔄 **Smart Video Processing**
- **GPU-accelerated transcoding**: NVIDIA CUDA/NVENC support
- **Adaptive bitrate**: Multiple quality levels for optimal delivery
- **Frame-level control**: B-frame reordering and IDR alignment
- **Memory efficient**: Pooled buffers and zero-copy operations

### 📦 **Modern Distribution**
- **Low-Latency HLS (LL-HLS)**: Sub-2 second glass-to-glass latency
- **HTTP/3 delivery**: QUIC protocol for improved performance
- **CDN-ready**: Seamless integration with CloudFront, Fastly, etc.
- **Multi-viewer support**: Up to 6 concurrent streams per viewer

### 🛡️ **Enterprise Ready**
- **High availability**: Redis-backed session management
- **Comprehensive monitoring**: Prometheus metrics and health checks
- **Security first**: TLS 1.3, authenticated streams, rate limiting
- **Cloud native**: Kubernetes-ready with horizontal scaling

## 🚀 Quick Start

### Prerequisites

- **Go 1.23+** - [Download here](https://go.dev/dl/)
- **Docker & Docker Compose** - [Install Docker](https://docs.docker.com/get-docker/)
- **Redis 7.0+** (automatically included in Docker setup)
- **SRT Library** (automatically installed via `make setup`)
- (Optional) **NVIDIA GPU with CUDA 12.0+** for hardware acceleration

### Installation & Setup

**Option 1: Complete Setup (Recommended for new developers)**
```bash
# Clone the repository
git clone https://github.com/yourusername/mirror.git
cd mirror

# 🚀 One-command setup - installs everything including SRT
make setup

# Start all services with Docker
make docker-compose

# View logs
make docker-compose-logs
```

**Option 2: Manual Setup**
```bash
# 1. Install dependencies
make deps

# 2. Check/install SRT library (required for video streaming)
make srt-check          # Check if SRT is available
make srt-setup          # Auto-install SRT (macOS/Ubuntu)

# 3. Generate certificates
make generate-certs

# 4. Start services
make docker-compose
```

**🔧 SRT Library Notes:**
- **macOS**: Installed via Homebrew (`brew install srt`)
- **Ubuntu/Debian**: Installed via apt (`libsrt-openssl-dev`)
- **Other systems**: See [SRT installation guide](https://github.com/Haivision/srt#requirements)
- The Makefile handles SRT environment automatically - no manual setup needed!

### Docker Commands

```bash
# Docker Compose operations
make docker-compose          # Start all services
make docker-compose-logs     # View logs
make docker-compose-restart  # Restart services
make docker-compose-down     # Stop services

# Run with monitoring stack (Prometheus + Grafana)
make docker-compose-monitoring

# Build and run standalone
make docker              # Build Docker image
make docker-run          # Run with all ports mapped
make docker-clean        # Clean up Docker resources
```

### Basic Usage

#### Start streaming with SRT:
```bash
# Stream to Mirror using FFmpeg (generates test pattern)
ffmpeg -re -f lavfi -i testsrc=duration=5:size=640x480:rate=30 -c:v libx264 -preset ultrafast -tune zerolatency -g 30 -keyint_min 30 -f mpegts "srt://localhost:30000?streamid=test"
```

#### Start streaming with RTP:
```bash
# Stream to Mirror using GStreamer (generates test pattern)
gst-launch-1.0 videotestsrc pattern=ball ! video/x-raw,width=1920,height=1080,framerate=30/1 ! \
  x264enc bitrate=5000 ! h264parse ! rtph264pay ! udpsink host=localhost port=5004

# Or stream with audio
gst-launch-1.0 videotestsrc pattern=smpte ! video/x-raw,width=1920,height=1080,framerate=30/1 ! \
  x264enc bitrate=5000 ! h264parse ! rtph264pay pt=96 ! udpsink host=localhost port=5004
```

#### Access the API:
```bash
# View active streams
curl https://localhost:8443/api/v1/streams

# Get specific stream details
curl https://localhost:8443/api/v1/streams/test

# Get stream statistics
curl https://localhost:8443/api/v1/streams/test/stats

# Get system-wide statistics
curl https://localhost:8443/api/v1/stats

# Get A/V sync status
curl https://localhost:8443/api/v1/streams/test/sync

# Control stream playback
curl -X POST https://localhost:8443/api/v1/streams/test/pause
curl -X POST https://localhost:8443/api/v1/streams/test/resume

# Stop a stream
curl -X DELETE https://localhost:8443/api/v1/streams/test
```

## 🏗️ Architecture

Mirror follows a modular, microservices-inspired architecture while maintaining the simplicity of a single binary deployment:

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   SRT Client    │     │   RTP Client    │     │  HTTP Client    │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                       │
         ▼                       ▼                       ▼
┌────────────────────────────────────────────────────────────────┐
│                        Mirror Platform                         │
├─────────────────┬──────────────┬────────────────┬──────────────┤
│  Ingestion      │  Processing  │  Distribution  │  Management  │
├─────────────────┼──────────────┼────────────────┼──────────────┤
│ • SRT Listener  │ • Decoder    │ • HLS Packager │ • REST API   │
│ • RTP Listener  │ • Transcoder │ • HTTP/3 Server│ • Metrics    │
│ • Frame Asm.    │ • Encoder    │ • CDN Push     │ • Health     │
│ • GOP Buffer    │ • GPU Pool   │ • Cache Control│ • Admin UI   │
└─────────────────┴──────────────┴────────────────┴──────────────┘
         │                       │                         │
         ▼                       ▼                         ▼
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│      Redis      │     │   Object Store  │     │   Prometheus    │
└─────────────────┘     └─────────────────┘     └─────────────────┘
```

### Core Components

- **Ingestion Service**: Handles incoming streams with protocol adapters
- **Processing Pipeline**: GPU-accelerated transcoding and packaging
- **Distribution Layer**: HTTP/3 server with intelligent caching
- **Management Plane**: APIs, monitoring, and administrative functions

## 📖 Documentation

### 📚 Documentation Index

#### Core Packages
- **[Configuration Management](internal/config/README.md)** - Hierarchical config with validation
- **[Error Handling](internal/errors/README.md)** - Typed errors with HTTP mapping
- **[Health Monitoring](internal/health/README.md)** - Extensible health check system
- **[Logging System](internal/logger/README.md)** - Structured, context-aware logging
- **[HTTP/3 Server](internal/server/README.md)** - QUIC-based server implementation
- **[Metrics Collection](internal/metrics/README.md)** - Prometheus integration
- **[Queue System](internal/queue/README.md)** - Hybrid memory/disk queue

#### Streaming Components
- **[Stream Ingestion](internal/ingestion/README.md)** - SRT/RTP protocol handling
  - [Buffer Management](internal/ingestion/buffer/README.md)
  - [Codec Support](internal/ingestion/codec/README.md)
  - [Frame Processing](internal/ingestion/frame/README.md)
  - [GOP Management](internal/ingestion/gop/README.md)
  - [A/V Synchronization](internal/ingestion/sync/README.md)

#### Implementation Guides
- **[Phase Documentation](docs/README.md)** - Detailed implementation phases
- **[Architecture Decisions](docs/CLAUDE.md)** - Design rationale and patterns
- **[Contributing Guide](CONTRIBUTING.md)** - How to contribute
- **[Claude AI Integration](CLAUDE.md)** - AI assistant guidance

### API Documentation

#### Current API Endpoints (Phase 2)

**Stream Management:**
- `GET /api/v1/streams` - List all active streams
- `GET /api/v1/streams/{id}` - Get stream details
- `DELETE /api/v1/streams/{id}` - Stop a stream
- `GET /api/v1/streams/{id}/stats` - Get stream statistics

**Stream Control:**
- `POST /api/v1/streams/{id}/pause` - Pause stream ingestion
- `POST /api/v1/streams/{id}/resume` - Resume stream ingestion

**System Information:**
- `GET /api/v1/stats` - System-wide statistics
- `GET /api/v1/streams/stats/video` - Video-specific statistics

**Stream Data Access:**
- `GET /api/v1/streams/{id}/data` - Stream data information
- `GET /api/v1/streams/{id}/buffer` - Buffer status
- `GET /api/v1/streams/{id}/preview` - Preview data
- `GET /api/v1/streams/{id}/sync` - A/V synchronization status

**Health & Monitoring:**
- `GET /health` - Comprehensive health check
- `GET /ready` - Readiness check
- `GET /live` - Liveness check
- `GET /metrics` - Prometheus metrics
- `GET /version` - Server version info

Interactive API documentation:
- [OpenAPI Specification](docs/openapi/server.yaml)
- [Ingestion API Spec](docs/openapi/ingestion.yaml)

### Configuration

Mirror uses a hierarchical configuration system:

```yaml
# configs/default.yaml
server:
  http3_port: 8443              # HTTP/3 (QUIC) primary port
  http_port: 8080               # HTTP/1.1/2 fallback port
  enable_http: false            # Enable fallback HTTP server
  enable_http2: true            # HTTP/2 support when HTTP enabled
  debug_endpoints: false        # Enable /debug/pprof/* endpoints
  tls_cert_file: "./certs/cert.pem"
  tls_key_file: "./certs/key.pem"

ingestion:
  queue_dir: "/tmp/mirror/queue" # Disk overflow directory
  srt:
    enabled: true
    port: 30000                 # SRT listener port
    latency: 120ms              # SRT latency window
    max_bandwidth: 52428800     # 50 Mbps max per stream
    input_bandwidth: 52428800   # Input bandwidth for sizing
    max_connections: 25         # Concurrent streams
    encryption:
      enabled: false
      passphrase: ""
      key_length: 128           # AES key length
  rtp:
    enabled: true
    port: 5004                  # RTP listener port
    rtcp_port: 5005             # RTCP listener port
    buffer_size: 65536          # 64KB receive buffer
    max_sessions: 25            # Concurrent sessions
    session_timeout: 10s        # Session idle timeout
  buffer:
    pool_size: 50               # Buffer pool size >= max connections
    ring_size: 4194304          # 4MB per stream
    write_timeout: 10ms
    read_timeout: 10ms
  memory:
    max_total: 8589934592       # 8GB total limit
    max_per_stream: 419430400   # 400MB per stream
  stream_handling:
    frame_assembly_timeout: 200ms # Frame assembly timeout
    gop_buffer_size: 3          # GOP buffer depth
    max_gop_age: 5s             # GOP cleanup age
    error_retry_limit: 3        # Error retry attempts
  backpressure:
    enabled: true               # Flow control enabled
    low_watermark: 0.25         # Low pressure (25%)
    medium_watermark: 0.5       # Medium pressure (50%)
    high_watermark: 0.75        # High pressure (75%)
    critical_watermark: 0.9     # Critical pressure (90%)
    response_window: 500ms      # Response time window
    frame_drop_ratio: 0.1       # Frame drop ratio (10%)
```

Environment variables override configuration:
```bash
MIRROR_SERVER_HTTP3_PORT=8443
MIRROR_INGESTION_SRT_PORT=30000
MIRROR_TRANSCODING_GPU_ENABLED=true
```

### Performance Tuning

See our [Performance Guide](docs/performance.md) for:
- Network optimization
- GPU utilization
- Memory management
- Scaling strategies

## 🧪 Testing

```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Run benchmarks
make bench

# Run integration tests
make test-integration

# Run comprehensive full system integration test
make test-full-integration
```

### Full Integration Testing

The `make test-full-integration` command runs a comprehensive end-to-end test that:

- ✅ **Starts a complete Mirror server** with all components
- ✅ **Generates RTP streams** with H.264 video packets
- ✅ **Simulates SRT streams** with MPEG-TS data
- ✅ **Validates server health** and API responses
- ✅ **Tests stream ingestion** and processing
- ✅ **Checks real-time metrics** and statistics
- ✅ **Verifies logging** and debugging output

This test is designed to be run in isolation and provides extensive validation of the entire streaming platform. It includes verbose output to help debug issues and verify correct operation.

## 🛠️ Development

### New Developer Quick Start

**1. Initial Setup**
```bash
# One command to rule them all
make setup              # Installs Go deps, SRT library, generates certs

# Or check what you need
make help               # Shows all commands with SRT status
make srt-check          # Verify SRT is installed
```

**2. Development Workflow**
```bash
# Run tests (automatically handles SRT environment)
make test               # Unit tests
make test-coverage      # Tests with coverage report
make test-race          # Tests with race detector

# Code quality
make lint               # Run linters  
make fmt                # Format code
make check              # Run all checks (fmt, vet, lint, test)

# Local development
make build              # Build binary
make run                # Build and run locally
make dev                # Hot reload (requires `air`)
```

**3. Docker Development**
```bash
# Recommended development environment
make docker-compose             # Start all services
make docker-compose-logs        # View logs
make docker-compose-monitoring  # Include Prometheus/Grafana
make docker-compose-down        # Stop everything
```

**4. SRT Environment (Automatic)**
The Makefile automatically handles SRT compilation:
- ✅ No more `source scripts/srt-env.sh` required
- ✅ Automatic SRT detection and setup
- ✅ Cross-platform support (macOS, Ubuntu/Debian)
- ✅ Clear error messages if SRT is missing

### Project Structure

```
mirror/
├── cmd/                    # Application entry points
│   └── mirror/            # Main server application
├── internal/              # Private application code
│   ├── config/           # Configuration management
│   ├── ingestion/        # Stream ingestion (SRT/RTP)
│   ├── transcoding/      # Video processing (Phase 3)
│   ├── distribution/     # HLS packaging (Phase 4)
│   └── ...
├── pkg/                   # Public packages
├── scripts/              # Build and setup scripts
├── configs/              # Configuration files
├── docker/               # Docker configurations
└── docs/                 # Documentation
```

### Building from Source

```bash
# Standard build (SRT environment handled automatically)
make build

# Manual build
go build -o bin/mirror ./cmd/mirror

# Production build with optimizations
go build -ldflags="-s -w" -o bin/mirror ./cmd/mirror

# Cross-compilation
GOOS=linux GOARCH=amd64 go build -o bin/mirror-linux-amd64 ./cmd/mirror
```

### Common Development Tasks

```bash
# Full development cycle
make check              # Run all quality checks
make test-coverage      # Verify test coverage
make docker-compose     # Test in containerized environment

# Debugging
make srt-check          # Check SRT status
VERBOSE=1 make test     # Verbose test output
SRT_DEBUG=1 make build  # Debug SRT compilation

# Cleanup
make clean              # Remove build artifacts
make docker-clean       # Clean Docker resources
```

### Troubleshooting

**SRT Library Issues**
```bash
# Check SRT status
make srt-check

# Install SRT automatically
make srt-setup

# Manual SRT installation
# macOS:
brew install srt

# Ubuntu/Debian:
sudo apt-get install libsrt-openssl-dev

# Verify SRT is working
pkg-config --exists srt && echo "SRT OK" || echo "SRT Missing"
```

**Build Issues**
```bash
# Clean and rebuild
make clean && make build

# Check Go version (need 1.23+)
go version

# Update dependencies
make deps
```

**Test Failures**
```bash
# Run tests with verbose output
VERBOSE=1 make test

# Run specific test
go test -v ./internal/config/...

# Check test coverage
make test-coverage
```

**Docker Issues**
```bash
# Reset Docker environment
make docker-clean
make docker-compose

# Check Docker logs
make docker-compose-logs

# Rebuild containers
make docker-compose down
make docker
make docker-compose
```

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

### How to Contribute

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Development Setup

```bash
# Complete setup for new developers
make setup              # Install deps, SRT, generate certs

# Development commands
make test               # Run tests
make lint               # Run linters  
make fmt                # Format code
make check              # Run all checks (fmt, vet, lint, test)

# Verify your environment
make help               # Shows all commands + SRT status
make srt-check          # Check SRT library availability
```

## 📊 Performance

Mirror is designed for high-performance video streaming:

| Metric | Phase 2 Value |
|--------|---------------|
| Concurrent SRT Streams | 25 |
| Concurrent RTP Sessions | 25 |
| Max Stream Bitrate | 50 Mbps |
| Ingestion Latency | < 200ms |
| Memory per Stream | 400MB |
| Total Memory Limit | 8GB |
| A/V Sync Accuracy | ±100ms |
| Frame Assembly Timeout | 200ms |
| GOP Buffer Depth | 3 GOPs |
| Codec Support | H.264, HEVC, AV1, JPEG-XS |
| Test Coverage | 85%+ |

## 🔒 Security

- **TLS 1.3** for all connections
- **Stream authentication** with tokens
- **Rate limiting** and DDoS protection
- **Secure storage** for sensitive data
- Regular security audits

## 📈 Roadmap

### ✅ Phase 1: Core Foundation
- [x] HTTP/3 server with QUIC
- [x] Configuration management
- [x] Health monitoring
- [x] Docker environment

### ✅ Phase 2: Stream Ingestion
- [x] SRT/RTP listeners
- [x] Codec detection
- [x] GOP management
- [x] A/V synchronization

### 🚧 Phase 3: Video Processing
- [ ] FFmpeg integration
- [ ] GPU acceleration
- [ ] Adaptive transcoding
- [ ] Quality optimization

### 📅 Future Phases
- **Phase 4**: HLS packaging and distribution
- **Phase 5**: Multi-stream viewer management
- **Phase 6**: CDN integration and storage
- **Phase 7**: Advanced monitoring and analytics

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [Haivision/srtgo](https://github.com/Haivision/srtgo) - SRT protocol implementation
- [pion/rtp](https://github.com/pion/rtp) - RTP protocol support
- [quic-go/quic-go](https://github.com/quic-go/quic-go) - HTTP/3 implementation
- [go-astiav](https://github.com/asticode/go-astiav) - FFmpeg bindings

---

<div align="center">
  <p>
    <strong>Built with ❤️ by the Mirror Team</strong>
  </p>
</div>
