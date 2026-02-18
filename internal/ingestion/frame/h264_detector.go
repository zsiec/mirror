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

	// Non-VCL NAL after VCL NAL signals the end of the current access unit
	// and the start of the next one (per H.264 Section 7.4.1.2.3)
	if d.frameStarted && d.isVCLNAL(d.lastNALType) &&
		(nalType == H264NALTypeSEI || nalType == H264NALTypeAUD ||
			nalType == H264NALTypeSPS || nalType == H264NALTypePPS) {
		isEnd = true
		// SEI/AUD/SPS/PPS after VCL starts the next access unit
		isStart = true
		d.frameStarted = true
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

	// RFC 6184 Section 5.8: R bit must be 0
	rBit := (fuHeader & 0x20) != 0
	if rBit {
		return false, false
	}

	if startBit {
		// First fragment of NAL — apply same frame start logic as single NALs
		if nalType == H264NALTypeIDR || nalType == H264NALTypeSPS ||
			nalType == H264NALTypePPS || nalType == H264NALTypeAUD {
			isStart = true
			d.frameStarted = true
		}
		// VCL NAL after non-VCL NAL starts new frame
		if d.isVCLNAL(nalType) && !d.isVCLNAL(d.lastNALType) && d.lastNALType != 0 {
			isStart = true
			d.frameStarted = true
		}
		// Non-VCL NAL after VCL NAL ends current frame and starts next
		if d.frameStarted && d.isVCLNAL(d.lastNALType) &&
			(nalType == H264NALTypeSEI || nalType == H264NALTypeAUD ||
				nalType == H264NALTypeSPS || nalType == H264NALTypePPS) {
			isEnd = true
			isStart = true
			d.frameStarted = true
		}
	}

	if endBit {
		// Last fragment — update lastNALType so the next packet's
		// boundary detection sees the correct previous NAL type
		d.lastNALType = nalType
		return isStart, isEnd
	}

	// For middle fragments, don't update lastNALType
	if startBit {
		d.lastNALType = nalType
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
	hasValidIDRSlice := false
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
			// For IDR NAL units, verify the slice type is actually I-slice
			if len(nal.Data) > 1 {
				parsedSliceType := d.parseSliceTypeFromNAL(nal.Data)
				// IDR frames must have I-slice (type 2 or 7)
				if parsedSliceType == 2 || parsedSliceType == 7 {
					hasValidIDRSlice = true
				}
				// Keep track of slice type for fallback
				if sliceType == -1 {
					sliceType = parsedSliceType
				}
			} else {
				// IDR with no/corrupted slice data - assume it should have been I-slice
				if sliceType == -1 {
					sliceType = 2 // Default to I-slice for corrupted IDR
				}
			}
		case H264NALTypeSPS:
			hasSPS = true
		case H264NALTypePPS:
			hasPPS = true
		case H264NALTypeSlice, H264NALTypeSliceDPA, H264NALTypeSliceDPB, H264NALTypeSliceDPC:
			// Parse slice header to get slice type for non-IDR slices
			if sliceType == -1 && len(nal.Data) > 1 {
				sliceType = d.parseSliceTypeFromNAL(nal.Data)
			}
		default:
			// For any other slice-like NAL units, try to parse slice type
			if d.isVCLNAL(nalType) && sliceType == -1 && len(nal.Data) > 1 {
				sliceType = d.parseSliceTypeFromNAL(nal.Data)
			}
		}
	}

	// IDR takes priority over parameter sets per H.264 spec —
	// an access unit containing IDR + SPS + PPS is an IDR frame, not an SPS frame.
	if hasValidIDRSlice {
		return types.FrameTypeIDR
	}

	// If we have an IDR NAL but couldn't parse the slice type (corrupted/too short),
	// fall back to I-frame. Otherwise let the actual parsed slice type determine
	// the frame type — even if the NAL says IDR, the slice data is authoritative.
	if hasIDR && sliceType < 0 {
		return types.FrameTypeI
	}

	// Only classify as pure SPS/PPS if no VCL NAL units are present
	// (i.e., no slice type was detected from any NAL unit)
	if sliceType < 0 {
		if hasSPS {
			return types.FrameTypeSPS
		}
		if hasPPS {
			return types.FrameTypePPS
		}
	}

	// Determine frame type from slice type
	switch sliceType {
	case 0, 5: // P slice types
		return types.FrameTypeP
	case 1, 6: // B slice types
		return types.FrameTypeB
	case 2, 7: // I slice types
		return types.FrameTypeI
	case 3, 8: // SP slice types
		return types.FrameTypeP // Treat SP as P
	case 4, 9: // SI slice types
		return types.FrameTypeI // Treat SI as I
	}

	// If we couldn't determine the slice type and have no other info,
	// we can't accurately classify the frame
	return types.FrameTypeP // Most conservative assumption
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

	// Check with start codes — only IDR NAL units are keyframes.
	// SPS/PPS are parameter sets, not keyframes.
	nalUnits, err := d.findNALUnits(data)
	if err != nil {
		// Error finding NAL units, data might be corrupted
		return false
	}
	for _, nalUnit := range nalUnits {
		if len(nalUnit) > 0 {
			nalType := nalUnit[0] & 0x1F
			if nalType == H264NALTypeIDR {
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

// getNALTypeName returns a human-readable name for the NAL type
func (d *H264Detector) getNALTypeName(nalType uint8) string {
	switch nalType {
	case H264NALTypeSlice:
		return "Slice"
	case H264NALTypeSliceDPA:
		return "SliceDPA"
	case H264NALTypeSliceDPB:
		return "SliceDPB"
	case H264NALTypeSliceDPC:
		return "SliceDPC"
	case H264NALTypeIDR:
		return "IDR"
	case H264NALTypeSEI:
		return "SEI"
	case H264NALTypeSPS:
		return "SPS"
	case H264NALTypePPS:
		return "PPS"
	case H264NALTypeAUD:
		return "AUD"
	case H264NALTypeEndSeq:
		return "EndSeq"
	case H264NALTypeEndStr:
		return "EndStr"
	case H264NALTypeFiller:
		return "Filler"
	case H264NALTypeSPSExt:
		return "SPSExt"
	case H264NALTypePrefix:
		return "Prefix"
	case H264NALTypeSubSPS:
		return "SubSPS"
	case H264NALTypeFUA:
		return "FU-A"
	case H264NALTypeFUB:
		return "FU-B"
	default:
		return fmt.Sprintf("Unknown(%d)", nalType)
	}
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
	if len(data) < 3 {
		return nalUnits, nil
	}

	// Limit number of NAL units to prevent DoS
	maxNALUnits := security.MaxNALUnitsPerFrame

	i := 0
	dataLen := len(data)

	for i < dataLen && len(nalUnits) < maxNALUnits {
		// Look for start code - ensure we have enough bytes to check
		// Need at least 3 bytes remaining for 3-byte start code
		if i+3 > dataLen {
			break
		}

		if data[i] == 0 && data[i+1] == 0 {
			startCodeLen := 0

			// Check for 3-byte start code (0x000001)
			if data[i+2] == 1 {
				startCodeLen = 3
			} else if i+4 <= dataLen && data[i+2] == 0 && data[i+3] == 1 {
				// Check for 4-byte start code (0x00000001)
				// Note: i+4 <= dataLen because we need to check data[i+3]
				startCodeLen = 4
			}

			if startCodeLen > 0 {
				// Found start code, find next one
				nalStart := i + startCodeLen

				// Ensure nalStart is within bounds
				if nalStart >= dataLen {
					break
				}

				// Initialize nalEnd to the end of data
				nalEnd := dataLen

				// Search for next start code with proper bounds checking
				for j := nalStart; j < dataLen; j++ {
					// Need at least 3 bytes remaining to check for start code
					if j+3 > dataLen {
						break
					}

					if data[j] == 0 && data[j+1] == 0 {
						// Check for 3-byte start code
						if data[j+2] == 1 {
							nalEnd = j
							break
						}
						// Check for 4-byte start code if we have enough bytes
						if j+4 <= dataLen && data[j+2] == 0 && data[j+3] == 1 {
							nalEnd = j
							break
						}
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

				// Move to the end of this NAL unit
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

// parseSliceTypeFromNAL extracts slice type from a complete NAL unit including header
func (d *H264Detector) parseSliceTypeFromNAL(nalData []byte) int {
	if len(nalData) < 2 {
		return -1
	}

	// Skip NAL header and parse the payload
	payload := nalData[1:]
	if len(payload) == 0 {
		return -1
	}

	// For slice headers, we need to handle emulation prevention bytes
	// but NOT remove trailing bytes like RBSP extraction does
	rbsp, err := types.RemoveEmulationPreventionBytes(payload)
	if err != nil || len(rbsp) == 0 {
		// If emulation prevention removal fails, use payload as-is
		rbsp = payload
	}

	// Create bit reader for parsing
	br := types.NewBitReader(rbsp)

	// Read first_mb_in_slice (ue(v))
	_, err = br.ReadUE()
	if err != nil {
		return -1
	}

	// Read slice_type (ue(v))
	sliceType, err := br.ReadUE()
	if err != nil {
		return -1
	}

	// H.264 slice types (Table 7-6):
	// 0, 5 = P slice
	// 1, 6 = B slice
	// 2, 7 = I slice
	// 3, 8 = SP slice
	// 4, 9 = SI slice
	// Values >= 10 are invalid
	if sliceType > 9 {
		return -1
	}

	return int(sliceType)
}
