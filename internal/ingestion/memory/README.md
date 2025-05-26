# Memory Management Package

This package provides intelligent memory management for the Mirror video streaming platform. It monitors memory usage, implements eviction policies, and prevents out-of-memory conditions during high-load scenarios.

## Overview

Video streaming applications can consume large amounts of memory due to buffering requirements. This package provides:

- **Usage Monitoring**: Real-time memory usage tracking
- **Eviction Policies**: Intelligent data removal when limits approached
- **Pressure Detection**: Early warning system for memory constraints
- **Per-Stream Limits**: Individual stream memory quotas
- **Global Controls**: System-wide memory management

## Architecture

```
┌─────────────────┐
│ Memory Monitor  │ ← System Memory Stats
├─────────────────┤
│ • RSS Tracking  │
│ • Heap Analysis │
│ • GC Monitoring │
│ • Allocation Rate│
└─────────────────┘
         │
         ▼
┌─────────────────┐
│ Controller      │
├─────────────────┤
│ • Calculate     │
│   Pressure      │
│ • Trigger       │
│   Eviction      │
│ • Enforce       │
│   Limits        │
└─────────────────┘
         │
    ┌────┴────┐
    ▼         ▼
┌─────────┐ ┌──────────┐
│Stream   │ │Global    │
│Eviction │ │Eviction  │
└─────────┘ └──────────┘
```

## Configuration

```go
type ControllerConfig struct {
    // Global memory limits
    GlobalLimit       int64         // Total memory limit (bytes)
    PerStreamLimit    int64         // Per-stream memory limit (bytes)
    
    // Pressure thresholds
    SoftLimit         float64       // Start eviction (default: 0.8)
    HardLimit         float64       // Aggressive eviction (default: 0.95)
    CriticalLimit     float64       // Emergency actions (default: 0.98)
    
    // Monitoring settings
    MonitorInterval   time.Duration // Memory check interval
    GCThreshold       float64       // GC pressure threshold
    
    // Eviction policies
    EvictionPolicy    EvictionPolicy // LRU, Priority, etc.
    MinRetentionTime  time.Duration  // Minimum data retention
    
    // Performance tuning
    AllocationRate    int64         // Max allocation rate (bytes/sec)
    FragmentationLimit float64      // Heap fragmentation threshold
}
```

## Core Components

### Memory Controller
The main controller monitors system memory and coordinates eviction:

```go
import "github.com/zsiec/mirror/internal/ingestion/memory"

// Create memory controller
config := memory.ControllerConfig{
    GlobalLimit:      8 * 1024 * 1024 * 1024, // 8GB
    PerStreamLimit:   400 * 1024 * 1024,      // 400MB per stream
    SoftLimit:        0.8,
    HardLimit:        0.95,
    MonitorInterval:  1 * time.Second,
}

controller, err := memory.NewController(config)
if err != nil {
    return fmt.Errorf("failed to create memory controller: %w", err)
}

// Start monitoring
controller.Start()
defer controller.Stop()
```

### Stream Memory Tracking
Each stream has its own memory quota and tracking:

```go
// Register a new stream for memory tracking
streamID := "stream-123"
streamLimit := 400 * 1024 * 1024 // 400MB

tracker := controller.CreateStreamTracker(streamID, streamLimit)

// Track memory allocation
tracker.Allocate("frames", frameData)
tracker.Allocate("gop_buffer", gopData)

// Check current usage
usage := tracker.GetUsage()
fmt.Printf("Stream %s using %d bytes\n", streamID, usage.TotalBytes)
```

## Memory Monitoring

