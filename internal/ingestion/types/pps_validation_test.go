package types

import (
	"bytes"
	"testing"
	"time"
)

// TestPPSValidation tests comprehensive PPS validation scenarios
func TestPPSValidation(t *testing.T) {
	tests := []struct {
		name          string
		ppsData       []byte
		expectPPSID   uint8
		expectSPSID   uint8
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid PPS with NAL header",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // PPS ID = 0
				bw.WriteUE(0)         // SPS ID = 0
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(0)         // num_slice_groups_minus1
				bw.WriteUE(0)         // num_ref_idx_l0_default_active_minus1
				bw.WriteUE(0)         // num_ref_idx_l1_default_active_minus1
				bw.WriteBit(0)        // weighted_pred_flag
				bw.WriteBits(0, 2)    // weighted_bipred_idc
				bw.WriteSE(0)         // pic_init_qp_minus26
				bw.WriteSE(0)         // pic_init_qs_minus26
				bw.WriteSE(0)         // chroma_qp_index_offset
				bw.WriteBit(0)        // deblocking_filter_control_present_flag
				bw.WriteBit(0)        // constrained_intra_pred_flag
				bw.WriteBit(0)        // redundant_pic_cnt_present_flag
				return bw.GetBytes()
			}(),
			expectPPSID: 0,
			expectSPSID: 0,
			expectError: false,
		},
		{
			name: "Valid PPS without NAL header",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteUE(1)      // PPS ID = 1
				bw.WriteUE(2)      // SPS ID = 2
				bw.WriteBit(0)     // entropy_coding_mode_flag
				bw.WriteBit(0)     // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(0)      // num_slice_groups_minus1
				bw.WriteUE(0)      // num_ref_idx_l0_default_active_minus1
				bw.WriteUE(0)      // num_ref_idx_l1_default_active_minus1
				bw.WriteBit(0)     // weighted_pred_flag
				bw.WriteBits(0, 2) // weighted_bipred_idc
				bw.WriteSE(0)      // pic_init_qp_minus26
				bw.WriteSE(0)      // pic_init_qs_minus26
				bw.WriteSE(0)      // chroma_qp_index_offset
				bw.WriteBit(0)     // deblocking_filter_control_present_flag
				bw.WriteBit(0)     // constrained_intra_pred_flag
				bw.WriteBit(0)     // redundant_pic_cnt_present_flag
				return bw.GetBytes()
			}(),
			expectPPSID: 1,
			expectSPSID: 2,
			expectError: false,
		},
		{
			name: "PPS with out-of-range PPS ID",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(256)       // Invalid PPS ID (> 255)
				bw.WriteUE(0)         // SPS ID = 0
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "out of range",
		},
		{
			name: "PPS with out-of-range SPS ID",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // PPS ID = 0
				bw.WriteUE(32)        // Invalid SPS ID (> 31)
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "out of range",
		},
		{
			name:          "Corrupted PPS data",
			ppsData:       []byte{0x68, 0x00, 0x00, 0x00, 0x00}, // All zeros after header - will fail exp-Golomb parsing
			expectError:   true,
			errorContains: "too many leading zeros",
		},
		{
			name:          "Empty PPS data",
			ppsData:       []byte{},
			expectError:   true,
			errorContains: "too short",
		},
		{
			name:          "PPS with wrong NAL type",
			ppsData:       []byte{0x67, 0x42, 0x00, 0x1E, 0x90}, // SPS NAL header instead of PPS
			expectError:   true,
			errorContains: "invalid PPS NAL type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ppsID, spsID, err := ValidatePPSData(tt.ppsData)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorContains != "" && !bytes.Contains([]byte(err.Error()), []byte(tt.errorContains)) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if ppsID != tt.expectPPSID {
					t.Errorf("Expected PPS ID %d, got %d", tt.expectPPSID, ppsID)
				}
				if spsID != tt.expectSPSID {
					t.Errorf("Expected SPS ID %d, got %d", tt.expectSPSID, spsID)
				}
			}
		})
	}
}

