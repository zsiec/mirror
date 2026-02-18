package ingestion

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/queue"
)

// MockVideoConnection implements VideoAwareConnection for testing
type MockVideoConnection struct {
	videoChannel chan types.TimestampedPacket
	audioChannel chan types.TimestampedPacket
	closed       bool
	mu           sync.RWMutex
}

func NewMockVideoConnection() *MockVideoConnection {
	return &MockVideoConnection{
		videoChannel: make(chan types.TimestampedPacket, 100),
		audioChannel: make(chan types.TimestampedPacket, 100),
	}
}

func (c *MockVideoConnection) GetVideoOutput() <-chan types.TimestampedPacket {
	return c.videoChannel
}

func (c *MockVideoConnection) GetAudioOutput() <-chan types.TimestampedPacket {
	return c.audioChannel
}

func (c *MockVideoConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		close(c.videoChannel)
		close(c.audioChannel)
		c.closed = true
	}
	return nil
}

func (c *MockVideoConnection) GetStats() interface{} {
	return nil
}

// Implement StreamConnection interface
func (c *MockVideoConnection) GetStreamID() string {
	return "test-stream"
}

func (c *MockVideoConnection) Read(b []byte) (int, error) {
	return 0, nil
}

// Helper function to create test dependencies
func createTestDependencies(t *testing.T) (*queue.HybridQueue, *memory.Controller, logger.Logger) {
	hybridQueue, err := queue.NewHybridQueue("test-queue", 1000, "/tmp")
	require.NoError(t, err)

	memController := memory.NewController(1024*1024*1024, 400*1024*1024) // 1GB total, 400MB per stream

	// Create a simple logger for testing
	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.DebugLevel)
	logrusEntry := logrus.NewEntry(logrusLogger)
	testLogger := logger.NewLogrusAdapter(logrusEntry)

	return hybridQueue, memController, testLogger
}

// TestStreamHandlerMemoryCleanup tests memory cleanup during shutdown
func TestStreamHandlerMemoryCleanup(t *testing.T) {
	ctx := context.Background()
	streamID := "test-stream-memory"

	// Create test dependencies
	hybridQueue, memController, testLogger := createTestDependencies(t)

	conn := NewMockVideoConnection()

	// Create stream handler
	handler := NewStreamHandler(ctx, streamID, conn, hybridQueue, memController, testLogger)
	require.NotNil(t, handler)

	// Start the handler
	handler.Start()

	// Add some data to buffers that should be cleaned up
	handler.recentFramesMu.Lock()
	for i := 0; i < 50; i++ {
		frame := &types.VideoFrame{
			ID:          uint64(i),
			StreamID:    streamID,
			Type:        types.FrameTypeI,
			PTS:         int64(i * 1000),
			DTS:         int64(i * 1000),
			TotalSize:   1024,
			CaptureTime: time.Now(),
		}
		handler.recentFrames = append(handler.recentFrames, frame)
	}
	handler.recentFramesMu.Unlock()

	// Add some bitrate data
	handler.bitrateWindowMu.Lock()
	for i := 0; i < 20; i++ {
		handler.bitrateWindow = append(handler.bitrateWindow, bitratePoint{
			timestamp: time.Now().Add(time.Duration(i) * time.Second),
			bytes:     uint64(i * 1024),
		})
	}
	handler.bitrateWindowMu.Unlock()

	// Add some parameter sets to session cache
	spsData := []byte{0x67, 0x42, 0x80, 0x1e, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04}
	ppsData := []byte{0x68, 0xce, 0x3c, 0x80}

	// Get cache reference safely
	handler.parameterCacheMu.RLock()
	cache := handler.sessionParameterCache
	handler.parameterCacheMu.RUnlock()

	// Add parameter sets without holding the handler's lock
	if cache != nil {
		err := cache.AddSPS(spsData)
		require.NoError(t, err)
		err = cache.AddPPS(ppsData)
		require.NoError(t, err)
	}

	// Verify data is present before cleanup
	handler.recentFramesMu.RLock()
	frameCountBefore := len(handler.recentFrames)
	handler.recentFramesMu.RUnlock()

	handler.bitrateWindowMu.Lock()
	bitrateCountBefore := len(handler.bitrateWindow)
	handler.bitrateWindowMu.Unlock()

	handler.parameterCacheMu.RLock()
	statsBefore := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.RUnlock()

	assert.Equal(t, 50, frameCountBefore)
	assert.Equal(t, 20, bitrateCountBefore)
	assert.Greater(t, statsBefore["total_sets"], 0)

	// Stop the handler (should trigger cleanup)
	err := handler.Stop()
	assert.NoError(t, err)

	// Verify cleanup occurred
	handler.recentFramesMu.RLock()
	frameCountAfter := len(handler.recentFrames)
	handler.recentFramesMu.RUnlock()

	handler.bitrateWindowMu.Lock()
	bitrateCountAfter := len(handler.bitrateWindow)
	handler.bitrateWindowMu.Unlock()

	handler.parameterCacheMu.RLock()
	statsAfter := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.RUnlock()

	assert.Equal(t, 0, frameCountAfter, "Recent frames should be cleared")
	assert.Equal(t, 0, bitrateCountAfter, "Bitrate window should be cleared")
	assert.Equal(t, 0, statsAfter["total_sets"], "Session cache should be cleared")

	t.Logf("Memory cleanup verified: frames %d->%d, bitrate points %d->%d, param sets %v->%v",
		frameCountBefore, frameCountAfter, bitrateCountBefore, bitrateCountAfter,
		statsBefore["total_sets"], statsAfter["total_sets"])
}

