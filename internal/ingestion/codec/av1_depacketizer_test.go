package codec

import (
	"testing"

	"github.com/pion/rtp"
)

// AV1 aggregation header bit layout:
//   bit 7: Z  (0x80) - Z=1 means continuation from previous packet (not-first fragment)
//   bit 6: Y  (0x40) - Y=1 means will continue in next packet (not-last fragment)
//   bits 5-4: W (0x30) - number of OBU element length fields
//   bit 3: N  (0x08) - new temporal unit indicator
//
// OBU header byte layout:
//   bit 7: forbidden (must be 0)
//   bits 6-3: OBU type (4 bits)
//   bit 2: extension flag
//   bit 1: has_size_field
//   bit 0: reserved
//
// OBU types:
//   1 = SequenceHeader  -> header byte 0x08
//   2 = TemporalDelimiter -> header byte 0x10
//   6 = Frame           -> header byte 0x30

func TestNewAV1Depacketizer(t *testing.T) {
	d := NewAV1Depacketizer()
	if d == nil {
		t.Fatal("NewAV1Depacketizer returned nil")
	}
}

func TestAV1Depacketizer_SingleOBU(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		seq     uint16
		wantErr bool
		wantLen int
	}{
		{
			name: "single OBU - temporal delimiter",
			// Aggregation header: Z=0, Y=0, W=0, N=1 (0x08)
			// OBU header: type=2 (temporal delimiter) = 0x10
			// OBU payload: 0x00
			payload: []byte{0x08, 0x10, 0x00},
			seq:     1,
			wantErr: false,
			wantLen: 1, // Temporal delimiter triggers flush
		},
		{
			name: "single OBU - sequence header",
			// Aggregation header: Z=0, Y=0, W=0, N=0 (0x00)
			// OBU header: type=1 (sequence header) = 0x08
			// OBU payload: some data
			payload: []byte{0x00, 0x08, 0x00, 0x00, 0x00},
			seq:     2,
			wantErr: false,
			wantLen: 0, // No temporal delimiter, so buffered
		},
		{
			name: "single OBU with W=1",
			// Aggregation header: Z=0, Y=0, W=1, N=0 (0x10)
			// W=1 means 1 OBU element; last (only) OBU has no length prefix
			// OBU header: type=2 (temporal delimiter) = 0x10
			// OBU payload: 0x00
			payload: []byte{0x10, 0x10, 0x00},
			seq:     3,
			wantErr: false,
			wantLen: 1, // Temporal delimiter triggers flush
		},
		{
			name:    "payload too short",
			payload: []byte{},
			seq:     4,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewAV1Depacketizer()
			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: tt.seq,
				},
				Payload: tt.payload,
			}
			obus, err := d.Depacketize(packet)

			if (err != nil) != tt.wantErr {
				t.Errorf("Depacketize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && len(obus) != tt.wantLen {
				t.Errorf("Depacketize() returned %d OBUs, want %d", len(obus), tt.wantLen)
			}
		})
	}
}

func TestAV1Depacketizer_FragmentedOBU(t *testing.T) {
	d := NewAV1Depacketizer()

	// First fragment: Z=0 (not continuation), Y=1 (will continue)
	// Aggregation header: Z=0, Y=1, W=0, N=0 (0x40)
	// OBU header: type=6 (frame) = 0x30
	// OBU payload: first part of frame data
	payload1 := []byte{0x40, 0x30, 0x01, 0x02, 0x03, 0x04}
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 100,
		},
		Payload: payload1,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("First fragment failed: %v", err)
	}
	if len(obus) != 0 {
		t.Error("First fragment should not return complete OBUs")
	}

	// Middle fragment: Z=1 (continuation), Y=1 (will continue)
	// Aggregation header: Z=1, Y=1, W=0, N=0 (0xC0)
	// Payload: middle part of frame data (no OBU header, raw continuation)
	payload2 := []byte{0xC0, 0x05, 0x06, 0x07, 0x08}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 101,
		},
		Payload: payload2,
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Middle fragment failed: %v", err)
	}
	if len(obus) != 0 {
		t.Error("Middle fragment should not return complete OBUs")
	}

	// Last fragment: Z=1 (continuation), Y=0 (will NOT continue)
	// Aggregation header: Z=1, Y=0, W=0, N=0 (0x80)
	// Payload: last part of frame data
	payload3 := []byte{0x80, 0x09, 0x0A, 0x0B, 0x0C}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 102,
		},
		Payload: payload3,
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Last fragment failed: %v", err)
	}
	// The reassembled OBU is buffered in temporalUnitBuf.
	// Since it doesn't contain a temporal delimiter, it stays buffered.
	if len(obus) != 0 {
		t.Error("Last fragment should buffer OBU until temporal delimiter")
	}
}

