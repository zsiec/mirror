package backpressure

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/logger"
)

func TestController_BasicOperation(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,    // 1KB/s min
		MaxRate:        1000000, // 1MB/s max
		TargetPressure: 0.5,
		IncreaseRatio:  1.2,
		DecreaseRatio:  0.8,
		AdjustInterval: 100 * time.Millisecond,
		HistorySize:    5,
	}

	controller := NewController("test-stream", config, logger)

	// Initial state
	assert.Equal(t, config.MaxRate, controller.GetCurrentRate())
	assert.Equal(t, 0.0, controller.GetPressure())

	// Update pressure
	controller.UpdatePressure(0.3)
	assert.Equal(t, 0.3, controller.GetPressure())
}

func TestController_RateAdjustment(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.2,
		DecreaseRatio:  0.8,
		AdjustInterval: 50 * time.Millisecond,
		HistorySize:    3,
	}

	controller := NewController("test-stream", config, logger)

	// Test rate increase on low pressure
	controller.UpdatePressure(0.2)
	controller.UpdatePressure(0.2)
	controller.UpdatePressure(0.2)

	controller.adjustRate()
	newRate := controller.GetCurrentRate()
	assert.Greater(t, newRate, config.MaxRate/2) // Should increase

	// Test rate decrease on high pressure
	controller.currentRate.Store(100000) // Reset to a middle value
	controller.UpdatePressure(0.8)
	controller.UpdatePressure(0.8)
	controller.UpdatePressure(0.8)

	controller.adjustRate()
	newRate = controller.GetCurrentRate()
	assert.Less(t, newRate, int64(100000)) // Should decrease
}

func TestController_PressureSmoothing(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		HistorySize:    3,
	}

	controller := NewController("test-stream", config, logger)

	// Add pressure readings
	controller.UpdatePressure(0.2)
	controller.UpdatePressure(0.4)
	controller.UpdatePressure(0.8)

	// Smoothed pressure should be weighted average
	// (0.2*1 + 0.4*2 + 0.8*3) / (1+2+3) = 3.4/6 = 0.567
	smoothed := controller.GetSmoothedPressure()
	assert.InDelta(t, 0.567, smoothed, 0.01)
}

func TestController_GOPAwareAdjustment(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.2,
		DecreaseRatio:  0.8,
	}

	controller := NewController("test-stream", config, logger)

	// Set GOP statistics
	gopStats := &gop.GOPStatistics{
		AverageGOPSize:  30,              // 30 frames per GOP
		AverageDuration: 1 * time.Second, // 1 second per GOP
	}
	controller.UpdateGOPStats(gopStats)

	// Test GOP boundary snapping at low rates
	lowRate := int64(40000) // Less than 1 GOP per second
	adjustedRate := controller.applyGOPAdjustments(lowRate, 0.7)

	// Should snap to at least 1 GOP per second
	assert.GreaterOrEqual(t, adjustedRate, int64(45000)) // 30 frames * 1500 bytes
}

func TestController_ExtremePressure(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		DecreaseRatio:  0.8,
	}

	controller := NewController("test-stream", config, logger)

	// Set up GOP stats
	gopStats := &gop.GOPStatistics{
		AverageGOPSize:  30,
		AverageDuration: time.Second,
	}
	controller.UpdateGOPStats(gopStats)

	// Test extreme pressure handling
	controller.currentRate.Store(100000)
	controller.UpdatePressure(0.95)

	// Should recommend dropping GOPs
	shouldDrop := controller.ShouldDropGOP(0.95)
	t.Logf("ShouldDropGOP(0.95) = %v, currentRate=%d, avgGOPSize=%d",
		shouldDrop, controller.currentRate.Load(), controller.avgGOPSize)
	assert.True(t, shouldDrop)
	assert.False(t, controller.ShouldDropGOP(0.8))

	// Test rate decrease under extreme pressure
	newRate := controller.calculateDecreaseRate(100000, 0.95)
	assert.Less(t, newRate, int64(50000)) // Should decrease aggressively
}

