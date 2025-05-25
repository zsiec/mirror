package sync

import (
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// SyncStatusType represents the sync status
type SyncStatusType string

const (
	SyncStatusUnknown    SyncStatusType = "unknown"
	SyncStatusSynced     SyncStatusType = "synced"
	SyncStatusDrifting   SyncStatusType = "drifting"
	SyncStatusCorrecting SyncStatusType = "correcting"
)

// TrackType identifies the type of media track
type TrackType string

const (
	TrackTypeVideo TrackType = "video"
	TrackTypeAudio TrackType = "audio"
)

// TrackSync maintains synchronization state for a single track
type TrackSync struct {
	// Track identification
	Type      TrackType
	StreamID  string
	TimeBase  types.Rational // Time base (e.g., 1/90000 for video, 1/48000 for audio)

	// Timing state
	LastPTS      int64     // Last presentation timestamp
	LastDTS      int64     // Last decode timestamp
	LastWallTime time.Time // Wall clock time of last sample

	// Synchronization references
	BaseTime    time.Time // Reference wall clock time
	BasePTS     int64     // Reference PTS
	PTSWrapCount int      // Number of PTS wraps detected

	// Statistics
	FrameCount   uint64 // Total frames/samples processed
	PTSJumps     uint64 // Number of PTS discontinuities
	DTSErrors    uint64 // DTS ordering errors
	DroppedCount uint64 // Dropped frames/samples
}

// DriftSample represents a single drift measurement
type DriftSample struct {
	Timestamp    time.Time     // When measurement was taken
	VideoPTS     int64         // Video PTS at measurement
	AudioPTS     int64         // Audio PTS at measurement
	Drift        time.Duration // Calculated drift (positive = video ahead)
	
	// Detailed drift components
	PTSDrift      time.Duration // Drift based on timestamps
	ProcessingLag time.Duration // Processing/delivery delay
}

// DriftCorrection represents a drift correction action
type DriftCorrection struct {
	Timestamp  time.Time     // When correction was applied
	Method     string        // Correction method used
	Amount     time.Duration // Correction amount
	Reason     string        // Why correction was needed
}

// SyncStatus represents the current synchronization state
type SyncStatus struct {
	// Overall sync state
	InSync       bool          // Whether streams are synchronized
	MaxDrift     time.Duration // Maximum acceptable drift
	CurrentDrift time.Duration // Current measured drift

	// Track states
	VideoSync *TrackSync // Video track sync state
	AudioSync *TrackSync // Audio track sync state

	// Drift history
	DriftWindow    []DriftSample    // Recent drift measurements
	Corrections    []DriftCorrection // Applied corrections
	LastCorrection time.Time        // Last correction time

	// Statistics
	AvgDrift       time.Duration // Average drift over window
	DriftVariance  float64       // Drift stability measure
	CorrectionRate float64       // Corrections per minute
}

// StreamSyncStatus represents sync status for a single stream
type StreamSyncStatus struct {
	StreamID     string        // Stream identifier
	LastPTS      int64         // Last processed PTS
	LastDTS      int64         // Last processed DTS
	Offset       time.Duration // Offset from master
	InSync       bool          // Whether in sync with master
	DroppedFrames uint64       // Frames dropped for sync
}

// SyncGroupStatus represents multi-stream sync status
type SyncGroupStatus struct {
	GroupID      string                       // Sync group identifier
	GroupName    string                       // Human-readable name
	MasterStream string                       // Master stream ID
	StreamStatus map[string]*StreamSyncStatus // Per-stream status
	InSync       bool                         // Overall sync status
	MaxOffset    time.Duration                // Maximum offset in group
}

// SyncConfig holds synchronization configuration
type SyncConfig struct {
	// Drift thresholds
	MaxAudioDrift      time.Duration // Maximum acceptable audio drift
	MaxVideoDrift      time.Duration // Maximum acceptable video drift
	CorrectionInterval time.Duration // How often to check drift

	// Correction parameters
	EnableAutoCorrect  bool    // Enable automatic drift correction
	CorrectionFactor   float64 // How aggressively to correct (0-1)
	MaxCorrectionStep  time.Duration // Maximum correction per step

	// Multi-stream sync
	MultiStreamTolerance time.Duration // Tolerance for multi-stream sync
	MasterClockSource    string        // NTP, system, or stream ID

	// Debug options
	EnableDriftLogging bool // Log drift measurements
	DriftLogInterval   time.Duration // How often to log drift
}

// DefaultSyncConfig returns default synchronization configuration
func DefaultSyncConfig() *SyncConfig {
	return &SyncConfig{
		MaxAudioDrift:        40 * time.Millisecond,
		MaxVideoDrift:        40 * time.Millisecond,
		CorrectionInterval:   100 * time.Millisecond,
		EnableAutoCorrect:    true,
		CorrectionFactor:     0.1, // Correct 10% per iteration
		MaxCorrectionStep:    5 * time.Millisecond,
		MultiStreamTolerance: 50 * time.Millisecond,
		MasterClockSource:    "system",
		EnableDriftLogging:   false,
		DriftLogInterval:     time.Second,
	}
}
