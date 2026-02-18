# Docker Configuration

This directory contains Docker-related files for containerizing the Mirror application.

## Files

### Dockerfile
Multi-stage Dockerfile optimized for production deployment.

### Dockerfile.test
Docker image for running tests in CI.

### docker-compose.yml
Development environment orchestration.

### docker-compose.override.yml
Local overrides (not checked in — create as needed).

### prometheus.yml
Prometheus scrape configuration.

### nginx.conf
Nginx configuration (for reverse proxy/load balancing scenarios).

### srt-test/
SRT test tooling for Docker-based stream testing.

### logs/
Log output directory for containerized runs.

---

### Dockerfile Details
Multi-stage Dockerfile optimized for production deployment:
- **Stage 1**: Build stage using Go 1.23 Alpine with CGO support
- **Stage 2**: Runtime stage using NVIDIA CUDA base image

Key features:
- CGO enabled for native SRT library integration
- Haivision SRT library built from source (v1.5.4)
- Optimized runtime image with NVIDIA GPU support
- Non-root user execution
- Health check configuration
- Exposed ports: 
  - 8443/udp (HTTP/3 API server - QUIC protocol)
  - 8080/tcp (HTTP/1.1 and HTTP/2 fallback)
  - 9090/tcp (Prometheus metrics)
  - 30000/udp (SRT stream ingestion)
  - 5004/udp (RTP stream ingestion)

### docker-compose.yml Details
Development environment orchestration including:
- Mirror application service
- Redis for state management
- Prometheus for metrics collection
- Volume mounts for configuration and certificates
- Network configuration

### prometheus.yml
Prometheus configuration for metrics collection:
- Scrape interval: 15s
- Targets: 
  - mirror:9090 (application metrics)
- Jobs:
  - mirror: Main application metrics including:
    - Stream ingestion metrics (packets, frames, GOPs)
    - Buffer utilization and backpressure
    - Codec detection statistics
    - Memory usage per stream
    - Connection counts and states

## Usage

### Development
```bash
# Start all services with proper port mapping
make docker-compose

# View logs
make docker-compose-logs

# Restart services
make docker-compose-restart

# Stop all services
make docker-compose-down

# Start with monitoring (Prometheus + Grafana)
make docker-compose-monitoring

# Clean up Docker resources
make docker-clean
```

### CGO and SRT Library
The Dockerfile now includes:
- **CGO Support**: Enabled for native library integration
- **SRT Library**: Haivision SRT v1.5.4 built from source
- **Build Dependencies**: GCC, CMake, OpenSSL for compilation
- **Runtime Dependencies**: Shared SRT library in runtime image

The build process:
1. Installs build tools and dependencies in Alpine
2. Compiles SRT library from official Haivision source
3. Builds Go application with CGO enabled
4. Creates minimal runtime image with SRT shared library
5. Configures proper library paths with ldconfig

### Port Mapping
All necessary ports are now mapped in docker-compose.yml:
- `8443:8443/udp` - HTTP/3 (QUIC) API server
- `8080:8080/tcp` - HTTP/1.1 and HTTP/2 fallback
- `9090:9090` - Prometheus metrics endpoint
- `30000:30000/udp` - SRT stream ingestion
- `5004:5004/udp` - RTP stream ingestion
- `6379:6379` - Redis (for development)
- `9091:9090` - Prometheus web UI (with monitoring profile)
- `3000:3000` - Grafana web UI (with monitoring profile)

### Production Build
```bash
# Build production image
make docker

# Run standalone with all ports
make docker-run

# Or manually:
docker run -d \
  -p 8443:8443/udp \
  -p 9090:9090 \
  -p 30000:30000/udp \
  -p 5004:5004/udp \
  -v $(pwd)/configs:/app/configs:ro \
  -v $(pwd)/certs:/app/certs:ro \
  -v /tmp/mirror:/tmp/mirror \
  --name mirror \
  mirror:latest
```

## GPU Support

The runtime image includes NVIDIA CUDA support for future video transcoding:

```bash
# Run with GPU access
docker run --gpus all -d \
  -p 8443:8443 \
  mirror:latest
```

## Environment Variables

Configure the application using environment variables:

```yaml
environment:
  - MIRROR_CONFIG_PATH=/app/configs/development.yaml
  - MIRROR_SERVER_HTTP3_PORT=8443
  - MIRROR_REDIS_ADDR=redis:6379
  - MIRROR_LOGGING_LEVEL=debug
  - MIRROR_INGESTION_SRT_PORT=30000
  - MIRROR_INGESTION_RTP_PORT=5004
  - MIRROR_INGESTION_MAX_CONNECTIONS=25
  - MIRROR_INGESTION_QUEUE_DISK_PATH=/tmp/mirror
```

## Volumes

Important volumes to mount:
- `/app/configs`: Configuration files
- `/app/certs`: TLS certificates
- `/app/logs`: Application logs (if file logging enabled)
- `/tmp/mirror`: Queue disk overflow storage (Phase 2)

## Networking

The docker-compose setup creates a custom network for service communication:
- Network name: `mirror-network`
- Services can communicate using service names (e.g., `redis:6379`)
- All required ports are exposed to the host for external access

## Customization

Use `docker-compose.override.yml` for local customizations:
```bash
# Create an override file (not checked in)
# Edit docker/docker-compose.override.yml as needed, then run normally
make docker-compose
```

The override file allows you to:
- Enable GPU support
- Customize environment variables
- Add additional volume mounts
- Expose additional ports
- Enable monitoring services by default

## Health Checks

The Dockerfile includes a health check configuration:
```dockerfile
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/app/mirror", "-health-check"]
```

## Security Considerations

- Run as non-root user (uid: 1000)
- Read-only root filesystem recommended
- Mount configs and certs as read-only
- Use secrets for sensitive environment variables
- Limit container capabilities

## Future Enhancements

As the project evolves, consider:
- Separate containers for different services
- Kubernetes deployment manifests
- Helm charts for cloud deployment
- Init containers for setup tasks
- Sidecar containers for logging/monitoring
