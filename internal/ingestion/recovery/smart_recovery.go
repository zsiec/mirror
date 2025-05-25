package recovery

import (
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/frame"
	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// SmartRecoveryHandler implements intelligent stream recovery strategies
type SmartRecoveryHandler struct {
	*Handler // Embed base handler

	// Enhanced detection
	idrDetector *frame.IDRDetector

	// Recovery state tracking
	recoveryAttempts int
	lastErrorType    ErrorType
	errorHistory     []ErrorEvent

	// Advanced recovery strategies
	adaptiveGOPSize bool
	fastRecovery    bool
	preemptiveMode  bool

	// Performance metrics
	avgRecoveryTime time.Duration
	successRate     float64

	// Frame analysis
	frameBuffer []*types.VideoFrame
	bufferSize  int

	mu sync.RWMutex
}

// ErrorEvent tracks error occurrences for pattern analysis
type ErrorEvent struct {
	Type      ErrorType
	Timestamp time.Time
	FrameNum  uint64
	Recovered bool
	Duration  time.Duration
}

// SmartConfig extends the base recovery config
type SmartConfig struct {
	Config
	EnableAdaptiveGOP  bool
	EnableFastRecovery bool
	EnablePreemptive   bool
	FrameBufferSize    int
}

// NewSmartRecoveryHandler creates an enhanced recovery handler
func NewSmartRecoveryHandler(
	streamID string,
	config SmartConfig,
	gopBuffer *gop.Buffer,
	codec types.CodecType,
	logger logger.Logger,
) *SmartRecoveryHandler {
	// Create base handler
	baseHandler := NewHandler(streamID, config.Config, gopBuffer, logger)

	h := &SmartRecoveryHandler{
		Handler:         baseHandler,
		idrDetector:     frame.NewIDRDetector(codec, logger),
		adaptiveGOPSize: config.EnableAdaptiveGOP,
		fastRecovery:    config.EnableFastRecovery,
		preemptiveMode:  config.EnablePreemptive,
		bufferSize:      config.FrameBufferSize,
		frameBuffer:     make([]*types.VideoFrame, 0, config.FrameBufferSize),
		errorHistory:    make([]ErrorEvent, 0, 100),
	}

	// Override base callbacks with smart versions
	h.SetSmartCallbacks()

	return h
}

// HandleError implements smart error recovery
func (h *SmartRecoveryHandler) HandleError(errorType ErrorType, details interface{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Track error event
	event := ErrorEvent{
		Type:      errorType,
		Timestamp: time.Now(),
		FrameNum:  h.getCurrentFrameNumber(),
	}
	h.errorHistory = append(h.errorHistory, event)
	h.lastErrorType = errorType

	// Analyze error patterns
	strategy := h.analyzeErrorPattern()

	// Apply smart recovery based on strategy
	switch strategy {
	case RecoveryStrategyFast:
		return h.applyFastRecovery(errorType, details)
	case RecoveryStrategyAdaptive:
		return h.applyAdaptiveRecovery(errorType, details)
	case RecoveryStrategyPreemptive:
		return h.applyPreemptiveRecovery(errorType, details)
	default:
		// Fall back to base handler
		h.recoveryAttempts++
		return h.Handler.HandleError(errorType, details)
	}
}

// ProcessFrame analyzes frames for preemptive recovery
func (h *SmartRecoveryHandler) ProcessFrame(frame *types.VideoFrame) {
	if frame == nil {
		return
	}

	h.mu.Lock()

	// Update IDR detector
	if h.idrDetector.IsIDRFrame(frame) {
		h.UpdateKeyframe(frame)
	}

	// Buffer frame for analysis
	h.frameBuffer = append(h.frameBuffer, frame)
	if len(h.frameBuffer) > h.bufferSize {
		h.frameBuffer = h.frameBuffer[1:]
	}

	// Check for corruption indicators
	if frame.IsCorrupted() {
		h.idrDetector.ReportCorruption()
	}

	// Check if preemptive recovery is needed
	needsRecovery := h.preemptiveMode && h.shouldTriggerPreemptiveRecovery(frame)
	h.mu.Unlock()

	// Trigger recovery outside of lock to avoid deadlock
	if needsRecovery {
		h.HandleError(ErrorTypeCorruption, "preemptive_recovery")
	}
}

// analyzeErrorPattern determines the best recovery strategy
func (h *SmartRecoveryHandler) analyzeErrorPattern() RecoveryStrategyType {
	if len(h.errorHistory) < 2 {
		return RecoveryStrategyDefault
	}

	// Check error frequency
	recentErrors := h.getRecentErrors(5 * time.Minute)
	errorRate := float64(len(recentErrors)) / 5.0 // errors per minute

	// High error rate suggests network issues
	if errorRate > 2.0 && h.fastRecovery {
		return RecoveryStrategyFast
	}

	// Repeating pattern suggests systematic issue
	if h.hasRepeatingPattern() && h.adaptiveGOPSize {
		return RecoveryStrategyAdaptive
	}

	// Corruption patterns benefit from preemptive recovery
	corruptionCount := 0
	for _, event := range recentErrors {
		if event.Type == ErrorTypeCorruption {
			corruptionCount++
		}
	}
	if corruptionCount > 2 && h.preemptiveMode {
		return RecoveryStrategyPreemptive
	}

	return RecoveryStrategyDefault
}

// applyFastRecovery implements rapid recovery for high error rates
func (h *SmartRecoveryHandler) applyFastRecovery(errorType ErrorType, details interface{}) error {
	h.logger.Info("Applying fast recovery strategy")
	h.recoveryAttempts++

	// Find nearest recovery point
	recoveryPoints := h.idrDetector.FindRecoveryPoints(h.frameBuffer)
	if len(recoveryPoints) > 0 {
		// Use the most recent high-confidence recovery point
		bestPoint := h.selectBestRecoveryPoint(recoveryPoints)

		h.logger.WithFields(map[string]interface{}{
			"frame_number": bestPoint.FrameNumber,
			"confidence":   bestPoint.Confidence,
			"is_idr":       bestPoint.IsIDR,
		}).Info("Fast recovery from buffered recovery point")

		// Signal recovery from this point
		h.setState(StateNormal)
		return nil
	}

	// No local recovery point, request keyframe immediately
	return h.requestKeyframeImmediate("fast_recovery")
}

// applyAdaptiveRecovery adjusts GOP expectations based on stream behavior
func (h *SmartRecoveryHandler) applyAdaptiveRecovery(errorType ErrorType, details interface{}) error {
	h.logger.Info("Applying adaptive recovery strategy")
	h.recoveryAttempts++

	// Get current GOP statistics
	gopInterval := h.idrDetector.GetKeyframeInterval()
	nextKeyframe := h.idrDetector.PredictNextKeyframe(h.getCurrentFrameNumber())

	// Calculate wait time
	framesUntilKeyframe := nextKeyframe - h.getCurrentFrameNumber()
	waitTime := time.Duration(float64(framesUntilKeyframe)*33.33) * time.Millisecond // Assume 30fps

	// If keyframe is coming soon, wait for it
	if waitTime < 500*time.Millisecond {
		h.logger.WithField("wait_time", waitTime).Info("Waiting for natural keyframe")
		go h.waitForKeyframeWithTimeout(waitTime + 100*time.Millisecond)
		return nil
	}

	// Otherwise, request keyframe but adjust future expectations
	h.logger.WithFields(map[string]interface{}{
		"current_gop_size": gopInterval,
		"frames_until_kf":  framesUntilKeyframe,
	}).Info("Requesting keyframe with adaptive timing")

	return h.requestKeyframe("adaptive_recovery")
}

// applyPreemptiveRecovery prevents errors before they cascade
func (h *SmartRecoveryHandler) applyPreemptiveRecovery(errorType ErrorType, details interface{}) error {
	h.logger.Info("Applying preemptive recovery strategy")
	h.recoveryAttempts++

	// Check if we're seeing early warning signs
	strategy := h.idrDetector.GetRecoveryStrategy()

	switch strategy {
	case frame.RecoveryStrategyRequestKeyframe:
		// Proactively request keyframe before corruption spreads
		h.logger.Info("Preemptively requesting keyframe")
		return h.requestKeyframe("preemptive")

	case frame.RecoveryStrategyFindRecoveryPoint:
		// Look for upcoming I-frames that could serve as recovery points
		upcomingRecovery := h.findUpcomingRecoveryPoint()
		if upcomingRecovery != nil {
			h.logger.WithField("frame_number", upcomingRecovery.FrameNumber).
				Info("Preparing for recovery at upcoming I-frame")
			// Set up monitoring for this frame
			go h.monitorRecoveryPoint(upcomingRecovery)
		}

	default:
		// Monitor situation closely
		h.increaseMonitoring()
	}

	return nil
}

// shouldTriggerPreemptiveRecovery looks for early warning signs
func (h *SmartRecoveryHandler) shouldTriggerPreemptiveRecovery(frame *types.VideoFrame) bool {
	// Check for quality degradation indicators
	qualityScore := h.assessFrameQuality(frame)

	if qualityScore < 0.5 {
		h.logger.WithField("quality_score", qualityScore).
			Warn("Frame quality degradation detected")

		// Check if we need keyframe
		if h.idrDetector.NeedsKeyframe(frame.FrameNumber, false) {
			return true
		}
	}

	// Check for reference frame issues
	if len(frame.References) > 0 {
		missingRefs := h.checkMissingReferences(frame)
		if missingRefs > 0 {
			h.logger.WithField("missing_refs", missingRefs).
				Warn("Missing reference frames detected")
			h.idrDetector.ReportCorruption()
			// Don't trigger recovery yet, just report corruption
		}
	}

	return false
}

// selectBestRecoveryPoint chooses the optimal recovery point
func (h *SmartRecoveryHandler) selectBestRecoveryPoint(points []frame.RecoveryPoint) frame.RecoveryPoint {
	if len(points) == 0 {
		return frame.RecoveryPoint{}
	}

	// Prefer IDR frames
	for _, point := range points {
		if point.IsIDR {
			return point
		}
	}

	// Otherwise, choose highest confidence
	best := points[0]
	for _, point := range points[1:] {
		if point.Confidence > best.Confidence {
			best = point
		}
	}

	return best
}

// assessFrameQuality estimates frame quality based on various metrics
func (h *SmartRecoveryHandler) assessFrameQuality(frame *types.VideoFrame) float64 {
	score := 1.0

	// Check for corruption flag
	if frame.IsCorrupted() {
		score *= 0.1
	}

	// Check size anomalies
	expectedSize := h.getExpectedFrameSize(frame.Type)
	if expectedSize > 0 {
		sizeRatio := float64(frame.TotalSize) / float64(expectedSize)
		if sizeRatio < 0.5 || sizeRatio > 2.0 {
			score *= 0.7
		}
	}

	// Check timing anomalies
	if frame.PTS < frame.DTS {
		score *= 0.5
	}

	// Check QP if available
	if frame.QP > 40 { // High QP indicates low quality
		score *= 0.8
	}

	return score
}

// Performance tracking methods

func (h *SmartRecoveryHandler) UpdateRecoveryMetrics(duration time.Duration, success bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Update average recovery time
	if h.avgRecoveryTime == 0 {
		h.avgRecoveryTime = duration
	} else {
		h.avgRecoveryTime = (h.avgRecoveryTime + duration) / 2
	}

	// Update success rate
	totalAttempts := float64(h.recoveryCount.Load())
	if success {
		h.successRate = (h.successRate*(totalAttempts-1) + 1) / totalAttempts
	} else {
		h.successRate = (h.successRate * (totalAttempts - 1)) / totalAttempts
	}

	// Update error history
	if len(h.errorHistory) > 0 {
		h.errorHistory[len(h.errorHistory)-1].Recovered = success
		h.errorHistory[len(h.errorHistory)-1].Duration = duration
	}
}

// GetSmartStatistics returns enhanced recovery statistics
func (h *SmartRecoveryHandler) GetSmartStatistics() SmartStatistics {
	h.mu.RLock()
	defer h.mu.RUnlock()

	baseStats := h.GetStatistics()

	return SmartStatistics{
		Statistics:       baseStats,
		RecoveryAttempts: h.recoveryAttempts,
		AvgRecoveryTime:  h.avgRecoveryTime,
		SuccessRate:      h.successRate,
		ErrorPatterns:    h.analyzeErrorPatterns(),
		GOPInterval:      h.idrDetector.GetKeyframeInterval(),
		Strategy:         h.getCurrentStrategy(),
	}
}

// Helper methods

func (h *SmartRecoveryHandler) getRecentErrors(duration time.Duration) []ErrorEvent {
	cutoff := time.Now().Add(-duration)
	recent := make([]ErrorEvent, 0)

	for _, event := range h.errorHistory {
		if event.Timestamp.After(cutoff) {
			recent = append(recent, event)
		}
	}

	return recent
}

func (h *SmartRecoveryHandler) hasRepeatingPattern() bool {
	if len(h.errorHistory) < 4 {
		return false
	}

	// Look for patterns in error types
	recent := h.errorHistory[len(h.errorHistory)-4:]

	// Check if errors occur at regular intervals
	intervals := make([]time.Duration, 0)
	for i := 1; i < len(recent); i++ {
		intervals = append(intervals, recent[i].Timestamp.Sub(recent[i-1].Timestamp))
	}

	// Check if intervals are similar (within 20%)
	if len(intervals) >= 2 {
		avgInterval := (intervals[0] + intervals[1]) / 2
		for _, interval := range intervals {
			ratio := float64(interval) / float64(avgInterval)
			if ratio < 0.8 || ratio > 1.2 {
				return false
			}
		}
		return true
	}

	return false
}

func (h *SmartRecoveryHandler) getCurrentFrameNumber() uint64 {
	if len(h.frameBuffer) > 0 {
		return h.frameBuffer[len(h.frameBuffer)-1].FrameNumber
	}
	return 0
}

func (h *SmartRecoveryHandler) requestKeyframeImmediate(reason string) error {
	h.logger.WithField("reason", reason).Info("Immediate keyframe request")

	// Set urgent flag for immediate handling
	h.setState(StateResyncing)

	if h.onForceKeyframe != nil {
		h.onForceKeyframe()
	}

	// Don't wait, return immediately
	return nil
}

func (h *SmartRecoveryHandler) waitForKeyframeWithTimeout(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	startTime := time.Now()

	select {
	case <-timer.C:
		// Timeout - escalate
		h.logger.Warn("Keyframe wait timeout, escalating")
		h.HandleError(ErrorTypeTimeout, "keyframe_timeout")

	case <-time.After(50 * time.Millisecond):
		// Check periodically
		for {
			if h.GetState() == StateNormal {
				duration := time.Since(startTime)
				h.UpdateRecoveryMetrics(duration, true)
				return
			}

			select {
			case <-timer.C:
				h.UpdateRecoveryMetrics(time.Since(startTime), false)
				return
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}
	}
}

func (h *SmartRecoveryHandler) findUpcomingRecoveryPoint() *frame.RecoveryPoint {
	// Look ahead in GOP structure
	currentFrame := h.getCurrentFrameNumber()
	predictedKeyframe := h.idrDetector.PredictNextKeyframe(currentFrame)

	// If keyframe is coming soon, prepare for it
	if predictedKeyframe-currentFrame < 10 {
		return &frame.RecoveryPoint{
			FrameNumber: predictedKeyframe,
			IsIDR:       true,
			Confidence:  0.9,
		}
	}

	return nil
}

func (h *SmartRecoveryHandler) monitorRecoveryPoint(point *frame.RecoveryPoint) {
	// Monitor for the specific recovery frame
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case <-timeout.C:
			h.logger.Warn("Recovery point monitoring timeout")
			return

		case <-ticker.C:
			current := h.getCurrentFrameNumber()
			if current >= point.FrameNumber {
				h.logger.Info("Reached recovery point")
				h.setState(StateNormal)
				return
			}
		}
	}
}

func (h *SmartRecoveryHandler) increaseMonitoring() {
	// Increase monitoring frequency
	h.logger.Debug("Increasing error monitoring")
	// Implementation would increase checking frequency
}

func (h *SmartRecoveryHandler) checkMissingReferences(frame *types.VideoFrame) int {
	missing := 0
	for _, ref := range frame.References {
		found := false
		for _, buffered := range h.frameBuffer {
			if buffered.FrameNumber == ref {
				found = true
				break
			}
		}
		if !found {
			missing++
		}
	}
	return missing
}

func (h *SmartRecoveryHandler) getExpectedFrameSize(frameType types.FrameType) int {
	// Estimate based on recent frames of same type
	sizes := make([]int, 0)

	for _, frame := range h.frameBuffer {
		if frame.Type == frameType {
			sizes = append(sizes, frame.TotalSize)
		}
	}

	if len(sizes) == 0 {
		return 0
	}

	// Return average
	sum := 0
	for _, size := range sizes {
		sum += size
	}
	return sum / len(sizes)
}

func (h *SmartRecoveryHandler) analyzeErrorPatterns() map[string]interface{} {
	patterns := make(map[string]interface{})

	if len(h.errorHistory) == 0 {
		return patterns
	}

	// Count error types
	typeCounts := make(map[ErrorType]int)
	for _, event := range h.errorHistory {
		typeCounts[event.Type]++
	}

	// Find most common error
	var mostCommon ErrorType
	maxCount := 0
	for errType, count := range typeCounts {
		if count > maxCount {
			mostCommon = errType
			maxCount = count
		}
	}

	patterns["most_common_error"] = mostCommon
	patterns["error_count"] = len(h.errorHistory)
	patterns["recovery_rate"] = h.successRate

	return patterns
}

func (h *SmartRecoveryHandler) getCurrentStrategy() string {
	if h.fastRecovery && h.lastErrorType == ErrorTypePacketLoss {
		return "fast"
	}
	if h.adaptiveGOPSize {
		return "adaptive"
	}
	if h.preemptiveMode {
		return "preemptive"
	}
	return "default"
}

// SetSmartCallbacks configures enhanced callbacks
func (h *SmartRecoveryHandler) SetSmartCallbacks() {
	h.SetCallbacks(
		func(errorType ErrorType) {
			h.logger.WithField("error_type", errorType).Info("Smart recovery started")
		},
		func(duration time.Duration, success bool) {
			h.UpdateRecoveryMetrics(duration, success)
			h.logger.WithFields(map[string]interface{}{
				"duration": duration,
				"success":  success,
			}).Info("Smart recovery completed")
		},
		func() {
			h.logger.Info("Keyframe requested by smart recovery")
		},
	)
}

// Types

type RecoveryStrategyType int

const (
	RecoveryStrategyDefault RecoveryStrategyType = iota
	RecoveryStrategyFast
	RecoveryStrategyAdaptive
	RecoveryStrategyPreemptive
)

type SmartStatistics struct {
	Statistics
	RecoveryAttempts int
	AvgRecoveryTime  time.Duration
	SuccessRate      float64
	ErrorPatterns    map[string]interface{}
	GOPInterval      float64
	Strategy         string
}
