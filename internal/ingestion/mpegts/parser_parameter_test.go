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
		// SPS data
		0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05, 0x06,
		// Number of PPS (1)
		0x01,
		// PPS length (4 bytes)
		0x00, 0x04,
		// PPS data
		0xCE, 0x3C, 0x80, 0x01,
	}
	
	parser := NewParser()
	paramSets := parser.extractH264ParameterSetsFromDescriptor(avcDescriptor)
	
	if len(paramSets) != 2 {
		t.Errorf("Expected 2 parameter sets (1 SPS + 1 PPS), got %d", len(paramSets))
	}
	
	// Verify SPS has proper start code and NAL header
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
	
	// Verify PPS has proper start code and NAL header
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
	hevcDescriptor[offset] = 0  // Number of VPS (high byte)
	hevcDescriptor[offset+1] = 1 // Number of VPS (low byte) = 1
	offset += 2
	hevcDescriptor[offset] = 0  // VPS length (high byte)
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
		0xFF,             // Length size minus 1
		0xE1,             // Number of SPS (1)
		0x00, 0x04,       // SPS length (4 bytes)
		0x42, 0x00, 0x1E, 0x8D, // SPS data
		0x01,             // Number of PPS (1)
		0x00, 0x04,       // PPS length (4 bytes)
		0xCE, 0x3C, 0x80, 0x01, // PPS data
	}
	
	// Create fake descriptors data
	descriptors := []byte{
		0x28,             // AVC video descriptor tag
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