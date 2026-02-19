package srt

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewPureGoAdapter(t *testing.T) {
	adapter := NewPureGoAdapter()
	assert.NotNil(t, adapter, "adapter should not be nil")
	assert.IsType(t, &PureGoAdapter{}, adapter, "should return PureGoAdapter type")
}

func TestPureGoAdapter_NewListener(t *testing.T) {
	adapter := NewPureGoAdapter()

	tests := []struct {
		name        string
		address     string
		port        int
		config      Config
		wantErr     bool
		description string
	}{
		{
			name:        "valid parameters",
			address:     "localhost",
			port:        30000,
			config:      Config{},
			wantErr:     false,
			description: "should create listener with valid parameters",
		},
		{
			name:        "zero port",
			address:     "0.0.0.0",
			port:        0,
			config:      Config{},
			wantErr:     false,
			description: "should create listener with zero port",
		},
		{
			name:        "empty address",
			address:     "",
			port:        30000,
			config:      Config{},
			wantErr:     false,
			description: "should create listener with empty address",
		},
		{
			name:    "with encryption config",
			address: "localhost",
			port:    30000,
			config: Config{
				Encryption: EncryptionConfig{
					Enabled:    true,
					Passphrase: "test123456", // min 10 bytes for SRT
					KeyLength:  16,
				},
			},
			wantErr:     false,
			description: "should create listener with encryption config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := adapter.NewListener(tt.address, tt.port, tt.config)

			if tt.wantErr {
				assert.Error(t, err, tt.description)
				assert.Nil(t, listener, "listener should be nil on error")
			} else {
				assert.NoError(t, err, tt.description)
				assert.NotNil(t, listener, "listener should not be nil")
				assert.IsType(t, &PureGoListener{}, listener, "should return PureGoListener type")

				// Verify listener has correct properties
				pgListener := listener.(*PureGoListener)
				assert.Equal(t, tt.address, pgListener.address, "address should match")
				assert.Equal(t, tt.port, pgListener.port, "port should match")
				assert.Equal(t, tt.config, pgListener.config, "config should match")
			}
		})
	}
}

func TestPureGoAdapter_NewConnection(t *testing.T) {
	adapter := NewPureGoAdapter()

	tests := []struct {
		name        string
		socket      SRTSocket
		wantErr     bool
		errContains string
		description string
	}{
		{
			name:        "nil socket",
			socket:      nil,
			wantErr:     true,
			errContains: "invalid socket type",
			description: "should fail with nil socket",
		},
		{
			name:        "wrong socket type",
			socket:      &testMockSRTSocket{}, // Not a PureGoSocket
			wantErr:     true,
			errContains: "invalid socket type",
			description: "should fail with wrong socket type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := adapter.NewConnection(tt.socket)

			if tt.wantErr {
				assert.Error(t, err, tt.description)
				assert.Nil(t, conn, "connection should be nil on error")
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains, "error should contain expected text")
				}
			} else {
				assert.NoError(t, err, tt.description)
				assert.NotNil(t, conn, "connection should not be nil")
			}
		})
	}
}

// Mock socket for testing (not a PureGoSocket)
type testMockSRTSocket struct{}

func (m *testMockSRTSocket) Close() error                                 { return nil }
func (m *testMockSRTSocket) GetStreamID() string                          { return "" }
func (m *testMockSRTSocket) SetRejectReason(reason RejectionReason) error { return nil }

func TestPureGoAdapter_Integration(t *testing.T) {
	adapter := NewPureGoAdapter()

	config := Config{
		Latency:      100 * time.Millisecond,
		MaxBandwidth: 1000000,
		PayloadSize:  1316,
	}

	listener, err := adapter.NewListener("localhost", 30001, config)
	assert.NoError(t, err, "should create listener successfully")
	assert.NotNil(t, listener, "listener should not be nil")

	pgListener, ok := listener.(*PureGoListener)
	assert.True(t, ok, "listener should be PureGoListener type")
	assert.Equal(t, "localhost", pgListener.address, "listener address should match")
	assert.Equal(t, 30001, pgListener.port, "listener port should match")
	assert.Equal(t, config, pgListener.config, "listener config should match")
}

func TestPureGoAdapter_ConfigVariations(t *testing.T) {
	adapter := NewPureGoAdapter()

	configs := []Config{
		{}, // Empty config
		{
			Latency:      50 * time.Millisecond,
			MaxBandwidth: 500000,
			PayloadSize:  1316,
		},
		{
			Latency:      200 * time.Millisecond,
			MaxBandwidth: 2000000,
			PayloadSize:  1316,
			Encryption: EncryptionConfig{
				Enabled:    true,
				Passphrase: "secret1234567890",
				KeyLength:  24,
			},
		},
		{
			// Maximum values
			Latency:      5 * time.Second,
			MaxBandwidth: 10000000,
			PayloadSize:  1500,
		},
	}

	for i, config := range configs {
		t.Run(fmt.Sprintf("config_%d", i), func(t *testing.T) {
			listener, err := adapter.NewListener("localhost", 30000+i, config)
			assert.NoError(t, err, "should create listener with config %d", i)
			assert.NotNil(t, listener, "listener should not be nil for config %d", i)

			pgListener := listener.(*PureGoListener)
			assert.Equal(t, config, pgListener.config, "config should be preserved for config %d", i)
		})
	}
}

