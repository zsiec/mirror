package codec

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/pion/rtp"
)

func TestH264Depacketizer_SingleNALUnit(t *testing.T) {
	tests := []struct {
		name      string
		payload   []byte
		wantNALs  int
		wantError bool
	}{
		{
			name:      "single NAL unit - type 1 (non-IDR slice)",
			payload:   []byte{0x41, 0x9a, 0x00, 0x01, 0x02, 0x03}, // NAL type 1
			wantNALs:  1,
			wantError: false,
		},
		{
			name:      "single NAL unit - type 5 (IDR slice)",
			payload:   []byte{0x65, 0x88, 0x80, 0x00, 0x01, 0x02}, // NAL type 5
			wantNALs:  1,
			wantError: false,
		},
		{
			name:      "single NAL unit - type 7 (SPS)",
			payload:   []byte{0x67, 0x42, 0x00, 0x1e, 0xab, 0x40}, // NAL type 7
			wantNALs:  1,
			wantError: false,
		},
		{
			name:      "single NAL unit - type 8 (PPS)",
			payload:   []byte{0x68, 0xce, 0x3c, 0x80}, // NAL type 8
			wantNALs:  1,
			wantError: false,
		},
		{
			name:      "empty payload",
			payload:   []byte{},
			wantNALs:  0,
			wantError: true,
		},
		{
			name:      "reserved NAL type 0",
			payload:   []byte{0x00, 0x00, 0x00},
			wantNALs:  0,
			wantError: true,
		},
		{
			name:      "reserved NAL type 30",
			payload:   []byte{0x7e, 0x00, 0x00},
			wantNALs:  0,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewH264Depacketizer()
			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: 1000,
				},
				Payload: tt.payload,
			}
			nalUnits, err := d.Depacketize(packet)

			if (err != nil) != tt.wantError {
				t.Errorf("Depacketize() error = %v, wantError %v", err, tt.wantError)
				return
			}

			if len(nalUnits) != tt.wantNALs {
				t.Errorf("Depacketize() returned %d NAL units, want %d", len(nalUnits), tt.wantNALs)
				return
			}

			// Verify start code is prepended for single NAL units
			if !tt.wantError && tt.wantNALs > 0 {
				startCode := []byte{0x00, 0x00, 0x00, 0x01}
				if !bytes.HasPrefix(nalUnits[0], startCode) {
					t.Error("NAL unit does not have start code prefix")
				}

				// Verify NAL data follows start code
				if len(nalUnits[0]) < 5 {
					t.Error("NAL unit too short")
				}

				// Check NAL header matches
				if nalUnits[0][4] != tt.payload[0] {
					t.Errorf("NAL header mismatch: got %02x, want %02x", nalUnits[0][4], tt.payload[0])
				}
			}
		})
	}
}

