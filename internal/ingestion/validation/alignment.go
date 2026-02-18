package validation

import (
	"fmt"
)

// AlignmentValidator provides validation for protocol-specific alignment requirements
type AlignmentValidator struct {
	// Buffer for accumulating partial MPEG-TS packets across SRT message boundaries
	partialPacketBuffer []byte
	// Track if we're currently accumulating a partial packet
	hasPartialPacket bool
	// Statistics for monitoring
	alignmentErrors  int64
	packetsProcessed int64
	partialPackets   int64
}

// NewAlignmentValidator creates a new alignment validator
func NewAlignmentValidator() *AlignmentValidator {
	return &AlignmentValidator{
		partialPacketBuffer: make([]byte, 0, 188), // Pre-allocate for one TS packet
	}
}

// ValidateMPEGTSAlignment checks if data contains properly aligned MPEG-TS packets
// Returns the number of complete packets and any remaining partial data
func (v *AlignmentValidator) ValidateMPEGTSAlignment(data []byte) (int, []byte, error) {
	const tsPacketSize = 188

	// Handle empty data
	if len(data) == 0 {
		return 0, nil, nil
	}

	// Calculate complete packets
	completePackets := len(data) / tsPacketSize
	remainder := len(data) % tsPacketSize

	// Validate sync bytes for complete packets
	for i := 0; i < completePackets; i++ {
		offset := i * tsPacketSize
		if data[offset] != 0x47 {
			v.alignmentErrors++
			return i, data[offset:], fmt.Errorf("sync byte missing at packet %d (offset %d): got 0x%02X", i, offset, data[offset])
		}
	}

	v.packetsProcessed += int64(completePackets)

	// Return remainder if any
	if remainder > 0 {
		v.partialPackets++
		return completePackets, data[len(data)-remainder:], nil
	}

	return completePackets, nil, nil
}

// ProcessWithAlignment handles data that may contain partial MPEG-TS packets across boundaries
// It accumulates partial packets and returns only complete, aligned packets
func (v *AlignmentValidator) ProcessWithAlignment(data []byte) ([]byte, error) {
	const tsPacketSize = 188

	// If we have a partial packet from before, prepend it
	var processData []byte
	if v.hasPartialPacket && len(v.partialPacketBuffer) > 0 {
		// Combine partial packet with new data
		processData = make([]byte, len(v.partialPacketBuffer)+len(data))
		copy(processData, v.partialPacketBuffer)
		copy(processData[len(v.partialPacketBuffer):], data)
		v.partialPacketBuffer = v.partialPacketBuffer[:0] // Reset buffer
		v.hasPartialPacket = false
	} else {
		processData = data
	}

	// Find first sync byte if not at start
	syncOffset := 0
	if len(processData) > 0 && processData[0] != 0x47 {
		syncOffset = v.findSyncByte(processData)
		if syncOffset < 0 {
			// No sync byte found, save all data as partial
			v.partialPacketBuffer = append(v.partialPacketBuffer, processData...)
			v.hasPartialPacket = true
			v.alignmentErrors++
			return nil, fmt.Errorf("no sync byte found in %d bytes", len(processData))
		}
		// Skip to sync byte
		processData = processData[syncOffset:]
	}

	// Calculate complete packets
	completeLength := (len(processData) / tsPacketSize) * tsPacketSize

	// Save any remainder
	if completeLength < len(processData) {
		remainder := processData[completeLength:]
		v.partialPacketBuffer = append(v.partialPacketBuffer[:0], remainder...)
		v.hasPartialPacket = true
	}

	// Return only complete packets
	if completeLength > 0 {
		return processData[:completeLength], nil
	}

	return nil, nil
}

// GetPartialPacket returns any accumulated partial packet data
func (v *AlignmentValidator) GetPartialPacket() []byte {
	if v.hasPartialPacket {
		return v.partialPacketBuffer
	}
	return nil
}

// HasPartialPacket returns true if there's a partial packet waiting
func (v *AlignmentValidator) HasPartialPacket() bool {
	return v.hasPartialPacket
}

// findSyncByte searches for the MPEG-TS sync byte (0x47) in the data
func (v *AlignmentValidator) findSyncByte(data []byte) int {
	for i := 0; i < len(data); i++ {
		if data[i] == 0x47 {
			// Verify it's likely a real sync byte by checking if there's another one 188 bytes later
			if i+188 < len(data) && data[i+188] == 0x47 {
				return i
			}
			// If we can't verify, assume it's valid
			if i+188 >= len(data) {
				return i
			}
		}
	}
	return -1
}

// GetStats returns alignment statistics
func (v *AlignmentValidator) GetStats() AlignmentStats {
	return AlignmentStats{
		AlignmentErrors:  v.alignmentErrors,
		PacketsProcessed: v.packetsProcessed,
		PartialPackets:   v.partialPackets,
	}
}

// Reset clears the validator state
func (v *AlignmentValidator) Reset() {
	v.partialPacketBuffer = v.partialPacketBuffer[:0]
	v.hasPartialPacket = false
	v.alignmentErrors = 0
	v.packetsProcessed = 0
	v.partialPackets = 0
}

// AlignmentStats contains alignment validation statistics
type AlignmentStats struct {
	AlignmentErrors  int64
	PacketsProcessed int64
	PartialPackets   int64
}