// TestSPSValidation tests comprehensive SPS validation scenarios
func TestSPSValidation(t *testing.T) {
	tests := []struct {
		name          string
		spsData       []byte
		expectSPSID   uint8
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid SPS with NAL header",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // SPS ID = 0
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(0)         // pic_order_cnt_type
				bw.WriteUE(0)         // log2_max_pic_order_cnt_lsb_minus4
				bw.WriteUE(1)         // max_num_ref_frames
				bw.WriteBit(0)        // gaps_in_frame_num_value_allowed_flag
				bw.WriteUE(15)        // pic_width_in_mbs_minus1
				bw.WriteUE(15)        // pic_height_in_map_units_minus1
				bw.WriteBit(1)        // frame_mbs_only_flag
				bw.WriteBit(1)        // direct_8x8_inference_flag
				bw.WriteBit(0)        // frame_cropping_flag
				return bw.GetBytes()
			}(),
			expectSPSID: 0,
			expectError: false,
		},
		{
			name: "Valid SPS without NAL header",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(66, 8) // profile_idc
				bw.WriteBits(0, 8)  // constraint flags
				bw.WriteBits(30, 8) // level_idc
				bw.WriteUE(3)       // SPS ID = 3
				return bw.GetBytes()
			}(),
			expectSPSID: 3,
			expectError: false,
		},
		{
			name: "SPS with out-of-range ID",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(32)        // Invalid SPS ID (> 31)
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "out of range",
		},
		{
			name:          "Empty SPS data",
			spsData:       []byte{},
			expectError:   true,
			errorContains: "too short",
		},
		{
			name:          "SPS with wrong NAL type",
			spsData:       []byte{0x68, 0x00, 0x00, 0x00}, // PPS NAL header instead of SPS
			expectError:   true,
			errorContains: "invalid SPS NAL type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spsID, err := ValidateSPSData(tt.spsData)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorContains != "" && !bytes.Contains([]byte(err.Error()), []byte(tt.errorContains)) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if spsID != tt.expectSPSID {
					t.Errorf("Expected SPS ID %d, got %d", tt.expectSPSID, spsID)
				}
			}
		})
	}
}

// TestValidateAndFixNALUnit tests NAL unit validation and fixing
func TestValidateAndFixNALUnit(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		expectedType uint8
		expectFixed  bool
		expectError  bool
	}{
		{
			name: "Valid SPS with header",
			data: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // SPS ID
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(0)         // pic_order_cnt_type
				bw.WriteUE(0)         // log2_max_pic_order_cnt_lsb_minus4
				bw.WriteUE(1)         // max_num_ref_frames
				bw.WriteBit(0)        // gaps_in_frame_num_value_allowed_flag
				bw.WriteUE(15)        // pic_width_in_mbs_minus1
				bw.WriteUE(15)        // pic_height_in_map_units_minus1
				bw.WriteBit(1)        // frame_mbs_only_flag
				bw.WriteBit(1)        // direct_8x8_inference_flag
				bw.WriteBit(0)        // frame_cropping_flag
				bw.WriteBit(0)        // vui_parameters_present_flag
				return bw.GetBytes()
			}(),
			expectedType: 7,
			expectFixed:  false,
			expectError:  false,
		},
		{
			name: "SPS data missing header",
			data: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(66, 8) // profile_idc (no header)
				bw.WriteBits(0, 8)  // constraint flags
				bw.WriteBits(30, 8) // level_idc
				bw.WriteUE(0)       // SPS ID
				bw.WriteUE(0)       // log2_max_frame_num_minus4
				bw.WriteUE(0)       // pic_order_cnt_type
				bw.WriteUE(0)       // log2_max_pic_order_cnt_lsb_minus4
				bw.WriteUE(1)       // max_num_ref_frames
				bw.WriteBit(0)      // gaps_in_frame_num_value_allowed_flag
				bw.WriteUE(15)      // pic_width_in_mbs_minus1
				bw.WriteUE(15)      // pic_height_in_map_units_minus1
				bw.WriteBit(1)      // frame_mbs_only_flag
				bw.WriteBit(1)      // direct_8x8_inference_flag
				bw.WriteBit(0)      // frame_cropping_flag
				bw.WriteBit(0)      // vui_parameters_present_flag
				return bw.GetBytes()
			}(),
			expectedType: 7,
			expectFixed:  true,
			expectError:  false,
		},
		{
			name: "Valid PPS with header",
			data: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // PPS ID
				bw.WriteUE(0)         // SPS ID
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(0)         // num_slice_groups_minus1
				bw.WriteUE(0)         // num_ref_idx_l0_default_active_minus1
				bw.WriteUE(0)         // num_ref_idx_l1_default_active_minus1
				bw.WriteBit(0)        // weighted_pred_flag
				bw.WriteBits(0, 2)    // weighted_bipred_idc
				bw.WriteSE(0)         // pic_init_qp_minus26
				bw.WriteSE(0)         // pic_init_qs_minus26
				bw.WriteSE(0)         // chroma_qp_index_offset
				bw.WriteBit(0)        // deblocking_filter_control_present_flag
				bw.WriteBit(0)        // constrained_intra_pred_flag
				bw.WriteBit(0)        // redundant_pic_cnt_present_flag
				return bw.GetBytes()
			}(),
			expectedType: 8,
			expectFixed:  false,
			expectError:  false,
		},
		{
			name:         "Wrong NAL type",
			data:         []byte{0x65, 0x00, 0x00}, // IDR slice, not SPS
			expectedType: 7,
			expectError:  true,
		},
		{
			name:         "Empty data",
			data:         []byte{},
			expectedType: 7,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ValidateAndFixNALUnit(tt.data, tt.expectedType)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Check if NAL type is correct
			if len(result) > 0 {
				nalType := result[0] & 0x1F
				if nalType != tt.expectedType {
					t.Errorf("Expected NAL type %d, got %d", tt.expectedType, nalType)
				}
			}

			// Check if data was fixed
			if tt.expectFixed && bytes.Equal(result, tt.data) {
				t.Error("Expected data to be fixed but it wasn't changed")
			}
			if !tt.expectFixed && !bytes.Equal(result, tt.data) {
				t.Error("Expected data to remain unchanged but it was modified")
			}
		})
	}
}

