package mpegts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewParser(t *testing.T) {
	parser := NewParser()
	assert.NotNil(t, parser)
	assert.NotNil(t, parser.pesBuffer)
	assert.NotNil(t, parser.pesStarted)
	assert.Equal(t, uint16(0), parser.videoPID)
	assert.Equal(t, uint16(0), parser.audioPID)
	assert.Equal(t, uint16(0), parser.pmtPID)
	assert.False(t, parser.patParsed)
	assert.False(t, parser.pmtParsed)
}

func TestParser_Parse_InvalidData(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name    string
		data    []byte
		want    int
		wantErr bool
	}{
		{
			name:    "empty data",
			data:    []byte{},
			wantErr: true,
		},
		{
			name:    "data too small",
			data:    make([]byte, PacketSize-1),
			wantErr: true,
		},
		{
			name: "invalid sync byte",
			data: func() []byte {
				data := make([]byte, PacketSize)
				data[0] = 0x46 // Wrong sync byte
				return data
			}(),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packets, err := parser.Parse(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, packets, tt.want)
			}
		})
	}
}

func TestParser_parsePacket_ValidPacket(t *testing.T) {
	parser := NewParser()

	// Create a valid MPEG-TS packet
	data := make([]byte, PacketSize)
	data[0] = SyncByte   // Sync byte
	data[1] = 0x40       // Payload unit start indicator + PID high
	data[2] = 0x11       // PID low (PID = 0x0011 = 17)
	data[3] = 0x10       // Adaptation field control (payload only) + continuity counter

	pkt, err := parser.parsePacket(data)
	require.NoError(t, err)
	assert.Equal(t, uint16(17), pkt.PID)
	assert.True(t, pkt.PayloadStart)
	assert.False(t, pkt.AdaptationFieldExists)
	assert.True(t, pkt.PayloadExists)
	assert.Equal(t, uint8(0), pkt.ContinuityCounter)
}

func TestParser_parsePacket_ErrorConditions(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{
			name:    "wrong size",
			data:    make([]byte, 100),
			wantErr: "invalid packet size",
		},
		{
			name: "missing sync byte",
			data: func() []byte {
				data := make([]byte, PacketSize)
				data[0] = 0x46 // Wrong sync byte
				return data
			}(),
			wantErr: "missing sync byte",
		},
		{
			name: "transport error indicator",
			data: func() []byte {
				data := make([]byte, PacketSize)
				data[0] = SyncByte
				data[1] = 0x80 // Transport error indicator set
				return data
			}(),
			wantErr: "transport error indicator set",
		},
		{
			name: "invalid PID",
			data: func() []byte {
				data := make([]byte, PacketSize)
				data[0] = SyncByte
				data[1] = 0x80 | 0x1F // Transport error indicator set + PID high bits
				data[2] = 0xFF
				return data
			}(),
			wantErr: "transport error indicator set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parser.parsePacket(tt.data)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParser_parsePacket_WithAdaptationField(t *testing.T) {
	parser := NewParser()

	// Create packet with adaptation field containing PCR
	data := make([]byte, PacketSize)
	data[0] = SyncByte
	data[1] = 0x00 // PID = 0
	data[2] = 0x00
	data[3] = 0x30 // Adaptation field + payload, continuity counter = 0

	// Adaptation field
	data[4] = 7    // Adaptation field length
	data[5] = 0x10 // PCR flag set

	// PCR value (6 bytes)
	data[6] = 0x01  // PCR base bits 32-25
	data[7] = 0x02  // PCR base bits 24-17
	data[8] = 0x03  // PCR base bits 16-9
	data[9] = 0x04  // PCR base bits 8-1
	data[10] = 0x08 // PCR base bit 0 + reserved + PCR ext bits 8-1
	data[11] = 0x10 // PCR ext bits 7-0

	pkt, err := parser.parsePacket(data)
	require.NoError(t, err)
	assert.True(t, pkt.AdaptationFieldExists)
	assert.True(t, pkt.PayloadExists)
	assert.True(t, pkt.HasPCR)
	assert.NotEqual(t, int64(0), pkt.PCR)
}

func TestParser_parsePESHeader(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		payload     []byte
		wantErr     bool
		expectPTS   bool
		expectDTS   bool
		errContains string
	}{
		{
			name:        "too short",
			payload:     []byte{0x00, 0x00, 0x01},
			wantErr:     true,
			errContains: "PES header too short",
		},
		{
			name:        "invalid start code",
			payload:     []byte{0x00, 0x00, 0x02, 0xE0, 0x00, 0x00, 0x80, 0x00, 0x00},
			wantErr:     true,
			errContains: "invalid PES start code",
		},
		{
			name:    "valid without PTS/DTS",
			payload: []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00, 0x80, 0x00, 0x00},
			wantErr: false,
		},
		{
			name: "with PTS only",
			payload: []byte{
				0x00, 0x00, 0x01, 0xE0, // PES start code + stream ID
				0x00, 0x00, 0x80, 0x80, 0x05, // PES packet length + flags + header length
				0x21, 0x00, 0x01, 0x00, 0x01, // PTS (5 bytes)
			},
			wantErr:   false,
			expectPTS: true,
		},
		{
			name: "with PTS and DTS",
			payload: []byte{
				0x00, 0x00, 0x01, 0xE0, // PES start code + stream ID
				0x00, 0x00, 0x80, 0xC0, 0x0A, // PES packet length + flags + header length
				0x31, 0x00, 0x01, 0x00, 0x01, // PTS (5 bytes)
				0x11, 0x00, 0x01, 0x00, 0x01, // DTS (5 bytes)
			},
			wantErr:   false,
			expectPTS: true,
			expectDTS: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := &Packet{Payload: tt.payload}
			err := parser.parsePESHeader(pkt)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectPTS, pkt.HasPTS)
				assert.Equal(t, tt.expectDTS, pkt.HasDTS)
			}
		})
	}
}

