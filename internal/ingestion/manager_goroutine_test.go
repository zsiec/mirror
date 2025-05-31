package ingestion

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/registry"
)

func TestManager_HandlerGoroutineTracking(t *testing.T) {
	manager, mr := setupTestManager(t)
	defer mr.Close()

	// Get initial goroutine count
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	ctx := context.Background()

	// Create a mock connection using existing mock
	mockConn := createTestConnection("test-stream-1", []byte("test data"))

	// Register stream in registry first
	stream := &registry.Stream{
		ID:         "test-stream-1",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "H264",
		CreatedAt:  time.Now(),
	}
	err = manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create stream handler
	handler, err := manager.CreateStreamHandler("test-stream-1", mockConn)
	require.NoError(t, err)
	require.NotNil(t, handler)

	// Allow handler to start
	time.Sleep(100 * time.Millisecond)

	// Verify goroutine was created
	afterCreateGoroutines := runtime.NumGoroutine()
	assert.Greater(t, afterCreateGoroutines, initialGoroutines, "Should have created new goroutines")

	// Stop the manager
	err = manager.Stop()
	assert.NoError(t, err)

	// Allow time for cleanup
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Check that goroutines were cleaned up
	finalGoroutines := runtime.NumGoroutine()

	// Should be back to near initial count (allow some variance for runtime goroutines)
	goroutineDiff := finalGoroutines - initialGoroutines
	assert.LessOrEqual(t, goroutineDiff, 2, "Goroutines should be cleaned up after Stop(), diff: %d", goroutineDiff)
}

func TestManager_MultipleHandlersGoroutineTracking(t *testing.T) {
	manager, mr := setupTestManager(t)
	defer mr.Close()

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	ctx := context.Background()
	numStreams := 5

	// Create multiple stream handlers
	for i := 0; i < numStreams; i++ {
		streamID := fmt.Sprintf("test-stream-%d", i)

		// Register stream
		stream := &registry.Stream{
			ID:         streamID,
			Type:       registry.StreamTypeSRT,
			Status:     registry.StatusActive,
			SourceAddr: fmt.Sprintf("192.168.1.%d:1234", i),
			VideoCodec: "H264",
			CreatedAt:  time.Now(),
		}
		err = manager.GetRegistry().Register(ctx, stream)
		require.NoError(t, err)

		// Create mock connection
		mockConn := createTestConnection(streamID, []byte("test data"))

		// Create handler
		handler, err := manager.CreateStreamHandler(streamID, mockConn)
		require.NoError(t, err)
		require.NotNil(t, handler)
	}

	// Allow handlers to start
	time.Sleep(200 * time.Millisecond)

	// Verify all handlers are tracked
	stats := manager.GetStats(ctx)
	assert.Equal(t, numStreams, stats.ActiveHandlers)

	// Stop manager should wait for all handlers
	stopDone := make(chan struct{})
	go func() {
		err := manager.Stop()
		assert.NoError(t, err)
		close(stopDone)
	}()

	// Stop should complete within reasonable time
	select {
	case <-stopDone:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Stop() did not complete in time - possible goroutine leak")
	}
}