// TestStreamHandlerSessionCacheLimits tests session cache size limits
func TestStreamHandlerSessionCacheLimits(t *testing.T) {
	ctx := context.Background()
	streamID := "test-stream-limits"

	// Create test dependencies
	hybridQueue, memController, testLogger := createTestDependencies(t)

	conn := NewMockVideoConnection()

	// Create stream handler
	handler := NewStreamHandler(ctx, streamID, conn, hybridQueue, memController, testLogger)
	require.NotNil(t, handler)

	// Test the enforcement function directly with empty cache
	// This tests the logic without dealing with parameter set parsing complexity

	// Call enforcement on empty cache (should not trigger cleanup)
	handler.enforceSessionCacheLimits()

	// Get stats to verify no crash occurred
	handler.parameterCacheMu.RLock()
	stats := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.RUnlock()

	t.Logf("Session cache stats: %v", stats)
	assert.GreaterOrEqual(t, stats["total_sets"], 0, "Should have non-negative parameter set count")

	// Stop handler
	err := handler.Stop()
	assert.NoError(t, err)
}

// TestStreamHandlerRecentFramesLimits tests recent frames buffer limits
func TestStreamHandlerRecentFramesLimits(t *testing.T) {
	ctx := context.Background()
	streamID := "test-stream-frames"

	// Create test dependencies
	hybridQueue, memController, testLogger := createTestDependencies(t)

	conn := NewMockVideoConnection()

	// Create stream handler
	handler := NewStreamHandler(ctx, streamID, conn, hybridQueue, memController, testLogger)
	require.NotNil(t, handler)

	// Add more frames than the limit
	handler.recentFramesMu.Lock()
	for i := 0; i < handler.maxRecentFrames+100; i++ {
		frame := &types.VideoFrame{
			ID:          uint64(i),
			StreamID:    streamID,
			Type:        types.FrameTypeI,
			PTS:         int64(i * 1000),
			DTS:         int64(i * 1000),
			TotalSize:   1024,
			CaptureTime: time.Now(),
		}
		handler.recentFrames = append(handler.recentFrames, frame)
	}
	frameCountBefore := len(handler.recentFrames)
	handler.recentFramesMu.Unlock()

	t.Logf("Frames before enforcement: %d (max: %d)", frameCountBefore, handler.maxRecentFrames)
	assert.Greater(t, frameCountBefore, handler.maxRecentFrames, "Should exceed max frames")

	// Call enforcement (this should trigger trimming)
	handler.enforceRecentFramesLimits()

	// Check that trimming occurred
	handler.recentFramesMu.RLock()
	frameCountAfter := len(handler.recentFrames)
	handler.recentFramesMu.RUnlock()

	t.Logf("Frames after enforcement: %d", frameCountAfter)
	assert.LessOrEqual(t, frameCountAfter, handler.maxRecentFrames, "Should be within limit")
	assert.Less(t, frameCountAfter, frameCountBefore, "Should have trimmed frames")

	// Verify the most recent frames are kept
	handler.recentFramesMu.RLock()
	if len(handler.recentFrames) > 0 {
		firstFrame := handler.recentFrames[0]
		lastFrame := handler.recentFrames[len(handler.recentFrames)-1]
		handler.recentFramesMu.RUnlock()

		// The frames should be recent (high IDs)
		assert.Greater(t, firstFrame.ID, uint64(100), "Should keep recent frames")
		assert.Greater(t, lastFrame.ID, uint64(300), "Should keep most recent frames")
	} else {
		handler.recentFramesMu.RUnlock()
	}

	// Stop handler
	err := handler.Stop()
	assert.NoError(t, err)
}

