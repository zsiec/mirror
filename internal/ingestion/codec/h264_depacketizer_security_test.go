package codec

import (
	"testing"

	"github.com/pion/rtp"

	"github.com/zsiec/mirror/internal/ingestion/security"
)

// TestSTAPAVulnerability tests the STAP-A buffer overflow vulnerability fix
func TestSTAPAVulnerability(t *testing.T) {
	depacketizer := NewH264Depacketizer()

	tests := []struct {
		name        string
		payload     []byte
		expectError bool
		desc        string
	}{
		{
			name: "valid_stap_a",
			payload: func() []byte {
				// Valid STAP-A packet
				data := []byte{nalTypeSTAPA} // STAP-A header
				// First NAL unit: size=3, data={0x41, 0x42, 0x43}
				data = append(data, 0x00, 0x03) // Size (network order)
				data = append(data, 0x41, 0x42, 0x43)
				// Second NAL unit: size=2, data={0x44, 0x45}
				data = append(data, 0x00, 0x02) // Size
				data = append(data, 0x44, 0x45)
				return data
			}(),
			expectError: false,
			desc:        "Valid STAP-A should parse successfully",
		},
		{
			name: "stap_a_size_overflow",
			payload: func() []byte {
				// STAP-A with NAL size that exceeds payload
				data := []byte{nalTypeSTAPA}
				// NAL size = 1000, but only 2 bytes follow
				data = append(data, 0x03, 0xE8) // Size = 1000
				data = append(data, 0x41, 0x42) // Only 2 bytes
				return data
			}(),
			expectError: true,
			desc:        "STAP-A with size exceeding payload should fail",
		},
		{
			name: "stap_a_truncated_size_field",
			payload: func() []byte {
				// STAP-A truncated in size field
				data := []byte{nalTypeSTAPA}
				data = append(data, 0x00) // Only 1 byte of size field
				return data
			}(),
			expectError: true,
			desc:        "STAP-A truncated in size field should fail",
		},
		{
			name: "stap_a_zero_size",
			payload: func() []byte {
				// STAP-A with zero-size NAL unit
				data := []byte{nalTypeSTAPA}
				data = append(data, 0x00, 0x00) // Size = 0
				data = append(data, 0x00, 0x02) // Next NAL: size=2
				data = append(data, 0x41, 0x42)
				return data
			}(),
			expectError: false,
			desc:        "STAP-A with zero-size NAL should skip it",
		},
		{
			name: "stap_a_oversized_nal",
			payload: func() []byte {
				// STAP-A with NAL exceeding max size
				data := []byte{nalTypeSTAPA}
				// Create size that would exceed max (use max uint16)
				data = append(data, 0xFF, 0xFF) // Size = 65535
				// Don't need actual data, should fail on size check
				return data
			}(),
			expectError: true,
			desc:        "STAP-A with oversized NAL should fail",
		},
		{
			name: "stap_a_too_many_nals",
			payload: func() []byte {
				// STAP-A with too many NAL units
				data := []byte{nalTypeSTAPA}
				// Add 101 small NAL units (limit is 100)
				for i := 0; i < 101; i++ {
					data = append(data, 0x00, 0x01) // Size = 1
					data = append(data, 0x41)       // Data
				}
				return data
			}(),
			expectError: true,
			desc:        "STAP-A with too many NALs should fail",
		},
		{
			name: "stap_a_exact_boundary",
			payload: func() []byte {
				// STAP-A where NAL ends exactly at payload boundary
				data := []byte{nalTypeSTAPA}
				data = append(data, 0x00, 0x03) // Size = 3
				data = append(data, 0x41, 0x42, 0x43)
				return data
			}(),
			expectError: false,
			desc:        "STAP-A with exact boundary should succeed",
		},
		{
			name: "stap_a_multiple_valid_nals",
			payload: func() []byte {
				// STAP-A with multiple valid NAL units
				data := []byte{nalTypeSTAPA}
				for i := 0; i < 5; i++ {
					data = append(data, 0x00, 0x04) // Size = 4
					data = append(data, 0x41, 0x42, 0x43, 0x44)
				}
				return data
			}(),
			expectError: false,
			desc:        "STAP-A with multiple valid NALs should succeed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: 1,
				},
				Payload: tt.payload,
			}

			nalUnits, err := depacketizer.Depacketize(pkt)

			if tt.expectError && err == nil {
				t.Errorf("%s: expected error but got none", tt.desc)
			}
			if !tt.expectError && err != nil {
				t.Errorf("%s: unexpected error: %v", tt.desc, err)
			}

			// If successful, verify NAL units have start codes
			if err == nil && nalUnits != nil {
				for i, nal := range nalUnits {
					if len(nal) < 4 {
						t.Errorf("NAL unit %d too short: %d bytes", i, len(nal))
						continue
					}
					// Check for start code
					if nal[0] != 0x00 || nal[1] != 0x00 || nal[2] != 0x00 || nal[3] != 0x01 {
						t.Errorf("NAL unit %d missing start code: %x %x %x %x",
							i, nal[0], nal[1], nal[2], nal[3])
					}
				}
			}
		})
	}
}

