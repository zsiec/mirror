package registry

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrStreamNotFound is returned when a stream is not found in the registry
	ErrStreamNotFound = errors.New("stream not found")
)

// Registry defines the interface for stream registry operations
type Registry interface {
	// Register adds a new stream to the registry
	Register(ctx context.Context, stream *Stream) error

	// Unregister removes a stream from the registry
	Unregister(ctx context.Context, streamID string) error

	// Get retrieves a stream by ID
	Get(ctx context.Context, streamID string) (*Stream, error)

	// List returns all active streams
	List(ctx context.Context) ([]*Stream, error)

	// UpdateHeartbeat updates the heartbeat timestamp for a stream
	UpdateHeartbeat(ctx context.Context, streamID string) error

	// UpdateStatus updates the status of a stream
	UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error

	// UpdateStats updates the statistics for a stream
	UpdateStats(ctx context.Context, streamID string, stats *StreamStats) error

	// Delete removes a stream from the registry (alias for Unregister)
	Delete(ctx context.Context, streamID string) error

	// Update updates an existing stream in the registry
	Update(ctx context.Context, stream *Stream) error

	// Close closes any resources held by the registry
	Close() error
}

// MockRegistry is a simple in-memory registry for testing
type MockRegistry struct {
	Streams map[string]*Stream
	mu      sync.RWMutex
}

func (m *MockRegistry) Register(ctx context.Context, stream *Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Streams[stream.ID] = stream
	return nil
}

func (m *MockRegistry) Unregister(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Streams, streamID)
	return nil
}

func (m *MockRegistry) Get(ctx context.Context, streamID string) (*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stream, exists := m.Streams[streamID]
	if !exists {
		return nil, ErrStreamNotFound
	}
	return stream, nil
}

func (m *MockRegistry) List(ctx context.Context) ([]*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	streams := make([]*Stream, 0, len(m.Streams))
	for _, stream := range m.Streams {
		streams = append(streams, stream)
	}
	return streams, nil
}

func (m *MockRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, exists := m.Streams[streamID]; exists {
		stream.LastHeartbeat = time.Now()
	}
	return nil
}

func (m *MockRegistry) UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, exists := m.Streams[streamID]; exists {
		stream.Status = status
		stream.LastHeartbeat = time.Now()
	}
	return nil
}

func (m *MockRegistry) UpdateStats(ctx context.Context, streamID string, stats *StreamStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, exists := m.Streams[streamID]; exists {
		stream.UpdateStats(stats)
		stream.LastHeartbeat = time.Now()
	}
	return nil
}

func (m *MockRegistry) Delete(ctx context.Context, streamID string) error {
	return m.Unregister(ctx, streamID)
}

func (m *MockRegistry) Update(ctx context.Context, stream *Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.Streams[stream.ID]; exists {
		m.Streams[stream.ID] = stream
		return nil
	}
	return ErrStreamNotFound
}

func (m *MockRegistry) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Streams = make(map[string]*Stream)
	return nil
}
