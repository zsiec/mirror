package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestNewTrackSyncManager(t *testing.T) {
	tests := []struct {
		name      string
		trackType TrackType
		streamID  string
		timeBase  types.Rational
		config    *SyncConfig
	}{
		{
			name:      "video track with default config",
			trackType: TrackTypeVideo,
			streamID:  "test-stream-1",
			timeBase:  types.Rational{Num: 1, Den: 90000},
			config:    nil,
		},
		{
			name:      "audio track with custom config",
			trackType: TrackTypeAudio,
			streamID:  "test-stream-2",
			timeBase:  types.Rational{Num: 1, Den: 48000},
			config:    &SyncConfig{MaxAudioDrift: 20 * time.Millisecond},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewTrackSyncManager(tt.trackType, tt.streamID, tt.timeBase, tt.config)

			assert.NotNil(t, mgr)
			assert.Equal(t, tt.trackType, mgr.sync.Type)
			assert.Equal(t, tt.streamID, mgr.sync.StreamID)
			assert.Equal(t, tt.timeBase, mgr.sync.TimeBase)
			assert.NotNil(t, mgr.config)
		})
	}
}

func TestProcessTimestamp(t *testing.T) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "test", types.Rational{Num: 1, Den: 90000}, nil)
	baseTime := time.Now()

	t.Run("first timestamp initializes base", func(t *testing.T) {
		err := mgr.ProcessTimestamp(1000, 1000, baseTime)
		require.NoError(t, err)

		state := mgr.GetSyncState()
		assert.Equal(t, baseTime, state.BaseTime)
		assert.Equal(t, int64(1000), state.BasePTS)
		assert.Equal(t, int64(1000), state.LastPTS)
		assert.Equal(t, uint64(1), state.FrameCount)
	})

	t.Run("subsequent timestamp updates state", func(t *testing.T) {
		err := mgr.ProcessTimestamp(4000, 4000, baseTime.Add(100*time.Millisecond))
		require.NoError(t, err)

		state := mgr.GetSyncState()
		assert.Equal(t, int64(4000), state.LastPTS)
		assert.Equal(t, uint64(2), state.FrameCount)
	})

	t.Run("PTS wrap detection", func(t *testing.T) {
		// Simulate 33-bit wrap (MPEG-TS PTS/DTS are always 33-bit)
		mgr.sync.LastPTS = (1 << 33) - 256 // Near max 33-bit
		err := mgr.ProcessTimestamp(100, 100, baseTime.Add(time.Second))
		require.NoError(t, err)

		state := mgr.GetSyncState()
		assert.Equal(t, 1, state.PTSWrapCount)
	})

	t.Run("PTS jump detection", func(t *testing.T) {
		mgr.Reset()
		mgr.ProcessTimestamp(1000, 1000, baseTime)

		// Jump more than 1 second
		err := mgr.ProcessTimestamp(100000, 100000, baseTime.Add(time.Second))
		require.NoError(t, err)

		state := mgr.GetSyncState()
		assert.Equal(t, uint64(1), state.PTSJumps)
	})

	t.Run("DTS ordering error", func(t *testing.T) {
		mgr.Reset()
		mgr.ProcessTimestamp(1000, 1000, baseTime)
		mgr.ProcessTimestamp(2000, 2000, baseTime.Add(33*time.Millisecond))

		// DTS goes backwards
		err := mgr.ProcessTimestamp(3000, 1500, baseTime.Add(66*time.Millisecond))
		require.NoError(t, err)

		state := mgr.GetSyncState()
		assert.Equal(t, uint64(1), state.DTSErrors)
	})
}

func TestGetPresentationTime(t *testing.T) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "test", types.Rational{Num: 1, Den: 90000}, nil)
	baseTime := time.Now()

	t.Run("uninitialized returns current time", func(t *testing.T) {
		pt := mgr.GetPresentationTime(1000)
		assert.WithinDuration(t, time.Now(), pt, time.Millisecond)
	})

	t.Run("calculates correct presentation time", func(t *testing.T) {
		mgr.ProcessTimestamp(0, 0, baseTime)

		// 90000 units = 1 second at 90kHz
		pt := mgr.GetPresentationTime(90000)
		expected := baseTime.Add(time.Second)
		assert.Equal(t, expected, pt)
	})

	t.Run("handles fractional seconds", func(t *testing.T) {
		mgr.Reset()
		mgr.ProcessTimestamp(0, 0, baseTime)

		// 45000 units = 0.5 seconds at 90kHz
		pt := mgr.GetPresentationTime(45000)
		expected := baseTime.Add(500 * time.Millisecond)
		assert.Equal(t, expected, pt)
	})

	t.Run("audio time base calculation", func(t *testing.T) {
		audioMgr := NewTrackSyncManager(TrackTypeAudio, "audio", types.Rational{Num: 1, Den: 48000}, nil)
		audioMgr.ProcessTimestamp(0, 0, baseTime)

		// 48000 units = 1 second at 48kHz
		pt := audioMgr.GetPresentationTime(48000)
		expected := baseTime.Add(time.Second)
		assert.Equal(t, expected, pt)
	})
}

