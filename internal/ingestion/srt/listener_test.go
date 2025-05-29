package srt

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/logger"
)

// Mock SRT adapter for testing
type mockSRTAdapter struct {
	listeners map[string]*mockSRTListener
}

func (m *mockSRTAdapter) NewListener(address string, port int, config Config) (SRTListener, error) {
	key := fmt.Sprintf("%s:%d", address, port)
	listener := &mockSRTListener{
		address: address,
		port:    port,
		config:  config,
		sockets: make(chan *mockSRTSocket, 10),
		closed:  make(chan struct{}),
	}
	if m.listeners == nil {
		m.listeners = make(map[string]*mockSRTListener)
	}
	m.listeners[key] = listener
	return listener, nil
}

func (m *mockSRTAdapter) NewConnection(socket SRTSocket) (SRTConnection, error) {
	mockSocket, ok := socket.(*mockSRTSocket)
	if !ok {
		return nil, fmt.Errorf("invalid socket type")
	}
	return &mockSRTConnection{
		streamID: mockSocket.streamID,
		socket:   mockSocket,
	}, nil
}

// Mock SRT listener
type mockSRTListener struct {
	address  string
	port     int
	config   Config
	callback ListenCallback
	sockets  chan *mockSRTSocket
	closed   chan struct{}
	mu       sync.Mutex
}

func (m *mockSRTListener) Listen(ctx context.Context, backlog int) error {
	return nil
}

func (m *mockSRTListener) Accept() (SRTSocket, *net.UDPAddr, error) {
	select {
	case socket := <-m.sockets:
		addr := &net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 12345,
		}
		return socket, addr, nil
	case <-m.closed:
		return nil, nil, fmt.Errorf("listener closed")
	}
}

func (m *mockSRTListener) SetListenCallback(callback ListenCallback) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callback = callback
	return nil
}

func (m *mockSRTListener) Close() error {
	select {
	case <-m.closed:
		// Already closed
	default:
		close(m.closed)
	}
	return nil
}

func (m *mockSRTListener) GetPort() int {
	return m.port
}

// Helper method to add mock connections
func (m *mockSRTListener) addConnection(streamID string, shouldAccept bool) {
	socket := &mockSRTSocket{
		streamID:     streamID,
		shouldAccept: shouldAccept,
	}

	// Call callback if set
	if m.callback != nil {
		addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
		if m.callback(socket, 1, addr, streamID) {
			select {
			case m.sockets <- socket:
			case <-m.closed:
			}
		}
	} else {
		select {
		case m.sockets <- socket:
		case <-m.closed:
		}
	}
}

// Mock SRT socket
type mockSRTSocket struct {
	streamID     string
	shouldAccept bool
	rejected     bool
	reason       RejectionReason
}

func (m *mockSRTSocket) GetStreamID() string {
	return m.streamID
}

func (m *mockSRTSocket) Close() error {
	return nil
}

func (m *mockSRTSocket) SetRejectReason(reason RejectionReason) error {
	m.rejected = true
	m.reason = reason
	return nil
}

// Mock SRT connection
type mockSRTConnection struct {
	streamID string
	socket   *mockSRTSocket
	stats    ConnectionStats
}

func (m *mockSRTConnection) Read(b []byte) (int, error) {
	// Simulate reading some test data
	testData := []byte("test data")
	n := copy(b, testData)
	return n, nil
}

func (m *mockSRTConnection) Write(b []byte) (int, error) {
	return len(b), nil
}

func (m *mockSRTConnection) Close() error {
	return nil
}

func (m *mockSRTConnection) GetStreamID() string {
	return m.streamID
}

func (m *mockSRTConnection) GetStats() ConnectionStats {
	return m.stats
}

func (m *mockSRTConnection) SetMaxBW(bw int64) error {
	return nil
}

func (m *mockSRTConnection) GetMaxBW() int64 {
	return 0
}

func createTestListener(t *testing.T) (*Listener, *mockSRTAdapter) {
	cfg := &config.SRTConfig{
		Port:            30000,
		Latency:         120 * time.Millisecond,
		MaxBandwidth:    50000000,
		MaxConnections:  25,
		PeerIdleTimeout: 30 * time.Second,
	}

	codecsCfg := &config.CodecsConfig{}
	registry := &mockRegistry{}
	adapter := &mockSRTAdapter{}

	logrusLogger := logrus.New()
	logrusLogger.SetLevel(logrus.ErrorLevel) // Reduce noise in tests
	testLogger := logger.NewLogrusAdapter(logrus.NewEntry(logrusLogger))

	listener := NewListenerWithAdapter(cfg, codecsCfg, registry, adapter, testLogger)
	return listener, adapter
}