func TestH264Depacketizer_STAPA(t *testing.T) {
	tests := []struct {
		name      string
		payload   []byte
		wantNALs  int
		wantSizes []int // Expected sizes excluding start codes
	}{
		{
			name: "STAP-A with two NAL units",
			payload: []byte{
				0x78, // STAP-A NAL header (type 24)
				// First NAL: size=4
				0x00, 0x04,
				0x67, 0x42, 0x00, 0x1e, // SPS
				// Second NAL: size=3
				0x00, 0x03,
				0x68, 0xce, 0x3c, // PPS
			},
			wantNALs:  2,
			wantSizes: []int{4, 3},
		},
		{
			name: "STAP-A with three NAL units",
			payload: []byte{
				0x78, // STAP-A NAL header
				// First NAL: size=2
				0x00, 0x02,
				0x06, 0x05, // SEI
				// Second NAL: size=4
				0x00, 0x04,
				0x67, 0x42, 0x00, 0x1e, // SPS
				// Third NAL: size=3
				0x00, 0x03,
				0x68, 0xce, 0x3c, // PPS
			},
			wantNALs:  3,
			wantSizes: []int{2, 4, 3},
		},
		{
			name: "STAP-A with single NAL unit",
			payload: []byte{
				0x78, // STAP-A NAL header
				0x00, 0x05,
				0x65, 0x88, 0x80, 0x00, 0x01, // IDR slice
			},
			wantNALs:  1,
			wantSizes: []int{5},
		},
		{
			name: "STAP-A with truncated size field",
			payload: []byte{
				0x78, // STAP-A NAL header
				0x00, // Incomplete size field
			},
			wantNALs:  0,
			wantSizes: []int{},
		},
		{
			name: "STAP-A with truncated NAL data",
			payload: []byte{
				0x78,       // STAP-A NAL header
				0x00, 0x05, // Size = 5
				0x65, 0x88, // Only 2 bytes instead of 5
			},
			wantNALs:  0,
			wantSizes: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewH264Depacketizer()
			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: 2000,
				},
				Payload: tt.payload,
			}
			nalUnits, err := d.Depacketize(packet)

			// For truncated/malformed packets, we now correctly return errors
			isTruncated := tt.name == "STAP-A with truncated size field" ||
				tt.name == "STAP-A with truncated NAL data"

			if isTruncated {
				// These should now return errors due to security fixes
				if err == nil {
					t.Errorf("Depacketize() expected error for truncated data, got nil")
					return
				}
				// Verify we got the expected error
				if !strings.Contains(err.Error(), "STAP-A") {
					t.Errorf("Depacketize() unexpected error type = %v", err)
				}
				return
			}

			if err != nil {
				t.Errorf("Depacketize() unexpected error = %v", err)
				return
			}

			if len(nalUnits) != tt.wantNALs {
				t.Errorf("Depacketize() returned %d NAL units, want %d", len(nalUnits), tt.wantNALs)
				return
			}

			// Verify each NAL unit
			for i, nalUnit := range nalUnits {
				// Check start code
				startCode := []byte{0x00, 0x00, 0x00, 0x01}
				if !bytes.HasPrefix(nalUnit, startCode) {
					t.Errorf("NAL unit %d does not have start code prefix", i)
				}

				// Check size (excluding start code)
				if i < len(tt.wantSizes) {
					actualSize := len(nalUnit) - 4 // Subtract start code length
					if actualSize != tt.wantSizes[i] {
						t.Errorf("NAL unit %d size = %d, want %d", i, actualSize, tt.wantSizes[i])
					}
				}
			}
		})
	}
}

