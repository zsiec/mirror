package mpegts

import (
	"testing"
)

func TestH264ParameterSetExtraction(t *testing.T) {
	// Test H.264 AVC descriptor with SPS and PPS
	avcDescriptor := []byte{
		// Profile, constraints, level
		0x42, 0x00, 0x1E,
		// Length size minus 1 (3 = 4-byte length)
		0xFF,
		// Number of SPS (1)
		0xE1,
		// SPS length (8 bytes)
		0x00, 0x08,
		// SPS data (NAL header 0x67 = SPS, followed by profile/level/etc)
		0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05,
		// Number of PPS (1)
		0x01,
		// PPS length (4 bytes)
		0x00, 0x04,
		// PPS data (NAL header 0x68 = PPS, followed by PPS RBSP)
		0x68, 0xCE, 0x3C, 0x80,
	}

	parser := NewParser()
	paramSets := parser.extractH264ParameterSetsFromDescriptor(avcDescriptor)

	if len(paramSets) != 2 {
		t.Errorf("Expected 2 parameter sets (1 SPS + 1 PPS), got %d", len(paramSets))
	}

	// Verify SPS has proper start code (NAL header is part of the data from the descriptor)
	sps := paramSets[0]
	expectedSPSStart := []byte{0x00, 0x00, 0x00, 0x01, 0x67}
	if len(sps) < 5 {
		t.Fatalf("SPS too short: %d bytes", len(sps))
	}
	for i, expected := range expectedSPSStart {
		if sps[i] != expected {
			t.Errorf("SPS start code mismatch at byte %d: expected %02x, got %02x", i, expected, sps[i])
		}
	}

	// Verify PPS has proper start code (NAL header is part of the data from the descriptor)
	pps := paramSets[1]
	expectedPPSStart := []byte{0x00, 0x00, 0x00, 0x01, 0x68}
	if len(pps) < 5 {
		t.Fatalf("PPS too short: %d bytes", len(pps))
	}
	for i, expected := range expectedPPSStart {
		if pps[i] != expected {
			t.Errorf("PPS start code mismatch at byte %d: expected %02x, got %02x", i, expected, pps[i])
		}
	}
}

func TestH264ParameterSetExtractionEmpty(t *testing.T) {
	parser := NewParser()

	// Test with empty descriptor
	paramSets := parser.extractH264ParameterSetsFromDescriptor([]byte{})
	if len(paramSets) != 0 {
		t.Errorf("Expected 0 parameter sets from empty descriptor, got %d", len(paramSets))
	}

	// Test with too short descriptor
	shortDescriptor := []byte{0x42, 0x00}
	paramSets = parser.extractH264ParameterSetsFromDescriptor(shortDescriptor)
	if len(paramSets) != 0 {
		t.Errorf("Expected 0 parameter sets from short descriptor, got %d", len(paramSets))
	}
}

func TestBitstreamParameterSetExtraction(t *testing.T) {
	parser := NewParser()

	// Create test bitstream with SPS and PPS
	bitstream := []byte{
		// Start code + SPS
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05,
		// Start code + PPS
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x3C, 0x80,
		// Start code + IDR frame (should be ignored)
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, 0x10,
	}

	paramSets := parser.extractParameterSetsFromBitstream(bitstream, 0x1B) // H.264

	if len(paramSets) != 2 {
		t.Errorf("Expected 2 parameter sets from bitstream, got %d", len(paramSets))
	}

	// Verify SPS
	sps := paramSets[0]
	expectedSPS := []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05}
	if len(sps) != len(expectedSPS) {
		t.Errorf("SPS length mismatch: expected %d, got %d", len(expectedSPS), len(sps))
	}
	for i, expected := range expectedSPS {
		if sps[i] != expected {
			t.Errorf("SPS data mismatch at byte %d: expected %02x, got %02x", i, expected, sps[i])
		}
	}

	// Verify PPS
	pps := paramSets[1]
	expectedPPS := []byte{0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x3C, 0x80}
	if len(pps) != len(expectedPPS) {
		t.Errorf("PPS length mismatch: expected %d, got %d", len(expectedPPS), len(pps))
	}
	for i, expected := range expectedPPS {
		if pps[i] != expected {
			t.Errorf("PPS data mismatch at byte %d: expected %02x, got %02x", i, expected, pps[i])
		}
	}
}