### System Memory Tracking
```go
type MemoryStats struct {
    // System memory
    TotalSystem    uint64 `json:"total_system"`
    AvailableSystem uint64 `json:"available_system"`
    UsedSystem     uint64 `json:"used_system"`
    
    // Process memory
    RSS            uint64 `json:"rss"`           // Resident Set Size
    VMS            uint64 `json:"vms"`           // Virtual Memory Size
    HeapAlloc      uint64 `json:"heap_alloc"`    // Currently allocated
    HeapSys        uint64 `json:"heap_sys"`      // System heap
    HeapIdle       uint64 `json:"heap_idle"`     // Idle heap
    HeapInuse      uint64 `json:"heap_inuse"`    // In-use heap
    
    // GC statistics
    GCCount        uint32 `json:"gc_count"`      // Number of GCs
    GCPauseTotal   uint64 `json:"gc_pause_total"`// Total GC pause time
    GCPauseAvg     uint64 `json:"gc_pause_avg"`  // Average GC pause
    
    // Allocation tracking
    AllocRate      uint64 `json:"alloc_rate"`    // Allocation rate (bytes/sec)
    Fragmentation  float64 `json:"fragmentation"` // Heap fragmentation %
}

// Get current memory statistics
stats := controller.GetMemoryStats()
fmt.Printf("Memory usage: %d/%d bytes (%.1f%%)\n", 
    stats.HeapInuse, config.GlobalLimit, 
    float64(stats.HeapInuse)/float64(config.GlobalLimit)*100)
```

### Per-Stream Memory Usage
```go
type StreamMemoryUsage struct {
    StreamID       string `json:"stream_id"`
    TotalBytes     int64  `json:"total_bytes"`
    LimitBytes     int64  `json:"limit_bytes"`
    UsagePercent   float64 `json:"usage_percent"`
    
    // Breakdown by component
    Frames         int64  `json:"frames"`
    GOPBuffer      int64  `json:"gop_buffer"`
    Packets        int64  `json:"packets"`
    Metadata       int64  `json:"metadata"`
    
    // Timing information
    LastUpdate     time.Time `json:"last_update"`
    AllocationRate int64     `json:"allocation_rate"` // bytes/sec
}
```

## Eviction Policies

### LRU (Least Recently Used)
Default eviction policy that removes oldest unused data:

```go
type LRUPolicy struct {
    maxEntries int
    items      map[string]*lruItem
    order      *list.List
}

// Evict least recently used items
func (p *LRUPolicy) Evict(bytesNeeded int64) []EvictionCandidate {
    var candidates []EvictionCandidate
    
    for bytesNeeded > 0 && p.order.Len() > 0 {
        // Get least recently used item
        oldest := p.order.Back()
        item := oldest.Value.(*lruItem)
        
        candidates = append(candidates, EvictionCandidate{
            Key:   item.key,
            Bytes: item.size,
            Age:   time.Since(item.lastAccess),
        })
        
        bytesNeeded -= item.size
        p.order.Remove(oldest)
        delete(p.items, item.key)
    }
    
    return candidates
}
```

### Priority-Based Eviction
Evicts data based on importance and video structure:

```go
type PriorityPolicy struct {
    priorities map[string]int // component -> priority
}

// Priority levels (higher number = higher priority, kept longer)
const (
    PriorityKeyframes = 100  // Always keep keyframes
    PriorityPFrames   = 80   // Keep P-frames when possible
    PriorityBFrames   = 60   // Drop B-frames first
    PriorityMetadata  = 40   // Drop metadata early
    PriorityPackets   = 20   // Drop raw packets first
)

func (p *PriorityPolicy) Evict(bytesNeeded int64) []EvictionCandidate {
    // Sort by priority (lowest first for eviction)
    candidates := p.getCandidatesByPriority()
    
    var toEvict []EvictionCandidate
    for _, candidate := range candidates {
        if bytesNeeded <= 0 {
            break
        }
        
        // Skip high-priority items unless critical
        if candidate.Priority > PriorityBFrames && !p.criticalMode {
            continue
        }
        
        toEvict = append(toEvict, candidate)
        bytesNeeded -= candidate.Bytes
    }
    
    return toEvict
}
```

## Memory Pressure Handling

### Pressure Levels
```go
type PressureLevel int

const (
    PressureNone PressureLevel = iota
    PressureLow      // 60-80% usage
    PressureModerate // 80-95% usage
    PressureHigh     // 95-98% usage
    PressureCritical // >98% usage
)

// Handle different pressure levels
func (c *Controller) handlePressure(level PressureLevel) {
    switch level {
    case PressureLow:
        // Start background cleanup
        c.scheduleCleanup()
        
    case PressureModerate:
        // Begin soft eviction
        c.evictLowPriorityData()
        
    case PressureHigh:
        // Aggressive eviction
        c.evictNonEssentialData()
        c.requestGC()
        
    case PressureCritical:
        // Emergency measures
        c.emergencyEviction()
        c.pauseNewAllocations()
        c.notifyBackpressure()
    }
}
```

