package health

import (
	"context"
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- DiskChecker tests ---

func TestNewDiskChecker(t *testing.T) {
	path := "/tmp"
	threshold := 0.9

	checker := NewDiskChecker(path, threshold)

	assert.NotNil(t, checker, "checker should not be nil")
	assert.Equal(t, path, checker.path, "path should be set")
	assert.Equal(t, threshold, checker.threshold, "threshold should be set")
	assert.Equal(t, DefaultMinDiskBytes, checker.minFreeBytes, "should have default min free bytes")
}

func TestDiskChecker_Name(t *testing.T) {
	checker := &DiskChecker{}
	assert.Equal(t, "disk", checker.Name(), "name should be 'disk'")
}

func TestDiskChecker_Check_ValidPath(t *testing.T) {
	// /tmp should exist on all Unix systems and have space available
	checker := NewDiskChecker("/tmp", 0.99) // Very generous threshold
	checker.SetMinFreeBytes(1)              // Only require 1 byte free

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.NoError(t, err, "disk check on /tmp should succeed with generous threshold")
}

func TestDiskChecker_Check_RealDiskUsage(t *testing.T) {
	// Test that we actually read real disk stats
	checker := NewDiskChecker("/tmp", 0.999) // Very generous threshold
	checker.SetMinFreeBytes(1)               // Only require 1 byte free

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.NoError(t, err, "disk check should succeed with very generous thresholds")
}

func TestDiskChecker_Check_InvalidPath(t *testing.T) {
	checker := NewDiskChecker("/nonexistent/path/that/does/not/exist", 0.9)

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.Error(t, err, "disk check should fail for nonexistent path")
	assert.Contains(t, err.Error(), "failed to get disk stats")
}

func TestDiskChecker_Check_EmptyPath(t *testing.T) {
	checker := NewDiskChecker("", 0.9)

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.Error(t, err, "disk check should fail for empty path")
	assert.Contains(t, err.Error(), "path is empty")
}

func TestDiskChecker_Check_ZeroThreshold(t *testing.T) {
	// A zero threshold means ANY usage is too much - should fail on a real filesystem
	checker := NewDiskChecker("/tmp", 0.0)
	checker.SetMinFreeBytes(1) // Don't trigger the absolute minimum check

	ctx := context.Background()
	err := checker.Check(ctx)

	// A real filesystem will always have some usage, so this should fail
	assert.Error(t, err, "disk check should fail with zero threshold on a real filesystem")
	assert.Contains(t, err.Error(), "exceeds threshold")
}

func TestDiskChecker_Check_HighMinFreeBytes(t *testing.T) {
	// Require an impossibly large amount of free space
	checker := NewDiskChecker("/tmp", 1.0) // Threshold won't trigger
	checker.SetMinFreeBytes(^uint64(0))    // Max uint64 - impossible to satisfy

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.Error(t, err, "disk check should fail when requiring impossible amount of free space")
	assert.Contains(t, err.Error(), "critically low")
}

func TestDiskChecker_SetMinFreeBytes(t *testing.T) {
	checker := NewDiskChecker("/tmp", 0.9)
	assert.Equal(t, DefaultMinDiskBytes, checker.minFreeBytes)

	checker.SetMinFreeBytes(500 * 1024 * 1024) // 500 MB
	assert.Equal(t, uint64(500*1024*1024), checker.minFreeBytes)
}

func TestDiskChecker_Check_HighThreshold(t *testing.T) {
	// Threshold > 1.0 should never trigger the percentage check
	checker := NewDiskChecker("/tmp", 1.5)
	checker.SetMinFreeBytes(1)

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.NoError(t, err, "disk check should pass with threshold over 1.0")
}

func TestDiskChecker_Integration(t *testing.T) {
	// Test with various valid paths
	paths := []string{"/tmp", "/"}

	for _, path := range paths {
		t.Run(fmt.Sprintf("path_%s", path), func(t *testing.T) {
			checker := NewDiskChecker(path, 0.999)
			checker.SetMinFreeBytes(1)

			ctx := context.Background()
			err := checker.Check(ctx)

			assert.NoError(t, err, "disk check should pass for %s with generous threshold", path)
			assert.Equal(t, "disk", checker.Name(), "name should be disk")
			assert.Equal(t, path, checker.path, "path should match")
		})
	}
}

// --- MemoryChecker tests ---

func TestNewMemoryChecker(t *testing.T) {
	threshold := 0.85

	checker := NewMemoryChecker(threshold)

	assert.NotNil(t, checker, "checker should not be nil")
	assert.Equal(t, threshold, checker.threshold, "threshold should be set")
	assert.Equal(t, DefaultMemoryLimit, checker.memoryLimit, "should have default memory limit")
}

func TestMemoryChecker_Name(t *testing.T) {
	checker := &MemoryChecker{}
	assert.Equal(t, "memory", checker.Name(), "name should be 'memory'")
}

func TestMemoryChecker_Check_WithinLimit(t *testing.T) {
	// Default limit is 8GB, test processes should use much less
	checker := NewMemoryChecker(0.9)

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.NoError(t, err, "memory check should pass - test process uses far less than 8GB")
}

func TestMemoryChecker_Check_ExceedsThreshold(t *testing.T) {
	// Set an impossibly low limit so that any allocation exceeds the threshold
	checker := NewMemoryChecker(0.01) // 1% threshold
	checker.SetMemoryLimit(1024)      // Only 1KB limit

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.Error(t, err, "memory check should fail with tiny limit")
	assert.Contains(t, err.Error(), "exceeds threshold")
}

func TestMemoryChecker_Check_ZeroLimit(t *testing.T) {
	checker := NewMemoryChecker(0.8)
	checker.SetMemoryLimit(0)

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.Error(t, err, "memory check should fail with zero limit")
	assert.Contains(t, err.Error(), "not configured")
}

func TestMemoryChecker_SetMemoryLimit(t *testing.T) {
	checker := NewMemoryChecker(0.8)
	assert.Equal(t, DefaultMemoryLimit, checker.memoryLimit)

	newLimit := uint64(4 * 1024 * 1024 * 1024) // 4 GB
	checker.SetMemoryLimit(newLimit)
	assert.Equal(t, newLimit, checker.memoryLimit)
}

func TestMemoryChecker_Check_GenerousThreshold(t *testing.T) {
	// With a very generous threshold and large limit, should always pass
	checker := NewMemoryChecker(0.99)
	checker.SetMemoryLimit(64 * 1024 * 1024 * 1024) // 64 GB

	ctx := context.Background()
	err := checker.Check(ctx)

	assert.NoError(t, err, "memory check should pass with very generous settings")
}

func TestMemoryChecker_Check_NegativeThreshold(t *testing.T) {
	// Negative threshold means any usage at all will exceed it
	checker := NewMemoryChecker(-0.1)

	ctx := context.Background()
	err := checker.Check(ctx)

	// A running process always has some memory allocated, so this should fail
	assert.Error(t, err, "memory check should fail with negative threshold")
	assert.Contains(t, err.Error(), "exceeds threshold")
}

func TestMemoryChecker_Integration(t *testing.T) {
	// Test with various threshold values against a reasonably large limit
	thresholds := []float64{0.5, 0.7, 0.8, 0.9, 0.95}

	for _, threshold := range thresholds {
		t.Run(fmt.Sprintf("threshold_%.1f", threshold), func(t *testing.T) {
			checker := NewMemoryChecker(threshold)
			// Use a large limit so the test process stays well under threshold
			checker.SetMemoryLimit(64 * 1024 * 1024 * 1024) // 64 GB

			ctx := context.Background()
			err := checker.Check(ctx)

			assert.NoError(t, err, "memory check should pass with large limit")
			assert.Equal(t, "memory", checker.Name(), "name should be memory")
			assert.Equal(t, threshold, checker.threshold, "threshold should match")
		})
	}
}

// --- Integration test with Manager ---

func TestCheckers_WithManager(t *testing.T) {
	t.Run("disk and memory checkers with manager", func(t *testing.T) {
		logger := logrus.New()
		logger.SetLevel(logrus.DebugLevel)
		manager := NewManager(logger)

		// Create checkers with generous thresholds
		diskChecker := NewDiskChecker("/tmp", 0.999)
		diskChecker.SetMinFreeBytes(1)
		memChecker := NewMemoryChecker(0.99)
		memChecker.SetMemoryLimit(64 * 1024 * 1024 * 1024)

		manager.Register(diskChecker)
		manager.Register(memChecker)

		ctx := context.Background()
		results := manager.RunChecks(ctx)

		require.Len(t, results, 2)

		diskResult := results["disk"]
		require.NotNil(t, diskResult)
		assert.Equal(t, StatusOK, diskResult.Status, "disk should be healthy")

		memResult := results["memory"]
		require.NotNil(t, memResult)
		assert.Equal(t, StatusOK, memResult.Status, "memory should be healthy")

		assert.Equal(t, StatusOK, manager.GetOverallStatus())
	})
}
