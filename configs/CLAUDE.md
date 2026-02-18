# Configuration Files

This directory contains YAML configuration files for the Mirror application.

## Configuration Hierarchy

The application loads configurations in the following order (later overrides earlier):
1. `default.yaml` - Base configuration with all default values
2. Environment-specific files (e.g., `development.yaml`, `docker.yaml`)
3. Environment variables with `MIRROR_` prefix

## Files

### default.yaml
Base configuration with production-oriented defaults:
- Server on port 443 (HTTP/3), HTTP fallback disabled
- Redis on localhost:6379
- SRT on port 1234, RTP on port 5004
- JSON logging at info level
- 8GB global memory limit, 400MB per stream
- FFmpeg integration with software decoding
- Backpressure control enabled

### development.yaml
Development-specific overrides:
- Server on port 8443 (HTTP/3), HTTP/1.1+HTTP/2 on port 8080
- Debug logging in text format
- Debug endpoints enabled (pprof)
- SRT on port 30000, RTP on port 15004
- Hardware acceleration enabled (VideoToolbox on macOS)

### docker.yaml
Docker-specific configuration:
- Redis hostname set to `redis` (Docker service name)

### test.yaml
Test configuration for CI/automated testing.

## Configuration Structure

The actual structure in `default.yaml` (not all fields shown — see the file for full details):

```yaml
server:
  http3_port: 443                # HTTP/3 (QUIC) port
  tls_cert_file: "./certs/cert.pem"
  tls_key_file: "./certs/key.pem"
  http_port: 8080                # HTTP/1.1+HTTP/2 fallback port
  enable_http: false             # Enable HTTP fallback server
  enable_http2: true             # Enable HTTP/2
  debug_endpoints: false         # Enable /debug/pprof/*
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 10s
  max_incoming_streams: 5000
  max_incoming_uni_streams: 1000
  max_idle_timeout: 30s

redis:
  addresses:                     # NOTE: array, not single string
    - "localhost:6379"
  password: ""
  db: 0
  pool_size: 100
  min_idle_conns: 10

logging:
  level: "info"
  format: "json"                 # "json" or "text"
  output: "stdout"

metrics:
  enabled: true
  port: 9090
  path: "/metrics"

ingestion:
  queue_dir: "/tmp/mirror/queue"   # Disk overflow path for hybrid queue
  srt:
    enabled: true
    listen_addr: "0.0.0.0"
    port: 1234                     # Default SRT port (30000 in development)
    latency: 120ms
    max_bandwidth: 52428800        # 50 Mbps
    input_bandwidth: 52428800
    payload_size: 1316
    fc_window: 25600
    peer_idle_timeout: 5s
    max_connections: 25
    encryption:
      enabled: false
      key_length: 128
  rtp:
    enabled: true
    listen_addr: "0.0.0.0"
    port: 5004                     # Default RTP port (15004 in development)
    rtcp_port: 5005
    buffer_size: 65536
    max_sessions: 25
    session_timeout: 10s
  buffer:
    ring_size: 4194304             # 4MB per stream
    pool_size: 50
    write_timeout: 10ms
    read_timeout: 10ms
    default_bitrate: 52428800      # 50 Mbps
    target_duration: 30s
  registry:
    redis_addr: "localhost:6379"
    ttl: 1h
  memory:
    max_total: 8589934592          # 8GB
    max_per_stream: 419430400      # 400MB
  ffmpeg:
    enabled: true
    log_level: "error"
    thread_count: 0                # 0 = auto-detect
    hardware_acceleration:
      enabled: false
      fallback_software: true
    decoder:
      buffer_size: 2
      pixel_format: "yuv420p"
      thread_type: "frame"
    jpeg:
      quality: 80
  stream_handling:
    frame_assembly_timeout: 200ms
    gop_buffer_size: 15
    max_gop_age: 5s
    error_retry_limit: 3
  backpressure:
    enabled: true
    low_watermark: 0.25
    medium_watermark: 0.5
    high_watermark: 0.75
    critical_watermark: 0.9
    response_window: 500ms
    frame_drop_ratio: 0.1
```

## Environment Variable Mapping

Environment variables follow the pattern `MIRROR_<SECTION>_<KEY>` with nesting using underscores:

```bash
# Server
MIRROR_SERVER_HTTP3_PORT=8443
MIRROR_SERVER_TLS_CERT_FILE=/path/to/cert.pem
MIRROR_SERVER_ENABLE_HTTP=true
MIRROR_SERVER_DEBUG_ENDPOINTS=true

# Redis
MIRROR_REDIS_ADDRESSES="redis-cluster:6379"
MIRROR_REDIS_PASSWORD=secret

# Logging
MIRROR_LOGGING_LEVEL=debug
MIRROR_LOGGING_FORMAT=json

# Ingestion - SRT (nested)
MIRROR_INGESTION_SRT_PORT=30000
MIRROR_INGESTION_SRT_MAX_CONNECTIONS=25
MIRROR_INGESTION_SRT_ENCRYPTION_PASSPHRASE=secret

# Ingestion - RTP (nested)
MIRROR_INGESTION_RTP_PORT=5004
MIRROR_INGESTION_RTP_MAX_SESSIONS=25

# Ingestion - Memory
MIRROR_INGESTION_MEMORY_MAX_TOTAL=8589934592
MIRROR_INGESTION_MEMORY_MAX_PER_STREAM=419430400

# Ingestion - Queue
MIRROR_INGESTION_QUEUE_DIR=/tmp/mirror/queue
```

## Adding New Configuration

When adding new configuration options:

1. Add to `default.yaml` with sensible defaults
2. Update the `Config` struct in `internal/config/config.go`
3. Add validation in `internal/config/validate.go`
4. Document the new options in this file
5. Update environment variable examples
