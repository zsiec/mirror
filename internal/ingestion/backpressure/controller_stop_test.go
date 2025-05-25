package backpressure

import (
	"runtime"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControllerStopStopsGoroutine verifies the control loop goroutine stops
func TestControllerStopStopsGoroutine(t *testing.T) {
	logger := logrus.New()

	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	config := Config{
		MinRate:        1000,
		MaxRate:        10000,
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.9,
		AdjustInterval: 10 * time.Millisecond,
		HistorySize:    10,
	}

	controller := NewController("test-stream", config, logger)

	// Start should increase goroutine count
	controller.Start()
	time.Sleep(50 * time.Millisecond)

	afterStart := runtime.NumGoroutine()
	assert.Greater(t, afterStart, baseline, "Starting controller should create a goroutine")

	// Stop should decrease goroutine count
	controller.Stop()
	time.Sleep(50 * time.Millisecond)

	afterStop := runtime.NumGoroutine()
	assert.LessOrEqual(t, afterStop, baseline+1, "Stopping controller should stop the goroutine")
}

// TestControllerContextDone verifies context is cancelled on Stop
func TestControllerContextDone(t *testing.T) {
	logger := logrus.New()

	config := Config{
		MinRate:        1000,
		MaxRate:        10000,
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.9,
		AdjustInterval: 10 * time.Millisecond,
		HistorySize:    10,
	}

	controller := NewController("test-stream", config, logger)

	// Context should not be done initially
	select {
	case <-controller.ctx.Done():
		t.Fatal("Context should not be done before Stop")
	default:
		// Good
	}

	// Start and stop
	controller.Start()
	controller.Stop()

	// Context should be done after stop
	select {
	case <-controller.ctx.Done():
		// Good
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Context should be done after Stop")
	}
}

// TestControllerMultipleStartStop verifies multiple start/stop cycles work
func TestControllerMultipleStartStop(t *testing.T) {
	logger := logrus.New()

	config := Config{
		MinRate:        1000,
		MaxRate:        10000,
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.9,
		AdjustInterval: 10 * time.Millisecond,
		HistorySize:    10,
	}

	// Create multiple controllers and ensure they all clean up
	for i := 0; i < 3; i++ {
		controller := NewController("test-stream", config, logger)
		controller.Start()
		time.Sleep(20 * time.Millisecond)
		controller.Stop()
		time.Sleep(20 * time.Millisecond)

		// Verify context is cancelled
		require.Error(t, controller.ctx.Err(), "Context should have error after stop")
	}
}
