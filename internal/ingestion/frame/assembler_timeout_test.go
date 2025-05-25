package frame

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestAssemblerTimeoutHandling verifies that timeouts don't block packet processing
func TestAssemblerTimeoutHandling(t *testing.T) {
	assembler := NewAssembler("test-stream", types.CodecH264, 100)
	require.NotNil(t, assembler)
	
	// Set a very short timeout
	assembler.SetFrameTimeout(50 * time.Millisecond)
	
	err := assembler.Start()
	require.NoError(t, err)
	defer assembler.Stop()

	// Start a frame
	pkt1 := types.TimestampedPacket{
		Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x67}, // SPS
		PTS:         1000,
		DTS:         1000,
		Flags:       types.PacketFlagFrameStart,
		CaptureTime: time.Now(),
	}
	err = assembler.AddPacket(pkt1)
	assert.NoError(t, err)

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Add another packet - this should work even if previous frame timed out
	pkt2 := types.TimestampedPacket{
		Data:        []byte{0x01, 0x02, 0x03}, // Some data
		PTS:         1000,
		DTS:         1000,
		CaptureTime: time.Now().Add(-70 * time.Millisecond), // Old timestamp to trigger timeout
	}
	err = assembler.AddPacket(pkt2)
	assert.NoError(t, err, "Should be able to add packet even after timeout")

	// Start a new frame - should work
	pkt3 := types.TimestampedPacket{
		Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
		PTS:         2000,
		DTS:         2000,
		Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
		CaptureTime: time.Now(),
	}
	err = assembler.AddPacket(pkt3)
	assert.NoError(t, err, "Should be able to start new frame after timeout")

	// Should get the new frame
	select {
	case frame := <-assembler.GetOutput():
		assert.NotNil(t, frame)
		// Could be either frame depending on timing
		assert.Contains(t, []int64{1000, 2000}, frame.PTS)
	case <-time.After(100 * time.Millisecond):
		// It's ok if no frame is available (timeout might have dropped it)
	}

	// Verify we can continue processing
	pkt4 := types.TimestampedPacket{
		Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
		PTS:         3000,
		DTS:         3000,
		Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
		CaptureTime: time.Now(),
	}
	err = assembler.AddPacket(pkt4)
	assert.NoError(t, err)

	frame := <-assembler.GetOutput()
	assert.NotNil(t, frame)
	assert.Contains(t, []int64{2000, 3000}, frame.PTS)

	stats := assembler.GetStats()
	t.Logf("Stats: Assembled=%d, Dropped=%d, PacketsDropped=%d",
		stats.FramesAssembled, stats.FramesDropped, stats.PacketsDropped)
}
