package rtp

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

// TestRTPSession_CodecDetectionStateMachine tests the codec detection state machine for race conditions
func TestRTPSession_CodecDetectionStateMachine(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}

	// Use Redis registry with test client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})
	redisRegistry := registry.NewRedisRegistry(redisClient, logrus.New())
	defer redisRegistry.Close()

	remoteAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:12345")
	session, err := NewSession("test-stream-state", remoteAddr, 12345, redisRegistry, codecsCfg, logger)
	require.NoError(t, err)
	defer session.Stop()

	// Test initial state
	assert.Equal(t, CodecStateUnknown, session.getCodecState())

	// Test state transitions under concurrent access
	t.Run("Concurrent State Access", func(t *testing.T) {
		var wg sync.WaitGroup
		const numReaders = 10
		const numWriters = 3
		const duration = 100 * time.Millisecond

		stopChan := make(chan struct{})
		var inconsistencies int32
		var stateReads int32

		// Start state readers
		for i := 0; i < numReaders; i++ {
			wg.Add(1)
			go func(readerID int) {
				defer wg.Done()

				for {
					select {
					case <-stopChan:
						return
					default:
						// Read state - each individual read should be atomic
						state := session.getCodecState()

						// Verify state is valid
						if state < CodecStateUnknown || state > CodecStateTimeout {
							atomic.AddInt32(&inconsistencies, 1)
						}

						atomic.AddInt32(&stateReads, 1)
						time.Sleep(time.Microsecond)
					}
				}
			}(i)
		}

		// Start state writers (simulating codec detection attempts)
		for i := 0; i < numWriters; i++ {
			wg.Add(1)
			go func(writerID int) {
				defer wg.Done()

				for {
					select {
					case <-stopChan:
						return
					default:
						// Simulate detection state transitions
						session.codecStateMu.Lock()
						if session.codecState == CodecStateUnknown {
							session.codecState = CodecStateDetecting
							session.detectionCond.Broadcast()
							session.codecStateMu.Unlock()

							// Simulate detection work
							time.Sleep(time.Millisecond)

							session.codecStateMu.Lock()
							session.codecState = CodecStateUnknown // Reset for next attempt
							session.detectionCond.Broadcast()
						}
						session.codecStateMu.Unlock()

						time.Sleep(time.Microsecond)
					}
				}
			}(i)
		}

		// Run test for specified duration
		time.Sleep(duration)
		close(stopChan)
		wg.Wait()

		t.Logf("Total state reads: %d, inconsistencies: %d",
			atomic.LoadInt32(&stateReads), atomic.LoadInt32(&inconsistencies))

		assert.Equal(t, int32(0), atomic.LoadInt32(&inconsistencies),
			"Should have no state reading inconsistencies")
	})
}

// TestRTPSession_CodecDetectionSDP tests SDP-based codec detection races
func TestRTPSession_CodecDetectionSDP(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}

	// Use Redis registry with test client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})
	redisRegistry := registry.NewRedisRegistry(redisClient, logrus.New())
	defer redisRegistry.Close()

	remoteAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:12345")
	session, err := NewSession("test-stream-sdp", remoteAddr, 12345, redisRegistry, codecsCfg, logger)
	require.NoError(t, err)
	defer session.Stop()

	session.Start()

	// Test concurrent SDP processing
	t.Run("Concurrent SDP Processing", func(t *testing.T) {
		var wg sync.WaitGroup
		const numGoroutines = 5
		var errors int32
		var successes int32

		sdp := `v=0
o=- 1234567 1234567 IN IP4 192.168.1.1
s=-
t=0 0
m=video 5004 RTP/AVP 96
a=rtpmap:96 H264/90000
a=fmtp:96 profile-level-id=42001e`

		// Process SDP concurrently
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()

				// Add small random delay to increase race probability
				time.Sleep(time.Duration(goroutineID) * time.Microsecond)

				if err := session.SetSDP(sdp); err != nil {
					// Check if it's a codec mismatch (expected for race conditions)
					if err.Error() != "codec mismatch: SDP indicates H264 but already detected H264" {
						atomic.AddInt32(&errors, 1)
						t.Errorf("Goroutine %d: unexpected error: %v", goroutineID, err)
					}
				} else {
					atomic.AddInt32(&successes, 1)
				}
			}(i)
		}

		wg.Wait()

		t.Logf("Successes: %d, Errors: %d",
			atomic.LoadInt32(&successes), atomic.LoadInt32(&errors))

		// Should have no unexpected errors from race conditions
		assert.Equal(t, int32(0), atomic.LoadInt32(&errors), "Should have no unexpected SDP processing errors")

		// At least one should succeed
		assert.Greater(t, atomic.LoadInt32(&successes), int32(0), "At least one SDP processing should succeed")

		// Final state should be detected
		assert.Equal(t, CodecStateDetected, session.getCodecState(), "Codec should be detected after SDP")

		session.mu.RLock()
		assert.Equal(t, codec.TypeH264, session.codecType)
		assert.NotNil(t, session.depacketizer)
		session.mu.RUnlock()
	})
}