func TestPureGoAdapter_PortRange(t *testing.T) {
	adapter := NewPureGoAdapter()
	config := Config{}

	ports := []int{0, 1, 1024, 30000, 65535}

	for _, port := range ports {
		t.Run(fmt.Sprintf("port_%d", port), func(t *testing.T) {
			listener, err := adapter.NewListener("localhost", port, config)
			assert.NoError(t, err, "should create listener with port %d", port)
			assert.NotNil(t, listener, "listener should not be nil for port %d", port)

			pgListener := listener.(*PureGoListener)
			assert.Equal(t, port, pgListener.port, "port should be preserved")
		})
	}
}

func TestPureGoAdapter_AddressVariations(t *testing.T) {
	adapter := NewPureGoAdapter()
	config := Config{}

	addresses := []string{
		"localhost",
		"127.0.0.1",
		"0.0.0.0",
		"::1",
		"::",
		"example.com",
		"",
	}

	for _, address := range addresses {
		t.Run(fmt.Sprintf("address_%s", address), func(t *testing.T) {
			listener, err := adapter.NewListener(address, 30000, config)
			assert.NoError(t, err, "should create listener with address '%s'", address)
			assert.NotNil(t, listener, "listener should not be nil for address '%s'", address)

			pgListener := listener.(*PureGoListener)
			assert.Equal(t, address, pgListener.address, "address should be preserved")
		})
	}
}

func TestBuildPureGoConfig(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		cfg := buildPureGoConfig(Config{})
		assert.Equal(t, 25, cfg.OverheadBW)
		assert.Equal(t, 128, cfg.LossMaxTTL)
		assert.Equal(t, 5*time.Second, cfg.ConnTimeout)
	})

	t.Run("latency mapping", func(t *testing.T) {
		cfg := buildPureGoConfig(Config{Latency: 200 * time.Millisecond})
		assert.Equal(t, 200*time.Millisecond, cfg.RecvLatency)
		assert.Equal(t, 200*time.Millisecond, cfg.PeerLatency)
	})

	t.Run("bandwidth mapping bits to bytes", func(t *testing.T) {
		cfg := buildPureGoConfig(Config{MaxBandwidth: 50_000_000}) // 50 Mbps
		assert.Equal(t, int64(50_000_000/8), cfg.MaxBW)
	})

	t.Run("input bandwidth relative mode", func(t *testing.T) {
		cfg := buildPureGoConfig(Config{InputBandwidth: 50_000_000})
		assert.Equal(t, int64(50_000_000/8), cfg.InputBW)
		assert.Equal(t, int64(0), cfg.MaxBW) // relative mode
	})

	t.Run("encryption", func(t *testing.T) {
		cfg := buildPureGoConfig(Config{
			Encryption: EncryptionConfig{
				Enabled:    true,
				Passphrase: "mypassphrase",
				KeyLength:  32,
			},
		})
		assert.Equal(t, "mypassphrase", cfg.Passphrase)
		assert.Equal(t, 32, cfg.KeyLength)
		assert.NotNil(t, cfg.EnforcedEncryption)
		assert.True(t, *cfg.EnforcedEncryption)
	})

	t.Run("buffer sizes", func(t *testing.T) {
		cfg := buildPureGoConfig(Config{
			InputBandwidth: 50_000_000,  // 50 Mbps
			Latency:        120 * time.Millisecond,
		})
		// Buffer should be at least 8MB / MSS packets
		assert.Greater(t, cfg.RecvBufSize, 0)
		assert.Greater(t, cfg.SendBufSize, 0)
	})
}

func TestPureGoCallbackSocket_RejectReason(t *testing.T) {
	tests := []struct {
		name   string
		reason RejectionReason
	}{
		{"unauthorized", RejectionReasonUnauthorized},
		{"resource unavailable", RejectionReasonResourceUnavailable},
		{"bad request", RejectionReasonBadRequest},
		{"forbidden", RejectionReasonForbidden},
		{"not found", RejectionReasonNotFound},
		{"bad mode", RejectionReasonBadMode},
		{"unacceptable", RejectionReasonUnacceptable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock := &PureGoCallbackSocket{streamID: "test"}
			err := sock.SetRejectReason(tt.reason)
			assert.NoError(t, err)
			assert.NotZero(t, sock.rejectReason)
		})
	}
}

func TestPureGoConnection_NilConn(t *testing.T) {
	conn := &PureGoConnection{}

	_, err := conn.Read(make([]byte, 100))
	assert.Error(t, err)

	_, err = conn.Write(make([]byte, 100))
	assert.Error(t, err)

	assert.Equal(t, "", conn.GetStreamID())
	assert.Equal(t, ConnectionStats{}, conn.GetStats())

	err = conn.SetMaxBW(1000)
	assert.Error(t, err)

	assert.Equal(t, int64(0), conn.GetMaxBW())
}
