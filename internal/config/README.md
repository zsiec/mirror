# Configuration Package

The `config` package provides hierarchical configuration management for the Mirror platform using [Viper](https://github.com/spf13/viper).

## Configuration Sources (in order of precedence)

1. **Environment variables** (`MIRROR_*` prefix)
2. **Configuration files** (YAML format)
3. **Default values** (set in `setDefaults()`)

## Usage

```go
import "github.com/zsiec/mirror/internal/config"

cfg, err := config.Load("configs/default.yaml")
if err != nil {
    log.Fatalf("Failed to load config: %v", err)
}

port := cfg.Server.HTTP3Port
addrs := cfg.Redis.Addresses  // []string
```

Environment variables are automatically mapped:

| Config Path | Environment Variable |
|---|---|
| `server.http3_port` | `MIRROR_SERVER_HTTP3_PORT` |
| `redis.addresses` | `MIRROR_REDIS_ADDRESSES` |
| `ingestion.srt.port` | `MIRROR_INGESTION_SRT_PORT` |
| `logging.level` | `MIRROR_LOGGING_LEVEL` |

## Configuration Structure

### Root Config

```go
type Config struct {
    Server    ServerConfig    `mapstructure:"server"`
    Redis     RedisConfig     `mapstructure:"redis"`
    Logging   LoggingConfig   `mapstructure:"logging"`
    Metrics   MetricsConfig   `mapstructure:"metrics"`
    Ingestion IngestionConfig `mapstructure:"ingestion"`
}
```

### ServerConfig

```go
type ServerConfig struct {
    HTTP3Port           int           // QUIC port (default: 443)
    TLSCertFile         string
    TLSKeyFile          string
    ReadTimeout         time.Duration // default: 30s
    WriteTimeout        time.Duration // default: 30s
    ShutdownTimeout     time.Duration // default: 10s

    // HTTP/1.1 and HTTP/2 fallback
    HTTPPort            int
    EnableHTTP          bool
    EnableHTTP2         bool
    HTTPSRedirect       bool
    DebugEndpoints      bool          // pprof, etc.

    // Authentication
    APIKey              string        // for destructive endpoints; empty disables auth

    // QUIC specific
    MaxIncomingStreams   int64         // default: 5000
    MaxIncomingUniStreams int64        // default: 1000
    MaxIdleTimeout      time.Duration // default: 30s
}
```

### RedisConfig

```go
type RedisConfig struct {
    Addresses    []string      // default: ["localhost:6379"]
    Password     string
    DB           int           // default: 0
    MaxRetries   int           // default: 3
    DialTimeout  time.Duration // default: 5s
    ReadTimeout  time.Duration // default: 3s
    WriteTimeout time.Duration // default: 3s
    PoolSize     int           // default: 100
    MinIdleConns int           // default: 10
}
```

### IngestionConfig

```go
type IngestionConfig struct {
    SRT            SRTConfig
    RTP            RTPConfig
    Buffer         BufferConfig
    Registry       RegistryConfig
    Codecs         CodecsConfig
    Memory         MemoryConfig
    QueueDir       string               // disk overflow path
    StreamHandling StreamHandlingConfig
    Backpressure   BackpressureConfig
}
```

Key sub-configs:
- **SRTConfig**: port, latency, max_bandwidth, input_bandwidth, payload_size, max_connections, encryption (passphrase, key_length), caller_mode
- **RTPConfig**: port, rtcp_port, buffer_size, max_sessions, session_timeout
- **BufferConfig**: ring_size (4MB default), pool_size, write/read timeout, metrics_enabled
- **RegistryConfig**: Redis connection for stream registry, TTL, heartbeat settings, max_streams_per_source
- **CodecsConfig**: supported codecs list, preferred codec, per-codec profiles/levels (H264, HEVC, AV1, JPEGXS)
- **MemoryConfig**: max_total (8GB), max_per_stream (400MB)
- **StreamHandlingConfig**: frame_assembly_timeout (200ms), gop_buffer_size, max_gop_age, error_retry_limit
- **BackpressureConfig**: enabled, low/medium/high/critical watermarks, response_window, frame_drop_ratio

### LoggingConfig

```go
type LoggingConfig struct {
    Level      string // panic, fatal, error, warn, info, debug, trace
    Format     string // json or text
    Output     string // stdout, stderr, or file path
    MaxSize    int    // MB (for file output)
    MaxBackups int
    MaxAge     int    // days
}
```

### MetricsConfig

```go
type MetricsConfig struct {
    Enabled bool   // default: true
    Path    string // default: "/metrics"
    Port    int    // default: 9090
}
```

## Validation

Each config section has its own `Validate()` method called from `Config.Validate()`. Key validations:

- **Server**: port range, TLS cert/key files exist, max_incoming_streams > 0, HTTP port if enabled
- **Redis**: at least one address, pool_size > 0, min_idle_conns <= pool_size
- **Logging**: valid level (panic-trace), format json/text
- **Metrics**: valid port if enabled
- **Ingestion**: at least one protocol enabled, buffer pool_size >= max total connections
- **SRT**: valid port, listen_addr not empty, max_bandwidth != 0 (positive or -1 for auto), payload_size 1-1500, encryption passphrase 10-80 chars if enabled, key_length in {0,128,192,256}
- **RTP**: valid port, RTP != RTCP port, buffer_size > 0
- **Memory**: max_per_stream <= max_total, both > 0
- **Registry**: max_streams_per_source > 0

## Files

- `config.go`: All config structs, `Load()`, and `setDefaults()`
- `validate.go`: Per-section validation methods
- `config_test.go`: Tests

## Config File Examples

```yaml
# configs/default.yaml (production defaults)
server:
  http3_port: 443
  tls_cert_file: "./certs/cert.pem"
  tls_key_file: "./certs/key.pem"

redis:
  addresses:
    - "localhost:6379"

ingestion:
  srt:
    port: 1234
  rtp:
    port: 5004
```

```yaml
# configs/development.yaml (local dev)
server:
  http3_port: 8443
  http_port: 8081
  enable_http: true
  debug_endpoints: true

ingestion:
  srt:
    port: 30000
  rtp:
    port: 15004
```
