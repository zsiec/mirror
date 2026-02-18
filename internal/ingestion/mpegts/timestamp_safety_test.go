package mpegts

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimestampExtractionSafety verifies timestamp extraction with validation
func TestTimestampExtractionSafety(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		data        []byte
		expectError bool
		errorMsg    string
	}{
		{
			name:        "too short data",
			data:        []byte{0x21, 0x00},
			expectError: true,
			errorMsg:    "timestamp extraction bounds check failed",
		},
		{
			name:        "invalid prefix byte",
			data:        []byte{0x00, 0x00, 0x00, 0x00, 0x00},
			expectError: true,
			errorMsg:    "invalid PTS/DTS prefix byte",
		},
		{
			name:        "missing marker bit at byte 2",
			data:        []byte{0x21, 0x00, 0x00, 0x00, 0x01},
			expectError: true,
			errorMsg:    "invalid timestamp marker bit at byte 2",
		},
		{
			name:        "missing marker bit at byte 4",
			data:        []byte{0x21, 0x00, 0x01, 0x00, 0x00},
			expectError: true,
			errorMsg:    "invalid timestamp marker bit at byte 4",
		},
		{
			name: "valid PTS timestamp",
			data: []byte{
				0x21,       // Valid prefix (0010 0001)
				0x00, 0x01, // Bits 32-22 with marker
				0x00, 0x01, // Bits 21-7 with marker
			},
			expectError: false,
		},
		{
			name: "valid DTS timestamp",
			data: []byte{
				0x31,       // Valid prefix for DTS (0011 0001)
				0x00, 0x01, // Bits 32-22 with marker
				0x00, 0x01, // Bits 21-7 with marker
			},
			expectError: false,
		},
		{
			name: "maximum valid timestamp",
			data: []byte{
				0x31,       // Valid prefix
				0xFF, 0xFF, // Max bits with marker
				0xFF, 0xFF, // Max bits with marker
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, err := parser.extractTimestamp(tt.data)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
				assert.GreaterOrEqual(t, ts, int64(0))
				assert.Less(t, ts, int64(1)<<33) // Must be within 33-bit range
			}
		})
	}
}

// TestPESHeaderParsingSafety tests PES header parsing with validation
func TestPESHeaderParsingSafety(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		setupPacket func() *Packet
		expectError bool
		errorMsg    string
	}{
		{
			name: "payload too short",
			setupPacket: func() *Packet {
				return &Packet{
					Payload: []byte{0x00, 0x00, 0x01, 0xE0}, // Only 4 bytes
				}
			},
			expectError: true,
			errorMsg:    "PES header too short",
		},
		{
			name: "invalid start code",
			setupPacket: func() *Packet {
				return &Packet{
					Payload: []byte{0xFF, 0xFF, 0xFF, 0xE0, 0x00, 0x00, 0x80, 0x80, 0x05},
				}
			},
			expectError: true,
			errorMsg:    "invalid PES start code",
		},
		{
			name: "PTS with invalid timestamp data",
			setupPacket: func() *Packet {
				return &Packet{
					Payload: []byte{
						0x00, 0x00, 0x01, 0xE0, // Start code + stream ID
						0x00, 0x00, // PES length
						0x80,                   // Flags
						0x80,                   // PTS flag set
						0x05,                   // Header data length
						0x00, 0x00, 0x00, 0x00, // Invalid PTS data (too short)
					},
				}
			},
			expectError: true,
			errorMsg:    "PES payload too short for PTS",
		},
		{
			name: "PTS and DTS with ordering violation",
			setupPacket: func() *Packet {
				return &Packet{
					Payload: []byte{
						0x00, 0x00, 0x01, 0xE0, // Start code + stream ID
						0x00, 0x00, // PES length
						0x80, // Flags
						0xC0, // PTS and DTS flags set
						0x0A, // Header data length (10 bytes for both)
						// PTS = 1000
						0x31, 0x00, 0x01, 0x00, 0x01,
						// DTS = 2000 (invalid: DTS > PTS)
						0x31, 0x00, 0x03, 0x00, 0x01,
					},
				}
			},
			expectError: true,
			errorMsg:    "PTS/DTS validation failed",
		},
		{
			name: "valid PES with PTS only",
			setupPacket: func() *Packet {
				return &Packet{
					Payload: []byte{
						0x00, 0x00, 0x01, 0xE0, // Start code + stream ID
						0x00, 0x00, // PES length
						0x80, // Flags
						0x80, // PTS flag set
						0x05, // Header data length
						// Valid PTS
						0x21, 0x00, 0x01, 0x00, 0x01,
					},
				}
			},
			expectError: false,
		},
		{
			name: "valid PES with PTS and DTS",
			setupPacket: func() *Packet {
				return &Packet{
					Payload: []byte{
						0x00, 0x00, 0x01, 0xE0, // Start code + stream ID
						0x00, 0x00, // PES length
						0x80, // Flags
						0xC0, // PTS and DTS flags set
						0x0A, // Header data length
						// PTS = 2000
						0x31, 0x00, 0x03, 0x00, 0x01,
						// DTS = 1000 (valid: DTS <= PTS)
						0x31, 0x00, 0x01, 0x00, 0x01,
					},
				}
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := tt.setupPacket()
			err := parser.parsePESHeader(pkt)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
				if pkt.HasPTS {
					assert.GreaterOrEqual(t, pkt.PTS, int64(0))
				}
				if pkt.HasDTS {
					assert.GreaterOrEqual(t, pkt.DTS, int64(0))
					assert.LessOrEqual(t, pkt.DTS, pkt.PTS)
				}
			}
		})
	}
}

