package frame

import (
	"fmt"
	
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// IDRDetector provides enhanced IDR/keyframe detection across multiple codecs
type IDRDetector struct {
	codec           types.CodecType
	h264Detector    *H264Detector
	hevcDetector    *HEVCDetector
	av1Detector     *AV1Detector
	jpegxsDetector  *JPEGXSDetector
	
	// Enhanced detection state
	lastKeyframe    uint64 // Frame number of last keyframe
	keyframeHistory []uint64 // History of keyframe intervals
	avgGOPSize      float64
	firstKeyframeSeen bool  // Track if we've seen the first keyframe
	
	// Recovery hints
	suspectedCorruption bool
	missingReferences   int
	
	logger logger.Logger
}

// NewIDRDetector creates a new enhanced IDR detector
func NewIDRDetector(codec types.CodecType, logger logger.Logger) *IDRDetector {
	d := &IDRDetector{
		codec:           codec,
		keyframeHistory: make([]uint64, 0, 100),
		logger:          logger,
	}
	
	// Initialize codec-specific detector
	switch codec {
	case types.CodecH264:
		d.h264Detector = NewH264Detector()
	case types.CodecHEVC:
		d.hevcDetector = NewHEVCDetector()
	case types.CodecAV1:
		d.av1Detector = NewAV1Detector()
	case types.CodecJPEGXS:
		d.jpegxsDetector = NewJPEGXSDetector()
	}
	
	return d
}

// IsIDRFrame performs enhanced IDR/keyframe detection
func (d *IDRDetector) IsIDRFrame(frame *types.VideoFrame) bool {
	if frame == nil || len(frame.NALUnits) == 0 {
		return false
	}
	
	// Quick check using frame flags
	if frame.HasFlag(types.FrameFlagKeyframe) {
		return true
	}
	
	// Codec-specific IDR detection
	switch d.codec {
	case types.CodecH264:
		return d.isH264IDR(frame)
	case types.CodecHEVC:
		return d.isHEVCIDR(frame)
	case types.CodecAV1:
		return d.isAV1Keyframe(frame)
	case types.CodecJPEGXS:
		return d.isJPEGXSKeyframe(frame)
	default:
		// Fallback to basic detection
		return frame.Type == types.FrameTypeI || frame.Type == types.FrameTypeIDR
	}
}

// isH264IDR performs enhanced H.264 IDR detection
func (d *IDRDetector) isH264IDR(frame *types.VideoFrame) bool {
	hasIDR := false
	hasSPS := false
	hasPPS := false
	hasRecoveryPoint := false
	
	for _, nal := range frame.NALUnits {
		if len(nal.Data) == 0 {
			continue
		}
		
		nalType := nal.Data[0] & 0x1F
		
		switch nalType {
		case H264NALTypeIDR:
			hasIDR = true
			
		case H264NALTypeSPS:
			hasSPS = true
			// Parse SPS for additional info
			d.parseSPS(nal.Data[1:])
			
		case H264NALTypePPS:
			hasPPS = true
			
		case H264NALTypeSEI:
			// Check for recovery point SEI
			if d.hasRecoveryPointSEI(nal.Data[1:]) {
				hasRecoveryPoint = true
			}
			
		case H264NALTypeSlice:
			// Check if this is an I-slice that could serve as recovery point
			if d.isIntraSlice(nal.Data[1:]) {
				// This could be a recovery point even if not IDR
				hasRecoveryPoint = true
			}
		}
	}
	
	// IDR frame confirmed
	if hasIDR {
		d.updateKeyframeStats(frame.FrameNumber)
		return true
	}
	
	// Recovery point can serve as keyframe for seeking
	if hasRecoveryPoint && hasSPS && hasPPS {
		d.logger.Debug("Found recovery point that can serve as keyframe")
		d.updateKeyframeStats(frame.FrameNumber)
		return true
	}
	
	return false
}

// isHEVCIDR performs enhanced HEVC IDR detection
func (d *IDRDetector) isHEVCIDR(frame *types.VideoFrame) bool {
	hasIRAP := false
	hasVPS := false
	hasSPS := false
	hasPPS := false
	
	for _, nal := range frame.NALUnits {
		if len(nal.Data) < 2 {
			continue
		}
		
		nalType := (nal.Data[0] >> 1) & 0x3F
		
		switch nalType {
		case HEVCNALTypeBLAWLP, HEVCNALTypeBLAWRADL, HEVCNALTypeBLANLP,
		     HEVCNALTypeIDRWRADL, HEVCNALTypeIDRNLP, HEVCNALTypeCRANUT:
			hasIRAP = true
			
		case HEVCNALTypeVPS:
			hasVPS = true
			
		case HEVCNALTypeSPS:
			hasSPS = true
			d.parseHEVCSPS(nal.Data[2:])
			
		case HEVCNALTypePPS:
			hasPPS = true
		}
	}
	
	// IRAP with parameter sets is a keyframe
	if hasIRAP {
		d.updateKeyframeStats(frame.FrameNumber)
		return true
	}
	
	// CRA (Clean Random Access) can also serve as keyframe
	if hasIRAP && hasVPS && hasSPS && hasPPS {
		d.updateKeyframeStats(frame.FrameNumber)
		return true
	}
	
	return false
}

// isAV1Keyframe performs AV1 keyframe detection
func (d *IDRDetector) isAV1Keyframe(frame *types.VideoFrame) bool {
	// For AV1, check OBU headers
	for _, nal := range frame.NALUnits {
		if len(nal.Data) < 2 {
			continue
		}
		
		// Parse OBU header
		obuType := (nal.Data[0] >> 3) & 0x0F
		
		// Check for key frame OBU
		if obuType == 6 { // OBU_FRAME
			// Check frame header for key frame
			if d.isAV1KeyframeOBU(nal.Data[1:]) {
				d.updateKeyframeStats(frame.FrameNumber)
				return true
			}
		}
	}
	
	return false
}

// isJPEGXSKeyframe checks for JPEG-XS keyframe
func (d *IDRDetector) isJPEGXSKeyframe(frame *types.VideoFrame) bool {
	// JPEG-XS frames are all intra-coded (keyframes)
	d.updateKeyframeStats(frame.FrameNumber)
	return true
}

// GetKeyframeInterval returns the average keyframe interval
func (d *IDRDetector) GetKeyframeInterval() float64 {
	return d.avgGOPSize
}

// PredictNextKeyframe predicts when the next keyframe will occur
func (d *IDRDetector) PredictNextKeyframe(currentFrame uint64) uint64 {
	if d.avgGOPSize == 0 {
		return currentFrame + 30 // Default to 30 frames
	}
	
	framesSinceLastKeyframe := currentFrame - d.lastKeyframe
	if framesSinceLastKeyframe >= uint64(d.avgGOPSize) {
		return currentFrame + 1 // Keyframe is due
	}
	
	return d.lastKeyframe + uint64(d.avgGOPSize)
}

// NeedsKeyframe checks if a keyframe is needed for recovery
func (d *IDRDetector) NeedsKeyframe(currentFrame uint64, hasErrors bool) bool {
	// Always need keyframe if we have errors
	if hasErrors || d.suspectedCorruption {
		return true
	}
	
	// Check if we've gone too long without a keyframe
	framesSinceLastKeyframe := currentFrame - d.lastKeyframe
	maxGOPSize := d.avgGOPSize * 2
	if maxGOPSize < 60 {
		maxGOPSize = 60 // At least 2 seconds at 30fps
	}
	
	return framesSinceLastKeyframe > uint64(maxGOPSize)
}

// updateKeyframeStats updates keyframe interval statistics
func (d *IDRDetector) updateKeyframeStats(frameNumber uint64) {
	if d.firstKeyframeSeen {
		interval := frameNumber - d.lastKeyframe
		if interval > 0 {
			d.keyframeHistory = append(d.keyframeHistory, interval)
			
			// Keep only recent history
			if len(d.keyframeHistory) > 100 {
				d.keyframeHistory = d.keyframeHistory[1:]
			}
			
			// Calculate average GOP size
			var sum uint64
			for _, interval := range d.keyframeHistory {
				sum += interval
			}
			d.avgGOPSize = float64(sum) / float64(len(d.keyframeHistory))
		}
	} else {
		d.firstKeyframeSeen = true
	}
	
	d.lastKeyframe = frameNumber
	d.suspectedCorruption = false
	d.missingReferences = 0
}

// ReportCorruption reports suspected corruption for recovery decisions
func (d *IDRDetector) ReportCorruption() {
	d.suspectedCorruption = true
	d.missingReferences++
}

// Helper methods for parsing

func (d *IDRDetector) parseSPS(data []byte) {
	// Basic SPS parsing to extract profile/level info
	// This helps identify codec capabilities
	if len(data) < 3 {
		return
	}
	
	profileIdc := data[0]
	levelIdc := data[2]
	
	d.logger.WithFields(map[string]interface{}{
		"profile": profileIdc,
		"level":   levelIdc,
	}).Debug("Parsed H.264 SPS")
}

func (d *IDRDetector) parseHEVCSPS(data []byte) {
	// Basic HEVC SPS parsing
	if len(data) < 2 {
		return
	}
	
	// Extract key information for recovery decisions
	d.logger.Debug("Parsed HEVC SPS")
}

func (d *IDRDetector) hasRecoveryPointSEI(data []byte) bool {
	// Check for recovery point SEI message
	// SEI payload type 6 is recovery point
	if len(data) < 2 {
		return false
	}
	
	payloadType := data[0]
	return payloadType == 6
}

func (d *IDRDetector) isIntraSlice(data []byte) bool {
	// Parse H.264 slice header to detect I-slice
	if len(data) == 0 {
		return false
	}
	
	// Skip first_mb_in_slice (ue(v))
	offset := 0
	_, err := decodeExpGolomb(data, &offset)
	if err != nil {
		return false
	}
	
	// Parse slice_type (ue(v))
	sliceType, err := decodeExpGolomb(data, &offset)
	if err != nil {
		return false
	}
	
	// Check if this is an I-slice
	// slice_type values 2, 4, 7, 9 are I-slices
	return sliceType%5 == 2 || sliceType%5 == 4
}

// decodeExpGolomb decodes an unsigned Exp-Golomb coded value
func decodeExpGolomb(data []byte, offset *int) (uint32, error) {
	leadingZeros := 0
	
	// Count leading zeros
	for {
		if *offset/8 >= len(data) {
			return 0, fmt.Errorf("insufficient data for exp-golomb")
		}
		
		if getBit(data, *offset) == 0 {
			leadingZeros++
			(*offset)++
		} else {
			(*offset)++ // Skip the '1' bit
			break
		}
		
		// Prevent infinite loops on corrupted data
		if leadingZeros > 32 {
			return 0, fmt.Errorf("invalid exp-golomb: too many leading zeros")
		}
	}
	
	// Read the next 'leadingZeros' bits
	info := uint32(1) << leadingZeros
	for i := leadingZeros - 1; i >= 0; i-- {
		if *offset/8 >= len(data) {
			return 0, fmt.Errorf("insufficient data for exp-golomb suffix")
		}
		info |= uint32(getBit(data, *offset)) << i
		(*offset)++
	}
	
	return info - 1, nil
}

// getBit extracts a single bit from data at the given bit offset
func getBit(data []byte, bitOffset int) byte {
	return (data[bitOffset/8] >> (7 - (bitOffset % 8))) & 0x1
}

func (d *IDRDetector) isAV1KeyframeOBU(data []byte) bool {
	// Parse AV1 frame header
	if len(data) < 1 {
		return false
	}
	
	// Check frame type bits
	frameType := (data[0] >> 5) & 0x03
	return frameType == 0 // KEY_FRAME
}

// RecoveryPoint represents a point where decoding can safely resume
type RecoveryPoint struct {
	FrameNumber uint64
	PTS         int64
	IsIDR       bool
	HasSPS      bool
	HasPPS      bool
	Confidence  float64 // 0.0 to 1.0
}

// FindRecoveryPoints finds all possible recovery points in recent frames
func (d *IDRDetector) FindRecoveryPoints(frames []*types.VideoFrame) []RecoveryPoint {
	points := make([]RecoveryPoint, 0)
	
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		
		point := RecoveryPoint{
			FrameNumber: frame.FrameNumber,
			PTS:         frame.PTS,
		}
		
		// Check if this is an IDR
		if d.IsIDRFrame(frame) {
			point.IsIDR = true
			point.Confidence = 1.0
			points = append(points, point)
			continue
		}
		
		// Check for other recovery indicators
		confidence := 0.0
		
		// I-frame without IDR
		if frame.Type == types.FrameTypeI {
			confidence += 0.7
		}
		
		// Check for parameter sets
		for _, nal := range frame.NALUnits {
			if len(nal.Data) == 0 {
				continue
			}
			
			switch d.codec {
			case types.CodecH264:
				nalType := nal.Data[0] & 0x1F
				if nalType == H264NALTypeSPS {
					point.HasSPS = true
					confidence += 0.15
				} else if nalType == H264NALTypePPS {
					point.HasPPS = true
					confidence += 0.15
				}
				
			case types.CodecHEVC:
				if len(nal.Data) >= 2 {
					nalType := (nal.Data[0] >> 1) & 0x3F
					if nalType == HEVCNALTypeSPS {
						point.HasSPS = true
						confidence += 0.15
					} else if nalType == HEVCNALTypePPS {
						point.HasPPS = true
						confidence += 0.15
					}
				}
			}
		}
		
		// Only consider as recovery point if confidence is high enough
		if confidence >= 0.7 {
			point.Confidence = confidence
			points = append(points, point)
		}
	}
	
	return points
}

