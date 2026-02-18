# Rate Limit Package

The `ratelimit` package provides token bucket rate limiting, connection limiting, and bandwidth management for video stream ingestion.

## RateLimiter Interface

```go
type RateLimiter interface {
    Allow(n int) bool                      // Non-blocking: check if n bytes can be processed
    AllowN(ctx context.Context, n int) error // Blocking: wait for n bytes (polls every 10ms)
    SetRate(bytesPerSecond int64)           // Update rate limit
    Rate() int64                            // Current rate limit
}
```

## TokenBucket

Standard token bucket algorithm implementing `RateLimiter`.

```go
tb := ratelimit.NewTokenBucket(1048576)  // 1 MB/s

// Non-blocking
if tb.Allow(1500) {
    // 1500 bytes allowed
}

// Blocking (waits for tokens or context cancellation)
err := tb.AllowN(ctx, 65536)

// Update rate
tb.SetRate(2097152)  // 2 MB/s
```

- Capacity is `max(bytesPerSecond, 1024)` (minimum 1KB)
- Tokens refill based on elapsed time (minimum 1ms granularity)
- Bucket starts full
- `AllowN` polls every 10ms until tokens are available

## ConnectionLimiter

Manages per-stream and global connection limits.

```go
cl := ratelimit.NewConnectionLimiter(5, 25)  // max 5 per stream, 25 total

if cl.TryAcquire("stream-123") {
    defer cl.Release("stream-123")
    // handle connection
}

count := cl.GetCount("stream-123")  // connections for this stream
total := cl.GetTotal()               // total connections
```

## BandwidthManager

Allocates bandwidth from a shared pool, creating per-stream `TokenBucket` limiters.

```go
bm := ratelimit.NewBandwidthManager(52428800)  // 50 MB/s total

limiter, ok := bm.AllocateBandwidth("stream-123", 6553600)  // request 6.25 MB/s
if ok {
    // Use limiter for this stream
    limiter.Allow(1500)

    defer bm.ReleaseBandwidth("stream-123")
}

available := bm.GetAvailableBandwidth()         // remaining unallocated
limiter := bm.GetLimiter("stream-123")          // get existing limiter (or nil)
```

## Files

- `ratelimit.go`: RateLimiter interface, TokenBucket, ConnectionLimiter, BandwidthManager
- `ratelimit_test.go`: Unit tests and benchmarks
