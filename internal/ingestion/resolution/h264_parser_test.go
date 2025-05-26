package resolution

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestH264SPS_GetResolution tests resolution calculation from SPS parameters
func TestH264SPS_GetResolution(t *testing.T) {
	tests := []struct {
		name     string
		sps      H264SPS
		expected Resolution
	}{
		{
			name: "1920x1080 progressive without cropping",
			sps: H264SPS{
				PicWidthInMbsMinus1:       119,  // (119+1) * 16 = 1920
				PicHeightInMapUnitsMinus1: 67,   // (67+1) * 16 = 1088, but 2 * (67+1) * 16 = 2176 for interlaced
				FrameMbsOnlyFlag:          true, // Progressive
				ChromaFormatIdc:           1,    // 4:2:0
				FrameCroppingFlag:         false,
			},
			expected: Resolution{Width: 1920, Height: 1088}, // No cropping, so height is 1088
		},
		{
			name: "1920x1080 progressive with cropping",
			sps: H264SPS{
				PicWidthInMbsMinus1:       119,  // (119+1) * 16 = 1920
				PicHeightInMapUnitsMinus1: 67,   // (67+1) * 16 = 1088
				FrameMbsOnlyFlag:          true, // Progressive
				ChromaFormatIdc:           1,    // 4:2:0
				FrameCroppingFlag:         true,
				FrameCropLeftOffset:       0,
				FrameCropRightOffset:      0,
				FrameCropTopOffset:        0,
				FrameCropBottomOffset:     4, // Crop 4 * 2 = 8 pixels from bottom (4:2:0 subsampling)
			},
			expected: Resolution{Width: 1920, Height: 1080},
		},
		{
			name: "1280x720 progressive",
			sps: H264SPS{
				PicWidthInMbsMinus1:       79,   // (79+1) * 16 = 1280
				PicHeightInMapUnitsMinus1: 44,   // (44+1) * 16 = 720
				FrameMbsOnlyFlag:          true, // Progressive
				ChromaFormatIdc:           1,    // 4:2:0
				FrameCroppingFlag:         false,
			},
			expected: Resolution{Width: 1280, Height: 720},
		},
		{
			name: "1920x1080 interlaced",
			sps: H264SPS{
				PicWidthInMbsMinus1:       119,   // (119+1) * 16 = 1920
				PicHeightInMapUnitsMinus1: 33,    // (33+1) * 16 = 544, but 2 * (33+1) * 16 = 1088 for interlaced
				FrameMbsOnlyFlag:          false, // Interlaced
				ChromaFormatIdc:           1,     // 4:2:0
				FrameCroppingFlag:         true,
				FrameCropLeftOffset:       0,
				FrameCropRightOffset:      0,
				FrameCropTopOffset:        0,
				FrameCropBottomOffset:     4, // Crop 4 * 4 = 16 pixels (interlaced has different crop unit)
			},
			expected: Resolution{Width: 1920, Height: 1072}, // 1088 - 16 = 1072
		},
		{
			name: "custom resolution with 4:2:2 chroma format",
			sps: H264SPS{
				PicWidthInMbsMinus1:       39,    // (39+1) * 16 = 640
				PicHeightInMapUnitsMinus1: 29,    // (29+1) * 16 = 480
				FrameMbsOnlyFlag:          true,  // Progressive
				ChromaFormatIdc:           2,     // 4:2:2
				FrameCroppingFlag:         true,
				FrameCropLeftOffset:       4,  // Crop 4 * 2 = 8 pixels from left (4:2:2 subsampling)
				FrameCropRightOffset:      4,  // Crop 4 * 2 = 8 pixels from right
				FrameCropTopOffset:        2,  // Crop 2 * 1 = 2 pixels from top
				FrameCropBottomOffset:     2,  // Crop 2 * 1 = 2 pixels from bottom
			},
			expected: Resolution{Width: 624, Height: 476}, // 640 - 16 = 624, 480 - 4 = 476
		},
		{
			name: "4:4:4 chroma format",
			sps: H264SPS{
				PicWidthInMbsMinus1:       59,    // (59+1) * 16 = 960
				PicHeightInMapUnitsMinus1: 53,    // (53+1) * 16 = 864
				FrameMbsOnlyFlag:          true,  // Progressive
				ChromaFormatIdc:           3,     // 4:4:4
				FrameCroppingFlag:         true,
				FrameCropLeftOffset:       2,  // Crop 2 * 1 = 2 pixels from left (4:4:4 subsampling)
				FrameCropRightOffset:      2,  // Crop 2 * 1 = 2 pixels from right
				FrameCropTopOffset:        1,  // Crop 1 * 1 = 1 pixel from top
				FrameCropBottomOffset:     1,  // Crop 1 * 1 = 1 pixel from bottom
			},
			expected: Resolution{Width: 956, Height: 862}, // 960 - 4 = 956, 864 - 2 = 862
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.sps.GetResolution()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestRemoveEmulationPrevention tests emulation prevention byte removal
func TestRemoveEmulationPrevention(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "no emulation prevention",
			input:    []byte{0x01, 0x02, 0x03, 0x04},
			expected: []byte{0x01, 0x02, 0x03, 0x04},
		},
		{
			name:     "single emulation prevention",
			input:    []byte{0x00, 0x00, 0x03, 0x01},
			expected: []byte{0x00, 0x00, 0x01},
		},
		{
			name:     "multiple emulation prevention",
			input:    []byte{0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x03, 0x02},
			expected: []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x02},
		},
		{
			name:     "emulation prevention at end",
			input:    []byte{0x01, 0x02, 0x00, 0x00, 0x03},
			expected: []byte{0x01, 0x02, 0x00, 0x00},
		},
		{
			name:     "false positive (0x03 not after 0x00 0x00)",
			input:    []byte{0x01, 0x03, 0x02, 0x03},
			expected: []byte{0x01, 0x03, 0x02, 0x03},
		},
		{
			name:     "partial pattern (only one 0x00)",
			input:    []byte{0x00, 0x03, 0x01},
			expected: []byte{0x00, 0x03, 0x01},
		},
		{
			name:     "empty input",
			input:    []byte{},
			expected: []byte{},
		},
		{
			name:     "short input",
			input:    []byte{0x00, 0x00},
			expected: []byte{0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeEmulationPrevention(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBoolToInt tests boolean to integer conversion
func TestBoolToInt(t *testing.T) {
	assert.Equal(t, 0, boolToInt(false))
	assert.Equal(t, 1, boolToInt(true))
}

// TestDetector_parseH264SPSFull tests full H.264 SPS parsing
func TestDetector_parseH264SPSFull(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name        string
		spsData     []byte
		expected    Resolution
		expectError bool
	}{
		{
			name:        "empty SPS data",
			spsData:     []byte{},
			expectError: true,
		},
		{
			name:        "too short SPS data",
			spsData:     []byte{0x01, 0x02},
			expectError: true,
		},
		{
			name: "minimal valid SPS - should fail due to insufficient data",
			spsData: []byte{
				0x42, 0x00, 0x0A, // profile_idc=66 (baseline), constraint_set_flags=0x00, level_idc=10
				0x80,             // seq_parameter_set_id=0 (UE)
				0x80,             // log2_max_frame_num_minus4=0 (UE)
				0x80,             // pic_order_cnt_type=0 (UE)
				0x80,             // log2_max_pic_order_cnt_lsb_minus4=0 (UE)
				0x80,             // max_num_ref_frames=0 (UE)
				// insufficient data for full parsing
			},
			expectError: true,
		},
		{
			name: "corrupted SPS data",
			spsData: []byte{
				0x42, 0x00, 0x0A, // profile_idc, constraint_set_flags, level_idc
				0x80,             // seq_parameter_set_id=0
				// Missing required fields
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := detector.parseH264SPSFull(tt.spsData)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestDetector_skipScalingList tests scaling list skipping
func TestDetector_skipScalingList(t *testing.T) {
	detector := NewDetector()

	tests := []struct {
		name        string
		data        []byte
		index       int
		expectError bool
	}{
		{
			name: "skip 4x4 scaling list (index < 6)",
			data: []byte{
				0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, // 16 values
				0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80,
			},
			index:       3,
			expectError: false,
		},
		{
			name: "skip 8x8 scaling list (index >= 6)",
			data: []byte{
				0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, // 64 values (first 8)
				0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, // ... need lots more
				// This would need 64 SE values total, simplified for test
			},
			index:       6,
			expectError: true, // Will fail due to insufficient data
		},
		{
			name:        "insufficient data",
			data:        []byte{0x80},
			index:       0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			err := detector.skipScalingList(br, tt.index)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestH264SPS_ChromaFormatHandling tests chroma format handling in resolution calculation
func TestH264SPS_ChromaFormatHandling(t *testing.T) {
	baseCase := H264SPS{
		PicWidthInMbsMinus1:       79,    // 1280 pixels
		PicHeightInMapUnitsMinus1: 44,    // 720 pixels  
		FrameMbsOnlyFlag:          true,
		FrameCroppingFlag:         true,
		FrameCropLeftOffset:       8,
		FrameCropRightOffset:      8,
		FrameCropTopOffset:        4,
		FrameCropBottomOffset:     4,
	}

	tests := []struct {
		name            string
		chromaFormatIdc uint32
		expectedWidth   int
		expectedHeight  int
	}{
		{
			name:            "4:2:0 chroma format",
			chromaFormatIdc: 1,
			expectedWidth:   1248, // 1280 - (8+8)*2 = 1248
			expectedHeight: 704,   // 720 - (4+4)*2 = 704
		},
		{
			name:            "4:2:2 chroma format", 
			chromaFormatIdc: 2,
			expectedWidth:   1248, // 1280 - (8+8)*2 = 1248
			expectedHeight: 712,   // 720 - (4+4)*1 = 712
		},
		{
			name:            "4:4:4 chroma format",
			chromaFormatIdc: 3,
			expectedWidth:   1264, // 1280 - (8+8)*1 = 1264
			expectedHeight: 712,   // 720 - (4+4)*1 = 712
		},
		{
			name:            "monochrome (4:0:0)",
			chromaFormatIdc: 0,
			expectedWidth:   1264, // 1280 - (8+8)*1 = 1264 (default subWidthC=1)
			expectedHeight: 712,   // 720 - (4+4)*1 = 712
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sps := baseCase
			sps.ChromaFormatIdc = tt.chromaFormatIdc
			
			result := sps.GetResolution()
			assert.Equal(t, tt.expectedWidth, result.Width)
			assert.Equal(t, tt.expectedHeight, result.Height)
		})
	}
}

// TestH264SPS_InterlacedHandling tests interlaced vs progressive handling
func TestH264SPS_InterlacedHandling(t *testing.T) {
	baseCase := H264SPS{
		PicWidthInMbsMinus1:       119, // 1920 pixels
		PicHeightInMapUnitsMinus1: 33,  // 544 map units
		ChromaFormatIdc:           1,   // 4:2:0
		FrameCroppingFlag:         true,
		FrameCropLeftOffset:       0,
		FrameCropRightOffset:      0,
		FrameCropTopOffset:        0,
		FrameCropBottomOffset:     4,
	}

	t.Run("progressive (frame_mbs_only_flag=1)", func(t *testing.T) {
		sps := baseCase
		sps.FrameMbsOnlyFlag = true
		
		result := sps.GetResolution()
		assert.Equal(t, 1920, result.Width)
		// Height = (2 - 1) * (33+1) * 16 = 1 * 34 * 16 = 544
		// Crop = 4 * 2 * 1 = 8
		// Final = 544 - 8 = 536
		assert.Equal(t, 536, result.Height)
	})

	t.Run("interlaced (frame_mbs_only_flag=0)", func(t *testing.T) {
		sps := baseCase
		sps.FrameMbsOnlyFlag = false
		
		result := sps.GetResolution()
		assert.Equal(t, 1920, result.Width)
		// Height = (2 - 0) * (33+1) * 16 = 2 * 34 * 16 = 1088
		// Crop = 4 * 2 * 2 = 16
		// Final = 1088 - 16 = 1072
		assert.Equal(t, 1072, result.Height)
	})
}

// BenchmarkH264SPS_GetResolution benchmarks resolution calculation
func BenchmarkH264SPS_GetResolution(b *testing.B) {
	sps := H264SPS{
		PicWidthInMbsMinus1:       119,
		PicHeightInMapUnitsMinus1: 67,
		FrameMbsOnlyFlag:          true,
		ChromaFormatIdc:           1,
		FrameCroppingFlag:         true,
		FrameCropLeftOffset:       0,
		FrameCropRightOffset:      0,
		FrameCropTopOffset:        0,
		FrameCropBottomOffset:     4,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sps.GetResolution()
	}
}

// BenchmarkRemoveEmulationPrevention benchmarks emulation prevention removal
func BenchmarkRemoveEmulationPrevention(b *testing.B) {
	// Create test data with some emulation prevention bytes
	data := make([]byte, 1000)
	for i := 0; i < len(data)-2; i += 10 {
		data[i] = 0x00
		data[i+1] = 0x00
		data[i+2] = 0x03
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		removeEmulationPrevention(data)
	}
}