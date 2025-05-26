package types

import (
	"fmt"
	"sync"
	"time"
)

// ParameterSetProber extracts missing parameter sets from recent frames
type ParameterSetProber struct {
	mu       sync.RWMutex
	streamID string

	// Frame history for probing
	frameHistory    []*VideoFrame
	maxFrameHistory int
	historyDuration time.Duration

	// Probing statistics
	probeAttempts  uint64
	probeSuccesses uint64
	extractedSPS   uint64
	extractedPPS   uint64
	extractedVPS   uint64

	// Recent extractions cache
	recentExtractions map[string]*ParameterSet
	cacheExpiry       time.Duration
}

// ProbeRequest represents a parameter set probe request
type ProbeRequest struct {
	MissingParameters []string    `json:"missing_parameters"`
	TargetFrame       *VideoFrame `json:"target_frame"`
	RequiredSPSID     *uint8      `json:"required_sps_id,omitempty"`
	RequiredPPSID     *uint8      `json:"required_pps_id,omitempty"`
	RequiredVPSID     *uint8      `json:"required_vps_id,omitempty"`
	MaxProbeFrames    int         `json:"max_probe_frames"`
}

// ProbeResult contains the results of parameter set probing
type ProbeResult struct {
	Success          bool                     `json:"success"`
	ExtractedSets    map[string]*ParameterSet `json:"extracted_sets"`
	FramesProbed     int                      `json:"frames_probed"`
	ExtractionSource map[string]uint64        `json:"extraction_source"` // param_name -> frame_id
	ProbeLatency     time.Duration            `json:"probe_latency"`
	FailureReason    string                   `json:"failure_reason,omitempty"`
}

// NewParameterSetProber creates a new parameter set prober
func NewParameterSetProber(streamID string, maxFrameHistory int, historyDuration time.Duration) *ParameterSetProber {
	return &ParameterSetProber{
		streamID:          streamID,
		maxFrameHistory:   maxFrameHistory,
		historyDuration:   historyDuration,
		frameHistory:      make([]*VideoFrame, 0, maxFrameHistory),
		recentExtractions: make(map[string]*ParameterSet),
		cacheExpiry:       5 * time.Minute,
	}
}

// AddFrame adds a frame to the probing history
func (p *ParameterSetProber) AddFrame(frame *VideoFrame) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Add to history
	p.frameHistory = append(p.frameHistory, frame)

	// Trim by count
	if len(p.frameHistory) > p.maxFrameHistory {
		p.frameHistory = p.frameHistory[1:]
	}

	// Trim by time
	cutoff := time.Now().Add(-p.historyDuration)
	for i, f := range p.frameHistory {
		if f.CaptureTime.After(cutoff) {
			if i > 0 {
				p.frameHistory = p.frameHistory[i:]
			}
			break
		}
	}

	// Opportunistically extract parameter sets
	p.opportunisticExtraction(frame)
}

// opportunisticExtraction extracts parameter sets when they appear
func (p *ParameterSetProber) opportunisticExtraction(frame *VideoFrame) {
	for _, nalUnit := range frame.NALUnits {
		if len(nalUnit.Data) == 0 {
			continue
		}

		nalType := nalUnit.Type
		if nalType == 0 && len(nalUnit.Data) > 0 {
			nalType = nalUnit.Data[0] & 0x1F
		}

		switch nalType {
		case 7: // H.264 SPS
			spsData := make([]byte, len(nalUnit.Data)+1)
			spsData[0] = 0x67
			copy(spsData[1:], nalUnit.Data)

			paramSet := &ParameterSet{
				Data:     spsData,
				ParsedAt: time.Now(),
				Size:     len(spsData),
				Valid:    true,
			}

			key := fmt.Sprintf("sps_%d", nalType)
			p.recentExtractions[key] = paramSet
			p.extractedSPS++

		case 8: // H.264 PPS
			ppsData := make([]byte, len(nalUnit.Data)+1)
			ppsData[0] = 0x68
			copy(ppsData[1:], nalUnit.Data)

			paramSet := &ParameterSet{
				Data:     ppsData,
				ParsedAt: time.Now(),
				Size:     len(ppsData),
				Valid:    true,
			}

			key := fmt.Sprintf("pps_%d", nalType)
			p.recentExtractions[key] = paramSet
			p.extractedPPS++

		case 32: // HEVC VPS
			paramSet := &ParameterSet{
				Data:     nalUnit.Data,
				ParsedAt: time.Now(),
				Size:     len(nalUnit.Data),
				Valid:    true,
			}

			key := fmt.Sprintf("vps_%d", nalType)
			p.recentExtractions[key] = paramSet
			p.extractedVPS++
		}
	}
}

