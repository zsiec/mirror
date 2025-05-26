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

// TestP2_11_HandlerGoroutineLeak demonstrates that session handler goroutines
// don't get cleaned up when the listener stops
func TestP2_11_HandlerGoroutineLeak(t *testing.T) {
	// This test demonstrates the goroutine leak in listener.go line 281
	// where a goroutine is started for each session handler but doesn't
	// check for context cancellation

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

	// Set a handler that simulates long-running work
	// This is the problematic pattern - handler doesn't check context
	listener.handler = func(session *Session) error {
		atomic.AddInt32(&activeHandlers, 1)
		handlerStarted <- struct{}{}

		defer atomic.AddInt32(&activeHandlers, -1)

		// BUG: Handler doesn't check session.ctx.Done()
		// This simulates a handler that runs for a long time
		// without checking if it should stop
		select {
		case <-time.After(10 * time.Second):
			return nil
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

	// Give a moment for cleanup
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

	// This demonstrates the bug - handlers are still running
	assert.Greater(t, activeCount, int32(0), "Handlers should still be running (bug)")
	assert.Greater(t, leaked, 0, "Should have leaked goroutines (bug)")
}
