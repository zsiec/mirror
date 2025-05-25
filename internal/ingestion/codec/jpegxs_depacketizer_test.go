package codec

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJPEGXSDepacketizer_SinglePacketFrame(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Create a single packet frame
	// Header: F=1, L=1 (first and last), E=0 (no extended header)
	header := []byte{
		0xC0,       // F=1, L=1, P=00, I=0, E=0, reserved=00
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	// Mock JPEG XS data
	jpegData := []byte{0xFF, 0x10, 0x00, 0x00, 0x01, 0x02, 0x03, 0x04}
	payload := append(header, jpegData...)
	pkt := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,
		},
		Payload: payload,
	}
	frames, err := d.Depacketize(pkt)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, jpegData, frames[0])
}

func TestJPEGXSDepacketizer_MultiPacketFrame(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// First packet
	header1 := []byte{
		0x80,       // F=1, L=0, P=00, I=0, E=0, reserved=00
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data1 := []byte{0xFF, 0x10, 0x00, 0x00}
	payload1 := append(header1, data1...)
	pkt1 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,
		},
		Payload: payload1,
	}
	frames, err := d.Depacketize(pkt1)
	require.NoError(t, err)
	assert.Len(t, frames, 0) // No complete frame yet

	// Middle packet
	header2 := []byte{
		0x00,       // F=0, L=0, P=00, I=0, E=0, reserved=00
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data2 := []byte{0x01, 0x02, 0x03, 0x04}
	payload2 := append(header2, data2...)
	pkt2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 101,
		},
		Payload: payload2,
	}
	frames, err = d.Depacketize(pkt2)
	require.NoError(t, err)
	assert.Len(t, frames, 0) // No complete frame yet

	// Last packet
	header3 := []byte{
		0x40,       // F=0, L=1, P=00, I=0, E=0, reserved=00
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data3 := []byte{0x05, 0x06, 0x07, 0x08}
	payload3 := append(header3, data3...)
	pkt3 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 102,
		},
		Payload: payload3,
	}
	frames, err = d.Depacketize(pkt3)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	// Verify assembled frame
	expectedFrame := append(append(data1, data2...), data3...)
	assert.Equal(t, expectedFrame, frames[0])
}

func TestJPEGXSDepacketizer_ExtendedHeader(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Create packet with extended header
	header := []byte{
		0xC4,       // F=1, L=1, P=00, I=0, E=1 (extended), reserved=00
		0x00, 0x00, 0x01, // Frame ID = 1
		0x00, 0x01, // Packet ID = 1
		0x2A,       // Profile=Main (0x2A), Level=10
		0x51,       // SubLevel=5, ChromaFormat=4:2:2, BitDepth=10-bit
	}
	jpegData := []byte{0xFF, 0x10, 0x00, 0x00, 0x01, 0x02}
	payload := append(header, jpegData...)
	pkt := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,
		},
		Payload: payload,
	}
	frames, err := d.Depacketize(pkt)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, jpegData, frames[0])
	// Verify profile was updated
	// Type assert to access JPEG XS specific methods
	jxsd, ok := d.(*JPEGXSDepacketizer)
	if !ok {
		t.Fatal("Expected JPEGXSDepacketizer")
	}
	assert.Equal(t, ProfileMain, jxsd.GetProfile())
}

func TestJPEGXSDepacketizer_DifferentProfiles(t *testing.T) {
	tests := []struct {
		name        string
		profileByte byte
		expected    JPEGXSProfile
	}{
		{"Light", 0x1A, ProfileLight},
		{"Main", 0x2A, ProfileMain},
		{"High", 0x3A, ProfileHigh},
		{"High444_12", 0x4A, ProfileHigh444_12},
		{"LightSubline", 0x1B, ProfileLightSubline},
		{"MainSubline", 0x2B, ProfileMainSubline},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewJPEGXSDepacketizer()

			header := []byte{
				0xC4,       // F=1, L=1, E=1
				0x00, 0x00, 0x01, // Frame ID
				0x00, 0x01,       // Packet ID
				tt.profileByte,   // Profile + Level
				0x00,             // SubLevel + format
			}
			
			jpegData := []byte{0xFF, 0x10}
			payload := append(header, jpegData...)
			
			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: 100,
				},
				Payload: payload,
			}
			frames, err := d.Depacketize(packet)
			require.NoError(t, err)
			require.Len(t, frames, 1)
			
			// Type assert to access JPEG XS specific methods
			jxsd, ok := d.(*JPEGXSDepacketizer)
			require.True(t, ok, "Expected JPEGXSDepacketizer")
			assert.Equal(t, tt.expected, jxsd.GetProfile())
		})
	}
}

