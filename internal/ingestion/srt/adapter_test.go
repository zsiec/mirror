package srt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRejectionReason_Values(t *testing.T) {
	// Test that rejection reasons have expected values
	assert.Equal(t, RejectionReason(0), RejectionReasonUnauthorized)
	assert.Equal(t, RejectionReason(1), RejectionReasonResourceUnavailable) 
	assert.Equal(t, RejectionReason(2), RejectionReasonBadRequest)
}

func TestConnectionStats_Initialization(t *testing.T) {
	stats := ConnectionStats{}
	
	// Test zero initialization
	assert.Equal(t, int64(0), stats.BytesReceived)
	assert.Equal(t, int64(0), stats.BytesSent)
	assert.Equal(t, int64(0), stats.PacketsReceived)
	assert.Equal(t, int64(0), stats.PacketsSent)
	assert.Equal(t, int64(0), stats.PacketsLost)
	assert.Equal(t, int64(0), stats.PacketsRetrans)
	assert.Equal(t, float64(0), stats.RTTMs)
	assert.Equal(t, float64(0), stats.BandwidthMbps)
	assert.Equal(t, float64(0), stats.DeliveryDelayMs)
	assert.Equal(t, time.Duration(0), stats.ConnectionTimeMs)
}

func TestConnectionStats_SetValues(t *testing.T) {
	stats := ConnectionStats{
		BytesReceived:    1024,
		BytesSent:        512,
		PacketsReceived:  100,
		PacketsSent:      50,
		PacketsLost:      2,
		PacketsRetrans:   1,
		RTTMs:           15.5,
		BandwidthMbps:   25.7,
		DeliveryDelayMs: 120.3,
		ConnectionTimeMs: 30 * time.Second,
	}
	
	// Verify all values are set correctly
	assert.Equal(t, int64(1024), stats.BytesReceived)
	assert.Equal(t, int64(512), stats.BytesSent)
	assert.Equal(t, int64(100), stats.PacketsReceived)
	assert.Equal(t, int64(50), stats.PacketsSent)
	assert.Equal(t, int64(2), stats.PacketsLost)
	assert.Equal(t, int64(1), stats.PacketsRetrans)
	assert.Equal(t, 15.5, stats.RTTMs)
	assert.Equal(t, 25.7, stats.BandwidthMbps)
	assert.Equal(t, 120.3, stats.DeliveryDelayMs)
	assert.Equal(t, 30*time.Second, stats.ConnectionTimeMs)
}

func TestConfig_Initialization(t *testing.T) {
	config := Config{}
	
	// Test zero initialization
	assert.Equal(t, "", config.Address)
	assert.Equal(t, 0, config.Port)
	assert.Equal(t, time.Duration(0), config.Latency)
	assert.Equal(t, int64(0), config.MaxBandwidth)
	assert.Equal(t, int64(0), config.InputBandwidth)
	assert.Equal(t, 0, config.PayloadSize)
	assert.Equal(t, 0, config.FlowControlWindow)
	assert.Equal(t, time.Duration(0), config.PeerIdleTimeout)
	assert.Equal(t, 0, config.MaxConnections)
	assert.False(t, config.Encryption.Enabled)
}

func TestConfig_SetValues(t *testing.T) {
	config := Config{
		Address:           "0.0.0.0",
		Port:              30000,
		Latency:           120 * time.Millisecond,
		MaxBandwidth:      50000000,
		InputBandwidth:    60000000,
		PayloadSize:       1316,
		FlowControlWindow: 25600,
		PeerIdleTimeout:   30 * time.Second,
		MaxConnections:    25,
		Encryption: EncryptionConfig{
			Enabled:         true,
			Passphrase:      "secret123",
			KeyLength:       128,
			PBKDFIterations: 2048,
		},
	}
	
	// Verify all values are set correctly
	assert.Equal(t, "0.0.0.0", config.Address)
	assert.Equal(t, 30000, config.Port)
	assert.Equal(t, 120*time.Millisecond, config.Latency)
	assert.Equal(t, int64(50000000), config.MaxBandwidth)
	assert.Equal(t, int64(60000000), config.InputBandwidth)
	assert.Equal(t, 1316, config.PayloadSize)
	assert.Equal(t, 25600, config.FlowControlWindow)
	assert.Equal(t, 30*time.Second, config.PeerIdleTimeout)
	assert.Equal(t, 25, config.MaxConnections)
	
	// Verify encryption config
	assert.True(t, config.Encryption.Enabled)
	assert.Equal(t, "secret123", config.Encryption.Passphrase)
	assert.Equal(t, 128, config.Encryption.KeyLength)
	assert.Equal(t, 2048, config.Encryption.PBKDFIterations)
}

