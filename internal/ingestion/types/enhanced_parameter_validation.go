package types

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"github.com/zsiec/mirror/internal/logger"
)

// EnhancedSPSValidation performs comprehensive SPS validation including POC type checks
func EnhancedSPSValidation(data []byte, streamID string) error {
	// Extract RBSP data based on whether NAL header is present
	var rbspData []byte
	var err error

	nalType := data[0] & 0x1F
	if len(data) > 0 && (nalType == 7 || nalType == 15) { // SPS NAL unit types
		rbspData, err = ExtractRBSPFromNALUnit(data)
	} else {
		rbspData, err = ExtractRBSPFromPayload(data)
	}
	if err != nil {
		return fmt.Errorf("failed to extract RBSP: %w", err)
	}

	br := NewBitReader(rbspData)

	// Parse profile_idc
	profileIDC, err := br.ReadBits(8)
	if err != nil {
		return fmt.Errorf("failed to read profile_idc: %w", err)
	}

	// Skip constraint flags (8 bits)
	_, err = br.ReadBits(8)
	if err != nil {
		return fmt.Errorf("failed to read constraint flags: %w", err)
	}

	// Parse level_idc
	_, err = br.ReadBits(8)
	if err != nil {
		return fmt.Errorf("failed to read level_idc: %w", err)
	}

	// Parse seq_parameter_set_id
	spsID, err := br.ReadUE()
	if err != nil {
		return fmt.Errorf("failed to read sps_id: %w", err)
	}
	if spsID > 31 {
		return fmt.Errorf("sps_id %d out of range (0-31)", spsID)
	}

	// Handle high profile specific fields
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 || profileIDC == 244 ||
		profileIDC == 44 || profileIDC == 83 || profileIDC == 86 || profileIDC == 118 ||
		profileIDC == 128 || profileIDC == 138 {

		// Read chroma_format_idc
		chromaFormatIDC, err := br.ReadUE()
		if err != nil {
			return fmt.Errorf("failed to read chroma_format_idc: %w", err)
		}
		if chromaFormatIDC > 3 {
			return fmt.Errorf("invalid chroma_format_idc: %d (must be 0-3)", chromaFormatIDC)
		}

		if chromaFormatIDC == 3 {
			// separate_colour_plane_flag
			_, err := br.ReadBit()
			if err != nil {
				return fmt.Errorf("failed to read separate_colour_plane_flag: %w", err)
			}
		}

		// bit_depth_luma_minus8
		bitDepthLuma, err := br.ReadUE()
		if err != nil {
			return fmt.Errorf("failed to read bit_depth_luma_minus8: %w", err)
		}
		if bitDepthLuma > 6 {
			return fmt.Errorf("invalid bit_depth_luma_minus8: %d (must be 0-6)", bitDepthLuma)
		}

		// bit_depth_chroma_minus8
		bitDepthChroma, err := br.ReadUE()
		if err != nil {
			return fmt.Errorf("failed to read bit_depth_chroma_minus8: %w", err)
		}
		if bitDepthChroma > 6 {
			return fmt.Errorf("invalid bit_depth_chroma_minus8: %d (must be 0-6)", bitDepthChroma)
		}

		// qpprime_y_zero_transform_bypass_flag
		_, err = br.ReadBit()
		if err != nil {
			return fmt.Errorf("failed to read qpprime_y_zero_transform_bypass_flag: %w", err)
		}

		// seq_scaling_matrix_present_flag
		scalingMatrixPresent, err := br.ReadBit()
		if err != nil {
			return fmt.Errorf("failed to read seq_scaling_matrix_present_flag: %w", err)
		}

		if scalingMatrixPresent == 1 {
			// Skip scaling matrices for now - complex parsing
			logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
			logger.WithFields(map[string]interface{}{
				"stream_id": streamID,
				"sps_id":    spsID,
			}).Warn("SPS contains scaling matrices - skipping detailed validation")
			return nil // Exit early, can't parse rest reliably
		}
	}

	// Parse log2_max_frame_num_minus4
	log2MaxFrameNum, err := br.ReadUE()
	if err != nil {
		return fmt.Errorf("failed to read log2_max_frame_num_minus4: %w", err)
	}
	if log2MaxFrameNum > 12 {
		return fmt.Errorf("invalid log2_max_frame_num_minus4: %d (must be 0-12)", log2MaxFrameNum)
	}

	// CRITICAL: Parse and validate pic_order_cnt_type
	pocType, err := br.ReadUE()
	if err != nil {
		return fmt.Errorf("failed to read pic_order_cnt_type: %w", err)
	}

	// This is the critical validation for the FFmpeg error
	if pocType > 2 {
		logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
		logger.WithFields(map[string]interface{}{
			"stream_id": streamID,
			"sps_id":    spsID,
			"poc_type":  pocType,
		}).Error("Invalid POC type detected - FFmpeg will reject this")
		return fmt.Errorf("invalid pic_order_cnt_type: %d (must be 0-2, FFmpeg reports 'illegal POC type %d')", pocType, pocType)
	}

	// Log valid POC type for debugging
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	logger.WithFields(map[string]interface{}{
		"stream_id": streamID,
		"sps_id":    spsID,
		"poc_type":  pocType,
	}).Debug("Valid POC type in SPS")

	return nil
}

