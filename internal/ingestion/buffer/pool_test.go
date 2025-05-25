package buffer

import (
	"fmt"
	"sync"
	"testing"

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
