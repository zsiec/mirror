package codec

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/pion/rtp"
)

// Detector detects video codec from various sources
type Detector struct {
	// RTP payload type to codec mapping
	payloadTypeMap map[uint8]Type
	mu             sync.RWMutex // Protects payloadTypeMap
}

// NewDetector creates a new codec detector
func NewDetector() *Detector {
	return &Detector{
		payloadTypeMap: make(map[uint8]Type),
	}
}

// DetectFromSDP parses SDP and extracts codec information
func (d *Detector) DetectFromSDP(sdp string) (Type, *Info, error) {
	lines := strings.Split(sdp, "\n")
	var codecType Type
	info := &Info{
		Parameters: make(map[string]string),
	}
	
	// Track current media section
	var currentPayloadType uint8
	inVideoSection := false
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Check for video media description
		if strings.HasPrefix(line, "m=video") {
			inVideoSection = true
			continue
		}
		
		// Skip non-video sections
		if strings.HasPrefix(line, "m=") && !strings.HasPrefix(line, "m=video") {
			inVideoSection = false
			continue
		}
		
		if !inVideoSection {
			continue
		}
		
		// Parse rtpmap for codec info
		if strings.HasPrefix(line, "a=rtpmap:") {
			parts := strings.Split(line[9:], " ")
			if len(parts) >= 2 {
				pt, _ := strconv.Atoi(parts[0])
				currentPayloadType = uint8(pt)
				
				codecParts := strings.Split(parts[1], "/")
				if len(codecParts) > 0 {
					codecName := strings.ToUpper(codecParts[0])
					switch codecName {
					case "H264", "H.264":
						codecType = TypeH264
						d.mu.Lock()
						d.payloadTypeMap[currentPayloadType] = TypeH264
						d.mu.Unlock()
					case "H265", "H.265", "HEVC":
						codecType = TypeHEVC
						d.mu.Lock()
						d.payloadTypeMap[currentPayloadType] = TypeHEVC
						d.mu.Unlock()
					case "AV1":
						codecType = TypeAV1
						d.mu.Lock()
						d.payloadTypeMap[currentPayloadType] = TypeAV1
						d.mu.Unlock()
					case "JXSV", "JPEG-XS", "JPEGXS":
						codecType = TypeJPEGXS
						d.mu.Lock()
						d.payloadTypeMap[currentPayloadType] = TypeJPEGXS
						d.mu.Unlock()
					}
					
					// Extract clock rate if available
					if len(codecParts) > 1 {
						info.Parameters["clock_rate"] = codecParts[1]
					}
				}
			}
		}
		
		// Parse fmtp for codec-specific parameters
		if strings.HasPrefix(line, "a=fmtp:") && codecType != TypeUnknown {
			fmtpParts := strings.SplitN(line[7:], " ", 2)
			if len(fmtpParts) == 2 {
				pt, _ := strconv.Atoi(fmtpParts[0])
				if uint8(pt) == currentPayloadType {
					d.parseFmtpParams(codecType, fmtpParts[1], info)
				}
			}
		}
		
		// Parse video attributes
		if strings.HasPrefix(line, "a=framesize:") {
			// Format: a=framesize:PT width-height
			parts := strings.Split(line[12:], " ")
			if len(parts) == 2 {
				dimParts := strings.Split(parts[1], "-")
				if len(dimParts) == 2 {
					info.Width, _ = strconv.Atoi(dimParts[0])
					info.Height, _ = strconv.Atoi(dimParts[1])
				}
			}
		}
		
		if strings.HasPrefix(line, "a=framerate:") {
			// Format: a=framerate:PT fps
			parts := strings.Split(line[12:], " ")
			if len(parts) == 2 {
				info.FrameRate, _ = strconv.ParseFloat(parts[1], 64)
			}
		}
	}
	
	if codecType == TypeUnknown {
		return TypeUnknown, nil, fmt.Errorf("no supported video codec found in SDP")
	}
	
	info.Type = codecType
	return codecType, info, nil
}

// parseFmtpParams parses codec-specific fmtp parameters
func (d *Detector) parseFmtpParams(codecType Type, params string, info *Info) {
	// Split parameters by semicolon
	paramPairs := strings.Split(params, ";")
	for _, pair := range paramPairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		
		switch codecType {
		case TypeH264:
			d.parseH264Params(key, value, info)
		case TypeHEVC:
			d.parseHEVCParams(key, value, info)
		case TypeAV1:
			d.parseAV1Params(key, value, info)
		case TypeJPEGXS:
			d.parseJPEGXSParams(key, value, info)
		}
		
		// Store all parameters
		info.Parameters[key] = value
	}
}

func (d *Detector) parseH264Params(key, value string, info *Info) {
	switch key {
	case "profile-level-id":
		// Extract profile from first byte
		if len(value) >= 2 {
			profileByte, _ := strconv.ParseUint(value[:2], 16, 8)
			switch profileByte {
			case 0x42:
				info.Profile = "baseline"
			case 0x4D:
				info.Profile = "main"
			case 0x58:
				info.Profile = "extended"
			case 0x64:
				info.Profile = "high"
			case 0x6E:
				info.Profile = "high10"
			case 0x7A:
				info.Profile = "high422"
			case 0xF4:
				info.Profile = "high444"
			}
		}
		// Extract level
		if len(value) >= 6 {
			levelByte, _ := strconv.ParseUint(value[4:6], 16, 8)
			info.Level = fmt.Sprintf("%.1f", float64(levelByte)/10.0)
		}
	}
}

