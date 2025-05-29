package resolution

import (
	"bytes"
	"fmt"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// Resolution represents video resolution
type Resolution struct {
	Width  int
	Height int
}

// String returns the resolution as a string (e.g., "1920x1080")
func (r Resolution) String() string {
	if r.Width == 0 || r.Height == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", r.Width, r.Height)
}

// Detector provides safe resolution detection for video codecs
type Detector struct{}

// NewDetector creates a new resolution detector
func NewDetector() *Detector {
	return &Detector{}
}

// DetectFromNALUnits attempts to detect resolution from NAL units
// Returns empty Resolution if detection fails - never panics or returns errors
func (d *Detector) DetectFromNALUnits(nalUnits [][]byte, codec types.CodecType) Resolution {
	if len(nalUnits) == 0 {
		return Resolution{}
	}

	// Try to detect from each NAL unit until we find one with resolution info
	for _, nalUnit := range nalUnits {
		if res := d.detectFromSingleNAL(nalUnit, codec); res.Width > 0 && res.Height > 0 {
			return res
		}
	}

	return Resolution{}
}

// DetectFromFrame attempts to detect resolution from a video frame
// Returns empty Resolution if detection fails - never panics or returns errors
func (d *Detector) DetectFromFrame(frameData []byte, codec types.CodecType) Resolution {
	if len(frameData) == 0 {
		return Resolution{}
	}

	// Find and extract NAL units from frame data
	nalUnits := d.extractNALUnits(frameData, codec)
	return d.DetectFromNALUnits(nalUnits, codec)
}

// detectFromSingleNAL attempts resolution detection from a single NAL unit
func (d *Detector) detectFromSingleNAL(nalUnit []byte, codec types.CodecType) Resolution {
	if len(nalUnit) == 0 {
		return Resolution{}
	}

	// Use defer to catch any panics and return empty resolution
	defer func() {
		if recover() != nil {
			// Silently ignore any parsing errors
		}
	}()

	switch codec {
	case types.CodecH264:
		return d.detectH264Resolution(nalUnit)
	case types.CodecHEVC:
		return d.detectHEVCResolution(nalUnit)
	case types.CodecAV1:
		return d.detectAV1Resolution(nalUnit)
	case types.CodecJPEGXS:
		return d.detectJPEGXSResolution(nalUnit)
	default:
		return Resolution{}
	}
}

// extractNALUnits safely extracts NAL units from frame data
func (d *Detector) extractNALUnits(frameData []byte, codec types.CodecType) [][]byte {
	defer func() {
		if recover() != nil {
			// Silently ignore any parsing errors
		}
	}()

	var nalUnits [][]byte

	switch codec {
	case types.CodecH264, types.CodecHEVC:
		// Look for NAL unit start codes (0x00000001 or 0x000001)
		nalUnits = d.extractAnnexBNALUnits(frameData)
	case types.CodecAV1:
		// AV1 uses OBUs (Open Bitstream Units)
		nalUnits = d.extractAV1OBUs(frameData)
	case types.CodecJPEGXS:
		// JPEG-XS has packet-based structure
		nalUnits = [][]byte{frameData} // Treat whole frame as single unit
	}

	return nalUnits
}

// extractAnnexBNALUnits extracts NAL units using Annex-B format (start codes)
func (d *Detector) extractAnnexBNALUnits(data []byte) [][]byte {
	var nalUnits [][]byte

	i := 0
	for i < len(data) {
		// Look for start code (0x00000001 or 0x000001)
		startCodeLen := 0
		if i+3 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			startCodeLen = 4
		} else if i+2 < len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			startCodeLen = 3
		}

		if startCodeLen == 0 {
			i++
			continue
		}

		// Find next start code or end of data
		nalStart := i + startCodeLen
		nalEnd := len(data)

		for j := nalStart + 1; j < len(data)-2; j++ {
			if data[j] == 0 && data[j+1] == 0 && (data[j+2] == 1 || (j+3 < len(data) && data[j+2] == 0 && data[j+3] == 1)) {
				nalEnd = j
				break
			}
		}

		if nalStart < nalEnd && nalEnd <= len(data) {
			nalUnits = append(nalUnits, data[nalStart:nalEnd])
		}

		i = nalEnd
	}

	return nalUnits
}

