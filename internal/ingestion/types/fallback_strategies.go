package types

import (
	"fmt"
	"sync"
	"time"
)

// FallbackStrategy defines different approaches for handling missing parameter sets
type FallbackStrategy int

const (
	FallbackNone FallbackStrategy = iota
	FallbackApproximate
	FallbackCompatible
	FallbackGeneric
)

// FallbackParameterManager implements fallback strategies
// for handling cases where exact parameter sets are missing
type FallbackParameterManager struct {
	mu       sync.RWMutex
	codec    CodecType
	streamID string

	// Fallback parameter set templates
	genericSPS *ParameterSet
	genericPPS *ParameterSet
	genericVPS *ParameterSet

	// Compatibility matrix for parameter set matching
	compatibilityRules map[string]CompatibilityRule

	// Statistics
	fallbacksUsed   uint64
	approximateHits uint64
	compatibleHits  uint64
	genericHits     uint64
	totalRequests   uint64

	lastUpdate time.Time
}

// CompatibilityRule defines how to match parameter sets for compatibility
type CompatibilityRule struct {
	ProfileCompatible   []uint8 `json:"profile_compatible"`   // Compatible profile IDs
	LevelCompatible     []uint8 `json:"level_compatible"`     // Compatible level IDs
	ResolutionTolerance float64 `json:"resolution_tolerance"` // % tolerance for resolution differences
	BitrateCompatible   bool    `json:"bitrate_compatible"`   // Whether bitrate differences are acceptable
	QualityScore        float64 `json:"quality_score"`        // Expected quality (0-1)
}

// FallbackResult contains the result of a fallback parameter set search
type FallbackResult struct {
	Found             bool                     `json:"found"`
	Strategy          FallbackStrategy         `json:"strategy"`
	ParameterSets     map[string]*ParameterSet `json:"parameter_sets"`
	QualityScore      float64                  `json:"quality_score"`
	CompatibilityInfo string                   `json:"compatibility_info"`
	EstimatedSuccess  float64                  `json:"estimated_success"`
}

// NewFallbackParameterManager creates a new fallback parameter manager
func NewFallbackParameterManager(codec CodecType, streamID string) *FallbackParameterManager {
	manager := &FallbackParameterManager{
		codec:              codec,
		streamID:           streamID,
		compatibilityRules: make(map[string]CompatibilityRule),
		lastUpdate:         time.Now(),
	}

	// Initialize generic parameter sets for common scenarios
	manager.initializeGenericParameterSets()
	manager.initializeCompatibilityRules()

	return manager
}

// FindFallbackParameterSets attempts to find suitable fallback parameter sets
func (fpm *FallbackParameterManager) FindFallbackParameterSets(
	requiredSets map[string]uint32,
	availableSets map[string]*ParameterSet,
	strategy FallbackStrategy,
) *FallbackResult {
	fpm.mu.Lock()
	defer fpm.mu.Unlock()

	fpm.totalRequests++

	result := &FallbackResult{
		Found:         false,
		Strategy:      strategy,
		ParameterSets: make(map[string]*ParameterSet),
		QualityScore:  0.0,
	}

	switch strategy {
	case FallbackApproximate:
		return fpm.findApproximateMatch(requiredSets, availableSets, result)
	case FallbackCompatible:
		return fpm.findCompatibleMatch(requiredSets, availableSets, result)
	case FallbackGeneric:
		return fpm.findGenericMatch(requiredSets, result)
	default:
		result.CompatibilityInfo = "No fallback strategy specified"
		return result
	}
}

// findApproximateMatch attempts to find parameter sets with similar characteristics
func (fpm *FallbackParameterManager) findApproximateMatch(
	requiredSets map[string]uint32,
	availableSets map[string]*ParameterSet,
	result *FallbackResult,
) *FallbackResult {

	// For H.264, try to find approximate SPS/PPS matches
	if fpm.codec == CodecH264 {
		result = fpm.findApproximateH264Match(requiredSets, availableSets, result)
	}

	// For HEVC, try to find approximate VPS/SPS/PPS matches
	if fpm.codec == CodecHEVC {
		result = fpm.findApproximateHEVCMatch(requiredSets, availableSets, result)
	}

	if result.Found {
		fpm.approximateHits++
		fpm.fallbacksUsed++
	}

	return result
}

