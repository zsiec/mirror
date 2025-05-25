package memory

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvictionStrategies tests different eviction strategies
func TestEvictionStrategies(t *testing.T) {
	now := time.Now()
	
	streams := []StreamInfo{
		{
			StreamID:    "stream1",
			MemoryUsage: 100 * 1024 * 1024, // 100MB
			LastAccess:  now.Add(-5 * time.Minute),
			Priority:    5,
			IsActive:    false,
			CreatedAt:   now.Add(-10 * time.Minute),
		},
		{
			StreamID:    "stream2",
			MemoryUsage: 50 * 1024 * 1024, // 50MB
			LastAccess:  now.Add(-1 * time.Minute),
			Priority:    3,
			IsActive:    false,
			CreatedAt:   now.Add(-5 * time.Minute),
		},
		{
			StreamID:    "stream3",
			MemoryUsage: 200 * 1024 * 1024, // 200MB
			LastAccess:  now.Add(-10 * time.Minute),
			Priority:    8,
			IsActive:    false,
			CreatedAt:   now.Add(-15 * time.Minute),
		},
		{
			StreamID:    "stream4",
			MemoryUsage: 75 * 1024 * 1024, // 75MB
			LastAccess:  now.Add(-30 * time.Second),
			Priority:    2,
			IsActive:    true, // Active stream
			CreatedAt:   now.Add(-2 * time.Minute),
		},
	}
	
	t.Run("LRU Strategy", func(t *testing.T) {
		strategy := &LRUEvictionStrategy{}
		targetBytes := int64(150 * 1024 * 1024) // 150MB
		
		selected := strategy.SelectStreamsForEviction(streams, targetBytes)
		
		// Should select stream3 (oldest access, 200MB) first
		assert.Contains(t, selected, "stream3")
		// Should not select active stream4 unless necessary
		assert.NotContains(t, selected, "stream4")
	})
	
	t.Run("Priority Strategy", func(t *testing.T) {
		strategy := &PriorityEvictionStrategy{}
		targetBytes := int64(100 * 1024 * 1024) // 100MB
		
		selected := strategy.SelectStreamsForEviction(streams, targetBytes)
		
		// Should select stream3 first (highest priority value = lowest priority)
		assert.Contains(t, selected, "stream3")
		assert.Len(t, selected, 1) // Only need stream3 to meet target
	})
	
	t.Run("Size-Based Strategy", func(t *testing.T) {
		strategy := &SizeBasedEvictionStrategy{}
		targetBytes := int64(100 * 1024 * 1024) // 100MB
		
		selected := strategy.SelectStreamsForEviction(streams, targetBytes)
		
		// Should select stream3 first (largest, 200MB)
		assert.Contains(t, selected, "stream3")
		assert.Len(t, selected, 1)
	})
	
	t.Run("Hybrid Strategy", func(t *testing.T) {
		strategy := &HybridEvictionStrategy{
			AgeWeight:      0.4,
			SizeWeight:     0.4,
			PriorityWeight: 0.2,
		}
		targetBytes := int64(150 * 1024 * 1024) // 150MB
		
		selected := strategy.SelectStreamsForEviction(streams, targetBytes)
		
		// Stream3 should score high (old, large, low priority)
		assert.Contains(t, selected, "stream3")
		// Active stream should not be selected unless necessary
		assert.NotContains(t, selected, "stream4")
	})
}

// TestMemoryControllerWithEviction tests the complete eviction flow
func TestMemoryControllerWithEviction(t *testing.T) {
	maxMemory := int64(500 * 1024 * 1024)      // 500MB total
	perStreamLimit := int64(200 * 1024 * 1024) // 200MB per stream
	
	controller := NewController(maxMemory, perStreamLimit)
	
	// Track evicted streams
	var evictedStreams []string
	var evictedBytes int64
	var mu sync.Mutex
	
	controller.SetEvictionCallback(func(streamID string, bytes int64) {
		mu.Lock()
		defer mu.Unlock()
		evictedStreams = append(evictedStreams, streamID)
		evictedBytes += bytes
		
		// Simulate actual eviction
		controller.ResetStreamUsage(streamID)
	})
	
	// Allocate memory for streams
	streams := []struct {
		id       string
		size     int64
		priority int
	}{
		{"stream1", 150 * 1024 * 1024, 5},
		{"stream2", 150 * 1024 * 1024, 3},
		{"stream3", 150 * 1024 * 1024, 7},
	}
	
	// Allocate memory for each stream
	for _, s := range streams {
		controller.TrackStream(s.id, s.priority)
		err := controller.RequestMemory(s.id, s.size)
		require.NoError(t, err)
		
		// Simulate some access patterns
		time.Sleep(10 * time.Millisecond)
		if s.id != "stream3" { // stream3 won't be accessed
			controller.UpdateStreamAccess(s.id)
		}
	}
	
	// Check current usage (should be 450MB)
	stats := controller.Stats()
	assert.Equal(t, int64(450*1024*1024), stats.GlobalUsage)
	assert.Equal(t, 3, stats.ActiveStreams)
	
	// Try to allocate more memory (should trigger eviction)
	controller.TrackStream("stream4", 1)
	err := controller.RequestMemory("stream4", 100*1024*1024) // 100MB more
	
	// Should succeed after eviction
	require.NoError(t, err)
	
	// Check eviction happened
	mu.Lock()
	assert.Greater(t, len(evictedStreams), 0, "Should have evicted at least one stream")
	assert.GreaterOrEqual(t, evictedBytes, int64(100*1024*1024), "Should have evicted enough memory")
	mu.Unlock()
	
	// Verify stats
	finalStats := controller.Stats()
	assert.LessOrEqual(t, finalStats.GlobalUsage, maxMemory)
	assert.Greater(t, finalStats.EvictionCount, int64(0))
}

