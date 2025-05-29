package types

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// EncoderSessionManager tracks encoder session changes and parameter set evolution
type EncoderSessionManager struct {
	mu       sync.RWMutex
	streamID string

	// Session tracking
	currentSession    *EncoderSession
	sessionHistory    []*EncoderSession
	maxSessionHistory int

	// Parameter set evolution
	parameterCache     *ParameterSetCache
	contextTransitions []ContextTransition

	// Change detection
	lastSPSHash        string
	lastPPSHash        string
	sessionChangeCount uint64

	// Statistics
	totalFrames     uint64
	decodableFrames uint64
	contextSwitches uint64
}

// EncoderSession represents a distinct encoder configuration period
type EncoderSession struct {
	ID            string                   `json:"id"`
	StartTime     time.Time                `json:"start_time"`
	EndTime       *time.Time               `json:"end_time,omitempty"`
	ParameterSets map[string]*ParameterSet `json:"parameter_sets"`
	FrameCount    uint64                   `json:"frame_count"`

	// Session characteristics
	Resolution   *Resolution `json:"resolution,omitempty"`
	ProfileLevel string      `json:"profile_level,omitempty"`
	ConfigHash   string      `json:"config_hash"`

	// Quality metrics
	SuccessfulDecodes uint64 `json:"successful_decodes"`
	FailedDecodes     uint64 `json:"failed_decodes"`
}

// ContextTransition tracks parameter set context changes
type ContextTransition struct {
	Timestamp    time.Time `json:"timestamp"`
	FromSession  string    `json:"from_session"`
	ToSession    string    `json:"to_session"`
	TriggerFrame uint64    `json:"trigger_frame"`
	Reason       string    `json:"reason"`
}

// Resolution represents video resolution
type Resolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// NewEncoderSessionManager creates a new encoder session manager
func NewEncoderSessionManager(streamID string, cacheConfig CacheConfig) *EncoderSessionManager {
	return &EncoderSessionManager{
		streamID:           streamID,
		maxSessionHistory:  cacheConfig.MaxSessions,
		parameterCache:     NewParameterSetCache(cacheConfig.MaxParameterSets, cacheConfig.ParameterSetTTL),
		sessionHistory:     make([]*EncoderSession, 0),
		contextTransitions: make([]ContextTransition, 0),
	}
}

// CacheConfig configures the parameter cache
type CacheConfig struct {
	MaxParameterSets int           `json:"max_parameter_sets"`
	ParameterSetTTL  time.Duration `json:"parameter_set_ttl"`
	MaxSessions      int           `json:"max_sessions"`
}

// ProcessFrame analyzes a frame and updates context tracking
func (esm *EncoderSessionManager) ProcessFrame(frame *VideoFrame, paramSets map[string]*ParameterSet) (*ContextAnalysis, error) {
	esm.mu.Lock()
	defer esm.mu.Unlock()

	esm.totalFrames++

	// Calculate configuration hash for session tracking
	configHash := esm.calculateConfigHash(paramSets)

	// Detect session changes
	sessionChanged := false
	if esm.currentSession == nil || esm.currentSession.ConfigHash != configHash {
		sessionChanged = true
		esm.handleSessionChange(configHash, paramSets, frame.ID)
	}

	// Update current session
	esm.currentSession.FrameCount++

	// Analyze frame decodability
	analysis := &ContextAnalysis{
		StreamID:       esm.streamID,
		FrameID:        frame.ID,
		SessionID:      esm.currentSession.ID,
		SessionChanged: sessionChanged,
		IsDecodable:    false,
		MissingParams:  make([]string, 0),
		MatchQuality:   0.0,
	}

	// Try to find compatible parameter sets
	compatibility := esm.findBestParameterSetMatch(frame)
	analysis.IsDecodable = compatibility.IsDecodable
	analysis.MissingParams = compatibility.MissingParams
	analysis.MatchQuality = compatibility.Quality
	analysis.MatchedSets = compatibility.MatchedSets

	if analysis.IsDecodable {
		esm.decodableFrames++
		esm.currentSession.SuccessfulDecodes++
	} else {
		esm.currentSession.FailedDecodes++
	}

	return analysis, nil
}

