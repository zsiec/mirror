package frame

import (
	"fmt"

	"github.com/zsiec/mirror/internal/ingestion/security"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// H.264 NAL unit types
const (
	H264NALTypeSlice    = 1  // Non-IDR slice
	H264NALTypeSliceDPA = 2  // Slice data partition A
	H264NALTypeSliceDPB = 3  // Slice data partition B
	H264NALTypeSliceDPC = 4  // Slice data partition C
	H264NALTypeIDR      = 5  // IDR slice
	H264NALTypeSEI      = 6  // Supplemental enhancement information
	H264NALTypeSPS      = 7  // Sequence parameter set
	H264NALTypePPS      = 8  // Picture parameter set
	H264NALTypeAUD      = 9  // Access unit delimiter
	H264NALTypeEndSeq   = 10 // End of sequence
	H264NALTypeEndStr   = 11 // End of stream
	H264NALTypeFiller   = 12 // Filler data
	H264NALTypeSPSExt   = 13 // SPS extension
	H264NALTypePrefix   = 14 // Prefix NAL unit
	H264NALTypeSubSPS   = 15 // Subset SPS

	// FU indicator for fragmentation units
	H264NALTypeFUA = 28 // FU-A fragmentation
	H264NALTypeFUB = 29 // FU-B fragmentation
)

// H264Detector detects H.264 frame boundaries and types
type H264Detector struct {
	lastNALType  uint8
	inAccessUnit bool
	nalBuffer    []byte
	frameStarted bool
}

// NewH264Detector creates a new H.264 frame detector
func NewH264Detector() *H264Detector {
	return &H264Detector{}
}

// DetectBoundaries detects frame boundaries in H.264 stream
func (d *H264Detector) DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	if len(pkt.Data) == 0 {
		return false, false
	}

	// Check if this is RTP (simple NAL) or MPEG-TS (with start codes)
	if d.hasStartCode(pkt.Data) {
		return d.detectBoundariesWithStartCode(pkt)
	}

	// RTP mode - NAL unit is in payload
	nalType := pkt.Data[0] & 0x1F

	// Handle fragmentation units
	if nalType == H264NALTypeFUA {
		return d.handleFUA(pkt)
	}

	// Access Unit Delimiter always starts new frame
	if nalType == H264NALTypeAUD {
		isStart = true
		d.frameStarted = true
	}

	// SPS/PPS/IDR start new frames
	if nalType == H264NALTypeSPS || nalType == H264NALTypePPS || nalType == H264NALTypeIDR {
		isStart = true
		d.frameStarted = true
	}

	// Slice after non-VCL NAL starts new frame
	if d.isVCLNAL(nalType) && !d.isVCLNAL(d.lastNALType) && d.lastNALType != 0 {
		isStart = true
		d.frameStarted = true
	}

	// SEI or AUD after VCL NAL ends frame
	if d.frameStarted && d.isVCLNAL(d.lastNALType) &&
		(nalType == H264NALTypeSEI || nalType == H264NALTypeAUD ||
			nalType == H264NALTypeSPS || nalType == H264NALTypePPS) {
		isEnd = true
		d.frameStarted = false
	}

	d.lastNALType = nalType

	return isStart, isEnd
}

// handleFUA handles fragmentation unit type A
func (d *H264Detector) handleFUA(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	if len(pkt.Data) < 2 {
		return false, false
	}

	fuHeader := pkt.Data[1]
	startBit := (fuHeader & 0x80) != 0
	endBit := (fuHeader & 0x40) != 0
	nalType := fuHeader & 0x1F

	if startBit {
		// First fragment of NAL
		if nalType == H264NALTypeIDR || nalType == H264NALTypeSPS ||
			nalType == H264NALTypePPS || nalType == H264NALTypeAUD {
			isStart = true
			d.frameStarted = true
		}
	}

	if endBit && d.frameStarted {
		// Last fragment
		if d.isVCLNAL(nalType) {
			// Frame might end after this NAL
			// Need to check next packet to be sure
		}
	}

	return isStart, isEnd
}

