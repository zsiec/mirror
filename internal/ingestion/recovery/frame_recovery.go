package recovery

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// FrameRecoveryStrategy defines how to recover frames
type FrameRecoveryStrategy int

const (
	FrameRecoveryNone FrameRecoveryStrategy = iota
	FrameRecoveryInterpolation
	FrameRecoveryDuplication
	FrameRecoveryGOPBased
	FrameRecoveryMLPrediction
)

// FrameRecovery handles partial frame recovery
type FrameRecovery struct {
	mu       sync.RWMutex
	streamID string
	logger   logger.Logger

	// Configuration
	strategy            FrameRecoveryStrategy
	maxRecoveryAttempts int
	recoveryWindow      time.Duration
	gopBufferSize       int

	// State
	gopBuffer       *gop.Buffer
	frameHistory    []FrameInfo
	historySize     int
	recoveryMetrics RecoveryMetrics

	// Metrics
	recoveredFrames *metrics.Counter
	droppedFrames   *metrics.Counter
	recoveryLatency *metrics.Histogram
	gopHealth       *metrics.Gauge
}

// FrameInfo contains frame metadata for recovery
type FrameInfo struct {
	FrameNumber uint64
	PTS         int64
	DTS         int64
	Type        types.FrameType
	IsIDR       bool
	Size        int
	Timestamp   time.Time
}

// RecoveryMetrics tracks recovery performance
type RecoveryMetrics struct {
	TotalRecovered      uint64
	TotalDropped        uint64
	SuccessRate         float64
	AverageRecoveryTime time.Duration
	LastRecovery        time.Time
}

// NewFrameRecovery creates a new frame recovery handler
func NewFrameRecovery(streamID string, logger logger.Logger) *FrameRecovery {
	fr := &FrameRecovery{
		streamID:            streamID,
		logger:              logger,
		strategy:            FrameRecoveryGOPBased,
		maxRecoveryAttempts: 3,
		recoveryWindow:      100 * time.Millisecond,
		gopBufferSize:       3,
		frameHistory:        make([]FrameInfo, 0, 100),
		historySize:         100,
	}

	// Initialize GOP buffer for recovery
	config := gop.BufferConfig{
		MaxGOPs:     fr.gopBufferSize,
		MaxBytes:    100 * 1024 * 1024, // 100MB
		MaxDuration: 30 * time.Second,
		Codec:       types.CodecH264, // Default to H.264
	}
	fr.gopBuffer = gop.NewBuffer(streamID, config, logger)

	// Initialize metrics
	fr.recoveredFrames = metrics.NewCounter("ingestion_frames_recovered",
		map[string]string{"stream_id": streamID})
	fr.droppedFrames = metrics.NewCounter("ingestion_frames_dropped",
		map[string]string{"stream_id": streamID})
	fr.recoveryLatency = metrics.NewHistogram("ingestion_frame_recovery_latency_seconds",
		map[string]string{"stream_id": streamID}, []float64{0.001, 0.005, 0.01, 0.05, 0.1})
	fr.gopHealth = metrics.NewGauge("ingestion_gop_health",
		map[string]string{"stream_id": streamID})

	return fr
}

// SetStrategy sets the recovery strategy
func (fr *FrameRecovery) SetStrategy(strategy FrameRecoveryStrategy) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.strategy = strategy
}

