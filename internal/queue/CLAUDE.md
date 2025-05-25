# Hybrid Queue Package

This package implements a high-performance hybrid memory/disk queue designed for handling large volumes of video data with automatic overflow to disk when memory limits are reached.

## Overview

The hybrid queue provides:
- Fast in-memory operations for recent data
- Automatic disk overflow for large datasets
- Persistence across restarts
- Configurable memory and disk limits
- Thread-safe operations
- Efficient serialization

## Architecture

```
┌─────────────────────────────────────┐
│         Hybrid Queue API            │
├─────────────────────────────────────┤
│      Memory Queue (Hot Data)        │
│  ┌─────────────────────────────┐    │
│  │   Circular Buffer (FIFO)    │    │
│  │   - Fast access             │    │
│  │   - Limited size            │    │
│  │   - Most recent items       │    │
│  └─────────────────────────────┘    │
├─────────────────────────────────────┤
│      Disk Queue (Cold Data)         │
│  ┌─────────────────────────────┐    │
│  │   Segmented File Storage    │    │
│  │   - Unlimited capacity*     │    │
│  │   - Persistent              │    │
│  │   - Older items             │    │
│  └─────────────────────────────┘    │
└─────────────────────────────────────┘
```

## Key Components

### HybridQueue Structure
```go
type HybridQueue struct {
    // Memory queue for hot data
    memQueue     *memoryQueue
    memoryLimit  int64
    
    // Disk queue for cold data
    diskQueue    *diskQueue
    diskPath     string
    maxDiskUsage int64
    
    // Synchronization
    mu           sync.RWMutex
    cond         *sync.Cond
    
    // State tracking
    totalItems   int64
    closed       bool
    
    // Metrics
    metrics      *QueueMetrics
}
```

### Memory Queue
In-memory circular buffer implementation:
- Fixed-size allocation
- O(1) enqueue/dequeue operations
- Zero-copy where possible
- Automatic overflow triggering

### Disk Queue
Segmented file-based storage:
- Multiple segment files for efficiency
- Automatic segment rotation
- Compression support
- Recovery from corrupted segments

## Core Operations

### Enqueue
```go
func (q *HybridQueue) Enqueue(item interface{}) error {
    q.mu.Lock()
    defer q.mu.Unlock()
    
    // Check if closed
    if q.closed {
        return ErrQueueClosed
    }
    
    // Try memory queue first
    if q.memQueue.Size() < q.memoryLimit {
        return q.memQueue.Enqueue(item)
    }
    
    // Overflow to disk
    if q.diskQueue.Size() < q.maxDiskUsage {
        return q.diskQueue.Enqueue(item)
    }
    
    return ErrQueueFull
}
```

### Dequeue
```go
func (q *HybridQueue) Dequeue() (interface{}, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    
    for q.totalItems == 0 && !q.closed {
        q.cond.Wait()
    }
    
    if q.totalItems == 0 {
        return nil, ErrQueueEmpty
    }
    
    // Check disk queue first (FIFO order)
    if !q.diskQueue.IsEmpty() {
        item, err := q.diskQueue.Dequeue()
        if err == nil {
            q.totalItems--
            return item, nil
        }
    }
    
    // Then memory queue
    item, err := q.memQueue.Dequeue()
    if err == nil {
        q.totalItems--
    }
    
    return item, err
}
```

### Batch Operations
```go
// Enqueue multiple items efficiently
func (q *HybridQueue) EnqueueBatch(items []interface{}) error

// Dequeue multiple items at once
func (q *HybridQueue) DequeueBatch(count int) ([]interface{}, error)

// Peek at items without removing
func (q *HybridQueue) PeekBatch(count int) ([]interface{}, error)
```

## Serialization

The queue uses efficient serialization for disk storage:

### Built-in Serializers
- **GOB**: Default, handles most Go types
- **JSON**: Human-readable, debugging
- **Protobuf**: Efficient, schema-based
- **Custom**: Interface for specific types

### Video Frame Serialization
```go
type FrameSerializer struct{}

func (s *FrameSerializer) Serialize(frame *Frame) ([]byte, error) {
    // Efficient binary encoding
    buf := new(bytes.Buffer)
    
    // Write fixed-size header
    binary.Write(buf, binary.LittleEndian, frame.Timestamp)
    binary.Write(buf, binary.LittleEndian, frame.Type)
    binary.Write(buf, binary.LittleEndian, len(frame.Data))
    
    // Write variable data
    buf.Write(frame.Data)
    
    return buf.Bytes(), nil
}

func (s *FrameSerializer) Deserialize(data []byte) (*Frame, error) {
    // Reverse the serialization
}
```

## Configuration

```go
type Config struct {
    // Memory settings
    MemorySize      int64         // Size of memory queue
    MemoryReserve   float64       // Reserve percentage (0.1 = 10%)
    
    // Disk settings
    DiskPath        string        // Base path for disk storage
    MaxDiskUsage    int64         // Maximum disk space
    SegmentSize     int64         // Size of each segment file
    Compression     bool          // Enable compression
    
    // Performance
    BatchSize       int           // Batch operation size
    FlushInterval   time.Duration // Disk flush interval
    
    // Recovery
    RecoveryMode    RecoveryMode  // How to handle corrupted data
    CheckpointInterval time.Duration // State checkpoint frequency
}
```

## Memory Management

