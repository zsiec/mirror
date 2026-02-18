package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// RateLimiter provides bandwidth limiting functionality
type RateLimiter interface {
	// Allow checks if n bytes can be processed
	Allow(n int) bool
	// AllowN checks if n bytes can be processed, waiting if necessary
	AllowN(ctx context.Context, n int) error
	// SetRate updates the rate limit (bytes per second)
	SetRate(bytesPerSecond int64)
	// Rate returns the current rate limit
	Rate() int64
}

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	rate       int64     // bytes per second
	capacity   int64     // bucket capacity
	tokens     int64     // current tokens
	lastRefill time.Time // last refill time
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket rate limiter
func NewTokenBucket(bytesPerSecond int64) *TokenBucket {
	capacity := bytesPerSecond // 1 second worth of tokens
	if capacity < 1024 {
		capacity = 1024 // minimum 1KB capacity
	}

	return &TokenBucket{
		rate:       bytesPerSecond,
		capacity:   capacity,
		tokens:     capacity,
		lastRefill: time.Now(),
	}
}

// Allow checks if n bytes can be processed immediately
func (tb *TokenBucket) Allow(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if int64(n) <= tb.tokens {
		tb.tokens -= int64(n)
		return true
	}

	return false
}

// AllowN waits until n bytes can be processed or context is cancelled
func (tb *TokenBucket) AllowN(ctx context.Context, n int) error {
	// Reject requests that can never be satisfied (n > capacity)
	tb.mu.Lock()
	cap := tb.capacity
	tb.mu.Unlock()
	if int64(n) > cap {
		return fmt.Errorf("requested %d bytes exceeds bucket capacity %d", n, cap)
	}

	// Fast path - check if tokens available
	if tb.Allow(n) {
		return nil
	}

	// Slow path - wait for tokens
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if tb.Allow(n) {
				return nil
			}
		}
	}
}

// SetRate updates the rate limit
func (tb *TokenBucket) SetRate(bytesPerSecond int64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.rate = bytesPerSecond
	tb.capacity = bytesPerSecond
	if tb.capacity < 1024 {
		tb.capacity = 1024
	}

	// Don't reduce current tokens, but cap at new capacity
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
}

// Rate returns the current rate limit
func (tb *TokenBucket) Rate() int64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.rate
}

// refill adds tokens based on elapsed time
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)

	// Only refill if enough time has passed
	if elapsed < time.Millisecond {
		return
	}

	// Calculate new tokens
	newTokens := int64(elapsed.Seconds() * float64(tb.rate))
	if newTokens > 0 {
		tb.tokens += newTokens
		if tb.tokens > tb.capacity {
			tb.tokens = tb.capacity
		}
		tb.lastRefill = now
	}
}

// ConnectionLimiter manages connection limits
type ConnectionLimiter struct {
	maxPerStream int
	maxTotal     int
	connections  map[string]int // streamID -> connection count
	total        int
	mu           sync.RWMutex
}

// NewConnectionLimiter creates a new connection limiter
func NewConnectionLimiter(maxPerStream, maxTotal int) *ConnectionLimiter {
	return &ConnectionLimiter{
		maxPerStream: maxPerStream,
		maxTotal:     maxTotal,
		connections:  make(map[string]int),
	}
}

// TryAcquire attempts to acquire a connection slot
func (cl *ConnectionLimiter) TryAcquire(streamID string) bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Check total limit
	if cl.maxTotal > 0 && cl.total >= cl.maxTotal {
		return false
	}

	// Check per-stream limit
	current := cl.connections[streamID]
	if cl.maxPerStream > 0 && current >= cl.maxPerStream {
		return false
	}

	// Acquire slot
	cl.connections[streamID] = current + 1
	cl.total++
	return true
}

// Release releases a connection slot
func (cl *ConnectionLimiter) Release(streamID string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if count, exists := cl.connections[streamID]; exists && count > 0 {
		if count == 1 {
			delete(cl.connections, streamID)
		} else {
			cl.connections[streamID] = count - 1
		}
		cl.total--
	}
}

// GetCount returns the current connection count for a stream
func (cl *ConnectionLimiter) GetCount(streamID string) int {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	return cl.connections[streamID]
}

// GetTotal returns the total connection count
func (cl *ConnectionLimiter) GetTotal() int {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	return cl.total
}

// BandwidthManager manages bandwidth allocation across streams
type BandwidthManager struct {
	totalBandwidth int64            // total bandwidth in bytes per second
	allocations    map[string]int64 // streamID -> allocated bandwidth
	limiters       map[string]RateLimiter
	mu             sync.RWMutex
}

// NewBandwidthManager creates a new bandwidth manager
func NewBandwidthManager(totalBandwidth int64) *BandwidthManager {
	return &BandwidthManager{
		totalBandwidth: totalBandwidth,
		allocations:    make(map[string]int64),
		limiters:       make(map[string]RateLimiter),
	}
}

// AllocateBandwidth allocates bandwidth for a stream
func (bm *BandwidthManager) AllocateBandwidth(streamID string, requested int64) (RateLimiter, bool) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	// Calculate available bandwidth
	var used int64
	for _, allocated := range bm.allocations {
		used += allocated
	}
	available := bm.totalBandwidth - used

	// Check if we can allocate the requested amount
	if requested > available {
		return nil, false
	}

	// Create rate limiter
	limiter := NewTokenBucket(requested)
	bm.allocations[streamID] = requested
	bm.limiters[streamID] = limiter

	return limiter, true
}

// ReleaseBandwidth releases bandwidth allocation for a stream
func (bm *BandwidthManager) ReleaseBandwidth(streamID string) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	delete(bm.allocations, streamID)
	delete(bm.limiters, streamID)
}

// GetLimiter returns the rate limiter for a stream
func (bm *BandwidthManager) GetLimiter(streamID string) RateLimiter {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.limiters[streamID]
}

// GetAvailableBandwidth returns the available bandwidth
func (bm *BandwidthManager) GetAvailableBandwidth() int64 {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var used int64
	for _, allocated := range bm.allocations {
		used += allocated
	}

	return bm.totalBandwidth - used
}
