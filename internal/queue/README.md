# Queue Package

The `queue` package provides a hybrid memory/disk queue for buffering video data per stream. The in-memory Go channel handles normal throughput, and a single append-only disk file absorbs overflow.

## Architecture

```
Enqueue(data)
    │
    ├── Try memory channel (non-blocking) → chan []byte (fixed capacity)
    │
    └── Channel full? → writeToDisk() (length-prefixed binary append)
                               │
                         diskToMemoryPump (background goroutine)
                               │
                         Reads from disk → pushes back into channel
```

## Usage

```go
import "github.com/zsiec/mirror/internal/queue"

q, err := queue.NewHybridQueue("stream-123", 1000, "/tmp/mirror/queue")
if err != nil {
    return err
}
defer q.Close()

// Producer
err = q.Enqueue(packetData)  // tries memory first, overflows to disk

// Consumer
data, err := q.Dequeue()                            // blocks indefinitely
data, err := q.DequeueTimeout(100 * time.Millisecond) // blocks with timeout
data, err := q.TryDequeue()                           // non-blocking
```

## API

### Constructor

```go
func NewHybridQueue(streamID string, memSize int, diskPath string) (*HybridQueue, error)
```

- `memSize`: channel capacity (number of items, not bytes)
- `diskPath`: directory for the overflow file (`<streamID>.overflow`)
- Creates the disk directory if needed
- Starts a background `diskToMemoryPump` goroutine

### Operations

```go
q.Enqueue(data []byte) error              // memory first, disk overflow
q.Dequeue() ([]byte, error)                // block until data available
q.DequeueTimeout(timeout) ([]byte, error)  // block with timeout
q.TryDequeue() ([]byte, error)             // non-blocking
q.Close() error                             // signals pump, waits, cleans up file
```

### Observability

```go
q.GetDepth() int64       // total items (memory + disk)
q.GetPressure() float64  // 0.0-1.0+ (>1.0 indicates disk overflow)
q.HasDiskData() bool     // whether disk overflow is active
q.Stats() QueueStats     // full stats snapshot
```

### QueueStats

```go
type QueueStats struct {
    StreamID    string
    Depth       int64    // total items
    MemoryItems int64    // items in memory channel
    DiskBytes   int64    // bytes on disk
    Pressure    float64  // pressure ratio
}
```

## Error Types

```go
var (
    ErrQueueClosed   = errors.New("queue closed")
    ErrRateLimited   = errors.New("rate limited")
    ErrCorruptedData = errors.New("corrupted data in disk file")
)
```

## Design Details

### Memory Queue
- A Go `chan []byte` with configurable capacity
- Non-blocking send on `Enqueue`; if full, falls through to disk

### Disk Overflow
- Single append-only file per stream: `<diskPath>/<streamID>.overflow`
- Length-prefixed binary format: `[4 bytes uint32 big-endian length][N bytes data]`
- Write side: 64KB buffered writer, flushed after each write
- Read side: 64KB buffered reader, tracks read offset
- 10MB max message size sanity check

### Background Pump
- `diskToMemoryPump` goroutine polls disk data and pushes it into the channel when space is available
- Polls every 10-50ms depending on state
- Shut down cleanly via `closeCh` channel and `sync.WaitGroup`

### Rate Limiting
- Built-in `rate.Limiter` at 10k ops/sec with burst of 1000
- Returns `ErrRateLimited` if exceeded

### Lifecycle
1. `NewHybridQueue()` creates the overflow file and starts the pump goroutine
2. `Close()` signals via `closeCh`, waits on WaitGroup, flushes disk, closes files, removes the overflow file

## Files

- `hybrid_queue.go`: Full implementation (HybridQueue, QueueStats)
- `hybrid_queue_test.go`: Core tests
- `hybrid_queue_goroutine_test.go`: Goroutine lifecycle tests
- `hybrid_queue_recovery_test.go`: Recovery scenario tests
