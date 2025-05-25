package gop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

func createTestFrame(frameNum uint64, frameType types.FrameType, pts int64) *types.VideoFrame {
	return &types.VideoFrame{
		ID:          frameNum,
		StreamID:    "test-stream",
		FrameNumber: frameNum,
		Type:        frameType,
		PTS:         pts,
		DTS:         pts - 100,
		TotalSize:   1000,
		CaptureTime: time.Now(),
	}
}

func TestDetector_NewGOP(t *testing.T) {
	detector := NewDetector("test-stream")

	// Process first keyframe
	iframe := createTestFrame(1, types.FrameTypeIDR, 1000)
	closedGOP := detector.ProcessFrame(iframe)

	assert.Nil(t, closedGOP) // No previous GOP to close
	assert.NotNil(t, detector.GetCurrentGOP())
	assert.Equal(t, uint64(1), detector.GetCurrentGOP().ID)
	assert.Equal(t, iframe, detector.GetCurrentGOP().Keyframe)
	assert.Equal(t, 1, len(detector.GetCurrentGOP().Frames))
}

func TestDetector_GOPBoundary(t *testing.T) {
	detector := NewDetector("test-stream")

	// Build first GOP
	iframe1 := createTestFrame(1, types.FrameTypeIDR, 1000)
	detector.ProcessFrame(iframe1)

	pframe1 := createTestFrame(2, types.FrameTypeP, 1033)
	detector.ProcessFrame(pframe1)

	bframe1 := createTestFrame(3, types.FrameTypeB, 1066)
	detector.ProcessFrame(bframe1)

	// Second keyframe should close first GOP
	iframe2 := createTestFrame(4, types.FrameTypeIDR, 2000)
	closedGOP := detector.ProcessFrame(iframe2)

	require.NotNil(t, closedGOP)
	assert.Equal(t, uint64(1), closedGOP.ID)
	assert.Equal(t, 3, closedGOP.FrameCount)
	assert.True(t, closedGOP.Closed)
	assert.Equal(t, int64(1000), closedGOP.StartPTS)
	assert.Equal(t, int64(2000), closedGOP.EndPTS)

	// Check frame counts
	assert.Equal(t, 1, closedGOP.IFrames)
	assert.Equal(t, 1, closedGOP.PFrames)
	assert.Equal(t, 1, closedGOP.BFrames)

	// New GOP should be started
	currentGOP := detector.GetCurrentGOP()
	assert.Equal(t, uint64(2), currentGOP.ID)
	assert.Equal(t, iframe2, currentGOP.Keyframe)
}

func TestDetector_FrameGOPAssignment(t *testing.T) {
	detector := NewDetector("test-stream")

	// First GOP
	iframe := createTestFrame(1, types.FrameTypeIDR, 1000)
	detector.ProcessFrame(iframe)
	assert.Equal(t, uint64(1), iframe.GOPId)
	assert.Equal(t, 0, iframe.GOPPosition)

	pframe := createTestFrame(2, types.FrameTypeP, 1033)
	detector.ProcessFrame(pframe)
	assert.Equal(t, uint64(1), pframe.GOPId)
	assert.Equal(t, 1, pframe.GOPPosition)

	bframe := createTestFrame(3, types.FrameTypeB, 1066)
	detector.ProcessFrame(bframe)
	assert.Equal(t, uint64(1), bframe.GOPId)
	assert.Equal(t, 2, bframe.GOPPosition)
}

func TestDetector_Statistics(t *testing.T) {
	detector := NewDetector("test-stream")

	// Create several GOPs
	for gopIdx := 0; gopIdx < 3; gopIdx++ {
		pts := int64(gopIdx * 1000)

		// I frame
		detector.ProcessFrame(createTestFrame(uint64(gopIdx*10+1), types.FrameTypeIDR, pts))

		// P frames
		for i := 1; i <= 3; i++ {
			detector.ProcessFrame(createTestFrame(uint64(gopIdx*10+i+1), types.FrameTypeP, pts+int64(i*33)))
		}

		// B frames
		for i := 4; i <= 6; i++ {
			detector.ProcessFrame(createTestFrame(uint64(gopIdx*10+i+1), types.FrameTypeB, pts+int64(i*33)))
		}
	}

	// Close last GOP with new keyframe
	detector.ProcessFrame(createTestFrame(100, types.FrameTypeIDR, 3000))

	stats := detector.GetStatistics()
	assert.Equal(t, "test-stream", stats.StreamID)
	assert.Equal(t, uint64(4), stats.TotalGOPs)         // 3 closed + 1 current
	assert.Greater(t, stats.AverageGOPSize, float64(6)) // Each GOP has 7 frames

	// Frame ratios (1 I, 3 P, 3 B = 7 total per GOP)
	assert.InDelta(t, 1.0/7.0, stats.IFrameRatio, 0.01)
	assert.InDelta(t, 3.0/7.0, stats.PFrameRatio, 0.01)
	assert.InDelta(t, 3.0/7.0, stats.BFrameRatio, 0.01)
}

func TestGOP_CanDropFrame(t *testing.T) {
	detector := NewDetector("test-stream")

	// Build a GOP: I B B P B B P
	frames := []*types.VideoFrame{
		createTestFrame(1, types.FrameTypeIDR, 1000), // 0: Never drop
		createTestFrame(2, types.FrameTypeB, 1033),   // 1: Can drop
		createTestFrame(3, types.FrameTypeB, 1066),   // 2: Can drop
		createTestFrame(4, types.FrameTypeP, 1100),   // 3: Cannot drop (B frames after depend on it)
		createTestFrame(5, types.FrameTypeB, 1133),   // 4: Can drop
		createTestFrame(6, types.FrameTypeB, 1166),   // 5: Can drop
		createTestFrame(7, types.FrameTypeP, 1200),   // 6: Can drop (no B frames after)
	}

	for _, frame := range frames {
		detector.ProcessFrame(frame)
	}

	gop := detector.GetCurrentGOP()

	// Test drop decisions
	assert.False(t, gop.CanDropFrame(0), "Should not drop I frame")
	assert.True(t, gop.CanDropFrame(1), "Should drop B frame")
	assert.True(t, gop.CanDropFrame(2), "Should drop B frame")
	assert.False(t, gop.CanDropFrame(3), "Should not drop P frame with dependent B frames")
	assert.True(t, gop.CanDropFrame(4), "Should drop B frame")
	assert.True(t, gop.CanDropFrame(5), "Should drop B frame")
	assert.True(t, gop.CanDropFrame(6), "Should drop P frame with no dependents")
}

func TestGOP_IsComplete(t *testing.T) {
	detector := NewDetector("test-stream")

	// Start GOP
	detector.ProcessFrame(createTestFrame(1, types.FrameTypeIDR, 1000))
	gop := detector.GetCurrentGOP()

	// Not complete with just 1 frame
	assert.False(t, gop.IsComplete())

	// Add frames
	for i := 2; i <= 30; i++ {
		frameType := types.FrameTypeP
		if i%3 == 0 {
			frameType = types.FrameTypeB
		}
		detector.ProcessFrame(createTestFrame(uint64(i), frameType, int64(1000+i*33)))
	}

	// Complete with 30 frames
	assert.True(t, gop.IsComplete())

	// Also complete when closed
	detector.ProcessFrame(createTestFrame(31, types.FrameTypeIDR, 2000))
	recentGOPs := detector.GetRecentGOPs()
	assert.True(t, recentGOPs[0].IsComplete())
	assert.True(t, recentGOPs[0].Closed)
}