// GetRecoveryStrategy suggests a recovery strategy based on stream analysis
func (d *IDRDetector) GetRecoveryStrategy() RecoveryStrategy {
	// Base strategy on observed GOP structure
	if d.avgGOPSize == 0 {
		return RecoveryStrategyWaitForKeyframe
	}
	
	// If GOPs are small, waiting is viable
	if d.avgGOPSize < 30 {
		return RecoveryStrategyWaitForKeyframe
	}
	
	// If corruption is suspected and GOPs are large, request keyframe
	if d.suspectedCorruption && d.avgGOPSize > 60 {
		return RecoveryStrategyRequestKeyframe
	}
	
	// If we have missing references, try to find recovery point
	if d.missingReferences > 2 {
		return RecoveryStrategyFindRecoveryPoint
	}
	
	return RecoveryStrategyWaitForKeyframe
}

// RecoveryStrategy represents different recovery approaches
type RecoveryStrategy int

const (
	RecoveryStrategyWaitForKeyframe RecoveryStrategy = iota
	RecoveryStrategyRequestKeyframe
	RecoveryStrategyFindRecoveryPoint
	RecoveryStrategyReset
)

// Clone creates a copy of the detector for parallel processing
func (d *IDRDetector) Clone() *IDRDetector {
	clone := &IDRDetector{
		codec:           d.codec,
		keyframeHistory: make([]uint64, len(d.keyframeHistory)),
		avgGOPSize:      d.avgGOPSize,
		lastKeyframe:    d.lastKeyframe,
		firstKeyframeSeen: d.firstKeyframeSeen,
		logger:          d.logger,
	}
	
	copy(clone.keyframeHistory, d.keyframeHistory)
	
	// Clone codec-specific detectors
	switch d.codec {
	case types.CodecH264:
		clone.h264Detector = NewH264Detector()
	case types.CodecHEVC:
		clone.hevcDetector = NewHEVCDetector()
	case types.CodecAV1:
		clone.av1Detector = NewAV1Detector()
	case types.CodecJPEGXS:
		clone.jpegxsDetector = NewJPEGXSDetector()
	}
	
	return clone
}
