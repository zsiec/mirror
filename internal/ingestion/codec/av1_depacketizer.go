package codec

import (
	"errors"
	"fmt"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/memory"
)

// AV1Depacketizer handles depacketization of AV1 RTP streams
// Based on draft-ietf-payload-av1 (RTP Payload Format for AV1)
type AV1Depacketizer struct {
	fragments       [][]byte
	lastSeq         uint16
	fragmentedOBU   bool
	obuType         uint8
	temporalUnitBuf [][]byte
}

// OBU (Open Bitstream Unit) type constants
const (
	obuSequenceHeader       = 1
	obuTemporalDelimiter    = 2
	obuFrameHeader          = 3
	obuTileGroup            = 4
	obuMetadata             = 5
	obuFrame                = 6
	obuRedundantFrameHeader = 7
	obuTileList             = 8
	obuPadding              = 15
)

// AV1 aggregation header constants
const (
	av1AggregationHeaderSize = 1
	av1PayloadHeaderSize     = 1
)

// Depacketize processes an RTP packet and returns complete OBUs
func (d *AV1Depacketizer) Depacketize(packet *rtp.Packet) ([][]byte, error) {
	// Extract payload and sequence number from packet
	payload := packet.Payload
	sequenceNumber := packet.SequenceNumber
	if len(payload) < av1PayloadHeaderSize {
		return nil, errors.New("payload too short")
	}

	// Parse AV1 aggregation header (first byte)
	aggHeader := payload[0]
	zBit := (aggHeader & 0x80) != 0   // First OBU fragment indicator
	yBit := (aggHeader & 0x40) != 0   // Last OBU fragment indicator
	wField := (aggHeader & 0x30) >> 4 // Number of OBU elements (0-3)
	nBit := (aggHeader & 0x08) != 0   // New temporal unit indicator

	// Skip aggregation header
	payload = payload[1:]

	// Check for packet loss during fragmentation.
	// Use fragmentedOBU flag instead of lastSeq != 0 — seq 0 is valid per RFC 3550.
	if d.fragmentedOBU && sequenceNumber != d.lastSeq+1 {
		// Packet loss detected, discard fragments
		d.fragments = [][]byte{}
		d.fragmentedOBU = false
		// After packet loss, ignore fragments until we get a new first fragment
		if !zBit {
			d.lastSeq = sequenceNumber
			return [][]byte{}, nil
		}
	}
	d.lastSeq = sequenceNumber

	var obus [][]byte

	// Handle new temporal unit
	if nBit && len(d.temporalUnitBuf) > 0 {
		// Flush previous temporal unit
		obus = append(obus, d.temporalUnitBuf...)
		d.temporalUnitBuf = [][]byte{}
	}

	// Process based on W field (number of OBU elements)
	switch wField {
	case 0:
		// Single OBU element (may be fragmented)
		obu, err := d.processSingleOBU(payload, zBit, yBit)
		if err != nil {
			return nil, err
		}
		if obu != nil {
			d.temporalUnitBuf = append(d.temporalUnitBuf, obu)
		}

	case 1, 2, 3:
		// Multiple OBU elements
		obuList, err := d.processMultipleOBUs(payload, int(wField))
		if err != nil {
			return nil, err
		}
		d.temporalUnitBuf = append(d.temporalUnitBuf, obuList...)
	}

	// If this is a complete temporal unit (next packet will have N bit set)
	// we can return the buffered OBUs
	if !d.fragmentedOBU && len(d.temporalUnitBuf) > 0 {
		// Check if we have a temporal delimiter
		for _, obu := range d.temporalUnitBuf {
			if len(obu) > 0 {
				obuType, _ := d.parseOBUHeader(obu)
				if obuType == obuTemporalDelimiter {
					// Found temporal delimiter, flush buffer
					obus = append(obus, d.temporalUnitBuf...)
					d.temporalUnitBuf = [][]byte{}
					break
				}
			}
		}
	}

	return obus, nil
}

// processSingleOBU handles a single OBU element (which may be fragmented)
func (d *AV1Depacketizer) processSingleOBU(payload []byte, zBit, yBit bool) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("empty OBU payload")
	}

	if zBit {
		// First fragment of OBU
		d.fragments = [][]byte{}
		d.fragmentedOBU = true

		// Parse OBU header to get type
		d.obuType, _ = d.parseOBUHeader(payload)
	} else if !d.fragmentedOBU {
		// Non-fragmented packet received when not expecting fragments
		return nil, errors.New("unexpected non-first fragment")
	}

	// Add fragment
	fragment := make([]byte, len(payload))
	copy(fragment, payload)
	d.fragments = append(d.fragments, fragment)

	if yBit {
		// Last fragment, combine all fragments
		totalSize := 0
		for _, frag := range d.fragments {
			totalSize += len(frag)
		}

		obu := make([]byte, 0, totalSize)
		for _, frag := range d.fragments {
			obu = append(obu, frag...)
		}

		d.fragments = [][]byte{}
		d.fragmentedOBU = false
		return obu, nil
	}

	// Middle fragment, continue collecting
	return nil, nil
}

