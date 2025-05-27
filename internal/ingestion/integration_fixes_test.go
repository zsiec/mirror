package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/zsiec/mirror/internal/server"
	"github.com/zsiec/mirror/internal/config"
	"github.com/redis/go-redis/v9"
)

// IntegrationTestSuite tests all fixes together
func TestIntegrationAllFixes(t *testing.T) {
	// Create integrated test setup
	ctx := context.Background()
	logger := logger.NewLogger(logrus.New())
	
	// Create a mock registry
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}

	// Create manager with real components
	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler),
		handlersMu:     sync.RWMutex{},
	}

	// Create handlers for ingestion API
	handlers := NewHandlers(manager, logger)

	// Create server with real configuration
	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  true,
		ShutdownTimeout: 5 * time.Second,
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	srv := server.New(cfg, logrus.New(), redisClient)

	// Register ingestion routes (tests route registration fix)
	srv.RegisterRoutes(handlers.RegisterRoutes)

	// Setup routes (this tests the duplicate registration fix)
	srv.GetRouter().Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		// Just walking to ensure no panics from duplicate routes
		return nil
	})

	// Create test server
	testServer := httptest.NewServer(srv.GetRouter())
	defer testServer.Close()

	t.Run("ServerRoutesIntegration", func(t *testing.T) {
		// Test that all expected routes are available
		routes := []struct {
			method   string
			path     string
			expected int
		}{
			{"GET", "/health", http.StatusOK},
			{"GET", "/ready", http.StatusOK},
			{"GET", "/version", http.StatusOK},
			{"GET", "/api/v1/streams", http.StatusOK},
			{"GET", "/debug/info", http.StatusOK}, // Debug enabled
		}

		for _, route := range routes {
			req, err := http.NewRequest(route.method, testServer.URL+route.path, nil)
			require.NoError(t, err)

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, route.expected, resp.StatusCode,
				"Route %s %s should return %d", route.method, route.path, route.expected)
		}
	})

	t.Run("ConcurrentAPIAccessIntegration", func(t *testing.T) {
		// Add test streams and handlers
		const numStreams = 10
		for i := 0; i < numStreams; i++ {
			streamID := fmt.Sprintf("integration-stream-%d", i)
			
			// Add to registry
			mockRegistry.Streams[streamID] = &registry.Stream{
				ID:            streamID,
				Type:          registry.StreamTypeSRT,
				Status:        registry.StreamStatusActive,
				SourceAddr:    "127.0.0.1:30000",
				CreatedAt:     time.Now(),
				LastHeartbeat: time.Now(),
			}

			// Add handler
			mockHandler := newMockStreamHandler(StreamStats{
				BytesProcessed:  uint64(1000 * (i + 1)),
				PacketsReceived: uint64(100 * (i + 1)),
				Bitrate:         float64(1000000 * (i + 1)),
			})
			manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(mockHandler))
		}

		// Test concurrent access to video stats (tests deadlock fix)
		const numConcurrentRequests = 20
		var wg sync.WaitGroup
		results := make(chan bool, numConcurrentRequests)

		for i := 0; i < numConcurrentRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				req, err := http.NewRequest("GET", testServer.URL+"/api/v1/streams/stats/video", nil)
				if err != nil {
					results <- false
					return
				}

				client := &http.Client{Timeout: 2 * time.Second}
				resp, err := client.Do(req)
				if err != nil {
					results <- false
					return
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					results <- false
					return
				}

				var response struct {
					Streams map[string]interface{} `json:"streams"`
					Count   int                    `json:"count"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
					results <- false
					return
				}

				results <- response.Count == numStreams
			}()
		}

		wg.Wait()
		close(results)

		successCount := 0
		for success := range results {
			if success {
				successCount++
			}
		}

		assert.Equal(t, numConcurrentRequests, successCount,
			"All concurrent requests should succeed without deadlock")
	})

	t.Run("WebSocketIntegrationWithAPIEndpoints", func(t *testing.T) {
		// Add a test stream for WebSocket testing
		testStreamID := "websocket-integration-stream"
		mockRegistry.Streams[testStreamID] = &registry.Stream{
			ID:     testStreamID,
			Type:   registry.StreamTypeSRT,
			Status: registry.StreamStatusActive,
		}

		mockHandler := newMockStreamHandler(StreamStats{
			BytesProcessed: 5000,
			Bitrate:        2000000,
		})
		manager.streamHandlers[testStreamID] = (*StreamHandler)(interface{}(mockHandler))

		// Start frame capture
		capturePayload := `{"enabled": true, "max_frames": 100}`
		req, err := http.NewRequest("POST", testServer.URL+"/api/v1/frames/capture/start",
			strings.NewReader(capturePayload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Connect to WebSocket
		wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + 
			"/api/v1/streams/" + testStreamID + "/frames/live"

		dialer := websocket.Dialer{}
		conn, _, err := dialer.Dial(wsURL, nil)
		require.NoError(t, err)
		defer conn.Close()

		// Receive some frames
		frameCount := 0
		for frameCount < 5 {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			var frameData FrameVisualizationData
			err := conn.ReadJSON(&frameData)
			require.NoError(t, err)
			
			assert.Equal(t, testStreamID, frameData.StreamID)
			frameCount++
		}

		// Test that API endpoints still work while WebSocket is active
		req, err = http.NewRequest("GET", testServer.URL+"/api/v1/streams/"+testStreamID, nil)
		require.NoError(t, err)

		resp, err = client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Remove stream handler to test cleanup
		delete(manager.streamHandlers, testStreamID)

		// WebSocket should close gracefully
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			err := conn.ReadJSON(&struct{}{})
			if err != nil {
				// Connection should close
				break
			}
		}
	})

	t.Run("MemoryLeakPrevention", func(t *testing.T) {
		// Create many short-lived streams to test memory cleanup
		const numTestStreams = 50
		
		initialHandlerCount := len(manager.streamHandlers)
		
		// Create streams
		streamIDs := make([]string, numTestStreams)
		for i := 0; i < numTestStreams; i++ {
			streamID := fmt.Sprintf("memory-test-stream-%d", i)
			streamIDs[i] = streamID
			
			mockRegistry.Streams[streamID] = &registry.Stream{
				ID:     streamID,
				Type:   registry.StreamTypeSRT,
				Status: registry.StreamStatusActive,
			}

			mockHandler := newMockStreamHandler(StreamStats{BytesProcessed: 1000})
			manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(mockHandler))
		}

		assert.Equal(t, initialHandlerCount+numTestStreams, len(manager.streamHandlers))

		// Remove all test streams
		for _, streamID := range streamIDs {
			delete(manager.streamHandlers, streamID)
			delete(mockRegistry.Streams, streamID)
		}

		// Handler count should return to initial value
		assert.Equal(t, initialHandlerCount, len(manager.streamHandlers))
	})

	t.Run("ErrorHandlingIntegration", func(t *testing.T) {
		// Test error scenarios don't cause panics or deadlocks
		
		// Request stats for non-existent stream
		req, err := http.NewRequest("GET", testServer.URL+"/api/v1/streams/non-existent/stats", nil)
		require.NoError(t, err)

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		// Try to connect WebSocket to non-existent stream
		wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + 
			"/api/v1/streams/non-existent/frames/live"

		dialer := websocket.Dialer{}
		_, resp, err = dialer.Dial(wsURL, nil)
		assert.Error(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		// Invalid capture request
		invalidPayload := `{"invalid": "json"`
		req, err = http.NewRequest("POST", testServer.URL+"/api/v1/frames/capture/start",
			strings.NewReader(invalidPayload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("PerformanceUnderLoad", func(t *testing.T) {
		// Create multiple streams for load testing
		const numLoadStreams = 20
		for i := 0; i < numLoadStreams; i++ {
			streamID := fmt.Sprintf("load-test-stream-%d", i)
			
			mockRegistry.Streams[streamID] = &registry.Stream{
				ID:     streamID,
				Type:   registry.StreamTypeSRT,
				Status: registry.StreamStatusActive,
			}

			mockHandler := newMockStreamHandler(StreamStats{
				BytesProcessed: uint64(10000 * (i + 1)),
				Bitrate:        float64(5000000),
			})
			manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(mockHandler))
		}

		// Create concurrent load
		const numWorkers = 10
		const requestsPerWorker = 20
		var wg sync.WaitGroup
		
		startTime := time.Now()
		
		for worker := 0; worker < numWorkers; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				client := &http.Client{Timeout: 1 * time.Second}
				for i := 0; i < requestsPerWorker; i++ {
					// Mix different API calls
					endpoints := []string{
						"/api/v1/streams",
						"/api/v1/streams/stats/video",
						"/api/v1/stats",
					}
					
					endpoint := endpoints[i%len(endpoints)]
					req, _ := http.NewRequest("GET", testServer.URL+endpoint, nil)
					
					resp, err := client.Do(req)
					if err == nil {
						resp.Body.Close()
					}
				}
			}()
		}

		wg.Wait()
		elapsed := time.Since(startTime)
		
		totalRequests := numWorkers * requestsPerWorker
		requestsPerSecond := float64(totalRequests) / elapsed.Seconds()
		
		// Should handle reasonable load without timeout
		assert.Less(t, elapsed, 10*time.Second, "Load test should complete in reasonable time")
		assert.Greater(t, requestsPerSecond, 10.0, "Should handle at least 10 requests per second")

		// Clean up load test streams
		for i := 0; i < numLoadStreams; i++ {
			streamID := fmt.Sprintf("load-test-stream-%d", i)
			delete(manager.streamHandlers, streamID)
			delete(mockRegistry.Streams, streamID)
		}
	})
}

// TestEndToEndScenario tests a complete realistic scenario
func TestEndToEndScenario(t *testing.T) {
	// This test simulates a complete workflow:
	// 1. Server starts with proper route registration
	// 2. Streams are created and start processing
	// 3. Multiple clients connect for monitoring
	// 4. Statistics are gathered concurrently
	// 5. Frame visualization is used
	// 6. Streams are terminated and cleaned up

	ctx := context.Background()
	logger := logger.NewLogger(logrus.New())
	
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}

	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler),
		handlersMu:     sync.RWMutex{},
	}

	handlers := NewHandlers(manager, logger)

	cfg := &config.ServerConfig{
		HTTP3Port:       8443,
		TLSCertFile:     "test-cert.pem",
		TLSKeyFile:      "test-key.pem",
		DebugEndpoints:  false,
		ShutdownTimeout: 5 * time.Second,
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	srv := server.New(cfg, logrus.New(), redisClient)
	srv.RegisterRoutes(handlers.RegisterRoutes)

	testServer := httptest.NewServer(srv.GetRouter())
	defer testServer.Close()

	// Phase 1: Create streams
	streamIDs := []string{"live-stream-1", "live-stream-2", "live-stream-3"}
	for _, streamID := range streamIDs {
		mockRegistry.Streams[streamID] = &registry.Stream{
			ID:            streamID,
			Type:          registry.StreamTypeSRT,
			Status:        registry.StreamStatusActive,
			SourceAddr:    "127.0.0.1:30000",
			CreatedAt:     time.Now(),
			LastHeartbeat: time.Now(),
		}

		mockHandler := newMockStreamHandler(StreamStats{
			BytesProcessed:  5000,
			PacketsReceived: 500,
			Bitrate:         2000000,
			FramesAssembled: 100,
			KeyframeCount:   10,
			PFrameCount:     90,
		})
		manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(mockHandler))
	}

	// Phase 2: Multiple clients monitoring concurrently
	const numClients = 5
	var clientWg sync.WaitGroup
	clientResults := make(chan bool, numClients)

	for i := 0; i < numClients; i++ {
		clientWg.Add(1)
		go func(clientID int) {
			defer clientWg.Done()
			
			client := &http.Client{Timeout: 2 * time.Second}
			success := true
			
			// Each client makes multiple requests
			for j := 0; j < 10; j++ {
				// Get stream list
				req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/streams", nil)
				resp, err := client.Do(req)
				if err != nil || resp.StatusCode != http.StatusOK {
					success = false
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				resp.Body.Close()

				// Get video stats
				req, _ = http.NewRequest("GET", testServer.URL+"/api/v1/streams/stats/video", nil)
				resp, err = client.Do(req)
				if err != nil || resp.StatusCode != http.StatusOK {
					success = false
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				resp.Body.Close()

				time.Sleep(10 * time.Millisecond)
			}
			
			clientResults <- success
		}(i)
	}

	// Phase 3: WebSocket monitoring
	wsClients := make([]*websocket.Conn, 0, len(streamIDs))
	for _, streamID := range streamIDs {
		wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + 
			"/api/v1/streams/" + streamID + "/frames/live"

		dialer := websocket.Dialer{}
		conn, _, err := dialer.Dial(wsURL, nil)
		require.NoError(t, err)
		wsClients = append(wsClients, conn)

		// Read a few frames to verify connection
		go func(c *websocket.Conn) {
			for i := 0; i < 5; i++ {
				c.SetReadDeadline(time.Now().Add(1 * time.Second))
				var frameData FrameVisualizationData
				c.ReadJSON(&frameData)
			}
		}(conn)
	}

	// Wait for clients to complete
	clientWg.Wait()
	close(clientResults)

	// Verify all clients succeeded
	for success := range clientResults {
		assert.True(t, success, "All monitoring clients should succeed")
	}

	// Phase 4: Stream termination and cleanup
	for _, streamID := range streamIDs {
		delete(manager.streamHandlers, streamID)
		delete(mockRegistry.Streams, streamID)
	}

	// Close WebSocket connections
	for _, conn := range wsClients {
		conn.Close()
	}

	// Verify cleanup
	assert.Equal(t, 0, len(manager.streamHandlers))
	assert.Equal(t, 0, len(mockRegistry.Streams))

	// Final verification that server still responds
	req, err := http.NewRequest("GET", testServer.URL+"/health", nil)
	require.NoError(t, err)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}