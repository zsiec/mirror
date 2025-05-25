package frame

import (
	"fmt"
	
	"github.com/zsiec/mirror/internal/ingestion/security"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// HEVC NAL unit types
const (
	// VCL NAL units
	HEVCNALTypeTrailN      = 0  // Trailing picture, non-reference
	HEVCNALTypeTrailR      = 1  // Trailing picture, reference
	HEVCNALTypeTSAN        = 2  // Temporal sub-layer access, non-reference
	HEVCNALTypeTSAR        = 3  // Temporal sub-layer access, reference
	HEVCNALTypeSTSAN       = 4  // Step-wise temporal sub-layer access, non-reference
	HEVCNALTypeSTSAR       = 5  // Step-wise temporal sub-layer access, reference
	HEVCNALTypeRADLN       = 6  // Random access decodable leading, non-reference
	HEVCNALTypeRADLR       = 7  // Random access decodable leading, reference
	HEVCNALTypeRASLN       = 8  // Random access skipped leading, non-reference
	HEVCNALTypeRASLR       = 9  // Random access skipped leading, reference
	
	// IRAP NAL units (Intra Random Access Point)
	HEVCNALTypeBLAWLP      = 16 // Broken link access with leading picture
	HEVCNALTypeBLAWRADL    = 17 // BLA with RADL
	HEVCNALTypeBLANLP      = 18 // BLA with no leading picture
	HEVCNALTypeIDRWRADL    = 19 // IDR with RADL
	HEVCNALTypeIDRNLP      = 20 // IDR with no leading picture
	HEVCNALTypeCRANUT      = 21 // Clean random access
	
	// Non-VCL NAL units
	HEVCNALTypeVPS         = 32 // Video parameter set
	HEVCNALTypeSPS         = 33 // Sequence parameter set
	HEVCNALTypePPS         = 34 // Picture parameter set
	HEVCNALTypeAUD         = 35 // Access unit delimiter
	HEVCNALTypeEOS         = 36 // End of sequence
	HEVCNALTypeEOB         = 37 // End of bitstream
	HEVCNALTypeFD          = 38 // Filler data
	HEVCNALTypePrefixSEI   = 39 // Prefix SEI
	HEVCNALTypeSuffixSEI   = 40 // Suffix SEI
	
	// Aggregation packets
	HEVCNALTypeAP          = 48 // Aggregation packet
	HEVCNALTypeFU          = 49 // Fragmentation unit
)

// HEVCDetector detects HEVC/H.265 frame boundaries and types
type HEVCDetector struct {
	lastNALType    uint8
	frameStarted   bool
	nalBuffer      []byte
}

// NewHEVCDetector creates a new HEVC frame detector
func NewHEVCDetector() *HEVCDetector {
	return &HEVCDetector{}
}

// DetectBoundaries detects frame boundaries in HEVC stream
func (d *HEVCDetector) DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	if len(pkt.Data) < 2 {
		return false, false
	}
	
	// Check if this is RTP or MPEG-TS with start codes
	if d.hasStartCode(pkt.Data) {
		return d.detectBoundariesWithStartCode(pkt)
	}
	
	// RTP mode - NAL unit header is 2 bytes
	nalType := (pkt.Data[0] >> 1) & 0x3F
	
	// Handle fragmentation units
	if nalType == HEVCNALTypeFU {
		return d.handleFU(pkt)
	}
	
	// Access Unit Delimiter always starts new frame
	if nalType == HEVCNALTypeAUD {
		isStart = true
		d.frameStarted = true
	}
	
	// IRAP (keyframe) NAL units start new frames
	if d.isIRAPNAL(nalType) {
		isStart = true
		d.frameStarted = true
	}
	
	// VPS/SPS/PPS start new frames
	if nalType == HEVCNALTypeVPS || nalType == HEVCNALTypeSPS || nalType == HEVCNALTypePPS {
		isStart = true
		d.frameStarted = true
	}
	
	// VCL NAL after non-VCL NAL starts new frame
	if d.isVCLNAL(nalType) && !d.isVCLNAL(d.lastNALType) && d.lastNALType != 0 {
		isStart = true
		d.frameStarted = true
	}
	
	// Non-VCL NAL after VCL NAL ends frame
	if d.frameStarted && d.isVCLNAL(d.lastNALType) && !d.isVCLNAL(nalType) {
		isEnd = true
		d.frameStarted = false
	}
	
	d.lastNALType = nalType
	
	return isStart, isEnd
}