func TestController_RateLimits(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        10000,  // 10KB/s
		MaxRate:        100000, // 100KB/s
		TargetPressure: 0.5,
	}

	controller := NewController("test-stream", config, logger)

	// Test max rate enforcement
	rate := controller.enforceRateLimits(200000)
	assert.Equal(t, config.MaxRate, rate)

	// Test min rate enforcement
	rate = controller.enforceRateLimits(5000)
	assert.Equal(t, config.MinRate, rate)

	// Test rate within limits
	rate = controller.enforceRateLimits(50000)
	assert.Equal(t, int64(50000), rate)
}

func TestController_Callbacks(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.5, // Aggressive increase for testing
		DecreaseRatio:  0.5, // Aggressive decrease for testing
	}

	controller := NewController("test-stream", config, logger)

	// Set up callback
	var callbackRate int64
	controller.SetRateChangeCallback(func(newRate int64) {
		callbackRate = newRate
	})

	// Trigger rate change
	controller.currentRate.Store(100000)
	controller.UpdatePressure(0.1) // Very low pressure
	controller.adjustRate()

	// Callback should have been called
	assert.Greater(t, callbackRate, int64(100000))
}

func TestController_Statistics(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.5,
		DecreaseRatio:  0.8,
		HistorySize:    5,
		AdjustInterval: 100 * time.Millisecond,
	}

	controller := NewController("test-stream", config, logger)

	// Update GOP stats first
	gopStats := &gop.GOPStatistics{
		AverageGOPSize:  30,
		AverageDuration: 1 * time.Second,
	}
	controller.UpdateGOPStats(gopStats)

	// Start the controller
	controller.Start()

	// Set initial rate lower than max
	controller.currentRate.Store(50000)

	// Add low pressure reading to trigger rate increase
	controller.UpdatePressure(0.1)

	// Wait for adjustment
	time.Sleep(150 * time.Millisecond)

	// Add more pressure readings
	controller.UpdatePressure(0.4)
	controller.UpdatePressure(0.5)
	controller.UpdatePressure(0.6)

	stats := controller.GetStatistics()
	assert.Greater(t, stats.CurrentRate, int64(50000)) // Should have increased from initial
	assert.Equal(t, 0.6, stats.CurrentPressure)
	assert.Equal(t, 0.5, stats.TargetPressure)
	assert.Greater(t, stats.SmoothedPressure, 0.0)
	assert.Greater(t, stats.AdjustmentCount, uint64(0)) // Should have at least 1 adjustment
}

// TestController_DeadlockPrevention tests the fixes for potential deadlocks
func TestController_DeadlockPrevention(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.2,
		DecreaseRatio:  0.8,
		AdjustInterval: 10 * time.Millisecond, // Fast adjustments
		HistorySize:    10,
	}

	controller := NewController("test-stream", config, logger)
	controller.Start()
	defer controller.Stop()

	var wg sync.WaitGroup
	const numGoroutines = 20
	const numOperations = 100

	// Goroutines continuously updating pressure
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				pressure := 0.1 + 0.8*float64(j%10)/10.0 // Vary between 0.1 and 0.9
				controller.UpdatePressure(pressure)
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Goroutines continuously reading statistics (which calls GetSmoothedPressure)
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				stats := controller.GetStatistics()
				_ = stats // Use the stats to prevent optimization
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Wait for all goroutines with timeout to detect deadlocks
	done := make(chan bool, 1)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Success - no deadlock
		t.Log("No deadlock detected")
	case <-time.After(5 * time.Second):
		t.Fatal("Potential deadlock detected - test timed out")
	}
}

