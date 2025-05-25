package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTokenBucket(t *testing.T) {
	t.Run("basic rate limiting", func(t *testing.T) {
		// 10000 bytes per second (10KB/s to avoid min capacity)
		tb := NewTokenBucket(10000)

		// Reset the lastRefill time to now to avoid initial refill
		tb.mu.Lock()
		tb.lastRefill = time.Now()
		tb.tokens = tb.capacity
		tb.mu.Unlock()

		// Drain all tokens in a loop to avoid timing issues
		total := 0
		for i := 0; i < 11000; i++ {
			if tb.Allow(1) {
				total++
			} else {
				break
			}
		}
		// Allow higher variance for CI environments (±5%)
		assert.InDelta(t, 10000, total, 500, "Should have approximately 10000 tokens")

		// Ensure bucket is empty
		assert.False(t, tb.Allow(1)) // No more tokens

		// Wait for refill (100ms should give us ~1000 tokens at 10000/sec)
		time.Sleep(105 * time.Millisecond)

		// Count how many tokens we can use
		refilled := 0
		for i := 0; i < 2000; i++ {
			if tb.Allow(1) {
				refilled++
			} else {
				break
			}
		}
		// Should have gotten approximately 1050 tokens (±20% for CI timing variance)
		assert.True(t, refilled >= 840 && refilled <= 1260, "Got %d tokens, expected ~1050", refilled)
	})

	t.Run("AllowN with context", func(t *testing.T) {
		tb := NewTokenBucket(1000) // 1KB/s

		// Reset the lastRefill time and drain all tokens
		tb.mu.Lock()
		tb.lastRefill = time.Now()
		tb.tokens = 0 // Start with empty bucket
		tb.mu.Unlock()

		// Test successful wait
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := tb.AllowN(ctx, 100)
		elapsed := time.Since(start)

		assert.NoError(t, err)
		// At 1KB/s, 100 bytes should take ~100ms, allow 50-250ms for CI variance
		assert.True(t, elapsed >= 50*time.Millisecond && elapsed < 250*time.Millisecond,
			"Expected wait between 50-250ms, got %v", elapsed)
	})

	t.Run("AllowN context cancellation", func(t *testing.T) {
		tb := NewTokenBucket(100) // Very slow rate

		// Use up all tokens
		assert.True(t, tb.Allow(100))

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := tb.AllowN(ctx, 1000) // Request more than will be available
		assert.Error(t, err)
		assert.Equal(t, context.DeadlineExceeded, err)
	})

	t.Run("SetRate", func(t *testing.T) {
		tb := NewTokenBucket(1000)

		// Change rate
		tb.SetRate(2000)
		assert.Equal(t, int64(2000), tb.Rate())

		// Should have higher capacity now
		time.Sleep(100 * time.Millisecond)
		assert.True(t, tb.Allow(200)) // 200 bytes in 100ms at 2KB/s
	})

	t.Run("concurrent access", func(t *testing.T) {
		tb := NewTokenBucket(10000) // 10KB/s

		// Reset to known state
		tb.mu.Lock()
		tb.lastRefill = time.Now()
		tb.tokens = tb.capacity
		tb.mu.Unlock()

		var allowed int32
		var wg sync.WaitGroup

		// Run 10 goroutines trying to consume tokens
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					if tb.Allow(10) {
						atomic.AddInt32(&allowed, 1)
					}
					time.Sleep(time.Microsecond)
				}
			}()
		}

		wg.Wait()

		// Should have allowed approximately 1000 operations (10KB capacity / 10 bytes each)
		// Allow wider margin for CI timing variance
		assert.True(t, allowed >= 800 && allowed <= 1200, "Allowed: %d", allowed)
	})
}

func TestConnectionLimiter(t *testing.T) {
	t.Run("per-stream limits", func(t *testing.T) {
		cl := NewConnectionLimiter(2, 10)

		// Should allow up to 2 connections per stream
		assert.True(t, cl.TryAcquire("stream1"))
		assert.True(t, cl.TryAcquire("stream1"))
		assert.False(t, cl.TryAcquire("stream1")) // Limit reached

		// Different stream should work
		assert.True(t, cl.TryAcquire("stream2"))

		// Release and try again
		cl.Release("stream1")
		assert.True(t, cl.TryAcquire("stream1"))
	})

	t.Run("total limit", func(t *testing.T) {
		cl := NewConnectionLimiter(5, 3)

		// Should enforce total limit
		assert.True(t, cl.TryAcquire("stream1"))
		assert.True(t, cl.TryAcquire("stream2"))
		assert.True(t, cl.TryAcquire("stream3"))
		assert.False(t, cl.TryAcquire("stream4")) // Total limit reached

		assert.Equal(t, 3, cl.GetTotal())
	})

	t.Run("GetCount", func(t *testing.T) {
		cl := NewConnectionLimiter(3, 10)

		assert.Equal(t, 0, cl.GetCount("stream1"))

		cl.TryAcquire("stream1")
		cl.TryAcquire("stream1")
		assert.Equal(t, 2, cl.GetCount("stream1"))

		cl.Release("stream1")
		assert.Equal(t, 1, cl.GetCount("stream1"))

		cl.Release("stream1")
		assert.Equal(t, 0, cl.GetCount("stream1"))
	})

	t.Run("concurrent access", func(t *testing.T) {
		cl := NewConnectionLimiter(10, 100)

		var wg sync.WaitGroup
		acquired := make([]bool, 20)

		// Try to acquire 20 connections for the same stream concurrently
		for i := 0; i < 20; i++ {
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				acquired[idx] = cl.TryAcquire("stream1")
			}()
		}

		wg.Wait()

		// Count successful acquisitions
		successCount := 0
		for _, success := range acquired {
			if success {
				successCount++
			}
		}

		assert.Equal(t, 10, successCount) // Should respect per-stream limit
		assert.Equal(t, 10, cl.GetCount("stream1"))
	})
}

