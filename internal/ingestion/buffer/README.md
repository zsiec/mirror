# Buffer Package

The `buffer` package provides efficient, video-aware buffering for stream ingestion in the Mirror platform. It includes ring buffers, buffer pools, and size-limited buffers optimized for high-throughput video streaming.

## Overview

Key components:
- **Ring Buffer**: Lock-free circular buffer for stream data
- **Buffer Pool**: Reusable buffer allocation to reduce GC pressure
- **Sized Buffer**: Size-limited wrapper with overflow protection

## Ring Buffer

Thread-safe circular buffer implementation:

```go
// Create 4MB ring buffer
rb := buffer.NewRingBuffer(4 << 20)

// Write data
n, err := rb.Write(data)
if err == buffer.ErrBufferFull {
    // Handle overflow
}

// Read data
buf := make([]byte, 1024)
n, err := rb.Read(buf)

// Get statistics
stats := rb.Stats()
fmt.Printf("Usage: %d/%d bytes\n", stats.Used, stats.Size)
```

## Buffer Pool

Memory-efficient buffer management:

```go
// Create pool with 30 pre-allocated buffers
pool := buffer.NewBufferPool(buffer.PoolConfig{
    BufferSize: 4 << 20, // 4MB each
    PoolSize:   30,      // 30 buffers
})

// Get buffer from pool
buf := pool.Get(streamID)
defer pool.Put(streamID, buf)

// Use buffer
buf.Write(data)
```

## Features

- **Zero-copy operations** where possible
- **Automatic overflow handling** with metrics
- **Thread-safe** concurrent access
- **Memory pooling** to reduce allocations
- **Real-time statistics** for monitoring

## Related Documentation

- [Ingestion Overview](../README.md)
- [Main Documentation](../../../README.md)