// detectBoundariesWithStartCode handles streams with start codes (MPEG-TS)
func (d *H264Detector) detectBoundariesWithStartCode(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	// Scan for start codes and NAL units
	nalUnits, err := d.findNALUnits(pkt.Data)
	if err != nil {
		// Log error but continue processing what we have
		// In production, this should be logged properly
		return false, false
	}

	for _, nalUnit := range nalUnits {
		if len(nalUnit) == 0 {
			continue
		}

		nalType := nalUnit[0] & 0x1F

		// Check for frame start
		if nalType == H264NALTypeAUD || nalType == H264NALTypeIDR ||
			nalType == H264NALTypeSPS || nalType == H264NALTypePPS {
			isStart = true
			d.frameStarted = true
		}

		// Check for frame end - more robust detection
		if d.frameStarted {
			// Frame ends when we see:
			// 1. Access Unit Delimiter (AUD)
			// 2. Start of next frame (SPS, PPS, IDR, or new slice)
			if nalType == H264NALTypeAUD {
				isEnd = true
				d.frameStarted = false
			} else if nalType == H264NALTypeSPS || nalType == H264NALTypePPS ||
				nalType == H264NALTypeIDR || nalType == H264NALTypeSlice {
				// If we already detected a frame start and see another frame start NAL,
				// the previous frame has ended
				if isStart {
					isEnd = true
					// Don't reset frameStarted here as we're starting a new frame
				}
			}
		}

		// For MPEG-TS, often each PES packet contains a complete frame
		// If we detect a frame start and have VCL NALs, assume frame end too
		if isStart {
			hasVCLNAL := false
			for _, nalUnit := range nalUnits {
				if len(nalUnit) > 0 {
					nt := nalUnit[0] & 0x1F
					if d.isVCLNAL(nt) {
						hasVCLNAL = true
						break
					}
				}
			}
			if hasVCLNAL {
				isEnd = true
			}
		}
	}

	return isStart, isEnd
}

// GetFrameType determines frame type from NAL units
func (d *H264Detector) GetFrameType(nalUnits []types.NALUnit) types.FrameType {
	hasIDR := false
	hasSPS := false
	hasPPS := false
	sliceType := -1

	for _, nal := range nalUnits {
		if len(nal.Data) == 0 {
			continue
		}

		nalType := nal.Data[0] & 0x1F

		switch nalType {
		case H264NALTypeIDR:
			hasIDR = true
		case H264NALTypeSPS:
			hasSPS = true
		case H264NALTypePPS:
			hasPPS = true
		case H264NALTypeSlice:
			// Parse slice header to get slice type
			if sliceType == -1 && len(nal.Data) > 1 {
				sliceType = d.parseSliceType(nal.Data[1:])
			}
		}
	}

	if hasIDR {
		return types.FrameTypeIDR
	}
	if hasSPS {
		return types.FrameTypeSPS
	}
	if hasPPS {
		return types.FrameTypePPS
	}

	// Determine P/B frame from slice type
	switch sliceType {
	case 0, 5: // P slice types
		return types.FrameTypeP
	case 1, 6: // B slice types
		return types.FrameTypeB
	case 2, 7: // I slice types
		return types.FrameTypeI
	default:
		return types.FrameTypeP // Default
	}
}

// IsKeyframe checks if data contains a keyframe
func (d *H264Detector) IsKeyframe(data []byte) bool {
	// Quick check for IDR NAL type
	if len(data) > 0 {
		nalType := data[0] & 0x1F
		if nalType == H264NALTypeIDR {
			return true
		}
	}

	// Check with start codes
	nalUnits, err := d.findNALUnits(data)
	if err != nil {
		// Error finding NAL units, data might be corrupted
		return false
	}
	for _, nalUnit := range nalUnits {
		if len(nalUnit) > 0 {
			nalType := nalUnit[0] & 0x1F
			if nalType == H264NALTypeIDR || nalType == H264NALTypeSPS || nalType == H264NALTypePPS {
				return true
			}
		}
	}

	return false
}

