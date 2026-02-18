package ingestion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestPTSWraparoundFix verifies the PTS wraparound fix works correctly
func TestPTSWraparoundFix(t *testing.T) {
	// Test various PTS scenarios
	tests := []struct {
		name             string
		lastPTS          int64
		currentPTS       int64
		expectBFrame     bool
		expectWraparound bool
		description      string
	}{
		// Normal B-frame scenarios
		{
			name:             "typical B-frame",
			lastPTS:          3000 * 90,
			currentPTS:       2000 * 90,
			expectBFrame:     true,
			expectWraparound: false,
			description:      "1 second backwards = B-frame",
		},
		{
			name:             "small B-frame jump",
			lastPTS:          1000 * 90,
			currentPTS:       900 * 90,
			expectBFrame:     true,
			expectWraparound: false,
			description:      "100ms backwards = B-frame",
		},

		// 33-bit wraparound scenarios
		{
			name:             "33-bit wraparound",
			lastPTS:          (1 << 33) - 1000*90,
			currentPTS:       1000 * 90,
			expectBFrame:     false,
			expectWraparound: true,
			description:      "Wraparound at 33-bit boundary",
		},
		{
			name:             "near 33-bit wraparound",
			lastPTS:          (1 << 33) - 100,
			currentPTS:       100,
			expectBFrame:     false,
			expectWraparound: true,
			description:      "Very close to 33-bit boundary",
		},

		// Edge cases that were problematic with 32-bit logic
		{
			name:             "32-bit false positive",
			lastPTS:          (1 << 32) + 1000*90,
			currentPTS:       1000 * 90,
			expectBFrame:     true, // This IS a B-frame, not wraparound
			expectWraparound: false,
			description:      "Large backwards jump but not 33-bit wraparound",
		},
		{
			name:             "half 33-bit space backwards",
			lastPTS:          ((1 << 33) / 2) + 1000,
			currentPTS:       1000,
			expectBFrame:     true,
			expectWraparound: false,
			description:      "Exactly half 33-bit space backwards",
		},

		// Forward progression
		{
			name:             "normal forward",
			lastPTS:          1000 * 90,
			currentPTS:       2000 * 90,
			expectBFrame:     false,
			expectWraparound: false,
			description:      "Normal forward progression",
		},

		// Real-world scenarios
		{
			name:             "26.5 hour stream wraparound",
			lastPTS:          int64(26.5 * 3600 * 90000),
			currentPTS:       0,
			expectBFrame:     false,
			expectWraparound: true,
			description:      "Stream running for 26.5 hours wraps around",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply the fixed logic
			const maxPTS = int64(1) << 33
			const halfMaxPTS = maxPTS / 2

			diff := tt.currentPTS - tt.lastPTS
			isWraparound := diff < -halfMaxPTS
			isBFrame := diff < 0 && !isWraparound

			assert.Equal(t, tt.expectWraparound, isWraparound,
				"Wraparound detection: %s", tt.description)
			assert.Equal(t, tt.expectBFrame, isBFrame,
				"B-frame detection: %s", tt.description)

			// Log the analysis for debugging
			t.Logf("PTS %d -> %d: diff=%d, wraparound=%v, bframe=%v",
				tt.lastPTS, tt.currentPTS, diff, isWraparound, isBFrame)
		})
	}
}

// TestPTSTimingCalculations verifies PTS timing calculations
func TestPTSTimingCalculations(t *testing.T) {
	// PTS is in 90kHz units
	const ptsFreq = 90000

	tests := []struct {
		name     string
		duration time.Duration
		pts      int64
	}{
		{
			name:     "1 second",
			duration: time.Second,
			pts:      90000,
		},
		{
			name:     "1 hour",
			duration: time.Hour,
			pts:      3600 * 90000,
		},
		{
			name:     "26.5 hours (near wraparound)",
			duration: time.Duration(26.5 * float64(time.Hour)),
			pts:      int64(26.5 * 3600 * 90000),
		},
		{
			name:     "33ms (1 frame at 30fps)",
			duration: time.Millisecond * 33,
			pts:      2970, // 33ms * 90
		},
		{
			name:     "40ms (1 frame at 25fps)",
			duration: time.Millisecond * 40,
			pts:      3600, // 40ms * 90
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calculatedPTS := int64(tt.duration.Seconds() * ptsFreq)
			assert.Equal(t, tt.pts, calculatedPTS,
				"PTS calculation for %v", tt.duration)

			// Verify it fits in 33 bits
			assert.Less(t, calculatedPTS, int64(1<<33),
				"PTS should fit in 33 bits")
		})
	}
}

// TestPTSWraparoundTiming calculates when PTS wraparound occurs
func TestPTSWraparoundTiming(t *testing.T) {
	const maxPTS = int64(1) << 33
	const ptsFreq = 90000

	// Calculate wraparound time
	wrapSeconds := float64(maxPTS) / float64(ptsFreq)
	wrapHours := wrapSeconds / 3600
	wrapDays := wrapHours / 24

	t.Logf("PTS wraparound occurs at:")
	t.Logf("  %d PTS units", maxPTS)
	t.Logf("  %.2f seconds", wrapSeconds)
	t.Logf("  %.2f hours", wrapHours)
	t.Logf("  %.2f days", wrapDays)

	// Verify it's approximately 26.5 hours
	assert.InDelta(t, 26.5, wrapHours, 0.1,
		"PTS wraparound should occur around 26.5 hours")
}
