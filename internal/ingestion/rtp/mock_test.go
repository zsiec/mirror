package rtp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/registry"
)

// mockRegistry implements registry.Registry for testing
type mockRegistry struct {
	mu      sync.RWMutex
	streams map[string]*registry.Stream
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		streams: make(map[string]*registry.Stream),
	}
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
	if _, ok := m.streams[stream.ID]; !ok {
		return fmt.Errorf("stream not found")
	}
	m.streams[stream.ID] = stream
	return nil
}

func (m *mockRegistry) Unregister(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.streams == nil {
		return nil
	}
	delete(m.streams, streamID)
	return nil
}

func (m *mockRegistry) List(ctx context.Context) ([]*registry.Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var streams []*registry.Stream
	for _, stream := range m.streams {
		streams = append(streams, stream)
	}
	return streams, nil
}

func (m *mockRegistry) UpdateStatus(ctx context.Context, streamID string, status registry.StreamStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, ok := m.streams[streamID]; ok {
		stream.Status = status
		return nil
	}
	return fmt.Errorf("stream not found")
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