func TestH264Depacketizer_FUA(t *testing.T) {
	// Create a large NAL unit to fragment
	originalNAL := []byte{0x65} // IDR slice header
	for i := 0; i < 100; i++ {
		originalNAL = append(originalNAL, byte(i))
	}

	tests := []struct {
		name        string
		fragments   [][]byte
		sequences   []uint16
		wantNALs    int
		wantSize    int // Expected size excluding start code
		wantError   bool
		description string
	}{
		{
			name: "complete FU-A sequence",
			fragments: [][]byte{
				// First fragment (start bit set)
				{
					0x7c,       // FU-A indicator (forbidden=0, nri=3, type=28)
					0x85,       // FU header (S=1, E=0, R=0, type=5)
					1, 2, 3, 4, // Fragment data
				},
				// Middle fragment
				{
					0x7c,          // FU-A indicator
					0x05,          // FU header (S=0, E=0, R=0, type=5)
					5, 6, 7, 8, 9, // Fragment data
				},
				// Last fragment (end bit set)
				{
					0x7c,               // FU-A indicator
					0x45,               // FU header (S=0, E=1, R=0, type=5)
					10, 11, 12, 13, 14, // Fragment data
				},
			},
			sequences:   []uint16{3000, 3001, 3002},
			wantNALs:    1,
			wantSize:    15, // 1 (reconstructed header) + 14 (payload bytes)
			wantError:   false,
			description: "Should reassemble fragmented NAL unit correctly",
		},
		{
			name: "FU-A with packet loss",
			fragments: [][]byte{
				// First fragment (start bit set)
				{
					0x7c,       // FU-A indicator
					0x85,       // FU header (S=1, E=0, R=0, type=5)
					1, 2, 3, 4, // Fragment data
				},
				// Missing middle fragment (sequence jump)
				// Last fragment (end bit set)
				{
					0x7c,           // FU-A indicator
					0x45,           // FU header (S=0, E=1, R=0, type=5)
					10, 11, 12, 13, // Fragment data
				},
			},
			sequences:   []uint16{4000, 4007}, // Note: gap of 7, exceeds tolerance
			wantNALs:    0,
			wantSize:    0,
			wantError:   false,
			description: "Should discard fragments when packet loss detected",
		},
		{
			name: "FU-A single fragment (both start and end) - RFC 6184 violation, treated as single NAL",
			fragments: [][]byte{
				{
					0x7c,          // FU-A indicator (NRI=3, type=28)
					0xc5,          // FU header (S=1, E=1, R=0, type=5)
					1, 2, 3, 4, 5, // Fragment data
				},
			},
			sequences:   []uint16{5000},
			wantNALs:    1,
			wantSize:    6, // NAL header (1) + payload (5) -- wantSize excludes start code
			wantError:   false,
			description: "RFC 6184 Section 5.8 violation: S+E both set treated as single NAL unit",
		},
		{
			name: "FU-A with short payload",
			fragments: [][]byte{
				{
					0x7c, // FU-A indicator
					// Missing FU header
				},
			},
			sequences:   []uint16{6000},
			wantNALs:    0,
			wantSize:    0,
			wantError:   false,
			description: "Should reject FU-A with insufficient data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewH264Depacketizer()
			var allNALs [][]byte

			// Process each fragment
			for i, fragment := range tt.fragments {
				packet := &rtp.Packet{
					Header: rtp.Header{
						SequenceNumber: tt.sequences[i],
					},
					Payload: fragment,
				}
				nalUnits, err := d.Depacketize(packet)
				if (err != nil) != tt.wantError {
					t.Errorf("Fragment %d: Depacketize() error = %v, wantError %v", i, err, tt.wantError)
					return
				}
				allNALs = append(allNALs, nalUnits...)
			}

			if len(allNALs) != tt.wantNALs {
				t.Errorf("Got %d NAL units, want %d. %s", len(allNALs), tt.wantNALs, tt.description)
				return
			}

			// Verify reassembled NAL unit
			if tt.wantNALs > 0 {
				nalUnit := allNALs[0]

				// Check start code
				startCode := []byte{0x00, 0x00, 0x00, 0x01}
				if !bytes.HasPrefix(nalUnit, startCode) {
					t.Error("NAL unit does not have start code prefix")
				}

				// Check size
				actualSize := len(nalUnit) - 4 // Subtract start code
				if actualSize != tt.wantSize {
					t.Errorf("NAL unit size = %d, want %d", actualSize, tt.wantSize)
				}

				// For IDR slices, verify the reconstructed NAL header
				if len(nalUnit) > 4 && GetNALType(nalUnit[4]) != 5 {
					t.Errorf("Reconstructed NAL type = %d, want 5 (IDR)", GetNALType(nalUnit[4]))
				}
			}
		})
	}
}

func TestH264Depacketizer_MixedPackets(t *testing.T) {
	d := NewH264Depacketizer()
	totalNALs := 0

	// 1. Single NAL unit (SPS)
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 7000,
		},
		Payload: []byte{0x67, 0x42, 0x00, 0x1e},
	}
	nalUnits, err := d.Depacketize(packet)
	if err != nil {
		t.Errorf("Single NAL: unexpected error = %v", err)
	}
	totalNALs += len(nalUnits)

	// 2. STAP-A with PPS and SEI
	stapA := []byte{
		0x78,                         // STAP-A
		0x00, 0x03, 0x68, 0xce, 0x3c, // PPS
		0x00, 0x02, 0x06, 0x05, // SEI
	}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 7001,
		},
		Payload: stapA,
	}
	nalUnits, err = d.Depacketize(packet)
	if err != nil {
		t.Errorf("STAP-A: unexpected error = %v", err)
	}
	totalNALs += len(nalUnits)

	// 3. FU-A start
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 7002,
		},
		Payload: []byte{0x7c, 0x85, 1, 2, 3},
	}
	nalUnits, err = d.Depacketize(packet)
	if err != nil {
		t.Errorf("FU-A start: unexpected error = %v", err)
	}
	totalNALs += len(nalUnits) // Should be 0 (incomplete)

	// 4. FU-A end
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 7003,
		},
		Payload: []byte{0x7c, 0x45, 4, 5, 6},
	}
	nalUnits, err = d.Depacketize(packet)
	if err != nil {
		t.Errorf("FU-A end: unexpected error = %v", err)
	}
	totalNALs += len(nalUnits) // Should be 1 (complete FU-A)

	// Verify total NAL units: 1 (SPS) + 2 (STAP-A) + 1 (FU-A) = 4
	if totalNALs != 4 {
		t.Errorf("Total NAL units = %d, want 4", totalNALs)
	}
}