func TestEncryptionConfig_Defaults(t *testing.T) {
	config := EncryptionConfig{}
	
	assert.False(t, config.Enabled)
	assert.Equal(t, "", config.Passphrase)
	assert.Equal(t, 0, config.KeyLength)
	assert.Equal(t, 0, config.PBKDFIterations)
}

func TestEncryptionConfig_ValidConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		config     EncryptionConfig
		expectValid bool
	}{
		{
			name:        "disabled encryption",
			config:      EncryptionConfig{Enabled: false},
			expectValid: true,
		},
		{
			name: "valid encryption config",
			config: EncryptionConfig{
				Enabled:         true,
				Passphrase:      "secret123",
				KeyLength:       128,
				PBKDFIterations: 2048,
			},
			expectValid: true,
		},
		{
			name: "encryption enabled but no passphrase",
			config: EncryptionConfig{
				Enabled:    true,
				Passphrase: "",
			},
			expectValid: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation logic
			valid := !tt.config.Enabled || tt.config.Passphrase != ""
			assert.Equal(t, tt.expectValid, valid)
		})
	}
}

// Test that the Config struct supports realistic SRT configurations
func TestConfig_RealisticConfigurations(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "low latency streaming",
			config: Config{
				Address:           "0.0.0.0",
				Port:              30000,
				Latency:           20 * time.Millisecond,
				MaxBandwidth:      10000000, // 10 Mbps
				InputBandwidth:    12000000, // 12 Mbps
				PayloadSize:       1316,
				FlowControlWindow: 8192,
				PeerIdleTimeout:   10 * time.Second,
				MaxConnections:    10,
			},
		},
		{
			name: "high quality streaming",
			config: Config{
				Address:           "0.0.0.0",
				Port:              30001,
				Latency:           200 * time.Millisecond,
				MaxBandwidth:      100000000, // 100 Mbps
				InputBandwidth:    120000000, // 120 Mbps
				PayloadSize:       1500,
				FlowControlWindow: 65536,
				PeerIdleTimeout:   60 * time.Second,
				MaxConnections:    50,
			},
		},
		{
			name: "encrypted streaming",
			config: Config{
				Address:           "0.0.0.0",
				Port:              30002,
				Latency:           120 * time.Millisecond,
				MaxBandwidth:      50000000,
				PayloadSize:       1316,
				MaxConnections:    25,
				Encryption: EncryptionConfig{
					Enabled:         true,
					Passphrase:      "super-secret-key-2024",
					KeyLength:       256,
					PBKDFIterations: 4096,
				},
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify config can be created and accessed
			config := tt.config
			
			assert.NotEmpty(t, config.Address)
			assert.Greater(t, config.Port, 0)
			assert.GreaterOrEqual(t, config.Latency, time.Duration(0))
			assert.GreaterOrEqual(t, config.MaxBandwidth, int64(0))
			assert.GreaterOrEqual(t, config.MaxConnections, 0)
			
			if config.Encryption.Enabled {
				assert.NotEmpty(t, config.Encryption.Passphrase)
				assert.Greater(t, config.Encryption.KeyLength, 0)
			}
		})
	}
}

// Test ConnectionStats calculations/metrics
func TestConnectionStats_Calculations(t *testing.T) {
	stats := ConnectionStats{
		BytesReceived:   1024000, // 1MB
		PacketsReceived: 1000,
		PacketsLost:     10,
		RTTMs:          15.5,
		BandwidthMbps:  25.0,
	}
	
	// Calculate packet loss rate
	lossRate := float64(stats.PacketsLost) / float64(stats.PacketsReceived) * 100
	assert.InDelta(t, 1.0, lossRate, 0.01) // 1% loss rate
	
	// Calculate average packet size
	avgPacketSize := float64(stats.BytesReceived) / float64(stats.PacketsReceived)
	assert.InDelta(t, 1024.0, avgPacketSize, 0.01) // 1KB average
	
	// Verify bandwidth is reasonable
	assert.Greater(t, stats.BandwidthMbps, 0.0)
	assert.Less(t, stats.RTTMs, 1000.0) // RTT should be reasonable
}
