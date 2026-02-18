package health

import (
	"context"
	"fmt"
	"runtime"
	"syscall"

	"github.com/redis/go-redis/v9"
)

// RedisChecker checks Redis connectivity.
type RedisChecker struct {
	client *redis.Client
	name   string
}

// NewRedisChecker creates a new Redis health checker.
func NewRedisChecker(client *redis.Client) *RedisChecker {
	return &RedisChecker{
		client: client,
		name:   "redis",
	}
}

// Name returns the name of the checker.
func (r *RedisChecker) Name() string {
	return r.name
}

// Check performs the Redis health check.
func (r *RedisChecker) Check(ctx context.Context) error {
	// Ping Redis
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}

	// Optionally check Redis info for additional validation
	info, err := r.client.Info(ctx, "server").Result()
	if err != nil {
		return fmt.Errorf("failed to get redis info: %w", err)
	}

	// Simple validation that we got a response
	if len(info) == 0 {
		return fmt.Errorf("empty redis info response")
	}

	return nil
}

// DefaultMinDiskBytes is the default minimum free disk space (1 GB).
const DefaultMinDiskBytes uint64 = 1 * 1024 * 1024 * 1024

// DiskChecker checks available disk space.
type DiskChecker struct {
	path         string
	threshold    float64 // percentage threshold (e.g., 0.9 means unhealthy if >90% used)
	minFreeBytes uint64  // minimum free bytes required (default 1GB)
}

// NewDiskChecker creates a new disk space checker.
// The threshold is a fraction (0.0-1.0) representing the maximum acceptable disk
// usage. For example, 0.9 means the check will fail if more than 90% of the disk
// is used. The check also fails if free space is below 1GB (configurable via
// SetMinFreeBytes).
func NewDiskChecker(path string, threshold float64) *DiskChecker {
	return &DiskChecker{
		path:         path,
		threshold:    threshold,
		minFreeBytes: DefaultMinDiskBytes,
	}
}

// SetMinFreeBytes sets the minimum free bytes threshold.
func (d *DiskChecker) SetMinFreeBytes(minBytes uint64) {
	d.minFreeBytes = minBytes
}

// Name returns the name of the checker.
func (d *DiskChecker) Name() string {
	return "disk"
}

// Check performs the disk space check using syscall.Statfs.
// It reports unhealthy if disk usage exceeds the threshold percentage
// or if free space is below the minimum free bytes threshold.
func (d *DiskChecker) Check(ctx context.Context) error {
	if d.path == "" {
		return fmt.Errorf("disk checker path is empty")
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(d.path, &stat); err != nil {
		return fmt.Errorf("failed to get disk stats for %s: %w", d.path, err)
	}

	// Total space in bytes
	totalBytes := stat.Blocks * uint64(stat.Bsize)
	// Available space (for unprivileged users) in bytes
	availBytes := stat.Bavail * uint64(stat.Bsize)

	if totalBytes == 0 {
		return fmt.Errorf("disk reports zero total space for %s", d.path)
	}

	// Check absolute free space threshold
	if availBytes < d.minFreeBytes {
		return fmt.Errorf("disk space critically low on %s: %d MB available, minimum %d MB required",
			d.path, availBytes/(1024*1024), d.minFreeBytes/(1024*1024))
	}

	// Check percentage-based threshold
	usedBytes := totalBytes - availBytes
	usageRatio := float64(usedBytes) / float64(totalBytes)

	if usageRatio > d.threshold {
		return fmt.Errorf("disk usage on %s is %.1f%%, exceeds threshold of %.1f%%",
			d.path, usageRatio*100, d.threshold*100)
	}

	return nil
}

// DefaultMemoryLimit is the default memory limit (8 GB), matching the project's
// configured global memory limit.
const DefaultMemoryLimit uint64 = 8 * 1024 * 1024 * 1024

// MemoryChecker checks Go process memory usage using runtime.MemStats.
type MemoryChecker struct {
	threshold   float64 // percentage threshold (e.g., 0.8 means unhealthy if >80% of limit used)
	memoryLimit uint64  // the memory limit to compare against (default 8GB)
}

// NewMemoryChecker creates a new memory checker.
// The threshold is a fraction (0.0-1.0) representing the maximum acceptable memory
// usage relative to the configured memory limit. For example, 0.8 means the check
// will fail if allocated memory exceeds 80% of the limit. The default limit is 8GB,
// matching the project's global memory limit configuration.
func NewMemoryChecker(threshold float64) *MemoryChecker {
	return &MemoryChecker{
		threshold:   threshold,
		memoryLimit: DefaultMemoryLimit,
	}
}

// SetMemoryLimit sets the memory limit to compare against.
func (m *MemoryChecker) SetMemoryLimit(limit uint64) {
	m.memoryLimit = limit
}

// Name returns the name of the checker.
func (m *MemoryChecker) Name() string {
	return "memory"
}

// Check performs the memory check using runtime.MemStats.
// It reports unhealthy if the total allocated memory (Alloc) exceeds the
// threshold percentage of the configured memory limit.
func (m *MemoryChecker) Check(ctx context.Context) error {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	if m.memoryLimit == 0 {
		return fmt.Errorf("memory limit is not configured (zero)")
	}

	// Use Alloc (bytes of allocated heap objects) as the primary metric.
	// This represents current live memory usage, excluding freed objects.
	allocBytes := memStats.Alloc
	usageRatio := float64(allocBytes) / float64(m.memoryLimit)

	if usageRatio > m.threshold {
		return fmt.Errorf("memory usage is %.1f%% of limit (%d MB / %d MB), exceeds threshold of %.1f%%",
			usageRatio*100,
			allocBytes/(1024*1024),
			m.memoryLimit/(1024*1024),
			m.threshold*100)
	}

	return nil
}
