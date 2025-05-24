# Mirror - High-Performance Video Streaming Backend

A Go-based backend for high-performance video streaming, designed to handle 25 concurrent SRT/RTP streams at 50Mbps with HEVC input, transcode to HLS-compatible formats, and broadcast to 5,000 concurrent viewers using Low-Latency HLS.

## Phase 1 - Core Foundation ✅

This phase establishes the foundational architecture with:

- ✅ Professional Go project structure with modular design
- ✅ Configuration management (YAML + environment variables)
- ✅ HTTP/3 server with QUIC transport
- ✅ Health check system with Redis connectivity monitoring
- ✅ Structured logging with rotation
- ✅ Custom error handling framework
- ✅ Prometheus metrics integration
- ✅ Docker development environment
- ✅ CI/CD pipeline with GitHub Actions

## Quick Start

### Prerequisites

- Go 1.21+
- Docker and Docker Compose
- Redis (or use Docker Compose)
- OpenSSL (for certificate generation)

### Setup

1. Clone the repository:
```bash
git clone https://github.com/zsiec/mirror.git
cd mirror
```

2. Run initial setup:
```bash
make setup
```

3. Start Redis (if not using Docker Compose):
```bash
docker run -d -p 6379:6379 redis:alpine
```

4. Run the server:
```bash
make run
```

### Using Docker Compose

```bash
# Start all services
docker-compose -f docker/docker-compose.yml up

# With monitoring stack (Prometheus + Grafana)
docker-compose -f docker/docker-compose.yml --profile monitoring up
```

## Development

### Available Commands

```bash
make help         # Show all available commands
make build        # Build the binary
make test         # Run tests
make lint         # Run linters
make fmt          # Format code
make dev          # Run with hot reload (requires air)
make docker       # Build Docker image
```

### Project Structure

```
mirror/
├── cmd/mirror/         # Application entry point
├── internal/           # Private application code
│   ├── config/        # Configuration management
│   ├── errors/        # Custom error types
│   ├── health/        # Health check system
│   ├── logger/        # Structured logging
│   └── server/        # HTTP/3 server implementation
├── pkg/version/       # Version information
├── configs/           # Configuration files
├── docker/            # Docker configurations
├── certs/            # TLS certificates (git-ignored)
└── scripts/          # Build and utility scripts
```

### API Endpoints

- `GET /health` - Detailed health check with component status
- `GET /ready` - Simple readiness check
- `GET /live` - Liveness probe
- `GET /version` - Version information
- `GET /metrics` - Prometheus metrics (port 9090)

**Note**: The server uses HTTP/3 (QUIC) which requires HTTP/3-capable clients. Standard curl won't work. Use tools like:
- `curl --http3` (if compiled with HTTP/3 support)
- `quiche-client`
- Chrome/Firefox with HTTP/3 enabled

### Configuration

Configuration can be set via:
1. YAML files (`configs/default.yaml`, `configs/development.yaml`)
2. Environment variables (prefix: `MIRROR_`)
3. Command-line flags

Example environment variables:
```bash
MIRROR_SERVER_HTTP3_PORT=8443
MIRROR_REDIS_ADDRESSES=localhost:6379
MIRROR_LOGGING_LEVEL=debug
```

## Testing

```bash
# Run unit tests
make test

# Run tests with coverage
make test-coverage

# Run benchmarks
make bench
```

## Next Phases

- **Phase 2**: Stream Ingestion Layer (SRT/RTP listeners)
- **Phase 3**: GPU-Accelerated Transcoding Pipeline
- **Phase 4**: Low-Latency HLS Packaging
- **Phase 5**: Storage and CDN Integration
- **Phase 6**: Admin Dashboard
- **Phase 7**: Production Deployment

## License

[Add your license here]