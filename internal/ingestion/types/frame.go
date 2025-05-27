package types

import (
	"time"
)

// FrameType represents the type of video frame
type FrameType uint8

const (
	FrameTypeI   FrameType = iota // Intra-coded (keyframe)
	FrameTypeP                    // Predictive
	FrameTypeB                    // Bidirectional
	FrameTypeIDR                  // Instantaneous Decoder Refresh
	FrameTypeSPS                  // Sequence Parameter Set
	FrameTypePPS                  // Picture Parameter Set
	FrameTypeVPS                  // Video Parameter Set (HEVC)
	FrameTypeSEI                  // Supplemental Enhancement Info
	FrameTypeAUD                  // Access Unit Delimiter
)

// String returns the string representation of FrameType
func (f FrameType) String() string {
	switch f {
	case FrameTypeI:
		return "I"
	case FrameTypeP:
		return "P"
	case FrameTypeB:
		return "B"
	case FrameTypeIDR:
		return "IDR"
	case FrameTypeSPS:
		return "SPS"
	case FrameTypePPS:
		return "PPS"
	case FrameTypeVPS:
		return "VPS"
	case FrameTypeSEI:
		return "SEI"
	case FrameTypeAUD:
		return "AUD"
	default:
		return "unknown"
	}
}

// IsKeyframe returns true if this is a keyframe type
func (f FrameType) IsKeyframe() bool {
	return f == FrameTypeI || f == FrameTypeIDR
}

// IsReference returns true if this frame can be used as reference
func (f FrameType) IsReference() bool {
	return f == FrameTypeI || f == FrameTypeP || f == FrameTypeIDR
}

// IsDiscardable returns true if this frame can be dropped without affecting others
func (f FrameType) IsDiscardable() bool {
	return f == FrameTypeB || f == FrameTypeSEI
}

// FrameFlags contains frame metadata
type FrameFlags uint16

const (
	FrameFlagKeyframe    FrameFlags = 1 << 0
	FrameFlagReference   FrameFlags = 1 << 1
	FrameFlagCorrupted   FrameFlags = 1 << 2
	FrameFlagDroppable   FrameFlags = 1 << 3
	FrameFlagLastInGOP   FrameFlags = 1 << 4
	FrameFlagSceneChange FrameFlags = 1 << 5
	FrameFlagInterlaced  FrameFlags = 1 << 6
)

// VideoFrame represents a complete video frame
type VideoFrame struct {
	// Frame identification
	ID          uint64 // Unique frame ID
	StreamID    string // Stream this frame belongs to
	FrameNumber uint64 // Frame number in stream

	// Frame data
	NALUnits  []NALUnit // NAL units that compose this frame
	TotalSize int       // Total size in bytes

	// Timing
	PTS          int64     // Presentation timestamp
	DTS          int64     // Decode timestamp
	Duration     int64     // Frame duration
	CaptureTime  time.Time // When first packet was received
	CompleteTime time.Time // When frame was completed

	// Frame type and characteristics
	Type  FrameType  // I, P, B, etc.
	Flags FrameFlags // Frame flags

	// GOP information
	GOPId       uint64 // GOP this frame belongs to
	GOPPosition int    // Position within GOP

	// Dependencies
	References   []uint64 // Frames this depends on
	ReferencedBy []uint64 // Frames that depend on this

	// Quality information
	QP   int     // Quantization parameter
	PSNR float64 // Peak signal-to-noise ratio (if available)

	// Codec specific
	CodecData interface{} // Codec-specific metadata

	// For presentation timing
	PresentationTime time.Time // Calculated presentation time
}

// NALUnit represents a Network Abstraction Layer unit
type NALUnit struct {
	Type       uint8  // NAL unit type
	Data       []byte // NAL unit data (including header)
	Importance uint8  // Importance level (0-5)
	RefIdc     uint8  // nal_ref_idc (for H.264)
}

// GetTypeName returns a human-readable name for the NAL unit type (H.264)
func (n *NALUnit) GetTypeName() string {
	switch n.Type {
	case 1:
		return "Coded slice (non-IDR)"
	case 5:
		return "Coded slice (IDR)"
	case 6:
		return "SEI"
	case 7:
		return "SPS"
	case 8:
		return "PPS"
	case 9:
		return "AUD"
	default:
		return "NAL unit"
	}
}

// HasFlag checks if a flag is set
func (f *VideoFrame) HasFlag(flag FrameFlags) bool {
	return f.Flags&flag != 0
}

// SetFlag sets a flag
func (f *VideoFrame) SetFlag(flag FrameFlags) {
	f.Flags |= flag
}

// IsKeyframe returns true if this is a keyframe
func (f *VideoFrame) IsKeyframe() bool {
	return f.Type.IsKeyframe() || f.HasFlag(FrameFlagKeyframe)
}

// IsReference returns true if this frame is used as reference
func (f *VideoFrame) IsReference() bool {
	return f.Type.IsReference() || f.HasFlag(FrameFlagReference)
}

// IsCorrupted returns true if frame data is corrupted
func (f *VideoFrame) IsCorrupted() bool {
	return f.HasFlag(FrameFlagCorrupted)
}

// CanDrop returns true if this frame can be dropped safely
func (f *VideoFrame) CanDrop() bool {
	return f.Type.IsDiscardable() || f.HasFlag(FrameFlagDroppable)
}

// GetNALUnitByType returns the first NAL unit of the specified type
func (f *VideoFrame) GetNALUnitByType(nalType uint8) *NALUnit {
	for i := range f.NALUnits {
		if f.NALUnits[i].Type == nalType {
			return &f.NALUnits[i]
		}
	}
	return nil
}

// HasNALUnitType returns true if frame contains NAL unit of specified type
func (f *VideoFrame) HasNALUnitType(nalType uint8) bool {
	return f.GetNALUnitByType(nalType) != nil
}

// CalculateBitrate returns the bitrate of this frame
func (f *VideoFrame) CalculateBitrate() float64 {
	if f.Duration <= 0 {
		return 0
	}
	return float64(f.TotalSize*8) / (float64(f.Duration) / 90000.0)
}
