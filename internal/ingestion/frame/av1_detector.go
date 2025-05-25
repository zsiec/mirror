package frame

import (
	"fmt"
	
	"github.com/zsiec/mirror/internal/ingestion/security"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// AV1 OBU types
const (
	AV1OBUTypeSequenceHeader        = 1
	AV1OBUTypeTemporalDelimiter     = 2
	AV1OBUTypeFrameHeader           = 3
	AV1OBUTypeTileGroup             = 4
	AV1OBUTypeMetadata              = 5
	AV1OBUTypeFrame                 = 6  // Frame header + tile group
	AV1OBUTypeRedundantFrameHeader  = 7
	AV1OBUTypeTileList              = 8
	AV1OBUTypePadding               = 15
)

// AV1 frame types
const (
	AV1FrameTypeKey        = 0
	AV1FrameTypeInter      = 1
	AV1FrameTypeIntraOnly  = 2
	AV1FrameTypeSwitch     = 3
)

// AV1Detector detects AV1 frame boundaries and types
type AV1Detector struct {
	frameStarted      bool
	lastOBUType       uint8
	currentFrameType  uint8
	seenFrameHeader   bool
}

// NewAV1Detector creates a new AV1 frame detector
func NewAV1Detector() *AV1Detector {
	return &AV1Detector{}
}

// DetectBoundaries detects frame boundaries in AV1 stream
func (d *AV1Detector) DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	if len(pkt.Data) == 0 {
		return false, false
	}
	
	// Parse OBUs in the packet
	obus, err := d.parseOBUs(pkt.Data)
	if err != nil {
		// For malformed data, we should not process it as a valid frame
		// This prevents potential security issues from malformed streams
		return false, false
	}
	
	for _, obu := range obus {
		// Temporal delimiter always starts new frame
		if obu.Type == AV1OBUTypeTemporalDelimiter {
			if d.frameStarted {
				isEnd = true
			}
			isStart = true
			d.frameStarted = true
			d.seenFrameHeader = false
		}
		
		// Sequence header starts new frame
		if obu.Type == AV1OBUTypeSequenceHeader {
			if d.frameStarted && !d.seenFrameHeader {
				isEnd = true
			}
			isStart = true
			d.frameStarted = true
		}
		
		// Frame header or complete frame
		if obu.Type == AV1OBUTypeFrameHeader || obu.Type == AV1OBUTypeFrame {
			if !d.seenFrameHeader {
				isStart = true
				d.frameStarted = true
				d.seenFrameHeader = true
			}
			
			// Parse frame type from header
			if len(obu.Data) > 0 {
				d.currentFrameType = d.parseFrameType(obu.Data)
			}
		}
		
		// Tile group after frame header
		if obu.Type == AV1OBUTypeTileGroup && d.seenFrameHeader {
			// This completes the frame
			isEnd = true
			d.frameStarted = false
			d.seenFrameHeader = false
		}
		
		// Complete frame OBU
		if obu.Type == AV1OBUTypeFrame {
			// Frame OBU contains both header and tile data
			isEnd = true
			d.frameStarted = false
			d.seenFrameHeader = false
		}
		
		d.lastOBUType = obu.Type
	}
	
	return isStart, isEnd
}

// GetFrameType determines frame type from NAL units (OBUs for AV1)
func (d *AV1Detector) GetFrameType(nalUnits []types.NALUnit) types.FrameType {
	// For AV1, we need to look at OBUs
	for _, nal := range nalUnits {
		obus, err := d.parseOBUs(nal.Data)
		if err != nil {
			continue // Skip malformed OBUs
		}
		for _, obu := range obus {
			if obu.Type == AV1OBUTypeFrameHeader || obu.Type == AV1OBUTypeFrame {
				frameType := d.parseFrameType(obu.Data)
				switch frameType {
				case AV1FrameTypeKey:
					return types.FrameTypeIDR
				case AV1FrameTypeIntraOnly:
					return types.FrameTypeI
				case AV1FrameTypeInter, AV1FrameTypeSwitch:
					// AV1 doesn't distinguish P/B in frame header
					// Would need reference frame info
					return types.FrameTypeP
				}
			}
			if obu.Type == AV1OBUTypeSequenceHeader {
				return types.FrameTypeSPS
			}
		}
	}
	
	return types.FrameTypeP // Default
}