// EnhancedPPSValidation performs comprehensive PPS validation including FMO checks
func EnhancedPPSValidation(data []byte, streamID string) error {
	// Extract RBSP data based on whether NAL header is present
	var rbspData []byte
	var err error

	nalType := data[0] & 0x1F
	if len(data) > 0 && nalType == 8 { // PPS NAL unit type
		rbspData, err = ExtractRBSPFromNALUnit(data)
	} else {
		rbspData, err = ExtractRBSPFromPayload(data)
	}
	if err != nil {
		return fmt.Errorf("failed to extract RBSP: %w", err)
	}

	br := NewBitReader(rbspData)

	// Parse pic_parameter_set_id
	ppsID, err := br.ReadUE()
	if err != nil {
		return fmt.Errorf("failed to read pps_id: %w", err)
	}
	if ppsID > 255 {
		return fmt.Errorf("pps_id %d out of range (0-255)", ppsID)
	}

	// Parse seq_parameter_set_id
	spsID, err := br.ReadUE()
	if err != nil {
		return fmt.Errorf("failed to read sps_id: %w", err)
	}
	if spsID > 31 {
		return fmt.Errorf("sps_id %d out of range (0-31)", spsID)
	}

	// entropy_coding_mode_flag
	entropyCodingMode, err := br.ReadBit()
	if err != nil {
		return fmt.Errorf("failed to read entropy_coding_mode_flag: %w", err)
	}

	// bottom_field_pic_order_in_frame_present_flag
	_, err = br.ReadBit()
	if err != nil {
		return fmt.Errorf("failed to read bottom_field_pic_order_in_frame_present_flag: %w", err)
	}

	// CRITICAL: Parse num_slice_groups_minus1 (FMO related)
	numSliceGroupsMinus1, err := br.ReadUE()
	if err != nil {
		return fmt.Errorf("failed to read num_slice_groups_minus1: %w", err)
	}

	// Check if FMO is used (num_slice_groups_minus1 > 0)
	if numSliceGroupsMinus1 > 0 {
		logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
		logger.WithFields(map[string]interface{}{
			"stream_id":        streamID,
			"pps_id":           ppsID,
			"num_slice_groups": numSliceGroupsMinus1 + 1,
		}).Warn("PPS uses FMO (Flexible Macroblock Ordering) - FFmpeg may report 'FMO is not implemented'")

		// H.264 spec: num_slice_groups_minus1 shall be in the range of 0 to 7
		if numSliceGroupsMinus1 > 7 {
			return fmt.Errorf("invalid num_slice_groups_minus1: %d (must be 0-7)", numSliceGroupsMinus1)
		}

		// Parse slice_group_map_type
		sliceGroupMapType, err := br.ReadUE()
		if err != nil {
			return fmt.Errorf("failed to read slice_group_map_type: %w", err)
		}

		// H.264 spec: slice_group_map_type shall be in the range of 0 to 6
		if sliceGroupMapType > 6 {
			return fmt.Errorf("invalid slice_group_map_type: %d (must be 0-6)", sliceGroupMapType)
		}

		logger.WithFields(map[string]interface{}{
			"stream_id":            streamID,
			"pps_id":               ppsID,
			"slice_group_map_type": sliceGroupMapType,
		}).Warn("FMO slice_group_map_type detected - FFmpeg decoder may fail")

		// We can't parse the rest reliably with FMO, so return early with warning
		return fmt.Errorf("FMO detected in PPS (num_slice_groups=%d, map_type=%d) - FFmpeg does not support FMO",
			numSliceGroupsMinus1+1, sliceGroupMapType)
	}

	// Continue with normal PPS parsing if no FMO
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	logger.WithFields(map[string]interface{}{
		"stream_id":      streamID,
		"pps_id":         ppsID,
		"sps_id":         spsID,
		"entropy_coding": entropyCodingMode,
		"fmo_enabled":    false,
	}).Debug("Valid PPS without FMO")

	return nil
}

// ValidateParameterSetsForFFmpeg validates parameter sets to catch issues before FFmpeg processing
func ValidateParameterSetsForFFmpeg(ctx *ParameterSetContext) error {
	ctx.mu.RLock()
	defer ctx.mu.RUnlock()

	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	// Validate all SPS
	for spsID, sps := range ctx.spsMap {
		if sps.Data != nil && len(sps.Data) > 0 {
			if err := EnhancedSPSValidation(sps.Data, ctx.streamID); err != nil {
				logger.WithFields(map[string]interface{}{
					"stream_id": ctx.streamID,
					"sps_id":    spsID,
					"error":     err.Error(),
				}).Error("SPS validation failed - FFmpeg will likely reject this stream")
				return fmt.Errorf("SPS %d validation failed: %w", spsID, err)
			}
		}
	}

	// Validate all PPS
	for ppsID, pps := range ctx.ppsMap {
		if pps.Data != nil && len(pps.Data) > 0 {
			if err := EnhancedPPSValidation(pps.Data, ctx.streamID); err != nil {
				logger.WithFields(map[string]interface{}{
					"stream_id": ctx.streamID,
					"pps_id":    ppsID,
					"error":     err.Error(),
				}).Error("PPS validation failed - FFmpeg will likely reject this stream")
				return fmt.Errorf("PPS %d validation failed: %w", ppsID, err)
			}
		}
	}

	return nil
}
