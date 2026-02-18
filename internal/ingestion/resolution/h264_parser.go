package resolution

import (
	"fmt"
)

// H264SPS represents parsed H.264 Sequence Parameter Set
type H264SPS struct {
	ProfileIdc                     uint32
	ConstraintSetFlags             uint32
	LevelIdc                       uint32
	SeqParameterSetId              uint32
	ChromaFormatIdc                uint32
	SeparateColourPlaneFlag        bool
	BitDepthLumaMinus8             uint32
	BitDepthChromaMinus8           uint32
	QpprimeYZeroTransformBypass    bool
	SeqScalingMatrixPresent        bool
	Log2MaxFrameNumMinus4          uint32
	PicOrderCntType                uint32
	Log2MaxPicOrderCntLsbMinus4    uint32
	DeltaPicOrderAlwaysZeroFlag    bool
	OffsetForNonRefPic             int32
	OffsetForTopToBottomField      int32
	NumRefFramesInPicOrderCntCycle uint32
	MaxNumRefFrames                uint32
	GapsInFrameNumValueAllowed     bool
	PicWidthInMbsMinus1            uint32
	PicHeightInMapUnitsMinus1      uint32
	FrameMbsOnlyFlag               bool
	MbAdaptiveFrameFieldFlag       bool
	Direct8x8InferenceFlag         bool
	FrameCroppingFlag              bool
	FrameCropLeftOffset            uint32
	FrameCropRightOffset           uint32
	FrameCropTopOffset             uint32
	FrameCropBottomOffset          uint32
	VuiParametersPresent           bool
}

// GetResolution calculates the actual video resolution from SPS parameters
func (sps *H264SPS) GetResolution() Resolution {
	// Calculate picture width and height in samples
	picWidthInSamples := (sps.PicWidthInMbsMinus1 + 1) * 16
	picHeightInSamples := (2 - uint32(boolToInt(sps.FrameMbsOnlyFlag))) * (sps.PicHeightInMapUnitsMinus1 + 1) * 16

	// Apply frame cropping if present
	width := picWidthInSamples
	height := picHeightInSamples

	if sps.FrameCroppingFlag {
		// Calculate chroma format multipliers
		var subWidthC, subHeightC uint32 = 1, 1
		switch sps.ChromaFormatIdc {
		case 1: // 4:2:0
			subWidthC, subHeightC = 2, 2
		case 2: // 4:2:2
			subWidthC, subHeightC = 2, 1
		case 3: // 4:4:4
			subWidthC, subHeightC = 1, 1
		}

		// Apply cropping
		cropUnitX := subWidthC
		cropUnitY := subHeightC * (2 - uint32(boolToInt(sps.FrameMbsOnlyFlag)))

		width -= (sps.FrameCropLeftOffset + sps.FrameCropRightOffset) * cropUnitX
		height -= (sps.FrameCropTopOffset + sps.FrameCropBottomOffset) * cropUnitY
	}

	return Resolution{
		Width:  int(width),
		Height: int(height),
	}
}

