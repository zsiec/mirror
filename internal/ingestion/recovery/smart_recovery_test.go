package recovery

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/ingestion/frame"
	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func testSmartLogger() logger.Logger {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	return logger.NewLogrusAdapter(logrus.NewEntry(log))
}

func createTestGOPBuffer() *gop.Buffer {
	config := gop.BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    10 * 1024 * 1024,
		MaxDuration: 30 * time.Second,
	}
	return gop.NewBuffer("test-stream", config, testSmartLogger())
}

func TestNewSmartRecoveryHandler(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime:  5 * time.Second,
			KeyframeTimeout:  2 * time.Second,
			CorruptionWindow: 10,
		},
		EnableAdaptiveGOP:  true,
		EnableFastRecovery: true,
		EnablePreemptive:   true,
		FrameBufferSize:    100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	assert.NotNil(t, handler)
	assert.NotNil(t, handler.idrDetector)
	assert.True(t, handler.adaptiveGOPSize)
	assert.True(t, handler.fastRecovery)
	assert.True(t, handler.preemptiveMode)
	assert.Equal(t, 100, handler.bufferSize)
}

func TestSmartRecoveryHandler_ProcessFrame(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime:  5 * time.Second,
			KeyframeTimeout:  2 * time.Second,
			CorruptionWindow: 10,
		},
		EnablePreemptive: true,
		FrameBufferSize:  10,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Process IDR frame
	idrFrame := &types.VideoFrame{
		FrameNumber: 100,
		Type:        types.FrameTypeIDR,
		NALUnits: []types.NALUnit{
			{Data: []byte{0x65}}, // IDR NAL
		},
	}
	handler.ProcessFrame(idrFrame)

	// Process corrupted frame
	corruptedFrame := &types.VideoFrame{
		FrameNumber: 101,
		Type:        types.FrameTypeP,
		Flags:       types.FrameFlagCorrupted,
	}
	handler.ProcessFrame(corruptedFrame)

	// Check frame buffer
	assert.Len(t, handler.frameBuffer, 2)
	assert.NotNil(t, handler.lastKeyframe)
	assert.Equal(t, uint64(100), handler.lastKeyframe.FrameNumber)
}

func TestSmartRecoveryHandler_ErrorPatternAnalysis(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		EnableAdaptiveGOP:  true,
		EnableFastRecovery: true,
		FrameBufferSize:    100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Simulate repeated errors for pattern detection
	now := time.Now()
	handler.errorHistory = []ErrorEvent{
		{Type: ErrorTypePacketLoss, Timestamp: now.Add(-4 * time.Minute)},
		{Type: ErrorTypePacketLoss, Timestamp: now.Add(-3 * time.Minute)},
		{Type: ErrorTypePacketLoss, Timestamp: now.Add(-2 * time.Minute)},
		{Type: ErrorTypePacketLoss, Timestamp: now.Add(-1 * time.Minute)},
	}

	// Test pattern analysis
	strategy := handler.analyzeErrorPattern()
	assert.Equal(t, RecoveryStrategyAdaptive, strategy) // Repeating pattern with adaptive GOP enabled

	// Test repeating pattern detection
	handler.errorHistory = []ErrorEvent{
		{Type: ErrorTypeCorruption, Timestamp: now.Add(-30 * time.Second)},
		{Type: ErrorTypeCorruption, Timestamp: now.Add(-20 * time.Second)},
		{Type: ErrorTypeCorruption, Timestamp: now.Add(-10 * time.Second)},
		{Type: ErrorTypeCorruption, Timestamp: now},
	}

	hasPattern := handler.hasRepeatingPattern()
	assert.True(t, hasPattern)
}

func TestSmartRecoveryHandler_FastRecovery(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		EnableFastRecovery: true,
		FrameBufferSize:    100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Add frames to buffer including recovery points
	handler.frameBuffer = []*types.VideoFrame{
		{
			FrameNumber: 100,
			Type:        types.FrameTypeIDR,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x65}}, // IDR
			},
		},
		{
			FrameNumber: 110,
			Type:        types.FrameTypeI,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x67, 0x42, 0x00, 0x1f}}, // SPS
				{Data: []byte{0x68}},                   // PPS
				{Data: []byte{0x61, 0x40}},             // I-slice
			},
		},
	}

	// Apply fast recovery
	err := handler.applyFastRecovery(ErrorTypePacketLoss, nil)
	assert.NoError(t, err)
	assert.Equal(t, StateNormal, handler.GetState())
}