### Automatic Actions
```go
// Automatic responses to memory pressure
func (c *Controller) monitorAndRespond() {
    ticker := time.NewTicker(c.config.MonitorInterval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            stats := c.getMemoryStats()
            pressure := c.calculatePressure(stats)
            
            switch {
            case pressure > c.config.CriticalLimit:
                c.handlePressure(PressureCritical)
            case pressure > c.config.HardLimit:
                c.handlePressure(PressureHigh)
            case pressure > c.config.SoftLimit:
                c.handlePressure(PressureModerate)
            default:
                c.handlePressure(PressureNone)
            }
            
        case <-c.ctx.Done():
            return
        }
    }
}
```

## Integration with Stream Components

### Frame Buffer Integration
```go
// Frame buffer with memory awareness
type MemoryAwareFrameBuffer struct {
    frames   []*Frame
    tracker  *memory.StreamTracker
    maxSize  int64
}

func (fb *MemoryAwareFrameBuffer) AddFrame(frame *Frame) error {
    // Check memory limit before adding
    if fb.tracker.WouldExceedLimit(int64(len(frame.Data))) {
        // Evict old frames to make space
        fb.evictOldFrames(int64(len(frame.Data)))
    }
    
    // Track allocation
    fb.tracker.Allocate("frame", frame.Data)
    fb.frames = append(fb.frames, frame)
    
    return nil
}
```

### GOP Buffer Integration
```go
// GOP buffer with memory limits
func (gb *GOPBuffer) AddGOP(gop *GOP) error {
    gopSize := gop.GetSize()
    
    // Check if addition would exceed memory limit
    if gb.memoryTracker.WouldExceedLimit(gopSize) {
        // Remove oldest GOP if necessary
        if len(gb.gops) > 0 {
            oldest := gb.gops[0]
            gb.memoryTracker.Deallocate("gop", oldest.GetSize())
            gb.gops = gb.gops[1:]
        }
    }
    
    // Add new GOP
    gb.memoryTracker.Allocate("gop", gopSize)
    gb.gops = append(gb.gops, gop)
    
    return nil
}
```

## Performance Optimization

### Memory Pooling
```go
type FramePool struct {
    pools map[int]*sync.Pool // size -> pool
    sizes []int              // available sizes
}

// Get frame from pool to reduce allocations
func (fp *FramePool) GetFrame(size int) *Frame {
    poolSize := fp.findOptimalSize(size)
    pool := fp.pools[poolSize]
    
    if frame := pool.Get(); frame != nil {
        return frame.(*Frame)
    }
    
    // Create new frame if pool empty
    return &Frame{Data: make([]byte, poolSize)}
}

// Return frame to pool
func (fp *FramePool) PutFrame(frame *Frame) {
    size := cap(frame.Data)
    if pool, exists := fp.pools[size]; exists {
        // Reset frame data
        frame.Reset()
        pool.Put(frame)
    }
}
```

### Allocation Rate Limiting
```go
type RateLimiter struct {
    rate     int64         // bytes per second
    bucket   int64         // current tokens
    lastTime time.Time
    mu       sync.Mutex
}

func (rl *RateLimiter) AllowAllocation(bytes int64) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    now := time.Now()
    elapsed := now.Sub(rl.lastTime)
    
    // Add tokens based on elapsed time
    tokens := int64(elapsed.Seconds() * float64(rl.rate))
    rl.bucket = min(rl.bucket + tokens, rl.rate) // cap at rate
    rl.lastTime = now
    
    // Check if we have enough tokens
    if rl.bucket >= bytes {
        rl.bucket -= bytes
        return true
    }
    
    return false
}
```

## Monitoring and Metrics

