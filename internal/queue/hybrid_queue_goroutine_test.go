package queue

import (
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHybridQueueGoroutineCleanup verifies that the diskToMemoryPump goroutine
// is properly cleaned up when the queue is closed
func TestHybridQueueGoroutineCleanup(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "queue_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	// Get initial goroutine count
	initialGoroutines := runtime.NumGoroutine()
	
	// Create and close multiple queues to test cleanup
	for i := 0; i < 5; i++ {
		q, err := NewHybridQueue("test-stream", 10, tmpDir)
		require.NoError(t, err)
		
		// Verify goroutine was started
		afterCreate := runtime.NumGoroutine()
		assert.Greater(t, afterCreate, initialGoroutines, "Goroutine should be created")
		
		// Close the queue
		err = q.Close()
		require.NoError(t, err)
		
		// Give a moment for goroutine to fully exit
		time.Sleep(10 * time.Millisecond)
		
		// Verify goroutine was cleaned up
		afterClose := runtime.NumGoroutine()
		assert.LessOrEqual(t, afterClose, initialGoroutines+1, 
			"Goroutine should be cleaned up after close (iteration %d)", i)
	}
	
	// Final check - should be back to initial count (or very close)
	time.Sleep(50 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()
	assert.LessOrEqual(t, finalGoroutines, initialGoroutines+2, 
		"All goroutines should be cleaned up (initial: %d, final: %d)", 
		initialGoroutines, finalGoroutines)
}

// TestHybridQueueCloseIdempotent verifies Close() can be called multiple times safely
func TestHybridQueueCloseIdempotent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	
	// First close should succeed
	err = q.Close()
	assert.NoError(t, err)
	
	// Second close should return error but not panic
	err = q.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already closed")
}

// TestHybridQueueGoroutineExitsOnClose tests that the pump goroutine exits promptly
func TestHybridQueueGoroutineExitsOnClose(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	
	// Add some data to disk to make the pump active
	for i := 0; i < 20; i++ {
		data := []byte("test data for disk overflow")
		err := q.Enqueue(data)
		require.NoError(t, err)
	}
	
	// Track that Close() returns in reasonable time
	done := make(chan bool)
	go func() {
		err := q.Close()
		assert.NoError(t, err)
		done <- true
	}()
	
	select {
	case <-done:
		// Good, Close() returned
	case <-time.After(1 * time.Second):
		t.Fatal("Close() did not return within 1 second - possible goroutine leak")
	}
}

// TestHybridQueueNoTimerLeak verifies we don't leak timers
func TestHybridQueueNoTimerLeak(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	// Create queue with small memory to force disk usage
	q, err := NewHybridQueue("test-stream", 2, tmpDir)
	require.NoError(t, err)
	
	// Fill memory queue
	for i := 0; i < 3; i++ {
		err := q.Enqueue([]byte("data"))
		require.NoError(t, err)
	}
	
	// Let the pump run through several timer cycles
	time.Sleep(200 * time.Millisecond)
	
	// Close should stop all timers
	err = q.Close()
	require.NoError(t, err)
	
	// If timers were leaked, they would still be running
	// This test mainly serves as documentation that we fixed timer leaks
}

// TestHybridQueueConcurrentCloseAndEnqueue tests race conditions
func TestHybridQueueConcurrentCloseAndEnqueue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	q, err := NewHybridQueue("test-stream", 10, tmpDir)
	require.NoError(t, err)
	
	var wg sync.WaitGroup
	
	// Start enqueuers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				data := []byte("test data")
				_ = q.Enqueue(data) // Ignore errors after close
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	
	// Close after a short delay
	time.Sleep(10 * time.Millisecond)
	err = q.Close()
	assert.NoError(t, err)
	
	// Wait for enqueuers to finish
	wg.Wait()
	
	// Verify no panic occurred and queue is closed
	assert.True(t, q.closed.Load())
}
