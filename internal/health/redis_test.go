package health

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRedisChecker(t *testing.T) {
	// Since we can't easily mock redis.Client, we'll test with nil
	// The actual redis functionality would need integration tests
	checker := NewRedisChecker(nil)

	assert.NotNil(t, checker, "checker should not be nil")
	assert.Nil(t, checker.client, "client should be nil for this test")
	assert.Equal(t, "redis", checker.name, "name should be 'redis'")
}

func TestRedisChecker_Name(t *testing.T) {
	checker := &RedisChecker{name: "redis"}
	assert.Equal(t, "redis", checker.Name(), "name should be 'redis'")
}

func TestRedisChecker_Check_NilClient(t *testing.T) {
	// Test with nil client - this will panic, which is expected behavior
	checker := &RedisChecker{
		client: nil,
		name:   "redis",
	}

	ctx := context.Background()

	// This should panic with nil client, so we expect a panic
	assert.Panics(t, func() {
		checker.Check(ctx)
	}, "should panic with nil client")
}

func TestNewDiskChecker(t *testing.T) {
	path := "/tmp"
	threshold := 0.9

	checker := NewDiskChecker(path, threshold)

	assert.NotNil(t, checker, "checker should not be nil")
	assert.Equal(t, path, checker.path, "path should be set")
	assert.Equal(t, threshold, checker.threshold, "threshold should be set")
}

func TestDiskChecker_Name(t *testing.T) {
	checker := &DiskChecker{}
	assert.Equal(t, "disk", checker.Name(), "name should be 'disk'")
}

func TestDiskChecker_Check(t *testing.T) {
	checker := &DiskChecker{
		path:      "/tmp",
		threshold: 0.9,
	}

	ctx := context.Background()
	err := checker.Check(ctx)

	// Currently implemented as a stub that always returns nil
	assert.NoError(t, err, "disk check should succeed (stub implementation)")
}

func TestNewMemoryChecker(t *testing.T) {
	threshold := 0.85

	checker := NewMemoryChecker(threshold)

	assert.NotNil(t, checker, "checker should not be nil")
	assert.Equal(t, threshold, checker.threshold, "threshold should be set")
}

func TestMemoryChecker_Name(t *testing.T) {
	checker := &MemoryChecker{}
	assert.Equal(t, "memory", checker.Name(), "name should be 'memory'")
}

func TestMemoryChecker_Check(t *testing.T) {
	checker := &MemoryChecker{
		threshold: 0.85,
	}

	ctx := context.Background()
	err := checker.Check(ctx)

	// Currently implemented as a stub that always returns nil
	assert.NoError(t, err, "memory check should succeed (stub implementation)")
}

func TestDiskChecker_Integration(t *testing.T) {
	// Test with various threshold values
	thresholds := []float64{0.5, 0.8, 0.9, 0.95}
	paths := []string{"/tmp", "/", "/var"}

	for _, threshold := range thresholds {
		for _, path := range paths {
			t.Run(fmt.Sprintf("path_%s_threshold_%.1f", path, threshold), func(t *testing.T) {
				checker := NewDiskChecker(path, threshold)

				ctx := context.Background()
				err := checker.Check(ctx)

				// Should not error with current stub implementation
				assert.NoError(t, err, "disk check should not error")
				assert.Equal(t, "disk", checker.Name(), "name should be disk")
				assert.Equal(t, path, checker.path, "path should match")
				assert.Equal(t, threshold, checker.threshold, "threshold should match")
			})
		}
	}
}

func TestMemoryChecker_Integration(t *testing.T) {
	// Test with various threshold values
	thresholds := []float64{0.5, 0.7, 0.8, 0.9, 0.95}

	for _, threshold := range thresholds {
		t.Run(fmt.Sprintf("threshold_%.1f", threshold), func(t *testing.T) {
			checker := NewMemoryChecker(threshold)

			ctx := context.Background()
			err := checker.Check(ctx)

			// Should not error with current stub implementation
			assert.NoError(t, err, "memory check should not error")
			assert.Equal(t, "memory", checker.Name(), "name should be memory")
			assert.Equal(t, threshold, checker.threshold, "threshold should match")
		})
	}
}

// Test edge cases
func TestCheckers_EdgeCases(t *testing.T) {
	t.Run("disk checker with zero threshold", func(t *testing.T) {
		checker := NewDiskChecker("/tmp", 0.0)
		assert.Equal(t, 0.0, checker.threshold, "should accept zero threshold")

		ctx := context.Background()
		err := checker.Check(ctx)
		assert.NoError(t, err, "should not error with zero threshold")
	})

	t.Run("disk checker with threshold over 1.0", func(t *testing.T) {
		checker := NewDiskChecker("/tmp", 1.5)
		assert.Equal(t, 1.5, checker.threshold, "should accept threshold over 1.0")

		ctx := context.Background()
		err := checker.Check(ctx)
		assert.NoError(t, err, "should not error with high threshold")
	})

	t.Run("memory checker with negative threshold", func(t *testing.T) {
		checker := NewMemoryChecker(-0.1)
		assert.Equal(t, -0.1, checker.threshold, "should accept negative threshold")

		ctx := context.Background()
		err := checker.Check(ctx)
		assert.NoError(t, err, "should not error with negative threshold")
	})

	t.Run("disk checker with empty path", func(t *testing.T) {
		checker := NewDiskChecker("", 0.9)
		assert.Equal(t, "", checker.path, "should accept empty path")

		ctx := context.Background()
		err := checker.Check(ctx)
		assert.NoError(t, err, "should not error with empty path (stub)")
	})
}
