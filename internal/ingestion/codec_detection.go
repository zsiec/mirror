package ingestion

import (
	"strconv"
	"strings"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// CodecDetector detects codec from various sources
type CodecDetector struct {
	// RTP payload type to codec mapping
	payloadTypeMap map[uint8]types.CodecType
}

// NewCodecDetector creates a new codec detector
func NewCodecDetector() *CodecDetector {
	return &CodecDetector{
		payloadTypeMap: make(map[uint8]types.CodecType),
	}
}

// DetectFromRTPPacket attempts to detect codec from RTP packet
func (d *CodecDetector) DetectFromRTPPacket(packet *rtp.Packet) types.CodecType {
	if packet == nil {
		return types.CodecUnknown
	}

	// Check static payload types first
	codec := d.detectFromPayloadType(packet.PayloadType)
	if codec != types.CodecUnknown {
		return codec
	}

	// For dynamic payload types, we need SDP or other context
	// For now, make educated guesses based on payload
	if len(packet.Payload) > 0 {
		// Try to detect from payload patterns
		return d.detectFromPayload(packet.Payload, packet.PayloadType)
	}

	return types.CodecUnknown
}

// DetectFromSDP parses SDP and extracts codec information
func (d *CodecDetector) DetectFromSDP(sdp string) (types.CodecType, map[string]string) {
	lines := strings.Split(sdp, "\n")
	codecType := types.CodecUnknown
	params := make(map[string]string)
	
	var currentPayloadType uint8
	var inVideoSection bool
	var inAudioSection bool
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Check for media sections
		if strings.HasPrefix(line, "m=video") {
			inVideoSection = true
			inAudioSection = false
			// Extract payload types from m= line
			parts := strings.Fields(line)
			if len(parts) > 3 {
				for i := 3; i < len(parts); i++ {
					if pt, err := strconv.Atoi(parts[i]); err == nil {
						currentPayloadType = uint8(pt)
						break
					}
				}
			}
			continue
		} else if strings.HasPrefix(line, "m=audio") {
			inVideoSection = false
			inAudioSection = true
			// Extract payload types
			parts := strings.Fields(line)
			if len(parts) > 3 {
				for i := 3; i < len(parts); i++ {
					if pt, err := strconv.Atoi(parts[i]); err == nil {
						currentPayloadType = uint8(pt)
						break
					}
				}
			}
			continue
		} else if strings.HasPrefix(line, "m=") {
			inVideoSection = false
			inAudioSection = false
			continue
		}
		
		// Parse rtpmap for codec info (only in relevant media sections)
		if strings.HasPrefix(line, "a=rtpmap:") && (inVideoSection || inAudioSection) {
			parts := strings.SplitN(line[9:], " ", 2)
			if len(parts) == 2 {
				if pt, err := strconv.Atoi(parts[0]); err == nil && uint8(pt) == currentPayloadType {
					// Parse encoding name and clock rate
					codecParts := strings.Split(parts[1], "/")
					if len(codecParts) > 0 {
						encodingName := codecParts[0]
						detectedCodec := detectCodecFromEncodingName(encodingName)
						if detectedCodec != types.CodecUnknown {
							codecType = detectedCodec
							d.payloadTypeMap[currentPayloadType] = detectedCodec
						}
						
						// Store clock rate if available
						if len(codecParts) > 1 {
							params["clock_rate"] = codecParts[1]
						}
					}
				}
			}
		}
		
		// Parse fmtp for codec parameters
		if strings.HasPrefix(line, "a=fmtp:") {
			parts := strings.SplitN(line[7:], " ", 2)
			if len(parts) == 2 {
				if pt, err := strconv.Atoi(parts[0]); err == nil && uint8(pt) == currentPayloadType {
					// Parse parameters
					fmtpParams := strings.Split(parts[1], ";")
					for _, param := range fmtpParams {
						kv := strings.SplitN(strings.TrimSpace(param), "=", 2)
						if len(kv) == 2 {
							params[kv[0]] = kv[1]
						}
					}
				}
			}
		}
	}
	
	return codecType, params
}

