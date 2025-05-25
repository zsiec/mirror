package sync

import (
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TrackSyncManager manages timing for a single media track
type TrackSyncManager struct {
	sync         *TrackSync
	config       *SyncConfig
	wrapDetector *PTSWrapDetector
	mu           sync.RWMutex
}

// NewTrackSyncManager creates a new track synchronization manager
func NewTrackSyncManager(trackType TrackType, streamID string, timeBase types.Rational, config *SyncConfig) *TrackSyncManager {
	if config == nil {
		config = DefaultSyncConfig()
	}

	return &TrackSyncManager{
		sync: &TrackSync{
			Type:     trackType,
			StreamID: streamID,
			TimeBase: timeBase,
			BaseTime: time.Time{}, // Will be set on first sample
			BasePTS:  0,
		},
		config:       config,
		wrapDetector: NewPTSWrapDetector(timeBase),
	}
}

// ProcessTimestamp updates track timing with a new timestamp
func (t *TrackSyncManager) ProcessTimestamp(pts, dts int64, wallTime time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Initialize base time on first sample
	if t.sync.BaseTime.IsZero() {
		t.sync.BaseTime = wallTime
		t.sync.BasePTS = pts
		t.sync.LastPTS = pts
		t.sync.LastDTS = dts
		t.sync.LastWallTime = wallTime
		t.sync.FrameCount = 1
		return nil
	}

	// Check for PTS wrap using our detector
	if t.wrapDetector.DetectWrap(pts, t.sync.LastPTS) {
		t.sync.PTSWrapCount++
	}

	// Check for PTS jump/discontinuity
	// We keep expectedPTS for potential future use (e.g., frame drop detection)
	_ = t.sync.LastPTS + t.estimateFrameDuration()

	// Check absolute difference from last PTS for large jumps
	absoluteDiff := abs(pts - t.sync.LastPTS)

	// Detect significant jumps (more than 1 second)
	// Calculate PTS units per second: Den/Num
	oneSecondInPTS := int64(t.sync.TimeBase.Den) / int64(t.sync.TimeBase.Num)

	// Use the absolute difference for jump detection, not the difference from expected
	if absoluteDiff > oneSecondInPTS {
		t.sync.PTSJumps++

		// Reset base time on very large jumps (>10 seconds)
		if absoluteDiff > 10*oneSecondInPTS {
			t.sync.BaseTime = wallTime
			t.sync.BasePTS = pts
		}
	}

	// Check DTS ordering
	if dts != 0 && dts < t.sync.LastDTS {
		t.sync.DTSErrors++
	}

	// Update state
	t.sync.LastPTS = pts
	t.sync.LastDTS = dts
	t.sync.LastWallTime = wallTime
	t.sync.FrameCount++

	return nil
}

// GetPresentationTime calculates the presentation time for a given PTS
func (t *TrackSyncManager) GetPresentationTime(pts int64) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Handle uninitialized state
	if t.sync.BaseTime.IsZero() {
		return time.Now()
	}

	// Calculate PTS delta accounting for wraps
	ptsDelta := t.calculatePTSDelta(pts)

	// Convert to duration based on time base
	// duration = ptsDelta * timeBase.Num / timeBase.Den
	nsDelta := (ptsDelta * int64(time.Second) * int64(t.sync.TimeBase.Num)) / int64(t.sync.TimeBase.Den)

	return t.sync.BaseTime.Add(time.Duration(nsDelta))
}

// GetDecodeTime calculates the decode time for a given DTS
func (t *TrackSyncManager) GetDecodeTime(dts int64) time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.sync.BaseTime.IsZero() || dts == 0 {
		return time.Time{}
	}

	// Use PTS as base if DTS equals PTS
	if dts == t.sync.LastPTS {
		return t.GetPresentationTime(dts)
	}

	// Calculate relative to base PTS/time
	dtsDelta := dts - t.sync.BasePTS
	nsDelta := (dtsDelta * int64(time.Second) * int64(t.sync.TimeBase.Num)) / int64(t.sync.TimeBase.Den)

	return t.sync.BaseTime.Add(time.Duration(nsDelta))
}

// GetSyncState returns the current synchronization state
func (t *TrackSyncManager) GetSyncState() *TrackSync {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Return a copy to avoid race conditions
	state := *t.sync
	return &state
}

// Reset resets the synchronization state
func (t *TrackSyncManager) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.sync.BaseTime = time.Time{}
	t.sync.BasePTS = 0
	t.sync.LastPTS = 0
	t.sync.LastDTS = 0
	t.sync.LastWallTime = time.Time{}
	t.sync.PTSWrapCount = 0
	t.sync.FrameCount = 0
	t.sync.PTSJumps = 0
	t.sync.DTSErrors = 0
	t.sync.DroppedCount = 0
}

// ReportDropped reports dropped frames/samples
func (t *TrackSyncManager) ReportDropped(count uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sync.DroppedCount += count
}

// calculatePTSDelta calculates PTS delta accounting for wraps
func (t *TrackSyncManager) calculatePTSDelta(pts int64) int64 {
	// Use our wrap detector to calculate the proper delta
	return t.wrapDetector.CalculatePTSDelta(pts, t.sync.BasePTS, t.sync.PTSWrapCount)
}