// handleSessionChange creates a new encoder session
func (esm *EncoderSessionManager) handleSessionChange(configHash string, paramSets map[string]*ParameterSet, triggerFrameID uint64) {
	// End current session
	if esm.currentSession != nil {
		now := time.Now()
		esm.currentSession.EndTime = &now
	}

	// Create new session
	sessionID := fmt.Sprintf("%s-%d", esm.streamID, time.Now().Unix())
	newSession := &EncoderSession{
		ID:            sessionID,
		StartTime:     time.Now(),
		ParameterSets: make(map[string]*ParameterSet),
		ConfigHash:    configHash,
	}

	// Copy parameter sets to session
	for name, paramSet := range paramSets {
		newSession.ParameterSets[name] = paramSet

		// Add to cache with session-specific key
		cacheKey := fmt.Sprintf("%s:%s:%d", sessionID, name, paramSet.ID)
		esm.parameterCache.Put(cacheKey, paramSet)
	}

	// Record transition
	if esm.currentSession != nil {
		transition := ContextTransition{
			Timestamp:    time.Now(),
			FromSession:  esm.currentSession.ID,
			ToSession:    sessionID,
			TriggerFrame: triggerFrameID,
			Reason:       "parameter_set_change",
		}
		esm.contextTransitions = append(esm.contextTransitions, transition)
	}

	// Update current session
	previousSession := esm.currentSession
	esm.currentSession = newSession

	// Add to history
	if previousSession != nil {
		esm.sessionHistory = append(esm.sessionHistory, previousSession)

		// Trim history if needed
		if len(esm.sessionHistory) > esm.maxSessionHistory {
			esm.sessionHistory = esm.sessionHistory[1:]
		}
	}

	esm.sessionChangeCount++
	esm.contextSwitches++
}

// calculateConfigHash creates a hash of the parameter set configuration
func (esm *EncoderSessionManager) calculateConfigHash(paramSets map[string]*ParameterSet) string {
	hasher := sha256.New()

	// Hash SPS if present
	if sps, exists := paramSets["sps"]; exists && sps != nil {
		hasher.Write([]byte("sps:"))
		hasher.Write([]byte(fmt.Sprintf("%d:", sps.ID)))
		hasher.Write(sps.Data)
	}

	// Hash PPS if present
	if pps, exists := paramSets["pps"]; exists && pps != nil {
		hasher.Write([]byte("pps:"))
		hasher.Write([]byte(fmt.Sprintf("%d:", pps.ID)))
		hasher.Write(pps.Data)
	}

	// Hash VPS if present (HEVC)
	if vps, exists := paramSets["vps"]; exists && vps != nil {
		hasher.Write([]byte("vps:"))
		hasher.Write([]byte(fmt.Sprintf("%d:", vps.ID)))
		hasher.Write(vps.Data)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil))[:16] // First 16 chars for brevity
}

// ContextAnalysis represents the result of frame context analysis
type ContextAnalysis struct {
	StreamID       string                   `json:"stream_id"`
	FrameID        uint64                   `json:"frame_id"`
	SessionID      string                   `json:"session_id"`
	SessionChanged bool                     `json:"session_changed"`
	IsDecodable    bool                     `json:"is_decodable"`
	MissingParams  []string                 `json:"missing_params"`
	MatchQuality   float64                  `json:"match_quality"`
	MatchedSets    map[string]*ParameterSet `json:"-"`
}

// ParameterSetCompatibility represents compatibility analysis
type ParameterSetCompatibility struct {
	IsDecodable   bool                     `json:"is_decodable"`
	MissingParams []string                 `json:"missing_params"`
	Quality       float64                  `json:"quality"`
	MatchedSets   map[string]*ParameterSet `json:"-"`
}

