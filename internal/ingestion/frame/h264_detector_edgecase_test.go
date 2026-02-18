package frame

import (
	"testing"
)

// TestFindNALUnits_MissedStartCodes tests if start codes in the last 3 bytes are detected
func TestFindNALUnits_MissedStartCodes(t *testing.T) {
	detector := NewH264Detector()

	tests := []struct {
		name        string
		data        []byte
		shouldFind  bool
		description string
	}{
		{
			name: "3-byte start code at position len-3",
			data: []byte{
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // padding
				0x00, 0x00, 0x01, // start code at position 7 (len=10, len-3=7)
			},
			shouldFind:  false, // With current loop condition i < len(data)-3, this will be missed
			description: "Start code at exactly len-3 position",
		},
		{
			name: "3-byte start code at position len-2",
			data: []byte{
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // padding
				0x00, 0x00, 0x01, // start code at position 8 (len=11, len-3=8)
			},
			shouldFind:  false, // This will also be missed
			description: "Start code at len-2 position",
		},
		{
			name: "3-byte start code at position len-1",
			data: []byte{
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // padding
				0x00, 0x00, 0x01, // start code at position 9 (len=12, len-3=9)
			},
			shouldFind:  false, // This will also be missed
			description: "Start code at len-1 position",
		},
		{
			name: "Valid NAL followed by start code at boundary",
			data: []byte{
				0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x0A, // First NAL unit
				0x00, 0x00, 0x01, // Second start code at boundary - will be missed
			},
			shouldFind:  true, // Should find first NAL, but miss the second start code
			description: "Second start code at boundary should be detected for proper NAL termination",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nalUnits, err := detector.findNALUnits(tt.data)
			if err != nil {
				t.Errorf("findNALUnits() error = %v", err)
				return
			}

			found := len(nalUnits) > 0
			if found != tt.shouldFind {
				t.Errorf("findNALUnits() found=%v, want shouldFind=%v. %s",
					found, tt.shouldFind, tt.description)
			}
		})
	}
}

// TestFindNALUnits_ProperNALTermination tests if NAL units are properly terminated
func TestFindNALUnits_ProperNALTermination(t *testing.T) {
	detector := NewH264Detector()

	// Test case where a NAL unit should be terminated by a start code at the boundary
	data := []byte{
		0x00, 0x00, 0x01, 0x67, // SPS start
		0x42, 0x00, 0x0A, 0xAB, // SPS data
		0xCD, 0xEF, 0x12, 0x34, // More SPS data
		0x00, 0x00, 0x01, // PPS start code at boundary
	}

	nalUnits, err := detector.findNALUnits(data)
	if err != nil {
		t.Fatalf("findNALUnits() error = %v", err)
	}

	if len(nalUnits) != 1 {
		t.Errorf("Expected 1 NAL unit, got %d", len(nalUnits))
	}

	if len(nalUnits) > 0 {
		// The NAL unit should NOT include the trailing start code
		// NAL data starts at position 3 (after 0x00 0x00 0x01)
		// and should end at position 12 (before the next 0x00 0x00 0x01)
		expectedLen := 9 // Data from position 3 to 12 (9 bytes of actual NAL data)
		if len(nalUnits[0]) != expectedLen {
			t.Errorf("NAL unit length = %d, want %d. NAL unit content: %x",
				len(nalUnits[0]), expectedLen, nalUnits[0])
		}
	}
}