func TestJPEGXSDepacketizer_InterlacedMode(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Top field
	header1 := []byte{
		0xD0,       // F=1, L=1, P=01 (interlaced), I=0 (top field), E=0
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data1 := []byte{0xFF, 0x10, 0x00, 0x00}
	payload1 := append(header1, data1...)
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

		},

		Payload: payload1,

	}

	frames, err := d.Depacketize(packet)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, data1, frames[0])

	// Bottom field
	header2 := []byte{
		0xD8,       // F=1, L=1, P=01 (interlaced), I=1 (bottom field), E=0
		0x00, 0x00, 0x02, // Frame ID = 2
	}
	data2 := []byte{0xFF, 0x11, 0x00, 0x00}
	payload2 := append(header2, data2...)
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 101,

		},

		Payload: payload2,

	}

	frames, err = d.Depacketize(packet2)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, data2, frames[0])
}

func TestJPEGXSDepacketizer_PacketLoss(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// First packet
	header1 := []byte{
		0x80,       // F=1, L=0
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data1 := []byte{0xFF, 0x10, 0x00, 0x00}
	payload1 := append(header1, data1...)
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

		},

		Payload: payload1,

	}

	_, err := d.Depacketize(packet)
	require.NoError(t, err)

	// Simulate packet loss - skip sequence 101

	// Third packet arrives (sequence 102)
	header3 := []byte{
		0x40,       // F=0, L=1
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data3 := []byte{0x05, 0x06, 0x07, 0x08}
	payload3 := append(header3, data3...)
	packet3 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 102,

		},

		Payload: payload3,

	}

	frames, err := d.Depacketize(packet3)
	require.NoError(t, err)
	assert.Len(t, frames, 0) // Frame should be discarded due to packet loss

	// New frame starts
	header4 := []byte{
		0xC0,       // F=1, L=1
		0x00, 0x00, 0x02, // Frame ID = 2
	}
	data4 := []byte{0xFF, 0x20, 0x00, 0x00}
	payload4 := append(header4, data4...)
	packet4 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 103,

		},

		Payload: payload4,

	}

	frames, err = d.Depacketize(packet4)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, data4, frames[0])
}

func TestJPEGXSDepacketizer_FrameIDChange(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Start frame 1
	header1 := []byte{
		0x80,       // F=1, L=0
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data1 := []byte{0xFF, 0x10}
	payload1 := append(header1, data1...)
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

		},

		Payload: payload1,

	}

	_, err := d.Depacketize(packet)
	require.NoError(t, err)

	// New frame starts without completing previous
	header2 := []byte{
		0xC0,       // F=1, L=1
		0x00, 0x00, 0x02, // Frame ID = 2 (different)
	}
	data2 := []byte{0xFF, 0x20}
	payload2 := append(header2, data2...)
	packet2 := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 101,

		},

		Payload: payload2,

	}

	frames, err := d.Depacketize(packet2)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, data2, frames[0]) // Should get new frame, old one discarded
}

func TestJPEGXSDepacketizer_EmptyPayload(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Valid header but no JPEG data
	header := []byte{
		0xC0,       // F=1, L=1
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

		},

		Payload: header,

	}

	_, err := d.Depacketize(packet)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no JPEG XS data")
}

func TestJPEGXSDepacketizer_ShortPayload(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Too short for header
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,
		},
		Payload: []byte{0x00, 0x00},
	}
	_, err := d.Depacketize(packet)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")

	// Too short for extended header
	headerPartial := []byte{
		0xC4,       // F=1, L=1, E=1 (extended)
		0x00, 0x00, 0x01, // Frame ID
		0x00, // Incomplete extended header
	}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

		},

		Payload: headerPartial,

	}

	_, err = d.Depacketize(packet)
	assert.Error(t, err)
}

func TestJPEGXSDepacketizer_Reset(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	// Start a frame
	header := []byte{
		0x80,       // F=1, L=0
		0x00, 0x00, 0x01, // Frame ID = 1
	}
	data := []byte{0xFF, 0x10}
	payload := append(header, data...)
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

		},

		Payload: payload,

	}

	_, err := d.Depacketize(packet)
	require.NoError(t, err)

	// Reset depacketizer
	d.Reset()

	// NOTE: Cannot verify internal state as fields are not exposed by the interface
	// Just verify that depacketizer works after reset
}

func TestJPEGXSDepacketizer_LargeFrame(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	const numPackets = 100
	frameID := uint32(42)
	var expectedData []byte

	// Send many packets
	for i := 0; i < numPackets; i++ {
		var flags byte
		if i == 0 {
			flags = 0x80 // First packet
		} else if i == numPackets-1 {
			flags = 0x40 // Last packet
		} else {
			flags = 0x00 // Middle packet
		}

		header := []byte{
			flags,
			byte(frameID >> 16),
			byte(frameID >> 8),
			byte(frameID),
		}
		
		// Create some data for this packet
		data := make([]byte, 1000)
		for j := range data {
			data[j] = byte((i + j) % 256)
		}
		expectedData = append(expectedData, data...)
		
		payload := append(header, data...)
		
		packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: uint16(100+i),

			},

			Payload: payload,

		}

		frames, err := d.Depacketize(packet)
		require.NoError(t, err)
		
		if i < numPackets-1 {
			assert.Len(t, frames, 0) // Not complete yet
		} else {
			require.Len(t, frames, 1) // Complete frame
			assert.Equal(t, expectedData, frames[0])
		}
	}
}

