package frame

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/zsiec/mirror/internal/ingestion/security"
)

// TestAV1DetectorBufferOverflowProtection tests buffer overflow protection in AV1 OBU parsing
func TestAV1DetectorBufferOverflowProtection(t *testing.T) {
	detector := NewAV1Detector()

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
			desc:        "Empty data should return empty OBUs",
		},
		{
			name:        "valid_temporal_delimiter",
			data:        []byte{0x12, 0x00}, // TD OBU with size
			expectError: false,
			desc:        "Valid temporal delimiter OBU",
		},
		{
			name:        "forbidden_bit_set",
			data:        []byte{0x80}, // Forbidden bit is set
			expectError: true,
			desc:        "OBU with forbidden bit should fail",
		},
		{
			name: "oversized_obu",
			data: func() []byte {
				// OBU header with size flag
				data := []byte{0x0A} // Type=1 (seq header), has_size=1
				// Add LEB128 size that exceeds limit
				sizeBytes := security.WriteLEB128(uint64(security.MaxNALUnitSize + 1))
				data = append(data, sizeBytes...)
				return data
			}(),
			expectError: true,
			desc:        "OBU exceeding max size should fail",
		},
		{
			name: "too_many_obus",
			data: func() []byte {
				// Create MaxOBUsPerFrame+1 small OBUs
				var buf bytes.Buffer
				for i := 0; i < security.MaxOBUsPerFrame+10; i++ {
					buf.WriteByte(0x12) // TD with size
					buf.WriteByte(0x00) // Size = 0
				}
				return buf.Bytes()
			}(),
			expectError: true,
			desc:        "Too many OBUs should fail",
		},
		{
			name: "malformed_leb128_size",
			data: []byte{
				0x0A, // Has size flag
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // Invalid LEB128
			},
			expectError: true,
			desc:        "Malformed LEB128 size should fail",
		},
		{
			name: "truncated_obu_header",
			data: []byte{0x0E}, // Has extension flag but no extension byte
			expectError: false,  // Should handle gracefully by returning partial results
			desc:        "Truncated OBU should be handled gracefully",
		},
		{
			name: "obu_size_exceeds_data",
			data: []byte{
				0x0A, // Has size
				0x10, // Size = 16
				0x00, // Only 1 byte of data but size says 16
			},
			expectError: false, // Should handle gracefully
			desc:        "OBU size exceeding available data",
		},
		{
			name: "valid_sequence_header",
			data: []byte{
				0x0A, // Type=1 (seq header), has_size=1
				0x05, // Size = 5
				0x00, 0x00, 0x00, 0x00, 0x00, // 5 bytes of data
			},
			expectError: false,
			desc:        "Valid sequence header OBU",
		},
		{
			name: "obu_without_size",
			data: []byte{
				0x08, // Type=1, no size flag
				0x00, 0x00, 0x00, // Data extends to end
			},
			expectError: false,
			desc:        "OBU without size field should use remaining data",
		},
		{
			name: "padding_obu_stops_parsing",
			data: []byte{
				0x12, 0x00, // TD OBU
				0x78, 0x00, // Padding OBU
				0x12, 0x00, // Another TD (should not be parsed)
			},
			expectError: false,
			desc:        "Padding OBU should stop parsing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obus, err := detector.parseOBUs(tt.data)
			
			if tt.expectError && err == nil {
				t.Errorf("%s: expected error but got none", tt.desc)
			}
			if !tt.expectError && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.desc, err)
			}
			
			// Verify no buffer overread occurred (test completes without panic)
			if !tt.expectError && err == nil && obus != nil {
				// Verify all OBUs are within bounds
				totalSize := 0
				for i, obu := range obus {
					if obu.Size > security.MaxNALUnitSize {
						t.Errorf("OBU %d exceeds max size: %d > %d", 
							i, obu.Size, security.MaxNALUnitSize)
					}
					totalSize += obu.Size
				}
			}
		})
	}
}

// TestAV1DetectorLEB128Integration tests LEB128 parsing integration
func TestAV1DetectorLEB128Integration(t *testing.T) {
	detector := NewAV1Detector()

	tests := []struct {
		name     string
		size     uint64
		wantErr  bool
		desc     string
	}{
		{
			name:    "small_size",
			size:    100,
			wantErr: false,
			desc:    "Small LEB128 size",
		},
		{
			name:    "medium_size",
			size:    65536,
			wantErr: false,
			desc:    "Medium LEB128 size",
		},
		{
			name:    "large_valid_size",
			size:    uint64(security.MaxNALUnitSize - 100),
			wantErr: false,
			desc:    "Large but valid size",
		},
		{
			name:    "oversized",
			size:    uint64(security.MaxNALUnitSize + 1),
			wantErr: true,
			desc:    "Size exceeding limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create OBU with LEB128 size
			data := []byte{0x0A} // Has size flag
			sizeBytes := security.WriteLEB128(tt.size)
			data = append(data, sizeBytes...)
			
			// Add dummy data (just a few bytes, actual parsing will check bounds)
			data = append(data, 0x00, 0x00, 0x00)
			
			obus, err := detector.parseOBUs(data)
			
			if tt.wantErr && err == nil {
				t.Errorf("%s: expected error but got none", tt.desc)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.desc, err)
			}
			
			// For valid cases, check the parsed size
			if !tt.wantErr && len(obus) > 0 {
				// The actual OBU data will be limited by available bytes
				// but the size field should be parsed correctly
				t.Logf("%s: Parsed %d OBUs", tt.desc, len(obus))
			}
		})
	}
}

