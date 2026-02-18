package integrity

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// ValidationResult represents the result of a stream validation
type ValidationResult struct {
	StreamID      string
	Timestamp     time.Time
	IsValid       bool
	HealthScore   float64
	Issues        []ValidationIssue
	FrameCount    uint64
	DroppedFrames uint64
	Latency       time.Duration
}

// ValidationIssue represents a specific validation problem
type ValidationIssue struct {
	Type        string
	Severity    Severity
	Description string
	Timestamp   time.Time
	FrameNumber uint64
}

// Severity levels for validation issues
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityError
	SeverityCritical
)

// StreamValidator validates stream integrity end-to-end
type StreamValidator struct {
	mu           sync.RWMutex
	streamID     string
	logger       logger.Logger
	checksummer  *Checksummer
	healthScorer *HealthScorer

	// Validation state
	frameCount         atomic.Uint64
	droppedFrames      atomic.Uint64
	lastValidation     time.Time
	validationInterval time.Duration

	// Metrics
	validationCounter  *metrics.Counter
	healthScoreGauge   *metrics.Gauge
	issueCounter       *metrics.Counter
	validationDuration *metrics.Histogram

	// Callbacks
	onValidationComplete func(result ValidationResult)
	onCorruptionDetected func(issue ValidationIssue)

	// Configuration
	enableChecksums  bool
	checksumInterval int // Check every N frames
	maxLatency       time.Duration
	minHealthScore   float64
}

// NewStreamValidator creates a new stream validator
func NewStreamValidator(streamID string, logger logger.Logger) *StreamValidator {
	sv := &StreamValidator{
		streamID:           streamID,
		logger:             logger,
		checksummer:        NewChecksummer(),
		healthScorer:       NewHealthScorer(),
		validationInterval: 5 * time.Second,
		enableChecksums:    true,
		checksumInterval:   100,
		maxLatency:         500 * time.Millisecond,
		minHealthScore:     0.8,
		lastValidation:     time.Now(),
	}

	// Initialize metrics
	sv.validationCounter = metrics.NewCounter("ingestion_validation_total",
		map[string]string{"stream_id": streamID})
	sv.healthScoreGauge = metrics.NewGauge("ingestion_health_score",
		map[string]string{"stream_id": streamID})
	sv.issueCounter = metrics.NewCounter("ingestion_validation_issues",
		map[string]string{"stream_id": streamID})
	sv.validationDuration = metrics.NewHistogram("ingestion_validation_duration_seconds",
		map[string]string{"stream_id": streamID}, []float64{0.001, 0.005, 0.01, 0.05, 0.1})

	return sv
}

// ValidateFrame validates a single frame
func (sv *StreamValidator) ValidateFrame(frame *types.VideoFrame) error {
	if frame == nil {
		return fmt.Errorf("nil frame")
	}

	frameNum := sv.frameCount.Add(1)

	// Timestamp validation
	if err := sv.validateTimestamp(frame); err != nil {
		sv.recordIssue(ValidationIssue{
			Type:        "timestamp_error",
			Severity:    SeverityError,
			Description: err.Error(),
			Timestamp:   time.Now(),
			FrameNumber: frameNum,
		})
		return err
	}

	// Size validation
	if err := sv.validateFrameSize(frame); err != nil {
		sv.recordIssue(ValidationIssue{
			Type:        "size_error",
			Severity:    SeverityWarning,
			Description: err.Error(),
			Timestamp:   time.Now(),
			FrameNumber: frameNum,
		})
	}

	// Checksum validation (periodic)
	if sv.enableChecksums && frameNum%uint64(sv.checksumInterval) == 0 {
		if err := sv.validateChecksum(frame); err != nil {
			sv.recordIssue(ValidationIssue{
				Type:        "checksum_error",
				Severity:    SeverityCritical,
				Description: err.Error(),
				Timestamp:   time.Now(),
				FrameNumber: frameNum,
			})
			return err
		}
	}

	// Trigger periodic validation
	if time.Since(sv.lastValidation) > sv.validationInterval {
		go sv.performFullValidation()
	}

	return nil
}

// ValidateGOP validates a Group of Pictures
func (sv *StreamValidator) ValidateGOP(gop *types.GOP) error {
	if gop == nil {
		return fmt.Errorf("nil GOP")
	}

	// Validate GOP structure
	if len(gop.Frames) == 0 {
		return fmt.Errorf("empty GOP")
	}

	// Validate first frame is IDR
	if !gop.Frames[0].IsKeyframe() {
		sv.recordIssue(ValidationIssue{
			Type:        "gop_structure",
			Severity:    SeverityError,
			Description: "GOP does not start with IDR frame",
			Timestamp:   time.Now(),
			FrameNumber: sv.frameCount.Load(),
		})
		return fmt.Errorf("GOP must start with IDR frame")
	}

	// Validate frame ordering
	if err := sv.validateFrameOrdering(gop); err != nil {
		return err
	}

	// Validate parameter sets
	if err := sv.validateParameterSets(gop); err != nil {
		return err
	}

	return nil
}

// GetValidationResult returns the current validation state
func (sv *StreamValidator) GetValidationResult() ValidationResult {
	sv.mu.RLock()
	defer sv.mu.RUnlock()

	health := sv.healthScorer.CalculateScore(sv.getHealthMetrics())

	return ValidationResult{
		StreamID:      sv.streamID,
		Timestamp:     time.Now(),
		IsValid:       health >= sv.minHealthScore,
		HealthScore:   health,
		FrameCount:    sv.frameCount.Load(),
		DroppedFrames: sv.droppedFrames.Load(),
		Latency:       sv.calculateLatency(),
	}
}

