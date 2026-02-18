package types

import (
	"sync"
	"time"
)

// ParameterSetInjector handles injection of parameter sets for streams
// that don't provide them inline
type ParameterSetInjector struct {
	mu               sync.RWMutex
	streamID         string
	codec            CodecType
	defaultSets      *DefaultParameterSets
	injected         bool
	injectionTime    time.Time
	framesSinceCheck uint64
}

// NewParameterSetInjector creates a new parameter set injector
func NewParameterSetInjector(streamID string, codec CodecType) *ParameterSetInjector {
	return &ParameterSetInjector{
		streamID:    streamID,
		codec:       codec,
		defaultSets: NewDefaultParameterSets(),
	}
}

// CheckAndInject checks if parameter sets need to be injected
// Returns true if injection was performed
func (p *ParameterSetInjector) CheckAndInject(ctx *ParameterSetContext, framesProcessed uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only inject once
	if p.injected {
		return false
	}

	// Only check every 100 frames to avoid performance impact
	if framesProcessed-p.framesSinceCheck < 100 {
		return false
	}
	p.framesSinceCheck = framesProcessed

	// Check if we have any parameter sets
	stats := ctx.GetStatistics()
	spsCount, _ := stats["sps_count"].(int)
	ppsCount, _ := stats["pps_count"].(int)

	// If we have parameter sets, no need to inject
	if spsCount > 0 && ppsCount > 0 {
		return false
	}

	// If we've processed more than 100 frames without parameter sets,
	// inject defaults
	if framesProcessed > 100 {
		return p.injectDefaults(ctx)
	}

	return false
}

// injectDefaults injects default parameter sets into the context
func (p *ParameterSetInjector) injectDefaults(ctx *ParameterSetContext) bool {
	// Only inject for H.264 for now
	if p.codec != CodecH264 {
		return false
	}

	// Generate minimal parameter sets
	sps, pps := p.defaultSets.GenerateMinimalParameterSets()

	// Add SPS
	if err := ctx.AddSPS(sps); err != nil {
		return false
	}

	// Add PPS
	if err := ctx.AddPPS(pps); err != nil {
		return false
	}

	p.injected = true
	p.injectionTime = time.Now()

	return true
}

// IsInjected returns whether default parameter sets have been injected
func (p *ParameterSetInjector) IsInjected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.injected
}

// GetInjectionInfo returns information about the injection
func (p *ParameterSetInjector) GetInjectionInfo() (bool, time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.injected, p.injectionTime
}
