package types

import (
	"fmt"
)

// H.264 bitstream parser for parameter sets and slice headers

// BitReader provides bit-level reading for H.264 parsing
type BitReader struct {
	data    []byte
	bytePos int
	bitPos  int
}

// NewBitReader creates a new bit reader
func NewBitReader(data []byte) *BitReader {
	return &BitReader{data: data}
}

// ReadBit reads a single bit
func (br *BitReader) ReadBit() (uint8, error) {
	if br.bytePos >= len(br.data) {
		return 0, fmt.Errorf("end of data reached")
	}

	bit := (br.data[br.bytePos] >> (7 - br.bitPos)) & 1
	br.bitPos++

	if br.bitPos >= 8 {
		br.bitPos = 0
		br.bytePos++
	}

	return bit, nil
}

// ReadBits reads multiple bits
func (br *BitReader) ReadBits(n int) (uint32, error) {
	// Safety check: prevent reading too many bits
	if n < 0 || n > 32 {
		return 0, fmt.Errorf("invalid number of bits to read: %d (must be 0-32)", n)
	}

	if n == 0 {
		return 0, nil
	}

	// Check if we have enough bits remaining
	bitsRemaining := (len(br.data)-br.bytePos)*8 - br.bitPos
	if n > bitsRemaining {
		return 0, fmt.Errorf("insufficient bits: requested %d, have %d", n, bitsRemaining)
	}

	var result uint32
	for i := 0; i < n; i++ {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, err
		}
		result = (result << 1) | uint32(bit)
	}
	return result, nil
}

// GetBitPosition returns the current bit position (absolute bit offset from start)
func (br *BitReader) GetBitPosition() int {
	return br.bytePos*8 + br.bitPos
}

// SeekToBit seeks to a specific bit position
func (br *BitReader) SeekToBit(bitPos int) {
	br.bytePos = bitPos / 8
	br.bitPos = bitPos % 8
}

// HasMoreBits returns true if there are more bits to read
func (br *BitReader) HasMoreBits() bool {
	// When bytePos == len(br.data), we've consumed all bytes — no more bits.
	// The second clause was wrong: if bytePos == len(data) and bitPos < 8,
	// we're past the end of data, not partway through a valid byte.
	return br.bytePos < len(br.data)
}

// BitWriter provides bit-level writing for H.264 bitstream construction
type BitWriter struct {
	data    []byte
	bytePos int
	bitPos  int
}

// NewBitWriter creates a new bit writer
func NewBitWriter() *BitWriter {
	return &BitWriter{data: make([]byte, 0, 1024)}
}

// WriteBit writes a single bit
func (bw *BitWriter) WriteBit(bit uint8) {
	// Ensure we have space
	for len(bw.data) <= bw.bytePos {
		bw.data = append(bw.data, 0)
	}

	if bit&1 == 1 {
		bw.data[bw.bytePos] |= (1 << (7 - bw.bitPos))
	}

	bw.bitPos++
	if bw.bitPos >= 8 {
		bw.bitPos = 0
		bw.bytePos++
	}
}

// WriteBits writes multiple bits
func (bw *BitWriter) WriteBits(value uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		bit := uint8((value >> i) & 1)
		bw.WriteBit(bit)
	}
}

// WriteUE writes an unsigned exponential Golomb coded value
func (bw *BitWriter) WriteUE(value uint32) {
	if value == 0 {
		bw.WriteBit(1) // Single bit for value 0
		return
	}

	// Calculate the number of leading zeros needed
	leadingZeros := 0
	temp := value + 1
	for temp > 1 {
		temp >>= 1
		leadingZeros++
	}

	// Write leading zeros
	for i := 0; i < leadingZeros; i++ {
		bw.WriteBit(0)
	}

	// Write the value + 1 in binary
	valuePlusOne := value + 1
	bw.WriteBits(valuePlusOne, leadingZeros+1)
}

// WriteSE writes a signed exponential Golomb coded value
func (bw *BitWriter) WriteSE(value int32) {
	if value <= 0 {
		bw.WriteUE(uint32(-value * 2))
	} else {
		bw.WriteUE(uint32(value*2 - 1))
	}
}

