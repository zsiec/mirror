package gop

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func TestGOP_UpdateDuration_AfterDropping(t *testing.T) {
	tests := []struct {
		name             string
		setupGOP         func() *types.GOP
		dropFrames       func(g *types.GOP)
		expectedStartPTS int64
		expectedEndPTS   int64
		expectedDuration int64
	}{
		{
			name: "drop_last_frames",
			setupGOP: func() *types.GOP {
				return &types.GOP{
					ID:       1,
					StartPTS: 0,
					EndPTS:   90000, // 1 second worth
					Duration: 90000, // 1 second worth in PTS units
					Frames: []*types.VideoFrame{
						{ID: 1, PTS: 0, Type: types.FrameTypeI, TotalSize: 1000},
						{ID: 2, PTS: 3000, Type: types.FrameTypeP, TotalSize: 500},
						{ID: 3, PTS: 6000, Type: types.FrameTypeB, TotalSize: 300},
						{ID: 4, PTS: 9000, Type: types.FrameTypeB, TotalSize: 300},
						{ID: 5, PTS: 90000, Type: types.FrameTypeP, TotalSize: 500},
					},
					StreamID:  "test-stream",
					Complete:  true,
					TotalSize: 2600,
				}
			},
			dropFrames: func(g *types.GOP) {
				// Drop last 2 frames
				g.Frames = g.Frames[:3]
				// FrameCount is now len(g.Frames) - automatically updated
				g.TotalSize = 1800
			},
			expectedStartPTS: 0,
			expectedEndPTS:   6000,
			expectedDuration: 6000, // 6000 PTS units
		},
		{
			name: "drop_all_but_keyframe",
			setupGOP: func() *types.GOP {
				return &types.GOP{
					ID:       2,
					StartPTS: 1000,
					EndPTS:   10000,
					Duration: 9000, // 100ms worth in PTS units
					Frames: []*types.VideoFrame{
						{ID: 1, PTS: 1000, Type: types.FrameTypeI, TotalSize: 1000},
						{ID: 2, PTS: 4000, Type: types.FrameTypeP, TotalSize: 500},
						{ID: 3, PTS: 7000, Type: types.FrameTypeB, TotalSize: 300},
						{ID: 4, PTS: 10000, Type: types.FrameTypeP, TotalSize: 500},
					},
					StreamID:  "test-stream",
					Complete:  true,
					TotalSize: 2300,
				}
			},
			dropFrames: func(g *types.GOP) {
				// Keep only keyframe
				g.Frames = g.Frames[:1]
				// FrameCount is now len(g.Frames) - automatically updated
				g.TotalSize = 1000
			},
			expectedStartPTS: 1000,
			expectedEndPTS:   1000,
			expectedDuration: 0,
		},
		{
			name: "drop_all_frames",
			setupGOP: func() *types.GOP {
				return &types.GOP{
					ID:       3,
					StartPTS: 0,
					EndPTS:   5000,
					Duration: 5000, // PTS units
					Frames: []*types.VideoFrame{
						{ID: 1, PTS: 0, Type: types.FrameTypeI, TotalSize: 1000},
						{ID: 2, PTS: 5000, Type: types.FrameTypeP, TotalSize: 500},
					},
					StreamID:  "test-stream",
					Complete:  true,
					TotalSize: 1500,
				}
			},
			dropFrames: func(g *types.GOP) {
				// Drop all frames
				g.Frames = g.Frames[:0]
				// FrameCount is now len(g.Frames) - automatically updated
				g.TotalSize = 0
			},
			expectedStartPTS: 0,
			expectedEndPTS:   0,
			expectedDuration: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup GOP
			gop := tt.setupGOP()

			// Drop frames
			tt.dropFrames(gop)

			// Update duration
			gop.UpdateDuration()

			// Verify
			assert.Equal(t, tt.expectedStartPTS, gop.StartPTS)
			assert.Equal(t, tt.expectedEndPTS, gop.EndPTS)
			assert.Equal(t, tt.expectedDuration, gop.Duration)

			// Verify bitrate calculation
			if gop.Duration > 0 {
				assert.Greater(t, gop.BitRate, int64(0))
			} else {
				assert.Equal(t, int64(0), gop.BitRate)
			}
		})
	}
}

// TestBuffer_DropFrames_UpdatesDuration tests that buffer updates GOP duration when dropping frames
func TestBuffer_DropFrames_UpdatesDuration(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    100000,
		MaxDuration: time.Minute,
	}
	buffer := NewBuffer("test-stream", config, logger)

	// Create a GOP with B frames
	gop := &types.GOP{
		ID:       1,
		Closed:   true,
		StartPTS: 0,
		EndPTS:   9000,
		Duration: 9000, // PTS units
		Keyframe: &types.VideoFrame{
			ID:          1,
			Type:        types.FrameTypeI,
			PTS:         0,
			TotalSize:   1000,
			CaptureTime: time.Now(),
		},
		Frames: []*types.VideoFrame{
			{ID: 1, Type: types.FrameTypeI, PTS: 0, TotalSize: 1000, CaptureTime: time.Now()},
			{ID: 2, Type: types.FrameTypeP, PTS: 3000, TotalSize: 500, CaptureTime: time.Now()},
			{ID: 3, Type: types.FrameTypeB, PTS: 6000, TotalSize: 300, CaptureTime: time.Now()},
			{ID: 4, Type: types.FrameTypeB, PTS: 9000, TotalSize: 300, CaptureTime: time.Now()},
		},
		StreamID:    "test-stream",
		Complete:    true,
		BFrameCount: 2,
		PFrameCount: 1,
		TotalSize:   2100,
	}

	// Add GOP to buffer
	buffer.AddGOP(gop)

	// Verify initial state
	assert.Equal(t, int64(9000), gop.Duration)
	assert.Equal(t, int64(9000), gop.EndPTS)

	// Drop B frames (should trigger duration update)
	dropped := buffer.dropBFrames(1)

	// Verify frames were dropped
	assert.Equal(t, 2, len(dropped))
	assert.Equal(t, 2, len(gop.Frames))

	// Verify duration was updated
	expectedDuration := int64(3000) // 3000 PTS units
	assert.Equal(t, expectedDuration, gop.Duration)
	assert.Equal(t, int64(3000), gop.EndPTS) // End PTS should be updated
	assert.Equal(t, int64(0), gop.StartPTS)  // Start PTS should remain 0
}
