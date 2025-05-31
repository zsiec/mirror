package ingestion

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/backpressure"
	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/queue"
)

// TestStreamHandler_BackpressureRace tests for race conditions in backpressure handling
func TestStreamHandler_BackpressureRace(t *testing.T) {
	// Create test components
	streamID := "test-stream-race"
	mockConn := createTestConnection(streamID, []byte("test data"))
	memCtrl := memory.NewController(100*1024*1024, 50*1024*1024)

	// Create queue
	tempDir := t.TempDir()
	q, err := queue.NewHybridQueue(streamID, 1000, tempDir)
	require.NoError(t, err)
	defer q.Close()

	// Create backpressure controller
	bpConfig := backpressure.Config{
		MinRate:        1 * 1024 * 1024,  // 1 Mbps
		MaxRate:        50 * 1024 * 1024, // 50 Mbps
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.7,
		AdjustInterval: 10 * time.Millisecond, // Fast adjustments for testing
		HistorySize:    10,
	}
	bpController := backpressure.NewController(streamID, bpConfig, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))

	// Create handler
	handler := &StreamHandler{
		streamID:         streamID,
		conn:             mockConn,
		frameQueue:       q,
		memoryController: memCtrl,
		bpController:     bpController,
		logger:           logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())),
		started:          true,
	}

	// Initialize context
	ctx, cancel := context.WithCancel(context.Background())
	handler.ctx = ctx
	handler.cancel = cancel
	defer cancel()

	// Initialize lastBackpressure with zero time
	handler.lastBackpressure.Store(time.Time{})

	// Run concurrent backpressure applications
	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	// Track successful applications
	var applications atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Try to apply backpressure
				before := time.Now()
				handler.applyBackpressure()

				// Check if it was actually applied (rate limiting may prevent it)
				if lastBP, ok := handler.lastBackpressure.Load().(time.Time); ok {
					if !lastBP.Before(before) {
						// This application succeeded
						applications.Add(1)
					}
				}

				// Small delay to allow rate limiting to work
				time.Sleep(time.Millisecond)
			}
		}()
	}

	// Run concurrent readers of backpressure state
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Read backpressure state
				isBackpressure := handler.backpressure.Load()

				// Read last backpressure time
				if lastBP, ok := handler.lastBackpressure.Load().(time.Time); ok {
					// Verify consistency: if backpressure is true, lastBP should not be zero
					if isBackpressure && lastBP.IsZero() {
						t.Error("Inconsistent state: backpressure is true but lastBackpressure is zero")
					}
				}

				time.Sleep(time.Millisecond)
			}
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify some applications succeeded (rate limiting will prevent all)
	count := applications.Load()
	assert.Greater(t, count, int32(0), "Some backpressure applications should succeed")
	assert.Less(t, count, int32(goroutines*iterations), "Rate limiting should prevent some applications")
}

// TestStreamHandler_ConcurrentMethodCalls tests concurrent calls to various methods
func TestStreamHandler_ConcurrentMethodCalls(t *testing.T) {
	// Create test components
	streamID := "test-stream-concurrent"
	mockConn := createTestConnection(streamID, []byte("test data"))
	memCtrl := memory.NewController(100*1024*1024, 50*1024*1024)

	// Create queue
	tempDir := t.TempDir()
	q, err := queue.NewHybridQueue(streamID, 1000, tempDir)
	require.NoError(t, err)
	defer q.Close()

	// Create backpressure controller
	bpConfig := backpressure.Config{
		MinRate:        1 * 1024 * 1024,  // 1 Mbps
		MaxRate:        50 * 1024 * 1024, // 50 Mbps
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.7,
		AdjustInterval: 10 * time.Millisecond,
		HistorySize:    10,
	}
	bpController := backpressure.NewController(streamID, bpConfig, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))

	// Create handler with minimal setup
	handler := &StreamHandler{
		streamID:         streamID,
		conn:             mockConn,
		frameQueue:       q,
		memoryController: memCtrl,
		bpController:     bpController,
		logger:           logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())),
		started:          true,
		startTime:        time.Now(),
		bitrateWindow:    make([]bitratePoint, 0, 60),
	}

	// Initialize context
	ctx, cancel := context.WithCancel(context.Background())
	handler.ctx = ctx
	handler.cancel = cancel

	// Initialize atomic values
	handler.lastBackpressure.Store(time.Time{})

	// Start handler
	handler.wg.Add(1)
	go func() {
		defer handler.wg.Done()
		// Simulate some work
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			return
		}
	}()

	// Run concurrent operations
	var wg sync.WaitGroup
	const goroutines = 20

	// Concurrent GetStats calls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = handler.GetStats()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Concurrent backpressure operations
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				handler.applyBackpressure()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Concurrent releaseBackpressure calls
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				handler.releaseBackpressure()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Let operations run
	time.Sleep(50 * time.Millisecond)

	// Stop handler
	cancel()

	// Wait for all operations to complete
	wg.Wait()

	// Stop should not panic or deadlock
	err = handler.Stop()
	assert.NoError(t, err)
}

// TestStreamHandler_BackpressureInitialization tests proper initialization
func TestStreamHandler_BackpressureInitialization(t *testing.T) {
	// Create handler
	handler := &StreamHandler{
		logger: logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())),
	}

	// Initialize lastBackpressure
	handler.lastBackpressure.Store(time.Time{})

	// First call should succeed (no previous time)
	handler.applyBackpressure()

	// Verify time was set
	lastBP, ok := handler.lastBackpressure.Load().(time.Time)
	assert.True(t, ok, "lastBackpressure should contain time.Time")
	assert.False(t, lastBP.IsZero(), "lastBackpressure should be set after first call")

	// Immediate second call should be rate limited
	time.Sleep(10 * time.Millisecond) // Small delay
	beforeSecond := time.Now()
	handler.applyBackpressure()

	// Time should not have changed (rate limited)
	lastBP2, ok := handler.lastBackpressure.Load().(time.Time)
	assert.True(t, ok)
	assert.True(t, lastBP2.Before(beforeSecond), "Second call should be rate limited")

	// After 1 second, should succeed
	time.Sleep(1 * time.Second)
	handler.applyBackpressure()

	lastBP3, ok := handler.lastBackpressure.Load().(time.Time)
	assert.True(t, ok)
	assert.True(t, lastBP3.After(lastBP2), "After 1 second, backpressure should be applied again")
}