func TestListener_Creation(t *testing.T) {
	listener, _ := createTestListener(t)

	assert.NotNil(t, listener)
	assert.NotNil(t, listener.config)
	assert.NotNil(t, listener.registry)
	assert.NotNil(t, listener.logger)
	assert.NotNil(t, listener.connLimiter)
	assert.NotNil(t, listener.bandwidthManager)
	assert.NotNil(t, listener.codecDetector)
}

func TestListener_Start(t *testing.T) {
	listener, _ := createTestListener(t)

	err := listener.Start()
	require.NoError(t, err)
	defer listener.Stop()

	// Verify listener was configured
	assert.NotNil(t, listener.listener)
}

func TestListener_Stop(t *testing.T) {
	listener, _ := createTestListener(t)

	err := listener.Start()
	require.NoError(t, err)

	err = listener.Stop()
	assert.NoError(t, err)
}

func TestListener_SetHandler(t *testing.T) {
	listener, _ := createTestListener(t)

	handler := func(conn *Connection) error {
		return nil
	}

	listener.SetHandler(handler)
	assert.NotNil(t, listener.handler)
}

func TestListener_ValidateStreamID(t *testing.T) {
	listener, _ := createTestListener(t)

	tests := []struct {
		name     string
		streamID string
		valid    bool
	}{
		{"valid alphanumeric", "test123", true},
		{"valid with underscore", "test_stream", true},
		{"valid with hyphen", "test-stream", true},
		{"empty string", "", false},
		{"too long", string(make([]byte, 65)), false},
		{"invalid chars", "test@stream", false},
		{"spaces", "test stream", false},
		{"special chars", "test$stream!", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := listener.isValidStreamID(tt.streamID)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestListener_HandleIncomingConnection(t *testing.T) {
	listener, _ := createTestListener(t)

	tests := []struct {
		name         string
		streamID     string
		shouldAccept bool
		reason       RejectionReason
	}{
		{"valid stream", "test-stream", true, 0},
		{"invalid stream ID", "", false, RejectionReasonBadRequest},
		{"invalid characters", "test@stream", false, RejectionReasonBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socket := &mockSRTSocket{streamID: tt.streamID}
			addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}

			result := listener.handleIncomingConnection(socket, 1, addr, tt.streamID)
			assert.Equal(t, tt.shouldAccept, result)

			if !tt.shouldAccept {
				assert.True(t, socket.rejected)
				assert.Equal(t, tt.reason, socket.reason)
			}
		})
	}
}

func TestListener_ConnectionDuplication(t *testing.T) {
	listener, _ := createTestListener(t)

	// Add a connection to the map
	streamID := "test-stream"
	conn := &Connection{streamID: streamID}
	listener.connections.Store(streamID, conn)

	// Try to add the same stream ID again
	socket := &mockSRTSocket{streamID: streamID}
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}

	result := listener.handleIncomingConnection(socket, 1, addr, streamID)
	assert.False(t, result)
	assert.True(t, socket.rejected)
	assert.Equal(t, RejectionReasonResourceUnavailable, socket.reason)
}

func TestListener_GetActiveConnections(t *testing.T) {
	listener, _ := createTestListener(t)

	// Initially no connections
	assert.Equal(t, 0, listener.GetActiveConnections())

	// Add some connections
	for i := 0; i < 5; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		conn := &Connection{streamID: streamID}
		listener.connections.Store(streamID, conn)
	}

	assert.Equal(t, 5, listener.GetActiveConnections())
}

func TestListener_GetConnectionInfo(t *testing.T) {
	listener, _ := createTestListener(t)

	// Add test connections
	startTime := time.Now()
	for i := 0; i < 3; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		conn := &Connection{
			streamID:  streamID,
			startTime: startTime,
		}
		listener.connections.Store(streamID, conn)
	}

	info := listener.GetConnectionInfo()
	assert.Equal(t, 3, info["active_count"])

	connections, ok := info["connections"].(map[string]interface{})
	assert.True(t, ok)
	assert.Len(t, connections, 3)

	// Check a specific connection
	conn0, exists := connections["stream-0"]
	assert.True(t, exists)

	connMap, ok := conn0.(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "stream-0", connMap["stream_id"])
	assert.Equal(t, startTime, connMap["start_time"])
}
