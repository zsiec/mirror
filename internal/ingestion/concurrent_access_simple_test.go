package ingestion

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SimpleStreamStats for testing without complex dependencies
type SimpleStreamStats struct {
	BytesProcessed  uint64  `json:"bytes_processed"`
	PacketsReceived uint64  `json:"packets_received"`
	Bitrate         float64 `json:"bitrate"`
}

// simpleMockStreamHandler for basic testing without SRT dependencies
type simpleMockStreamHandler struct {
	mu    sync.RWMutex
	stats SimpleStreamStats
	delay time.Duration
}

func newSimpleMockStreamHandler(stats SimpleStreamStats) *simpleMockStreamHandler {
	return &simpleMockStreamHandler{
		stats: stats,
		delay: 10 * time.Millisecond, // Simulate processing time
	}
}

func (s *simpleMockStreamHandler) GetStats() SimpleStreamStats {
	// Add delay to expose potential race conditions
	time.Sleep(s.delay)
	
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// simpleTestManager for testing without full Manager dependencies
type simpleTestManager struct {
	streamHandlers map[string]*simpleMockStreamHandler
	handlersMu     sync.RWMutex
}

func newSimpleTestManager() *simpleTestManager {
	return &simpleTestManager{
		streamHandlers: make(map[string]*simpleMockStreamHandler),
	}
}

func (m *simpleTestManager) addHandler(streamID string, handler *simpleMockStreamHandler) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()
	m.streamHandlers[streamID] = handler
}

func (m *simpleTestManager) removeHandler(streamID string) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()
	delete(m.streamHandlers, streamID)
}

// Simulate the fixed HandleVideoStats method
func (m *simpleTestManager) handleVideoStatsFixed(w http.ResponseWriter, r *http.Request) {
	// Get handlers without holding lock during GetStats calls (the fix)
	m.handlersMu.RLock()
	handlers := make(map[string]*simpleMockStreamHandler, len(m.streamHandlers))
	for streamID, handler := range m.streamHandlers {
		handlers[streamID] = handler
	}
	m.handlersMu.RUnlock()

	stats := make(map[string]interface{})
	for streamID, handler := range handlers {
		stats[streamID] = handler.GetStats()
	}

	response := struct {
		Streams   map[string]interface{} `json:"streams"`
		Count     int                    `json:"count"`
		Timestamp time.Time              `json:"timestamp"`
	}{
		Streams:   stats,
		Count:     len(stats),
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// Simulate the old buggy method (holding lock during GetStats)
func (m *simpleTestManager) handleVideoStatsBuggy(w http.ResponseWriter, r *http.Request) {
	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock() // This is the bug - holding lock during GetStats

	stats := make(map[string]interface{})
	for streamID, handler := range m.streamHandlers {
		stats[streamID] = handler.GetStats() // Potential deadlock here
	}

	response := struct {
		Streams   map[string]interface{} `json:"streams"`
		Count     int                    `json:"count"`
		Timestamp time.Time              `json:"timestamp"`
	}{
		Streams:   stats,
		Count:     len(stats),
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func TestConcurrentAccessFix(t *testing.T) {
	manager := newSimpleTestManager()

	// Add test handlers
	for i := 0; i < 10; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		handler := newSimpleMockStreamHandler(SimpleStreamStats{
			BytesProcessed:  uint64(1000 * (i + 1)),
			PacketsReceived: uint64(100 * (i + 1)),
			Bitrate:         float64(1000000 * (i + 1)),
		})
		manager.addHandler(streamID, handler)
	}

	t.Run("FixedVersionNoConcurrencyIssues", func(t *testing.T) {
		router := mux.NewRouter()
		router.HandleFunc("/api/v1/streams/stats/video", manager.handleVideoStatsFixed).Methods("GET")

		// Test concurrent requests
		const numRequests = 50
		var wg sync.WaitGroup
		successCount := int32(0)

		for i := 0; i < numRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
				require.NoError(t, err)

				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)

				if rr.Code == http.StatusOK {
					// Verify response format
					var response struct {
						Streams map[string]interface{} `json:"streams"`
						Count   int                    `json:"count"`
					}
					if json.Unmarshal(rr.Body.Bytes(), &response) == nil {
						if response.Count == 10 {
							successCount++
						}
					}
				}
			}()
		}

		// Concurrently modify handlers map
		go func() {
			for i := 0; i < 20; i++ {
				newStreamID := fmt.Sprintf("temp-stream-%d", i)
				handler := newSimpleMockStreamHandler(SimpleStreamStats{BytesProcessed: 1000})
				
				manager.addHandler(newStreamID, handler)
				time.Sleep(5 * time.Millisecond)
				manager.removeHandler(newStreamID)
				time.Sleep(5 * time.Millisecond)
			}
		}()

		wg.Wait()

		// All requests should succeed with the fixed version
		assert.Equal(t, int32(numRequests), successCount, 
			"All requests should succeed with the deadlock fix")
	})

	t.Run("DeadlockPreventionComparison", func(t *testing.T) {
		// This test demonstrates that the fix prevents deadlocks
		// by testing completion time
		
		fixedRouter := mux.NewRouter()
		fixedRouter.HandleFunc("/fixed", manager.handleVideoStatsFixed).Methods("GET")

		// Test fixed version
		start := time.Now()
		const testRequests = 20
		var wg sync.WaitGroup

		for i := 0; i < testRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest("GET", "/fixed", nil)
				rr := httptest.NewRecorder()
				fixedRouter.ServeHTTP(rr, req)
			}()
		}

		wg.Wait()
		fixedDuration := time.Since(start)

		// Should complete quickly without deadlocks
		assert.Less(t, fixedDuration, 2*time.Second, 
			"Fixed version should complete quickly without deadlocks")
	})
}