// ProbeForParameterSets attempts to find missing parameter sets
func (p *ParameterSetProber) ProbeForParameterSets(request *ProbeRequest) *ProbeResult {
	startTime := time.Now()
	p.mu.RLock()
	defer p.mu.RUnlock()

	p.probeAttempts++

	result := &ProbeResult{
		ExtractedSets:    make(map[string]*ParameterSet),
		ExtractionSource: make(map[string]uint64),
		ProbeLatency:     0,
	}

	// First check recent extractions cache
	found := p.checkRecentExtractions(request, result)
	if found {
		result.Success = true
		result.ProbeLatency = time.Since(startTime)
		p.probeSuccesses++
		return result
	}

	// Probe frame history
	maxFrames := request.MaxProbeFrames
	if maxFrames == 0 || maxFrames > len(p.frameHistory) {
		maxFrames = len(p.frameHistory)
	}

	// Start from most recent frames
	for i := len(p.frameHistory) - 1; i >= 0 && result.FramesProbed < maxFrames; i-- {
		frame := p.frameHistory[i]
		result.FramesProbed++

		// Extract parameter sets from this frame
		extracted := p.extractParameterSetsFromFrame(frame, request)

		// Add to result
		for name, paramSet := range extracted {
			if _, exists := result.ExtractedSets[name]; !exists {
				result.ExtractedSets[name] = paramSet
				result.ExtractionSource[name] = frame.ID
			}
		}

		// Check if we have everything we need
		if p.hasAllRequiredParameters(request, result.ExtractedSets) {
			result.Success = true
			break
		}
	}

	if !result.Success {
		missing := p.findMissingParameters(request, result.ExtractedSets)
		result.FailureReason = fmt.Sprintf("could not find parameter sets: %v", missing)
	} else {
		p.probeSuccesses++
	}

	result.ProbeLatency = time.Since(startTime)
	return result
}

// checkRecentExtractions checks if we have the required sets in recent cache
func (p *ParameterSetProber) checkRecentExtractions(request *ProbeRequest, result *ProbeResult) bool {
	now := time.Now()
	foundAll := true

	for _, paramName := range request.MissingParameters {
		found := false

		// Try different possible keys
		possibleKeys := []string{
			paramName,
			fmt.Sprintf("%s_7", paramName),  // H.264 SPS
			fmt.Sprintf("%s_8", paramName),  // H.264 PPS
			fmt.Sprintf("%s_32", paramName), // HEVC VPS
			fmt.Sprintf("%s_33", paramName), // HEVC SPS
			fmt.Sprintf("%s_34", paramName), // HEVC PPS
		}

		for _, key := range possibleKeys {
			if paramSet, exists := p.recentExtractions[key]; exists {
				// Check if still valid
				if now.Sub(paramSet.ParsedAt) < p.cacheExpiry {
					result.ExtractedSets[paramName] = paramSet
					result.ExtractionSource[paramName] = 0 // Cache source
					found = true
					break
				}
			}
		}

		if !found {
			foundAll = false
		}
	}

	return foundAll
}

