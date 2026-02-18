package buffer

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBufferPool_SafeReuse verifies buffers are properly reset before reuse
func TestBufferPool_SafeReuse(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Small pool to force reuse
	pool := NewBufferPool(1024, 2, logger)

	// Write pattern to first buffer
	stream1 := "stream1"
	buf1 := pool.Get(stream1)
	pattern1 := []byte("STREAM1_PATTERN_UNIQUE_DATA")
	_, err := buf1.Write(pattern1)
	require.NoError(t, err)

	// Verify data is there
	readBuf := make([]byte, len(pattern1))
	n, err := buf1.Read(readBuf)
	require.NoError(t, err)
	assert.Equal(t, len(pattern1), n)
	assert.Equal(t, pattern1, readBuf)

	// Return buffer to pool
	pool.Put(stream1, buf1)

	// Get a new buffer (should reuse the previous one)
	stream2 := "stream2"
	buf2 := pool.Get(stream2)

	// Verify buffer is clean (no old data)
	stats := buf2.Stats()
	assert.Equal(t, int64(0), stats.Written, "Buffer should be reset")
	assert.Equal(t, int64(0), stats.Read, "Buffer should be reset")

	// Write different pattern
	pattern2 := []byte("STREAM2_DIFFERENT_PATTERN")
	_, err = buf2.Write(pattern2)
	require.NoError(t, err)

	// Read and verify only new data is present
	readBuf2 := make([]byte, 1024)
	n, err = buf2.Read(readBuf2)
	require.NoError(t, err)
	assert.Equal(t, len(pattern2), n)
	assert.Equal(t, pattern2, readBuf2[:n])

	// Ensure no trace of old pattern
	assert.NotContains(t, readBuf2[:n], pattern1)
}

// TestBufferPool_ConcurrentGetSameStream verifies only one buffer per stream
func TestBufferPool_ConcurrentGetSameStream(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 10, logger)

	streamID := "concurrent-stream"
	numGoroutines := 100
	buffers := make([]*RingBuffer, numGoroutines)
	var wg sync.WaitGroup

	// Many goroutines try to get buffer for same stream
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			buffers[idx] = pool.Get(streamID)
		}(i)
	}

	wg.Wait()

	// All should have gotten the same buffer instance
	firstBuffer := buffers[0]
	for i := 1; i < numGoroutines; i++ {
		assert.Same(t, firstBuffer, buffers[i],
			"All goroutines should get the same buffer instance")
	}
}

// TestBufferPool_MemoryPatternVerification verifies memory corruption detection
func TestBufferPool_MemoryPatternVerification(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 5, logger)

	// Test pattern that would reveal memory issues
	testPattern := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}

	// Use and return multiple buffers
	for i := 0; i < 10; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		buf := pool.Get(streamID)

		// Write test pattern
		_, err := buf.Write(testPattern)
		require.NoError(t, err)

		// Read it back
		readBuf := make([]byte, len(testPattern))
		n, err := buf.Read(readBuf)
		require.NoError(t, err)
		assert.Equal(t, len(testPattern), n)
		assert.Equal(t, testPattern, readBuf)

		// Return to pool
		pool.Put(streamID, buf)
	}

	// Get new buffers and verify they're clean
	for i := 0; i < 5; i++ {
		streamID := fmt.Sprintf("verify-stream-%d", i)
		buf := pool.Get(streamID)

		// Try to read - should timeout (no data)
		readBuf := make([]byte, 100)
		n, err := buf.ReadTimeout(readBuf, time.Millisecond*10)
		assert.Equal(t, ErrTimeout, err, "New buffer should timeout on read")
		assert.Equal(t, 0, n, "New buffer should have no data")

		// Write new data
		newData := []byte(fmt.Sprintf("NEW_DATA_%d", i))
		_, err = buf.Write(newData)
		require.NoError(t, err)

		// Read and verify
		n, err = buf.Read(readBuf)
		require.NoError(t, err)
		assert.Equal(t, len(newData), n)
		assert.Equal(t, newData, readBuf[:n])
	}
}

// TestBufferPool_RaceConditionStress stress tests the pool under high concurrency
func TestBufferPool_RaceConditionStress(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce log noise

	pool := NewBufferPool(4096, 20, logger)

	numStreams := 50
	numOperations := 100
	var streamErrors int32
	var dataErrors int32

	var wg sync.WaitGroup

	// Concurrent stream creators/destroyers
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()

			for op := 0; op < numOperations; op++ {
				streamID := fmt.Sprintf("stress-stream-%d", streamNum)

				// Get buffer
				buf := pool.Get(streamID)

				// Write unique data
				data := []byte(fmt.Sprintf("STREAM_%d_OP_%d_DATA", streamNum, op))
				_, err := buf.Write(data)
				if err != nil {
					atomic.AddInt32(&streamErrors, 1)
					continue
				}

				// Read it back
				readBuf := make([]byte, len(data))
				n, err := buf.Read(readBuf)
				if err != nil || n != len(data) {
					atomic.AddInt32(&streamErrors, 1)
					continue
				}

				// Verify data integrity
				if string(readBuf) != string(data) {
					atomic.AddInt32(&dataErrors, 1)
				}

				// Sometimes return to pool, sometimes just remove
				if op%3 == 0 {
					pool.Put(streamID, buf)
				} else if op%3 == 1 {
					pool.Remove(streamID)
				}
				// else: leave it in the pool (will be overwritten next iteration)

				// Small random delay
				time.Sleep(time.Microsecond * time.Duration(op%10))
			}
		}(i)
	}

	wg.Wait()

	// Check results
	assert.Equal(t, int32(0), atomic.LoadInt32(&streamErrors), "Should have no stream errors")
	assert.Equal(t, int32(0), atomic.LoadInt32(&dataErrors), "Should have no data corruption")

	// Verify pool stats
	stats := pool.Stats()
	t.Logf("Final pool stats: Active=%d, Free=%d, Drops=%d",
		stats.ActiveBuffers, stats.FreeBuffers, stats.TotalDrops)
}

// TestBufferPool_BufferLeakPrevention verifies buffers don't leak
func TestBufferPool_BufferLeakPrevention(t *testing.T) {
	logger := logrus.New()
	pool := NewBufferPool(1024, 5, logger)

	// Track initial state
	_ = pool.Stats() // Just to verify it works

	// Create and destroy many streams
	for i := 0; i < 100; i++ {
		streamID := fmt.Sprintf("leak-test-%d", i)
		buf := pool.Get(streamID)

		// Write some data
		_, _ = buf.Write([]byte("test data"))

		// Always clean up
		if i%2 == 0 {
			pool.Put(streamID, buf)
		} else {
			pool.Remove(streamID)
		}
	}

	// Force a small delay for cleanup
	time.Sleep(time.Millisecond * 10)

	// Check final state
	finalStats := pool.Stats()
	assert.Equal(t, 0, finalStats.ActiveBuffers, "All buffers should be released")
	assert.LessOrEqual(t, finalStats.FreeBuffers, pool.poolSize,
		"Free list should not exceed pool size")
}
