package security

import (
	"errors"
	"io"
)

// LEB128 decoding errors
var (
	ErrLEB128Overflow     = errors.New("LEB128 value overflows uint64")
	ErrLEB128TooManyBytes = errors.New("LEB128 encoding exceeds maximum allowed bytes")
	ErrLEB128Incomplete   = errors.New("LEB128 encoding is incomplete")
)

// ReadLEB128 reads an unsigned LEB128 value from a byte slice
// Returns the value, number of bytes read, and any error
func ReadLEB128(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, ErrLEB128Incomplete
	}

	var value uint64
	var shift uint
	var bytesRead int

	for bytesRead < len(data) && bytesRead < MaxLEB128Bytes {
		b := data[bytesRead]
		bytesRead++

		// Check for overflow before shifting
		if shift >= 64 {
			return 0, bytesRead, ErrLEB128Overflow
		}

		// Extract lower 7 bits
		lowBits := uint64(b & 0x7F)

		// Check if adding these bits would overflow
		// For the last possible byte (shift=63), we need special handling
		if shift == 63 && lowBits > 1 {
			return 0, bytesRead, ErrLEB128Overflow
		} else if shift > 63 {
			// Any non-zero bits would overflow
			if lowBits != 0 {
				return 0, bytesRead, ErrLEB128Overflow
			}
		}

		// Add the bits to our value
		value |= lowBits << shift
		shift += 7

		// Check if this is the last byte (high bit not set)
		if (b & 0x80) == 0 {
			return value, bytesRead, nil
		}
	}

	// If we've read MaxLEB128Bytes and still have continuation bit set
	if bytesRead >= MaxLEB128Bytes {
		return 0, bytesRead, ErrLEB128TooManyBytes
	}

	// Incomplete LEB128 encoding
	return 0, bytesRead, ErrLEB128Incomplete
}

// ReadLEB128FromReader reads an unsigned LEB128 value from an io.Reader
func ReadLEB128FromReader(r io.Reader) (uint64, int, error) {
	var value uint64
	var shift uint
	var bytesRead int
	buf := make([]byte, 1)

	for bytesRead < MaxLEB128Bytes {
		n, err := r.Read(buf)
		if err != nil {
			if err == io.EOF && bytesRead > 0 {
				return 0, bytesRead, ErrLEB128Incomplete
			}
			return 0, bytesRead, err
		}
		if n == 0 {
			return 0, bytesRead, ErrLEB128Incomplete
		}

		b := buf[0]
		bytesRead++

		// Check for overflow before shifting
		if shift >= 64 {
			return 0, bytesRead, ErrLEB128Overflow
		}

		// Extract lower 7 bits
		lowBits := uint64(b & 0x7F)

		// Check if adding these bits would overflow
		// For the last possible byte (shift=63), we need special handling
		if shift == 63 && lowBits > 1 {
			return 0, bytesRead, ErrLEB128Overflow
		} else if shift > 63 {
			// Any non-zero bits would overflow
			if lowBits != 0 {
				return 0, bytesRead, ErrLEB128Overflow
			}
		}

		// Add the bits to our value
		value |= lowBits << shift
		shift += 7

		// Check if this is the last byte (high bit not set)
		if (b & 0x80) == 0 {
			return value, bytesRead, nil
		}
	}

	// Too many bytes
	return 0, bytesRead, ErrLEB128TooManyBytes
}

// WriteLEB128 encodes a uint64 value as LEB128
func WriteLEB128(value uint64) []byte {
	var result []byte

	for {
		b := byte(value & 0x7F)
		value >>= 7

		if value != 0 {
			// More bytes follow
			b |= 0x80
		}

		result = append(result, b)

		if value == 0 {
			break
		}
	}

	return result
}

// LEB128Size returns the number of bytes needed to encode a value
func LEB128Size(value uint64) int {
	size := 1
	for value >>= 7; value != 0; value >>= 7 {
		size++
	}
	return size
}
