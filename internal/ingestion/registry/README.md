# Registry Package

The `registry` package provides a Redis-backed stream registry for tracking active streaming sessions. It implements race-condition-safe operations using Lua scripts for atomic read-modify-write.

## Registry Interface

```go
type Registry interface {
    Register(ctx context.Context, stream *Stream) error
    Unregister(ctx context.Context, streamID string) error
    Get(ctx context.Context, streamID string) (*Stream, error)
    List(ctx context.Context) ([]*Stream, error)
    UpdateHeartbeat(ctx context.Context, streamID string) error
    UpdateStatus(ctx context.Context, streamID string, status StreamStatus) error
    UpdateStats(ctx context.Context, streamID string, stats *StreamStats) error
    Delete(ctx context.Context, streamID string) error
    Update(ctx context.Context, stream *Stream) error
    Close() error
}
```

## Stream Model

```go
type StreamType string    // "srt", "rtp"
type StreamStatus string  // "connecting", "active", "paused", "error", "closed"

type Stream struct {
    ID            string       `json:"id"`
    Type          StreamType   `json:"type"`
    SourceAddr    string       `json:"source_addr"`
    Status        StreamStatus `json:"status"`
    CreatedAt     time.Time    `json:"created_at"`
    LastHeartbeat time.Time    `json:"last_heartbeat"`

    // Metadata
    VideoCodec string  `json:"video_codec"`
    Resolution string  `json:"resolution"`
    Bitrate    int64   `json:"bitrate"`
    FrameRate  float64 `json:"frame_rate"`

    // Statistics
    BytesReceived   int64 `json:"bytes_received"`
    PacketsReceived int64 `json:"packets_received"`
    PacketsLost     int64 `json:"packets_lost"`
}
```

### Stream Methods

```go
stream.UpdateStats(stats *StreamStats)
stream.UpdateHeartbeat()
stream.SetStatus(status StreamStatus)
stream.GetStatus() StreamStatus
stream.IsActive() bool              // true if "active" or "connecting"
stream.MarshalJSON() ([]byte, error) // mutex-safe serialization
```

### StreamStats

```go
type StreamStats struct {
    BytesReceived   int64
    PacketsReceived int64
    PacketsLost     int64
    Bitrate         int64
}
```

### Stream ID Generation

```go
id := registry.GenerateStreamID(streamType, sourceAddr)
// Format: "<type>_<YYYYMMDD>_<HHMMSS>_<counter>"
// Example: "srt_20240115_143052_001"
```

## RedisRegistry

```go
reg := registry.NewRedisRegistry(redisClient, logrusLogger)
```

### Redis Design

- **Key prefix**: `mirror:streams:<streamID>`
- **Active set**: `mirror:streams:active` (Redis Set of stream IDs)
- **TTL**: 5 minutes per stream key (auto-expiry for dead streams)

### Atomicity

- **Register**: Uses `SetNX` to prevent race conditions on new streams; preserves `CreatedAt` on re-registration
- **List**: Lua script for atomic `SMEMBERS` + `GET` with auto-cleanup of expired entries
- **ListPaginated**: `SSCAN` with pipeline batch `GET` for large registries
- **UpdateHeartbeat/UpdateStatus/UpdateStats**: Lua scripts for atomic read-modify-write with TTL refresh

### Error Handling

- `Register` returns error if stream already exists (SetNX failed)
- `Register` rolls back on failure (deletes key if active-set add fails)
- `Get`/`Update*` return "stream not found" if key doesn't exist
- `Unregister` returns error if stream not found

## MockRegistry

A `MockRegistry` is provided for testing:

```go
mock := registry.NewMockRegistry()
```

Implements the full `Registry` interface using in-memory maps.

## Files

- `registry.go`: Registry interface definition
- `stream.go`: Stream, StreamType, StreamStatus, StreamStats, GenerateStreamID
- `redis_registry.go`: RedisRegistry implementation with Lua scripts
- `mock_registry.go`: In-memory mock for testing
- `redis_registry_test.go`: Redis registry tests
- `stream_test.go`: Stream model tests
