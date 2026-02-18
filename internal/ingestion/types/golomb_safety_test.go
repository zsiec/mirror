package types

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExponentialGolombSafety(t *testing.T) {
	t.Run("ReadUE with excessive leading zeros", func(t *testing.T) {
		// Create data with 32 leading zeros (should fail at 31)
		data := make([]byte, 5)
		// First 4 bytes are all zeros (32 bits)
		data[4] = 0x80 // Stop bit at position 32

		br := NewBitReader(data)
		_, err := br.ReadUE()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many leading zeros")
	})

	t.Run("ReadUE with maximum safe value", func(t *testing.T) {
		// Test with 20 leading zeros (should succeed)
		// This gives us a value around 2^20 = ~1M
		bw := NewBitWriter()
		// Write 20 zeros
		for i := 0; i < 20; i++ {
			bw.WriteBit(0)
		}
		// Write stop bit
		bw.WriteBit(1)
		// Write a moderate value (not all 1s)
		for i := 0; i < 20; i++ {
			if i < 10 {
				bw.WriteBit(1)
			} else {
				bw.WriteBit(0)
			}
		}

		br := NewBitReader(bw.GetBytes())
		val, err := br.ReadUE()
		assert.NoError(t, err)
		assert.Greater(t, val, uint32(0))
		assert.Less(t, val, uint32(100000000)) // Should be under our limit
	})

	t.Run("ReadUE large value is valid per spec", func(t *testing.T) {
		// Values up to 2^32-2 are valid per H.264 spec.
		// Using 27 leading zeros gives us base = 2^27 = 134M which is valid.
		bw := NewBitWriter()
		for i := 0; i < 27; i++ {
			bw.WriteBit(0)
		}
		// Write stop bit
		bw.WriteBit(1)
		// Write value bits (all zeros = base - 1 = 134217727)
		for i := 0; i < 27; i++ {
			bw.WriteBit(0)
		}

		br := NewBitReader(bw.GetBytes())
		val, err := br.ReadUE()
		assert.NoError(t, err)
		assert.Equal(t, uint32(134217727), val)
	})

	t.Run("ReadSE overflow protection", func(t *testing.T) {
		// Create data that would cause SE overflow
		// For SE, we need a UE value > 0xFFFFFFFE
		// Just create a simple value that's clearly too large
		data := []byte{0x00, 0x00, 0x00, 0x00, 0x00} // Many zeros will hit our limit

		br := NewBitReader(data)
		_, err := br.ReadSE()
		assert.Error(t, err)
		// Should fail either on UE limit or SE overflow
	})

	t.Run("ReadBits bounds checking", func(t *testing.T) {
		data := []byte{0xFF, 0xFF}
		br := NewBitReader(data)

		// Try to read more bits than available
		_, err := br.ReadBits(17)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "insufficient bits")

		// Try to read negative bits
		_, err = br.ReadBits(-1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid number of bits")

		// Try to read more than 32 bits
		_, err = br.ReadBits(33)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must be 0-32")
	})

	t.Run("ReadUE with truncated data", func(t *testing.T) {
		// Create data that ends mid-value
		data := []byte{0x00} // 8 zeros, no stop bit

		br := NewBitReader(data)
		_, err := br.ReadUE()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read bit")
	})

	t.Run("Valid exponential Golomb sequences", func(t *testing.T) {
		testCases := []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255}

		for _, value := range testCases {
			t.Run(fmt.Sprintf("value_%d", value), func(t *testing.T) {
				// Write the value
				bw := NewBitWriter()
				bw.WriteUE(value)

				// Read it back
				br := NewBitReader(bw.GetBytes())
				val, err := br.ReadUE()
				require.NoError(t, err)
				assert.Equal(t, value, val)
			})
		}
	})

	t.Run("Signed exponential Golomb round trip", func(t *testing.T) {
		testValues := []int32{0, 1, -1, 2, -2, 100, -100, 1000, -1000}

		for _, expected := range testValues {
			// Convert to unsigned
			var ue uint32
			if expected <= 0 {
				ue = uint32(-expected * 2)
			} else {
				ue = uint32(expected*2 - 1)
			}

			// Write and read back
			bw := NewBitWriter()
			bw.WriteUE(ue)

			br := NewBitReader(bw.GetBytes())
			val, err := br.ReadSE()
			require.NoError(t, err)
			assert.Equal(t, expected, val)
		}
	})
}

func TestGolombPerformance(t *testing.T) {
	// Test that the safety checks don't significantly impact performance
	// for typical H.264 values

	data := make([]byte, 1000)
	// Fill with typical exponential Golomb values
	bw := NewBitWriter()
	for i := 0; i < 100; i++ {
		bw.WriteUE(uint32(i % 100))
	}
	copy(data, bw.GetBytes())

	br := NewBitReader(data)

	// Read values back
	count := 0
	for count < 100 {
		val, err := br.ReadUE()
		if err != nil {
			break
		}
		assert.Less(t, val, uint32(100))
		count++
	}

	assert.Equal(t, 100, count)
}

func BenchmarkReadUE(b *testing.B) {
	// Prepare test data with various exponential Golomb values
	bw := NewBitWriter()
	values := []uint32{0, 1, 2, 3, 7, 15, 31, 63, 127, 255}
	for i := 0; i < 100; i++ {
		bw.WriteUE(values[i%len(values)])
	}
	data := bw.GetBytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br := NewBitReader(data)
		for j := 0; j < 100; j++ {
			_, _ = br.ReadUE()
		}
	}
}
