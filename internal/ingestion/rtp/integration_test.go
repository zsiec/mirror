//go:build integration
// +build integration

package rtp_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/buffer"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	rtpListener "github.com/zsiec/mirror/internal/ingestion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/testdata"
	"github.com/zsiec/mirror/tests"
)

func TestRTPIntegration_StreamIngestion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	ctx := context.Background()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	// Create test Redis registry
	redisClient := tests.SetupTestRedis(t)
	reg := registry.NewRedisRegistry(redisClient, logger)

	// Create buffer pool
	bufferPool := buffer.NewBufferPool(1024*1024, 10, logger) // 1MB buffers

	// Create RTP listener config
	cfg := &config.RTPConfig{
		Enabled:        true,
		ListenAddr:     "127.0.0.1",
		Port:           15004,
		RTCPPort:       15005,
		BufferSize:     2097152, // 2MB
		SessionTimeout: 30 * time.Second,
	}

	// Create and start listener
	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}
	listener := rtpListener.NewListener(cfg, codecsCfg, reg, bufferPool, logger)
	err := listener.Start()
	require.NoError(t, err)
	defer listener.Stop()

	// Give listener time to start
	time.Sleep(100 * time.Millisecond)

	t.Run("single RTP stream", func(t *testing.T) {
		// Create UDP connection for RTP
		serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port))
		require.NoError(t, err)

		conn, err := net.DialUDP("udp", nil, serverAddr)
		require.NoError(t, err)
		defer conn.Close()

		// Generate test RTP packets
		ssrc := uint32(12345)
		packets := testdata.GenerateTestRTPPackets(1920, 1080, 30, 1000, ssrc) // 1 second

		// Send packets
		for _, packet := range packets {
			data, err := packet.Marshal()
			require.NoError(t, err)

			_, err = conn.Write(data)
			require.NoError(t, err)

			time.Sleep(5 * time.Millisecond) // Simulate real-time streaming
		}

		// Wait for processing
		time.Sleep(200 * time.Millisecond)

		// Verify stream was registered
		streams, err := reg.List(ctx)
		require.NoError(t, err)
		require.Greater(t, len(streams), 0)

		// Find our stream
		var foundStream *registry.Stream
		for _, s := range streams {
			if s.Type == registry.StreamTypeRTP {
				foundStream = s
				break
			}
		}
		require.NotNil(t, foundStream)
		assert.Equal(t, registry.StatusActive, foundStream.Status)

		// Verify data was buffered
		buf := bufferPool.Get(foundStream.ID)
		require.NotNil(t, buf)
		stats := buf.Stats()
		assert.Greater(t, stats.Written, int64(0))
	})

	t.Run("multiple SSRC streams", func(t *testing.T) {
		serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port))
		require.NoError(t, err)

		// Create multiple connections with different SSRCs
		numStreams := 3
		for i := 0; i < numStreams; i++ {
			conn, err := net.DialUDP("udp", nil, serverAddr)
			require.NoError(t, err)
			defer conn.Close()

			// Each stream has unique SSRC
			ssrc := uint32(20000 + i)
			packets := testdata.GenerateTestRTPPackets(1280, 720, 25, 500, ssrc) // 500ms

			// Send packets
			go func(packets []*rtp.Packet) {
				for _, packet := range packets {
					data, _ := packet.Marshal()
					conn.Write(data)
					time.Sleep(10 * time.Millisecond)
				}
			}(packets)
		}

		// Wait for streams to register
		time.Sleep(1 * time.Second)

		// Verify multiple streams
		streams, err := reg.List(ctx)
		require.NoError(t, err)

		rtpStreams := 0
		for _, s := range streams {
			if s.Type == registry.StreamTypeRTP {
				rtpStreams++
			}
		}
		assert.GreaterOrEqual(t, rtpStreams, numStreams)

		// Check listener sessions
		assert.GreaterOrEqual(t, listener.GetActiveSessions(), numStreams)
	})

	t.Run("RTCP feedback", func(t *testing.T) {
		// Create RTP connection
		rtpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port))
		require.NoError(t, err)

		rtpConn, err := net.DialUDP("udp", nil, rtpAddr)
		require.NoError(t, err)
		defer rtpConn.Close()

		// Create RTCP connection
		rtcpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.RTCPPort))
		require.NoError(t, err)

		rtcpConn, err := net.DialUDP("udp", nil, rtcpAddr)
		require.NoError(t, err)
		defer rtcpConn.Close()

		// Send RTP packets
		ssrc := uint32(30000)
		packets := testdata.GenerateTestRTPPackets(1920, 1080, 30, 500, ssrc)

		for _, packet := range packets {
			data, _ := packet.Marshal()
			rtpConn.Write(data)
			time.Sleep(5 * time.Millisecond)
		}

		// Send RTCP Sender Report
		// This is a simplified SR packet
		sr := []byte{
			0x80, 0xC8, // V=2, P=0, RC=0, PT=200 (SR)
			0x00, 0x06, // Length = 6 (in 32-bit words minus 1)
			// SSRC
			byte(ssrc >> 24), byte(ssrc >> 16), byte(ssrc >> 8), byte(ssrc),
			// NTP timestamp (8 bytes) - simplified
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			// RTP timestamp
			0x00, 0x00, 0x00, 0x00,
			// Packet count
			0x00, 0x00, 0x00, byte(len(packets)),
			// Octet count
			0x00, 0x00, 0x10, 0x00,
		}

		_, err = rtcpConn.Write(sr)
		assert.NoError(t, err)

		// The listener should process RTCP without errors
		time.Sleep(100 * time.Millisecond)

		// Verify stream is still active
		streams, err := reg.List(ctx)
		require.NoError(t, err)
		assert.Greater(t, len(streams), 0)
	})

	t.Run("session timeout", func(t *testing.T) {
		// Create a new listener with fast timeouts for testing
		testCfg := &config.RTPConfig{
			Enabled:        true,
			ListenAddr:     "127.0.0.1",
			Port:           15008, // Different port to avoid conflicts
			RTCPPort:       15009,
			BufferSize:     2097152,
			SessionTimeout: 1 * time.Second, // Fast timeout for testing
		}

		codecsCfg := &config.CodecsConfig{
			Supported: []string{"h264", "hevc"},
			Preferred: "hevc",
		}
		testListener := rtpListener.NewListener(testCfg, codecsCfg, reg, bufferPool, logger)
		testListener.SetTestTimeouts(500*time.Millisecond, 1*time.Second) // Cleanup every 500ms, timeout after 1s

		err := testListener.Start()
		require.NoError(t, err)
		defer testListener.Stop()

		time.Sleep(100 * time.Millisecond)

		serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", testCfg.ListenAddr, testCfg.Port))
		require.NoError(t, err)

		conn, err := net.DialUDP("udp", nil, serverAddr)
		require.NoError(t, err)
		defer conn.Close()

		// Send a few packets
		ssrc := uint32(40000)
		packets := testdata.GenerateTestRTPPackets(640, 480, 15, 100, ssrc) // 100ms

		for _, packet := range packets {
			data, _ := packet.Marshal()
			conn.Write(data)
			time.Sleep(10 * time.Millisecond)
		}

		// Wait for session to be created
		time.Sleep(200 * time.Millisecond)

		// Verify session exists
		sessionsBefore := testListener.GetActiveSessions()
		assert.Greater(t, sessionsBefore, 0)

		// Stop sending and wait for timeout
		// With 1s timeout and 500ms cleanup interval, max wait is 1.5s
		time.Sleep(2 * time.Second)

		// Session should be cleaned up
		sessionsAfter := testListener.GetActiveSessions()
		assert.Less(t, sessionsAfter, sessionsBefore)
	})

	t.Run("packet loss simulation", func(t *testing.T) {
		serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port))
		require.NoError(t, err)

		conn, err := net.DialUDP("udp", nil, serverAddr)
		require.NoError(t, err)
		defer conn.Close()

		// Generate packets
		ssrc := uint32(50000)
		packets := testdata.GenerateTestRTPPackets(1920, 1080, 30, 1000, ssrc)

		// Send packets with some drops
		dropped := 0
		for i, packet := range packets {
			// Simulate 5% packet loss
			if i%20 == 0 && i > 0 {
				dropped++
				continue
			}

			data, _ := packet.Marshal()
			conn.Write(data)
			time.Sleep(5 * time.Millisecond)
		}

		// Wait for processing and stats update
		time.Sleep(1 * time.Second)

		// Find stream and check packet loss stats
		streams, err := reg.List(ctx)
		require.NoError(t, err)

		var stream *registry.Stream
		for _, s := range streams {
			if s.Type == registry.StreamTypeRTP {
				stream = s
				break
			}
		}
		require.NotNil(t, stream)

		// Log the stats
		t.Logf("Dropped %d packets, detected loss: %d", dropped, stream.PacketsLost)

		// Should either detect packet loss OR have received fewer packets than sent
		totalSent := len(packets)
		if stream.PacketsLost == 0 {
			// If no loss detected via sequence numbers, check if we received fewer packets
			assert.Less(t, stream.PacketsReceived, int64(totalSent))
		}
	})
}

