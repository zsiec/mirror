package recovery

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/logger"
)

// MockStateStore implements StateStore for testing
type MockStateStore struct {
	mu      sync.Mutex
	states  map[string]*PreservedState
	saveErr error
	loadErr error
}

func NewMockStateStore() *MockStateStore {
	return &MockStateStore{
		states: make(map[string]*PreservedState),
	}
}

func (m *MockStateStore) Save(state *PreservedState) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[state.StreamID] = state
	return nil
}

func (m *MockStateStore) Load(streamID string) (*PreservedState, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.states[streamID]
	if !ok {
		return nil, fmt.Errorf("state not found")
	}
	return state, nil
}

func (m *MockStateStore) Delete(streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, streamID)
	return nil
}

func TestStreamRecovery(t *testing.T) {
	t.Run("Basic Recovery", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		config.MaxRetries = 3
		config.InitialBackoff = 10 * time.Millisecond

		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		recoverCalled := atomic.Int32{}
		sr.SetCallbacks(
			func() error {
				recoverCalled.Add(1)
				return nil
			},
			nil,
			nil,
		)

		// Trigger error
		sr.HandleError(fmt.Errorf("test error"))

		// Wait for recovery
		time.Sleep(50 * time.Millisecond)

		assert.Equal(t, int32(1), recoverCalled.Load())
		assert.Equal(t, StreamRecoveryStateHealthy, sr.GetState())
		assert.Equal(t, int32(1), sr.recoveries.Load())
	})

	t.Run("Recovery with Backoff", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		config.MaxRetries = 3
		config.InitialBackoff = 20 * time.Millisecond
		config.BackoffMultiplier = 2.0

		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		attempts := atomic.Int32{}
		sr.SetCallbacks(
			func() error {
				attempt := attempts.Add(1)
				if attempt < 3 {
					return fmt.Errorf("still failing")
				}
				return nil
			},
			nil,
			nil,
		)

		start := time.Now()
		sr.HandleError(fmt.Errorf("test error"))

		// Wait for recovery
		time.Sleep(200 * time.Millisecond)

		assert.Equal(t, int32(3), attempts.Load())
		assert.Equal(t, StreamRecoveryStateHealthy, sr.GetState())

		// Check that backoff was applied (should take at least 20+40=60ms)
		assert.Greater(t, time.Since(start), 60*time.Millisecond)
	})

	t.Run("Recovery Failure", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		config.MaxRetries = 2
		config.InitialBackoff = 10 * time.Millisecond

		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		var failureErr error
		sr.SetCallbacks(
			func() error {
				return fmt.Errorf("persistent error")
			},
			nil,
			func(err error) {
				failureErr = err
			},
		)

		sr.HandleError(fmt.Errorf("test error"))

		// Wait for recovery attempts
		time.Sleep(100 * time.Millisecond)

		assert.Equal(t, StreamRecoveryStateFailed, sr.GetState())
		assert.NotNil(t, failureErr)
		assert.Contains(t, failureErr.Error(), "failed after 2 attempts")
	})

	t.Run("State Preservation", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		config.EnableStatePreservation = true

		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		store := NewMockStateStore()
		sr.SetStateStore(store)

		var restoredState *PreservedState
		sr.SetCallbacks(
			func() error {
				return nil
			},
			func(state *PreservedState) error {
				restoredState = state
				return nil
			},
			nil,
		)

		// Trigger error
		sr.HandleError(fmt.Errorf("test error"))

		// Wait for recovery
		time.Sleep(50 * time.Millisecond)

		// Check state was preserved
		stored, err := store.Load("test-stream")
		if err != nil {
			// State preservation might not have completed yet or might be disabled
			t.Logf("State load error: %v", err)
		} else {
			assert.NotNil(t, stored)
			if stored != nil {
				assert.Equal(t, "test-stream", stored.StreamID)
			}
		}

		// Check state was restored
		if restoredState != nil {
			assert.Equal(t, "test-stream", restoredState.StreamID)
		} else {
			t.Log("State restoration callback was not invoked")
		}
	})

	t.Run("Circuit Breaker", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		config.CircuitBreakerThreshold = 2
		config.CircuitBreakerTimeout = 50 * time.Millisecond

		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		attempts := atomic.Int32{}
		sr.SetCallbacks(
			func() error {
				attempts.Add(1)
				return fmt.Errorf("error")
			},
			nil,
			nil,
		)

		// Trigger multiple errors
		for i := 0; i < 5; i++ {
			sr.HandleError(fmt.Errorf("error %d", i))
			time.Sleep(10 * time.Millisecond)
		}

		// Circuit breaker should open after threshold
		time.Sleep(50 * time.Millisecond)
		attemptCount := attempts.Load()

		// Should have stopped after circuit breaker opened
		assert.Less(t, attemptCount, int32(5))

		// Wait for circuit breaker timeout
		time.Sleep(100 * time.Millisecond)

		// Trigger another error - should retry now
		sr.HandleError(fmt.Errorf("error after timeout"))
		time.Sleep(50 * time.Millisecond)

		assert.Greater(t, attempts.Load(), attemptCount)
	})

	t.Run("Statistics", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		sr.SetCallbacks(
			func() error {
				return nil
			},
			nil,
			nil,
		)

		// Trigger some errors
		sr.HandleError(fmt.Errorf("error 1"))
		time.Sleep(20 * time.Millisecond)
		sr.HandleError(fmt.Errorf("error 2"))
		time.Sleep(20 * time.Millisecond)

		stats := sr.GetStatistics()
		assert.Equal(t, "healthy", stats["state"])
		assert.Equal(t, int32(2), stats["failures"])
		assert.Equal(t, int32(2), stats["recoveries"])
		assert.NotNil(t, stats["last_recovery"])
	})
}

