package codec

import (
	"errors"
	"fmt"
	"sync"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/ingestion/security"
)

// AV1Depacketizer handles depacketization of AV1 RTP streams
// Based on draft-ietf-payload-av1 (RTP Payload Format for AV1)
type AV1Depacketizer struct {
	fragments       [][]byte
	lastSeq         uint16
	fragmentedOBU   bool
	obuType         uint8
	temporalUnitBuf [][]byte
	mu              sync.Mutex
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
	d.mu.Lock()
	defer d.mu.Unlock()

	// Extract payload and sequence number from packet
	payload := packet.Payload
	sequenceNumber := packet.SequenceNumber
	if len(payload) < av1PayloadHeaderSize {
		return nil, errors.New("payload too short")
	}

	// Parse AV1 aggregation header (first byte)
	aggHeader := payload[0]
	// Z=1 means "continuation from previous packet" (this is NOT the first fragment)
	zBit := (aggHeader & 0x80) != 0
	// Y=1 means "will continue in next packet" (this is NOT the last fragment)
	yBit := (aggHeader & 0x40) != 0
	wField := (aggHeader & 0x30) >> 4 // Number of OBU elements (0-3)
	nBit := (aggHeader & 0x08) != 0   // New temporal unit indicator

	// Skip aggregation header
	payload = payload[1:]

	// Check for packet loss during fragmentation.
	if d.fragmentedOBU && sequenceNumber != d.lastSeq+1 {
		// Packet loss detected, discard fragments
		d.fragments = [][]byte{}
		d.fragmentedOBU = false
		// After packet loss, ignore continuation fragments until we get a new first fragment
		if zBit {
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

	// Handle fragmentation state
	if zBit {
		// Continuation fragment (not-first)
		if !d.fragmentedOBU || len(d.fragments) == 0 {
			// Received continuation without start, discard
			return obus, nil
		}

		// Check fragment accumulation size limit
		currentSize := 0
		for _, frag := range d.fragments {
			currentSize += len(frag)
		}
		if currentSize+len(payload) > security.MaxNALUnitSize {
			d.fragments = [][]byte{}
			d.fragmentedOBU = false
			return obus, fmt.Errorf("AV1 fragment accumulation exceeds size limit")
		}

		fragment := make([]byte, len(payload))
		copy(fragment, payload)
		d.fragments = append(d.fragments, fragment)

		if !yBit {
			// Last fragment (Y=0 means "does not continue"), assemble complete OBU
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
			d.temporalUnitBuf = append(d.temporalUnitBuf, obu)
		}
		// If yBit is set, more fragments coming, continue collecting
	} else if yBit && !zBit {
		// First fragment of a fragmented OBU (Z=0, Y=1)
		d.fragments = [][]byte{}
		d.fragmentedOBU = true
		d.obuType, _ = d.parseOBUHeader(payload)

		fragment := make([]byte, len(payload))
		copy(fragment, payload)
		d.fragments = append(d.fragments, fragment)
	} else if !zBit && !yBit {
		// Non-fragmented packet: complete OBU element(s)
		if wField == 0 {
			// W=0: variable number of OBU elements, each prefixed with LEB128 length
			obuList, err := d.processLEB128PrefixedOBUs(payload)
			if err != nil {
				return obus, err
			}
			d.temporalUnitBuf = append(d.temporalUnitBuf, obuList...)
		} else {
			// Multiple OBU elements (W=1,2,3)
			// Per spec: first W-1 OBUs have LEB128 length prefix, last extends to end
			obuList, err := d.processMultipleOBUs(payload, int(wField))
			if err != nil {
				return obus, err
			}
			d.temporalUnitBuf = append(d.temporalUnitBuf, obuList...)
		}
	}

	// If this is a complete temporal unit (not fragmented) and we have buffered OBUs
	if !d.fragmentedOBU && len(d.temporalUnitBuf) > 0 {
		// Check if we have a temporal delimiter — it marks the START of a new TU
		for i, obu := range d.temporalUnitBuf {
			if len(obu) > 0 {
				obuType, _ := d.parseOBUHeader(obu)
				if obuType == obuTemporalDelimiter && i > 0 {
					// Flush OBUs before the TD (they belong to the previous TU)
					obus = append(obus, d.temporalUnitBuf[:i]...)
					// Start new buffer with the TD and everything after
					remaining := make([][]byte, len(d.temporalUnitBuf[i:]))
					copy(remaining, d.temporalUnitBuf[i:])
					d.temporalUnitBuf = remaining
					break
				}
			}
		}
	}

	return obus, nil
}

// processLEB128PrefixedOBUs handles W=0 case: variable number of LEB128-prefixed OBU elements
func (d *AV1Depacketizer) processLEB128PrefixedOBUs(payload []byte) ([][]byte, error) {
	var obus [][]byte
	offset := 0

	for offset < len(payload) {
		// Parse LEB128 encoded length
		length, bytesRead, err := security.ReadLEB128(payload[offset:])
		if err != nil {
			return nil, fmt.Errorf("failed to parse OBU length at offset %d: %w", offset, err)
		}
		offset += bytesRead

		// Validate length fits in remaining payload
		if length > uint64(len(payload)-offset) {
			return nil, fmt.Errorf("OBU length %d exceeds remaining payload size %d", length, len(payload)-offset)
		}

		if length > 0 {
			obu := make([]byte, length)
			copy(obu, payload[offset:offset+int(length)])
			obus = append(obus, obu)
			offset += int(length)
		}
	}

	return obus, nil
}

// processMultipleOBUs handles multiple OBU elements in a single packet
// Per AV1 RTP spec: the first W-1 OBUs have LEB128 length prefix,
// the last OBU extends to end of payload (no length prefix)
func (d *AV1Depacketizer) processMultipleOBUs(payload []byte, count int) ([][]byte, error) {
	var obus [][]byte
	offset := 0

	// First W-1 OBUs have length prefix
	for i := 0; i < count-1 && offset < len(payload); i++ {
		// Parse LEB128 encoded length using security-hardened parser
		length, bytesRead, err := security.ReadLEB128(payload[offset:])
		if err != nil {
			return nil, fmt.Errorf("failed to parse OBU length: %w", err)
		}
		offset += bytesRead

		// Validate length fits in remaining payload before int cast
		if length > uint64(len(payload)-offset) {
			return nil, fmt.Errorf("OBU length %d exceeds remaining payload size %d", length, len(payload)-offset)
		}

		// Extract OBU
		obu := make([]byte, length)
		copy(obu, payload[offset:offset+int(length)])
		obus = append(obus, obu)

		offset += int(length)
	}

	// Last OBU extends to end of payload (no length prefix)
	if offset < len(payload) {
		remaining := len(payload) - offset
		obu := make([]byte, remaining)
		copy(obu, payload[offset:])
		obus = append(obus, obu)
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

	// Release memory on error
	if err != nil {
		d.memController.ReleaseMemory(d.streamID, estimatedSize)
		d.currentUsage -= estimatedSize
		return nil, err
	}

	// If we got complete OBUs, release all accumulated memory minus actual output size
	if len(obus) > 0 {
		actualSize := int64(0)
		for _, obu := range obus {
			actualSize += int64(len(obu))
		}

		// Release the difference between what we requested and what we're actually using
		if d.currentUsage > actualSize {
			d.memController.ReleaseMemory(d.streamID, d.currentUsage-actualSize)
		}
		d.currentUsage = 0
	}

	return obus, nil
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