// IsKeyframe checks if data contains a keyframe
func (d *AV1Detector) IsKeyframe(data []byte) bool {
	obus, err := d.parseOBUs(data)
	if err != nil {
		// Malformed data cannot be identified as keyframe
		return false
	}
	
	for _, obu := range obus {
		if obu.Type == AV1OBUTypeSequenceHeader {
			return true
		}
		
		if obu.Type == AV1OBUTypeFrameHeader || obu.Type == AV1OBUTypeFrame {
			frameType := d.parseFrameType(obu.Data)
			if frameType == AV1FrameTypeKey {
				return true
			}
		}
	}
	
	return false
}

// GetCodec returns the codec type
func (d *AV1Detector) GetCodec() types.CodecType {
	return types.CodecAV1
}

// OBU represents an AV1 Open Bitstream Unit
type OBU struct {
	Type     uint8
	Size     int
	Data     []byte
	HasSize  bool
}

// parseOBUs parses OBUs from data with proper bounds checking
func (d *AV1Detector) parseOBUs(data []byte) ([]OBU, error) {
	obus := make([]OBU, 0)
	offset := 0
	
	// Limit number of OBUs to prevent DoS
	maxOBUs := security.MaxOBUsPerFrame
	
	for offset < len(data) && len(obus) < maxOBUs {
		// Need at least 1 byte for header
		if offset >= len(data) {
			break
		}
		
		// Parse OBU header
		header := data[offset]
		offset++
		
		// Check forbidden bit
		if header&0x80 != 0 {
			return obus, fmt.Errorf("AV1 OBU forbidden bit set")
		}
		
		obu := OBU{
			Type:    (header >> 3) & 0x0F,
			HasSize: (header & 0x02) != 0,
		}
		
		// Skip extension if present
		if header&0x04 != 0 {
			if offset >= len(data) {
				// Incomplete OBU
				break
			}
			offset++ // Skip extension byte
		}
		
		// Parse size if present
		if obu.HasSize {
			if offset >= len(data) {
				// No size data available
				break
			}
			size, bytesRead, err := security.ReadLEB128(data[offset:])
			if err != nil {
				// Malformed LEB128
				return obus, fmt.Errorf("failed to parse OBU size: %w", err)
			}
			if bytesRead == 0 {
				// Failed to parse size
				break
			}
			offset += bytesRead
			
			// Check size limits
			if size > uint64(security.MaxNALUnitSize) {
				return obus, fmt.Errorf("AV1 OBU size too large: %d > %d", size, security.MaxNALUnitSize)
			}
			obu.Size = int(size)
		} else {
			// No size field, OBU extends to end of data
			obu.Size = len(data) - offset
			
			// Still check size limit
			if obu.Size > security.MaxNALUnitSize {
				return obus, fmt.Errorf("AV1 OBU size too large: %d > %d", obu.Size, security.MaxNALUnitSize)
			}
		}
		
		// Check if we have enough data for the OBU
		if obu.Size < 0 || offset+obu.Size > len(data) {
			// Not enough data for complete OBU
			break
		}
		
		// Extract OBU data
		obu.Data = data[offset : offset+obu.Size]
		offset += obu.Size
		
		obus = append(obus, obu)
		
		// Stop if we hit padding
		if obu.Type == AV1OBUTypePadding {
			break
		}
	}
	
	// Check if we hit the OBU limit
	if len(obus) >= maxOBUs {
		return obus, fmt.Errorf("too many OBUs in frame: %d >= %d", len(obus), maxOBUs)
	}
	
	return obus, nil
}


// parseFrameType extracts frame type from frame header OBU
// This is simplified - real implementation needs full bitstream parsing
func (d *AV1Detector) parseFrameType(data []byte) uint8 {
	if len(data) < 1 {
		return AV1FrameTypeInter
	}
	
	// Frame header starts with show_existing_frame flag
	// If not set, next bits contain frame_type
	// This is a simplified heuristic
	firstByte := data[0]
	
	if firstByte&0x80 != 0 {
		// show_existing_frame = 1
		return AV1FrameTypeInter
	}
	
	// Simplified: check second bit for key frame
	if firstByte&0x40 != 0 {
		return AV1FrameTypeKey
	}
	
	return AV1FrameTypeInter
}
