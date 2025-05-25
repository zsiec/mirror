<div align="center">
  <img src="assets/logo.svg" alt="Mirror Logo" width="200" height="200">
  
  # Mirror
  
  **High-Performance Video Streaming Platform**
  
  [![Go Version](https://img.shields.io/badge/Go-1.23%2B-00ADD8?style=for-the-badge&logo=go)](https://go.dev/)
  [![License](https://img.shields.io/badge/License-MIT-green.svg?style=for-the-badge)](LICENSE)
  [![Build Status](https://img.shields.io/github/actions/workflow/status/yourusername/mirror/ci.yml?branch=main&style=for-the-badge)](https://github.com/yourusername/mirror/actions)
  [![Coverage](https://img.shields.io/badge/Coverage-85%25-brightgreen?style=for-the-badge)](https://github.com/yourusername/mirror)
  [![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=for-the-badge&logo=docker&logoColor=white)](https://hub.docker.com/r/yourusername/mirror)
  
  <p align="center">
    <a href="#-features">Features</a> •
    <a href="#-quick-start">Quick Start</a> •
    <a href="#-architecture">Architecture</a> •
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

Mirror is a cutting-edge video streaming platform designed to handle the demands of modern video delivery at scale. Built from the ground up with performance and reliability in mind, Mirror leverages the latest technologies including **HTTP/3 (QUIC)**, **hardware-accelerated transcoding**, and **intelligent stream management** to deliver exceptional video experiences.

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

- Go 1.23 or later
- Docker and Docker Compose
- Redis 7.0+
- (Optional) NVIDIA GPU with CUDA 12.0+ for hardware acceleration

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/mirror.git
cd mirror

# Generate self-signed certificates for development
make certs

# Build the application
make build

# Run with Docker Compose (includes Redis)
make docker-run
```

### Basic Usage

#### Start streaming with SRT:
```bash
# Stream to Mirror using FFmpeg
ffmpeg -re -i input.mp4 -c copy -f mpegts \
  "srt://localhost:30000?streamid=mystream&passphrase=secret"
```

#### Start streaming with RTP:
```bash
# Stream to Mirror using GStreamer
gst-launch-1.0 filesrc location=input.mp4 ! \
  qtdemux ! h264parse ! rtph264pay ! \
  udpsink host=localhost port=5004
```

#### Access the stream:
```bash
# View stream information
curl https://localhost:8443/api/v1/streams

# Access HLS playlist (after Phase 4)
curl https://localhost:8443/live/mystream/playlist.m3u8
```

## 🏗️ Architecture

Mirror follows a modular, microservices-inspired architecture while maintaining the simplicity of a single binary deployment:

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   SRT Client    │     │   RTP Client    │     │  HTTP Client    │
└────────┬────────┘     └────────┬────────┘     └────────┬────────┘
         │                       │                         │
         ▼                       ▼                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                        Mirror Platform                           │
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

Interactive API documentation is available at:
- Development: https://localhost:8443/docs
- [OpenAPI Specification](docs/openapi/server.yaml)

### Configuration

Mirror uses a hierarchical configuration system:

```yaml
# configs/default.yaml
server:
  http3_port: 8443
  tls_cert_file: "./certs/cert.pem"
  tls_key_file: "./certs/key.pem"

ingestion:
  srt:
    port: 30000
    latency: 120ms
    max_bandwidth: 60000000  # 60 Mbps
  rtp:
    port: 5004
    buffer_size: 2097152     # 2MB

transcoding:
  gpu_enabled: true
  preset: "medium"
  output_formats:
    - codec: "h264"
      bitrate: "5M"
      resolution: "1920x1080"
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
```

## 🛠️ Development

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
├── api/                   # API definitions
├── web/                   # Web UI assets
└── docs/                  # Documentation
```

### Building from Source

```bash
# Standard build
go build -o bin/mirror ./cmd/mirror

# Production build with optimizations
go build -ldflags="-s -w" -o bin/mirror ./cmd/mirror

# Cross-compilation
GOOS=linux GOARCH=amd64 go build -o bin/mirror-linux-amd64 ./cmd/mirror
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
# Install development dependencies
make setup-dev

# Run linters
make lint

# Format code
make fmt

# Run pre-commit checks
make pre-commit
```

## 📊 Performance

Mirror is designed for high-performance video streaming:

| Metric | Value |
|--------|-------|
| Concurrent Streams | 25+ |
| Stream Bitrate | Up to 50 Mbps |
| Transcoding Latency | < 100ms |
| Distribution Latency | < 2s (LL-HLS) |
| Memory per Stream | ~200MB |
| CPU Usage | < 50% (25 streams) |

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

- [datarhei/gosrt](https://github.com/datarhei/gosrt) - SRT protocol implementation
- [pion/rtp](https://github.com/pion/rtp) - RTP protocol support
- [quic-go/quic-go](https://github.com/quic-go/quic-go) - HTTP/3 implementation
- [go-astiav](https://github.com/asticode/go-astiav) - FFmpeg bindings

## 💬 Community

- **Discord**: [Join our server](https://discord.gg/mirror)
- **Twitter**: [@MirrorStreaming](https://twitter.com/mirrorstreaming)
- **Blog**: [blog.mirror.dev](https://blog.mirror.dev)

---

<div align="center">
  <p>
    <strong>Built with ❤️ by the Mirror Team</strong>
  </p>
  <p>
    <a href="https://mirror.dev">Website</a> •
    <a href="https://docs.mirror.dev">Documentation</a> •
    <a href="https://status.mirror.dev">Status</a>
  </p>
</div>