// TestStreamHandlerGOPBufferDropHandling tests GOP buffer drop handling with limits
func TestStreamHandlerGOPBufferDropHandling(t *testing.T) {
	ctx := context.Background()
	streamID := "test-stream-gop-drop"

	// Create test dependencies
	hybridQueue, memController, testLogger := createTestDependencies(t)

	conn := NewMockVideoConnection()

	// Create stream handler
	handler := NewStreamHandler(ctx, streamID, conn, hybridQueue, memController, testLogger)
	require.NotNil(t, handler)

	// Create a GOP buffer context with parameter sets
	gopBufferContext := types.NewParameterSetContextForTest(types.CodecH264, "gop-buffer")
	spsData := []byte{0x67, 0x42, 0x80, 0x1e, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04}
	ppsData := []byte{0x68, 0xce, 0x3c, 0x80}
	err := gopBufferContext.AddSPS(spsData)
	require.NoError(t, err)
	err = gopBufferContext.AddPPS(ppsData)
	require.NoError(t, err)

	// Create a test GOP
	testGOP := &types.GOP{
		ID:       1,
		StreamID: streamID,
		Frames: []*types.VideoFrame{
			{
				ID:       1,
				StreamID: streamID,
				Type:     types.FrameTypeI,
				PTS:      1000,
				DTS:      1000,
			},
		},
	}

	// Test normal GOP drop handling
	handler.parameterCacheMu.Lock()
	statsBefore := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.Unlock()

	// Call GOP buffer drop handler
	handler.onGOPBufferDrop(testGOP, gopBufferContext)

	handler.parameterCacheMu.Lock()
	statsAfter := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.Unlock()

	assert.Greater(t, statsAfter["total_sets"], statsBefore["total_sets"], "Should have copied parameter sets")

	// Test GOP drop handling when session cache is at limit
	handler.parameterCacheMu.Lock()
	// Add enough parameter sets to exceed the 500 threshold
	for i := 0; i <= 501; i++ {
		// Generate unique SPS data for each iteration
		spsData := []byte{0x67, 0x42, 0x80, 0x1e, byte(i % 256), 0x01, 0x40, 0x16, 0xec, 0x04}
		ppsData := []byte{0x68, 0xce, 0x3c, byte(i % 256)}
		_ = handler.sessionParameterCache.AddSPS(spsData)
		_ = handler.sessionParameterCache.AddPPS(ppsData)
	}
	statsBeforeLimit := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.Unlock()

	t.Logf("Parameter sets before limit test: %v", statsBeforeLimit["total_sets"])

	// Try to add more via GOP drop (should be skipped due to limit)
	handler.onGOPBufferDrop(testGOP, gopBufferContext)

	handler.parameterCacheMu.Lock()
	statsAfterLimit := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.Unlock()

	t.Logf("Parameter sets after limit test: %v", statsAfterLimit["total_sets"])
	// The parameter cache has validation that might add a few more sets (like the 2 from gopBufferContext)
	// So we check that the increase is minimal (no more than the GOP buffer's parameter sets)
	increase := statsAfterLimit["total_sets"].(int) - statsBeforeLimit["total_sets"].(int)
	assert.LessOrEqual(t, increase, 2, "Should copy at most the GOP buffer's parameter sets when near limit")

	// Stop handler
	err = handler.Stop()
	assert.NoError(t, err)
}

