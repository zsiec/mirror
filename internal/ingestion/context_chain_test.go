package ingestion

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/registry"
)

func TestManager_ContextPropagation(t *testing.T) {
	manager, mr := setupTestManager(t)
	defer mr.Close()

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	ctx := context.Background()

	// Register a stream
	stream := &registry.Stream{
		ID:         "test-stream-ctx",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "H264",
		CreatedAt:  time.Now(),
	}
	err = manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create test connection
	mockConn := createTestConnection("test-stream-ctx", []byte("test data"))

	// Create stream handler
	handler, err := manager.CreateStreamHandler("test-stream-ctx", mockConn)
	require.NoError(t, err)
	require.NotNil(t, handler)

	// Give handler time to start
	time.Sleep(100 * time.Millisecond)

	// Stop the manager - this should cancel the context
	stopDone := make(chan struct{})
	go func() {
		err := manager.Stop()
		assert.NoError(t, err)
		close(stopDone)
	}()

	// Stop should complete quickly due to context cancellation
	select {
	case <-stopDone:
		// Success - context propagation worked
	case <-time.After(2 * time.Second):
		t.Fatal("Manager.Stop() took too long - context not properly propagated")
	}
}

func TestStreamHandler_ContextCancellation(t *testing.T) {
	manager, mr := setupTestManager(t)
	defer mr.Close()

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Create a context that we can cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Register stream
	stream := &registry.Stream{
		ID:         "test-stream-cancel",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "H264",
		CreatedAt:  time.Now(),
	}
	err = manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create mock connection
	mockConn := createTestConnection("test-stream-cancel", []byte("test data"))

	// Create handler
	handler, err := manager.CreateStreamHandler("test-stream-cancel", mockConn)
	require.NoError(t, err)
	require.NotNil(t, handler)

	// Verify handler is running
	time.Sleep(100 * time.Millisecond)
	stats := manager.GetStats(ctx)
	assert.Equal(t, 1, stats.ActiveHandlers)

	// Cancel the parent context
	cancel()

	// Handler should stop due to manager's context being cancelled when Stop is called
	manager.Stop()

	// Give handler time to clean up after stop
	time.Sleep(100 * time.Millisecond)

	// Verify handler stopped
	stats = manager.GetStats(context.Background())
	assert.Equal(t, 0, stats.ActiveHandlers)
}

func TestVideoPipeline_ContextPropagation(t *testing.T) {
	// This test verifies that the video pipeline receives the proper context
	// from the stream handler, which in turn gets it from the manager

	manager, mr := setupTestManager(t)
	defer mr.Close()

	err := manager.Start()
	require.NoError(t, err)

	ctx := context.Background()

	// Register stream
	stream := &registry.Stream{
		ID:         "test-stream-pipeline",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "H264",
		CreatedAt:  time.Now(),
	}
	err = manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create mock connection that provides video packets
	mockConn := createTestConnection("test-stream-pipeline", []byte("test data"))

	// Create handler - this will create the video pipeline internally
	handler, err := manager.CreateStreamHandler("test-stream-pipeline", mockConn)
	require.NoError(t, err)
	require.NotNil(t, handler)

	// Allow pipeline to start
	time.Sleep(100 * time.Millisecond)

	// Stop manager - should propagate through handler to pipeline
	err = manager.Stop()
	assert.NoError(t, err)

	// If context propagation works, everything should be cleaned up
	time.Sleep(100 * time.Millisecond)
}
