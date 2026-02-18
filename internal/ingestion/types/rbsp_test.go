package types

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveEmulationPreventionBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
		wantErr  bool
	}{
		{
			name:     "No emulation prevention bytes",
			input:    []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68},
			expected: []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68},
		},
		{
			name:     "Single emulation prevention byte",
			input:    []byte{0x00, 0x00, 0x03, 0x00, 0x01},
			expected: []byte{0x00, 0x00, 0x00, 0x01},
		},
		{
			name:     "Multiple emulation prevention bytes",
			input:    []byte{0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
			expected: []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x02},
		},
		{
			name:     "Emulation prevention at end",
			input:    []byte{0x00, 0x00, 0x03},
			expected: []byte{0x00, 0x00},
		},
		{
			name:     "No emulation prevention for 0x00 0x00 0x03 0x04",
			input:    []byte{0x00, 0x00, 0x03, 0x04},
			expected: []byte{0x00, 0x00, 0x03, 0x04}, // 0x03 is NOT removed
		},
		{
			name:     "Complex pattern",
			input:    []byte{0x00, 0x00, 0x03, 0x00, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x03},
			expected: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x03},
		},
		{
			name:     "Empty input",
			input:    []byte{},
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "Real SPS data with emulation prevention",
			input:    []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1, 0x00, 0x00, 0x03, 0x00, 0x01},
			expected: []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1, 0x00, 0x00, 0x00, 0x01},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RemoveEmulationPreventionBytes(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestAddEmulationPreventionBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "No emulation prevention needed",
			input:    []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68},
			expected: []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68},
		},
		{
			name:     "Add single emulation prevention byte",
			input:    []byte{0x00, 0x00, 0x00, 0x01},
			expected: []byte{0x00, 0x00, 0x03, 0x00, 0x01},
		},
		{
			name:     "Add multiple emulation prevention bytes",
			input:    []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x02},
			expected: []byte{0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
		},
		{
			name:     "Pattern at end",
			input:    []byte{0x00, 0x00, 0x00},
			expected: []byte{0x00, 0x00, 0x03, 0x00},
		},
		{
			name:     "No prevention for 0x00 0x00 0x04",
			input:    []byte{0x00, 0x00, 0x04},
			expected: []byte{0x00, 0x00, 0x04},
		},
		{
			name:     "Empty input",
			input:    []byte{},
			expected: []byte{},
		},
		{
			name:     "Complex pattern with multiple sequences",
			input:    []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x03},
			expected: []byte{0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x00, 0x03, 0x03},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AddEmulationPreventionBytes(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoundTripConversion(t *testing.T) {
	// Test that removing and then adding emulation prevention bytes gives correct result
	testCases := [][]byte{
		{0x00, 0x00, 0x00, 0x01},
		{0x00, 0x00, 0x01, 0x00, 0x00, 0x02},
		{0x00, 0x00, 0x03},
		{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1},
		{0xff, 0x00, 0x00, 0x00, 0xff, 0x00, 0x00, 0x01},
	}

	for i, original := range testCases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			// Add emulation prevention
			withPrevention := AddEmulationPreventionBytes(original)

			// Remove emulation prevention
			result, err := RemoveEmulationPreventionBytes(withPrevention)
			require.NoError(t, err)

			// Should match original
			assert.Equal(t, original, result)
		})
	}
}

