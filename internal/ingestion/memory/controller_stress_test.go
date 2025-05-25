package memory

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestController_StressHighContention(t *testing.T) {
	// Test with many streams competing for limited memory
	totalMemory := int64(1024 * 1024 * 100) // 100MB total
	perStreamLimit := int64(1024 * 1024 * 10) // 10MB per stream
	ctrl := NewController(totalMemory, perStreamLimit)
	
	numStreams := 20 // More streams than can fit
	numRequests := 100
	requestSize := int64(1024 * 512) // 512KB per request
	
	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failureCount atomic.Int64
	var evictionCount atomic.Int64
	
	// Set eviction callback
	ctrl.SetEvictionCallback(func(streamID string, amount int64) {
		evictionCount.Add(1)
	})
	
	// Start multiple streams
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()
			streamID := fmt.Sprintf("stream-%d", streamNum)
			
			for j := 0; j < numRequests; j++ {
				// Random request size
				size := requestSize + int64(rand.Intn(int(requestSize)))
				
				if err := ctrl.RequestMemory(streamID, size); err == nil {
					successCount.Add(1)
					
					// Hold memory for a bit
					time.Sleep(time.Millisecond * time.Duration(rand.Intn(10)))
					
					// Random release
					if rand.Float32() < 0.8 {
						ctrl.ReleaseMemory(streamID, size)
					}
				} else {
					failureCount.Add(1)
				}
			}
			
			// Final cleanup
			ctrl.ResetStreamUsage(streamID)
		}(i)
	}
	
	wg.Wait()
	
	// Verify results
	t.Logf("Success: %d, Failures: %d, Evictions: %d", 
		successCount.Load(), failureCount.Load(), evictionCount.Load())
	
	// Should have some failures and evictions due to contention
	if failureCount.Load() == 0 {
		t.Error("Expected some allocation failures due to memory pressure")
	}
	
	if evictionCount.Load() == 0 {
		t.Error("Expected some evictions due to memory pressure")
	}
	
	// Memory should be fully released
	pressure := ctrl.GetPressure()
	if pressure > 0.01 {
		t.Errorf("Memory not fully released, pressure: %.2f", pressure)
	}
}

func TestController_StressRapidAllocDealloc(t *testing.T) {
	// Test rapid allocation/deallocation patterns
	totalMemory := int64(1024 * 1024 * 50) // 50MB
	perStreamLimit := int64(1024 * 1024 * 25) // 25MB per stream
	ctrl := NewController(totalMemory, perStreamLimit)
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	var operations atomic.Int64
	
	// Start rapid allocator/deallocator
	go func() {
		streamID := "rapid-stream"
		sizes := []int64{1024, 4096, 16384, 65536, 262144}
		
		for {
			select {
			case <-ctx.Done():
				return
			default:
				size := sizes[rand.Intn(len(sizes))]
				
				if err := ctrl.RequestMemory(streamID, size); err == nil {
					operations.Add(1)
					// Immediately release
					ctrl.ReleaseMemory(streamID, size)
					operations.Add(1)
				}
			}
		}
	}()
	
	// Start another stream doing larger allocations
	go func() {
		streamID := "bulk-stream"
		bulkSize := int64(1024 * 1024 * 5) // 5MB chunks
		
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if err := ctrl.RequestMemory(streamID, bulkSize); err == nil {
					operations.Add(1)
					time.Sleep(time.Millisecond * 10)
					ctrl.ReleaseMemory(streamID, bulkSize)
					operations.Add(1)
				}
			}
		}
	}()
	
	<-ctx.Done()
	time.Sleep(time.Millisecond * 100) // Let operations complete
	
	t.Logf("Completed %d operations", operations.Load())
	
	// Verify no memory leaks
	pressure := ctrl.GetPressure()
	if pressure > 0.01 {
		t.Errorf("Memory leak detected, pressure: %.2f", pressure)
	}
}