// handleFU handles fragmentation units
func (d *HEVCDetector) handleFU(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	if len(pkt.Data) < 3 {
		return false, false
	}
	
	// FU header is in third byte
	fuHeader := pkt.Data[2]
	startBit := (fuHeader & 0x80) != 0
	endBit := (fuHeader & 0x40) != 0
	nalType := fuHeader & 0x3F
	
	if startBit {
		// First fragment
		if d.isIRAPNAL(nalType) || nalType == HEVCNALTypeAUD ||
		   nalType == HEVCNALTypeVPS || nalType == HEVCNALTypeSPS || nalType == HEVCNALTypePPS {
			isStart = true
			d.frameStarted = true
		}
	}
	
	if endBit && d.frameStarted && d.isVCLNAL(nalType) {
		// Last fragment of VCL NAL
		// Frame might end after this
	}
	
	return isStart, isEnd
}

// detectBoundariesWithStartCode handles streams with start codes
func (d *HEVCDetector) detectBoundariesWithStartCode(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	nalUnits, err := d.findNALUnits(pkt.Data)
	if err != nil {
		// Log error but continue processing what we have
		// In production, this should be logged properly
		return false, false
	}
	
	for _, nalUnit := range nalUnits {
		if len(nalUnit) < 2 {
			continue
		}
		
		nalType := (nalUnit[0] >> 1) & 0x3F
		
		// Check for frame start
		if nalType == HEVCNALTypeAUD || d.isIRAPNAL(nalType) ||
		   nalType == HEVCNALTypeVPS || nalType == HEVCNALTypeSPS || nalType == HEVCNALTypePPS {
			isStart = true
		}
		
		// Check for frame end
		if d.frameStarted && nalType == HEVCNALTypeAUD {
			isEnd = true
		}
	}
	
	return isStart, isEnd
}

// GetFrameType determines frame type from NAL units
func (d *HEVCDetector) GetFrameType(nalUnits []types.NALUnit) types.FrameType {
	hasIRAP := false
	hasSPS := false
	hasPPS := false
	isReference := false
	
	for _, nal := range nalUnits {
		if len(nal.Data) < 2 {
			continue
		}
		
		nalType := (nal.Data[0] >> 1) & 0x3F
		
		if d.isIRAPNAL(nalType) {
			hasIRAP = true
		}
		
		switch nalType {
		case HEVCNALTypeVPS:
			// VPS detected but not used for frame type determination
		case HEVCNALTypeSPS:
			hasSPS = true
		case HEVCNALTypePPS:
			hasPPS = true
		case HEVCNALTypeTrailR, HEVCNALTypeTSAR, HEVCNALTypeSTSAR, HEVCNALTypeRADLR, HEVCNALTypeRASLR:
			isReference = true
		}
	}
	
	if hasIRAP {
		return types.FrameTypeIDR
	}
	if hasSPS {
		return types.FrameTypeSPS
	}
	if hasPPS {
		return types.FrameTypePPS
	}
	
	// HEVC doesn't have explicit P/B frame types in NAL headers
	// Would need slice header parsing for accurate detection
	if isReference {
		return types.FrameTypeP // Reference frame, likely P
	}
	
	return types.FrameTypeB // Non-reference, likely B
}

// IsKeyframe checks if data contains a keyframe
func (d *HEVCDetector) IsKeyframe(data []byte) bool {
	if len(data) >= 2 {
		nalType := (data[0] >> 1) & 0x3F
		if d.isIRAPNAL(nalType) {
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
		if len(nalUnit) >= 2 {
			nalType := (nalUnit[0] >> 1) & 0x3F
			if d.isIRAPNAL(nalType) || nalType == HEVCNALTypeVPS || 
			   nalType == HEVCNALTypeSPS || nalType == HEVCNALTypePPS {
				return true
			}
		}
	}
	
	return false
}

// GetCodec returns the codec type
func (d *HEVCDetector) GetCodec() types.CodecType {
	return types.CodecHEVC
}

// isVCLNAL checks if NAL type is VCL
func (d *HEVCDetector) isVCLNAL(nalType uint8) bool {
	return nalType <= 31 // nalType is uint8, always >= 0
}

// isIRAPNAL checks if NAL type is IRAP (keyframe)
func (d *HEVCDetector) isIRAPNAL(nalType uint8) bool {
	return nalType >= 16 && nalType <= 21
}

// hasStartCode checks if data begins with start code
func (d *HEVCDetector) hasStartCode(data []byte) bool {
	if len(data) >= 3 {
		return data[0] == 0 && data[1] == 0 && (data[2] == 1 || (len(data) > 3 && data[2] == 0 && data[3] == 1))
	}
	return false
}

// findNALUnits finds NAL units in data with start codes
// Returns found NAL units and any error encountered
func (d *HEVCDetector) findNALUnits(data []byte) ([][]byte, error) {
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
