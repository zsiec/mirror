package resolution

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBitReader_NewBitReader tests BitReader creation
func TestBitReader_NewBitReader(t *testing.T) {
	data := []byte{0xAB, 0xCD, 0xEF}
	br := NewBitReader(data)
	
	assert.NotNil(t, br)
	assert.Equal(t, data, br.data)
	assert.Equal(t, 0, br.bitPos)
	assert.Equal(t, 0, br.bytePos)
}

// TestBitReader_ReadBit tests single bit reading
func TestBitReader_ReadBit(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected []uint32
		wantErr  bool
	}{
		{
			name:     "read bits from 0xAB (10101011)",
			data:     []byte{0xAB},
			expected: []uint32{1, 0, 1, 0, 1, 0, 1, 1},
			wantErr:  false,
		},
		{
			name:     "read bits from 0xFF (11111111)",
			data:     []byte{0xFF},
			expected: []uint32{1, 1, 1, 1, 1, 1, 1, 1},
			wantErr:  false,
		},
		{
			name:     "read bits from 0x00 (00000000)",
			data:     []byte{0x00},
			expected: []uint32{0, 0, 0, 0, 0, 0, 0, 0},
			wantErr:  false,
		},
		{
			name:    "empty data",
			data:    []byte{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			
			for i, expectedBit := range tt.expected {
				bit, err := br.ReadBit()
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				
				require.NoError(t, err, "bit %d", i)
				assert.Equal(t, expectedBit, bit, "bit %d", i)
			}
			
			// Should error when trying to read past end
			_, err := br.ReadBit()
			assert.Error(t, err)
		})
	}
}

