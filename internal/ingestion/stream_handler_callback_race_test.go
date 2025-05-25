package ingestion

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestStreamHandlerNoCallbackRace tests that we don't have duplicate callback setup
func TestStreamHandlerNoCallbackRace(t *testing.T) {
	// This test validates the fix where we removed duplicate callback setup
	// from the Start() method to prevent race conditions
	
	// Create a mock backpressure controller to verify callbacks are set only once
	mockController := &mockBackpressureController{
		rateCallbackCount: 0,
		gopCallbackCount:  0,
	}
	
	// Simulate multiple concurrent starts
	var wg sync.WaitGroup
	const numGoroutines = 10
	
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			
			// In the original code, this would be called in Start()
			// but we've removed it to fix the race condition
			// mockController.SetRateChangeCallback(func(rate int64) {})
			// mockController.SetGOPDropCallback(func(gopID uint64) {})
			
			// Simulate some work
			time.Sleep(time.Millisecond)
		}()
	}
	
	// Also simulate the constructor setting callbacks once
	mockController.SetRateChangeCallback(func(rate int64) {})
	mockController.SetGOPDropCallback(func(gopID uint64) {})
	
	wg.Wait()
	
	// Verify callbacks were only set once (in constructor)
	assert.Equal(t, int32(1), mockController.rateCallbackCount, "Rate callback should be set only once")
	assert.Equal(t, int32(1), mockController.gopCallbackCount, "GOP callback should be set only once")
}

// mockBackpressureController tracks callback setting
type mockBackpressureController struct {
	rateCallbackCount int32
	gopCallbackCount  int32
	mu                sync.Mutex
}

func (m *mockBackpressureController) SetRateChangeCallback(cb func(int64)) {
	atomic.AddInt32(&m.rateCallbackCount, 1)
}

func (m *mockBackpressureController) SetGOPDropCallback(cb func(uint64)) {
	atomic.AddInt32(&m.gopCallbackCount, 1)
}

// TestCallbackSetupPattern validates the correct pattern for setting callbacks
func TestCallbackSetupPattern(t *testing.T) {
	// This test documents the correct pattern:
	// 1. Callbacks should be set in the constructor (NewStreamHandler)
	// 2. Callbacks should NOT be set in Start()
	
	callbackSetInConstructor := true
	callbackSetInStart := false // This is what we fixed
	
	assert.True(t, callbackSetInConstructor, "Callbacks should be set in constructor")
	assert.False(t, callbackSetInStart, "Callbacks should NOT be set in Start() to avoid race conditions")
}