// GetBytes returns the written bytes (with proper padding)
func (bw *BitWriter) GetBytes() []byte {
	// Pad the last byte to complete it
	if bw.bitPos > 0 {
		// Ensure we have allocated the current byte
		for len(bw.data) <= bw.bytePos {
			bw.data = append(bw.data, 0)
		}
		// The remaining bits in the current byte are already 0
		// Just advance to the next byte position
		bw.bytePos++
	}
	return bw.data[:bw.bytePos]
}

// ReadUE reads an unsigned exponential Golomb coded value
func (br *BitReader) ReadUE() (uint32, error) {
	leadingZeros := 0

	// Count leading zeros (without storing them)
	for {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, fmt.Errorf("failed to read bit while counting zeros: %w", err)
		}
		if bit == 1 {
			break
		}
		leadingZeros++
		// Safety check: H.264 spec limits most syntax elements
		// 31 leading zeros would give us a value >= 2^31-1
		if leadingZeros > 31 {
			return 0, fmt.Errorf("invalid exponential Golomb code: too many leading zeros (%d)", leadingZeros)
		}
	}

	if leadingZeros == 0 {
		return 0, nil
	}

	// Read the value bits directly into result
	value, err := br.ReadBits(leadingZeros)
	if err != nil {
		return 0, fmt.Errorf("failed to read %d value bits: %w", leadingZeros, err)
	}

	// Safe calculation with overflow check
	// result = 2^leadingZeros - 1 + value
	base := uint32(1) << leadingZeros
	if base == 0 { // Overflow occurred
		return 0, fmt.Errorf("exponential Golomb base calculation overflow")
	}

	result := base - 1 + value

	return result, nil
}

// ReadSE reads a signed exponential Golomb coded value
func (br *BitReader) ReadSE() (int32, error) {
	ue, err := br.ReadUE()
	if err != nil {
		return 0, err
	}

	if ue == 0 {
		return 0, nil
	}

	// Safety check to prevent integer overflow
	// The maximum positive value for int32 is 2^31-1
	// The maximum ue value that can safely convert is 2^32-2
	if ue > 0xFFFFFFFE {
		return 0, fmt.Errorf("signed exponential Golomb value would overflow int32")
	}

	// Convert unsigned to signed using the standard mapping:
	// ue=1 => 1, ue=2 => -1, ue=3 => 2, ue=4 => -2, etc.
	if ue%2 == 1 {
		// Positive values: (ue + 1) / 2
		// Safe because we checked ue <= 0xFFFFFFFE
		return int32((ue + 1) / 2), nil
	} else {
		// Negative values: -(ue / 2)
		// Safe because ue/2 <= 0x7FFFFFFF
		return -int32(ue / 2), nil
	}
}

