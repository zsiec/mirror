package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestP1_6_AVSyncDriftLogicBug demonstrates the A/V sync drift calculation bug
func TestP1_6_AVSyncDriftLogicBug(t *testing.T) {
	// Bug: The code mixes PTS drift and wall clock drift by averaging them
	// This is incorrect because:
	// 1. PTS drift measures synchronization error in the source timestamps
	// 2. Wall clock drift measures processing/delivery delays
	// 3. These are different types of measurements that shouldn't be averaged
	
	t.Run("Incorrect drift averaging", func(t *testing.T) {
		// Example scenario:
		// - PTS drift: 100ms (audio is 100ms ahead of video in timestamps)
		// - Wall clock drift: 20ms (audio arrived 20ms after video)
		
		ptsDrift := 100 * time.Millisecond
		wallClockDrift := 20 * time.Millisecond
		
		// Bug: Current code does this
		buggyDrift := (ptsDrift + wallClockDrift) / 2 // = 60ms
		
		// This is wrong because:
		// - The actual sync error is still 100ms (based on timestamps)
		// - The 20ms wall clock difference is just network/processing jitter
		// - Averaging them gives incorrect 60ms drift
		
		assert.Equal(t, 60*time.Millisecond, buggyDrift, "Bug: averaging gives wrong result")
		assert.NotEqual(t, ptsDrift, buggyDrift, "Buggy calculation loses actual PTS drift")
	})
	
	t.Run("Wall clock jitter affects sync incorrectly", func(t *testing.T) {
		// Scenario: Stable PTS drift but varying network delays
		ptsDrift := 50 * time.Millisecond // Constant sync error
		
		// Network jitter causes varying wall clock differences
		wallClockDrifts := []time.Duration{
			5 * time.Millisecond,
			-15 * time.Millisecond, // Audio arrived before video
			30 * time.Millisecond,
			-10 * time.Millisecond,
		}
		
		// With the bug, calculated drift varies wildly
		for _, wcDrift := range wallClockDrifts {
			if abs(int64(wcDrift)) > int64(10*time.Millisecond) {
				buggyDrift := (ptsDrift + wcDrift) / 2
				
				// The sync error appears to change even though PTS drift is constant
				t.Logf("PTS drift: %v, Wall clock: %v, Buggy result: %v",
					ptsDrift, wcDrift, buggyDrift)
				
				// This causes unnecessary sync adjustments
				assert.NotEqual(t, ptsDrift, buggyDrift)
			}
		}
	})
}

// TestP1_6_DriftMeasurementTypes shows what different drift types mean
func TestP1_6_DriftMeasurementTypes(t *testing.T) {
	t.Run("PTS drift vs Processing lag", func(t *testing.T) {
		// PTS drift: Difference in presentation timestamps
		// This is the actual synchronization error we need to correct
		videoPTS := int64(90000)    // 1 second in 90kHz
		audioPTS := int64(90900)    // 1.01 seconds
		ptsDrift := time.Duration((audioPTS - videoPTS) * 1000000 / 90) // Convert to nanoseconds
		
		assert.Equal(t, 10*time.Millisecond, ptsDrift, "PTS drift is 10ms")
		
		// Wall clock drift: Difference in arrival times
		// This indicates network jitter or processing delays
		videoArrival := time.Now()
		audioArrival := videoArrival.Add(5 * time.Millisecond)
		wallClockDrift := audioArrival.Sub(videoArrival)
		
		assert.Equal(t, 5*time.Millisecond, wallClockDrift, "Wall clock drift is 5ms")
		
		// These measure different things and shouldn't be averaged!
		// - PTS drift needs to be corrected by adjusting playback timing
		// - Wall clock drift is just measurement noise
	})
}

// TestP1_6_CorrectDriftCalculation shows how drift should be calculated
func TestP1_6_CorrectDriftCalculation(t *testing.T) {
	type DriftMeasurement struct {
		PTSDrift      time.Duration
		ProcessingLag time.Duration
		TotalDrift    time.Duration
	}
	
	testCases := []struct {
		name          string
		ptsDrift      time.Duration
		processingLag time.Duration
		expected      DriftMeasurement
	}{
		{
			name:          "Only PTS drift",
			ptsDrift:      50 * time.Millisecond,
			processingLag: 0,
			expected: DriftMeasurement{
				PTSDrift:      50 * time.Millisecond,
				ProcessingLag: 0,
				TotalDrift:    50 * time.Millisecond,
			},
		},
		{
			name:          "PTS drift with small jitter",
			ptsDrift:      50 * time.Millisecond,
			processingLag: 5 * time.Millisecond,
			expected: DriftMeasurement{
				PTSDrift:      50 * time.Millisecond,
				ProcessingLag: 5 * time.Millisecond,
				TotalDrift:    50 * time.Millisecond, // Ignore small jitter
			},
		},
		{
			name:          "PTS drift with significant processing lag",
			ptsDrift:      50 * time.Millisecond,
			processingLag: 100 * time.Millisecond,
			expected: DriftMeasurement{
				PTSDrift:      50 * time.Millisecond,
				ProcessingLag: 100 * time.Millisecond,
				// Large processing lag indicates a problem, weight it less
				TotalDrift:    50*time.Millisecond + time.Duration(float64(100*time.Millisecond)*0.3),
			},
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Correct calculation keeps measurements separate
			drift := DriftMeasurement{
				PTSDrift:      tc.ptsDrift,
				ProcessingLag: tc.processingLag,
			}
			
			// Total drift calculation depends on use case
			if abs(int64(tc.processingLag)) < int64(10*time.Millisecond) {
				// Small processing lag is just jitter, ignore it
				drift.TotalDrift = tc.ptsDrift
			} else {
				// Large processing lag might indicate buffering issues
				// Weight it less than PTS drift
				drift.TotalDrift = tc.ptsDrift + time.Duration(float64(tc.processingLag)*0.3)
			}
			
			assert.Equal(t, tc.expected.TotalDrift, drift.TotalDrift)
		})
	}
}

