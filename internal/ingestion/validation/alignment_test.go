package validation

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlignmentValidator_ValidateMPEGTSAlignment(t *testing.T) {
	validator := NewAlignmentValidator()

	tests := []struct {
		name          string
		data          []byte
		wantPackets   int
		wantRemainder []byte
		wantErr       bool
	}{
		{
			name:          "empty data",
			data:          []byte{},
			wantPackets:   0,
			wantRemainder: nil,
			wantErr:       false,
		},
		{
			name:          "single complete packet",
			data:          createValidTSPacket(0),
			wantPackets:   1,
			wantRemainder: nil,
			wantErr:       false,
		},
		{
			name:          "multiple complete packets",
			data:          append(createValidTSPacket(0), createValidTSPacket(1)...),
			wantPackets:   2,
			wantRemainder: nil,
			wantErr:       false,
		},
		{
			name:          "packet with remainder",
			data:          append(createValidTSPacket(0), []byte{0x47, 0x00, 0x01}...),
			wantPackets:   1,
			wantRemainder: []byte{0x47, 0x00, 0x01},
			wantErr:       false,
		},
		{
			name:          "missing sync byte",
			data:          createInvalidTSPacket(),
			wantPackets:   0,
			wantRemainder: createInvalidTSPacket(),
			wantErr:       true,
		},
		{
			name:          "partial packet only",
			data:          []byte{0x47, 0x00, 0x01, 0x02},
			wantPackets:   0,
			wantRemainder: []byte{0x47, 0x00, 0x01, 0x02},
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packets, remainder, err := validator.ValidateMPEGTSAlignment(tt.data)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.Equal(t, tt.wantPackets, packets)
			assert.Equal(t, tt.wantRemainder, remainder)
		})
	}
}

func TestAlignmentValidator_ProcessWithAlignment(t *testing.T) {
	tests := []struct {
		name        string
		inputs      [][]byte
		wantOutputs [][]byte
		wantPartial bool
	}{
		{
			name: "single aligned message",
			inputs: [][]byte{
				createValidTSPacket(0),
			},
			wantOutputs: [][]byte{
				createValidTSPacket(0),
			},
			wantPartial: false,
		},
		{
			name: "message split across boundaries",
			inputs: [][]byte{
				createValidTSPacket(0)[:100], // First part
				createValidTSPacket(0)[100:], // Second part
			},
			wantOutputs: [][]byte{
				nil,                    // First input produces no complete packets
				createValidTSPacket(0), // Second input completes the packet
			},
			wantPartial: false,
		},
		{
			name: "multiple packets with partial",
			inputs: [][]byte{
				append(createValidTSPacket(0), createValidTSPacket(1)[:50]...),
				append(createValidTSPacket(1)[50:], createValidTSPacket(2)...),
			},
			wantOutputs: [][]byte{
				createValidTSPacket(0), // First complete packet
				append(createValidTSPacket(1), createValidTSPacket(2)...), // Completed + new packet
			},
			wantPartial: false,
		},
		{
			name: "misaligned data with sync search",
			inputs: [][]byte{
				append([]byte{0xFF, 0xFF}, createValidTSPacket(0)...),
			},
			wantOutputs: [][]byte{
				createValidTSPacket(0),
			},
			wantPartial: false,
		},
		{
			name: "no sync byte found",
			inputs: [][]byte{
				bytes.Repeat([]byte{0xFF}, 200),
			},
			wantOutputs: [][]byte{
				nil,
			},
			wantPartial: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewAlignmentValidator()

			for i, input := range tt.inputs {
				output, err := validator.ProcessWithAlignment(input)

				// Allow errors for no sync byte cases
				if tt.name == "no sync byte found" {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}

				assert.Equal(t, tt.wantOutputs[i], output, "Output mismatch at input %d", i)
			}

			assert.Equal(t, tt.wantPartial, validator.HasPartialPacket())
		})
	}
}

func TestAlignmentValidator_ContinuityAcrossMessages(t *testing.T) {
	validator := NewAlignmentValidator()

	// Simulate SRT messages that don't align with TS packet boundaries
	packet1 := createValidTSPacket(0)
	packet2 := createValidTSPacket(1)
	packet3 := createValidTSPacket(2)

	// Split packets across message boundaries
	msg1 := append(packet1, packet2[:100]...)
	msg2 := append(packet2[100:], packet3[:50]...)
	msg3 := packet3[50:]

	// Process first message
	output1, err := validator.ProcessWithAlignment(msg1)
	require.NoError(t, err)
	assert.Equal(t, packet1, output1)
	assert.True(t, validator.HasPartialPacket())

	// Process second message
	output2, err := validator.ProcessWithAlignment(msg2)
	require.NoError(t, err)
	assert.Equal(t, packet2, output2)
	assert.True(t, validator.HasPartialPacket())

	// Process third message
	output3, err := validator.ProcessWithAlignment(msg3)
	require.NoError(t, err)
	assert.Equal(t, packet3, output3)
	assert.False(t, validator.HasPartialPacket())
}

func TestAlignmentValidator_FindSyncByte(t *testing.T) {
	validator := NewAlignmentValidator()

	tests := []struct {
		name    string
		data    []byte
		wantPos int
	}{
		{
			name:    "sync at start",
			data:    createValidTSPacket(0),
			wantPos: 0,
		},
		{
			name:    "sync after garbage",
			data:    append([]byte{0xFF, 0xFF, 0xFF}, createValidTSPacket(0)...),
			wantPos: 3,
		},
		{
			name:    "multiple sync bytes - verify with next packet",
			data:    append([]byte{0x47, 0xFF}, append(createValidTSPacket(0), createValidTSPacket(1)...)...),
			wantPos: 2, // Should find the real sync byte
		},
		{
			name:    "no sync byte",
			data:    bytes.Repeat([]byte{0xFF}, 200),
			wantPos: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := validator.findSyncByte(tt.data)
			assert.Equal(t, tt.wantPos, pos)
		})
	}
}

func TestAlignmentValidator_Stats(t *testing.T) {
	validator := NewAlignmentValidator()

	// Process some packets
	validator.ValidateMPEGTSAlignment(createValidTSPacket(0))
	validator.ValidateMPEGTSAlignment(append(createValidTSPacket(1), []byte{0x47}...))
	validator.ValidateMPEGTSAlignment(createInvalidTSPacket())

	stats := validator.GetStats()
	assert.Equal(t, int64(2), stats.PacketsProcessed)
	assert.Equal(t, int64(1), stats.PartialPackets)
	assert.Equal(t, int64(1), stats.AlignmentErrors)

	// Test reset
	validator.Reset()
	stats = validator.GetStats()
	assert.Equal(t, int64(0), stats.PacketsProcessed)
	assert.Equal(t, int64(0), stats.PartialPackets)
	assert.Equal(t, int64(0), stats.AlignmentErrors)
}

// Helper functions

func createValidTSPacket(pid uint16) []byte {
	packet := make([]byte, 188)
	packet[0] = 0x47 // Sync byte
	packet[1] = byte(pid >> 8 & 0x1F)
	packet[2] = byte(pid)
	packet[3] = 0x10 // Payload exists, no adaptation field
	// Fill with dummy payload
	for i := 4; i < 188; i++ {
		packet[i] = byte(i)
	}
	return packet
}

func createInvalidTSPacket() []byte {
	packet := make([]byte, 188)
	packet[0] = 0xFF // Invalid sync byte
	return packet
}
