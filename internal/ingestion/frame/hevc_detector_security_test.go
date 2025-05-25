package frame

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/zsiec/mirror/internal/ingestion/security"
)

// TestHEVCDetectorBufferOverflowProtection tests buffer overflow protection in HEVC NAL parsing
func TestHEVCDetectorBufferOverflowProtection(t *testing.T) {
	detector := NewHEVCDetector()

	tests := []struct {
		name        string
		data        []byte
		expectError bool
		desc        string
	}{
		{
			name:        "empty_data",
			data:        []byte{},
			expectError: false,
			desc:        "Empty data should return empty NAL units",
		},
		{
			name:        "too_small_for_start_code",
			data:        []byte{0x00, 0x00},
			expectError: false,
			desc:        "Data too small for start code should return empty",
		},
		{
			name:        "valid_3byte_start_code",
			data:        []byte{0x00, 0x00, 0x01, 0x40, 0x01, 0x0C}, // VPS NAL
			expectError: false,
			desc:        "Valid 3-byte start code should parse correctly",
		},
		{
			name:        "valid_4byte_start_code",
			data:        []byte{0x00, 0x00, 0x00, 0x01, 0x42, 0x01, 0x01}, // SPS NAL
			expectError: false,
			desc:        "Valid 4-byte start code should parse correctly",
		},
		{
			name: "oversized_nal_unit",
			data: func() []byte {
				// Create data with start code followed by MaxNALUnitSize+1 bytes
				data := make([]byte, security.MaxNALUnitSize+10)
				data[0], data[1], data[2] = 0x00, 0x00, 0x01
				data[3] = 0x26 // IDR NAL
				return data
			}(),
			expectError: true,
			desc:        "NAL unit exceeding max size should return error",
		},
		{
			name: "too_many_nal_units",
			data: func() []byte {
				// Create data with MaxNALUnitsPerFrame+1 NAL units
				var buf bytes.Buffer
				for i := 0; i < security.MaxNALUnitsPerFrame+10; i++ {
					buf.Write([]byte{0x00, 0x00, 0x01, 0x01, 0x02}) // TRAIL_R NAL
				}
				return buf.Bytes()
			}(),
			expectError: true,
			desc:        "Too many NAL units should return error",
		},
		{
			name: "malformed_start_codes_at_boundary",
			data: []byte{
				0x00, 0x00, 0x01, 0x40, 0x01, // First NAL
				0x00, 0x00, // Incomplete start code at end
			},
			expectError: false,
			desc:        "Incomplete start code at boundary should not crash",
		},
		{
			name:        "hevc_irap_nal",
			data:        []byte{0x00, 0x00, 0x01, 0x26, 0x01}, // IDR_W_RADL (type 19)
			expectError: false,
			desc:        "HEVC IRAP NAL should parse correctly",
		},
		{
			name: "hevc_vps_sps_pps_sequence",
			data: []byte{
				0x00, 0x00, 0x01, 0x40, 0x01, // VPS (type 32)
				0x00, 0x00, 0x01, 0x42, 0x01, // SPS (type 33)
				0x00, 0x00, 0x01, 0x44, 0x01, // PPS (type 34)
			},
			expectError: false,
			desc:        "HEVC parameter sets should parse correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nalUnits, err := detector.findNALUnits(tt.data)

			if tt.expectError && err == nil {
				t.Errorf("%s: expected error but got none", tt.desc)
			}
			if !tt.expectError && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.desc, err)
			}

			// Verify no buffer overread occurred (test completes without panic)
			if !tt.expectError && err == nil && nalUnits != nil {
				// Verify all NAL units are within bounds
				for i, nal := range nalUnits {
					if len(nal) > security.MaxNALUnitSize {
						t.Errorf("NAL unit %d exceeds max size: %d > %d",
							i, len(nal), security.MaxNALUnitSize)
					}
				}
			}
		})
	}
}

