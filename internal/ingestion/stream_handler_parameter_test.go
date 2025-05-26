package ingestion

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// TestStreamHandlerParameterCacheInitialization tests that session parameter cache is properly initialized
func TestStreamHandlerParameterCacheInitialization(t *testing.T) {
	// Test the session parameter cache directly without full StreamHandler
	paramContext := types.NewParameterSetContext(types.CodecH264, "test-stream-init")
	require.NotNil(t, paramContext)

	// Verify initial state
	stats := paramContext.GetStatistics()
	assert.Equal(t, "test-stream-init", stats["stream_id"])
	assert.Equal(t, "h264", stats["codec"])
	assert.Equal(t, 0, stats["sps_count"])
	assert.Equal(t, 0, stats["pps_count"])
	assert.Equal(t, 0, stats["total_sets"])
}

// TestParameterSetExtraction tests the parameter set extraction method
func TestParameterSetExtraction(t *testing.T) {
	// Test parameter set extraction using GOP buffer's ExtractParameterSetFromNAL directly
	logrusLogger := logrus.New()
	testLogger := logger.NewLogrusAdapter(logrus.NewEntry(logrusLogger))

	// Create parameter context
	paramContext := types.NewParameterSetContext(types.CodecH264, "test-stream-extract")

	// Create GOP buffer for testing extraction
	gopBufferConfig := gop.BufferConfig{
		MaxGOPs:     10,
		MaxBytes:    50 * 1024 * 1024,
		MaxDuration: 30 * time.Second,
		Codec:       types.CodecH264,
	}
	gopBuffer := gop.NewBuffer("test-stream-extract", gopBufferConfig, testLogger)

	// Create test NAL unit data for SPS (H.264 SPS NAL type 7)
	spsData := []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x67,             // NAL header (type 7 = SPS)
		0x42, 0x00, 0x1F, // profile_idc=66, constraint_flags=0, level_idc=31
		0xE9, // seq_parameter_set_id=7 (encoded as exp-golomb)
		// Additional SPS data would go here
	}

	// Create test NAL unit data for PPS (H.264 PPS NAL type 8)
	ppsData := []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x68,       // NAL header (type 8 = PPS)
		0x8F, 0x20, // Properly encoded PPS data: pps_id=0, sps_id=0, plus additional fields
		// Additional PPS data would go here
	}

	// Test parameter extraction using GOP buffer method
	t.Run("ExtractSPS", func(t *testing.T) {
		// Test SPS extraction
		spsNAL := types.NALUnit{
			Type: 7,
			Data: spsData[4:], // Skip start code
		}

		extracted := gopBuffer.ExtractParameterSetFromNAL(paramContext, spsNAL, 7, 1)
		assert.True(t, extracted, "Should successfully extract SPS")

		// Check if SPS was stored in session cache
		stats := paramContext.GetStatistics()
		assert.Equal(t, 1, stats["total_sets"], "Should have extracted 1 parameter set")
		assert.Equal(t, 1, stats["sps_count"], "Should have 1 SPS")
		assert.Equal(t, 0, stats["pps_count"], "Should have 0 PPS")
	})

	t.Run("ExtractPPS", func(t *testing.T) {
		// Test PPS extraction
		ppsNAL := types.NALUnit{
			Type: 8,
			Data: ppsData[4:], // Skip start code
		}

		extracted := gopBuffer.ExtractParameterSetFromNAL(paramContext, ppsNAL, 8, 1)
		assert.True(t, extracted, "Should successfully extract PPS")

		// Check if PPS was stored in session cache
		stats := paramContext.GetStatistics()
		assert.Equal(t, 2, stats["total_sets"], "Should have extracted 2 parameter sets")
		assert.Equal(t, 1, stats["sps_count"], "Should still have 1 SPS")
		assert.Equal(t, 1, stats["pps_count"], "Should now have 1 PPS")
	})
}

// TestSessionParameterContext tests the parameter context functionality
func TestSessionParameterContext(t *testing.T) {
	// Test parameter context basic functionality
	paramContext := types.NewParameterSetContext(types.CodecH264, "test-session-context")
	require.NotNil(t, paramContext)

	// Test session manager access
	sessionManager := paramContext.GetSessionManager()
	assert.NotNil(t, sessionManager, "Should have session manager")

	// Test statistics
	stats := paramContext.GetStatistics()
	assert.Equal(t, "test-session-context", stats["stream_id"])
	assert.Equal(t, "h264", stats["codec"])
	assert.Equal(t, 0, stats["sps_count"])
	assert.Equal(t, 0, stats["pps_count"])
}