func TestController_StressFragmentation(t *testing.T) {
	// Test memory fragmentation scenarios
	totalMemory := int64(1024 * 1024 * 100) // 100MB
	perStreamLimit := int64(1024 * 1024 * 50) // 50MB per stream
	ctrl := NewController(totalMemory, perStreamLimit)
	
	numStreams := 10
	var wg sync.WaitGroup
	
	// Each stream allocates different sized chunks
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()
			streamID := fmt.Sprintf("stream-%d", streamNum)
			
			// Allocate in increasing sizes
			for size := int64(1024); size <= 1024*1024; size *= 2 {
				if err := ctrl.RequestMemory(streamID, size); err != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			
			// Release in random order
			usage := ctrl.GetStreamUsage(streamID)
			for usage > 0 {
				releaseSize := int64(1024) << uint(rand.Intn(10))
				if releaseSize > usage {
					releaseSize = usage
				}
				ctrl.ReleaseMemory(streamID, releaseSize)
				usage = ctrl.GetStreamUsage(streamID)
			}
		}(i)
	}
	
	wg.Wait()
	
	// All memory should be freed
	pressure := ctrl.GetPressure()
	if pressure > 0.01 {
		t.Errorf("Memory not fully freed: pressure %.2f", pressure)
	}
}

func TestController_StressEvictionPressure(t *testing.T) {
	// Test eviction under extreme pressure
	totalMemory := int64(1024 * 1024 * 10) // 10MB total (very limited)
	perStreamLimit := int64(1024 * 1024 * 5) // 5MB per stream
	ctrl := NewController(totalMemory, perStreamLimit)
	
	evictionMap := make(map[string]int64)
	var evictionMu sync.Mutex
	
	ctrl.SetEvictionCallback(func(streamID string, amount int64) {
		evictionMu.Lock()
		evictionMap[streamID] += amount
		evictionMu.Unlock()
	})
	
	// Start many streams trying to allocate maximum
	numStreams := 10
	var wg sync.WaitGroup
	
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamNum int) {
			defer wg.Done()
			streamID := fmt.Sprintf("stream-%d", streamNum)
			
			// Try to allocate full limit
			allocSize := perStreamLimit
			attempts := 0
			
			for attempts < 10 {
				if err := ctrl.RequestMemory(streamID, allocSize); err == nil {
					// Hold for a bit
					time.Sleep(time.Millisecond * 50)
					break
				}
				attempts++
				time.Sleep(time.Millisecond * 10)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Check eviction happened
	evictionMu.Lock()
	totalEvicted := int64(0)
	for _, amount := range evictionMap {
		totalEvicted += amount
	}
	evictionMu.Unlock()
	
	t.Logf("Total evicted: %d bytes from %d streams", totalEvicted, len(evictionMap))
	
	if len(evictionMap) == 0 {
		t.Error("Expected evictions but none occurred")
	}
}

func TestController_StressConcurrentStats(t *testing.T) {
	// Test concurrent access to statistics
	totalMemory := int64(1024 * 1024 * 100) // 100MB
	perStreamLimit := int64(1024 * 1024 * 10) // 10MB per stream
	ctrl := NewController(totalMemory, perStreamLimit)
	
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	// Start allocators
	for i := 0; i < 5; i++ {
		go func(streamNum int) {
			streamID := fmt.Sprintf("stream-%d", streamNum)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					size := int64(rand.Intn(1024*1024) + 1024)
					if err := ctrl.RequestMemory(streamID, size); err == nil {
						time.Sleep(time.Microsecond * 100)
						ctrl.ReleaseMemory(streamID, size/2) // Partial release
					}
				}
			}
		}(i)
	}
	
	// Start stats readers
	var statsErrors atomic.Int64
	for i := 0; i < 3; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					pressure := ctrl.GetPressure()
					if pressure < 0 || pressure > 1 {
						statsErrors.Add(1)
					}
					
					for j := 0; j < 5; j++ {
						streamID := fmt.Sprintf("stream-%d", j)
						usage := ctrl.GetStreamUsage(streamID)
						if usage < 0 {
							statsErrors.Add(1)
						}
					}
				}
			}
		}()
	}
	
	<-ctx.Done()
	
	if statsErrors.Load() > 0 {
		t.Errorf("Stats returned invalid values: %d errors", statsErrors.Load())
	}
}