// TestController_TypeSafety tests the type safety fixes
func TestController_TypeSafety(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.2,
		DecreaseRatio:  0.8,
		HistorySize:    5,
	}

	controller := NewController("test-stream", config, logger)

	// Test GetPressure with uninitialized value
	pressure := controller.GetPressure()
	assert.Equal(t, 0.0, pressure, "Should return 0.0 for uninitialized pressure")

	// Test GetSmoothedPressure with uninitialized value
	smoothed := controller.GetSmoothedPressure()
	assert.Equal(t, 0.0, smoothed, "Should return 0.0 for uninitialized smoothed pressure")

	// Test GetStatistics with uninitialized value
	stats := controller.GetStatistics()
	assert.Equal(t, 0.0, stats.CurrentPressure, "Should return 0.0 for uninitialized pressure in stats")

	// Test with valid pressure value
	controller.UpdatePressure(0.8)
	pressure = controller.GetPressure()
	assert.Equal(t, 0.8, pressure, "Should return correct pressure")

	// Test type consistency - atomic.Value requires consistent types
	// We can't store different types after storing float64, so test the safety mechanisms
	stats = controller.GetStatistics()
	assert.Equal(t, 0.8, stats.CurrentPressure, "Should return correct pressure in stats")
	
	// Test edge case with extreme values
	controller.UpdatePressure(1.5) // Over 1.0
	pressure = controller.GetPressure()
	assert.Equal(t, 1.5, pressure, "Should handle values over 1.0")
	
	controller.UpdatePressure(-0.1) // Negative
	pressure = controller.GetPressure()
	assert.Equal(t, -0.1, pressure, "Should handle negative values")
}

// TestController_ConcurrentPressureUpdates tests concurrent pressure updates and rate adjustments
func TestController_ConcurrentPressureUpdates(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.2,
		DecreaseRatio:  0.8,
		AdjustInterval: 5 * time.Millisecond, // Very fast for stress testing
		HistorySize:    20,
	}

	controller := NewController("test-stream", config, logger)
	controller.Start()
	defer controller.Stop()

	var wg sync.WaitGroup
	var totalPressureUpdates atomic.Uint64
	var totalRateChanges atomic.Uint64

	// Callback to count rate changes
	controller.SetRateChangeCallback(func(newRate int64) {
		totalRateChanges.Add(1)
	})

	const numGoroutines = 10
	const numOperations = 200

	// Multiple goroutines updating pressure simultaneously
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			
			for j := 0; j < numOperations; j++ {
				// Create pressure patterns that should trigger adjustments
				var pressure float64
				switch j % 4 {
				case 0:
					pressure = 0.1 // Low pressure - should increase rate
				case 1:
					pressure = 0.3 // Medium-low pressure
				case 2:
					pressure = 0.7 // Medium-high pressure  
				case 3:
					pressure = 0.9 // High pressure - should decrease rate
				}
				
				controller.UpdatePressure(pressure)
				totalPressureUpdates.Add(1)
				
				// Read statistics to exercise the type safety fixes
				stats := controller.GetStatistics()
				require.GreaterOrEqual(t, stats.CurrentPressure, 0.0)
				require.LessOrEqual(t, stats.CurrentPressure, 1.0)
				
				// Small delay to increase concurrency
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	wg.Wait()

	// Allow time for final adjustments
	time.Sleep(100 * time.Millisecond)

	// Verify we processed all updates
	assert.Equal(t, uint64(numGoroutines*numOperations), totalPressureUpdates.Load())
	
	// Should have triggered multiple rate changes
	assert.Greater(t, totalRateChanges.Load(), uint64(0), "Should have triggered rate changes")
	
	// Final state should be consistent
	stats := controller.GetStatistics()
	assert.GreaterOrEqual(t, stats.CurrentRate, config.MinRate)
	assert.LessOrEqual(t, stats.CurrentRate, config.MaxRate)
	assert.GreaterOrEqual(t, stats.CurrentPressure, 0.0)
	assert.LessOrEqual(t, stats.CurrentPressure, 1.0)
}

