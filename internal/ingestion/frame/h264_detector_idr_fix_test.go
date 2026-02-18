package frame

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestH264Detector_GetFrameType_IDRValidation(t *testing.T) {
	detector := NewH264Detector()

	tests := []struct {
		name         string
		nalUnits     []types.NALUnit
		expectedType types.FrameType
		description  string
	}{
		{
			name: "Valid IDR with I-slice",
			nalUnits: []types.NALUnit{
				{
					Type: 5, // IDR
					Data: []byte{
						0x65, // NAL header: IDR slice
						0x88, // first_mb_in_slice = 0, slice_type = 7
						0x04, // continuation (bits: 1 0001000 00000100)
					},
				},
			},
			expectedType: types.FrameTypeIDR,
			description:  "IDR NAL with valid I-slice should be FrameTypeIDR",
		},
		{
			name: "Invalid IDR with P-slice",
			nalUnits: []types.NALUnit{
				{
					Type: 5, // IDR
					Data: []byte{
						0x45, // NAL header: IDR slice (NRI=2)
						0xC0, // first_mb_in_slice = 0, slice_type = 0 (bits: 11...)
						// Exp-golomb: '1' = 0, '1' = 0
					},
				},
			},
			expectedType: types.FrameTypeP, // Should NOT be IDR, and should reflect actual slice type
			description:  "IDR NAL with P-slice should be treated as P-frame (invalid but accurate)",
		},
		{
			name: "Valid non-IDR I-slice",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{
						0x41, // NAL header: non-IDR slice
						0xB0, // first_mb_in_slice = 0, slice_type = 2
						// Exp-golomb: '1' = 0, '011' = 2
					},
				},
			},
			expectedType: types.FrameTypeI,
			description:  "Non-IDR NAL with I-slice should be FrameTypeI",
		},
		{
			name: "Regular P-slice",
			nalUnits: []types.NALUnit{
				{
					Type: 1, // Non-IDR slice
					Data: []byte{
						0x41, // NAL header: non-IDR slice
						0xC0, // first_mb_in_slice = 0, slice_type = 0
						// Exp-golomb: '1' = 0, '1' = 0
					},
				},
			},
			expectedType: types.FrameTypeP,
			description:  "Non-IDR NAL with P-slice should be FrameTypeP",
		},
		{
			name: "IDR with corrupted slice header",
			nalUnits: []types.NALUnit{
				{
					Type: 5,            // IDR
					Data: []byte{0x65}, // Only NAL header, no slice data
				},
			},
			expectedType: types.FrameTypeI,
			description:  "IDR NAL with corrupted data should fallback to FrameTypeI",
		},
		{
			name: "Multiple NALs with invalid IDR",
			nalUnits: []types.NALUnit{
				{
					Type: 8, // PPS
					Data: []byte{0x68, 0x01, 0x02, 0x03},
				},
				{
					Type: 5, // IDR with P-slice (invalid!)
					Data: []byte{
						0x45, // NAL header: IDR
						0xC0, // first_mb_in_slice = 0, slice_type = 0 (P-slice)
					},
				},
			},
			expectedType: types.FrameTypeP,
			description:  "VCL NAL content takes priority over non-VCL PPS classification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frameType := detector.GetFrameType(tt.nalUnits)
			assert.Equal(t, tt.expectedType, frameType, tt.description)
		})
	}
}

// TestH264Detector_parseSliceTypeFromNAL tests the slice type parsing
func TestH264Detector_parseSliceTypeFromNAL(t *testing.T) {
	detector := NewH264Detector()

	tests := []struct {
		name              string
		nalData           []byte
		expectedSliceType int
	}{
		{
			name: "P-slice (type 0)",
			nalData: []byte{
				0x41, // NAL header
				0xC0, // first_mb_in_slice = 0, slice_type = 0
			},
			expectedSliceType: 0,
		},
		{
			name: "I-slice (type 2)",
			nalData: []byte{
				0x65, // NAL header (IDR)
				0xB0, // first_mb_in_slice = 0, slice_type = 2
				// Exp-golomb: '1' = 0, '011' = 2
			},
			expectedSliceType: 2,
		},
		{
			name: "I-slice all (type 7)",
			nalData: []byte{
				0x65,       // NAL header (IDR)
				0x88,       // first_mb_in_slice = 0
				0xE0, 0x40, // slice_type = 7 (multi-byte golomb)
			},
			expectedSliceType: 7,
		},
		{
			name: "B-slice (type 1)",
			nalData: []byte{
				0x01, // NAL header
				0xA0, // first_mb_in_slice = 0, slice_type = 1
				// Exp-golomb: '1' = 0, '01' = 1
			},
			expectedSliceType: 1,
		},
		{
			name:              "Too short NAL",
			nalData:           []byte{0x41},
			expectedSliceType: -1,
		},
		{
			name:              "Empty payload",
			nalData:           []byte{0x41, 0x00}, // NAL header but empty RBSP
			expectedSliceType: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sliceType := detector.parseSliceTypeFromNAL(tt.nalData)
			assert.Equal(t, tt.expectedSliceType, sliceType)
		})
	}
}

// TestH264Detector_CorruptedIDRHandling specifically tests the scenario from the bug
func TestH264Detector_CorruptedIDRHandling(t *testing.T) {
	detector := NewH264Detector()

	// This is the exact scenario that was causing FFmpeg to fail
	nalUnits := []types.NALUnit{
		{
			Type: 5, // IDR NAL type
			Data: []byte{
				0x45,                   // NAL header (forbidden=0, NRI=2, type=5)
				0xC0,                   // first_mb_in_slice = 0, slice_type = 0 (P-slice) - INVALID for IDR!
				0x00, 0x00, 0x00, 0x00, // Rest of slice header
			},
		},
	}

	frameType := detector.GetFrameType(nalUnits)

	// The fix should prevent this from being marked as IDR
	assert.NotEqual(t, types.FrameTypeIDR, frameType,
		"Frame with IDR NAL containing P-slice should NOT be marked as FrameTypeIDR")

	// It should be treated as a regular I-frame or P-frame
	assert.True(t, frameType == types.FrameTypeI || frameType == types.FrameTypeP,
		"Invalid IDR should be treated as regular frame type")
}
