package memory

import (
	"testing"
	"time"
)

// TestMemoryControllerDoubleReleaseFixed tests that the memory controller
// correctly handles global and stream memory accounting
func TestMemoryControllerDoubleReleaseFixed(t *testing.T) {
	// Create controller with 1000 bytes global, 500 bytes per stream
	controller := NewController(1000, 500)
	
	// Test case 1: Normal allocation should work
	err := controller.RequestMemory("stream1", 400)
	if err != nil {
		t.Fatalf("Normal allocation failed: %v", err)
	}
	
	// Verify global usage is 400
	if controller.usage.Load() != 400 {
		t.Errorf("Expected global usage 400, got %d", controller.usage.Load())
	}
	
	// Verify stream usage is 400
	if controller.GetStreamUsage("stream1") != 400 {
		t.Errorf("Expected stream1 usage 400, got %d", controller.GetStreamUsage("stream1"))
	}
	
	// Test case 2: Stream limit exceeded should rollback properly
	err = controller.RequestMemory("stream1", 200) // Would make stream1 = 600 > 500 limit
	if err != ErrStreamMemoryLimit {
		t.Fatalf("Expected stream memory limit error, got: %v", err)
	}
	
	// Verify global usage is still 400 (no double decrement)
	if controller.usage.Load() != 400 {
		t.Errorf("Expected global usage still 400 after stream limit failure, got %d", controller.usage.Load())
	}
	
	// Verify stream usage is still 400
	if controller.GetStreamUsage("stream1") != 400 {
		t.Errorf("Expected stream1 usage still 400 after limit failure, got %d", controller.GetStreamUsage("stream1"))
	}
	
	// Test case 3: Global limit exceeded should rollback properly
	err = controller.RequestMemory("stream2", 700) // Would make global = 1100 > 1000 limit
	if err != ErrGlobalMemoryLimit {
		t.Fatalf("Expected global memory limit error, got: %v", err)
	}
	
	// Verify global usage is still 400 (no change due to global limit)
	if controller.usage.Load() != 400 {
		t.Errorf("Expected global usage still 400 after global limit failure, got %d", controller.usage.Load())
	}
	
	// Verify stream2 was never allocated
	if controller.GetStreamUsage("stream2") != 0 {
		t.Errorf("Expected stream2 usage 0 after global limit failure, got %d", controller.GetStreamUsage("stream2"))
	}
	
	// Test case 4: Release should work correctly
	controller.ReleaseMemory("stream1", 200)
	
	// Verify global usage is now 200
	if controller.usage.Load() != 200 {
		t.Errorf("Expected global usage 200 after release, got %d", controller.usage.Load())
	}
	
	// Verify stream usage is now 200
	if controller.GetStreamUsage("stream1") != 200 {
		t.Errorf("Expected stream1 usage 200 after release, got %d", controller.GetStreamUsage("stream1"))
	}
}

// TestMemoryControllerGCRetryPath tests the GC retry logic
func TestMemoryControllerGCRetryPath(t *testing.T) {
	controller := NewController(1000, 800)
	controller.lastGCTime = time.Now().Add(-20 * time.Second) // Force GC to be allowed
	
	// Fill up most of the memory (within stream limit)
	err := controller.RequestMemory("stream1", 700)
	if err != nil {
		t.Fatalf("Initial allocation failed: %v", err)
	}
	
	// This should trigger GC path but still fail due to global limit
	err = controller.RequestMemory("stream2", 400) // 700 + 400 = 1100 > 1000
	if err != ErrGlobalMemoryLimit {
		t.Fatalf("Expected global memory limit error after GC retry, got: %v", err)
	}
	
	// Verify accounting is correct - no memory leaked
	expectedGlobal := int64(700) // Only stream1 should be allocated
	if controller.usage.Load() != expectedGlobal {
		t.Errorf("Expected global usage %d after GC retry failure, got %d", expectedGlobal, controller.usage.Load())
	}
	
	if controller.GetStreamUsage("stream2") != 0 {
		t.Errorf("Expected stream2 usage 0 after GC retry failure, got %d", controller.GetStreamUsage("stream2"))
	}
}

// TestMemoryControllerEvictionRetryPath tests the eviction retry logic
func TestMemoryControllerEvictionRetryPath(t *testing.T) {
	controller := NewController(1000, 800)
	controller.pressureThreshold = 0.5 // Lower threshold to trigger eviction
	
	// Set up eviction callback that does nothing (simulates failed eviction)
	evictionCalled := false
	controller.SetEvictionCallback(func(streamID string, bytes int64) {
		evictionCalled = true
		// Don't actually release memory to simulate eviction failure
	})
	
	// Fill up memory to trigger eviction path
	err := controller.RequestMemory("stream1", 600)
	if err != nil {
		t.Fatalf("Initial allocation failed: %v", err)
	}
	
	// This should trigger eviction path but still fail
	err = controller.RequestMemory("stream2", 500)
	if err != ErrGlobalMemoryLimit {
		t.Fatalf("Expected global memory limit error after eviction retry, got: %v", err)
	}
	
	// Verify eviction was attempted
	if !evictionCalled {
		t.Error("Expected eviction callback to be called")
	}
	
	// Verify accounting is correct after failed eviction
	if controller.usage.Load() != 600 {
		t.Errorf("Expected global usage 600 after eviction retry failure, got %d", controller.usage.Load())
	}
	
	if controller.GetStreamUsage("stream2") != 0 {
		t.Errorf("Expected stream2 usage 0 after eviction retry failure, got %d", controller.GetStreamUsage("stream2"))
	}
}