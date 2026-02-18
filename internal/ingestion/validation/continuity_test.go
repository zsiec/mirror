package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContinuityValidator_BasicSequence(t *testing.T) {
	validator := NewContinuityValidator()
	pid := uint16(100)

	// Normal sequence
	for i := 0; i < 16; i++ {
		err := validator.ValidateContinuity(pid, uint8(i), true, false)
		assert.NoError(t, err, "Counter %d should be valid", i)
	}

	// Wraparound from 15 to 0
	err := validator.ValidateContinuity(pid, 0, true, false)
	assert.NoError(t, err, "Wraparound from 15 to 0 should be valid")

	stats := validator.GetStats()
	assert.Equal(t, int64(0), stats.TotalDiscontinuities)
	assert.Equal(t, int64(17), stats.PacketsValidated)
}

func TestContinuityValidator_Discontinuity(t *testing.T) {
	validator := NewContinuityValidator()
	pid := uint16(100)

	// Start sequence
	validator.ValidateContinuity(pid, 5, true, false)
	validator.ValidateContinuity(pid, 6, true, false)

	// Skip counter 7
	err := validator.ValidateContinuity(pid, 8, true, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected 7, got 8")

	stats := validator.GetStats()
	assert.Equal(t, int64(1), stats.TotalDiscontinuities)
}

func TestContinuityValidator_DuplicatePacket(t *testing.T) {
	validator := NewContinuityValidator()
	pid := uint16(100)

	// Normal packet
	err := validator.ValidateContinuity(pid, 5, true, false)
	assert.NoError(t, err)

	// Duplicate packet (same counter) - should be allowed
	err = validator.ValidateContinuity(pid, 5, true, false)
	assert.NoError(t, err, "Duplicate packets should be allowed")

	// Next packet should still expect 6
	err = validator.ValidateContinuity(pid, 6, true, false)
	assert.NoError(t, err)

	stats := validator.GetStats()
	assert.Equal(t, int64(0), stats.TotalDiscontinuities)
}

func TestContinuityValidator_AdaptationOnly(t *testing.T) {
	validator := NewContinuityValidator()
	pid := uint16(100)

	// Normal packet
	validator.ValidateContinuity(pid, 5, true, false)

	// Adaptation field only - counter should not increment
	err := validator.ValidateContinuity(pid, 5, false, true)
	assert.NoError(t, err, "Adaptation-only packet should not increment counter")

	// Next packet with payload should expect 6
	err = validator.ValidateContinuity(pid, 6, true, false)
	assert.NoError(t, err)
}

func TestContinuityValidator_MultiplePIDs(t *testing.T) {
	validator := NewContinuityValidator()

	// Track different PIDs independently
	pids := []uint16{100, 200, 300}

	for _, pid := range pids {
		for i := 0; i < 5; i++ {
			err := validator.ValidateContinuity(pid, uint8(i), true, false)
			assert.NoError(t, err)
		}
	}

	stats := validator.GetStats()
	assert.Equal(t, 3, stats.PIDs)
	assert.Equal(t, int64(15), stats.PacketsValidated)
	assert.Equal(t, int64(0), stats.TotalDiscontinuities)

	// Check individual PID stats
	for _, pid := range pids {
		discontinuities, exists := validator.GetPIDStats(pid)
		assert.True(t, exists)
		assert.Equal(t, int64(0), discontinuities)
	}
}

func TestContinuityValidator_Reset(t *testing.T) {
	validator := NewContinuityValidator()
	pid := uint16(100)

	// Add some data
	validator.ValidateContinuity(pid, 5, true, false)
	validator.ValidateContinuity(pid, 7, true, false) // Discontinuity

	// Reset all
	validator.Reset()

	stats := validator.GetStats()
	assert.Equal(t, 0, stats.PIDs)
	assert.Equal(t, int64(0), stats.TotalDiscontinuities)
	assert.Equal(t, int64(0), stats.PacketsValidated)

	// Should start fresh
	err := validator.ValidateContinuity(pid, 10, true, false)
	assert.NoError(t, err)
}

func TestContinuityValidator_ResetPID(t *testing.T) {
	validator := NewContinuityValidator()
	pid1 := uint16(100)
	pid2 := uint16(200)

	// Add data for both PIDs
	validator.ValidateContinuity(pid1, 5, true, false)
	validator.ValidateContinuity(pid2, 5, true, false)

	// Reset only PID1
	validator.ResetPID(pid1)

	// PID1 should start fresh
	err := validator.ValidateContinuity(pid1, 10, true, false)
	assert.NoError(t, err)

	// PID2 should continue normally
	err = validator.ValidateContinuity(pid2, 6, true, false)
	assert.NoError(t, err)
}

func TestContinuityValidator_Wraparound(t *testing.T) {
	validator := NewContinuityValidator()
	pid := uint16(100)

	// Start near wraparound
	validator.ValidateContinuity(pid, 14, true, false)
	validator.ValidateContinuity(pid, 15, true, false)

	// Wraparound to 0
	err := validator.ValidateContinuity(pid, 0, true, false)
	assert.NoError(t, err, "Wraparound from 15 to 0 should be valid")

	// Continue sequence
	err = validator.ValidateContinuity(pid, 1, true, false)
	assert.NoError(t, err)
}