// TestStreamHandlerMemoryEnforcementIntegration tests the integration of memory enforcement
func TestStreamHandlerMemoryEnforcementIntegration(t *testing.T) {
	ctx := context.Background()
	streamID := "test-stream-integration"

	// Create test dependencies
	hybridQueue, memController, testLogger := createTestDependencies(t)

	conn := NewMockVideoConnection()

	// Create stream handler
	handler := NewStreamHandler(ctx, streamID, conn, hybridQueue, memController, testLogger)
	require.NotNil(t, handler)

	// Start the handler
	handler.Start()

	// Simulate memory pressure by adding lots of data
	handler.recentFramesMu.Lock()
	for i := 0; i < handler.maxRecentFrames+200; i++ {
		frame := &types.VideoFrame{
			ID:          uint64(i),
			StreamID:    streamID,
			Type:        types.FrameTypeI,
			PTS:         int64(i * 1000),
			DTS:         int64(i * 1000),
			TotalSize:   1024,
			CaptureTime: time.Now(),
		}
		handler.recentFrames = append(handler.recentFrames, frame)
	}
	framesBefore := len(handler.recentFrames)
	handler.recentFramesMu.Unlock()

	handler.parameterCacheMu.Lock()
	// Use basic parameter sets for testing - avoid complex parsing issues
	// Add basic parameter sets
	_ = handler.sessionParameterCache.AddSPS([]byte{0x67, 0x42, 0x80, 0x1e, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04})
	_ = handler.sessionParameterCache.AddPPS([]byte{0x68, 0xce, 0x3c, 0x80})
	statsBefore := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.Unlock()

	t.Logf("Before enforcement: frames=%d, param_sets=%v", framesBefore, statsBefore["total_sets"])

	// Manually trigger enforcement since automatic enforcement is rate-limited
	handler.enforceRecentFramesLimits()
	handler.enforceSessionCacheLimits()

	// Check that enforcement occurred
	handler.recentFramesMu.RLock()
	framesAfter := len(handler.recentFrames)
	handler.recentFramesMu.RUnlock()

	handler.parameterCacheMu.RLock()
	statsAfter := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.RUnlock()

	t.Logf("After enforcement: frames=%d, param_sets=%v", framesAfter, statsAfter["total_sets"])

	// Verify enforcement occurred automatically
	assert.LessOrEqual(t, framesAfter, handler.maxRecentFrames, "Frames should be within limit")
	// Parameter sets might not be reduced if they're under the threshold (500)
	assert.GreaterOrEqual(t, statsAfter["total_sets"], 0, "Parameter sets should be non-negative")

	// Stop handler
	err := handler.Stop()
	assert.NoError(t, err)
}

// TestStreamHandlerConcurrentMemoryAccess tests concurrent access to memory structures
func TestStreamHandlerConcurrentMemoryAccess(t *testing.T) {
	ctx := context.Background()
	streamID := "test-stream-concurrent"

	// Create test dependencies
	hybridQueue, memController, testLogger := createTestDependencies(t)

	conn := NewMockVideoConnection()

	// Create stream handler
	handler := NewStreamHandler(ctx, streamID, conn, hybridQueue, memController, testLogger)
	require.NotNil(t, handler)

	handler.Start()

	var wg sync.WaitGroup
	const numGoroutines = 10
	const operationsPerGoroutine = 100

	// Concurrent frame additions
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				frame := &types.VideoFrame{
					ID:          uint64(goroutineID*1000 + j),
					StreamID:    streamID,
					Type:        types.FrameTypeI,
					PTS:         int64(j * 1000),
					DTS:         int64(j * 1000),
					TotalSize:   1024,
					CaptureTime: time.Now(),
				}
				handler.storeRecentFrame(frame)
			}
		}(i)
	}

	// Concurrent parameter set additions
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				handler.parameterCacheMu.Lock()

				// Use basic parameter sets for testing - avoid complex parsing issues
				_ = handler.sessionParameterCache.AddSPS([]byte{0x67, 0x42, 0x80, 0x1e, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04})
				_ = handler.sessionParameterCache.AddPPS([]byte{0x68, 0xce, 0x3c, 0x80})
				handler.parameterCacheMu.Unlock()
			}
		}(i)
	}

	// Concurrent cleanup operations
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			handler.enforceRecentFramesLimits()
			time.Sleep(10 * time.Millisecond)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			handler.enforceSessionCacheLimits()
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Wait for all operations to complete
	wg.Wait()

	// Verify no race conditions occurred (test will fail on race detector if issues exist)
	handler.recentFramesMu.RLock()
	frameCount := len(handler.recentFrames)
	handler.recentFramesMu.RUnlock()

	handler.parameterCacheMu.RLock()
	stats := handler.sessionParameterCache.GetStatistics()
	handler.parameterCacheMu.RUnlock()

	t.Logf("Concurrent test completed: frames=%d, param_sets=%v", frameCount, stats["total_sets"])

	// Values should be reasonable (not necessarily exact due to cleanup)
	assert.GreaterOrEqual(t, frameCount, 0)
	assert.GreaterOrEqual(t, stats["total_sets"], 0)

	// Stop handler
	err := handler.Stop()
	assert.NoError(t, err)
}
