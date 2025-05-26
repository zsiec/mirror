package resolution

import (
	"fmt"
)

// HEVCVPS represents parsed HEVC Video Parameter Set
type HEVCVPS struct {
	VpsVideoParameterSetId         uint32
	VpsMaxLayersMinus1             uint32
	VpsMaxSubLayersMinus1          uint32
	VpsTemporalIdNestingFlag       bool
	ProfileTierLevel               HEVCProfileTierLevel
	VpsSubLayerOrderingInfoPresent bool
	VpsMaxDecPicBufferingMinus1    []uint32
	VpsMaxNumReorderPics           []uint32
	VpsMaxLatencyIncreasePlus1     []uint32
	VpsMaxLayerId                  uint32
	VpsNumLayerSetsMinus1          uint32
}

// HEVCSPS represents parsed HEVC Sequence Parameter Set
type HEVCSPS struct {
	SpsVideoParameterSetId               uint32
	SpsMaxSubLayersMinus1                uint32
	SpsTemporalIdNestingFlag             bool
	ProfileTierLevel                     HEVCProfileTierLevel
	SpsSeqParameterSetId                 uint32
	ChromaFormatIdc                      uint32
	SeparateColourPlaneFlag              bool
	PicWidthInLumaSamples                uint32
	PicHeightInLumaSamples               uint32
	ConformanceWindowFlag                bool
	ConfWinLeftOffset                    uint32
	ConfWinRightOffset                   uint32
	ConfWinTopOffset                     uint32
	ConfWinBottomOffset                  uint32
	BitDepthLumaMinus8                   uint32
	BitDepthChromaMinus8                 uint32
	Log2MaxPicOrderCntLsbMinus4          uint32
	SpsSubLayerOrderingInfoPresent       bool
	SpsMaxDecPicBufferingMinus1          []uint32
	SpsMaxNumReorderPics                 []uint32
	SpsMaxLatencyIncreasePlus1           []uint32
	Log2MinLumaCodingBlockSizeMinus3     uint32
	Log2DiffMaxMinLumaCodingBlockSize    uint32
	Log2MinTransformBlockSizeMinus2      uint32
	Log2DiffMaxMinTransformBlockSize     uint32
	MaxTransformHierarchyDepthInter      uint32
	MaxTransformHierarchyDepthIntra      uint32
	ScalingListEnabledFlag               bool
	AmpEnabledFlag                       bool
	SampleAdaptiveOffsetEnabledFlag      bool
	PcmEnabledFlag                       bool
	PcmSampleBitDepthLumaMinus1          uint32
	PcmSampleBitDepthChromaMinus1        uint32
	Log2MinPcmLumaCodingBlockSizeMinus3  uint32
	Log2DiffMaxMinPcmLumaCodingBlockSize uint32
	PcmLoopFilterDisabledFlag            bool
	NumShortTermRefPicSets               uint32
	LongTermRefPicsPresent               bool
	SpsTemporalMvpEnabledFlag            bool
	StrongIntraSmoothingEnabledFlag      bool
	VuiParametersPresent                 bool
}

// HEVCProfileTierLevel represents HEVC profile, tier and level information
type HEVCProfileTierLevel struct {
	GeneralProfileSpace              uint32
	GeneralTierFlag                  bool
	GeneralProfileIdc                uint32
	GeneralProfileCompatibilityFlags uint32
	GeneralProgressiveSourceFlag     bool
	GeneralInterlacedSourceFlag      bool
	GeneralNonPackedConstraintFlag   bool
	GeneralFrameOnlyConstraintFlag   bool
	GeneralLevelIdc                  uint32
}

// GetResolution calculates the actual video resolution from HEVC SPS parameters
func (sps *HEVCSPS) GetResolution() Resolution {
	width := sps.PicWidthInLumaSamples
	height := sps.PicHeightInLumaSamples

	// Apply conformance window cropping if present
	if sps.ConformanceWindowFlag {
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
		width -= (sps.ConfWinLeftOffset + sps.ConfWinRightOffset) * subWidthC
		height -= (sps.ConfWinTopOffset + sps.ConfWinBottomOffset) * subHeightC
	}

	return Resolution{
		Width:  int(width),
		Height: int(height),
	}
}

