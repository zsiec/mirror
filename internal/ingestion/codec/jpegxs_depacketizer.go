package codec

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/memory"
)

// JPEGXSDepacketizer handles depacketization of JPEG XS RTP streams
// Based on RFC 9134 (RTP Payload Format for JPEG XS)
type JPEGXSDepacketizer struct {
	fragments       [][]byte
	lastSeq         uint16
	hasLastSeq      bool // Whether lastSeq has been set (seq 0 is valid per RFC 3550)
	currentFrameID  uint32
	expectedPackets uint16
	receivedPackets uint16
	frameComplete   bool
	profile         JPEGXSProfile
}

// JPEGXSProfile represents JPEG XS profiles
type JPEGXSProfile uint8

const (
	// JPEG XS Profile values from RFC 9134
	ProfileLight        JPEGXSProfile = 0x1A // Light profile
	ProfileMain         JPEGXSProfile = 0x2A // Main profile
	ProfileHigh         JPEGXSProfile = 0x3A // High profile
	ProfileHigh444_12   JPEGXSProfile = 0x4A // High 4:4:4 12-bit profile
	ProfileLightSubline JPEGXSProfile = 0x1B // Light Subline profile
	ProfileMainSubline  JPEGXSProfile = 0x2B // Main Subline profile
)

// JPEG XS RTP header constants
const (
	// Minimum header size (4 bytes)
	jpegxsMinHeaderSize = 4

	// Extended header size (additional 4 bytes)
	jpegxsExtendedHeaderSize = 8

	// Packetization modes
	packetModeProgressive = 0
	packetModeInterlaced  = 1

	// Field identification for interlaced
	fieldTop    = 0
	fieldBottom = 1
)

// Depacketize processes an RTP packet and returns complete JPEG XS frames
func (d *JPEGXSDepacketizer) Depacketize(packet *rtp.Packet) ([][]byte, error) {
	// Extract payload and sequence number from packet
	payload := packet.Payload
	sequenceNumber := packet.SequenceNumber
	if len(payload) < jpegxsMinHeaderSize {
		return nil, errors.New("payload too short for JPEG XS header")
	}

	// Parse JPEG XS RTP header (RFC 9134 Section 4.3)
	header, headerSize, err := d.parseHeader(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JPEG XS header: %w", err)
	}

	// Check for packet loss — use hasLastSeq flag since seq 0 is valid per RFC 3550
	packetLossDetected := false
	if d.hasLastSeq && sequenceNumber != d.lastSeq+1 {
		// Packet loss detected
		packetLossDetected = true
		if !d.frameComplete && len(d.fragments) > 0 {
			// Discard incomplete frame
			d.resetFrame()
		}
	}
	d.lastSeq = sequenceNumber
	d.hasLastSeq = true

	// Check if this is a new frame
	if header.FirstPacket || header.FrameID != d.currentFrameID {
		// Start of a new frame
		if !d.frameComplete && len(d.fragments) > 0 {
			// Previous frame was incomplete, discard it
			d.resetFrame()
		}
		d.currentFrameID = header.FrameID
		d.expectedPackets = 0
		d.receivedPackets = 0
		d.frameComplete = false
		d.fragments = [][]byte{}
	} else if packetLossDetected && !header.FirstPacket {
		// Received a non-first packet after packet loss, discard
		return nil, nil
	}

	// Extract JPEG XS data (after header)
	jpegData := payload[headerSize:]
	if len(jpegData) == 0 {
		return nil, errors.New("no JPEG XS data in payload")
	}

	// Add fragment
	fragment := make([]byte, len(jpegData))
	copy(fragment, jpegData)
	d.fragments = append(d.fragments, fragment)
	d.receivedPackets++

	// Update expected packets count if this is the last packet
	if header.LastPacket {
		d.expectedPackets = d.receivedPackets
	}

	// Check if frame is complete
	var frames [][]byte
	if header.LastPacket {
		// This is the last packet of the frame
		frame, err := d.assembleFrame()
		if err != nil {
			d.resetFrame()
			return nil, err
		}
		frames = append(frames, frame)
		d.frameComplete = true
		d.resetFrame()
	}

	return frames, nil
}