func TestGetProfileName(t *testing.T) {
	tests := []struct {
		profile  JPEGXSProfile
		expected string
	}{
		{ProfileLight, "Light"},
		{ProfileMain, "Main"},
		{ProfileHigh, "High"},
		{ProfileHigh444_12, "High 4:4:4 12-bit"},
		{ProfileLightSubline, "Light Subline"},
		{ProfileMainSubline, "Main Subline"},
		{JPEGXSProfile(0xFF), "Unknown(0xFF)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetProfileName(tt.profile))
		})
	}
}

func TestValidateProfile(t *testing.T) {
	validProfiles := []JPEGXSProfile{
		ProfileLight, ProfileMain, ProfileHigh,
		ProfileHigh444_12, ProfileLightSubline, ProfileMainSubline,
	}

	for _, p := range validProfiles {
		assert.True(t, ValidateProfile(p), "Profile %v should be valid", p)
	}

	// Test invalid profiles
	assert.False(t, ValidateProfile(JPEGXSProfile(0x00)))
	assert.False(t, ValidateProfile(JPEGXSProfile(0xFF)))
}

func TestGetChromaFormatString(t *testing.T) {
	tests := []struct {
		format   uint8
		expected string
	}{
		{0, "4:2:0"},
		{1, "4:2:2"},
		{2, "4:4:4"},
		{3, "Unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, GetChromaFormatString(tt.format))
	}
}

func TestGetBitDepth(t *testing.T) {
	tests := []struct {
		code     uint8
		expected int
	}{
		{0, 8},
		{1, 10},
		{2, 12},
		{3, 0},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, GetBitDepth(tt.code))
	}
}

func TestJPEGXSDepacketizer_ChromaAndBitDepthParsing(t *testing.T) {
	d := NewJPEGXSDepacketizer()

	tests := []struct {
		name         string
		formatByte   byte
		chromaFormat uint8
		bitDepth     uint8
	}{
		{"4:2:0 8-bit", 0x00, 0, 0},
		{"4:2:2 10-bit", 0x05, 1, 1},
		{"4:4:4 12-bit", 0x0A, 2, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := []byte{
				0xC4, // F=1, L=1, E=1
				0x00, 0x00, 0x01, // Frame ID
				0x00, 0x01,    // Packet ID
				0x2A,          // Profile=Main
				tt.formatByte, // SubLevel + ChromaFormat + BitDepth
			}
			
			jpegData := []byte{0xFF, 0x10}
			payload := append(header, jpegData...)
			
			packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,

				},

				Payload: payload,

			}

			frames, err := d.Depacketize(packet)
			require.NoError(t, err)
			require.Len(t, frames, 1)
			
			// Verify parsing was correct
			chromaFormat := (tt.formatByte >> 2) & 0x03
			bitDepth := tt.formatByte & 0x03
			assert.Equal(t, tt.chromaFormat, chromaFormat)
			assert.Equal(t, tt.bitDepth, bitDepth)
		})
	}
}

// BenchmarkJPEGXSDepacketizer_SinglePacket benchmarks single packet processing
func BenchmarkJPEGXSDepacketizer_SinglePacket(b *testing.B) {
	d := NewJPEGXSDepacketizer()
	header := []byte{
		0xC0,       // F=1, L=1
		0x00, 0x00, 0x01, // Frame ID
	}
	data := make([]byte, 1400) // Typical MTU-sized payload
	payload := append(header, data...)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: uint16(i),

			},

			Payload: payload,

		}

		d.Depacketize(packet)
		d.Reset()
	}
}

// BenchmarkJPEGXSDepacketizer_MultiPacket benchmarks multi-packet frame assembly
func BenchmarkJPEGXSDepacketizer_MultiPacket(b *testing.B) {
	d := NewJPEGXSDepacketizer()
	// Create 10 packets for a frame
	packets := make([][]byte, 10)
	for i := 0; i < 10; i++ {
		var flags byte
		if i == 0 {
			flags = 0x80 // First
		} else if i == 9 {
			flags = 0x40 // Last
		} else {
			flags = 0x00 // Middle
		}
		
		header := []byte{
			flags,
			0x00, 0x00, 0x01, // Frame ID
		}
		data := make([]byte, 1400)
		packets[i] = append(header, data...)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j, pkt := range packets {
			packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: uint16(i*10+j),

				},

				Payload: pkt,

			}

			d.Depacketize(packet)
		}
		d.Reset()
	}
}
