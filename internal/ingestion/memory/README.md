# Memory Management Package

The `memory` package controls memory allocation and limits for the Mirror ingestion service. It tracks per-stream and global memory usage, enforces limits, and evicts streams under pressure.

## Architecture

```
┌─────────────────────────────────────┐
│          Controller                 │
│  • Global usage tracking (atomic)   │
│  • Per-stream usage (sync.Map)      │
│  • Pressure-based eviction          │
│  • GC throttling                    │
└───────────────┬─────────────────────┘
                │
     ┌──────────┴──────────┐
     ▼                     ▼
┌──────────────┐   ┌──────────────────┐
│StreamTracker │   │EvictionStrategy  │
│ • Access time│   │ • LRU            │
│ • Priority   │   │ • Priority       │
│ • Active flag│   │ • Size-based     │
└──────────────┘   │ • Hybrid (default)│
                   └──────────────────┘
```

## Core Components

### Controller

The `Controller` manages all memory allocation via atomic counters and a sync.Map for per-stream tracking.

```go
// Create controller with global and per-stream limits
controller := memory.NewController(
    2684354560, // 2.5GB global limit
    209715200,  // 200MB per-stream limit
)

// Request memory for a stream (returns error if over limit)
err := controller.RequestMemory("stream-123", frameSize)
if err == memory.ErrGlobalMemoryLimit {
    // Global limit exceeded
} else if err == memory.ErrStreamMemoryLimit {
    // Per-stream limit exceeded
}

// Release memory when done
controller.ReleaseMemory("stream-123", frameSize)
```

#### Constructor

```go
func NewController(maxMemory, perStreamLimit int64) *Controller
```

Takes two `int64` parameters — no config struct. Returns a `*Controller` (no error). Sets the pressure threshold to `0.8` (80%) and uses a `HybridEvictionStrategy` by default with weights `0.4/0.4/0.2` (age/size/priority).

#### Key Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `RequestMemory` | `(streamID string, size int64) error` | Allocate memory; tries GC + eviction on failure |
| `ReleaseMemory` | `(streamID string, size int64)` | Release memory with CAS loop for safety |
| `GetPressure` | `() float64` | Current pressure ratio (0.0–1.0) |
| `GetStreamUsage` | `(streamID string) int64` | Bytes used by a specific stream |
| `Stats` | `() MemoryStats` | Full statistics snapshot |
| `ResetStreamUsage` | `(streamID string)` | Remove stream and reclaim its memory |
| `TrackStream` | `(streamID string, priority int)` | Register stream with eviction tracker |
| `UpdateStreamAccess` | `(streamID string)` | Update last-access time for eviction |
| `SetStreamActive` | `(streamID string, active bool)` | Mark stream active/inactive |
| `SetEvictionCallback` | `(func(streamID string, bytes int64))` | Callback invoked during eviction |
| `SetEvictionStrategy` | `(strategy EvictionStrategy)` | Swap eviction strategy at runtime |

### Memory Pressure and Eviction

When `RequestMemory` fails due to the global limit, the controller:

1. **Tries GC** — if >10 seconds since last GC, triggers `runtime.GC()` and retries
2. **Tries eviction** — if pressure > 0.8, selects streams via the eviction strategy and calls the eviction callback

```go
// Set callback to handle eviction (e.g., drop GOP buffers)
controller.SetEvictionCallback(func(streamID string, bytes int64) {
    handler.DropOldestGOP(streamID)
})
```

### Eviction Strategies

All strategies implement the `EvictionStrategy` interface:

```go
type EvictionStrategy interface {
    SelectStreamsForEviction(streams []StreamInfo, targetBytes int64) []string
}
```

| Strategy | Behavior |
|----------|----------|
| `LRUEvictionStrategy` | Evicts inactive streams first, then by oldest access time |
| `PriorityEvictionStrategy` | Evicts highest priority number first (lower number = more important), then LRU |
| `SizeBasedEvictionStrategy` | Evicts largest streams first |
| `HybridEvictionStrategy` | Weighted score: age (0.4) + size (0.4) + priority (0.2). Skips active streams unless necessary |

The `HybridEvictionStrategy` (default) scores each stream and sorts by score. Active streams get a negative score and are only evicted as a last resort.

### StreamTracker

The `StreamTracker` maintains per-stream metadata for eviction decisions:

```go
type StreamInfo struct {
    StreamID    string
    MemoryUsage int64
    LastAccess  time.Time
    Priority    int  // Lower = higher importance
    IsActive    bool
    CreatedAt   time.Time
}
```

Register and update streams:

```go
controller.TrackStream("stream-123", 5) // priority 5 (medium)
controller.UpdateStreamAccess("stream-123")
controller.SetStreamActive("stream-123", false)
```

### Statistics

```go
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

type StreamMemoryStats struct {
    StreamID string
    Usage    int64
    Percent  float64 // Usage as % of per-stream limit
}

stats := controller.Stats()
fmt.Printf("Memory: %d/%d bytes (%.1f%% pressure)\n",
    stats.GlobalUsage, stats.GlobalLimit, stats.GlobalPressure*100)
```

## Thread Safety

- Global usage: `atomic.Int64` — lock-free
- Per-stream usage: `sync.Map` of `*atomic.Int64` — lock-free reads
- Stream init: `streamInitMu` — mutex only on first-time stream creation (double-checked locking)
- Release: CAS loop to prevent TOCTOU races between Load and Add
- StreamTracker: `sync.RWMutex` for metadata access

## Error Variables

```go
var ErrGlobalMemoryLimit = errors.New("global memory limit exceeded")
var ErrStreamMemoryLimit = errors.New("stream memory limit exceeded")
```

## Testing

```bash
# Run memory package tests
source scripts/srt-env.sh && go test ./internal/ingestion/memory/...
```
