package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/zsiec/mirror/internal/logger"
)

// mockStreamHandler implements StreamHandler interface for testing
type mockStreamHandler struct {
	mu       sync.RWMutex
	stats    StreamStats
	getDelay time.Duration // Simulate processing time in GetStats
}

func newMockStreamHandler(stats StreamStats) *mockStreamHandler {
	return &mockStreamHandler{
		stats: stats,
	}
}

func (m *mockStreamHandler) GetStats() StreamStats {
	// Simulate some processing time to expose race conditions
	if m.getDelay > 0 {
		time.Sleep(m.getDelay)
	}
	
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

func (m *mockStreamHandler) SetGetStatsDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getDelay = delay
}

func (m *mockStreamHandler) UpdateStats(stats StreamStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats = stats
}

// Additional required methods for StreamHandler interface (mocked)
func (m *mockStreamHandler) Start() error { return nil }
func (m *mockStreamHandler) Stop() error  { return nil }

// TestHandleVideoStatsConcurrentAccess tests the concurrent access fix
func TestHandleVideoStatsConcurrentAccess(t *testing.T) {
	// Create test manager
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	// Create a mock registry
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}
	
	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler),
		handlersMu:     sync.RWMutex{},
	}

	// Create mock stream handlers with different stats
	handlers := make(map[string]*mockStreamHandler)
	for i := 0; i < 5; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		mockHandler := newMockStreamHandler(StreamStats{
			BytesProcessed:   uint64(1000 * (i + 1)),
			PacketsReceived:  uint64(100 * (i + 1)),
			Bitrate:          float64(1000000 * (i + 1)),
			FramesAssembled:  uint64(50 * (i + 1)),
			KeyframeCount:    uint64(5 * (i + 1)),
			PFrameCount:      uint64(45 * (i + 1)),
		})
		
		// Add some delay to GetStats to make race conditions more likely
		mockHandler.SetGetStatsDelay(10 * time.Millisecond)
		
		handlers[streamID] = mockHandler
		
		// Cast to interface and store in manager
		manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(mockHandler))
	}

	// Create HTTP handler
	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.HandleVideoStats).Methods("GET")

	t.Run("ConcurrentRequests", func(t *testing.T) {
		const numRequests = 50
		var wg sync.WaitGroup
		results := make(chan struct {
			statusCode int
			err        error
		}, numRequests)

		// Make multiple concurrent requests
		for i := 0; i < numRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
				if err != nil {
					results <- struct {
						statusCode int
						err        error
					}{0, err}
					return
				}

				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)

				results <- struct {
					statusCode int
					err        error
				}{rr.Code, nil}
			}()
		}

		wg.Wait()
		close(results)

		// Check all requests succeeded
		successCount := 0
		for result := range results {
			if result.err != nil {
				t.Errorf("Request failed with error: %v", result.err)
				continue
			}
			if result.statusCode == http.StatusOK {
				successCount++
			} else {
				t.Errorf("Request failed with status code: %d", result.statusCode)
			}
		}

		assert.Equal(t, numRequests, successCount, "All concurrent requests should succeed")
	})

	t.Run("ConcurrentRequestsWithHandlerModification", func(t *testing.T) {
		const numRequests = 20
		const numModifications = 5
		
		var wg sync.WaitGroup
		results := make(chan bool, numRequests)

		// Start concurrent requests
		for i := 0; i < numRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				
				req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
				require.NoError(t, err)

				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)

				// Should not panic or fail due to concurrent map access
				success := rr.Code == http.StatusOK
				results <- success
			}()
		}

		// Concurrently modify handlers map
		for i := 0; i < numModifications; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				
				// Add and remove handlers
				newStreamID := fmt.Sprintf("new-stream-%d", index)
				newHandler := newMockStreamHandler(StreamStats{
					BytesProcessed: uint64(2000 * (index + 1)),
				})
				
				manager.handlersMu.Lock()
				manager.streamHandlers[newStreamID] = (*StreamHandler)(interface{}(newHandler))
				manager.handlersMu.Unlock()
				
				time.Sleep(5 * time.Millisecond)
				
				manager.handlersMu.Lock()
				delete(manager.streamHandlers, newStreamID)
				manager.handlersMu.Unlock()
			}(i)
		}

		wg.Wait()
		close(results)

		// Check that no requests failed due to race conditions
		successCount := 0
		for success := range results {
			if success {
				successCount++
			}
		}

		assert.Equal(t, numRequests, successCount, "All requests should succeed despite concurrent map modifications")
	})
}

