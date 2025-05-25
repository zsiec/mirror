package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestP1_7_PipelineWithBackpressure verifies that the pipeline applies backpressure
// instead of dropping frames when the output channel is full
func TestP1_7_PipelineWithBackpressure(t *testing.T) {
	// Create a pipeline with a small output buffer
	inputChan := make(chan types.TimestampedPacket, 100)
	config := Config{
		StreamID:        "test-stream",
		Codec:           types.CodecH264,
		FrameBufferSize: 2, // Small buffer to test backpressure
		FrameAssemblyTimeout: 10, // Short timeout
	}
	
	pipeline, err := NewVideoPipeline(context.Background(), config, inputChan)
	require.NoError(t, err)
	
	// Start the pipeline
	err = pipeline.Start()
	require.NoError(t, err)
	defer pipeline.Stop()
	
	// Send frames in a goroutine to avoid blocking the test
	framesSent := 5
	sendComplete := make(chan bool)
	
	go func() {
		for i := 0; i < framesSent; i++ {
			var nalData []byte
			if i == 0 {
				nalData = []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00} // IDR NAL
			} else {
				nalData = []byte{0x00, 0x00, 0x00, 0x01, 0x09, 0xF0, // AUD
					0x00, 0x00, 0x00, 0x01, 0x41, 0x9A, 0x00} // Non-IDR slice
			}
			
			packet := types.TimestampedPacket{
				StreamID: "test-stream",
				Data:     nalData,
				PTS:      int64(i * 3000),
				DTS:      int64(i * 3000),
				Type:     types.PacketTypeVideo,
				Codec:    types.CodecH264,
				Flags:    types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
			}
			if i == 0 {
				packet.SetFlag(types.PacketFlagKeyframe)
			}
			
			select {
			case inputChan <- packet:
				// Packet sent
			case <-time.After(1 * time.Second):
				t.Error("Timeout sending packet - this shouldn't happen with buffered channel")
			}
		}
		close(sendComplete)
	}()
	
	// Let the pipeline process some frames
	time.Sleep(50 * time.Millisecond)
	
	// Now slowly read frames to test backpressure
	outputChan := pipeline.GetOutput()
	receivedFrames := 0
	
	for i := 0; i < framesSent; i++ {
		select {
		case frame := <-outputChan:
			if frame != nil {
				receivedFrames++
				// Simulate slow consumer
				time.Sleep(20 * time.Millisecond)
			}
		case <-time.After(500 * time.Millisecond):
			// This might happen if assembly times out
			t.Logf("Timeout waiting for frame %d", i)
		}
	}
	
	// Wait for sending to complete
	select {
	case <-sendComplete:
		// Good
	case <-time.After(100 * time.Millisecond):
		// This is OK - sending might still be in progress due to backpressure
	}
	
	// Check stats
	stats := pipeline.GetStats()
	t.Logf("Stats - Packets: %d, Frames: %d, Errors: %d", 
		stats.PacketsProcessed, stats.FramesOutput, stats.Errors)
	
	// With backpressure fix, no frames should be dropped due to output blocking
	assert.Equal(t, uint64(0), stats.Errors, "No frames should be dropped with backpressure")
	
	// Note: Some frames might timeout in assembly, but that's different from channel drops
	// The key is that stats.Errors (which counts channel drops) should be 0
}
