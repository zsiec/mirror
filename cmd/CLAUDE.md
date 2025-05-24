# Command Line Applications

This directory contains the executable entry points for the Mirror platform.

## Applications

### mirror/
The main server application that runs the Mirror streaming platform.

Key features:
- HTTP/3 server with QUIC protocol
- Health check endpoints
- Prometheus metrics server
- Graceful shutdown handling
- Signal handling (SIGINT, SIGTERM)

Usage:
```bash
# Run with default config
./mirror

# Run with custom config
./mirror -config configs/production.yaml

# Run with environment overrides
MIRROR_SERVER_HTTP3_PORT=8443 ./mirror
```

### test-client/
HTTP/3 test client for verifying server endpoints.

Features:
- HTTP/3 client with QUIC support
- TLS configuration (accepts self-signed certs in dev)
- Response body and header display

Usage:
```bash
# Test health endpoint
./test-client -url https://localhost:8443/health

# Test any HTTP/3 endpoint
./test-client -url https://localhost:8443/version
```

## Building

Both applications are built as static binaries for easy deployment:

```bash
# Build both applications
make build

# Build specific application
go build -o bin/mirror cmd/mirror/main.go
go build -o bin/test-client cmd/test-client/main.go

# Build with version information
go build -ldflags "-X github.com/zsiec/mirror/pkg/version.Version=1.0.0" -o bin/mirror cmd/mirror/main.go
```

## Docker

The mirror application is containerized with multi-stage builds:

```bash
# Build Docker image
docker build -f docker/Dockerfile -t mirror:latest .

# Run with Docker Compose
docker-compose up -d
```

## Future Applications

As the project evolves, additional command-line tools may be added:
- `stream-ingest`: Dedicated stream ingestion service
- `transcode-worker`: Standalone transcoding worker
- `hls-packager`: HLS packaging service
- `mirror-cli`: Administrative CLI tool

## Development Tips

1. Use `make dev` for hot-reload during development
2. Always handle graceful shutdown properly
3. Use structured logging from the start
4. Implement health checks for all services
5. Add version information to all binaries