// TestHEVCDetectorNALTypes tests HEVC-specific NAL type handling
func TestHEVCDetectorNALTypes(t *testing.T) {
	detector := NewHEVCDetector()

	tests := []struct {
		name    string
		nalType uint8
		isVCL   bool
		isIRAP  bool
		desc    string
	}{
		{
			name:    "trail_n",
			nalType: 0,
			isVCL:   true,
			isIRAP:  false,
			desc:    "TRAIL_N is VCL but not IRAP",
		},
		{
			name:    "trail_r",
			nalType: 1,
			isVCL:   true,
			isIRAP:  false,
			desc:    "TRAIL_R is VCL but not IRAP",
		},
		{
			name:    "idr_w_radl",
			nalType: 19,
			isVCL:   true,
			isIRAP:  true,
			desc:    "IDR_W_RADL is both VCL and IRAP",
		},
		{
			name:    "cra_nut",
			nalType: 21,
			isVCL:   true,
			isIRAP:  true,
			desc:    "CRA_NUT is both VCL and IRAP",
		},
		{
			name:    "vps",
			nalType: 32,
			isVCL:   false,
			isIRAP:  false,
			desc:    "VPS is non-VCL",
		},
		{
			name:    "sps",
			nalType: 33,
			isVCL:   false,
			isIRAP:  false,
			desc:    "SPS is non-VCL",
		},
		{
			name:    "boundary_vcl",
			nalType: 31,
			isVCL:   true,
			isIRAP:  false,
			desc:    "NAL type 31 is last VCL type",
		},
		{
			name:    "boundary_non_vcl",
			nalType: 32,
			isVCL:   false,
			isIRAP:  false,
			desc:    "NAL type 32 is first non-VCL type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if detector.isVCLNAL(tt.nalType) != tt.isVCL {
				t.Errorf("%s: isVCLNAL(%d) = %v, want %v",
					tt.desc, tt.nalType, detector.isVCLNAL(tt.nalType), tt.isVCL)
			}
			if detector.isIRAPNAL(tt.nalType) != tt.isIRAP {
				t.Errorf("%s: isIRAPNAL(%d) = %v, want %v",
					tt.desc, tt.nalType, detector.isIRAPNAL(tt.nalType), tt.isIRAP)
			}
		})
	}
}

// TestHEVCDetectorBoundaryConditions tests edge cases that could cause buffer overreads
func TestHEVCDetectorBoundaryConditions(t *testing.T) {
	detector := NewHEVCDetector()

	tests := []struct {
		name string
		data []byte
		desc string
	}{
		{
			name: "hevc_2byte_nal_header",
			data: []byte{0x00, 0x00, 0x01, 0x01, 0x02}, // HEVC has 2-byte NAL header
			desc: "HEVC NAL header is 2 bytes, ensure proper handling",
		},
		{
			name: "mixed_nal_types",
			data: []byte{
				0x00, 0x00, 0x01, 0x40, 0x01, // VPS
				0x00, 0x00, 0x01, 0x26, 0x01, // IDR
				0x00, 0x00, 0x01, 0x02, 0x01, // TRAIL_N
			},
			desc: "Mixed NAL types should parse correctly",
		},
		{
			name: "fu_packet_simulation",
			data: []byte{
				0x00, 0x00, 0x01, 0x62, 0x01, // FU type (49)
			},
			desc: "Fragmentation unit NAL type",
		},
		{
			name: "ap_packet_simulation",
			data: []byte{
				0x00, 0x00, 0x01, 0x60, 0x01, // AP type (48)
			},
			desc: "Aggregation packet NAL type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This should not panic or cause buffer overread
			nalUnits, err := detector.findNALUnits(tt.data)
			if err != nil {
				t.Logf("%s: Got error (might be expected): %v", tt.desc, err)
			}

			// Verify the function completed without panic
			t.Logf("%s: Successfully parsed %d NAL units", tt.desc, len(nalUnits))
		})
	}
}

// TestHEVCDetectorStressTest performs stress testing with random data
func TestHEVCDetectorStressTest(t *testing.T) {
	detector := NewHEVCDetector()

	// Test with various sizes of random-ish data that might contain start codes
	sizes := []int{0, 1, 2, 3, 4, 100, 1024, 10240}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			data := make([]byte, size)
			// Fill with pattern that might create accidental start codes
			for i := range data {
				data[i] = byte(i % 4) // Creates 0x00, 0x01, 0x02, 0x03 pattern
			}

			// Should not panic regardless of input
			_, err := detector.findNALUnits(data)
			if err != nil {
				// Error is acceptable for malformed data
				t.Logf("Size %d returned error (expected): %v", size, err)
			}
		})
	}
}

// BenchmarkHEVCDetectorSecurity benchmarks the performance impact of security checks
func BenchmarkHEVCDetectorSecurity(b *testing.B) {
	detector := NewHEVCDetector()

	// Create realistic HEVC data with multiple NAL units
	data := make([]byte, 0, 10000)
	for i := 0; i < 50; i++ {
		// Add start code
		data = append(data, 0x00, 0x00, 0x01)
		// Add NAL header (2 bytes for HEVC)
		data = append(data, 0x01, 0x02) // TRAIL_R
		// Add some payload
		payload := make([]byte, 100)
		data = append(data, payload...)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		nalUnits, err := detector.findNALUnits(data)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		if len(nalUnits) == 0 {
			b.Fatal("Expected NAL units")
		}
	}
}
