package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Metrics   MetricsConfig   `mapstructure:"metrics"`
	Ingestion IngestionConfig `mapstructure:"ingestion"`
}

type ServerConfig struct {
	// HTTP/3 Server
	HTTP3Port       int           `mapstructure:"http3_port"`
	TLSCertFile     string        `mapstructure:"tls_cert_file"`
	TLSKeyFile      string        `mapstructure:"tls_key_file"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`

	// QUIC specific
	MaxIncomingStreams    int64         `mapstructure:"max_incoming_streams"`
	MaxIncomingUniStreams int64         `mapstructure:"max_incoming_uni_streams"`
	MaxIdleTimeout        time.Duration `mapstructure:"max_idle_timeout"`
}

type RedisConfig struct {
	Addresses    []string      `mapstructure:"addresses"`
	Password     string        `mapstructure:"password"`
	DB           int           `mapstructure:"db"`
	MaxRetries   int           `mapstructure:"max_retries"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	PoolSize     int           `mapstructure:"pool_size"`
	MinIdleConns int           `mapstructure:"min_idle_conns"`
}

type LoggingConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`     // json or text
	Output     string `mapstructure:"output"`     // stdout, stderr, or file path
	MaxSize    int    `mapstructure:"max_size"`   // MB
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"` // days
}

type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
	Port    int    `mapstructure:"port"`
}

type IngestionConfig struct {
	SRT             SRTConfig             `mapstructure:"srt"`
	RTP             RTPConfig             `mapstructure:"rtp"`
	Buffer          BufferConfig          `mapstructure:"buffer"`
	Registry        RegistryConfig        `mapstructure:"registry"`
	Codecs          CodecsConfig          `mapstructure:"codecs"`
	Memory          MemoryConfig          `mapstructure:"memory"`
	QueueDir        string                `mapstructure:"queue_dir"` // Directory for queue overflow
}

type SRTConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	ListenAddr        string        `mapstructure:"listen_addr"`
	Port              int           `mapstructure:"port"`
	Latency           time.Duration `mapstructure:"latency"`         // Default 120ms
	MaxBandwidth      int64         `mapstructure:"max_bandwidth"`   // Maximum bandwidth limit (bps) - SRT MaxBW
	InputBandwidth    int64         `mapstructure:"input_bandwidth"` // Expected input rate (bps) - SRT InputBW for buffer sizing
	PayloadSize       int           `mapstructure:"payload_size"`    // MTU-friendly (1316)
	FlowControlWindow int           `mapstructure:"fc_window"`       // Flow control window
	PeerIdleTimeout   time.Duration `mapstructure:"peer_idle_timeout"`
	MaxConnections    int           `mapstructure:"max_connections"`
	Encryption        SRTEncryption `mapstructure:"encryption"`
}

type SRTEncryption struct {
	Enabled         bool   `mapstructure:"enabled"`
	Passphrase      string `mapstructure:"passphrase"`       // Min 10 chars
	KeyLength       int    `mapstructure:"key_length"`       // 0 (auto), 128, 192, 256
	PBKDFIterations int    `mapstructure:"pbkdf_iterations"` // Default: 0 (auto)
}

type RTPConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	ListenAddr     string        `mapstructure:"listen_addr"`
	Port           int           `mapstructure:"port"`
	RTCPPort       int           `mapstructure:"rtcp_port"`
	BufferSize     int           `mapstructure:"buffer_size"`
	MaxSessions    int           `mapstructure:"max_sessions"`
	SessionTimeout time.Duration `mapstructure:"session_timeout"`
}

type BufferConfig struct {
	RingSize       int           `mapstructure:"ring_size"`     // Per stream (4MB default)
	PoolSize       int           `mapstructure:"pool_size"`     // Pre-allocated buffers
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	MetricsEnabled bool          `mapstructure:"metrics_enabled"`
}

type RegistryConfig struct {
	RedisAddr           string        `mapstructure:"redis_addr"`
	RedisPassword       string        `mapstructure:"redis_password"`
	RedisDB             int           `mapstructure:"redis_db"`
	TTL                 time.Duration `mapstructure:"ttl"`
	HeartbeatInterval   time.Duration `mapstructure:"heartbeat_interval"`
	HeartbeatTimeout    time.Duration `mapstructure:"heartbeat_timeout"`
	CleanupInterval     time.Duration `mapstructure:"cleanup_interval"`
	MaxStreamsPerSource int           `mapstructure:"max_streams_per_source"`
}

type CodecsConfig struct {
	Supported []string        `mapstructure:"supported"`
	Preferred string          `mapstructure:"preferred"`
	H264      H264CodecConfig `mapstructure:"h264"`
	HEVC      HEVCCodecConfig `mapstructure:"hevc"`
	AV1       AV1CodecConfig  `mapstructure:"av1"`
	JPEGXS    JPEGXSCodecConfig `mapstructure:"jpegxs"`
}

type H264CodecConfig struct {
	Profiles []string `mapstructure:"profiles"`
	Level    string   `mapstructure:"level"`
}

type HEVCCodecConfig struct {
	Profiles []string `mapstructure:"profiles"`
	Level    string   `mapstructure:"level"`
}

type AV1CodecConfig struct {
	Profile string `mapstructure:"profile"`
	Level   string `mapstructure:"level"`
}