### Memory Pressure Handling
```go
func (q *HybridQueue) HandleMemoryPressure() {
    q.mu.Lock()
    defer q.mu.Unlock()
    
    // Calculate pressure ratio
    pressure := float64(q.memQueue.Size()) / float64(q.memoryLimit)
    
    if pressure > 0.9 {
        // Start aggressive overflow to disk
        q.overflowToDisk(q.memQueue.Size() / 2)
    } else if pressure > 0.8 {
        // Gentle overflow
        q.overflowToDisk(q.memQueue.Size() / 10)
    }
}
```

### Buffer Pooling
```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        return make([]byte, 0, 64*1024) // 64KB buffers
    },
}

func getBuffer() []byte {
    return bufferPool.Get().([]byte)
}

func putBuffer(buf []byte) {
    buf = buf[:0]
    bufferPool.Put(buf)
}
```

## Disk Management

### Segment Files
```
/tmp/mirror/queue/
├── segment_0001.dat     # Oldest data
├── segment_0002.dat
├── segment_0003.dat     # Current write segment
├── index.dat            # Segment index
└── checkpoint.dat       # Queue state
```

### Rotation Policy
```go
func (d *diskQueue) shouldRotate() bool {
    return d.currentSegment.Size() >= d.segmentSize ||
           time.Since(d.lastRotation) > d.rotationInterval
}

func (d *diskQueue) rotate() error {
    // Close current segment
    d.currentSegment.Close()
    
    // Create new segment
    d.segmentCounter++
    newSegment := d.createSegment(d.segmentCounter)
    
    // Update index
    d.index.AddSegment(newSegment)
    
    // Cleanup old segments if needed
    d.cleanupOldSegments()
    
    return nil
}
```

## Recovery

### Startup Recovery
```go
func (q *HybridQueue) Recover() error {
    // Load checkpoint
    checkpoint, err := q.loadCheckpoint()
    if err != nil {
        return q.recoverFromScratch()
    }
    
    // Restore memory queue state
    q.memQueue.Restore(checkpoint.MemoryState)
    
    // Restore disk queue
    q.diskQueue.Restore(checkpoint.DiskState)
    
    // Verify integrity
    return q.verifyIntegrity()
}
```

### Corruption Handling
```go
type RecoveryMode int

const (
    // Skip corrupted items
    RecoveryModeSkip RecoveryMode = iota
    
    // Fail on corruption
    RecoveryModeFail
    
    // Try to repair
    RecoveryModeRepair
)
```

## Metrics

### Queue Metrics
```go
type QueueMetrics struct {
    // Size metrics
    MemoryItems    prometheus.Gauge
    DiskItems      prometheus.Gauge
    TotalItems     prometheus.Gauge
    
    // Throughput metrics
    EnqueueRate    prometheus.Counter
    DequeueRate    prometheus.Counter
    
    // Performance metrics
    EnqueueLatency prometheus.Histogram
    DequeueLatency prometheus.Histogram
    
    // Error metrics
    EnqueueErrors  prometheus.Counter
    DequeueErrors  prometheus.Counter
    
    // Disk metrics
    DiskUsageBytes prometheus.Gauge
    SegmentCount   prometheus.Gauge
}
```

## Usage Examples

### Basic Usage
```go
// Create queue
config := &QueueConfig{
    MemorySize:   100 * 1024 * 1024,  // 100MB
    DiskPath:     "/tmp/mirror/queue",
    MaxDiskUsage: 10 * 1024 * 1024 * 1024, // 10GB
}
queue, err := NewHybridQueue(config)

// Enqueue items
err = queue.Enqueue(videoFrame)

// Dequeue items
item, err := queue.Dequeue()
frame := item.(*VideoFrame)
```

### With Custom Serialization
```go
// Register custom serializer
queue.RegisterSerializer(reflect.TypeOf(&VideoFrame{}), &FrameSerializer{})

// Use normally
queue.Enqueue(frame)
```

### Batch Processing
```go
// Producer
frames := collectFrames()
err := queue.EnqueueBatch(frames)

// Consumer
batch, err := queue.DequeueBatch(100)
for _, item := range batch {
    processFrame(item.(*VideoFrame))
}
```

## Testing

### Unit Tests
- Memory queue operations
- Disk queue operations
- Serialization/deserialization
- Overflow behavior
- Recovery scenarios

### Integration Tests
- Large data volumes
- Concurrent access
- Crash recovery
- Disk full scenarios
- Memory pressure

### Stress Tests
```go
func TestQueueStress(t *testing.T) {
    queue := createTestQueue()
    
    // Concurrent producers
    for i := 0; i < 10; i++ {
        go func() {
            for j := 0; j < 10000; j++ {
                queue.Enqueue(generateFrame())
            }
        }()
    }
    
    // Concurrent consumers
    for i := 0; i < 5; i++ {
        go func() {
            for {
                queue.Dequeue()
            }
        }()
    }
    
    // Run for duration
    time.Sleep(30 * time.Second)
    
    // Verify consistency
    assert.Equal(t, queue.EnqueueCount(), queue.DequeueCount())
}
```

## Best Practices

### Configuration
1. Set memory limit based on available RAM
2. Use SSD for disk queue path
3. Enable compression for large items
4. Set appropriate segment sizes

### Performance
1. Use batch operations when possible
2. Pre-allocate buffers
3. Monitor disk I/O
4. Implement backpressure

### Reliability
1. Regular checkpoints
2. Monitor disk space
3. Implement alerting
4. Test recovery procedures

## Future Enhancements

### Planned Features
- Distributed queue support
- Priority queue mode
- TTL for items
- Encryption at rest
- S3 backend option

### Performance Improvements
- Memory-mapped files
- Zero-copy optimization
- Parallel disk I/O
- Compression algorithms

### Monitoring
- Detailed metrics
- Performance profiling
- Queue visualization
- Capacity planning