// TestPPSCorruptionScenario tests the specific corruption scenario from logs
func TestPPSCorruptionScenario(t *testing.T) {
	// This tests the scenario where FFmpeg reports "pps_id 3199971767 out of range"
	// The value 3199971767 is 0xBE8DFAB7 in hex

	// Create corrupted PPS data that would produce this ID
	// In exp-Golomb, many leading zeros followed by a large value
	corruptedPPS := []byte{
		0x68, // PPS NAL header
		// Create an exp-Golomb value with many zeros that would overflow
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, // 55 leading zeros + 1 stop bit
		0xFF, 0xFF, 0xFF, 0xFF, // Value bits
	}

	ppsID, spsID, err := ValidatePPSData(corruptedPPS)
	if err == nil {
		t.Errorf("Expected error for corrupted PPS, but got PPS ID %d, SPS ID %d", ppsID, spsID)
	}
	if err != nil && !bytes.Contains([]byte(err.Error()), []byte("too many leading zeros")) {
		t.Errorf("Expected 'too many leading zeros' error, got: %v", err)
	}
}

// TestPPSParameterSetIntegration tests the full parameter set flow
func TestPPSParameterSetIntegration(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Create valid SPS
	spsData := func() []byte {
		bw := NewBitWriter()
		bw.WriteBits(0x67, 8) // SPS NAL header
		bw.WriteBits(66, 8)   // profile_idc
		bw.WriteBits(0, 8)    // constraint flags
		bw.WriteBits(30, 8)   // level_idc
		bw.WriteUE(7)         // SPS ID = 7
		bw.WriteUE(0)         // log2_max_frame_num_minus4
		bw.WriteUE(0)         // pic_order_cnt_type
		bw.WriteUE(0)         // log2_max_pic_order_cnt_lsb_minus4
		bw.WriteUE(1)         // max_num_ref_frames
		bw.WriteBit(0)        // gaps_in_frame_num_value_allowed_flag
		bw.WriteUE(15)        // pic_width_in_mbs_minus1
		bw.WriteUE(15)        // pic_height_in_map_units_minus1
		bw.WriteBit(1)        // frame_mbs_only_flag
		bw.WriteBit(1)        // direct_8x8_inference_flag
		bw.WriteBit(0)        // frame_cropping_flag
		return bw.GetBytes()
	}()

	// Create valid PPS that references SPS 7
	ppsData := func() []byte {
		bw := NewBitWriter()
		bw.WriteBits(0x68, 8) // PPS NAL header
		bw.WriteUE(0)         // PPS ID = 0
		bw.WriteUE(7)         // SPS ID = 7
		bw.WriteBit(0)        // entropy_coding_mode_flag
		bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
		bw.WriteUE(0)         // num_slice_groups_minus1
		bw.WriteUE(0)         // num_ref_idx_l0_default_active_minus1
		bw.WriteUE(0)         // num_ref_idx_l1_default_active_minus1
		bw.WriteBit(0)        // weighted_pred_flag
		bw.WriteBits(0, 2)    // weighted_bipred_idc
		bw.WriteSE(0)         // pic_init_qp_minus26
		bw.WriteSE(0)         // pic_init_qs_minus26
		bw.WriteSE(0)         // chroma_qp_index_offset
		bw.WriteBit(0)        // deblocking_filter_control_present_flag
		bw.WriteBit(0)        // constrained_intra_pred_flag
		bw.WriteBit(0)        // redundant_pic_cnt_present_flag
		return bw.GetBytes()
	}()

	// Add SPS first
	err := ctx.AddSPS(spsData)
	if err != nil {
		t.Fatalf("Failed to add SPS: %v", err)
	}

	// Add PPS
	err = ctx.AddPPS(ppsData)
	if err != nil {
		t.Fatalf("Failed to add PPS: %v", err)
	}

	// Create a frame that references PPS 0
	frame := &VideoFrame{
		ID: 1,
		NALUnits: []NALUnit{
			{
				Type: 5, // IDR slice
				Data: []byte{
					0x65, // IDR NAL header
					0x88, // first_mb_in_slice=0, slice_type=7
					0x84, // pic_parameter_set_id=0, frame_num=0
					0x00, // More data
					0x00, // More data
					0x00, // More data
					0x80, // Stop bit + padding
				},
			},
		},
	}

	// Check if frame can be decoded
	canDecode, reason := ctx.CanDecodeFrame(frame)
	if !canDecode {
		t.Errorf("Frame should be decodable but got: %s", reason)
	}

	// Generate decodable stream
	stream, err := ctx.GenerateDecodableStream(frame)
	if err != nil {
		t.Fatalf("Failed to generate decodable stream: %v", err)
	}

	// Verify stream structure
	// Should have: start code + SPS + start code + PPS + start code + slice
	if len(stream) < 20 {
		t.Error("Generated stream too short")
	}

	// Verify start codes
	startCode := []byte{0x00, 0x00, 0x00, 0x01}
	if !bytes.Equal(stream[0:4], startCode) {
		t.Error("Missing first start code")
	}
}

