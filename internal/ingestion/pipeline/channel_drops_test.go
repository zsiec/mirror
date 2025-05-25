package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	
	"github.com/zsiec/mirror/internal/ingestion/types"
)


// TestP1_7_PipelineChannelDrops demonstrates frames being dropped when output is blocked
func TestP1_7_PipelineChannelDrops(t *testing.T) {
	// We need to test the actual implementation
	// Let's create a custom goroutine that mimics processFrames behavior
	
	output := make(chan *types.VideoFrame, 2) // Small buffer to trigger drops
	frameInput := make(chan *types.VideoFrame, 10)
	errors := uint64(0)
	framesOutput := uint64(0)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	logger := logrus.New()
	
	// Replicate the buggy processFrames logic
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case frame, ok := <-frameInput:
				if !ok {
					return
				}
				
				// This is the buggy part from video_pipeline.go
				select {
				case output <- frame:
					atomic.AddUint64(&framesOutput, 1)
				case <-ctx.Done():
					return
				default:
					// Output blocked, drop frame
					atomic.AddUint64(&errors, 1)
					logger.Warn("Pipeline output blocked, dropping frame")
				}
			}
		}
	}()
	
	// Send frames quickly without reading output
	framesSent := 5
	for i := 0; i < framesSent; i++ {
		frame := &types.VideoFrame{
			ID:       uint64(i),
			StreamID: "test-stream",
			PTS:      int64(i * 3000),
		}
		frameInput <- frame
	}
	
	// Give time for processing
	time.Sleep(100 * time.Millisecond)
	
	// Now read from output
	receivedFrames := 0
	timeout := time.After(200 * time.Millisecond)
	
loop:
	for {
		select {
		case frame := <-output:
			if frame != nil {
				receivedFrames++
			}
		case <-timeout:
			break loop
		}
	}
	
	// Verify the bug: frames were dropped
	assert.Less(t, receivedFrames, framesSent, "Some frames should have been dropped")
	assert.Greater(t, atomic.LoadUint64(&errors), uint64(0), "Errors should be recorded for dropped frames")
	
	t.Logf("Sent %d frames, received %d, dropped %d", framesSent, receivedFrames, framesSent-receivedFrames)
}

// TestP1_7_DesiredBehaviorWithBackpressure shows what should happen after fix
func TestP1_7_DesiredBehaviorWithBackpressure(t *testing.T) {
	t.Skip("This test will pass after implementing backpressure fix")
	
	// After the fix, the pipeline should block instead of dropping frames
	// The fix would remove the default case in the select statement
}