// parseH264SPS parses H.264 SPS with full standards compliance
func (d *Detector) parseH264SPSFull(spsData []byte) (Resolution, error) {
	if len(spsData) < 3 {
		return Resolution{}, fmt.Errorf("SPS too short")
	}

	// Remove emulation prevention bytes (0x03 after 0x00 0x00)
	cleanData := removeEmulationPrevention(spsData)

	br := NewBitReader(cleanData)

	sps := &H264SPS{}

	// Parse profile_idc
	var err error
	sps.ProfileIdc, err = br.ReadBits(8)
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read profile_idc: %w", err)
	}

	// Parse constraint_set_flags (6 bits) + reserved_zero_2bits (2 bits)
	sps.ConstraintSetFlags, err = br.ReadBits(8)
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read constraint flags: %w", err)
	}

	// Parse level_idc
	sps.LevelIdc, err = br.ReadBits(8)
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read level_idc: %w", err)
	}

	// Parse seq_parameter_set_id
	sps.SeqParameterSetId, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read seq_parameter_set_id: %w", err)
	}

	// Handle high profiles
	if sps.ProfileIdc == 100 || sps.ProfileIdc == 110 || sps.ProfileIdc == 122 ||
		sps.ProfileIdc == 244 || sps.ProfileIdc == 44 || sps.ProfileIdc == 83 ||
		sps.ProfileIdc == 86 || sps.ProfileIdc == 118 || sps.ProfileIdc == 128 ||
		sps.ProfileIdc == 138 {

		// chroma_format_idc
		sps.ChromaFormatIdc, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read chroma_format_idc: %w", err)
		}

		if sps.ChromaFormatIdc == 3 {
			// separate_colour_plane_flag
			bit, err := br.ReadBit()
			if err != nil {
				return Resolution{}, fmt.Errorf("failed to read separate_colour_plane_flag: %w", err)
			}
			sps.SeparateColourPlaneFlag = bit == 1
		}

		// bit_depth_luma_minus8
		sps.BitDepthLumaMinus8, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read bit_depth_luma_minus8: %w", err)
		}

		// bit_depth_chroma_minus8
		sps.BitDepthChromaMinus8, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read bit_depth_chroma_minus8: %w", err)
		}

		// qpprime_y_zero_transform_bypass_flag
		bit, err := br.ReadBit()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read qpprime_y_zero_transform_bypass_flag: %w", err)
		}
		sps.QpprimeYZeroTransformBypass = bit == 1

		// seq_scaling_matrix_present_flag
		bit, err = br.ReadBit()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read seq_scaling_matrix_present_flag: %w", err)
		}
		sps.SeqScalingMatrixPresent = bit == 1

		if sps.SeqScalingMatrixPresent {
			// Skip scaling matrices for now (complex parsing)
			maxMatrices := 8
			if sps.ChromaFormatIdc == 3 {
				maxMatrices = 12
			}

			for i := 0; i < maxMatrices; i++ {
				bit, err := br.ReadBit()
				if err != nil {
					return Resolution{}, fmt.Errorf("failed to read scaling_list_present_flag[%d]: %w", i, err)
				}
				if bit == 1 {
					// Skip the scaling list (would need complex parsing)
					if err := d.skipScalingList(br, i); err != nil {
						return Resolution{}, fmt.Errorf("failed to skip scaling list: %w", err)
					}
				}
			}
		}
	} else {
		// Default values for non-high profiles
		sps.ChromaFormatIdc = 1 // 4:2:0
	}

	// log2_max_frame_num_minus4
	sps.Log2MaxFrameNumMinus4, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read log2_max_frame_num_minus4: %w", err)
	}

	// pic_order_cnt_type
	sps.PicOrderCntType, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read pic_order_cnt_type: %w", err)
	}

	if sps.PicOrderCntType == 0 {
		// log2_max_pic_order_cnt_lsb_minus4
		sps.Log2MaxPicOrderCntLsbMinus4, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read log2_max_pic_order_cnt_lsb_minus4: %w", err)
		}
	} else if sps.PicOrderCntType == 1 {
		// delta_pic_order_always_zero_flag
		bit, err := br.ReadBit()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read delta_pic_order_always_zero_flag: %w", err)
		}
		sps.DeltaPicOrderAlwaysZeroFlag = bit == 1

		// offset_for_non_ref_pic
		sps.OffsetForNonRefPic, err = br.ReadSE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read offset_for_non_ref_pic: %w", err)
		}

		// offset_for_top_to_bottom_field
		sps.OffsetForTopToBottomField, err = br.ReadSE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read offset_for_top_to_bottom_field: %w", err)
		}

		// num_ref_frames_in_pic_order_cnt_cycle
		sps.NumRefFramesInPicOrderCntCycle, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read num_ref_frames_in_pic_order_cnt_cycle: %w", err)
		}

		// Skip offset_for_ref_frame array
		for i := uint32(0); i < sps.NumRefFramesInPicOrderCntCycle; i++ {
			_, err = br.ReadSE()
			if err != nil {
				return Resolution{}, fmt.Errorf("failed to read offset_for_ref_frame[%d]: %w", i, err)
			}
		}
	}

	// max_num_ref_frames
	sps.MaxNumRefFrames, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read max_num_ref_frames: %w", err)
	}

	// gaps_in_frame_num_value_allowed_flag
	bit, err := br.ReadBit()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read gaps_in_frame_num_value_allowed_flag: %w", err)
	}
	sps.GapsInFrameNumValueAllowed = bit == 1

	// pic_width_in_mbs_minus1
	sps.PicWidthInMbsMinus1, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read pic_width_in_mbs_minus1: %w", err)
	}

	// pic_height_in_map_units_minus1
	sps.PicHeightInMapUnitsMinus1, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read pic_height_in_map_units_minus1: %w", err)
	}

	// frame_mbs_only_flag
	bit, err = br.ReadBit()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read frame_mbs_only_flag: %w", err)
	}
	sps.FrameMbsOnlyFlag = bit == 1

	if !sps.FrameMbsOnlyFlag {
		// mb_adaptive_frame_field_flag
		bit, err = br.ReadBit()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read mb_adaptive_frame_field_flag: %w", err)
		}
		sps.MbAdaptiveFrameFieldFlag = bit == 1
	}

	// direct_8x8_inference_flag
	bit, err = br.ReadBit()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read direct_8x8_inference_flag: %w", err)
	}
	sps.Direct8x8InferenceFlag = bit == 1

	// frame_cropping_flag
	bit, err = br.ReadBit()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read frame_cropping_flag: %w", err)
	}
	sps.FrameCroppingFlag = bit == 1

	if sps.FrameCroppingFlag {
		// frame_crop_left_offset
		sps.FrameCropLeftOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read frame_crop_left_offset: %w", err)
		}

		// frame_crop_right_offset
		sps.FrameCropRightOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read frame_crop_right_offset: %w", err)
		}

		// frame_crop_top_offset
		sps.FrameCropTopOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read frame_crop_top_offset: %w", err)
		}

		// frame_crop_bottom_offset
		sps.FrameCropBottomOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read frame_crop_bottom_offset: %w", err)
		}
	}

	// Calculate resolution
	return sps.GetResolution(), nil
}

