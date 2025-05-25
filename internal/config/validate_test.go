package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedisConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  RedisConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: RedisConfig{
				Addresses:    []string{"localhost:6379"},
				DB:           0,
				MaxRetries:   3,
				PoolSize:     100,
				MinIdleConns: 10,
			},
			wantErr: false,
		},
		{
			name: "missing addresses",
			config: RedisConfig{
				Addresses: []string{},
				DB:        0,
				PoolSize:  100,
			},
			wantErr: true,
			errMsg:  "at least one Redis address is required",
		},
		{
			name: "negative DB",
			config: RedisConfig{
				Addresses: []string{"localhost:6379"},
				DB:        -1,
				PoolSize:  100,
			},
			wantErr: true,
			errMsg:  "invalid Redis database number",
		},
		{
			name: "zero pool size",
			config: RedisConfig{
				Addresses: []string{"localhost:6379"},
				DB:        0,
				PoolSize:  0,
			},
			wantErr: true,
			errMsg:  "pool_size must be positive",
		},
		{
			name: "min idle conns greater than pool size",
			config: RedisConfig{
				Addresses:    []string{"localhost:6379"},
				DB:           0,
				PoolSize:     10,
				MinIdleConns: 20,
			},
			wantErr: true,
			errMsg:  "min_idle_conns cannot be greater than pool_size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoggingConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  LoggingConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: LoggingConfig{
				Level:  "info",
				Format: "json",
				Output: "stdout",
			},
			wantErr: false,
		},
		{
			name: "invalid log level",
			config: LoggingConfig{
				Level:  "invalid",
				Format: "json",
				Output: "stdout",
			},
			wantErr: true,
			errMsg:  "invalid log level",
		},
		{
			name: "invalid format",
			config: LoggingConfig{
				Level:  "info",
				Format: "xml",
				Output: "stdout",
			},
			wantErr: true,
			errMsg:  "log format must be 'json' or 'text'",
		},
		{
			name: "file output with zero max size",
			config: LoggingConfig{
				Level:   "info",
				Format:  "json",
				Output:  "/var/log/mirror.log",
				MaxSize: 0,
			},
			wantErr: true,
			errMsg:  "max_size must be positive for file output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMetricsConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  MetricsConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config enabled",
			config: MetricsConfig{
				Enabled: true,
				Path:    "/metrics",
				Port:    9090,
			},
			wantErr: false,
		},
		{
			name: "valid config disabled",
			config: MetricsConfig{
				Enabled: false,
			},
			wantErr: false,
		},
		{
			name: "enabled with invalid port",
			config: MetricsConfig{
				Enabled: true,
				Path:    "/metrics",
				Port:    0,
			},
			wantErr: true,
			errMsg:  "invalid metrics port",
		},
		{
			name: "enabled with empty path",
			config: MetricsConfig{
				Enabled: true,
				Path:    "",
				Port:    9090,
			},
			wantErr: true,
			errMsg:  "metrics path cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
