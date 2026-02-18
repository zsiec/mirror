package frame

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestH264Detector_BufferBoundaryEdgeCases(t *testing.T) {
	detector := NewH264Detector()

	tests := []struct {
		name         string
		data         []byte
		wantNALCount int
		wantError    bool
		description  string
	}{
		{
			name:         "empty buffer",
			data:         []byte{},
			wantNALCount: 0,
			wantError:    false,
			description:  "Should handle empty buffer gracefully",
		},
		{
			name:         "buffer too small for any start code",
			data:         []byte{0x00, 0x00},
			wantNALCount: 0,
			wantError:    false,
			description:  "Should handle buffer smaller than minimum start code",
		},
		{
			name:         "exactly 3 bytes - valid 3-byte start code",
			data:         []byte{0x00, 0x00, 0x01},
			wantNALCount: 0,
			wantError:    false,
			description:  "Start code at end of buffer with no NAL data",
		},
		{
			name:         "3-byte start code at buffer end",
			data:         []byte{0x00, 0x00, 0x01, 0x41},
			wantNALCount: 1,
			wantError:    false,
			description:  "Should find NAL unit with data after start code",
		},
		{
			name:         "4-byte start code at exact buffer end",
			data:         []byte{0x00, 0x00, 0x00, 0x01},
			wantNALCount: 0,
			wantError:    false,
			description:  "4-byte start code fills entire buffer",
		},
		{
			name:         "incomplete 4-byte start code at buffer end",
			data:         []byte{0x00, 0x00, 0x01, 0x41, 0x00, 0x00, 0x00},
			wantNALCount: 1,
			wantError:    false,
			description:  "Partial 4-byte pattern should not be mistaken for start code",
		},
		{
			name: "multiple NALs with 3-byte start codes",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, 0x01, // NAL 1
				0x00, 0x00, 0x01, 0x42, 0x02, // NAL 2
			},
			wantNALCount: 2,
			wantError:    false,
			description:  "Should correctly parse multiple NALs with 3-byte start codes",
		},
		{
			name: "multiple NALs with mixed start codes",
			data: []byte{
				0x00, 0x00, 0x00, 0x01, 0x41, 0x01, // NAL 1 (4-byte)
				0x00, 0x00, 0x01, 0x42, 0x02, // NAL 2 (3-byte)
			},
			wantNALCount: 2,
			wantError:    false,
			description:  "Should handle mixed 3-byte and 4-byte start codes",
		},
		{
			name: "NAL at exact buffer boundary",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, 0x01, 0x02, 0x03, // NAL 1
				0x00, 0x00, 0x01, // Start code at exact end
			},
			wantNALCount: 1,
			wantError:    false,
			description:  "Should handle start code at exact buffer end",
		},
		{
			name: "false start code pattern",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, // Real start code + NAL
				0xFF, 0x00, 0x00, 0x01, // False pattern (not at start)
				0x00, 0x00, 0x01, 0x42, // Real start code + NAL
			},
			wantNALCount: 2,
			wantError:    false,
			description:  "Should not be confused by 0x000001 pattern in NAL data",
		},
		{
			name: "single byte NAL unit",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, // NAL 1 (single byte)
				0x00, 0x00, 0x01, 0x42, // NAL 2 (single byte)
			},
			wantNALCount: 2,
			wantError:    false,
			description:  "Should handle single-byte NAL units",
		},
		{
			name: "buffer ends mid-start-code check",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, 0x01, 0x02, // Complete NAL
				0x00, 0x00, // Incomplete potential start code
			},
			wantNALCount: 1,
			wantError:    false,
			description:  "Should handle incomplete start code pattern at buffer end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nalUnits, err := detector.findNALUnits(tt.data)

			if tt.wantError {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}

			assert.Equal(t, tt.wantNALCount, len(nalUnits),
				"%s - got %d NAL units, want %d", tt.description, len(nalUnits), tt.wantNALCount)

			// Verify NAL unit contents are correct
			if len(nalUnits) > 0 {
				for i, nal := range nalUnits {
					assert.NotEmpty(t, nal, "NAL unit %d should not be empty", i)
					// Verify NAL data doesn't include start codes
					assert.NotEqual(t, byte(0), nal[0], "NAL unit %d should not start with 0x00", i)
				}
			}
		})
	}
}

