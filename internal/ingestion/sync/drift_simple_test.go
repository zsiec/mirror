package sync

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// TestSimpleDriftCalculation verifies the basic drift calculation
func TestSimpleDriftCalculation(t *testing.T) {
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	config := DefaultSyncConfig()
	config.EnableDriftLogging = true

	manager := NewManager("test-stream", config, log)
	manager.InitializeVideo(types.NewRational(1, 90000)) // 90kHz
	manager.InitializeAudio(types.NewRational(1, 90000)) // 90kHz

	now := time.Now()

	// Frame 0: Both arrive at same time with same PTS
	manager.ProcessVideoFrame(&types.VideoFrame{
		PTS: 0, DTS: 0, CaptureTime: now,
	})
	manager.ProcessAudioPacket(&types.TimestampedPacket{
		PTS: 0, DTS: 0, CaptureTime: now,
	})

	// Frame 1: Video at 1s, Audio at 1s + 50ms (audio ahead in PTS)
	manager.ProcessVideoFrame(&types.VideoFrame{
		PTS:         90000, // 1 second
		DTS:         90000,
		CaptureTime: now.Add(1 * time.Second),
	})
	manager.ProcessAudioPacket(&types.TimestampedPacket{
		PTS:         90000 + 4500, // 1.05 seconds (50ms ahead)
		DTS:         90000 + 4500,
		CaptureTime: now.Add(1 * time.Second), // Same wall time
	})

	status := manager.GetSyncStatus()

	// Check drift window
	assert.Len(t, status.DriftWindow, 1, "Should have one drift sample")

	if len(status.DriftWindow) > 0 {
		sample := status.DriftWindow[0]

		// Audio is 50ms ahead in PTS, so drift should be -50ms
		assert.Equal(t, -50*time.Millisecond, sample.PTSDrift, "PTS drift should be -50ms")

		// Both arrived at same wall time, so no processing lag
		assert.Equal(t, time.Duration(0), sample.ProcessingLag, "Processing lag should be 0")

		// Total drift should equal PTS drift
		assert.Equal(t, sample.PTSDrift, sample.Drift, "Total drift should equal PTS drift")
	}

	// Test with processing lag
	manager.ProcessVideoFrame(&types.VideoFrame{
		PTS:         180000, // 2 seconds
		DTS:         180000,
		CaptureTime: now.Add(2 * time.Second),
	})
	manager.ProcessAudioPacket(&types.TimestampedPacket{
		PTS:         180000, // Same PTS
		DTS:         180000,
		CaptureTime: now.Add(2*time.Second + 100*time.Millisecond), // 100ms late
	})

	status = manager.GetSyncStatus()
	// We'll have 3 samples: initial sync, after frame 1, and after video frame 2
	assert.GreaterOrEqual(t, len(status.DriftWindow), 3, "Should have at least 3 drift samples")

	if len(status.DriftWindow) >= 3 {
		// The last sample is after both video and audio frame 2 are processed
		lastSample := status.DriftWindow[len(status.DriftWindow)-1]

		// Same PTS, so no PTS drift
		assert.Equal(t, time.Duration(0), lastSample.PTSDrift, "PTS drift should be 0")

		// Audio arrived 100ms late
		assert.Equal(t, -100*time.Millisecond, lastSample.ProcessingLag, "Processing lag should be -100ms")

		// Total drift should equal PTS drift (0)
		assert.Equal(t, time.Duration(0), lastSample.Drift, "Total drift should be 0 (PTS drift)")
	}
}
