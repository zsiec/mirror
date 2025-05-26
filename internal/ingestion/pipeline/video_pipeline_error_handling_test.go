package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestVideoPipelineErrorHandling tests comprehensive error handling scenarios
func TestVideoPipelineErrorHandling(t *testing.T) {
	t.Run("EmptyPacketValidation", func(t *testing.T) {
		// Test that empty packets are properly rejected
		input := make(chan types.TimestampedPacket, 10)
		cfg := Config{
			StreamID:        "test-empty-packets",
			Codec:           types.CodecH264,
			FrameBufferSize: 10,
		}

		pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
		require.NoError(t, err)
		require.NotNil(t, pipeline)

		err = pipeline.Start()
		require.NoError(t, err)

		// Send empty packet (should be rejected)
		emptyPacket := types.TimestampedPacket{
			Type: types.PacketTypeVideo,
			Data: []byte{}, // Empty data
			PTS:  1000,
			DTS:  1000,
		}
		input <- emptyPacket

		// Send valid packet
		validPacket := types.TimestampedPacket{
			Type: types.PacketTypeVideo,
			Data: []byte{0x00, 0x00, 0x00, 0x01, 0x67}, // H.264 SPS start
			PTS:  2000,
			DTS:  2000,
		}
		input <- validPacket

		// Wait for processing
		time.Sleep(100 * time.Millisecond)

		stats := pipeline.GetStats()
		assert.Equal(t, uint64(1), stats.PacketsProcessed, "Should process valid packet only")
		assert.Greater(t, stats.Errors, uint64(0), "Should have errors from empty packet")

		err = pipeline.Stop()
		assert.NoError(t, err)
	})

	t.Run("CriticalErrorDetection", func(t *testing.T) {
		// Test that critical errors are properly detected
		testCases := []struct {
			name        string
			err         error
			shouldStop  bool
		}{
			{
				name:       "OutOfMemoryError",
				err:        errors.New("out of memory"),
				shouldStop: true,
			},
			{
				name:       "CorruptedDataError", 
				err:        errors.New("data corrupted"),
				shouldStop: true,
			},
			{
				name:       "RegularError",
				err:        errors.New("normal processing error"),
				shouldStop: false,
			},
			{
				name:       "NilError",
				err:        nil,
				shouldStop: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				input := make(chan types.TimestampedPacket, 10)
				cfg := Config{
					StreamID:        "test-critical-errors",
					Codec:           types.CodecH264,
					FrameBufferSize: 10,
				}

				pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
				require.NoError(t, err)

				result := pipeline.isCriticalError(tc.err)
				assert.Equal(t, tc.shouldStop, result, "Critical error detection failed for %s", tc.name)
			})
		}
	})

	t.Run("NilInputValidation", func(t *testing.T) {
		// Test that nil input channel is properly handled
		cfg := Config{
			StreamID:        "test-nil-input",
			Codec:           types.CodecH264,
			FrameBufferSize: 10,
		}

		pipeline, err := NewVideoPipeline(context.Background(), cfg, nil)
		require.NoError(t, err)

		err = pipeline.Start()
		require.NoError(t, err)

		// Wait for component validation to detect nil input
		time.Sleep(100 * time.Millisecond)

		stats := pipeline.GetStats()
		assert.Greater(t, stats.Errors, uint64(0), "Should have errors from nil input validation")

		err = pipeline.Stop()
		assert.NoError(t, err)
	})

	t.Run("ShutdownSafety", func(t *testing.T) {
		// Test that pipeline shuts down safely even in error conditions
		input := make(chan types.TimestampedPacket, 10)
		cfg := Config{
			StreamID:        "test-shutdown-safety",
			Codec:           types.CodecH264,
			FrameBufferSize: 10,
		}

		pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
		require.NoError(t, err)

		err = pipeline.Start()
		require.NoError(t, err)

		// Send some invalid data to trigger errors
		invalidPacket := types.TimestampedPacket{
			Type: types.PacketTypeVideo,
			Data: []byte{0xFF, 0xFF, 0xFF, 0xFF}, // Invalid H.264 data
			PTS:  1000,
			DTS:  1000,
		}
		input <- invalidPacket

		time.Sleep(50 * time.Millisecond)

		// Should still shut down cleanly despite errors
		err = pipeline.Stop()
		assert.NoError(t, err)
	})
}

// TestVideoPipelineResourceCleanupEnhanced tests enhanced resource cleanup
func TestVideoPipelineResourceCleanupEnhanced(t *testing.T) {
	t.Run("GracefulShutdownWithPendingFrames", func(t *testing.T) {
		input := make(chan types.TimestampedPacket, 10)
		cfg := Config{
			StreamID:        "test-graceful-shutdown",
			Codec:           types.CodecH264,
			FrameBufferSize: 100,
		}

		pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
		require.NoError(t, err)

		err = pipeline.Start()
		require.NoError(t, err)

		// Send multiple packets
		for i := 0; i < 10; i++ {
			packet := types.TimestampedPacket{
				Type: types.PacketTypeVideo,
				Data: []byte{0x00, 0x00, 0x00, 0x01, 0x67, byte(i)},
				PTS:  int64(i * 1000),
				DTS:  int64(i * 1000),
			}
			input <- packet
		}

		// Wait for some processing
		time.Sleep(100 * time.Millisecond)

		// Stop should complete gracefully
		start := time.Now()
		err = pipeline.Stop()
		elapsed := time.Since(start)

		assert.NoError(t, err)
		assert.Less(t, elapsed, 6*time.Second, "Stop should complete within timeout")

		stats := pipeline.GetStats()
		assert.Greater(t, stats.PacketsProcessed, uint64(0), "Should have processed packets")
		t.Logf("Processed %d packets, %d frames, %d errors", 
			stats.PacketsProcessed, stats.FramesOutput, stats.Errors)
	})

	t.Run("MultipleStopCalls", func(t *testing.T) {
		// Test that multiple Stop() calls don't cause issues
		input := make(chan types.TimestampedPacket, 10)
		cfg := Config{
			StreamID:        "test-multiple-stops",
			Codec:           types.CodecH264,
			FrameBufferSize: 10,
		}

		pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
		require.NoError(t, err)

		err = pipeline.Start()
		require.NoError(t, err)

		// Call Stop multiple times concurrently
		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := pipeline.Stop()
				assert.NoError(t, err)
			}()
		}

		wg.Wait()
		t.Log("Multiple stop calls completed successfully")
	})

	t.Run("ResourceLeakPrevention", func(t *testing.T) {
		// Test that resources are properly cleaned up
		input := make(chan types.TimestampedPacket, 10)
		cfg := Config{
			StreamID:        "test-resource-leak",
			Codec:           types.CodecH264,
			FrameBufferSize: 10,
		}

		pipeline, err := NewVideoPipeline(context.Background(), cfg, input)
		require.NoError(t, err)

		err = pipeline.Start()
		require.NoError(t, err)

		// Send packets
		for i := 0; i < 5; i++ {
			packet := types.TimestampedPacket{
				Type: types.PacketTypeVideo,
				Data: []byte{0x00, 0x00, 0x00, 0x01, 0x67, byte(i)},
				PTS:  int64(i * 1000),
				DTS:  int64(i * 1000),
			}
			input <- packet
		}

		time.Sleep(50 * time.Millisecond)

		err = pipeline.Stop()
		require.NoError(t, err)

		// Verify channel is closed
		select {
		case _, ok := <-pipeline.GetOutput():
			assert.False(t, ok, "Output channel should be closed")
		case <-time.After(100 * time.Millisecond):
			t.Error("Output channel should have been closed")
		}
	})
}