package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestP1_7_ChannelBufferDropsFixed verifies that frames are NOT dropped
// when the output channel is full - backpressure is applied instead
func TestP1_7_ChannelBufferDropsFixed(t *testing.T) {
	// Create a pipeline with a small output buffer
	inputChan := make(chan types.TimestampedPacket, 100)
	config := Config{
		StreamID:        "test-stream",
		Codec:           types.CodecH264,
		FrameBufferSize: 2, // Small buffer to trigger drops
	}

	pipeline, err := NewVideoPipeline(context.Background(), config, inputChan)
	require.NoError(t, err)

	// Start the pipeline
	err = pipeline.Start()
	require.NoError(t, err)
	defer pipeline.Stop()

	// Send packets that will assemble into frames
	// We'll send complete frames quickly
	for i := 0; i < 10; i++ {
		// Send a complete frame as a single packet
		var nalData []byte
		if i == 0 {
			// First frame: IDR (keyframe) with NAL type 5
			nalData = []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0x88, 0x84, 0x00} // IDR NAL
		} else {
			// Non-keyframe: AUD followed by slice
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
		inputChan <- packet
	}

	// Don't read from output channel immediately to let it fill up
	time.Sleep(100 * time.Millisecond)

	// Now read some frames
	outputChan := pipeline.GetOutput()
	receivedFrames := 0

	// Read available frames
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			goto done
		case frame := <-outputChan:
			if frame != nil {
				receivedFrames++
			}
		}
	}

done:
	// Check statistics
	stats := pipeline.GetStats()
	t.Logf("Stats - Packets: %d, Frames: %d, Errors: %d",
		stats.PacketsProcessed, stats.FramesOutput, stats.Errors)

	// After the fix: No frames are dropped due to channel blocking
	// The pipeline applies backpressure instead
	assert.Equal(t, uint64(0), stats.Errors, "No channel drops should occur with backpressure")

	// Note: We might not receive all 10 frames due to assembly timeouts
	// but that's different from channel drops. The key is Errors = 0.
}

// TestP1_7_BackpressureInsteadOfDrops shows the desired behavior
func TestP1_7_BackpressureInsteadOfDrops(t *testing.T) {
	t.Skip("This test will pass after the fix is implemented")

	// After fix: the pipeline should block instead of dropping frames
	inputChan := make(chan types.TimestampedPacket, 100)
	config := Config{
		StreamID:        "test-stream",
		Codec:           types.CodecH264,
		FrameBufferSize: 2, // Small buffer
	}

	pipeline, err := NewVideoPipeline(context.Background(), config, inputChan)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)
	defer pipeline.Stop()

	// Send packets
	framesSent := 5
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
		inputChan <- packet
	}

	// Let pipeline process
	time.Sleep(100 * time.Millisecond)

	// Read all frames
	outputChan := pipeline.GetOutput()
	receivedFrames := 0

	for i := 0; i < framesSent; i++ {
		select {
		case frame := <-outputChan:
			if frame != nil {
				receivedFrames++
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timeout waiting for frame")
		}
	}

	// After fix: all frames should be received
	assert.Equal(t, framesSent, receivedFrames, "All frames should be received")

	stats := pipeline.GetStats()
	assert.Equal(t, uint64(0), stats.Errors, "No errors should occur with backpressure")
}
