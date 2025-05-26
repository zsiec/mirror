package types

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/zsiec/mirror/internal/logger"
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
	return br.bytePos < len(br.data) || (br.bytePos == len(br.data) && br.bitPos < 8)
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

// GetBytes returns the written bytes (with proper padding)
func (bw *BitWriter) GetBytes() []byte {
	// Pad the last byte to complete it
	if bw.bitPos > 0 {
		// Fill remaining bits in current byte with zeros
		for bw.bitPos < 8 {
			bw.WriteBit(0)
		}
	}
	return bw.data[:bw.bytePos]
}

// ReadUE reads an unsigned exponential Golomb coded value
func (br *BitReader) ReadUE() (uint32, error) {
	leadingZeros := 0

	// Count leading zeros
	var zeroBits []uint8
	for {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, err
		}
		zeroBits = append(zeroBits, bit)
		if bit == 1 {
			break
		}
		leadingZeros++
		if leadingZeros > 32 { // Safety check
			return 0, fmt.Errorf("invalid exponential Golomb code")
		}
	}

	if leadingZeros == 0 {
		return 0, nil
	}

	// Read the remaining bits
	var valueBits []uint8
	for i := 0; i < leadingZeros; i++ {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, err
		}
		valueBits = append(valueBits, bit)
	}

	// Calculate value manually to verify
	var value uint32
	for _, bit := range valueBits {
		value = (value << 1) | uint32(bit)
	}

	result := (1 << leadingZeros) - 1 + value

	// Removed expensive logging that was causing crashes

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

	if ue%2 == 1 {
		return int32((ue + 1) / 2), nil
	} else {
		return -int32(ue / 2), nil
	}
}

// parseSPS parses an H.264 SPS NAL unit
func (ctx *ParameterSetContext) parseSPS(data []byte) (*ParameterSet, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("SPS too short: %d bytes", len(data))
	}

	// Skip NAL header, start from SPS payload
	spsData := data[1:] // Skip the 0x67 NAL header

	br := NewBitReader(spsData)

	// Parse profile_idc
	profileIDC, err := br.ReadBits(8)
	if err != nil {
		return nil, fmt.Errorf("failed to read profile_idc: %w", err)
	}

	// Skip constraint flags (8 bits)
	_, err = br.ReadBits(8)
	if err != nil {
		return nil, fmt.Errorf("failed to read constraint flags: %w", err)
	}

	// Parse level_idc
	levelIDC, err := br.ReadBits(8)
	if err != nil {
		return nil, fmt.Errorf("failed to read level_idc: %w", err)
	}

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
	levelU8 := uint8(levelIDC)
	sps.ProfileIDC = &profileU8
	sps.LevelIDC = &levelU8

	// For basic streams, try to parse resolution (simplified)
	width, height := ctx.parseResolutionFromSPS(br, uint8(profileIDC))
	if width > 0 && height > 0 {
		sps.Width = &width
		sps.Height = &height
	}

	return sps, nil
}