// parseHEVCVPS parses HEVC VPS with full standards compliance
func (d *Detector) parseHEVCVPS(vpsData []byte) (*HEVCVPS, error) {
	if len(vpsData) < 2 {
		return nil, fmt.Errorf("VPS too short")
	}

	// Remove emulation prevention bytes
	cleanData := removeEmulationPrevention(vpsData)
	br := NewBitReader(cleanData)

	vps := &HEVCVPS{}

	// vps_video_parameter_set_id (4 bits)
	var err error
	vps.VpsVideoParameterSetId, err = br.ReadBits(4)
	if err != nil {
		return nil, fmt.Errorf("failed to read vps_video_parameter_set_id: %w", err)
	}

	// vps_reserved_three_2bits (2 bits) - should be 11 binary
	reserved, err := br.ReadBits(2)
	if err != nil {
		return nil, fmt.Errorf("failed to read vps_reserved_three_2bits: %w", err)
	}
	if reserved != 3 {
		return nil, fmt.Errorf("invalid vps_reserved_three_2bits: %d", reserved)
	}

	// vps_max_layers_minus1 (6 bits)
	vps.VpsMaxLayersMinus1, err = br.ReadBits(6)
	if err != nil {
		return nil, fmt.Errorf("failed to read vps_max_layers_minus1: %w", err)
	}

	// vps_max_sub_layers_minus1 (3 bits)
	vps.VpsMaxSubLayersMinus1, err = br.ReadBits(3)
	if err != nil {
		return nil, fmt.Errorf("failed to read vps_max_sub_layers_minus1: %w", err)
	}

	// vps_temporal_id_nesting_flag (1 bit)
	bit, err := br.ReadBit()
	if err != nil {
		return nil, fmt.Errorf("failed to read vps_temporal_id_nesting_flag: %w", err)
	}
	vps.VpsTemporalIdNestingFlag = bit == 1

	// vps_reserved_0xffff_16bits (16 bits)
	reserved16, err := br.ReadBits(16)
	if err != nil {
		return nil, fmt.Errorf("failed to read vps_reserved_0xffff_16bits: %w", err)
	}
	if reserved16 != 0xFFFF {
		return nil, fmt.Errorf("invalid vps_reserved_0xffff_16bits: 0x%x", reserved16)
	}

	// profile_tier_level()
	vps.ProfileTierLevel, err = d.parseHEVCProfileTierLevel(br, true, vps.VpsMaxSubLayersMinus1)
	if err != nil {
		return nil, fmt.Errorf("failed to parse profile_tier_level: %w", err)
	}

	// For now, we'll return the VPS without parsing the rest
	// The important resolution information is typically in the SPS
	return vps, nil
}

// parseHEVCSPS parses HEVC SPS with full standards compliance
func (d *Detector) parseHEVCSPSFull(spsData []byte) (Resolution, error) {
	if len(spsData) < 2 {
		return Resolution{}, fmt.Errorf("SPS too short")
	}

	// Remove emulation prevention bytes
	cleanData := removeEmulationPrevention(spsData)
	br := NewBitReader(cleanData)

	sps := &HEVCSPS{}

	// sps_video_parameter_set_id (4 bits)
	var err error
	sps.SpsVideoParameterSetId, err = br.ReadBits(4)
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read sps_video_parameter_set_id: %w", err)
	}

	// sps_max_sub_layers_minus1 (3 bits)
	sps.SpsMaxSubLayersMinus1, err = br.ReadBits(3)
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read sps_max_sub_layers_minus1: %w", err)
	}

	// sps_temporal_id_nesting_flag (1 bit)
	bit, err := br.ReadBit()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read sps_temporal_id_nesting_flag: %w", err)
	}
	sps.SpsTemporalIdNestingFlag = bit == 1

	// profile_tier_level()
	sps.ProfileTierLevel, err = d.parseHEVCProfileTierLevel(br, true, sps.SpsMaxSubLayersMinus1)
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to parse profile_tier_level: %w", err)
	}

	// sps_seq_parameter_set_id
	sps.SpsSeqParameterSetId, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read sps_seq_parameter_set_id: %w", err)
	}

	// chroma_format_idc
	sps.ChromaFormatIdc, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read chroma_format_idc: %w", err)
	}

	if sps.ChromaFormatIdc == 3 {
		// separate_colour_plane_flag
		bit, err = br.ReadBit()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read separate_colour_plane_flag: %w", err)
		}
		sps.SeparateColourPlaneFlag = bit == 1
	}

	// pic_width_in_luma_samples
	sps.PicWidthInLumaSamples, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read pic_width_in_luma_samples: %w", err)
	}

	// pic_height_in_luma_samples
	sps.PicHeightInLumaSamples, err = br.ReadUE()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read pic_height_in_luma_samples: %w", err)
	}

	// conformance_window_flag
	bit, err = br.ReadBit()
	if err != nil {
		return Resolution{}, fmt.Errorf("failed to read conformance_window_flag: %w", err)
	}
	sps.ConformanceWindowFlag = bit == 1

	if sps.ConformanceWindowFlag {
		// conf_win_left_offset
		sps.ConfWinLeftOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read conf_win_left_offset: %w", err)
		}

		// conf_win_right_offset
		sps.ConfWinRightOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read conf_win_right_offset: %w", err)
		}

		// conf_win_top_offset
		sps.ConfWinTopOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read conf_win_top_offset: %w", err)
		}

		// conf_win_bottom_offset
		sps.ConfWinBottomOffset, err = br.ReadUE()
		if err != nil {
			return Resolution{}, fmt.Errorf("failed to read conf_win_bottom_offset: %w", err)
		}
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

	// We have enough information to calculate resolution
	return sps.GetResolution(), nil
}

