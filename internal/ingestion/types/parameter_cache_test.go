package types

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestParameterSetCache_PutAndGet(t *testing.T) {
	cache := NewParameterSetCache(10, time.Minute)
	defer cache.Close()

	paramSet := &ParameterSet{
		ID:    0,
		Data:  []byte{0x67, 0x42, 0x00, 0x1f},
		Valid: true,
	}

	// Test Put and Get
	cache.Put("test_sps", paramSet)

	retrieved, exists := cache.Get("test_sps")
	if !exists {
		t.Fatal("Expected to retrieve parameter set, got not found")
	}
	if retrieved == nil {
		t.Fatal("Expected to retrieve parameter set, got nil")
	}

	if retrieved.ID != paramSet.ID {
		t.Errorf("Expected ID %d, got %d", paramSet.ID, retrieved.ID)
	}

	if !retrieved.Valid {
		t.Errorf("Expected Valid true, got %v", retrieved.Valid)
	}
}

func TestParameterSetCache_TTLExpiration(t *testing.T) {
	shortTTL := 10 * time.Millisecond
	cache := NewParameterSetCache(10, shortTTL)
	defer cache.Close()

	paramSet := &ParameterSet{
		ID:    0,
		Data:  []byte{0x67, 0x42, 0x00, 0x1f},
		Valid: true,
	}

	// Put and immediately retrieve
	cache.Put("test_sps", paramSet)
	if _, exists := cache.Get("test_sps"); !exists {
		t.Error("Should be able to retrieve immediately after putting")
	}

	// Wait for TTL expiration
	time.Sleep(shortTTL + 5*time.Millisecond)

	// Force cleanup to run
	cache.forceCleanup()

	if _, exists := cache.Get("test_sps"); exists {
		t.Error("Parameter set should be expired after TTL")
	}
}

func TestParameterSetCache_LRUEviction(t *testing.T) {
	cache := NewParameterSetCache(2, time.Hour) // Small capacity for LRU testing
	defer cache.Close()

	paramSet1 := &ParameterSet{ID: 1, Data: []byte{0x01}, Valid: true}
	paramSet2 := &ParameterSet{ID: 2, Data: []byte{0x02}, Valid: true}
	paramSet3 := &ParameterSet{ID: 3, Data: []byte{0x03}, Valid: true}

	// Fill cache to capacity
	cache.Put("param1", paramSet1)
	cache.Put("param2", paramSet2)

	// Both should be retrievable
	if _, exists := cache.Get("param1"); !exists {
		t.Error("param1 should be in cache")
	}
	if _, exists := cache.Get("param2"); !exists {
		t.Error("param2 should be in cache")
	}

	// Add third parameter set, should evict least recently used (param1)
	cache.Put("param3", paramSet3)

	// param1 should be evicted, param2 and param3 should remain
	if _, exists := cache.Get("param1"); exists {
		t.Error("param1 should be evicted")
	}
	if _, exists := cache.Get("param2"); !exists {
		t.Error("param2 should still be in cache")
	}
	if _, exists := cache.Get("param3"); !exists {
		t.Error("param3 should be in cache")
	}
}

func TestParameterSetCache_ConcurrentAccess(t *testing.T) {
	cache := NewParameterSetCache(100, time.Hour)
	defer cache.Close()

	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // writers and readers

	// Start writers
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("worker_%d_param_%d", workerID, j)
				paramSet := &ParameterSet{
					ID:    uint8(j),
					Data:  []byte{byte(workerID), byte(j)},
					Valid: true,
				}
				cache.Put(key, paramSet)
			}
		}(i)
	}

	// Start readers
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("worker_%d_param_%d", workerID, j)
				// Try to read, may or may not exist depending on timing
				cache.Get(key)
			}
		}(i)
	}

	wg.Wait()

	// Check that cache operations completed without panics
	stats := cache.GetStatistics()
	if stats == nil {
		t.Error("Should be able to get statistics after concurrent operations")
	}
}

