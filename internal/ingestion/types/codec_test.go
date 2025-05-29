package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCodecType_String(t *testing.T) {
	tests := []struct {
		name     string
		codec    CodecType
		expected string
	}{
		// Video codecs
		{"H.264", CodecH264, "h264"},
		{"HEVC", CodecHEVC, "hevc"},
		{"AV1", CodecAV1, "av1"},
		{"VP8", CodecVP8, "vp8"},
		{"VP9", CodecVP9, "vp9"},
		{"JPEG-XS", CodecJPEGXS, "jpegxs"},

		// Audio codecs
		{"AAC", CodecAAC, "aac"},
		{"Opus", CodecOpus, "opus"},
		{"MP3", CodecMP3, "mp3"},
		{"PCM", CodecPCM, "pcm"},
		{"G.711", CodecG711, "g711"},
		{"G.722", CodecG722, "g722"},
		{"L16", CodecL16, "l16"},
		{"Vorbis", CodecVorbis, "vorbis"},
		{"Speex", CodecSpeex, "speex"},

		// Legacy video codecs
		{"H.261", CodecH261, "h261"},
		{"H.263", CodecH263, "h263"},
		{"JPEG", CodecJPEG, "jpeg"},
		{"MPV", CodecMPV, "mpv"},

		// Container formats
		{"MP2T", CodecMP2T, "mp2t"},

		// Unknown
		{"Unknown", CodecUnknown, "unknown"},
		{"Invalid", CodecType(255), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.codec.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCodecType_IsVideo(t *testing.T) {
	tests := []struct {
		name     string
		codec    CodecType
		expected bool
	}{
		// Modern video codecs
		{"H.264 is video", CodecH264, true},
		{"HEVC is video", CodecHEVC, true},
		{"AV1 is video", CodecAV1, true},
		{"VP8 is video", CodecVP8, true},
		{"VP9 is video", CodecVP9, true},
		{"JPEG-XS is video", CodecJPEGXS, true},

		// Legacy video codecs
		{"H.261 is video", CodecH261, true},
		{"H.263 is video", CodecH263, true},
		{"JPEG is video", CodecJPEG, true},
		{"MPV is video", CodecMPV, true},

		// Audio codecs should not be video
		{"AAC is not video", CodecAAC, false},
		{"Opus is not video", CodecOpus, false},
		{"MP3 is not video", CodecMP3, false},
		{"PCM is not video", CodecPCM, false},
		{"G.711 is not video", CodecG711, false},
		{"G.722 is not video", CodecG722, false},
		{"L16 is not video", CodecL16, false},
		{"Vorbis is not video", CodecVorbis, false},
		{"Speex is not video", CodecSpeex, false},

		// Unknown/container formats
		{"Unknown is not video", CodecUnknown, false},
		{"MP2T is not video", CodecMP2T, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.codec.IsVideo()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCodecType_IsAudio(t *testing.T) {
	tests := []struct {
		name     string
		codec    CodecType
		expected bool
	}{
		// Audio codecs
		{"AAC is audio", CodecAAC, true},
		{"Opus is audio", CodecOpus, true},
		{"MP3 is audio", CodecMP3, true},
		{"PCM is audio", CodecPCM, true},
		{"G.711 is audio", CodecG711, true},
		{"G.722 is audio", CodecG722, true},
		{"L16 is audio", CodecL16, true},
		{"Vorbis is audio", CodecVorbis, true},
		{"Speex is audio", CodecSpeex, true},

		// Video codecs should not be audio
		{"H.264 is not audio", CodecH264, false},
		{"HEVC is not audio", CodecHEVC, false},
		{"AV1 is not audio", CodecAV1, false},
		{"VP8 is not audio", CodecVP8, false},
		{"VP9 is not audio", CodecVP9, false},
		{"JPEG-XS is not audio", CodecJPEGXS, false},
		{"H.261 is not audio", CodecH261, false},
		{"H.263 is not audio", CodecH263, false},
		{"JPEG is not audio", CodecJPEG, false},
		{"MPV is not audio", CodecMPV, false},

		// Unknown/container formats
		{"Unknown is not audio", CodecUnknown, false},
		{"MP2T is not audio", CodecMP2T, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.codec.IsAudio()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCodecType_GetClockRate(t *testing.T) {
	tests := []struct {
		name     string
		codec    CodecType
		expected uint32
	}{
		// Video codecs (90kHz)
		{"H.264 clock rate", CodecH264, 90000},
		{"HEVC clock rate", CodecHEVC, 90000},
		{"AV1 clock rate", CodecAV1, 90000},
		{"VP8 clock rate", CodecVP8, 90000},
		{"VP9 clock rate", CodecVP9, 90000},
		{"JPEG-XS clock rate", CodecJPEGXS, 90000},
		{"H.261 clock rate", CodecH261, 90000},
		{"H.263 clock rate", CodecH263, 90000},
		{"JPEG clock rate", CodecJPEG, 90000},
		{"MPV clock rate", CodecMPV, 90000},
		{"MP2T clock rate", CodecMP2T, 90000},

		// High-quality audio codecs (48kHz)
		{"AAC clock rate", CodecAAC, 48000},
		{"Opus clock rate", CodecOpus, 48000},
		{"PCM clock rate", CodecPCM, 48000},
		{"L16 clock rate", CodecL16, 48000},
		{"Vorbis clock rate", CodecVorbis, 48000},

		// Telephony audio codecs (8kHz/16kHz)
		{"G.711 clock rate", CodecG711, 8000},
		{"G.722 clock rate", CodecG722, 16000},
		{"Speex clock rate", CodecSpeex, 16000},

		// Special cases
		{"MP3 clock rate", CodecMP3, 90000}, // MP3 uses 90kHz in RTP

		// Unknown/default
		{"Unknown clock rate", CodecUnknown, 90000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.codec.GetClockRate()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetClockRateForPayloadType(t *testing.T) {
	tests := []struct {
		name        string
		payloadType uint8
		expected    uint32
	}{
		// G.711 codecs
		{"PCMU (G.711)", 0, 8000},
		{"PCMA (G.711)", 8, 8000},

		// Other 8kHz audio codecs
		{"GSM", 3, 8000},
		{"G723", 4, 8000},
		{"DVI4-8k", 5, 8000},
		{"DVI4-8k-2", 6, 8000},
		{"LPC", 7, 8000},
		{"G722", 9, 8000}, // Note: 8kHz RTP clock, 16kHz sampling
		{"QCELP", 12, 8000},
		{"CN", 13, 8000},
		{"G728", 15, 8000},
		{"G729", 18, 8000},

		// Higher audio rates
		{"L16 stereo", 10, 44100},
		{"L16 mono", 11, 44100},
		{"DVI4-11k", 16, 11025},
		{"DVI4-22k", 17, 22050},

		// Audio with 90kHz
		{"MPA (MPEG audio)", 14, 90000},

		// Video codecs (90kHz)
		{"CelB", 25, 90000},
		{"JPEG", 26, 90000},
		{"nv", 28, 90000},
		{"H261", 31, 90000},
		{"MPV", 32, 90000},
		{"MP2T", 33, 90000},
		{"H263", 34, 90000},

		// Dynamic payload types (no fixed rate)
		{"Dynamic PT 96", 96, 0},
		{"Dynamic PT 127", 127, 0},
		{"Unknown PT", 50, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetClockRateForPayloadType(tt.payloadType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCodecType_Consistency(t *testing.T) {
	// Test that video and audio classifications are mutually exclusive
	allCodecs := []CodecType{
		CodecUnknown, CodecH264, CodecHEVC, CodecAV1, CodecVP8, CodecVP9, CodecJPEGXS,
		CodecAAC, CodecOpus, CodecMP3, CodecPCM, CodecG711, CodecG722, CodecL16,
		CodecVorbis, CodecSpeex, CodecH261, CodecH263, CodecJPEG, CodecMPV, CodecMP2T,
	}

	for _, codec := range allCodecs {
		t.Run(codec.String(), func(t *testing.T) {
			isVideo := codec.IsVideo()
			isAudio := codec.IsAudio()

			// Codec should not be both video and audio
			assert.False(t, isVideo && isAudio, "Codec %s cannot be both video and audio", codec.String())

			// Known codecs should be either video or audio (except unknown and container formats)
			if codec != CodecUnknown && codec != CodecMP2T {
				assert.True(t, isVideo || isAudio, "Codec %s should be either video or audio", codec.String())
			}

			// Clock rate should always be positive
			clockRate := codec.GetClockRate()
			assert.Greater(t, clockRate, uint32(0), "Clock rate for %s should be positive", codec.String())
		})
	}
}

func TestCodecType_EnumValues(t *testing.T) {
	// Test that codec enum values are unique and sequential
	expectedCodecs := []struct {
		codec CodecType
		value uint8
		name  string
	}{
		{CodecUnknown, 0, "unknown"},
		{CodecH264, 1, "h264"},
		{CodecHEVC, 2, "hevc"},
		{CodecAV1, 3, "av1"},
		{CodecVP8, 4, "vp8"},
		{CodecVP9, 5, "vp9"},
		{CodecJPEGXS, 6, "jpegxs"},
		{CodecAAC, 7, "aac"},
	}

	for _, expected := range expectedCodecs {
		t.Run(expected.name, func(t *testing.T) {
			assert.Equal(t, expected.value, uint8(expected.codec), "Codec %s should have value %d", expected.name, expected.value)
			assert.Equal(t, expected.name, expected.codec.String(), "Codec value %d should stringify to %s", expected.value, expected.name)
		})
	}
}

func TestGetClockRateForPayloadType_EdgeCases(t *testing.T) {
	// Test edge cases and boundary conditions
	tests := []struct {
		name        string
		payloadType uint8
		expected    uint32
	}{
		{"PT 0 (minimum)", 0, 8000},
		{"PT 34 (maximum standard)", 34, 90000},
		{"PT 35 (first unassigned)", 35, 0},
		{"PT 95 (last unassigned)", 95, 0},
		{"PT 96 (first dynamic)", 96, 0},
		{"PT 127 (last dynamic)", 127, 0},
		{"PT 255 (maximum value)", 255, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetClockRateForPayloadType(tt.payloadType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCodecType_RealWorldScenarios(t *testing.T) {
	// Test common real-world codec combinations
	tests := []struct {
		name        string
		videoCodec  CodecType
		audioCodec  CodecType
		expectedVCR uint32 // Video clock rate
		expectedACR uint32 // Audio clock rate
	}{
		{
			name:        "H.264 + AAC",
			videoCodec:  CodecH264,
			audioCodec:  CodecAAC,
			expectedVCR: 90000,
			expectedACR: 48000,
		},
		{
			name:        "HEVC + Opus",
			videoCodec:  CodecHEVC,
			audioCodec:  CodecOpus,
			expectedVCR: 90000,
			expectedACR: 48000,
		},
		{
			name:        "AV1 + Opus",
			videoCodec:  CodecAV1,
			audioCodec:  CodecOpus,
			expectedVCR: 90000,
			expectedACR: 48000,
		},
		{
			name:        "VP9 + Vorbis",
			videoCodec:  CodecVP9,
			audioCodec:  CodecVorbis,
			expectedVCR: 90000,
			expectedACR: 48000,
		},
		{
			name:        "H.264 + G.711",
			videoCodec:  CodecH264,
			audioCodec:  CodecG711,
			expectedVCR: 90000,
			expectedACR: 8000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify video codec properties
			assert.True(t, tt.videoCodec.IsVideo(), "Video codec should be video")
			assert.False(t, tt.videoCodec.IsAudio(), "Video codec should not be audio")
			assert.Equal(t, tt.expectedVCR, tt.videoCodec.GetClockRate(), "Video codec clock rate")

			// Verify audio codec properties
			assert.True(t, tt.audioCodec.IsAudio(), "Audio codec should be audio")
			assert.False(t, tt.audioCodec.IsVideo(), "Audio codec should not be video")
			assert.Equal(t, tt.expectedACR, tt.audioCodec.GetClockRate(), "Audio codec clock rate")

			// Verify both have valid string representations
			assert.NotEmpty(t, tt.videoCodec.String(), "Video codec should have string representation")
			assert.NotEmpty(t, tt.audioCodec.String(), "Audio codec should have string representation")
			assert.NotEqual(t, "unknown", tt.videoCodec.String(), "Video codec should not be unknown")
			assert.NotEqual(t, "unknown", tt.audioCodec.String(), "Audio codec should not be unknown")
		})
	}
}
