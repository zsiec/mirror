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

	if err := c.Ingestion.Validate(); err != nil {
		return fmt.Errorf("ingestion config: %w", err)
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

	// Validate HTTP/1.1 and HTTP/2 configuration if enabled
	if s.EnableHTTP {
		if s.HTTPPort < 1 || s.HTTPPort > 65535 {
			return fmt.Errorf("invalid HTTP port: %d", s.HTTPPort)
		}

		// Warn if debug endpoints are enabled
		if s.DebugEndpoints {
			// This is just a warning, not an error
			// The logger might not be available here, so we'll handle this at startup
		}
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

func (i *IngestionConfig) Validate() error {
	if err := i.SRT.Validate(); err != nil {
		return fmt.Errorf("srt config: %w", err)
	}

	if err := i.RTP.Validate(); err != nil {
		return fmt.Errorf("rtp config: %w", err)
	}

	if err := i.Buffer.Validate(); err != nil {
		return fmt.Errorf("buffer config: %w", err)
	}

	if err := i.Registry.Validate(); err != nil {
		return fmt.Errorf("registry config: %w", err)
	}

	if err := i.Memory.Validate(); err != nil {
		return fmt.Errorf("memory config: %w", err)
	}

	if i.QueueDir == "" {
		return fmt.Errorf("queue_dir cannot be empty")
	}

	if !i.SRT.Enabled && !i.RTP.Enabled {
		return fmt.Errorf("at least one ingestion protocol must be enabled")
	}

	// Validate buffer pool size against max connections
	maxConnections := 0
	if i.SRT.Enabled {
		maxConnections += i.SRT.MaxConnections
	}
	if i.RTP.Enabled {
		maxConnections += i.RTP.MaxSessions
	}

	if i.Buffer.PoolSize < maxConnections {
		return fmt.Errorf("buffer pool size (%d) should be >= max total connections (%d) to avoid runtime allocations",
			i.Buffer.PoolSize, maxConnections)
	}

	return nil
}

func (s *SRTConfig) Validate() error {
	if !s.Enabled {
		return nil
	}

	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("invalid SRT port: %d", s.Port)
	}

	if s.ListenAddr == "" {
		return fmt.Errorf("SRT listen address cannot be empty")
	}

	if s.MaxBandwidth <= 0 {
		return fmt.Errorf("max_bandwidth must be positive")
	}

	if s.InputBandwidth <= 0 {
		return fmt.Errorf("input_bandwidth must be positive")
	}

	if s.InputBandwidth > s.MaxBandwidth {
		return fmt.Errorf("input_bandwidth cannot exceed max_bandwidth")
	}

	if s.PayloadSize <= 0 || s.PayloadSize > 1500 {
		return fmt.Errorf("payload_size must be between 1 and 1500")
	}

	if s.MaxConnections <= 0 {
		return fmt.Errorf("max_connections must be positive")
	}

	return nil
}

func (r *RTPConfig) Validate() error {
	if !r.Enabled {
		return nil
	}

	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("invalid RTP port: %d", r.Port)
	}

	if r.RTCPPort < 1 || r.RTCPPort > 65535 {
		return fmt.Errorf("invalid RTCP port: %d", r.RTCPPort)
	}

	if r.Port == r.RTCPPort {
		return fmt.Errorf("RTP and RTCP ports must be different")
	}

	if r.ListenAddr == "" {
		return fmt.Errorf("RTP listen address cannot be empty")
	}

	if r.BufferSize <= 0 {
		return fmt.Errorf("buffer_size must be positive")
	}

	if r.MaxSessions <= 0 {
		return fmt.Errorf("max_sessions must be positive")
	}

	return nil
}

func (b *BufferConfig) Validate() error {
	if b.RingSize <= 0 {
		return fmt.Errorf("ring_size must be positive")
	}

	if b.PoolSize <= 0 {
		return fmt.Errorf("pool_size must be positive")
	}

	return nil
}

func (m *MemoryConfig) Validate() error {
	if m.MaxTotal <= 0 {
		return fmt.Errorf("max_total must be positive")
	}

	if m.MaxPerStream <= 0 {
		return fmt.Errorf("max_per_stream must be positive")
	}

	if m.MaxPerStream > m.MaxTotal {
		return fmt.Errorf("max_per_stream (%d) cannot exceed max_total (%d)", m.MaxPerStream, m.MaxTotal)
	}

	return nil
}

func (r *RegistryConfig) Validate() error {
	if r.MaxStreamsPerSource <= 0 {
		return fmt.Errorf("max_streams_per_source must be positive")
	}

	return nil
}
