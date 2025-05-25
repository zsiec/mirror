package ingestion

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/sync"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func TestHandleStreamSync(t *testing.T) {
	// Set up test logger
	testLogger, err := logger.New(&config.LoggingConfig{Level: "debug"})
	require.NoError(t, err)

	// Create test manager
	cfg := &config.IngestionConfig{}

	manager, err := NewManager(cfg, testLogger)
	require.NoError(t, err)

	// Create handlers and router
	handlers := NewHandlers(manager, testLogger)
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	t.Run("stream not found", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/v1/streams/nonexistent/sync", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code)

		var resp map[string]interface{}
		err = json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Contains(t, resp["message"], "Stream not found")
	})

	t.Run("sync status for active stream", func(t *testing.T) {
		// Create a mock stream handler with sync manager
		streamID := "test-stream-123"
		syncManager := sync.NewManager(streamID, &sync.SyncConfig{}, testLogger)
		handler := &StreamHandler{
			streamID:    streamID,
			codec:       types.CodecH264,
			syncManager: syncManager,
			logger:      testLogger,
		}

		// Initialize sync manager tracks
		syncManager.InitializeVideo(types.NewRational(1, 90000))
		syncManager.InitializeAudio(types.NewRational(1, 48000))

		// Add some test data
		videoFrame := &types.VideoFrame{
			PTS:         1000,
			DTS:         900,
			CaptureTime: time.Now(),
		}
		handler.syncManager.ProcessVideoFrame(videoFrame)

		audioPacket := &types.TimestampedPacket{
			PTS:         500,
			DTS:         500,
			CaptureTime: time.Now(),
		}
		handler.syncManager.ProcessAudioPacket(audioPacket)

		// Register the handler
		manager.handlersMu.Lock()
		manager.streamHandlers[streamID] = handler
		manager.handlersMu.Unlock()

		// Make request
		req, err := http.NewRequest("GET", "/api/v1/streams/"+streamID+"/sync", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var resp SyncStatusResponse
		err = json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)

		// Verify response
		assert.Equal(t, streamID, resp.StreamID)
		// Status could be synced or drifting depending on timing
		assert.Contains(t, []sync.SyncStatusType{sync.SyncStatusSynced, sync.SyncStatusDrifting}, resp.Status)
		assert.NotNil(t, resp.Video)
		assert.NotNil(t, resp.Audio)
		assert.NotNil(t, resp.Drift)

		// Check track data
		assert.Equal(t, "video", resp.Video.Type)
		assert.Equal(t, int64(1000), resp.Video.LastPTS)
		assert.Equal(t, int64(900), resp.Video.LastDTS)
		assert.Greater(t, resp.Video.PacketCount, int64(0))

		assert.Equal(t, "audio", resp.Audio.Type)
		assert.Equal(t, int64(500), resp.Audio.LastPTS)
		assert.Equal(t, int64(500), resp.Audio.LastDTS)
		assert.Greater(t, resp.Audio.PacketCount, int64(0))

		// Cleanup
		manager.handlersMu.Lock()
		delete(manager.streamHandlers, streamID)
		manager.handlersMu.Unlock()
	})

	t.Run("sync not available for stream", func(t *testing.T) {
		// Create a stream handler without sync manager
		streamID := "test-stream-no-sync"
		handler := &StreamHandler{
			streamID:    streamID,
			codec:       types.CodecH264,
			syncManager: nil, // No sync manager
			logger:      testLogger,
		}

		// Register the handler
		manager.handlersMu.Lock()
		manager.streamHandlers[streamID] = handler
		manager.handlersMu.Unlock()

		// Make request
		req, err := http.NewRequest("GET", "/api/v1/streams/"+streamID+"/sync", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code)

		var resp map[string]interface{}
		err = json.Unmarshal(rr.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Contains(t, resp["message"], "Sync not available")

		// Cleanup
		manager.handlersMu.Lock()
		delete(manager.streamHandlers, streamID)
		manager.handlersMu.Unlock()
	})
}
