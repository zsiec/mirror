package gop

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestGOP_UpdateDuration_AfterDropping(t *testing.T) {
	tests := []struct {
		name            string
		setupGOP        func() *GOP
		dropFrames      func(g *GOP)
		expectedStartPTS int64
		expectedEndPTS   int64
		expectedDuration time.Duration
	}{
		{
			name: "drop_last_frames",
			setupGOP: func() *GOP {
				return &GOP{
					ID:       1,
					StartPTS: 0,
					EndPTS:   90000, // 1 second worth
					Duration: time.Second,
					Frames: []*types.VideoFrame{
						{ID: 1, PTS: 0, Type: types.FrameTypeI, TotalSize: 1000},
						{ID: 2, PTS: 3000, Type: types.FrameTypeP, TotalSize: 500},
						{ID: 3, PTS: 6000, Type: types.FrameTypeB, TotalSize: 300},
						{ID: 4, PTS: 9000, Type: types.FrameTypeB, TotalSize: 300},
						{ID: 5, PTS: 90000, Type: types.FrameTypeP, TotalSize: 500},
					},
					FrameCount: 5,
					TotalSize:  2600,
				}
			},
			dropFrames: func(g *GOP) {
				// Drop last 2 frames
				g.Frames = g.Frames[:3]
				g.FrameCount = 3
				g.TotalSize = 1800
			},
			expectedStartPTS: 0,
			expectedEndPTS:   6000,
			expectedDuration: time.Duration(66666666), // ~66.7ms (6000 PTS units at 90kHz)
		},
		{
			name: "drop_all_but_keyframe",
			setupGOP: func() *GOP {
				return &GOP{
					ID:       2,
					StartPTS: 1000,
					EndPTS:   10000,
					Duration: time.Millisecond * 100,
					Frames: []*types.VideoFrame{
						{ID: 1, PTS: 1000, Type: types.FrameTypeI, TotalSize: 1000},
						{ID: 2, PTS: 4000, Type: types.FrameTypeP, TotalSize: 500},
						{ID: 3, PTS: 7000, Type: types.FrameTypeB, TotalSize: 300},
						{ID: 4, PTS: 10000, Type: types.FrameTypeP, TotalSize: 500},
					},
					FrameCount: 4,
					TotalSize:  2300,
				}
			},
			dropFrames: func(g *GOP) {
				// Keep only keyframe
				g.Frames = g.Frames[:1]
				g.FrameCount = 1
				g.TotalSize = 1000
			},
			expectedStartPTS: 1000,
			expectedEndPTS:   1000,
			expectedDuration: 0,
		},
		{
			name: "drop_all_frames",
			setupGOP: func() *GOP {
				return &GOP{
					ID:       3,
					StartPTS: 0,
					EndPTS:   5000,
					Duration: time.Millisecond * 55,
					Frames: []*types.VideoFrame{
						{ID: 1, PTS: 0, Type: types.FrameTypeI, TotalSize: 1000},
						{ID: 2, PTS: 5000, Type: types.FrameTypeP, TotalSize: 500},
					},
					FrameCount: 2,
					TotalSize:  1500,
				}
			},
			dropFrames: func(g *GOP) {
				// Drop all frames
				g.Frames = g.Frames[:0]
				g.FrameCount = 0
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
	logger := logrus.NewEntry(logrus.New())
	config := BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    100000,
		MaxDuration: time.Minute,
	}
	buffer := NewBuffer("test-stream", config, logger)
	
	// Create a GOP with B frames
	gop := &GOP{
		ID:       1,
		Closed:   true,
		StartPTS: 0,
		EndPTS:   9000,
		Duration: time.Millisecond * 100,
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
		FrameCount: 4,
		BFrames:    2,
		PFrames:    1,
		TotalSize:  2100,
	}
	
	// Add GOP to buffer
	buffer.AddGOP(gop)
	
	// Verify initial state
	assert.Equal(t, time.Millisecond*100, gop.Duration)
	assert.Equal(t, int64(9000), gop.EndPTS)
	
	// Drop B frames (should trigger duration update)
	dropped := buffer.dropBFrames(1)
	
	// Verify frames were dropped
	assert.Equal(t, 2, len(dropped))
	assert.Equal(t, 2, len(gop.Frames))
	
	// Verify duration was updated
	expectedDuration := time.Duration(33333333) // ~33.3ms (3000 PTS units at 90kHz)
	assert.Equal(t, expectedDuration, gop.Duration)
	assert.Equal(t, int64(3000), gop.EndPTS)   // End PTS should be updated
	assert.Equal(t, int64(0), gop.StartPTS)    // Start PTS should remain 0
}
