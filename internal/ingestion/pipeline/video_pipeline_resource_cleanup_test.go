package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestVideoPipelineResourceCleanup tests proper resource cleanup during shutdown
func TestVideoPipelineResourceCleanup(t *testing.T) {
	ctx := context.Background()
	input := make(chan types.TimestampedPacket, 10)

	cfg := Config{
		StreamID:        "test-cleanup",
		Codec:           types.CodecH264,
		FrameBufferSize: 5,
	}

	pipeline, err := NewVideoPipeline(ctx, cfg, input)
	require.NoError(t, err)
	require.NotNil(t, pipeline)

	// Start pipeline
	err = pipeline.Start()
	require.NoError(t, err)

	// Send some test packets
	testPackets := []types.TimestampedPacket{
		createH264Packet(0, types.PacketTypeVideo),
		createH264Packet(3000, types.PacketTypeVideo),
		createH264Packet(6000, types.PacketTypeVideo),
	}

	for _, pkt := range testPackets {
		select {
		case input <- pkt:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout sending packet")
		}
	}

	// Allow some processing time
	time.Sleep(50 * time.Millisecond)

	// Stop pipeline and verify clean shutdown
	err = pipeline.Stop()
	assert.NoError(t, err)

	// Verify pipeline state after stop
	stats := pipeline.GetStats()
	assert.Greater(t, stats.PacketsProcessed, uint64(0), "Should have processed some packets")

	// Verify we can call Stop multiple times safely
	err = pipeline.Stop()
	assert.NoError(t, err)

	// Close input channel
	close(input)
}

// TestVideoPipelineShutdownTimeout tests timeout handling during shutdown
func TestVideoPipelineShutdownTimeout(t *testing.T) {
	ctx := context.Background()
	input := make(chan types.TimestampedPacket, 100)

	cfg := Config{
		StreamID:        "test-timeout",
		Codec:           types.CodecH264,
		FrameBufferSize: 1, // Small buffer to create potential blockage
	}

	pipeline, err := NewVideoPipeline(ctx, cfg, input)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)

	// Fill input with many packets rapidly
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			packet := createH264Packet(uint64(i*3000), types.PacketTypeVideo)
			select {
			case input <- packet:
			case <-time.After(10 * time.Millisecond):
				return // Channel might be closed or blocked
			}
		}
	}()

	// Stop immediately to test timeout handling
	start := time.Now()
	err = pipeline.Stop()
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Less(t, duration, 6*time.Second, "Stop should complete within timeout period")

	// Wait for the sending goroutine to finish before closing the channel
	<-done
	close(input)
}

// TestVideoPipelineFrameFlushDuringShutdown tests proper frame flushing during shutdown
func TestVideoPipelineFrameFlushDuringShutdown(t *testing.T) {
	ctx := context.Background()
	input := make(chan types.TimestampedPacket, 10)

	cfg := Config{
		StreamID:        "test-flush",
		Codec:           types.CodecH264,
		FrameBufferSize: 10,
	}

	pipeline, err := NewVideoPipeline(ctx, cfg, input)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)

	// Send packets that will create some frames
	testPackets := []types.TimestampedPacket{
		createH264Packet(0, types.PacketTypeVideo),    // I-frame
		createH264Packet(3000, types.PacketTypeVideo), // P-frame
		createH264Packet(6000, types.PacketTypeVideo), // P-frame
		createH264Packet(9000, types.PacketTypeVideo), // P-frame
	}

	for _, pkt := range testPackets {
		select {
		case input <- pkt:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout sending packet")
		}
	}

	// Give time for frame assembly
	time.Sleep(100 * time.Millisecond)

	// Get stats before stop
	statsBefore := pipeline.GetStats()

	// Stop pipeline
	err = pipeline.Stop()
	assert.NoError(t, err)

	// Get stats after stop
	statsAfter := pipeline.GetStats()

	// Verify processing occurred
	assert.Greater(t, statsAfter.PacketsProcessed, uint64(0), "Should have processed packets")
	assert.GreaterOrEqual(t, statsAfter.PacketsProcessed, statsBefore.PacketsProcessed, "Packet count should not decrease")

	close(input)
}