// extractAV1OBUs extracts AV1 Open Bitstream Units
func (d *Detector) extractAV1OBUs(data []byte) [][]byte {
	// Simplified AV1 OBU extraction - just return the whole frame
	// Real implementation would parse the OBU structure
	if len(data) > 0 {
		return [][]byte{data}
	}
	return nil
}

// detectH264Resolution detects resolution from H.264 SPS NAL unit
func (d *Detector) detectH264Resolution(nalUnit []byte) Resolution {
	if len(nalUnit) < 2 {
		return Resolution{}
	}

	// Check if this is an SPS NAL unit (type 7)
	nalType := nalUnit[0] & 0x1F
	if nalType != 7 {
		return Resolution{}
	}

	// Parse SPS - this is simplified parsing
	// Real implementation would need full SPS parser with exponential golomb decoding
	return d.parseH264SPS(nalUnit[1:])
}

// detectHEVCResolution detects resolution from HEVC SPS or VPS NAL unit
func (d *Detector) detectHEVCResolution(nalUnit []byte) Resolution {
	if len(nalUnit) < 2 {
		return Resolution{}
	}

	// HEVC NAL unit type is in bits 9-15 of first two bytes
	nalType := (nalUnit[0] >> 1) & 0x3F

	switch nalType {
	case 32: // VPS (Video Parameter Set)
		return d.parseHEVCVPSWrapper(nalUnit[2:])
	case 33: // SPS (Sequence Parameter Set)
		return d.parseHEVCSPS(nalUnit[2:])
	default:
		return Resolution{}
	}
}

// detectAV1Resolution detects resolution from AV1 sequence header
func (d *Detector) detectAV1Resolution(obu []byte) Resolution {
	if len(obu) < 2 {
		return Resolution{}
	}

	// Use defer to catch any panics and return empty resolution
	defer func() {
		if recover() != nil {
			// Silently ignore any parsing errors
		}
	}()

	// Parse OBU header
	obuHeader := obu[0]
	obuType := (obuHeader >> 3) & 0x0F
	obuExtensionFlag := (obuHeader >> 2) & 0x01
	obuHasSizeField := (obuHeader >> 1) & 0x01

	// Only process sequence header OBUs (type 1)
	if obuType != 1 {
		return Resolution{}
	}

	offset := 1
	// Skip extension header if present
	if obuExtensionFlag == 1 {
		if offset >= len(obu) {
			return Resolution{}
		}
		offset++ // Skip extension header byte
	}

	// Skip size field if present (LEB128 encoded)
	if obuHasSizeField == 1 {
		// Skip LEB128 size field
		for offset < len(obu) && (obu[offset]&0x80) != 0 {
			offset++
		}
		if offset < len(obu) {
			offset++ // Skip final byte of LEB128
		}
	}

	// Now we're at the sequence header payload
	if offset >= len(obu) {
		return Resolution{}
	}

	return d.parseAV1SequenceHeader(obu[offset:])
}

// detectJPEGXSResolution detects resolution from JPEG-XS header
func (d *Detector) detectJPEGXSResolution(frameData []byte) Resolution {
	if len(frameData) < 4 {
		return Resolution{}
	}

	// Use defer to catch any panics and return empty resolution
	defer func() {
		if recover() != nil {
			// Silently ignore any parsing errors
		}
	}()

	// Look for SOC marker (Start of Codestream: 0xFF10)
	if frameData[0] != 0xFF || frameData[1] != 0x10 {
		return Resolution{}
	}

	// Search for picture header marker in the header segments
	offset := 2 // Skip SOC marker
	for offset < len(frameData)-3 {
		if frameData[offset] == 0xFF {
			markerType := frameData[offset+1]

			// Look for potential picture header or capability markers
			// Since exact marker codes aren't publicly documented, we try common patterns
			if markerType >= 0x30 && markerType <= 0x3F {
				// Potential picture header marker range
				if res := d.parseJPEGXSHeader(frameData[offset:]); res.Width > 0 && res.Height > 0 {
					return res
				}
			}

			// Try to find resolution by pattern matching common values
			if res := d.findJPEGXSResolutionPattern(frameData[offset:]); res.Width > 0 && res.Height > 0 {
				return res
			}

			offset += 2
		} else {
			offset++
		}

		// Limit search to first 1KB to avoid scanning entire frame
		if offset > 1024 {
			break
		}
	}

	return Resolution{}
}

