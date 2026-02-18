package memory

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrGlobalMemoryLimit indicates the global memory limit has been reached
	ErrGlobalMemoryLimit = errors.New("global memory limit exceeded")

	// ErrStreamMemoryLimit indicates a stream's memory limit has been reached
	ErrStreamMemoryLimit = errors.New("stream memory limit exceeded")
)

// Controller manages memory allocation and limits for the ingestion service
type Controller struct {
	maxMemory      int64 // Total memory budget
	perStreamLimit int64 // Per-stream limit
	usage          atomic.Int64
	streamUsage    sync.Map // streamID -> *atomic.Int64

	// Memory pressure relief
	pressureThreshold float64
	evictionCallback  func(streamID string, bytes int64)
	evictionStrategy  EvictionStrategy
	streamTracker     *StreamTracker

	// Metrics
	allocationCount atomic.Int64
	releaseCount    atomic.Int64
	evictionCount   atomic.Int64
	lastGCTime      time.Time
	mu              sync.Mutex

	// Stream initialization mutex
	streamInitMu sync.Mutex
}

// NewController creates a new memory controller
func NewController(maxMemory, perStreamLimit int64) *Controller {
	return &Controller{
		maxMemory:         maxMemory,
		perStreamLimit:    perStreamLimit,
		pressureThreshold: 0.8, // Start eviction at 80%
		lastGCTime:        time.Now(),
		streamTracker:     NewStreamTracker(),
		evictionStrategy: &HybridEvictionStrategy{
			AgeWeight:      0.4,
			SizeWeight:     0.4,
			PriorityWeight: 0.2,
		},
	}
}

// SetEvictionCallback sets the callback for memory pressure eviction
func (c *Controller) SetEvictionCallback(callback func(streamID string, bytes int64)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictionCallback = callback
}

// RequestMemory requests memory allocation for a stream
func (c *Controller) RequestMemory(streamID string, size int64) error {
	// Check global limit
	newUsage := c.usage.Add(size)
	if newUsage > c.maxMemory {
		c.usage.Add(-size)

		// Try garbage collection first
		c.mu.Lock()
		if time.Since(c.lastGCTime) > 10*time.Second {
			runtime.GC()
			c.lastGCTime = time.Now()
			c.mu.Unlock()

			// Retry after GC
			newUsage = c.usage.Add(size)
			if newUsage <= c.maxMemory {
				goto checkStreamLimit
			}
			c.usage.Add(-size)
		} else {
			c.mu.Unlock()
		}

		// Check pressure level and try eviction
		pressure := float64(c.usage.Load()) / float64(c.maxMemory)
		if pressure > c.pressureThreshold {
			// Try to evict enough memory
			evicted := c.evictMemory(size)
			if evicted > 0 {
				// Retry after eviction - global usage was already rolled back at line 69
				newUsage = c.usage.Add(size)
				if newUsage <= c.maxMemory {
					goto checkStreamLimit
				}
				c.usage.Add(-size)
			}
		}

		return ErrGlobalMemoryLimit
	}

checkStreamLimit:
	// Check per-stream limit
	usage := c.getOrCreateStreamUsage(streamID)

	if usage.Add(size) > c.perStreamLimit {
		// Rollback the stream usage increment
		usage.Add(-size)
		// Rollback the global usage increment (global was successfully added when we reached checkStreamLimit)
		c.usage.Add(-size)
		return ErrStreamMemoryLimit
	}

	c.allocationCount.Add(1)
	return nil
}

// getOrCreateStreamUsage returns the usage counter for a stream, creating it if needed
func (c *Controller) getOrCreateStreamUsage(streamID string) *atomic.Int64 {
	// Fast path: check if already exists
	if val, ok := c.streamUsage.Load(streamID); ok {
		return val.(*atomic.Int64)
	}

	// Slow path: create with mutex protection
	c.streamInitMu.Lock()
	defer c.streamInitMu.Unlock()

	// Double-check after acquiring lock
	if val, ok := c.streamUsage.Load(streamID); ok {
		return val.(*atomic.Int64)
	}

	// Create new usage counter
	usage := &atomic.Int64{}
	c.streamUsage.Store(streamID, usage)
	return usage
}

// ReleaseMemory releases memory for a stream
func (c *Controller) ReleaseMemory(streamID string, size int64) {
	// Only release memory if the stream exists in our tracking
	if streamUsage, ok := c.streamUsage.Load(streamID); ok {
		usage := streamUsage.(*atomic.Int64)
		// Use CAS loop to avoid TOCTOU race between Load and Add/Store
		for {
			oldUsage := usage.Load()
			if oldUsage <= 0 {
				// Nothing to release
				break
			}
			if oldUsage >= size {
				if usage.CompareAndSwap(oldUsage, oldUsage-size) {
					c.usage.Add(-size)
					break
				}
			} else {
				// Release only what was actually allocated for this stream
				if usage.CompareAndSwap(oldUsage, 0) {
					c.usage.Add(-oldUsage)
					break
				}
			}
			// CAS failed, retry
		}
	} else {
		// Stream not found - this is a programming error but handle gracefully
		// Don't modify global usage if we can't find the stream
		return
	}

	c.releaseCount.Add(1)
}

