package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/ingestion/security"
)

// H264Depacketizer handles depacketization of H.264 (AVC) RTP streams
// Based on RFC 6184
type H264Depacketizer struct {
	fragments         [][]byte
	lastSeq           uint16
	fuInProgress      bool      // Whether FU-A assembly is in progress (avoids seq 0 sentinel)
	fragmentStartTime time.Time // Track when fragment assembly started
	fragmentTimeout   time.Duration
	mu                sync.Mutex // Protects fragments and lastSeq
}

// NAL unit type constants for H.264
const (
	// Single NAL unit packet - types 1-23
	nalTypeSTAPA  = 24 // Single-time aggregation packet type A
	nalTypeSTAPB  = 25 // Single-time aggregation packet type B
	nalTypeMTAP16 = 26 // Multi-time aggregation packet
	nalTypeMTAP24 = 27 // Multi-time aggregation packet
	nalTypeFUA    = 28 // Fragmentation unit A
	nalTypeFUB    = 29 // Fragmentation unit B
)

// Depacketize processes an RTP packet and returns complete NAL units
func (d *H264Depacketizer) Depacketize(packet *rtp.Packet) ([][]byte, error) {
	// Extract payload and sequence number from packet
	payload := packet.Payload
	sequenceNumber := packet.SequenceNumber
	if len(payload) < 1 {
		return nil, errors.New("payload too short")
	}

	// Parse NAL unit header (1 byte for H.264)
	nalHeader := payload[0]

	// RFC 6184 Section 1.3: forbidden_zero_bit must be 0
	if nalHeader&0x80 != 0 {
		return nil, errors.New("H.264 forbidden_zero_bit is set")
	}

	nalType := nalHeader & 0x1F

	var nalUnits [][]byte

	switch nalType {
	case 0, 30, 31:
		// Reserved NAL types
		return nil, fmt.Errorf("reserved NAL type: %d", nalType)

	case 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23:
		// Single NAL unit packet
		nalUnit := d.prependStartCode(payload)
		nalUnits = append(nalUnits, nalUnit)

	case nalTypeSTAPA:
		// STAP-A - Single-time aggregation packet
		var err error
		nalUnits, err = d.handleSTAPA(payload) // Pass full payload, handleSTAPA handles offset
		if err != nil {
			return nil, fmt.Errorf("failed to handle STAP-A: %w", err)
		}

	case nalTypeFUA:
		// FU-A - Fragmentation unit
		// Lock for fragment handling
		d.mu.Lock()
		nalUnit, complete := d.handleFUA(payload, sequenceNumber)
		d.lastSeq = sequenceNumber
		d.mu.Unlock()

		if complete && nalUnit != nil {
			nalUnits = append(nalUnits, nalUnit)
		}

	case nalTypeSTAPB, nalTypeMTAP16, nalTypeMTAP24, nalTypeFUB:
		// These are less commonly used and not implemented here
		return nil, fmt.Errorf("unsupported NAL type: %d", nalType)

	default:
		return nil, fmt.Errorf("unknown NAL type: %d", nalType)
	}

	// Update last sequence number for non-FUA packets
	if nalType != nalTypeFUA {
		d.mu.Lock()
		d.lastSeq = sequenceNumber
		d.mu.Unlock()
	}

	return nalUnits, nil
}

// handleSTAPA processes a STAP-A (Single-Time Aggregation Packet type A)
func (d *H264Depacketizer) handleSTAPA(payload []byte) ([][]byte, error) {
	var nalUnits [][]byte
	offset := 1 // Skip STAP-A NAL header byte

	// Limit number of NAL units to prevent DoS
	maxNALUnits := 100 // Reasonable limit for aggregated packets

	for offset < len(payload) && len(nalUnits) < maxNALUnits {
		if offset+2 > len(payload) {
			// Not enough data for size field
			return nalUnits, fmt.Errorf("STAP-A truncated at offset %d: need 2 bytes for size, have %d",
				offset, len(payload)-offset)
		}

		// Read NAL unit size (2 bytes, network byte order)
		nalSize := binary.BigEndian.Uint16(payload[offset : offset+2])
		offset += 2

		if nalSize == 0 {
			return nalUnits, fmt.Errorf("STAP-A contains zero-size NAL unit at offset %d", offset-2)
		}

		if int(nalSize) > security.MaxNALUnitSize {
			return nalUnits, fmt.Errorf(security.ErrMsgNALUnitTooLarge, nalSize, security.MaxNALUnitSize)
		}

		if offset+int(nalSize) > len(payload) {
			return nalUnits, fmt.Errorf("STAP-A NAL unit out of bounds: offset=%d, nalSize=%d, available=%d",
				offset, nalSize, len(payload)-offset)
		}

		// Extract NAL unit and prepend start code
		nalUnit := d.prependStartCode(payload[offset : offset+int(nalSize)])
		nalUnits = append(nalUnits, nalUnit)

		offset += int(nalSize)
	}

	if len(nalUnits) >= maxNALUnits {
		return nalUnits, fmt.Errorf("too many NAL units in STAP-A packet: %d", len(nalUnits))
	}

	return nalUnits, nil
}

