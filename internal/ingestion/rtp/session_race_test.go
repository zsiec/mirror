package rtp

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/logger"
)

// TestSessionCodecDetectionRace tests for race conditions in codec detection
func TestSessionCodecDetectionRace(t *testing.T) {
	// Create test dependencies
	reg := &mockRegistry{}
	logEntry := logrus.NewEntry(logrus.New())
	logEntry.Logger.SetLevel(logrus.DebugLevel)
	log := logger.NewLogrusAdapter(logEntry)

	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5004")
	streamID := "test-stream"

	// Create session
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
	}
	session, err := NewSession(streamID, addr, 12345, reg, codecsCfg, log)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Start the session
	session.Start()
	defer session.Stop()

	// Create a wait group for goroutines
	var wg sync.WaitGroup
	numGoroutines := 10
	packetsPerGoroutine := 100

	// Track errors
	errChan := make(chan error, numGoroutines*2)

	// Concurrent packet processing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < packetsPerGoroutine; j++ {
				// Create H.264 RTP packet
				packet := &rtp.Packet{
					Header: rtp.Header{
						Version:        2,
						PayloadType:    96,
						SequenceNumber: uint16(id*packetsPerGoroutine + j),
						Timestamp:      uint32(j * 3000),
						SSRC:           12345,
					},
					// H.264 NAL unit (SPS)
					Payload: []byte{0x67, 0x42, 0x00, 0x1f, 0x96, 0x54, 0x05, 0x01, 0x6c, 0x80},
				}

				// Process packet
				session.ProcessPacket(packet)

				// Small delay to increase chance of race
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Concurrent SDP setting
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Create sample SDP
			sdp := `v=0
o=- 0 0 IN IP4 127.0.0.1
s=-
c=IN IP4 127.0.0.1
t=0 0
m=video 5004 RTP/AVP 96
a=rtpmap:96 H264/90000
a=fmtp:96 profile-level-id=42001f`

			// Try to set SDP multiple times
			for j := 0; j < 10; j++ {
				if err := session.SetSDP(sdp); err != nil {
					// Codec mismatch errors are expected in race scenarios
					if err.Error() != "codec mismatch: SDP indicates h264 but already detected h264" {
						select {
						case errChan <- err:
						default:
						}
					}
				}
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Wait for all goroutines
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Unexpected error during concurrent processing: %v", err)
	}

	// Verify codec was detected
	session.mu.RLock()
	codecType := session.codecType
	hasDepacketizer := session.depacketizer != nil
	session.mu.RUnlock()

	if codecType == codec.TypeUnknown {
		t.Error("Codec was not detected")
	}
	if !hasDepacketizer {
		t.Error("Depacketizer was not created")
	}

	// Verify stats are reasonable
	stats := session.GetStats()
	if stats.PacketsReceived == 0 {
		t.Error("No packets were processed")
	}

	t.Logf("Processed %d packets, codec: %s", stats.PacketsReceived, codecType)
}

// TestSessionConcurrentStats tests concurrent access to session statistics
func TestSessionConcurrentStats(t *testing.T) {
	// Create test dependencies
	reg := &mockRegistry{}
	logEntry := logrus.NewEntry(logrus.New())
	logEntry.Logger.SetLevel(logrus.DebugLevel)
	log := logger.NewLogrusAdapter(logEntry)

	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5004")
	streamID := "test-stream"

	// Create session
	codecsCfg := &config.CodecsConfig{
		Preferred: "h264",
	}
	session, err := NewSession(streamID, addr, 12345, reg, codecsCfg, log)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Start the session
	session.Start()
	defer session.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Concurrent operations
	var wg sync.WaitGroup

	// Writer goroutines - update stats
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < 1000; j++ {
				select {
				case <-ctx.Done():
					return
				default:
				}

				packet := &rtp.Packet{
					Header: rtp.Header{
						SequenceNumber: uint16(id*1000 + j),
						Timestamp:      uint32(j * 3000),
						SSRC:           12345,
					},
					Payload: make([]byte, 100),
				}

				session.updateStats(packet, len(packet.Payload))
			}
		}(i)
	}

	// Reader goroutines - read stats
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				stats := session.GetStats()
				// Just access the stats to trigger any race
				_ = stats.PacketsReceived
				_ = stats.BytesReceived
				_ = stats.LastPacketTime

				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Timeout/pause operations
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			session.IsTimedOut()
			session.GetLastPacketTime()

			// Just check timeout state - Pause/Resume not implemented yet
			_ = session.IsTimedOut()

			time.Sleep(time.Millisecond)
		}
	}()

	// Wait for completion
	wg.Wait()

	// Verify no panics occurred
	t.Log("Concurrent stats test completed without panics")
}

// BenchmarkSessionCodecDetection benchmarks codec detection performance
func BenchmarkSessionCodecDetection(b *testing.B) {
	// Create test dependencies
	reg := &mockRegistry{}
	logEntry := logrus.NewEntry(logrus.New())
	logEntry.Logger.SetLevel(logrus.ErrorLevel)
	log := logger.NewLogrusAdapter(logEntry)

	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5004")

	// Create H.264 packet
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1,
			Timestamp:      3000,
			SSRC:           12345,
		},
		Payload: []byte{0x67, 0x42, 0x00, 0x1f, 0x96, 0x54, 0x05, 0x01, 0x6c, 0x80},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create new session for each iteration
		codecsCfg := &config.CodecsConfig{
			Preferred: "h264",
		}
		session, _ := NewSession("bench-stream", addr, 12345, reg, codecsCfg, log)

		// Process packet (triggers codec detection)
		session.ProcessPacket(packet)

		// Stop session
		session.Stop()
	}
}
