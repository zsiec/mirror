package buffer

import (
	"fmt"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferPool_GetAndPut(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	pool := NewBufferPool(1024, 10, logger)

	// Get a buffer
	streamID := "test-stream-1"
	buffer := pool.Get(streamID)
	assert.NotNil(t, buffer)

	// Verify it's the same buffer on subsequent get
	buffer2 := pool.Get(streamID)
	assert.Same(t, buffer, buffer2)

	// Put it back
	pool.Put(streamID, buffer)

	// Should be able to get a new one with same ID
	buffer3 := pool.Get(streamID)
	assert.NotNil(t, buffer3)
}

func TestBufferPool_Remove(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 10, logger)

	// Get a buffer
	streamID := "test-stream-2"
	buffer := pool.Get(streamID)
	require.NotNil(t, buffer)

	// Write some data
	data := []byte("test data")
	n, err := buffer.Write(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)

	// Remove it
	pool.Remove(streamID)

	// Getting with same ID should return a new buffer
	buffer2 := pool.Get(streamID)
	assert.NotNil(t, buffer2)
	assert.NotSame(t, buffer, buffer2)
}

func TestBufferPool_ConcurrentAccess(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 20, logger)

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent gets and puts
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				streamID := fmt.Sprintf("stream-%d-%d", id, j%5)

				// Get buffer
				buffer := pool.Get(streamID)
				assert.NotNil(t, buffer)

				// Write some data
				data := []byte(fmt.Sprintf("data-%d-%d", id, j))
				buffer.Write(data)

				// Sometimes put it back
				if j%3 == 0 {
					pool.Put(streamID, buffer)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify pool stats
	stats := pool.Stats()
	assert.GreaterOrEqual(t, stats.ActiveBuffers, 0)
	assert.LessOrEqual(t, stats.ActiveBuffers, numGoroutines*5)
}

func TestBufferPool_Stats(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 10, logger)

	// Initial stats
	stats := pool.Stats()
	assert.Equal(t, 0, stats.ActiveBuffers)
	assert.GreaterOrEqual(t, stats.FreeBuffers, 5) // Pre-allocated

	// Get some buffers
	buffers := make(map[string]*RingBuffer)
	for i := 0; i < 3; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		buffer := pool.Get(streamID)
		buffers[streamID] = buffer

		// Write data
		data := []byte(fmt.Sprintf("test data %d", i))
		buffer.Write(data)
	}

	// Check stats after getting buffers
	stats = pool.Stats()
	assert.Equal(t, 3, stats.ActiveBuffers)
	assert.Greater(t, stats.TotalWritten, int64(0))

	// Return one buffer
	pool.Put("stream-0", buffers["stream-0"])

	stats = pool.Stats()
	assert.Equal(t, 2, stats.ActiveBuffers)
}

func TestBufferPool_FreeListManagement(t *testing.T) {
	logger := logrus.New()
	poolSize := 4
	pool := NewBufferPool(1024, poolSize, logger)

	// Get initial free list size
	initialStats := pool.Stats()
	initialFree := initialStats.FreeBuffers

	// Get a buffer from a new stream
	buffer1 := pool.Get("stream-1")
	assert.NotNil(t, buffer1)

	// Stats should show one less free buffer if it came from free list
	stats := pool.Stats()
	if initialFree > 0 {
		assert.Equal(t, initialFree-1, stats.FreeBuffers)
	}

	// Put it back
	pool.Put("stream-1", buffer1)

	// Should go back to free list if there's space
	stats = pool.Stats()
	assert.LessOrEqual(t, stats.FreeBuffers, poolSize)
}

func TestBufferPool_LoadOrStore(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 10, logger)

	var wg sync.WaitGroup
	streamID := "concurrent-stream"
	buffers := make([]*RingBuffer, 10)

	// Multiple goroutines trying to get the same stream
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			buffers[idx] = pool.Get(streamID)
		}(i)
	}

	wg.Wait()

	// All should get the same buffer instance
	for i := 1; i < 10; i++ {
		assert.Same(t, buffers[0], buffers[i], "All goroutines should get the same buffer")
	}
}