// findCompatibleMatch finds parameter sets that are known to be compatible
func (fpm *FallbackParameterManager) findCompatibleMatch(
	requiredSets map[string]uint32,
	availableSets map[string]*ParameterSet,
	result *FallbackResult,
) *FallbackResult {

	// Check compatibility rules for each required parameter set
	for setType, requiredID := range requiredSets {
		ruleKey := fmt.Sprintf("%s_%d", setType, requiredID)

		if rule, exists := fpm.compatibilityRules[ruleKey]; exists {
			// Find compatible parameter set based on rule
			compatible := fpm.findCompatibleParameterSet(setType, rule, availableSets)
			if compatible != nil {
				result.ParameterSets[setType] = compatible
				result.QualityScore += rule.QualityScore
			}
		}
	}

	// Check if we found all required parameter sets
	if len(result.ParameterSets) == len(requiredSets) {
		result.Found = true
		result.QualityScore /= float64(len(requiredSets)) // Average quality
		result.EstimatedSuccess = fpm.calculateSuccessProbability(result.ParameterSets)
		result.CompatibilityInfo = "Found compatible parameter sets using compatibility rules"

		fpm.compatibleHits++
		fpm.fallbacksUsed++
	} else {
		result.CompatibilityInfo = fmt.Sprintf("Only found %d of %d required parameter sets",
			len(result.ParameterSets), len(requiredSets))
	}

	return result
}

// findGenericMatch uses generic parameter sets as last resort
func (fpm *FallbackParameterManager) findGenericMatch(
	requiredSets map[string]uint32,
	result *FallbackResult,
) *FallbackResult {

	// Use generic parameter sets based on codec
	switch fpm.codec {
	case CodecH264:
		if _, needsSPS := requiredSets["sps"]; needsSPS && fpm.genericSPS != nil {
			result.ParameterSets["sps"] = fpm.genericSPS
		}
		if _, needsPPS := requiredSets["pps"]; needsPPS && fpm.genericPPS != nil {
			result.ParameterSets["pps"] = fpm.genericPPS
		}

	case CodecHEVC:
		if _, needsVPS := requiredSets["vps"]; needsVPS && fpm.genericVPS != nil {
			result.ParameterSets["vps"] = fpm.genericVPS
		}
		if _, needsSPS := requiredSets["sps"]; needsSPS && fpm.genericSPS != nil {
			result.ParameterSets["sps"] = fpm.genericSPS
		}
		if _, needsPPS := requiredSets["pps"]; needsPPS && fpm.genericPPS != nil {
			result.ParameterSets["pps"] = fpm.genericPPS
		}
	}

	// Check if we have all required sets
	if len(result.ParameterSets) == len(requiredSets) {
		result.Found = true
		result.QualityScore = 0.3     // Generic parameter sets have lower quality score
		result.EstimatedSuccess = 0.5 // Moderate success probability
		result.CompatibilityInfo = "Using generic parameter sets - basic decoding may work"

		fpm.genericHits++
		fpm.fallbacksUsed++
	} else {
		result.CompatibilityInfo = "Generic parameter sets not available for all required types"
	}

	return result
}

// findApproximateH264Match finds approximate H.264 parameter sets
func (fpm *FallbackParameterManager) findApproximateH264Match(
	requiredSets map[string]uint32,
	availableSets map[string]*ParameterSet,
	result *FallbackResult,
) *FallbackResult {

	// Simple heuristic: use any available SPS/PPS if they exist
	// In production, this would analyze profile/level compatibility

	if _, needsSPS := requiredSets["sps"]; needsSPS {
		for _, paramSet := range availableSets {
			if fpm.isLikelySPS(paramSet) {
				result.ParameterSets["sps"] = paramSet
				break
			}
		}
	}

	if _, needsPPS := requiredSets["pps"]; needsPPS {
		for _, paramSet := range availableSets {
			if fpm.isLikelyPPS(paramSet) {
				result.ParameterSets["pps"] = paramSet
				break
			}
		}
	}

	if len(result.ParameterSets) == len(requiredSets) {
		result.Found = true
		result.QualityScore = 0.7 // Approximate matches have decent quality
		result.EstimatedSuccess = 0.75
		result.CompatibilityInfo = "Found approximate H.264 parameter set matches"
	}

	return result
}