func TestBitstreamParameterSetExtractionWith3ByteStartCode(t *testing.T) {
	parser := NewParser()

	// Create test bitstream with 3-byte start codes
	bitstream := []byte{
		// 3-byte start code + SPS
		0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05,
		// 3-byte start code + PPS
		0x00, 0x00, 0x01, 0x68, 0xCE, 0x3C, 0x80,
	}

	paramSets := parser.extractParameterSetsFromBitstream(bitstream, 0x1B) // H.264

	if len(paramSets) != 2 {
		t.Errorf("Expected 2 parameter sets from bitstream with 3-byte start codes, got %d", len(paramSets))
	}

	// Verify SPS has 3-byte start code
	sps := paramSets[0]
	expectedSPSStart := []byte{0x00, 0x00, 0x01, 0x67}
	if len(sps) < 4 {
		t.Fatalf("SPS too short: %d bytes", len(sps))
	}
	for i, expected := range expectedSPSStart {
		if sps[i] != expected {
			t.Errorf("SPS start code mismatch at byte %d: expected %02x, got %02x", i, expected, sps[i])
		}
	}
}

func TestHEVCParameterSetExtraction(t *testing.T) {
	parser := NewParser()

	// Create minimal HEVC configuration record
	hevcDescriptor := make([]byte, 50)

	// Skip fixed fields (22 bytes)
	offset := 22

	// Number of arrays (3: VPS, SPS, PPS)
	hevcDescriptor[offset] = 3
	offset++

	// VPS array
	hevcDescriptor[offset] = 32 // VPS NAL unit type
	offset++
	hevcDescriptor[offset] = 0   // Number of VPS (high byte)
	hevcDescriptor[offset+1] = 1 // Number of VPS (low byte) = 1
	offset += 2
	hevcDescriptor[offset] = 0   // VPS length (high byte)
	hevcDescriptor[offset+1] = 4 // VPS length (low byte) = 4
	offset += 2
	// VPS data (4 bytes)
	hevcDescriptor[offset] = 0x40
	hevcDescriptor[offset+1] = 0x01
	hevcDescriptor[offset+2] = 0x0C
	hevcDescriptor[offset+3] = 0x01

	// For this test, we'll truncate here since we're testing the basic parsing
	truncatedDescriptor := hevcDescriptor[:offset+4]

	paramSets := parser.extractHEVCParameterSetsFromDescriptor(truncatedDescriptor)

	// Should extract 1 VPS
	if len(paramSets) != 1 {
		t.Errorf("Expected 1 parameter set from HEVC descriptor, got %d", len(paramSets))
	}

	if len(paramSets) > 0 {
		vps := paramSets[0]
		expectedStart := []byte{0x00, 0x00, 0x00, 0x01}
		if len(vps) < 4 {
			t.Fatalf("VPS too short: %d bytes", len(vps))
		}
		for i, expected := range expectedStart {
			if vps[i] != expected {
				t.Errorf("VPS start code mismatch at byte %d: expected %02x, got %02x", i, expected, vps[i])
			}
		}
	}
}

func TestParameterSetExtractionWithInvalidData(t *testing.T) {
	parser := NewParser()

	// Test with non-parameter set NAL units in bitstream
	bitstream := []byte{
		// Start code + IDR frame (NAL type 5, not a parameter set)
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00, 0x10,
		// Start code + P frame (NAL type 1, not a parameter set)
		0x00, 0x00, 0x00, 0x01, 0x41, 0x9A, 0x24, 0x6C,
	}

	paramSets := parser.extractParameterSetsFromBitstream(bitstream, 0x1B) // H.264

	if len(paramSets) != 0 {
		t.Errorf("Expected 0 parameter sets from non-parameter NAL units, got %d", len(paramSets))
	}
}

func TestParameterSetExtractionUnsupportedCodec(t *testing.T) {
	parser := NewParser()

	// Test with bitstream but unsupported codec type
	bitstream := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05,
	}

	paramSets := parser.extractParameterSetsFromBitstream(bitstream, 0xFF) // Invalid codec

	if len(paramSets) != 0 {
		t.Errorf("Expected 0 parameter sets from unsupported codec, got %d", len(paramSets))
	}
}