// TestBitReader_ReadBits tests multiple bit reading
func TestBitReader_ReadBits(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		n        int
		expected uint32
		wantErr  bool
	}{
		{
			name:     "read 4 bits from 0xAB",
			data:     []byte{0xAB}, // 10101011
			n:        4,
			expected: 0xA, // 1010
			wantErr:  false,
		},
		{
			name:     "read 8 bits from 0xAB",
			data:     []byte{0xAB},
			n:        8,
			expected: 0xAB,
			wantErr:  false,
		},
		{
			name:     "read 16 bits from 0xABCD",
			data:     []byte{0xAB, 0xCD},
			n:        16,
			expected: 0xABCD,
			wantErr:  false,
		},
		{
			name:     "read 0 bits",
			data:     []byte{0xAB},
			n:        0,
			expected: 0,
			wantErr:  false,
		},
		{
			name:    "read too many bits (33)",
			data:    []byte{0xAB, 0xCD, 0xEF, 0x01, 0x23},
			n:       33,
			wantErr: true,
		},
		{
			name:    "read negative bits",
			data:    []byte{0xAB},
			n:       -1,
			wantErr: true,
		},
		{
			name:    "read past end of data",
			data:    []byte{0xAB},
			n:       16,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			result, err := br.ReadBits(tt.n)
			
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBitReader_ReadUE tests unsigned exponential Golomb reading
func TestBitReader_ReadUE(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected uint32
		wantErr  bool
	}{
		{
			name:     "UE value 0 (bit pattern: 1)",
			data:     []byte{0x80}, // 10000000
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "UE value 1 (bit pattern: 010)",
			data:     []byte{0x40}, // 01000000
			expected: 1,
			wantErr:  false,
		},
		{
			name:     "UE value 2 (bit pattern: 011)",
			data:     []byte{0x60}, // 01100000
			expected: 2,
			wantErr:  false,
		},
		{
			name:     "UE value 3 (bit pattern: 00100)",
			data:     []byte{0x20}, // 00100000
			expected: 3,
			wantErr:  false,
		},
		{
			name:     "UE value 4 (bit pattern: 00101)",
			data:     []byte{0x28}, // 00101000
			expected: 4,
			wantErr:  false,
		},
		{
			name:     "UE value 7 (bit pattern: 0001000)",
			data:     []byte{0x10}, // 00010000 - this is actually UE(7) with trailing 0s
			expected: 7,
			wantErr:  false,
		},
		{
			name:    "invalid UE (all zeros)",
			data:    []byte{0x00, 0x00, 0x00, 0x00, 0x00},
			wantErr: true,
		},
		{
			name:    "incomplete UE",
			data:    []byte{0x01}, // 00000001 - single bit, but needs more for suffix
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			result, err := br.ReadUE()
			
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBitReader_ReadSE tests signed exponential Golomb reading
func TestBitReader_ReadSE(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected int32
		wantErr  bool
	}{
		{
			name:     "SE value 0 (UE 0)",
			data:     []byte{0x80}, // 10000000
			expected: 0,
			wantErr:  false,
		},
		{
			name:     "SE value 1 (UE 1)",
			data:     []byte{0x40}, // 01000000
			expected: 1,
			wantErr:  false,
		},
		{
			name:     "SE value -1 (UE 2)",
			data:     []byte{0x60}, // 01100000
			expected: -1,
			wantErr:  false,
		},
		{
			name:     "SE value 2 (UE 3)",
			data:     []byte{0x20}, // 00100000
			expected: 2,
			wantErr:  false,
		},
		{
			name:     "SE value -2 (UE 4)",
			data:     []byte{0x28}, // 00101000
			expected: -2,
			wantErr:  false,
		},
		{
			name:    "invalid SE",
			data:    []byte{0x00},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			result, err := br.ReadSE()
			
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBitReader_SkipBits tests bit skipping
func TestBitReader_SkipBits(t *testing.T) {
	data := []byte{0xAB, 0xCD} // 10101011 11001101
	br := NewBitReader(data)
	
	// Skip first 4 bits
	err := br.SkipBits(4)
	require.NoError(t, err)
	
	// Read next 4 bits (should be 1011)
	bits, err := br.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, uint32(0xB), bits)
	
	// Skip too many bits
	err = br.SkipBits(20)
	assert.Error(t, err)
}

// TestBitReader_BytesRemaining tests remaining bytes calculation
func TestBitReader_BytesRemaining(t *testing.T) {
	data := []byte{0xAB, 0xCD, 0xEF}
	br := NewBitReader(data)
	
	// Initially 3 bytes remaining
	assert.Equal(t, 3, br.BytesRemaining())
	
	// Read 4 bits (still in first byte)
	_, err := br.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, 2, br.BytesRemaining()) // First byte partially consumed
	
	// Read 4 more bits (complete first byte)
	_, err = br.ReadBits(4)
	require.NoError(t, err)
	assert.Equal(t, 2, br.BytesRemaining()) // Two complete bytes left
	
	// Read another byte
	_, err = br.ReadBits(8)
	require.NoError(t, err)
	assert.Equal(t, 1, br.BytesRemaining())
}

// TestBitReader_IsAligned tests byte alignment checking
func TestBitReader_IsAligned(t *testing.T) {
	data := []byte{0xAB, 0xCD}
	br := NewBitReader(data)
	
	// Initially aligned
	assert.True(t, br.IsAligned())
	
	// Read 1 bit - not aligned
	_, err := br.ReadBit()
	require.NoError(t, err)
	assert.False(t, br.IsAligned())
	
	// Read 7 more bits - aligned again
	_, err = br.ReadBits(7)
	require.NoError(t, err)
	assert.True(t, br.IsAligned())
}

// TestBitReader_AlignToByte tests byte alignment
func TestBitReader_AlignToByte(t *testing.T) {
	data := []byte{0xAB, 0xCD, 0xEF}
	br := NewBitReader(data)
	
	// Read 3 bits
	_, err := br.ReadBits(3)
	require.NoError(t, err)
	assert.False(t, br.IsAligned())
	
	// Align to byte
	br.AlignToByte()
	assert.True(t, br.IsAligned())
	
	// Should be at start of second byte
	bits, err := br.ReadBits(8)
	require.NoError(t, err)
	assert.Equal(t, uint32(0xCD), bits)
}

// TestBitReader_ComplexSequence tests a complex sequence of operations
func TestBitReader_ComplexSequence(t *testing.T) {
	// Create data with carefully planned bit sequences
	// 0x40 = 01000000 (UE value 1: '010')
	// 0x80 = 10000000 (UE value 0: '1')
	data := []byte{0x40, 0x80}
	br := NewBitReader(data)
	
	// Read first UE (uses 3 bits: 010)
	ue1, err := br.ReadUE()
	require.NoError(t, err)
	assert.Equal(t, uint32(1), ue1)
	
	// Read next 5 bits (remaining 5 bits from first byte)
	bits, err := br.ReadBits(5)
	require.NoError(t, err)
	assert.Equal(t, uint32(0x0), bits) // 00000 from 0x40
	
	// Read SE (uses 1 bit from second byte: '1' which is UE 0, so SE 0)
	se, err := br.ReadSE()
	require.NoError(t, err)
	assert.Equal(t, int32(0), se)
}

// TestBitReader_EdgeCases tests edge cases and error conditions
func TestBitReader_EdgeCases(t *testing.T) {
	t.Run("empty data", func(t *testing.T) {
		br := NewBitReader([]byte{})
		
		_, err := br.ReadBit()
		assert.Error(t, err)
		
		_, err = br.ReadBits(1)
		assert.Error(t, err)
		
		_, err = br.ReadUE()
		assert.Error(t, err)
		
		_, err = br.ReadSE()
		assert.Error(t, err)
	})
	
	t.Run("single byte boundary", func(t *testing.T) {
		br := NewBitReader([]byte{0xFF})
		
		// Read all 8 bits
		for i := 0; i < 8; i++ {
			bit, err := br.ReadBit()
			require.NoError(t, err)
			assert.Equal(t, uint32(1), bit)
		}
		
		// Next read should fail
		_, err := br.ReadBit()
		assert.Error(t, err)
	})
	
	t.Run("large UE value", func(t *testing.T) {
		// Create data with many leading zeros (should fail)
		data := make([]byte, 10) // All zeros
		br := NewBitReader(data)
		
		_, err := br.ReadUE()
		assert.Error(t, err, "should fail with too many leading zeros")
	})
}

// BenchmarkBitReader_ReadBit benchmarks single bit reading
func BenchmarkBitReader_ReadBit(b *testing.B) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = 0xAA // Alternating pattern
	}
	
	br := NewBitReader(data)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if br.bytePos >= len(data) {
			br = NewBitReader(data) // Reset for next iteration
		}
		br.ReadBit()
	}
}

// BenchmarkBitReader_ReadUE benchmarks UE reading
func BenchmarkBitReader_ReadUE(b *testing.B) {
	// Create data with UE values
	data := make([]byte, 1000)
	for i := range data {
		data[i] = 0x80 // UE value 0
	}
	
	br := NewBitReader(data)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if br.bytePos >= len(data) {
			br = NewBitReader(data) // Reset for next iteration
		}
		br.ReadUE()
	}
}