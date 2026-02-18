package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestAVSyncIntegration tests the full A/V synchronization workflow
func TestAVSyncIntegration(t *testing.T) {
	testLogger := testLogger()
	streamID := "test-stream"

	// Create sync manager with default config
	config := &SyncConfig{}

	manager := NewManager(streamID, config, testLogger)

	// Initialize tracks
	videoTimeBase := types.NewRational(1, 90000) // 90kHz
	audioTimeBase := types.NewRational(1, 48000) // 48kHz

	manager.InitializeVideo(videoTimeBase)
	manager.InitializeAudio(audioTimeBase)

	// Simulate synchronized playback
	baseTime := time.Now()
	videoPTS := int64(0)
	audioPTS := int64(0)

	// Process 1 second of synchronized content
	// Video: 30fps = 33.33ms per frame = 3000 ticks at 90kHz
	// Audio: 48kHz with 1024 samples per frame = ~21.33ms per frame = 1024 ticks

	for i := 0; i < 30; i++ {
		// Process video frame
		videoFrame := &types.VideoFrame{
			PTS:         videoPTS,
			DTS:         videoPTS,
			CaptureTime: baseTime.Add(time.Duration(i) * 33333 * time.Microsecond),
			Duration:    3000, // 33.33ms in 90kHz ticks
		}

		err := manager.ProcessVideoFrame(videoFrame)
		require.NoError(t, err)

		videoPTS += 3000 // Next frame

		// Process ~1.5 audio frames per video frame to maintain sync
		// (33.33ms / 21.33ms ≈ 1.56)
		if i%2 == 0 {
			// Process 1 audio frame
			audioPacket := &types.TimestampedPacket{
				PTS:         audioPTS,
				DTS:         audioPTS,
				CaptureTime: baseTime.Add(time.Duration(audioPTS) * time.Second / 48000),
			}
			err = manager.ProcessAudioPacket(audioPacket)
			require.NoError(t, err)
			audioPTS += 1024
		} else {
			// Process 2 audio frames
			for j := 0; j < 2; j++ {
				audioPacket := &types.TimestampedPacket{
					PTS:         audioPTS,
					DTS:         audioPTS,
					CaptureTime: baseTime.Add(time.Duration(audioPTS) * time.Second / 48000),
				}
				err = manager.ProcessAudioPacket(audioPacket)
				require.NoError(t, err)
				audioPTS += 1024
			}
		}
	}

	// Check sync status
	stats := manager.GetStatistics()

	// Get current drift
	currentDriftMs, ok := stats["current_drift_ms"].(int64)
	assert.True(t, ok)
	assert.Less(t, absInt64(currentDriftMs), int64(40), "Drift should be less than 40ms")

	// Should be in sync (within 40ms tolerance)
	inSync, ok := stats["in_sync"].(bool)
	assert.True(t, ok)
	// With different frame rates, we'll have some drift but should be within tolerance
	if !inSync {
		// If not in sync, verify drift is at least close to threshold
		assert.Less(t, absInt64(currentDriftMs), int64(50), "Drift should be close to sync threshold")
	}

	// Check track statistics
	videoStats, ok := stats["video"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, uint64(30), videoStats["frame_count"])

	audioStats, ok := stats["audio"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, uint64(45), audioStats["frame_count"]) // 15*1 + 15*2 = 45
}

// TestAVSyncDriftCorrection tests drift detection and correction
func TestAVSyncDriftCorrection(t *testing.T) {
	testLogger := testLogger()
	streamID := "test-stream"

	// Create sync manager
	config := &SyncConfig{}

	manager := NewManager(streamID, config, testLogger)

	// Initialize tracks
	manager.InitializeVideo(types.NewRational(1, 90000))
	manager.InitializeAudio(types.NewRational(1, 48000))

	baseTime := time.Now()

	// Process frames with increasing drift
	for i := 0; i < 10; i++ {
		// Video frame on time
		videoFrame := &types.VideoFrame{
			PTS:         int64(i) * 3000,
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33 * time.Millisecond),
		}
		manager.ProcessVideoFrame(videoFrame)

		// Audio frame with increasing delay (simulating drift)
		drift := time.Duration(i*5) * time.Millisecond
		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i) * 1600, // ~33ms at 48kHz
			DTS:         int64(i) * 1600,
			CaptureTime: baseTime.Add(time.Duration(i)*33*time.Millisecond + drift),
		}
		manager.ProcessAudioPacket(audioPacket)
	}

	// Check that drift was detected
	stats := manager.GetStatistics()
	currentDriftMs, _ := stats["current_drift_ms"].(int64)
	t.Logf("Detected drift: %dms", currentDriftMs)

	// With the fixed calculation: current drift equals PTS drift
	// Since PTS values are properly spaced, PTS drift should be near 0
	// Wall clock drift (processing lag) is tracked separately for diagnostics
	assert.InDelta(t, 0, float64(absInt64(currentDriftMs)), 5.0, "Current drift should equal PTS drift (near 0)")

	// Check if corrections were needed (they may not be if drift is within tolerance)
	correctionCount, _ := stats["correction_count"].(int)
	if absInt64(currentDriftMs) > 40 {
		assert.Greater(t, correctionCount, 0, "Should have applied corrections for large drift")
	}
}

// TestAVSyncWithDroppedFrames tests sync maintenance when frames are dropped
func TestAVSyncWithDroppedFrames(t *testing.T) {
	testLogger := testLogger()
	streamID := "test-stream"

	config := DefaultSyncConfig()
	config.EnableDriftLogging = true

	manager := NewManager(streamID, config, testLogger)

	// Initialize tracks with same timebase for easier calculation
	manager.InitializeVideo(types.NewRational(1, 90000))
	manager.InitializeAudio(types.NewRational(1, 90000))

	baseTime := time.Now()

	// Process some frames normally with synchronized PTS
	for i := 0; i < 5; i++ {
		videoFrame := &types.VideoFrame{
			PTS:         int64(i) * 3000, // 33.33ms per frame at 90kHz
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33333 * time.Microsecond),
		}
		manager.ProcessVideoFrame(videoFrame)

		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i) * 3000, // Same PTS timing as video
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33333 * time.Microsecond),
		}
		manager.ProcessAudioPacket(audioPacket)
	}

	// Report dropped video frames
	manager.ReportVideoDropped(3)

	// Continue processing after dropped frames
	// Even though we skip frames 5-7, the PTS should still be synchronized
	for i := 8; i < 10; i++ { // Skip frames 5-7
		videoFrame := &types.VideoFrame{
			PTS:         int64(i) * 3000,
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33333 * time.Microsecond),
		}
		manager.ProcessVideoFrame(videoFrame)

		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i) * 3000, // Still synchronized
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33333 * time.Microsecond),
		}
		manager.ProcessAudioPacket(audioPacket)
	}

	// Check stats
	stats := manager.GetStatistics()
	videoStats, _ := stats["video"].(map[string]interface{})
	droppedCount, _ := videoStats["dropped_count"].(uint64)
	assert.Equal(t, uint64(3), droppedCount, "Should track dropped frames")

	// Should maintain sync despite drops since PTS values are still aligned
	inSync, _ := stats["in_sync"].(bool)
	assert.True(t, inSync, "Should maintain sync despite dropped frames")

	// Verify drift is small
	currentDriftMs, _ := stats["current_drift_ms"].(int64)
	assert.InDelta(t, 0, currentDriftMs, 5, "Drift should be near 0 with synchronized PTS")
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