// parsePPS parses an H.264 PPS NAL unit
func (ctx *ParameterSetContext) parsePPS(data []byte) (*PPSContext, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("PPS too short: %d bytes", len(data))
	}

	// **DEBUG: Validate NAL header**
	nalHeader := data[0]
	if nalHeader != 0x68 {
		return nil, fmt.Errorf("invalid PPS NAL header: expected 0x68, got 0x%02x", nalHeader)
	}

	// Skip NAL header, start from PPS payload
	ppsData := data[1:] // Skip the 0x68 NAL header

	// **DEBUG: Additional validation**
	if len(ppsData) < 1 {
		return nil, fmt.Errorf("PPS payload too short: %d bytes", len(ppsData))
	}

	br := NewBitReader(ppsData)

	// Parse pic_parameter_set_id
	ppsID, err := br.ReadUE()
	if err != nil {
		maxBytes := len(ppsData)
		if maxBytes > 4 {
			maxBytes = 4
		}
		return nil, fmt.Errorf("failed to read pps_id: %w (first bytes: %x)", err, ppsData[:maxBytes])
	}

	if ppsID > 255 {
		return nil, fmt.Errorf("pps_id %d out of range (0-255)", ppsID)
	}

	// Parse seq_parameter_set_id (referenced SPS)
	spsID, err := br.ReadUE()
	if err != nil {
		endPos := br.bytePos + 4
		if endPos > len(ppsData) {
			endPos = len(ppsData)
		}
		if br.bytePos < len(ppsData) {
			return nil, fmt.Errorf("failed to read referenced sps_id: %w (pps_id=%d, remaining bytes: %x)", err, ppsID, ppsData[br.bytePos:endPos])
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

	// **DEBUG: Enhanced slice header parsing with detailed logging**
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	logger.WithFields(map[string]interface{}{
		"stream_id":  ctx.streamID,
		"data_size":  len(data),
		"nal_header": fmt.Sprintf("0x%02x", data[0]),
		"first_16_bytes": func() string {
			maxBytes := 16
			if len(data) < maxBytes {
				maxBytes = len(data)
			}
			return fmt.Sprintf("%02x", data[:maxBytes])
		}(),
		"is_idr": isIDR,
	}).Info("🔍 SLICE HEADER DEBUG: Starting slice header parsing")

	// Skip NAL header, start from slice header
	sliceData := data[1:]

	logger.WithFields(map[string]interface{}{
		"stream_id":       ctx.streamID,
		"slice_data_size": len(sliceData),
		"slice_first_8_bytes": func() string {
			maxBytes := 8
			if len(sliceData) < maxBytes {
				maxBytes = len(sliceData)
			}
			return fmt.Sprintf("%02x", sliceData[:maxBytes])
		}(),
	}).Info("🔍 SLICE HEADER DEBUG: Slice data extracted")

	br := NewBitReader(sliceData)

	// Parse first_mb_in_slice
	logger.Info("🔍 SLICE HEADER DEBUG: About to parse first_mb_in_slice")
	firstMBInSlice, err := br.ReadUE()
	if err != nil {
		logger.WithError(err).Error("🔍 SLICE HEADER DEBUG: Failed to parse first_mb_in_slice")
		return nil, fmt.Errorf("failed to read first_mb_in_slice: %w", err)
	}
	logger.WithFields(map[string]interface{}{
		"stream_id":         ctx.streamID,
		"first_mb_in_slice": firstMBInSlice,
		"bit_pos":           br.bitPos,
		"byte_pos":          br.bytePos,
	}).Info("🔍 SLICE HEADER DEBUG: Parsed first_mb_in_slice")

	// Parse slice_type
	logger.Info("🔍 SLICE HEADER DEBUG: About to parse slice_type")
	sliceType, err := br.ReadUE()
	if err != nil {
		logger.WithError(err).Error("🔍 SLICE HEADER DEBUG: Failed to parse slice_type")
		return nil, fmt.Errorf("failed to read slice_type: %w", err)
	}
	logger.WithFields(map[string]interface{}{
		"stream_id":  ctx.streamID,
		"slice_type": sliceType,
		"bit_pos":    br.bitPos,
		"byte_pos":   br.bytePos,
	}).Info("🔍 SLICE HEADER DEBUG: Parsed slice_type")

	// Parse pic_parameter_set_id
	logger.WithFields(map[string]interface{}{
		"stream_id": ctx.streamID,
		"bit_pos":   br.bitPos,
		"byte_pos":  br.bytePos,
		"remaining_bytes": func() string {
			if br.bytePos < len(sliceData) {
				maxBytes := 8
				endPos := br.bytePos + maxBytes
				if endPos > len(sliceData) {
					endPos = len(sliceData)
				}
				return fmt.Sprintf("%02x", sliceData[br.bytePos:endPos])
			}
			return "none"
		}(),
	}).Info("🔍 SLICE HEADER DEBUG: About to parse pic_parameter_set_id")

	ppsID, err := br.ReadUE()
	if err != nil {
		logger.WithError(err).WithFields(map[string]interface{}{
			"stream_id": ctx.streamID,
			"bit_pos":   br.bitPos,
			"byte_pos":  br.bytePos,
		}).Error("🔍 SLICE HEADER DEBUG: Failed to parse pps_id")
		return nil, fmt.Errorf("failed to read pps_id: %w", err)
	}

	logger.WithFields(map[string]interface{}{
		"stream_id": ctx.streamID,
		"pps_id":    ppsID,
		"bit_pos":   br.bitPos,
		"byte_pos":  br.bytePos,
		"is_valid":  ppsID <= 255,
	}).Info("🔍 SLICE HEADER DEBUG: Parsed pic_parameter_set_id")

	if ppsID > 255 {
		logger.WithFields(map[string]interface{}{
			"stream_id":      ctx.streamID,
			"invalid_pps_id": ppsID,
			"max_valid":      255,
		}).Error("🔍 SLICE HEADER DEBUG: PPS ID out of valid range")
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

	return requirements, nil
}

// parseResolutionFromSPS attempts to parse resolution from SPS (simplified)
func (ctx *ParameterSetContext) parseResolutionFromSPS(br *BitReader, profileIDC uint8) (int, int) {
	// Full implementation would handle all profile-specific fields

	// For many profiles, we need to handle chroma_format_idc
	if profileIDC == 100 || profileIDC == 110 || profileIDC == 122 || profileIDC == 244 ||
		profileIDC == 44 || profileIDC == 83 || profileIDC == 86 || profileIDC == 118 ||
		profileIDC == 128 || profileIDC == 138 {

		// Read chroma_format_idc
		chromaFormatIDC, err := br.ReadUE()
		if err != nil {
			return 0, 0
		}

		if chromaFormatIDC == 3 {
			// separate_colour_plane_flag
			_, err := br.ReadBit()
			if err != nil {
				return 0, 0
			}
		}

		// Skip bit_depth fields
		_, err = br.ReadUE() // bit_depth_luma_minus8
		if err != nil {
			return 0, 0
		}
		_, err = br.ReadUE() // bit_depth_chroma_minus8
		if err != nil {
			return 0, 0
		}

		// Skip other fields...
		_, err = br.ReadBit() // qpprime_y_zero_transform_bypass_flag
		if err != nil {
			return 0, 0
		}

		// seq_scaling_matrix_present_flag
		scalingMatrixPresent, err := br.ReadBit()
		if err != nil {
			return 0, 0
		}

		if scalingMatrixPresent == 1 {
			// Skip scaling matrices (complex parsing)
			return 0, 0
		}
	}

	// Parse log2_max_frame_num_minus4
	_, err := br.ReadUE()
	if err != nil {
		return 0, 0
	}

	// Parse pic_order_cnt_type
	pocType, err := br.ReadUE()
	if err != nil {
		return 0, 0
	}

	// Handle different POC types
	if pocType == 0 {
		_, err = br.ReadUE() // log2_max_pic_order_cnt_lsb_minus4
		if err != nil {
			return 0, 0
		}
	} else if pocType == 1 {
		// Skip complex POC type 1 parsing
		return 0, 0
	}

	// Parse max_num_ref_frames
	_, err = br.ReadUE()
	if err != nil {
		return 0, 0
	}

	// Parse gaps_in_frame_num_value_allowed_flag
	_, err = br.ReadBit()
	if err != nil {
		return 0, 0
	}

	// Parse pic_width_in_mbs_minus1
	widthInMBs, err := br.ReadUE()
	if err != nil {
		return 0, 0
	}

	// Parse pic_height_in_map_units_minus1
	heightInMapUnits, err := br.ReadUE()
	if err != nil {
		return 0, 0
	}

	// Calculate actual resolution
	width := int(widthInMBs+1) * 16
	height := int(heightInMapUnits+1) * 16

	// Parse frame_mbs_only_flag
	frameMBSOnlyFlag, err := br.ReadBit()
	if err != nil {
		return width, height
	}

	if frameMBSOnlyFlag == 0 {
		height *= 2 // Field coding
	}

	return width, height
}