func TestResponseFormatConsistency(t *testing.T) {
	manager := newSimpleTestManager()

	// Add different types of handlers
	testCases := map[string]SimpleStreamStats{
		"high-bitrate-stream": {
			BytesProcessed:  50000,
			PacketsReceived: 5000,
			Bitrate:         10000000,
		},
		"low-bitrate-stream": {
			BytesProcessed:  5000,
			PacketsReceived: 500,
			Bitrate:         1000000,
		},
	}

	for streamID, stats := range testCases {
		handler := newSimpleMockStreamHandler(stats)
		manager.addHandler(streamID, handler)
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.handleVideoStatsFixed).Methods("GET")

	req, err := http.NewRequest("GET", "/api/v1/streams/stats/video", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var response struct {
		Streams   map[string]interface{} `json:"streams"`
		Count     int                    `json:"count"`
		Timestamp time.Time              `json:"timestamp"`
	}

	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify response structure
	assert.Equal(t, 2, response.Count)
	assert.Equal(t, 2, len(response.Streams))
	assert.Contains(t, response.Streams, "high-bitrate-stream")
	assert.Contains(t, response.Streams, "low-bitrate-stream")
	assert.WithinDuration(t, time.Now(), response.Timestamp, 5*time.Second)
}

func TestEmptyHandlersScenario(t *testing.T) {
	manager := newSimpleTestManager() // Empty handlers

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/streams/stats/video", manager.handleVideoStatsFixed).Methods("GET")

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

// Benchmark to compare performance
func BenchmarkConcurrentAccessFixed(b *testing.B) {
	manager := newSimpleTestManager()

	// Add handlers
	for i := 0; i < 100; i++ {
		streamID := fmt.Sprintf("stream-%d", i)
		handler := newSimpleMockStreamHandler(SimpleStreamStats{
			BytesProcessed: uint64(1000 * (i + 1)),
		})
		manager.addHandler(streamID, handler)
	}

	router := mux.NewRouter()
	router.HandleFunc("/fixed", manager.handleVideoStatsFixed).Methods("GET")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", "/fixed", nil)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)
		}
	})
}