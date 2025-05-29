package ingestion

import (
	"testing"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestNewCodecDetector(t *testing.T) {
	detector := NewCodecDetector()
	assert.NotNil(t, detector)
	assert.NotNil(t, detector.payloadTypeMap)
	assert.Len(t, detector.payloadTypeMap, 0)
}

func TestCodecDetector_DetectFromRTPPacket(t *testing.T) {
	detector := NewCodecDetector()

	tests := []struct {
		name        string
		packet      *rtp.Packet
		expected    types.CodecType
		description string
	}{
		{
			name:        "nil packet",
			packet:      nil,
			expected:    types.CodecUnknown,
			description: "should handle nil packet gracefully",
		},
		{
			name: "static payload type PCMU",
			packet: &rtp.Packet{
				Header: rtp.Header{PayloadType: 0},
			},
			expected:    types.CodecG711,
			description: "should detect PCMU from static payload type 0",
		},
		{
			name: "static payload type PCMA",
			packet: &rtp.Packet{
				Header: rtp.Header{PayloadType: 8},
			},
			expected:    types.CodecG711,
			description: "should detect PCMA from static payload type 8",
		},
		{
			name: "static payload type H261",
			packet: &rtp.Packet{
				Header: rtp.Header{PayloadType: 31},
			},
			expected:    types.CodecH261,
			description: "should detect H261 from static payload type 31",
		},
		{
			name: "dynamic payload type with H264 NAL",
			packet: &rtp.Packet{
				Header:  rtp.Header{PayloadType: 96},
				Payload: []byte{0x67, 0x00, 0x00}, // NAL type 7 (SPS)
			},
			expected:    types.CodecH264,
			description: "should detect H264 from NAL unit patterns",
		},
		{
			name: "dynamic payload type with HEVC NAL",
			packet: &rtp.Packet{
				Header:  rtp.Header{PayloadType: 97},
				Payload: []byte{0x40, 0x01}, // HEVC NAL type 32 (VPS)
			},
			expected:    types.CodecHEVC,
			description: "should detect HEVC from NAL unit patterns",
		},
		{
			name: "dynamic payload type unknown",
			packet: &rtp.Packet{
				Header:  rtp.Header{PayloadType: 96},
				Payload: []byte{0xFF, 0xFF}, // Invalid NAL patterns
			},
			expected:    types.CodecUnknown,
			description: "should return unknown for unrecognizable patterns",
		},
		{
			name: "unknown static payload type",
			packet: &rtp.Packet{
				Header: rtp.Header{PayloadType: 50},
			},
			expected:    types.CodecUnknown,
			description: "should return unknown for unmapped payload types",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectFromRTPPacket(tt.packet)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestCodecDetector_DetectFromSDP(t *testing.T) {
	detector := NewCodecDetector()

	tests := []struct {
		name           string
		sdp            string
		expectedCodec  types.CodecType
		expectedParams map[string]string
		description    string
	}{
		{
			name: "H264 video SDP",
			sdp: `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
c=IN IP4 127.0.0.1
t=0 0
m=video 5004 RTP/AVP 96
a=rtpmap:96 H264/90000
a=fmtp:96 profile-level-id=42e01e`,
			expectedCodec: types.CodecH264,
			expectedParams: map[string]string{
				"clock_rate":       "90000",
				"profile-level-id": "42e01e",
			},
			description: "should detect H264 from video SDP",
		},
		{
			name: "HEVC video SDP",
			sdp: `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
c=IN IP4 127.0.0.1
t=0 0
m=video 5004 RTP/AVP 97
a=rtpmap:97 H265/90000
a=fmtp:97 profile-id=1`,
			expectedCodec: types.CodecHEVC,
			expectedParams: map[string]string{
				"clock_rate": "90000",
				"profile-id": "1",
			},
			description: "should detect HEVC from video SDP",
		},
		{
			name: "AAC audio SDP",
			sdp: `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
c=IN IP4 127.0.0.1
t=0 0
m=audio 5006 RTP/AVP 96
a=rtpmap:96 MPEG4-GENERIC/48000/2
a=fmtp:96 streamtype=5; profile-level-id=1; mode=AAC-hbr`,
			expectedCodec: types.CodecAAC,
			expectedParams: map[string]string{
				"clock_rate":       "48000",
				"streamtype":       "5",
				"profile-level-id": "1",
				"mode":             "AAC-hbr",
			},
			description: "should detect AAC from audio SDP",
		},
		{
			name: "multiple media sections",
			sdp: `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
c=IN IP4 127.0.0.1
t=0 0
m=video 5004 RTP/AVP 96
a=rtpmap:96 H264/90000
m=audio 5006 RTP/AVP 97
a=rtpmap:97 AAC/48000/2`,
			expectedCodec: types.CodecAAC, // Last detected codec
			expectedParams: map[string]string{
				"clock_rate": "48000",
			},
			description: "should handle multiple media sections",
		},
		{
			name:           "empty SDP",
			sdp:            "",
			expectedCodec:  types.CodecUnknown,
			expectedParams: map[string]string{},
			description:    "should handle empty SDP",
		},
		{
			name: "unknown codec",
			sdp: `v=0
m=video 5004 RTP/AVP 96
a=rtpmap:96 UNKNOWN/90000`,
			expectedCodec:  types.CodecUnknown,
			expectedParams: map[string]string{"clock_rate": "90000"},
			description:    "should return unknown for unrecognized codecs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec, params := detector.DetectFromSDP(tt.sdp)
			assert.Equal(t, tt.expectedCodec, codec, tt.description)
			for key, expectedValue := range tt.expectedParams {
				assert.Equal(t, expectedValue, params[key], "parameter %s should match", key)
			}
		})
	}
}

func TestCodecDetector_detectFromPayloadType(t *testing.T) {
	detector := NewCodecDetector()

	tests := []struct {
		payloadType uint8
		expected    types.CodecType
		description string
	}{
		{0, types.CodecG711, "PCMU"},
		{8, types.CodecG711, "PCMA"},
		{9, types.CodecG722, "G722"},
		{10, types.CodecL16, "L16 (1)"},
		{11, types.CodecL16, "L16 (2)"},
		{14, types.CodecMP3, "MPA/MP3"},
		{26, types.CodecJPEG, "JPEG"},
		{31, types.CodecH261, "H261"},
		{32, types.CodecMPV, "MPV"},
		{33, types.CodecMP2T, "MP2T"},
		{34, types.CodecH263, "H263"},
		{99, types.CodecUnknown, "Unknown payload type"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := detector.detectFromPayloadType(tt.payloadType)
			assert.Equal(t, tt.expected, result, "Payload type %d should map to %v", tt.payloadType, tt.expected)
		})
	}
}

func TestCodecDetector_AddPayloadTypeMapping(t *testing.T) {
	detector := NewCodecDetector()

	// Add custom mapping
	detector.AddPayloadTypeMapping(96, types.CodecH264)
	detector.AddPayloadTypeMapping(97, types.CodecAAC)

	// Test custom mappings are used
	packet96 := &rtp.Packet{Header: rtp.Header{PayloadType: 96}}
	codec96 := detector.DetectFromRTPPacket(packet96)
	assert.Equal(t, types.CodecH264, codec96)

	packet97 := &rtp.Packet{Header: rtp.Header{PayloadType: 97}}
	codec97 := detector.DetectFromRTPPacket(packet97)
	assert.Equal(t, types.CodecAAC, codec97)

	// Test that static mappings still work
	packet0 := &rtp.Packet{Header: rtp.Header{PayloadType: 0}}
	codec0 := detector.DetectFromRTPPacket(packet0)
	assert.Equal(t, types.CodecG711, codec0)
}

func TestCodecDetector_detectFromPayload(t *testing.T) {
	detector := NewCodecDetector()

	tests := []struct {
		name        string
		payload     []byte
		payloadType uint8
		expected    types.CodecType
		description string
	}{
		{
			name:        "empty payload",
			payload:     []byte{},
			payloadType: 96,
			expected:    types.CodecUnknown,
			description: "should return unknown for empty payload",
		},
		{
			name:        "non-dynamic payload type",
			payload:     []byte{0x67},
			payloadType: 0,
			expected:    types.CodecUnknown,
			description: "should not analyze static payload types",
		},
		{
			name:        "H264 NAL type 1",
			payload:     []byte{0x61}, // NAL type 1
			payloadType: 96,
			expected:    types.CodecH264,
			description: "should detect H264 from NAL type 1",
		},
		{
			name:        "H264 NAL type 7 (SPS)",
			payload:     []byte{0x67}, // NAL type 7
			payloadType: 96,
			expected:    types.CodecH264,
			description: "should detect H264 from SPS NAL",
		},
		{
			name:        "H264 NAL type 8 (PPS)",
			payload:     []byte{0x68}, // NAL type 8
			payloadType: 96,
			expected:    types.CodecH264,
			description: "should detect H264 from PPS NAL",
		},
		{
			name:        "HEVC NAL type 32 (VPS)",
			payload:     []byte{0x40, 0x01}, // NAL type 32
			payloadType: 97,
			expected:    types.CodecHEVC,
			description: "should detect HEVC from VPS NAL",
		},
		{
			name:        "HEVC NAL type 33 (SPS)",
			payload:     []byte{0x42, 0x01}, // For H264: 0x42 & 0x1F = 2 (valid), so detects H264
			payloadType: 97,
			expected:    types.CodecH264, // H264 detection takes precedence
			description: "should detect H264 NAL patterns first",
		},
		{
			name:        "invalid NAL type for H264",
			payload:     []byte{0xFF}, // NAL type 31 (invalid)
			payloadType: 96,
			expected:    types.CodecUnknown,
			description: "should not detect invalid H264 NAL types",
		},
		{
			name:        "invalid HEVC NAL type",
			payload:     []byte{0xFF, 0xFF}, // Invalid HEVC NAL
			payloadType: 97,
			expected:    types.CodecUnknown,
			description: "should not detect invalid HEVC NAL types",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.detectFromPayload(tt.payload, tt.payloadType)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestDetectCodecFromEncodingName(t *testing.T) {
	tests := []struct {
		name     string
		expected types.CodecType
	}{
		// Video codecs
		{"H264", types.CodecH264},
		{"AVC", types.CodecH264},
		{"h264", types.CodecH264}, // Case insensitive
		{"H265", types.CodecHEVC},
		{"HEVC", types.CodecHEVC},
		{"AV1", types.CodecAV1},
		{"VP8", types.CodecVP8},
		{"VP9", types.CodecVP9},
		{"JPEG-XS", types.CodecJPEGXS},
		{"JPEGXS", types.CodecJPEGXS},
		{"JXS", types.CodecJPEGXS},
		{"H261", types.CodecH261},
		{"H263", types.CodecH263},
		{"H263-1998", types.CodecH263},
		{"H263-2000", types.CodecH263},
		{"JPEG", types.CodecJPEG},
		{"MPV", types.CodecMPV},

		// Audio codecs
		{"AAC", types.CodecAAC},
		{"MP4A-LATM", types.CodecAAC},
		{"MPEG4-GENERIC", types.CodecAAC},
		{"OPUS", types.CodecOpus},
		{"MP3", types.CodecMP3},
		{"MPA", types.CodecMP3},
		{"PCMU", types.CodecG711},
		{"PCMA", types.CodecG711},
		{"G711", types.CodecG711},
		{"G722", types.CodecG722},
		{"L16", types.CodecPCM},
		{"L24", types.CodecPCM},
		{"VORBIS", types.CodecVorbis},
		{"SPEEX", types.CodecSpeex},

		// Container formats
		{"MP2T", types.CodecMP2T},

		// Unknown
		{"UNKNOWN", types.CodecUnknown},
		{"", types.CodecUnknown},
		{"INVALID_CODEC", types.CodecUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectCodecFromEncodingName(tt.name)
			assert.Equal(t, tt.expected, result, "Encoding name '%s' should map to %v", tt.name, tt.expected)
		})
	}
}

// Mock session for testing DetectCodecFromRTPSession
type mockRTPSession struct {
	payloadType  uint8
	mediaFormat  string
	encodingName string
	clockRate    uint32
}

func (m *mockRTPSession) GetPayloadType() uint8 {
	return m.payloadType
}

func (m *mockRTPSession) GetMediaFormat() string {
	return m.mediaFormat
}

func (m *mockRTPSession) GetEncodingName() string {
	return m.encodingName
}

func (m *mockRTPSession) GetClockRate() uint32 {
	return m.clockRate
}

func TestDetectCodecFromRTPSession(t *testing.T) {
	tests := []struct {
		name        string
		session     *mockRTPSession
		expected    types.CodecType
		description string
	}{
		{
			name: "static payload type PCMU",
			session: &mockRTPSession{
				payloadType: 0,
				clockRate:   8000,
			},
			expected:    types.CodecG711,
			description: "should detect static payload types",
		},
		{
			name: "dynamic payload type with media format",
			session: &mockRTPSession{
				payloadType: 96,
				mediaFormat: "H264/90000",
				clockRate:   90000,
			},
			expected:    types.CodecH264,
			description: "should detect from media format",
		},
		{
			name: "dynamic payload type with encoding name",
			session: &mockRTPSession{
				payloadType:  97,
				encodingName: "AAC",
				clockRate:    48000,
			},
			expected:    types.CodecAAC,
			description: "should detect from encoding name",
		},
		{
			name: "guess from video clock rate",
			session: &mockRTPSession{
				payloadType: 98,
				clockRate:   90000,
			},
			expected:    types.CodecH264,
			description: "should guess video codec from 90kHz clock rate",
		},
		{
			name: "guess from audio clock rate 48kHz",
			session: &mockRTPSession{
				payloadType: 99,
				clockRate:   48000,
			},
			expected:    types.CodecAAC,
			description: "should guess audio codec from 48kHz clock rate",
		},
		{
			name: "guess from audio clock rate 8kHz",
			session: &mockRTPSession{
				payloadType: 100,
				clockRate:   8000,
			},
			expected:    types.CodecG711,
			description: "should guess G711 from 8kHz clock rate",
		},
		{
			name: "unknown parameters",
			session: &mockRTPSession{
				payloadType: 101,
				clockRate:   12345,
			},
			expected:    types.CodecUnknown,
			description: "should return unknown for unrecognizable parameters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectCodecFromRTPSession(tt.session)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestCodecDetector_Integration(t *testing.T) {
	detector := NewCodecDetector()

	// Test that SDP detection populates payload type mapping
	sdp := `v=0
m=video 5004 RTP/AVP 96
a=rtpmap:96 H264/90000
a=fmtp:96 profile-level-id=42e01e`

	codec, params := detector.DetectFromSDP(sdp)
	assert.Equal(t, types.CodecH264, codec)
	assert.Equal(t, "90000", params["clock_rate"])
	assert.Equal(t, "42e01e", params["profile-level-id"])

	// Now test that RTP packet detection uses the mapping
	packet := &rtp.Packet{
		Header: rtp.Header{PayloadType: 96},
	}
	detectedCodec := detector.DetectFromRTPPacket(packet)
	assert.Equal(t, types.CodecH264, detectedCodec, "should use payload type mapping from SDP")
}
