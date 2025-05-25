package backpressure

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/ingestion/gop"
)

func TestController_BasicOperation(t *testing.T) {
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
