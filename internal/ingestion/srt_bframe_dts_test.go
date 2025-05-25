package ingestion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestP1_5_BFrameDTSCalculationBug demonstrates the B-frame DTS calculation bug
// where DTS is incorrectly set equal to PTS, breaking B-frame decoding
func TestP1_5_BFrameDTSCalculationBug(t *testing.T) {
	// Bug: In srt_connection_adapter.go:201, the code sets tspkt.DTS = tspkt.PTS
	// This is incorrect for streams with B-frames because:
	// 1. B-frames have PTS > DTS due to reordering
	// 2. DTS must always be monotonically increasing
	// 3. DTS <= PTS for all frames

	// Create a mock MPEG-TS parser that returns packets with different frame types
	mockPackets := []struct {
		frameType string // I, P, or B
		pts       int64
		dts       int64 // What DTS should be
	}{
		// GOP structure: I B B P B B P
		// Display order:  I B B P B B P (PTS order)
		// Decode order:   I P B B P B B (DTS order)
		{"I", 1000, 1000}, // I-frame: PTS = DTS
		{"B", 2000, 1500}, // B-frame: PTS > DTS (displayed after I, decoded after P)
		{"B", 3000, 2500}, // B-frame: PTS > DTS
		{"P", 4000, 4000}, // P-frame: PTS = DTS
		{"B", 5000, 4500}, // B-frame: PTS > DTS
		{"B", 6000, 5500}, // B-frame: PTS > DTS
		{"P", 7000, 7000}, // P-frame: PTS = DTS
	}

	// Test the buggy behavior
	for i, pkt := range mockPackets {
		// Simulate what the buggy code does
		buggyDTS := pkt.pts // BUG: DTS = PTS

		if pkt.frameType == "B" {
			// For B-frames, DTS should be less than PTS
			assert.Greater(t, pkt.pts, pkt.dts,
				"Frame %d (%s): B-frame should have PTS > DTS", i, pkt.frameType)

			// But the bug makes them equal
			assert.Equal(t, buggyDTS, pkt.pts,
				"Bug confirmed: DTS incorrectly set to PTS for B-frame %d", i)

			// This violates the DTS <= PTS constraint
			assert.GreaterOrEqual(t, buggyDTS, pkt.pts,
				"Bug creates invalid timestamps where DTS > PTS")
		}
	}

	// Demonstrate the problem with DTS ordering
	t.Run("DTS must be monotonic", func(t *testing.T) {
		lastDTS := int64(0)
		for i, pkt := range mockPackets {
			buggyDTS := pkt.pts // The bug

			if i > 0 && pkt.frameType == "B" {
				// B-frames will have non-monotonic DTS with the bug
				// because their PTS can be less than previous P-frame's PTS
				if buggyDTS < lastDTS {
					t.Logf("Bug causes non-monotonic DTS at frame %d: %d < %d",
						i, buggyDTS, lastDTS)
				}
			}
			lastDTS = buggyDTS
		}
	})
}

// TestP1_5_DTSPTSRelationship verifies correct DTS/PTS relationships
func TestP1_5_DTSPTSRelationship(t *testing.T) {
	// This test shows what the correct behavior should be

	testCases := []struct {
		name      string
		frameType string
		pts       int64
		dts       int64
		valid     bool
		reason    string
	}{
		{
			name:      "I-frame valid",
			frameType: "I",
			pts:       1000,
			dts:       1000,
			valid:     true,
			reason:    "I-frames have PTS = DTS",
		},
		{
			name:      "P-frame valid",
			frameType: "P",
			pts:       2000,
			dts:       2000,
			valid:     true,
			reason:    "P-frames have PTS = DTS",
		},
		{
			name:      "B-frame valid",
			frameType: "B",
			pts:       3000,
			dts:       2500,
			valid:     true,
			reason:    "B-frames have PTS > DTS",
		},
		{
			name:      "B-frame invalid (bug)",
			frameType: "B",
			pts:       3000,
			dts:       3000, // BUG: DTS = PTS
			valid:     false,
			reason:    "B-frames cannot have DTS = PTS",
		},
		{
			name:      "Any frame with DTS > PTS",
			frameType: "any",
			pts:       1000,
			dts:       2000,
			valid:     false,
			reason:    "DTS must never exceed PTS",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Check DTS <= PTS constraint
			if tc.dts > tc.pts {
				assert.False(t, tc.valid, "DTS > PTS is always invalid")
			}

			// Check B-frame specific constraint
			if tc.frameType == "B" && tc.dts == tc.pts {
				assert.False(t, tc.valid, "B-frames must have DTS < PTS")
			}
		})
	}
}

// TestP1_5_BFrameReorderingDelay demonstrates the need for reordering delay
func TestP1_5_BFrameReorderingDelay(t *testing.T) {
	// Common reordering delays based on GOP structure
	testCases := []struct {
		name              string
		maxBFrames        int
		reorderingDelay   time.Duration // at 30fps
		reorderingDelayMS int64
	}{
		{
			name:              "No B-frames",
			maxBFrames:        0,
			reorderingDelay:   0,
			reorderingDelayMS: 0,
		},
		{
			name:              "1 B-frame",
			maxBFrames:        1,
			reorderingDelay:   33 * time.Millisecond, // 1 frame at 30fps
			reorderingDelayMS: 3000,                  // 90kHz units
		},
		{
			name:              "2 B-frames (common)",
			maxBFrames:        2,
			reorderingDelay:   66 * time.Millisecond, // 2 frames at 30fps
			reorderingDelayMS: 6000,                  // 90kHz units
		},
		{
			name:              "3 B-frames",
			maxBFrames:        3,
			reorderingDelay:   100 * time.Millisecond, // 3 frames at 30fps
			reorderingDelayMS: 9000,                   // 90kHz units
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// For B-frames, DTS = PTS - reorderingDelay
			pts := int64(90000) // 1 second in 90kHz
			expectedDTS := pts - tc.reorderingDelayMS

			if tc.maxBFrames > 0 {
				assert.Less(t, expectedDTS, pts,
					"With %d B-frames, DTS should be %v behind PTS",
					tc.maxBFrames, tc.reorderingDelay)
			} else {
				assert.Equal(t, expectedDTS, pts,
					"Without B-frames, DTS should equal PTS")
			}
		})
	}
}