func TestSmartRecoveryHandler_AdaptiveRecovery(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		EnableAdaptiveGOP: true,
		FrameBufferSize:   100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Set up IDR detector state
	handler.idrDetector = frame.NewIDRDetector(types.CodecH264, testSmartLogger())
	// Simulate known GOP structure
	for i := uint64(0); i <= 120; i += 30 {
		frame := &types.VideoFrame{
			FrameNumber: i,
			Type:        types.FrameTypeIDR,
			NALUnits: []types.NALUnit{
				{Data: []byte{0x65}},
			},
		}
		handler.idrDetector.IsIDRFrame(frame)
	}

	// Current frame is 125, next keyframe at 150
	handler.frameBuffer = []*types.VideoFrame{
		{FrameNumber: 125, Type: types.FrameTypeP},
	}

	// Apply adaptive recovery
	err := handler.applyAdaptiveRecovery(ErrorTypeTimeout, nil)
	assert.NoError(t, err)
}

func TestSmartRecoveryHandler_PreemptiveRecovery(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		EnablePreemptive: true,
		FrameBufferSize:  100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Set up corruption scenario
	handler.idrDetector.ReportCorruption()
	handler.idrDetector.ReportCorruption()

	// Apply preemptive recovery
	err := handler.applyPreemptiveRecovery(ErrorTypeCorruption, nil)
	assert.NoError(t, err)
}

func TestSmartRecoveryHandler_FrameQualityAssessment(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		FrameBufferSize: 100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	tests := []struct {
		name     string
		frame    *types.VideoFrame
		expected float64
	}{
		{
			name: "Normal frame",
			frame: &types.VideoFrame{
				Type:      types.FrameTypeP,
				TotalSize: 5000,
				PTS:       1000,
				DTS:       900,
				QP:        25,
			},
			expected: 1.0,
		},
		{
			name: "Corrupted frame",
			frame: &types.VideoFrame{
				Type:      types.FrameTypeP,
				TotalSize: 5000,
				Flags:     types.FrameFlagCorrupted,
				PTS:       1000,
				DTS:       900,
			},
			expected: 0.1,
		},
		{
			name: "Invalid timestamps",
			frame: &types.VideoFrame{
				Type:      types.FrameTypeP,
				TotalSize: 5000,
				PTS:       900,
				DTS:       1000, // PTS < DTS
			},
			expected: 0.5,
		},
		{
			name: "High QP",
			frame: &types.VideoFrame{
				Type:      types.FrameTypeP,
				TotalSize: 5000,
				PTS:       1000,
				DTS:       900,
				QP:        45,
			},
			expected: 0.8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quality := handler.assessFrameQuality(tt.frame)
			assert.InDelta(t, tt.expected, quality, 0.01)
		})
	}
}

func TestSmartRecoveryHandler_RecoveryPointSelection(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		FrameBufferSize: 100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	points := []frame.RecoveryPoint{
		{FrameNumber: 100, PTS: 1000, IsIDR: false, Confidence: 0.7},
		{FrameNumber: 110, PTS: 1100, IsIDR: true, Confidence: 1.0},
		{FrameNumber: 120, PTS: 1200, IsIDR: false, Confidence: 0.9},
	}

	best := handler.selectBestRecoveryPoint(points)
	assert.Equal(t, uint64(110), best.FrameNumber)
	assert.True(t, best.IsIDR)

	// Test without IDR
	nonIDRPoints := []frame.RecoveryPoint{
		{FrameNumber: 100, PTS: 1000, IsIDR: false, Confidence: 0.7},
		{FrameNumber: 120, PTS: 1200, IsIDR: false, Confidence: 0.9},
	}

	best = handler.selectBestRecoveryPoint(nonIDRPoints)
	assert.Equal(t, uint64(120), best.FrameNumber)
	assert.Equal(t, 0.9, best.Confidence)
}

