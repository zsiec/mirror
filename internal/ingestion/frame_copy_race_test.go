package ingestion

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// TestP2_10_FrameCopyRaceCondition demonstrates the race condition
// when accessing frame data while it might be modified elsewhere
func TestP2_10_FrameCopyRaceCondition(t *testing.T) {
	// This test simulates the race between GetFramePreview reading frames
	// and other operations potentially modifying frame data
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	logger := logger.FromContext(ctx)
	
	// Create a minimal StreamHandler for testing
	h := &StreamHandler{
		streamID:        "test-stream",
		codec:           types.CodecH264,
		recentFrames:    make([]*types.VideoFrame, 0, 100),
		maxRecentFrames: 100,
		ctx:             ctx,
		logger:          logger,
	}
	
	// Number of concurrent operations
	numWriters := 5
	numReaders := 5
	iterations := 100
	
	var wg sync.WaitGroup
	
	// Writer goroutines - simulate frame processing that modifies frames
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < iterations; j++ {
				// Create a frame
				frame := &types.VideoFrame{
					ID:        uint64(id*1000 + j),
					StreamID:  "test-stream",
					Type:      types.FrameTypeP,
					PTS:       int64(j * 3000),
					TotalSize: 1000,
				}
				
				// Store it
				h.storeRecentFrame(frame)
				
				// Simulate modification of the frame after storage
				// This represents another part of the system modifying frame data
				frame.Type = types.FrameTypeB
				frame.TotalSize = 2000
				frame.PTS = int64(j * 4000)
				
				// Small delay to increase race likelihood
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	
	// Reader goroutines - simulate GetFramePreview access
	previewErrors := make(chan error, numReaders*iterations)
	
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < iterations; j++ {
				// Read frame preview
				preview, frameCount := h.GetFramePreview(1.0)
				
				// Verify the preview data makes sense
				if frameCount > 0 && len(preview) == 0 {
					previewErrors <- fmt.Errorf("empty preview with %d frames", frameCount)
				}
				
				// Check for data consistency
				previewStr := string(preview)
				if frameCount > 0 && previewStr == "" {
					previewErrors <- fmt.Errorf("invalid preview data")
				}
				
				time.Sleep(time.Microsecond)
			}
		}(i)
	}
	
	// Wait for all operations to complete
	wg.Wait()
	close(previewErrors)
	
	// Check for errors
	errorCount := 0
	for err := range previewErrors {
		t.Logf("Preview error: %v", err)
		errorCount++
	}
	
	// The test demonstrates potential race conditions
	// Run with: go test -race -run TestP2_10_FrameCopyRaceCondition
	t.Logf("Test completed with %d errors", errorCount)
}

// TestP2_10_FrameDataRace shows the specific race on frame fields
func TestP2_10_FrameDataRace(t *testing.T) {
	// Create a mock stream handler with recent frames storage
	handler := &StreamHandler{
		streamID:        "test-stream",
		recentFrames:    make([]*types.VideoFrame, 0, 100),
		maxRecentFrames: 100,
		logger:          logrus.NewEntry(logrus.New()),
		codec:           types.CodecH264,
	}
	
	var wg sync.WaitGroup
	
	// Writer goroutine - simulates pipeline modifying frames after storage
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			// Create a frame that will be stored
			frame := &types.VideoFrame{
				ID:        uint64(i),
				StreamID:  "test-stream",
				Type:      types.FrameTypeI,
				PTS:       int64(i * 1000),
				DTS:       int64(i * 1000),
				TotalSize: 1024 + i,
				NALUnits:  []types.NALUnit{{Type: 5, Data: []byte{0x00, 0x00, 0x00, 0x01, 0x65}}},
			}
			
			// Store the frame
			handler.storeRecentFrame(frame)
			
			// Simulate pipeline continuing to modify the frame
			// This should NOT affect the stored copy
			frame.Type = types.FrameTypeP
			frame.PTS = int64(i * 2000)
			frame.TotalSize = i * 2
		}
	}()
	
	// Reader goroutine - reads stored frames via GetFramePreview
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			// Simulate what GetFramePreview does
			preview, count := handler.GetFramePreview(1.0)
			_ = preview
			_ = count
			
			// Small delay to interleave with writes
			time.Sleep(time.Microsecond)
		}
	}()
	
	wg.Wait()
	
	// Verify the stored frames are not corrupted
	handler.recentFramesMu.RLock()
	defer handler.recentFramesMu.RUnlock()
	
	// Check that stored frames have consistent data
	for i, frame := range handler.recentFrames {
		// The stored frame should have the original values, not the modified ones
		expectedPTS := int64(i * 1000)
		if frame.PTS != expectedPTS {
			t.Errorf("Frame %d has corrupted PTS: got %d, want %d", i, frame.PTS, expectedPTS)
		}
	}
	
	t.Log("Frame data race test completed")
}

// TestP2_10_SafeFrameHandling shows the desired behavior after fix
func TestP2_10_SafeFrameHandling(t *testing.T) {
	t.Skip("This test will pass after implementing safe frame handling")
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	logger := logger.FromContext(ctx)
	
	h := &StreamHandler{
		streamID:        "test-stream",
		codec:           types.CodecH264,
		recentFrames:    make([]*types.VideoFrame, 0, 100),
		maxRecentFrames: 100,
		ctx:             ctx,
		logger:          logger,
	}
	
	const numGoroutines = 10
	const numFrames = 100
	
	var wg sync.WaitGroup
	
	// Concurrent frame storage
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < numFrames; j++ {
				frame := &types.VideoFrame{
					ID:        uint64(id*1000 + j),
					StreamID:  "test-stream",
					Type:      types.FrameTypeP,
					PTS:       int64(j * 3000),
					TotalSize: 1000 + j,
				}
				
				h.storeRecentFrame(frame)
				
				// After fix, modifying the original frame should not
				// affect the stored copy
				frame.Type = types.FrameTypeB
				frame.TotalSize = 99999
			}
		}(i)
	}
	
	// Concurrent preview reading
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			
			for j := 0; j < numFrames; j++ {
				preview, frameCount := h.GetFramePreview(1.0)
				
				// After fix, preview should always be consistent
				require.NotNil(t, preview)
				if frameCount > 0 {
					// Verify frame data is not corrupted
					previewStr := string(preview)
					assert.Contains(t, previewStr, "Stream: test-stream")
					assert.Contains(t, previewStr, "Codec: h264")
					
					// Frame sizes should be reasonable (not 99999)
					assert.NotContains(t, previewStr, "Size=99999")
				}
			}
		}()
	}
	
	wg.Wait()
	
	// Final verification
	preview, frameCount := h.GetFramePreview(3.0)
	t.Logf("Final preview: %d frames", frameCount)
	assert.NotNil(t, preview)
}