func TestAV1Depacketizer_FragmentedOBU_WithFlush(t *testing.T) {
	d := NewAV1Depacketizer()

	// First fragment of a frame OBU: Z=0, Y=1 (0x40)
	// OBU header: type=6 (frame) = 0x30
	payload1 := []byte{0x40, 0x30, 0x01, 0x02, 0x03}
	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 100},
		Payload: payload1,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("First fragment failed: %v", err)
	}
	if len(obus) != 0 {
		t.Error("First fragment should not return OBUs")
	}

	// Last fragment: Z=1, Y=0 (0x80)
	payload2 := []byte{0x80, 0x04, 0x05, 0x06}
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 101},
		Payload: payload2,
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Last fragment failed: %v", err)
	}
	// Frame OBU is buffered (no temporal delimiter)
	if len(obus) != 0 {
		t.Error("Last fragment should buffer OBU (no temporal delimiter)")
	}

	// Now send a temporal delimiter to flush: Z=0, Y=0, W=0, N=1 (0x08)
	// OBU header: type=2 (temporal delimiter) = 0x10
	payload3 := []byte{0x08, 0x10, 0x00}
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 102},
		Payload: payload3,
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Temporal delimiter failed: %v", err)
	}
	// N=1 flushes previous temporal unit (the frame), then adds the new
	// temporal delimiter which also gets flushed due to TD detection.
	// Previous TU had 1 OBU (the reassembled frame) + current has 1 OBU (TD) = 2
	if len(obus) != 2 {
		t.Errorf("Expected 2 OBUs (flushed frame + temporal delimiter), got %d", len(obus))
	}
}

func TestAV1Depacketizer_MultipleOBUs(t *testing.T) {
	d := NewAV1Depacketizer()

	// Multiple OBUs in single packet with W=2
	// Aggregation header: Z=0, Y=0, W=2, N=0 (0x20)
	// W=2 means: first W-1=1 OBU has LEB128 length prefix, last extends to end
	payload := []byte{
		0x20,
		// First OBU: LEB128 length=2, then 2-byte temporal delimiter OBU
		0x02, 0x10, 0x00,
		// Second OBU (last, no length prefix): sequence header, extends to end
		0x08, 0x00, 0x00, 0x00,
	}

	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 200,
		},
		Payload: payload,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Multiple OBUs failed: %v", err)
	}

	// Should return OBUs immediately due to temporal delimiter in the buffer
	if len(obus) != 2 {
		t.Errorf("Expected 2 OBUs, got %d", len(obus))
	}

	// Verify first OBU is temporal delimiter (header byte 0x10, type = 2)
	if len(obus) >= 1 && len(obus[0]) > 0 {
		obuType := (obus[0][0] >> 3) & 0x0F
		if obuType != obuTemporalDelimiter {
			t.Errorf("First OBU type = %d, want %d (TemporalDelimiter)", obuType, obuTemporalDelimiter)
		}
	}

	// Verify second OBU is sequence header (header byte 0x08, type = 1)
	if len(obus) >= 2 && len(obus[1]) > 0 {
		obuType := (obus[1][0] >> 3) & 0x0F
		if obuType != obuSequenceHeader {
			t.Errorf("Second OBU type = %d, want %d (SequenceHeader)", obuType, obuSequenceHeader)
		}
	}
}

func TestAV1Depacketizer_MultipleOBUs_W3(t *testing.T) {
	d := NewAV1Depacketizer()

	// W=3: first 2 OBUs have LEB128 length prefix, third extends to end
	// Aggregation header: Z=0, Y=0, W=3, N=0 (0x30)
	payload := []byte{
		0x30,
		// First OBU: LEB128 length=2, then temporal delimiter
		0x02, 0x10, 0x00,
		// Second OBU: LEB128 length=2, then frame header (type=3, 0x18)
		0x02, 0x18, 0x00,
		// Third OBU (last, no length prefix): sequence header
		0x08, 0x00,
	}

	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 210},
		Payload: payload,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("W=3 multiple OBUs failed: %v", err)
	}

	// Should flush all 3 OBUs because temporal delimiter is present
	if len(obus) != 3 {
		t.Errorf("Expected 3 OBUs, got %d", len(obus))
	}
}