func TestParameterSetCache_Statistics(t *testing.T) {
	cache := NewParameterSetCache(10, time.Hour)
	defer cache.Close()

	paramSet := &ParameterSet{
		ID:    0,
		Data:  []byte{0x67, 0x42, 0x00, 0x1f},
		Valid: true,
	}

	// Initial statistics
	stats := cache.GetStatistics()
	if stats["size"].(int) != 0 {
		t.Error("Initial cache should be empty")
	}
	if stats["hits"].(uint64) != 0 {
		t.Error("Initial hit count should be 0")
	}

	// Add parameter set
	cache.Put("test_sps", paramSet)
	stats = cache.GetStatistics()
	if stats["size"].(int) != 1 {
		t.Error("Cache should have 1 entry after put")
	}

	// Successful get (cache hit)
	if _, exists := cache.Get("test_sps"); !exists {
		t.Error("Should be able to retrieve parameter set")
	}
	stats = cache.GetStatistics()
	if stats["hits"].(uint64) != 1 {
		t.Error("Hit count should be 1 after successful get")
	}

	// Failed get (cache miss)
	if _, exists := cache.Get("nonexistent"); exists {
		t.Error("Should not retrieve nonexistent parameter set")
	}
	stats = cache.GetStatistics()
	if stats["misses"].(uint64) != 1 {
		t.Error("Miss count should be 1 after failed get")
	}
}

func TestParameterSetCache_CleanupOnClose(t *testing.T) {
	cache := NewParameterSetCache(10, time.Hour)

	paramSet := &ParameterSet{
		ID:    0,
		Data:  []byte{0x67, 0x42, 0x00, 0x1f},
		Valid: true,
	}

	cache.Put("test_sps", paramSet)

	// Verify it's in cache
	if _, exists := cache.Get("test_sps"); !exists {
		t.Error("Parameter set should be in cache before close")
	}

	// Close cache
	cache.Close()

	// Verify cleanup stopped (this is hard to test directly, but we can check that
	// the cache still functions for existing entries)
	if _, exists := cache.Get("test_sps"); !exists {
		t.Error("Existing entries should still be accessible after close")
	}

	// Note: Multiple closes might not be safe with current implementation
}

func TestParameterSetCache_ZeroCapacity(t *testing.T) {
	// Zero capacity should still work but evict immediately
	cache := NewParameterSetCache(0, time.Hour)
	defer cache.Close()

	paramSet := &ParameterSet{
		ID:    0,
		Data:  []byte{0x67, 0x42, 0x00, 0x1f},
		Valid: true,
	}

	cache.Put("test_sps", paramSet)

	// Should not be able to retrieve due to zero capacity
	if _, exists := cache.Get("test_sps"); exists {
		t.Error("Zero capacity cache should not store entries")
	}
}

func TestParameterSetCache_UpdateExisting(t *testing.T) {
	cache := NewParameterSetCache(10, time.Hour)
	defer cache.Close()

	paramSet1 := &ParameterSet{
		ID:    0,
		Data:  []byte{0x67, 0x42, 0x00, 0x1f},
		Valid: true,
	}

	paramSet2 := &ParameterSet{
		ID:    1,
		Data:  []byte{0x67, 0x42, 0x00, 0x2f},
		Valid: true,
	}

	// Put first parameter set
	cache.Put("test_sps", paramSet1)
	retrieved, _ := cache.Get("test_sps")
	if retrieved.ID != 0 {
		t.Error("Should retrieve first parameter set")
	}

	// Update with second parameter set
	cache.Put("test_sps", paramSet2)
	retrieved, _ = cache.Get("test_sps")
	if retrieved.ID != 1 {
		t.Error("Should retrieve updated parameter set")
	}

	// Cache size should still be 1
	stats := cache.GetStatistics()
	if stats["size"].(int) != 1 {
		t.Error("Cache should still have only 1 entry after update")
	}
}

// forceCleanup forces the cleanup for testing - simplified version
func (c *ParameterSetCache) forceCleanup() {
	// Wait a bit for the background cleanup goroutine to run
	time.Sleep(15 * time.Millisecond)
}
