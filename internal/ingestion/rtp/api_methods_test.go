package rtp

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

// TestListener_SetSessionHandler tests setting session handler
func TestListener_SetSessionHandler(t *testing.T) {
	cfg := &config.RTPConfig{
		ListenAddr: "127.0.0.1",
		Port:       0, // Use random port
	}
	codecsCfg := &config.CodecsConfig{}

	mockReg := &testMockRegistry{}
	logger := createTestLogger()
	listener := NewListener(cfg, codecsCfg, mockReg, logger)

	// Test setting handler
	handlerCalled := false
	handler := func(session *Session) error {
		handlerCalled = true
		return nil
	}

	listener.SetSessionHandler(handler)
	assert.NotNil(t, listener.handler)

	// Test handler is actually set by calling it
	testSession := &Session{
		ssrc:     12345,
		streamID: "test-stream",
	}

	err := listener.handler(testSession)
	assert.NoError(t, err)
	assert.True(t, handlerCalled)
}

// TestListener_StreamControlMethods tests stream control API methods
func TestListener_StreamControlMethods(t *testing.T) {
	cfg := &config.RTPConfig{
		ListenAddr: "127.0.0.1",
		Port:       0,
	}
	codecsCfg := &config.CodecsConfig{}

	mockReg := &testMockRegistry{}
	logger := createTestLogger()
	listener := NewListener(cfg, codecsCfg, mockReg, logger)

	// Create a test session properly
	testSession, err := NewSession("test-stream-123", &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5004},
		12345, mockReg, codecsCfg, logger)
	require.NoError(t, err)

	// Add session to listener
	listener.mu.Lock()
	sessionKey := fmt.Sprintf("%s:%d", testSession.remoteAddr.String(), testSession.ssrc)
	listener.sessions[sessionKey] = testSession
	listener.mu.Unlock()

	t.Run("TerminateStream - existing stream", func(t *testing.T) {
		err := listener.TerminateStream("test-stream-123")
		assert.NoError(t, err)

		// Verify session was removed
		listener.mu.Lock()
		_, exists := listener.sessions[sessionKey]
		listener.mu.Unlock()
		assert.False(t, exists)
	})

	t.Run("TerminateStream - non-existent stream", func(t *testing.T) {
		err := listener.TerminateStream("non-existent-stream")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stream non-existent-stream not found")
	})

	// Add session back for pause/resume tests
	testSession2, err := NewSession("test-stream-456", &net.UDPAddr{IP: net.ParseIP("192.168.1.2"), Port: 5004},
		67890, mockReg, codecsCfg, logger)
	require.NoError(t, err)
	sessionKey2 := fmt.Sprintf("%s:%d", testSession2.remoteAddr.String(), testSession2.ssrc)
	listener.mu.Lock()
	listener.sessions[sessionKey2] = testSession2
	listener.mu.Unlock()

	t.Run("PauseStream - existing stream", func(t *testing.T) {
		err := listener.PauseStream("test-stream-456")
		assert.NoError(t, err)

		// Verify session is still there (paused flag is internal)
		listener.mu.Lock()
		session := listener.sessions[sessionKey2]
		listener.mu.Unlock()
		assert.NotNil(t, session)
	})

	t.Run("ResumeStream - paused stream", func(t *testing.T) {
		err := listener.ResumeStream("test-stream-456")
		assert.NoError(t, err)

		// Verify session is still there
		listener.mu.Lock()
		session := listener.sessions[sessionKey2]
		listener.mu.Unlock()
		assert.NotNil(t, session)
	})

	t.Run("PauseStream - non-existent stream", func(t *testing.T) {
		err := listener.PauseStream("non-existent-stream")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stream non-existent-stream not found")
	})

	t.Run("ResumeStream - non-existent stream", func(t *testing.T) {
		err := listener.ResumeStream("non-existent-stream")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "stream non-existent-stream not found")
	})
}

// TestSession_SetNALCallback tests setting NAL callback on session
func TestSession_SetNALCallback(t *testing.T) {
	mockReg := &testMockRegistry{}
	logger := createTestLogger()

	session, err := NewSession("test-stream", &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5004},
		12345, mockReg, &config.CodecsConfig{}, logger)
	require.NoError(t, err)

	// Test setting NAL callback
	callbackCalled := false
	callback := func(nalUnits [][]byte) error {
		callbackCalled = true
		return nil
	}

	session.SetNALCallback(callback)
	assert.NotNil(t, session.nalCallback)

	// Test callback is actually set by calling it
	testNALUnits := [][]byte{{0x67, 0x42}, {0x68, 0x43}} // SPS, PPS
	err = session.nalCallback(testNALUnits)
	assert.NoError(t, err)
	assert.True(t, callbackCalled)
}

