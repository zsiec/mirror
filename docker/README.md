# Docker Development Environment

This directory contains Docker configuration for running Mirror in development and production environments.

## Quick Start

```bash
# Start all services with development settings
make docker-compose

# View logs
make docker-compose-logs

# Access services
# - API: https://localhost:8443
# - Metrics: http://localhost:9090/metrics
# - Prometheus: http://localhost:9091
# - Grafana: http://localhost:3000 (admin/admin)
```

## Exposed Ports

### Application Ports
| Port | Protocol | Service | Description |
|------|----------|---------|-------------|
| 8443 | UDP | HTTP/3 API | Main API server (QUIC protocol) |
| 9090 | TCP | Metrics | Prometheus metrics endpoint |
| 6060 | TCP | pprof | Go profiling endpoint (dev only) |
| 8080 | TCP | Debug | Debug HTTP server (dev only) |
| 2345 | TCP | Delve | Go debugger port (dev only) |

### Streaming Ports
| Port | Protocol | Service | Description |
|------|----------|---------|-------------|
| 30000 | UDP | SRT Primary | Main SRT ingestion port |
| 30001-30004 | UDP | SRT Secondary | Additional SRT ports for testing |
| 5004 | UDP | RTP Primary | Main RTP ingestion port |
| 5005-5006 | UDP | RTP Secondary | Additional RTP ports |
| 5005,5007 | UDP | RTCP | RTP control protocol ports |

### Infrastructure Ports
| Port | Protocol | Service | Description |
|------|----------|---------|-------------|
| 6379 | TCP | Redis | Redis database |
| 9091 | TCP | Prometheus | Prometheus web UI |
| 3000 | TCP | Grafana | Grafana dashboards |

## Development Features

### docker-compose.override.yml

The override file automatically applies development settings:

1. **Enhanced Logging**
   - Debug level logging for all services
   - Text format for better readability
   - Redis debug logs enabled

2. **Increased Limits**
   - 50 concurrent connections (vs 25 in production)
   - 8GB global memory limit
   - Shorter timeouts for faster testing

3. **Additional Ports**
   - Multiple SRT/RTP ports for concurrent stream testing
   - Debug and profiling endpoints
   - RTCP ports for RTP control

4. **Development Tools**
   - Prometheus runs by default (no --profile needed)
   - Grafana with pre-configured dashboards
   - Source code mounted for debugging

## Testing Multiple Streams

```bash
# Stream 1 - Primary SRT port
ffmpeg -re -i video1.mp4 -c copy -f mpegts \
  "srt://localhost:30000?streamid=stream1"

# Stream 2 - Secondary SRT port
ffmpeg -re -i video2.mp4 -c copy -f mpegts \
  "srt://localhost:30001?streamid=stream2"

# Stream 3 - RTP
ffmpeg -re -i video3.mp4 -c copy -f rtp \
  "rtp://localhost:5004"
```

## Debugging

### Using pprof
```bash
# CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile

# Memory profile
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine dump
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

### Using Delve
```bash
# Connect to running container
dlv connect localhost:2345
```

### Viewing Logs
```bash
# All services
make docker-compose-logs

# Specific service
docker-compose logs -f mirror

# With timestamps
docker-compose logs -f -t mirror
```

## Monitoring

### Prometheus (http://localhost:9091)
- Stream ingestion metrics
- Buffer utilization
- Memory usage per stream
- Connection statistics

### Grafana (http://localhost:3000)
- Pre-configured dashboards
- Real-time stream monitoring
- System resource usage
- Custom alerts

## Production Deployment

For production, use the base docker-compose.yml without the override:

```bash
# Use specific compose file
docker-compose -f docker/docker-compose.yml up -d

# Or remove override file
rm docker/docker-compose.override.yml
make docker-compose
```

## Customization

### Enable GPU Support
Uncomment the GPU section in docker-compose.override.yml:
```yaml
deploy:
  resources:
    reservations:
      devices:
        - driver: nvidia
          count: 1
          capabilities: [gpu]
```

### Add Optional Services
Uncomment services in docker-compose.override.yml:
- Jaeger: Distributed tracing
- Redis Commander: Redis GUI

### Custom Environment Variables
Add to the mirror service environment section:
```yaml
environment:
  - MIRROR_CUSTOM_SETTING=value
```

## Troubleshooting

### Port Already in Use
```bash
# Find process using port
lsof -i :8443

# Or use different ports in override
ports:
  - "8444:8443/udp"
```

### Container Won't Start
```bash
# Check logs
docker-compose logs mirror

# Verify certificates exist
ls -la certs/

# Check Redis connection
docker-compose exec redis redis-cli ping
```

### High Memory Usage
```bash
# Check memory limits
docker stats

# Adjust in override file
environment:
  - MIRROR_INGESTION_MEMORY_GLOBAL_LIMIT=4GB
```
