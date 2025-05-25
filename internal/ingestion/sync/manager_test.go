package sync

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestNewManager(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		mgr := NewManager("test-stream", nil, nil)
		assert.NotNil(t, mgr)
		assert.Equal(t, "test-stream", mgr.streamID)
		assert.NotNil(t, mgr.config)
		assert.NotNil(t, mgr.status)
		assert.True(t, mgr.status.InSync)
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &SyncConfig{
			MaxAudioDrift: 20 * time.Millisecond,
		}
		mgr := NewManager("test-stream", config, nil)
		assert.Equal(t, 20*time.Millisecond, mgr.config.MaxAudioDrift)
	})
}

func TestInitializeTracks(t *testing.T) {
	mgr := NewManager("test", nil, testLogger())

	t.Run("initialize video", func(t *testing.T) {
		err := mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
		require.NoError(t, err)
		assert.NotNil(t, mgr.videoSync)

		// Should error on double init
		err = mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
		assert.Error(t, err)
	})

	t.Run("initialize audio", func(t *testing.T) {
		err := mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
		require.NoError(t, err)
		assert.NotNil(t, mgr.audioSync)

		// Should error on double init
		err = mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
		assert.Error(t, err)
	})
}

func TestProcessVideoFrame(t *testing.T) {
	mgr := NewManager("test", nil, testLogger())
	
	t.Run("error when not initialized", func(t *testing.T) {
		frame := &types.VideoFrame{
			PTS:         1000,
			DTS:         1000,
			CaptureTime: time.Now(),
		}
		err := mgr.ProcessVideoFrame(frame)
		assert.Error(t, err)
	})

	t.Run("processes frame correctly", func(t *testing.T) {
		mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
		
		frame := &types.VideoFrame{
			PTS:         90000, // 1 second
			DTS:         90000,
			CaptureTime: time.Now(),
		}
		
		err := mgr.ProcessVideoFrame(frame)
		require.NoError(t, err)
		assert.False(t, frame.PresentationTime.IsZero())
		
		status := mgr.GetSyncStatus()
		assert.Equal(t, uint64(1), status.VideoSync.FrameCount)
	})
}

func TestProcessAudioPacket(t *testing.T) {
	mgr := NewManager("test", nil, testLogger())
	
	t.Run("error when not initialized", func(t *testing.T) {
		packet := &types.TimestampedPacket{
			PTS:         1000,
			DTS:         1000,
			CaptureTime: time.Now(),
		}
		err := mgr.ProcessAudioPacket(packet)
		assert.Error(t, err)
	})

	t.Run("processes packet correctly", func(t *testing.T) {
		mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
		
		packet := &types.TimestampedPacket{
			PTS:         48000, // 1 second
			DTS:         48000,
			CaptureTime: time.Now(),
		}
		
		err := mgr.ProcessAudioPacket(packet)
		require.NoError(t, err)
		assert.False(t, packet.PresentationTime.IsZero())
		
		status := mgr.GetSyncStatus()
		assert.Equal(t, uint64(1), status.AudioSync.FrameCount)
	})

	t.Run("applies audio offset", func(t *testing.T) {
		mgr := NewManager("test", nil, testLogger())
		mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
		mgr.SetAudioOffset(50 * time.Millisecond)
		
		packet := &types.TimestampedPacket{
			PTS:         0,
			DTS:         0,
			CaptureTime: time.Now(),
		}
		
		err := mgr.ProcessAudioPacket(packet)
		require.NoError(t, err)
		
		// Presentation time should include offset
		baseTime := mgr.audioSync.GetPresentationTime(0)
		expectedTime := baseTime.Add(50 * time.Millisecond)
		assert.Equal(t, expectedTime, packet.PresentationTime)
	})
}

func TestDriftMeasurement(t *testing.T) {
	mgr := NewManager("test", nil, testLogger())
	mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
	mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
	
	baseTime := time.Now()
	
	// Process video frame
	videoFrame := &types.VideoFrame{
		PTS:         90000, // 1 second
		DTS:         90000,
		CaptureTime: baseTime,
	}
	mgr.ProcessVideoFrame(videoFrame)
	
	// Process audio packet at same logical time
	audioPacket := &types.TimestampedPacket{
		PTS:         48000, // 1 second
		DTS:         48000,
		CaptureTime: baseTime,
	}
	mgr.ProcessAudioPacket(audioPacket)
	
	// Check drift measurement
	status := mgr.GetSyncStatus()
	assert.NotEmpty(t, status.DriftWindow)
	assert.Equal(t, 1, len(status.DriftWindow))
	
	// With same capture times and equivalent PTS, drift should be minimal
	assert.Less(t, abs(int64(status.CurrentDrift)), int64(time.Millisecond))
}