// TestSession_GetterMethods tests session getter methods
func TestSession_GetterMethods(t *testing.T) {
	mockReg := &testMockRegistry{}
	logger := createTestLogger()

	session, err := NewSession("test-stream", &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5004},
		12345, mockReg, &config.CodecsConfig{}, logger)
	require.NoError(t, err)

	// Test with default values (can't set private fields directly)

	t.Run("GetPayloadType", func(t *testing.T) {
		pt := session.GetPayloadType()
		// Default/unset payload type
		assert.Equal(t, uint8(0), pt)
	})

	t.Run("GetMediaFormat", func(t *testing.T) {
		format := session.GetMediaFormat()
		// Default/unset format
		assert.Equal(t, "", format)
	})

	t.Run("GetEncodingName", func(t *testing.T) {
		encoding := session.GetEncodingName()
		// Default/unset encoding
		assert.Equal(t, "", encoding)
	})

	t.Run("GetClockRate", func(t *testing.T) {
		rate := session.GetClockRate()
		// Returns default audio clock rate for unknown codec with payload type 0
		assert.Equal(t, uint32(8000), rate)
	})
}

// TestSession_SetSDPInfo tests setting SDP information
func TestSession_SetSDPInfo(t *testing.T) {
	mockReg := &testMockRegistry{}
	logger := createTestLogger()

	session, err := NewSession("test-stream", &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5004},
		12345, mockReg, &config.CodecsConfig{}, logger)
	require.NoError(t, err)

	// Test setting SDP info
	session.SetSDPInfo("H265", "H265", 90000)

	// Verify values were set through getters
	assert.Equal(t, "H265", session.GetMediaFormat())
	assert.Equal(t, "H265", session.GetEncodingName())
	assert.Equal(t, uint32(90000), session.GetClockRate())
}

// TestSession_ProcessRTCPPacket tests RTCP packet processing
func TestSession_ProcessRTCPPacket(t *testing.T) {
	mockReg := &testMockRegistry{}
	logger := createTestLogger()

	session, err := NewSession("test-stream", &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5004},
		12345, mockReg, &config.CodecsConfig{}, logger)
	require.NoError(t, err)

	// Test with empty RTCP packet (should not panic)
	emptyPacket := []byte{}
	session.ProcessRTCPPacket(emptyPacket)

	// Test with minimal RTCP packet structure
	// RTCP SR packet: Version(2) + Padding(0) + RC(0) + PT(200) + Length(6) + SSRC
	rtcpPacket := []byte{
		0x80,       // Version=2, Padding=0, RC=0
		200,        // PT=200 (SR)
		0x00, 0x06, // Length=6 words
		0x00, 0x00, 0x30, 0x39, // SSRC=12345
		// Minimal SR fields (simplified for test)
		0x00, 0x00, 0x00, 0x00, // NTP timestamp MSW
		0x00, 0x00, 0x00, 0x00, // NTP timestamp LSW
		0x00, 0x00, 0x00, 0x00, // RTP timestamp
		0x00, 0x00, 0x00, 0x00, // Packet count
		0x00, 0x00, 0x00, 0x00, // Octet count
	}

	// Should not panic
	session.ProcessRTCPPacket(rtcpPacket)
}

// TestListener_MinFunction tests the min utility function
func TestListener_MinFunction(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int
		expected int
	}{
		{
			name:     "a less than b",
			a:        5,
			b:        10,
			expected: 5,
		},
		{
			name:     "b less than a",
			a:        15,
			b:        8,
			expected: 8,
		},
		{
			name:     "a equals b",
			a:        7,
			b:        7,
			expected: 7,
		},
		{
			name:     "negative numbers",
			a:        -5,
			b:        -2,
			expected: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := min(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSession_StartStop tests the session lifecycle
func TestSession_StartStop(t *testing.T) {
	mockReg := &testMockRegistry{}
	logger := createTestLogger()

	session, err := NewSession("test-stream", &net.UDPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5004},
		12345, mockReg, &config.CodecsConfig{}, logger)
	require.NoError(t, err)

	// Test start/stop lifecycle (should not panic)
	session.Start()
	session.Stop()

	// Test passes if no panic occurs
	assert.True(t, true)
}

// Helper functions for tests

// testMockRegistry is a simple mock for testing
type testMockRegistry struct{}

func (m *testMockRegistry) Register(ctx context.Context, stream *registry.Stream) error {
	return nil
}

func (m *testMockRegistry) Unregister(ctx context.Context, streamID string) error {
	return nil
}

func (m *testMockRegistry) Get(ctx context.Context, streamID string) (*registry.Stream, error) {
	return nil, nil
}

func (m *testMockRegistry) List(ctx context.Context) ([]*registry.Stream, error) {
	return []*registry.Stream{}, nil
}

func (m *testMockRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	return nil
}

func (m *testMockRegistry) UpdateStatus(ctx context.Context, streamID string, status registry.StreamStatus) error {
	return nil
}

func (m *testMockRegistry) UpdateStats(ctx context.Context, streamID string, stats *registry.StreamStats) error {
	return nil
}

func (m *testMockRegistry) Delete(ctx context.Context, streamID string) error {
	return nil
}

func (m *testMockRegistry) Update(ctx context.Context, stream *registry.Stream) error {
	return nil
}

func (m *testMockRegistry) Close() error {
	return nil
}

// createTestLogger creates a test logger
func createTestLogger() logger.Logger {
	// Create a logrus logger with error level to minimize noise in tests
	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.ErrorLevel)
	return logger.NewLogrusAdapter(logrus.NewEntry(logrusLogger))
}
