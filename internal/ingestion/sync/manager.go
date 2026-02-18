package sync

import (
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// Manager handles audio/video synchronization for a single stream
type Manager struct {
	streamID string
	config   *SyncConfig

	// Track synchronization
	videoSync *TrackSyncManager
	audioSync *TrackSyncManager

	// Drift tracking
	driftWindow     []DriftSample
	driftWindowSize int
	corrections     []DriftCorrection

	// Sync adjustment
	audioOffset     time.Duration // Offset to apply to audio
	lastCorrection  time.Time
	correctionCount uint64

	// Status
	status *SyncStatus

	// Logging control
	lastDriftLog time.Time

	mu     sync.RWMutex
	logger logger.Logger
}

// NewManager creates a new A/V synchronization manager
func NewManager(streamID string, config *SyncConfig, log logger.Logger) *Manager {
	if config == nil {
		config = DefaultSyncConfig()
	}

	if log == nil {
		log = logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	}

	return &Manager{
		streamID:        streamID,
		config:          config,
		driftWindow:     make([]DriftSample, 0, 100),
		driftWindowSize: 100,
		corrections:     make([]DriftCorrection, 0, 50),
		status: &SyncStatus{
			InSync:   true,
			MaxDrift: config.MaxAudioDrift,
		},
		logger: log,
	}
}

// InitializeVideo initializes video track synchronization
func (m *Manager) InitializeVideo(timeBase types.Rational) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.videoSync != nil {
		return fmt.Errorf("video sync already initialized")
	}

	m.videoSync = NewTrackSyncManager(TrackTypeVideo, m.streamID, timeBase, m.config)
	m.status.VideoSync = m.videoSync.GetSyncState()

	m.logger.Info("Initialized video sync",
		"stream_id", m.streamID,
		"time_base", fmt.Sprintf("%d/%d", timeBase.Num, timeBase.Den))

	return nil
}

// InitializeAudio initializes audio track synchronization
func (m *Manager) InitializeAudio(timeBase types.Rational) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.audioSync != nil {
		return fmt.Errorf("audio sync already initialized")
	}

	m.audioSync = NewTrackSyncManager(TrackTypeAudio, m.streamID, timeBase, m.config)
	m.status.AudioSync = m.audioSync.GetSyncState()

	m.logger.Info("Initialized audio sync",
		"stream_id", m.streamID,
		"time_base", fmt.Sprintf("%d/%d", timeBase.Num, timeBase.Den))

	return nil
}

// ProcessVideoFrame updates video synchronization with a new frame
func (m *Manager) ProcessVideoFrame(frame *types.VideoFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.videoSync == nil {
		return fmt.Errorf("video sync not initialized")
	}

	// Update video timing
	err := m.videoSync.ProcessTimestamp(frame.PTS, frame.DTS, frame.CaptureTime)
	if err != nil {
		return fmt.Errorf("failed to process video timestamp: %w", err)
	}

	// Calculate and set presentation time
	frame.PresentationTime = m.videoSync.GetPresentationTime(frame.PTS)

	// Update status
	m.status.VideoSync = m.videoSync.GetSyncState()

	// Measure drift if audio is available
	if m.audioSync != nil && m.audioSync.GetSyncState().FrameCount > 0 {
		m.measureDrift()
	}

	return nil
}

// ProcessAudioPacket updates audio synchronization with a new packet
func (m *Manager) ProcessAudioPacket(packet *types.TimestampedPacket) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.audioSync == nil {
		return fmt.Errorf("audio sync not initialized")
	}

	// Update audio timing
	err := m.audioSync.ProcessTimestamp(packet.PTS, packet.DTS, packet.CaptureTime)
	if err != nil {
		return fmt.Errorf("failed to process audio timestamp: %w", err)
	}

	// Calculate presentation time with offset
	basePresentationTime := m.audioSync.GetPresentationTime(packet.PTS)
	packet.PresentationTime = basePresentationTime.Add(m.audioOffset)

	// Update status
	m.status.AudioSync = m.audioSync.GetSyncState()

	// Measure drift if video is available
	if m.videoSync != nil && m.videoSync.GetSyncState().FrameCount > 0 {
		m.measureDrift()
	}

	return nil
}