// parseSPS parses an H.264 SPS NAL unit
func (ctx *ParameterSetContext) parseSPS(data []byte) (*ParameterSet, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("SPS too short: %d bytes", len(data))
	}

	// Detect if NAL header is present and extract RBSP accordingly
	var rbspData []byte
	var err error
	nalType := data[0] & 0x1F
	if nalType == 7 || nalType == 15 { // SPS NAL unit types (7=SPS, 15=subset SPS)
		rbspData, err = ExtractRBSPFromNALUnit(data)
	} else {
		rbspData, err = ExtractRBSPFromPayload(data)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to extract RBSP from SPS: %w", err)
	}

	br := NewBitReader(rbspData)

	// Parse profile_idc
	profileIDC, err := br.ReadBits(8)
	if err != nil {
		return nil, fmt.Errorf("failed to read profile_idc: %w", err)
	}

	// Validate profile_idc - common values are 66, 77, 88, 100, 110, 122, 244
	// NOTE: The user says "the stream is correct", so we should be more permissive
	// and log warnings instead of rejecting unknown profiles
	validProfiles := map[uint32]bool{
		66:  true, // Baseline
		77:  true, // Main
		88:  true, // Extended
		100: true, // High
		110: true, // High 10
		122: true, // High 4:2:2
		244: true, // High 4:4:4
		44:  true, // CAVLC 4:4:4
		83:  true, // Scalable Baseline
		86:  true, // Scalable High
		118: true, // Multiview High
		128: true, // Stereo High
		138: true, // Multiview Depth High
	}

	// Unknown profiles are allowed - the stream might use a profile we don't recognize
	_ = validProfiles[profileIDC]

	// Read constraint_set0..5_flag + reserved_zero_2bits (8 bits total)
	constraintFlags, err := br.ReadBits(8)
	if err != nil {
		return nil, fmt.Errorf("failed to read constraint flags: %w", err)
	}

	// Parse level_idc
	levelIDC, err := br.ReadBits(8)
	if err != nil {
		return nil, fmt.Errorf("failed to read level_idc: %w", err)
	}

	// Validate level_idc - common values are 10, 11, 12, 13, 20, 21, 22, 30, 31, 32, 40, 41, 42, 50, 51, 52
	// NOTE: The user says "the stream is correct", so we should be more permissive
	validLevels := map[uint32]bool{
		9:  true, // Level 1b
		10: true, // Level 1
		11: true, // Level 1.1
		12: true, // Level 1.2
		13: true, // Level 1.3
		20: true, // Level 2
		21: true, // Level 2.1
		22: true, // Level 2.2
		30: true, // Level 3
		31: true, // Level 3.1
		32: true, // Level 3.2
		40: true, // Level 4
		41: true, // Level 4.1
		42: true, // Level 4.2
		50: true, // Level 5
		51: true, // Level 5.1
		52: true, // Level 5.2
		60: true, // Level 6
		61: true, // Level 6.1
		62: true, // Level 6.2
	}

	// Unknown levels are allowed - the stream might use a level we don't recognize
	_ = validLevels[levelIDC]

	// Parse seq_parameter_set_id
	spsID, err := br.ReadUE()
	if err != nil {
		return nil, fmt.Errorf("failed to read sps_id: %w", err)
	}

	if spsID > 31 {
		return nil, fmt.Errorf("sps_id %d out of range (0-31)", spsID)
	}

	sps := &ParameterSet{
		ID:       uint8(spsID),
		Data:     data,
		ParsedAt: ctx.lastUpdated,
		Size:     len(data),
		Valid:    true,
	}

	// Store parsed values
	profileU8 := uint8(profileIDC)
	constraintU8 := uint8(constraintFlags)
	levelU8 := uint8(levelIDC)
	sps.ProfileIDC = &profileU8
	sps.ConstraintFlags = &constraintU8
	sps.LevelIDC = &levelU8

	// Parse remaining SPS fields including resolution
	ctx.parseResolutionFromSPS(br, uint8(profileIDC), sps)

	return sps, nil
}

// parsePPS parses an H.264 PPS NAL unit
func (ctx *ParameterSetContext) parsePPS(data []byte) (*PPSContext, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("PPS too short: %d bytes", len(data))
	}

	// Detect if NAL header is present and extract RBSP accordingly
	var rbspData []byte
	var err error
	// Check if this looks like a NAL header with PPS type (8)
	nalType := data[0] & 0x1F
	if len(data) > 0 && nalType == 8 { // PPS NAL unit type
		// NAL header present, skip it
		rbspData, err = ExtractRBSPFromNALUnit(data)
	} else {
		// NAL header already stripped, process payload directly
		rbspData, err = ExtractRBSPFromPayload(data)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to extract RBSP from PPS: %w", err)
	}

	if len(rbspData) < 1 {
		return nil, fmt.Errorf("PPS RBSP payload too short: %d bytes", len(rbspData))
	}

	br := NewBitReader(rbspData)

	// Parse pic_parameter_set_id
	ppsID, err := br.ReadUE()
	if err != nil {
		maxBytes := len(rbspData)
		if maxBytes > 4 {
			maxBytes = 4
		}
		return nil, fmt.Errorf("failed to read pps_id: %w (first bytes: %x)", err, rbspData[:maxBytes])
	}

	if ppsID > 255 {
		return nil, fmt.Errorf("pps_id %d out of range (0-255)", ppsID)
	}

	// Parse seq_parameter_set_id (referenced SPS)
	spsID, err := br.ReadUE()
	if err != nil {
		endPos := br.bytePos + 4
		if endPos > len(rbspData) {
			endPos = len(rbspData)
		}
		if br.bytePos < len(rbspData) {
			return nil, fmt.Errorf("failed to read referenced sps_id: %w (pps_id=%d, remaining bytes: %x)", err, ppsID, rbspData[br.bytePos:endPos])
		} else {
			return nil, fmt.Errorf("failed to read referenced sps_id: %w (pps_id=%d, no remaining bytes)", err, ppsID)
		}
	}

	if spsID > 31 {
		return nil, fmt.Errorf("referenced sps_id %d out of range (0-31) (pps_id=%d, bit_pos=%d, byte_pos=%d)", spsID, ppsID, br.bitPos, br.bytePos)
	}

	pps := &PPSContext{
		ParameterSet: &ParameterSet{
			ID:       uint8(ppsID),
			Data:     data,
			ParsedAt: ctx.lastUpdated,
			Size:     len(data),
			Valid:    true,
		},
		ReferencedSPSID: uint8(spsID),
	}

	return pps, nil
}

