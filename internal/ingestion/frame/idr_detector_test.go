package frame

import (
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func testIDRLogger() logger.Logger {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return log
}

func TestNewIDRDetector(t *testing.T) {
	tests := []struct {
		name  string
		codec types.CodecType
	}{
		{"H.264", types.CodecH264},
		{"HEVC", types.CodecHEVC},
		{"AV1", types.CodecAV1},
		{"JPEG-XS", types.CodecJPEGXS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewIDRDetector(tt.codec, testIDRLogger())
			assert.NotNil(t, detector)
			assert.Equal(t, tt.codec, detector.codec)
			assert.Empty(t, detector.keyframeHistory)
			assert.Equal(t, float64(0), detector.avgGOPSize)
		})
	}
}

func TestIDRDetector_H264IDR(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())

	tests := []struct {
		name     string
		frame    *types.VideoFrame
		expected bool
	}{
		{
			name: "IDR frame",
			frame: &types.VideoFrame{
				FrameNumber: 100,
				Type:        types.FrameTypeIDR,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x65}}, // IDR NAL type = 5, with forbidden_zero_bit=0
				},
			},
			expected: true,
		},
		{
			name: "IDR with SPS and PPS",
			frame: &types.VideoFrame{
				FrameNumber: 200,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x67, 0x42, 0x00, 0x1f}}, // SPS
					{Data: []byte{0x68}},                    // PPS
					{Data: []byte{0x65}},                    // IDR
				},
			},
			expected: true,
		},
		{
			name: "I-slice with SPS/PPS (recovery point)",
			frame: &types.VideoFrame{
				FrameNumber: 300,
				Type:        types.FrameTypeI,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x67, 0x42, 0x00, 0x1f}}, // SPS
					{Data: []byte{0x68}},                    // PPS
					{Data: []byte{0x61, 0xB0}},             // Non-IDR I-slice (first_mb=0, type=2)
				},
			},
			expected: true,
		},
		{
			name: "P-frame",
			frame: &types.VideoFrame{
				FrameNumber: 101,
				Type:        types.FrameTypeP,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x61}}, // Non-IDR slice
				},
			},
			expected: false,
		},
		{
			name: "Recovery point SEI with I-slice",
			frame: &types.VideoFrame{
				FrameNumber: 400,
				Type:        types.FrameTypeI,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x67, 0x42, 0x00, 0x1f}}, // SPS
					{Data: []byte{0x68}},                    // PPS
					{Data: []byte{0x66, 0x06}},             // SEI with recovery point
					{Data: []byte{0x61, 0xB0}},             // I-slice (first_mb=0, type=2)
				},
			},
			expected: true,
		},
		{
			name: "Frame with keyframe flag",
			frame: &types.VideoFrame{
				FrameNumber: 500,
				Flags:       types.FrameFlagKeyframe,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x61}}, // Any NAL
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.IsIDRFrame(tt.frame)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIDRDetector_HEVCIDR(t *testing.T) {
	detector := NewIDRDetector(types.CodecHEVC, testIDRLogger())

	tests := []struct {
		name     string
		frame    *types.VideoFrame
		expected bool
	}{
		{
			name: "IDR frame",
			frame: &types.VideoFrame{
				FrameNumber: 100,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x26, 0x01}}, // IDR_W_RADL (type 19)
				},
			},
			expected: true,
		},
		{
			name: "CRA frame",
			frame: &types.VideoFrame{
				FrameNumber: 200,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x2A, 0x01}}, // CRA_NUT (type 21)
				},
			},
			expected: true,
		},
		{
			name: "BLA frame",
			frame: &types.VideoFrame{
				FrameNumber: 300,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x20, 0x01}}, // BLA_W_LP (type 16)
				},
			},
			expected: true,
		},
		{
			name: "IRAP with parameter sets",
			frame: &types.VideoFrame{
				FrameNumber: 400,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x40, 0x01}}, // VPS (type 32)
					{Data: []byte{0x42, 0x01}}, // SPS (type 33)
					{Data: []byte{0x44, 0x01}}, // PPS (type 34)
					{Data: []byte{0x26, 0x01}}, // IDR
				},
			},
			expected: true,
		},
		{
			name: "Trail frame",
			frame: &types.VideoFrame{
				FrameNumber: 101,
				NALUnits: []types.NALUnit{
					{Data: []byte{0x02, 0x01}}, // TRAIL_R (type 1)
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.IsIDRFrame(tt.frame)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIDRDetector_KeyframeStats(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())

	// Simulate keyframes at regular intervals
	keyframeNumbers := []uint64{0, 30, 60, 90, 120, 150}
	
	for _, frameNum := range keyframeNumbers {
		frame := &types.VideoFrame{
			FrameNumber: frameNum,
			Type:        types.FrameTypeIDR,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x65}}, // IDR
			},
		}
		
		isIDR := detector.IsIDRFrame(frame)
		assert.True(t, isIDR)
	}

	// Check statistics
	assert.Equal(t, uint64(150), detector.lastKeyframe)
	assert.InDelta(t, 30.0, detector.avgGOPSize, 0.1)
	assert.Len(t, detector.keyframeHistory, 5) // 30, 30, 30, 30, 30
}

func TestIDRDetector_PredictNextKeyframe(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())
	
	// Set up known GOP structure
	detector.lastKeyframe = 100
	detector.avgGOPSize = 30.0

	tests := []struct {
		currentFrame uint64
		expected     uint64
	}{
		{105, 130},  // Not due yet
		{125, 130},  // Almost due
		{130, 131},  // Due now
		{140, 141},  // Overdue
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("frame_%d", tt.currentFrame), func(t *testing.T) {
			next := detector.PredictNextKeyframe(tt.currentFrame)
			assert.Equal(t, tt.expected, next)
		})
	}
}

