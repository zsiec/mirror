package recovery

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// ErrorType represents different types of streaming errors
type ErrorType int

const (
	ErrorTypePacketLoss ErrorType = iota
	ErrorTypeCorruption
	ErrorTypeTimeout
	ErrorTypeSequenceGap
	ErrorTypeTimestampJump
	ErrorTypeCodecError
)

// RecoveryState represents the current recovery state
type RecoveryState int

const (
	StateNormal RecoveryState = iota
	StateRecovering
	StateResyncing
	StateFailed
)

// Handler manages error recovery for video streams
type Handler struct {
	streamID      string
	state         atomic.Value // RecoveryState
	gopBuffer     *gop.Buffer
	lastKeyframe  *types.VideoFrame
	
	// Recovery statistics
	recoveryCount    atomic.Uint64
	corruptionCount  atomic.Uint64
	resyncCount      atomic.Uint64
	lastRecoveryTime atomic.Value // time.Time
	
	// Recovery configuration
	maxRecoveryTime   time.Duration
	keyframeTimeout   time.Duration
	corruptionWindow  int // Number of frames to check for corruption spread
	
	// Callbacks
	onRecoveryStart  func(errorType ErrorType)
	onRecoveryEnd    func(duration time.Duration, success bool)
	onForceKeyframe  func() // Request keyframe from source
	
	mu               sync.RWMutex
	logger           logger.Logger
}

// Config configures the recovery handler
type Config struct {
	MaxRecoveryTime  time.Duration
	KeyframeTimeout  time.Duration
	CorruptionWindow int
}

// NewHandler creates a new recovery handler
func NewHandler(streamID string, config Config, gopBuffer *gop.Buffer, logger logger.Logger) *Handler {
	h := &Handler{
		streamID:         streamID,
		gopBuffer:        gopBuffer,
		maxRecoveryTime:  config.MaxRecoveryTime,
		keyframeTimeout:  config.KeyframeTimeout,
		corruptionWindow: config.CorruptionWindow,
		logger:           logger.WithField("component", "recovery_handler"),
	}
	
	h.state.Store(StateNormal)
	h.lastRecoveryTime.Store(time.Time{})
	
	return h
}

// HandleError processes a streaming error and initiates recovery if needed
func (h *Handler) HandleError(errorType ErrorType, details interface{}) error {
	currentState := h.GetState()
	
	// If already recovering, check if we should escalate
	if currentState == StateRecovering {
		return h.escalateRecovery(errorType)
	}
	
	// Start recovery based on error type
	switch errorType {
	case ErrorTypePacketLoss:
		return h.recoverFromPacketLoss(details)
	case ErrorTypeCorruption:
		return h.recoverFromCorruption(details)
	case ErrorTypeTimeout:
		return h.recoverFromTimeout()
	case ErrorTypeSequenceGap:
		return h.recoverFromSequenceGap(details)
	case ErrorTypeTimestampJump:
		return h.recoverFromTimestampJump(details)
	case ErrorTypeCodecError:
		return h.recoverFromCodecError(details)
	default:
		h.logger.WithField("error_type", errorType).Warn("Unknown error type")
		return nil
	}
}

// recoverFromPacketLoss handles packet loss recovery
func (h *Handler) recoverFromPacketLoss(details interface{}) error {
	h.setState(StateRecovering)
	h.recoveryCount.Add(1)
	startTime := time.Now()
	
	if h.onRecoveryStart != nil {
		h.onRecoveryStart(ErrorTypePacketLoss)
	}
	
	h.logger.WithField("details", details).Info("Starting packet loss recovery")
	
	// Check if we can recover from GOP buffer
	recentGOPs := h.gopBuffer.GetRecentGOPs(3)
	if len(recentGOPs) > 0 {
		// Find the last complete GOP
		for i := len(recentGOPs) - 1; i >= 0; i-- {
			gop := recentGOPs[i]
			if gop.Closed {
				h.logger.WithField("gop_id", gop.ID).Info("Recovering from GOP buffer")
				h.setState(StateNormal)
				
				if h.onRecoveryEnd != nil {
					h.onRecoveryEnd(time.Since(startTime), true)
				}
				return nil
			}
		}
	}
	
	// If no complete GOP, request keyframe
	return h.requestKeyframe("packet_loss")
}