func TestGetDriftFromWallClock(t *testing.T) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "test", types.Rational{Num: 1, Den: 90000}, nil)
	baseTime := time.Now()

	mgr.ProcessTimestamp(0, 0, baseTime)

	tests := []struct {
		name          string
		pts           int64
		actualTime    time.Time
		expectedDrift time.Duration
	}{
		{
			name:          "no drift",
			pts:           90000, // 1 second
			actualTime:    baseTime.Add(time.Second),
			expectedDrift: 0,
		},
		{
			name:          "positive drift (actual ahead)",
			pts:           90000,
			actualTime:    baseTime.Add(time.Second + 50*time.Millisecond),
			expectedDrift: 50 * time.Millisecond,
		},
		{
			name:          "negative drift (actual behind)",
			pts:           90000,
			actualTime:    baseTime.Add(time.Second - 30*time.Millisecond),
			expectedDrift: -30 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drift := mgr.GetDriftFromWallClock(tt.pts, tt.actualTime)
			assert.Equal(t, tt.expectedDrift, drift)
		})
	}
}

func TestReportDropped(t *testing.T) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "test", types.Rational{Num: 1, Den: 90000}, nil)

	mgr.ReportDropped(5)
	state := mgr.GetSyncState()
	assert.Equal(t, uint64(5), state.DroppedCount)

	mgr.ReportDropped(3)
	state = mgr.GetSyncState()
	assert.Equal(t, uint64(8), state.DroppedCount)
}

func TestReset(t *testing.T) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "test", types.Rational{Num: 1, Den: 90000}, nil)
	baseTime := time.Now()

	// Set up some state
	mgr.ProcessTimestamp(1000, 1000, baseTime)
	mgr.ProcessTimestamp(2000, 2000, baseTime.Add(33*time.Millisecond))
	mgr.ReportDropped(5)

	// Reset
	mgr.Reset()

	state := mgr.GetSyncState()
	assert.True(t, state.BaseTime.IsZero())
	assert.Equal(t, int64(0), state.BasePTS)
	assert.Equal(t, int64(0), state.LastPTS)
	assert.Equal(t, uint64(0), state.FrameCount)
	assert.Equal(t, uint64(0), state.DroppedCount)
}

func TestGetStatistics(t *testing.T) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "test-stream", types.Rational{Num: 1, Den: 90000}, nil)
	baseTime := time.Now()

	mgr.ProcessTimestamp(1000, 1000, baseTime)
	mgr.ProcessTimestamp(2000, 2000, baseTime.Add(33*time.Millisecond))
	mgr.ReportDropped(2)

	stats := mgr.GetStatistics()

	assert.Equal(t, "video", stats["track_type"])
	assert.Equal(t, "test-stream", stats["stream_id"])
	assert.Equal(t, uint64(2), stats["frame_count"])
	assert.Equal(t, uint64(2), stats["dropped_count"])
	assert.Equal(t, int64(2000), stats["last_pts"])
	assert.NotNil(t, stats["base_time"])
	assert.NotNil(t, stats["stream_duration"])
}

func BenchmarkProcessTimestamp(b *testing.B) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "bench", types.Rational{Num: 1, Den: 90000}, nil)
	baseTime := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pts := int64(i * 3000) // ~33ms per frame at 90kHz
		mgr.ProcessTimestamp(pts, pts, baseTime.Add(time.Duration(i*33)*time.Millisecond))
	}
}

func BenchmarkGetPresentationTime(b *testing.B) {
	mgr := NewTrackSyncManager(TrackTypeVideo, "bench", types.Rational{Num: 1, Den: 90000}, nil)
	mgr.ProcessTimestamp(0, 0, time.Now())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.GetPresentationTime(int64(i * 3000))
	}
}
