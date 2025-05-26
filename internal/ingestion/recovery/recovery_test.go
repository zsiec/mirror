package recovery

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// createRealisticGOP creates a realistic GOP for testing with proper metadata
func createRealisticGOP(id uint64, streamID string) *types.GOP {
	baseTime := time.Now()
	basePTS := int64(90000) // 1 second at 90kHz

	// Create realistic frames
	frames := []*types.VideoFrame{
		{
			ID:          id*10 + 1,
			Type:        types.FrameTypeI,
			CaptureTime: baseTime,
			PTS:         basePTS,
			DTS:         basePTS,
			TotalSize:   15000, // Realistic I-frame size
			QP:          24,
		},
		{
			ID:          id*10 + 2,
			Type:        types.FrameTypeP,
			CaptureTime: baseTime.Add(33 * time.Millisecond),
			PTS:         basePTS + 3000, // 30fps = 3000 PTS units
			DTS:         basePTS + 3000,
			TotalSize:   5000, // Realistic P-frame size
			QP:          26,
		},
		{
			ID:          id*10 + 3,
			Type:        types.FrameTypeB,
			CaptureTime: baseTime.Add(66 * time.Millisecond),
			PTS:         basePTS + 6000,
			DTS:         basePTS + 3000, // B-frame reordering
			TotalSize:   3000,           // Realistic B-frame size
			QP:          28,
		},
	}

	// Create GOP with realistic metadata
	gop := &types.GOP{
		ID:           id,
		StreamID:     streamID,
		Keyframe:     frames[0],
		Frames:       frames,
		StartTime:    baseTime,
		EndTime:      baseTime.Add(66 * time.Millisecond),
		Duration:     basePTS + 6000 - basePTS, // PTS duration
		Complete:     true,
		Closed:       true,
		TotalSize:    23000, // Sum of frame sizes
		PFrameCount:  1,
		BFrameCount:  1,
		IFrameSize:   15000,
		AvgFrameSize: 7666,    // 23000/3
		BitRate:      2784000, // Calculated from size and duration
		Structure: types.GOPStructure{
			Size:    3,
			Pattern: "IBP",
		},
	}

	// Set GOP ID for each frame
	for _, frame := range frames {
		frame.GOPId = id
	}

	return gop
}

func TestHandler_PacketLossRecovery(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Add a complete GOP to buffer
	testGOP := createRealisticGOP(1, "test-stream")
	gopBuffer.AddGOP(testGOP)

	// Test packet loss recovery
	err := handler.HandleError(ErrorTypePacketLoss, 5)
	assert.NoError(t, err)
	assert.Equal(t, StateNormal, handler.GetState())
	assert.Equal(t, uint64(1), handler.recoveryCount.Load())
}

func TestHandler_CorruptionRecovery(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Add GOP with corrupted frame
	testGOP := createRealisticGOP(1, "test-stream")
	// Mark one frame as corrupted
	testGOP.Frames[1].Flags = types.FrameFlagCorrupted
	testGOP.CorruptedFrames = 1
	gopBuffer.AddGOP(testGOP)

	// Add another GOP with good keyframe
	goodGOP := createRealisticGOP(2, "test-stream")
	gopBuffer.AddGOP(goodGOP)

	// Test corruption recovery
	err := handler.HandleError(ErrorTypeCorruption, "frame corruption")
	assert.NoError(t, err)
	// After corruption detection, it should find the good keyframe from GOP 2
	assert.Equal(t, StateNormal, handler.GetState())
	assert.Equal(t, uint64(1), handler.corruptionCount.Load())
}

func TestHandler_TimeoutRecovery(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  100 * time.Millisecond, // Short timeout for test
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Set old keyframe
	oldKeyframe := &types.VideoFrame{
		ID:          1,
		Type:        types.FrameTypeI,
		CaptureTime: time.Now().Add(-2 * time.Second),
	}
	handler.UpdateKeyframe(oldKeyframe)

	// Set callbacks
	keyframeRequested := false
	handler.SetCallbacks(
		func(errorType ErrorType) {
			assert.Equal(t, ErrorTypeTimeout, errorType)
		},
		func(duration time.Duration, success bool) {
			// Recovery should fail due to timeout
			assert.False(t, success)
		},
		func() {
			keyframeRequested = true
		},
	)

	// Test timeout recovery
	err := handler.HandleError(ErrorTypeTimeout, nil)
	assert.NoError(t, err)
	assert.Equal(t, StateResyncing, handler.GetState())
	assert.True(t, keyframeRequested)

	// Wait for timeout
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, StateFailed, handler.GetState())
}