// parseSliceHeader parses a slice header to extract PPS reference
func (ctx *ParameterSetContext) parseSliceHeader(data []byte, isIDR bool) (*FrameDecodingRequirements, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("slice header too short: %d bytes", len(data))
	}

	// Detect if NAL header is present and extract RBSP accordingly
	var rbspData []byte
	var err error
	nalType := uint8(0)
	if len(data) > 0 {
		nalType = data[0] & 0x1F
	}

	// Check if this looks like a NAL header (non-VCL NAL units have types 0-23, VCL NAL units 1-5)
	if nalType >= 1 && nalType <= 5 {
		// This is likely a slice NAL unit with header
		rbspData, err = ExtractRBSPFromNALUnit(data)
	} else if nalType > 5 && nalType <= 23 {
		// Non-VCL NAL unit with header
		rbspData, err = ExtractRBSPFromNALUnit(data)
	} else {
		// NAL header already stripped or invalid, process as payload
		rbspData, err = ExtractRBSPFromPayload(data)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to extract RBSP from slice header: %w", err)
	}

	br := NewBitReader(rbspData)

	// Parse first_mb_in_slice
	_, err = br.ReadUE()
	if err != nil {
		return nil, fmt.Errorf("failed to read first_mb_in_slice: %w", err)
	}

	// Parse slice_type (H.264 Table 7-6: values 0-9)
	sliceType, err := br.ReadUE()
	if err != nil {
		return nil, fmt.Errorf("failed to read slice_type: %w", err)
	}
	if sliceType > 9 {
		return nil, fmt.Errorf("invalid slice_type %d (must be 0-9)", sliceType)
	}

	// Parse pic_parameter_set_id
	ppsID, err := br.ReadUE()
	if err != nil {
		return nil, fmt.Errorf("failed to read pps_id: %w", err)
	}

	if ppsID > 255 {
		return nil, fmt.Errorf("slice references invalid pps_id %d", ppsID)
	}

	// Find the SPS that this PPS references
	ctx.mu.RLock()
	pps, hasPPS := ctx.ppsMap[uint8(ppsID)]
	ctx.mu.RUnlock()

	if !hasPPS {
		return nil, fmt.Errorf("slice references unknown pps_id %d", ppsID)
	}

	requirements := &FrameDecodingRequirements{
		RequiredPPSID: uint8(ppsID),
		RequiredSPSID: pps.ReferencedSPSID,
		SliceType:     uint8(sliceType),
		IsIDR:         isIDR,
	}

	// Parse frame_num if the referenced SPS has Log2MaxFrameNumMinus4
	ctx.mu.RLock()
	sps, hasSPS := ctx.spsMap[pps.ReferencedSPSID]
	ctx.mu.RUnlock()

	if hasSPS && sps.Log2MaxFrameNumMinus4 != nil {
		frameNumBits := int(*sps.Log2MaxFrameNumMinus4) + 4
		frameNum, err := br.ReadBits(frameNumBits)
		if err == nil {
			requirements.FrameNum = frameNum
			requirements.HasFrameNum = true
		}
	}

	return requirements, nil
}