// findApproximateHEVCMatch finds approximate HEVC parameter sets
func (fpm *FallbackParameterManager) findApproximateHEVCMatch(
	requiredSets map[string]uint32,
	availableSets map[string]*ParameterSet,
	result *FallbackResult,
) *FallbackResult {

	// Similar logic for HEVC - use any available VPS/SPS/PPS
	parameterSetFound := make(map[string]bool)

	for setType := range requiredSets {
		for _, paramSet := range availableSets {
			if fpm.isLikelyHEVCParameterSet(paramSet, setType) {
				result.ParameterSets[setType] = paramSet
				parameterSetFound[setType] = true
				break
			}
		}
	}

	if len(result.ParameterSets) == len(requiredSets) {
		result.Found = true
		result.QualityScore = 0.7
		result.EstimatedSuccess = 0.75
		result.CompatibilityInfo = "Found approximate HEVC parameter set matches"
	}

	return result
}

// Helper methods for parameter set identification
func (fpm *FallbackParameterManager) isLikelySPS(paramSet *ParameterSet) bool {
	if len(paramSet.Data) == 0 {
		return false
	}
	// H.264 SPS NAL type is 7 (0x67 with F=0, NRI=3)
	return (paramSet.Data[0] & 0x1F) == 7
}

func (fpm *FallbackParameterManager) isLikelyPPS(paramSet *ParameterSet) bool {
	if len(paramSet.Data) == 0 {
		return false
	}
	// H.264 PPS NAL type is 8 (0x68 with F=0, NRI=3)
	return (paramSet.Data[0] & 0x1F) == 8
}

func (fpm *FallbackParameterManager) isLikelyHEVCParameterSet(paramSet *ParameterSet, setType string) bool {
	if len(paramSet.Data) == 0 {
		return false
	}

	nalType := (paramSet.Data[0] >> 1) & 0x3F
	switch setType {
	case "vps":
		return nalType == 32 // HEVC VPS
	case "sps":
		return nalType == 33 // HEVC SPS
	case "pps":
		return nalType == 34 // HEVC PPS
	}
	return false
}

// findCompatibleParameterSet finds a parameter set compatible with the given rule
func (fpm *FallbackParameterManager) findCompatibleParameterSet(
	setType string,
	rule CompatibilityRule,
	availableSets map[string]*ParameterSet,
) *ParameterSet {

	// Simple compatibility check - in production this would be more sophisticated
	for _, paramSet := range availableSets {
		if fpm.isParameterSetCompatible(paramSet, setType, rule) {
			return paramSet
		}
	}

	return nil
}

// isParameterSetCompatible checks if a parameter set matches compatibility rules
func (fpm *FallbackParameterManager) isParameterSetCompatible(
	paramSet *ParameterSet,
	setType string,
	rule CompatibilityRule,
) bool {

	// Basic compatibility checks
	if !paramSet.Valid {
		return false
	}

	// Check profile compatibility if available
	if paramSet.ProfileIDC != nil {
		profileCompatible := false
		for _, compatibleProfile := range rule.ProfileCompatible {
			if *paramSet.ProfileIDC == compatibleProfile {
				profileCompatible = true
				break
			}
		}
		if !profileCompatible && len(rule.ProfileCompatible) > 0 {
			return false
		}
	}

	// Check level compatibility if available
	if paramSet.LevelIDC != nil {
		levelCompatible := false
		for _, compatibleLevel := range rule.LevelCompatible {
			if *paramSet.LevelIDC == compatibleLevel {
				levelCompatible = true
				break
			}
		}
		if !levelCompatible && len(rule.LevelCompatible) > 0 {
			return false
		}
	}

	return true
}

// calculateSuccessProbability estimates the probability of successful decoding
func (fpm *FallbackParameterManager) calculateSuccessProbability(paramSets map[string]*ParameterSet) float64 {
	if len(paramSets) == 0 {
		return 0.0
	}

	totalScore := 0.0
	for _, paramSet := range paramSets {
		if paramSet.Valid {
			totalScore += 1.0
		} else {
			totalScore += 0.3 // Invalid parameter sets have lower success probability
		}
	}

	return totalScore / float64(len(paramSets))
}

