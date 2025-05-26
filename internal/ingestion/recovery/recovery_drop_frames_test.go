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

// countIFrames counts the number of I-frames in a GOP
func countIFrames(gop *types.GOP) int {
	count := 0
	for _, frame := range gop.Frames {
		if frame.IsKeyframe() {
			count++
		}
	}
	return count
}

// countPFrames counts the number of P-frames in a GOP
func countPFrames(gop *types.GOP) int {
	count := 0
	for _, frame := range gop.Frames {
		if frame.Type == types.FrameTypeP {
			count++
		}
	}
	return count
}

// countBFrames counts the number of B-frames in a GOP
func countBFrames(gop *types.GOP) int {
	count := 0
	for _, frame := range gop.Frames {
		if frame.Type == types.FrameTypeB {
			count++
		}
	}
	return count
}

func TestHandler_DropCorruptedFrames_Persistence(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := gop.BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    1000000,
		MaxDuration: time.Minute,
	}
	buffer := gop.NewBuffer("test-stream", config, logger)

	handler := &Handler{
		streamID:  "test-stream",
		gopBuffer: buffer,
		logger:    logger,
	}

	// Create a GOP with some frames, one of which is corrupted
	testGOP := &types.GOP{
		ID:     1,
		Complete: true,
		Keyframe: &types.VideoFrame{
			ID:          1,
			Type:        types.FrameTypeI,
			CaptureTime: time.Now(),
			TotalSize:   1000,
		},
		Frames: []*types.VideoFrame{
			{
				ID:          1,
				Type:        types.FrameTypeI,
				CaptureTime: time.Now(),
				TotalSize:   1000,
			},
			{
				ID:          2,
				Type:        types.FrameTypeP,
				CaptureTime: time.Now().Add(33 * time.Millisecond),
				TotalSize:   500,
			},
			{
				ID:          3,
				Type:        types.FrameTypeB,
				CaptureTime: time.Now().Add(66 * time.Millisecond),
				TotalSize:   300,
				Flags:       types.FrameFlagCorrupted, // Mark as corrupted
			},
			{
				ID:          4,
				Type:        types.FrameTypeB,
				CaptureTime: time.Now().Add(99 * time.Millisecond),
				TotalSize:   300,
			},
			{
				ID:          5,
				Type:        types.FrameTypeP,
				CaptureTime: time.Now().Add(132 * time.Millisecond),
				TotalSize:   500,
			},
		},
		StreamID:  "test-stream",
		TotalSize: 2600,
	}

	// Add GOP to buffer
	buffer.AddGOP(testGOP)

	// Verify initial state
	assert.Equal(t, 5, len(testGOP.Frames))
	frame3 := buffer.GetFrame(3)
	assert.NotNil(t, frame3)
	assert.True(t, frame3.IsCorrupted())

	// Drop corrupted frames
	droppedFrames := handler.dropCorruptedFrames()

	// Verify frames were dropped
	assert.Equal(t, 3, len(droppedFrames)) // Frames 3, 4, 5 should be dropped
	assert.Equal(t, uint64(3), droppedFrames[0].ID)
	assert.Equal(t, uint64(4), droppedFrames[1].ID)
	assert.Equal(t, uint64(5), droppedFrames[2].ID)

	// Verify changes persisted in the buffer
	frame3After := buffer.GetFrame(3)
	assert.Nil(t, frame3After, "Frame 3 should no longer exist in buffer")

	frame4After := buffer.GetFrame(4)
	assert.Nil(t, frame4After, "Frame 4 should no longer exist in buffer")

	frame5After := buffer.GetFrame(5)
	assert.Nil(t, frame5After, "Frame 5 should no longer exist in buffer")

	// Verify frames 1 and 2 still exist
	frame1 := buffer.GetFrame(1)
	assert.NotNil(t, frame1, "Frame 1 should still exist")

	frame2 := buffer.GetFrame(2)
	assert.NotNil(t, frame2, "Frame 2 should still exist")

	// Verify GOP was updated correctly
	gops := buffer.GetRecentGOPs(1)
	assert.Equal(t, 1, len(gops))
	updatedGOP := gops[0]

	assert.Equal(t, 2, len(updatedGOP.Frames), "GOP should only have 2 frames left")
	assert.Equal(t, int64(1500), updatedGOP.TotalSize) // 1000 + 500
	assert.Equal(t, 1, countIFrames(updatedGOP))
	assert.Equal(t, 1, countPFrames(updatedGOP))
	assert.Equal(t, 0, countBFrames(updatedGOP))
}