// handleFUA processes a FU-A (Fragmentation Unit type A)
// IMPORTANT: This method must be called with d.mu lock held
func (d *H264Depacketizer) handleFUA(payload []byte, sequenceNumber uint16) ([]byte, bool) {
	if len(payload) < 2 {
		return nil, false
	}

	// FU indicator (same as NAL header for FU-A)
	fuIndicator := payload[0]

	// FU header
	fuHeader := payload[1]
	startBit := (fuHeader & 0x80) != 0
	endBit := (fuHeader & 0x40) != 0

	nalType := fuHeader & 0x1F

	// RFC 6184 Section 5.8: S and E bits MUST NOT both be set to one
	// This is a protocol violation - treat as single-fragment NAL unit
	if startBit && endBit {
		// Reconstruct as single NAL unit despite protocol violation
		reconstructedHeader := (fuIndicator & 0xE0) | nalType
		nalUnit := make([]byte, 0, 5+len(payload)-2)
		nalUnit = append(nalUnit, 0x00, 0x00, 0x00, 0x01, reconstructedHeader)
		nalUnit = append(nalUnit, payload[2:]...)
		d.lastSeq = sequenceNumber
		return nalUnit, true
	}

	// FU payload starts at byte 2
	if len(payload) <= 2 {
		// No payload data
		return nil, false
	}
	fuPayload := payload[2:]

	// Check fragment size limit
	if len(fuPayload) > security.MaxFragmentSize {
		// Fragment too large, reset and skip
		d.fragments = nil
		d.fragmentStartTime = time.Time{}
		return nil, false
	}

	// Check for fragment timeout before processing
	if d.fragmentTimeout > 0 && !d.fragmentStartTime.IsZero() && time.Since(d.fragmentStartTime) > d.fragmentTimeout {
		// Fragment assembly timed out, reset
		d.fragments = [][]byte{}
		d.fragmentStartTime = time.Time{}
	}

	if startBit {
		// Start of a new fragmented NAL unit
		d.fragments = [][]byte{}
		d.fragmentStartTime = time.Now()
		d.fuInProgress = true

		// Reconstruct NAL unit header
		nalHeader := (fuIndicator & 0xE0) | nalType

		// Add start code and reconstructed NAL header
		startCodeAndHeader := []byte{0x00, 0x00, 0x00, 0x01, nalHeader}
		d.fragments = append(d.fragments, startCodeAndHeader)
	}

	// Check for packet loss with sequence number wraparound handling
	// Use fuInProgress flag instead of lastSeq != 0 since seq 0 is valid (RFC 3550)
	if d.fuInProgress && !startBit && len(d.fragments) > 0 {
		// Calculate sequence number distance with proper wraparound handling
		// Using the serial number arithmetic from RFC 1982
		gap := d.sequenceDistance(sequenceNumber, d.lastSeq)

		// During fragmentation, we expect consecutive packets
		// gap = 1: Normal sequential packet
		// gap = 0: Duplicate packet (should not happen in fragments)
		// gap > 1: Missing packets
		// gap < 0: Reordering (packets arrived out of order)

		if gap == 0 {
			// Duplicate packet — silently ignore
			return nil, false
		}
		if gap != 1 {
			// Any gap during FU-A reassembly means missing data.
			// Tolerating gaps would produce corrupt NAL units with holes.
			// Reset fragment assembly and discard.
			d.fragments = [][]byte{}
			d.fragmentStartTime = time.Time{}
			return nil, false
		}
	}

	// Add fragment payload with size check
	if len(d.fragments) > 0 {
		// Calculate current total size
		currentSize := 0
		for _, frag := range d.fragments {
			currentSize += len(frag)
		}

		// Check if adding this fragment would exceed limits
		if currentSize+len(fuPayload) > security.MaxNALUnitSize {
			// Fragment accumulation too large, reset
			d.fragments = [][]byte{}
			d.fragmentStartTime = time.Time{}
			return nil, false
		}

		fragment := make([]byte, len(fuPayload))
		copy(fragment, fuPayload)
		d.fragments = append(d.fragments, fragment)

		// Limit number of fragments to prevent DoS
		if len(d.fragments) > 1000 {
			// Too many fragments, reset
			d.fragments = [][]byte{}
			d.fragmentStartTime = time.Time{}
			return nil, false
		}
	}

	if endBit && len(d.fragments) > 0 {
		// End of fragmented NAL unit, combine all fragments
		totalSize := 0
		for _, frag := range d.fragments {
			totalSize += len(frag)
		}

		// Final size check
		if totalSize > security.MaxNALUnitSize {
			// Assembled NAL unit too large
			d.fragments = [][]byte{}
			d.fragmentStartTime = time.Time{}
			return nil, false
		}

		nalUnit := make([]byte, 0, totalSize)
		for _, frag := range d.fragments {
			nalUnit = append(nalUnit, frag...)
		}

		d.fragments = [][]byte{}
		d.fragmentStartTime = time.Time{}
		d.fuInProgress = false
		// Update last sequence number after successful reassembly
		d.lastSeq = sequenceNumber
		return nalUnit, true
	}

	// Update last sequence number for fragment tracking
	d.lastSeq = sequenceNumber
	return nil, false
}

