package codec

import (
	"sync"
	"testing"

	"github.com/pion/rtp"
)

// TestH264DepacketizerRace tests for race conditions in H264 depacketizer
func TestH264DepacketizerRace(t *testing.T) {
	depacketizer := &H264Depacketizer{}
	
	// Create fragmented NAL unit packets
	packets := createH264FragmentedPackets()
	
	// Run concurrent depacketization
	var wg sync.WaitGroup
	numGoroutines := 10
	
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < 100; j++ {
				for _, pkt := range packets {
					_, err := depacketizer.Depacketize(pkt)
					if err != nil && err.Error() != "unknown NAL type: 0" {
						t.Errorf("Goroutine %d: unexpected error: %v", id, err)
					}
				}
				
				// Occasionally reset
				if j%10 == 0 {
					depacketizer.Reset()
				}
			}
		}(i)
	}
	
	wg.Wait()
}

// TestHEVCDepacketizerRace tests for race conditions in HEVC depacketizer
func TestHEVCDepacketizerRace(t *testing.T) {
	depacketizer := &HEVCDepacketizer{}
	
	// Create fragmented NAL unit packets
	packets := createHEVCFragmentedPackets()
	
	// Run concurrent depacketization
	var wg sync.WaitGroup
	numGoroutines := 10
	
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < 100; j++ {
				for _, pkt := range packets {
					_, err := depacketizer.Depacketize(pkt)
					if err != nil {
						t.Errorf("Goroutine %d: unexpected error: %v", id, err)
					}
				}
				
				// Occasionally reset
				if j%10 == 0 {
					depacketizer.Reset()
				}
			}
		}(i)
	}
	
	wg.Wait()
}

// createH264FragmentedPackets creates a sequence of H.264 FU-A packets
func createH264FragmentedPackets() []*rtp.Packet {
	packets := make([]*rtp.Packet, 0)
	
	// Large NAL unit to fragment
	nalData := make([]byte, 3000)
	for i := range nalData {
		nalData[i] = byte(i % 256)
	}
	
	// Fragment into FU-A packets
	mtu := 1200
	offset := 0
	seqNum := uint16(1000)
	
	for offset < len(nalData) {
		remaining := len(nalData) - offset
		fragmentSize := mtu - 2 // Account for FU indicator and header
		if fragmentSize > remaining {
			fragmentSize = remaining
		}
		
		// Create FU-A packet
		payload := make([]byte, fragmentSize+2)
		
		// FU indicator
		payload[0] = 0x7C // FU-A type (28) with NRI=3
		
		// FU header
		if offset == 0 {
			// Start bit
			payload[1] = 0x80 | 0x05 // S=1, E=0, Type=5 (IDR)
		} else if offset+fragmentSize >= len(nalData) {
			// End bit
			payload[1] = 0x40 | 0x05 // S=0, E=1, Type=5 (IDR)
		} else {
			// Middle fragment
			payload[1] = 0x05 // S=0, E=0, Type=5 (IDR)
		}
		
		// Copy fragment data
		copy(payload[2:], nalData[offset:offset+fragmentSize])
		
		packet := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    96,
				SequenceNumber: seqNum,
				Timestamp:      90000,
				SSRC:           12345,
			},
			Payload: payload,
		}
		
		packets = append(packets, packet)
		offset += fragmentSize
		seqNum++
	}
	
	// Add a single NAL unit packet
	singleNAL := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: seqNum,
			Timestamp:      90000,
			SSRC:           12345,
		},
		Payload: []byte{0x67, 0x42, 0x00, 0x1f}, // SPS
	}
	packets = append(packets, singleNAL)
	
	return packets
}

// createHEVCFragmentedPackets creates a sequence of HEVC FU packets
func createHEVCFragmentedPackets() []*rtp.Packet {
	packets := make([]*rtp.Packet, 0)
	
	// Large NAL unit to fragment
	nalData := make([]byte, 3000)
	for i := range nalData {
		nalData[i] = byte(i % 256)
	}
	
	// Fragment into FU packets
	mtu := 1200
	offset := 0
	seqNum := uint16(2000)
	
	for offset < len(nalData) {
		remaining := len(nalData) - offset
		fragmentSize := mtu - 3 // Account for PayloadHdr (2) and FU header (1)
		if fragmentSize > remaining {
			fragmentSize = remaining
		}
		
		// Create FU packet
		payload := make([]byte, fragmentSize+3)
		
		// PayloadHdr for FU (type 49)
		payload[0] = (49 << 1) | 0x01 // Type=49, layer_id=0, TID=1
		payload[1] = 0x01
		
		// FU header
		if offset == 0 {
			// Start bit
			payload[2] = 0x80 | 19 // S=1, E=0, Type=19 (IDR)
		} else if offset+fragmentSize >= len(nalData) {
			// End bit
			payload[2] = 0x40 | 19 // S=0, E=1, Type=19 (IDR)
		} else {
			// Middle fragment
			payload[2] = 19 // S=0, E=0, Type=19 (IDR)
		}
		
		// Copy fragment data
		copy(payload[3:], nalData[offset:offset+fragmentSize])
		
		packet := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				PayloadType:    96,
				SequenceNumber: seqNum,
				Timestamp:      90000,
				SSRC:           12345,
			},
			Payload: payload,
		}
		
		packets = append(packets, packet)
		offset += fragmentSize
		seqNum++
	}
	
	// Add a single NAL unit packet
	singleNAL := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: seqNum,
			Timestamp:      90000,
			SSRC:           12345,
		},
		Payload: []byte{0x40, 0x01, 0x0C, 0x01, 0xFF}, // VPS
	}
	packets = append(packets, singleNAL)
	
	return packets
}

// BenchmarkH264DepacketizerConcurrent benchmarks concurrent H264 depacketization
func BenchmarkH264DepacketizerConcurrent(b *testing.B) {
	depacketizer := &H264Depacketizer{}
	packets := createH264FragmentedPackets()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for _, pkt := range packets {
				depacketizer.Depacketize(pkt)
			}
		}
	})
}

// BenchmarkHEVCDepacketizerConcurrent benchmarks concurrent HEVC depacketization
func BenchmarkHEVCDepacketizerConcurrent(b *testing.B) {
	depacketizer := &HEVCDepacketizer{}
	packets := createHEVCFragmentedPackets()
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for _, pkt := range packets {
				depacketizer.Depacketize(pkt)
			}
		}
	})
}