// recoverFromCorruption handles corruption recovery
func (h *Handler) recoverFromCorruption(details interface{}) error {
	h.setState(StateRecovering)
	h.corruptionCount.Add(1)
	startTime := time.Now()
	
	if h.onRecoveryStart != nil {
		h.onRecoveryStart(ErrorTypeCorruption)
	}
	
	h.logger.WithField("details", details).Warn("Corruption detected, initiating recovery")
	
	// Drop corrupted frames and all dependent frames
	droppedFrames := h.dropCorruptedFrames()
	h.logger.WithField("dropped_frames", len(droppedFrames)).Info("Dropped corrupted frames")
	
	// Find next keyframe in buffer
	keyframe := h.findNextKeyframe()
	if keyframe != nil {
		h.logger.WithField("frame_id", keyframe.ID).Info("Resuming from buffered keyframe")
		h.setState(StateNormal)
		
		if h.onRecoveryEnd != nil {
			h.onRecoveryEnd(time.Since(startTime), true)
		}
		return nil
	}
	
	// No keyframe available, request new one
	return h.requestKeyframe("corruption")
}

// recoverFromTimeout handles timeout recovery
func (h *Handler) recoverFromTimeout() error {
	h.setState(StateResyncing)
	h.resyncCount.Add(1)
	
	h.logger.Warn("Stream timeout detected, attempting resync")
	
	// Check time since last keyframe
	h.mu.RLock()
	lastKF := h.lastKeyframe
	h.mu.RUnlock()
	
	if lastKF != nil && time.Since(lastKF.CaptureTime) > h.keyframeTimeout {
		return h.requestKeyframe("timeout")
	}
	
	// Wait for natural keyframe
	go h.waitForKeyframe()
	return nil
}

// recoverFromSequenceGap handles sequence number gaps
func (h *Handler) recoverFromSequenceGap(details interface{}) error {
	gap, ok := details.(int)
	if !ok {
		return nil
	}
	
	h.logger.WithField("gap_size", gap).Warn("Sequence gap detected")
	
	// Small gaps might be reordering
	if gap < 5 {
		// Wait briefly for reordered packets
		time.Sleep(10 * time.Millisecond)
		return nil
	}
	
	// Large gap indicates packet loss
	return h.recoverFromPacketLoss(gap)
}

// recoverFromTimestampJump handles timestamp discontinuities
func (h *Handler) recoverFromTimestampJump(details interface{}) error {
	jump, ok := details.(time.Duration)
	if !ok {
		return nil
	}
	
	h.logger.WithField("jump_ms", jump.Milliseconds()).Warn("Timestamp jump detected")
	
	// Resync timestamps from next keyframe
	h.setState(StateResyncing)
	return h.requestKeyframe("timestamp_jump")
}

// recoverFromCodecError handles codec-specific errors
func (h *Handler) recoverFromCodecError(details interface{}) error {
	h.logger.WithField("details", details).Error("Codec error detected")
	
	// Codec errors usually require full resync
	h.setState(StateResyncing)
	return h.requestKeyframe("codec_error")
}

// dropCorruptedFrames removes corrupted frames and their dependents
func (h *Handler) dropCorruptedFrames() []*types.VideoFrame {
	// Get all recent GOPs to check for corruption
	recentGOPs := h.gopBuffer.GetRecentGOPs(10)
	if len(recentGOPs) == 0 {
		return []*types.VideoFrame{}
	}
	
	var allDroppedFrames []*types.VideoFrame
	
	// Check all GOPs for corrupted frames
	for _, gop := range recentGOPs {
		for i, frame := range gop.Frames {
			if frame.IsCorrupted() {
				// Use the buffer's method to properly drop frames
				droppedFrames := h.gopBuffer.DropFramesFromGOP(gop.ID, i)
				if droppedFrames != nil {
					allDroppedFrames = append(allDroppedFrames, droppedFrames...)
					
					h.logger.WithFields(map[string]interface{}{
						"gop_id":        gop.ID,
						"start_index":   i,
						"frames_dropped": len(droppedFrames),
					}).Info("Dropped corrupted frames from GOP")
				}
				break
			}
		}
	}
	
	return allDroppedFrames
}

