package types

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestCleanNALHeader(t *testing.T) {
	tests := []struct {
		name     string
		input    byte
		expected byte
	}{
		{
			name:     "forbidden bit set",
			input:    0x88, // 10001000 - forbidden=1, NRI=0, type=8 (PPS)
			expected: 0x08, // 00001000 - forbidden=0, NRI=0, type=8 (PPS)
		},
		{
			name:     "valid header unchanged",
			input:    0x65, // 01100101 - forbidden=0, NRI=3, type=5 (IDR)
			expected: 0x65, // unchanged
		},
		{
			name:     "forbidden bit with high NRI",
			input:    0xE7, // 11100111 - forbidden=1, NRI=3, type=7 (SPS)
			expected: 0x67, // 01100111 - forbidden=0, NRI=3, type=7 (SPS)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanNALHeader(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCleanNALUnitData(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "clean PPS with forbidden bit",
			input:    []byte{0x88, 0xDE, 0x58, 0xB8}, // PPS with forbidden bit set
			expected: []byte{0x08, 0xDE, 0x58, 0xB8}, // PPS with forbidden bit cleared
		},
		{
			name:     "valid SPS unchanged",
			input:    []byte{0x67, 0x42, 0x00, 0x1E}, // Valid SPS
			expected: []byte{0x67, 0x42, 0x00, 0x1E}, // Unchanged
		},
		{
			name:     "empty data",
			input:    []byte{},
			expected: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanNALUnitData(tt.input)
			assert.Equal(t, tt.expected, result)

			// Verify original is not modified
			if len(tt.input) > 0 {
				assert.NotSame(t, &tt.input[0], &result[0], "should create a copy")
			}
		})
	}
}

func TestIsValidNALHeader(t *testing.T) {
	tests := []struct {
		name     string
		header   byte
		expected bool
	}{
		{
			name:     "valid SPS",
			header:   0x67, // forbidden=0, NRI=3, type=7
			expected: true,
		},
		{
			name:     "forbidden bit set",
			header:   0x88, // forbidden=1, NRI=0, type=8
			expected: false,
		},
		{
			name:     "NAL type 0",
			header:   0x60, // forbidden=0, NRI=3, type=0
			expected: false,
		},
		{
			name:     "valid IDR",
			header:   0x65, // forbidden=0, NRI=3, type=5
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidNALHeader(tt.header)
			assert.Equal(t, tt.expected, result)
		})
	}
}
