package sync

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TestTrackSyncTimebaseCalculations tests time calculations with various timebases
func TestTrackSyncTimebaseCalculations(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	testCases := []struct {
		name              string
		timeBase          types.Rational
		fps               int
		expectedOneSecPTS int64
		description       string
	}{
		{
			name:              "Standard 90kHz video",
			timeBase:          types.Rational{Num: 1, Den: 90000},
			fps:               30,
			expectedOneSecPTS: 90000,
			description:       "Common for H.264/HEVC RTP",
		},
		{
			name:              "NTSC 29.97fps",
			timeBase:          types.Rational{Num: 1001, Den: 30000},
			fps:               30,
			expectedOneSecPTS: 29, // For timebase 1001/30000, 1 second = 30000/1001 ≈ 29.97, truncated to 29
			description:       "NTSC frame rate",
		},
		{
			name:              "Film 23.976fps",
			timeBase:          types.Rational{Num: 1001, Den: 24000},
			fps:               24,
			expectedOneSecPTS: 23, // For timebase 1001/24000, 1 second = 24000/1001 ≈ 23.976, truncated to 23
			description:       "Film frame rate",
		},
		{
			name:              "48kHz audio",
			timeBase:          types.Rational{Num: 1, Den: 48000},
			fps:               0, // Not applicable for audio
			expectedOneSecPTS: 48000,
			description:       "Common audio sample rate",
		},
		{
			name:              "1kHz timebase",
			timeBase:          types.Rational{Num: 1, Den: 1000},
			fps:               25,
			expectedOneSecPTS: 1000,
			description:       "Millisecond precision",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create sync manager with the timebase
			trackType := TrackTypeVideo
			if tc.fps == 0 {
				trackType = TrackTypeAudio
			}

			config := DefaultSyncConfig()
			manager := NewTrackSyncManager(trackType, "test-stream", tc.timeBase, config)

			// Initialize with first timestamp
			baseTime := time.Now()
			manager.ProcessTimestamp(0, 0, baseTime)

			// Process a timestamp that's exactly 1 second later
			pts := tc.expectedOneSecPTS
			manager.ProcessTimestamp(pts, pts, baseTime.Add(time.Second))

			// Verify no jumps were detected (1 second is our threshold)
			syncState := manager.GetSyncState()

			// Debug output for failing cases
			if syncState.PTSJumps > 0 {
				oneSecInManager := int64(tc.timeBase.Den) / int64(tc.timeBase.Num)
				t.Logf("Unexpected jump detected: timebase=%d/%d, pts=%d, expected1sec=%d, manager1sec=%d, lastPTS=%d",
					tc.timeBase.Num, tc.timeBase.Den, pts, tc.expectedOneSecPTS, oneSecInManager, syncState.LastPTS)
			}

			assert.Equal(t, uint64(0), syncState.PTSJumps,
				"Should not detect jump for exactly 1 second difference")

			// Test jump detection for 1.5 seconds
			jumpPTS := pts + tc.expectedOneSecPTS/2
			manager.ProcessTimestamp(jumpPTS*2, jumpPTS*2, baseTime.Add(3*time.Second))

			syncState = manager.GetSyncState()
			assert.Greater(t, syncState.PTSJumps, uint64(0),
				"Should detect jump for >1 second difference")
		})
	}
}

// TestPTSJumpDetection tests the corrected jump detection logic
func TestPTSJumpDetection(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Use NTSC timebase to test fractional calculations
	timeBase := types.Rational{Num: 1001, Den: 30000} // ~29.97 fps
	config := DefaultSyncConfig()
	manager := NewTrackSyncManager(TrackTypeVideo, "test", timeBase, config)

	baseTime := time.Now()

	// Initialize
	manager.ProcessTimestamp(0, 0, baseTime)

	// For NTSC with timebase 1001/30000:
	// Each PTS unit represents 1001/30000 seconds
	// So 1 frame at 29.97fps = 1/29.97 seconds = 1 PTS unit
	frameTime := int64(1) // One frame = 1 PTS unit

	// Process 30 frames (approximately 1 second)
	for i := int64(1); i <= 30; i++ {
		manager.ProcessTimestamp(i*frameTime, i*frameTime, baseTime.Add(time.Duration(i)*time.Second/30))
	}

	syncState := manager.GetSyncState()
	assert.Equal(t, uint64(0), syncState.PTSJumps, "Normal progression should not cause jumps")

	// Now test a jump of exactly 1 second (should not trigger)
	oneSecondPTS := int64(30000) / int64(1001) // 29 PTS units = ~1 second
	currentPTS := int64(30) * frameTime
	manager.ProcessTimestamp(currentPTS+oneSecondPTS, currentPTS+oneSecondPTS, baseTime.Add(2*time.Second))

	syncState = manager.GetSyncState()
	assert.Equal(t, uint64(0), syncState.PTSJumps, "Exactly 1 second should not trigger jump")

	// Test a jump of 2 seconds (should trigger)
	manager.ProcessTimestamp(currentPTS+2*oneSecondPTS+1000, currentPTS+2*oneSecondPTS+1000, baseTime.Add(4*time.Second))

	syncState = manager.GetSyncState()
	assert.Equal(t, uint64(1), syncState.PTSJumps, "2+ seconds should trigger jump")

	// Test a jump of 11 seconds (should trigger reset)
	oldBasePTS := syncState.BasePTS
	manager.ProcessTimestamp(currentPTS+11*oneSecondPTS, currentPTS+11*oneSecondPTS, baseTime.Add(15*time.Second))

	syncState = manager.GetSyncState()
	assert.Equal(t, uint64(2), syncState.PTSJumps, "11 seconds should trigger another jump")
	assert.NotEqual(t, oldBasePTS, syncState.BasePTS, "11 seconds should reset base PTS")
}

