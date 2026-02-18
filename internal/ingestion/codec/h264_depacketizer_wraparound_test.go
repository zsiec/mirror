package codec

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestH264Depacketizer_SequenceWrapAround(t *testing.T) {
	d := &H264Depacketizer{
		fragments:       [][]byte{},
		fragmentTimeout: 5 * time.Second,
	}

	tests := []struct {
		name           string
		packets        []rtp.Packet
		expectNALUnits int
		expectError    bool
	}{
		{
			name: "Normal sequence",
			packets: []rtp.Packet{
				{
					Header: rtp.Header{
						SequenceNumber: 100,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
				},
				{
					Header: rtp.Header{
						SequenceNumber: 101,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x05, 0x04, 0x05, 0x06}, // FU-A middle
				},
				{
					Header: rtp.Header{
						SequenceNumber: 102,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x45, 0x07, 0x08, 0x09}, // FU-A end
				},
			},
			expectNALUnits: 1,
			expectError:    false,
		},
		{
			name: "Sequence wraparound during fragmentation",
			packets: []rtp.Packet{
				{
					Header: rtp.Header{
						SequenceNumber: 65534,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
				},
				{
					Header: rtp.Header{
						SequenceNumber: 65535,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x05, 0x04, 0x05, 0x06}, // FU-A middle
				},
				{
					Header: rtp.Header{
						SequenceNumber: 0, // Wraparound
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x05, 0x07, 0x08, 0x09}, // FU-A middle
				},
				{
					Header: rtp.Header{
						SequenceNumber: 1,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x45, 0x0A, 0x0B, 0x0C}, // FU-A end
				},
			},
			expectNALUnits: 1,
			expectError:    false,
		},
		{
			name: "Large gap detected as packet loss",
			packets: []rtp.Packet{
				{
					Header: rtp.Header{
						SequenceNumber: 100,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
				},
				{
					Header: rtp.Header{
						SequenceNumber: 250, // Gap of 150
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x05, 0x04, 0x05, 0x06}, // FU-A middle
				},
			},
			expectNALUnits: 0,
			expectError:    false,
		},
		{
			name: "Gap of 2 during FU-A resets (missing packet)",
			packets: []rtp.Packet{
				{
					Header: rtp.Header{
						SequenceNumber: 100,
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
				},
				{
					Header: rtp.Header{
						SequenceNumber: 102, // Gap of 2 - missing packet produces corrupt NAL
						PayloadType:    96,
					},
					Payload: []byte{0x7C, 0x45, 0x04, 0x05, 0x06}, // FU-A end
				},
			},
			expectNALUnits: 0,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d.Reset()
			nalUnitsCollected := 0

			for _, packet := range tt.packets {
				nalUnits, err := d.Depacketize(&packet)
				if tt.expectError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
				nalUnitsCollected += len(nalUnits)
			}

			assert.Equal(t, tt.expectNALUnits, nalUnitsCollected)
		})
	}
}

func TestH264Depacketizer_FragmentTimeout(t *testing.T) {
	d := &H264Depacketizer{
		fragments:       [][]byte{},
		fragmentTimeout: 100 * time.Millisecond, // Short timeout for testing
	}

	// Start a fragmented NAL unit
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
	}

	nalUnits, err := d.Depacketize(packet1)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 0) // No complete NAL unit yet

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Send middle fragment after timeout
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 101,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x05, 0x04, 0x05, 0x06}, // FU-A middle
	}

	nalUnits, err = d.Depacketize(packet2)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 0) // Should be discarded due to timeout

	// Start new fragment
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 102,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x85, 0x07, 0x08, 0x09}, // FU-A start
	}

	nalUnits, err = d.Depacketize(packet3)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 0) // No complete NAL unit yet

	// Complete quickly
	packet4 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 103,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x45, 0x0A, 0x0B, 0x0C}, // FU-A end
	}

	nalUnits, err = d.Depacketize(packet4)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 1) // Should complete successfully
}

func TestH264Depacketizer_WraparoundEdgeCases(t *testing.T) {
	d := &H264Depacketizer{
		fragments:       [][]byte{},
		fragmentTimeout: 5 * time.Second,
	}

	tests := []struct {
		name     string
		lastSeq  uint16
		nextSeq  uint16
		isStart  bool
		expectOK bool
	}{
		{
			name:     "Normal increment",
			lastSeq:  100,
			nextSeq:  101,
			isStart:  false,
			expectOK: true,
		},
		{
			name:     "Wraparound from max to 0",
			lastSeq:  65535,
			nextSeq:  0,
			isStart:  false,
			expectOK: true,
		},
		{
			name:     "Wraparound from max to 1 (gap of 2 - missing seq 0)",
			lastSeq:  65535,
			nextSeq:  1,
			isStart:  false,
			expectOK: false, // Gap of 2 means one missing packet — must discard
		},
		{
			name:     "Small backward jump (reordering)",
			lastSeq:  100,
			nextSeq:  98,
			isStart:  false,
			expectOK: false, // Will reset fragments
		},
		{
			name:     "Large forward jump",
			lastSeq:  100,
			nextSeq:  300,
			isStart:  false,
			expectOK: false, // Will reset fragments
		},
		{
			name:     "Start bit ignores sequence check",
			lastSeq:  100,
			nextSeq:  200,
			isStart:  true,
			expectOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup initial state
			d.Reset()
			d.lastSeq = tt.lastSeq

			// Start a fragment if testing continuation
			if !tt.isStart {
				d.fragments = [][]byte{{0x00, 0x00, 0x00, 0x01, 0x05}} // Fake start
			}

			// Create test packet
			fuHeader := byte(0x05) // NAL type 5
			if tt.isStart {
				fuHeader |= 0x80 // Set start bit
			}

			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: tt.nextSeq,
					PayloadType:    96,
				},
				Payload: []byte{0x7C, fuHeader, 0x01, 0x02, 0x03}, // FU-A
			}

			nalUnits, err := d.Depacketize(packet)
			assert.NoError(t, err)

			if tt.expectOK {
				// Should continue accumulating
				assert.NotEmpty(t, d.fragments)
			} else {
				// Should have reset due to gap
				if !tt.isStart {
					assert.Len(t, nalUnits, 0)
				}
			}
		})
	}
}