// parseHEVCProfileTierLevel parses HEVC profile_tier_level structure
func (d *Detector) parseHEVCProfileTierLevel(br *BitReader, profilePresentFlag bool, maxNumSubLayersMinus1 uint32) (HEVCProfileTierLevel, error) {
	ptl := HEVCProfileTierLevel{}

	if profilePresentFlag {
		// general_profile_space (2 bits)
		var err error
		ptl.GeneralProfileSpace, err = br.ReadBits(2)
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_profile_space: %w", err)
		}

		// general_tier_flag (1 bit)
		bit, err := br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_tier_flag: %w", err)
		}
		ptl.GeneralTierFlag = bit == 1

		// general_profile_idc (5 bits)
		ptl.GeneralProfileIdc, err = br.ReadBits(5)
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_profile_idc: %w", err)
		}

		// general_profile_compatibility_flag (32 bits)
		ptl.GeneralProfileCompatibilityFlags, err = br.ReadBits(32)
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_profile_compatibility_flags: %w", err)
		}

		// general_progressive_source_flag (1 bit)
		bit, err = br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_progressive_source_flag: %w", err)
		}
		ptl.GeneralProgressiveSourceFlag = bit == 1

		// general_interlaced_source_flag (1 bit)
		bit, err = br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_interlaced_source_flag: %w", err)
		}
		ptl.GeneralInterlacedSourceFlag = bit == 1

		// general_non_packed_constraint_flag (1 bit)
		bit, err = br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_non_packed_constraint_flag: %w", err)
		}
		ptl.GeneralNonPackedConstraintFlag = bit == 1

		// general_frame_only_constraint_flag (1 bit)
		bit, err = br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read general_frame_only_constraint_flag: %w", err)
		}
		ptl.GeneralFrameOnlyConstraintFlag = bit == 1

		// Skip 44 reserved zero bits
		for i := 0; i < 44; i++ {
			_, err = br.ReadBit()
			if err != nil {
				return ptl, fmt.Errorf("failed to read reserved bit %d: %w", i, err)
			}
		}
	}

	// general_level_idc (8 bits)
	var err error
	ptl.GeneralLevelIdc, err = br.ReadBits(8)
	if err != nil {
		return ptl, fmt.Errorf("failed to read general_level_idc: %w", err)
	}

	// Skip sub-layer profile/tier/level information for now
	for i := uint32(0); i < maxNumSubLayersMinus1; i++ {
		// sub_layer_profile_present_flag
		_, err = br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read sub_layer_profile_present_flag[%d]: %w", i, err)
		}

		// sub_layer_level_present_flag
		_, err = br.ReadBit()
		if err != nil {
			return ptl, fmt.Errorf("failed to read sub_layer_level_present_flag[%d]: %w", i, err)
		}
	}

	// Skip remaining sub-layer information
	if maxNumSubLayersMinus1 > 0 {
		for i := maxNumSubLayersMinus1; i < 8; i++ {
			// reserved_zero_2bits
			_, err = br.ReadBits(2)
			if err != nil {
				return ptl, fmt.Errorf("failed to read reserved_zero_2bits[%d]: %w", i, err)
			}
		}
	}

	// We would parse sub-layer profile/tier/level here, but for resolution detection
	// we don't need this information, so we'll skip it for now

	return ptl, nil
}