func TestAV1Depacketizer_PacketLoss(t *testing.T) {
	d := NewAV1Depacketizer()

	// Start fragmented OBU: first fragment Z=0, Y=1 (0x40)
	// OBU header: type=6 (frame) = 0x30
	payload1 := []byte{0x40, 0x30, 0x01, 0x02, 0x03}
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 300,
		},
		Payload: payload1,
	}
	_, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("First fragment failed: %v", err)
	}

	// Simulate packet loss (skip sequence 301)
	// Send continuation fragment with sequence 302: Z=1, Y=0 (0x80)
	payload2 := []byte{0x80, 0x04, 0x05, 0x06}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 302,
		},
		Payload: payload2,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Packet after loss failed: %v", err)
	}

	// Should have discarded fragments due to packet loss
	// Z=1 after loss gets silently dropped (line 73-76)
	if len(obus) != 0 {
		t.Error("Should not return OBUs after packet loss")
	}
}

func TestAV1Depacketizer_PacketLoss_NewFragmentAfterLoss(t *testing.T) {
	d := NewAV1Depacketizer()

	// Start fragmented OBU: first fragment Z=0, Y=1 (0x40)
	payload1 := []byte{0x40, 0x30, 0x01, 0x02, 0x03}
	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 300},
		Payload: payload1,
	}
	_, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("First fragment failed: %v", err)
	}

	// Skip 301, send non-continuation at 302: Z=0, Y=0, W=0, N=1 (0x08)
	// OBU header: type=2 (temporal delimiter) = 0x10
	payload2 := []byte{0x08, 0x10, 0x00}
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 302},
		Payload: payload2,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Packet after loss failed: %v", err)
	}

	// Packet loss detected, fragments discarded. Then Z=0 so it processes as
	// new single OBU (temporal delimiter), which gets flushed.
	if len(obus) != 1 {
		t.Errorf("Expected 1 OBU after packet loss recovery, got %d", len(obus))
	}
}

func TestAV1Depacketizer_TemporalUnitAssembly(t *testing.T) {
	d := NewAV1Depacketizer()

	// First packet: temporal delimiter with N=1
	// Aggregation header: Z=0, Y=0, W=0, N=1 (0x08)
	// OBU header: type=2 (temporal delimiter) = 0x10
	payload1 := []byte{0x08, 0x10, 0x00}
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 400,
		},
		Payload: payload1,
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("First packet failed: %v", err)
	}
	// Single OBU (temporal delimiter) should be flushed
	if len(obus) != 1 {
		t.Errorf("Expected 1 OBU for temporal delimiter, got %d", len(obus))
	}

	// Second packet: frame OBU without N bit
	// Aggregation header: Z=0, Y=0, W=0, N=0 (0x00)
	// OBU header: type=6 (frame) = 0x30
	payload2 := []byte{0x00, 0x30, 0x01, 0x02}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 401,
		},
		Payload: payload2,
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Second packet failed: %v", err)
	}
	// Frame OBU has no temporal delimiter, stays buffered
	if len(obus) != 0 {
		t.Error("Should buffer frame OBU")
	}

	// Third packet: new temporal delimiter with N=1
	// N=1 flushes previous temporal unit (the frame), then the new TD also gets flushed
	// Aggregation header: Z=0, Y=0, W=0, N=1 (0x08)
	payload3 := []byte{0x08, 0x10, 0x00}
	packet = &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 402,
		},
		Payload: payload3,
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Third packet failed: %v", err)
	}
	// Should flush previous temporal unit (frame) and return new temporal delimiter
	if len(obus) != 2 {
		t.Errorf("Expected 2 OBUs (flushed frame + new delimiter), got %d", len(obus))
	}
}

