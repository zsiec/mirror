# Configuration Package

The `config` package provides a robust, hierarchical configuration management system for the Mirror platform. It supports multiple configuration sources with proper precedence, validation, and type safety.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Usage](#usage)
- [Configuration Structure](#configuration-structure)
- [Validation](#validation)
- [Environment Variables](#environment-variables)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The configuration system is built on top of [Viper](https://github.com/spf13/viper) and provides:

- **Hierarchical configuration** from multiple sources
- **Type-safe structs** with validation
- **Environment variable overrides** with automatic mapping
- **Default values** with sensible production defaults
- **Hot reload support** (optional)
- **Comprehensive validation** with detailed error messages

## Features

### Configuration Sources (in order of precedence)

1. **Command-line flags** (highest priority)
2. **Environment variables** (MIRROR_* prefix)
3. **Configuration files** (YAML format)
4. **Default values** (lowest priority)

### Key Capabilities

- **Nested configuration** with dot notation support
- **Duration parsing** (e.g., "30s", "5m", "1h")
- **Size parsing** (e.g., "1MB", "100GB")
- **Array and map support** for complex configurations
- **Validation hooks** for custom validation logic
- **Thread-safe** configuration access

## Usage

### Basic Usage

```go
import "github.com/zsiec/mirror/internal/config"

// Load configuration from default location
cfg, err := config.Load("configs/default.yaml")
if err != nil {
    log.Fatalf("Failed to load config: %v", err)
}

// Access configuration values
port := cfg.Server.HTTP3Port
redisAddr := cfg.Redis.Addr
```

### Loading with Environment Overrides

```go
// Configuration automatically picks up environment variables
// MIRROR_SERVER_HTTP3_PORT=8443
// MIRROR_REDIS_ADDR=redis.example.com:6379

cfg, err := config.Load("configs/default.yaml")
// cfg.Server.HTTP3Port will be 8443
// cfg.Redis.Addr will be "redis.example.com:6379"
```

### Custom Configuration Loading

```go
// Load with custom options
cfg, err := config.LoadWithOptions(config.Options{
    ConfigPath:    "configs/production.yaml",
    EnvPrefix:     "MYAPP",
    AllowReload:   true,
    ValidateExtra: true,
})
```

## Configuration Structure

### Root Configuration

```go
type Config struct {
    Server      ServerConfig      `mapstructure:"server" validate:"required"`
    Redis       RedisConfig       `mapstructure:"redis" validate:"required"`
    Logging     LoggingConfig     `mapstructure:"logging" validate:"required"`
    Metrics     MetricsConfig     `mapstructure:"metrics"`
    Ingestion   IngestionConfig   `mapstructure:"ingestion" validate:"required"`
    Transcoding TranscodingConfig `mapstructure:"transcoding"`
}
```

### Server Configuration

```go
type ServerConfig struct {
    // HTTP/3 Configuration
    HTTP3Port           int           `mapstructure:"http3_port" validate:"required,min=1,max=65535"`
    TLSCertFile         string        `mapstructure:"tls_cert_file" validate:"required,file"`
    TLSKeyFile          string        `mapstructure:"tls_key_file" validate:"required,file"`
    
    // Timeouts
    ReadTimeout         time.Duration `mapstructure:"read_timeout" validate:"required,min=1s"`
    WriteTimeout        time.Duration `mapstructure:"write_timeout" validate:"required,min=1s"`
    IdleTimeout         time.Duration `mapstructure:"idle_timeout"`
    ShutdownTimeout     time.Duration `mapstructure:"shutdown_timeout" validate:"required"`
    
    // QUIC Parameters
    MaxIncomingStreams  int64         `mapstructure:"max_incoming_streams" validate:"min=0"`
    MaxIdleTimeout      time.Duration `mapstructure:"max_idle_timeout"`
    
    // Request Limits
    MaxRequestSize      int64         `mapstructure:"max_request_size" validate:"min=0"`
    MaxHeaderSize       int           `mapstructure:"max_header_size" validate:"min=0"`
}
```

### Redis Configuration

```go
type RedisConfig struct {
    Addr            string        `mapstructure:"addr" validate:"required,hostname_port"`
    Password        string        `mapstructure:"password"`
    DB              int           `mapstructure:"db" validate:"min=0,max=15"`
    
    // Connection Pool
    MaxRetries      int           `mapstructure:"max_retries" validate:"min=0"`
    MinIdleConns    int           `mapstructure:"min_idle_conns" validate:"min=0"`
    MaxIdleConns    int           `mapstructure:"max_idle_conns" validate:"min=0"`
    MaxActiveConns  int           `mapstructure:"max_active_conns" validate:"min=0"`
    
    // Timeouts
    DialTimeout     time.Duration `mapstructure:"dial_timeout" validate:"required"`
    ReadTimeout     time.Duration `mapstructure:"read_timeout" validate:"required"`
    WriteTimeout    time.Duration `mapstructure:"write_timeout" validate:"required"`
    PoolTimeout     time.Duration `mapstructure:"pool_timeout"`
    IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
    
    // Cluster Configuration (optional)
    ClusterMode     bool          `mapstructure:"cluster_mode"`
    ClusterNodes    []string      `mapstructure:"cluster_nodes"`
}
```

### Ingestion Configuration

```go
type IngestionConfig struct {
    MaxConnections      int             `mapstructure:"max_connections" validate:"required,min=1"`
    StreamTimeout       time.Duration   `mapstructure:"stream_timeout" validate:"required"`
    ReconnectInterval   time.Duration   `mapstructure:"reconnect_interval"`
    
    SRT                 SRTConfig       `mapstructure:"srt" validate:"required"`
    RTP                 RTPConfig       `mapstructure:"rtp" validate:"required"`
    Buffer              BufferConfig    `mapstructure:"buffer" validate:"required"`
    Memory              MemoryConfig    `mapstructure:"memory" validate:"required"`
    Backpressure        BackpressureConfig `mapstructure:"backpressure"`
}
```

## Validation

The configuration system includes comprehensive validation using the `validator` package:

### Built-in Validations

- **Required fields**: `validate:"required"`
- **Numeric ranges**: `validate:"min=1,max=100"`
- **String patterns**: `validate:"alphanum,lowercase"`
- **Network addresses**: `validate:"hostname_port"`
- **File paths**: `validate:"file"` or `validate:"dir"`
- **URLs**: `validate:"url"`
- **Durations**: `validate:"min=1s,max=1h"`

### Custom Validation

```go
// validate.go
func (c *Config) Validate() error {
    // Use struct tags for basic validation
    if err := validator.Validate(c); err != nil {
        return err
    }
    
    // Custom validation logic
    if c.Server.ReadTimeout > c.Server.WriteTimeout {
        return errors.New("read timeout cannot exceed write timeout")
    }
    
    // Cross-field validation
    if c.Redis.MinIdleConns > c.Redis.MaxIdleConns {
        return errors.New("min idle connections cannot exceed max idle")
    }
    
    // Business logic validation
    if c.Ingestion.Buffer.Size < 1024*1024 { // 1MB
        return errors.New("buffer size must be at least 1MB")
    }
    
    return nil
}
```

### Validation Error Handling

```go
cfg, err := config.Load("config.yaml")
if err != nil {
    if validationErr, ok := err.(*config.ValidationError); ok {
        // Detailed validation errors
        for _, fieldErr := range validationErr.Errors {
            log.Printf("Field %s: %s", fieldErr.Field, fieldErr.Message)
        }
    }
    return err
}
```

## Environment Variables

### Automatic Mapping

Environment variables are automatically mapped using the `MIRROR_` prefix:

| Config Path | Environment Variable |
|-------------|---------------------|
| `server.http3_port` | `MIRROR_SERVER_HTTP3_PORT` |
| `redis.addr` | `MIRROR_REDIS_ADDR` |
| `ingestion.srt.port` | `MIRROR_INGESTION_SRT_PORT` |
| `logging.level` | `MIRROR_LOGGING_LEVEL` |

### Complex Types

Arrays and maps can be set via environment variables:

```bash
# Array values (comma-separated)
MIRROR_REDIS_CLUSTER_NODES="redis1:6379,redis2:6379,redis3:6379"

# Duration values
MIRROR_SERVER_READ_TIMEOUT="30s"
MIRROR_INGESTION_STREAM_TIMEOUT="5m"

# Size values
MIRROR_INGESTION_BUFFER_SIZE="4MB"
MIRROR_INGESTION_MEMORY_GLOBAL_LIMIT="4GB"
```

## Best Practices

### 1. Configuration Files

```yaml
# configs/default.yaml - Base configuration
server:
  http3_port: 8443
  read_timeout: 30s
  write_timeout: 30s

# configs/development.yaml - Development overrides
extends: default.yaml
logging:
  level: debug
  output: stdout

# configs/production.yaml - Production settings
extends: default.yaml
logging:
  level: info
  output: /var/log/mirror/app.log
redis:
  cluster_mode: true
```

### 2. Sensitive Data

```go
// Never log sensitive configuration
func (c *Config) String() string {
    // Redact sensitive fields
    copy := *c
    copy.Redis.Password = "[REDACTED]"
    copy.Auth.Secret = "[REDACTED]"
    return fmt.Sprintf("%+v", copy)
}
```

### 3. Configuration Reloading

```go
// Watch for configuration changes
config.WatchConfig(func(cfg *Config) {
    log.Info("Configuration reloaded")
    // Update application state
    app.UpdateConfig(cfg)
})
```

### 4. Testing Configuration

```go
// Use test fixtures
func TestConfig(t *testing.T) {
    cfg := config.TestConfig()
    cfg.Server.HTTP3Port = 0 // Use random port
    cfg.Redis.Addr = "localhost:6379"
    
    // Test with config
    app := NewApp(cfg)
    // ...
}
```

## Testing

### Unit Tests

```go
func TestConfigValidation(t *testing.T) {
    tests := []struct {
        name    string
        config  Config
        wantErr bool
    }{
        {
            name: "valid config",
            config: Config{
                Server: ServerConfig{
                    HTTP3Port: 8443,
                    // ... other required fields
                },
            },
            wantErr: false,
        },
        {
            name: "invalid port",
            config: Config{
                Server: ServerConfig{
                    HTTP3Port: 70000, // > 65535
                },
            },
            wantErr: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.config.Validate()
            if (err != nil) != tt.wantErr {
                t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Integration Tests

```go
func TestConfigLoading(t *testing.T) {
    // Create temporary config file
    tmpfile := createTempConfig(t, `
server:
  http3_port: 8443
  tls_cert_file: "cert.pem"
  tls_key_file: "key.pem"
`)
    defer os.Remove(tmpfile)
    
    // Test loading
    cfg, err := config.Load(tmpfile)
    require.NoError(t, err)
    assert.Equal(t, 8443, cfg.Server.HTTP3Port)
}
```

## Examples

### Complete Configuration Example

```yaml
# Complete configuration with all options
server:
  http3_port: 8443
  tls_cert_file: "./certs/cert.pem"
  tls_key_file: "./certs/key.pem"
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 10s
  max_incoming_streams: 5000
  max_idle_timeout: 30s
  max_request_size: 10485760  # 10MB
  max_header_size: 8192       # 8KB

redis:
  addr: "localhost:6379"
  password: ""
  db: 0
  max_retries: 3
  min_idle_conns: 10
  max_idle_conns: 100
  max_active_conns: 0  # unlimited
  dial_timeout: 5s
  read_timeout: 3s
  write_timeout: 3s
  pool_timeout: 4s
  idle_timeout: 5m
  cluster_mode: false
  cluster_nodes: []

logging:
  level: "info"
  format: "json"
  output: "stdout"
  max_size: 100     # MB
  max_backups: 5
  max_age: 30       # days
  compress: true
  
metrics:
  enabled: true
  path: "/metrics"
  port: 9090
  namespace: "mirror"
  subsystem: "streaming"

ingestion:
  max_connections: 25
  stream_timeout: 30s
  reconnect_interval: 5s
  
  srt:
    enabled: true
    port: 30000
    latency: 120ms
    max_bandwidth: 60000000
    payload_size: 1316
    
  rtp:
    enabled: true
    port: 5004
    buffer_size: 2097152
    
  buffer:
    ring_size: 4194304      # 4MB
    pool_size: 30
    
  memory:
    global_limit: 4294967296  # 4GB
    per_stream_limit: 209715200  # 200MB
    
  backpressure:
    enabled: true
    high_watermark: 0.8
    low_watermark: 0.6
```

### Programmatic Configuration

```go
// Create configuration programmatically
cfg := &config.Config{
    Server: config.ServerConfig{
        HTTP3Port:       8443,
        TLSCertFile:     "cert.pem",
        TLSKeyFile:      "key.pem",
        ReadTimeout:     30 * time.Second,
        WriteTimeout:    30 * time.Second,
        ShutdownTimeout: 10 * time.Second,
    },
    Redis: config.RedisConfig{
        Addr:         "localhost:6379",
        MaxRetries:   3,
        DialTimeout:  5 * time.Second,
        ReadTimeout:  3 * time.Second,
        WriteTimeout: 3 * time.Second,
    },
    // ... other configuration
}

// Validate
if err := cfg.Validate(); err != nil {
    return fmt.Errorf("invalid configuration: %w", err)
}
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Environment Setup](../../docs/setup.md) - Development environment
- [Deployment Guide](../../docs/deployment.md) - Production deployment