// TestFUAFragmentLimits tests FU-A fragment accumulation limits
func TestFUAFragmentLimits(t *testing.T) {
	depacketizer := NewH264Depacketizer()

	tests := []struct {
		name      string
		packets   [][]byte
		expectNAL bool
		desc      string
	}{
		{
			name: "valid_fua_sequence",
			packets: [][]byte{
				// Start fragment
				{nalTypeFUA, 0x81, 0x41, 0x42, 0x43}, // Start=1, End=0, Type=1
				// Middle fragment
				{nalTypeFUA, 0x01, 0x44, 0x45, 0x46}, // Start=0, End=0
				// End fragment
				{nalTypeFUA, 0x41, 0x47, 0x48}, // Start=0, End=1
			},
			expectNAL: true,
			desc:      "Valid FU-A sequence should produce NAL",
		},
		{
			name: "fua_oversized_fragment",
			packets: func() [][]byte {
				// Create oversized fragment
				oversized := make([]byte, security.MaxFragmentSize+100)
				oversized[0] = nalTypeFUA
				oversized[1] = 0x81 // Start bit
				return [][]byte{oversized}
			}(),
			expectNAL: false,
			desc:      "Oversized FU-A fragment should be rejected",
		},
		{
			name: "fua_too_many_fragments",
			packets: func() [][]byte {
				packets := make([][]byte, 0)
				// Start fragment
				packets = append(packets, []byte{nalTypeFUA, 0x81, 0x41})
				// Add 1001 middle fragments (exceeds limit)
				for i := 0; i < 1001; i++ {
					packets = append(packets, []byte{nalTypeFUA, 0x01, 0x42})
				}
				return packets
			}(),
			expectNAL: false,
			desc:      "Too many FU-A fragments should reset",
		},
		{
			name: "fua_total_size_exceeded",
			packets: func() [][]byte {
				packets := make([][]byte, 0)
				// Start fragment
				packets = append(packets, []byte{nalTypeFUA, 0x81, 0x41})
				// Add fragments that will exceed MaxNALUnitSize
				fragmentSize := 1024 * 1024 // 1MB per fragment
				numFragments := (security.MaxNALUnitSize / fragmentSize) + 2
				for i := 0; i < numFragments; i++ {
					data := make([]byte, fragmentSize+2)
					data[0] = nalTypeFUA
					data[1] = 0x01 // Middle fragment
					packets = append(packets, data)
				}
				return packets
			}(),
			expectNAL: false,
			desc:      "FU-A total size exceeding limit should reset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var finalNAL []byte
			var lastSeq uint16 = 0

			for i, payload := range tt.packets {
				pkt := &rtp.Packet{
					Header: rtp.Header{
						SequenceNumber: lastSeq + 1,
					},
					Payload: payload,
				}
				lastSeq = pkt.Header.SequenceNumber

				nalUnits, err := depacketizer.Depacketize(pkt)
				if err != nil {
					t.Logf("Packet %d returned error: %v", i, err)
				}

				if len(nalUnits) > 0 {
					finalNAL = nalUnits[0]
				}
			}

			if tt.expectNAL && finalNAL == nil {
				t.Errorf("%s: expected NAL unit but got none", tt.desc)
			}
			if !tt.expectNAL && finalNAL != nil {
				t.Errorf("%s: expected no NAL but got one with %d bytes", tt.desc, len(finalNAL))
			}
		})
	}
}

// TestDepacketizerEdgeCases tests various edge cases
func TestDepacketizerEdgeCases(t *testing.T) {
	depacketizer := NewH264Depacketizer()

	tests := []struct {
		name    string
		payload []byte
		desc    string
	}{
		{
			name:    "empty_payload",
			payload: []byte{},
			desc:    "Empty payload should return error",
		},
		{
			name:    "single_byte_payload",
			payload: []byte{0x41},
			desc:    "Single byte should be valid single NAL",
		},
		{
			name:    "reserved_nal_type",
			payload: []byte{0x00}, // Type 0 is reserved
			desc:    "Reserved NAL type should return error",
		},
		{
			name:    "fua_missing_header",
			payload: []byte{nalTypeFUA}, // FU-A without FU header
			desc:    "FU-A without header should fail",
		},
		{
			name:    "stap_a_empty",
			payload: []byte{nalTypeSTAPA}, // STAP-A without any NALs
			desc:    "Empty STAP-A should succeed with no NALs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkt := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: 1,
				},
				Payload: tt.payload,
			}

			nalUnits, err := depacketizer.Depacketize(pkt)
			t.Logf("%s: err=%v, nalUnits=%d", tt.desc, err, len(nalUnits))

			// Just ensure it doesn't panic
			_ = nalUnits
		})
	}
}

// BenchmarkSTAPADepacketization benchmarks STAP-A with security checks
func BenchmarkSTAPADepacketization(b *testing.B) {
	depacketizer := NewH264Depacketizer()

	// Create a realistic STAP-A packet with multiple NALs
	payload := []byte{nalTypeSTAPA}
	for i := 0; i < 10; i++ {
		nalData := make([]byte, 100)
		for j := range nalData {
			nalData[j] = byte(j)
		}
		size := uint16(len(nalData))
		payload = append(payload, byte(size>>8), byte(size))
		payload = append(payload, nalData...)
	}

	pkt := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1,
		},
		Payload: payload,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		nalUnits, err := depacketizer.Depacketize(pkt)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		if len(nalUnits) != 10 {
			b.Fatalf("Expected 10 NAL units, got %d", len(nalUnits))
		}
	}
}