// measureDrift calculates current A/V drift
func (m *Manager) measureDrift() {
	videoState := m.videoSync.GetSyncState()
	audioState := m.audioSync.GetSyncState()

	// Skip if either track hasn't started
	if videoState.LastPTS == 0 || audioState.LastPTS == 0 {
		m.logger.Debug("Skipping drift measurement - zero PTS",
			"video_pts", videoState.LastPTS,
			"audio_pts", audioState.LastPTS)
		return
	}

	// Method 1: Compare presentation times based on PTS
	videoPresentationTime := m.videoSync.GetPresentationTime(videoState.LastPTS)
	audioPresentationTime := m.audioSync.GetPresentationTime(audioState.LastPTS).Add(m.audioOffset)

	// Method 2: Also consider the actual wall clock times when packets arrived
	// This helps detect network/processing delays
	wallClockDrift := videoState.LastWallTime.Sub(audioState.LastWallTime)

	// The total drift combines both PTS-based drift and wall clock drift
	// This catches both timestamp issues and delivery delays
	ptsDrift := videoPresentationTime.Sub(audioPresentationTime)

	// Calculate total drift considering both factors separately
	// PTS drift is the actual synchronization error we need to correct
	// Wall clock drift indicates processing delays or network jitter

	// Primary sync error is always the PTS drift
	totalDrift := ptsDrift

	// For diagnostic purposes, we track processing lag separately
	// Large processing lag might indicate buffering or network issues
	// but should not directly affect sync correction decisions

	// Add to drift window with detailed components
	sample := DriftSample{
		Timestamp:     time.Now(),
		VideoPTS:      videoState.LastPTS,
		AudioPTS:      audioState.LastPTS,
		Drift:         totalDrift,
		PTSDrift:      ptsDrift,
		ProcessingLag: wallClockDrift,
	}

	m.logger.Debug("Adding drift sample",
		"pts_drift", ptsDrift,
		"wall_clock_drift", wallClockDrift,
		"total_drift", totalDrift)

	m.driftWindow = append(m.driftWindow, sample)
	if len(m.driftWindow) > m.driftWindowSize {
		m.driftWindow = m.driftWindow[1:]
	}

	// Update status
	m.status.CurrentDrift = totalDrift
	m.status.DriftWindow = m.driftWindow
	m.updateDriftStatistics()

	// Check if correction is needed based on PTS drift only
	if m.config.EnableAutoCorrect && time.Since(m.lastCorrection) > m.config.CorrectionInterval {
		// Use PTS drift for correction decisions, not processing lag
		if abs(int64(ptsDrift)) > int64(m.config.MaxAudioDrift) {
			m.applyDriftCorrection()
		}
	}

	// Log drift if enabled with component breakdown
	if m.config.EnableDriftLogging && time.Since(m.lastDriftLog) > m.config.DriftLogInterval {
		m.logger.Debug("A/V drift measurement",
			"stream_id", m.streamID,
			"total_drift_ms", totalDrift.Milliseconds(),
			"pts_drift_ms", ptsDrift.Milliseconds(),
			"processing_lag_ms", wallClockDrift.Milliseconds(),
			"video_pts", videoState.LastPTS,
			"audio_pts", audioState.LastPTS,
			"avg_drift_ms", m.status.AvgDrift.Milliseconds())
		m.lastDriftLog = time.Now()
	}
}

// applyDriftCorrection applies gradual drift correction
func (m *Manager) applyDriftCorrection() {
	if len(m.driftWindow) < 10 {
		return // Need sufficient samples
	}

	avgDrift := m.status.AvgDrift
	if abs(int64(avgDrift)) < int64(m.config.MaxAudioDrift/2) {
		return // Drift within acceptable range
	}

	// Calculate correction amount
	correctionAmount := time.Duration(float64(avgDrift) * m.config.CorrectionFactor)

	// Limit correction step size
	if abs(int64(correctionAmount)) > int64(m.config.MaxCorrectionStep) {
		if correctionAmount > 0 {
			correctionAmount = m.config.MaxCorrectionStep
		} else {
			correctionAmount = -m.config.MaxCorrectionStep
		}
	}

	// Apply correction to audio offset
	oldOffset := m.audioOffset
	m.audioOffset -= correctionAmount

	// Record correction
	correction := DriftCorrection{
		Timestamp: time.Now(),
		Method:    "gradual",
		Amount:    correctionAmount,
		Reason:    fmt.Sprintf("avg_drift=%.2fms", avgDrift.Seconds()*1000),
	}

	m.corrections = append(m.corrections, correction)
	if len(m.corrections) > 50 {
		m.corrections = m.corrections[1:]
	}

	m.lastCorrection = time.Now()
	m.correctionCount++
	m.status.Corrections = m.corrections
	m.status.LastCorrection = m.lastCorrection

	m.logger.Info("Applied drift correction",
		"stream_id", m.streamID,
		"old_offset_ms", oldOffset.Milliseconds(),
		"new_offset_ms", m.audioOffset.Milliseconds(),
		"correction_ms", correctionAmount.Milliseconds(),
		"avg_drift_ms", avgDrift.Milliseconds())
}

