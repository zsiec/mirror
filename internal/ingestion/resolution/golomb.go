package resolution

import (
	"fmt"
)

// BitReader provides bit-level reading operations for video header parsing
type BitReader struct {
	data    []byte
	bitPos  int // Current bit position (0-7 within current byte)
	bytePos int // Current byte position
}

// NewBitReader creates a new bit reader
func NewBitReader(data []byte) *BitReader {
	return &BitReader{
		data:    data,
		bitPos:  0,
		bytePos: 0,
	}
}

// ReadBit reads a single bit
func (br *BitReader) ReadBit() (uint32, error) {
	if br.bytePos >= len(br.data) {
		return 0, fmt.Errorf("end of data")
	}

	bit := (br.data[br.bytePos] >> (7 - br.bitPos)) & 1
	br.bitPos++

	if br.bitPos == 8 {
		br.bitPos = 0
		br.bytePos++
	}

	return uint32(bit), nil
}

// ReadBits reads multiple bits (up to 32)
func (br *BitReader) ReadBits(n int) (uint32, error) {
	if n > 32 || n < 0 {
		return 0, fmt.Errorf("invalid bit count: %d", n)
	}

	// Pre-check that enough bits remain before reading
	totalBitsAvailable := (len(br.data)-br.bytePos)*8 - br.bitPos
	if totalBitsAvailable < n {
		return 0, fmt.Errorf("not enough bits: need %d, have %d", n, totalBitsAvailable)
	}

	var result uint32
	for i := 0; i < n; i++ {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, err
		}
		result = (result << 1) | bit
	}

	return result, nil
}

// ReadUE reads an unsigned exponential Golomb coded value
func (br *BitReader) ReadUE() (uint32, error) {
	// Count leading zeros
	leadingZeros := 0
	for {
		bit, err := br.ReadBit()
		if err != nil {
			return 0, err
		}
		if bit == 1 {
			break
		}
		leadingZeros++

		// Prevent infinite loops on malformed data
		if leadingZeros > 31 {
			return 0, fmt.Errorf("too many leading zeros in UE")
		}
	}

	// Read the remaining bits
	if leadingZeros == 0 {
		return 0, nil
	}

	suffix, err := br.ReadBits(leadingZeros)
	if err != nil {
		return 0, err
	}

	return (1 << leadingZeros) - 1 + suffix, nil
}

// ReadSE reads a signed exponential Golomb coded value
func (br *BitReader) ReadSE() (int32, error) {
	ue, err := br.ReadUE()
	if err != nil {
		return 0, err
	}

	// Convert unsigned to signed
	if ue%2 == 0 {
		return -int32(ue / 2), nil
	}
	return int32((ue + 1) / 2), nil
}

// SkipBits skips the specified number of bits
func (br *BitReader) SkipBits(n int) error {
	for i := 0; i < n; i++ {
		_, err := br.ReadBit()
		if err != nil {
			return err
		}
	}
	return nil
}

// BytesRemaining returns the number of bytes remaining
func (br *BitReader) BytesRemaining() int {
	remaining := len(br.data) - br.bytePos
	if br.bitPos > 0 {
		remaining-- // Current byte is partially consumed
	}
	return remaining
}

// IsAligned returns true if the reader is byte-aligned
func (br *BitReader) IsAligned() bool {
	return br.bitPos == 0
}

// AlignToByte skips bits to align to the next byte boundary
func (br *BitReader) AlignToByte() {
	if br.bitPos != 0 {
		br.bitPos = 0
		br.bytePos++
	}
}
