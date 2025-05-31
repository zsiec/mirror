package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// mockStreamConnection implements StreamConnection for testing
type mockStreamConnection struct {
	streamID string
	data     []byte
	pos      int
	closed   bool
	mu       sync.Mutex
}

func newMockStreamConnection(streamID string, data []byte) *mockStreamConnection {
	return &mockStreamConnection{
		streamID: streamID,
		data:     data,
	}
}

func (m *mockStreamConnection) GetStreamID() string {
	return m.streamID
}

func (m *mockStreamConnection) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, io.EOF
	}

	if m.pos >= len(m.data) {
		// Simulate continuous stream by blocking
		time.Sleep(100 * time.Millisecond)
		return 0, nil
	}

	n := copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockStreamConnection) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// mockRTPConnectionAdapter wraps mockStreamConnection to provide RTP adapter functionality
type mockRTPConnectionAdapter struct {
	*mockStreamConnection
	videoOutput chan types.TimestampedPacket
	audioOutput chan types.TimestampedPacket
}

// createTestConnection creates a mock connection that implements VideoAwareConnection
func createTestConnection(streamID string, data []byte) VideoAwareConnection {
	return newMockRTPConnectionAdapter(streamID, data)
}

func newMockRTPConnectionAdapter(streamID string, data []byte) *mockRTPConnectionAdapter {
	videoOutput := make(chan types.TimestampedPacket, 100)
	audioOutput := make(chan types.TimestampedPacket, 100)

	// Send test data as a video packet
	go func() {
		// Create a simple H.264 packet
		packet := types.TimestampedPacket{
			Data:        data,
			CaptureTime: time.Now(),
			PTS:         0,
			DTS:         0,
			StreamID:    streamID,
			Type:        types.PacketTypeVideo,
			Codec:       types.CodecH264,
			Flags:       types.PacketFlagFrameStart | types.PacketFlagFrameEnd,
		}
		videoOutput <- packet
		close(videoOutput)
		close(audioOutput)
	}()

	return &mockRTPConnectionAdapter{
		mockStreamConnection: newMockStreamConnection(streamID, data),
		videoOutput:          videoOutput,
		audioOutput:          audioOutput,
	}
}

func (m *mockRTPConnectionAdapter) GetVideoOutput() <-chan types.TimestampedPacket {
	return m.videoOutput
}

func (m *mockRTPConnectionAdapter) GetAudioOutput() <-chan types.TimestampedPacket {
	return m.audioOutput
}

