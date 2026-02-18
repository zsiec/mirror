# Command Line Applications

This directory contains the executable entry points for the Mirror platform.

## Applications

### mirror/
The main server application that runs the Mirror streaming platform.

Key features:
- HTTP/3 server with QUIC protocol
- Health check endpoints
- Prometheus metrics server
- Stream ingestion (SRT/RTP) - Phase 2
- Stream management API
- Graceful shutdown handling
- Signal handling (SIGINT, SIGTERM)

Usage:
```bash
# Run with default config (port 443)
./mirror

# Run with development config (port 8443)
./mirror -config configs/development.yaml

# Run with environment overrides
MIRROR_SERVER_HTTP3_PORT=8443 ./mirror
```

### test-client/
HTTP/3 test client for verifying server endpoints.

Features:
- HTTP/3 client with QUIC support
- TLS configuration (accepts self-signed certs in dev)
- Response body and header display
- JSON response pretty-printing

Usage (port depends on config — 443 default, 8443 development):
```bash
# Test health endpoint
./test-client -url https://localhost:8443/health

# Test version endpoint
./test-client -url https://localhost:8443/version

# Test stream API endpoints
./test-client -url https://localhost:8443/api/v1/streams
./test-client -url https://localhost:8443/api/v1/stats

# Test metrics endpoint (HTTP, separate port)
curl http://localhost:9090/metrics
```

## Building

Both applications are built via the Makefile (which handles SRT environment setup):

```bash
# Build main application (requires SRT library)
make build

# Build with version information (automatic via Makefile)
# Version, git commit, and build time are injected via ldflags

# Manual build (requires SRT env — use `source scripts/srt-env.sh` first)
go build -o bin/mirror cmd/mirror/main.go
go build -o bin/test-client cmd/test-client/main.go
```

## Docker

The mirror application is containerized with multi-stage builds:

```bash
# Build Docker image
docker build -f docker/Dockerfile -t mirror:latest .

# Run with Docker Compose
docker-compose up -d
```

## Stream Testing

To test stream ingestion (ports depend on config — shown with development.yaml):

```bash
# Send test stream via SRT (port 30000 in development, 1234 in default)
ffmpeg -re -i test_video.mp4 -c copy -f mpegts "srt://localhost:30000?streamid=test-stream"

# Send test stream via RTP (port 15004 in development, 5004 in default)
ffmpeg -re -i test_video.mp4 -c copy -f rtp rtp://localhost:15004

# Check active streams
./test-client -url https://localhost:8443/api/v1/streams

# Run full integration test with Rich dashboard
make test-full-integration
```

## Future Applications

As the project evolves, additional command-line tools may be added:
- `transcode-worker`: Standalone transcoding worker (Phase 3)
- `hls-packager`: HLS packaging service (Phase 4)
- `mirror-cli`: Administrative CLI tool
- `stream-simulator`: Load testing tool for multiple streams

## Development Tips

1. Use `make dev` for hot-reload during development
2. Always handle graceful shutdown properly
3. Use structured logging from the start
4. Implement health checks for all services
5. Add version information to all binaries

## Related Documentation

- [Main README](../README.md) - Project overview
- [Server Package](../internal/server/README.md) - HTTP/3 server implementation
- [Configuration Guide](../internal/config/README.md) - Configuration management
- [Health Checks](../internal/health/README.md) - Health check implementation
- [Logging](../internal/logger/README.md) - Structured logging
