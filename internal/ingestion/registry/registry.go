package registry

import (
	"context"
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