func TestBandwidthManager(t *testing.T) {
	t.Run("bandwidth allocation", func(t *testing.T) {
		bm := NewBandwidthManager(1000000) // 1MB/s total

		// Allocate bandwidth for streams
		limiter1, ok := bm.AllocateBandwidth("stream1", 400000) // 400KB/s
		assert.True(t, ok)
		assert.NotNil(t, limiter1)

		limiter2, ok := bm.AllocateBandwidth("stream2", 500000) // 500KB/s
		assert.True(t, ok)
		assert.NotNil(t, limiter2)

		// Should fail - not enough bandwidth
		_, ok = bm.AllocateBandwidth("stream3", 200000) // 200KB/s
		assert.False(t, ok)

		// Should have 100KB/s available
		assert.Equal(t, int64(100000), bm.GetAvailableBandwidth())
	})

	t.Run("bandwidth release", func(t *testing.T) {
		bm := NewBandwidthManager(1000000) // 1MB/s

		bm.AllocateBandwidth("stream1", 600000)
		assert.Equal(t, int64(400000), bm.GetAvailableBandwidth())

		bm.ReleaseBandwidth("stream1")
		assert.Equal(t, int64(1000000), bm.GetAvailableBandwidth())
	})

	t.Run("get limiter", func(t *testing.T) {
		bm := NewBandwidthManager(1000000)

		limiter, _ := bm.AllocateBandwidth("stream1", 500000)

		// Should get the same limiter
		retrieved := bm.GetLimiter("stream1")
		assert.Equal(t, limiter, retrieved)

		// Non-existent stream
		assert.Nil(t, bm.GetLimiter("non-existent"))
	})

	t.Run("concurrent operations", func(t *testing.T) {
		bm := NewBandwidthManager(10000000) // 10MB/s

		var wg sync.WaitGroup
		successCount := int32(0)

		// Try to allocate bandwidth from multiple goroutines
		for i := 0; i < 20; i++ {
			wg.Add(1)
			streamID := string(rune(i))
			go func() {
				defer wg.Done()
				if _, ok := bm.AllocateBandwidth(streamID, 1000000); ok { // 1MB/s each
					atomic.AddInt32(&successCount, 1)
					time.Sleep(10 * time.Millisecond)
					bm.ReleaseBandwidth(streamID)
				}
			}()
		}

		wg.Wait()

		// Should have allocated to 10 streams
		assert.Equal(t, int32(10), atomic.LoadInt32(&successCount))
		assert.Equal(t, int64(10000000), bm.GetAvailableBandwidth())
	})
}

func TestIntegration(t *testing.T) {
	// Test rate limiter with actual data flow simulation
	t.Run("data flow simulation", func(t *testing.T) {
		tb := NewTokenBucket(1000) // 1KB/s

		// Reset to empty bucket for predictable behavior
		tb.mu.Lock()
		tb.lastRefill = time.Now()
		tb.tokens = 0
		tb.mu.Unlock()

		totalBytes := 0
		start := time.Now()

		// Simulate sending data for 1 second
		for time.Since(start) < 1*time.Second {
			if tb.Allow(100) { // Try to send 100 bytes
				totalBytes += 100
			}
			time.Sleep(10 * time.Millisecond)
		}

		// Should have sent approximately 1000 bytes (±40% for CI timing variations)
		assert.True(t, totalBytes >= 600 && totalBytes <= 1400, "Sent %d bytes in 1 second", totalBytes)
	})
}

func BenchmarkTokenBucket(b *testing.B) {
	tb := NewTokenBucket(1000000) // 1MB/s

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.Allow(100)
		}
	})
}

func BenchmarkConnectionLimiter(b *testing.B) {
	cl := NewConnectionLimiter(100, 10000)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			streamID := string(rune(i % 100))
			if cl.TryAcquire(streamID) {
				cl.Release(streamID)
			}
			i++
		}
	})
}