func TestH264Depacketizer_SequenceDistance(t *testing.T) {
	d := &H264Depacketizer{}

	tests := []struct {
		s1       uint16
		s2       uint16
		expected int
		desc     string
	}{
		// Normal cases
		{100, 99, 1, "Normal forward"},
		{100, 101, -1, "Normal backward"},
		{100, 100, 0, "Same sequence"},

		// Wraparound cases
		{0, 65535, 1, "Forward wrap from 65535 to 0"},
		{1, 65535, 2, "Forward wrap from 65535 to 1"},
		{65535, 0, -1, "Backward wrap from 0 to 65535"},
		{65535, 1, -2, "Backward wrap from 1 to 65535"},

		// Large differences
		{32768, 0, -32768, "Exactly half range backward"},
		{0, 32768, -32768, "Exactly half range forward (interpreted as backward)"},
		{32769, 0, -32767, "Just over half range forward (wraps to negative)"},
		{0, 32769, 32767, "Just over half range backward (wraps to positive)"},

		// Edge cases near wraparound
		{65534, 0, -2, "Two steps to wraparound"},
		{65534, 1, -3, "Three steps to wraparound"},
		{2, 65535, 3, "Three steps from wraparound"},
		{2, 65534, 4, "Four steps from wraparound"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			result := d.sequenceDistance(tt.s1, tt.s2)
			assert.Equal(t, tt.expected, result,
				"sequenceDistance(%d, %d) = %d, want %d",
				tt.s1, tt.s2, result, tt.expected)
		})
	}
}

func TestH264Depacketizer_DuplicatePacketHandling(t *testing.T) {
	d := &H264Depacketizer{
		fragments:       [][]byte{},
		fragmentTimeout: 5 * time.Second,
	}

	// Start a fragment
	packet1 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1000,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
	}

	nalUnits, err := d.Depacketize(packet1)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 0)

	// Send duplicate packet (same sequence number)
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1001,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x05, 0x04, 0x05, 0x06}, // FU-A middle
	}

	nalUnits, err = d.Depacketize(packet2)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 0)

	// Send the same packet again (duplicate)
	nalUnits, err = d.Depacketize(packet2)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 0) // Should be ignored

	// Complete the fragment
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1002,
			PayloadType:    96,
		},
		Payload: []byte{0x7C, 0x45, 0x07, 0x08, 0x09}, // FU-A end
	}

	nalUnits, err = d.Depacketize(packet3)
	require.NoError(t, err)
	assert.Len(t, nalUnits, 1) // Should complete successfully
}

func TestH264Depacketizer_NetworkJitterTolerance(t *testing.T) {
	d := &H264Depacketizer{
		fragments:       [][]byte{},
		fragmentTimeout: 5 * time.Second,
	}

	// Test various gap sizes
	testCases := []struct {
		name        string
		gap         int
		shouldReset bool
	}{
		{"Gap 1 - normal", 1, false},
		// Any gap > 1 during FU-A means missing data — must discard to avoid corrupt NALs
		{"Gap 2 - missing packet", 2, true},
		{"Gap 3 - moderate loss", 3, true},
		{"Gap 4 - severe loss", 4, true},
		{"Gap 5 - severe loss", 5, true},
		{"Gap 6 - too large", 6, true},
		{"Gap 10 - definitely too large", 10, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d.Reset()

			// Start fragment
			startSeq := uint16(1000)
			packet1 := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: startSeq,
					PayloadType:    96,
				},
				Payload: []byte{0x7C, 0x85, 0x01, 0x02, 0x03}, // FU-A start
			}

			_, err := d.Depacketize(packet1)
			require.NoError(t, err)

			// Send next packet with gap
			packet2 := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: startSeq + uint16(tc.gap),
					PayloadType:    96,
				},
				Payload: []byte{0x7C, 0x45, 0x04, 0x05, 0x06}, // FU-A end
			}

			nalUnits, err := d.Depacketize(packet2)
			require.NoError(t, err)

			if tc.shouldReset {
				assert.Len(t, nalUnits, 0, "Gap %d should reset fragments", tc.gap)
			} else {
				assert.Len(t, nalUnits, 1, "Gap %d should be tolerated", tc.gap)
			}
		})
	}
}