func TestH264Detector_LargeBufferStress(t *testing.T) {
	detector := NewH264Detector()

	// Create a large buffer with many NAL units
	largeBuffer := make([]byte, 0, 1024*1024) // 1MB

	// Add 100 NAL units with varying sizes
	for i := 0; i < 100; i++ {
		// Alternate between 3-byte and 4-byte start codes
		if i%2 == 0 {
			largeBuffer = append(largeBuffer, 0x00, 0x00, 0x01)
		} else {
			largeBuffer = append(largeBuffer, 0x00, 0x00, 0x00, 0x01)
		}

		// Add NAL header
		largeBuffer = append(largeBuffer, byte(0x41+i%5))

		// Add variable length NAL data
		nalSize := 100 + i*10
		for j := 0; j < nalSize; j++ {
			largeBuffer = append(largeBuffer, byte(j&0xFF))
		}
	}

	// Parse the buffer
	nalUnits, err := detector.findNALUnits(largeBuffer)
	require.NoError(t, err, "Should parse large buffer without error")
	assert.Equal(t, 100, len(nalUnits), "Should find all 100 NAL units")

	// Verify each NAL unit
	for i, nal := range nalUnits {
		assert.NotEmpty(t, nal, "NAL unit %d should not be empty", i)
		expectedSize := 1 + 100 + i*10 // header + base size + increment
		assert.Equal(t, expectedSize, len(nal), "NAL unit %d size mismatch", i)
	}
}

func TestH264Detector_MaliciousPatterns(t *testing.T) {
	detector := NewH264Detector()

	tests := []struct {
		name        string
		data        []byte
		description string
	}{
		{
			name: "many consecutive zero bytes",
			data: func() []byte {
				data := make([]byte, 1000)
				// Fill with zeros except for one NAL
				copy(data[500:], []byte{0x00, 0x00, 0x01, 0x41, 0x01})
				return data
			}(),
			description: "Should handle buffers with many consecutive zeros",
		},
		{
			name: "alternating start code patterns",
			data: func() []byte {
				data := make([]byte, 0, 100)
				for i := 0; i < 10; i++ {
					data = append(data, 0x00, 0x00, 0x01, 0x41)
					data = append(data, 0x00, 0x00, 0x00, 0x01, 0x42)
				}
				return data
			}(),
			description: "Should handle rapidly alternating start code types",
		},
		{
			name: "almost start codes",
			data: []byte{
				0x00, 0x00, 0x02, 0x41, // Not a start code (0x02 instead of 0x01)
				0x00, 0x01, 0x01, 0x42, // Not a start code (0x01 at wrong position)
				0x00, 0x00, 0x01, 0x43, // Valid start code
			},
			description: "Should only detect valid start codes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nalUnits, err := detector.findNALUnits(tt.data)
			assert.NoError(t, err, tt.description)

			// Verify parsing completes without panic or hang
			t.Logf("%s: Found %d NAL units", tt.description, len(nalUnits))
		})
	}
}

func TestH264Detector_BoundaryAccessSafety(t *testing.T) {
	detector := NewH264Detector()

	// Test specific boundary conditions that could cause buffer overreads
	tests := []struct {
		name   string
		data   []byte
		verify func(t *testing.T, nalUnits [][]byte, err error)
	}{
		{
			name: "start code at positions that would cause overread",
			data: []byte{0xFF, 0x00, 0x00, 0x01, 0x41}, // Start code at position 1
			verify: func(t *testing.T, nalUnits [][]byte, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(nalUnits))
				assert.Equal(t, []byte{0x41}, nalUnits[0])
			},
		},
		{
			name: "multiple start codes near buffer end",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, // First NAL
				0x00, 0x00, 0x01, // Second start code at buffer end
			},
			verify: func(t *testing.T, nalUnits [][]byte, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(nalUnits))
			},
		},
		{
			name: "4-byte start code detection boundary",
			data: []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x00, 0x00, 0x00}, // Incomplete 4-byte at end
			verify: func(t *testing.T, nalUnits [][]byte, err error) {
				assert.NoError(t, err)
				assert.Equal(t, 1, len(nalUnits))
				assert.Equal(t, []byte{0x41, 0x00, 0x00, 0x00}, nalUnits[0])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run with race detector if available
			done := make(chan bool)
			go func() {
				nalUnits, err := detector.findNALUnits(tt.data)
				tt.verify(t, nalUnits, err)
				done <- true
			}()

			select {
			case <-done:
				// Success
			case <-time.After(1 * time.Second):
				t.Fatal("Test timed out - possible infinite loop")
			}
		})
	}
}