func TestRTPIntegration_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	logger := logrus.New()

	// Create test Redis registry
	redisClient := tests.SetupTestRedis(t)
	reg := registry.NewRedisRegistry(redisClient, logger)

	// Create buffer pool
	bufferPool := buffer.NewBufferPool(1024*1024, 10, logger)

	// Create RTP listener
	cfg := &config.RTPConfig{
		Enabled:        true,
		ListenAddr:     "127.0.0.1",
		Port:           15006,
		RTCPPort:       15007,
		BufferSize:     2097152,
		SessionTimeout: 30 * time.Second,
	}

	codecsCfg := &config.CodecsConfig{
		Supported: []string{"h264", "hevc"},
		Preferred: "hevc",
	}
	listener := rtpListener.NewListener(cfg, codecsCfg, reg, bufferPool, logger)
	err := listener.Start()
	require.NoError(t, err)
	defer listener.Stop()

	time.Sleep(100 * time.Millisecond)

	t.Run("high bitrate stream", func(t *testing.T) {
		serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.ListenAddr, cfg.Port))
		require.NoError(t, err)

		conn, err := net.DialUDP("udp", nil, serverAddr)
		require.NoError(t, err)
		defer conn.Close()

		// Generate high bitrate stream (50 Mbps)
		ssrc := uint32(60000)
		duration := 2000 // 2 seconds
		fps := 60        // High frame rate

		// Calculate packet timing for 50 Mbps
		targetBitrate := 50_000_000 // 50 Mbps
		packetSize := 1400          // bytes
		packetsPerSecond := targetBitrate / 8 / packetSize
		packetInterval := time.Second / time.Duration(packetsPerSecond)

		// Generate packets
		packets := testdata.GenerateTestRTPPackets(3840, 2160, fps, duration, ssrc) // 4K

		// Send at high rate
		start := time.Now()
		bytesSent := 0

		for _, packet := range packets {
			data, _ := packet.Marshal()
			n, err := conn.Write(data)
			require.NoError(t, err)
			bytesSent += n

			time.Sleep(packetInterval)
		}

		elapsed := time.Since(start)
		actualBitrate := float64(bytesSent*8) / elapsed.Seconds()

		t.Logf("Sent %d bytes in %v, bitrate: %.2f Mbps",
			bytesSent, elapsed, actualBitrate/1_000_000)

		// Verify stream handled the load
		time.Sleep(500 * time.Millisecond)
		sessionStats := listener.GetSessionStats()
		assert.Greater(t, len(sessionStats), 0)

		for _, stats := range sessionStats {
			assert.Greater(t, stats.BytesReceived, uint64(0))
			t.Logf("Session received %d bytes, %d packets",
				stats.BytesReceived, stats.PacketsReceived)
		}
	})
}
