package rtp

import (
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/logger"
)

// TestP2_11_HandlerGoroutineLeakFixed verifies that handler goroutines
// are properly cleaned up when the listener stops
func TestP2_11_HandlerGoroutineLeakFixed(t *testing.T) {
	// This test verifies the fix for the goroutine leak
	// Handler goroutines should be cancelled when listener stops

	// Track goroutines
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutines: %d", initialGoroutines)

	// Create test config
	cfg := &config.RTPConfig{
		ListenAddr:     "127.0.0.1",
		Port:           0, // Random port
		SessionTimeout: 5 * time.Second,
	}

	codecsCfg := &config.CodecsConfig{}
	reg := &mockRegistry{}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	// Create listener
	listener := NewListener(cfg, codecsCfg, reg, log)
	listener.SetTestTimeouts(100*time.Millisecond, 500*time.Millisecond)

	// Track handler goroutines
	var activeHandlers int32
	handlerStarted := make(chan struct{}, 10)
	handlerStopped := make(chan struct{}, 10)

	// Set a handler that simulates long-running work but can be cancelled
	listener.handler = func(session *Session) error {
		atomic.AddInt32(&activeHandlers, 1)
		handlerStarted <- struct{}{}

		defer func() {
			atomic.AddInt32(&activeHandlers, -1)
			handlerStopped <- struct{}{}
		}()

		// Handler that runs for a long time but can be interrupted
		// In real handlers, they should check session.ctx periodically
		select {
		case <-time.After(10 * time.Second):
			return nil
		case <-session.ctx.Done():
			// Session context cancelled
			return session.ctx.Err()
		}
	}

	// Start listener
	err := listener.Start()
	require.NoError(t, err)

	// Get the actual port
	rtpAddr := listener.rtpConn.LocalAddr().(*net.UDPAddr)

	// Create test sessions
	createSession := func(ssrc uint32) {
		conn, err := net.Dial("udp", rtpAddr.String())
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a few RTP packets to create a session
		for i := 0; i < 3; i++ {
			packet := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    96,
					SSRC:           ssrc,
					Timestamp:      uint32(i * 3000),
					SequenceNumber: uint16(i),
				},
				Payload: []byte("test data"),
			}

			data, _ := packet.Marshal()
			conn.Write(data)
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Create 3 sessions
	for i := 0; i < 3; i++ {
		go createSession(uint32(1000 + i))
	}

	// Wait for handlers to start
	for i := 0; i < 3; i++ {
		select {
		case <-handlerStarted:
			// Good
		case <-time.After(2 * time.Second):
			t.Fatalf("Handler %d didn't start", i)
		}
	}

	activeCount := atomic.LoadInt32(&activeHandlers)
	t.Logf("Active handlers before stop: %d", activeCount)
	assert.Equal(t, int32(3), activeCount)

	// Stop the listener
	err = listener.Stop()
	require.NoError(t, err)

	// Wait for handlers to stop (with timeout)
	stopped := 0
	timeout := time.After(2 * time.Second)
	for stopped < 3 {
		select {
		case <-handlerStopped:
			stopped++
		case <-timeout:
			t.Logf("Only %d handlers stopped in time", stopped)
			break
		}
	}

	// Give a moment for final cleanup
	time.Sleep(200 * time.Millisecond)

	// Check goroutines
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	afterGoroutines := runtime.NumGoroutine()

	activeCount = atomic.LoadInt32(&activeHandlers)
	t.Logf("Active handlers after stop: %d", activeCount)
	t.Logf("Goroutines after stop: %d", afterGoroutines)

	leaked := afterGoroutines - initialGoroutines
	t.Logf("Leaked goroutines: %d", leaked)

	// After the fix, handlers should be cancelled
	assert.Equal(t, int32(0), activeCount, "All handlers should be stopped")
	assert.LessOrEqual(t, leaked, 2, "Should have minimal or no goroutine leak")
}
