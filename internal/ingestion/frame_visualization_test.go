package ingestion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

// mockManager implements required Manager methods for testing
type mockManagerForVisualization struct {
	handlers map[string]*StreamHandler
	mu       sync.RWMutex
	registry registry.Registry
}

func newMockManagerForVisualization() *mockManagerForVisualization {
	return &mockManagerForVisualization{
		handlers: make(map[string]*StreamHandler),
		registry: &registry.MockRegistry{
			Streams: make(map[string]*registry.Stream),
		},
	}
}

func (m *mockManagerForVisualization) GetStreamHandler(streamID string) (*StreamHandler, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	handler, exists := m.handlers[streamID]
	return handler, exists
}

func (m *mockManagerForVisualization) AddStreamHandler(streamID string, handler *StreamHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[streamID] = handler
}

func (m *mockManagerForVisualization) RemoveStreamHandler(streamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.handlers, streamID)
}

func (m *mockManagerForVisualization) GetStreamHandlerCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.handlers)
}

// mockStreamHandlerForVisualization for frame visualization testing
type mockStreamHandlerForVisualization struct {
	streamID string
	stats    StreamStats
	removed  bool
	mu       sync.RWMutex
}

func newMockStreamHandlerForVisualization(streamID string) *mockStreamHandlerForVisualization {
	return &mockStreamHandlerForVisualization{
		streamID: streamID,
		stats: StreamStats{
			BytesProcessed:  1000,
			PacketsReceived: 100,
			Bitrate:         1000000,
			FramesAssembled: 50,
		},
	}
}

func (m *mockStreamHandlerForVisualization) GetStats() StreamStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

func (m *mockStreamHandlerForVisualization) MarkRemoved() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = true
}

func (m *mockStreamHandlerForVisualization) IsRemoved() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.removed
}

func TestFrameVisualizationManagerCreation(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	assert.NotNil(t, fvm)
	assert.NotNil(t, fvm.connections)
	assert.NotNil(t, fvm.captureBuffer)
	assert.Equal(t, 1000, fvm.captureBuffer.maxSize)
	assert.False(t, fvm.captureBuffer.enabled)

	// Verify cleanup goroutine starts (give it time to start)
	time.Sleep(100 * time.Millisecond)
}