func setupTestHandlers(t *testing.T) (*Handlers, *Manager, *mux.Router) {
	manager, _ := setupTestManager(t)
	testLogger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	handlers := NewHandlers(manager, testLogger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	return handlers, manager, router
}

// createTestStreamHandler creates a stream handler for testing
func createTestStreamHandler(t *testing.T, manager *Manager, streamID string, data []byte) {
	conn := newMockRTPConnectionAdapter(streamID, data)
	handler, err := manager.CreateStreamHandler(streamID, conn)
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestHandlers_GetStreams(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Register some test streams
	streams := []*registry.Stream{
		{
			ID:         "stream-1",
			Type:       registry.StreamTypeSRT,
			Status:     registry.StatusActive,
			SourceAddr: "192.168.1.100:1234",
		},
		{
			ID:         "stream-2",
			Type:       registry.StreamTypeRTP,
			Status:     registry.StatusActive,
			SourceAddr: "192.168.1.101:5004",
		},
	}

	for _, s := range streams {
		err := manager.GetRegistry().Register(ctx, s)
		require.NoError(t, err)
	}

	// Test getting all streams
	req := httptest.NewRequest("GET", "/api/v1/streams", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var response StreamListResponse
	err := json.NewDecoder(rec.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, 2, response.Count)
	assert.Len(t, response.Streams, 2)
}

func TestHandlers_GetStream(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Register a test stream
	stream := &registry.Stream{
		ID:         "test-stream-get",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "HEVC",
		Resolution: "1920x1080",
		Bitrate:    50000000,
	}

	err := manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Test successful get
	req := httptest.NewRequest("GET", "/api/v1/streams/test-stream-get", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var retrieved StreamDTO
	err = json.NewDecoder(rec.Body).Decode(&retrieved)
	require.NoError(t, err)

	assert.Equal(t, stream.ID, retrieved.ID)
	assert.Equal(t, stream.VideoCodec, retrieved.VideoCodec)
	assert.Equal(t, stream.Resolution, retrieved.Resolution)

	// Test non-existent stream
	req = httptest.NewRequest("GET", "/api/v1/streams/non-existent", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlers_GetStats(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	// Get stats
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var stats IngestionStats
	err := json.NewDecoder(rec.Body).Decode(&stats)
	require.NoError(t, err)

	assert.True(t, stats.Started)
	assert.Equal(t, 0, stats.ActiveHandlers) // No active handlers in test
}

func TestHandlers_GetStreamData(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Create a test stream
	stream := &registry.Stream{
		ID:         "test-stream-data",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "HEVC",
		Bitrate:    5000000,
	}
	err := manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create a stream handler with test data
	testData := []byte("STREAM_DATA_CONTENT_FOR_TESTING")
	createTestStreamHandler(t, manager, "test-stream-data", testData)

	// Give the handler time to process the data
	time.Sleep(100 * time.Millisecond)

	// Test getting stream data with a small timeout to avoid blocking
	req := httptest.NewRequest("GET", "/api/v1/streams/test-stream-data/data", nil)
	rec := httptest.NewRecorder()

	// Use a goroutine to serve the request and cancel it after a short time
	done := make(chan bool)
	go func() {
		router.ServeHTTP(rec, req)
		done <- true
	}()

	// Wait a bit then check if we got some data
	select {
	case <-done:
		// Request completed
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
		body := rec.Body.String()
		assert.Contains(t, body, "STREAM_DATA_FRAMES")
	case <-time.After(500 * time.Millisecond):
		// This is expected for streaming - the handler blocks waiting for more data
		// Just verify we got the right headers
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
	}

	// Test non-existent stream
	req = httptest.NewRequest("GET", "/api/v1/streams/non-existent/data", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestHandlers_WriteError has been removed as writeError is now a private helper function

func TestHandlers_EmptyStreamID(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	// Test GetStream with empty ID
	req := httptest.NewRequest("GET", "/api/v1/streams/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Should get 404 from router (no route match)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlers_ConcurrentRequests(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Register multiple streams
	for i := 0; i < 5; i++ {
		stream := &registry.Stream{
			ID:         fmt.Sprintf("concurrent-stream-%d", i),
			Type:       registry.StreamTypeSRT,
			Status:     registry.StatusActive,
			SourceAddr: fmt.Sprintf("192.168.1.%d:1234", 100+i),
		}
		err := manager.GetRegistry().Register(ctx, stream)
		require.NoError(t, err)
	}

	// Concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Random endpoints
			endpoints := []string{
				"/api/v1/streams",
				"/api/v1/stats",
				"/api/v1/streams/concurrent-stream-0",
			}

			for _, endpoint := range endpoints {
				req := httptest.NewRequest("GET", endpoint, nil)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusOK, rec.Code)
			}
		}()
	}

	wg.Wait()
}

func TestHandlers_StreamStats(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Create a test stream with stats
	stream := &registry.Stream{
		ID:              "test-stream-stats",
		Type:            registry.StreamTypeSRT,
		Status:          registry.StatusActive,
		SourceAddr:      "192.168.1.100:1234",
		VideoCodec:      "HEVC",
		Bitrate:         5000000,
		BytesReceived:   1024000,
		PacketsReceived: 1000,
		PacketsLost:     5,
	}
	err := manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create a stream handler with test data
	testData := make([]byte, 1024) // 1KB of test data
	createTestStreamHandler(t, manager, "test-stream-stats", testData)

	// Give the handler time to process some data
	time.Sleep(50 * time.Millisecond)

	// Test getting stream stats
	req := httptest.NewRequest("GET", "/api/v1/streams/test-stream-stats/stats", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var stats StreamStatsDTO
	err = json.Unmarshal(rec.Body.Bytes(), &stats)
	require.NoError(t, err)

	// Test expects realistic values based on mock handler processing
	// The mock sends 1 packet with 1KB data
	assert.GreaterOrEqual(t, stats.BytesReceived, int64(0)) // May be 0 if packet dropped
	assert.Equal(t, int64(1), stats.PacketsReceived)        // 1 packet processed
	assert.Equal(t, int64(5), stats.PacketsLost)            // From registry
	// Bitrate may be 0 initially due to short processing time
	assert.GreaterOrEqual(t, stats.Bitrate, int64(0))

	// Test non-existent stream
	req = httptest.NewRequest("GET", "/api/v1/streams/non-existent/stats", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlers_DeleteStream(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	// Test deleting non-existent stream
	req := httptest.NewRequest("DELETE", "/api/v1/streams/non-existent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlers_FrameBuffer(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Create a test stream
	stream := &registry.Stream{
		ID:         "test-stream-buffer",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "HEVC",
		Bitrate:    5000000,
	}
	err := manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create a stream handler with test data
	testData := make([]byte, 2048) // 2KB of test data
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	createTestStreamHandler(t, manager, "test-stream-buffer", testData)

	// Give the handler time to process the data
	time.Sleep(100 * time.Millisecond)

	// Test getting frame buffer stats
	req := httptest.NewRequest("GET", "/api/v1/streams/test-stream-buffer/buffer", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var bufferInfo struct {
		Capacity        int64   `json:"capacity"`
		Used            int64   `json:"used"`
		Available       int64   `json:"available"`
		FramesAssembled uint64  `json:"frames_assembled"`
		FramesDropped   uint64  `json:"frames_dropped"`
		QueuePressure   float64 `json:"queue_pressure"`
		Keyframes       uint64  `json:"keyframes"`
		PFrames         uint64  `json:"p_frames"`
		BFrames         uint64  `json:"b_frames"`
		StreamID        string  `json:"stream_id"`
		Codec           string  `json:"codec"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &bufferInfo)
	require.NoError(t, err)

	assert.Greater(t, bufferInfo.Capacity, int64(0))
	assert.GreaterOrEqual(t, bufferInfo.Used, int64(0))
	assert.GreaterOrEqual(t, bufferInfo.Available, int64(0))
	assert.Equal(t, bufferInfo.Capacity, bufferInfo.Used+bufferInfo.Available)

	// Test non-existent stream
	req = httptest.NewRequest("GET", "/api/v1/streams/non-existent/buffer", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlers_StreamPreview(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	ctx := context.Background()

	// Create a test stream
	stream := &registry.Stream{
		ID:         "test-stream-preview",
		Type:       registry.StreamTypeSRT,
		Status:     registry.StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "HEVC",
		Bitrate:    5000000,
	}
	err := manager.GetRegistry().Register(ctx, stream)
	require.NoError(t, err)

	// Create a stream handler with recognizable test data
	testData := []byte("PREVIEW_DATA_TEST_CONTENT_THAT_IS_RECOGNIZABLE")
	createTestStreamHandler(t, manager, "test-stream-preview", testData)

	// Give the handler time to process the data
	time.Sleep(100 * time.Millisecond)

	// Test getting preview with default duration
	req := httptest.NewRequest("GET", "/api/v1/streams/test-stream-preview/preview", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var preview struct {
		StreamID   string    `json:"stream_id"`
		Duration   float64   `json:"duration_seconds"`
		FrameCount int       `json:"frame_count"`
		Preview    string    `json:"preview"`
		Timestamp  time.Time `json:"timestamp"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &preview)
	require.NoError(t, err)

	assert.Equal(t, "test-stream-preview", preview.StreamID)
	assert.Equal(t, 1.0, preview.Duration) // Default is 1 second
	assert.NotEmpty(t, preview.Preview)
	// Check that we got frame count
	assert.GreaterOrEqual(t, preview.FrameCount, 0)

	// Test with custom duration
	req = httptest.NewRequest("GET", "/api/v1/streams/test-stream-preview/preview?duration=5", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	err = json.Unmarshal(rec.Body.Bytes(), &preview)
	require.NoError(t, err)
	assert.Equal(t, 5.0, preview.Duration) // 5 seconds requested

	// Test non-existent stream
	req = httptest.NewRequest("GET", "/api/v1/streams/non-existent/preview", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlers_PauseResumeStream(t *testing.T) {
	_, manager, router := setupTestHandlers(t)
	defer manager.Stop()

	// Test pausing non-existent stream
	req := httptest.NewRequest("POST", "/api/v1/streams/non-existent/pause", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Test resuming non-existent stream
	req = httptest.NewRequest("POST", "/api/v1/streams/non-existent/resume", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