// estimateFrameDuration estimates the duration of one frame/sample in PTS units
func (t *TrackSyncManager) estimateFrameDuration() int64 {
	switch t.sync.Type {
	case TrackTypeVideo:
		// Try to calculate actual frame rate from recent frame history
		if t.sync.FrameCount > 1 && !t.sync.BaseTime.IsZero() {
			// Calculate elapsed time and frames to estimate frame rate
			elapsedTime := t.sync.LastWallTime.Sub(t.sync.BaseTime)
			if elapsedTime > 0 {
				estimatedFPS := float64(t.sync.FrameCount-1) / elapsedTime.Seconds()

				// Sanity check: frame rate should be between 1 and 120 fps
				if estimatedFPS >= 1.0 && estimatedFPS <= 120.0 {
					// Calculate frame duration in PTS units
					return int64(float64(t.sync.TimeBase.Den) / (estimatedFPS * float64(t.sync.TimeBase.Num)))
				}
			}
		}

		// Fallback to common frame rates based on timebase
		if t.sync.TimeBase.Den >= 90000 {
			// High frequency timebase (like 90kHz)
			// Use precise calculations for standard rates
			if t.sync.TimeBase.Den == 90000 && t.sync.TimeBase.Num == 1 {
				// Standard 90kHz timebase - use exact values
				return 3000 // 30fps: 90000/30 = 3000
			}

			// For NTSC 29.97fps: 90000 * 1001 / 30000 = 3003
			if t.sync.TimeBase.Den == 90000 && t.sync.TimeBase.Num == 1 {
				// Check if this might be NTSC by looking at recent frame timing
				// For now, default to 30fps exact
				return 3000
			}

			// Try common frame rates: 29.97, 30, 25, 24, 60, 50
			commonRates := []float64{30.0, 29.97, 25.0, 24.0, 60.0, 50.0, 23.976}

			for _, rate := range commonRates {
				duration := int64(float64(t.sync.TimeBase.Den) / (rate * float64(t.sync.TimeBase.Num)))
				// Check if this gives a reasonable integer duration
				if duration > 0 && duration < int64(t.sync.TimeBase.Den) {
					return duration
				}
			}

			// Ultimate fallback for 90kHz timebase
			return int64(t.sync.TimeBase.Den) / (30 * int64(t.sync.TimeBase.Num))
		} else {
			// Low frequency timebase, likely frame-based
			return 1
		}
	case TrackTypeAudio:
		// For audio, assume 1024 samples per frame (typical for AAC)
		// Duration = samples * timeBase.Den / (sampleRate * timeBase.Num)
		// Try common audio sample rates: 48kHz, 44.1kHz, 32kHz, 16kHz
		sampleRates := []int64{48000, 44100, 32000, 16000}
		samplesPerFrame := int64(1024)

		for _, sampleRate := range sampleRates {
			duration := (samplesPerFrame * int64(t.sync.TimeBase.Den)) / (sampleRate * int64(t.sync.TimeBase.Num))
			if duration > 0 {
				return duration
			}
		}

		// Fallback calculation
		return (samplesPerFrame * int64(t.sync.TimeBase.Den)) / (48000 * int64(t.sync.TimeBase.Num))
	default:
		return 0
	}
}

// GetDriftFromWallClock calculates drift from expected wall clock time
func (t *TrackSyncManager) GetDriftFromWallClock(pts int64, actualWallTime time.Time) time.Duration {
	expectedTime := t.GetPresentationTime(pts)
	return actualWallTime.Sub(expectedTime)
}

// GetStatistics returns track synchronization statistics
func (t *TrackSyncManager) GetStatistics() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := map[string]interface{}{
		"track_type":    string(t.sync.Type),
		"stream_id":     t.sync.StreamID,
		"frame_count":   t.sync.FrameCount,
		"pts_jumps":     t.sync.PTSJumps,
		"dts_errors":    t.sync.DTSErrors,
		"dropped_count": t.sync.DroppedCount,
		"pts_wraps":     t.sync.PTSWrapCount,
		"last_pts":      t.sync.LastPTS,
		"last_dts":      t.sync.LastDTS,
	}

	if !t.sync.BaseTime.IsZero() {
		stats["base_time"] = t.sync.BaseTime.Format(time.RFC3339Nano)
		stats["last_wall_time"] = t.sync.LastWallTime.Format(time.RFC3339Nano)
		stats["stream_duration"] = time.Since(t.sync.BaseTime).String()
	}

	return stats
}

// abs returns the absolute value of an int64
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// String returns a string representation of the track sync state
func (t *TrackSyncManager) String() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return fmt.Sprintf("TrackSync{Type: %s, Stream: %s, Frames: %d, LastPTS: %d, Jumps: %d, Errors: %d}",
		t.sync.Type, t.sync.StreamID, t.sync.FrameCount, t.sync.LastPTS, t.sync.PTSJumps, t.sync.DTSErrors)
}
