package types

import (
	"sync"
	"time"
)

// StreamWithoutParametersHandler handles streams that don't provide inline parameter sets
type StreamWithoutParametersHandler struct {
	mu                 sync.RWMutex
	streamID           string
	codec              CodecType
	framesProcessed    uint64
	lastCheck          time.Time
	warnedAboutMissing bool
	checkInterval      time.Duration
	frameThreshold     uint64
}

// NewStreamWithoutParametersHandler creates a new handler for streams without inline parameter sets
func NewStreamWithoutParametersHandler(streamID string, codec CodecType) *StreamWithoutParametersHandler {
	return &StreamWithoutParametersHandler{
		streamID:       streamID,
		codec:          codec,
		checkInterval:  5 * time.Second,
		frameThreshold: 100, // Warn after 100 frames without parameter sets
	}
}

// CheckAndWarn checks if we should warn about missing parameter sets
func (h *StreamWithoutParametersHandler) CheckAndWarn(ctx *ParameterSetContext, framesProcessed uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.framesProcessed = framesProcessed

	// Only check periodically to avoid performance impact
	if time.Since(h.lastCheck) < h.checkInterval {
		return
	}
	h.lastCheck = time.Now()

	// Check if we have parameter sets
	stats := ctx.GetStatistics()
	spsCount, _ := stats["sps_count"].(int)
	ppsCount, _ := stats["pps_count"].(int)

	// If we have parameter sets, no need to warn
	if spsCount > 0 || ppsCount > 0 {
		h.warnedAboutMissing = false
		return
	}

	// If we've processed enough frames without parameter sets, warn once
	if framesProcessed > h.frameThreshold && !h.warnedAboutMissing {
		// Log warning about missing parameter sets
		// Note: This handler doesn't have access to a logger, so the calling code
		// should handle logging based on the status returned by this handler

		h.warnedAboutMissing = true
	}
}

// ShouldSkipParameterValidation returns whether to skip parameter validation for this stream
func (h *StreamWithoutParametersHandler) ShouldSkipParameterValidation() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// After threshold, we know this stream doesn't have inline parameters
	return h.framesProcessed > h.frameThreshold && h.warnedAboutMissing
}

// GetStatus returns the current status
func (h *StreamWithoutParametersHandler) GetStatus() (framesProcessed uint64, hasWarned bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.framesProcessed, h.warnedAboutMissing
}