// skipScalingList skips a scaling list in the SPS
func (d *Detector) skipScalingList(br *BitReader, index int) error {
	size := 16
	if index >= 6 {
		size = 64
	}

	lastScale := int32(8)
	nextScale := int32(8)

	for i := 0; i < size; i++ {
		if nextScale != 0 {
			deltaScale, err := br.ReadSE()
			if err != nil {
				return err
			}
			nextScale = (lastScale + deltaScale + 256) % 256
		}
		if nextScale != 0 {
			lastScale = nextScale
		}
	}

	return nil
}

// removeEmulationPrevention removes emulation prevention bytes (0x03) from
// the byte sequence 0x00 0x00 0x03 0xNN where NN is 0x00, 0x01, 0x02, or 0x03.
// Per H.264 Section 7.4.1, only these specific patterns are emulation prevention.
func removeEmulationPrevention(data []byte) []byte {
	if len(data) < 3 {
		return data
	}

	result := make([]byte, 0, len(data))

	for i := 0; i < len(data); i++ {
		if i >= 2 && data[i-2] == 0x00 && data[i-1] == 0x00 && data[i] == 0x03 {
			// Only strip 0x03 if followed by 0x00-0x03 (or at end of data)
			// Per H.264 Section 7.4.1
			if i+1 >= len(data) || data[i+1] <= 0x03 {
				continue
			}
		}
		result = append(result, data[i])
	}

	return result
}

// boolToInt converts bool to int for calculations
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