// jpegxsHeader represents the parsed JPEG XS RTP header
type jpegxsHeader struct {
	FirstPacket  bool
	LastPacket   bool
	PacketMode   uint8
	FieldID      uint8
	FrameID      uint32
	PacketID     uint16
	HasExtended  bool
	Profile      JPEGXSProfile
	Level        uint8
	SubLevel     uint8
	ChromaFormat uint8
	BitDepth     uint8
	Width        uint16
	Height       uint16
	Interlaced   bool
}

// parseHeader parses the JPEG XS RTP header according to RFC 9134
func (d *JPEGXSDepacketizer) parseHeader(payload []byte) (*jpegxsHeader, int, error) {
	if len(payload) < jpegxsMinHeaderSize {
		return nil, 0, errors.New("payload too short")
	}

	header := &jpegxsHeader{}

	// Parse first 4 bytes (mandatory header)
	// Byte 0: Flags
	flags := payload[0]
	header.FirstPacket = (flags & 0x80) != 0 // F bit
	header.LastPacket = (flags & 0x40) != 0  // L bit
	header.PacketMode = (flags >> 4) & 0x03  // P field (2 bits)
	header.FieldID = (flags >> 3) & 0x01     // I bit
	header.HasExtended = (flags & 0x04) != 0 // E bit
	// Reserved bits: 0x03

	// Bytes 1-3: Frame ID (24 bits)
	header.FrameID = uint32(payload[1])<<16 | uint32(payload[2])<<8 | uint32(payload[3])

	headerSize := jpegxsMinHeaderSize

	// Parse extended header if present
	if header.HasExtended {
		if len(payload) < jpegxsExtendedHeaderSize {
			return nil, 0, errors.New("payload too short for extended header")
		}

		// Bytes 4-5: Packet ID (16 bits)
		header.PacketID = binary.BigEndian.Uint16(payload[4:6])

		// Byte 6: Profile and Level
		header.Profile = JPEGXSProfile(payload[6])
		header.Level = payload[6] & 0x0F

		// Byte 7: Sub-level and additional info
		header.SubLevel = (payload[7] >> 4) & 0x0F
		header.ChromaFormat = (payload[7] >> 2) & 0x03
		header.BitDepth = payload[7] & 0x03

		// Update depacketizer profile
		d.profile = header.Profile

		headerSize = jpegxsExtendedHeaderSize
	}

	// Set interlaced flag based on packet mode
	header.Interlaced = header.PacketMode == packetModeInterlaced

	return header, headerSize, nil
}

// assembleFrame combines all fragments into a complete JPEG XS frame
func (d *JPEGXSDepacketizer) assembleFrame() ([]byte, error) {
	if len(d.fragments) == 0 {
		return nil, errors.New("no fragments to assemble")
	}

	// Calculate total size
	totalSize := 0
	for _, frag := range d.fragments {
		totalSize += len(frag)
	}

	// Combine fragments
	frame := make([]byte, 0, totalSize)
	for _, frag := range d.fragments {
		frame = append(frame, frag...)
	}

	return frame, nil
}

// resetFrame resets the frame assembly state
func (d *JPEGXSDepacketizer) resetFrame() {
	d.fragments = [][]byte{}
	d.expectedPackets = 0
	d.receivedPackets = 0
	d.frameComplete = false
}

// Reset clears the depacketizer state
func (d *JPEGXSDepacketizer) Reset() {
	d.resetFrame()
	d.lastSeq = 0
	d.hasLastSeq = false
	d.currentFrameID = 0
}

// GetProfile returns the current JPEG XS profile
func (d *JPEGXSDepacketizer) GetProfile() JPEGXSProfile {
	return d.profile
}

// SetProfile sets the expected JPEG XS profile
func (d *JPEGXSDepacketizer) SetProfile(profile JPEGXSProfile) {
	d.profile = profile
}