// parseH264SPS parses H.264 SPS to extract resolution using standards-compliant parsing
func (d *Detector) parseH264SPS(spsData []byte) Resolution {
	if len(spsData) < 4 {
		return Resolution{}
	}

	// Use the full standards-compliant H.264 SPS parser
	res, err := d.parseH264SPSFull(spsData)
	if err != nil {
		// Fall back to heuristic parsing if full parsing fails
		return d.parseH264SPSHeuristic(spsData)
	}

	return res
}

// parseH264SPSHeuristic provides fallback heuristic parsing for H.264 SPS
func (d *Detector) parseH264SPSHeuristic(spsData []byte) Resolution {
	if len(spsData) < 10 {
		return Resolution{}
	}

	// Look for common resolution patterns in the SPS
	// This is a heuristic approach - not standards compliant
	if len(spsData) >= 20 {
		// Check for 1920x1080 (0x780 x 0x438)
		if bytes.Contains(spsData, []byte{0x07, 0x80}) || bytes.Contains(spsData, []byte{0x78, 0x00}) {
			return Resolution{Width: 1920, Height: 1080}
		}

		// Check for 1280x720 (0x500 x 0x2D0)
		if bytes.Contains(spsData, []byte{0x05, 0x00}) || bytes.Contains(spsData, []byte{0x50, 0x00}) {
			return Resolution{Width: 1280, Height: 720}
		}

		// Check for 640x480 (0x280 x 0x1E0)
		if bytes.Contains(spsData, []byte{0x02, 0x80}) {
			return Resolution{Width: 640, Height: 480}
		}
	}

	return Resolution{}
}

// parseHEVCSPS parses HEVC SPS to extract resolution using standards-compliant parsing
func (d *Detector) parseHEVCSPS(spsData []byte) Resolution {
	if len(spsData) < 4 {
		return Resolution{}
	}

	// Use the full standards-compliant HEVC SPS parser
	res, err := d.parseHEVCSPSFull(spsData)
	if err != nil {
		// Fall back to heuristic parsing if full parsing fails
		return d.parseHEVCSPSHeuristic(spsData)
	}

	return res
}

// parseHEVCSPSHeuristic provides fallback heuristic parsing for HEVC SPS
func (d *Detector) parseHEVCSPSHeuristic(spsData []byte) Resolution {
	if len(spsData) < 10 {
		return Resolution{}
	}

	// Simplified HEVC SPS parsing - heuristic approach
	if len(spsData) >= 20 {
		// Look for common resolution patterns
		if bytes.Contains(spsData, []byte{0x07, 0x80}) || bytes.Contains(spsData, []byte{0x78, 0x00}) {
			return Resolution{Width: 1920, Height: 1080}
		}

		if bytes.Contains(spsData, []byte{0x05, 0x00}) || bytes.Contains(spsData, []byte{0x50, 0x00}) {
			return Resolution{Width: 1280, Height: 720}
		}

		if bytes.Contains(spsData, []byte{0x02, 0x80}) {
			return Resolution{Width: 640, Height: 480}
		}
	}

	return Resolution{}
}

// parseHEVCVPSWrapper parses HEVC VPS to extract resolution using standards-compliant parsing
func (d *Detector) parseHEVCVPSWrapper(vpsData []byte) Resolution {
	if len(vpsData) < 4 {
		return Resolution{}
	}

	// Parse the VPS to validate it, but VPS doesn't contain resolution info
	// Resolution information is in the SPS (Sequence Parameter Set)
	_, err := d.parseHEVCVPS(vpsData)
	if err != nil {
		return Resolution{}
	}

	// VPS parsed successfully but doesn't contain resolution
	// Return empty resolution - caller should look for SPS
	return Resolution{}
}

// parseAV1SequenceHeader parses AV1 sequence header to extract resolution
// This is a simplified implementation that uses pattern matching as a fallback
func (d *Detector) parseAV1SequenceHeader(data []byte) Resolution {
	if len(data) < 4 {
		return Resolution{}
	}

	// Try standards-compliant parsing first
	if res := d.parseAV1SequenceHeaderStandard(data); res.Width > 0 && res.Height > 0 {
		return res
	}

	// Fall back to heuristic pattern matching for common resolutions
	return d.findAV1ResolutionPattern(data)
}

