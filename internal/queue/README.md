# Queue Package

The `queue` package provides a high-performance hybrid memory/disk queue system for the Mirror platform. It offers automatic overflow to disk, persistence across restarts, and efficient batch processing for video segment handling.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Architecture](#architecture)
- [Usage](#usage)
- [Memory Management](#memory-management)
- [Disk Persistence](#disk-persistence)
- [Performance](#performance)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The queue package provides:

- **Hybrid memory/disk storage** with automatic overflow
- **Persistence** across application restarts
- **Batch processing** for efficient throughput
- **Priority queuing** for critical operations
- **Backpressure support** to prevent overload
- **Thread-safe operations** for concurrent access
- **Metrics integration** for monitoring

## Features

### Core Capabilities

- **Automatic overflow** from memory to disk at threshold
- **Transparent operation** - same API for memory/disk items
- **Efficient serialization** with minimal overhead
- **Crash recovery** with transaction log
- **Multiple queue types** (FIFO, Priority, Circular)
- **TTL support** for automatic expiration
- **Compression** for disk storage

### Use Cases

- **HLS segment buffering** before CDN upload
- **Transcoding job queue** with priorities
- **Event stream buffer** for analytics
- **Recovery queue** for failed operations
- **Rate limiting buffer** for API calls
- **Batch aggregation** for efficient processing

## Architecture

### Queue Structure

```
┌─────────────────────────────────────────┐
│           Hybrid Queue                   │
├─────────────────────────────────────────┤
│                                         │
│  ┌─────────────┐    ┌─────────────┐   │
│  │   Memory     │    │    Disk     │   │
│  │   Queue      │    │   Storage   │   │
│  │             │    │             │   │
│  │  [Item 3]   │    │  [Item 1]   │   │
│  │  [Item 4]   │    │  [Item 2]   │   │
│  │  [Item 5]   │    │             │   │
│  └─────────────┘    └─────────────┘   │
│         ↑                    ↑          │
│         └────────┬───────────┘          │
│                 │                       │
│          Queue Manager                  │
│                 │                       │
│         ┌───────┴────────┐              │
│         │   Public API   │              │
│         └────────────────┘              │
└─────────────────────────────────────────┘
```

### Component Overview

- **Queue Manager**: Coordinates memory and disk operations
- **Memory Queue**: Fast in-memory circular buffer
- **Disk Storage**: Persistent storage with WAL
- **Serializer**: Efficient encoding/decoding
- **Recovery Manager**: Handles crash recovery
- **Metrics Collector**: Performance monitoring

## Usage

### Basic Queue Operations

```go
import "github.com/zsiec/mirror/internal/queue"

// Create hybrid queue
q, err := queue.NewHybridQueue(queue.Config{
    Name:           "segments",
    MemorySize:     1000,      // Keep 1000 items in memory
    DiskPath:       "/var/lib/mirror/queues",
    MaxDiskSize:    10 << 30,  // 10GB max disk usage
    EnableMetrics:  true,
})

// Enqueue items
err := q.Enqueue(&Segment{
    ID:        "seg_123",
    Data:      data,
    Priority:  queue.NormalPriority,
})

// Dequeue items
item, err := q.Dequeue()
if err == queue.ErrQueueEmpty {
    // Queue is empty
}

// Batch operations
items, err := q.DequeueBatch(100) // Get up to 100 items

// Check size
size := q.Size()
memSize := q.MemorySize()
diskSize := q.DiskSize()
```

### Priority Queue

```go
// Create priority queue
pq := queue.NewPriorityQueue(queue.Config{
    Name:       "jobs",
    MemorySize: 500,
})

// Enqueue with priority
pq.EnqueueWithPriority(job1, queue.HighPriority)
pq.EnqueueWithPriority(job2, queue.NormalPriority)
pq.EnqueueWithPriority(job3, queue.LowPriority)

// Dequeues in priority order (high → normal → low)
job := pq.Dequeue() // Returns job1
```

### Circular Buffer Queue

```go
// Fixed-size circular buffer
cb := queue.NewCircularBuffer(queue.Config{
    Name:     "recent_events",
    Capacity: 10000, // Keep last 10k events
})

// Add events (overwrites oldest when full)
cb.Push(event)

// Get recent events
recent := cb.GetLast(100) // Get last 100 events

// Iterate over all events
cb.ForEach(func(item interface{}) bool {
    event := item.(*Event)
    // Process event
    return true // Continue iteration
})
```

## Memory Management

### Overflow Strategy

```go
// Configure overflow behavior
type OverflowStrategy int

const (
    // Move oldest items to disk
    OverflowOldest OverflowStrategy = iota
    
    // Move largest items to disk
    OverflowLargest
    
    // Move by priority (low priority first)
    OverflowByPriority
)

q := queue.NewHybridQueue(queue.Config{
    MemorySize:       1000,
    OverflowStrategy: queue.OverflowByPriority,
    OverflowRatio:    0.8, // Start overflow at 80% full
})
```

### Memory Pressure Handling

```go
// Register memory pressure callback
q.OnMemoryPressure(func(usage float64) {
    if usage > 0.9 {
        // Critical - force flush to disk
        q.FlushToDisk(500) // Move 500 items to disk
    } else if usage > 0.7 {
        // Warning - reduce memory usage
        q.CompactMemory()
    }
})

// Manual memory management
q.SetMemoryLimit(500 << 20) // 500MB limit
q.ShrinkMemory()             // Release unused memory
```

## Disk Persistence

### Storage Format

```go
// Queue items are stored in segments on disk
type DiskSegment struct {
    Header SegmentHeader
    Items  []SerializedItem
    Index  []IndexEntry
    Footer SegmentFooter
}

// Efficient binary format
type SerializedItem struct {
    ID        [16]byte  // UUID
    Timestamp int64     // Unix nano
    Priority  uint8     // 0-255
    DataSize  uint32    // Size in bytes
    Data      []byte    // Compressed data
    Checksum  uint32    // CRC32
}
```

### Write-Ahead Log (WAL)

```go
// WAL for crash recovery
type WAL struct {
    logFile *os.File
    buffer  *bufio.Writer
}

// All operations logged before execution
func (w *WAL) LogEnqueue(item *Item) error {
    entry := WALEntry{
        Op:        OpEnqueue,
        Timestamp: time.Now().UnixNano(),
        ItemID:    item.ID,
        Data:      item.Serialize(),
    }
    return w.writeEntry(entry)
}

// Recovery on startup
func (q *Queue) Recover() error {
    wal, err := OpenWAL(q.walPath)
    if err != nil {
        return err
    }
    
    // Replay operations
    return wal.Replay(func(entry WALEntry) error {
        switch entry.Op {
        case OpEnqueue:
            return q.recoverEnqueue(entry)
        case OpDequeue:
            return q.recoverDequeue(entry)
        }
        return nil
    })
}
```

### Disk Management

```go
// Automatic disk space management
type DiskManager struct {
    maxSize     int64
    currentSize int64
    segments    []*Segment
}

// Cleanup old segments
func (d *DiskManager) Cleanup() error {
    if d.currentSize < d.maxSize {
        return nil
    }
    
    // Remove oldest segments
    for d.currentSize > d.maxSize*0.9 {
        oldest := d.segments[0]
        if err := oldest.Delete(); err != nil {
            return err
        }
        d.segments = d.segments[1:]
        d.currentSize -= oldest.Size()
    }
    
    return nil
}

// Compaction for space efficiency
func (d *DiskManager) Compact() error {
    // Merge small segments
    var toMerge []*Segment
    for _, seg := range d.segments {
        if seg.Size() < MinSegmentSize {
            toMerge = append(toMerge, seg)
        }
    }
    
    return d.mergeSegments(toMerge)
}
```

## Performance

### Benchmarks

```go
// Benchmark results on typical hardware
// CPU: Intel i7-9700K, RAM: 32GB, SSD: NVMe

BenchmarkEnqueueMemory-8      10000000    105 ns/op     48 B/op    1 allocs/op
BenchmarkEnqueueDisk-8         1000000   1053 ns/op    112 B/op    3 allocs/op
BenchmarkDequeueMemory-8      20000000     84 ns/op     32 B/op    1 allocs/op
BenchmarkDequeueDisk-8         2000000    742 ns/op     96 B/op    2 allocs/op
BenchmarkBatchDequeue-8        5000000    312 ns/op    896 B/op    2 allocs/op
```

### Optimization Techniques

```go
// 1. Batch operations for efficiency
func (q *Queue) ProcessBatch() error {
    items, err := q.DequeueBatch(1000)
    if err != nil {
        return err
    }
    
    // Process all items at once
    return q.processor.ProcessBatch(items)
}

// 2. Memory pooling for allocations
type ItemPool struct {
    pool sync.Pool
}

func (p *ItemPool) Get() *Item {
    if v := p.pool.Get(); v != nil {
        return v.(*Item)
    }
    return &Item{}
}

func (p *ItemPool) Put(item *Item) {
    item.Reset()
    p.pool.Put(item)
}

// 3. Compression for disk storage
func (q *Queue) compressItem(item *Item) ([]byte, error) {
    var buf bytes.Buffer
    w := snappy.NewBufferedWriter(&buf)
    
    if err := gob.NewEncoder(w).Encode(item); err != nil {
        return nil, err
    }
    
    if err := w.Close(); err != nil {
        return nil, err
    }
    
    return buf.Bytes(), nil
}
```

### Concurrent Access

```go
// Thread-safe operations with minimal locking
type ConcurrentQueue struct {
    mem  *MemoryQueue
    disk *DiskQueue
    
    // Separate locks for memory and disk
    memLock  sync.RWMutex
    diskLock sync.Mutex
    
    // Lock-free counters
    memSize  atomic.Int64
    diskSize atomic.Int64
}

func (q *ConcurrentQueue) Enqueue(item interface{}) error {
    // Try memory first (read lock)
    q.memLock.RLock()
    if q.memSize.Load() < q.memCapacity {
        q.memLock.RUnlock()
        
        // Upgrade to write lock
        q.memLock.Lock()
        err := q.mem.Enqueue(item)
        q.memSize.Add(1)
        q.memLock.Unlock()
        
        return err
    }
    q.memLock.RUnlock()
    
    // Overflow to disk
    q.diskLock.Lock()
    defer q.diskLock.Unlock()
    
    err := q.disk.Enqueue(item)
    if err == nil {
        q.diskSize.Add(1)
    }
    
    return err
}
```

## Best Practices

### 1. Queue Sizing

```go
// DO: Size based on expected load
q := queue.NewHybridQueue(queue.Config{
    // Memory for hot data (1-2 minutes of segments)
    MemorySize: 1000, // 1000 segments * 2MB = 2GB
    
    // Disk for cold storage (1 hour buffer)
    MaxDiskSize: 30 << 30, // 30GB
    
    // Batch size for processing efficiency
    DefaultBatchSize: 100,
})

// DON'T: Arbitrary sizing
q := queue.NewHybridQueue(queue.Config{
    MemorySize:  1000000, // Too much memory!
    MaxDiskSize: 1 << 40, // 1TB is excessive
})
```

### 2. Error Handling

```go
// DO: Handle queue full gracefully
err := q.Enqueue(item)
if err == queue.ErrQueueFull {
    // Try to make space
    if err := q.FlushOldest(100); err != nil {
        return fmt.Errorf("queue full and flush failed: %w", err)
    }
    
    // Retry
    err = q.Enqueue(item)
}

// DO: Check for empty queue
item, err := q.Dequeue()
if err == queue.ErrQueueEmpty {
    // Normal condition - wait or return
    return nil
}
```

### 3. Shutdown Handling

```go
// DO: Graceful shutdown with persistence
func (app *App) Shutdown(ctx context.Context) error {
    // Stop accepting new items
    app.queue.Close()
    
    // Process remaining items
    for {
        items, err := app.queue.DequeueBatch(100)
        if err == queue.ErrQueueEmpty {
            break
        }
        
        if err := app.processItems(ctx, items); err != nil {
            // Re-queue failed items
            for _, item := range items {
                app.queue.EnqueueFront(item)
            }
            break
        }
    }
    
    // Flush to disk
    return app.queue.Sync()
}
```

### 4. Monitoring

```go
// DO: Monitor queue health
type QueueMonitor struct {
    queue   *HybridQueue
    metrics *prometheus.Registry
}

func (m *QueueMonitor) Start(ctx context.Context) {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            stats := m.queue.Stats()
            
            // Update metrics
            queueSize.Set(float64(stats.TotalSize))
            memoryUsage.Set(float64(stats.MemoryBytes))
            diskUsage.Set(float64(stats.DiskBytes))
            
            // Alert on issues
            if stats.MemoryUsageRatio > 0.9 {
                log.Warn("Queue memory usage high", 
                    "usage", stats.MemoryUsageRatio)
            }
        }
    }
}
```

## Testing

### Unit Tests

```go
func TestHybridQueue_MemoryOverflow(t *testing.T) {
    // Create small memory queue
    q := queue.NewHybridQueue(queue.Config{
        MemorySize: 10,
        DiskPath:   t.TempDir(),
    })
    defer q.Close()
    
    // Fill memory
    for i := 0; i < 10; i++ {
        err := q.Enqueue(fmt.Sprintf("item_%d", i))
        require.NoError(t, err)
    }
    
    assert.Equal(t, 10, q.MemorySize())
    assert.Equal(t, 0, q.DiskSize())
    
    // Trigger overflow
    err := q.Enqueue("item_10")
    require.NoError(t, err)
    
    assert.Equal(t, 10, q.MemorySize())
    assert.Equal(t, 1, q.DiskSize())
    
    // Verify order preserved
    first, err := q.Dequeue()
    require.NoError(t, err)
    assert.Equal(t, "item_0", first)
}
```

### Integration Tests

```go
func TestQueuePersistence(t *testing.T) {
    dir := t.TempDir()
    
    // Create and populate queue
    q1 := queue.NewHybridQueue(queue.Config{
        Name:     "test",
        DiskPath: dir,
    })
    
    items := []string{"a", "b", "c", "d", "e"}
    for _, item := range items {
        require.NoError(t, q1.Enqueue(item))
    }
    
    require.NoError(t, q1.Close())
    
    // Reopen queue
    q2 := queue.NewHybridQueue(queue.Config{
        Name:     "test",
        DiskPath: dir,
    })
    defer q2.Close()
    
    // Verify items preserved
    for _, expected := range items {
        actual, err := q2.Dequeue()
        require.NoError(t, err)
        assert.Equal(t, expected, actual)
    }
}
```

### Benchmark Tests

```go
func BenchmarkConcurrentEnqueue(b *testing.B) {
    q := queue.NewHybridQueue(queue.Config{
        MemorySize: 10000,
        DiskPath:   b.TempDir(),
    })
    defer q.Close()
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            q.Enqueue(fmt.Sprintf("item_%d", i))
            i++
        }
    })
}
```

## Examples

### HLS Segment Queue

```go
// Queue for HLS segments awaiting upload
type SegmentQueue struct {
    queue *queue.HybridQueue
}

func NewSegmentQueue(config Config) (*SegmentQueue, error) {
    q, err := queue.NewHybridQueue(queue.Config{
        Name:           "hls_segments",
        MemorySize:     config.MemorySegments,
        DiskPath:       config.QueuePath,
        MaxDiskSize:    config.MaxDiskSize,
        ItemSerializer: &SegmentSerializer{},
    })
    
    if err != nil {
        return nil, err
    }
    
    return &SegmentQueue{queue: q}, nil
}

func (sq *SegmentQueue) EnqueueSegment(segment *Segment) error {
    // Priority based on stream importance
    priority := queue.NormalPriority
    if segment.Stream.Premium {
        priority = queue.HighPriority
    }
    
    return sq.queue.EnqueueWithPriority(segment, priority)
}

func (sq *SegmentQueue) ProcessBatch(ctx context.Context) error {
    segments, err := sq.queue.DequeueBatch(50)
    if err != nil {
        return err
    }
    
    // Upload to CDN
    failed := sq.uploadBatch(ctx, segments)
    
    // Re-queue failed segments
    for _, seg := range failed {
        sq.queue.EnqueueFront(seg)
    }
    
    return nil
}
```

### Event Buffer with TTL

```go
// Time-limited event queue
type EventQueue struct {
    queue *queue.CircularBuffer
    ttl   time.Duration
}

func NewEventQueue(size int, ttl time.Duration) *EventQueue {
    return &EventQueue{
        queue: queue.NewCircularBuffer(queue.Config{
            Capacity: size,
        }),
        ttl: ttl,
    }
}

func (eq *EventQueue) Add(event Event) {
    event.Timestamp = time.Now()
    eq.queue.Push(event)
    
    // Clean expired events periodically
    eq.cleanExpired()
}

func (eq *EventQueue) GetRecent(since time.Duration) []Event {
    cutoff := time.Now().Add(-since)
    var recent []Event
    
    eq.queue.ForEach(func(item interface{}) bool {
        event := item.(Event)
        if event.Timestamp.After(cutoff) {
            recent = append(recent, event)
        }
        return true
    })
    
    return recent
}

func (eq *EventQueue) cleanExpired() {
    cutoff := time.Now().Add(-eq.ttl)
    
    eq.queue.RemoveWhere(func(item interface{}) bool {
        event := item.(Event)
        return event.Timestamp.Before(cutoff)
    })
}
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Buffer Management](../ingestion/buffer/README.md) - In-memory buffering
- [Metrics Package](../metrics/README.md) - Queue metrics
- [Performance Guide](../../docs/performance.md) - Optimization tips
- [Architecture](../../docs/architecture.md) - System design