// GetPressure returns the current memory pressure (0.0 to 1.0)
func (c *Controller) GetPressure() float64 {
	return float64(c.usage.Load()) / float64(c.maxMemory)
}

// GetStreamUsage returns the memory usage for a specific stream
func (c *Controller) GetStreamUsage(streamID string) int64 {
	if streamUsage, ok := c.streamUsage.Load(streamID); ok {
		usage := streamUsage.(*atomic.Int64)
		return usage.Load()
	}
	return 0
}

// Stats returns memory controller statistics
func (c *Controller) Stats() MemoryStats {
	globalUsage := c.usage.Load()
	pressure := float64(globalUsage) / float64(c.maxMemory)

	// Count active streams
	activeStreams := 0
	var streamStats []StreamMemoryStats

	c.streamUsage.Range(func(key, value interface{}) bool {
		streamID := key.(string)
		usage := value.(*atomic.Int64).Load()
		if usage > 0 {
			activeStreams++
			streamStats = append(streamStats, StreamMemoryStats{
				StreamID: streamID,
				Usage:    usage,
				Percent:  float64(usage) / float64(c.perStreamLimit) * 100,
			})
		}
		return true
	})

	return MemoryStats{
		GlobalUsage:       globalUsage,
		GlobalLimit:       c.maxMemory,
		GlobalPressure:    pressure,
		PerStreamLimit:    c.perStreamLimit,
		ActiveStreams:     activeStreams,
		StreamStats:       streamStats,
		AllocationCount:   c.allocationCount.Load(),
		ReleaseCount:      c.releaseCount.Load(),
		EvictionCount:     c.evictionCount.Load(),
		PressureThreshold: c.pressureThreshold,
	}
}

// evictMemory attempts to evict memory to make room for the requested size
// Returns the amount of memory evicted
func (c *Controller) evictMemory(targetSize int64) int64 {
	// Snapshot callback and strategy under lock to avoid data race
	c.mu.Lock()
	evictionCallback := c.evictionCallback
	evictionStrategy := c.evictionStrategy
	c.mu.Unlock()

	if evictionCallback == nil {
		return 0
	}

	// Collect stream information
	var streams []StreamInfo
	c.streamUsage.Range(func(key, value interface{}) bool {
		streamID := key.(string)
		usage := value.(*atomic.Int64).Load()
		if usage > 0 {
			info := c.streamTracker.GetStreamInfo(streamID, usage)
			streams = append(streams, *info)
		}
		return true
	})

	if len(streams) == 0 {
		return 0
	}

	// Select streams for eviction
	selected := evictionStrategy.SelectStreamsForEviction(streams, targetSize)

	var totalEvicted int64
	for _, streamID := range selected {
		if streamUsage, ok := c.streamUsage.Load(streamID); ok {
			usage := streamUsage.(*atomic.Int64)
			// Atomically swap to 0 to get the usage and prevent double-decrement.
			// The callback is for notification only; we handle accounting here.
			usageBefore := usage.Swap(0)
			if usageBefore > 0 {
				c.usage.Add(-usageBefore)
				totalEvicted += usageBefore
			}
			// Notify callback after accounting (callback should NOT call ReleaseMemory)
			evictionCallback(streamID, usageBefore)
			c.evictionCount.Add(1)
		}
	}

	return totalEvicted
}

// ResetStreamUsage resets usage tracking for a stream
func (c *Controller) ResetStreamUsage(streamID string) {
	if streamUsage, ok := c.streamUsage.LoadAndDelete(streamID); ok {
		usage := streamUsage.(*atomic.Int64)
		remaining := usage.Swap(0) // Atomically zero AND get the old value
		if remaining > 0 {
			c.usage.Add(-remaining)
		}
	}
	c.streamTracker.RemoveStream(streamID)
}

// TrackStream registers a stream with the controller
func (c *Controller) TrackStream(streamID string, priority int) {
	c.streamTracker.TrackStream(streamID, priority)
}

// UpdateStreamAccess updates the last access time for a stream
func (c *Controller) UpdateStreamAccess(streamID string) {
	c.streamTracker.UpdateAccess(streamID)
}

// SetStreamActive marks a stream as active or inactive
func (c *Controller) SetStreamActive(streamID string, active bool) {
	c.streamTracker.SetActive(streamID, active)
}

// SetEvictionStrategy allows changing the eviction strategy
func (c *Controller) SetEvictionStrategy(strategy EvictionStrategy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictionStrategy = strategy
}

// MemoryStats holds memory controller statistics
type MemoryStats struct {
	GlobalUsage       int64
	GlobalLimit       int64
	GlobalPressure    float64
	PerStreamLimit    int64
	ActiveStreams     int
	StreamStats       []StreamMemoryStats
	AllocationCount   int64
	ReleaseCount      int64
	EvictionCount     int64
	PressureThreshold float64
}

// StreamMemoryStats holds per-stream memory statistics
type StreamMemoryStats struct {
	StreamID string
	Usage    int64
	Percent  float64
}