func TestAV1Depacketizer_Reset(t *testing.T) {
	d := NewAV1Depacketizer()

	// Just test that Reset() doesn't panic
	d.Reset()

	// Verify depacketizer works after reset
	// Aggregation header: Z=0, Y=0, W=0, N=1 (0x08)
	// OBU header: type=2 (temporal delimiter) = 0x10
	packet := &rtp.Packet{
		Header: rtp.Header{
			SequenceNumber: 1000,
		},
		Payload: []byte{0x08, 0x10, 0x00},
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Errorf("Depacketize after reset failed: %v", err)
	}
	if len(obus) != 1 {
		t.Errorf("Expected 1 OBU after reset, got %d", len(obus))
	}
}

func TestAV1Depacketizer_ResetDuringFragmentation(t *testing.T) {
	d := NewAV1Depacketizer()

	// Start a fragment: Z=0, Y=1 (0x40)
	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 500},
		Payload: []byte{0x40, 0x30, 0x01, 0x02},
	}
	_, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Fragment failed: %v", err)
	}

	// Reset should clear fragment state
	d.Reset()

	// After reset, a new single OBU should work fine
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 600},
		Payload: []byte{0x08, 0x10, 0x00}, // TD with N=1
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Errorf("Depacketize after reset failed: %v", err)
	}
	if len(obus) != 1 {
		t.Errorf("Expected 1 OBU after reset, got %d", len(obus))
	}
}

func TestGetOBUTypeName(t *testing.T) {
	tests := []struct {
		obuType uint8
		want    string
	}{
		{obuSequenceHeader, "SequenceHeader"},
		{obuTemporalDelimiter, "TemporalDelimiter"},
		{obuFrameHeader, "FrameHeader"},
		{obuTileGroup, "TileGroup"},
		{obuMetadata, "Metadata"},
		{obuFrame, "Frame"},
		{obuRedundantFrameHeader, "RedundantFrameHeader"},
		{obuTileList, "TileList"},
		{obuPadding, "Padding"},
		{99, "Unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := GetOBUTypeName(tt.obuType)
			if got != tt.want {
				t.Errorf("GetOBUTypeName(%d) = %s, want %s", tt.obuType, got, tt.want)
			}
		})
	}
}