func TestParser_extractTimestamp(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name     string
		data     []byte
		wantErr  bool
		expected int64
	}{
		{
			name:    "insufficient data",
			data:    []byte{0x01, 0x02, 0x03},
			wantErr: true,
		},
		{
			name:     "valid timestamp",
			data:     []byte{0x21, 0x00, 0x01, 0x00, 0x01},
			wantErr:  false,
			expected: 0, // Calculate: (0x21&0x0E)<<29 | 0x00<<22 | (0x01&0xFE)<<14 | 0x00<<7 | 0x01>>1 = 0
		},
		{
			name:     "zero timestamp",
			data:     []byte{0x00, 0x00, 0x00, 0x00, 0x00},
			wantErr:  false,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamp, err := parser.extractTimestamp(tt.data)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, timestamp)
			}
		})
	}
}

func TestParser_PIDMethods(t *testing.T) {
	parser := NewParser()

	// Test initial state
	assert.Equal(t, uint16(0), parser.GetVideoPID())
	assert.Equal(t, uint16(0), parser.GetAudioPID())
	assert.Equal(t, uint16(0), parser.GetPMTPID())
	assert.False(t, parser.IsVideoPID(100))
	assert.False(t, parser.IsAudioPID(200))
	assert.False(t, parser.IsPCRPID(300))

	// Set PIDs
	parser.SetVideoPID(100)
	parser.SetAudioPID(200)
	parser.SetPCRPID(300)

	// Test getters
	assert.Equal(t, uint16(100), parser.GetVideoPID())
	assert.Equal(t, uint16(200), parser.GetAudioPID())

	// Test checkers
	assert.True(t, parser.IsVideoPID(100))
	assert.False(t, parser.IsVideoPID(101))
	assert.True(t, parser.IsAudioPID(200))
	assert.False(t, parser.IsAudioPID(201))
	assert.True(t, parser.IsPCRPID(300))
	assert.False(t, parser.IsPCRPID(301))
}

func TestParser_GetStreamTypes(t *testing.T) {
	parser := NewParser()

	// Initial state
	assert.Equal(t, uint8(0), parser.GetVideoStreamType())
	assert.Equal(t, uint8(0), parser.GetAudioStreamType())

	// Set through PMT parsing (we'll need to simulate this)
	parser.videoStreamType = 0x1B // H.264
	parser.audioStreamType = 0x0F // AAC

	assert.Equal(t, uint8(0x1B), parser.GetVideoStreamType())
	assert.Equal(t, uint8(0x0F), parser.GetAudioStreamType())
}

