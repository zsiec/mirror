package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestVideoPipeline_OutputChannelSafety tests that the output channel is closed safely
func TestVideoPipeline_OutputChannelSafety(t *testing.T) {
	cfg := Config{
		StreamID:        "test-stream",
		Codec:           types.CodecH264,
		FrameBufferSize: 10,
	}

	input := make(chan types.TimestampedPacket, 100)
	pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
	require.NoError(t, err)

	// Start the pipeline
	err = pipeline.Start()
	require.NoError(t, err)

	// Get output channel
	output := pipeline.GetOutput()

	// Start a consumer that reads from output
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range output {
			// Consume frames
		}
	}()

	// Send some packets
	for i := 0; i < 10; i++ {
		select {
		case input <- types.TimestampedPacket{
			Data:        []byte{0, 0, 0, 1, 0x65}, // Fake H.264 IDR
			CaptureTime: time.Now(),
			PTS:         int64(i * 3000),
			StreamID:    "test-stream",
			Type:        types.PacketTypeVideo,
			Codec:       types.CodecH264,
			Flags:       types.PacketFlagKeyframe | types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
		}:
		case <-time.After(10 * time.Millisecond):
			// Skip if channel is full
		}
	}

	// Give some time for processing
	time.Sleep(50 * time.Millisecond)

	// Stop the pipeline
	err = pipeline.Stop()
	assert.NoError(t, err)

	// Verify the consumer goroutine exits properly
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good, consumer exited
	case <-time.After(1 * time.Second):
		t.Fatal("Consumer goroutine did not exit after pipeline stop")
	}

	// Verify we can't send to output after stop (channel should be closed)
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Expected: sending on closed channel
				return
			}
		}()

		// Try to read from output again - should get nothing (channel is closed)
		output2 := pipeline.GetOutput()
		select {
		case _, ok := <-output2:
			if ok {
				t.Fatal("Should not receive from closed channel")
			}
			// Channel is closed as expected
		default:
			// This is also acceptable - non-blocking receive returns immediately
		}
	}()
}

// TestVideoPipeline_ConcurrentStop tests stopping the pipeline concurrently
func TestVideoPipeline_ConcurrentStop(t *testing.T) {
	for i := 0; i < 10; i++ {
		cfg := Config{
			StreamID:        "test-stream",
			Codec:           types.CodecH264,
			FrameBufferSize: 10,
		}

		input := make(chan types.TimestampedPacket, 100)
		pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
		require.NoError(t, err)

		err = pipeline.Start()
		require.NoError(t, err)

		// Start multiple goroutines trying to stop
		var wg sync.WaitGroup
		for j := 0; j < 5; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pipeline.Stop()
			}()
		}

		// Wait for all stops to complete
		wg.Wait()

		// Should not panic or deadlock
	}
}

// TestVideoPipeline_StopWithPendingFrames tests stopping with frames being processed
func TestVideoPipeline_StopWithPendingFrames(t *testing.T) {
	cfg := Config{
		StreamID:              "test-stream",
		Codec:                 types.CodecH264,
		FrameBufferSize:       100,
		MaxBFrameReorderDepth: 3,
		MaxReorderDelay:       200,
	}

	input := make(chan types.TimestampedPacket, 1000)
	pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)

	// Send many packets to ensure some are being processed during stop
	go func() {
		for i := 0; i < 100; i++ {
			frameType := byte(0x41) // P-frame
			if i%10 == 0 {
				frameType = 0x65 // IDR frame
			} else if i%3 == 1 {
				frameType = 0x01 // B-frame
			}

			select {
			case input <- types.TimestampedPacket{
				Data:        []byte{0, 0, 0, 1, frameType},
				CaptureTime: time.Now(),
				PTS:         int64(i * 3000),
				StreamID:    "test-stream",
				Type:        types.PacketTypeVideo,
				Codec:       types.CodecH264,
			}:
			default:
				// Channel full, stop sending
				return
			}
		}
	}()

	// Let processing start
	time.Sleep(20 * time.Millisecond)

	// Stop while frames are being processed
	done := make(chan error)
	go func() {
		done <- pipeline.Stop()
	}()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Pipeline stop timed out")
	}
}

// TestVideoPipeline_OutputChannelCloseOnce tests that output channel is only closed once
func TestVideoPipeline_OutputChannelCloseOnce(t *testing.T) {
	cfg := Config{
		StreamID:        "test-stream",
		Codec:           types.CodecH264,
		FrameBufferSize: 10,
	}

	input := make(chan types.TimestampedPacket)
	pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)

	// Stop multiple times - should not panic
	var wg sync.WaitGroup
	errors := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			errors[idx] = pipeline.Stop()
		}()
	}

	wg.Wait()

	// All stops should succeed without panic
	for _, err := range errors {
		assert.NoError(t, err)
	}
}
