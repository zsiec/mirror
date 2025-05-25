package queue

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestHybridQueue_MemoryOnly(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	defer q.Close()
	
	// Enqueue some data (should stay in memory)
	for i := 0; i < 5; i++ {
		data := []byte(string(rune('A' + i)))
		err := q.Enqueue(data)
		require.NoError(t, err)
	}
	
	// Check stats
	stats := q.Stats()
	assert.Equal(t, int64(5), stats.Depth)
	assert.Equal(t, int64(5), stats.MemoryItems)
	assert.Equal(t, int64(0), stats.DiskBytes)
	
	// Dequeue and verify
	for i := 0; i < 5; i++ {
		data, err := q.Dequeue()
		require.NoError(t, err)
		assert.Equal(t, []byte{byte('A' + i)}, data)
	}
}

func TestHybridQueue_DiskOverflow(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 3, tmpDir) // Small memory queue
	require.NoError(t, err)
	defer q.Close()
	
	// Enqueue more than memory can hold
	for i := 0; i < 10; i++ {
		data := []byte(string(rune('0' + i)))
		err := q.Enqueue(data)
		require.NoError(t, err)
	}
	
	// Wait for disk pump to work
	time.Sleep(100 * time.Millisecond)
	
	// Check stats
	stats := q.Stats()
	assert.Equal(t, int64(10), stats.Depth)
	assert.True(t, stats.DiskBytes > 0 || stats.MemoryItems == 10) // Some should be on disk or all pumped to memory
	
	// Dequeue all and verify order
	for i := 0; i < 10; i++ {
		data, err := q.DequeueTimeout(1 * time.Second)
		require.NoError(t, err)
		assert.Equal(t, []byte{byte('0' + i)}, data)
	}
	
	// Final stats
	stats = q.Stats()
	assert.Equal(t, int64(0), stats.Depth)
	assert.Equal(t, int64(0), stats.MemoryItems)
}

func TestHybridQueue_RateLimit(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	defer q.Close()
	
	// Override rate limiter for testing
	q.rateLimiter = rate.NewLimiter(1, 1) // 1 per second, burst 1
	
	// First enqueue should work
	err = q.Enqueue([]byte("test1"))
	require.NoError(t, err)
	
	// Immediate second should be rate limited
	err = q.Enqueue([]byte("test2"))
	assert.ErrorIs(t, err, ErrRateLimited)
	
	// Wait and try again
	time.Sleep(time.Second)
	err = q.Enqueue([]byte("test3"))
	assert.NoError(t, err)
}

func TestHybridQueue_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 5, tmpDir)
	require.NoError(t, err)
	defer q.Close()
	
	var wg sync.WaitGroup
	enqueued := &atomic.Int64{}
	dequeued := &atomic.Int64{}
	
	// Multiple producers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				data := []byte{byte(id), byte(j)}
				if err := q.Enqueue(data); err == nil {
					enqueued.Add(1)
				}
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	
	// Multiple consumers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				// Use timeout to avoid hanging
				if data, err := q.DequeueTimeout(100 * time.Millisecond); err == nil {
					dequeued.Add(1)
					assert.Len(t, data, 2)
				} else {
					// Check if we're done
					if dequeued.Load() >= enqueued.Load() && enqueued.Load() > 0 {
						break
					}
				}
			}
		}()
	}
	
	wg.Wait()
	
	// All enqueued should be dequeued
	assert.Equal(t, enqueued.Load(), dequeued.Load())
	assert.Greater(t, enqueued.Load(), int64(0))
}

func TestHybridQueue_Close(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	
	// Enqueue some data
	q.Enqueue([]byte("test"))
	
	// Dequeue the data first
	data, err := q.Dequeue()
	require.NoError(t, err)
	assert.Equal(t, []byte("test"), data)
	
	// Close
	err = q.Close()
	require.NoError(t, err)
	
	// Operations should fail
	err = q.Enqueue([]byte("fail"))
	assert.ErrorIs(t, err, ErrQueueClosed)
	
	_, err = q.Dequeue()
	assert.ErrorIs(t, err, ErrQueueClosed)
	
	// Overflow file should be removed
	filename := filepath.Join(tmpDir, "test-stream.overflow")
	_, err = os.Stat(filename)
	assert.True(t, os.IsNotExist(err))
}

func TestHybridQueue_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create queue and add data that overflows to disk
	q, err := NewHybridQueue("test-stream", 2, tmpDir)
	require.NoError(t, err)
	
	// Add data
	for i := 0; i < 10; i++ {
		err := q.Enqueue([]byte{byte(i)})
		require.NoError(t, err)
	}
	
	// Ensure some data is on disk
	time.Sleep(50 * time.Millisecond)
	stats := q.Stats()
	t.Logf("Before close - Depth: %d, Memory: %d, Disk: %d", 
		stats.Depth, stats.MemoryItems, stats.DiskBytes)
	
	// Note: We can't easily test persistence across queue instances
	// because the current implementation removes the file on close.
	// In a real system, you might want to support resuming from disk.
	
	q.Close()
}

func TestHybridQueue_LargeMessages(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 2, tmpDir)
	require.NoError(t, err)
	defer q.Close()
	
	// Create large messages
	largeData := make([]byte, 1024*1024) // 1MB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	
	// Enqueue several large messages
	for i := 0; i < 5; i++ {
		err := q.Enqueue(largeData)
		require.NoError(t, err)
	}
	
	// Dequeue and verify
	for i := 0; i < 5; i++ {
		data, err := q.Dequeue()
		require.NoError(t, err)
		assert.Equal(t, largeData, data)
	}
}

func TestHybridQueue_GetPressure(t *testing.T) {
	tmpDir := t.TempDir()
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	defer q.Close()
	
	// Initially no pressure
	assert.Equal(t, 0.0, q.GetPressure())
	
	// Add some items
	for i := 0; i < 5; i++ {
		q.Enqueue([]byte("test"))
	}
	
	// Should show memory pressure
	pressure := q.GetPressure()
	assert.InDelta(t, 0.5, pressure, 0.1)
	
	// Fill memory
	for i := 0; i < 10; i++ {
		q.Enqueue([]byte("test"))
	}
	
	// Pressure should be higher
	pressure = q.GetPressure()
	assert.Greater(t, pressure, 1.0) // Over 1.0 indicates disk usage
}
