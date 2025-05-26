package types

import (
	"fmt"
	"time"
)

// DecodingContext contains all metadata needed for independent frame processing
type DecodingContext struct {
	// Context identification and versioning
	ID      string    `json:"id"`      // Unique context identifier
	Version int       `json:"version"` // Context format version
	Updated time.Time `json:"updated"` // Last update time

	// Core codec context
	Codec         CodecType              `json:"codec"`          // Primary codec
	ParameterSets map[string][]byte      `json:"parameter_sets"` // SPS, PPS, VPS, etc.
	CodecSpecific map[string]interface{} `json:"codec_specific"` // Codec-specific metadata

	// Visual characteristics
	Video *VideoContext `json:"video,omitempty"` // Video-specific context
	Audio *AudioContext `json:"audio,omitempty"` // Audio-specific context (future)

	// Processing hints
	Processing *ProcessingContext `json:"processing,omitempty"` // Processing hints

	// Stream context
	Stream *StreamContext `json:"stream,omitempty"` // Stream-level context
}

// VideoContext contains video-specific decoding context
type VideoContext struct {
	// Resolution and format
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	PixelFormat  string `json:"pixel_format"`  // YUV420p, YUV444p, etc.
	BitDepth     int    `json:"bit_depth"`     // 8, 10, 12 bits
	ChromaFormat string `json:"chroma_format"` // 4:2:0, 4:2:2, 4:4:4

	// Timing
	FrameRate  Rational `json:"frame_rate"`  // Frames per second
	TimeBase   Rational `json:"time_base"`   // Time base for timestamps
	FieldOrder string   `json:"field_order"` // Progressive, interlaced

	// Color space and HDR
	ColorSpace  *ColorSpace `json:"color_space,omitempty"`  // Color space info
	HDRMetadata *HDRInfo    `json:"hdr_metadata,omitempty"` // HDR metadata

	// Quality and encoding
	Profile       string       `json:"profile"`        // Codec profile
	Level         string       `json:"level"`          // Codec level
	Tier          string       `json:"tier,omitempty"` // HEVC tier
	BitrateLimits *BitrateInfo `json:"bitrate_limits,omitempty"`
}

// AudioContext contains audio-specific context (future extensibility)
type AudioContext struct {
	SampleRate    int    `json:"sample_rate"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channel_layout"`
	SampleFormat  string `json:"sample_format"`
	Codec         string `json:"codec"`
}

// ProcessingContext contains hints for frame processing
type ProcessingContext struct {
	// Transcoding hints
	TargetFormats    []string          `json:"target_formats,omitempty"`    // HLS, DASH, etc.
	QualityLevels    []string          `json:"quality_levels,omitempty"`    // 1080p, 720p, etc.
	TranscodingHints map[string]string `json:"transcoding_hints,omitempty"` // Key-value hints

	// Analysis metadata
	SceneChanges    []int64     `json:"scene_changes,omitempty"`    // PTS of scene changes
	ComplexityScore float64     `json:"complexity_score,omitempty"` // Visual complexity
	MotionVectors   interface{} `json:"motion_vectors,omitempty"`   // Motion vector data

	// Caching strategy
	CacheImportance int    `json:"cache_importance"`         // 0-10 priority
	CacheCategory   string `json:"cache_category,omitempty"` // "keyframe", "frequent", etc.
}

// StreamContext contains stream-level context
type StreamContext struct {
	// Stream identity
	StreamID  string `json:"stream_id"`
	SessionID string `json:"session_id"`
	Origin    string `json:"origin"` // Source identifier

	// Multi-track context
	TrackRole string `json:"track_role"` // "primary", "alternate", etc.
	Language  string `json:"language,omitempty"`
	SyncGroup string `json:"sync_group,omitempty"` // For A/V sync

	// Adaptive streaming
	Bandwidth    int64   `json:"bandwidth"`               // Target bandwidth
	QualityLevel string  `json:"quality_level"`           // Quality tier
	SwitchPoints []int64 `json:"switch_points,omitempty"` // Safe switching points

	// Network context
	Protocol   string          `json:"protocol"`             // SRT, RTP, etc.
	Encryption *EncryptionInfo `json:"encryption,omitempty"` // Encryption context
}

// Supporting types for extensibility