// parseResolutionFromSPS parses resolution and other fields from SPS into the ParameterSet.
func (ctx *ParameterSetContext) parseResolutionFromSPS(br *BitReader, profileIDC uint8, sps *ParameterSet) {
	// Local resolution tracking — stored to sps on any return path
	var width, height int
	defer func() {
		if width > 0 && height > 0 {
			sps.Width = &width
			sps.Height = &height
		}
	}()

	// Track chroma_format_idc for crop calculation (default 1 = 4:2:0 for non-high profiles)
	// ChromaArrayType is used for cropping and may differ when separate_colour_plane_flag == 1
	chromaFormatIDC := uint32(1)
	chromaArrayType := uint32(1)

	// For high profiles, we need to handle chroma_format_idc and related fields
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 || profileIDC == 244 ||
		profileIDC == 44 || profileIDC == 83 || profileIDC == 86 || profileIDC == 118 ||
		profileIDC == 128 || profileIDC == 138 {

		// Read chroma_format_idc
		var err error
		chromaFormatIDC, err = br.ReadUE()
		if err != nil {
			return
		}

		// H.264 spec: chroma_format_idc shall be in the range 0 to 3
		if chromaFormatIDC > 3 {
			return
		}

		// ChromaArrayType governs cropping units and is derived from chroma_format_idc.
		// It differs from chromaFormatIDC only when separate_colour_plane_flag == 1.
		chromaArrayType = chromaFormatIDC

		if chromaFormatIDC == 3 {
			// separate_colour_plane_flag — per H.264 Section 7.4.2.1.1,
			// when this flag is 1, ChromaArrayType is set to 0
			separateColourPlaneFlag, err := br.ReadBit()
			if err != nil {
				return
			}
			if separateColourPlaneFlag == 1 {
				// Per spec: ChromaArrayType = 0 when separate_colour_plane_flag == 1
				// This affects cropping calculations — treat as monochrome for crop units
				// Note: chromaFormatIDC remains 3 for scaling matrix count (12 matrices)
				chromaArrayType = 0
			}
		}

		// Skip bit_depth fields
		_, err = br.ReadUE() // bit_depth_luma_minus8
		if err != nil {
			return
		}
		_, err = br.ReadUE() // bit_depth_chroma_minus8
		if err != nil {
			return
		}

		// Skip other fields...
		_, err = br.ReadBit() // qpprime_y_zero_transform_bypass_flag
		if err != nil {
			return
		}

		// seq_scaling_matrix_present_flag
		scalingMatrixPresent, err := br.ReadBit()
		if err != nil {
			return
		}

		if scalingMatrixPresent == 1 {
			// Skip scaling matrices per ITU-T H.264 Section 7.3.2.1.1
			// Use syntactic chromaFormatIDC (not ChromaArrayType) for matrix count
			maxMatrices := 8
			if chromaFormatIDC == 3 {
				maxMatrices = 12
			}
			for i := 0; i < maxMatrices; i++ {
				present, err := br.ReadBit()
				if err != nil {
					return
				}
				if present == 1 {
					if err := skipScalingList(br, i); err != nil {
						return
					}
				}
			}
		}
	}

	// Parse log2_max_frame_num_minus4
	log2MaxFrameNumMinus4, err := br.ReadUE()
	if err != nil {
		return
	}
	// H.264 spec: log2_max_frame_num_minus4 shall be in the range of 0 to 12
	if log2MaxFrameNumMinus4 > 12 {
		return
	}
	l2mfn := uint8(log2MaxFrameNumMinus4)
	sps.Log2MaxFrameNumMinus4 = &l2mfn

	// Parse pic_order_cnt_type
	pocType, err := br.ReadUE()
	if err != nil {
		return
	}

	// H.264 spec: pic_order_cnt_type shall be in the range of 0 to 2
	if pocType > 2 {
		// Invalid POC type, abort parsing
		return
	}

	// Handle different POC types
	if pocType == 0 {
		log2MaxPicOrderCntLsbMinus4, err := br.ReadUE()
		if err != nil {
			return
		}
		// H.264 spec: log2_max_pic_order_cnt_lsb_minus4 shall be in the range of 0 to 12
		if log2MaxPicOrderCntLsbMinus4 > 12 {
			return
		}
	} else if pocType == 1 {
		// Parse POC type 1 fields per ITU-T H.264 Section 7.4.2.1
		// delta_pic_order_always_zero_flag
		_, err := br.ReadBit()
		if err != nil {
			return
		}
		// offset_for_non_ref_pic
		_, err = br.ReadSE()
		if err != nil {
			return
		}
		// offset_for_top_to_bottom_field
		_, err = br.ReadSE()
		if err != nil {
			return
		}
		// num_ref_frames_in_pic_order_cnt_cycle
		numRefFrames, err := br.ReadUE()
		if err != nil {
			return
		}
		// H.264 spec: num_ref_frames_in_pic_order_cnt_cycle shall be in range 0 to 255
		if numRefFrames > 255 {
			return
		}
		// Skip offset_for_ref_frame array
		for i := uint32(0); i < numRefFrames; i++ {
			_, err = br.ReadSE()
			if err != nil {
				return
			}
		}
	}

	// Parse max_num_ref_frames
	maxNumRefFrames, err := br.ReadUE()
	if err != nil {
		return
	}

	// max_num_ref_frames typically <= 16; allow higher values but they may indicate corruption
	_ = maxNumRefFrames

	// Parse gaps_in_frame_num_value_allowed_flag
	_, err = br.ReadBit()
	if err != nil {
		return
	}

	// Parse pic_width_in_mbs_minus1
	widthInMBs, err := br.ReadUE()
	if err != nil {
		return
	}

	// Validate width is reasonable (max 512 MBs = 8192 pixels)
	if widthInMBs > 511 {
		return
	}

	// Parse pic_height_in_map_units_minus1
	heightInMapUnits, err := br.ReadUE()
	if err != nil {
		return
	}

	// Validate height is reasonable (max 512 map units = 8192 pixels)
	if heightInMapUnits > 511 {
		return
	}

	// Calculate actual resolution
	width = int(widthInMBs+1) * 16
	height = int(heightInMapUnits+1) * 16

	// Final sanity check
	if width < 16 || height < 16 || width > 8192 || height > 8192 {
		return
	}

	// Parse frame_mbs_only_flag
	frameMBSOnlyFlagBit, err := br.ReadBit()
	if err != nil {
		return
	}
	frameMBSOnlyFlag := uint32(frameMBSOnlyFlagBit)

	if frameMBSOnlyFlag == 0 {
		// Interlaced: pic_height_in_map_units represents field height, not frame height.
		// Per ITU-T H.264 Section 7.4.2.1.1:
		//   FrameHeightInMbs = (2 - frame_mbs_only_flag) * PicHeightInMapUnits
		// So for interlaced (frame_mbs_only_flag == 0), frame height = map_units * 16 * 2.
		height *= 2
		// mb_adaptive_frame_field_flag
		_, err = br.ReadBit()
		if err != nil {
			return
		}
	}

	// Parse direct_8x8_inference_flag
	_, err = br.ReadBit()
	if err != nil {
		return
	}

	// Parse frame_cropping_flag
	frameCroppingFlag, err := br.ReadBit()
	if err != nil {
		return
	}

	if frameCroppingFlag == 1 {
		// Parse crop values
		cropLeft, err := br.ReadUE()
		if err != nil {
			return
		}
		cropRight, err := br.ReadUE()
		if err != nil {
			return
		}
		cropTop, err := br.ReadUE()
		if err != nil {
			return
		}
		cropBottom, err := br.ReadUE()
		if err != nil {
			return
		}

		// Apply crop values per ITU-T H.264 Table 6-1 and Section 7.4.2.1.1
		// SubWidthC and SubHeightC depend on ChromaArrayType (not raw chroma_format_idc)
		var subWidthC, subHeightC uint32
		switch chromaArrayType {
		case 0: // Monochrome (or separate colour planes)
			subWidthC, subHeightC = 1, 1
		case 1: // 4:2:0
			subWidthC, subHeightC = 2, 2
		case 2: // 4:2:2
			subWidthC, subHeightC = 2, 1
		case 3: // 4:4:4
			subWidthC, subHeightC = 1, 1
		default:
			subWidthC, subHeightC = 2, 2 // Safe default
		}

		// CropUnitX = SubWidthC
		// CropUnitY = SubHeightC * (2 - frame_mbs_only_flag) for interlaced
		cropUnitX := subWidthC
		cropUnitY := subHeightC * (2 - frameMBSOnlyFlag)

		// Validate crop values
		if cropLeft*cropUnitX >= uint32(width) || cropRight*cropUnitX >= uint32(width) ||
			cropTop*cropUnitY >= uint32(height) || cropBottom*cropUnitY >= uint32(height) {
			return
		}

		// Calculate actual display dimensions
		width = width - int(cropLeft*cropUnitX) - int(cropRight*cropUnitX)
		height = height - int(cropTop*cropUnitY) - int(cropBottom*cropUnitY)
	}

	return
}

// skipScalingList skips a scaling list in the SPS per ITU-T H.264 Section 7.3.2.1.1
func skipScalingList(br *BitReader, index int) error {
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
