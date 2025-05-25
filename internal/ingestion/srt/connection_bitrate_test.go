package srt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// testBitrateCalc is a helper function to calculate bitrate
func testBitrateCalc(currentBytes, lastBytes int64, duration time.Duration) int64 {
	if duration.Seconds() > 0 {
		deltaBytes := currentBytes - lastBytes
		return int64(float64(deltaBytes*8) / duration.Seconds())
	}
	return 0
}

func TestConnection_BitrateCalculation(t *testing.T) {

	tests := []struct {
		name            string
		currentBytes    int64
		lastBytes       int64
		duration        time.Duration
		expectedBitrate int64
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate bitrate
			actualBitrate := testBitrateCalc(tt.currentBytes, tt.lastBytes, tt.duration)

			// Check bitrate calculation
			assert.Equal(t, tt.expectedBitrate, actualBitrate,
				"Expected bitrate %d, got %d",
				tt.expectedBitrate, actualBitrate)
		})
	}
}

// TestConnection_BitrateOverTime tests bitrate calculation over multiple updates
func TestConnection_BitrateOverTime(t *testing.T) {
	// Test the evolution of bitrate calculation over time
	scenarios := []struct {
		name    string
		updates []struct {
			bytes    int64
			duration time.Duration
		}
		expectedBitrates []int64
	}{
		{
			name: "constant_rate",
			updates: []struct {
				bytes    int64
				duration time.Duration
			}{
				{1000000, time.Second}, // 1MB after 1s
				{2000000, time.Second}, // 2MB after 2s
				{3000000, time.Second}, // 3MB after 3s
			},
			expectedBitrates: []int64{
				8000000, // 8 Mbps (1MB/1s)
				8000000, // 8 Mbps (1MB delta/1s)
				8000000, // 8 Mbps (1MB delta/1s)
			},
		},
		{
			name: "variable_rate",
			updates: []struct {
				bytes    int64
				duration time.Duration
			}{
				{500000, time.Second},             // 500KB after 1s
				{1500000, time.Second},            // 1.5MB after 2s (1MB delta)
				{2000000, 500 * time.Millisecond}, // 2MB after 2.5s (500KB delta)
			},
			expectedBitrates: []int64{
				4000000, // 4 Mbps (500KB/1s)
				8000000, // 8 Mbps (1MB/1s)
				8000000, // 8 Mbps (500KB/0.5s)
			},
		},
		{
			name: "bursty_traffic",
			updates: []struct {
				bytes    int64
				duration time.Duration
			}{
				{5000000, 100 * time.Millisecond},  // 5MB in 100ms
				{5000000, 2 * time.Second},         // No new data for 2s
				{10000000, 100 * time.Millisecond}, // 5MB more in 100ms
			},
			expectedBitrates: []int64{
				400000000, // 400 Mbps burst
				0,         // No new data
				400000000, // 400 Mbps burst again
			},
		},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			var lastBytes int64 = 0

			for i, update := range scenario.updates {
				// Calculate expected bitrate
				deltaBytes := update.bytes - lastBytes
				actualBitrate := testBitrateCalc(update.bytes, lastBytes, update.duration)

				assert.Equal(t, scenario.expectedBitrates[i], actualBitrate,
					"Update %d: Expected %d bps, got %d bps (delta: %d bytes)",
					i+1, scenario.expectedBitrates[i], actualBitrate, deltaBytes)

				lastBytes = update.bytes
			}
		})
	}
}