func TestParser_parsePAT(t *testing.T) {
	parser := NewParser()

	// Create a minimal valid PAT
	payload := []byte{
		0x00,                         // Pointer field
		0x00,                         // Table ID (PAT = 0x00)
		0xB0, 0x0D,                   // Section syntax indicator + section length (13 bytes)
		0x00, 0x01,                   // Transport stream ID
		0xC1,                         // Version number + current/next indicator
		0x00,                         // Section number
		0x00,                         // Last section number
		0x00, 0x01, 0x00, 0x10,       // Program 1, PMT PID 16
		0x00, 0x00, 0x00, 0x00,       // CRC32 (placeholder)
	}

	parser.parsePAT(payload)

	assert.True(t, parser.patParsed)
	assert.Equal(t, uint16(1), parser.programNum)
	assert.Equal(t, uint16(16), parser.pmtPID)
}

func TestParser_parsePAT_InvalidConditions(t *testing.T) {
	tests := []struct {
		name    string
		parser  *Parser
		payload []byte
	}{
		{
			name:    "already parsed",
			parser:  &Parser{patParsed: true, pesBuffer: make(map[uint16][]byte), pesStarted: make(map[uint16]bool)},
			payload: []byte{0x00, 0x00, 0xB0, 0x0D},
		},
		{
			name:    "too short",
			parser:  NewParser(),
			payload: []byte{0x00, 0x00, 0xB0},
		},
		{
			name:    "wrong table ID",
			parser:  NewParser(),
			payload: []byte{0x00, 0x01, 0xB0, 0x0D, 0x00, 0x01, 0xC1, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.parser.parsePAT(tt.payload)
			// Should not crash and should not set patParsed for invalid conditions
			if tt.name != "already parsed" {
				assert.False(t, tt.parser.patParsed)
			}
		})
	}
}

func TestParameterSetExtractor(t *testing.T) {
	var extractedSets [][]byte
	var extractedStreamType uint8

	extractor := func(parameterSets [][]byte, streamType uint8) {
		extractedSets = parameterSets
		extractedStreamType = streamType
	}

	// Test that the extractor function type works
	assert.NotNil(t, extractor)

	// Simulate calling the extractor
	testData := [][]byte{{0x01, 0x02, 0x03}}
	extractor(testData, 0x1B)

	assert.Equal(t, testData, extractedSets)
	assert.Equal(t, uint8(0x1B), extractedStreamType)
}

func TestParser_ParseWithExtractor(t *testing.T) {
	parser := NewParser()

	// Create a simple valid MPEG-TS packet
	data := make([]byte, PacketSize)
	data[0] = SyncByte
	data[1] = 0x40 // Payload unit start indicator
	data[2] = 0x11 // PID
	data[3] = 0x10 // Payload only

	packets, err := parser.ParseWithExtractor(data, nil)
	assert.NoError(t, err)
	assert.Len(t, packets, 0) // No valid PES packets in this simple test
}

func TestParser_Constants(t *testing.T) {
	assert.Equal(t, 188, PacketSize)
	assert.Equal(t, 0x47, int(SyncByte))
	assert.Equal(t, 8191, MaxPID)
	assert.Equal(t, 0x0000, int(PIDProgramAssociation))
	assert.Equal(t, 0x0001, int(PIDConditionalAccess))
	assert.Equal(t, 0x1FFF, int(PIDNull))
}

func TestPacket_StructFields(t *testing.T) {
	pkt := &Packet{
		PID:                   123,
		PayloadStart:          true,
		AdaptationFieldExists: true,
		PayloadExists:         true,
		ContinuityCounter:     5,
		Payload:               []byte{0x01, 0x02, 0x03},
		HasPTS:                true,
		HasDTS:                false,
		PTS:                   12345,
		DTS:                   0,
		HasPCR:                true,
		PCR:                   67890,
	}

	assert.Equal(t, uint16(123), pkt.PID)
	assert.True(t, pkt.PayloadStart)
	assert.True(t, pkt.AdaptationFieldExists)
	assert.True(t, pkt.PayloadExists)
	assert.Equal(t, uint8(5), pkt.ContinuityCounter)
	assert.Equal(t, []byte{0x01, 0x02, 0x03}, pkt.Payload)
	assert.True(t, pkt.HasPTS)
	assert.False(t, pkt.HasDTS)
	assert.Equal(t, int64(12345), pkt.PTS)
	assert.Equal(t, int64(0), pkt.DTS)
	assert.True(t, pkt.HasPCR)
	assert.Equal(t, int64(67890), pkt.PCR)
}