func TestValidateRBSP(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "Valid RBSP",
			input:   []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68},
			wantErr: false,
		},
		{
			name:    "Invalid - contains 0x00 0x00 0x00",
			input:   []byte{0x00, 0x00, 0x00, 0x01},
			wantErr: true,
			errMsg:  "0x00 0x00 0x00",
		},
		{
			name:    "Invalid - contains 0x00 0x00 0x01",
			input:   []byte{0x00, 0x00, 0x01},
			wantErr: true,
			errMsg:  "0x00 0x00 0x01",
		},
		{
			name:    "Invalid - contains 0x00 0x00 0x02",
			input:   []byte{0x00, 0x00, 0x02},
			wantErr: true,
			errMsg:  "0x00 0x00 0x02",
		},
		{
			name:    "Invalid - contains 0x00 0x00 0x03",
			input:   []byte{0x00, 0x00, 0x03},
			wantErr: true,
			errMsg:  "0x00 0x00 0x03",
		},
		{
			name:    "Valid - contains 0x00 0x00 0x04",
			input:   []byte{0x00, 0x00, 0x04},
			wantErr: false,
		},
		{
			name:    "Empty input",
			input:   []byte{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRBSP(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractRBSPFromNALUnit(t *testing.T) {
	tests := []struct {
		name     string
		nalUnit  []byte
		expected []byte
		wantErr  bool
	}{
		{
			name:     "SPS NAL unit",
			nalUnit:  []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68},
			expected: []byte{0x42, 0x00, 0x1e, 0x8d, 0x68},
		},
		{
			name:     "PPS NAL unit with emulation prevention",
			nalUnit:  []byte{0x68, 0x00, 0x00, 0x03, 0x00, 0x01},
			expected: []byte{0x00, 0x00, 0x00, 0x01},
		},
		{
			name: "NAL unit with trailing zeros",
			// After removing NAL header (0x67) and emulation prevention bytes,
			// the trailing bytes (0x80, 0x00, 0x00) are preserved — they are
			// legitimate RBSP data, not to be stripped by RBSP extraction.
			nalUnit:  []byte{0x67, 0x42, 0x00, 0x1e, 0x80, 0x00, 0x00},
			expected: []byte{0x42, 0x00, 0x1e, 0x80, 0x00, 0x00},
		},
		{
			name:     "Empty NAL unit",
			nalUnit:  []byte{},
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "Single byte NAL unit",
			nalUnit:  []byte{0x67},
			expected: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractRBSPFromNALUnit(tt.nalUnit)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestCompareRBSP(t *testing.T) {
	tests := []struct {
		name     string
		a        []byte
		b        []byte
		expected bool
	}{
		{
			name:     "Identical data",
			a:        []byte{0x00, 0x00, 0x01},
			b:        []byte{0x00, 0x00, 0x01},
			expected: true,
		},
		{
			name:     "Same data with/without emulation prevention",
			a:        []byte{0x00, 0x00, 0x03, 0x01},
			b:        []byte{0x00, 0x00, 0x01},
			expected: true,
		},
		{
			name:     "Different data",
			a:        []byte{0x00, 0x00, 0x01},
			b:        []byte{0x00, 0x00, 0x02},
			expected: false,
		},
		{
			name:     "Both have emulation prevention",
			a:        []byte{0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
			b:        []byte{0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
			expected: true,
		},
		{
			name:     "Empty arrays",
			a:        []byte{},
			b:        []byte{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CompareRBSP(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRBSPBitReader(t *testing.T) {
	tests := []struct {
		name         string
		rbspData     []byte
		expectedBits []uint8
		expectedUE   []uint32
		wantErr      bool
	}{
		{
			name:         "Basic RBSP reading",
			rbspData:     []byte{0x00, 0x00, 0x03, 0x00, 0x01}, // Will become 0x00, 0x00, 0x00, 0x01
			expectedBits: []uint8{0, 0, 0, 0, 0, 0, 0, 0},      // First 8 bits
			wantErr:      false,
		},
		{
			name:         "RBSP with exponential Golomb",
			rbspData:     []byte{0x40}, // RBSP data starting with 010... (0x40 = 01000000)
			expectedBits: []uint8{},    // No bits to read individually
			expectedUE:   []uint32{1},  // ReadUE: 010 = value 1 (1 zero, then 10 = 2-1 = 1)
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, err := NewRBSPBitReader(tt.rbspData)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, reader)

			// Read expected bits
			for i, expectedBit := range tt.expectedBits {
				bit, err := reader.ReadBit()
				assert.NoError(t, err, "Failed to read bit %d", i)
				assert.Equal(t, expectedBit, bit, "Bit %d mismatch", i)
			}

			// Read expected UE values
			for i, expectedUE := range tt.expectedUE {
				ue, err := reader.ReadUE()
				assert.NoError(t, err, "Failed to read UE %d", i)
				assert.Equal(t, expectedUE, ue, "UE %d mismatch", i)
			}
		})
	}
}

func TestRemoveTrailingBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "No trailing bytes",
			input:    []byte{0x42, 0x00, 0x1e},
			expected: []byte{0x42, 0x00, 0x1e},
		},
		{
			name:     "Trailing zeros",
			input:    []byte{0x42, 0x00, 0x1e, 0x00, 0x00},
			expected: []byte{0x42, 0x00, 0x1e},
		},
		{
			name:     "Stop bit at byte boundary",
			input:    []byte{0x42, 0x00, 0x1e, 0x80},
			expected: []byte{0x42, 0x00, 0x1e},
		},
		{
			name:     "Stop bit with padding",
			input:    []byte{0x42, 0x00, 0x1e, 0x40}, // Stop bit at bit 6
			expected: []byte{0x42, 0x00, 0x1e, 0x40},
		},
		{
			name:     "All zeros",
			input:    []byte{0x00, 0x00, 0x00},
			expected: []byte{},
		},
		{
			name:     "Empty input",
			input:    []byte{},
			expected: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeTrailingBytes(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func BenchmarkRemoveEmulationPreventionBytes(b *testing.B) {
	// Create test data with multiple emulation prevention bytes
	testData := make([]byte, 1024)
	for i := 0; i < len(testData); i += 4 {
		if i+3 < len(testData) {
			testData[i] = 0x00
			testData[i+1] = 0x00
			testData[i+2] = 0x03
			testData[i+3] = 0x01
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RemoveEmulationPreventionBytes(testData)
	}
}

func BenchmarkAddEmulationPreventionBytes(b *testing.B) {
	// Create test data that will need emulation prevention
	testData := make([]byte, 1024)
	for i := 0; i < len(testData); i += 3 {
		if i+2 < len(testData) {
			testData[i] = 0x00
			testData[i+1] = 0x00
			testData[i+2] = byte(i % 4)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AddEmulationPreventionBytes(testData)
	}
}
