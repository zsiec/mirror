package gop

import (
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// TestGOPBuffer_FrameIndexConsistency tests that frame indices remain consistent during concurrent operations
func TestGOPBuffer_FrameIndexConsistency(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    1024 * 1024,
		MaxDuration: 30 * time.Second,
		Codec:       types.CodecH264,
	}

	buffer := NewBuffer("test-stream", config, logger)

	// Create test GOP with multiple frames
	gop := createTestGOPWithFrames(1, 5)
	buffer.AddGOP(gop)

	// Verify initial frame index consistency
	t.Run("Initial Index Consistency", func(t *testing.T) {
		for i, frame := range gop.Frames {
			location := buffer.frameIndex[frame.ID]
			require.NotNil(t, location, "Frame %d should have index entry", i)
			assert.Equal(t, gop, location.GOP, "Frame %d should point to correct GOP", i)
			assert.Equal(t, i, location.Position, "Frame %d should have correct position", i)
		}
	})

	// Test concurrent frame access while modifying
	t.Run("Concurrent Frame Access", func(t *testing.T) {
		var wg sync.WaitGroup
		const numReaders = 10
		const numOperations = 100
		errors := make(chan error, numReaders*numOperations)

		// Start readers that continuously access frames
		for i := 0; i < numReaders; i++ {
			wg.Add(1)
			go func(readerID int) {
				defer wg.Done()
				for j := 0; j < numOperations; j++ {
					for _, frame := range gop.Frames {
						retrievedFrame := buffer.GetFrame(frame.ID)
						if retrievedFrame == nil {
							continue // Frame may have been dropped
						}

						// Verify frame consistency
						if retrievedFrame.ID != frame.ID {
							errors <- assert.AnError
							return
						}
					}
					time.Sleep(time.Microsecond)
				}
			}(i)
		}

		// Concurrently drop frames to test index updates
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond) // Let readers start

			// Drop B frames (this should update frame indices)
			buffer.DropFramesForPressure(0.6) // Should drop B frames

			// Wait a bit and drop more
			time.Sleep(10 * time.Millisecond)
			buffer.DropFramesForPressure(0.8) // Should drop P frames
		}()

		wg.Wait()
		close(errors)

		// Check if any errors occurred
		errorCount := 0
		for err := range errors {
			if err != nil {
				errorCount++
			}
		}
		assert.Equal(t, 0, errorCount, "Should not have frame access errors")
	})
}

// TestGOPBuffer_FrameIndexAfterDrops tests frame index consistency after various drop operations
func TestGOPBuffer_FrameIndexAfterDrops(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     5,
		MaxBytes:    1024 * 1024,
		MaxDuration: 30 * time.Second,
		Codec:       types.CodecH264,
	}

	t.Run("B-Frame Drops", func(t *testing.T) {
		buffer := NewBuffer("test-stream", config, logger)
		gop := createTestGOPWithFrames(1, 10) // I, P, B, P, B, P, B, P, B, P
		buffer.AddGOP(gop)

		// Drop B frames
		droppedFrames := buffer.DropFramesForPressure(0.6)
		assert.Greater(t, len(droppedFrames), 0, "Should drop some B frames")

		// Verify remaining frame indices are correct
		buffer.mu.RLock()
		for i, frame := range gop.Frames {
			location, exists := buffer.frameIndex[frame.ID]
			require.True(t, exists, "Frame %d should still have index entry", i)
			assert.Equal(t, i, location.Position, "Frame %d should have correct updated position", i)
			assert.Equal(t, gop, location.GOP, "Frame %d should point to correct GOP", i)
		}
		buffer.mu.RUnlock()
	})

	t.Run("P-Frame Drops", func(t *testing.T) {
		buffer := NewBuffer("test-stream", config, logger)
		gop := createTestGOPWithFrames(2, 8)
		buffer.AddGOP(gop)

		// Drop P frames
		droppedFrames := buffer.DropFramesForPressure(0.75)
		assert.Greater(t, len(droppedFrames), 0, "Should drop some frames")

		// Verify remaining frame indices are correct
		buffer.mu.RLock()
		for i, frame := range gop.Frames {
			location, exists := buffer.frameIndex[frame.ID]
			require.True(t, exists, "Frame %d should still have index entry", i)
			assert.Equal(t, i, location.Position, "Frame %d should have correct updated position", i)
		}
		buffer.mu.RUnlock()
	})

	t.Run("Drop All Non-Keyframes", func(t *testing.T) {
		buffer := NewBuffer("test-stream", config, logger)
		gop := createTestGOPWithFrames(3, 6)
		buffer.AddGOP(gop)

		// Drop all non-keyframes
		droppedFrames := buffer.DropFramesForPressure(0.95)
		assert.Greater(t, len(droppedFrames), 0, "Should drop non-keyframes")

		// Should only have keyframes left
		buffer.mu.RLock()
		for i, frame := range gop.Frames {
			assert.True(t, frame.IsKeyframe(), "Only keyframes should remain")
			location, exists := buffer.frameIndex[frame.ID]
			require.True(t, exists, "Keyframe %d should have index entry", i)
			assert.Equal(t, i, location.Position, "Keyframe %d should have correct position", i)
		}
		buffer.mu.RUnlock()
	})
}