// TestStreamTracker tests stream tracking functionality
func TestStreamTracker(t *testing.T) {
	tracker := NewStreamTracker()
	
	// Track a stream
	tracker.TrackStream("stream1", 3)
	
	// Update access
	time.Sleep(10 * time.Millisecond)
	tracker.UpdateAccess("stream1")
	
	// Get info
	info := tracker.GetStreamInfo("stream1", 1024)
	assert.Equal(t, "stream1", info.StreamID)
	assert.Equal(t, int64(1024), info.MemoryUsage)
	assert.Equal(t, 3, info.Priority)
	assert.True(t, info.IsActive)
	
	// Set inactive
	tracker.SetActive("stream1", false)
	info = tracker.GetStreamInfo("stream1", 1024)
	assert.False(t, info.IsActive)
	
	// Remove stream
	tracker.RemoveStream("stream1")
	
	// Get info for removed stream (should return default)
	info = tracker.GetStreamInfo("stream1", 2048)
	assert.Equal(t, 5, info.Priority) // Default priority
	assert.True(t, info.IsActive)     // Default active
}

// TestEvictionUnderPressure tests eviction behavior under memory pressure
func TestEvictionUnderPressure(t *testing.T) {
	maxMemory := int64(100 * 1024 * 1024)     // 100MB total
	perStreamLimit := int64(50 * 1024 * 1024) // 50MB per stream
	
	controller := NewController(maxMemory, perStreamLimit)
	
	// Use LRU strategy for predictable behavior
	controller.SetEvictionStrategy(&LRUEvictionStrategy{})
	
	evictionCount := 0
	evictedStreams := make(map[string]int64)
	controller.SetEvictionCallback(func(streamID string, bytes int64) {
		evictionCount++
		evictedStreams[streamID] = bytes
		// Simulate actual memory release
		controller.ReleaseMemory(streamID, bytes)
	})
	
	// Fill up to 85% (above 80% threshold)
	controller.TrackStream("stream1", 5)
	err := controller.RequestMemory("stream1", 45*1024*1024) // 45MB
	require.NoError(t, err)
	
	controller.TrackStream("stream2", 5)
	err = controller.RequestMemory("stream2", 40*1024*1024) // 40MB (total 85MB)
	require.NoError(t, err)
	
	assert.Equal(t, 0, evictionCount, "Should not evict on initial allocation")
	
	// Update access time for stream2 (make stream1 older)
	time.Sleep(10 * time.Millisecond)
	controller.UpdateStreamAccess("stream2")
	
	// Check current usage before allocation
	statsBeforeAlloc := controller.Stats()
	t.Logf("Usage before stream3 allocation: %d MB (%.1f%%)", 
		statsBeforeAlloc.GlobalUsage/(1024*1024), 
		float64(statsBeforeAlloc.GlobalUsage)/float64(maxMemory)*100)
	
	// Try to allocate more - we're already at 85%, so eviction should trigger
	controller.TrackStream("stream3", 5)
	err = controller.RequestMemory("stream3", 20*1024*1024) // 20MB more
	
	if err != nil {
		t.Logf("Allocation failed: %v", err)
		t.Logf("Eviction count: %d", evictionCount)
		statsAfterFail := controller.Stats()
		t.Logf("Usage after failed allocation: %d MB", statsAfterFail.GlobalUsage/(1024*1024))
	}
	
	require.NoError(t, err)
	
	assert.Greater(t, evictionCount, 0, "Should evict when over threshold")
	
	// Verify total usage is within limits
	stats := controller.Stats()
	assert.LessOrEqual(t, stats.GlobalUsage, maxMemory)
}
