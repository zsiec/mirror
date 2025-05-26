package ingestion

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/zsiec/mirror/internal/ingestion/mpegts"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/logger"
)

// mockSRTConnectionPES provides a minimal mock for PES testing
type mockSRTConnectionPES struct {
	streamID string
}

func (m *mockSRTConnectionPES) GetStreamID() string {
	return m.streamID
}

func (m *mockSRTConnection) Close() error {
	return nil
}

func (m *mockSRTConnection) Read([]byte) (int, error) {
	return 0, nil
}

func (m *mockSRTConnection) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockSRTConnection) GetStats() *srt.ConnectionStats {
	return &srt.ConnectionStats{}
}

func (m *mockSRTConnection) IsConnected() bool {
	return true
}

// TestPESPayloadExtraction tests that we correctly extract video bitstream from PES packets
func TestPESPayloadExtraction(t *testing.T) {
	tests := []struct {
		name                string
		pesPacket           []byte
		expectedBitstream   []byte
		expectedValid       bool
		description         string
	}{
		{
			name: "Valid PES packet with SPS NAL unit",
			pesPacket: []byte{
				// PES header (9 bytes + 5 byte extension)
				0x00, 0x00, 0x01, 0xE0, // PES start code + stream ID
				0x00, 0x20,             // PES packet length
				0x80, 0x80,             // PES header flags
				0x05,                   // PES header data length (5 bytes)
				0x21, 0x00, 0x57, 0x07, 0xA1, // PTS (5 bytes)
				// Video bitstream starts here
				0x00, 0x00, 0x00, 0x01, // Start code
				0x67,                   // SPS NAL unit type (7)
				0x42, 0x80, 0x28, 0x95, // SPS data
				0xA0, 0x14, 0x01, 0x6E,
			},
			expectedBitstream: []byte{
				0x00, 0x00, 0x00, 0x01, // Start code
				0x67,                   // SPS NAL unit
				0x42, 0x80, 0x28, 0x95,
				0xA0, 0x14, 0x01, 0x6E,
			},
			expectedValid: true,
			description:   "Should extract SPS NAL unit from PES packet",
		},
		{
			name: "Valid PES packet with multiple NAL units",
			pesPacket: []byte{
				// PES header (9 bytes + 3 byte extension)
				0x00, 0x00, 0x01, 0xE0, // PES start code + stream ID
				0x00, 0x30,             // PES packet length
				0x80, 0x80,             // PES header flags
				0x03,                   // PES header data length (3 bytes)
				0x21, 0x00, 0x57,       // PTS partial (3 bytes)
				// Video bitstream with multiple NAL units
				0x00, 0x00, 0x00, 0x01, // Start code 1
				0x09, 0xF0,             // AUD NAL unit
				0x00, 0x00, 0x00, 0x01, // Start code 2
				0x41, 0x9B, 0x00, 0x22, // Slice NAL unit
				0x80, 0x50,
			},
			expectedBitstream: []byte{
				0x00, 0x00, 0x00, 0x01, // Start code 1
				0x09, 0xF0,             // AUD NAL unit
				0x00, 0x00, 0x00, 0x01, // Start code 2
				0x41, 0x9B, 0x00, 0x22, // Slice NAL unit
				0x80, 0x50,
			},
			expectedValid: true,
			description:   "Should extract multiple NAL units from PES packet",
		},
		{
			name: "PES packet with corrupted header",
			pesPacket: []byte{
				// Invalid PES start code
				0x00, 0x01, 0x01, 0xE0, // Wrong start code
				0x00, 0x20,
				0x80, 0x80,
				0x05,
			},
			expectedBitstream: []byte{
				0x00, 0x01, 0x01, 0xE0,
				0x00, 0x20,
				0x80, 0x80,
				0x05,
			}, // Should return original payload
			expectedValid: false,
			description:   "Should handle corrupted PES header gracefully",
		},
		{
			name: "Short PES packet",
			pesPacket: []byte{
				0x00, 0x00, 0x01, // Too short for PES header
			},
			expectedBitstream: []byte{
				0x00, 0x00, 0x01,
			}, // Should return original payload
			expectedValid: false,
			description:   "Should handle short packets gracefully",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create minimal test adapter without full SRT connection
			adapter := &SRTConnectionAdapter{
				mpegtsParser: mpegts.NewParser(),
				logger:       logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())),
			}

			// Create MPEG-TS packet
			tsPkt := &mpegts.Packet{
				PID:          256, // Video PID
				PayloadStart: true,
				PayloadExists: true,
				Payload:      tt.pesPacket,
			}

			// Set video PID in parser
			adapter.mpegtsParser.SetVideoPID(256)

			// Extract video bitstream
			result := adapter.extractVideoBitstream(tsPkt)

			// Verify result
			if len(result) != len(tt.expectedBitstream) {
				t.Errorf("Expected bitstream length %d, got %d", len(tt.expectedBitstream), len(result))
			}

			// Compare byte-by-byte
			for i, expected := range tt.expectedBitstream {
				if i >= len(result) {
					t.Errorf("Result too short at byte %d", i)
					break
				}
				if result[i] != expected {
					t.Errorf("Byte %d: expected 0x%02X, got 0x%02X", i, expected, result[i])
				}
			}

			t.Logf("✅ %s: extracted %d bytes from %d byte PES packet", 
				tt.description, len(result), len(tt.pesPacket))
		})
	}
}

