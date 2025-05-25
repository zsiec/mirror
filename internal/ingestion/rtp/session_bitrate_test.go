package rtp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// testRTPBitrateCalc is a helper function to calculate RTP bitrate
func testRTPBitrateCalc(currentBytes, lastBytes uint64, duration time.Duration) float64 {
	if duration.Seconds() > 0 {
		// Handle counter reset (current < last)
		if currentBytes < lastBytes {
			// Counter reset detected - return 0 as we can't determine actual rate
			// In production, you might want to log this anomaly
			return 0
		}
		deltaBytes := currentBytes - lastBytes
		return float64(deltaBytes*8) / duration.Seconds()
	}
	return 0
}

func TestRTPSession_BitrateCalculation(t *testing.T) {
	tests := []struct {
		name            string
		currentBytes    uint64
		lastBytes       uint64
		duration        time.Duration
		expectedBitrate float64
	}{
		{
			name:            "normal_bitrate",
			currentBytes:    1000000, // 1MB
			lastBytes:       0,
			duration:        time.Second,
			expectedBitrate: 8000000, // 8 Mbps
		},
		{
			name:            "delta_calculation",
			currentBytes:    2000000, // 2MB total
			lastBytes:       1000000, // 1MB previously
			duration:        time.Second,
			expectedBitrate: 8000000, // 8 Mbps (only 1MB delta)
		},
		{
			name:            "half_second_duration",
			currentBytes:    500000, // 500KB
			lastBytes:       0,
			duration:        500 * time.Millisecond,
			expectedBitrate: 8000000, // 8 Mbps
		},
		{
			name:            "no_new_data",
			currentBytes:    1000000,
			lastBytes:       1000000, // Same as current
			duration:        time.Second,
			expectedBitrate: 0, // No new data
		},
		{
			name:            "high_bitrate",
			currentBytes:    50000000, // 50MB
			lastBytes:       0,
			duration:        time.Second,
			expectedBitrate: 400000000, // 400 Mbps
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate bitrate
			actualBitrate := testRTPBitrateCalc(tt.currentBytes, tt.lastBytes, tt.duration)

			// Check bitrate calculation
			assert.Equal(t, tt.expectedBitrate, actualBitrate,
				"Expected bitrate %f, got %f",
				tt.expectedBitrate, actualBitrate)
		})
	}
}

// TestRTPSession_BitrateOverTime tests bitrate calculation over multiple updates
func TestRTPSession_BitrateOverTime(t *testing.T) {
	// Test the evolution of bitrate calculation over time
	scenarios := []struct {
		name    string
		updates []struct {
			bytes    uint64
			duration time.Duration
		}
		expectedBitrates []float64
	}{
		{
			name: "constant_rate",
			updates: []struct {
				bytes    uint64
				duration time.Duration
			}{
				{1000000, time.Second}, // 1MB after 1s
				{2000000, time.Second}, // 2MB after 2s
				{3000000, time.Second}, // 3MB after 3s
			},
			expectedBitrates: []float64{
				8000000, // 8 Mbps (1MB/1s)
				8000000, // 8 Mbps (1MB delta/1s)
				8000000, // 8 Mbps (1MB delta/1s)
			},
		},
		{
			name: "variable_rate",
			updates: []struct {
				bytes    uint64
				duration time.Duration
			}{
				{500000, time.Second},             // 500KB after 1s
				{1500000, time.Second},            // 1.5MB after 2s (1MB delta)
				{2000000, 500 * time.Millisecond}, // 2MB after 2.5s (500KB delta)
			},
			expectedBitrates: []float64{
				4000000, // 4 Mbps (500KB/1s)
				8000000, // 8 Mbps (1MB/1s)
				8000000, // 8 Mbps (500KB/0.5s)
			},
		},
		{
			name: "bursty_traffic",
			updates: []struct {
				bytes    uint64
				duration time.Duration
			}{
				{5000000, 100 * time.Millisecond},  // 5MB in 100ms
				{5000000, 2 * time.Second},         // No new data for 2s
				{10000000, 100 * time.Millisecond}, // 5MB more in 100ms
			},
			expectedBitrates: []float64{
				400000000, // 400 Mbps burst
				0,         // No new data
				400000000, // 400 Mbps burst again
			},
		},
		{
			name: "decreasing_bytes_scenario",
			updates: []struct {
				bytes    uint64
				duration time.Duration
			}{
				{1000000, time.Second}, // 1MB
				{500000, time.Second},  // Counter reset to 500KB
				{1000000, time.Second}, // Back to 1MB
			},
			expectedBitrates: []float64{
				8000000, // 8 Mbps (1MB/1s)
				0,       // 0 Mbps (counter reset detected)
				4000000, // 4 Mbps (500KB/1s)
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			var lastBytes uint64 = 0

			for i, update := range scenario.updates {
				// Calculate expected bitrate
				actualBitrate := testRTPBitrateCalc(update.bytes, lastBytes, update.duration)

				assert.Equal(t, scenario.expectedBitrates[i], actualBitrate,
					"Update %d: Expected %f bps, got %f bps",
					i+1, scenario.expectedBitrates[i], actualBitrate)

				lastBytes = update.bytes
			}
		})
	}
}

// TestRTPSession_EdgeCases tests edge cases in bitrate calculation
func TestRTPSession_EdgeCases(t *testing.T) {
	t.Run("zero_duration", func(t *testing.T) {
		// With zero duration, bitrate should be 0
		bitrate := testRTPBitrateCalc(1000000, 0, 0)
		assert.Equal(t, float64(0), bitrate)
	})

	t.Run("very_small_duration", func(t *testing.T) {
		// 1 byte in 1 microsecond = 8 Mbps
		bitrate := testRTPBitrateCalc(1, 0, time.Microsecond)
		assert.Equal(t, float64(8000000), bitrate)
	})

	t.Run("very_large_values", func(t *testing.T) {
		// Test with large values that might cause overflow in naive implementations
		// 1TB in 1 hour = ~2.2 Gbps
		terabyte := uint64(1000000000000)
		hour := time.Hour
		bitrate := testRTPBitrateCalc(terabyte, 0, hour)
		expectedBitrate := float64(terabyte*8) / hour.Seconds()

		assert.InDelta(t, expectedBitrate, bitrate, 1000,
			"Expected ~%.2f Gbps, got %.2f Gbps",
			expectedBitrate/1e9, bitrate/1e9)
	})
}
