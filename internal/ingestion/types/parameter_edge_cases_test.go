package types

import (
	"bytes"
	"testing"
	"time"
)

// TestRBSPExtractionWithAndWithoutHeader tests RBSP extraction for both cases
func TestRBSPExtractionWithAndWithoutHeader(t *testing.T) {
	tests := []struct {
		name         string
		input        []byte
		hasNALHeader bool
		expectedRBSP []byte
		expectError  bool
	}{
		{
			name:         "SPS with NAL header",
			input:        []byte{0x67, 0x42, 0x00, 0x1E, 0x95, 0xA8, 0x08, 0x0F},
			hasNALHeader: true,
			expectedRBSP: []byte{0x42, 0x00, 0x1E, 0x95, 0xA8, 0x08, 0x0F},
			expectError:  false,
		},
		{
			name:         "SPS without NAL header",
			input:        []byte{0x42, 0x00, 0x1E, 0x95, 0xA8, 0x08, 0x0F},
			hasNALHeader: false,
			expectedRBSP: []byte{0x42, 0x00, 0x1E, 0x95, 0xA8, 0x08, 0x0F},
			expectError:  false,
		},
		{
			name:         "PPS with NAL header",
			input:        []byte{0x68, 0xCE, 0x3C, 0x80},
			hasNALHeader: true,
			expectedRBSP: []byte{0xCE, 0x3C, 0x80}, // Trailing bytes are preserved (not stripped)
			expectError:  false,
		},
		{
			name:         "PPS without NAL header",
			input:        []byte{0xCE, 0x3C, 0x80},
			hasNALHeader: false,
			expectedRBSP: []byte{0xCE, 0x3C, 0x80}, // Trailing bytes are preserved (not stripped)
			expectError:  false,
		},
		{
			name:         "Data with emulation prevention bytes",
			input:        []byte{0x67, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
			hasNALHeader: true,
			expectedRBSP: []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x02},
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rbsp []byte
			var err error

			if tt.hasNALHeader {
				rbsp, err = ExtractRBSPFromNALUnit(tt.input)
			} else {
				rbsp, err = ExtractRBSPFromPayload(tt.input)
			}

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if !bytes.Equal(rbsp, tt.expectedRBSP) {
				t.Errorf("RBSP mismatch: got %v, want %v", rbsp, tt.expectedRBSP)
			}
		})
	}
}

// TestParameterSetParsingWithoutHeader tests parsing parameter sets without NAL headers
func TestParameterSetParsingWithoutHeader(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Test SPS without NAL header - need complete minimal SPS
	bw := NewBitWriter()
	// Write minimal SPS data
	bw.WriteBits(66, 8) // profile_idc
	bw.WriteBits(0, 8)  // constraint flags
	bw.WriteBits(30, 8) // level_idc
	bw.WriteUE(0)       // seq_parameter_set_id
	bw.WriteUE(0)       // log2_max_frame_num_minus4
	bw.WriteUE(0)       // pic_order_cnt_type
	bw.WriteUE(0)       // log2_max_pic_order_cnt_lsb_minus4
	bw.WriteUE(1)       // max_num_ref_frames
	bw.WriteBit(0)      // gaps_in_frame_num_value_allowed_flag
	bw.WriteUE(15)      // pic_width_in_mbs_minus1 (256 pixels)
	bw.WriteUE(15)      // pic_height_in_map_units_minus1 (256 pixels)
	bw.WriteBit(1)      // frame_mbs_only_flag
	bw.WriteBit(1)      // direct_8x8_inference_flag
	bw.WriteBit(0)      // frame_cropping_flag

	spsPayload := bw.GetBytes()

	// This should parse correctly without NAL header
	sps, err := ctx.parseSPS(spsPayload)
	if err != nil {
		t.Fatalf("Failed to parse SPS without header: %v", err)
	}
	if sps.ID != 0 {
		t.Errorf("Expected SPS ID 0, got %d", sps.ID)
	}

	// Test PPS without NAL header - need complete minimal PPS
	bw2 := NewBitWriter()
	bw2.WriteUE(0)      // pic_parameter_set_id
	bw2.WriteUE(0)      // seq_parameter_set_id
	bw2.WriteBit(0)     // entropy_coding_mode_flag
	bw2.WriteBit(0)     // bottom_field_pic_order_in_frame_present_flag
	bw2.WriteUE(0)      // num_slice_groups_minus1
	bw2.WriteUE(0)      // num_ref_idx_l0_default_active_minus1
	bw2.WriteUE(0)      // num_ref_idx_l1_default_active_minus1
	bw2.WriteBit(0)     // weighted_pred_flag
	bw2.WriteBits(0, 2) // weighted_bipred_idc
	bw2.WriteSE(0)      // pic_init_qp_minus26
	bw2.WriteSE(0)      // pic_init_qs_minus26
	bw2.WriteSE(0)      // chroma_qp_index_offset
	bw2.WriteBit(0)     // deblocking_filter_control_present_flag
	bw2.WriteBit(0)     // constrained_intra_pred_flag
	bw2.WriteBit(0)     // redundant_pic_cnt_present_flag

	ppsPayload := bw2.GetBytes()

	pps, err := ctx.parsePPS(ppsPayload)
	if err != nil {
		t.Fatalf("Failed to parse PPS without header: %v", err)
	}
	if pps.ID != 0 {
		t.Errorf("Expected PPS ID 0, got %d", pps.ID)
	}
	if pps.ReferencedSPSID != 0 {
		t.Errorf("Expected referenced SPS ID 0, got %d", pps.ReferencedSPSID)
	}
}