// ColorSpace defines color space information
type ColorSpace struct {
	Primaries      string `json:"primaries"`       // bt709, bt2020, etc.
	Transfer       string `json:"transfer"`        // sRGB, PQ, HLG
	Matrix         string `json:"matrix"`          // bt709, bt2020nc
	Range          string `json:"range"`           // limited, full
	ChromaLocation string `json:"chroma_location"` // left, center, topleft
}

// HDRInfo contains HDR metadata
type HDRInfo struct {
	MaxLuminance     float64     `json:"max_luminance"`               // nits
	MinLuminance     float64     `json:"min_luminance"`               // nits
	MaxCLL           int         `json:"max_cll,omitempty"`           // Maximum Content Light Level
	MaxFALL          int         `json:"max_fall,omitempty"`          // Maximum Frame Average Light Level
	MasteringDisplay interface{} `json:"mastering_display,omitempty"` // Mastering display info
}

// BitrateInfo contains bitrate constraints
type BitrateInfo struct {
	Target  int64 `json:"target"`  // Target bitrate
	Maximum int64 `json:"maximum"` // Maximum bitrate
	Minimum int64 `json:"minimum"` // Minimum bitrate
	Buffer  int64 `json:"buffer"`  // Buffer size
}

// EncryptionInfo contains encryption context
type EncryptionInfo struct {
	Method   string            `json:"method"`             // AES-128, AES-256, etc.
	KeyID    string            `json:"key_id,omitempty"`   // Key identifier
	IV       []byte            `json:"iv,omitempty"`       // Initialization vector
	Metadata map[string]string `json:"metadata,omitempty"` // Additional encryption data
}

// DecodingContextBuilder helps construct contexts incrementally
type DecodingContextBuilder struct {
	context *DecodingContext
}

// NewDecodingContextBuilder creates a new builder
func NewDecodingContextBuilder(codec CodecType, streamID string) *DecodingContextBuilder {
	return &DecodingContextBuilder{
		context: &DecodingContext{
			ID:            generateContextID(codec, streamID),
			Version:       1,
			Updated:       time.Now(),
			Codec:         codec,
			ParameterSets: make(map[string][]byte),
			CodecSpecific: make(map[string]interface{}),
			Stream: &StreamContext{
				StreamID: streamID,
			},
		},
	}
}

// WithParameterSet adds a parameter set (SPS, PPS, VPS, etc.)
func (b *DecodingContextBuilder) WithParameterSet(name string, data []byte) *DecodingContextBuilder {
	b.context.ParameterSets[name] = data
	return b
}

// WithVideo adds video context
func (b *DecodingContextBuilder) WithVideo(video *VideoContext) *DecodingContextBuilder {
	b.context.Video = video
	return b
}

// WithProcessing adds processing hints
func (b *DecodingContextBuilder) WithProcessing(processing *ProcessingContext) *DecodingContextBuilder {
	b.context.Processing = processing
	return b
}

// WithCodecSpecific adds codec-specific metadata
func (b *DecodingContextBuilder) WithCodecSpecific(key string, value interface{}) *DecodingContextBuilder {
	b.context.CodecSpecific[key] = value
	return b
}

// Build returns the constructed context
func (b *DecodingContextBuilder) Build() *DecodingContext {
	return b.context
}

// Helper methods for DecodingContext

// HasParameterSet checks if a parameter set exists
func (dc *DecodingContext) HasParameterSet(name string) bool {
	_, exists := dc.ParameterSets[name]
	return exists
}

// GetParameterSet retrieves a parameter set
func (dc *DecodingContext) GetParameterSet(name string) []byte {
	return dc.ParameterSets[name]
}

