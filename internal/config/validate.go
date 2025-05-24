package config

import (
	"fmt"
	"os"
)

func (c *Config) Validate() error {
	if err := c.Server.Validate(); err != nil {
		return fmt.Errorf("server config: %w", err)
	}

	if err := c.Redis.Validate(); err != nil {
		return fmt.Errorf("redis config: %w", err)
	}

	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging config: %w", err)
	}

	if err := c.Metrics.Validate(); err != nil {
		return fmt.Errorf("metrics config: %w", err)
	}

	return nil
}

func (s *ServerConfig) Validate() error {
	if s.HTTP3Port < 1 || s.HTTP3Port > 65535 {
		return fmt.Errorf("invalid HTTP3 port: %d", s.HTTP3Port)
	}

	if s.TLSCertFile == "" {
		return fmt.Errorf("TLS certificate file is required")
	}

	if s.TLSKeyFile == "" {
		return fmt.Errorf("TLS key file is required")
	}

	// Check if certificate files exist
	if _, err := os.Stat(s.TLSCertFile); os.IsNotExist(err) {
		return fmt.Errorf("TLS certificate file not found: %s", s.TLSCertFile)
	}

	if _, err := os.Stat(s.TLSKeyFile); os.IsNotExist(err) {
		return fmt.Errorf("TLS key file not found: %s", s.TLSKeyFile)
	}

	if s.MaxIncomingStreams <= 0 {
		return fmt.Errorf("max_incoming_streams must be positive")
	}

	if s.MaxIncomingUniStreams <= 0 {
		return fmt.Errorf("max_incoming_uni_streams must be positive")
	}

	return nil
}

func (r *RedisConfig) Validate() error {
	if len(r.Addresses) == 0 {
		return fmt.Errorf("at least one Redis address is required")
	}

	if r.DB < 0 {
		return fmt.Errorf("invalid Redis database number: %d", r.DB)
	}

	if r.MaxRetries < 0 {
		return fmt.Errorf("max_retries cannot be negative")
	}

	if r.PoolSize <= 0 {
		return fmt.Errorf("pool_size must be positive")
	}

	if r.MinIdleConns < 0 {
		return fmt.Errorf("min_idle_conns cannot be negative")
	}

	if r.MinIdleConns > r.PoolSize {
		return fmt.Errorf("min_idle_conns cannot be greater than pool_size")
	}

	return nil
}

func (l *LoggingConfig) Validate() error {
	validLevels := map[string]bool{
		"panic": true,
		"fatal": true,
		"error": true,
		"warn":  true,
		"info":  true,
		"debug": true,
		"trace": true,
	}

	if !validLevels[l.Level] {
		return fmt.Errorf("invalid log level: %s", l.Level)
	}

	if l.Format != "json" && l.Format != "text" {
		return fmt.Errorf("log format must be 'json' or 'text'")
	}

	if l.Output != "stdout" && l.Output != "stderr" {
		// If it's a file path, check if the directory exists
		if l.MaxSize <= 0 {
			return fmt.Errorf("max_size must be positive for file output")
		}
		if l.MaxBackups < 0 {
			return fmt.Errorf("max_backups cannot be negative")
		}
		if l.MaxAge < 0 {
			return fmt.Errorf("max_age cannot be negative")
		}
	}

	return nil
}

func (m *MetricsConfig) Validate() error {
	if m.Enabled {
		if m.Port < 1 || m.Port > 65535 {
			return fmt.Errorf("invalid metrics port: %d", m.Port)
		}

		if m.Path == "" {
			return fmt.Errorf("metrics path cannot be empty")
		}
	}

	return nil
}