func TestParameterExtractorCallback(t *testing.T) {
	callbackCalled := false
	var receivedParamSets [][]byte
	var receivedStreamType uint8

	extractor := func(paramSets [][]byte, streamType uint8) {
		callbackCalled = true
		receivedParamSets = paramSets
		receivedStreamType = streamType
	}

	parser := NewParser()

	// Create test descriptor with one SPS
	avcDescriptor := []byte{
		0x42, 0x00, 0x1E, // Profile, constraints, level
		0xFF,       // Length size minus 1
		0xE1,       // Number of SPS (1)
		0x00, 0x04, // SPS length (4 bytes)
		0x42, 0x00, 0x1E, 0x8D, // SPS data
		0x01,       // Number of PPS (1)
		0x00, 0x04, // PPS length (4 bytes)
		0xCE, 0x3C, 0x80, 0x01, // PPS data
	}

	// Create fake descriptors data
	descriptors := []byte{
		0x28,                     // AVC video descriptor tag
		byte(len(avcDescriptor)), // Descriptor length
	}
	descriptors = append(descriptors, avcDescriptor...)

	parser.extractParameterSetsFromDescriptors(descriptors, 0x1B, extractor)

	if !callbackCalled {
		t.Errorf("Parameter set extractor callback should have been called")
	}

	if receivedStreamType != 0x1B {
		t.Errorf("Expected stream type 0x1B, got 0x%02x", receivedStreamType)
	}

	if len(receivedParamSets) != 2 {
		t.Errorf("Expected 2 parameter sets in callback, got %d", len(receivedParamSets))
	}
}

func TestCorruptedAVCDescriptor(t *testing.T) {
	parser := NewParser()

	// Test with corrupted descriptor that has unreasonably large PPS length
	corruptedDescriptor := []byte{
		// Profile, constraints, level
		0x42, 0x00, 0x1E,
		// Length size minus 1
		0xFF,
		// Number of SPS (1)
		0xE1,
		// SPS length (8 bytes)
		0x00, 0x08,
		// SPS data (NAL header 0x67 + 7 bytes)
		0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05,
		// Number of PPS (1)
		0x01,
		// PPS length (1656 bytes - the problematic value)
		0x06, 0x78, // This is 1656 in big-endian
		// Not enough data follows...
		0xCE, 0x3C,
	}

	paramSets := parser.extractH264ParameterSetsFromDescriptor(corruptedDescriptor)

	// Should extract only the SPS since PPS is corrupted
	if len(paramSets) != 1 {
		t.Errorf("Expected 1 parameter set (SPS only) from corrupted descriptor, got %d", len(paramSets))
	}

	// Verify the SPS was extracted correctly
	if len(paramSets) > 0 {
		sps := paramSets[0]
		if len(sps) != 12 { // 4 bytes start code + 8 bytes data (including NAL header)
			t.Errorf("Expected SPS length of 12 bytes, got %d", len(sps))
		}
	}
}

func TestAVCDescriptorWithInvalidProfile(t *testing.T) {
	parser := NewParser()

	// Test with invalid profile but otherwise valid descriptor
	descriptor := []byte{
		// Invalid profile 0xFF
		0xFF, 0x00, 0x1E,
		// Length size minus 1
		0xFF,
		// Number of SPS (1)
		0xE1,
		// SPS length (4 bytes)
		0x00, 0x04,
		// SPS data
		0x42, 0x00, 0x1E, 0x8D,
		// Number of PPS (1)
		0x01,
		// PPS length (4 bytes)
		0x00, 0x04,
		// PPS data
		0xCE, 0x3C, 0x80, 0x01,
	}

	paramSets := parser.extractH264ParameterSetsFromDescriptor(descriptor)

	// Should still extract parameter sets despite invalid profile
	if len(paramSets) != 2 {
		t.Errorf("Expected 2 parameter sets despite invalid profile, got %d", len(paramSets))
	}
}

func TestAVCDescriptorTooLarge(t *testing.T) {
	parser := NewParser()

	// Create a descriptor that's too large (> 1024 bytes)
	largeDescriptor := make([]byte, 1100)
	// Fill with some data
	largeDescriptor[0] = 0x42 // Profile
	largeDescriptor[1] = 0x00 // Constraints
	largeDescriptor[2] = 0x1E // Level

	paramSets := parser.extractH264ParameterSetsFromDescriptor(largeDescriptor)

	// Should return empty due to size check
	if len(paramSets) != 0 {
		t.Errorf("Expected 0 parameter sets from oversized descriptor, got %d", len(paramSets))
	}
}

func TestAVCDescriptorWithTooManyPPS(t *testing.T) {
	parser := NewParser()

	// Test with too many PPS (> 8)
	descriptor := []byte{
		// Profile, constraints, level
		0x42, 0x00, 0x1E,
		// Length size minus 1
		0xFF,
		// Number of SPS (1)
		0xE1,
		// SPS length (4 bytes)
		0x00, 0x04,
		// SPS data
		0x42, 0x00, 0x1E, 0x8D,
		// Number of PPS (20 - too many)
		0x14,
		// First PPS length (4 bytes)
		0x00, 0x04,
		// First PPS data
		0xCE, 0x3C, 0x80, 0x01,
		// More PPS entries would follow...
	}

	// Add more PPS entries
	for i := 0; i < 7; i++ {
		descriptor = append(descriptor, 0x00, 0x04)                  // Length
		descriptor = append(descriptor, 0xCE, 0x3C, 0x80, byte(i+2)) // Data
	}

	paramSets := parser.extractH264ParameterSetsFromDescriptor(descriptor)

	// Should extract 1 SPS + maximum 8 PPS = 9 total
	if len(paramSets) > 9 {
		t.Errorf("Expected at most 9 parameter sets (1 SPS + 8 PPS), got %d", len(paramSets))
	}
}

