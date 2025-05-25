package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestDriftMeasurementDetailed(t *testing.T) {
	logger := testLogger()
	streamID := "test-stream"

	config := &SyncConfig{
		MaxAudioDrift:      40 * time.Millisecond,
		EnableDriftLogging: true,
		DriftLogInterval:   100 * time.Millisecond,
	}

	manager := NewManager(streamID, config, logger)

	// Initialize tracks
	videoTimeBase := types.NewRational(1, 90000) // 90kHz
	audioTimeBase := types.NewRational(1, 48000) // 48kHz

	err := manager.InitializeVideo(videoTimeBase)
	require.NoError(t, err)

	err = manager.InitializeAudio(audioTimeBase)
	require.NoError(t, err)

	// Process first video frame to establish baseline
	baseTime := time.Now()
	videoFrame := &types.VideoFrame{
		PTS:         0,
		DTS:         0,
		CaptureTime: baseTime,
	}
	err = manager.ProcessVideoFrame(videoFrame)
	require.NoError(t, err)

	// Process first audio packet at same time
	audioPacket := &types.TimestampedPacket{
		PTS:         0,
		DTS:         0,
		CaptureTime: baseTime,
	}
	err = manager.ProcessAudioPacket(audioPacket)
	require.NoError(t, err)

	// Check initial state - should be in sync
	stats := manager.GetStatistics()
	t.Logf("Initial stats: %+v", stats)

	inSync, ok := stats["in_sync"].(bool)
	assert.True(t, ok)
	assert.True(t, inSync, "Should be in sync initially")

	currentDriftMs, ok := stats["current_drift_ms"].(int64)
	assert.True(t, ok)
	assert.Equal(t, int64(0), currentDriftMs, "Initial drift should be 0")

	// Process more frames with exact timing
	for i := 1; i <= 10; i++ {
		// Video at 30fps: 33.33ms per frame = 3000 ticks at 90kHz
		videoFrame := &types.VideoFrame{
			PTS:         int64(i) * 3000,
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33333 * time.Microsecond),
		}
		err = manager.ProcessVideoFrame(videoFrame)
		require.NoError(t, err)

		// Audio at ~46.875fps (48000/1024): 21.33ms per frame = 1024 ticks at 48kHz
		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i) * 1024,
			DTS:         int64(i) * 1024,
			CaptureTime: baseTime.Add(time.Duration(i) * 21333 * time.Microsecond),
		}
		err = manager.ProcessAudioPacket(audioPacket)
		require.NoError(t, err)
	}

	// Check drift after processing
	stats = manager.GetStatistics()
	t.Logf("Final stats: %+v", stats)

	currentDriftMs, ok = stats["current_drift_ms"].(int64)
	assert.True(t, ok)
	t.Logf("Current drift: %dms", currentDriftMs)

	// The drift should be approximately the difference in frame timing
	// Video advances 33.33ms per iteration, audio advances 21.33ms
	// So video should be ahead by ~12ms per iteration * 10 = 120ms
	// But we're measuring at the wall clock time, so it depends on when we measure

	videoStats, ok := stats["video"].(map[string]interface{})
	assert.True(t, ok)
	t.Logf("Video stats: %+v", videoStats)

	audioStats, ok := stats["audio"].(map[string]interface{})
	assert.True(t, ok)
	t.Logf("Audio stats: %+v", audioStats)
}

func TestDriftWithAudioDelay(t *testing.T) {
	logger := testLogger()
	streamID := "test-stream"

	config := DefaultSyncConfig()
	manager := NewManager(streamID, config, logger)

	// Initialize tracks
	manager.InitializeVideo(types.NewRational(1, 90000))
	manager.InitializeAudio(types.NewRational(1, 48000))

	baseTime := time.Now()

	// Process synchronized frames first
	for i := 0; i < 5; i++ {
		videoFrame := &types.VideoFrame{
			PTS:         int64(i) * 3000,
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33 * time.Millisecond),
		}
		manager.ProcessVideoFrame(videoFrame)

		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i) * 1600,
			DTS:         int64(i) * 1600,
			CaptureTime: baseTime.Add(time.Duration(i) * 33 * time.Millisecond),
		}
		manager.ProcessAudioPacket(audioPacket)
	}

	// Now add delay to audio
	for i := 5; i < 10; i++ {
		videoFrame := &types.VideoFrame{
			PTS:         int64(i) * 3000,
			DTS:         int64(i) * 3000,
			CaptureTime: baseTime.Add(time.Duration(i) * 33 * time.Millisecond),
		}
		manager.ProcessVideoFrame(videoFrame)

		// Audio arrives 50ms late
		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i) * 1600,
			DTS:         int64(i) * 1600,
			CaptureTime: baseTime.Add(time.Duration(i)*33*time.Millisecond + 50*time.Millisecond),
		}
		manager.ProcessAudioPacket(audioPacket)
	}

	stats := manager.GetStatistics()
	t.Logf("Stats after delay: %+v", stats)

	// Should detect drift
	currentDriftMs, _ := stats["current_drift_ms"].(int64)
	t.Logf("Detected drift: %dms", currentDriftMs)

	// With the fix, large wall clock drift (50ms) is weighted at 30%
	// So total drift = 0ms PTS drift + (50ms * 0.3) = 15ms
	assert.InDelta(t, -15, currentDriftMs, 5, "Should detect approximately -15ms drift with weighted calculation")

	// With default 40ms threshold, 25ms drift is still considered "in sync"
	// Let's verify the drift is detected even if still within tolerance
	avgDriftMs, _ := stats["avg_drift_ms"].(int64)
	assert.NotZero(t, avgDriftMs, "Should have non-zero average drift")
}
