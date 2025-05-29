package memory

import (
	"fmt"
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
	assert.Equal(t, "stream1", evictionStreamID)    // stream1 should be evicted
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

// TestController_AccountingAccuracy tests the fixed double-counting and accounting issues
func TestController_AccountingAccuracy(t *testing.T) {
	ctrl := NewController(1024*1024, 256*1024) // 1MB total, 256KB per stream

	t.Run("StreamLimitRollback", func(t *testing.T) {
		// Request memory that will hit stream limit
		err := ctrl.RequestMemory("stream1", 200*1024)
		require.NoError(t, err)

		// Check initial state
		assert.Equal(t, int64(200*1024), ctrl.usage.Load())
		assert.Equal(t, int64(200*1024), ctrl.GetStreamUsage("stream1"))

		// Request more than stream limit allows (200KB + 100KB > 256KB limit)
		err = ctrl.RequestMemory("stream1", 100*1024)
		assert.ErrorIs(t, err, ErrStreamMemoryLimit)

		// Global and stream usage should remain unchanged after failed request
		assert.Equal(t, int64(200*1024), ctrl.usage.Load(), "Global usage should not change after stream limit error")
		assert.Equal(t, int64(200*1024), ctrl.GetStreamUsage("stream1"), "Stream usage should not change after stream limit error")
	})

	t.Run("ReleaseNonExistentStream", func(t *testing.T) {
		initialGlobalUsage := ctrl.usage.Load()

		// Try to release memory for non-existent stream
		ctrl.ReleaseMemory("nonexistent", 50*1024)

		// Global usage should be unchanged
		assert.Equal(t, initialGlobalUsage, ctrl.usage.Load(), "Global usage should not change when releasing non-existent stream")
	})

	t.Run("ReleaseMoreThanAllocated", func(t *testing.T) {
		// Allocate some memory
		err := ctrl.RequestMemory("stream2", 100*1024)
		require.NoError(t, err)

		initialGlobalUsage := ctrl.usage.Load()

		// Try to release more than allocated
		ctrl.ReleaseMemory("stream2", 200*1024) // More than the 100KB allocated

		// Should only release what was actually allocated
		expectedGlobalUsage := initialGlobalUsage - 100*1024 // Only 100KB should be released
		assert.Equal(t, expectedGlobalUsage, ctrl.usage.Load(), "Should only release what was actually allocated")
		assert.Equal(t, int64(0), ctrl.GetStreamUsage("stream2"), "Stream usage should be zero after over-release")
	})
}

// TestController_ConcurrentAccountingStress tests accounting under high concurrent stress
func TestController_ConcurrentAccountingStress(t *testing.T) {
	ctrl := NewController(10*1024*1024, 1024*1024) // 10MB total, 1MB per stream

	const numGoroutines = 20
	const numOperations = 100

	var wg sync.WaitGroup
	var totalExpectedUsage atomic.Int64

	// Track all successful allocations to verify accounting
	type allocation struct {
		streamID string
		amount   int64
	}
	var successfulAllocations sync.Map

	// Multiple goroutines doing allocations and releases
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			streamID := fmt.Sprintf("stream-%d", goroutineID)
			var allocatedForThisStream int64

			for j := 0; j < numOperations; j++ {
				size := int64(50 * 1024) // 50KB chunks

				if j%2 == 0 {
					// Allocate
					if err := ctrl.RequestMemory(streamID, size); err == nil {
						allocatedForThisStream += size
						totalExpectedUsage.Add(size)
						successfulAllocations.Store(fmt.Sprintf("%s-%d", streamID, j), allocation{streamID, size})
					}
				} else {
					// Release some of what we allocated
					if allocatedForThisStream >= size {
						ctrl.ReleaseMemory(streamID, size)
						allocatedForThisStream -= size
						totalExpectedUsage.Add(-size)
					}
				}
			}

			// Release remaining allocation for this stream
			if allocatedForThisStream > 0 {
				ctrl.ReleaseMemory(streamID, allocatedForThisStream)
				totalExpectedUsage.Add(-allocatedForThisStream)
			}
		}(i)
	}

	wg.Wait()

	// Final global usage should be zero (all memory released)
	assert.Equal(t, int64(0), ctrl.usage.Load(), "All memory should be released after test")
	assert.Equal(t, int64(0), totalExpectedUsage.Load(), "Expected usage tracking should also be zero")

	// All stream usages should be zero
	for i := 0; i < numGoroutines; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		assert.Equal(t, int64(0), ctrl.GetStreamUsage(streamID), "Stream %s should have zero usage", streamID)
	}
}

// TestController_PartialGlobalMemoryFailure tests accounting when global memory allocation partially fails
func TestController_PartialGlobalMemoryFailure(t *testing.T) {
	ctrl := NewController(100*1024, 1024*1024) // Very small global limit (100KB), large per-stream limit

	// Fill up most of global memory
	err := ctrl.RequestMemory("stream1", 80*1024)
	require.NoError(t, err)
	assert.Equal(t, int64(80*1024), ctrl.usage.Load())

	// Try to allocate more than remaining global capacity
	err = ctrl.RequestMemory("stream2", 50*1024) // Would exceed 100KB limit
	assert.ErrorIs(t, err, ErrGlobalMemoryLimit)

	// Global usage should be unchanged
	assert.Equal(t, int64(80*1024), ctrl.usage.Load(), "Global usage should be unchanged after failed allocation")
	assert.Equal(t, int64(0), ctrl.GetStreamUsage("stream2"), "stream2 should have no usage after failed allocation")

	// stream1 should still have its original allocation
	assert.Equal(t, int64(80*1024), ctrl.GetStreamUsage("stream1"), "stream1 usage should be unchanged")
}

// TestController_SimultaneousStreamLimitFailures tests multiple streams hitting limits simultaneously
func TestController_SimultaneousStreamLimitFailures(t *testing.T) {
	ctrl := NewController(10*1024*1024, 100*1024) // 10MB global, 100KB per stream

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var failureCount atomic.Int32

	// Multiple goroutines trying to exceed stream limits simultaneously
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()

			streamID := fmt.Sprintf("stream-%d", streamNum)

			// First allocation should succeed (90KB < 100KB limit)
			if err := ctrl.RequestMemory(streamID, 90*1024); err == nil {
				successCount.Add(1)

				// Second allocation should fail (90KB + 20KB > 100KB limit)
				if err := ctrl.RequestMemory(streamID, 20*1024); err != nil {
					failureCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	// All streams should have succeeded with first allocation
	assert.Equal(t, int32(10), successCount.Load(), "All first allocations should succeed")

	// All second allocations should have failed
	assert.Equal(t, int32(10), failureCount.Load(), "All second allocations should fail")

	// Global usage should be exactly 10 * 90KB = 900KB
	expectedGlobalUsage := int64(10 * 90 * 1024)
	assert.Equal(t, expectedGlobalUsage, ctrl.usage.Load(), "Global usage should be exactly sum of successful allocations")

	// Each stream should have exactly 90KB
	for i := 0; i < 10; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		assert.Equal(t, int64(90*1024), ctrl.GetStreamUsage(streamID), "Stream %s should have exactly 90KB", streamID)
	}
}