// GetProfileName returns a human-readable name for a JPEG XS profile
func GetProfileName(profile JPEGXSProfile) string {
	switch profile {
	case ProfileLight:
		return "Light"
	case ProfileMain:
		return "Main"
	case ProfileHigh:
		return "High"
	case ProfileHigh444_12:
		return "High 4:4:4 12-bit"
	case ProfileLightSubline:
		return "Light Subline"
	case ProfileMainSubline:
		return "Main Subline"
	default:
		return fmt.Sprintf("Unknown(0x%02X)", uint8(profile))
	}
}

// ValidateProfile checks if a profile value is valid
func ValidateProfile(profile JPEGXSProfile) bool {
	switch profile {
	case ProfileLight, ProfileMain, ProfileHigh, ProfileHigh444_12, ProfileLightSubline, ProfileMainSubline:
		return true
	default:
		return false
	}
}

// GetChromaFormatString returns a string representation of the chroma format
func GetChromaFormatString(chromaFormat uint8) string {
	switch chromaFormat {
	case 0:
		return "4:2:0"
	case 1:
		return "4:2:2"
	case 2:
		return "4:4:4"
	default:
		return "Unknown"
	}
}

// GetBitDepth returns the actual bit depth from the encoded value
func GetBitDepth(bitDepthCode uint8) int {
	switch bitDepthCode {
	case 0:
		return 8
	case 1:
		return 10
	case 2:
		return 12
	default:
		return 0
	}
}

// JPEGXSDepacketizerWithMemory extends JPEGXSDepacketizer with memory management
type JPEGXSDepacketizerWithMemory struct {
	JPEGXSDepacketizer
	streamID      string
	memController *memory.Controller
	memoryLimit   int64
	currentUsage  int64
}

// NewJPEGXSDepacketizerWithMemory creates a memory-aware JPEG-XS depacketizer
func NewJPEGXSDepacketizerWithMemory(streamID string, memController *memory.Controller, limit int64) Depacketizer {
	return &JPEGXSDepacketizerWithMemory{
		JPEGXSDepacketizer: JPEGXSDepacketizer{
			fragments: [][]byte{},
		},
		streamID:      streamID,
		memController: memController,
		memoryLimit:   limit,
	}
}

// Depacketize processes an RTP packet with memory management
func (d *JPEGXSDepacketizerWithMemory) Depacketize(packet *rtp.Packet) ([][]byte, error) {
	// Estimate memory needed for this packet
	estimatedSize := int64(len(packet.Payload) * 2) // Conservative estimate

	// Check if we would exceed memory limit
	if d.currentUsage+estimatedSize > d.memoryLimit {
		return nil, fmt.Errorf("frame size would exceed memory limit: current=%d, needed=%d, limit=%d",
			d.currentUsage, estimatedSize, d.memoryLimit)
	}

	// Request memory from controller
	if err := d.memController.RequestMemory(d.streamID, estimatedSize); err != nil {
		return nil, fmt.Errorf("memory allocation failed: %w", err)
	}
	d.currentUsage += estimatedSize

	// Process packet
	frames, err := d.JPEGXSDepacketizer.Depacketize(packet)

	// If we got complete frames, release fragment memory
	if len(frames) > 0 {
		// Calculate actual memory used
		actualSize := int64(0)
		for _, frame := range frames {
			actualSize += int64(len(frame))
		}

		// Release excess memory
		if estimatedSize > actualSize {
			excessMemory := estimatedSize - actualSize
			d.memController.ReleaseMemory(d.streamID, excessMemory)
			d.currentUsage -= excessMemory
		}

		// Reset fragment memory tracking
		d.currentUsage = 0
	}

	return frames, err
}

// Reset clears the depacketizer state and releases memory
func (d *JPEGXSDepacketizerWithMemory) Reset() {
	// Release any held memory
	if d.currentUsage > 0 {
		d.memController.ReleaseMemory(d.streamID, d.currentUsage)
		d.currentUsage = 0
	}

	d.JPEGXSDepacketizer.Reset()
}
