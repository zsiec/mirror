package gop

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

func createTestGOP(id uint64, frameCount int) *GOP {
	baseTime := time.Now()
	gop := &GOP{
		ID:         id,
		StartPTS:   int64(id * 1000),
		StartTime:  baseTime.Add(time.Duration(id) * 100 * time.Millisecond), // Closer together
		Frames:     make([]*types.VideoFrame, 0, frameCount),
		Closed:     true,
	}
	
	// Add keyframe
	keyframe := &types.VideoFrame{
		ID:          id * 100,
		Type:        types.FrameTypeIDR,
		PTS:         gop.StartPTS,
		TotalSize:   5000,
		CaptureTime: gop.StartTime,
	}
	gop.Frames = append(gop.Frames, keyframe)
	gop.Keyframe = keyframe
	gop.IFrames = 1
	gop.TotalSize = int64(keyframe.TotalSize)
	
	// Add P and B frames
	for i := 1; i < frameCount; i++ {
		frameType := types.FrameTypeP
		if i%3 == 0 {
			frameType = types.FrameTypeB
		}
		
		frame := &types.VideoFrame{
			ID:        id*100 + uint64(i),
			Type:      frameType,
			PTS:       gop.StartPTS + int64(i*33),
			TotalSize: 1000,
		}
		gop.Frames = append(gop.Frames, frame)
		
		if frameType == types.FrameTypeP {
			gop.PFrames++
		} else {
			gop.BFrames++
		}
		gop.TotalSize += int64(frame.TotalSize)
	}
	
	gop.FrameCount = len(gop.Frames)
	gop.Duration = time.Duration(frameCount*33) * time.Millisecond
	
	return gop
}

func TestBuffer_AddGOP(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{
		MaxGOPs:     5,
		MaxBytes:    1000000, // 1MB to avoid byte limit
		MaxDuration: 0, // No duration limit
	}
	
	buffer := NewBuffer("test-stream", config, logger)
	
	// Add first GOP
	gop1 := createTestGOP(1, 30)
	buffer.AddGOP(gop1)
	
	stats := buffer.GetStatistics()
	assert.Equal(t, 1, stats.GOPCount)
	assert.Equal(t, 30, stats.FrameCount)
	assert.Equal(t, uint64(1), stats.TotalGOPs)
	
	// Add more GOPs
	for i := uint64(2); i <= 3; i++ {
		buffer.AddGOP(createTestGOP(i, 30))
	}
	
	stats = buffer.GetStatistics()
	assert.Equal(t, 3, stats.GOPCount)
	assert.Equal(t, 90, stats.FrameCount)
}

func TestBuffer_EnforceLimits(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{
		MaxGOPs:     3,
		MaxBytes:    500000, // Increased to avoid byte limit triggering
		MaxDuration: 5 * time.Second,
	}
	
	buffer := NewBuffer("test-stream", config, logger)
	
	// Add GOPs beyond limit
	for i := uint64(1); i <= 5; i++ {
		buffer.AddGOP(createTestGOP(i, 30))
	}
	
	// Should only keep last 3 GOPs
	stats := buffer.GetStatistics()
	assert.Equal(t, 3, stats.GOPCount)
	assert.Equal(t, uint64(5), stats.TotalGOPs)
	assert.Equal(t, uint64(2), stats.DroppedGOPs)
	
	// Verify oldest GOP is #3
	gops := buffer.GetRecentGOPs(10)
	assert.Equal(t, uint64(3), gops[0].ID)
}

func TestBuffer_GetFrame(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{MaxGOPs: 5}
	buffer := NewBuffer("test-stream", config, logger)
	
	gop := createTestGOP(1, 10)
	buffer.AddGOP(gop)
	
	// Get existing frame
	frame := buffer.GetFrame(105) // Frame 5 in GOP 1
	require.NotNil(t, frame)
	assert.Equal(t, uint64(105), frame.ID)
	
	// Get non-existent frame
	frame = buffer.GetFrame(999)
	assert.Nil(t, frame)
}