### Memory Metrics Collection
```go
type Metrics struct {
    // Usage metrics
    GlobalUsage      prometheus.Gauge
    StreamUsage      *prometheus.GaugeVec
    HeapSize         prometheus.Gauge
    
    // Pressure metrics
    PressureLevel    prometheus.Gauge
    EvictionCount    prometheus.Counter
    
    // Performance metrics
    AllocationRate   prometheus.Gauge
    GCPauseTime      prometheus.Histogram
    FragmentationPct prometheus.Gauge
}

// Update metrics periodically
func (c *Controller) updateMetrics() {
    stats := c.GetMemoryStats()
    
    c.metrics.GlobalUsage.Set(float64(stats.HeapInuse))
    c.metrics.HeapSize.Set(float64(stats.HeapSys))
    c.metrics.AllocationRate.Set(float64(stats.AllocRate))
    c.metrics.FragmentationPct.Set(stats.Fragmentation)
    
    // Update per-stream metrics
    for _, stream := range c.streams {
        usage := stream.GetUsage()
        c.metrics.StreamUsage.WithLabelValues(stream.ID).Set(float64(usage.TotalBytes))
    }
}
```

## Error Handling and Recovery

### Out-of-Memory Prevention
```go
func (c *Controller) preventOOM() {
    // Monitor for rapid memory growth
    if c.isMemoryGrowthAbnormal() {
        c.emergencyEviction()
        c.pauseNewStreams()
        c.alertOperators("High memory growth detected")
    }
    
    // Preemptive GC if needed
    if c.shouldForceGC() {
        runtime.GC()
        c.metrics.ForcedGCs.Inc()
    }
}
```

### Memory Leak Detection
```go
func (c *Controller) detectLeaks() {
    // Compare allocations vs deallocations
    allocated := c.totalAllocated
    deallocated := c.totalDeallocated
    
    if allocated - deallocated > c.config.LeakThreshold {
        c.logger.Warn("Potential memory leak detected",
            "allocated", allocated,
            "deallocated", deallocated,
            "difference", allocated - deallocated)
        
        c.triggerLeakAnalysis()
    }
}
```

## Testing and Debugging

### Memory Usage Testing
```bash
# Test memory management
go test ./internal/ingestion/memory/...

# Test with memory constraints
go test -test.memprofile=mem.prof ./internal/ingestion/memory/...

# Stress test memory limits
go test -run TestMemoryStress ./internal/ingestion/memory/...
```

### Memory Profiling
```go
// Enable memory profiling in tests
func TestMemoryUsage(t *testing.T) {
    // Start memory profiling
    f, _ := os.Create("memory.prof")
    defer f.Close()
    pprof.WriteHeapProfile(f)
    
    // Run memory-intensive test
    controller := setupMemoryController()
    simulateHighMemoryLoad(controller)
    
    // Verify memory was properly managed
    stats := controller.GetMemoryStats()
    assert.Less(t, stats.HeapInuse, maxAllowedMemory)
}
```

## Best Practices

1. **Set Appropriate Limits**: Configure memory limits based on available system memory
2. **Monitor Continuously**: Always track memory usage in production
3. **Implement Backpressure**: Stop accepting new streams when memory is constrained
4. **Use Memory Pools**: Reduce allocation pressure with object reuse
5. **Profile Regularly**: Use Go's built-in profiling tools to identify leaks
6. **Test Edge Cases**: Simulate memory pressure scenarios in testing

## Configuration Examples

### Development Configuration
```go
config := memory.ControllerConfig{
    GlobalLimit:      2 * 1024 * 1024 * 1024, // 2GB
    PerStreamLimit:   100 * 1024 * 1024,      // 100MB
    SoftLimit:        0.7,
    HardLimit:        0.9,
    MonitorInterval:  5 * time.Second,
}
```

### Production Configuration
```go
config := memory.ControllerConfig{
    GlobalLimit:      16 * 1024 * 1024 * 1024, // 16GB
    PerStreamLimit:   400 * 1024 * 1024,       // 400MB
    SoftLimit:        0.8,
    HardLimit:        0.95,
    MonitorInterval:  1 * time.Second,
}
```
