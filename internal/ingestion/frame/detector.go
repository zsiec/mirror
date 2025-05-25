package frame

import (
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// Detector detects frame boundaries and types from packets
type Detector interface {
	// DetectBoundaries analyzes a packet and detects frame boundaries
	DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool)
	
	// GetFrameType determines the frame type from NAL units
	GetFrameType(nalUnits []types.NALUnit) types.FrameType
	
	// IsKeyframe checks if the packet contains keyframe data
	IsKeyframe(data []byte) bool
	
	// GetCodec returns the codec type this detector handles
	GetCodec() types.CodecType
}

// Factory creates frame detectors based on codec
type DetectorFactory struct{}

// NewDetectorFactory creates a new detector factory
func NewDetectorFactory() *DetectorFactory {
	return &DetectorFactory{}
}

// CreateDetector creates a frame detector for the specified codec
func (f *DetectorFactory) CreateDetector(codec types.CodecType) Detector {
	switch codec {
	case types.CodecH264:
		return NewH264Detector()
	case types.CodecHEVC:
		return NewHEVCDetector()
	case types.CodecAV1:
		return NewAV1Detector()
	case types.CodecJPEGXS:
		return NewJPEGXSDetector()
	default:
		return nil
	}
}