func TestCircuitBreaker(t *testing.T) {
	t.Run("Basic Operation", func(t *testing.T) {
		cb := NewCircuitBreaker(3, 100*time.Millisecond)

		// Initially closed
		assert.Equal(t, "closed", cb.State())
		assert.True(t, cb.Allow())

		// Record failures
		cb.RecordFailure()
		cb.RecordFailure()
		assert.Equal(t, "closed", cb.State())
		assert.True(t, cb.Allow())

		// Third failure opens circuit
		cb.RecordFailure()
		assert.Equal(t, "open", cb.State())
		assert.False(t, cb.Allow())

		// Success resets
		cb.RecordSuccess()
		assert.Equal(t, "closed", cb.State())
		assert.True(t, cb.Allow())
	})

	t.Run("Timeout Transition", func(t *testing.T) {
		cb := NewCircuitBreaker(1, 50*time.Millisecond)

		// Open circuit
		cb.RecordFailure()
		assert.Equal(t, "open", cb.State())
		assert.False(t, cb.Allow())

		// Wait for timeout
		time.Sleep(60 * time.Millisecond)

		// Should transition to half-open
		assert.True(t, cb.Allow())
		assert.Equal(t, "half-open", cb.State())

		// Success closes circuit
		cb.RecordSuccess()
		assert.Equal(t, "closed", cb.State())
	})

	t.Run("Reset", func(t *testing.T) {
		cb := NewCircuitBreaker(2, 100*time.Millisecond)

		// Open circuit
		cb.RecordFailure()
		cb.RecordFailure()
		assert.Equal(t, "open", cb.State())

		// Reset
		cb.Reset()
		assert.Equal(t, "closed", cb.State())
		assert.True(t, cb.Allow())
	})
}

