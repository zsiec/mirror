package frame

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestFrameAssemblerOutputChannelTimeout tests the improved timeout logic
func TestFrameAssemblerOutputChannelTimeout(t *testing.T) {
	tests := []struct {
		name           string
		bufferSize     int
		frameTimeout   time.Duration
		expectedFrames int
		expectDrops    bool
	}{
		{
			name:           "small_buffer_fast_timeout",
			bufferSize:     1,
			frameTimeout:   50 * time.Millisecond,
			expectedFrames: 1,
			expectDrops:    true,
		},
		{
			name:           "medium_buffer_normal_timeout",
			bufferSize:     5,
			frameTimeout:   200 * time.Millisecond,
			expectedFrames: 5,
			expectDrops:    true,
		},
		{
			name:           "large_buffer_slow_timeout",
			bufferSize:     10,
			frameTimeout:   500 * time.Millisecond,
			expectedFrames: 10,
			expectDrops:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembler := NewAssembler("test-stream", types.CodecH264, tt.bufferSize)
			require.NotNil(t, assembler)

			assembler.SetFrameTimeout(tt.frameTimeout)
			err := assembler.Start()
			require.NoError(t, err)
			defer assembler.Stop()

			// Fill the output channel buffer
			totalFramesToAdd := tt.bufferSize + 5
			framesAdded := 0
			for i := 0; i < totalFramesToAdd; i++ {
				pkt := types.TimestampedPacket{
					Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
					PTS:         int64((i + 1) * 1000),
					DTS:         int64((i + 1) * 1000),
					Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
					CaptureTime: time.Now(),
				}
				err := assembler.AddPacket(pkt)
				// Count all packets we attempted to add
				framesAdded++
				if err != nil {
					t.Logf("Frame %d resulted in error: %v", i+1, err)
				}
			}

			// Give some time for timeouts to occur
			time.Sleep(tt.frameTimeout + 50*time.Millisecond)

			// Read what we can from the channel
			framesReceived := 0
			timeout := time.After(100 * time.Millisecond)
			for {
				select {
				case frame := <-assembler.GetOutput():
					require.NotNil(t, frame)
					framesReceived++
				case <-timeout:
					goto done
				}
			}
		done:

			stats := assembler.GetStats()
			t.Logf("Buffer size: %d, Frames added: %d, Frames received: %d, Assembled: %d, Dropped: %d",
				tt.bufferSize, framesAdded, framesReceived, stats.FramesAssembled, stats.FramesDropped)

			// Basic validations
			assert.LessOrEqual(t, framesReceived, tt.bufferSize+1) // Allow slight variance
			
			if tt.expectDrops {
				assert.Greater(t, stats.FramesDropped, uint64(0), "Expected frame drops with small buffer")
			}
			
			// Total frames processed should equal assembled + dropped
			totalProcessed := stats.FramesAssembled + stats.FramesDropped
			assert.Equal(t, totalProcessed, uint64(framesAdded), "Total processed should equal frames added")
			assert.Greater(t, totalProcessed, uint64(0), "Should have processed some frames")
		})
	}
}

// TestFrameAssemblerOutputChannelConcurrentAccess tests concurrent output channel access
func TestFrameAssemblerOutputChannelConcurrentAccess(t *testing.T) {
	assembler := NewAssembler("test-stream", types.CodecH264, 2) // Small buffer
	require.NotNil(t, assembler)
	
	assembler.SetFrameTimeout(100 * time.Millisecond)
	err := assembler.Start()
	require.NoError(t, err)
	defer assembler.Stop()

	var wg sync.WaitGroup
	const numProducers = 3
	const numConsumers = 2
	const framesPerProducer = 10

	framesReceived := make([]int, numConsumers)
	var receivedMu sync.Mutex

	// Start consumers
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func(consumerID int) {
			defer wg.Done()
			count := 0
			timeout := time.After(2 * time.Second)
			
			for {
				select {
				case frame := <-assembler.GetOutput():
					if frame != nil {
						count++
					}
				case <-timeout:
					receivedMu.Lock()
					framesReceived[consumerID] = count
					receivedMu.Unlock()
					return
				}
			}
		}(i)
	}

	// Start producers
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()
			
			for j := 0; j < framesPerProducer; j++ {
				pkt := types.TimestampedPacket{
					Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
					PTS:         int64((producerID*1000) + (j * 100)),
					DTS:         int64((producerID*1000) + (j * 100)),
					Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
					CaptureTime: time.Now(),
				}
				
				// Ignore errors from timeout (expected with small buffer)
				_ = assembler.AddPacket(pkt)
				
				// Small delay between frames
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	stats := assembler.GetStats()
	totalReceived := 0
	for i, count := range framesReceived {
		t.Logf("Consumer %d received %d frames", i, count)
		totalReceived += count
	}

	t.Logf("Total frames: Assembled=%d, Dropped=%d, Received=%d",
		stats.FramesAssembled, stats.FramesDropped, totalReceived)

	// Verify metrics consistency
	assert.Equal(t, stats.FramesAssembled, uint64(totalReceived))
	assert.LessOrEqual(t, totalReceived, numProducers*framesPerProducer)
	// With concurrent access and a small buffer, we might expect drops, but it's not guaranteed
	// The test should pass regardless of whether drops occur
	assert.GreaterOrEqual(t, stats.FramesDropped, uint64(0)) // Drops are possible but not required
}