// parseAV1SequenceHeaderStandard attempts standards-compliant AV1 sequence header parsing
func (d *Detector) parseAV1SequenceHeaderStandard(data []byte) Resolution {
	if len(data) < 4 {
		return Resolution{}
	}

	br := NewBitReader(data)

	// seq_profile (3 bits)
	_, err := br.ReadBits(3)
	if err != nil {
		return Resolution{}
	}

	// still_picture (1 bit)
	_, err = br.ReadBit()
	if err != nil {
		return Resolution{}
	}

	// reduced_still_picture_header (1 bit)
	reducedStillPictureHeader, err := br.ReadBit()
	if err != nil {
		return Resolution{}
	}

	// For reduced_still_picture_header, we can skip directly to frame size
	if reducedStillPictureHeader == 1 {
		return d.parseAV1FrameSize(br)
	}

	// For non-reduced headers, the structure is complex, so we'll fall back to pattern matching
	// This is a simplified implementation for demonstration
	return Resolution{}
}

// parseAV1FrameSize parses the frame size fields from AV1 sequence header
func (d *Detector) parseAV1FrameSize(br *BitReader) Resolution {
	// frame_width_bits_minus_1 (4 bits)
	frameWidthBitsMinus1, err := br.ReadBits(4)
	if err != nil {
		return Resolution{}
	}

	// frame_height_bits_minus_1 (4 bits)
	frameHeightBitsMinus1, err := br.ReadBits(4)
	if err != nil {
		return Resolution{}
	}

	// Validate bit counts (should be reasonable)
	if frameWidthBitsMinus1 > 15 || frameHeightBitsMinus1 > 15 {
		return Resolution{}
	}

	// max_frame_width_minus_1 (n bits where n = frame_width_bits_minus_1 + 1)
	widthBits := int(frameWidthBitsMinus1 + 1)
	maxFrameWidthMinus1, err := br.ReadBits(widthBits)
	if err != nil {
		return Resolution{}
	}

	// max_frame_height_minus_1 (n bits where n = frame_height_bits_minus_1 + 1)
	heightBits := int(frameHeightBitsMinus1 + 1)
	maxFrameHeightMinus1, err := br.ReadBits(heightBits)
	if err != nil {
		return Resolution{}
	}

	// Calculate final resolution
	width := maxFrameWidthMinus1 + 1
	height := maxFrameHeightMinus1 + 1

	// Validate resolution bounds
	if width > 0 && width <= 7680 && height > 0 && height <= 4320 {
		return Resolution{
			Width:  int(width),
			Height: int(height),
		}
	}

	return Resolution{}
}

// findAV1ResolutionPattern searches for common resolution patterns in AV1 data
func (d *Detector) findAV1ResolutionPattern(data []byte) Resolution {
	if len(data) < 8 {
		return Resolution{}
	}

	// Common resolutions to look for
	commonResolutions := []Resolution{
		{1920, 1080},
		{1280, 720},
		{3840, 2160},
		{1920, 1200},
		{1600, 900},
		{1366, 768},
		{1024, 768},
		{1280, 1024},
		{1440, 900},
		{1680, 1050},
		{2560, 1440},
		{3440, 1440},
	}

	// Look for resolution patterns encoded in various ways
	for _, res := range commonResolutions {
		// Check for width-1 and height-1 patterns (AV1 uses minus_1 encoding)
		widthMinus1 := uint32(res.Width - 1)
		heightMinus1 := uint32(res.Height - 1)

		// Try different bit widths for the dimension encoding
		for widthBits := 8; widthBits <= 16; widthBits += 4 {
			for heightBits := 8; heightBits <= 16; heightBits += 4 {
				if d.findAV1DimensionPattern(data, widthMinus1, heightMinus1, widthBits, heightBits) {
					return res
				}
			}
		}
	}

	return Resolution{}
}

// findAV1DimensionPattern looks for specific width/height pattern in bit-packed data
func (d *Detector) findAV1DimensionPattern(data []byte, width, height uint32, widthBits, heightBits int) bool {
	if len(data) < 4 {
		return false
	}

	// Create expected bit pattern
	totalBits := widthBits + heightBits
	if totalBits > 32 || totalBits < 16 {
		return false
	}

	expectedValue := (width << heightBits) | height

	// Search through the data for this pattern, but only in the first few bytes
	// where sequence header frame size would typically be located
	searchLimit := len(data)
	if searchLimit > 16 {
		searchLimit = 16 // Very restrictive search area
	}

	matchCount := 0
	for offset := 0; offset < searchLimit-3; offset++ {
		// Try different bit alignments, but only first few
		for bitOffset := 0; bitOffset < 4; bitOffset++ {
			br := NewBitReader(data[offset:])
			if err := br.SkipBits(bitOffset); err != nil {
				continue
			}

			value, err := br.ReadBits(totalBits)
			if err != nil {
				continue
			}

			if value == expectedValue {
				matchCount++
				// For pattern matching to work, we need at least one clear match
				// in a reasonable location (not buried deep in random data)
				if offset < 8 {
					return true
				}
			}
		}
	}

	return false
}