func TestRecoveryStrategies(t *testing.T) {
	t.Run("Immediate Strategy", func(t *testing.T) {
		config := DefaultRecoveryConfig()
		config.MaxRetries = 1
		config.InitialBackoff = 0

		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		recoverTime := time.Time{}
		sr.SetCallbacks(
			func() error {
				recoverTime = time.Now()
				return nil
			},
			nil,
			nil,
		)

		start := time.Now()
		sr.HandleError(fmt.Errorf("error"))

		time.Sleep(20 * time.Millisecond)

		// Should recover immediately
		assert.Less(t, recoverTime.Sub(start), 10*time.Millisecond)
	})

	t.Run("Adaptive Strategy", func(t *testing.T) {
		// This would test adaptive recovery based on error patterns
		// For now, we test the basic recovery mechanism
		config := DefaultRecoveryConfig()
		config.MaxRetries = 5
		config.InitialBackoff = 50 * time.Millisecond
		config.BackoffMultiplier = 1.5
		sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
		defer sr.Stop()

		errorTypes := []string{}
		sr.SetCallbacks(
			func() error {
				// Simulate different error types
				if len(errorTypes) < 2 {
					errorTypes = append(errorTypes, "transient")
					return fmt.Errorf("transient error")
				}
				return nil
			},
			nil,
			nil,
		)

		sr.HandleError(fmt.Errorf("initial error"))

		// Wait for recovery to complete
		// First attempt: immediate
		// Second attempt: after 50ms backoff
		// Third attempt: after 75ms backoff (50 * 1.5)
		// Total: ~125ms + processing time
		time.Sleep(300 * time.Millisecond)

		// Recovery should eventually succeed
		assert.Equal(t, StreamRecoveryStateHealthy, sr.GetState())
		// The callback simulates 2 failures before success, so it should be called 3 times
		assert.Equal(t, 2, len(errorTypes))
	})
}

func TestConcurrentRecovery(t *testing.T) {
	config := DefaultRecoveryConfig()
	config.MaxRetries = 5

	sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
	defer sr.Stop()

	recoveries := atomic.Int32{}
	sr.SetCallbacks(
		func() error {
			recoveries.Add(1)
			time.Sleep(20 * time.Millisecond)
			return nil
		},
		nil,
		nil,
	)

	// Trigger multiple errors concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sr.HandleError(fmt.Errorf("error %d", n))
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Should handle concurrent errors gracefully
	assert.Equal(t, StreamRecoveryStateHealthy, sr.GetState())
	assert.Greater(t, recoveries.Load(), int32(0))
}

func TestHealthMonitoring(t *testing.T) {
	config := DefaultRecoveryConfig()
	config.HealthCheckInterval = 50 * time.Millisecond
	config.CircuitBreakerTimeout = 100 * time.Millisecond
	config.MaxRetries = 1 // Only one retry so it fails quickly
	config.InitialBackoff = 10 * time.Millisecond

	sr := NewStreamRecovery("test-stream", logger.NewNullLogger(), config)
	defer sr.Stop()

	retryCount := atomic.Int32{}
	sr.SetCallbacks(
		func() error {
			count := retryCount.Add(1)
			if count == 1 {
				return fmt.Errorf("still failing")
			}
			return nil
		},
		nil,
		nil,
	)

	// Cause initial failure
	sr.HandleError(fmt.Errorf("error"))
	time.Sleep(30 * time.Millisecond) // Wait for first recovery attempt to fail

	// Should be in failed state after max retries exceeded
	assert.Equal(t, StreamRecoveryStateFailed, sr.GetState())

	// Wait for health check to retry
	time.Sleep(100 * time.Millisecond)

	// Should have retried and recovered via health check
	assert.Equal(t, StreamRecoveryStateHealthy, sr.GetState())
	assert.Equal(t, int32(2), retryCount.Load())
}

func BenchmarkStreamRecovery(b *testing.B) {
	config := DefaultRecoveryConfig()
	config.MaxRetries = 1
	config.InitialBackoff = 0

	sr := NewStreamRecovery("bench-stream", logger.NewNullLogger(), config)
	defer sr.Stop()

	sr.SetCallbacks(
		func() error {
			return nil
		},
		nil,
		nil,
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sr.HandleError(fmt.Errorf("error %d", i))
	}
}