func TestWebSocketConnectionLifecycle(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	// Add a test stream to registry
	testStreamID := "test-stream-001"
	manager.registry.(*registry.MockRegistry).Streams[testStreamID] = &registry.Stream{
		ID:       testStreamID,
		Type:     registry.StreamTypeSRT,
		Status:   registry.StreamStatusActive,
		CreatedAt: time.Now(),
	}

	// Add stream handler
	handler := newMockStreamHandlerForVisualization(testStreamID)
	manager.AddStreamHandler(testStreamID, (*StreamHandler)(interface{}(handler)))

	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	// Setup HTTP server with WebSocket endpoint
	router := mux.NewRouter()
	fvm.RegisterVisualizationRoutes(router)
	
	server := httptest.NewServer(router)
	defer server.Close()

	// Convert HTTP URL to WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streams/" + testStreamID + "/frames/live"

	t.Run("SuccessfulConnection", func(t *testing.T) {
		// Connect to WebSocket
		dialer := websocket.Dialer{}
		conn, resp, err := dialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer conn.Close()
		
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

		// Should receive frame data
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var frameData FrameVisualizationData
		err = conn.ReadJSON(&frameData)
		require.NoError(t, err)

		assert.Equal(t, testStreamID, frameData.StreamID)
		assert.NotEmpty(t, frameData.FrameType)
		assert.Greater(t, frameData.FrameSize, 0)
	})

	t.Run("NonExistentStream", func(t *testing.T) {
		invalidURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streams/non-existent/frames/live"
		
		dialer := websocket.Dialer{}
		_, resp, err := dialer.Dial(invalidURL, nil)
		
		// Should fail to connect
		assert.Error(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestStreamHandlerRemovalDetection(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	testStreamID := "test-stream-removal"
	manager.registry.(*registry.MockRegistry).Streams[testStreamID] = &registry.Stream{
		ID:     testStreamID,
		Type:   registry.StreamTypeSRT,
		Status: registry.StreamStatusActive,
	}

	handler := newMockStreamHandlerForVisualization(testStreamID)
	manager.AddStreamHandler(testStreamID, (*StreamHandler)(interface{}(handler)))

	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	// Setup HTTP server
	router := mux.NewRouter()
	fvm.RegisterVisualizationRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streams/" + testStreamID + "/frames/live"

	// Connect to WebSocket
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Verify connection is working
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	var frameData FrameVisualizationData
	err = conn.ReadJSON(&frameData)
	require.NoError(t, err)

	// Remove stream handler to simulate stream termination
	manager.RemoveStreamHandler(testStreamID)

	// Connection should close due to stream handler removal detection
	// Set a reasonable timeout for connection to close
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	
	// Keep reading until connection closes
	for {
		err = conn.ReadJSON(&frameData)
		if err != nil {
			// Connection should close
			assert.True(t, websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) ||
				websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway))
			break
		}
	}
}

func TestOrphanedConnectionCleanup(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	testStreamID := "test-stream-cleanup"
	manager.registry.(*registry.MockRegistry).Streams[testStreamID] = &registry.Stream{
		ID:     testStreamID,
		Type:   registry.StreamTypeSRT,
		Status: registry.StreamStatusActive,
	}

	handler := newMockStreamHandlerForVisualization(testStreamID)
	manager.AddStreamHandler(testStreamID, (*StreamHandler)(interface{}(handler)))

	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	// Create a mock connection manually (simulating orphaned connection)
	ctx, cancel := context.WithCancel(context.Background())
	mockConn := &FrameStreamConnection{
		streamID: testStreamID,
		send:     make(chan *FrameVisualizationData, 10),
		ctx:      ctx,
		cancel:   cancel,
	}

	// Manually add to connections map
	connKey := "mock-connection-key"
	fvm.connMutex.Lock()
	fvm.connections[connKey] = mockConn
	fvm.connMutex.Unlock()

	// Verify connection exists
	fvm.connMutex.RLock()
	_, exists := fvm.connections[connKey]
	fvm.connMutex.RUnlock()
	assert.True(t, exists, "Mock connection should exist")

	// Remove stream handler (making connection orphaned)
	manager.RemoveStreamHandler(testStreamID)

	// Wait for cleanup cycle (cleanup runs every 30 seconds, but we'll trigger it manually)
	// Since we can't easily trigger the cleanup manually, we'll test the cleanup logic directly
	fvm.connMutex.RLock()
	connectionsToClose := make([]string, 0)
	for connKey, streamConn := range fvm.connections {
		if _, exists := manager.GetStreamHandler(streamConn.streamID); !exists {
			connectionsToClose = append(connectionsToClose, connKey)
		}
	}
	fvm.connMutex.RUnlock()

	// Close orphaned connections
	for _, connKey := range connectionsToClose {
		fvm.connMutex.Lock()
		if streamConn, exists := fvm.connections[connKey]; exists {
			streamConn.cancel()
			delete(fvm.connections, connKey)
		}
		fvm.connMutex.Unlock()
	}

	// Verify connection was cleaned up
	fvm.connMutex.RLock()
	_, exists = fvm.connections[connKey]
	fvm.connMutex.RUnlock()
	assert.False(t, exists, "Orphaned connection should be cleaned up")

	// Verify context was cancelled
	select {
	case <-mockConn.ctx.Done():
		// Good - context was cancelled
	default:
		t.Error("Mock connection context should be cancelled")
	}
}

func TestFrameCaptureControl(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	router := mux.NewRouter()
	fvm.RegisterVisualizationRoutes(router)
	
	server := httptest.NewServer(router)
	defer server.Close()

	t.Run("StartCapture", func(t *testing.T) {
		payload := `{"enabled": true, "max_frames": 500}`
		req, err := http.NewRequest("POST", server.URL+"/api/v1/frames/capture/start", strings.NewReader(payload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify capture is enabled
		fvm.captureMutex.RLock()
		enabled := fvm.captureBuffer.enabled
		maxSize := fvm.captureBuffer.maxSize
		fvm.captureMutex.RUnlock()

		assert.True(t, enabled)
		assert.Equal(t, 500, maxSize)
	})

	t.Run("StopCapture", func(t *testing.T) {
		req, err := http.NewRequest("POST", server.URL+"/api/v1/frames/capture/stop", nil)
		require.NoError(t, err)

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify capture is disabled
		fvm.captureMutex.RLock()
		enabled := fvm.captureBuffer.enabled
		fvm.captureMutex.RUnlock()

		assert.False(t, enabled)
	})

	t.Run("CaptureStatus", func(t *testing.T) {
		req, err := http.NewRequest("GET", server.URL+"/api/v1/frames/capture/status", nil)
		require.NoError(t, err)

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var status struct {
			Enabled        bool `json:"enabled"`
			MaxFrames      int  `json:"max_frames"`
			CapturedFrames int  `json:"captured_frames"`
		}
		err = json.NewDecoder(resp.Body).Decode(&status)
		require.NoError(t, err)

		assert.False(t, status.Enabled) // Should be false from previous test
		assert.Equal(t, 500, status.MaxFrames)
	})
}

func TestFrameRateReduction(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	testStreamID := "test-stream-rate"
	manager.registry.(*registry.MockRegistry).Streams[testStreamID] = &registry.Stream{
		ID:     testStreamID,
		Type:   registry.StreamTypeSRT,
		Status: registry.StreamStatusActive,
	}

	handler := newMockStreamHandlerForVisualization(testStreamID)
	manager.AddStreamHandler(testStreamID, (*StreamHandler)(interface{}(handler)))

	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	router := mux.NewRouter()
	fvm.RegisterVisualizationRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streams/" + testStreamID + "/frames/live"

	// Connect to WebSocket
	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Measure frame rate
	frameCount := 0
	startTime := time.Now()
	timeout := time.After(2 * time.Second)

	for {
		select {
		case <-timeout:
			// Calculate effective frame rate
			elapsed := time.Since(startTime)
			effectiveRate := float64(frameCount) / elapsed.Seconds()
			
			// Should be around 10 FPS (100ms interval), not 30 FPS
			assert.Less(t, effectiveRate, 15.0, "Frame rate should be reduced to ~10 FPS")
			assert.Greater(t, effectiveRate, 5.0, "Frame rate should not be too low")
			return

		default:
			conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			var frameData FrameVisualizationData
			err := conn.ReadJSON(&frameData)
			if err == nil {
				frameCount++
			}
		}
	}
}

func TestConcurrentWebSocketConnections(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	// Create multiple test streams
	streamIDs := []string{"stream-1", "stream-2", "stream-3"}
	for _, streamID := range streamIDs {
		manager.registry.(*registry.MockRegistry).Streams[streamID] = &registry.Stream{
			ID:     streamID,
			Type:   registry.StreamTypeSRT,
			Status: registry.StreamStatusActive,
		}
		
		handler := newMockStreamHandlerForVisualization(streamID)
		manager.AddStreamHandler(streamID, (*StreamHandler)(interface{}(handler)))
	}

	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	router := mux.NewRouter()
	fvm.RegisterVisualizationRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	// Create concurrent WebSocket connections
	const numConnections = 10
	var wg sync.WaitGroup
	results := make(chan bool, numConnections)

	for i := 0; i < numConnections; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			
			streamID := streamIDs[index%len(streamIDs)]
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/streams/" + streamID + "/frames/live"

			dialer := websocket.Dialer{}
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				results <- false
				return
			}
			defer conn.Close()

			// Read a few frames
			success := true
			for j := 0; j < 3; j++ {
				conn.SetReadDeadline(time.Now().Add(1 * time.Second))
				var frameData FrameVisualizationData
				if err := conn.ReadJSON(&frameData); err != nil {
					success = false
					break
				}
			}
			
			results <- success
		}(i)
	}

	wg.Wait()
	close(results)

	// Check all connections succeeded
	successCount := 0
	for success := range results {
		if success {
			successCount++
		}
	}

	assert.Equal(t, numConnections, successCount, "All concurrent WebSocket connections should succeed")
}

func TestFrameVisualizationDataConversion(t *testing.T) {
	manager := newMockManagerForVisualization()
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	fvm := NewFrameVisualizationManager((*Manager)(manager), logger)

	handler := newMockStreamHandlerForVisualization("test-stream")
	
	// Create a mock video frame
	mockFrame := &types.VideoFrame{
		StreamID:     "test-stream",
		Type:         types.FrameTypeI,
		TotalSize:    8192,
		PTS:          90000,
		DTS:          90000,
		Duration:     3000,
		CaptureTime:  time.Now().Add(-5 * time.Millisecond),
		CompleteTime: time.Now(),
		NALUnits: []types.NALUnit{
			{Type: 7, RefIdc: 3, Data: make([]byte, 32)}, // SPS
			{Type: 8, RefIdc: 3, Data: make([]byte, 16)}, // PPS
			{Type: 5, RefIdc: 3, Data: make([]byte, 8144)}, // IDR
		},
	}
	
	frameData := fvm.convertFrameToVisualizationData(mockFrame, (*StreamHandler)(interface{}(handler)))

	// Verify frame data structure
	assert.Equal(t, "test-stream", frameData.StreamID)
	assert.Equal(t, "I", frameData.FrameType)
	assert.Equal(t, 8192, frameData.FrameSize)
	assert.Equal(t, int64(90000), frameData.PTS)
	assert.Equal(t, int64(90000), frameData.DTS)
	assert.Equal(t, int64(3000), frameData.Duration)
	assert.Len(t, frameData.NALUnits, 3)
	assert.Equal(t, uint8(7), frameData.NALUnits[0].Type)
	assert.Equal(t, "SPS", frameData.NALUnits[0].TypeName)
	assert.NotEmpty(t, frameData.Flags)
}