# Configuration Files

This directory contains YAML configuration files for the Mirror application.

## Configuration Hierarchy

The application loads configurations in the following order (later overrides earlier):
1. `default.yaml` - Base configuration with all default values
2. Environment-specific files (e.g., `development.yaml`, `production.yaml`)
3. Environment variables with `MIRROR_` prefix
4. Command-line flags

## Files

### default.yaml
Base configuration with sensible defaults for all settings:
- Server configuration (HTTP/3 port, TLS, timeouts)
- Redis connection settings
- Logging configuration
- Metrics settings

### development.yaml
Development-specific overrides:
- Debug logging enabled
- Relaxed timeouts
- Local Redis connection
- Console logging format

### docker.yaml
Docker-specific configuration:
- Redis hostname set to `redis` (Docker service name)
- Appropriate paths for containerized environment

### production.yaml (future)
Production configuration should include:
- Production Redis cluster endpoints
- Optimized timeouts
- JSON logging format
- Enhanced security settings

## Configuration Structure

```yaml
server:
  http3_port: 8443              # HTTP/3 server port
  tls_cert_file: ""            # Path to TLS certificate
  tls_key_file: ""             # Path to TLS private key
  max_incoming_streams: 100     # QUIC max concurrent streams
  max_incoming_uni_streams: 50  # QUIC max unidirectional streams
  max_idle_timeout: 30s         # Connection idle timeout
  shutdown_timeout: 10s         # Graceful shutdown timeout

redis:
  addr: "localhost:6379"        # Redis server address
  password: ""                  # Redis password (if required)
  db: 0                        # Redis database number
  max_retries: 3               # Maximum retry attempts
  min_retry_backoff: 8ms       # Minimum retry backoff
  max_retry_backoff: 512ms     # Maximum retry backoff
  dial_timeout: 5s             # Connection timeout
  read_timeout: 3s             # Read operation timeout
  write_timeout: 3s            # Write operation timeout
  pool_size: 100               # Connection pool size
  min_idle_conns: 10           # Minimum idle connections

logging:
  level: "info"                # Log level (debug, info, warn, error)
  format: "json"               # Log format (json, text)
  output: "stdout"             # Output destination
  file_path: ""                # Log file path (if output is file)
  max_size: 100                # Max size in MB (for file rotation)
  max_backups: 3               # Max number of old files
  max_age: 7                   # Max days to retain
  compress: true               # Compress rotated files

metrics:
  enabled: true                # Enable Prometheus metrics
  port: 9090                   # Metrics server port
  path: "/metrics"             # Metrics endpoint path

ingestion:
  srt_port: 30000              # SRT listener port
  rtp_port: 5004               # RTP listener port
  max_connections: 25          # Maximum concurrent streams
  connection_timeout: 10s      # Connection establishment timeout
  stream_timeout: 30s          # Stream idle timeout
  buffer_size: 1048576         # Ring buffer size (1MB)
  frame_timeout: 5s            # Frame assembly timeout
  gop_buffer_size: 3           # Number of GOPs to buffer
  sync_threshold: 100ms        # A/V sync drift threshold
  memory:
    global_limit: 4GB          # Global memory limit
    per_stream_limit: 200MB    # Per-stream memory limit
    check_interval: 1s         # Memory check interval
  queue:
    memory_size: 100MB         # In-memory queue size
    disk_path: "/tmp/mirror"   # Disk overflow path
    max_disk_usage: 10GB       # Maximum disk usage
```

## Environment Variable Mapping

Environment variables follow the pattern `MIRROR_<SECTION>_<KEY>`:

```bash
# Server settings
MIRROR_SERVER_HTTP3_PORT=8443
MIRROR_SERVER_TLS_CERT_FILE=/path/to/cert.pem
MIRROR_SERVER_TLS_KEY_FILE=/path/to/key.pem
MIRROR_SERVER_SHUTDOWN_TIMEOUT=30s

# Redis settings
MIRROR_REDIS_ADDR=redis-cluster:6379
MIRROR_REDIS_PASSWORD=secret
MIRROR_REDIS_DB=0

# Logging settings
MIRROR_LOGGING_LEVEL=debug
MIRROR_LOGGING_FORMAT=json
MIRROR_LOGGING_OUTPUT=file
MIRROR_LOGGING_FILE_PATH=/var/log/mirror/app.log

# Metrics settings
MIRROR_METRICS_ENABLED=true
MIRROR_METRICS_PORT=9090

# Ingestion settings
MIRROR_INGESTION_SRT_PORT=30000
MIRROR_INGESTION_RTP_PORT=5004
MIRROR_INGESTION_MAX_CONNECTIONS=25
MIRROR_INGESTION_BUFFER_SIZE=1048576
MIRROR_INGESTION_GOP_BUFFER_SIZE=3
MIRROR_INGESTION_MEMORY_GLOBAL_LIMIT=4GB
MIRROR_INGESTION_MEMORY_PER_STREAM_LIMIT=200MB
MIRROR_INGESTION_QUEUE_MEMORY_SIZE=100MB
MIRROR_INGESTION_QUEUE_DISK_PATH=/tmp/mirror
```

## Adding New Configuration

When adding new configuration options:

1. Add to `default.yaml` with sensible defaults
2. Update the `Config` struct in `internal/config/config.go`
3. Add validation in `internal/config/validate.go`
4. Document the new options in this file
5. Update environment variable examples

## Best Practices

1. Always provide defaults in `default.yaml`
2. Use duration strings (e.g., "30s", "5m") for time values
3. Keep secrets out of config files (use env vars or secret management)
4. Validate all configuration at startup
5. Log configuration values at startup (except secrets)
6. Use structured configuration sections
7. Document units for numeric values