// SetCallbacks sets validation event callbacks
func (sv *StreamValidator) SetCallbacks(
	onComplete func(ValidationResult),
	onCorruption func(ValidationIssue)) {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	sv.onValidationComplete = onComplete
	sv.onCorruptionDetected = onCorruption
}

// Close cleans up validator resources
func (sv *StreamValidator) Close() error {
	// Any cleanup needed
	return nil
}

// Private methods

func (sv *StreamValidator) validateTimestamp(frame *types.VideoFrame) error {
	if frame.PTS == 0 && frame.DTS == 0 {
		return fmt.Errorf("invalid timestamps: PTS=%d, DTS=%d", frame.PTS, frame.DTS)
	}

	if frame.DTS > frame.PTS {
		return fmt.Errorf("DTS > PTS: DTS=%d, PTS=%d", frame.DTS, frame.PTS)
	}

	return nil
}

func (sv *StreamValidator) validateFrameSize(frame *types.VideoFrame) error {
	if frame.TotalSize == 0 {
		return fmt.Errorf("empty frame data")
	}

	const maxFrameSize = 50 * 1024 * 1024 // 50MB
	if frame.TotalSize > maxFrameSize {
		return fmt.Errorf("frame size %d exceeds maximum %d", frame.TotalSize, maxFrameSize)
	}

	return nil
}

func (sv *StreamValidator) validateChecksum(frame *types.VideoFrame) error {
	// Calculate checksum from NAL units
	var data []byte
	for _, nal := range frame.NALUnits {
		data = append(data, nal.Data...)
	}
	expected := sv.checksummer.Calculate(data)

	// For now, we store checksum in frame metadata
	if codecData, ok := frame.CodecData.(map[string]interface{}); ok {
		if stored, ok := codecData["checksum"].(uint32); ok {
			if stored != expected {
				return fmt.Errorf("checksum mismatch: expected=%x, got=%x", expected, stored)
			}
		} else {
			// Store checksum for future validation
			codecData["checksum"] = expected
		}
	} else {
		// Initialize CodecData as map
		frame.CodecData = map[string]interface{}{
			"checksum": expected,
		}
	}

	return nil
}

func (sv *StreamValidator) validateFrameOrdering(gop *types.GOP) error {
	var lastPTS int64 = -1

	for i, frame := range gop.Frames {
		if frame.PTS <= lastPTS {
			return fmt.Errorf("non-monotonic PTS at frame %d: %d <= %d", i, frame.PTS, lastPTS)
		}
		lastPTS = frame.PTS
	}

	return nil
}

func (sv *StreamValidator) validateParameterSets(gop *types.GOP) error {
	// Check that all frames can be decoded with available parameter sets
	for _, frame := range gop.Frames {
		// For H.264/HEVC frames, check if parameter sets are available
		if frame.Type == types.FrameTypeI || frame.Type == types.FrameTypeP || frame.Type == types.FrameTypeB {
			// This is a simplified check - real implementation would verify codec-specific requirements
			// For now, just ensure the GOP has a keyframe which typically includes parameter sets
			if gop.Keyframe == nil {
				return fmt.Errorf("GOP missing keyframe with parameter sets")
			}
		}
	}

	return nil
}

func (sv *StreamValidator) performFullValidation() {
	start := time.Now()
	sv.mu.Lock()
	sv.lastValidation = time.Now()
	sv.mu.Unlock()

	result := sv.GetValidationResult()

	// Update metrics
	sv.validationCounter.Inc()
	sv.healthScoreGauge.Set(result.HealthScore)
	sv.validationDuration.Observe(time.Since(start).Seconds())

	// Notify callbacks
	if sv.onValidationComplete != nil {
		sv.onValidationComplete(result)
	}

	sv.logger.WithFields(logger.Fields{
		"stream_id":    result.StreamID,
		"health_score": result.HealthScore,
		"is_valid":     result.IsValid,
		"frame_count":  result.FrameCount,
		"dropped":      result.DroppedFrames,
	}).Info("Stream validation completed")
}

func (sv *StreamValidator) recordIssue(issue ValidationIssue) {
	sv.issueCounter.Inc()

	if sv.onCorruptionDetected != nil {
		sv.onCorruptionDetected(issue)
	}

	sv.logger.WithFields(logger.Fields{
		"type":         issue.Type,
		"severity":     issue.Severity,
		"description":  issue.Description,
		"frame_number": issue.FrameNumber,
	}).Warn("Validation issue detected")
}

func (sv *StreamValidator) getHealthMetrics() HealthMetrics {
	frameCount := sv.frameCount.Load()
	droppedFrames := sv.droppedFrames.Load()

	var dropRate float64
	if frameCount > 0 {
		dropRate = float64(droppedFrames) / float64(frameCount)
	}

	return HealthMetrics{
		FrameDropRate:     dropRate,
		TimestampDrift:    0, // TODO: Calculate actual drift
		BufferUtilization: 0, // TODO: Get from buffer manager
		ErrorRate:         0, // TODO: Calculate from issues
	}
}

func (sv *StreamValidator) calculateLatency() time.Duration {
	// TODO: Implement actual latency calculation
	return 100 * time.Millisecond
}

// RecordDroppedFrame records a dropped frame
func (sv *StreamValidator) RecordDroppedFrame() {
	sv.droppedFrames.Add(1)
	sv.recordIssue(ValidationIssue{
		Type:        "frame_drop",
		Severity:    SeverityWarning,
		Description: "Frame dropped",
		Timestamp:   time.Now(),
		FrameNumber: sv.frameCount.Load(),
	})
}
