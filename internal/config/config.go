package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Redis   RedisConfig   `mapstructure:"redis"`
	Logging LoggingConfig `mapstructure:"logging"`
	Metrics MetricsConfig `mapstructure:"metrics"`
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
}