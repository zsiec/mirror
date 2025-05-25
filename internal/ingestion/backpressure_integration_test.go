package ingestion

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/buffer"
	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/queue"
)

// MockStreamConnection simulates a stream connection for testing
type MockStreamConnection struct {
	streamID        string
	data            chan []byte
	closed          int32
	bytesRead       int64
	readErrors      int32
	maxBandwidth    int64
	bandwidthMu     sync.RWMutex
	backpressureLog []int64
}

func NewMockStreamConnection(streamID string) *MockStreamConnection {
	return &MockStreamConnection{
		streamID:        streamID,
		data:            make(chan []byte, 100),
		maxBandwidth:    50 * 1024 * 1024, // 50 Mbps default
		backpressureLog: make([]int64, 0),
	}
}

func (m *MockStreamConnection) GetStreamID() string {
	return m.streamID
}

func (m *MockStreamConnection) Read(p []byte) (n int, err error) {
	if atomic.LoadInt32(&m.closed) == 1 {
		return 0, io.EOF
	}

	select {
	case data := <-m.data:
		n = copy(p, data)
		atomic.AddInt64(&m.bytesRead, int64(n))
		return n, nil
	case <-time.After(100 * time.Millisecond):
		return 0, nil
	}
}

func (m *MockStreamConnection) Close() error {
	atomic.StoreInt32(&m.closed, 1)
	close(m.data)
	return nil
}

func (m *MockStreamConnection) GetMaxBW() int64 {
	m.bandwidthMu.RLock()
	defer m.bandwidthMu.RUnlock()
	return m.maxBandwidth
}

func (m *MockStreamConnection) SetMaxBW(bw int64) error {
	m.bandwidthMu.Lock()
	defer m.bandwidthMu.Unlock()
	m.maxBandwidth = bw
	m.backpressureLog = append(m.backpressureLog, bw)
	return nil
}

func (m *MockStreamConnection) GetBitrate() int64 {
	return atomic.LoadInt64(&m.bytesRead) * 8 // bits
}

func (m *MockStreamConnection) GetSSRC() uint32 {
	return 12345
}

func (m *MockStreamConnection) SendRTCP(pkt rtcp.Packet) error {
	// Log RTCP feedback
	if remb, ok := pkt.(*rtcp.ReceiverEstimatedMaximumBitrate); ok {
		m.bandwidthMu.Lock()
		m.backpressureLog = append(m.backpressureLog, int64(remb.Bitrate))
		m.bandwidthMu.Unlock()
	}
	return nil
}

func (m *MockStreamConnection) GenerateData(size int, interval time.Duration, count int) {
	go func() {
		data := make([]byte, size)
		for i := 0; i < count && atomic.LoadInt32(&m.closed) == 0; i++ {
			select {
			case m.data <- data:
			case <-time.After(interval):
				atomic.AddInt32(&m.readErrors, 1)
			}
			time.Sleep(interval)
		}
	}()
}

// TestBackpressure_DirectBufferPressure tests backpressure by directly creating buffer pressure
func TestBackpressure_DirectBufferPressure(t *testing.T) {
	t.Skip("Backpressure test needs to be updated for unified video-aware handler")
	streamID := "test-stream"

	// Create mock SRT connection
	mockStream := NewMockStreamConnection(streamID)
	mockStream.maxBandwidth = 52428800 // 50 Mbps default
	mockConn := &mockSRTConnection{
		MockStreamConnection: mockStream,
	}

	// Create memory controller
	memCtrl := memory.NewController(100*1024*1024, 50*1024*1024) // 100MB total

	// Create a properly sized buffer with low bitrate to trigger backpressure
	// Using 80Kbps (10KB/s) bitrate means 300KB buffer for 30 seconds
	_, err := buffer.NewProperSizedBuffer(streamID, 80*1024, memCtrl) // 80Kbps
	require.NoError(t, err)

	// Create queue for the handler
	queue, err := queue.NewHybridQueue(streamID, 1000, t.TempDir())
	require.NoError(t, err)
	defer queue.Close()

	// Create StreamHandler using the mock connection directly
	// The handler will detect it's not a known adapter type and use basic handling
	handler := NewStreamHandler(context.Background(), streamID, mockConn, queue, memCtrl, logrus.New())

	// Test applying backpressure
	initialBW := mockConn.GetMaxBW()
	handler.applyBackpressure() // No pressure parameter in new API

	// Check that bandwidth was reduced
	newBW := mockConn.GetMaxBW()
	assert.Less(t, newBW, initialBW, "Bandwidth should be reduced under pressure")

	// Test releasing backpressure
	handler.releaseBackpressure()
	finalBW := mockConn.GetMaxBW()
	assert.Greater(t, finalBW, newBW, "Bandwidth should increase when pressure is released")
}