func TestIDRDetector_NeedsKeyframe(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())
	detector.lastKeyframe = 100
	detector.avgGOPSize = 30.0

	tests := []struct {
		name         string
		currentFrame uint64
		hasErrors    bool
		expected     bool
	}{
		{"Normal operation", 120, false, false},
		{"With errors", 110, true, true},
		{"Long without keyframe", 200, false, true},
		{"Suspected corruption", 110, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "Suspected corruption" {
				detector.suspectedCorruption = true
				tt.expected = true
			}
			
			needs := detector.NeedsKeyframe(tt.currentFrame, tt.hasErrors)
			assert.Equal(t, tt.expected, needs)
		})
	}
}

func TestIDRDetector_FindRecoveryPoints(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())

	frames := []*types.VideoFrame{
		{
			FrameNumber: 100,
			PTS:         1000,
			Type:        types.FrameTypeIDR,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x65}}, // IDR
			},
		},
		{
			FrameNumber: 101,
			PTS:         1033,
			Type:        types.FrameTypeP,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x61}}, // P-slice
			},
		},
		{
			FrameNumber: 110,
			PTS:         1333,
			Type:        types.FrameTypeI,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x67, 0x42, 0x00, 0x1f}}, // SPS
				{Data: []byte{0x68}},                    // PPS
				{Data: []byte{0x61, 0x40}},             // I-slice
			},
		},
	}

	points := detector.FindRecoveryPoints(frames)
	require.Len(t, points, 2)

	// First recovery point should be the IDR
	assert.Equal(t, uint64(100), points[0].FrameNumber)
	assert.True(t, points[0].IsIDR)
	assert.Equal(t, 1.0, points[0].Confidence)

	// Second should be the I-frame with parameter sets
	assert.Equal(t, uint64(110), points[1].FrameNumber)
	assert.False(t, points[1].IsIDR)
	assert.True(t, points[1].HasSPS)
	assert.True(t, points[1].HasPPS)
	assert.Equal(t, 1.0, points[1].Confidence)
}

func TestIDRDetector_RecoveryStrategy(t *testing.T) {
	tests := []struct {
		name                string
		avgGOPSize          float64
		suspectedCorruption bool
		missingReferences   int
		expected            RecoveryStrategy
	}{
		{
			name:       "Small GOPs",
			avgGOPSize: 25.0,
			expected:   RecoveryStrategyWaitForKeyframe,
		},
		{
			name:                "Large GOPs with corruption",
			avgGOPSize:          120.0,
			suspectedCorruption: true,
			expected:            RecoveryStrategyRequestKeyframe,
		},
		{
			name:              "Missing references",
			avgGOPSize:        30.0,
			missingReferences: 3,
			expected:          RecoveryStrategyFindRecoveryPoint,
		},
		{
			name:       "Unknown GOP structure",
			avgGOPSize: 0,
			expected:   RecoveryStrategyWaitForKeyframe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewIDRDetector(types.CodecH264, testIDRLogger())
			detector.avgGOPSize = tt.avgGOPSize
			detector.suspectedCorruption = tt.suspectedCorruption
			detector.missingReferences = tt.missingReferences

			strategy := detector.GetRecoveryStrategy()
			assert.Equal(t, tt.expected, strategy)
		})
	}
}

func TestIDRDetector_ReportCorruption(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())
	
	assert.False(t, detector.suspectedCorruption)
	assert.Equal(t, 0, detector.missingReferences)

	detector.ReportCorruption()
	assert.True(t, detector.suspectedCorruption)
	assert.Equal(t, 1, detector.missingReferences)

	detector.ReportCorruption()
	assert.Equal(t, 2, detector.missingReferences)
}

func TestIDRDetector_Clone(t *testing.T) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())
	detector.lastKeyframe = 100
	detector.avgGOPSize = 30.0
	detector.keyframeHistory = []uint64{30, 30, 30}

	clone := detector.Clone()
	
	assert.Equal(t, detector.codec, clone.codec)
	assert.Equal(t, detector.lastKeyframe, clone.lastKeyframe)
	assert.Equal(t, detector.avgGOPSize, clone.avgGOPSize)
	assert.Equal(t, detector.keyframeHistory, clone.keyframeHistory)
	
	// Ensure it's a deep copy
	clone.keyframeHistory[0] = 60
	assert.NotEqual(t, detector.keyframeHistory[0], clone.keyframeHistory[0])
}

func TestIDRDetector_JPEGXSKeyframe(t *testing.T) {
	detector := NewIDRDetector(types.CodecJPEGXS, testIDRLogger())

	frame := &types.VideoFrame{
		FrameNumber: 100,
		Type:        types.FrameTypeI,
		NALUnits: []types.NALUnit{
			{Data: []byte{0xFF, 0x10}}, // JPEG-XS marker
		},
	}

	// JPEG-XS frames are always keyframes
	assert.True(t, detector.IsIDRFrame(frame))
}

// Benchmark for performance testing
func BenchmarkIDRDetector_IsIDRFrame(b *testing.B) {
	detector := NewIDRDetector(types.CodecH264, testIDRLogger())
	
	frame := &types.VideoFrame{
		FrameNumber: 100,
		NALUnits: []types.NALUnit{
			{Data: []byte{0x67, 0x42, 0x00, 0x1f}}, // SPS
			{Data: []byte{0x68}},                    // PPS
			{Data: []byte{0x65}},                    // IDR
			{Data: make([]byte, 1000)},             // Payload
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = detector.IsIDRFrame(frame)
	}
}
