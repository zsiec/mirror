package types

// CodecType represents supported video/audio codecs
type CodecType uint8

const (
	CodecUnknown CodecType = iota
	CodecH264
	CodecHEVC // H.265
	CodecAV1
	CodecVP8
	CodecVP9
	CodecJPEGXS
	// Audio codecs
	CodecAAC
	CodecOpus
	CodecMP3
	CodecPCM
	CodecG711
	CodecG722
	CodecL16
	CodecVorbis
	CodecSpeex
	// Video codecs (legacy)
	CodecH261
	CodecH263
	CodecJPEG
	CodecMPV
	// Container formats
	CodecMP2T
)

// String returns the string representation of CodecType
func (c CodecType) String() string {
	switch c {
	case CodecH264:
		return "h264"
	case CodecHEVC:
		return "hevc"
	case CodecAV1:
		return "av1"
	case CodecVP8:
		return "vp8"
	case CodecVP9:
		return "vp9"
	case CodecJPEGXS:
		return "jpegxs"
	case CodecAAC:
		return "aac"
	case CodecOpus:
		return "opus"
	case CodecMP3:
		return "mp3"
	case CodecPCM:
		return "pcm"
	case CodecG711:
		return "g711"
	case CodecG722:
		return "g722"
	case CodecL16:
		return "l16"
	case CodecVorbis:
		return "vorbis"
	case CodecSpeex:
		return "speex"
	case CodecH261:
		return "h261"
	case CodecH263:
		return "h263"
	case CodecJPEG:
		return "jpeg"
	case CodecMPV:
		return "mpv"
	case CodecMP2T:
		return "mp2t"
	default:
		return "unknown"
	}
}

// IsVideo returns true if this is a video codec
func (c CodecType) IsVideo() bool {
	switch c {
	case CodecH264, CodecHEVC, CodecAV1, CodecVP8, CodecVP9, CodecJPEGXS,
		CodecH261, CodecH263, CodecJPEG, CodecMPV:
		return true
	default:
		return false
	}
}

// IsAudio returns true if this is an audio codec
func (c CodecType) IsAudio() bool {
	switch c {
	case CodecAAC, CodecOpus, CodecMP3, CodecPCM, CodecG711, CodecG722,
		CodecL16, CodecVorbis, CodecSpeex:
		return true
	default:
		return false
	}
}

// GetClockRate returns the typical RTP clock rate for this codec
func (c CodecType) GetClockRate() uint32 {
	switch c {
	case CodecH264, CodecHEVC, CodecAV1, CodecVP8, CodecVP9, CodecJPEGXS:
		return 90000 // 90kHz for video
	case CodecAAC:
		return 48000 // Can vary, but 48kHz is common
	case CodecOpus:
		return 48000
	case CodecMP3:
		return 90000 // MP3 uses 90kHz in RTP
	case CodecPCM:
		return 48000 // Can vary based on sample rate
	case CodecG711:
		return 8000 // G.711 is typically 8kHz
	case CodecG722:
		return 8000 // G.722 RTP clock rate is 8000 per RFC 3551 (historical quirk)
	case CodecL16:
		return 44100 // RFC 3551 Section 4.5.16: L16 default is 44100 Hz
	case CodecVorbis:
		return 48000 // Typically 48kHz
	case CodecSpeex:
		return 16000 // Common Speex rate
	case CodecH261, CodecH263, CodecJPEG, CodecMPV:
		return 90000 // Video codecs use 90kHz
	case CodecMP2T:
		return 90000 // MPEG-TS uses 90kHz
	default:
		return 90000
	}
}

// GetClockRateForPayloadType returns the standard clock rate for static RTP payload types
func GetClockRateForPayloadType(payloadType uint8) uint32 {
	switch payloadType {
	// Audio payload types
	case 0, 8: // PCMU, PCMA (G.711)
		return 8000
	case 3: // GSM
		return 8000
	case 4: // G723
		return 8000
	case 5, 6: // DVI4
		return 8000
	case 7: // LPC
		return 8000
	case 9: // G722
		return 8000 // Note: G722 uses 8kHz RTP clock but samples at 16kHz
	case 10: // L16 stereo
		return 44100
	case 11: // L16 mono
		return 44100
	case 12: // QCELP
		return 8000
	case 13: // CN
		return 8000
	case 14: // MPA (MPEG audio)
		return 90000
	case 15: // G728
		return 8000
	case 16: // DVI4
		return 11025
	case 17: // DVI4
		return 22050
	case 18: // G729
		return 8000

	// Video payload types
	case 25: // CelB
		return 90000
	case 26: // JPEG
		return 90000
	case 28: // nv
		return 90000
	case 31: // H261
		return 90000
	case 32: // MPV (MPEG-1/2 video)
		return 90000
	case 33: // MP2T (MPEG-2 Transport Stream)
		return 90000
	case 34: // H263
		return 90000

	default:
		// Dynamic payload types (96-127) don't have fixed clock rates
		return 0
	}
}
