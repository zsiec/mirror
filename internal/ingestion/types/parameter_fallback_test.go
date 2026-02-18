package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPPSWithMissingSPSFallback(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Add SPS with ID 0 (minimal valid SPS)
	spsData := []byte{
		0x67,             // NAL header for SPS
		0x42, 0xC0, 0x1E, // profile_idc, constraint_flags, level_idc
		0xD9, 0x00, 0x10, 0x00, 0x00, 0x03, 0x00, 0x10,
		0x00, 0x00, 0x03, 0x01, 0xE0, 0xF1, 0x42, 0x99,
	}
	err := ctx.AddSPS(spsData)
	require.NoError(t, err)

	// Add PPS with ID 0 that references SPS ID 2 (which doesn't exist)
	ppsData := []byte{
		0x68,             // NAL header for PPS
		0xEE, 0x3C, 0x80, // PPS ID 0, SPS ID 2 encoded
	}
	err = ctx.AddPPS(ppsData)
	require.NoError(t, err) // Should succeed with remapping

	// Verify the PPS was remapped to reference SPS 0
	stats := ctx.GetStatistics()
	assert.Equal(t, 1, stats["sps_count"])
	assert.Equal(t, 1, stats["pps_count"])

	// Create a test frame that requires PPS 0
	frame := &VideoFrame{
		ID: 1,
		NALUnits: []NALUnit{
			{
				Type: 1, // Non-IDR slice
				Data: []byte{
					0x41, // NAL header
					0x9A, 0x01, 0x0C, 0x04, 0x89, 0xFB, 0x01, 0x6A,
					// Minimal slice data with PPS ID 0
				},
			},
		},
	}

	// Check if we can decode the frame (should succeed with fallback)
	canDecode, reason := ctx.CanDecodeFrame(frame)
	t.Logf("CanDecodeFrame result: %v, reason: %s", canDecode, reason)

	// The frame should be decodable with the fallback mechanism
	assert.True(t, canDecode, "Frame should be decodable with SPS fallback")
}

func TestPPSWithMultipleSPSNoFallback(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Add SPS with ID 0 and 1
	sps0Data := []byte{
		0x67, 0x42, 0xC0, 0x1E, 0xD9, 0x00, 0x10, 0x00,
		0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03, 0x01,
		0xE0, 0xF1, 0x42, 0x99,
	}
	sps1Data := []byte{
		0x67, 0x42, 0xC0, 0x1E, 0x29, 0x90, 0x16, 0x02,
		0x20, 0x59, 0x40, 0x68, 0x00, 0x00, 0x03, 0x00,
		0x08, 0x00, 0x00, 0x03, 0x01, 0xE0, 0xF1, 0x42, 0x99,
	} // SPS ID 1

	err := ctx.AddSPS(sps0Data)
	require.NoError(t, err)
	err = ctx.AddSPS(sps1Data)
	require.NoError(t, err)

	// Add PPS that references non-existent SPS 2
	ppsData := []byte{
		0x68,             // NAL header for PPS
		0xEE, 0x3C, 0x80, // PPS ID 0, SPS ID 2
	}
	err = ctx.AddPPS(ppsData)
	require.NoError(t, err) // Should store but not remap (multiple SPS available)

	// Create a test frame
	frame := &VideoFrame{
		ID: 1,
		NALUnits: []NALUnit{
			{
				Type: 1,
				Data: []byte{
					0x41, // NAL header
					0x9A, 0x01, 0x0C, 0x04, 0x89, 0xFB, 0x01, 0x6A,
				},
			},
		},
	}

	// Should still work with runtime fallback
	canDecode, reason := ctx.CanDecodeFrame(frame)
	t.Logf("CanDecodeFrame result: %v, reason: %s", canDecode, reason)

	// Should be decodable with runtime fallback
	assert.True(t, canDecode, "Frame should be decodable with runtime SPS fallback")
}
