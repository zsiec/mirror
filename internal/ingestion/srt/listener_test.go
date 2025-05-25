package srt

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	srt "github.com/datarhei/gosrt"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/registry"
)

type mockRegistry struct {
	streams map[string]*registry.Stream
}

func (m *mockRegistry) Register(ctx context.Context, stream *registry.Stream) error {
	if m.streams == nil {
		m.streams = make(map[string]*registry.Stream)
	}
	m.streams[stream.ID] = stream
	return nil
}

func (m *mockRegistry) Get(ctx context.Context, streamID string) (*registry.Stream, error) {
	if stream, ok := m.streams[streamID]; ok {
		return stream, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockRegistry) Update(ctx context.Context, stream *registry.Stream) error {
	m.streams[stream.ID] = stream
	return nil
}

func (m *mockRegistry) Unregister(ctx context.Context, streamID string) error {
	delete(m.streams, streamID)
	return nil
}

func (m *mockRegistry) List(ctx context.Context) ([]*registry.Stream, error) {
	var streams []*registry.Stream
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	return streams, nil
}

func (m *mockRegistry) UpdateStatus(ctx context.Context, streamID string, status registry.StreamStatus) error {
	if stream, ok := m.streams[streamID]; ok {
		stream.Status = status
	}
	return nil
}

func (m *mockRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	if stream, ok := m.streams[streamID]; ok {
		stream.LastHeartbeat = time.Now()
	}
	return nil
}

func (m *mockRegistry) UpdateStats(ctx context.Context, streamID string, stats *registry.StreamStats) error {
	if stream, ok := m.streams[streamID]; ok {
		if stats != nil {
			stream.BytesReceived = stats.BytesReceived
			stream.PacketsReceived = stats.PacketsReceived
			stream.PacketsLost = stats.PacketsLost
			stream.Bitrate = stats.Bitrate
		}
	}
	return nil
}

func (m *mockRegistry) Heartbeat(ctx context.Context, streamID string) error {
	return nil
}

func (m *mockRegistry) Delete(ctx context.Context, streamID string) error {
	return m.Unregister(ctx, streamID)
}

func (m *mockRegistry) Close() error {
	return nil
}

func TestListener_NewListener(t *testing.T) {
	cfg := &config.SRTConfig{
		ListenAddr: "127.0.0.1",
		Port:       1234,
	}
	
	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}
	
	logger := logrus.New()
	registry := &mockRegistry{}
	
	listener := NewListener(cfg, codecsCfg, registry, logger)
	
	assert.NotNil(t, listener)
	assert.Equal(t, cfg, listener.config)
	assert.Equal(t, codecsCfg, listener.codecsConfig)
	assert.NotNil(t, listener.ctx)
	assert.NotNil(t, listener.cancel)
}

func TestListener_ValidateStreamID(t *testing.T) {
	listener := &Listener{
		logger: logrus.New(),
	}
	
	tests := []struct {
		name     string
		streamID string
		wantErr  bool
	}{
		{
			name:     "valid stream ID",
			streamID: "test-stream-123",
			wantErr:  false,
		},
		{
			name:     "valid with underscore",
			streamID: "test_stream_123",
			wantErr:  false,
		},
		{
			name:     "empty stream ID",
			streamID: "",
			wantErr:  true,
		},
		{
			name:     "too long",
			streamID: "this-is-a-very-long-stream-id-that-exceeds-the-maximum-allowed-length-of-64-characters",
			wantErr:  true,
		},
		{
			name:     "invalid characters",
			streamID: "test@stream#123",
			wantErr:  true,
		},
		{
			name:     "starts with hyphen",
			streamID: "-test-stream",
			wantErr:  true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := listener.validateStreamID(tt.streamID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestListener_GetActiveSessions(t *testing.T) {
	listener := &Listener{
		connections: sync.Map{},
	}
	
	// Initially no connections
	assert.Equal(t, 0, listener.GetActiveSessions())
	
	// Add some connections
	listener.connections.Store("conn1", &Connection{})
	listener.connections.Store("conn2", &Connection{})
	listener.connections.Store("conn3", &Connection{})
	
	assert.Equal(t, 3, listener.GetActiveSessions())
	
	// Remove one
	listener.connections.Delete("conn2")
	assert.Equal(t, 2, listener.GetActiveSessions())
}

func TestListener_ConfigureEncryption(t *testing.T) {
	tests := []struct {
		name    string
		config  *config.SRTConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid encryption config",
			config: &config.SRTConfig{
				Encryption: config.SRTEncryption{
					Enabled:    true,
					Passphrase: "validpassphrase123",
					KeyLength:  256,
				},
			},
			wantErr: false,
		},
		{
			name: "passphrase too short",
			config: &config.SRTConfig{
				Encryption: config.SRTEncryption{
					Enabled:    true,
					Passphrase: "short",
					KeyLength:  256,
				},
			},
			wantErr: true,
			errMsg:  "SRT encryption passphrase must be at least 10 characters long",
		},
		{
			name: "invalid key length",
			config: &config.SRTConfig{
				Encryption: config.SRTEncryption{
					Enabled:    true,
					Passphrase: "validpassphrase123",
					KeyLength:  512,
				},
			},
			wantErr: true,
			errMsg:  "invalid SRT encryption key length: 512",
		},
		{
			name: "auto key length (0)",
			config: &config.SRTConfig{
				Encryption: config.SRTEncryption{
					Enabled:    true,
					Passphrase: "validpassphrase123",
					KeyLength:  0,
				},
			},
			wantErr: false,
		},
		{
			name: "key length 128",
			config: &config.SRTConfig{
				Encryption: config.SRTEncryption{
					Enabled:    true,
					Passphrase: "validpassphrase123",
					KeyLength:  128,
				},
			},
			wantErr: false,
		},
		{
			name: "key length 192",
			config: &config.SRTConfig{
				Encryption: config.SRTEncryption{
					Enabled:    true,
					Passphrase: "validpassphrase123",
					KeyLength:  192,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener := &Listener{
				config: tt.config,
				logger: logrus.New(),
			}
			
			// Create a mock srt.Config
			srtCfg := &srt.Config{}
			
			err := listener.configureEncryption(srtCfg)
			
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
				// Verify passphrase was set
				assert.Equal(t, tt.config.Encryption.Passphrase, srtCfg.Passphrase)
				// Verify key length was set if specified
				if tt.config.Encryption.KeyLength > 0 {
					assert.Equal(t, tt.config.Encryption.KeyLength, srtCfg.PBKeylen)
				}
			}
		})
	}
}