// extractParameterSetsFromFrame extracts parameter sets from a specific frame
func (p *ParameterSetProber) extractParameterSetsFromFrame(frame *VideoFrame, request *ProbeRequest) map[string]*ParameterSet {
	extracted := make(map[string]*ParameterSet)

	for _, nalUnit := range frame.NALUnits {
		if len(nalUnit.Data) == 0 {
			continue
		}

		nalType := nalUnit.Type
		if nalType == 0 && len(nalUnit.Data) > 0 {
			nalType = nalUnit.Data[0] & 0x1F
		}

		// Check if this is a parameter set we need
		paramName := ""
		switch nalType {
		case 7: // H.264 SPS
			if p.isInList("sps", request.MissingParameters) {
				paramName = "sps"

				// Check if specific SPS ID is required
				if request.RequiredSPSID != nil {
					// Parse SPS ID (simplified)
					if p.extractSPSID(nalUnit.Data) == *request.RequiredSPSID {
						spsData := make([]byte, len(nalUnit.Data)+1)
						spsData[0] = 0x67
						copy(spsData[1:], nalUnit.Data)

						extracted[paramName] = &ParameterSet{
							ID:       *request.RequiredSPSID,
							Data:     spsData,
							ParsedAt: time.Now(),
							Size:     len(spsData),
							Valid:    true,
						}
					}
				} else {
					// Take any SPS
					spsData := make([]byte, len(nalUnit.Data)+1)
					spsData[0] = 0x67
					copy(spsData[1:], nalUnit.Data)

					extracted[paramName] = &ParameterSet{
						Data:     spsData,
						ParsedAt: time.Now(),
						Size:     len(spsData),
						Valid:    true,
					}
				}
			}

		case 8: // H.264 PPS
			if p.isInList("pps", request.MissingParameters) {
				paramName = "pps"

				ppsData := make([]byte, len(nalUnit.Data)+1)
				ppsData[0] = 0x68
				copy(ppsData[1:], nalUnit.Data)

				extracted[paramName] = &ParameterSet{
					Data:     ppsData,
					ParsedAt: time.Now(),
					Size:     len(ppsData),
					Valid:    true,
				}
			}

		case 32: // HEVC VPS
			if p.isInList("vps", request.MissingParameters) {
				paramName = "vps"

				extracted[paramName] = &ParameterSet{
					Data:     nalUnit.Data,
					ParsedAt: time.Now(),
					Size:     len(nalUnit.Data),
					Valid:    true,
				}
			}
		}
	}

	return extracted
}

// Helper functions
func (p *ParameterSetProber) isInList(item string, list []string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func (p *ParameterSetProber) hasAllRequiredParameters(request *ProbeRequest, extracted map[string]*ParameterSet) bool {
	for _, param := range request.MissingParameters {
		if _, exists := extracted[param]; !exists {
			return false
		}
	}
	return true
}

func (p *ParameterSetProber) findMissingParameters(request *ProbeRequest, extracted map[string]*ParameterSet) []string {
	missing := make([]string, 0)
	for _, param := range request.MissingParameters {
		if _, exists := extracted[param]; !exists {
			missing = append(missing, param)
		}
	}
	return missing
}

// extractSPSID extracts SPS ID from H.264 SPS data (simplified)
func (p *ParameterSetProber) extractSPSID(data []byte) uint8 {
	// Simplified SPS ID extraction
	// In production this would use proper bitstream parsing
	if len(data) < 4 {
		return 0
	}

	// Skip profile_idc, constraint flags, level_idc
	// SPS ID is typically 0 for most streams
	return 0
}

// GetStatistics returns probing statistics
func (p *ParameterSetProber) GetStatistics() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	successRate := float64(0)
	if p.probeAttempts > 0 {
		successRate = float64(p.probeSuccesses) / float64(p.probeAttempts)
	}

	return map[string]interface{}{
		"stream_id":          p.streamID,
		"frame_history_size": len(p.frameHistory),
		"max_frame_history":  p.maxFrameHistory,
		"probe_attempts":     p.probeAttempts,
		"probe_successes":    p.probeSuccesses,
		"success_rate":       successRate,
		"extracted_sps":      p.extractedSPS,
		"extracted_pps":      p.extractedPPS,
		"extracted_vps":      p.extractedVPS,
		"cached_extractions": len(p.recentExtractions),
	}
}