// findNextKeyframe searches the buffer for the next keyframe
func (h *Handler) findNextKeyframe() *types.VideoFrame {
	recentGOPs := h.gopBuffer.GetRecentGOPs(5)
	
	for _, gop := range recentGOPs {
		if gop.Keyframe != nil && !gop.Keyframe.IsCorrupted() {
			return gop.Keyframe
		}
	}
	
	return nil
}

// requestKeyframe requests a new keyframe from the source
func (h *Handler) requestKeyframe(reason string) error {
	h.logger.WithField("reason", reason).Info("Requesting keyframe from source")
	
	if h.onForceKeyframe != nil {
		h.onForceKeyframe()
	}
	
	// Set timeout for keyframe arrival
	go h.waitForKeyframe()
	
	return nil
}

// waitForKeyframe waits for a keyframe with timeout
func (h *Handler) waitForKeyframe() {
	timer := time.NewTimer(h.keyframeTimeout)
	defer timer.Stop()
	
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	startTime := time.Now()
	
	for {
		select {
		case <-timer.C:
			h.logger.Error("Keyframe timeout exceeded")
			h.setState(StateFailed)
			if h.onRecoveryEnd != nil {
				h.onRecoveryEnd(time.Since(startTime), false)
			}
			return
			
		case <-ticker.C:
			// Check if we received a keyframe
			h.mu.RLock()
			lastKF := h.lastKeyframe
			h.mu.RUnlock()
			
			if lastKF != nil && lastKF.CaptureTime.After(startTime) {
				h.logger.Info("Keyframe received, recovery complete")
				h.setState(StateNormal)
				if h.onRecoveryEnd != nil {
					h.onRecoveryEnd(time.Since(startTime), true)
				}
				return
			}
		}
	}
}

// escalateRecovery handles recovery escalation when initial recovery fails
func (h *Handler) escalateRecovery(errorType ErrorType) error {
	lastRecovery := h.lastRecoveryTime.Load().(time.Time)
	if time.Since(lastRecovery) > h.maxRecoveryTime {
		h.logger.Error("Recovery timeout exceeded, marking stream as failed")
		h.setState(StateFailed)
		return nil
	}
	
	h.logger.WithField("error_type", errorType).Warn("Escalating recovery")
	
	// Force immediate keyframe request
	return h.requestKeyframe("escalation")
}

// UpdateKeyframe updates the last known good keyframe
func (h *Handler) UpdateKeyframe(frame *types.VideoFrame) {
	if frame == nil || !frame.IsKeyframe() {
		return
	}
	
	h.mu.Lock()
	h.lastKeyframe = frame
	h.mu.Unlock()
	
	// If we were waiting for a keyframe, check state
	if h.GetState() == StateResyncing {
		h.logger.Info("Keyframe received during resync")
	}
}

// GetState returns the current recovery state
func (h *Handler) GetState() RecoveryState {
	return h.state.Load().(RecoveryState)
}

// setState updates the recovery state
func (h *Handler) setState(state RecoveryState) {
	oldState := h.GetState()
	h.state.Store(state)
	
	if oldState != state {
		h.logger.WithFields(map[string]interface{}{
			"old_state": oldState,
			"new_state": state,
		}).Info("Recovery state changed")
		
		if state == StateRecovering || state == StateResyncing {
			h.lastRecoveryTime.Store(time.Now())
		}
	}
}

// GetStatistics returns recovery statistics
func (h *Handler) GetStatistics() Statistics {
	lastRecovery := h.lastRecoveryTime.Load().(time.Time)
	
	return Statistics{
		State:            h.GetState(),
		RecoveryCount:    h.recoveryCount.Load(),
		CorruptionCount:  h.corruptionCount.Load(),
		ResyncCount:      h.resyncCount.Load(),
		LastRecoveryTime: lastRecovery,
		IsHealthy:        h.GetState() == StateNormal,
	}
}

// SetCallbacks sets the recovery callbacks
func (h *Handler) SetCallbacks(onStart func(ErrorType), onEnd func(time.Duration, bool), onKeyframe func()) {
	h.onRecoveryStart = onStart
	h.onRecoveryEnd = onEnd
	h.onForceKeyframe = onKeyframe
}

// Statistics contains recovery statistics
type Statistics struct {
	State            RecoveryState
	RecoveryCount    uint64
	CorruptionCount  uint64
	ResyncCount      uint64
	LastRecoveryTime time.Time
	IsHealthy        bool
}