// TestAV1DetectorOBUTypes tests various OBU type handling
func TestAV1DetectorOBUTypes(t *testing.T) {
	detector := NewAV1Detector()

	tests := []struct {
		name       string
		obuType    uint8
		hasSize    bool
		hasExt     bool
		dataSize   int
		desc       string
	}{
		{
			name:     "sequence_header",
			obuType:  1,
			hasSize:  true,
			dataSize: 10,
			desc:     "Sequence header OBU",
		},
		{
			name:     "temporal_delimiter",
			obuType:  2,
			hasSize:  true,
			dataSize: 0,
			desc:     "Temporal delimiter OBU",
		},
		{
			name:     "frame_header",
			obuType:  3,
			hasSize:  true,
			dataSize: 20,
			desc:     "Frame header OBU",
		},
		{
			name:     "padding",
			obuType:  15,
			hasSize:  true,
			dataSize: 100,
			desc:     "Padding OBU",
		},
		{
			name:     "with_extension",
			obuType:  1,
			hasSize:  true,
			hasExt:   true,
			dataSize: 10,
			desc:     "OBU with extension header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Construct OBU header
			header := tt.obuType << 3
			if tt.hasExt {
				header |= 0x04
			}
			if tt.hasSize {
				header |= 0x02
			}
			
			data := []byte{header}
			
			// Add extension if present
			if tt.hasExt {
				data = append(data, 0x00) // Extension byte
			}
			
			// Add size if present
			if tt.hasSize {
				data = append(data, byte(tt.dataSize))
			}
			
			// Add data
			for i := 0; i < tt.dataSize; i++ {
				data = append(data, byte(i))
			}
			
			obus, err := detector.parseOBUs(data)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tt.desc, err)
			}
			
			if len(obus) != 1 {
				t.Errorf("%s: expected 1 OBU, got %d", tt.desc, len(obus))
				return
			}
			
			obu := obus[0]
			if obu.Type != tt.obuType {
				t.Errorf("%s: OBU type = %d, want %d", tt.desc, obu.Type, tt.obuType)
			}
			if obu.HasSize != tt.hasSize {
				t.Errorf("%s: HasSize = %v, want %v", tt.desc, obu.HasSize, tt.hasSize)
			}
			if len(obu.Data) != tt.dataSize {
				t.Errorf("%s: Data length = %d, want %d", tt.desc, len(obu.Data), tt.dataSize)
			}
		})
	}
}

// TestAV1DetectorStressTest performs stress testing with random data
func TestAV1DetectorStressTest(t *testing.T) {
	detector := NewAV1Detector()
	
	// Test with various sizes of random-ish data
	sizes := []int{0, 1, 2, 3, 4, 100, 1024, 10240}
	
	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			data := make([]byte, size)
			// Fill with pattern that might create valid OBU headers
			for i := range data {
				// Avoid forbidden bit, create various OBU patterns
				data[i] = byte((i * 7) & 0x7F)
			}
			
			// Should not panic regardless of input
			obus, err := detector.parseOBUs(data)
			if err != nil {
				// Error is acceptable for malformed data
				t.Logf("Size %d returned error (expected): %v", size, err)
			} else {
				t.Logf("Size %d parsed %d OBUs", size, len(obus))
			}
		})
	}
}

// BenchmarkAV1DetectorSecurity benchmarks the performance impact of security checks
func BenchmarkAV1DetectorSecurity(b *testing.B) {
	detector := NewAV1Detector()
	
	// Create realistic AV1 data with multiple OBUs
	data := make([]byte, 0, 10000)
	for i := 0; i < 50; i++ {
		// Add temporal delimiter
		data = append(data, 0x12, 0x00)
		// Add frame OBU
		data = append(data, 0x32) // Frame OBU with size
		data = append(data, 100)  // Size = 100
		// Add some payload
		payload := make([]byte, 100)
		data = append(data, payload...)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		obus, err := detector.parseOBUs(data)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		if len(obus) == 0 {
			b.Fatal("Expected OBUs")
		}
	}
}