// TestBufferPool_RaceConditionFixed specifically tests the race condition that was fixed
func TestBufferPool_RaceConditionFixed(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard) // Suppress logs during testing
	
	pool := NewBufferPool(1024, 10, logger)
	
	const streamID = "race-test-stream"
	const numGoroutines = 100
	
	// Use a barrier to synchronize goroutine start
	startBarrier := make(chan struct{})
	
	var wg sync.WaitGroup
	var bufferInstances sync.Map
	
	wg.Add(numGoroutines)
	
	// Launch many goroutines that try to get the same stream simultaneously
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			
			// Wait for barrier
			<-startBarrier
			
			// Get buffer
			buffer := pool.Get(streamID)
			require.NotNil(t, buffer)
			require.Equal(t, streamID, buffer.streamID)
			
			// Track unique buffer instances
			bufferInstances.Store(id, buffer)
		}(i)
	}
	
	// Release all goroutines simultaneously
	close(startBarrier)
	
	// Wait for completion
	wg.Wait()
	
	// Verify only one unique buffer instance was created
	var uniqueBuffers []*RingBuffer
	bufferInstances.Range(func(key, value interface{}) bool {
		buffer := value.(*RingBuffer)
		
		// Check if this buffer instance is already in our list
		found := false
		for _, existing := range uniqueBuffers {
			if existing == buffer {
				found = true
				break
			}
		}
		
		if !found {
			uniqueBuffers = append(uniqueBuffers, buffer)
		}
		
		return true
	})
	
	assert.Equal(t, 1, len(uniqueBuffers), 
		"Race condition detected: multiple buffer instances created for same stream")
}

// TestBufferPool_MemoryLeakPrevention tests for memory leaks
func TestBufferPool_MemoryLeakPrevention(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	
	pool := NewBufferPool(1024, 5, logger) // Small pool to test overflow
	
	const numStreams = 20 // More than pool size
	
	// Get runtime stats before
	var m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)
	
	// Create many buffers
	buffers := make([]*RingBuffer, numStreams)
	for i := 0; i < numStreams; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		buffers[i] = pool.Get(streamID)
		require.NotNil(t, buffers[i])
	}
	
	stats := pool.Stats()
	assert.Equal(t, numStreams, stats.ActiveBuffers)
	
	// Return all buffers to pool
	for i := 0; i < numStreams; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		pool.Put(streamID, buffers[i])
	}
	
	// Check stats after returning
	stats = pool.Stats()
	assert.Equal(t, 0, stats.ActiveBuffers)
	
	// Force garbage collection and check memory
	runtime.GC()
	runtime.GC() // Run twice to be sure
	
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	// Memory should not have grown significantly
	// This is a heuristic test - we allow some growth but not excessive
	memoryGrowth := int64(m2.Alloc) - int64(m1.Alloc)
	t.Logf("Memory growth: %d bytes", memoryGrowth)
	
	// Should not grow by more than 1MB (very generous threshold)
	assert.Less(t, memoryGrowth, int64(1024*1024), "Potential memory leak detected")
}

// TestBufferPool_ErrorConditions tests error handling
func TestBufferPool_ErrorConditions(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	
	pool := NewBufferPool(1024, 10, logger)
	
	t.Run("PutNilBuffer", func(t *testing.T) {
		assert.NotPanics(t, func() {
			pool.Put("test-stream", nil)
		})
	})
	
	t.Run("RemoveNonExistentStream", func(t *testing.T) {
		assert.NotPanics(t, func() {
			pool.Remove("non-existent-stream")
		})
	})
	
	t.Run("DoubleRemove", func(t *testing.T) {
		const streamID = "test-stream"
		
		// Get a buffer
		buffer := pool.Get(streamID)
		require.NotNil(t, buffer)
		
		// Remove it twice
		pool.Remove(streamID)
		assert.NotPanics(t, func() {
			pool.Remove(streamID) // Second remove should be safe
		})
	})
}

// TestBufferPool_HighConcurrency tests behavior under high concurrent load
func TestBufferPool_HighConcurrency(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	
	pool := NewBufferPool(1024, 20, logger)
	
	const numGoroutines = 50
	const numOperations = 100
	
	var wg sync.WaitGroup
	
	// Test concurrent operations on different streams
	wg.Add(numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			
			for j := 0; j < numOperations; j++ {
				streamID := fmt.Sprintf("stream-%d-%d", goroutineID, j%10)
				
				// Get buffer
				buffer := pool.Get(streamID)
				assert.NotNil(t, buffer)
				assert.Equal(t, streamID, buffer.streamID)
				
				// Write some data
				data := []byte(fmt.Sprintf("data-%d-%d", goroutineID, j))
				buffer.Write(data)
				
				// Randomly put back or remove
				switch j % 3 {
				case 0:
					pool.Put(streamID, buffer)
				case 1:
					pool.Remove(streamID)
				}
				
				// Small delay to increase chance of race conditions
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Pool should be in a consistent state
	stats := pool.Stats()
	assert.GreaterOrEqual(t, stats.ActiveBuffers, 0)
	assert.LessOrEqual(t, stats.FreeBuffers, 20)
}
