package types

import (
	"time"
)

// PacketType identifies the type of packet
type PacketType uint8

const (
	PacketTypeVideo PacketType = iota
	PacketTypeAudio
	PacketTypeData
)

// String returns the string representation of PacketType
func (p PacketType) String() string {
	switch p {
	case PacketTypeVideo:
		return "video"
	case PacketTypeAudio:
		return "audio"
	case PacketTypeData:
		return "data"
	default:
		return "unknown"
	}
}

// PacketFlags contains packet metadata flags
type PacketFlags uint16

const (
	PacketFlagKeyframe     PacketFlags = 1 << 0  // This packet starts a keyframe
	PacketFlagFrameStart   PacketFlags = 1 << 1  // Start of a frame
	PacketFlagFrameEnd     PacketFlags = 1 << 2  // End of a frame
	PacketFlagDiscardable  PacketFlags = 1 << 3  // Can be dropped (B-frame)
	PacketFlagGOPStart     PacketFlags = 1 << 4  // Start of GOP
	PacketFlagFlush        PacketFlags = 1 << 5  // Flush buffers after this
	PacketFlagCorrupted    PacketFlags = 1 << 6  // Data corruption detected
	PacketFlagPriority     PacketFlags = 1 << 7  // High priority packet
)

// TimestampedPacket represents a packet with full timing information
type TimestampedPacket struct {
	// Packet data
	Data         []byte
	
	// Timing information
	CaptureTime  time.Time    // When packet was received
	PTS          int64        // Presentation timestamp (90kHz for RTP)
	DTS          int64        // Decode timestamp (0 if same as PTS)
	Duration     int64        // Duration of this packet
	
	// Source information
	StreamID     string       // Stream identifier
	SSRC         uint32       // RTP SSRC
	SeqNum       uint16       // RTP sequence number
	
	// Metadata
	Type         PacketType   // Video/Audio/Data
	Codec        CodecType    // H.264, HEVC, etc.
	Flags        PacketFlags  // Packet flags
	
	// Frame information (if known)
	FrameNumber  uint64       // Frame this packet belongs to
	PacketInFrame int         // Packet number within frame
	TotalPackets  int         // Total packets in frame
	
	// Network information
	SourceAddr   string       // Source IP:port
	ArrivalDelta int64        // Microseconds since last packet
	
	// For presentation timing
	PresentationTime time.Time  // Calculated presentation time
}

// HasFlag checks if a flag is set
func (p *TimestampedPacket) HasFlag(flag PacketFlags) bool {
	return p.Flags&flag != 0
}

// SetFlag sets a flag
func (p *TimestampedPacket) SetFlag(flag PacketFlags) {
	p.Flags |= flag
}

// ClearFlag clears a flag
func (p *TimestampedPacket) ClearFlag(flag PacketFlags) {
	p.Flags &^= flag
}

// IsKeyframe returns true if this packet contains keyframe data
func (p *TimestampedPacket) IsKeyframe() bool {
	return p.HasFlag(PacketFlagKeyframe)
}

// IsFrameStart returns true if this packet starts a frame
func (p *TimestampedPacket) IsFrameStart() bool {
	return p.HasFlag(PacketFlagFrameStart)
}

// IsFrameEnd returns true if this packet ends a frame
func (p *TimestampedPacket) IsFrameEnd() bool {
	return p.HasFlag(PacketFlagFrameEnd)
}

// IsDiscardable returns true if this packet can be dropped without affecting other frames
func (p *TimestampedPacket) IsDiscardable() bool {
	return p.HasFlag(PacketFlagDiscardable)
}

// Clone creates a deep copy of the packet
func (p *TimestampedPacket) Clone() *TimestampedPacket {
	data := make([]byte, len(p.Data))
	copy(data, p.Data)
	
	clone := *p
	clone.Data = data
	return &clone
}