// GetCodec returns the codec type
func (d *H264Detector) GetCodec() types.CodecType {
	return types.CodecH264
}

// isVCLNAL checks if NAL type is VCL (Video Coding Layer)
func (d *H264Detector) isVCLNAL(nalType uint8) bool {
	return nalType >= 1 && nalType <= 5
}

// hasStartCode checks if data begins with start code
func (d *H264Detector) hasStartCode(data []byte) bool {
	if len(data) >= 3 {
		return data[0] == 0 && data[1] == 0 && (data[2] == 1 || (len(data) > 3 && data[2] == 0 && data[3] == 1))
	}
	return false
}

// findNALUnits finds NAL units in data with start codes
// Returns found NAL units and any error encountered
func (d *H264Detector) findNALUnits(data []byte) ([][]byte, error) {
	nalUnits := make([][]byte, 0)

	// Check minimum size for start code search
	if len(data) < 4 {
		return nalUnits, nil
	}

	// Limit number of NAL units to prevent DoS
	maxNALUnits := security.MaxNALUnitsPerFrame

	i := 0
	for i < len(data)-3 && len(nalUnits) < maxNALUnits {
		// Look for start code
		if data[i] == 0 && data[i+1] == 0 {
			startCodeLen := 0

			// Check bounds for 3-byte start code
			if i+2 < len(data) && data[i+2] == 1 {
				startCodeLen = 3
			} else if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
				// Check bounds for 4-byte start code
				startCodeLen = 4
			}

			if startCodeLen > 0 {
				// Found start code, find next one
				nalStart := i + startCodeLen

				// Ensure nalStart is within bounds
				if nalStart >= len(data) {
					break
				}

				nalEnd := len(data)

				// Search for next start code with bounds checking
				for j := nalStart; j <= len(data)-3; j++ {
					// Check if we can safely read 3 bytes
					if j+2 < len(data) && data[j] == 0 && data[j+1] == 0 && data[j+2] == 1 {
						nalEnd = j
						break
					}
					// Check if we can safely read 4 bytes
					if j+3 < len(data) && data[j] == 0 && data[j+1] == 0 && data[j+2] == 0 && data[j+3] == 1 {
						nalEnd = j
						break
					}
				}

				// Calculate NAL unit size
				nalSize := nalEnd - nalStart

				// Check NAL unit size limits
				if nalSize > 0 && nalSize <= security.MaxNALUnitSize {
					nalUnits = append(nalUnits, data[nalStart:nalEnd])
				} else if nalSize > security.MaxNALUnitSize {
					// Return error for oversized NAL unit
					return nalUnits, fmt.Errorf(security.ErrMsgNALUnitTooLarge, nalSize, security.MaxNALUnitSize)
				}

				i = nalEnd
				continue
			}
		}
		i++
	}

	// Check if we hit the NAL unit limit
	if len(nalUnits) >= maxNALUnits {
		return nalUnits, fmt.Errorf(security.ErrMsgTooManyNALUnits, len(nalUnits), maxNALUnits)
	}

	return nalUnits, nil
}

// parseSliceType extracts slice type from slice header
// This is simplified - real implementation needs full bitstream parsing
func (d *H264Detector) parseSliceType(data []byte) int {
	if len(data) == 0 {
		return -1
	}

	// first_mb_in_slice is ue(v), skip it
	// slice_type is ue(v)
	// This is a simplified version - proper parsing needs Exp-Golomb decoding

	// For now, use heuristic based on first byte
	firstByte := data[0]
	if firstByte&0x80 == 0 {
		// Rough approximation
		return int((firstByte >> 5) & 0x07)
	}

	return -1
}
