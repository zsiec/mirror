package types

// Frame represents a simplified frame for validation purposes
type Frame struct {
	// Timing
	PTS int64 // Presentation timestamp
	DTS int64 // Decode timestamp

	// Data
	Data []byte

	// Frame characteristics
	Type  FrameType
	IsIDR bool

	// Metadata
	Metadata map[string]interface{}

	// Quality and corruption tracking
	RequiresSPS bool
	Corrupted   bool
}

// Additional frame type for video frames (used in tests)
const (
	FrameTypeVideo FrameType = 100 // Distinct from existing types
)

// IsKeyframe returns true if this is a keyframe (IDR, CRA, BLA, or I-frame)
func (f *Frame) IsKeyframe() bool {
	return f.IsIDR || f.Type.IsKeyframe()
}

// IsCorrupted returns true if frame data is corrupted
func (f *Frame) IsCorrupted() bool {
	return f.Corrupted
}
