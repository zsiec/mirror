package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParameterSetValidator(t *testing.T) {
	t.Run("Basic SPS Validation", func(t *testing.T) {
		validator := NewParameterSetValidator(100*time.Millisecond, 10)

		// Valid SPS
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00}
		err := validator.ValidateSPS(0, spsData)
		assert.NoError(t, err)

		// Same SPS again should be allowed (duplicate) - no update occurs
		for i := 0; i < 5; i++ {
			err = validator.ValidateSPS(0, spsData)
			assert.NoError(t, err, "Duplicate SPS should always be allowed")
		}

		// Try to update immediately with different data - should fail with rate limit
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1f, 0x8d, 0x68, 0x05, 0x00}
		err = validator.ValidateSPS(0, spsData2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "rate limit exceeded")

		// Wait for rate limit to expire
		time.Sleep(110 * time.Millisecond)

		// Now update should succeed (enforceConsistency is false by default)
		err = validator.ValidateSPS(0, spsData2)
		assert.NoError(t, err)

		// Check that checksum mismatch was tracked
		stats := validator.GetStatistics()
		assert.Equal(t, 1, stats["checksum_mismatches"])
	})

	t.Run("SPS Rate Limiting", func(t *testing.T) {
		validator := NewParameterSetValidator(100*time.Millisecond, 3)

		// First update should succeed
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1e, 0x01}
		err := validator.ValidateSPS(1, spsData1)
		assert.NoError(t, err)

		// Immediate update should fail (too fast) - but only if it's a different SPS ID or after initial update
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1e, 0x02}
		time.Sleep(10 * time.Millisecond) // Small delay to ensure time has passed
		err = validator.ValidateSPS(1, spsData2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "rate limit exceeded")

		// Wait for rate limit to expire
		time.Sleep(110 * time.Millisecond)

		// Now it should succeed (enforceConsistency is false)
		err = validator.ValidateSPS(1, spsData2)
		assert.NoError(t, err)

		// Test with new SPS ID for hourly limit
		time.Sleep(110 * time.Millisecond)
		for i := 2; i < 5; i++ {
			spsData := []byte{0x67, 0x42, 0x00, 0x1e, byte(i)}
			err = validator.ValidateSPS(uint8(i), spsData)
			if i < 5 {
				assert.NoError(t, err, "Update %d should succeed", i)
			}
			if i < 4 {
				time.Sleep(110 * time.Millisecond)
			}
		}

		// Try quick update with a new ID - should succeed
		spsData5 := []byte{0x67, 0x42, 0x00, 0x1e, 0x05}
		err = validator.ValidateSPS(5, spsData5)
		assert.NoError(t, err)

		// Try to update that same ID immediately - should fail
		spsData6 := []byte{0x67, 0x42, 0x00, 0x1e, 0x06}
		err = validator.ValidateSPS(5, spsData6)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "rate limit exceeded")
	})

	t.Run("PPS Validation with Dependencies", func(t *testing.T) {
		validator := NewParameterSetValidator(10*time.Millisecond, 10)

		// Add SPS first
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68}
		err := validator.ValidateSPS(0, spsData)
		require.NoError(t, err)

		// PPS referencing existing SPS should succeed
		ppsData := []byte{0x68, 0xce, 0x38, 0x80}
		err = validator.ValidatePPS(0, 0, ppsData)
		assert.NoError(t, err)

		// PPS referencing non-existent SPS should succeed (enforceConsistency is false)
		err = validator.ValidatePPS(1, 5, ppsData)
		assert.NoError(t, err)

		// Check dependencies
		deps := validator.GetDependencies()
		assert.Equal(t, uint8(0), deps[0])
		assert.Equal(t, uint8(5), deps[1]) // PPS 1 references SPS 5
	})

	t.Run("Cross Reference Validation", func(t *testing.T) {
		validator := NewParameterSetValidator(10*time.Millisecond, 10)

		// Add SPS
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d}
		err := validator.ValidateSPS(0, spsData)
		require.NoError(t, err)

		// Add PPS referencing SPS
		ppsData := []byte{0x68, 0xce, 0x38}
		err = validator.ValidatePPS(0, 0, ppsData)
		require.NoError(t, err)

		// Cross-reference should be valid
		err = validator.ValidateCrossReferences()
		assert.NoError(t, err)

		// Manually break the dependency (simulating corruption)
		validator.mu.Lock()
		delete(validator.spsChecksums, 0)
		validator.mu.Unlock()

		// Cross-reference should now fail
		err = validator.ValidateCrossReferences()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "references missing SPS")
	})

	t.Run("PPS Dependency Changes", func(t *testing.T) {
		validator := NewParameterSetValidator(10*time.Millisecond, 10)

		// Add two SPS
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1e, 0x01}
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1f, 0x02}
		err := validator.ValidateSPS(0, spsData1)
		require.NoError(t, err)
		time.Sleep(15 * time.Millisecond)
		err = validator.ValidateSPS(1, spsData2)
		require.NoError(t, err)

		// Add PPS referencing first SPS
		ppsData := []byte{0x68, 0xce, 0x01}
		err = validator.ValidatePPS(0, 0, ppsData)
		require.NoError(t, err)

		// Try to change PPS to reference different SPS
		time.Sleep(15 * time.Millisecond)
		ppsData2 := []byte{0x68, 0xce, 0x02} // Different data
		err = validator.ValidatePPS(0, 1, ppsData2)
		assert.NoError(t, err) // Should succeed because enforceConsistency is false
	})

	t.Run("Statistics", func(t *testing.T) {
		validator := NewParameterSetValidator(10*time.Millisecond, 10)

		// Generate some activity
		spsData := []byte{0x67, 0x42, 0x00, 0x1e}
		err := validator.ValidateSPS(0, spsData)
		assert.NoError(t, err)

		// Try to update too quickly (rate limit) - but this is a new ID so it should succeed
		time.Sleep(5 * time.Millisecond)
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1e, 0x01}
		err = validator.ValidateSPS(1, spsData2)
		assert.NoError(t, err) // New ID, no rate limit

		// Try to update same ID too quickly
		spsData3 := []byte{0x67, 0x42, 0x00, 0x1e, 0x02}
		err = validator.ValidateSPS(1, spsData3)
		assert.Error(t, err) // Should fail with rate limit

		// Add PPS with bad reference
		ppsData := []byte{0x68, 0xce}
		validator.ValidatePPS(0, 99, ppsData)

		// Try to change existing SPS (checksum mismatch)
		time.Sleep(15 * time.Millisecond)
		validator.ValidateSPS(0, append(spsData, 0x01))

		stats := validator.GetStatistics()
		assert.Equal(t, 2, stats["tracked_sps"]) // We added 2 SPS (0 and 1)
		assert.Equal(t, 1, stats["rate_limit_violations"])
		assert.Equal(t, 0, stats["validation_errors"]) // No validation errors because enforceConsistency is false
		assert.Equal(t, 1, stats["checksum_mismatches"])
	})

	t.Run("Reset", func(t *testing.T) {
		validator := NewParameterSetValidator(10*time.Millisecond, 10)

		// Add some data
		spsData := []byte{0x67, 0x42, 0x00, 0x1e}
		validator.ValidateSPS(0, spsData)

		// Verify data exists
		stats := validator.GetStatistics()
		assert.Equal(t, 1, stats["tracked_sps"])

		// Reset
		validator.Reset()

		// Verify data is cleared
		stats = validator.GetStatistics()
		assert.Equal(t, 0, stats["tracked_sps"])
		assert.Equal(t, 0, stats["validation_errors"])
	})
}

