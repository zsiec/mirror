package health

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockChecker is a mock implementation of Checker for testing
type mockChecker struct {
	name string
	err  error
	delay time.Duration
}

func (m *mockChecker) Name() string {
	return m.name
}

func (m *mockChecker) Check(ctx context.Context) error {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.err
}

func TestManager(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	t.Run("Register and RunChecks", func(t *testing.T) {
		manager := NewManager(logger)

		// Register checkers
		manager.Register(&mockChecker{name: "checker1", err: nil})
		manager.Register(&mockChecker{name: "checker2", err: errors.New("checker2 failed")})
		manager.Register(&mockChecker{name: "checker3", err: nil})

		// Run checks
		ctx := context.Background()
		results := manager.RunChecks(ctx)

		// Verify results
		assert.Len(t, results, 3)

		// Check individual results
		check1 := results["checker1"]
		assert.NotNil(t, check1)
		assert.Equal(t, StatusOK, check1.Status)
		assert.Empty(t, check1.Message)

		check2 := results["checker2"]
		assert.NotNil(t, check2)
		assert.Equal(t, StatusDown, check2.Status)
		assert.Contains(t, check2.Message, "checker2 failed")

		check3 := results["checker3"]
		assert.NotNil(t, check3)
		assert.Equal(t, StatusOK, check3.Status)
	})

	t.Run("GetResults", func(t *testing.T) {
		manager := NewManager(logger)
		manager.Register(&mockChecker{name: "test", err: nil})

		// Run checks first
		ctx := context.Background()
		manager.RunChecks(ctx)

		// Get results
		results := manager.GetResults()
		assert.Len(t, results, 1)
		assert.Contains(t, results, "test")
	})

	t.Run("GetOverallStatus", func(t *testing.T) {
		tests := []struct {
			name     string
			checkers []Checker
			want     Status
		}{
			{
				name:     "all healthy",
				checkers: []Checker{
					&mockChecker{name: "c1", err: nil},
					&mockChecker{name: "c2", err: nil},
				},
				want: StatusOK,
			},
			{
				name:     "one down",
				checkers: []Checker{
					&mockChecker{name: "c1", err: nil},
					&mockChecker{name: "c2", err: errors.New("error")},
				},
				want: StatusDown,
			},
			{
				name:     "no checkers",
				checkers: []Checker{},
				want:     StatusDown,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				manager := NewManager(logger)
				for _, checker := range tt.checkers {
					manager.Register(checker)
				}

				if len(tt.checkers) > 0 {
					ctx := context.Background()
					manager.RunChecks(ctx)
				}

				got := manager.GetOverallStatus()
				assert.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("Timeout handling", func(t *testing.T) {
		manager := NewManager(logger)
		
		// Register a slow checker
		manager.Register(&mockChecker{
			name:  "slow-checker",
			delay: 10 * time.Second,
			err:   nil,
		})

		// Run with short context timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		start := time.Now()
		results := manager.RunChecks(ctx)
		duration := time.Since(start)

		// Should complete within reasonable time
		assert.Less(t, duration, 6*time.Second)

		// Check should be marked as down due to timeout
		check := results["slow-checker"]
		require.NotNil(t, check)
		assert.Equal(t, StatusDown, check.Status)
		assert.Contains(t, check.Message, "timed out")
	})
}

func TestStartPeriodicChecks(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	
	manager := NewManager(logger)
	
	// Track check count using a counter that increments on each check
	checkCount := 0
	mu := &sync.Mutex{}
	
	manager.Register(&mockChecker{
		name: "counter",
		err: nil,
		delay: 0, // No delay for quick execution
	})

	// Start periodic checks with a short interval
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	
	go func() {
		manager.StartPeriodicChecks(ctx, 40*time.Millisecond)
		done <- true
	}()

	// Monitor the results to count checks
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
				results := manager.GetResults()
				if len(results) > 0 {
					mu.Lock()
					if results["counter"] != nil && results["counter"].Status == StatusOK {
						checkCount++
					}
					mu.Unlock()
				}
			}
		}
	}()

	// Wait for several check cycles
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Wait for goroutine to finish
	<-done

	// Should have run at least 3 checks
	mu.Lock()
	finalCount := checkCount
	mu.Unlock()
	
	assert.GreaterOrEqual(t, finalCount, 3, "Expected at least 3 periodic checks but got %d", finalCount)
}

func TestCheckDurationTracking(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)

	// Register checker with delay
	manager.Register(&mockChecker{
		name:  "delayed",
		delay: 50 * time.Millisecond,
		err:   nil,
	})

	ctx := context.Background()
	results := manager.RunChecks(ctx)

	check := results["delayed"]
	require.NotNil(t, check)
	
	// Duration should be at least the delay
	assert.GreaterOrEqual(t, check.Duration, 50*time.Millisecond)
	assert.GreaterOrEqual(t, check.DurationMS, float64(50))
}