func TestBuffer_DropFramesForPressure(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{MaxGOPs: 5}
	buffer := NewBuffer("test-stream", config, logger)
	
	// Add several GOPs
	for i := uint64(1); i <= 3; i++ {
		buffer.AddGOP(createTestGOP(i, 30))
	}
	
	initialStats := buffer.GetStatistics()
	initialFrames := initialStats.FrameCount
	
	// Test low pressure - no dropping
	dropped := buffer.DropFramesForPressure(0.3)
	assert.Empty(t, dropped)
	
	// Test medium pressure - drop B frames
	dropped = buffer.DropFramesForPressure(0.6)
	assert.NotEmpty(t, dropped)
	for _, frame := range dropped {
		assert.Equal(t, types.FrameTypeB, frame.Type)
	}
	
	// Verify B frames were dropped
	stats := buffer.GetStatistics()
	assert.Less(t, stats.FrameCount, initialFrames)
	assert.Less(t, stats.BFrames, initialStats.BFrames)
	
	// Test high pressure - drop P frames too
	dropped = buffer.DropFramesForPressure(0.8)
	assert.NotEmpty(t, dropped)
	
	hasP := false
	for _, frame := range dropped {
		if frame.Type == types.FrameTypeP {
			hasP = true
			break
		}
	}
	assert.True(t, hasP, "Should have dropped some P frames")
}

func TestBuffer_DropStrategies(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{MaxGOPs: 5}
	buffer := NewBuffer("test-stream", config, logger)
	
	// Create a GOP with specific structure for testing
	gop := &GOP{
		ID:        1,
		StartPTS:  1000,
		StartTime: time.Now(),
		Closed:    true,
		Frames:    make([]*types.VideoFrame, 0),
	}
	
	// Add frames: I B B P B B P
	frames := []struct {
		id        uint64
		frameType types.FrameType
	}{
		{100, types.FrameTypeIDR}, // 0
		{101, types.FrameTypeB},   // 1
		{102, types.FrameTypeB},   // 2
		{103, types.FrameTypeP},   // 3
		{104, types.FrameTypeB},   // 4
		{105, types.FrameTypeB},   // 5
		{106, types.FrameTypeP},   // 6
	}
	
	for i, f := range frames {
		frame := &types.VideoFrame{
			ID:        f.id,
			Type:      f.frameType,
			PTS:       int64(1000 + i*33),
			TotalSize: 1000,
		}
		gop.Frames = append(gop.Frames, frame)
		
		if i == 0 {
			gop.Keyframe = frame
			gop.IFrames = 1
		} else if f.frameType == types.FrameTypeP {
			gop.PFrames++
		} else {
			gop.BFrames++
		}
	}
	
	gop.FrameCount = len(gop.Frames)
	gop.TotalSize = int64(len(gop.Frames) * 1000)
	
	buffer.AddGOP(gop)
	
	// Test B frame dropping
	dropped := buffer.dropBFrames(1)
	assert.Equal(t, 4, len(dropped)) // All B frames
	for _, frame := range dropped {
		assert.Equal(t, types.FrameTypeB, frame.Type)
	}
	
	// Verify GOP structure after dropping
	remainingGOP := buffer.GetGOP(1)
	assert.Equal(t, 3, remainingGOP.FrameCount) // I + 2P
	assert.Equal(t, 0, remainingGOP.BFrames)
}

func TestBuffer_ExtremePresssure(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{MaxGOPs: 5}
	buffer := NewBuffer("test-stream", config, logger)
	
	// Add multiple GOPs
	for i := uint64(1); i <= 3; i++ {
		buffer.AddGOP(createTestGOP(i, 30))
	}
	
	// Test extreme pressure - should drop entire GOPs
	dropped := buffer.DropFramesForPressure(0.96)
	assert.NotEmpty(t, dropped)
	
	// Should have dropped frames from at least one GOP
	stats := buffer.GetStatistics()
	assert.Greater(t, stats.DroppedFrames, uint64(0))
}

func TestBuffer_Clear(t *testing.T) {
	logger := logrus.New()
	config := BufferConfig{MaxGOPs: 5}
	buffer := NewBuffer("test-stream", config, logger)
	
	// Add GOPs
	for i := uint64(1); i <= 3; i++ {
		buffer.AddGOP(createTestGOP(i, 30))
	}
	
	// Clear buffer
	buffer.Clear()
	
	stats := buffer.GetStatistics()
	assert.Equal(t, 0, stats.GOPCount)
	assert.Equal(t, 0, stats.FrameCount)
	assert.Equal(t, int64(0), stats.TotalBytes)
}