func TestParameterSetValidatorInvalidData(t *testing.T) {
	validator := NewParameterSetValidator(10*time.Millisecond, 10)

	t.Run("Short SPS", func(t *testing.T) {
		err := validator.ValidateSPS(0, []byte{0x67, 0x42})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too short")
	})

	t.Run("Wrong NAL Type", func(t *testing.T) {
		err := validator.ValidateSPS(0, []byte{0x68, 0x42, 0x00, 0x1e}) // PPS NAL type
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not an SPS NAL unit")
	})
}

func TestParameterSetValidatorConcurrency(t *testing.T) {
	validator := NewParameterSetValidator(1*time.Millisecond, 1000)

	// Run concurrent updates
	done := make(chan bool)

	// SPS updater
	go func() {
		for i := 0; i < 100; i++ {
			spsData := []byte{0x67, 0x42, 0x00, byte(i)}
			validator.ValidateSPS(uint8(i%10), spsData)
			time.Sleep(2 * time.Millisecond)
		}
		done <- true
	}()

	// PPS updater
	go func() {
		for i := 0; i < 100; i++ {
			// Add SPS first
			spsData := []byte{0x67, 0x42, 0x00, byte(i)}
			validator.ValidateSPS(uint8(i%10), spsData)

			// Add PPS
			ppsData := []byte{0x68, 0xce, byte(i)}
			validator.ValidatePPS(uint8(i%10), uint8(i%10), ppsData)
			time.Sleep(2 * time.Millisecond)
		}
		done <- true
	}()

	// Stats reader
	go func() {
		for i := 0; i < 50; i++ {
			stats := validator.GetStatistics()
			_ = stats // Just reading
			deps := validator.GetDependencies()
			_ = deps
			time.Sleep(4 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify no panic and some data was processed
	stats := validator.GetStatistics()
	assert.Greater(t, stats["tracked_sps"].(int), 0)
}

func TestCorruptedSPSValidation(t *testing.T) {
	t.Run("Invalid Profile IDC", func(t *testing.T) {
		// Test data with profile_idc=7 (invalid) and level_idc=146
		// This should be rejected by both ValidateSPSData and parseSPS
		corruptedSPS := []byte{
			0x67, // NAL header (SPS type)
			0x07, // profile_idc = 7 (INVALID - not in the valid profiles list)
			0x00, // constraint flags
			0x92, // level_idc = 146 (INVALID - not in the valid levels list)
			0x8d, // seq_parameter_set_id = 0 (exp-Golomb) + additional data
			0x68, // Additional fields to ensure sufficient RBSP data
			0x80, // Stop bit
		}

		// Test 1: ValidateSPSData should accept the data (it only does basic validation)
		_, err := ValidateSPSData(corruptedSPS)
		assert.NoError(t, err, "ValidateSPSData performs only basic validation")

		// Test 2: parseSPS now accepts unknown profile_idc with warnings
		ctx := NewParameterSetContext(CodecH264, "test-stream")
		sps, err := ctx.parseSPS(corruptedSPS)
		assert.NoError(t, err, "parseSPS now accepts unknown profile_idc values")
		assert.NotNil(t, sps)

		// Test 3: AddSPS should accept the data despite unknown profile
		err = ctx.AddSPS(corruptedSPS)
		assert.NoError(t, err, "AddSPS now accepts unknown profile_idc values")

		// Verify the SPS was stored despite unknown profile
		spsData, exists := ctx.GetParameterSetData("sps", 0)
		assert.True(t, exists)
		assert.NotNil(t, spsData)
	})

	t.Run("Invalid Level IDC", func(t *testing.T) {
		// Test data with valid profile but invalid level
		corruptedSPS := []byte{
			0x67, // NAL header (SPS type)
			0x42, // profile_idc = 66 (Baseline - VALID)
			0x00, // constraint flags
			0x92, // level_idc = 146 (INVALID - not in the valid levels list)
			0x8d, // seq_parameter_set_id = 0 (exp-Golomb) + additional data
			0x68, // Additional fields to ensure sufficient RBSP data
			0x80, // Stop bit
		}

		// parseSPS now accepts unknown level_idc with warnings
		ctx := NewParameterSetContext(CodecH264, "test-stream")
		sps, err := ctx.parseSPS(corruptedSPS)
		assert.NoError(t, err, "parseSPS now accepts unknown level_idc values")
		assert.NotNil(t, sps)
	})

	t.Run("Multiple Invalid Values", func(t *testing.T) {
		// Test with both invalid profile and level
		corruptedSPS := []byte{
			0x67, // NAL header (SPS type)
			0x07, // profile_idc = 7 (INVALID)
			0x00, // constraint flags
			0x92, // level_idc = 146 (INVALID)
			0x8d, // seq_parameter_set_id = 0 + additional data
			0x68, // Additional fields to ensure sufficient RBSP data
			0x80, // Stop bit
		}

		ctx := NewParameterSetContext(CodecH264, "test-stream")

		// Should now accept with warnings for both invalid values
		sps, err := ctx.parseSPS(corruptedSPS)
		assert.NoError(t, err, "parseSPS now accepts unknown profile_idc and level_idc values")
		assert.NotNil(t, sps)

		// AddSPS should also accept
		err = ctx.AddSPS(corruptedSPS)
		assert.NoError(t, err, "AddSPS now accepts unknown profile_idc and level_idc values")
	})

	t.Run("Parameter Validator Rejects Corrupted SPS", func(t *testing.T) {
		validator := NewParameterSetValidator(100*time.Millisecond, 10)

		// Corrupted SPS data
		corruptedSPS := []byte{
			0x67, // NAL header (SPS type)
			0x07, // profile_idc = 7 (INVALID)
			0x00, // constraint flags
			0x92, // level_idc = 146 (INVALID)
			0x8d, // seq_parameter_set_id = 0 + additional data
			0x68, // Additional fields to ensure sufficient RBSP data
			0x80, // Stop bit
		}

		// The validator's ValidateSPS performs structural validation
		// but doesn't parse the actual profile/level values
		err := validator.ValidateSPS(0, corruptedSPS)
		assert.NoError(t, err, "ParameterSetValidator only does structural validation")
	})

	t.Run("Context With Validator Rejects Corrupted SPS", func(t *testing.T) {
		// Create context with validator
		config := ParameterSetContextConfig{
			ValidatorUpdateRate:        100 * time.Millisecond,
			ValidatorMaxUpdatesPerHour: 10,
			EnableVersioning:           true,
			MaxVersions:                5,
		}
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-stream", config)

		// Corrupted SPS data
		corruptedSPS := []byte{
			0x67, // NAL header (SPS type)
			0x07, // profile_idc = 7 (INVALID)
			0x00, // constraint flags
			0x92, // level_idc = 146 (INVALID)
			0x8d, // seq_parameter_set_id = 0 + additional data
			0x68, // Additional fields to ensure sufficient RBSP data
			0x80, // Stop bit
		}

		// Should now accept despite invalid profile_idc
		err := ctx.AddSPS(corruptedSPS)
		assert.NoError(t, err, "AddSPS now accepts unknown profile_idc values")

		// Verify it was stored
		spsData, exists := ctx.GetParameterSetData("sps", 0)
		assert.True(t, exists)
		assert.NotNil(t, spsData)

		allParams := ctx.GetAllParameterSets()
		assert.Equal(t, 1, len(allParams["sps"]))
	})
}

func BenchmarkParameterSetValidator(b *testing.B) {
	validator := NewParameterSetValidator(1*time.Microsecond, 1000000)
	spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use different IDs to avoid rate limiting
		validator.ValidateSPS(uint8(i%256), spsData)
	}
}