func TestAVCDescriptorValidation(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name              string
		descriptor        []byte
		shouldLookLikeAVC bool
		expectedParamSets int
	}{
		{
			name: "valid AVC descriptor",
			descriptor: []byte{
				0x42, 0x00, 0x1E, // Profile, constraints, level
				0xFF,       // Length size minus 1
				0xE1,       // Number of SPS (1)
				0x00, 0x04, // SPS length (4 bytes)
				0x42, 0x00, 0x1E, 0x8D, // SPS data
				0x01,       // Number of PPS (1)
				0x00, 0x04, // PPS length (4 bytes)
				0xCE, 0x3C, 0x80, 0x01, // PPS data
			},
			shouldLookLikeAVC: true,
			expectedParamSets: 2,
		},
		{
			name: "corrupted with PPS length 1656",
			descriptor: []byte{
				0x42, 0x00, 0x1E, // Profile, constraints, level
				0xFF,       // Length size minus 1
				0xE1,       // Number of SPS (1)
				0x00, 0x08, // SPS length (8 bytes)
				0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05, 0x06, // SPS data
				0x01,       // Number of PPS (1)
				0x06, 0x78, // PPS length (1656 bytes - invalid)
				0xCE, 0x3C, // Not enough data
			},
			shouldLookLikeAVC: true, // Basic structure looks OK
			expectedParamSets: 1,    // Should extract SPS only
		},
		{
			name:              "too short",
			descriptor:        []byte{0x42, 0x00, 0x1E},
			shouldLookLikeAVC: false,
			expectedParamSets: 0,
		},
		{
			name: "zero SPS length",
			descriptor: []byte{
				0x42, 0x00, 0x1E, // Profile, constraints, level
				0xFF,       // Length size minus 1
				0xE1,       // Number of SPS (1)
				0x00, 0x00, // SPS length (0 bytes - invalid)
			},
			shouldLookLikeAVC: true, // Basic structure looks OK
			expectedParamSets: 0,    // Zero-length SPS is skipped
		},
		{
			name: "SPS exceeds buffer",
			descriptor: []byte{
				0x42, 0x00, 0x1E, // Profile, constraints, level
				0xFF,       // Length size minus 1
				0xE1,       // Number of SPS (1)
				0x00, 0x10, // SPS length (16 bytes)
				0x42, 0x00, // Only 2 bytes of SPS data
			},
			shouldLookLikeAVC: true, // Basic structure looks OK
			expectedParamSets: 0,    // Can't extract because data is truncated
		},
		{
			name: "invalid SPS count byte",
			descriptor: []byte{
				0x42, 0x00, 0x1E, // Profile, constraints, level
				0xFF,       // Length size minus 1
				0x01,       // Invalid SPS count byte (should be 0xE0-0xFF)
				0x00, 0x04, // SPS length
			},
			shouldLookLikeAVC: false,
			expectedParamSets: 0,
		},
		{
			name: "zero profile",
			descriptor: []byte{
				0x00, 0x00, 0x1E, // Profile 0x00 is suspicious
				0xFF,
				0xE1,
				0x00,
			},
			shouldLookLikeAVC: false,
			expectedParamSets: 0,
		},
		{
			name: "0xFF profile",
			descriptor: []byte{
				0xFF, 0x00, 0x1E, // Profile 0xFF is suspicious but allowed
				0xFF,
				0xE1,
				0x00,
			},
			shouldLookLikeAVC: true, // Now allows 0xFF with warning
			expectedParamSets: 0,    // Still 0 because descriptor is incomplete
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := parser.looksLikeAVCDescriptor(tt.descriptor)
			if isValid != tt.shouldLookLikeAVC {
				t.Errorf("Expected looksLikeAVCDescriptor to return %v, got %v", tt.shouldLookLikeAVC, isValid)
			}

			// Test extraction
			paramSets := parser.extractH264ParameterSetsFromDescriptor(tt.descriptor)
			if len(paramSets) != tt.expectedParamSets {
				t.Errorf("Expected %d parameter sets, got %d", tt.expectedParamSets, len(paramSets))
			}
		})
	}
}
