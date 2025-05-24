# Docker Configuration

This directory contains Docker-related files for containerizing the Mirror application.

## Files

### Dockerfile
Multi-stage Dockerfile optimized for production deployment:
- **Stage 1**: Build stage using Go 1.23
- **Stage 2**: Runtime stage using NVIDIA CUDA base image

Key features:
- Static binary compilation with CGO disabled
- Minimal runtime image with NVIDIA GPU support
- Non-root user execution
- Health check configuration
- Exposed ports: 8443 (HTTP/3), 9090 (metrics)

### docker-compose.yml
Development environment orchestration including:
- Mirror application service
- Redis for state management
- Prometheus for metrics collection
- Volume mounts for configuration and certificates
- Network configuration

### prometheus.yml
Prometheus configuration for metrics collection:
- Scrape interval: 15s
- Target: mirror:9090
- Job name: mirror

## Usage

### Development
```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f mirror

# Stop all services
docker-compose down

# Rebuild after changes
docker-compose up -d --build
```

### Production Build
```bash
# Build production image
docker build -f docker/Dockerfile -t mirror:latest .

# Run standalone
docker run -d \
  -p 8443:8443 \
  -p 9090:9090 \
  -v $(pwd)/configs:/app/configs:ro \
  -v $(pwd)/certs:/app/certs:ro \
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
```

## Volumes

Important volumes to mount:
- `/app/configs`: Configuration files
- `/app/certs`: TLS certificates
- `/app/logs`: Application logs (if file logging enabled)

## Networking

The docker-compose setup creates a custom network for service communication:
- Network name: `mirror-network`
- Services can communicate using service names (e.g., `redis:6379`)

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