// TestParsePacketWithValidation tests the enhanced packet parsing
func TestParsePacketWithValidation(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		packet      []byte
		expectError bool
		errorMsg    string
	}{
		{
			name:        "packet too small",
			packet:      make([]byte, 100),
			expectError: true,
			errorMsg:    "packet validation failed",
		},
		{
			name:        "packet too large",
			packet:      make([]byte, 200),
			expectError: true,
			errorMsg:    "packet validation failed",
		},
		{
			name:        "missing sync byte",
			packet:      make([]byte, 188),
			expectError: true,
			errorMsg:    "packet validation failed",
		},
		{
			name: "transport error indicator set",
			packet: func() []byte {
				pkt := make([]byte, 188)
				pkt[0] = SyncByte
				pkt[1] = 0x80 // Set transport error indicator
				return pkt
			}(),
			expectError: true,
			errorMsg:    "transport error indicator set",
		},
		{
			name: "valid packet",
			packet: func() []byte {
				pkt := make([]byte, 188)
				pkt[0] = SyncByte
				pkt[1] = 0x40 // Payload start
				pkt[2] = 0x64 // PID = 100
				pkt[3] = 0x10 // Payload exists
				return pkt
			}(),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt, err := parser.parsePacket(tt.packet)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, pkt)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, pkt)
				assert.Equal(t, uint16(100), pkt.PID)
			}
		})
	}
}

// TestBoundaryValidationInParsing tests boundary validation throughout parsing
func TestBoundaryValidationInParsing(t *testing.T) {
	parser := NewParser()

	t.Run("PAT parsing with malformed pointer field", func(t *testing.T) {
		// PAT with pointer field that points beyond payload
		payload := []byte{
			0xFF,       // Pointer field = 255 (way too large)
			0x00,       // Table ID
			0x00, 0x00, // Section length
		}

		// Should not panic
		parser.parsePAT(payload)
		assert.False(t, parser.patParsed)
	})

	t.Run("PMT parsing with invalid section length", func(t *testing.T) {
		// PMT with section length larger than payload
		payload := []byte{
			0x00,       // Pointer field
			0x02,       // Table ID (PMT)
			0x0F, 0xFF, // Section length = 4095 (too large)
			0x00, 0x00, 0x00, 0x00, 0x00,
		}

		// Should not panic
		parser.parsePMTWithExtractor(payload, nil)
		assert.False(t, parser.pmtParsed)
	})
}

// BenchmarkTimestampExtractionWithValidation benchmarks the performance impact
func BenchmarkTimestampExtractionWithValidation(b *testing.B) {
	parser := NewParser()
	validTimestamp := []byte{0x21, 0x00, 0x01, 0x00, 0x01}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.extractTimestamp(validTimestamp)
	}
}