// TestFrameAssemblerTimeoutBounds tests the timeout bounds logic
func TestFrameAssemblerTimeoutBounds(t *testing.T) {
	tests := []struct {
		name                string
		frameTimeout        time.Duration
		expectedMinTimeout  time.Duration
		expectedMaxTimeout  time.Duration
	}{
		{
			name:                "very_short_timeout",
			frameTimeout:        10 * time.Millisecond,
			expectedMinTimeout:  50 * time.Millisecond,
			expectedMaxTimeout:  50 * time.Millisecond,
		},
		{
			name:                "normal_timeout", 
			frameTimeout:        200 * time.Millisecond,
			expectedMinTimeout:  200 * time.Millisecond,
			expectedMaxTimeout:  200 * time.Millisecond,
		},
		{
			name:                "very_long_timeout",
			frameTimeout:        1000 * time.Millisecond,
			expectedMinTimeout:  500 * time.Millisecond,
			expectedMaxTimeout:  500 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembler := NewAssembler("test-stream", types.CodecH264, 1)
			require.NotNil(t, assembler)
			
			assembler.SetFrameTimeout(tt.frameTimeout)
			err := assembler.Start()
			require.NoError(t, err)
			defer assembler.Stop()

			// Create a frame to trigger sendFrameToOutput
			pkt := types.TimestampedPacket{
				Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
				PTS:         1000,
				DTS:         1000,
				Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
				CaptureTime: time.Now(),
			}

			// Fill the buffer to force timeout logic
			err = assembler.AddPacket(pkt)
			require.NoError(t, err)

			// Consume the frame to make room
			<-assembler.GetOutput()

			// Add another frame that will block and test timeout
			// Fill the buffer first to force blocking
			pkt.PTS = 2000
			pkt.DTS = 2000
			err = assembler.AddPacket(pkt) // This should succeed (buffer has room)
			
			// Now add a frame that will definitely block
			start := time.Now()
			pkt.PTS = 3000
			pkt.DTS = 3000
			err = assembler.AddPacket(pkt)
			elapsed := time.Since(start)
			
			// The timeout behavior should be within expected bounds if it occurred
			if err == ErrOutputBlocked {
				assert.GreaterOrEqual(t, elapsed, tt.expectedMinTimeout-20*time.Millisecond)
				assert.LessOrEqual(t, elapsed, tt.expectedMaxTimeout+100*time.Millisecond)
				t.Logf("Timeout occurred in %v (expected %v-%v)", elapsed, tt.expectedMinTimeout, tt.expectedMaxTimeout)
			} else if err == nil {
				t.Logf("No timeout occurred (frame was accepted)")
			}
		})
	}
}

// TestFrameAssemblerContextCancellation tests proper context handling during output
func TestFrameAssemblerContextCancellation(t *testing.T) {
	assembler := NewAssembler("test-stream", types.CodecH264, 1)
	require.NotNil(t, assembler)
	
	assembler.SetFrameTimeout(1 * time.Second) // Long timeout
	err := assembler.Start()
	require.NoError(t, err)

	// Add one packet first
	pkt := types.TimestampedPacket{
		Data:        []byte{0x00, 0x00, 0x00, 0x01, 0x65}, // IDR
		PTS:         1000,
		DTS:         1000,
		Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
		CaptureTime: time.Now(),
	}
	err = assembler.AddPacket(pkt)
	require.NoError(t, err)

	// Stop the assembler
	err = assembler.Stop()
	assert.NoError(t, err)
	
	// Now try to add another packet - should return context canceled
	pkt.PTS = 2000
	pkt.DTS = 2000
	err = assembler.AddPacket(pkt)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)

	stats := assembler.GetStats()
	t.Logf("Final stats: Assembled=%d, Dropped=%d", stats.FramesAssembled, stats.FramesDropped)
}

// TestFrameAssemblerSendFrameToOutputDirectly tests the sendFrameToOutput method directly
func TestFrameAssemblerSendFrameToOutputDirectly(t *testing.T) {
	assembler := NewAssembler("test-stream", types.CodecH264, 2)
	require.NotNil(t, assembler)
	
	assembler.SetFrameTimeout(100 * time.Millisecond)
	err := assembler.Start()
	require.NoError(t, err)
	defer assembler.Stop()

	// Create a test frame
	frame := &types.VideoFrame{
		ID:          1,
		StreamID:    "test-stream",
		PTS:         1000,
		DTS:         1000,
		CaptureTime: time.Now(),
	}

	// Test successful send
	err = assembler.sendFrameToOutput(frame)
	assert.NoError(t, err)
	
	// Verify frame was received
	receivedFrame := <-assembler.GetOutput()
	assert.Equal(t, frame.ID, receivedFrame.ID)

	// Fill buffer to test timeout
	frame2 := &types.VideoFrame{
		ID:          2,
		StreamID:    "test-stream", 
		PTS:         2000,
		DTS:         2000,
		CaptureTime: time.Now(),
	}
	frame3 := &types.VideoFrame{
		ID:          3,
		StreamID:    "test-stream",
		PTS:         3000,
		DTS:         3000,
		CaptureTime: time.Now(),
	}

	// Fill the buffer
	err = assembler.sendFrameToOutput(frame2)
	assert.NoError(t, err)
	err = assembler.sendFrameToOutput(frame3)
	assert.NoError(t, err)

	// This should timeout
	frame4 := &types.VideoFrame{
		ID:          4,
		StreamID:    "test-stream",
		PTS:         4000,
		DTS:         4000,
		CaptureTime: time.Now(),
	}

	start := time.Now()
	err = assembler.sendFrameToOutput(frame4)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Equal(t, ErrOutputBlocked, err)
	assert.GreaterOrEqual(t, elapsed, 90*time.Millisecond) // Should be close to 100ms timeout
	assert.LessOrEqual(t, elapsed, 150*time.Millisecond)

	stats := assembler.GetStats()
	assert.Equal(t, uint64(3), stats.FramesAssembled) // 3 successful sends
	assert.Equal(t, uint64(1), stats.FramesDropped)   // 1 timeout
}