// processMultipleOBUs handles multiple OBU elements in a single packet
func (d *AV1Depacketizer) processMultipleOBUs(payload []byte, count int) ([][]byte, error) {
	var obus [][]byte
	offset := 0

	// Each OBU is preceded by a length field
	for i := 0; i < count && offset < len(payload); i++ {
		// Parse LEB128 encoded length
		length, bytesRead, err := d.parseLEB128(payload[offset:])
		if err != nil {
			return nil, fmt.Errorf("failed to parse OBU length: %w", err)
		}
		offset += bytesRead

		if offset+int(length) > len(payload) {
			return nil, fmt.Errorf("OBU length exceeds payload size")
		}

		// Extract OBU
		obu := make([]byte, length)
		copy(obu, payload[offset:offset+int(length)])
		obus = append(obus, obu)

		offset += int(length)
	}

	return obus, nil
}

// parseOBUHeader parses the OBU header and returns the OBU type
func (d *AV1Depacketizer) parseOBUHeader(data []byte) (uint8, error) {
	if len(data) < 1 {
		return 0, errors.New("insufficient data for OBU header")
	}

	header := data[0]

	// OBU header format:
	// forbidden_bit (1) | type (4) | extension_flag (1) | has_size_field (1) | reserved (1)
	if (header & 0x80) != 0 {
		return 0, errors.New("forbidden bit set in OBU header")
	}

	obuType := (header >> 3) & 0x0F
	return obuType, nil
}

// parseLEB128 parses a LEB128 (Little Endian Base 128) encoded integer
func (d *AV1Depacketizer) parseLEB128(data []byte) (uint64, int, error) {
	var value uint64
	var bytesRead int

	for i := 0; i < 8 && i < len(data); i++ {
		b := data[i]
		value |= uint64(b&0x7F) << (i * 7)
		bytesRead++

		if (b & 0x80) == 0 {
			// Most significant bit is 0, this is the last byte
			return value, bytesRead, nil
		}
	}

	return 0, 0, errors.New("LEB128 value too large or incomplete")
}

// Reset clears the depacketizer state
func (d *AV1Depacketizer) Reset() {
	d.fragments = [][]byte{}
	d.temporalUnitBuf = [][]byte{}
	d.lastSeq = 0
	d.fragmentedOBU = false
	d.obuType = 0
}

// GetOBUTypeName returns a human-readable name for an OBU type
func GetOBUTypeName(obuType uint8) string {
	switch obuType {
	case obuSequenceHeader:
		return "SequenceHeader"
	case obuTemporalDelimiter:
		return "TemporalDelimiter"
	case obuFrameHeader:
		return "FrameHeader"
	case obuTileGroup:
		return "TileGroup"
	case obuMetadata:
		return "Metadata"
	case obuFrame:
		return "Frame"
	case obuRedundantFrameHeader:
		return "RedundantFrameHeader"
	case obuTileList:
		return "TileList"
	case obuPadding:
		return "Padding"
	default:
		return fmt.Sprintf("Unknown(%d)", obuType)
	}
}

// AV1DepacketizerWithMemory extends AV1Depacketizer with memory management
type AV1DepacketizerWithMemory struct {
	AV1Depacketizer
	streamID      string
	memController *memory.Controller
	memoryLimit   int64
	currentUsage  int64
}

// NewAV1DepacketizerWithMemory creates a memory-aware AV1 depacketizer
func NewAV1DepacketizerWithMemory(streamID string, memController *memory.Controller, limit int64) Depacketizer {
	return &AV1DepacketizerWithMemory{
		AV1Depacketizer: AV1Depacketizer{
			fragments: [][]byte{},
		},
		streamID:      streamID,
		memController: memController,
		memoryLimit:   limit,
	}
}

// Depacketize processes an RTP packet with memory management
func (d *AV1DepacketizerWithMemory) Depacketize(packet *rtp.Packet) ([][]byte, error) {
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
	obus, err := d.AV1Depacketizer.Depacketize(packet)

	// If we got complete OBUs, release fragment memory
	if len(obus) > 0 {
		// Calculate actual memory used
		actualSize := int64(0)
		for _, obu := range obus {
			actualSize += int64(len(obu))
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

	return obus, err
}

// Reset clears the depacketizer state and releases memory
func (d *AV1DepacketizerWithMemory) Reset() {
	// Release any held memory
	if d.currentUsage > 0 {
		d.memController.ReleaseMemory(d.streamID, d.currentUsage)
		d.currentUsage = 0
	}

	d.AV1Depacketizer.Reset()
}
