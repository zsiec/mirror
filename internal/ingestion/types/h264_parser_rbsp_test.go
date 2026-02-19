package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestH264ParserWithRBSP(t *testing.T) {
	tests := []struct {
		name        string
		nalUnit     []byte
		description string
		wantErr     bool
		validate    func(t *testing.T, ps *ParameterSet)
	}{
		{
			name: "SPS with emulation prevention bytes",
			// Real SPS with 0x00 0x00 0x03 pattern
			nalUnit: []byte{
				0x67,             // NAL header (SPS)
				0x42, 0x00, 0x1e, // profile_idc, constraint_set, level_idc
				0x8d, // seq_parameter_set_id (UE)
				0x68, 0x05, 0x00, 0x5b, 0xa1,
				0x00, 0x00, 0x03, 0x00, 0x01, // Contains emulation prevention
			},
			description: "SPS with emulation prevention in the middle",
			wantErr:     false,
			validate: func(t *testing.T, ps *ParameterSet) {
				assert.NotNil(t, ps)
				assert.Equal(t, uint8(0x42), *ps.ProfileIDC)
				assert.Equal(t, uint8(0x1e), *ps.LevelIDC)
			},
		},
		{
			name: "PPS with emulation prevention bytes",
			nalUnit: []byte{
				0x68,                   // NAL header (PPS)
				0xce,                   // pic_parameter_set_id and seq_parameter_set_id (UE)
				0x00, 0x00, 0x03, 0x02, // Contains emulation prevention
			},
			description: "PPS with emulation prevention pattern",
			wantErr:     false,
			validate: func(t *testing.T, ps *ParameterSet) {
				// PPS parsing returns PPSContext, not ParameterSet directly
				// This test mainly verifies no panic/error during parsing
			},
		},
		{
			name: "Complex SPS with multiple emulation prevention",
			nalUnit: []byte{
				0x67,             // NAL header
				0x64, 0x00, 0x29, // High profile
				0xac,                   // seq_parameter_set_id
				0x00, 0x00, 0x03, 0x00, // First emulation prevention
				0x00, 0x03, 0x01, // Second emulation prevention
				0x00, 0x00, 0x03, 0x03, // Third emulation prevention (0x03 itself needs escaping)
			},
			description: "SPS with multiple emulation prevention patterns",
			wantErr:     false,
		},
		{
			name: "Slice header with emulation prevention",
			nalUnit: []byte{
				0x41,                   // NAL header (non-IDR slice)
				0x9a,                   // first_mb_in_slice (UE)
				0x00, 0x00, 0x03, 0x01, // slice_type with emulation prevention
				0x00, 0x00, 0x03, 0x00, // pic_parameter_set_id with emulation prevention
			},
			description: "Slice header with emulation prevention in critical fields",
			wantErr:     true, // Expected to fail due to missing PPS reference
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewParameterSetContextForTest(CodecH264, "test-stream")

			// Determine NAL type
			nalType := tt.nalUnit[0] & 0x1f

			var err error
			var result interface{}

			switch nalType {
			case 7: // SPS
				result, err = ctx.parseSPS(tt.nalUnit)
			case 8: // PPS
				result, err = ctx.parsePPS(tt.nalUnit)
			case 1, 5: // Slice
				isIDR := nalType == 5
				result, err = ctx.parseSliceHeader(tt.nalUnit, isIDR)
			default:
				t.Fatalf("Unsupported NAL type: %d", nalType)
			}

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.validate != nil && result != nil {
					if ps, ok := result.(*ParameterSet); ok {
						tt.validate(t, ps)
					}
				}
			}
		})
	}
}

func TestRBSPExtractionInParser(t *testing.T) {
	// Test that RBSP extraction is properly integrated
	ctx := NewParameterSetContextForTest(CodecH264, "test")

	// Create SPS with known emulation prevention pattern
	spsWithEmulation := []byte{
		0x67, // SPS NAL header
		0x42, 0x00, 0x1e, 0x8d, 0x68,
		0x00, 0x00, 0x03, 0x00, // This 0x03 should be removed
		0x00, 0x00, 0x03, 0x01, // This 0x03 should be removed
	}

	ps, err := ctx.parseSPS(spsWithEmulation)
	require.NoError(t, err)
	require.NotNil(t, ps)

	// The parser should have successfully handled the emulation prevention
	assert.Equal(t, uint8(0x42), *ps.ProfileIDC)
	assert.Equal(t, uint8(0x1e), *ps.LevelIDC)
}

func TestParameterSetWithComplexRBSP(t *testing.T) {
	// Test parameter set parsing with complex RBSP patterns
	ctx := NewParameterSetContextForTest(CodecH264, "test-complex")

	// Test that our RBSP extraction works with parameter sets
	// This is a minimal test to verify RBSP is integrated

	// Add an SPS without emulation prevention
	sps := []byte{
		0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1,
	}
	err := ctx.AddSPS(sps)
	require.NoError(t, err)

	// Add a PPS with emulation prevention
	pps := []byte{
		0x68,                   // PPS NAL header
		0xce,                   // pic_parameter_set_id and seq_parameter_set_id
		0x00, 0x00, 0x03, 0x01, // Data with emulation prevention
		0x80, // Additional data
	}

	err = ctx.AddPPS(pps)
	require.NoError(t, err)

	// Verify that both were added successfully
	stats := ctx.GetStatistics()
	assert.Equal(t, 1, stats["sps_count"])
	assert.Equal(t, 1, stats["pps_count"])
}

func BenchmarkRBSPExtraction(b *testing.B) {
	// Benchmark RBSP extraction performance
	nalUnit := make([]byte, 1024)
	nalUnit[0] = 0x67 // SPS NAL header

	// Fill with pattern that requires emulation prevention
	for i := 1; i < len(nalUnit)-3; i += 4 {
		nalUnit[i] = 0x00
		nalUnit[i+1] = 0x00
		nalUnit[i+2] = 0x03
		nalUnit[i+3] = 0x01
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ExtractRBSPFromNALUnit(nalUnit)
	}
}
