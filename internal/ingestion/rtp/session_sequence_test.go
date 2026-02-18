package rtp

import (
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/logger"
)

func TestSession_SequenceNumberWraparound(t *testing.T) {
	// Create test session
	remoteAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
		Supported: []string{"h264"},
	}

	session, err := NewSession("test-stream", remoteAddr, 12345, newMockRegistry(), codecsCfg, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))
	require.NoError(t, err)
	defer session.Stop()

	// Start session
	session.Start()

	// Test sequence wraparound
	sequences := []uint16{65534, 65535, 0, 1, 2}

	for _, seq := range sequences {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: seq,
				SSRC:           12345,
				Timestamp:      uint32(seq * 3000),
				PayloadType:    96,
			},
			Payload: []byte{0x65, 0x00, 0x00, 0x00}, // Simple NAL unit
		}

		// Process packet (will update stats but skip depacketization due to no codec)
		session.ProcessPacket(packet)
	}

	// Wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := session.GetStats()
	assert.Equal(t, uint64(5), stats.PacketsReceived)
	assert.Equal(t, uint64(0), stats.PacketsLost) // No packets lost across wraparound
	assert.Equal(t, uint16(2), stats.LastSequence)
}

func TestSession_SequenceGapDetection(t *testing.T) {
	// Create test session
	remoteAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
		Supported: []string{"h264"},
	}

	session, err := NewSession("test-stream", remoteAddr, 12345, newMockRegistry(), codecsCfg, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))
	require.NoError(t, err)
	defer session.Stop()

	// Start session
	session.Start()

	// Send packets with gaps
	sequences := []uint16{100, 101, 105, 106, 110} // Gaps: 2 packets lost, then 3 packets lost

	for _, seq := range sequences {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: seq,
				SSRC:           12345,
				Timestamp:      uint32(seq * 3000),
				PayloadType:    96,
			},
			Payload: []byte{0x65, 0x00, 0x00, 0x00},
		}

		session.ProcessPacket(packet)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := session.GetStats()
	assert.Equal(t, uint64(5), stats.PacketsReceived)
	assert.Equal(t, uint64(6), stats.PacketsLost)    // 3 + 3 lost packets (102-104, 107-109)
	assert.Equal(t, uint64(2), stats.SequenceGaps)   // 2 gap events
	assert.Equal(t, uint16(4), stats.MaxSequenceGap) // Largest gap was 4 (106-102)
}

func TestSession_ReorderedPackets(t *testing.T) {
	// Create test session
	remoteAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
		Supported: []string{"h264"},
	}

	session, err := NewSession("test-stream", remoteAddr, 12345, newMockRegistry(), codecsCfg, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))
	require.NoError(t, err)
	defer session.Stop()

	// Start session
	session.Start()

	// Send packets out of order
	sequences := []uint16{100, 102, 101, 104, 103} // 101 and 103 are reordered

	for _, seq := range sequences {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: seq,
				SSRC:           12345,
				Timestamp:      uint32(seq * 3000),
				PayloadType:    96,
			},
			Payload: []byte{0x65, 0x00, 0x00, 0x00},
		}

		session.ProcessPacket(packet)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := session.GetStats()
	assert.Equal(t, uint64(5), stats.PacketsReceived)
	assert.Equal(t, uint64(2), stats.ReorderedPackets) // 101 and 103 were reordered
}

func TestSession_SSRCChange(t *testing.T) {
	// Create test session
	remoteAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
		Supported: []string{"h264"},
	}

	session, err := NewSession("test-stream", remoteAddr, 12345, newMockRegistry(), codecsCfg, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))
	require.NoError(t, err)
	defer session.Stop()

	// Start session
	session.Start()

	// Send packets with SSRC change
	ssrcs := []uint32{12345, 12345, 54321, 54321} // SSRC changes at packet 3

	for i, ssrc := range ssrcs {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: uint16(i),
				SSRC:           ssrc,
				Timestamp:      uint32(i * 3000),
				PayloadType:    96,
			},
			Payload: []byte{0x65, 0x00, 0x00, 0x00},
		}

		session.ProcessPacket(packet)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := session.GetStats()
	assert.Equal(t, uint64(4), stats.PacketsReceived)
	assert.Equal(t, uint64(1), stats.SSRCChanges) // One SSRC change detected
	assert.Equal(t, uint32(54321), stats.LastSSRC)
}

func TestSession_JitterCalculation(t *testing.T) {
	// Create test session
	remoteAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
		Supported: []string{"h264"},
	}

	session, err := NewSession("test-stream", remoteAddr, 12345, newMockRegistry(), codecsCfg, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))
	require.NoError(t, err)
	defer session.Stop()

	// Start session
	session.Start()

	// Send packets with varying timestamps to simulate jitter
	for i := 0; i < 10; i++ {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: uint16(i),
				SSRC:           12345,
				Timestamp:      uint32(i * 3000), // 33ms intervals at 90kHz
				PayloadType:    96,
			},
			Payload: []byte{0x65, 0x00, 0x00, 0x00},
		}

		session.ProcessPacket(packet)

		// Vary the inter-packet delay to simulate jitter
		if i%2 == 0 {
			time.Sleep(30 * time.Millisecond)
		} else {
			time.Sleep(40 * time.Millisecond)
		}
	}

	// Check stats
	stats := session.GetStats()
	assert.Equal(t, uint64(10), stats.PacketsReceived)
	assert.Greater(t, stats.JitterSamples, uint64(5)) // Should have jitter samples
	assert.Greater(t, stats.MaxJitter, float64(0))    // Should have measured some jitter
}

func TestSession_LargeSequenceGap(t *testing.T) {
	// Create test session
	remoteAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5000}
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
		Supported: []string{"h264"},
	}

	session, err := NewSession("test-stream", remoteAddr, 12345, newMockRegistry(), codecsCfg, logger.NewLogrusAdapter(logrus.NewEntry(logrus.New())))
	require.NoError(t, err)
	defer session.Stop()

	// Start session
	session.Start()

	// Send packets with large gap (sequence reset)
	sequences := []uint16{100, 101, 102, 1000} // Large gap indicates reset

	for _, seq := range sequences {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SequenceNumber: seq,
				SSRC:           12345,
				Timestamp:      uint32(seq * 3000),
				PayloadType:    96,
			},
			Payload: []byte{0x65, 0x00, 0x00, 0x00},
		}

		session.ProcessPacket(packet)
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := session.GetStats()
	assert.Equal(t, uint64(4), stats.PacketsReceived)
	assert.Equal(t, uint64(1), stats.SequenceResets) // Large gap detected as reset
}
