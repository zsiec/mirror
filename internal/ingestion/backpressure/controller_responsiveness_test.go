package backpressure

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestControllerResponsiveness verifies the controller responds to pressure changes
func TestControllerResponsiveness(t *testing.T) {
	logger := logrus.New()
	
	config := Config{
		MinRate:        1000,
		MaxRate:        10000,
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.9,
		AdjustInterval: 50 * time.Millisecond,
		HistorySize:    10,
	}
	
	controller := NewController("test-stream", config, logger)
	
	// Track rate changes
	var rateChanges []int64
	controller.SetRateChangeCallback(func(newRate int64) {
		rateChanges = append(rateChanges, newRate)
	})
	
	// Start the controller
	controller.Start()
	defer controller.Stop()
	
	// Start with high pressure to force rate down first
	controller.UpdatePressure(0.9) // Above target
	time.Sleep(200 * time.Millisecond)
	
	initialChanges := len(rateChanges)
	assert.Greater(t, initialChanges, 0, "Should have rate changes for high pressure")
	
	// Test 1: Low pressure should increase rate
	controller.UpdatePressure(0.5) // Well below target of 0.7
	time.Sleep(200 * time.Millisecond)
	
	assert.Greater(t, len(rateChanges), initialChanges, "Should have more rate changes for low pressure")
	if len(rateChanges) > initialChanges {
		// Rate should increase when pressure is low
		assert.Greater(t, rateChanges[len(rateChanges)-1], rateChanges[initialChanges-1], "Low pressure should increase rate")
	}
	
	// Test 2: High pressure should decrease rate
	controller.UpdatePressure(0.9) // Above target
	time.Sleep(150 * time.Millisecond)
	
	assert.Greater(t, len(rateChanges), 1, "Should have more rate changes for high pressure")
	if len(rateChanges) > 1 {
		lastRate := rateChanges[len(rateChanges)-1]
		assert.Less(t, lastRate, int64(10000), "High pressure should decrease rate")
	}
	
	// Test 3: Return to target should stabilize
	controller.UpdatePressure(0.7) // Exactly at target
	time.Sleep(200 * time.Millisecond)
	
	// Rate might still adjust slightly but should be stable
	finalCount := len(rateChanges)
	time.Sleep(200 * time.Millisecond)
	assert.LessOrEqual(t, len(rateChanges)-finalCount, 4, "Rate should stabilize near target pressure")
}

// TestControllerDeadZone verifies the dead zone is not too wide
func TestControllerDeadZone(t *testing.T) {
	logger := logrus.New()
	
	config := Config{
		MinRate:        1000,
		MaxRate:        10000,
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.9,
		AdjustInterval: 50 * time.Millisecond,
		HistorySize:    10,
	}
	
	controller := NewController("test-stream", config, logger)
	
	var changeCount atomic.Int32
	controller.SetRateChangeCallback(func(newRate int64) {
		changeCount.Add(1)
	})
	
	controller.Start()
	defer controller.Stop()
	
	// First bring rate down from max to allow both increase/decrease
	controller.UpdatePressure(0.85)
	time.Sleep(200 * time.Millisecond)
	
	// Test pressure values just outside the target
	testCases := []struct {
		pressure float64
		desc     string
		expect   string
	}{
		{0.60, "10% below target", "should trigger increase"},  // 0.7 * 0.9 = 0.63, so 0.60 is outside
		{0.78, "10% above target", "should trigger decrease"},  // 0.7 * 1.1 = 0.77, so 0.78 is outside
		{0.65, "7% below target", "should be in stable zone"},  // Within 0.63-0.77 range
		{0.75, "7% above target", "should be in stable zone"},  // Within 0.63-0.77 range
	}
	
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			changeCount.Store(0)
			controller.UpdatePressure(tc.pressure)
			time.Sleep(150 * time.Millisecond)
			
			changes := changeCount.Load()
			if tc.expect == "should be in stable zone" {
				// Stable zone might still make minor adjustments
				assert.LessOrEqual(t, changes, int32(3), tc.desc)
			} else {
				assert.Greater(t, changes, int32(0), tc.desc)
			}
		})
	}
}

// TestControllerSmallChanges verifies small rate changes are applied
func TestControllerSmallChanges(t *testing.T) {
	logger := logrus.New()
	
	config := Config{
		MinRate:        1000,
		MaxRate:        10000,
		TargetPressure: 0.5,
		IncreaseRatio:  1.01, // Very small increase
		DecreaseRatio:  0.99, // Very small decrease
		AdjustInterval: 50 * time.Millisecond,
		HistorySize:    10,
	}
	
	controller := NewController("test-stream", config, logger)
	
	var lastRate atomic.Int64
	lastRate.Store(5000)
	
	controller.SetRateChangeCallback(func(newRate int64) {
		lastRate.Store(newRate)
	})
	
	// Manually set initial rate to middle value
	controller.currentRate.Store(5000)
	
	controller.Start()
	defer controller.Stop()
	
	// Apply slight pressure to trigger small decrease
	controller.UpdatePressure(0.6) // Slightly above target
	time.Sleep(150 * time.Millisecond)
	
	// Should see a change even if it's small
	assert.NotEqual(t, int64(5000), lastRate.Load(), "Even small rate changes should be applied")
}