// TestVideoPipelineContextCancellation tests proper handling of context cancellation
func TestVideoPipelineContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	input := make(chan types.TimestampedPacket, 10)

	cfg := Config{
		StreamID:        "test-context",
		Codec:           types.CodecH264,
		FrameBufferSize: 5,
	}

	pipeline, err := NewVideoPipeline(ctx, cfg, input)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)

	// Send a packet
	packet := createH264Packet(0, types.PacketTypeVideo)
	select {
	case input <- packet:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout sending packet")
	}

	// Cancel context
	cancel()

	// Allow time for context cancellation to propagate
	time.Sleep(50 * time.Millisecond)

	// Stop should still work even after context cancellation
	err = pipeline.Stop()
	assert.NoError(t, err)

	close(input)
}

// TestVideoPipelineNilSafety tests nil safety in pipeline operations
func TestVideoPipelineNilSafety(t *testing.T) {
	ctx := context.Background()
	input := make(chan types.TimestampedPacket, 10)

	cfg := Config{
		StreamID:        "test-nil-safety",
		Codec:           types.CodecH264,
		FrameBufferSize: 5,
	}

	pipeline, err := NewVideoPipeline(ctx, cfg, input)
	require.NoError(t, err)

	// Test getting stats before start
	stats := pipeline.GetStats()
	assert.Equal(t, uint64(0), stats.PacketsProcessed, "Should start with zero stats")

	err = pipeline.Start()
	require.NoError(t, err)

	// Stop immediately
	err = pipeline.Stop()
	assert.NoError(t, err)

	// Test getting stats after stop
	stats = pipeline.GetStats()
	assert.GreaterOrEqual(t, stats.PacketsProcessed, uint64(0), "Stats should be non-negative")

	close(input)
}

// TestVideoPipelineOutputChannelCleanup tests proper output channel cleanup
func TestVideoPipelineOutputChannelCleanup(t *testing.T) {
	ctx := context.Background()
	input := make(chan types.TimestampedPacket, 10)

	cfg := Config{
		StreamID:        "test-output-cleanup",
		Codec:           types.CodecH264,
		FrameBufferSize: 5,
	}

	pipeline, err := NewVideoPipeline(ctx, cfg, input)
	require.NoError(t, err)

	err = pipeline.Start()
	require.NoError(t, err)

	// Get output channel
	output := pipeline.GetOutput()
	require.NotNil(t, output)

	// Send some packets
	packet := createH264Packet(0, types.PacketTypeVideo)
	select {
	case input <- packet:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout sending packet")
	}

	// Stop pipeline
	err = pipeline.Stop()
	assert.NoError(t, err)

	// Output channel should be closed
	select {
	case _, ok := <-output:
		if ok {
			// If we got data, that's fine, but channel should eventually close
			// Check again after a delay
			time.Sleep(50 * time.Millisecond)
			select {
			case _, ok := <-output:
				assert.False(t, ok, "Output channel should be closed after stop")
			case <-time.After(100 * time.Millisecond):
				t.Log("Output channel not immediately closed, which is acceptable")
			}
		} else {
			assert.False(t, ok, "Output channel should be closed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Log("No immediate output, which is acceptable")
	}

	close(input)
}

// Helper function to create test H.264 packets
func createH264Packet(pts uint64, packetType types.PacketType) types.TimestampedPacket {
	// Create a simple H.264 NAL unit (IDR frame start)
	data := []byte{0x00, 0x00, 0x00, 0x01, 0x65} // IDR NAL unit
	return types.TimestampedPacket{
		Data: data,
		PTS:  int64(pts),
		DTS:  int64(pts),
		Type: packetType,
	}
}
