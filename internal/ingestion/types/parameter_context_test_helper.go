package types

import "time"

// testValidSPSBytes generates a valid H.264 Baseline SPS byte array that passes
// both EnhancedSPSValidation and parseSPS. Use this instead of hand-crafted short
// byte arrays that may be too short for full SPS validation.
func testValidSPSBytes(levelIDC byte) []byte {
	bw := NewBitWriter()
	bw.WriteBits(0x67, 8)             // NAL header (SPS)
	bw.WriteBits(0x42, 8)             // profile_idc (66 = Baseline)
	bw.WriteBits(0xC0, 8)             // constraint_set0=1, constraint_set1=1
	bw.WriteBits(uint32(levelIDC), 8) // level_idc
	bw.WriteUE(0)                     // seq_parameter_set_id = 0
	bw.WriteUE(0)                     // log2_max_frame_num_minus4 = 0
	bw.WriteUE(0)                     // pic_order_cnt_type = 0
	bw.WriteUE(0)                     // log2_max_pic_order_cnt_lsb_minus4 = 0
	bw.WriteUE(1)                     // max_num_ref_frames = 1
	bw.WriteBit(0)                    // gaps_in_frame_num_value_allowed_flag = 0
	bw.WriteUE(119)                   // pic_width_in_mbs_minus1 (1920px)
	bw.WriteUE(67)                    // pic_height_in_map_units_minus1 (1088px)
	bw.WriteBit(1)                    // frame_mbs_only_flag = 1
	return bw.GetBytes()
}

// testValidPPSBytes generates a valid H.264 PPS byte array that passes EnhancedPPSValidation.
func testValidPPSBytes() []byte {
	bw := NewBitWriter()
	bw.WriteBits(0x68, 8) // NAL header (PPS)
	bw.WriteUE(0)         // pic_parameter_set_id = 0
	bw.WriteUE(0)         // seq_parameter_set_id = 0
	bw.WriteBit(0)        // entropy_coding_mode_flag = 0
	bw.WriteBit(0)        // bottom_field_pic_order_in_frame_present_flag = 0
	bw.WriteUE(0)         // num_slice_groups_minus1 = 0 (no FMO)
	return bw.GetBytes()
}

// NewParameterSetContextForTest creates a parameter context without rate limiting for testing
func NewParameterSetContextForTest(codec CodecType, streamID string) *ParameterSetContext {
	return NewParameterSetContextWithConfig(codec, streamID, ParameterSetContextConfig{
		ValidatorUpdateRate:        0, // Disabled
		ValidatorMaxUpdatesPerHour: 0, // Disabled
		EnableVersioning:           true,
		MaxVersions:                10,
	})
}

// NewParameterSetContextWithValidatorForTest creates a parameter context with test-friendly rate limits
func NewParameterSetContextWithValidatorForTest(codec CodecType, streamID string) *ParameterSetContext {
	return NewParameterSetContextWithConfig(codec, streamID, ParameterSetContextConfig{
		ValidatorUpdateRate:        1 * time.Millisecond, // Very short for tests
		ValidatorMaxUpdatesPerHour: 10000,                // Very high for tests
		EnableVersioning:           true,
		MaxVersions:                10,
	})
}
