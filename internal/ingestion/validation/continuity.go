package validation

import (
	"fmt"
	"sync"
)

// ContinuityValidator tracks MPEG-TS continuity counters per PID
type ContinuityValidator struct {
	mu       sync.RWMutex
	counters map[uint16]*continuityState
	stats    ContinuityStats
}

// continuityState tracks continuity counter for a single PID
type continuityState struct {
	lastCounter     uint8
	expectedNext    uint8
	initialized     bool
	discontinuities int64
}

// ContinuityStats contains continuity validation statistics
type ContinuityStats struct {
	TotalDiscontinuities int64
	PIDs                 int
	PacketsValidated     int64
}

// NewContinuityValidator creates a new continuity validator
func NewContinuityValidator() *ContinuityValidator {
	return &ContinuityValidator{
		counters: make(map[uint16]*continuityState),
	}
}

// ValidateContinuity checks if the continuity counter is correct for the given PID
func (v *ContinuityValidator) ValidateContinuity(pid uint16, counter uint8, hasPayload bool, hasAdaptation bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Get or create state for this PID
	state, exists := v.counters[pid]
	if !exists {
		state = &continuityState{}
		v.counters[pid] = state
	}

	v.stats.PacketsValidated++

	// First packet for this PID
	if !state.initialized {
		state.lastCounter = counter
		state.expectedNext = (counter + 1) & 0x0F
		state.initialized = true
		return nil
	}

	// Special cases where continuity counter should not increment
	if !hasPayload && hasAdaptation {
		// Adaptation field only - counter should not change
		if counter != state.lastCounter {
			state.discontinuities++
			v.stats.TotalDiscontinuities++
			return fmt.Errorf("continuity error for PID %d: expected %d (no increment), got %d",
				pid, state.lastCounter, counter)
		}
		return nil
	}

	// Check for expected counter
	if counter != state.expectedNext {
		// Check if it's a duplicate (retransmission)
		if counter == state.lastCounter {
			// Duplicate packet - this is allowed in MPEG-TS
			return nil
		}

		state.discontinuities++
		v.stats.TotalDiscontinuities++
		err := fmt.Errorf("continuity error for PID %d: expected %d, got %d",
			pid, state.expectedNext, counter)

		// Update state even on error to continue tracking
		state.lastCounter = counter
		state.expectedNext = (counter + 1) & 0x0F

		return err
	}

	// Update state
	state.lastCounter = counter
	state.expectedNext = (counter + 1) & 0x0F

	return nil
}

// GetStats returns continuity validation statistics
func (v *ContinuityValidator) GetStats() ContinuityStats {
	v.mu.RLock()
	defer v.mu.RUnlock()

	stats := v.stats
	stats.PIDs = len(v.counters)
	return stats
}

// GetPIDStats returns statistics for a specific PID
func (v *ContinuityValidator) GetPIDStats(pid uint16) (int64, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	state, exists := v.counters[pid]
	if !exists {
		return 0, false
	}

	return state.discontinuities, true
}

// Reset clears all continuity counters
func (v *ContinuityValidator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.counters = make(map[uint16]*continuityState)
	v.stats = ContinuityStats{}
}

// ResetPID resets the continuity counter for a specific PID
func (v *ContinuityValidator) ResetPID(pid uint16) {
	v.mu.Lock()
	defer v.mu.Unlock()

	delete(v.counters, pid)
}
