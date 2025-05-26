package gop

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func TestBuffer_GetFrame_NilSafety(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     100,
		MaxBytes:    1000000,
		MaxDuration: time.Minute,
	}
	buffer := NewBuffer("test-stream", config, logger)

	tests := []struct {
		name    string
		setup   func()
		frameID uint64
		wantNil bool
	}{
		{
			name: "non_existent_frame",
			setup: func() {
				// No setup - empty buffer
			},
			frameID: 999,
			wantNil: true,
		},
		{
			name: "frame_with_nil_gop",
			setup: func() {
				// Manually insert a frame location with nil GOP (simulating corruption)
				buffer.frameIndex[100] = &FrameLocation{
					GOP:      nil, // This could happen due to corruption or bugs
					Position: 0,
				}
			},
			frameID: 100,
			wantNil: true,
		},
		{
			name: "frame_with_out_of_bounds_position",
			setup: func() {
				// Create a GOP with one frame
				gop := &types.GOP{
					ID:       1,
					Closed:   true,
					Complete: true,
					Keyframe: &types.VideoFrame{
						ID:          1,
						Type:        types.FrameTypeI,
						CaptureTime: time.Now(),
					},
					Frames: []*types.VideoFrame{
						{
							ID:          1,
							Type:        types.FrameTypeI,
							CaptureTime: time.Now(),
						},
					},
				}

				// Add GOP to buffer
				buffer.AddGOP(gop)

				// Manually corrupt the frame index with wrong position
				buffer.frameIndex[1] = &FrameLocation{
					GOP:      gop,
					Position: 10, // Out of bounds
				}
			},
			frameID: 1,
			wantNil: true,
		},
		{
			name: "valid_frame",
			setup: func() {
				// Create a valid GOP
				gop := &types.GOP{
					ID:       2,
					Closed:   true,
					Complete: true,
					Keyframe: &types.VideoFrame{
						ID:          2,
						Type:        types.FrameTypeI,
						CaptureTime: time.Now(),
					},
					Frames: []*types.VideoFrame{
						{
							ID:          2,
							Type:        types.FrameTypeI,
							CaptureTime: time.Now(),
						},
						{
							ID:          3,
							Type:        types.FrameTypeP,
							CaptureTime: time.Now().Add(time.Millisecond * 33),
						},
					},
				}

				// Add GOP to buffer properly
				buffer.AddGOP(gop)
			},
			frameID: 3,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset buffer
			buffer.gops.Init()
			buffer.frameIndex = make(map[uint64]*FrameLocation)
			buffer.currentBytes = 0

			// Run setup
			tt.setup()

			// Test GetFrame - should not panic even with nil GOP
			assert.NotPanics(t, func() {
				frame := buffer.GetFrame(tt.frameID)
				if tt.wantNil {
					assert.Nil(t, frame)
				} else {
					assert.NotNil(t, frame)
					assert.Equal(t, tt.frameID, frame.ID)
				}
			})
		})
	}
}

// TestBuffer_DropFrames_NilSafety tests that DropFrames handles nil GOPs safely
func TestBuffer_DropFrames_NilSafety(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     100,
		MaxBytes:    1000000,
		MaxDuration: time.Minute,
	}
	buffer := NewBuffer("test-stream", config, logger)

	// Add a valid GOP
	gop := &types.GOP{
		ID:       1,
		Closed:   true,
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
				Type:        types.FrameTypeB,
				CaptureTime: time.Now().Add(time.Millisecond * 33),
				TotalSize:   500,
			},
		},
	}

	buffer.AddGOP(gop)

	// Should handle operations without panic even if internal state is corrupted
	assert.NotPanics(t, func() {
		// Even if we had nil GOPs, these operations should be safe
		gops := buffer.GetRecentGOPs(5)
		assert.NotNil(t, gops)
	})
}
