package reconnect

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExponentialBackoff(t *testing.T) {
	tests := []struct {
		name         string
		initialDelay time.Duration
		maxDelay     time.Duration
		multiplier   float64
		maxRetries   int
		wantDelays   []time.Duration // approximate expected delays
	}{
		{
			name:         "basic exponential backoff",
			initialDelay: 100 * time.Millisecond,
			maxDelay:     2 * time.Second,
			multiplier:   2.0,
			maxRetries:   5,
			wantDelays: []time.Duration{
				100 * time.Millisecond,
				200 * time.Millisecond,
				400 * time.Millisecond,
				800 * time.Millisecond,
				1600 * time.Millisecond,
			},
		},
		{
			name:         "backoff with max delay cap",
			initialDelay: 500 * time.Millisecond,
			maxDelay:     1 * time.Second,
			multiplier:   3.0,
			maxRetries:   4,
			wantDelays: []time.Duration{
				500 * time.Millisecond,
				1 * time.Second, // capped
				1 * time.Second, // capped
				1 * time.Second, // capped
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff := NewExponentialBackoff(tt.initialDelay, tt.maxDelay, tt.multiplier, tt.maxRetries)

			for i, expectedDelay := range tt.wantDelays {
				delay, shouldRetry := backoff.NextDelay()
				assert.True(t, shouldRetry, "Retry %d should continue", i+1)
				
				// Check within 40% range due to jitter
				minDelay := time.Duration(float64(expectedDelay) * 0.6)
				maxDelay := time.Duration(float64(expectedDelay) * 1.4)
				assert.True(t, delay >= minDelay && delay <= maxDelay,
					"Delay %v should be between %v and %v", delay, minDelay, maxDelay)
			}

			// Should stop after max retries
			_, shouldRetry := backoff.NextDelay()
			assert.False(t, shouldRetry, "Should stop after max retries")

			// Reset should allow retrying again
			backoff.Reset()
			delay, shouldRetry := backoff.NextDelay()
			assert.True(t, shouldRetry, "Should retry after reset")
			minDelay := time.Duration(float64(tt.initialDelay) * 0.6)
			maxDelay := time.Duration(float64(tt.initialDelay) * 1.4)
			assert.True(t, delay >= minDelay && delay <= maxDelay,
				"First delay after reset should be near initial delay")
		})
	}
}

func TestLinearBackoff(t *testing.T) {
	delay := 200 * time.Millisecond
	maxRetries := 3

	backoff := NewLinearBackoff(delay, maxRetries)

	// Test fixed delays
	for i := 0; i < maxRetries; i++ {
		d, shouldRetry := backoff.NextDelay()
		assert.True(t, shouldRetry)
		assert.Equal(t, delay, d)
	}

	// Should stop after max retries
	_, shouldRetry := backoff.NextDelay()
	assert.False(t, shouldRetry)

	// Reset and try again
	backoff.Reset()
	d, shouldRetry := backoff.NextDelay()
	assert.True(t, shouldRetry)
	assert.Equal(t, delay, d)
}

func TestReconnectManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	t.Run("successful reconnection", func(t *testing.T) {
		strategy := NewLinearBackoff(50*time.Millisecond, 3)
		manager := NewManager(strategy, logger)

		connectAttempts := int32(0)
		var successCalled atomic.Bool

		manager.SetCallbacks(
			func(ctx context.Context) error {
				attempts := atomic.AddInt32(&connectAttempts, 1)
				if attempts < 3 {
					return errors.New("connection failed")
				}
				return nil // Success on 3rd attempt
			},
			func() {
				successCalled.Store(true)
			},
			func(err error) {
				t.Error("Failure callback should not be called")
			},
		)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		manager.Start(ctx)
		
		// Wait for completion
		time.Sleep(300 * time.Millisecond)
		
		assert.Equal(t, int32(3), atomic.LoadInt32(&connectAttempts))
		assert.True(t, successCalled.Load())
	})

	t.Run("max retries exceeded", func(t *testing.T) {
		strategy := NewLinearBackoff(20*time.Millisecond, 2)
		manager := NewManager(strategy, logger)

		connectAttempts := int32(0)
		var failureCalled atomic.Bool

		manager.SetCallbacks(
			func(ctx context.Context) error {
				atomic.AddInt32(&connectAttempts, 1)
				return errors.New("connection failed")
			},
			func() {
				t.Error("Success callback should not be called")
			},
			func(err error) {
				failureCalled.Store(true)
			},
		)

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		manager.Start(ctx)
		
		// Wait for completion
		time.Sleep(100 * time.Millisecond)
		
		// Should have 2 attempts (max retries) plus potentially 1 more in flight
		attempts := atomic.LoadInt32(&connectAttempts)
		assert.True(t, attempts == 2 || attempts == 3, "Expected 2-3 attempts, got %d", attempts)
		assert.True(t, failureCalled.Load())
	})

	t.Run("context cancellation", func(t *testing.T) {
		strategy := NewLinearBackoff(100*time.Millisecond, 10)
		manager := NewManager(strategy, logger)

		connectAttempts := int32(0)

		manager.SetCallbacks(
			func(ctx context.Context) error {
				atomic.AddInt32(&connectAttempts, 1)
				return errors.New("connection failed")
			},
			nil,
			nil,
		)

		ctx, cancel := context.WithCancel(context.Background())
		
		manager.Start(ctx)
		
		// Cancel after a short time
		time.Sleep(150 * time.Millisecond)
		cancel()
		
		// Wait a bit more
		time.Sleep(150 * time.Millisecond)
		
		// Should have stopped after context cancellation
		attempts := atomic.LoadInt32(&connectAttempts)
		assert.True(t, attempts >= 1 && attempts <= 3, "Should have 1-3 attempts, got %d", attempts)
	})

	t.Run("stop method", func(t *testing.T) {
		strategy := NewLinearBackoff(50*time.Millisecond, 10)
		manager := NewManager(strategy, logger)

		connectAttempts := int32(0)

		manager.SetCallbacks(
			func(ctx context.Context) error {
				atomic.AddInt32(&connectAttempts, 1)
				return errors.New("connection failed")
			},
			nil,
			nil,
		)

		ctx := context.Background()
		manager.Start(ctx)
		
		// Stop after a short time
		time.Sleep(75 * time.Millisecond)
		manager.Stop()
		
		// Wait a bit more
		time.Sleep(100 * time.Millisecond)
		
		// Should have stopped
		attempts := atomic.LoadInt32(&connectAttempts)
		assert.True(t, attempts >= 1 && attempts <= 3, "Should have 1-3 attempts, got %d", attempts)
	})
}

func TestCalculateBackoff(t *testing.T) {
	base := 100 * time.Millisecond
	max := 5 * time.Second

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, 1600 * time.Millisecond},
		{6, 3200 * time.Millisecond},
		{7, 5 * time.Second}, // capped at max
		{8, 5 * time.Second}, // capped at max
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.attempt)), func(t *testing.T) {
			result := calculateBackoff(tt.attempt, base, max)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExponentialBackoffNoMaxRetries(t *testing.T) {
	backoff := NewExponentialBackoff(100*time.Millisecond, 1*time.Second, 2.0, 0)

	// Should continue indefinitely when MaxRetries is 0
	for i := 0; i < 20; i++ {
		_, shouldRetry := backoff.NextDelay()
		require.True(t, shouldRetry, "Should always retry when MaxRetries is 0")
	}
}