// GenerateDecodableStream creates a self-contained stream with all necessary context
func (dc *DecodingContext) GenerateDecodableStream(frame *VideoFrame) []byte {
	var result []byte
	startCode := []byte{0x00, 0x00, 0x00, 0x01}

	// Add parameter sets based on codec
	switch dc.Codec {
	case CodecH264:
		// Add SPS
		if sps := dc.GetParameterSet("sps"); len(sps) > 0 {
			// Validate SPS NAL unit type
			if len(sps) > 0 && (sps[0]&0x1F) == 7 {
				result = append(result, startCode...)
				result = append(result, sps...)
			}
		}
		// Add PPS
		if pps := dc.GetParameterSet("pps"); len(pps) > 0 {
			// Validate PPS NAL unit type
			if len(pps) > 0 && (pps[0]&0x1F) == 8 {
				result = append(result, startCode...)
				result = append(result, pps...)
			}
		}

	case CodecHEVC:
		// Add VPS, SPS, PPS for HEVC
		if vps := dc.GetParameterSet("vps"); len(vps) > 0 {
			if len(vps) > 0 && ((vps[0]>>1)&0x3F) == 32 {
				result = append(result, startCode...)
				result = append(result, vps...)
			}
		}
		if sps := dc.GetParameterSet("sps"); len(sps) > 0 {
			if len(sps) > 0 && ((sps[0]>>1)&0x3F) == 33 {
				result = append(result, startCode...)
				result = append(result, sps...)
			}
		}
		if pps := dc.GetParameterSet("pps"); len(pps) > 0 {
			if len(pps) > 0 && ((pps[0]>>1)&0x3F) == 34 {
				result = append(result, startCode...)
				result = append(result, pps...)
			}
		}

	case CodecAV1:
		// Add sequence header for AV1
		if seqHdr := dc.GetParameterSet("sequence_header"); len(seqHdr) > 0 {
			result = append(result, seqHdr...)
		}
	}

	// Add frame NAL units
	for _, nalUnit := range frame.NALUnits {
		if dc.Codec == CodecH264 || dc.Codec == CodecHEVC {
			result = append(result, startCode...)
		}
		result = append(result, nalUnit.Data...)
	}

	return result
}

// IsDecodable checks if the context has sufficient information for decoding
func (dc *DecodingContext) IsDecodable() bool {
	switch dc.Codec {
	case CodecH264:
		return dc.HasValidParameterSet("sps") && dc.HasValidParameterSet("pps")
	case CodecHEVC:
		return dc.HasValidParameterSet("vps") && dc.HasValidParameterSet("sps") && dc.HasValidParameterSet("pps")
	case CodecAV1:
		return dc.HasParameterSet("sequence_header")
	case CodecJPEGXS:
		return true // JPEG-XS frames are self-contained
	default:
		return false
	}
}

// HasValidParameterSet checks if a parameter set exists and is valid
func (dc *DecodingContext) HasValidParameterSet(name string) bool {
	data := dc.GetParameterSet(name)
	if len(data) == 0 {
		return false
	}

	switch dc.Codec {
	case CodecH264:
		switch name {
		case "sps":
			return len(data) > 0 && (data[0]&0x1F) == 7
		case "pps":
			return len(data) > 0 && (data[0]&0x1F) == 8
		}
	case CodecHEVC:
		switch name {
		case "vps":
			return len(data) > 0 && ((data[0]>>1)&0x3F) == 32
		case "sps":
			return len(data) > 0 && ((data[0]>>1)&0x3F) == 33
		case "pps":
			return len(data) > 0 && ((data[0]>>1)&0x3F) == 34
		}
	}

	return true // For other cases, assume valid if present
}

// GetParameterSetDebugInfo returns debugging information about parameter sets
func (dc *DecodingContext) GetParameterSetDebugInfo() map[string]interface{} {
	info := make(map[string]interface{})

	for name, data := range dc.ParameterSets {
		if len(data) > 0 {
			info[name+"_size"] = len(data)
			info[name+"_first_byte"] = fmt.Sprintf("0x%02x", data[0])

			switch dc.Codec {
			case CodecH264:
				info[name+"_nal_type"] = data[0] & 0x1F
			case CodecHEVC:
				info[name+"_nal_type"] = (data[0] >> 1) & 0x3F
			}

			info[name+"_valid"] = dc.HasValidParameterSet(name)
		} else {
			info[name+"_size"] = 0
			info[name+"_valid"] = false
		}
	}

	return info
}

// Clone creates a deep copy of the context
func (dc *DecodingContext) Clone() *DecodingContext {
	clone := *dc

	// Deep copy parameter sets
	clone.ParameterSets = make(map[string][]byte)
	for k, v := range dc.ParameterSets {
		clone.ParameterSets[k] = make([]byte, len(v))
		copy(clone.ParameterSets[k], v)
	}

	// Deep copy codec specific data
	clone.CodecSpecific = make(map[string]interface{})
	for k, v := range dc.CodecSpecific {
		clone.CodecSpecific[k] = v
	}

	return &clone
}

// generateContextID creates a unique context identifier
func generateContextID(codec CodecType, streamID string) string {
	return fmt.Sprintf("%s-%s-%d", codec.String(), streamID, time.Now().Unix())
}