func TestHandler_DropCorruptedFrames_MultipleGOPs(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := gop.BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    1000000,
		MaxDuration: time.Minute,
	}
	buffer := gop.NewBuffer("test-stream", config, logger)

	handler := &Handler{
		streamID:  "test-stream",
		gopBuffer: buffer,
		logger:    logger,
	}

	// Create multiple GOPs with corrupted frames
	for i := 1; i <= 3; i++ {
		gopID := uint64(i)
		frames := []*types.VideoFrame{
			{
				ID:          gopID*10 + 1,
				Type:        types.FrameTypeI,
				CaptureTime: time.Now(),
				TotalSize:   1000,
			},
			{
				ID:          gopID*10 + 2,
				Type:        types.FrameTypeP,
				CaptureTime: time.Now().Add(33 * time.Millisecond),
				TotalSize:   500,
			},
		}

		// Add corrupted frame to GOP 2
		if i == 2 {
			frames = append(frames, &types.VideoFrame{
				ID:          gopID*10 + 3,
				Type:        types.FrameTypeB,
				CaptureTime: time.Now().Add(66 * time.Millisecond),
				TotalSize:   300,
				Flags:       types.FrameFlagCorrupted,
			})
			frames = append(frames, &types.VideoFrame{
				ID:          gopID*10 + 4,
				Type:        types.FrameTypeP,
				CaptureTime: time.Now().Add(99 * time.Millisecond),
				TotalSize:   500,
			})
		}

		gop := &types.GOP{
			ID:         gopID,
			Complete:   true,
			Keyframe:   frames[0],
			Frames:     frames,
			StreamID:   "test-stream",
			TotalSize:  1500,
		}

		if i == 2 {
			gop.TotalSize = 2300
		}

		buffer.AddGOP(gop)
	}

	// Drop corrupted frames
	droppedFrames := handler.dropCorruptedFrames()

	// Only GOP 2 should have frames dropped
	assert.Equal(t, 2, len(droppedFrames)) // Frames 23 and 24
	assert.Equal(t, uint64(23), droppedFrames[0].ID)
	assert.Equal(t, uint64(24), droppedFrames[1].ID)

	// Verify only GOP 2 was affected
	gops := buffer.GetRecentGOPs(10)
	assert.Equal(t, 3, len(gops))

	// GOP 1 should be unchanged
	assert.Equal(t, 2, len(gops[0].Frames))

	// GOP 2 should have frames removed
	assert.Equal(t, 2, len(gops[1].Frames))
	assert.Nil(t, buffer.GetFrame(23))
	assert.Nil(t, buffer.GetFrame(24))

	// GOP 3 should be unchanged
	assert.Equal(t, 2, len(gops[2].Frames))
}

func TestHandler_DropCorruptedFrames_EmptyBuffer(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := gop.BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    1000000,
		MaxDuration: time.Minute,
	}
	buffer := gop.NewBuffer("test-stream", config, logger)

	handler := &Handler{
		streamID:  "test-stream",
		gopBuffer: buffer,
		logger:    logger,
	}

	// Drop corrupted frames from empty buffer
	droppedFrames := handler.dropCorruptedFrames()

	// Should return empty slice, not panic
	assert.NotNil(t, droppedFrames)
	assert.Equal(t, 0, len(droppedFrames))
}