// TestGOPBuffer_FrameIndexConcurrentModification tests frame index integrity under heavy concurrent modification
func TestGOPBuffer_FrameIndexConcurrentModification(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     20,
		MaxBytes:    10 * 1024 * 1024,
		MaxDuration: 60 * time.Second,
		Codec:       types.CodecH264,
	}

	buffer := NewBuffer("test-stream", config, logger)

	// Add multiple GOPs
	for i := uint64(1); i <= 10; i++ {
		gop := createTestGOPWithFrames(i, 8)
		buffer.AddGOP(gop)
	}

	var wg sync.WaitGroup
	const numModifiers = 5
	const numReaders = 10
	const duration = 200 * time.Millisecond

	stopChan := make(chan struct{})

	// Start frame readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			inconsistencies := 0
			reads := 0

			for {
				select {
				case <-stopChan:
					t.Logf("Reader %d: %d reads, %d inconsistencies", readerID, reads, inconsistencies)
					return
				default:
					// Read frame index randomly
					frameID := uint64(readerID*100 + reads%50 + 1)
					frame := buffer.GetFrame(frameID)
					reads++

					if frame != nil {
						// Verify frame index consistency
						buffer.mu.RLock()
						if location, exists := buffer.frameIndex[frame.ID]; exists {
							if location.Position >= len(location.GOP.Frames) ||
								location.GOP.Frames[location.Position].ID != frame.ID {
								inconsistencies++
							}
						}
						buffer.mu.RUnlock()
					}
					time.Sleep(time.Microsecond)
				}
			}
		}(i)
	}

	// Start frame modifiers
	for i := 0; i < numModifiers; i++ {
		wg.Add(1)
		go func(modifierID int) {
			defer wg.Done()
			modifications := 0

			for {
				select {
				case <-stopChan:
					t.Logf("Modifier %d: %d modifications", modifierID, modifications)
					return
				default:
					// Randomly drop frames from different GOPs
					pressure := 0.5 + float64(modifierID)*0.1
					buffer.DropFramesForPressure(pressure)
					modifications++
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Let the test run for a while
	time.Sleep(duration)
	close(stopChan)
	wg.Wait()

	// Final consistency check
	t.Run("Final Consistency Check", func(t *testing.T) {
		buffer.mu.RLock()
		defer buffer.mu.RUnlock()

		for frameID, location := range buffer.frameIndex {
			// Verify frame exists in GOP at correct position
			require.Less(t, location.Position, len(location.GOP.Frames),
				"Frame position should be within GOP bounds")

			actualFrame := location.GOP.Frames[location.Position]
			assert.Equal(t, frameID, actualFrame.ID,
				"Frame at position should match indexed frame ID")
		}
	})
}

// TestGOPBuffer_DropFramesFromGOP tests the DropFramesFromGOP method for index consistency
func TestGOPBuffer_DropFramesFromGOP(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := BufferConfig{
		MaxGOPs:     5,
		MaxBytes:    1024 * 1024,
		MaxDuration: 30 * time.Second,
		Codec:       types.CodecH264,
	}

	buffer := NewBuffer("test-stream", config, logger)
	gop := createTestGOPWithFrames(1, 6)
	buffer.AddGOP(gop)

	originalFrameCount := len(gop.Frames)

	// Drop frames from index 3 onwards
	droppedFrames := buffer.DropFramesFromGOP(gop.ID, 3)

	assert.Equal(t, 3, originalFrameCount-len(droppedFrames),
		"Should have 3 frames remaining")
	assert.Equal(t, 3, len(gop.Frames), "GOP should have 3 frames")

	// Verify frame index consistency
	buffer.mu.RLock()
	for i, frame := range gop.Frames {
		location, exists := buffer.frameIndex[frame.ID]
		require.True(t, exists, "Frame %d should have index entry", i)
		assert.Equal(t, i, location.Position, "Frame %d should have correct position", i)
		assert.Equal(t, gop, location.GOP, "Frame %d should point to correct GOP", i)
	}

	// Verify dropped frames are not in index
	for _, droppedFrame := range droppedFrames {
		_, exists := buffer.frameIndex[droppedFrame.ID]
		assert.False(t, exists, "Dropped frame should not be in index")
	}
	buffer.mu.RUnlock()
}

// createTestGOPWithFrames creates a test GOP with specified number of frames
func createTestGOPWithFrames(gopID uint64, frameCount int) *types.GOP {
	gop := &types.GOP{
		ID:        gopID,
		Frames:    make([]*types.VideoFrame, frameCount),
		StartTime: time.Now(),
	}

	for i := 0; i < frameCount; i++ {
		frameType := types.FrameTypeP
		if i == 0 {
			frameType = types.FrameTypeI
		} else if i%3 == 0 {
			frameType = types.FrameTypeB
		}

		frame := &types.VideoFrame{
			ID:          gopID*100 + uint64(i) + 1,
			Type:        frameType,
			TotalSize:   1024,
			PTS:         int64(i) * 3000,
			DTS:         int64(i) * 3000,
			CaptureTime: time.Now(),
		}

		gop.Frames[i] = frame
		gop.TotalSize += int64(frame.TotalSize)

		if frameType == types.FrameTypeI {
			gop.Keyframe = frame
		} else if frameType == types.FrameTypeP {
			gop.PFrameCount++
		} else if frameType == types.FrameTypeB {
			gop.BFrameCount++
		}
	}

	gop.Closed = true
	return gop
}