func TestH264Depacketizer_Reset(t *testing.T) {
	d := NewH264Depacketizer()

	// Start a FU-A sequence
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 8000,
		},
		Payload: []byte{0x7c, 0x85, 1, 2, 3},
	}
	_, err := d.Depacketize(packet)
	if err != nil {
		t.Errorf("FU-A start: unexpected error = %v", err)
	}

	// Reset the depacketizer
	d.Reset()

	// Try to continue the FU-A sequence (should fail as state is cleared)
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 8001,
		},
		Payload: []byte{0x7c, 0x45, 4, 5, 6},
	}
	nalUnits, err := d.Depacketize(packet)
	if err != nil {
		t.Errorf("FU-A end: unexpected error = %v", err)
	}

	// Should get no NAL units as the fragments were cleared
	if len(nalUnits) != 0 {
		t.Errorf("Expected 0 NAL units after reset, got %d", len(nalUnits))
	}
}

func TestGetNALType(t *testing.T) {
	tests := []struct {
		nalHeader byte
		wantType  byte
	}{
		{0x41, 1},  // Non-IDR slice
		{0x65, 5},  // IDR slice
		{0x67, 7},  // SPS
		{0x68, 8},  // PPS
		{0x78, 24}, // STAP-A
		{0x7c, 28}, // FU-A
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("header_%02x", tt.nalHeader), func(t *testing.T) {
			gotType := GetNALType(tt.nalHeader)
			if gotType != tt.wantType {
				t.Errorf("GetNALType(%02x) = %d, want %d", tt.nalHeader, gotType, tt.wantType)
			}
		})
	}
}

func TestIsKeyFrame(t *testing.T) {
	tests := []struct {
		nalType      byte
		wantKeyFrame bool
	}{
		{1, false}, // Non-IDR slice
		{5, true},  // IDR slice
		{7, false}, // SPS (parameter set, not a frame)
		{8, false}, // PPS
		{6, false}, // SEI
		{9, false}, // Access unit delimiter
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("type_%d", tt.nalType), func(t *testing.T) {
			got := IsKeyFrame(tt.nalType)
			if got != tt.wantKeyFrame {
				t.Errorf("IsKeyFrame(%d) = %v, want %v", tt.nalType, got, tt.wantKeyFrame)
			}
		})
	}
}

func TestH264Depacketizer_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		payload   []byte
		wantError bool
		errorMsg  string
	}{
		{
			name:      "unsupported NAL type STAP-B",
			payload:   []byte{0x79, 0x00, 0x00}, // Type 25
			wantError: true,
			errorMsg:  "unsupported NAL type: 25",
		},
		{
			name:      "unsupported NAL type MTAP16",
			payload:   []byte{0x7a, 0x00, 0x00}, // Type 26
			wantError: true,
			errorMsg:  "unsupported NAL type: 26",
		},
		{
			name:      "unsupported NAL type FU-B",
			payload:   []byte{0x7d, 0x00, 0x00}, // Type 29
			wantError: true,
			errorMsg:  "unsupported NAL type: 29",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewH264Depacketizer()
			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: 9000,
				},
				Payload: tt.payload,
			}
			_, err := d.Depacketize(packet)

			if (err != nil) != tt.wantError {
				t.Errorf("Depacketize() error = %v, wantError %v", err, tt.wantError)
			}

			if err != nil && err.Error() != tt.errorMsg {
				t.Errorf("Error message = %q, want %q", err.Error(), tt.errorMsg)
			}
		})
	}
}