type JPEGXSCodecConfig struct {
	Profile     string `mapstructure:"profile"`
	Subsampling string `mapstructure:"subsampling"`
}

type MemoryConfig struct {
	MaxTotal     int64 `mapstructure:"max_total"`      // Total memory limit in bytes
	MaxPerStream int64 `mapstructure:"max_per_stream"` // Per-stream memory limit in bytes
}


func Load(configPath string) (*Config, error) {
	viper.SetConfigType("yaml")
	viper.SetConfigFile(configPath)

	// Environment variable override
	viper.SetEnvPrefix("MIRROR")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Defaults
	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func setDefaults() {
	// Server defaults
	viper.SetDefault("server.http3_port", 443)
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.shutdown_timeout", "10s")
	viper.SetDefault("server.max_incoming_streams", 5000)
	viper.SetDefault("server.max_incoming_uni_streams", 1000)
	viper.SetDefault("server.max_idle_timeout", "30s")

	// Redis defaults
	viper.SetDefault("redis.addresses", []string{"localhost:6379"})
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.max_retries", 3)
	viper.SetDefault("redis.dial_timeout", "5s")
	viper.SetDefault("redis.read_timeout", "3s")
	viper.SetDefault("redis.write_timeout", "3s")
	viper.SetDefault("redis.pool_size", 100)
	viper.SetDefault("redis.min_idle_conns", 10)

	// Logging defaults
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")
	viper.SetDefault("logging.output", "stdout")
	viper.SetDefault("logging.max_size", 100)
	viper.SetDefault("logging.max_backups", 5)
	viper.SetDefault("logging.max_age", 30)

	// Metrics defaults
	viper.SetDefault("metrics.enabled", true)
	viper.SetDefault("metrics.path", "/metrics")
	viper.SetDefault("metrics.port", 9090)

	// Ingestion defaults
	// SRT defaults
	viper.SetDefault("ingestion.srt.enabled", true)
	viper.SetDefault("ingestion.srt.listen_addr", "0.0.0.0")
	viper.SetDefault("ingestion.srt.port", 6000)
	viper.SetDefault("ingestion.srt.latency", "120ms")
	viper.SetDefault("ingestion.srt.max_bandwidth", 60000000)    // 60 Mbps
	viper.SetDefault("ingestion.srt.input_bandwidth", 55000000)  // 55 Mbps
	viper.SetDefault("ingestion.srt.payload_size", 1316)
	viper.SetDefault("ingestion.srt.fc_window", 25600)
	viper.SetDefault("ingestion.srt.peer_idle_timeout", "30s")
	viper.SetDefault("ingestion.srt.max_connections", 30)
	
	// SRT encryption defaults
	viper.SetDefault("ingestion.srt.encryption.enabled", false)
	viper.SetDefault("ingestion.srt.encryption.passphrase", "")
	viper.SetDefault("ingestion.srt.encryption.key_length", 0) // Auto
	viper.SetDefault("ingestion.srt.encryption.pbkdf_iterations", 0) // Auto

	// RTP defaults
	viper.SetDefault("ingestion.rtp.enabled", true)
	viper.SetDefault("ingestion.rtp.listen_addr", "0.0.0.0")
	viper.SetDefault("ingestion.rtp.port", 5004)
	viper.SetDefault("ingestion.rtp.rtcp_port", 5005)
	viper.SetDefault("ingestion.rtp.buffer_size", 2097152) // 2MB
	viper.SetDefault("ingestion.rtp.max_sessions", 30)
	viper.SetDefault("ingestion.rtp.session_timeout", "30s")

	// Buffer defaults
	viper.SetDefault("ingestion.buffer.ring_size", 4194304) // 4MB per stream
	viper.SetDefault("ingestion.buffer.pool_size", 30)      // Pre-allocate for 30 streams
	viper.SetDefault("ingestion.buffer.write_timeout", "100ms")
	viper.SetDefault("ingestion.buffer.read_timeout", "100ms")
	viper.SetDefault("ingestion.buffer.metrics_enabled", true)
	
	// Queue defaults
	viper.SetDefault("ingestion.queue_dir", "/var/lib/mirror/queues")

	// Registry defaults
	viper.SetDefault("ingestion.registry.heartbeat_interval", "10s")
	viper.SetDefault("ingestion.registry.heartbeat_timeout", "30s")
	viper.SetDefault("ingestion.registry.cleanup_interval", "60s")
	viper.SetDefault("ingestion.registry.max_streams_per_source", 5)
	
	// Codec defaults
	viper.SetDefault("ingestion.codecs.supported", []string{"HEVC", "H264", "AV1", "JPEGXS"})
	viper.SetDefault("ingestion.codecs.preferred", "HEVC")
	viper.SetDefault("ingestion.codecs.h264.profiles", []string{"baseline", "main", "high"})
	viper.SetDefault("ingestion.codecs.h264.level", "5.1")
	viper.SetDefault("ingestion.codecs.hevc.profiles", []string{"main", "main10"})
	viper.SetDefault("ingestion.codecs.hevc.level", "5.1")
	viper.SetDefault("ingestion.codecs.av1.profile", "0")
	viper.SetDefault("ingestion.codecs.av1.level", "5.1")
	viper.SetDefault("ingestion.codecs.jpegxs.profile", "main")
	viper.SetDefault("ingestion.codecs.jpegxs.subsampling", "422")
}
