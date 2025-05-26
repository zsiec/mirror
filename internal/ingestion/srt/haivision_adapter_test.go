package srt

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewHaivisionAdapter(t *testing.T) {
	adapter := NewHaivisionAdapter()
	assert.NotNil(t, adapter, "adapter should not be nil")
	assert.IsType(t, &HaivisionAdapter{}, adapter, "should return HaivisionAdapter type")
}

func TestHaivisionAdapter_NewListener(t *testing.T) {
	adapter := NewHaivisionAdapter()
	
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
					Passphrase: "test123",
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
				assert.IsType(t, &HaivisionListener{}, listener, "should return HaivisionListener type")
				
				// Verify listener has correct properties
				hvListener := listener.(*HaivisionListener)
				assert.Equal(t, tt.address, hvListener.address, "address should match")
				assert.Equal(t, uint16(tt.port), hvListener.port, "port should match")
				assert.Equal(t, tt.config, hvListener.config, "config should match")
			}
		})
	}
}

func TestHaivisionAdapter_NewConnection(t *testing.T) {
	adapter := NewHaivisionAdapter()

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
			socket:      &testMockSRTSocket{}, // Not a HaivisionSocket
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

// Mock socket for testing (not a HaivisionSocket)
type testMockSRTSocket struct{}

func (m *testMockSRTSocket) Close() error                              { return nil }
func (m *testMockSRTSocket) GetStreamID() string                       { return "" }
func (m *testMockSRTSocket) SetRejectReason(reason RejectionReason) error { return nil }

func TestHaivisionAdapter_Integration(t *testing.T) {
	// Test that the adapter can be created and used together
	adapter := NewHaivisionAdapter()
	
	// Create a listener
	config := Config{
		Latency:      100 * time.Millisecond,
		MaxBandwidth: 1000000,
		PayloadSize:  1316,
	}
	
	listener, err := adapter.NewListener("localhost", 30001, config)
	assert.NoError(t, err, "should create listener successfully")
	assert.NotNil(t, listener, "listener should not be nil")
	
	// Verify listener type and properties
	hvListener, ok := listener.(*HaivisionListener)
	assert.True(t, ok, "listener should be HaivisionListener type")
	assert.Equal(t, "localhost", hvListener.address, "listener address should match")
	assert.Equal(t, uint16(30001), hvListener.port, "listener port should match")
	assert.Equal(t, config, hvListener.config, "listener config should match")
}

func TestHaivisionAdapter_ConfigVariations(t *testing.T) {
	adapter := NewHaivisionAdapter()
	
	// Test various config combinations
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
				Passphrase: "secret123",
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
			
			hvListener := listener.(*HaivisionListener)
			assert.Equal(t, config, hvListener.config, "config should be preserved for config %d", i)
		})
	}
}

func TestHaivisionAdapter_PortRange(t *testing.T) {
	adapter := NewHaivisionAdapter()
	config := Config{}
	
	// Test various port values
	ports := []int{0, 1, 1024, 30000, 65535}
	
	for _, port := range ports {
		t.Run(fmt.Sprintf("port_%d", port), func(t *testing.T) {
			listener, err := adapter.NewListener("localhost", port, config)
			assert.NoError(t, err, "should create listener with port %d", port)
			assert.NotNil(t, listener, "listener should not be nil for port %d", port)
			
			hvListener := listener.(*HaivisionListener)
			assert.Equal(t, uint16(port), hvListener.port, "port should be preserved as uint16")
		})
	}
}

func TestHaivisionAdapter_AddressVariations(t *testing.T) {
	adapter := NewHaivisionAdapter()
	config := Config{}
	
	// Test various address formats
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
			
			hvListener := listener.(*HaivisionListener)
			assert.Equal(t, address, hvListener.address, "address should be preserved")
		})
	}
}