func TestHandler_SequenceGapRecovery(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Test small gap (reordering)
	err := handler.HandleError(ErrorTypeSequenceGap, 3)
	assert.NoError(t, err)
	assert.Equal(t, StateNormal, handler.GetState())

	// Test large gap (packet loss)
	err = handler.HandleError(ErrorTypeSequenceGap, 10)
	assert.NoError(t, err)
	assert.Equal(t, StateRecovering, handler.GetState())
}

func TestHandler_TimestampJumpRecovery(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	keyframeRequested := false
	handler.SetCallbacks(nil, nil, func() {
		keyframeRequested = true
	})

	// Test timestamp jump
	err := handler.HandleError(ErrorTypeTimestampJump, 5*time.Second)
	assert.NoError(t, err)
	assert.Equal(t, StateResyncing, handler.GetState())
	assert.True(t, keyframeRequested)
}

func TestHandler_CodecErrorRecovery(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	keyframeRequested := false
	handler.SetCallbacks(nil, nil, func() {
		keyframeRequested = true
	})

	// Test codec error
	err := handler.HandleError(ErrorTypeCodecError, "invalid NAL unit")
	assert.NoError(t, err)
	assert.Equal(t, StateResyncing, handler.GetState())
	assert.True(t, keyframeRequested)
}

func TestHandler_RecoveryEscalation(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  100 * time.Millisecond, // Short timeout for test
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Start recovery
	handler.setState(StateRecovering)
	handler.lastRecoveryTime.Store(time.Now().Add(-200 * time.Millisecond))

	// Try to handle another error (should escalate)
	err := handler.HandleError(ErrorTypePacketLoss, 5)
	assert.NoError(t, err)
	assert.Equal(t, StateFailed, handler.GetState())
}

func TestHandler_UpdateKeyframe(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Update with non-keyframe (should be ignored)
	nonKeyframe := &types.VideoFrame{
		ID:          1,
		Type:        types.FrameTypeP,
		CaptureTime: time.Now(),
	}
	handler.UpdateKeyframe(nonKeyframe)
	assert.Nil(t, handler.lastKeyframe)

	// Update with keyframe
	keyframe := &types.VideoFrame{
		ID:          2,
		Type:        types.FrameTypeI,
		CaptureTime: time.Now(),
	}
	handler.UpdateKeyframe(keyframe)
	assert.Equal(t, keyframe, handler.lastKeyframe)
}

func TestHandler_Statistics(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  2 * time.Second,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	// Trigger some recoveries
	// First handle packet loss
	handler.HandleError(ErrorTypePacketLoss, 5)
	// Reset state to normal so next errors aren't escalated
	handler.setState(StateNormal)

	// Handle corruption
	handler.HandleError(ErrorTypeCorruption, "test")
	// Reset state again
	handler.setState(StateNormal)

	// Handle timeout
	handler.HandleError(ErrorTypeTimeout, nil)

	stats := handler.GetStatistics()
	// Packet loss increments recovery count
	assert.Equal(t, uint64(1), stats.RecoveryCount)
	// Corruption increments corruption count (not recovery count)
	assert.Equal(t, uint64(1), stats.CorruptionCount)
	// Timeout increments resync count
	assert.Equal(t, uint64(1), stats.ResyncCount)
	// State should be resyncing from the timeout
	assert.Equal(t, StateResyncing, stats.State)
	assert.False(t, stats.IsHealthy)

	// Set to normal state
	handler.setState(StateNormal)
	stats = handler.GetStatistics()
	assert.True(t, stats.IsHealthy)
}

func TestHandler_WaitForKeyframe(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxDuration: 30 * time.Second,
	}
	gopBuffer := gop.NewBuffer("test-stream", gopBufferConfig, logger)

	config := Config{
		MaxRecoveryTime:  5 * time.Second,
		KeyframeTimeout:  200 * time.Millisecond,
		CorruptionWindow: 10,
	}

	handler := NewHandler("test-stream", config, gopBuffer, logger)

	recoveryComplete := make(chan bool)
	handler.SetCallbacks(nil, func(duration time.Duration, success bool) {
		recoveryComplete <- success
	}, nil)

	// Start waiting for keyframe
	go handler.waitForKeyframe()

	// Simulate keyframe arrival after 50ms (well before timeout)
	go func() {
		time.Sleep(50 * time.Millisecond)
		keyframe := &types.VideoFrame{
			ID:          1,
			Type:        types.FrameTypeI,
			CaptureTime: time.Now().Add(10 * time.Millisecond), // Just slightly in future
		}
		handler.UpdateKeyframe(keyframe)
	}()

	// Should succeed
	success := <-recoveryComplete
	assert.True(t, success)
	assert.Equal(t, StateNormal, handler.GetState())
}
