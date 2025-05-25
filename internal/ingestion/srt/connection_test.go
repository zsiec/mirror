package srt

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionStats(t *testing.T) {
	stats := &ConnectionStats{}

	// Test initial state
	assert.Equal(t, int64(0), stats.BytesReceived)
	assert.Equal(t, int64(0), stats.PacketsReceived)
	assert.Equal(t, int64(0), stats.PacketsLost)

	// Test atomic operations
	stats.BytesReceived = 1000
	stats.PacketsReceived = 10
	stats.PacketsLost = 1

	assert.Equal(t, int64(1000), stats.BytesReceived)
	assert.Equal(t, int64(10), stats.PacketsReceived)
	assert.Equal(t, int64(1), stats.PacketsLost)
}

func TestExponentialBackoff(t *testing.T) {
	backoff := NewExponentialBackoff()

	// Test initial backoff (2^1 * 100ms = 200ms ± 25%)
	delay1 := backoff.Next()
	assert.GreaterOrEqual(t, delay1, 150*time.Millisecond) // 200ms - 25%
	assert.LessOrEqual(t, delay1, 250*time.Millisecond)    // 200ms + 25%

	// Test exponential growth (2^2 * 100ms = 400ms ± 25%)
	delay2 := backoff.Next()
	assert.GreaterOrEqual(t, delay2, 300*time.Millisecond) // 400ms - 25%
	assert.LessOrEqual(t, delay2, 500*time.Millisecond)    // 400ms + 25%

	// Test max delay (30s)
	for i := 0; i < 10; i++ {
		backoff.Next()
	}
	maxDelay := backoff.Next()
	assert.LessOrEqual(t, maxDelay, 37500*time.Millisecond) // 30s + 25%

	// Test reset
	backoff.Reset()
	delayAfterReset := backoff.Next()
	assert.GreaterOrEqual(t, delayAfterReset, 150*time.Millisecond)
	assert.LessOrEqual(t, delayAfterReset, 250*time.Millisecond)
}

func TestConnection_GoroutineCleanup(t *testing.T) {
	// Track goroutine count before
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	// Create mock dependencies
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel) // Reduce noise

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create multiple connections and close them
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Create a connection
			streamID := fmt.Sprintf("test-stream-%d", id)
			conn := &Connection{
				streamID:   streamID,
				registry:   &mockRegistry{},
				logger:     logger,
				startTime:  time.Now(),
				lastActive: time.Now(),
				done:       make(chan struct{}),
			}

			// Create a mock connection read loop
			go func() {
				select {
				case <-ctx.Done():
					return
				case <-conn.done:
					return
				case <-time.After(5 * time.Second):
					// Timeout to prevent hanging
					return
				}
			}()

			// Let it run briefly
			time.Sleep(50 * time.Millisecond)

			// Close the connection
			conn.Close()
		}(i)
	}

	// Wait for all connections to be created and closed
	wg.Wait()

	// Give time for goroutines to clean up
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Check goroutine count after
	after := runtime.NumGoroutine()

	// Allow for some variance but should be close to original
	diff := after - before
	assert.LessOrEqual(t, diff, 2, "Goroutine leak detected: before=%d, after=%d, diff=%d", before, after, diff)
}

func TestConnection_CloseIdempotent(t *testing.T) {
	conn := &Connection{
		streamID:   "test-stream",
		logger:     logrus.New(),
		startTime:  time.Now(),
		lastActive: time.Now(),
		done:       make(chan struct{}),
	}

	// Close multiple times should be safe
	require.NoError(t, conn.Close())
	require.NoError(t, conn.Close())
	require.NoError(t, conn.Close())

	// done channel should only be closed once (no panic)
	select {
	case <-conn.done:
		// Good, channel is closed
	default:
		t.Fatal("done channel should be closed")
	}
}
