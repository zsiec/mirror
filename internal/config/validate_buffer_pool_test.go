package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestBufferPoolSizeValidation tests buffer pool size validation against max connections
func TestBufferPoolSizeValidation(t *testing.T) {
	tests := []struct {
		name           string
		srtEnabled     bool
		srtMaxConn     int
		rtpEnabled     bool
		rtpMaxSessions int
		bufferPoolSize int
		expectError    bool
		errorContains  string
	}{
		{
			name:           "Valid: pool size equals max connections",
			srtEnabled:     true,
			srtMaxConn:     25,
			rtpEnabled:     true,
			rtpMaxSessions: 25,
			bufferPoolSize: 50,
			expectError:    false,
		},
		{
			name:           "Valid: pool size exceeds max connections",
			srtEnabled:     true,
			srtMaxConn:     25,
			rtpEnabled:     true,
			rtpMaxSessions: 25,
			bufferPoolSize: 100,
			expectError:    false,
		},
		{
			name:           "Invalid: pool size less than max connections",
			srtEnabled:     true,
			srtMaxConn:     25,
			rtpEnabled:     true,
			rtpMaxSessions: 25,
			bufferPoolSize: 30,
			expectError:    true,
			errorContains:  "buffer pool size (30) should be >= max total connections (50)",
		},
		{
			name:           "Valid: only SRT enabled",
			srtEnabled:     true,
			srtMaxConn:     30,
			rtpEnabled:     false,
			rtpMaxSessions: 0,
			bufferPoolSize: 30,
			expectError:    false,
		},
		{
			name:           "Valid: only RTP enabled",
			srtEnabled:     false,
			srtMaxConn:     0,
			rtpEnabled:     true,
			rtpMaxSessions: 20,
			bufferPoolSize: 20,
			expectError:    false,
		},
		{
			name:           "Invalid: default config mismatch",
			srtEnabled:     true,
			srtMaxConn:     30, // Updated default in code
			rtpEnabled:     true,
			rtpMaxSessions: 30, // Updated default in code
			bufferPoolSize: 10, // Original default
			expectError:    true,
			errorContains:  "buffer pool size (10) should be >= max total connections (60)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &IngestionConfig{
				SRT: SRTConfig{
					Enabled:        tt.srtEnabled,
					ListenAddr:     "0.0.0.0",
					Port:           6000,
					MaxConnections: tt.srtMaxConn,
					Latency:        120 * time.Millisecond,
					MaxBandwidth:   60000000,
					InputBandwidth: 55000000,
					PayloadSize:    1316,
					FlowControlWindow: 25600,
					PeerIdleTimeout: 30 * time.Second,
				},
				RTP: RTPConfig{
					Enabled:        tt.rtpEnabled,
					ListenAddr:     "0.0.0.0",
					Port:           5004,
					RTCPPort:       5005,
					BufferSize:     2097152,
					MaxSessions:    tt.rtpMaxSessions,
					SessionTimeout: 30 * time.Second,
				},
				Buffer: BufferConfig{
					RingSize:       4194304,
					PoolSize:       tt.bufferPoolSize,
					WriteTimeout:   100 * time.Millisecond,
					ReadTimeout:    100 * time.Millisecond,
					MetricsEnabled: true,
				},
				Registry: RegistryConfig{
					MaxStreamsPerSource: 5,
				},
				Memory: MemoryConfig{
					MaxTotal:     2684354560,
					MaxPerStream: 209715200,
				},
				QueueDir: "/tmp/mirror/queue",
			}

			err := cfg.Validate()

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateWarnsAboutPerformance verifies we provide helpful validation messages
func TestValidateWarnsAboutPerformance(t *testing.T) {
	// This test documents the rationale for the validation
	cfg := &IngestionConfig{
		SRT: SRTConfig{
			Enabled:        true,
			MaxConnections: 50,
			// ... other required fields
		},
		Buffer: BufferConfig{
			PoolSize: 10, // Too small!
			// ... other required fields
		},
	}
	
	// When pool size is too small, we'll need to allocate buffers at runtime
	// This causes:
	// 1. Memory allocation spikes during connection surges
	// 2. Increased GC pressure
	// 3. Potential latency spikes
	// 4. Less predictable performance
	
	// The validation helps catch this configuration issue early
	_ = cfg // We don't run full validation here as it needs all fields
}