// TestPPSValidationPerformance tests validation performance
func TestPPSValidationPerformance(t *testing.T) {
	// Create a valid PPS
	ppsData := func() []byte {
		bw := NewBitWriter()
		bw.WriteBits(0x68, 8) // PPS NAL header
		bw.WriteUE(0)         // PPS ID
		bw.WriteUE(0)         // SPS ID
		bw.WriteBit(0)        // entropy_coding_mode_flag
		bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
		bw.WriteUE(0)         // num_slice_groups_minus1
		bw.WriteUE(0)         // num_ref_idx_l0_default_active_minus1
		bw.WriteUE(0)         // num_ref_idx_l1_default_active_minus1
		bw.WriteBit(0)        // weighted_pred_flag
		bw.WriteBits(0, 2)    // weighted_bipred_idc
		bw.WriteSE(0)         // pic_init_qp_minus26
		bw.WriteSE(0)         // pic_init_qs_minus26
		bw.WriteSE(0)         // chroma_qp_index_offset
		bw.WriteBit(0)        // deblocking_filter_control_present_flag
		bw.WriteBit(0)        // constrained_intra_pred_flag
		bw.WriteBit(0)        // redundant_pic_cnt_present_flag
		return bw.GetBytes()
	}()

	// Benchmark validation
	iterations := 10000
	start := time.Now()

	for i := 0; i < iterations; i++ {
		_, _, err := ValidatePPSData(ppsData)
		if err != nil {
			t.Fatalf("Validation failed: %v", err)
		}
	}

	elapsed := time.Since(start)
	perOp := elapsed / time.Duration(iterations)

	t.Logf("PPS validation performance: %v per operation (%d iterations in %v)", perOp, iterations, elapsed)

	// Ensure it's reasonably fast (< 10 microseconds per validation)
	if perOp > 10*time.Microsecond {
		t.Errorf("PPS validation too slow: %v per operation", perOp)
	}
}
