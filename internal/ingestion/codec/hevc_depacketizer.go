package codec

import (
	"errors"
	"fmt"
	"sync"

	"github.com/pion/rtp"

	"github.com/zsiec/mirror/internal/ingestion/memory"
)

// HEVCDepacketizer handles depacketization of HEVC (H.265) RTP streams
// Based on RFC 7798
type HEVCDepacketizer struct {
	fragments [][]byte
	lastSeq   uint16
	mu        sync.Mutex // Protects fragments and lastSeq
}

// NAL unit type constants for HEVC
const (
	// Single NAL unit packet
	nalTypeSingle = 0
	// Aggregation packet
	nalTypeAP = 48
	// Fragmentation unit
	nalTypeFU = 49
)

// Depacketize processes an RTP packet and returns complete NAL units
func (d *HEVCDepacketizer) Depacketize(packet *rtp.Packet) ([][]byte, error) {
	payload := packet.Payload
	sequenceNumber := packet.SequenceNumber
	if len(payload) < 2 {
		return nil, errors.New("payload too short")
	}

	// Parse NAL unit header (2 bytes for HEVC)
	nalHeader := uint16(payload[0])<<8 | uint16(payload[1])
	nalType := (nalHeader >> 9) & 0x3F

	var nalUnits [][]byte

	switch {
	case nalType < nalTypeAP:
		// Single NAL unit packet
		nalUnit := make([]byte, len(payload))
		copy(nalUnit, payload)
		nalUnits = append(nalUnits, nalUnit)

	case nalType == nalTypeAP:
		// Aggregation packet - multiple NAL units in one RTP packet
		nalUnits = d.handleAggregationPacket(payload[2:])

	case nalType == nalTypeFU:
		// Fragmentation unit
		// Lock for fragment handling
		d.mu.Lock()
		nalUnit, complete := d.handleFragmentationUnit(payload, sequenceNumber)
		d.lastSeq = sequenceNumber
		d.mu.Unlock()

		if complete && nalUnit != nil {
			nalUnits = append(nalUnits, nalUnit)
		}

	default:
		return nil, fmt.Errorf("unsupported NAL type: %d", nalType)
	}

	// Update last sequence number for non-FU packets
	if nalType != nalTypeFU {
		d.mu.Lock()
		d.lastSeq = sequenceNumber
		d.mu.Unlock()
	}

	return nalUnits, nil
}

// handleAggregationPacket processes an aggregation packet containing multiple NAL units
func (d *HEVCDepacketizer) handleAggregationPacket(payload []byte) [][]byte {
	var nalUnits [][]byte
	offset := 0

	for offset < len(payload) {
		if offset+2 > len(payload) {
			break
		}

		// Read NAL unit size (2 bytes)
		nalSize := int(payload[offset])<<8 | int(payload[offset+1])
		offset += 2

		if offset+nalSize > len(payload) {
			break
		}

		// Extract NAL unit
		nalUnit := make([]byte, nalSize)
		copy(nalUnit, payload[offset:offset+nalSize])
		nalUnits = append(nalUnits, nalUnit)

		offset += nalSize
	}

	return nalUnits
}

// handleFragmentationUnit processes a fragmentation unit
// IMPORTANT: This method must be called with d.mu lock held
func (d *HEVCDepacketizer) handleFragmentationUnit(payload []byte, sequenceNumber uint16) ([]byte, bool) {
	if len(payload) < 3 {
		return nil, false
	}

	// FU header
	fuHeader := payload[2]
	startBit := (fuHeader & 0x80) != 0
	endBit := (fuHeader & 0x40) != 0
	fuType := fuHeader & 0x3F

	// FU payload starts at byte 3
	fuPayload := payload[3:]

	if startBit {
		// Start of a new fragmented NAL unit
		d.fragments = [][]byte{}

		// Reconstruct NAL unit header
		nalHeader := make([]byte, 2)
		nalHeader[0] = (payload[0] & 0x81) | (fuType << 1)
		nalHeader[1] = payload[1]

		d.fragments = append(d.fragments, nalHeader)
	}

	// Check for packet loss
	if d.lastSeq != 0 && sequenceNumber != d.lastSeq+1 {
		// Packet loss detected, discard fragments
		d.fragments = [][]byte{}
		return nil, false
	}

	// Add fragment payload
	fragment := make([]byte, len(fuPayload))
	copy(fragment, fuPayload)
	d.fragments = append(d.fragments, fragment)

	if endBit {
		// End of fragmented NAL unit, combine all fragments
		totalSize := 0
		for _, frag := range d.fragments {
			totalSize += len(frag)
		}

		nalUnit := make([]byte, 0, totalSize)
		for _, frag := range d.fragments {
			nalUnit = append(nalUnit, frag...)
		}

		d.fragments = [][]byte{}
		return nalUnit, true
	}

	return nil, false
}

// Reset clears the depacketizer state
func (d *HEVCDepacketizer) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fragments = [][]byte{}
	d.lastSeq = 0
}

// HEVCDepacketizerWithMemory extends HEVCDepacketizer with memory management
type HEVCDepacketizerWithMemory struct {
	HEVCDepacketizer
	streamID      string
	memController *memory.Controller
	memoryLimit   int64
	currentUsage  int64
}

// NewHEVCDepacketizerWithMemory creates a memory-aware HEVC depacketizer
func NewHEVCDepacketizerWithMemory(streamID string, memController *memory.Controller, limit int64) Depacketizer {
	return &HEVCDepacketizerWithMemory{
		HEVCDepacketizer: HEVCDepacketizer{
			fragments: [][]byte{},
		},
		streamID:      streamID,
		memController: memController,
		memoryLimit:   limit,
	}
}

// Depacketize processes an RTP packet with memory management
func (d *HEVCDepacketizerWithMemory) Depacketize(packet *rtp.Packet) ([][]byte, error) {
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
	nalUnits, err := d.HEVCDepacketizer.Depacketize(packet)

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
func (d *HEVCDepacketizerWithMemory) Reset() {
	// Release any held memory
	if d.currentUsage > 0 {
		d.memController.ReleaseMemory(d.streamID, d.currentUsage)
		d.currentUsage = 0
	}

	d.HEVCDepacketizer.Reset()
}