// TestEstimateFrameDuration tests frame duration estimation with different timebases
func TestEstimateFrameDuration(t *testing.T) {
	testCases := []struct {
		name             string
		trackType        TrackType
		timeBase         types.Rational
		expectedDuration int64
		description      string
	}{
		{
			name:             "30fps video with 90kHz",
			trackType:        TrackTypeVideo,
			timeBase:         types.Rational{Num: 1, Den: 90000},
			expectedDuration: 3000, // 90000 / 30
			description:      "Standard video",
		},
		{
			name:             "NTSC video",
			trackType:        TrackTypeVideo,
			timeBase:         types.Rational{Num: 1001, Den: 30000},
			expectedDuration: 1, // Low frequency timebase, frame-based
			description:      "NTSC 29.97fps",
		},
		{
			name:             "48kHz audio",
			trackType:        TrackTypeAudio,
			timeBase:         types.Rational{Num: 1, Den: 48000},
			expectedDuration: 1024, // 1024 samples at 48kHz
			description:      "AAC frame",
		},
		{
			name:             "44.1kHz audio",
			trackType:        TrackTypeAudio,
			timeBase:         types.Rational{Num: 1, Den: 44100},
			expectedDuration: 941, // (1024 * 44100) / (48000 * 1) ≈ 941
			description:      "CD quality audio",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultSyncConfig()
			manager := NewTrackSyncManager(tc.trackType, "test", tc.timeBase, config)

			// Access the private method through reflection (or make it public for testing)
			duration := manager.estimateFrameDuration()

			// Allow small rounding differences
			assert.InDelta(t, tc.expectedDuration, duration, 1.0,
				"Frame duration should be approximately %d for %s",
				tc.expectedDuration, tc.description)
		})
	}
}

// TestTimebaseConversion tests PTS to time conversion with various timebases
func TestTimebaseConversion(t *testing.T) {
	testCases := []struct {
		name         string
		timeBase     types.Rational
		pts          int64
		expectedTime time.Duration
	}{
		{
			name:         "90kHz 1 second",
			timeBase:     types.Rational{Num: 1, Den: 90000},
			pts:          90000,
			expectedTime: time.Second,
		},
		{
			name:         "48kHz half second",
			timeBase:     types.Rational{Num: 1, Den: 48000},
			pts:          24000,
			expectedTime: 500 * time.Millisecond,
		},
		{
			name:         "NTSC 1 second",
			timeBase:     types.Rational{Num: 1001, Den: 30000},
			pts:          29,                              // 30000/1001 truncated
			expectedTime: 29 * 1001 * time.Second / 30000, // Slightly less than 1 second
		},
		{
			name:         "Fractional second",
			timeBase:     types.Rational{Num: 1, Den: 1000},
			pts:          1500,
			expectedTime: 1500 * time.Millisecond,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultSyncConfig()
			manager := NewTrackSyncManager(TrackTypeVideo, "test", tc.timeBase, config)

			// Initialize at time zero
			baseTime := time.Now()
			manager.ProcessTimestamp(0, 0, baseTime)

			// Get presentation time for PTS
			presentTime := manager.GetPresentationTime(tc.pts)
			actualDuration := presentTime.Sub(baseTime)

			// Allow 1 microsecond tolerance for floating point calculations
			assert.InDelta(t, tc.expectedTime.Nanoseconds(), actualDuration.Nanoseconds(), 1000,
				"PTS %d with timebase %d/%d should equal %v",
				tc.pts, tc.timeBase.Num, tc.timeBase.Den, tc.expectedTime)
		})
	}
}
