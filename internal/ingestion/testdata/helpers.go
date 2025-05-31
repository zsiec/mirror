package testdata

import (
	"net"
	"time"

	"github.com/pion/rtp"
)

// GenerateTestHEVCStream generates a test HEVC stream for the specified duration
func GenerateTestHEVCStream(width, height, fps, durationMs int) []byte {
	gen := NewHEVCGenerator(width, height, fps)
	return gen.GenerateStreamSegment(durationMs)
}

// GenerateTestRTPPackets generates test RTP packets containing HEVC data
func GenerateTestRTPPackets(width, height, fps, durationMs int, ssrc uint32) []*rtp.Packet {
	gen := NewHEVCGenerator(width, height, fps)
	customPackets := gen.GenerateRTPStream(durationMs, ssrc)

	// Convert to pion/rtp packets
	packets := make([]*rtp.Packet, len(customPackets))
	for i, pkt := range customPackets {
		packets[i] = &rtp.Packet{
			Header: rtp.Header{
				Version:        pkt.Version,
				Padding:        pkt.Padding,
				Extension:      pkt.Extension,
				Marker:         pkt.Marker,
				PayloadType:    pkt.PayloadType,
				SequenceNumber: pkt.SequenceNumber,
				Timestamp:      pkt.Timestamp,
				SSRC:           pkt.SSRC,
				CSRC:           pkt.CSRC,
			},
			Payload: pkt.Payload,
		}
	}

	return packets
}

// SendRTPPackets sends RTP packets to a UDP connection with timing
func SendRTPPackets(conn *net.UDPConn, addr *net.UDPAddr, packets []*rtp.Packet, fps int) error {
	// Calculate packet interval
	packetsPerFrame := len(packets) / (len(packets) * 1000 / (fps * 100)) // Rough estimate
	if packetsPerFrame == 0 {
		packetsPerFrame = 1
	}

	interval := time.Second / time.Duration(fps) / time.Duration(packetsPerFrame)

	for _, packet := range packets {
		data, err := packet.Marshal()
		if err != nil {
			return err
		}

		if _, err := conn.WriteToUDP(data, addr); err != nil {
			return err
		}

		time.Sleep(interval)
	}

	return nil
}

// CreateTestSRTPayload creates a test payload for SRT
func CreateTestSRTPayload(streamID string, durationMs int) []byte {
	// For SRT, we can send raw HEVC stream
	gen := NewHEVCGenerator(1920, 1080, 30)
	return gen.GenerateStreamSegment(durationMs)
}

// StreamConfig holds configuration for test streams
type StreamConfig struct {
	StreamID   string
	Width      int
	Height     int
	FPS        int
	Bitrate    int
	DurationMs int
}

// DefaultStreamConfig returns a default test stream configuration
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		StreamID:   "test-stream-1",
		Width:      1920,
		Height:     1080,
		FPS:        30,
		Bitrate:    5000000, // 5 Mbps
		DurationMs: 1000,    // 1 second
	}
}