// TestNALUnitValidation tests that extracted bitstream produces valid NAL units
func TestNALUnitValidation(t *testing.T) {
	// Create a realistic H.264 bitstream with SPS, PPS, and IDR frame
	h264Bitstream := []byte{
		// SPS NAL unit
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x80, 0x28,
		0x95, 0xA0, 0x14, 0x01, 0x6E, 0x02, 0xD1, 0x00,
		0x00, 0x03, 0x00, 0x01, 0x00, 0x00, 0x03, 0x00,
		0x32, 0x0F, 0x18, 0x31, 0x96,
		
		// PPS NAL unit
		0x00, 0x00, 0x00, 0x01, 0x68, 0xCE, 0x06, 0xE2,
		
		// IDR slice
		0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00,
		0x33, 0xFF,
	}

	// Wrap in PES packet
	pesPacket := make([]byte, 0)
	pesPacket = append(pesPacket, []byte{
		0x00, 0x00, 0x01, 0xE0, // PES start code + stream ID
		0x00, 0x00,             // PES packet length (0 = variable)
		0x80, 0x80,             // PES header flags
		0x05,                   // PES header data length
		0x21, 0x00, 0x57, 0x07, 0xA1, // PTS
	}...)
	pesPacket = append(pesPacket, h264Bitstream...)

	// Create minimal test adapter
	adapter := &SRTConnectionAdapter{
		mpegtsParser: mpegts.NewParser(),
		logger:       logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())),
	}

	// Create MPEG-TS packet
	tsPkt := &mpegts.Packet{
		PID:          256,
		PayloadStart: true,
		PayloadExists: true,
		Payload:      pesPacket,
	}

	adapter.mpegtsParser.SetVideoPID(256)

	// Extract bitstream
	result := adapter.extractVideoBitstream(tsPkt)

	// Verify we got the expected H.264 bitstream
	if len(result) != len(h264Bitstream) {
		t.Errorf("Expected bitstream length %d, got %d", len(h264Bitstream), len(result))
	}

	// Verify start codes are present
	startCodeCount := 0
	for i := 0; i < len(result)-3; i++ {
		if result[i] == 0x00 && result[i+1] == 0x00 && result[i+2] == 0x00 && result[i+3] == 0x01 {
			startCodeCount++
		}
	}

	expectedStartCodes := 3 // SPS, PPS, IDR
	if startCodeCount != expectedStartCodes {
		t.Errorf("Expected %d start codes, found %d", expectedStartCodes, startCodeCount)
	}

	// Verify NAL unit types
	nalTypes := make([]uint8, 0)
	for i := 0; i < len(result)-4; i++ {
		if result[i] == 0x00 && result[i+1] == 0x00 && result[i+2] == 0x00 && result[i+3] == 0x01 {
			if i+4 < len(result) {
				nalType := result[i+4] & 0x1F
				nalTypes = append(nalTypes, nalType)
			}
		}
	}

	expectedNALTypes := []uint8{7, 8, 5} // SPS, PPS, IDR
	if len(nalTypes) != len(expectedNALTypes) {
		t.Errorf("Expected %d NAL types, got %d", len(expectedNALTypes), len(nalTypes))
	}

	for i, expected := range expectedNALTypes {
		if i >= len(nalTypes) || nalTypes[i] != expected {
			t.Errorf("NAL type %d: expected %d, got %d", i, expected, nalTypes[i])
		}
	}

	t.Logf("✅ Successfully extracted valid H.264 bitstream with NAL types: %v", nalTypes)
}

// TestContinuationPackets tests handling of PES continuation packets
func TestContinuationPackets(t *testing.T) {
	// Create minimal test adapter
	adapter := &SRTConnectionAdapter{
		mpegtsParser: mpegts.NewParser(),
		logger:       logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())),
	}

	// Test continuation packet (PayloadStart = false)
	continuationData := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC}
	tsPkt := &mpegts.Packet{
		PID:          256,
		PayloadStart: false, // This is a continuation packet
		PayloadExists: true,
		Payload:      continuationData,
	}

	adapter.mpegtsParser.SetVideoPID(256)

	// Extract bitstream - should return original payload for continuation packets
	result := adapter.extractVideoBitstream(tsPkt)

	if len(result) != len(continuationData) {
		t.Errorf("Expected length %d, got %d", len(continuationData), len(result))
	}

	for i, expected := range continuationData {
		if result[i] != expected {
			t.Errorf("Byte %d: expected 0x%02X, got 0x%02X", i, expected, result[i])
		}
	}

	t.Log("✅ Continuation packets handled correctly")
}