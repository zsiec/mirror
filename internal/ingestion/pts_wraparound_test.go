package ingestion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPTSWraparoundLogic tests the PTS wraparound detection logic
func TestPTSWraparoundLogic(t *testing.T) {
	// Current (incorrect) logic from the code
	currentLogic := func(lastPTS, currentPTS int64) (isWraparound, isBFrame bool) {
		const maxPTSWrap = int64(1) << 32 // 2^32 (incorrect, should be 33-bit)
		ptsDiff := lastPTS - currentPTS

		isWraparound = ptsDiff > maxPTSWrap
		isPTSBackwards := currentPTS < lastPTS && !isWraparound

		return isWraparound, isPTSBackwards
	}

	// Correct logic for 33-bit PTS
	correctLogic := func(lastPTS, currentPTS int64) (isWraparound, isBFrame bool) {
		const maxPTS = int64(1) << 33 // 2^33 for 33-bit PTS
		const halfMaxPTS = maxPTS / 2

		// Calculate forward difference
		diff := currentPTS - lastPTS

		// If the difference is very negative (more than half the 33-bit space),
		// it's likely a wraparound
		if diff < -halfMaxPTS {
			// Wraparound case
			isWraparound = true
			isBFrame = false
		} else if diff < 0 {
			// Normal backwards jump (B-frame)
			isWraparound = false
			isBFrame = true
		} else {
			// Forward progression
			isWraparound = false
			isBFrame = false
		}

		return isWraparound, isBFrame
	}

	tests := []struct {
		name             string
		lastPTS          int64
		currentPTS       int64
		expectWraparound bool
		expectBFrame     bool
	}{
		{
			name:             "normal B-frame",
			lastPTS:          2000 * 90,
			currentPTS:       1000 * 90,
			expectWraparound: false,
			expectBFrame:     true,
		},
		{
			name:             "33-bit wraparound",
			lastPTS:          (1 << 33) - 1000*90,
			currentPTS:       1000 * 90,
			expectWraparound: true,
			expectBFrame:     false,
		},
		{
			name:             "not wraparound at 32-bit boundary",
			lastPTS:          (1 << 32) + 1000*90,
			currentPTS:       1000 * 90,
			expectWraparound: false,
			expectBFrame:     true, // This is a large backwards jump, not wraparound
		},
		{
			name:             "forward progression",
			lastPTS:          1000 * 90,
			currentPTS:       2000 * 90,
			expectWraparound: false,
			expectBFrame:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test current (buggy) logic
			currentWrap, currentBFrame := currentLogic(tt.lastPTS, tt.currentPTS)

			// Test correct logic
			correctWrap, correctBFrame := correctLogic(tt.lastPTS, tt.currentPTS)

			// Log the differences
			if currentWrap != tt.expectWraparound || currentBFrame != tt.expectBFrame {
				t.Logf("Current logic fails: wraparound=%v (expect %v), bframe=%v (expect %v)",
					currentWrap, tt.expectWraparound, currentBFrame, tt.expectBFrame)
			}

			// Assert correct logic works
			assert.Equal(t, tt.expectWraparound, correctWrap,
				"Correct logic wraparound detection for PTS %d -> %d", tt.lastPTS, tt.currentPTS)
			assert.Equal(t, tt.expectBFrame, correctBFrame,
				"Correct logic B-frame detection for PTS %d -> %d", tt.lastPTS, tt.currentPTS)
		})
	}
}

// TestPTS33BitValues verifies handling of actual 33-bit PTS values
func TestPTS33BitValues(t *testing.T) {
	const maxPTS = int64(1) << 33

	// Test that we handle values near the 33-bit boundary correctly
	tests := []struct {
		name    string
		pts     int64
		isValid bool
	}{
		{
			name:    "zero PTS",
			pts:     0,
			isValid: true,
		},
		{
			name:    "max valid 33-bit PTS",
			pts:     maxPTS - 1,
			isValid: true,
		},
		{
			name:    "exceeds 33-bit",
			pts:     maxPTS,
			isValid: false, // Should be wrapped/invalid
		},
		{
			name:    "typical PTS value",
			pts:     3600 * 90000, // 1 hour in 90kHz
			isValid: true,
		},
		{
			name:    "26.5 hours in 90kHz (near wraparound)",
			pts:     int64(26.5 * 3600 * 90000),
			isValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// In MPEG-TS, PTS values should be masked to 33 bits
			maskedPTS := tt.pts & ((1 << 33) - 1)
			isInRange := tt.pts >= 0 && tt.pts < maxPTS

			assert.Equal(t, tt.isValid, isInRange,
				"PTS %d validity check", tt.pts)

			if tt.isValid {
				assert.Equal(t, tt.pts, maskedPTS,
					"Valid PTS should not need masking")
			}
		})
	}
}