func (d *Detector) parseHEVCParams(key, value string, info *Info) {
	switch key {
	case "profile-id":
		switch value {
		case "1":
			info.Profile = "main"
		case "2":
			info.Profile = "main10"
		case "3":
			info.Profile = "mainsp"
		case "4":
			info.Profile = "rext"
		}
	case "level-id":
		// HEVC level = value / 30.0
		levelVal, _ := strconv.Atoi(value)
		info.Level = fmt.Sprintf("%.1f", float64(levelVal)/30.0)
	}
}

func (d *Detector) parseAV1Params(key, value string, info *Info) {
	switch key {
	case "profile":
		info.Profile = value
	case "level-idx":
		// Map level index to level string
		levelIdx, _ := strconv.Atoi(value)
		info.Level = d.av1LevelFromIndex(levelIdx)
	case "tier":
		info.Parameters["tier"] = value
	}
}

func (d *Detector) parseJPEGXSParams(key, value string, info *Info) {
	switch key {
	case "profile":
		info.Profile = value
	case "level":
		info.Level = value
	case "sublevel":
		info.Parameters["sublevel"] = value
	case "depth":
		bitDepth, _ := strconv.Atoi(value)
		info.BitDepth = bitDepth
	case "sampling":
		info.ChromaFmt = value
	}
}

// DetectFromRTPPacket attempts to detect codec from RTP packet
func (d *Detector) DetectFromRTPPacket(packet *rtp.Packet) (Type, error) {
	// First check if we have a mapping from SDP
	d.mu.RLock()
	codecType, ok := d.payloadTypeMap[packet.PayloadType]
	d.mu.RUnlock()
	
	if ok {
		return codecType, nil
	}
	
	// Try to detect from payload patterns (less reliable)
	if len(packet.Payload) < 4 {
		return TypeUnknown, fmt.Errorf("payload too short for detection")
	}
	
	// Check for H.264 NAL unit patterns
	nalType := packet.Payload[0] & 0x1F
	if nalType >= 1 && nalType <= 23 {
		// Likely H.264 single NAL unit
		return TypeH264, nil
	} else if nalType == 24 || nalType == 25 || nalType == 26 || nalType == 27 {
		// H.264 aggregation or fragmentation units
		return TypeH264, nil
	}
	
	// Check for HEVC patterns
	if len(packet.Payload) >= 2 {
		hevcNalType := (packet.Payload[0] >> 1) & 0x3F
		if hevcNalType >= 0 && hevcNalType <= 47 {
			// Possibly HEVC
			return TypeHEVC, nil
		}
	}
	
	// Check for AV1 OBU patterns
	if packet.Payload[0]&0x80 == 0 { // Forbidden bit must be 0
		obuType := (packet.Payload[0] >> 3) & 0x0F
		if obuType >= 1 && obuType <= 8 {
			// Likely AV1 OBU
			return TypeAV1, nil
		}
	}
	
	return TypeUnknown, fmt.Errorf("unable to detect codec from packet")
}

// DetectFromSRTMetadata extracts codec info from SRT metadata
func (d *Detector) DetectFromSRTMetadata(metadata map[string]string) (Type, *Info, error) {
	info := &Info{
		Parameters: make(map[string]string),
	}
	
	// Check for codec in metadata
	codecStr, ok := metadata["codec"]
	if !ok {
		// Try alternative keys
		codecStr, ok = metadata["video_codec"]
		if !ok {
			codecStr, ok = metadata["v_codec"]
		}
	}
	
	if ok {
		info.Type = ParseType(codecStr)
		if info.Type == TypeUnknown {
			return TypeUnknown, nil, fmt.Errorf("unknown codec '%s' - supported codecs: H264, HEVC, AV1, JPEGXS", codecStr)
		}
	} else {
		return TypeUnknown, nil, fmt.Errorf("no codec information in metadata")
	}
	
	// Extract additional parameters
	if profile, ok := metadata["profile"]; ok {
		info.Profile = profile
	}
	if level, ok := metadata["level"]; ok {
		info.Level = level
	}
	if widthStr, ok := metadata["width"]; ok {
		info.Width, _ = strconv.Atoi(widthStr)
	}
	if heightStr, ok := metadata["height"]; ok {
		info.Height, _ = strconv.Atoi(heightStr)
	}
	if fpsStr, ok := metadata["fps"]; ok {
		info.FrameRate, _ = strconv.ParseFloat(fpsStr, 64)
	}
	if depthStr, ok := metadata["bit_depth"]; ok {
		info.BitDepth, _ = strconv.Atoi(depthStr)
	}
	if chromaStr, ok := metadata["chroma"]; ok {
		info.ChromaFmt = chromaStr
	}
	
	// Copy all metadata to parameters
	for k, v := range metadata {
		info.Parameters[k] = v
	}
	
	return info.Type, info, nil
}

// av1LevelFromIndex converts AV1 level index to level string
func (d *Detector) av1LevelFromIndex(idx int) string {
	levels := []string{
		"2.0", "2.1", "2.2", "2.3",
		"3.0", "3.1", "3.2", "3.3",
		"4.0", "4.1", "4.2", "4.3",
		"5.0", "5.1", "5.2", "5.3",
		"6.0", "6.1", "6.2", "6.3",
		"7.0", "7.1", "7.2", "7.3",
	}
	
	if idx >= 0 && idx < len(levels) {
		return levels[idx]
	}
	return fmt.Sprintf("unknown-%d", idx)
}

// Reset clears the detector state
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.payloadTypeMap = make(map[uint8]Type)
}