// prependStartCode adds the H.264 start code (0x00 0x00 0x00 0x01) to a NAL unit
func (d *H264Depacketizer) prependStartCode(nalUnit []byte) []byte {
	startCode := []byte{0x00, 0x00, 0x00, 0x01}
	result := make([]byte, len(startCode)+len(nalUnit))
	copy(result, startCode)
	copy(result[len(startCode):], nalUnit)
	return result
}

// sequenceDistance calculates the distance between two RTP sequence numbers
// handling 16-bit wraparound according to RFC 1982 serial number arithmetic.
// Returns positive if s1 is ahead of s2, negative if behind.
func (d *H264Depacketizer) sequenceDistance(s1, s2 uint16) int {
	// Cast to signed 16-bit to handle wraparound
	diff := int16(s1 - s2)
	return int(diff)
}

// Reset clears the depacketizer state
func (d *H264Depacketizer) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fragments = [][]byte{}
	d.fuInProgress = false
	d.fragmentStartTime = time.Time{}
}

// GetNALType extracts the NAL unit type from a NAL header
func GetNALType(nalHeader byte) byte {
	return nalHeader & 0x1F
}

// IsKeyFrame checks if the NAL unit type indicates a key frame
func IsKeyFrame(nalType byte) bool {
	// Only NAL unit type 5 (IDR slice) is a key frame.
	// SPS (7) is a parameter set, not a frame.
	return nalType == 5
}

// H264DepacketizerWithMemory extends H264Depacketizer with memory management
type H264DepacketizerWithMemory struct {
	H264Depacketizer
	streamID      string
	memController *memory.Controller
	memoryLimit   int64
	currentUsage  int64
}

// NewH264DepacketizerWithMemory creates a memory-aware H264 depacketizer
func NewH264DepacketizerWithMemory(streamID string, memController *memory.Controller, limit int64) Depacketizer {
	return &H264DepacketizerWithMemory{
		H264Depacketizer: H264Depacketizer{
			fragments:       [][]byte{},
			fragmentTimeout: 5 * time.Second, // Default 5 second timeout for fragment assembly
		},
		streamID:      streamID,
		memController: memController,
		memoryLimit:   limit,
	}
}

// Depacketize processes an RTP packet with memory management
func (d *H264DepacketizerWithMemory) Depacketize(packet *rtp.Packet) ([][]byte, error) {
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
	nalUnits, err := d.H264Depacketizer.Depacketize(packet)

	// If we got complete NAL units, release fragment memory
	if len(nalUnits) > 0 {
		// Calculate actual memory used
		actualSize := int64(0)
		for _, unit := range nalUnits {
			actualSize += int64(len(unit))
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

	return nalUnits, err
}

// Reset clears the depacketizer state and releases memory
func (d *H264DepacketizerWithMemory) Reset() {
	// Release any held memory
	if d.currentUsage > 0 {
		d.memController.ReleaseMemory(d.streamID, d.currentUsage)
		d.currentUsage = 0
	}

	d.H264Depacketizer.Reset()
}