// updateDriftStatistics calculates drift statistics
func (m *Manager) updateDriftStatistics() {
	if len(m.driftWindow) == 0 {
		return
	}

	// Calculate average PTS drift (the actual sync error)
	var totalPTSDrift time.Duration
	var totalProcessingLag time.Duration
	for _, sample := range m.driftWindow {
		totalPTSDrift += sample.PTSDrift
		totalProcessingLag += sample.ProcessingLag
	}
	m.status.AvgDrift = totalPTSDrift / time.Duration(len(m.driftWindow))

	// Calculate variance based on PTS drift
	var variance float64
	avgMs := float64(m.status.AvgDrift.Milliseconds())
	for _, sample := range m.driftWindow {
		diffMs := float64(sample.PTSDrift.Milliseconds()) - avgMs
		variance += diffMs * diffMs
	}
	m.status.DriftVariance = variance / float64(len(m.driftWindow))

	// Also track average processing lag for diagnostics
	avgProcessingLag := totalProcessingLag / time.Duration(len(m.driftWindow))
	if abs(int64(avgProcessingLag)) > int64(50*time.Millisecond) {
		m.logger.Warn("High average processing lag detected",
			"stream_id", m.streamID,
			"avg_lag_ms", avgProcessingLag.Milliseconds())
	}

	// Calculate correction rate
	if m.correctionCount > 0 && !m.status.VideoSync.BaseTime.IsZero() {
		duration := time.Since(m.status.VideoSync.BaseTime).Minutes()
		if duration > 0 {
			m.status.CorrectionRate = float64(m.correctionCount) / duration
		}
	}

	// Update sync status based on PTS drift average
	m.status.InSync = abs(int64(m.status.AvgDrift)) <= int64(m.config.MaxAudioDrift)
}

// GetSyncStatus returns the current synchronization status
func (m *Manager) GetSyncStatus() *SyncStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy
	status := *m.status
	if m.videoSync != nil {
		status.VideoSync = m.videoSync.GetSyncState()
	}
	if m.audioSync != nil {
		status.AudioSync = m.audioSync.GetSyncState()
	}

	return &status
}

// GetStatistics returns synchronization statistics
func (m *Manager) GetStatistics() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]interface{}{
		"stream_id":        m.streamID,
		"in_sync":          m.status.InSync,
		"current_drift_ms": m.status.CurrentDrift.Milliseconds(),
		"avg_drift_ms":     m.status.AvgDrift.Milliseconds(),
		"drift_variance":   m.status.DriftVariance,
		"correction_count": m.correctionCount,
		"correction_rate":  m.status.CorrectionRate,
		"audio_offset_ms":  m.audioOffset.Milliseconds(),
	}

	if m.videoSync != nil {
		stats["video"] = m.videoSync.GetStatistics()
	}
	if m.audioSync != nil {
		stats["audio"] = m.audioSync.GetStatistics()
	}

	return stats
}

// ReportVideoDropped reports dropped video frames
func (m *Manager) ReportVideoDropped(count uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.videoSync != nil {
		m.videoSync.ReportDropped(count)
	}
}

// ReportAudioDropped reports dropped audio samples
func (m *Manager) ReportAudioDropped(count uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.audioSync != nil {
		m.audioSync.ReportDropped(count)
	}
}

// Reset resets the synchronization state
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.videoSync != nil {
		m.videoSync.Reset()
	}
	if m.audioSync != nil {
		m.audioSync.Reset()
	}

	m.driftWindow = m.driftWindow[:0]
	m.corrections = m.corrections[:0]
	m.audioOffset = 0
	m.lastCorrection = time.Time{}
	m.correctionCount = 0

	m.status = &SyncStatus{
		InSync:   true,
		MaxDrift: m.config.MaxAudioDrift,
	}
}

// SetAudioOffset manually sets the audio offset
func (m *Manager) SetAudioOffset(offset time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldOffset := m.audioOffset
	m.audioOffset = offset

	// Record manual correction
	correction := DriftCorrection{
		Timestamp: time.Now(),
		Method:    "manual",
		Amount:    offset - oldOffset,
		Reason:    "manual adjustment",
	}

	m.corrections = append(m.corrections, correction)
	m.lastCorrection = time.Now()

	m.logger.Info("Manual audio offset set",
		"stream_id", m.streamID,
		"old_offset_ms", oldOffset.Milliseconds(),
		"new_offset_ms", offset.Milliseconds())
}

