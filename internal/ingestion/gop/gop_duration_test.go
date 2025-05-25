package gop

import (
	"testing"
	"time"
	
	"github.com/stretchr/testify/assert"
	
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestP1_8_GOPDurationCalculationFixed verifies that GOP duration
// now correctly includes the duration of the last frame
func TestP1_8_GOPDurationCalculationFixed(t *testing.T) {
	// Create a GOP with frames at 30fps (33.33ms per frame)
	// Each frame has a duration of 3000 in 90kHz units (33.33ms)
	gop := &types.GOP{
		ID:       1,
		StreamID: "test-stream",
		Frames:   make([]*types.VideoFrame, 0),
	}
	
	// Add frames with consistent timing
	// Frame 0: PTS 0, duration 3000
	// Frame 1: PTS 3000, duration 3000
	// Frame 2: PTS 6000, duration 3000
	// Frame 3: PTS 9000, duration 3000
	frameDuration := int64(3000) // 33.33ms at 90kHz
	
	for i := 0; i < 4; i++ {
		frame := &types.VideoFrame{
			ID:           uint64(i),
			StreamID:     "test-stream",
			PTS:          int64(i) * frameDuration,
			DTS:          int64(i) * frameDuration,
			Duration:     frameDuration,
			CaptureTime:  time.Now(),
			CompleteTime: time.Now(),
		}
		
		if i == 0 {
			frame.Type = types.FrameTypeI
			frame.SetFlag(types.FrameFlagKeyframe)
		} else {
			frame.Type = types.FrameTypeP
		}
		
		gop.AddFrame(frame)
	}
	
	// Calculate stats
	gop.CalculateStats()
	
	// After fix: Duration correctly includes the last frame's duration
	// Duration = EndPTS - StartPTS + LastFrameDuration
	// Duration = 9000 - 0 + 3000 = 12000 (133.33ms)
	
	expectedDuration := int64(4) * frameDuration // 4 frames * 3000 each
	assert.Equal(t, expectedDuration, gop.Duration, 
		"Fixed: GOP duration now includes last frame duration")
	
	// Verify bitrate calculation is now correct
	// With 4 frames of 1000 bytes each = 4000 bytes
	gop.TotalSize = 4000
	gop.CalculateStats()
	
	// Fixed bitrate: 4000 * 8 / (12000/90000) = 240,000 bps
	expectedBitrate := int64(float64(gop.TotalSize*8) / (float64(expectedDuration)/90000.0))
	assert.Equal(t, expectedBitrate, gop.BitRate, "Bitrate calculation is now correct")
	
	t.Logf("Fixed duration: %d (%.2fms)", gop.Duration, float64(gop.Duration)/90.0)
	t.Logf("Fixed bitrate: %d bps", gop.BitRate)
}

// TestP1_8_GOPDurationWithLastFrameDuration shows the correct calculation
func TestP1_8_GOPDurationWithLastFrameDuration(t *testing.T) {
	// Test is no longer skipped - fix has been implemented
	
	gop := &types.GOP{
		ID:       1,
		StreamID: "test-stream",
		Frames:   make([]*types.VideoFrame, 0),
	}
	
	frameDuration := int64(3000)
	frameCount := 4
	
	for i := 0; i < frameCount; i++ {
		frame := &types.VideoFrame{
			ID:           uint64(i),
			StreamID:     "test-stream",
			PTS:          int64(i) * frameDuration,
			DTS:          int64(i) * frameDuration,
			Duration:     frameDuration,
			CaptureTime:  time.Now(),
			CompleteTime: time.Now(),
			TotalSize:    1000, // 1KB per frame
		}
		
		if i == 0 {
			frame.Type = types.FrameTypeI
			frame.SetFlag(types.FrameFlagKeyframe)
		} else {
			frame.Type = types.FrameTypeP
		}
		
		gop.AddFrame(frame)
	}
	
	// Calculate stats with fixed implementation
	gop.CalculateStats()
	
	// After fix: Duration should include the last frame's duration
	expectedDuration := gop.EndPTS - gop.StartPTS + frameDuration
	assert.Equal(t, expectedDuration, gop.Duration, 
		"Duration should include last frame duration")
	
	// Verify bitrate calculation is correct
	expectedBitrate := int64(float64(gop.TotalSize*8) / (float64(expectedDuration)/90000.0))
	assert.Equal(t, expectedBitrate, gop.BitRate, 
		"Bitrate should be calculated with correct duration")
}

// TestP1_8_UpdateDurationAfterDroppingFrames verifies duration update works correctly
func TestP1_8_UpdateDurationAfterDroppingFrames(t *testing.T) {
	gop := &types.GOP{
		ID:       1,
		StreamID: "test-stream",
		Frames:   make([]*types.VideoFrame, 0),
	}
	
	// Add 6 frames
	frameDuration := int64(3000)
	for i := 0; i < 6; i++ {
		frame := &types.VideoFrame{
			ID:       uint64(i),
			PTS:      int64(i) * frameDuration,
			Duration: frameDuration,
		}
		gop.Frames = append(gop.Frames, frame)
	}
	
	// Set initial values
	gop.StartPTS = gop.Frames[0].PTS
	gop.EndPTS = gop.Frames[len(gop.Frames)-1].PTS
	gop.TotalSize = 6000
	
	// Drop middle frames (simulate B-frame dropping)
	gop.Frames = append(gop.Frames[:2], gop.Frames[4:]...)
	gop.TotalSize = 4000
	
	// Update duration
	gop.UpdateDuration()
	
	// Verify duration is recalculated correctly
	assert.Equal(t, gop.Frames[0].PTS, gop.StartPTS, "Start PTS should be updated")
	assert.Equal(t, gop.Frames[len(gop.Frames)-1].PTS, gop.EndPTS, "End PTS should be updated")
	
	// After fix: The duration should include the last frame's duration
	expectedDuration := gop.EndPTS - gop.StartPTS + frameDuration
	assert.Equal(t, expectedDuration, gop.Duration, "UpdateDuration now includes last frame duration")
	
	// Verify the frames we kept are correct (frame 0, 1, 4, 5)
	assert.Len(t, gop.Frames, 4, "Should have 4 frames after dropping middle ones")
	assert.Equal(t, int64(0), gop.Frames[0].PTS, "First frame PTS")
	assert.Equal(t, int64(3000), gop.Frames[1].PTS, "Second frame PTS")
	assert.Equal(t, int64(12000), gop.Frames[2].PTS, "Third frame PTS (was frame 4)")
	assert.Equal(t, int64(15000), gop.Frames[3].PTS, "Fourth frame PTS (was frame 5)")
}
