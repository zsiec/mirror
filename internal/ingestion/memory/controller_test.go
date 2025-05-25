package memory

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestController_RequestRelease(t *testing.T) {
	ctrl := NewController(1024*1024, 256*1024) // 1MB total, 256KB per stream

	// Request memory
	err := ctrl.RequestMemory("stream1", 100*1024) // 100KB
	require.NoError(t, err)
	assert.Equal(t, int64(100*1024), ctrl.usage.Load())

	// Request more for same stream
	err = ctrl.RequestMemory("stream1", 100*1024) // Another 100KB
	require.NoError(t, err)
	assert.Equal(t, int64(200*1024), ctrl.usage.Load())
	assert.Equal(t, int64(200*1024), ctrl.GetStreamUsage("stream1"))

	// Release memory
	ctrl.ReleaseMemory("stream1", 100*1024)
	assert.Equal(t, int64(100*1024), ctrl.usage.Load())
	assert.Equal(t, int64(100*1024), ctrl.GetStreamUsage("stream1"))
}

func TestController_GlobalLimit(t *testing.T) {
	ctrl := NewController(1024*1024, 2*1024*1024) // 1MB total, 2MB per stream (higher than global)

	// Fill up to global limit
	err := ctrl.RequestMemory("stream1", 1024*1024)
	require.NoError(t, err)

	// Request more should fail with global limit error
	err = ctrl.RequestMemory("stream2", 1024)
	assert.ErrorIs(t, err, ErrGlobalMemoryLimit)
}

func TestController_PerStreamLimit(t *testing.T) {
	ctrl := NewController(1024*1024, 256*1024) // 1MB total, 256KB per stream

	// Request up to stream limit
	err := ctrl.RequestMemory("stream1", 256*1024)
	require.NoError(t, err)

	// Request more for same stream should fail
	err = ctrl.RequestMemory("stream1", 1024)
	assert.ErrorIs(t, err, ErrStreamMemoryLimit)

	// Different stream should work
	err = ctrl.RequestMemory("stream2", 100*1024)
	assert.NoError(t, err)
}

func TestController_Eviction(t *testing.T) {
	ctrl := NewController(1024*1024, 1024*1024) // 1MB total, 1MB per stream

	evictionCalled := false
	evictionStreamID := ""
	evictionBytes := int64(0)

	ctrl.SetEvictionCallback(func(streamID string, bytes int64) {
		evictionCalled = true
		evictionStreamID = streamID
		evictionBytes = bytes
		// Simulate eviction by releasing the stream's memory
		ctrl.ReleaseMemory(streamID, bytes)
	})

	// Fill to 80% (trigger threshold)
	err := ctrl.RequestMemory("stream1", 850*1024)
	require.NoError(t, err)

	// Next request should trigger eviction
	err = ctrl.RequestMemory("stream2", 200*1024)
	assert.NoError(t, err)
	assert.True(t, evictionCalled)
	assert.Equal(t, "stream1", evictionStreamID) // stream1 should be evicted
	assert.Equal(t, int64(850*1024), evictionBytes) // Full amount of stream1
}

func TestController_ConcurrentAccess(t *testing.T) {
	ctrl := NewController(1*1024*1024, 200*1024) // 1MB total, 200KB per stream - tight limits

	var wg sync.WaitGroup
	allocations := &atomic.Int32{}
	errors := &atomic.Int32{}

	// Multiple goroutines requesting/releasing memory
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()
			streamID := string(rune('A' + streamNum))

			for j := 0; j < 50; j++ {
				size := int64(50 * 1024) // 50KB chunks

				if err := ctrl.RequestMemory(streamID, size); err != nil {
					errors.Add(1)
					continue
				}
				
				allocations.Add(1)
				
				// Hold memory briefly to create contention
				time.Sleep(time.Millisecond)
				ctrl.ReleaseMemory(streamID, size)
			}
		}(i)
	}

	wg.Wait()
	
	// Should have successful allocations
	assert.Greater(t, allocations.Load(), int32(0))
	
	// Log stats for debugging
	t.Logf("Allocations: %d, Errors: %d", allocations.Load(), errors.Load())
	t.Logf("Controller stats: %+v", ctrl.Stats())
	
	// With 10 streams * 50 requests * 50KB = 25MB attempted on 1MB limit,
	// we should have errors OR the test should work fine if memory is released fast enough
	// Both outcomes are valid for concurrent access
	
	// But final state should be clean
	assert.Equal(t, int64(0), ctrl.usage.Load())
}

func TestController_Stats(t *testing.T) {
	ctrl := NewController(1024*1024, 256*1024) // 1MB total, 256KB per stream

	// Add some memory usage
	ctrl.RequestMemory("stream1", 100*1024)
	ctrl.RequestMemory("stream2", 150*1024)
	ctrl.ReleaseMemory("stream1", 50*1024)

	stats := ctrl.Stats()

	assert.Equal(t, int64(200*1024), stats.GlobalUsage)
	assert.Equal(t, int64(1024*1024), stats.GlobalLimit)
	assert.InDelta(t, 0.195, stats.GlobalPressure, 0.01)
	assert.Equal(t, 2, stats.ActiveStreams)
	assert.Equal(t, int64(2), stats.AllocationCount)
	assert.Equal(t, int64(1), stats.ReleaseCount)

	// Check stream stats
	assert.Len(t, stats.StreamStats, 2)
	for _, s := range stats.StreamStats {
		if s.StreamID == "stream1" {
			assert.Equal(t, int64(50*1024), s.Usage)
		} else if s.StreamID == "stream2" {
			assert.Equal(t, int64(150*1024), s.Usage)
		}
	}
}

func TestController_ResetStreamUsage(t *testing.T) {
	ctrl := NewController(1024*1024, 256*1024)

	// Add memory usage
	ctrl.RequestMemory("stream1", 100*1024)
	assert.Equal(t, int64(100*1024), ctrl.GetStreamUsage("stream1"))

	// Reset stream
	ctrl.ResetStreamUsage("stream1")
	assert.Equal(t, int64(0), ctrl.GetStreamUsage("stream1"))
	assert.Equal(t, int64(0), ctrl.usage.Load())
}

func TestController_GetPressure(t *testing.T) {
	ctrl := NewController(1024*1024, 512*1024)

	// No usage
	assert.Equal(t, 0.0, ctrl.GetPressure())

	// Half usage
	ctrl.RequestMemory("stream1", 512*1024)
	assert.InDelta(t, 0.5, ctrl.GetPressure(), 0.01)

	// Near limit
	ctrl.RequestMemory("stream2", 400*1024)
	assert.InDelta(t, 0.89, ctrl.GetPressure(), 0.01)
}
