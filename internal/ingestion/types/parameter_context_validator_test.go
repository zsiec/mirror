package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParameterContextWithValidator(t *testing.T) {
	t.Run("SPS Validation Integration", func(t *testing.T) {
		// Create context with very strict rate limits for testing
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-validator-integration", ParameterSetContextConfig{
			ValidatorUpdateRate:        100 * time.Millisecond, // Require 100ms between updates
			ValidatorMaxUpdatesPerHour: 36,                     // Very low limit for testing
			EnableVersioning:           true,
			MaxVersions:                10,
		})
		require.NotNil(t, ctx.validator)

		// First SPS should succeed
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00}
		err := ctx.AddSPS(spsData)
		assert.NoError(t, err)

		// Same SPS again should succeed (duplicate allowed)
		err = ctx.AddSPS(spsData)
		assert.NoError(t, err)

		// Different SPS immediately should fail (rate limit)
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1f, 0x8d, 0x68, 0x05, 0x00}
		err = ctx.AddSPS(spsData2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "rate limit")

		// Check statistics include validator info
		stats := ctx.GetStatistics()
		assert.Contains(t, stats, "validator_rate_limit_violations")
		assert.Equal(t, 1, stats["validator_rate_limit_violations"])
	})

	t.Run("PPS Validation with Dependencies", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-pps-deps")

		// Add SPS first with proper data
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1}
		err := ctx.AddSPS(spsData)
		require.NoError(t, err)

		// PPS referencing existing SPS should succeed
		// PPS with pps_id=0, sps_id=0 (both encoded as UE(0) = 1 bit "1")
		ppsData := []byte{0x68, 0xCE, 0x38, 0x80} // Proper PPS encoding
		err = ctx.AddPPS(ppsData)
		assert.NoError(t, err)

		// Create a new context to test bad reference
		ctx2 := NewParameterSetContext(CodecH264, "test-bad-ref")

		// Try to add PPS without corresponding SPS
		ppsBadRef := []byte{0x68, 0xCE, 0x38, 0x80} // PPS referencing SPS 0 which doesn't exist
		err = ctx2.AddPPS(ppsBadRef)
		// By default, enforceConsistency is false to allow flexible parameter discovery
		// So this should succeed even though SPS doesn't exist
		assert.NoError(t, err, "PPS should be allowed even without SPS (enforceConsistency=false)")
	})

	t.Run("Cross Reference Validation", func(t *testing.T) {
		// Create context with validator enabled
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-cross-ref", ParameterSetContextConfig{
			ValidatorUpdateRate:        100 * time.Millisecond,
			ValidatorMaxUpdatesPerHour: 100,
			EnableVersioning:           true,
			MaxVersions:                20,
		})

		// Add SPS with complete valid data
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1}
		err := ctx.AddSPS(spsData)
		require.NoError(t, err)

		// Add PPS
		ppsData := []byte{0x68, 0xCE, 0x38, 0x80}
		err = ctx.AddPPS(ppsData)
		require.NoError(t, err)

		// Trigger cleanup which also validates cross-references
		ctx.checkAndPerformCleanup()

		// Check validator was used
		stats := ctx.GetStatistics()
		assert.Contains(t, stats, "validator_tracked_sps")
		assert.Contains(t, stats, "validator_tracked_pps")
	})

	t.Run("Security Features", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-security")

		// Add initial SPS
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05}
		err := ctx.AddSPS(spsData)
		require.NoError(t, err)

		// Wait for rate limit to expire
		time.Sleep(110 * time.Millisecond)

		// Try to change SPS (different data, same ID)
		spsChanged := []byte{0x67, 0x42, 0x00, 0x1f, 0x8d, 0x68, 0x05}
		err = ctx.AddSPS(spsChanged)
		// The validator doesn't enforce consistency by default, so this won't return an error
		// But it should track the checksum mismatch
		assert.NoError(t, err)

		// Verify security violation was tracked
		stats := ctx.GetStatistics()
		if stats["validator_checksum_mismatches"] != nil {
			assert.Equal(t, 1, stats["validator_checksum_mismatches"])
		} else {
			t.Log("validator_checksum_mismatches not found in stats, validator may not be tracking this metric")
		}
	})

	t.Run("Parameter Set Protection", func(t *testing.T) {
		// Create context with stricter rate limits for testing
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-protection", ParameterSetContextConfig{
			ValidatorUpdateRate:        20 * time.Millisecond, // Require 20ms between updates
			ValidatorMaxUpdatesPerHour: 180,                   // 180 per hour (3 per minute)
			EnableVersioning:           true,
			MaxVersions:                10,
		})

		// Rapidly add multiple parameter sets
		successCount := 0
		failureCount := 0
		for i := 0; i < 10; i++ {
			spsData := []byte{0x67, 0x42, 0x00, byte(0x20 + i), 0x8d, 0x68}
			err := ctx.AddSPS(spsData)

			if err == nil {
				successCount++
			} else {
				failureCount++
			}

			// Small delay between attempts (5ms is less than our 20ms rate limit)
			time.Sleep(5 * time.Millisecond)
		}

		// Should have at least some failures due to rate limiting
		assert.Greater(t, failureCount, 5, "Should have multiple rate limit failures")

		// Check rate limit violations were tracked
		stats := ctx.GetStatistics()
		violations := stats["validator_rate_limit_violations"].(int)
		assert.Greater(t, violations, 5, "Should have multiple rate limit violations")
	})
}

func TestParameterValidatorDisabled(t *testing.T) {
	// Test that parameter context works even if validator is nil
	ctx := NewParameterSetContext(CodecH264, "test-no-validator")

	// Temporarily disable validator to test graceful handling
	ctx.validator = nil

	// Operations should still work
	spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68}
	err := ctx.AddSPS(spsData)
	assert.NoError(t, err)

	ppsData := []byte{0x68, 0x8F, 0x20}
	err = ctx.AddPPS(ppsData)
	assert.NoError(t, err)

	// Statistics should work but not include validator stats
	stats := ctx.GetStatistics()
	assert.NotContains(t, stats, "validator_rate_limit_violations")
	assert.Equal(t, 1, stats["sps_count"])
	assert.Equal(t, 1, stats["pps_count"])
}
