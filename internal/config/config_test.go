package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "cert files not found",
			config: &Config{
				Server: ServerConfig{
					HTTP3Port:             443,
					TLSCertFile:           "/nonexistent/cert.pem",
					TLSKeyFile:            "/nonexistent/key.pem",
					MaxIncomingStreams:    5000,
					MaxIncomingUniStreams: 1000,
				},
				Redis: RedisConfig{
					Addresses:    []string{"localhost:6379"},
					DB:           0,
					MaxRetries:   3,
					PoolSize:     100,
					MinIdleConns: 10,
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
					Output: "stdout",
				},
				Metrics: MetricsConfig{
					Enabled: true,
					Path:    "/metrics",
					Port:    9090,
				},
			},
			wantErr: true,
			errMsg:  "TLS certificate file not found",
		},
		{
			name: "invalid server port",
			config: &Config{
				Server: ServerConfig{
					HTTP3Port: 0,
				},
			},
			wantErr: true,
			errMsg:  "invalid HTTP3 port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if err != nil {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	tmpfile, err := os.CreateTemp("", "test-config-*.yaml")
	require.NoError(t, err)
	defer func() {
		_ = os.Remove(tmpfile.Name())
	}()

	configContent := `
server:
  http3_port: 8443
  tls_cert_file: "test-cert.pem"
  tls_key_file: "test-key.pem"
  max_incoming_streams: 1000
  max_incoming_uni_streams: 500

redis:
  addresses:
    - "localhost:6379"
  pool_size: 50

logging:
  level: "debug"
  format: "text"

metrics:
  enabled: false
`
	_, err = tmpfile.Write([]byte(configContent))
	require.NoError(t, err)
	_ = tmpfile.Close()

	// Test loading config
	cfg, err := Load(tmpfile.Name())
	assert.Error(t, err) // Should fail on validation (missing cert files)
	assert.Nil(t, cfg)
}

func TestServerConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  ServerConfig
		wantErr bool
	}{
		{
			name: "empty cert file",
			config: ServerConfig{
				HTTP3Port:   443,
				TLSCertFile: "",
				TLSKeyFile:  "key.pem",
			},
			wantErr: true,
		},
		{
			name: "empty key file",
			config: ServerConfig{
				HTTP3Port:   443,
				TLSCertFile: "cert.pem",
				TLSKeyFile:  "",
			},
			wantErr: true,
		},
		{
			name: "invalid port",
			config: ServerConfig{
				HTTP3Port:   70000,
				TLSCertFile: "cert.pem",
				TLSKeyFile:  "key.pem",
			},
			wantErr: true,
		},
		{
			name: "zero max incoming streams",
			config: ServerConfig{
				HTTP3Port:             443,
				TLSCertFile:           "cert.pem",
				TLSKeyFile:            "key.pem",
				MaxIncomingStreams:    0,
				MaxIncomingUniStreams: 1000,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRedisConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  RedisConfig
		wantErr bool
	}{
		{
			name: "negative DB",
			config: RedisConfig{
				Addresses: []string{"localhost:6379"},
				DB:        -1,
				PoolSize:  100,
			},
			wantErr: true,
		},
		{
			name: "zero pool size",
			config: RedisConfig{
				Addresses: []string{"localhost:6379"},
				DB:        0,
				PoolSize:  0,
			},
			wantErr: true,
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
