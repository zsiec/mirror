# Hybrid Queue Package

This package implements a hybrid memory/disk queue for buffering video data per stream. The in-memory channel handles normal throughput, and a single append-only disk file absorbs overflow.

## Architecture

```
Enqueue(data)
    │
    ├── Try memory channel (non-blocking) ──→ chan []byte (fixed size)
    │
    └── Channel full? ──→ writeToDisk() (length-prefixed append)
                                │
                          diskToMemoryPump (background goroutine)
                                │
                          Reads from disk ──→ pushes back to channel
```

## Key Design

- **Memory**: A Go `chan []byte` with configurable capacity (number of items, not bytes)
- **Disk**: A single append-only overflow file (`<streamID>.overflow`) using length-prefixed binary messages
- **Background pump**: A goroutine continuously moves disk data back into the memory channel when space is available
- **Rate limiting**: Built-in `rate.Limiter` at 10k ops/sec with burst of 1000
- **Thread-safe**: All operations are safe for concurrent use

## Files

- `hybrid_queue.go`: Full implementation
- `hybrid_queue_test.go`: Core tests
- `hybrid_queue_goroutine_test.go`: Goroutine lifecycle tests
- `hybrid_queue_recovery_test.go`: Recovery scenario tests

## API

### Constructor
```go
// Creates queue with memory channel of memSize items, disk overflow at diskPath
q, err := queue.NewHybridQueue(streamID string, memSize int, diskPath string) (*HybridQueue, error)
```

### Operations
```go
q.Enqueue(data []byte) error          // Add data (memory first, disk overflow)
q.Dequeue() ([]byte, error)            // Block until data available
q.DequeueTimeout(timeout) ([]byte, error) // Block with timeout
q.TryDequeue() ([]byte, error)         // Non-blocking dequeue
q.Close() error                         // Shutdown (signals pump, waits, cleans up file)
```

### Observability
```go
q.GetDepth() int64        // Total items (memory + disk)
q.GetPressure() float64   // 0.0-1.0+ (>1.0 indicates disk overflow)
q.HasDiskData() bool      // Whether disk overflow is active
q.Stats() QueueStats      // Full stats snapshot
```

### QueueStats
```go
type QueueStats struct {
    StreamID    string
    Depth       int64    // Total items
    MemoryItems int64    // Items in memory channel
    DiskBytes   int64    // Bytes on disk
    Pressure    float64  // Pressure ratio
}
```

## Error Types

- `ErrQueueClosed`: Queue has been closed
- `ErrRateLimited`: Operation exceeded rate limit
- `ErrCorruptedData`: Disk file has corrupted data (message > 10MB sanity check)

## Disk Format

The overflow file uses simple length-prefixed messages:

```
[4 bytes: uint32 big-endian length][N bytes: data]
[4 bytes: uint32 big-endian length][N bytes: data]
...
```

- Write side: buffered writer (64KB), flushed after each write
- Read side: buffered reader (64KB), tracks read offset to avoid re-reading
- File is deleted on `Close()`

## Lifecycle

1. `NewHybridQueue()` creates the overflow file and starts the background `diskToMemoryPump` goroutine
2. `Enqueue()` tries the channel first (non-blocking); if full, writes to disk
3. The pump goroutine polls disk data and pushes it into the channel when space is available
4. `Close()` signals via `closeCh`, waits on `sync.WaitGroup`, flushes disk, closes files, removes the overflow file

## Usage

```go
q, err := queue.NewHybridQueue("stream-123", 1000, "/tmp/mirror/queue")
if err != nil {
    return err
}
defer q.Close()

// Producer
q.Enqueue(packetData)

// Consumer
data, err := q.DequeueTimeout(100 * time.Millisecond)
```
