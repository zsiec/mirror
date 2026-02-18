package types

import (
	"bytes"
	"fmt"
)

// RBSP (Raw Byte Sequence Payload) handling for H.264
// Implements emulation prevention byte removal and insertion

// RemoveEmulationPreventionBytes removes 0x03 emulation prevention bytes from NAL unit data
// This converts RBSP (Raw Byte Sequence Payload) to SODB (String of Data Bits)
func RemoveEmulationPreventionBytes(data []byte) ([]byte, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("empty NAL unit data")
	}

	// Allocate output buffer (worst case is same size as input)
	output := make([]byte, 0, len(data))

	// State machine for detecting 0x00 0x00 0x03 pattern
	zeroCount := 0

	for i := 0; i < len(data); i++ {
		b := data[i]

		if zeroCount >= 2 && b == 0x03 {
			// This is an emulation prevention byte
			// Check if we have more data after it
			if i+1 < len(data) {
				next := data[i+1]
				// Valid emulation prevention: 0x00 0x00 0x03 followed by 0x00, 0x01, 0x02, or 0x03
				if next <= 0x03 {
					// Skip the 0x03 byte
					zeroCount = 0
					continue
				}
			} else {
				// 0x03 at end of data after two zeros — this IS a valid emulation
				// prevention byte per H.264 Section 7.4.1. At end-of-NAL, the RBSP
				// stop bit and zero-padding follow, so stripping is correct.
				zeroCount = 0
				continue
			}
		}

		// Update zero count
		if b == 0x00 {
			zeroCount++
		} else {
			zeroCount = 0
		}

		output = append(output, b)
	}

	return output, nil
}

// AddEmulationPreventionBytes adds 0x03 emulation prevention bytes where needed
// This converts SODB back to RBSP for transmission
func AddEmulationPreventionBytes(data []byte) []byte {
	if len(data) < 1 {
		return data
	}

	// Allocate output buffer (worst case is 50% larger)
	output := make([]byte, 0, len(data)*3/2)

	// State machine for detecting patterns that need emulation prevention
	zeroCount := 0

	for i := 0; i < len(data); i++ {
		b := data[i]

		// Check if we need to insert emulation prevention byte
		if zeroCount >= 2 && b <= 0x03 {
			// We have at least two consecutive zeros followed by 0x00/0x01/0x02/0x03
			// Need to insert 0x03 before this byte
			output = append(output, 0x03)

			// After inserting 0x03, the zero sequence is broken
			// If current byte is 0x00, new count is 1, otherwise 0
			if b == 0x00 {
				zeroCount = 1
			} else {
				zeroCount = 0
			}
		} else {
			// Normal counting
			if b == 0x00 {
				zeroCount++
			} else {
				zeroCount = 0
			}
		}

		output = append(output, b)
	}

	return output
}

// ValidateRBSP checks if data contains valid RBSP (no emulation prevention needed)
func ValidateRBSP(data []byte) error {
	zeroCount := 0

	for i := 0; i < len(data); i++ {
		b := data[i]

		if zeroCount >= 2 && b <= 0x03 {
			// Found pattern that would need emulation prevention
			return fmt.Errorf("invalid RBSP: found 0x00 0x00 0x%02x pattern at offset %d", b, i-2)
		}

		if b == 0x00 {
			zeroCount++
		} else {
			zeroCount = 0
		}
	}

	return nil
}

// ExtractRBSPFromNALUnit extracts RBSP data from a complete NAL unit
// Handles NAL unit header and trailing bytes
func ExtractRBSPFromNALUnit(nalUnit []byte) ([]byte, error) {
	if len(nalUnit) < 1 {
		return nil, fmt.Errorf("NAL unit too short")
	}

	// Skip NAL unit header (first byte)
	rbspData := nalUnit[1:]

	// Handle empty payload case
	if len(rbspData) == 0 {
		return []byte{}, nil
	}

	// Remove emulation prevention bytes
	sodb, err := RemoveEmulationPreventionBytes(rbspData)
	if err != nil {
		return nil, fmt.Errorf("failed to remove emulation prevention bytes: %w", err)
	}

	// NOTE: Do NOT call removeTrailingBytes here. RBSP trailing bits are part of the
	// valid bitstream and must be preserved for downstream parsers (SPS/PPS/slice headers).
	// Removing them corrupts the data by stripping legitimate payload bytes.
	// Per H.264 Section 7.4.1, the RBSP stop bit and alignment zero bits are only
	// relevant at the RBSP boundary and are handled by the bit-level parser.

	return sodb, nil
}

// ExtractRBSPFromPayload extracts RBSP data from NAL unit payload (no header)
// This is used when the NAL header has already been stripped
func ExtractRBSPFromPayload(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return []byte{}, nil
	}

	// Remove emulation prevention bytes
	sodb, err := RemoveEmulationPreventionBytes(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to remove emulation prevention bytes: %w", err)
	}

	// NOTE: Do NOT call removeTrailingBytes here — see ExtractRBSPFromNALUnit comment.

	return sodb, nil
}

// removeTrailingBytes removes the stop bit and any trailing zero bytes
func removeTrailingBytes(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	// Find the last non-zero byte
	lastNonZero := len(data) - 1
	for lastNonZero >= 0 && data[lastNonZero] == 0x00 {
		lastNonZero--
	}

	if lastNonZero < 0 {
		// All zeros
		return []byte{}
	}

	// The last non-zero byte should contain the stop bit (0x80)
	// We need to remove it and any padding bits
	lastByte := data[lastNonZero]

	// Find the position of the stop bit (highest 1 bit)
	stopBitPos := 7
	for stopBitPos >= 0 && (lastByte&(1<<stopBitPos)) == 0 {
		stopBitPos--
	}

	if stopBitPos < 0 {
		// No stop bit found, return data as is
		return data[:lastNonZero+1]
	}

	// If stop bit is at bit 7, we can remove this byte entirely
	if stopBitPos == 7 {
		return data[:lastNonZero]
	}

	// Otherwise, keep the byte but note that it has padding
	return data[:lastNonZero+1]
}

// CompareRBSP compares two RBSP data arrays for equality
func CompareRBSP(a, b []byte) bool {
	// Handle empty arrays
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	// Remove emulation prevention from both
	aClean, err := RemoveEmulationPreventionBytes(a)
	if err != nil {
		return false
	}

	bClean, err := RemoveEmulationPreventionBytes(b)
	if err != nil {
		return false
	}

	return bytes.Equal(aClean, bClean)
}

// RBSPBitReader wraps BitReader to handle RBSP data
type RBSPBitReader struct {
	*BitReader
	originalData []byte
	cleanData    []byte
}

// NewRBSPBitReader creates a new RBSP-aware bit reader
func NewRBSPBitReader(rbspData []byte) (*RBSPBitReader, error) {
	// Remove emulation prevention bytes
	cleanData, err := RemoveEmulationPreventionBytes(rbspData)
	if err != nil {
		return nil, err
	}

	return &RBSPBitReader{
		BitReader:    NewBitReader(cleanData),
		originalData: rbspData,
		cleanData:    cleanData,
	}, nil
}

// GetCleanData returns the RBSP data with emulation prevention bytes removed
func (r *RBSPBitReader) GetCleanData() []byte {
	return r.cleanData
}
