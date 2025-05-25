package frame

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/zsiec/mirror/internal/ingestion/security"
)

// TestH264DetectorBufferOverflowProtection tests buffer overflow protection in NAL parsing
func TestH264DetectorBufferOverflowProtection(t *testing.T) {
	detector := NewH264Detector()

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
			data:        []byte{0x00, 0x00, 0x01, 0x41, 0x42, 0x43},
			expectError: false,
			desc:        "Valid 3-byte start code should parse correctly",
		},
		{
			name:        "valid_4byte_start_code",
			data:        []byte{0x00, 0x00, 0x00, 0x01, 0x41, 0x42, 0x43},
			expectError: false,
			desc:        "Valid 4-byte start code should parse correctly",
		},
		{
			name: "oversized_nal_unit",
			data: func() []byte {
				// Create data with start code followed by MaxNALUnitSize+1 bytes
				data := make([]byte, security.MaxNALUnitSize+10)
				data[0], data[1], data[2] = 0x00, 0x00, 0x01
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
					buf.Write([]byte{0x00, 0x00, 0x01, 0x41, 0x42})
				}
				return buf.Bytes()
			}(),
			expectError: true,
			desc:        "Too many NAL units should return error",
		},
		{
			name: "malformed_start_codes_at_boundary",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, // First NAL
				0x00, 0x00, // Incomplete start code at end
			},
			expectError: false,
			desc:        "Incomplete start code at boundary should not crash",
		},
		{
			name:        "start_code_at_exact_boundary",
			data:        []byte{0x00, 0x00, 0x00}, // Could be mistaken for 4-byte start code
			expectError: false,
			desc:        "Start code pattern at exact boundary should not crash",
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

// TestH264DetectorBoundaryConditions tests edge cases that could cause buffer overreads
func TestH264DetectorBoundaryConditions(t *testing.T) {
	detector := NewH264Detector()

	tests := []struct {
		name string
		data []byte
		desc string
	}{
		{
			name: "zeros_pattern_not_start_code",
			data: []byte{0x00, 0x00, 0x02, 0x00, 0x00, 0x03},
			desc: "Pattern similar to start code but not valid",
		},
		{
			name: "multiple_consecutive_start_codes",
			data: []byte{
				0x00, 0x00, 0x01, // Start code 1
				0x00, 0x00, 0x01, // Start code 2 immediately after
				0x41, 0x42, // NAL data
			},
			desc: "Consecutive start codes should be handled",
		},
		{
			name: "start_code_variants_mixed",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, // 3-byte start code
				0x00, 0x00, 0x00, 0x01, 0x42, // 4-byte start code
				0x00, 0x00, 0x01, 0x43, // 3-byte start code
			},
			desc: "Mixed start code types should parse correctly",
		},
		{
			name: "near_boundary_access",
			data: []byte{
				0x00, 0x00, 0x01, 0x41, 0x42, 0x43,
				0x00, 0x00, // Ends with potential start code prefix
			},
			desc: "Data ending with partial start code pattern",
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

// TestH264DetectorStressTest performs stress testing with random data
func TestH264DetectorStressTest(t *testing.T) {
	detector := NewH264Detector()

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

// BenchmarkH264DetectorSecurity benchmarks the performance impact of security checks
func BenchmarkH264DetectorSecurity(b *testing.B) {
	detector := NewH264Detector()

	// Create realistic H.264 data with multiple NAL units
	data := make([]byte, 0, 10000)
	for i := 0; i < 50; i++ {
		// Add start code
		data = append(data, 0x00, 0x00, 0x01)
		// Add NAL header and some data
		data = append(data, 0x41)
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