// detectFromPayloadType returns codec based on static RTP payload types
func (d *CodecDetector) detectFromPayloadType(pt uint8) types.CodecType {
	// Check custom mapping first
	if codec, ok := d.payloadTypeMap[pt]; ok {
		return codec
	}
	
	// Static payload types (RFC 3551)
	switch pt {
	// Audio
	case 0:
		return types.CodecG711 // PCMU
	case 8:
		return types.CodecG711 // PCMA
	case 9:
		return types.CodecG722
	case 10, 11:
		return types.CodecL16
	case 14:
		return types.CodecMP3 // MPA
		
	// Video
	case 26:
		return types.CodecJPEG
	case 31:
		return types.CodecH261
	case 32:
		return types.CodecMPV
	case 33:
		return types.CodecMP2T
	case 34:
		return types.CodecH263
		
	default:
		return types.CodecUnknown
	}
}

// detectFromPayload attempts to detect codec from payload patterns
func (d *CodecDetector) detectFromPayload(payload []byte, payloadType uint8) types.CodecType {
	if len(payload) < 1 {
		return types.CodecUnknown
	}
	
	// For dynamic payload types, try to detect from NAL unit patterns
	if payloadType >= 96 && payloadType <= 127 {
		// H.264 detection
		nalType := payload[0] & 0x1F
		if nalType >= 1 && nalType <= 23 {
			return types.CodecH264
		}
		
		// HEVC detection (simple check)
		if len(payload) >= 2 {
			nalType := (payload[0] >> 1) & 0x3F
			if nalType <= 40 { // Valid HEVC NAL types are 0-40
				return types.CodecHEVC
			}
		}
	}
	
	return types.CodecUnknown
}

// AddPayloadTypeMapping adds a custom payload type to codec mapping
func (d *CodecDetector) AddPayloadTypeMapping(payloadType uint8, codec types.CodecType) {
	d.payloadTypeMap[payloadType] = codec
}

// detectCodecFromEncodingName maps SDP encoding names to codec types
func detectCodecFromEncodingName(name string) types.CodecType {
	name = strings.ToUpper(strings.TrimSpace(name))
	
	switch name {
	// Video codecs
	case "H264", "AVC":
		return types.CodecH264
	case "H265", "HEVC":
		return types.CodecHEVC
	case "AV1":
		return types.CodecAV1
	case "VP8":
		return types.CodecVP8
	case "VP9":
		return types.CodecVP9
	case "JPEG-XS", "JPEGXS", "JXS":
		return types.CodecJPEGXS
	case "H261":
		return types.CodecH261
	case "H263", "H263-1998", "H263-2000":
		return types.CodecH263
	case "JPEG":
		return types.CodecJPEG
	case "MPV":
		return types.CodecMPV
		
	// Audio codecs
	case "AAC", "MP4A-LATM", "MPEG4-GENERIC":
		return types.CodecAAC
	case "OPUS":
		return types.CodecOpus
	case "MP3", "MPA":
		return types.CodecMP3
	case "PCMU", "PCMA", "G711":
		return types.CodecG711
	case "G722":
		return types.CodecG722
	case "L16", "L24":
		return types.CodecPCM
	case "VORBIS":
		return types.CodecVorbis
	case "SPEEX":
		return types.CodecSpeex
		
	// Container formats
	case "MP2T":
		return types.CodecMP2T
		
	default:
		return types.CodecUnknown
	}
}


// DetectCodecFromRTPSession is a helper that uses session metadata
func DetectCodecFromRTPSession(session interface{ 
	GetPayloadType() uint8
	GetMediaFormat() string  
	GetEncodingName() string
	GetClockRate() uint32
}) types.CodecType {
	detector := NewCodecDetector()
	
	// Try static payload type first
	payloadType := session.GetPayloadType()
	codec := detector.detectFromPayloadType(payloadType)
	if codec != types.CodecUnknown {
		return codec
	}
	
	// For dynamic payload types, check media format
	if payloadType >= 96 && payloadType <= 127 {
		// Check media format
		if mediaFormat := session.GetMediaFormat(); mediaFormat != "" {
			parts := strings.Split(mediaFormat, "/")
			if len(parts) > 0 {
				return detectCodecFromEncodingName(parts[0])
			}
		}
		
		// Check encoding name
		if encodingName := session.GetEncodingName(); encodingName != "" {
			return detectCodecFromEncodingName(encodingName)
		}
	}
	
	// Make educated guess from clock rate
	clockRate := session.GetClockRate()
	switch clockRate {
	case 90000:
		return types.CodecH264 // Default video codec
	case 48000, 44100, 32000, 16000:
		return types.CodecAAC // Default audio codec
	case 8000:
		return types.CodecG711
	}
	
	return types.CodecUnknown
}