// TestController_RaceConditionFixed tests that the race condition in getSmoothedPressureUnsafe is fixed
func TestController_RaceConditionFixed(t *testing.T) {
	t.Parallel()
	
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        50000,
		TargetPressure: 0.7,
		HistorySize:    20, // Moderate history size
	}
	
	controller := NewController("test-race-fix", config, logger)
	
	const numGoroutines = 50
	const numOperations = 1000
	
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // writers and readers
	
	// Start writer goroutines that update pressure
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				pressure := 0.1 + float64(id%10)*0.08 // 0.1 to 0.9
				controller.UpdatePressure(pressure)
				if j%100 == 0 {
					time.Sleep(time.Microsecond) // Occasional small delay
				}
			}
		}(i)
	}
	
	// Start reader goroutines that read smoothed pressure
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// This should not race with UpdatePressure after the fix
				smoothed := controller.getSmoothedPressureUnsafe()
				
				// Validate the result is reasonable
				if smoothed < 0.0 || smoothed > 1.0 {
					t.Errorf("Invalid smoothed pressure: %f", smoothed)
				}
				
				if j%100 == 0 {
					time.Sleep(time.Microsecond) // Occasional small delay
				}
			}
		}(i)
	}
	
	// Wait for all goroutines to complete
	wg.Wait()
	
	// If we reach here without the race detector triggering, the fix is working
	t.Logf("Race condition test completed successfully - no data races detected")
	
	// Verify final state is consistent
	stats := controller.GetStatistics()
	assert.GreaterOrEqual(t, stats.CurrentPressure, 0.0)
	assert.LessOrEqual(t, stats.CurrentPressure, 1.0)
	assert.GreaterOrEqual(t, stats.SmoothedPressure, 0.0)
	assert.LessOrEqual(t, stats.SmoothedPressure, 1.0)
}

// TestController_PressureHistoryConsistency tests pressure history under concurrent access
func TestController_PressureHistoryConsistency(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		HistorySize:    100, // Large history to increase contention
	}

	controller := NewController("test-stream", config, logger)

	var wg sync.WaitGroup
	const numWriters = 5
	const numReaders = 10
	const numOperations = 500

	// Writer goroutines
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			
			for j := 0; j < numOperations; j++ {
				pressure := 0.1 + 0.8*float64(j%10)/10.0 // Predictable pattern
				controller.UpdatePressure(pressure)
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Reader goroutines
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			
			for j := 0; j < numOperations; j++ {
				// Test both direct and timeout-based access
				smoothed := controller.GetSmoothedPressure()
				assert.GreaterOrEqual(t, smoothed, 0.0)
				assert.LessOrEqual(t, smoothed, 1.0)
				
				// Also test the unsafe version indirectly through adjustRate
				current := controller.GetPressure()
				smoothedUnsafe := controller.getSmoothedPressureUnsafe()
				
				// Both should be valid values
				assert.GreaterOrEqual(t, current, 0.0)
				assert.GreaterOrEqual(t, smoothedUnsafe, 0.0)
				
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Wait for completion
	wg.Wait()

	// Final consistency check
	finalSmoothed := controller.GetSmoothedPressure()
	assert.GreaterOrEqual(t, finalSmoothed, 0.0)
	assert.LessOrEqual(t, finalSmoothed, 1.0)
}

// TestController_LockTimeoutBehavior tests the timeout behavior in getSmoothedPressureUnsafe
func TestController_LockTimeoutBehavior(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := Config{
		MinRate:        1000,
		MaxRate:        1000000,
		TargetPressure: 0.5,
		HistorySize:    10,
	}

	controller := NewController("test-stream", config, logger)

	// Set a known pressure value
	controller.UpdatePressure(0.7)

	// Hold the lock to force timeout in getSmoothedPressureUnsafe
	controller.mu.Lock()
	
	go func() {
		// Release lock after timeout period
		time.Sleep(20 * time.Millisecond)
		controller.mu.Unlock()
	}()

	// This should timeout and fallback to current pressure
	result := controller.getSmoothedPressureUnsafe()
	
	// Should return the current pressure (0.7) due to timeout fallback
	assert.Equal(t, 0.7, result)
}
