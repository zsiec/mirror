package sync

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func TestP1_6_DriftCalculationFixed(t *testing.T) {
	// Test that drift calculation properly combines PTS drift and wall clock drift
	// instead of averaging them
	baseLog := logrus.New()
	baseLog.SetLevel(logrus.DebugLevel)
	log := logger.NewLogrusAdapter(logrus.NewEntry(baseLog))
	config := DefaultSyncConfig()

	// Create track sync managers
	videoSync := NewTrackSyncManager(TrackTypeVideo, "test-stream",
		types.Rational{Num: 1, Den: 90000}, config)
	audioSync := NewTrackSyncManager(TrackTypeAudio, "test-stream",
		types.Rational{Num: 1, Den: 48000}, config)

	// Initialize with non-zero base timestamps
	now := time.Now()
	videoSync.ProcessTimestamp(1000, 1000, now)
	audioSync.ProcessTimestamp(1000, 1000, now)

	// Process frames with different arrival times to simulate processing lag
	videoSync.ProcessTimestamp(91000, 91000, now.Add(900*time.Millisecond)) // Video arrives 100ms late
	audioSync.ProcessTimestamp(51400, 51400, now.Add(time.Second))          // Audio arrives on time

	m := &Manager{
		streamID:        "test-stream",
		config:          config,
		videoSync:       videoSync,
		audioSync:       audioSync,
		driftWindow:     []DriftSample{},
		driftWindowSize: 100,
		status: &SyncStatus{
			InSync:    true,
			MaxDrift:  config.MaxAudioDrift,
			VideoSync: videoSync.GetSyncState(),
			AudioSync: audioSync.GetSyncState(),
		},
		logger: log,
	}

	// Debug: Check states before measuring
	vState := m.videoSync.GetSyncState()
	aState := m.audioSync.GetSyncState()
	t.Logf("Before measure - Video LastPTS: %d, Audio LastPTS: %d", vState.LastPTS, aState.LastPTS)
	t.Logf("Video LastWallTime: %v, Audio LastWallTime: %v", vState.LastWallTime, aState.LastWallTime)

	// Measure drift with proper locking
	m.mu.Lock()
	m.measureDrift()
	m.mu.Unlock()

	// Verify drift calculation
	t.Logf("Drift window length: %d", len(m.driftWindow))
	assert.NotEmpty(t, m.driftWindow)
	if len(m.driftWindow) > 0 {
		lastDrift := m.driftWindow[len(m.driftWindow)-1]

		// Should have separate components tracked
		assert.NotZero(t, lastDrift.PTSDrift, "PTS drift should be tracked")
		assert.NotZero(t, lastDrift.ProcessingLag, "Processing lag should be tracked")

		// Total drift should be weighted, not averaged
		avgDrift := (lastDrift.PTSDrift + lastDrift.ProcessingLag) / 2
		assert.NotEqual(t, avgDrift, lastDrift.Drift,
			"Total drift should not be simple average of components")

		// With 100ms processing lag, total drift should apply 30% weight to lag
		expectedDrift := lastDrift.PTSDrift + time.Duration(float64(lastDrift.ProcessingLag)*0.3)
		assert.InDelta(t, float64(expectedDrift), float64(lastDrift.Drift), float64(time.Millisecond),
			"Drift should be PTS drift + 30% of processing lag")
	}
}
