package recovery

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestHandler_PacketLossRecovery(t *testing.T) {
	logger := logrus.New()
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
	testGOP := &gop.GOP{
		ID:     1,
		Closed: true,
		Keyframe: &types.VideoFrame{
			ID:          1,
			Type:        types.FrameTypeI,
			CaptureTime: time.Now(),
		},
		Frames: []*types.VideoFrame{
			{ID: 1, Type: types.FrameTypeI},
			{ID: 2, Type: types.FrameTypeP},
			{ID: 3, Type: types.FrameTypeB},
		},
	}
	gopBuffer.AddGOP(testGOP)

	// Test packet loss recovery
	err := handler.HandleError(ErrorTypePacketLoss, 5)
	assert.NoError(t, err)
	assert.Equal(t, StateNormal, handler.GetState())
	assert.Equal(t, uint64(1), handler.recoveryCount.Load())
}

func TestHandler_CorruptionRecovery(t *testing.T) {
	logger := logrus.New()
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
	testGOP := &gop.GOP{
		ID:     1,
		Closed: true, // Mark as closed so it gets added
		Keyframe: &types.VideoFrame{
			ID:          1,
			Type:        types.FrameTypeI,
			CaptureTime: time.Now(),
		},
		Frames: []*types.VideoFrame{
			{ID: 1, Type: types.FrameTypeI},
			{ID: 2, Type: types.FrameTypeP, Flags: types.FrameFlagCorrupted},
			{ID: 3, Type: types.FrameTypeB},
			{ID: 4, Type: types.FrameTypeB},
		},
		FrameCount: 4,
	}
	gopBuffer.AddGOP(testGOP)

	// Add another GOP with good keyframe
	goodGOP := &gop.GOP{
		ID:     2,
		Closed: true, // Mark as closed so it gets added
		Keyframe: &types.VideoFrame{
			ID:          5,
			Type:        types.FrameTypeI,
			CaptureTime: time.Now(),
		},
		Frames: []*types.VideoFrame{
			{ID: 5, Type: types.FrameTypeI},
		},
		FrameCount: 1,
	}
	gopBuffer.AddGOP(goodGOP)

	// Test corruption recovery
	err := handler.HandleError(ErrorTypeCorruption, "frame corruption")
	assert.NoError(t, err)
	// After corruption detection, it should find the good keyframe from GOP 2
	assert.Equal(t, StateNormal, handler.GetState())
	assert.Equal(t, uint64(1), handler.corruptionCount.Load())
}

func TestHandler_TimeoutRecovery(t *testing.T) {
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
	logger := logrus.New()
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
