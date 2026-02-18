package validation

import (
	"fmt"
	"sync"
	"time"
)

const (
	// MaxPTSValue is the maximum value for a 33-bit PTS/DTS (2^33 - 1)
	MaxPTSValue = (1 << 33) - 1

	// PTSWrapThreshold is the threshold for detecting PTS wraparound
	// If the difference is greater than half the max value, it's likely a wrap
	PTSWrapThreshold = MaxPTSValue / 2

	// MaxTimestampJump is the maximum allowed forward jump in timestamps (10 seconds at 90kHz)
	MaxTimestampJump = 10 * 90000
)

// PTSDTSValidator validates PTS/DTS timestamps
type PTSDTSValidator struct {
	streamID     string
	lastPTS      int64
	lastDTS      int64
	ptsWrapCount int
	dtsWrapCount int
	initialized  bool
	errorCount   int
	mu           sync.RWMutex
}

// ValidationResult contains the result of timestamp validation
type ValidationResult struct {
	Valid        bool
	Error        error
	Warnings     []string
	CorrectedPTS int64
	CorrectedDTS int64
	PTSWrapped   bool
	DTSWrapped   bool
}

// NewPTSDTSValidator creates a new PTS/DTS validator
func NewPTSDTSValidator(streamID string) *PTSDTSValidator {
	return &PTSDTSValidator{
		streamID: streamID,
	}
}

// ValidateTimestamps validates PTS and DTS values
func (v *PTSDTSValidator) ValidateTimestamps(pts, dts int64) *ValidationResult {
	v.mu.Lock()
	defer v.mu.Unlock()

	result := &ValidationResult{
		Valid:        true,
		CorrectedPTS: pts,
		CorrectedDTS: dts,
		Warnings:     make([]string, 0),
	}

	// Validate PTS range
	if pts < 0 {
		result.Valid = false
		result.Error = fmt.Errorf("negative PTS value: %d", pts)
		v.errorCount++
		return result
	}

	if pts > MaxPTSValue {
		result.Valid = false
		result.Error = fmt.Errorf("PTS exceeds 33-bit limit: %d > %d", pts, MaxPTSValue)
		v.errorCount++
		return result
	}

	// Validate DTS range if present
	if dts != 0 {
		if dts < 0 {
			result.Valid = false
			result.Error = fmt.Errorf("negative DTS value: %d", dts)
			v.errorCount++
			return result
		}

		if dts > MaxPTSValue {
			result.Valid = false
			result.Error = fmt.Errorf("DTS exceeds 33-bit limit: %d > %d", dts, MaxPTSValue)
			v.errorCount++
			return result
		}

		// Validate PTS >= DTS
		if pts < dts {
			result.Valid = false
			result.Error = fmt.Errorf("PTS < DTS: %d < %d", pts, dts)
			v.errorCount++
			return result
		}

		// Warn if PTS-DTS difference is too large (> 1 second)
		if pts-dts > 90000 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("large PTS-DTS difference: %d (%.2fs)", pts-dts, float64(pts-dts)/90000))
		}
	}

	// If this is the first timestamp, just record it
	if !v.initialized {
		v.lastPTS = pts
		v.lastDTS = dts
		v.initialized = true
		return result
	}

	// Check for PTS wraparound
	ptsDiff := pts - v.lastPTS
	if ptsDiff < -PTSWrapThreshold {
		// Likely a wraparound
		v.ptsWrapCount++
		result.PTSWrapped = true
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("PTS wraparound detected: %d -> %d (wrap #%d)", v.lastPTS, pts, v.ptsWrapCount))

		// Correct the PTS for internal tracking
		result.CorrectedPTS = pts + int64(v.ptsWrapCount)*MaxPTSValue
	} else if ptsDiff > MaxTimestampJump {
		// Large forward jump - might be a discontinuity
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("large PTS jump: %d -> %d (diff: %d)", v.lastPTS, pts, ptsDiff))
	} else if ptsDiff < 0 && ptsDiff > -MaxTimestampJump {
		// Small backward jump - likely out of order
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("PTS went backward: %d -> %d (diff: %d)", v.lastPTS, pts, ptsDiff))
	}

	// Check for DTS wraparound if DTS is present
	if dts != 0 && v.lastDTS != 0 {
		dtsDiff := dts - v.lastDTS
		if dtsDiff < -PTSWrapThreshold {
			// Likely a wraparound
			v.dtsWrapCount++
			result.DTSWrapped = true
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("DTS wraparound detected: %d -> %d (wrap #%d)", v.lastDTS, dts, v.dtsWrapCount))

			// Correct the DTS for internal tracking
			result.CorrectedDTS = dts + int64(v.dtsWrapCount)*MaxPTSValue
		} else if dtsDiff < 0 && dtsDiff > -MaxTimestampJump {
			// DTS should always be monotonic
			result.Valid = false
			result.Error = fmt.Errorf("DTS went backward: %d -> %d (diff: %d)", v.lastDTS, dts, dtsDiff)
			v.errorCount++
			return result
		}
	}

	// Update last values
	v.lastPTS = pts
	v.lastDTS = dts

	return result
}