// RecordFrame records frame info for potential recovery
func (fr *FrameRecovery) RecordFrame(frame *types.VideoFrame) {
	if frame == nil {
		return
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()

	// Add to history
	info := FrameInfo{
		FrameNumber: frame.FrameNumber,
		PTS:         frame.PTS,
		DTS:         frame.DTS,
		Type:        frame.Type,
		IsIDR:       frame.IsKeyframe(),
		Size:        frame.TotalSize,
		Timestamp:   time.Now(),
	}

	fr.frameHistory = append(fr.frameHistory, info)

	// Trim history
	if len(fr.frameHistory) > fr.historySize {
		fr.frameHistory = fr.frameHistory[len(fr.frameHistory)-fr.historySize:]
	}
}

// RecordGOP records a GOP for recovery purposes
func (fr *FrameRecovery) RecordGOP(gop *types.GOP) error {
	if gop == nil {
		return fmt.Errorf("nil GOP")
	}

	// Add to GOP buffer
	fr.gopBuffer.AddGOP(gop)

	// Update GOP health metric
	health := fr.calculateGOPHealth(gop)
	fr.gopHealth.Set(health)

	return nil
}

// RecoverFrame attempts to recover a missing frame
func (fr *FrameRecovery) RecoverFrame(frameNumber uint64, frameType types.FrameType) (*types.VideoFrame, error) {
	start := time.Now()

	fr.mu.Lock()
	strategy := fr.strategy
	fr.mu.Unlock()

	var recovered *types.VideoFrame
	var err error

	switch strategy {
	case FrameRecoveryNone:
		err = fmt.Errorf("recovery disabled")

	case FrameRecoveryInterpolation:
		recovered, err = fr.interpolateFrame(frameNumber, frameType)

	case FrameRecoveryDuplication:
		recovered, err = fr.duplicateFrame(frameNumber, frameType)

	case FrameRecoveryGOPBased:
		recovered, err = fr.recoverFromGOP(frameNumber, frameType)

	case FrameRecoveryMLPrediction:
		// Future: ML-based prediction
		err = fmt.Errorf("ML prediction not implemented")

	default:
		err = fmt.Errorf("unknown recovery strategy: %v", strategy)
	}

	if err != nil {
		fr.droppedFrames.Inc()
		fr.updateMetrics(false, 0)
		return nil, err
	}

	fr.recoveredFrames.Inc()
	fr.recoveryLatency.Observe(time.Since(start).Seconds())
	fr.updateMetrics(true, time.Since(start))

	fr.logger.WithFields(logger.Fields{
		"frame_number": frameNumber,
		"strategy":     strategy.String(),
		"latency_ms":   time.Since(start).Milliseconds(),
	}).Info("Frame recovered")

	return recovered, nil
}

// RecoverPartialGOP attempts to recover a partial GOP
func (fr *FrameRecovery) RecoverPartialGOP(partialGOP *types.GOP) (*types.GOP, error) {
	if partialGOP == nil {
		return nil, fmt.Errorf("nil GOP")
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()

	// Check if we can recover from buffered GOPs
	bufferedGOPs := fr.gopBuffer.GetRecentGOPs(10)
	if len(bufferedGOPs) == 0 {
		return nil, fmt.Errorf("no buffered GOPs available")
	}

	// Find the best matching GOP
	var bestMatch *types.GOP
	var bestScore float64

	for _, gop := range bufferedGOPs {
		score := fr.calculateGOPSimilarity(partialGOP, gop)
		if score > bestScore {
			bestScore = score
			bestMatch = gop
		}
	}

	if bestMatch == nil || bestScore < 0.5 {
		return nil, fmt.Errorf("no suitable GOP found for recovery")
	}

	// Merge partial GOP with best match
	recovered := fr.mergeGOPs(partialGOP, bestMatch)

	fr.logger.WithFields(logger.Fields{
		"partial_frames":   len(partialGOP.Frames),
		"recovered_frames": len(recovered.Frames),
		"similarity_score": bestScore,
	}).Info("Partial GOP recovered")

	return recovered, nil
}

// GetMetrics returns recovery metrics
func (fr *FrameRecovery) GetMetrics() RecoveryMetrics {
	fr.mu.RLock()
	defer fr.mu.RUnlock()
	return fr.recoveryMetrics
}

// Private methods

func (fr *FrameRecovery) interpolateFrame(frameNumber uint64, frameType types.FrameType) (*types.VideoFrame, error) {
	// Find surrounding frames
	prev, next := fr.findSurroundingFrames(frameNumber)
	if prev == nil || next == nil {
		return nil, fmt.Errorf("cannot find surrounding frames for interpolation")
	}

	// Simple interpolation - duplicate previous frame with adjusted timestamp
	interpolated := &types.VideoFrame{
		FrameNumber: frameNumber,
		Type:        frameType,
		PTS:         (prev.PTS + next.PTS) / 2,
		DTS:         (prev.DTS + next.DTS) / 2,
		TotalSize:   prev.Size, // Placeholder
		CodecData: map[string]interface{}{
			"recovered": true,
			"method":    "interpolation",
		},
	}

	return interpolated, nil
}

func (fr *FrameRecovery) duplicateFrame(frameNumber uint64, frameType types.FrameType) (*types.VideoFrame, error) {
	// Find previous frame of same type
	prev := fr.findPreviousFrame(frameNumber, frameType)
	if prev == nil {
		return nil, fmt.Errorf("no previous frame found for duplication")
	}

	// Duplicate with adjusted number
	duplicated := &types.VideoFrame{
		FrameNumber: frameNumber,
		Type:        frameType,
		PTS:         prev.PTS + 1000, // Adjust timestamp
		DTS:         prev.DTS + 1000,
		TotalSize:   prev.Size, // Placeholder
		CodecData: map[string]interface{}{
			"recovered": true,
			"method":    "duplication",
		},
	}

	return duplicated, nil
}

func (fr *FrameRecovery) recoverFromGOP(frameNumber uint64, frameType types.FrameType) (*types.VideoFrame, error) {
	// Get current GOPs
	gops := fr.gopBuffer.GetRecentGOPs(10) // Get last 10 GOPs
	if len(gops) == 0 {
		return nil, fmt.Errorf("no GOPs available")
	}

	// Find GOP containing this frame number
	for _, gop := range gops {
		for _, frame := range gop.Frames {
			if frame.FrameNumber == frameNumber {
				// Frame exists in GOP
				return frame, nil
			}
		}
	}

	// Try to recover from previous GOP
	lastGOP := gops[len(gops)-1]
	if len(lastGOP.Frames) > 0 {
		// Use last frame as template
		template := lastGOP.Frames[len(lastGOP.Frames)-1]
		recovered := &types.VideoFrame{
			FrameNumber: frameNumber,
			Type:        frameType,
			PTS:         template.PTS + 1000,
			DTS:         template.DTS + 1000,
			NALUnits:    template.NALUnits, // Copy NAL units
			TotalSize:   template.TotalSize,
			CodecData: map[string]interface{}{
				"recovered": true,
				"method":    "gop_based",
			},
		}
		return recovered, nil
	}

	return nil, fmt.Errorf("cannot recover from GOP")
}

func (fr *FrameRecovery) findSurroundingFrames(frameNumber uint64) (*FrameInfo, *FrameInfo) {
	var prev, next *FrameInfo

	for i, info := range fr.frameHistory {
		if info.FrameNumber < frameNumber {
			prev = &fr.frameHistory[i]
		} else if info.FrameNumber > frameNumber && next == nil {
			next = &fr.frameHistory[i]
			break
		}
	}

	return prev, next
}

func (fr *FrameRecovery) findPreviousFrame(frameNumber uint64, frameType types.FrameType) *FrameInfo {
	for i := len(fr.frameHistory) - 1; i >= 0; i-- {
		info := &fr.frameHistory[i]
		if info.FrameNumber < frameNumber && info.Type == frameType {
			return info
		}
	}
	return nil
}

func (fr *FrameRecovery) calculateGOPHealth(gop *types.GOP) float64 {
	if gop == nil || len(gop.Frames) == 0 {
		return 0.0
	}

	health := 1.0

	// Check for IDR frame
	if !gop.Frames[0].IsKeyframe() {
		health -= 0.3
	}

	// Check frame continuity
	var gaps int
	for i := 1; i < len(gop.Frames); i++ {
		if gop.Frames[i].FrameNumber != gop.Frames[i-1].FrameNumber+1 {
			gaps++
		}
	}

	if gaps > 0 {
		health -= float64(gaps) * 0.1
	}

	// Ensure health is between 0 and 1
	if health < 0 {
		health = 0
	}

	return health
}

func (fr *FrameRecovery) calculateGOPSimilarity(gop1, gop2 *types.GOP) float64 {
	if gop1 == nil || gop2 == nil {
		return 0.0
	}

	// Simple similarity based on frame count and timing
	countSimilarity := 1.0 - math.Abs(float64(len(gop1.Frames)-len(gop2.Frames)))/float64(max(len(gop1.Frames), len(gop2.Frames)))

	// Check timing similarity
	var timingSimilarity float64
	if len(gop1.Frames) > 0 && len(gop2.Frames) > 0 {
		duration1 := gop1.Frames[len(gop1.Frames)-1].PTS - gop1.Frames[0].PTS
		duration2 := gop2.Frames[len(gop2.Frames)-1].PTS - gop2.Frames[0].PTS

		if duration1 > 0 && duration2 > 0 {
			maxDuration := duration1
			if duration2 > maxDuration {
				maxDuration = duration2
			}
			timingSimilarity = 1.0 - math.Abs(float64(duration1-duration2))/float64(maxDuration)
		}
	}

	return (countSimilarity + timingSimilarity) / 2
}

func (fr *FrameRecovery) mergeGOPs(partial, template *types.GOP) *types.GOP {
	merged := &types.GOP{
		ID:        partial.ID,
		StreamID:  partial.StreamID,
		StartTime: partial.StartTime,
		StartPTS:  partial.StartPTS,
		Keyframe:  partial.Keyframe,
		Frames:    make([]*types.VideoFrame, 0, len(template.Frames)),
	}

	// Copy existing frames from partial
	frameMap := make(map[uint64]*types.VideoFrame)
	for _, frame := range partial.Frames {
		frameMap[frame.FrameNumber] = frame
		merged.Frames = append(merged.Frames, frame)
	}

	// Fill missing frames from template
	for _, frame := range template.Frames {
		if _, exists := frameMap[frame.FrameNumber]; !exists {
			// Adjust frame number if needed
			recoveredFrame := *frame
			recoveredFrame.CodecData = map[string]interface{}{
				"recovered": true,
				"source":    "template_gop",
			}
			merged.Frames = append(merged.Frames, &recoveredFrame)
		}
	}

	return merged
}

func (fr *FrameRecovery) updateMetrics(success bool, duration time.Duration) {
	fr.mu.Lock()
	defer fr.mu.Unlock()

	if success {
		fr.recoveryMetrics.TotalRecovered++
	} else {
		fr.recoveryMetrics.TotalDropped++
	}

	total := fr.recoveryMetrics.TotalRecovered + fr.recoveryMetrics.TotalDropped
	if total > 0 {
		fr.recoveryMetrics.SuccessRate = float64(fr.recoveryMetrics.TotalRecovered) / float64(total)
	}

	if success && duration > 0 {
		// Update average recovery time
		if fr.recoveryMetrics.AverageRecoveryTime == 0 {
			fr.recoveryMetrics.AverageRecoveryTime = duration
		} else {
			// Exponential moving average
			fr.recoveryMetrics.AverageRecoveryTime = time.Duration(
				0.9*float64(fr.recoveryMetrics.AverageRecoveryTime) + 0.1*float64(duration))
		}
		fr.recoveryMetrics.LastRecovery = time.Now()
	}
}

// String returns string representation of FrameRecoveryStrategy
func (s FrameRecoveryStrategy) String() string {
	switch s {
	case FrameRecoveryNone:
		return "none"
	case FrameRecoveryInterpolation:
		return "interpolation"
	case FrameRecoveryDuplication:
		return "duplication"
	case FrameRecoveryGOPBased:
		return "gop_based"
	case FrameRecoveryMLPrediction:
		return "ml_prediction"
	default:
		return "unknown"
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