func TestDriftCorrection(t *testing.T) {
	config := &SyncConfig{
		MaxAudioDrift:      20 * time.Millisecond,
		EnableAutoCorrect:  true,
		CorrectionFactor:   0.5,
		MaxCorrectionStep:  10 * time.Millisecond,
		CorrectionInterval: 0, // Immediate correction for testing
	}
	
	mgr := NewManager("test", config, testLogger())
	mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
	mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
	
	baseTime := time.Now()
	
	// Create significant drift by processing frames with different capture times
	for i := 0; i < 15; i++ {
		// Video frame
		videoFrame := &types.VideoFrame{
			PTS:         int64(i * 3000), // ~33ms per frame
			DTS:         int64(i * 3000),
			CaptureTime: baseTime.Add(time.Duration(i*33) * time.Millisecond),
		}
		mgr.ProcessVideoFrame(videoFrame)
		
		// Audio packet with 50ms drift
		audioPacket := &types.TimestampedPacket{
			PTS:         int64(i * 1600), // ~33ms at 48kHz
			DTS:         int64(i * 1600),
			CaptureTime: baseTime.Add(time.Duration(i*33+50) * time.Millisecond),
		}
		mgr.ProcessAudioPacket(audioPacket)
	}
	
	// Check that correction was applied
	status := mgr.GetSyncStatus()
	assert.NotEmpty(t, status.Corrections)
	assert.NotZero(t, mgr.audioOffset)
}

func TestManagerReportDropped(t *testing.T) {
	mgr := NewManager("test", nil, testLogger())
	mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
	mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
	
	mgr.ReportVideoDropped(5)
	mgr.ReportAudioDropped(3)
	
	status := mgr.GetSyncStatus()
	assert.Equal(t, uint64(5), status.VideoSync.DroppedCount)
	assert.Equal(t, uint64(3), status.AudioSync.DroppedCount)
}

func TestManagerReset(t *testing.T) {
	mgr := NewManager("test", nil, testLogger())
	mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
	mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
	
	// Process some frames
	baseTime := time.Now()
	videoFrame := &types.VideoFrame{
		PTS:         90000,
		DTS:         90000,
		CaptureTime: baseTime,
	}
	mgr.ProcessVideoFrame(videoFrame)
	
	audioPacket := &types.TimestampedPacket{
		PTS:         48000,
		DTS:         48000,
		CaptureTime: baseTime,
	}
	mgr.ProcessAudioPacket(audioPacket)
	
	// Set offset and report drops
	mgr.SetAudioOffset(50 * time.Millisecond)
	mgr.ReportVideoDropped(5)
	
	// Reset
	mgr.Reset()
	
	// Check everything is reset
	status := mgr.GetSyncStatus()
	assert.True(t, status.InSync)
	assert.Empty(t, status.DriftWindow)
	assert.Empty(t, status.Corrections)
	assert.Zero(t, mgr.audioOffset)
	assert.Equal(t, uint64(0), status.VideoSync.FrameCount)
	assert.Equal(t, uint64(0), status.AudioSync.FrameCount)
}

func TestManagerGetStatistics(t *testing.T) {
	mgr := NewManager("test-stream", nil, testLogger())
	mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
	mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
	
	stats := mgr.GetStatistics()
	
	assert.Equal(t, "test-stream", stats["stream_id"])
	assert.Equal(t, true, stats["in_sync"])
	assert.NotNil(t, stats["video"])
	assert.NotNil(t, stats["audio"])
	assert.Equal(t, int64(0), stats["current_drift_ms"])
	assert.Equal(t, uint64(0), stats["correction_count"])
}

func BenchmarkProcessFrames(b *testing.B) {
	mgr := NewManager("bench", nil, testLogger())
	mgr.InitializeVideo(types.Rational{Num: 1, Den: 90000})
	mgr.InitializeAudio(types.Rational{Num: 1, Den: 48000})
	
	baseTime := time.Now()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		videoFrame := &types.VideoFrame{
			PTS:         int64(i * 3000),
			DTS:         int64(i * 3000),
			CaptureTime: baseTime.Add(time.Duration(i*33) * time.Millisecond),
		}
		mgr.ProcessVideoFrame(videoFrame)
		
		if i%3 == 0 { // Process audio every 3rd frame
			audioPacket := &types.TimestampedPacket{
				PTS:         int64(i * 1600),
				DTS:         int64(i * 1600),
				CaptureTime: baseTime.Add(time.Duration(i*33) * time.Millisecond),
			}
			mgr.ProcessAudioPacket(audioPacket)
		}
	}
}