// TestPPSRateLimitingWithDuplicates tests that duplicate PPS don't trigger rate limiting
func TestPPSRateLimitingWithDuplicates(t *testing.T) {
	validator := NewParameterSetValidator(10*time.Millisecond, 100)

	// Add initial SPS
	spsData := []byte{0x67, 0x42, 0xC0, 0x1E, 0x90}
	err := validator.ValidateSPS(0, spsData)
	if err != nil {
		t.Fatalf("Failed to add initial SPS: %v", err)
	}

	// Add initial PPS
	ppsData := []byte{0x68, 0x90, 0x90} // PPS ID=0, SPS ID=0
	err = validator.ValidatePPS(0, 0, ppsData)
	if err != nil {
		t.Fatalf("Failed to add initial PPS: %v", err)
	}

	// Send duplicate PPS multiple times - should not trigger rate limiting
	for i := 0; i < 10; i++ {
		err = validator.ValidatePPS(0, 0, ppsData)
		if err != nil {
			t.Errorf("Duplicate PPS triggered rate limiting on iteration %d: %v", i, err)
		}
	}

	// Now send a different PPS too quickly - should trigger rate limiting
	differentPPS := []byte{0x68, 0x90, 0x91} // Different data
	err = validator.ValidatePPS(0, 0, differentPPS)
	if err == nil {
		t.Error("Expected rate limiting error for different PPS sent too quickly")
	}
}

// TestCropValuesParsing tests SPS parsing with frame cropping
func TestCropValuesParsing(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Create SPS with cropping
	// This is a simplified SPS with crop values
	br := NewBitWriter()

	// NAL header
	br.WriteBits(0x67, 8) // NAL unit type 7 (SPS)

	// Profile/level
	br.WriteBits(66, 8) // profile_idc
	br.WriteBits(0, 8)  // constraint flags
	br.WriteBits(30, 8) // level_idc

	// seq_parameter_set_id
	br.WriteUE(0)

	// For baseline profile, skip high profile fields

	// log2_max_frame_num_minus4
	br.WriteUE(0)

	// pic_order_cnt_type
	br.WriteUE(0)
	// log2_max_pic_order_cnt_lsb_minus4
	br.WriteUE(0)

	// max_num_ref_frames
	br.WriteUE(1)

	// gaps_in_frame_num_value_allowed_flag
	br.WriteBit(0)

	// pic_width_in_mbs_minus1 (1920/16 - 1 = 119)
	br.WriteUE(119)

	// pic_height_in_map_units_minus1 (1088/16 - 1 = 67)
	br.WriteUE(67)

	// frame_mbs_only_flag
	br.WriteBit(1)

	// direct_8x8_inference_flag
	br.WriteBit(1)

	// frame_cropping_flag
	br.WriteBit(1)

	// Crop values to get 1920x1080 from 1920x1088
	br.WriteUE(0) // crop_left
	br.WriteUE(0) // crop_right
	br.WriteUE(0) // crop_top
	br.WriteUE(4) // crop_bottom (4*2 = 8 pixels)

	spsData := br.GetBytes()

	sps, err := ctx.parseSPS(spsData)
	if err != nil {
		t.Fatalf("Failed to parse SPS with crop values: %v", err)
	}

	// Check dimensions
	if sps.Width == nil || sps.Height == nil {
		t.Fatal("SPS dimensions not parsed")
	}

	expectedWidth := 1920
	expectedHeight := 1080 // 1088 - 8

	if *sps.Width != expectedWidth {
		t.Errorf("Expected width %d, got %d", expectedWidth, *sps.Width)
	}
	if *sps.Height != expectedHeight {
		t.Errorf("Expected height %d, got %d", expectedHeight, *sps.Height)
	}
}

// TestInvalidSPSIDHandling tests handling of out-of-range SPS IDs
func TestInvalidSPSIDHandling(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Create SPS with invalid ID (> 31)
	br := NewBitWriter()
	br.WriteBits(0x67, 8) // NAL header
	br.WriteBits(66, 8)   // profile_idc
	br.WriteBits(0, 8)    // constraints
	br.WriteBits(30, 8)   // level_idc
	br.WriteUE(255)       // Invalid SPS ID
	// Add more fields to make it a valid SPS structure
	br.WriteUE(0) // log2_max_frame_num_minus4
	br.WriteUE(0) // pic_order_cnt_type
	br.WriteUE(0) // log2_max_pic_order_cnt_lsb_minus4

	spsData := br.GetBytes()

	_, err := ctx.parseSPS(spsData)
	if err == nil {
		t.Error("Expected error for invalid SPS ID 255")
	}
	if err != nil && !bytes.Contains([]byte(err.Error()), []byte("out of range")) {
		t.Errorf("Expected 'out of range' error, got: %v", err)
	}
}

// TestPPSDependencyValidation tests that PPS properly references existing SPS
func TestPPSDependencyValidation(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Try to add PPS that references non-existent SPS
	br := NewBitWriter()
	br.WriteBits(0x68, 8) // NAL header for PPS
	br.WriteUE(0)         // PPS ID = 0
	br.WriteUE(7)         // Reference SPS ID = 7 (doesn't exist)
	br.WriteBit(0)        // entropy_coding_mode_flag
	br.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
	br.WriteUE(0)         // num_slice_groups_minus1

	ppsData := br.GetBytes()

	// Should still succeed because enforceConsistency is false by default
	pps, err := ctx.parsePPS(ppsData)
	if err != nil {
		t.Fatalf("Failed to parse PPS: %v", err)
	}
	if pps.ReferencedSPSID != 7 {
		t.Errorf("Expected referenced SPS ID 7, got %d", pps.ReferencedSPSID)
	}

	// The actual validation happens when trying to decode frames
	// The frame decoder should detect missing SPS 7
}