// GetStatistics returns validation statistics
func (v *PTSDTSValidator) GetStatistics() map[string]interface{} {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return map[string]interface{}{
		"stream_id":      v.streamID,
		"initialized":    v.initialized,
		"last_pts":       v.lastPTS,
		"last_dts":       v.lastDTS,
		"pts_wrap_count": v.ptsWrapCount,
		"dts_wrap_count": v.dtsWrapCount,
		"error_count":    v.errorCount,
	}
}

// Reset resets the validator state
func (v *PTSDTSValidator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.lastPTS = 0
	v.lastDTS = 0
	v.ptsWrapCount = 0
	v.dtsWrapCount = 0
	v.initialized = false
	v.errorCount = 0
}

// PTSDTSValidationService provides centralized PTS/DTS validation
type PTSDTSValidationService struct {
	validators map[string]*PTSDTSValidator
	mu         sync.RWMutex
}

// NewPTSDTSValidationService creates a new validation service
func NewPTSDTSValidationService() *PTSDTSValidationService {
	return &PTSDTSValidationService{
		validators: make(map[string]*PTSDTSValidator),
	}
}

// GetValidator gets or creates a validator for a stream
func (s *PTSDTSValidationService) GetValidator(streamID string) *PTSDTSValidator {
	s.mu.Lock()
	defer s.mu.Unlock()

	validator, exists := s.validators[streamID]
	if !exists {
		validator = NewPTSDTSValidator(streamID)
		s.validators[streamID] = validator
	}

	return validator
}

// RemoveValidator removes a validator for a stream
func (s *PTSDTSValidationService) RemoveValidator(streamID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.validators, streamID)
}

// GetAllStatistics returns statistics for all validators
func (s *PTSDTSValidationService) GetAllStatistics() map[string]map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]map[string]interface{})
	for streamID, validator := range s.validators {
		stats[streamID] = validator.GetStatistics()
	}

	return stats
}

// ValidatePTSRange validates that a PTS value is within the valid 33-bit range
func ValidatePTSRange(pts int64) error {
	if pts < 0 {
		return fmt.Errorf("negative PTS value: %d", pts)
	}
	if pts > MaxPTSValue {
		return fmt.Errorf("PTS exceeds 33-bit limit: %d > %d", pts, MaxPTSValue)
	}
	return nil
}

// ValidateDTSRange validates that a DTS value is within the valid 33-bit range
func ValidateDTSRange(dts int64) error {
	if dts < 0 {
		return fmt.Errorf("negative DTS value: %d", dts)
	}
	if dts > MaxPTSValue {
		return fmt.Errorf("DTS exceeds 33-bit limit: %d > %d", dts, MaxPTSValue)
	}
	return nil
}

// ValidatePTSDTSOrder validates that PTS >= DTS
func ValidatePTSDTSOrder(pts, dts int64) error {
	if dts != 0 && pts < dts {
		return fmt.Errorf("PTS < DTS: %d < %d", pts, dts)
	}
	return nil
}

// CalculateTimeDuration converts a PTS/DTS value to time.Duration at a given clock rate
func CalculateTimeDuration(timestamp int64, clockRate int64) time.Duration {
	if clockRate == 0 {
		return 0
	}
	return time.Duration(timestamp) * time.Second / time.Duration(clockRate)
}

// IsLikelyWraparound checks if a timestamp difference indicates a wraparound
func IsLikelyWraparound(oldTS, newTS int64) bool {
	diff := newTS - oldTS
	return diff < -PTSWrapThreshold
}

// HandleWraparound adjusts a timestamp for wraparound
func HandleWraparound(timestamp int64, wrapCount int) int64 {
	return timestamp + int64(wrapCount)*(MaxPTSValue+1)
}