// TestBackpressure_QueuePressure tests backpressure triggered by queue pressure
func TestBackpressure_QueuePressure(t *testing.T) {
	t.Skip("Backpressure test needs to be updated for unified video-aware handler")
	tempDir := t.TempDir()
	streamID := "queue-pressure-test"

	// Create components
	cfg := &config.IngestionConfig{
		Buffer: config.BufferConfig{
			RingSize: 100 * 1024, // 100KB
			PoolSize: 10,
		},
		QueueDir: tempDir,
	}

	mgr, err := createTestManager(cfg)
	require.NoError(t, err)
	defer mgr.Stop()

	// Override memory controller with smaller limits for testing
	mgr.memoryController = memory.NewController(10*1024*1024, 5*1024*1024) // 10MB total, 5MB per stream

	// Create connection
	conn := NewMockStreamConnection(streamID)
	rtpConn := &mockRTPConnection{MockStreamConnection: conn}

	// Create queue and buffer for the handler
	queue, err := queue.NewHybridQueue(streamID, 1000, tempDir)
	require.NoError(t, err)
	defer queue.Close()

	buffer, err := buffer.NewProperSizedBuffer(streamID, 1024*1024, mgr.memoryController)
	require.NoError(t, err)
	defer buffer.Close()

	// Create RTP adapter to wrap the connection
	// Note: RTP adapter expects an rtp.Session, but we have a mock
	// For testing, we'll use the mock directly as it implements StreamConnection
	handler := NewStreamHandler(context.Background(), streamID, rtpConn, queue, mgr.memoryController, mgr.logger)

	// Simulate high pressure and verify RTCP is sent
	handler.applyBackpressure()

	// Check that RTCP feedback was logged
	conn.bandwidthMu.RLock()
	rtcpSent := len(conn.backpressureLog) > 0
	conn.bandwidthMu.RUnlock()

	assert.True(t, rtcpSent, "RTCP feedback should be sent under pressure")
}

// TestBackpressure_MemoryEviction tests backpressure with memory eviction
func TestBackpressure_MemoryEviction(t *testing.T) {
	// Create memory controller with low limits
	memCtrl := memory.NewController(1*1024*1024, 512*1024) // 1MB total, 512KB per stream

	evictionCount := 0
	memCtrl.SetEvictionCallback(func(streamID string, bytes int64) {
		evictionCount++
		t.Logf("Eviction requested for stream %s: %d bytes", streamID, bytes)
		// Simulate actual memory release after eviction
		memCtrl.ReleaseMemory(streamID, bytes)
	})

	// First allocate enough to get close to the limit
	// This ensures we're above 80% pressure when we need eviction
	err := memCtrl.RequestMemory("stream1", 450*1024) // 450KB
	require.NoError(t, err)

	err = memCtrl.RequestMemory("stream2", 450*1024) // 450KB
	require.NoError(t, err)

	// Now we're at 900KB out of 1MB (90% pressure)
	// Next allocation should trigger eviction
	err = memCtrl.RequestMemory("stream3", 200*1024) // 200KB more
	if err != nil {
		t.Logf("Stream3 allocation result: %v", err)
	}

	// Should have triggered evictions due to memory pressure
	assert.Greater(t, evictionCount, 0, "Evictions should occur under memory pressure")

	// Check final pressure
	pressure := memCtrl.GetPressure()
	t.Logf("Final memory pressure: %.2f", pressure)
	assert.Greater(t, pressure, 0.5, "Memory pressure should be significant")
}

// Helper types

type mockSRTConnection struct {
	*MockStreamConnection
}

func (m *mockSRTConnection) GetMaxBW() int64 {
	return m.MockStreamConnection.GetMaxBW()
}

func (m *mockSRTConnection) SetMaxBW(bw int64) error {
	return m.MockStreamConnection.SetMaxBW(bw)
}

type mockRTPConnection struct {
	*MockStreamConnection
}

func (m *mockRTPConnection) GetBitrate() int64 {
	return m.MockStreamConnection.GetBitrate()
}

func (m *mockRTPConnection) GetSSRC() uint32 {
	return m.MockStreamConnection.GetSSRC()
}

func (m *mockRTPConnection) SendRTCP(pkt rtcp.Packet) error {
	return m.MockStreamConnection.SendRTCP(pkt)
}

// Helper function to create test manager
func createTestManager(cfg *config.IngestionConfig) (*Manager, error) {
	// Create logger
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Create memory controller with realistic memory for testing
	// For 10Mbps streams with 30s buffer: 10*30/8 = 37.5MB per stream
	memController := memory.NewController(200*1024*1024, 50*1024*1024) // 200MB total, 50MB per stream

	// Create mock registry
	reg := &mockRegistry{
		streams: make(map[string]*registry.Stream),
		mu:      sync.RWMutex{},
	}

	return &Manager{
		config:           cfg,
		registry:         reg,
		memoryController: memController,
		streamHandlers:   make(map[string]*StreamHandler),
		logger:           logger.Logger(log),
		started:          true,
	}, nil
}

type mockRegistry struct {
	streams map[string]*registry.Stream
	mu      sync.RWMutex
}

func (m *mockRegistry) Register(ctx context.Context, stream *registry.Stream) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[stream.ID] = stream
	return nil
}

func (m *mockRegistry) Get(ctx context.Context, streamID string) (*registry.Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stream, ok := m.streams[streamID]
	if !ok {
		return nil, fmt.Errorf("stream not found")
	}
	return stream, nil
}

func (m *mockRegistry) List(ctx context.Context) ([]*registry.Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	streams := make([]*registry.Stream, 0, len(m.streams))
	for _, s := range m.streams {
		streams = append(streams, s)
	}
	return streams, nil
}

func (m *mockRegistry) UpdateStatus(ctx context.Context, streamID string, status registry.StreamStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, ok := m.streams[streamID]; ok {
		stream.Status = status
	}
	return nil
}

func (m *mockRegistry) UpdateHeartbeat(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stream, ok := m.streams[streamID]; ok {
		stream.LastHeartbeat = time.Now()
	}
	return nil
}

func (m *mockRegistry) UpdateStats(ctx context.Context, streamID string, stats *registry.StreamStats) error {
	return nil
}

func (m *mockRegistry) Unregister(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streams, streamID)
	return nil
}

func (m *mockRegistry) Delete(ctx context.Context, streamID string) error {
	return m.Unregister(ctx, streamID)
}

func (m *mockRegistry) Update(ctx context.Context, stream *registry.Stream) error {
	return m.Register(ctx, stream)
}

func (m *mockRegistry) Close() error {
	return nil
}