// initializeGenericParameterSets creates generic parameter sets for fallback
func (fpm *FallbackParameterManager) initializeGenericParameterSets() {
	switch fpm.codec {
	case CodecH264:
		// Generic H.264 SPS for 1920x1080 Baseline Profile
		fpm.genericSPS = &ParameterSet{
			ID:       0,
			Data:     []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04, 0x40, 0x00, 0x00, 0x03, 0x00, 0x40, 0x00, 0x00, 0x0f, 0x03, 0xc5, 0x8b, 0xa8},
			ParsedAt: time.Now(),
			Size:     23,
			Valid:    true,
		}

		// Generic H.264 PPS
		fpm.genericPPS = &ParameterSet{
			ID:       0,
			Data:     []byte{0x68, 0xce, 0x38, 0x80},
			ParsedAt: time.Now(),
			Size:     4,
			Valid:    true,
		}

	case CodecHEVC:
		// Generic HEVC VPS
		fpm.genericVPS = &ParameterSet{
			ID:       0,
			Data:     []byte{0x40, 0x01, 0x0c, 0x01, 0xff, 0xff, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0x90, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x5d, 0xac, 0x09},
			ParsedAt: time.Now(),
			Size:     23,
			Valid:    true,
		}

		// Generic HEVC SPS
		fpm.genericSPS = &ParameterSet{
			ID:       0,
			Data:     []byte{0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0x90, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x5d, 0xa0, 0x02, 0x80, 0x80, 0x2d, 0x16},
			ParsedAt: time.Now(),
			Size:     24,
			Valid:    true,
		}

		// Generic HEVC PPS
		fpm.genericPPS = &ParameterSet{
			ID:       0,
			Data:     []byte{0x44, 0x01, 0xc1, 0x72, 0xb4, 0x62, 0x40},
			ParsedAt: time.Now(),
			Size:     7,
			Valid:    true,
		}
	}
}

// initializeCompatibilityRules sets up default compatibility rules
func (fpm *FallbackParameterManager) initializeCompatibilityRules() {
	if fpm.codec == CodecH264 {
		// H.264 Baseline Profile compatibility
		fpm.compatibilityRules["sps_0"] = CompatibilityRule{
			ProfileCompatible:   []uint8{66, 77, 88, 100}, // Baseline, Main, Extended, High
			LevelCompatible:     []uint8{30, 31, 32, 40, 41, 42, 50, 51, 52},
			ResolutionTolerance: 0.1, // 10% tolerance
			BitrateCompatible:   true,
			QualityScore:        0.8,
		}

		fpm.compatibilityRules["pps_0"] = CompatibilityRule{
			ProfileCompatible: []uint8{66, 77, 88, 100},
			QualityScore:      0.9,
		}
	}
}

// GetStatistics returns fallback manager statistics
func (fpm *FallbackParameterManager) GetStatistics() map[string]interface{} {
	fpm.mu.RLock()
	defer fpm.mu.RUnlock()

	fallbackRate := float64(0)
	if fpm.totalRequests > 0 {
		fallbackRate = float64(fpm.fallbacksUsed) / float64(fpm.totalRequests)
	}

	return map[string]interface{}{
		"stream_id":        fpm.streamID,
		"codec":            fpm.codec.String(),
		"total_requests":   fpm.totalRequests,
		"fallbacks_used":   fpm.fallbacksUsed,
		"fallback_rate":    fallbackRate,
		"approximate_hits": fpm.approximateHits,
		"compatible_hits":  fpm.compatibleHits,
		"generic_hits":     fpm.genericHits,
		"last_update":      fpm.lastUpdate,
		"rules_count":      len(fpm.compatibilityRules),
	}
}

// UpdateCompatibilityRule adds or updates a compatibility rule
func (fpm *FallbackParameterManager) UpdateCompatibilityRule(key string, rule CompatibilityRule) {
	fpm.mu.Lock()
	defer fpm.mu.Unlock()

	fpm.compatibilityRules[key] = rule
	fpm.lastUpdate = time.Now()
}

// Close cleans up resources
func (fpm *FallbackParameterManager) Close() {
	fpm.mu.Lock()
	defer fpm.mu.Unlock()

	// Clear references to help GC
	fpm.compatibilityRules = nil
	fpm.genericSPS = nil
	fpm.genericPPS = nil
	fpm.genericVPS = nil
}