func TestHandleVideoStatsDeadlockPrevention(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}
	
	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler),
		handlersMu:     sync.RWMutex{},
	}

	// Create a handler that tries to acquire manager lock during GetStats
	// This would cause deadlock with the old implementation
	deadlockHandler := &struct {
		*mockStreamHandler
		manager *Manager
	}{
		mockStreamHandler: newMockStreamHandler(StreamStats{BytesProcessed: 1000}),
		manager:           manager,
	}

	// Override GetStats to try to acquire manager lock
	originalGetStats := deadlockHandler.mockStreamHandler.GetStats
	deadlockHandler.mockStreamHandler.GetStats = func() StreamStats {
		// This would cause deadlock in the old implementation
		// where HandleVideoStats held handlersMu during GetStats calls
		go func() {
			deadlockHandler.manager.handlersMu.RLock()
			time.Sleep(1 * time.Millisecond)
			deadlockHandler.manager.handlersMu.RUnlock()
		}()
		
		time.Sleep(5 * time.Millisecond) // Give the goroutine time to try acquiring lock
		return originalGetStats()
	}

	streamID := "deadlock-test-stream"
	manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(deadlockHandler.mockStreamHandler))

	// Create HTTP handler
	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.HandleVideoStats).Methods("GET")

	// Test with timeout to detect deadlock
	done := make(chan bool, 1)
	go func() {
		req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
		require.NoError(t, err)

		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, "Request should complete without deadlock")
		done <- true
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("Request timed out - likely deadlock detected")
	}
}

func TestHandleVideoStatsResponseFormat(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}
	
	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler),
		handlersMu:     sync.RWMutex{},
	}

	// Add test handlers
	testStats := map[string]StreamStats{
		"stream-1": {
			BytesProcessed:  1000,
			PacketsReceived: 100,
			Bitrate:         1000000,
			FramesAssembled: 50,
		},
		"stream-2": {
			BytesProcessed:  2000,
			PacketsReceived: 200,
			Bitrate:         2000000,
			FramesAssembled: 100,
		},
	}

	for streamID, stats := range testStats {
		handler := newMockStreamHandler(stats)
		manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(handler))
	}

	// Create HTTP handler
	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.HandleVideoStats).Methods("GET")

	req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	// Parse response
	var response struct {
		Streams   map[string]interface{} `json:"streams"`
		Count     int                    `json:"count"`
		Timestamp time.Time              `json:"timestamp"`
	}

	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify response format
	assert.Equal(t, 2, response.Count)
	assert.Equal(t, 2, len(response.Streams))
	assert.Contains(t, response.Streams, "stream-1")
	assert.Contains(t, response.Streams, "stream-2")
	assert.WithinDuration(t, time.Now(), response.Timestamp, 5*time.Second)
}

func TestHandleVideoStatsEmptyHandlers(t *testing.T) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}
	
	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler), // Empty
		handlersMu:     sync.RWMutex{},
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.HandleVideoStats).Methods("GET")

	req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response struct {
		Streams   map[string]interface{} `json:"streams"`
		Count     int                    `json:"count"`
		Timestamp time.Time              `json:"timestamp"`
	}

	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, 0, response.Count)
	assert.Equal(t, 0, len(response.Streams))
}

// Benchmark to verify performance characteristics
func BenchmarkHandleVideoStatsConcurrent(b *testing.B) {
	logger := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))
	
	mockRegistry := &registry.MockRegistry{
		Streams: make(map[string]*registry.Stream),
	}
	
	manager := &Manager{
		logger:         logger,
		registry:       mockRegistry,
		streamHandlers: make(map[string]*StreamHandler),
		handlersMu:     sync.RWMutex{},
	}

	// Create 100 mock handlers
	for i := 0; i < 100; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		handler := newMockStreamHandler(StreamStats{
			BytesProcessed: uint64(1000 * (i + 1)),
		})
		manager.streamHandlers[streamID] = (*StreamHandler)(interface{}(handler))
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.HandleVideoStats).Methods("GET")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
		}
	})
}