// TestRTPSession_CodecDetectionConditionVariable tests the condition variable behavior
func TestRTPSession_CodecDetectionConditionVariable(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}

	// Use Redis registry with test client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})
	redisRegistry := registry.NewRedisRegistry(redisClient, logrus.New())
	defer redisRegistry.Close()

	remoteAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:12345")
	session, err := NewSession("test-stream-cond", remoteAddr, 12345, redisRegistry, codecsCfg, logger)
	require.NoError(t, err)
	defer session.Stop()

	// Test condition variable coordination
	t.Run("Condition Variable Coordination", func(t *testing.T) {
		var wg sync.WaitGroup
		const numWaiters = 5
		var notifiedCount int32

		// Set state to detecting to test condition variable
		session.codecStateMu.Lock()
		session.codecState = CodecStateDetecting
		session.codecStateMu.Unlock()

		// Start waiters
		for i := 0; i < numWaiters; i++ {
			wg.Add(1)
			go func(waiterID int) {
				defer wg.Done()

				session.codecStateMu.Lock()
				for session.codecState == CodecStateDetecting {
					session.detectionCond.Wait()
				}
				session.codecStateMu.Unlock()

				atomic.AddInt32(&notifiedCount, 1)
			}(i)
		}

		// Wait for waiters to start waiting
		time.Sleep(50 * time.Millisecond)

		// Signal completion
		session.codecStateMu.Lock()
		session.codecState = CodecStateDetected
		session.detectionCond.Broadcast()
		session.codecStateMu.Unlock()

		wg.Wait()

		assert.Equal(t, int32(numWaiters), atomic.LoadInt32(&notifiedCount),
			"All waiters should be notified")
	})
}

// TestRTPSession_CodecDetectionTimeout tests timeout handling
func TestRTPSession_CodecDetectionTimeout(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}

	// Use Redis registry with test client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})
	redisRegistry := registry.NewRedisRegistry(redisClient, logrus.New())
	defer redisRegistry.Close()

	remoteAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:12345")
	session, err := NewSession("test-stream-timeout", remoteAddr, 12345, redisRegistry, codecsCfg, logger)
	require.NoError(t, err)
	defer session.Stop()

	session.Start()

	// Simulate codec detection timeout by setting first packet time far in the past
	session.mu.Lock()
	session.firstPacketTime = time.Now().Add(-15 * time.Second)
	session.mu.Unlock()

	// Create a fake packet that would normally trigger detection
	fakePacket := &rtp.Packet{
		Header: rtp.Header{
			PayloadType:    96,
			SequenceNumber: 1000,
			Timestamp:      12345,
			SSRC:           12345,
		},
		Payload: []byte{0x7C, 0x89, 0x01, 0x23, 0x45},
	}

	// Process packet should trigger timeout
	result := session.handleCodecDetection(fakePacket)
	assert.False(t, result, "Should return false for timeout")

	// Wait a moment for async timeout processing
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, CodecStateTimeout, session.getCodecState(), "Should be in timeout state")
	assert.Equal(t, int32(1), atomic.LoadInt32(&session.paused), "Session should be paused")
}