func TestSmartRecoveryHandler_Statistics(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		EnableAdaptiveGOP: true,
		FrameBufferSize:   100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Set up some recovery history
	handler.recoveryAttempts = 5
	handler.avgRecoveryTime = 500 * time.Millisecond
	handler.successRate = 0.8
	handler.errorHistory = []ErrorEvent{
		{Type: ErrorTypePacketLoss, Recovered: true},
		{Type: ErrorTypeCorruption, Recovered: true},
		{Type: ErrorTypeTimeout, Recovered: false},
	}

	stats := handler.GetSmartStatistics()
	assert.Equal(t, 5, stats.RecoveryAttempts)
	assert.Equal(t, 500*time.Millisecond, stats.AvgRecoveryTime)
	assert.Equal(t, 0.8, stats.SuccessRate)
	assert.Equal(t, "adaptive", stats.Strategy)
}

func TestSmartRecoveryHandler_MissingReferences(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		FrameBufferSize: 100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Set up frame buffer
	handler.frameBuffer = []*types.VideoFrame{
		{FrameNumber: 100},
		{FrameNumber: 102},
		{FrameNumber: 103},
	}

	// Frame with missing reference
	frame := &types.VideoFrame{
		FrameNumber: 104,
		References:  []uint64{101, 102, 103}, // 101 is missing
	}

	missing := handler.checkMissingReferences(frame)
	assert.Equal(t, 1, missing)
}

func TestSmartRecoveryHandler_UpdateRecoveryMetrics(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime: 5 * time.Second,
		},
		FrameBufferSize: 100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// First recovery
	handler.recoveryCount.Store(1)
	handler.UpdateRecoveryMetrics(200*time.Millisecond, true)
	assert.Equal(t, 200*time.Millisecond, handler.avgRecoveryTime)
	assert.Equal(t, 1.0, handler.successRate)

	// Second recovery
	handler.recoveryCount.Store(2)
	handler.UpdateRecoveryMetrics(300*time.Millisecond, true)
	assert.Equal(t, 250*time.Millisecond, handler.avgRecoveryTime)
	assert.Equal(t, 1.0, handler.successRate)

	// Failed recovery
	handler.recoveryCount.Store(3)
	handler.UpdateRecoveryMetrics(400*time.Millisecond, false)
	assert.InDelta(t, 0.667, handler.successRate, 0.01)
}

// Integration test
func TestSmartRecoveryHandler_Integration(t *testing.T) {
	config := SmartConfig{
		Config: Config{
			MaxRecoveryTime:  5 * time.Second,
			KeyframeTimeout:  2 * time.Second,
			CorruptionWindow: 10,
		},
		EnableAdaptiveGOP:  true,
		EnableFastRecovery: true,
		EnablePreemptive:   true,
		FrameBufferSize:    100,
	}

	gopBuffer := createTestGOPBuffer()
	handler := NewSmartRecoveryHandler("test-stream", config, gopBuffer, types.CodecH264, testSmartLogger())

	// Set up callbacks
	handler.SetCallbacks(
		func(errorType ErrorType) {},
		func(duration time.Duration, success bool) {},
		func() {},
	)

	// Process some frames
	for i := uint64(0); i < 30; i++ {
		frameType := types.FrameTypeP
		if i%30 == 0 {
			frameType = types.FrameTypeIDR
		}

		frame := &types.VideoFrame{
			FrameNumber: i,
			Type:        frameType,
			TotalSize:   5000,
			PTS:         int64(i * 1000),
			DTS:         int64(i * 1000),
		}

		if frameType == types.FrameTypeIDR {
			frame.NALUnits = []types.NALUnit{{Data: []byte{0x65}}}
		}

		handler.ProcessFrame(frame)
	}

	// Simulate error
	err := handler.HandleError(ErrorTypePacketLoss, "test")
	assert.NoError(t, err)

	// Check if recovery was attempted
	assert.Greater(t, handler.recoveryAttempts, 0)
}
