package registry

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockRegistry is an in-memory implementation of Registry for testing
type MockRegistry struct {
	mu      sync.RWMutex
	streams map[string]*Stream
	closed  bool
}

// NewMockRegistry creates a new mock registry
func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		streams: make(map[string]*Stream),
	}
}

// Register adds a new stream to the registry
func (m *MockRegistry) Register(ctx context.Context, stream *Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry is closed")
	}

	if _, exists := m.streams[stream.ID]; exists {
		return fmt.Errorf("stream %s already exists", stream.ID)
	}

	// Store a copy (excluding mutex) to avoid external modifications
	streamCopy := copyStream(stream)
	streamCopy.CreatedAt = time.Now()
	streamCopy.LastHeartbeat = time.Now()

	m.streams[stream.ID] = streamCopy
	return nil
}

// Unregister removes a stream from the registry
func (m *MockRegistry) Unregister(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry is closed")
	}

	if _, exists := m.streams[streamID]; !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	delete(m.streams, streamID)
	return nil
}

// Get retrieves a stream by ID
func (m *MockRegistry) Get(ctx context.Context, streamID string) (*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, fmt.Errorf("registry is closed")
	}

	stream, exists := m.streams[streamID]
	if !exists {
		return nil, fmt.Errorf("stream %s not found", streamID)
	}

	// Return a copy to avoid external modifications
	return copyStream(stream), nil
}

// List returns all active streams
func (m *MockRegistry) List(ctx context.Context) ([]*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, fmt.Errorf("registry is closed")
	}

	streams := make([]*Stream, 0, len(m.streams))
	for _, stream := range m.streams {
		// Return copies to avoid external modifications
		streams = append(streams, copyStream(stream))
	}

	return streams, nil
}

// UpdateHeartbeat updates the heartbeat timestamp for a stream
func (m *MockRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry is closed")
	}

	stream, exists := m.streams[streamID]
	if !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	stream.LastHeartbeat = time.Now()
	return nil
}

// UpdateStatus updates the status of a stream
func (m *MockRegistry) UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry is closed")
	}

	stream, exists := m.streams[streamID]
	if !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	stream.Status = status
	return nil
}

// UpdateStats updates the statistics for a stream
func (m *MockRegistry) UpdateStats(ctx context.Context, streamID string, stats *StreamStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry is closed")
	}

	stream, exists := m.streams[streamID]
	if !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	if stats != nil {
		stream.BytesReceived = stats.BytesReceived
		stream.PacketsReceived = stats.PacketsReceived
		stream.PacketsLost = stats.PacketsLost
		stream.Bitrate = stats.Bitrate
	}
	return nil
}

// Delete removes a stream from the registry (alias for Unregister)
func (m *MockRegistry) Delete(ctx context.Context, streamID string) error {
	return m.Unregister(ctx, streamID)
}

// Update updates an existing stream in the registry
func (m *MockRegistry) Update(ctx context.Context, stream *Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry is closed")
	}

	if _, exists := m.streams[stream.ID]; !exists {
		return fmt.Errorf("stream %s not found", stream.ID)
	}

	// Update the stream data
	m.streams[stream.ID] = copyStream(stream)
	return nil
}

// Close closes the registry
func (m *MockRegistry) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return fmt.Errorf("registry already closed")
	}

	m.closed = true
	m.streams = nil
	return nil
}

// copyStream creates a copy of a Stream without copying the mutex.
func copyStream(s *Stream) *Stream {
	return &Stream{
		ID:              s.ID,
		Type:            s.Type,
		SourceAddr:      s.SourceAddr,
		Status:          s.Status,
		CreatedAt:       s.CreatedAt,
		LastHeartbeat:   s.LastHeartbeat,
		VideoCodec:      s.VideoCodec,
		Resolution:      s.Resolution,
		Bitrate:         s.Bitrate,
		FrameRate:       s.FrameRate,
		BytesReceived:   s.BytesReceived,
		PacketsReceived: s.PacketsReceived,
		PacketsLost:     s.PacketsLost,
	}
}

// Ensure MockRegistry implements Registry interface
var _ Registry = (*MockRegistry)(nil)