func TestAV1Depacketizer_ErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(d Depacketizer) // Optional setup to put depacketizer in a specific state
		payload []byte
		seq     uint16
		wantErr bool
		errMsg  string
	}{
		{
			name:    "payload too short - empty",
			payload: []byte{},
			seq:     600,
			wantErr: true,
			errMsg:  "payload too short",
		},
		{
			name: "OBU length exceeds payload in multi-OBU packet",
			// Aggregation header: Z=0, Y=0, W=2, N=0 (0x20)
			// W=2: first OBU has LEB128 length prefix, second extends to end
			// LEB128 length = 16 (0x10), but only 1 byte follows
			payload: []byte{0x20, 0x10, 0x01},
			seq:     601,
			wantErr: true,
			errMsg:  "OBU length exceeds payload size",
		},
		{
			name: "continuation without prior fragment",
			// Z=1 (continuation) but no fragment state set up
			// Aggregation header: Z=1, Y=0, W=0, N=0 (0x80)
			payload: []byte{0x80, 0x01, 0x02, 0x03},
			seq:     602,
			wantErr: false, // Not an error; silently returns empty
		},
		{
			name: "fragment accumulation exceeds size limit",
			setup: func(d Depacketizer) {
				// Start a fragment to set up fragment state
				// First fragment: Z=0, Y=1 (0x40)
				pkt := &rtp.Packet{
					Header:  rtp.Header{SequenceNumber: 699},
					Payload: []byte{0x40, 0x30, 0x01},
				}
				_, _ = d.Depacketize(pkt)
			},
			// Continuation with huge payload won't actually hit the limit
			// with small test data, but we can verify the path exists.
			// Z=1, Y=0 (0x80) - last continuation fragment
			payload: []byte{0x80, 0x04, 0x05},
			seq:     700,
			wantErr: false, // Small fragments won't exceed 10MB limit
		},
		{
			name: "invalid LEB128 in multi-OBU packet",
			// Aggregation header: Z=0, Y=0, W=2, N=0 (0x20)
			// All bytes have MSB set = incomplete LEB128
			payload: []byte{0x20, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
			seq:     603,
			wantErr: true,
			errMsg:  "failed to parse OBU length: LEB128 value too large or incomplete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewAV1Depacketizer()

			if tt.setup != nil {
				tt.setup(d)
			}

			packet := &rtp.Packet{
				Header: rtp.Header{
					SequenceNumber: tt.seq,
				},
				Payload: tt.payload,
			}
			_, err := d.Depacketize(packet)

			if (err != nil) != tt.wantErr {
				t.Errorf("Depacketize() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr && err != nil && tt.errMsg != "" {
				if err.Error() != tt.errMsg {
					t.Errorf("Error message = %q, want %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestAV1Depacketizer_ContinuationWithoutStart(t *testing.T) {
	d := NewAV1Depacketizer()

	// Send a continuation fragment (Z=1) without any prior first fragment
	// Aggregation header: Z=1, Y=0, W=0, N=0 (0x80)
	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 1},
		Payload: []byte{0x80, 0x01, 0x02, 0x03},
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(obus) != 0 {
		t.Errorf("Expected 0 OBUs for orphaned continuation, got %d", len(obus))
	}
}

func TestAV1Depacketizer_MiddleFragmentContinuation(t *testing.T) {
	d := NewAV1Depacketizer()

	// First fragment: Z=0, Y=1 (0x40)
	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 10},
		Payload: []byte{0x40, 0x30, 0x01, 0x02},
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("First fragment error: %v", err)
	}
	if len(obus) != 0 {
		t.Error("First fragment should not return OBUs")
	}

	// Middle fragment: Z=1, Y=1 (0xC0)
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 11},
		Payload: []byte{0xC0, 0x03, 0x04},
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Middle fragment error: %v", err)
	}
	if len(obus) != 0 {
		t.Error("Middle fragment should not return OBUs")
	}

	// Another middle fragment: Z=1, Y=1 (0xC0)
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 12},
		Payload: []byte{0xC0, 0x05, 0x06},
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Second middle fragment error: %v", err)
	}
	if len(obus) != 0 {
		t.Error("Second middle fragment should not return OBUs")
	}

	// Last fragment: Z=1, Y=0 (0x80)
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 13},
		Payload: []byte{0x80, 0x07, 0x08},
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Last fragment error: %v", err)
	}
	// Reassembled OBU is in temporalUnitBuf (no temporal delimiter to flush)
	if len(obus) != 0 {
		t.Error("Reassembled frame should be buffered (no temporal delimiter)")
	}

	// Now send temporal delimiter to flush: Z=0, Y=0, W=0, N=1 (0x08)
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 14},
		Payload: []byte{0x08, 0x10, 0x00},
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Temporal delimiter error: %v", err)
	}
	// Should flush the reassembled frame + the temporal delimiter
	if len(obus) != 2 {
		t.Errorf("Expected 2 OBUs (reassembled frame + TD), got %d", len(obus))
	}

	// Verify the reassembled content
	if len(obus) >= 1 {
		expected := []byte{0x30, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
		if len(obus[0]) != len(expected) {
			t.Errorf("Reassembled OBU length = %d, want %d", len(obus[0]), len(expected))
		} else {
			for i, b := range obus[0] {
				if b != expected[i] {
					t.Errorf("Reassembled OBU[%d] = 0x%02x, want 0x%02x", i, b, expected[i])
				}
			}
		}
	}
}

func TestAV1Depacketizer_NBitFlushes(t *testing.T) {
	d := NewAV1Depacketizer()

	// First: send a sequence header (no temporal delimiter, stays buffered)
	// Z=0, Y=0, W=0, N=0 (0x00)
	packet := &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 50},
		Payload: []byte{0x00, 0x08, 0xAA, 0xBB}, // Sequence header OBU
	}
	obus, err := d.Depacketize(packet)
	if err != nil {
		t.Fatalf("Sequence header failed: %v", err)
	}
	if len(obus) != 0 {
		t.Error("Sequence header should be buffered")
	}

	// Second: send new temporal unit (N=1) with temporal delimiter
	// The N=1 should flush the previous buffered sequence header
	packet = &rtp.Packet{
		Header:  rtp.Header{SequenceNumber: 51},
		Payload: []byte{0x08, 0x10, 0x00}, // N=1, temporal delimiter
	}
	obus, err = d.Depacketize(packet)
	if err != nil {
		t.Fatalf("N=1 packet failed: %v", err)
	}
	// N=1 flushes previous TU (sequence header) then current TD also flushes
	if len(obus) != 2 {
		t.Errorf("Expected 2 OBUs (flushed SH + TD), got %d", len(obus))
	}
}