// findBestParameterSetMatch finds the best parameter set match for a frame
func (esm *EncoderSessionManager) findBestParameterSetMatch(frame *VideoFrame) *ParameterSetCompatibility {
	compatibility := &ParameterSetCompatibility{
		IsDecodable:   false,
		MissingParams: make([]string, 0),
		Quality:       0.0,
		MatchedSets:   make(map[string]*ParameterSet),
	}

	// Try current session first
	if esm.currentSession != nil {
		quality := esm.evaluateParameterSetMatch(frame, esm.currentSession.ParameterSets)
		if quality > compatibility.Quality {
			compatibility.Quality = quality
			compatibility.MatchedSets = esm.currentSession.ParameterSets
			if quality >= 1.0 {
				compatibility.IsDecodable = true
			}
		}
	}

	// Try recent sessions if current doesn't work
	if !compatibility.IsDecodable {
		for i := len(esm.sessionHistory) - 1; i >= 0 && i >= len(esm.sessionHistory)-3; i-- {
			session := esm.sessionHistory[i]
			quality := esm.evaluateParameterSetMatch(frame, session.ParameterSets)
			if quality > compatibility.Quality {
				compatibility.Quality = quality
				compatibility.MatchedSets = session.ParameterSets
				if quality >= 1.0 {
					compatibility.IsDecodable = true
					break
				}
			}
		}
	}

	return compatibility
}

// evaluateParameterSetMatch evaluates how well parameter sets match a frame
func (esm *EncoderSessionManager) evaluateParameterSetMatch(frame *VideoFrame, paramSets map[string]*ParameterSet) float64 {
	// Simplified evaluation - in production this would be more sophisticated
	score := 0.0
	maxScore := 2.0 // SPS + PPS

	if _, hasSPS := paramSets["sps"]; hasSPS {
		score += 1.0
	}
	if _, hasPPS := paramSets["pps"]; hasPPS {
		score += 1.0
	}

	return score / maxScore
}

// GetStatistics returns comprehensive context statistics
func (esm *EncoderSessionManager) GetStatistics() map[string]interface{} {
	esm.mu.RLock()
	defer esm.mu.RUnlock()

	decodabilityRate := float64(0)
	if esm.totalFrames > 0 {
		decodabilityRate = float64(esm.decodableFrames) / float64(esm.totalFrames)
	}

	stats := map[string]interface{}{
		"stream_id":         esm.streamID,
		"total_frames":      esm.totalFrames,
		"decodable_frames":  esm.decodableFrames,
		"decodability_rate": decodabilityRate,
		"session_changes":   esm.sessionChangeCount,
		"context_switches":  esm.contextSwitches,
		"active_sessions":   len(esm.sessionHistory) + 1, // +1 for current
		"cache_stats":       esm.parameterCache.GetStatistics(),
	}

	if esm.currentSession != nil {
		stats["current_session"] = map[string]interface{}{
			"id":                 esm.currentSession.ID,
			"start_time":         esm.currentSession.StartTime,
			"frame_count":        esm.currentSession.FrameCount,
			"successful_decodes": esm.currentSession.SuccessfulDecodes,
			"failed_decodes":     esm.currentSession.FailedDecodes,
			"config_hash":        esm.currentSession.ConfigHash,
		}
	}

	return stats
}

// OnParameterSetChange notifies the session manager of parameter set changes
func (esm *EncoderSessionManager) OnParameterSetChange(setType string, setID uint8, data []byte) {
	esm.mu.Lock()
	defer esm.mu.Unlock()

	// Track parameter set change for encoder context analysis
	esm.sessionChangeCount++

	// If we have an active session, check if this represents a significant change
	if esm.currentSession != nil {
	}
}

// Close cleans up resources
func (esm *EncoderSessionManager) Close() {
	esm.mu.Lock()
	defer esm.mu.Unlock()

	if esm.parameterCache != nil {
		esm.parameterCache.Close()
	}
}
