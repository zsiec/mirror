package types

import (
	"testing"
)

func TestEnhancedSPSValidation(t *testing.T) {
	tests := []struct {
		name          string
		spsData       []byte
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid SPS with POC type 0",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc (Baseline)
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(0)         // pic_order_cnt_type = 0 (VALID)
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
			expectError: false,
		},
		{
			name: "Invalid SPS with POC type 32",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc (Baseline)
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(32)        // pic_order_cnt_type = 32 (INVALID - causes FFmpeg error)
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "illegal POC type 32",
		},
		{
			name: "Valid SPS with POC type 1",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc (Baseline)
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(1)         // pic_order_cnt_type = 1 (VALID)
				// POC type 1 requires additional fields
				bw.WriteBit(0) // delta_pic_order_always_zero_flag
				bw.WriteSE(0)  // offset_for_non_ref_pic
				bw.WriteSE(0)  // offset_for_top_to_bottom_field
				bw.WriteUE(0)  // num_ref_frames_in_pic_order_cnt_cycle
				bw.WriteUE(1)  // max_num_ref_frames
				bw.WriteBit(0) // gaps_in_frame_num_value_allowed_flag
				bw.WriteUE(15) // pic_width_in_mbs_minus1
				bw.WriteUE(15) // pic_height_in_map_units_minus1
				bw.WriteBit(1) // frame_mbs_only_flag
				bw.WriteBit(1) // direct_8x8_inference_flag
				bw.WriteBit(0) // frame_cropping_flag
				bw.WriteBit(0) // vui_parameters_present_flag
				return bw.GetBytes()
			}(),
			expectError: false,
		},
		{
			name: "Valid SPS with POC type 2",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc (Baseline)
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(2)         // pic_order_cnt_type = 2 (VALID)
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
			expectError: false,
		},
		{
			name: "Invalid SPS with POC type 255",
			spsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x67, 8) // SPS NAL header
				bw.WriteBits(66, 8)   // profile_idc (Baseline)
				bw.WriteBits(0, 8)    // constraint flags
				bw.WriteBits(30, 8)   // level_idc
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteUE(0)         // log2_max_frame_num_minus4
				bw.WriteUE(255)       // pic_order_cnt_type = 255 (INVALID)
				// Add some padding to ensure the data isn't truncated
				bw.WriteBits(0xFF, 8) // padding byte
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "illegal POC type 255",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnhancedSPSValidation(tt.spsData, "test-stream")

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectError && tt.errorContains != "" && err != nil {
				if !contains(err.Error(), tt.errorContains) {
					t.Errorf("Error message doesn't contain expected text.\nGot: %s\nWant substring: %s",
						err.Error(), tt.errorContains)
				}
			}
		})
	}
}

func TestEnhancedPPSValidation(t *testing.T) {
	tests := []struct {
		name          string
		ppsData       []byte
		expectError   bool
		errorContains string
	}{
		{
			name: "Valid PPS without FMO",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // pic_parameter_set_id
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(0)         // num_slice_groups_minus1 = 0 (NO FMO)
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
			expectError: false,
		},
		{
			name: "PPS with FMO (num_slice_groups=2)",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // pic_parameter_set_id
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(1)         // num_slice_groups_minus1 = 1 (2 slice groups - FMO!)
				bw.WriteUE(0)         // slice_group_map_type
				// Add padding to ensure data isn't truncated
				bw.WriteBits(0xFF, 8) // padding
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "FMO detected",
		},
		{
			name: "PPS with invalid slice_group_map_type",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // pic_parameter_set_id
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(1)         // num_slice_groups_minus1 = 1 (FMO enabled)
				bw.WriteUE(7)         // slice_group_map_type = 7 (INVALID, must be 0-6)
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "invalid slice_group_map_type",
		},
		{
			name: "PPS with max slice groups (8)",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // pic_parameter_set_id
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(7)         // num_slice_groups_minus1 = 7 (8 slice groups - max allowed)
				bw.WriteUE(0)         // slice_group_map_type
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "FMO detected",
		},
		{
			name: "PPS with too many slice groups",
			ppsData: func() []byte {
				bw := NewBitWriter()
				bw.WriteBits(0x68, 8) // PPS NAL header
				bw.WriteUE(0)         // pic_parameter_set_id
				bw.WriteUE(0)         // seq_parameter_set_id
				bw.WriteBit(0)        // entropy_coding_mode_flag
				bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag
				bw.WriteUE(8)         // num_slice_groups_minus1 = 8 (9 slice groups - INVALID)
				return bw.GetBytes()
			}(),
			expectError:   true,
			errorContains: "invalid num_slice_groups_minus1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnhancedPPSValidation(tt.ppsData, "test-stream")

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectError && tt.errorContains != "" && err != nil {
				if !contains(err.Error(), tt.errorContains) {
					t.Errorf("Error message doesn't contain expected text.\nGot: %s\nWant substring: %s",
						err.Error(), tt.errorContains)
				}
			}
		})
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
