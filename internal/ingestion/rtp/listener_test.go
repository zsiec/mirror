package rtp

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

type mockRegistry struct {
	mu      sync.RWMutex
	streams map[string]*registry.Stream
}

func (m *mockRegistry) Register(ctx context.Context, stream *registry.Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streams == nil {
		m.streams = make(map[string]*registry.Stream)
	}
	m.streams[stream.ID] = stream
	return nil
}

func (m *mockRegistry) Get(ctx context.Context, streamID string) (*registry.Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if stream, ok := m.streams[streamID]; ok {
		return stream, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *mockRegistry) Update(ctx context.Context, stream *registry.Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streams == nil {
		m.streams = make(map[string]*registry.Stream)
	}
	m.streams[stream.ID] = stream
	return nil
}

func (m *mockRegistry) Unregister(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streams, streamID)
	return nil
}

func (m *mockRegistry) List(ctx context.Context) ([]*registry.Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var streams []*registry.Stream
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	return streams, nil
}

func (m *mockRegistry) UpdateStatus(ctx context.Context, streamID string, status registry.StreamStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, ok := m.streams[streamID]; ok {
		stream.Status = status
	}
	return nil
}

func (m *mockRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, ok := m.streams[streamID]; ok {
		stream.LastHeartbeat = time.Now()
	}
	return nil
}

func (m *mockRegistry) UpdateStats(ctx context.Context, streamID string, stats *registry.StreamStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	cfg := &config.RTPConfig{
		ListenAddr: "127.0.0.1",
		Port:       5004,
		RTCPPort:   5005,
		BufferSize: 65536,
	}

	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}

	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	registry := &mockRegistry{}

	listener := NewListener(cfg, codecsCfg, registry, logger)

	assert.NotNil(t, listener)
	assert.Equal(t, cfg, listener.config)
	assert.Equal(t, codecsCfg, listener.codecsConfig)
	assert.NotNil(t, listener.sessions)
	assert.NotNil(t, listener.ctx)
	assert.NotNil(t, listener.cancel)
}

func TestListener_GetActiveSessions(t *testing.T) {
	listener := &Listener{
		sessions: make(map[string]*Session),
	}

	// Initially no sessions
	assert.Equal(t, 0, listener.GetActiveSessions())

	// Add some sessions
	listener.sessions["session1"] = &Session{}
	listener.sessions["session2"] = &Session{}
	listener.sessions["session3"] = &Session{}

	assert.Equal(t, 3, listener.GetActiveSessions())

	// Remove one
	delete(listener.sessions, "session2")
	assert.Equal(t, 2, listener.GetActiveSessions())
}

func TestListener_GetSessionStats(t *testing.T) {
	listener := &Listener{
		sessions: make(map[string]*Session),
	}

	// Add sessions with stats
	listener.sessions["stream1"] = &Session{
		streamID: "stream1",
		stats: &SessionStats{
			PacketsReceived: 100,
			BytesReceived:   10000,
		},
	}

	listener.sessions["stream2"] = &Session{
		streamID: "stream2",
		stats: &SessionStats{
			PacketsReceived: 200,
			BytesReceived:   20000,
		},
	}

	stats := listener.GetSessionStats()

	assert.Len(t, stats, 2)
	assert.Contains(t, stats, "stream1")
	assert.Contains(t, stats, "stream2")
	assert.Equal(t, uint64(100), stats["stream1"].PacketsReceived)
	assert.Equal(t, uint64(200), stats["stream2"].PacketsReceived)
}