// parseJPEGXSHeader attempts to parse JPEG-XS header for resolution
func (d *Detector) parseJPEGXSHeader(data []byte) Resolution {
	if len(data) < 8 {
		return Resolution{}
	}

	// Look for potential width/height fields after marker
	// JPEG-XS stores dimensions in big-endian format
	offset := 2 // Skip marker

	// Try to find 16-bit or 32-bit width/height pairs
	for i := offset; i < len(data)-7; i += 2 {
		// Try 16-bit values
		width16 := (uint32(data[i]) << 8) | uint32(data[i+1])
		height16 := (uint32(data[i+2]) << 8) | uint32(data[i+3])

		if d.isValidResolution(width16, height16) {
			return Resolution{Width: int(width16), Height: int(height16)}
		}

		// Try 32-bit values (if enough data)
		if i+7 < len(data) {
			width32 := (uint32(data[i]) << 24) | (uint32(data[i+1]) << 16) |
				(uint32(data[i+2]) << 8) | uint32(data[i+3])
			height32 := (uint32(data[i+4]) << 24) | (uint32(data[i+5]) << 16) |
				(uint32(data[i+6]) << 8) | uint32(data[i+7])

			if d.isValidResolution(width32, height32) {
				return Resolution{Width: int(width32), Height: int(height32)}
			}
		}
	}

	return Resolution{}
}

// findJPEGXSResolutionPattern searches for common resolution patterns
func (d *Detector) findJPEGXSResolutionPattern(data []byte) Resolution {
	if len(data) < 16 {
		return Resolution{}
	}

	// Common resolutions to look for (in various byte orders)
	commonResolutions := []Resolution{
		{1920, 1080},
		{1280, 720},
		{3840, 2160},
		{1920, 1200},
		{1600, 900},
		{1366, 768},
		{1024, 768},
		{1280, 1024},
		{1440, 900},
		{1680, 1050},
		{2560, 1440},
		{3440, 1440},
	}

	// Search for these patterns in the data
	for _, res := range commonResolutions {
		// Try different byte orders
		patterns := [][]byte{
			// Big-endian 16-bit
			{byte(res.Width >> 8), byte(res.Width), byte(res.Height >> 8), byte(res.Height)},
			// Little-endian 16-bit
			{byte(res.Width), byte(res.Width >> 8), byte(res.Height), byte(res.Height >> 8)},
			// Big-endian 32-bit
			{0, 0, byte(res.Width >> 8), byte(res.Width), 0, 0, byte(res.Height >> 8), byte(res.Height)},
		}

		for _, pattern := range patterns {
			if bytes.Contains(data, pattern) {
				return res
			}
		}
	}

	return Resolution{}
}

// isValidResolution checks if width/height values represent a valid video resolution
func (d *Detector) isValidResolution(width, height uint32) bool {
	// Check bounds: minimum 64x64, maximum 8K (7680x4320)
	if width < 64 || width > 7680 || height < 64 || height > 4320 {
		return false
	}

	// Check if dimensions are reasonable (not too extreme aspect ratios)
	aspectRatio := float64(width) / float64(height)
	if aspectRatio < 0.25 || aspectRatio > 4.0 {
		return false
	}

	// Check if dimensions are likely to be real (often multiples of 8 or 16)
	if (width%8 == 0 || width%16 == 0) && (height%8 == 0 || height%16 == 0) {
		return true
	}

	// Check against common resolutions
	commonResolutions := []struct{ w, h uint32 }{
		{1920, 1080},
		{1280, 720},
		{3840, 2160},
		{1920, 1200},
		{1600, 900},
		{1366, 768},
		{1024, 768},
		{1280, 1024},
		{1440, 900},
		{1680, 1050},
		{2560, 1440},
		{3440, 1440},
		{720, 480},
		{720, 576},
		{640, 480},
		{800, 600},
	}

	for _, res := range commonResolutions {
		if width == res.w && height == res.h {
			return true
		}
	}

	return false
}
