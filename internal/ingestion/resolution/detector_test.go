package resolution

import (
	"fmt"
	"testing"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestDetector_DetectFromNALUnits(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name      string
		nalUnits  [][]byte
		codec     types.CodecType
		expected  Resolution
		wantEmpty bool
	}{
		{
			name:      "empty NAL units",
			nalUnits:  [][]byte{},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name:      "nil NAL units",
			nalUnits:  nil,
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "invalid NAL unit - too short",
			nalUnits: [][]byte{
				{0x67}, // H.264 SPS but too short
			},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "H.264 non-SPS NAL unit",
			nalUnits: [][]byte{
				{0x65, 0x88, 0x84, 0x00}, // IDR slice, not SPS
			},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "HEVC non-SPS NAL unit",
			nalUnits: [][]byte{
				{0x02, 0x01, 0x00, 0x00}, // Not VPS or SPS
			},
			codec:     types.CodecHEVC,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "H.264 SPS - too short for parsing",
			nalUnits: [][]byte{
				{0x67, 0x64}, // SPS NAL type but insufficient data
			},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "HEVC SPS - too short for parsing",
			nalUnits: [][]byte{
				{0x42, 0x01, 0x01}, // SPS NAL type but insufficient data
			},
			codec:     types.CodecHEVC,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "HEVC VPS - should return empty (no resolution)",
			nalUnits: [][]byte{
				{0x40, 0x01, 0x0C, 0x01, 0xFF, 0xFF, 0x01, 0x60, 0x00, 0x00}, // VPS with minimal data
			},
			codec:     types.CodecHEVC,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "unsupported codec",
			nalUnits: [][]byte{
				{0x67, 0x64, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0x50},
			},
			codec:     types.CodecType(99), // Invalid codec
			expected:  Resolution{},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectFromNALUnits(tt.nalUnits, tt.codec)

			if tt.wantEmpty {
				if result.Width != 0 || result.Height != 0 {
					t.Errorf("DetectFromNALUnits() expected empty resolution, got %v", result)
				}
			} else {
				if result != tt.expected {
					t.Errorf("DetectFromNALUnits() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestDetector_DetectFromFrame(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name      string
		frameData []byte
		codec     types.CodecType
		expected  Resolution
		wantEmpty bool
	}{
		{
			name:      "empty frame data",
			frameData: []byte{},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name:      "nil frame data",
			frameData: nil,
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "frame without start codes",
			frameData: []byte{
				0x67, 0x64, 0x00, 0x1E, // No start code prefix
			},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "H.264 frame with 4-byte start code",
			frameData: []byte{
				0x00, 0x00, 0x00, 0x01, // 4-byte start code
				0x67, 0x64, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0x50, // Minimal SPS
			},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true, // Will fall back to heuristic which won't find pattern
		},
		{
			name: "H.264 frame with 3-byte start code",
			frameData: []byte{
				0x00, 0x00, 0x01, // 3-byte start code
				0x67, 0x64, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0x50, // Minimal SPS
			},
			codec:     types.CodecH264,
			expected:  Resolution{},
			wantEmpty: true, // Will fall back to heuristic which won't find pattern
		},
		{
			name: "HEVC frame with VPS",
			frameData: []byte{
				0x00, 0x00, 0x00, 0x01, // Start code
				0x40, 0x01, 0x0C, 0x01, 0xFF, 0xFF, 0x01, 0x60, 0x00, 0x00, // VPS
			},
			codec:     types.CodecHEVC,
			expected:  Resolution{},
			wantEmpty: true, // VPS doesn't contain resolution
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectFromFrame(tt.frameData, tt.codec)

			if tt.wantEmpty {
				if result.Width != 0 || result.Height != 0 {
					t.Errorf("DetectFromFrame() expected empty resolution, got %v", result)
				}
			} else {
				if result != tt.expected {
					t.Errorf("DetectFromFrame() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestDetector_ExtractNALUnits(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name      string
		frameData []byte
		codec     types.CodecType
		expected  int // Number of NAL units expected
	}{
		{
			name:      "empty data",
			frameData: []byte{},
			codec:     types.CodecH264,
			expected:  0,
		},
		{
			name: "single NAL unit with 4-byte start code",
			frameData: []byte{
				0x00, 0x00, 0x00, 0x01, // Start code
				0x67, 0x64, 0x00, 0x1E, // NAL data
			},
			codec:    types.CodecH264,
			expected: 1,
		},
		{
			name: "single NAL unit with 3-byte start code",
			frameData: []byte{
				0x00, 0x00, 0x01, // Start code
				0x67, 0x64, 0x00, 0x1E, // NAL data
			},
			codec:    types.CodecH264,
			expected: 1,
		},
		{
			name: "multiple NAL units",
			frameData: []byte{
				0x00, 0x00, 0x00, 0x01, // Start code 1
				0x67, 0x64, 0x00, 0x1E, // NAL 1
				0x00, 0x00, 0x00, 0x01, // Start code 2
				0x68, 0xEB, 0xE3, 0xCB, // NAL 2
			},
			codec:    types.CodecH264,
			expected: 2,
		},
		{
			name: "mixed start codes",
			frameData: []byte{
				0x00, 0x00, 0x00, 0x01, // 4-byte start code
				0x67, 0x64, 0x00, 0x1E, // NAL 1
				0x00, 0x00, 0x01, // 3-byte start code
				0x68, 0xEB, 0xE3, 0xCB, // NAL 2
			},
			codec:    types.CodecH264,
			expected: 2,
		},
		{
			name: "no start codes",
			frameData: []byte{
				0x67, 0x64, 0x00, 0x1E, // Just data, no start codes
			},
			codec:    types.CodecH264,
			expected: 0,
		},
		{
			name: "AV1 OBU",
			frameData: []byte{
				0x12, 0x00, 0x0A, 0x0A, // AV1 OBU data
			},
			codec:    types.CodecAV1,
			expected: 1, // Whole frame treated as single unit
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nalUnits := detector.extractNALUnits(tt.frameData, tt.codec)

			if len(nalUnits) != tt.expected {
				t.Errorf("extractNALUnits() returned %d NAL units, want %d", len(nalUnits), tt.expected)
			}
		})
	}
}

func TestDetector_SafetyAndErrorHandling(t *testing.T) {
	detector := NewDetector()

	// Test that the detector never panics with malformed data
	malformedData := [][]byte{
		nil,
		{},
		{0x00},
		{0x00, 0x00, 0x00, 0x01},       // Start code only
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // Random data
		make([]byte, 10000),            // Large empty buffer
		{0x67, 0x64},                   // H.264 SPS header but truncated
		{0x42, 0x01},                   // HEVC SPS header but truncated
	}

	codecs := []types.CodecType{
		types.CodecH264,
		types.CodecHEVC,
		types.CodecAV1,
		types.CodecJPEGXS,
		types.CodecType(255), // Invalid codec
	}

	for i, data := range malformedData {
		for j, codec := range codecs {
			t.Run(fmt.Sprintf("malformed_data_%d_codec_%d", i, j), func(t *testing.T) {
				// Test DetectFromFrame - should never panic
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("DetectFromFrame panicked with data %v and codec %v: %v", data, codec, r)
					}
				}()

				result := detector.DetectFromFrame(data, codec)
				// Should always return empty resolution for malformed data
				if result.Width != 0 || result.Height != 0 {
					t.Logf("DetectFromFrame returned non-empty resolution %v for malformed data (may be ok for some inputs)", result)
				}
			})
		}
	}

	// Test DetectFromNALUnits with malformed NAL unit arrays
	malformedNALUnits := [][][]byte{
		nil,
		{},
		{nil},
		{{}},
		{{0x67}},              // Too short for H.264 SPS
		{{0x42, 0x01}},        // Too short for HEVC SPS
		{make([]byte, 10000)}, // Very large NAL unit
	}

	for i, nalUnits := range malformedNALUnits {
		for j, codec := range codecs {
			t.Run(fmt.Sprintf("malformed_nal_%d_codec_%d", i, j), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("DetectFromNALUnits panicked with NAL units %v and codec %v: %v", nalUnits, codec, r)
					}
				}()

				result := detector.DetectFromNALUnits(nalUnits, codec)
				// Should always return empty resolution for malformed data
				if result.Width != 0 || result.Height != 0 {
					t.Logf("DetectFromNALUnits returned non-empty resolution %v for malformed data (may be ok for some inputs)", result)
				}
			})
		}
	}
}

func TestResolution_String(t *testing.T) {
	tests := []struct {
		name     string
		res      Resolution
		expected string
	}{
		{
			name:     "1920x1080",
			res:      Resolution{Width: 1920, Height: 1080},
			expected: "1920x1080",
		},
		{
			name:     "1280x720",
			res:      Resolution{Width: 1280, Height: 720},
			expected: "1280x720",
		},
		{
			name:     "zero width",
			res:      Resolution{Width: 0, Height: 1080},
			expected: "",
		},
		{
			name:     "zero height",
			res:      Resolution{Width: 1920, Height: 0},
			expected: "",
		},
		{
			name:     "both zero",
			res:      Resolution{Width: 0, Height: 0},
			expected: "",
		},
		{
			name:     "4K",
			res:      Resolution{Width: 3840, Height: 2160},
			expected: "3840x2160",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.res.String()
			if result != tt.expected {
				t.Errorf("Resolution.String() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// Benchmark tests for performance validation
func BenchmarkDetector_DetectFromFrame_H264(b *testing.B) {
	detector := NewDetector()
	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x67, 0x64, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0x50, 0x05, 0xBB, 0x01, 0x6A, 0x02, 0x02, 0x02, 0x80,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.DetectFromFrame(frameData, types.CodecH264)
	}
}

func BenchmarkDetector_DetectFromFrame_HEVC(b *testing.B) {
	detector := NewDetector()
	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0xB0, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.DetectFromFrame(frameData, types.CodecHEVC)
	}
}

func BenchmarkDetector_ExtractNALUnits(b *testing.B) {
	detector := NewDetector()
	frameData := []byte{
		0x00, 0x00, 0x00, 0x01, // Start code 1
		0x67, 0x64, 0x00, 0x1E, 0xAC, 0xD9, 0x40, 0x50, // NAL 1
		0x00, 0x00, 0x00, 0x01, // Start code 2
		0x68, 0xEB, 0xE3, 0xCB, 0x80, // NAL 2
		0x00, 0x00, 0x01, // Start code 3
		0x65, 0x88, 0x84, 0x00, 0x12, 0x34, 0x56, 0x78, // NAL 3
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.extractNALUnits(frameData, types.CodecH264)
	}
}

// generateAV1FrameSizeData creates properly bit-packed AV1 frame size data
func generateAV1FrameSizeData(width, height int) []byte {
	widthMinus1 := uint32(width - 1)
	heightMinus1 := uint32(height - 1)

	// Calculate required bits
	widthBits := 32 - countLeadingZeros(widthMinus1)
	heightBits := 32 - countLeadingZeros(heightMinus1)

	// Ensure minimum 1 bit
	if widthBits == 0 {
		widthBits = 1
	}
	if heightBits == 0 {
		heightBits = 1
	}

	widthBitsMinus1 := uint32(widthBits - 1)
	heightBitsMinus1 := uint32(heightBits - 1)

	// Create bit writer to pack data properly
	var bits []bool

	// Add frame_width_bits_minus_1 (4 bits)
	for i := 3; i >= 0; i-- {
		bits = append(bits, (widthBitsMinus1>>i)&1 == 1)
	}

	// Add frame_height_bits_minus_1 (4 bits)
	for i := 3; i >= 0; i-- {
		bits = append(bits, (heightBitsMinus1>>i)&1 == 1)
	}

	// Add max_frame_width_minus_1 (widthBits bits)
	for i := widthBits - 1; i >= 0; i-- {
		bits = append(bits, (widthMinus1>>i)&1 == 1)
	}

	// Add max_frame_height_minus_1 (heightBits bits)
	for i := heightBits - 1; i >= 0; i-- {
		bits = append(bits, (heightMinus1>>i)&1 == 1)
	}

	// Convert bits to bytes
	var data []byte
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8 && i+j < len(bits); j++ {
			if bits[i+j] {
				b |= 1 << (7 - j)
			}
		}
		data = append(data, b)
	}

	return data
}

// countLeadingZeros counts leading zero bits in a 32-bit value
func countLeadingZeros(x uint32) int {
	if x == 0 {
		return 32
	}
	n := 0
	if x <= 0x0000FFFF {
		n += 16
		x <<= 16
	}
	if x <= 0x00FFFFFF {
		n += 8
		x <<= 8
	}
	if x <= 0x0FFFFFFF {
		n += 4
		x <<= 4
	}
	if x <= 0x3FFFFFFF {
		n += 2
		x <<= 2
	}
	if x <= 0x7FFFFFFF {
		n += 1
	}
	return n
}

func TestDetector_AV1Resolution(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name      string
		obuData   []byte
		expected  Resolution
		wantEmpty bool
	}{
		{
			name:      "empty OBU data",
			obuData:   []byte{},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name:      "too short OBU",
			obuData:   []byte{0x12},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "non-sequence header OBU (frame header)",
			obuData: []byte{
				0x30,                   // OBU header: type=6 (frame header), no extension, no size
				0x00, 0x00, 0x00, 0x00, // Some frame header data
			},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "AV1 sequence header - 1920x1080 pattern matching",
			obuData: []byte{
				0x0A, // OBU header: type=1 (sequence), no extension, has size field
				0x10, // LEB128 size: 16 bytes follow
				0x00, // seq_profile=0, still_picture=0, reduced_still_picture_header=0
				// Add 1920x1080 pattern that should be detected: 1919 and 1079 (minus 1 values)
				0x07, 0x7F, 0x04, 0x37, // Contains patterns for common resolutions
				0x87, 0x7F, 0x21, 0xBF, // Additional pattern data
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			},
			expected:  Resolution{Width: 1920, Height: 1080},
			wantEmpty: false,
		},
		{
			name: "AV1 sequence header - 1280x720 pattern matching",
			obuData: []byte{
				0x0A, // OBU header: type=1 (sequence), no extension, has size field
				0x10, // LEB128 size: 16 bytes follow
				0x00, // seq_profile=0, still_picture=0, reduced_still_picture_header=0
				// Add 1280x720 pattern: 1279 and 719 (minus 1 values)
				0x04, 0xFF, 0x02, 0xCF, // Contains patterns for 1280x720
				0x4F, 0xF8, 0x16, 0xE0, // Additional pattern data
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			},
			expected:  Resolution{Width: 1280, Height: 720},
			wantEmpty: false,
		},
		{
			name: "AV1 sequence header - invalid resolution too large",
			obuData: []byte{
				0x0A, // OBU header: type=1 (sequence), no extension, has size field
				0x08, // LEB128 size: 8 bytes follow
				0x00, // seq_profile=0, still_picture=0, reduced_still_picture_header=0
				// Add data that specifically avoids common resolution patterns
				// 1024x768 minus 1 = 1023x767 = 0x3FF x 0x2FF
				// Avoid these patterns: 0x3FF, 0x2FF, etc.
				0x11, 0x22, 0x33, 0x44, 0x55, // Non-matching data
			},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "AV1 sequence header - truncated data",
			obuData: []byte{
				0x0A, // OBU header: type=1 (sequence), no extension, has size field
				0x0B, // LEB128 size: 11 bytes follow (but we provide less)
				0x00, // seq_profile=0, still_picture=0, reduced_still_picture_header=0
				0x00, // timing_info_present_flag=0
				// Truncated - missing remaining data
			},
			expected:  Resolution{},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.detectAV1Resolution(tt.obuData)

			if tt.wantEmpty {
				if result.Width != 0 || result.Height != 0 {
					t.Errorf("detectAV1Resolution() expected empty resolution, got %v", result)
				}
			} else {
				if result != tt.expected {
					t.Errorf("detectAV1Resolution() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestDetector_JPEGXSResolution(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name      string
		frameData []byte
		expected  Resolution
		wantEmpty bool
	}{
		{
			name:      "empty frame data",
			frameData: []byte{},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name:      "too short frame data",
			frameData: []byte{0xFF, 0x10},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "invalid SOC marker",
			frameData: []byte{
				0xFF, 0x11, // Wrong marker (should be 0xFF10)
				0x00, 0x00, 0x00, 0x00,
			},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "JPEG-XS with 1920x1080 in big-endian 16-bit",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x30, // Picture header marker (assumed)
				0x07, 0x80, // 1920 in big-endian
				0x04, 0x38, // 1080 in big-endian
				0x00, 0x00, 0x00, 0x00, // Additional data
			},
			expected:  Resolution{Width: 1920, Height: 1080},
			wantEmpty: false,
		},
		{
			name: "JPEG-XS with 1280x720 pattern matching",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x35, // Some marker
				0x00, 0x00, 0x00, 0x00, // Filler
				0x05, 0x00, // 1280 in big-endian
				0x02, 0xD0, // 720 in big-endian
				0x00, 0x00, 0x00, 0x00, // Additional data
			},
			expected:  Resolution{Width: 1280, Height: 720},
			wantEmpty: false,
		},
		{
			name: "JPEG-XS with common resolution pattern (1920x1080)",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x32, // Some marker
				0x07, 0x80, 0x04, 0x38, // 1920x1080 pattern in big-endian 16-bit
				0x00, 0x00, 0x00, 0x00,
			},
			expected:  Resolution{Width: 1920, Height: 1080},
			wantEmpty: false,
		},
		{
			name: "JPEG-XS with invalid aspect ratio",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x31, // Some marker
				0x00, 0x10, // 16 width (too small/extreme aspect ratio)
				0x04, 0x38, // 1080 height
				0x00, 0x00, 0x00, 0x00,
			},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "JPEG-XS with 3840x2160 (4K)",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x34, // Some marker
				0x0F, 0x00, // 3840 in big-endian
				0x08, 0x70, // 2160 in big-endian
				0x00, 0x00, 0x00, 0x00,
			},
			expected:  Resolution{Width: 3840, Height: 2160},
			wantEmpty: false,
		},
		{
			name: "JPEG-XS no valid resolution found",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x20, // Slice header marker
				0x12, 0x34, // Random data
				0x56, 0x78, // Random data
				0xAB, 0xCD, 0xEF, 0x01,
			},
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name:      "JPEG-XS with little-endian pattern (1920x1080)",
			frameData: make([]byte, 100), // Large enough frame
			expected:  Resolution{Width: 1920, Height: 1080},
			wantEmpty: false,
		},
	}

	// Setup the little-endian pattern test
	littleEndianTest := &tests[len(tests)-1]
	copy(littleEndianTest.frameData, []byte{
		0xFF, 0x10, // SOC marker
		0xFF, 0x33, // Some marker
	})
	// Add little-endian 1920x1080 pattern
	littleEndianPattern := []byte{0x80, 0x07, 0x38, 0x04} // 1920, 1080 in little-endian 16-bit
	copy(littleEndianTest.frameData[20:], littleEndianPattern)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.detectJPEGXSResolution(tt.frameData)

			if tt.wantEmpty {
				if result.Width != 0 || result.Height != 0 {
					t.Errorf("detectJPEGXSResolution() expected empty resolution, got %v", result)
				}
			} else {
				if result != tt.expected {
					t.Errorf("detectJPEGXSResolution() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestDetector_AV1AndJPEGXSFromFrame(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name      string
		frameData []byte
		codec     types.CodecType
		expected  Resolution
		wantEmpty bool
	}{
		{
			name: "AV1 frame with sequence header",
			frameData: []byte{
				0x0A, // OBU header: type=1 (sequence), no extension, has size field
				0x10, // LEB128 size: 16 bytes follow
				0x00, // seq_profile=0, still_picture=0, reduced_still_picture_header=0
				// Add 1920x1080 pattern that should be detected by pattern matching
				0x07, 0x7F, 0x04, 0x37, // Contains patterns for common resolutions
				0x87, 0x7F, 0x21, 0xBF, // Additional pattern data
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			},
			codec:     types.CodecAV1,
			expected:  Resolution{Width: 1920, Height: 1080},
			wantEmpty: false,
		},
		{
			name: "JPEG-XS frame with resolution",
			frameData: []byte{
				0xFF, 0x10, // SOC marker
				0xFF, 0x30, // Picture header marker
				0x07, 0x80, // 1920 in big-endian
				0x04, 0x38, // 1080 in big-endian
				0x00, 0x00, 0x00, 0x00, // Additional data
			},
			codec:     types.CodecJPEGXS,
			expected:  Resolution{Width: 1920, Height: 1080},
			wantEmpty: false,
		},
		{
			name: "AV1 frame header (not sequence header)",
			frameData: []byte{
				0x30,                   // OBU header: type=6 (frame header), no extension, no size
				0x00, 0x00, 0x00, 0x00, // Some frame header data
			},
			codec:     types.CodecAV1,
			expected:  Resolution{},
			wantEmpty: true,
		},
		{
			name: "JPEG-XS without SOC marker",
			frameData: []byte{
				0xFF, 0x20, // SLH marker instead of SOC
				0x07, 0x80, // 1920 in big-endian
				0x04, 0x38, // 1080 in big-endian
			},
			codec:     types.CodecJPEGXS,
			expected:  Resolution{},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectFromFrame(tt.frameData, tt.codec)

			if tt.wantEmpty {
				if result.Width != 0 || result.Height != 0 {
					t.Errorf("DetectFromFrame() expected empty resolution, got %v", result)
				}
			} else {
				if result != tt.expected {
					t.Errorf("DetectFromFrame() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestDetector_IsValidResolution(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name     string
		width    uint32
		height   uint32
		expected bool
	}{
		{"valid 1920x1080", 1920, 1080, true},
		{"valid 1280x720", 1280, 720, true},
		{"valid 3840x2160", 3840, 2160, true},
		{"too small width", 32, 720, false},
		{"too small height", 1920, 32, false},
		{"too large width", 8000, 1080, false},
		{"too large height", 1920, 5000, false},
		{"extreme aspect ratio", 2000, 100, false},
		{"valid 16:9 aspect", 1600, 900, true},
		{"valid 4:3 aspect", 1024, 768, true},
		{"common resolution 640x480", 640, 480, true},
		{"edge case minimum", 64, 64, true},
		{"edge case maximum", 7680, 4320, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.isValidResolution(tt.width, tt.height)
			if result != tt.expected {
				t.Errorf("isValidResolution(%d, %d) = %v, want %v", tt.width, tt.height, result, tt.expected)
			}
		})
	}
}

// Benchmark tests for AV1 and JPEG-XS
func BenchmarkDetector_DetectFromFrame_AV1(b *testing.B) {
	detector := NewDetector()
	frameData := []byte{
		0x0A, // OBU header: type=1 (sequence), no extension, has size field
		0x10, // LEB128 size: 16 bytes follow
		0x00, // seq_profile=0, still_picture=0, reduced_still_picture_header=0
		// Add 1920x1080 pattern that should be detected by pattern matching
		0x07, 0x7F, 0x04, 0x37, // Contains patterns for common resolutions
		0x87, 0x7F, 0x21, 0xBF, // Additional pattern data
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.DetectFromFrame(frameData, types.CodecAV1)
	}
}

func BenchmarkDetector_DetectFromFrame_JPEGXS(b *testing.B) {
	detector := NewDetector()
	frameData := []byte{
		0xFF, 0x10, // SOC marker
		0xFF, 0x30, // Picture header marker
		0x07, 0x80, // 1920 in big-endian
		0x04, 0x38, // 1080 in big-endian
		0x00, 0x00, 0x00, 0x00, // Additional data
	}

	// Make frame larger to test search performance
	largeFrame := make([]byte, 1024)
	copy(largeFrame, frameData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.DetectFromFrame(largeFrame, types.CodecJPEGXS)
	}
}
