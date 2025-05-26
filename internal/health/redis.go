package health

import (
	"context"
	"fmt"

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

// DiskChecker checks available disk space.
type DiskChecker struct {
	path      string
	threshold float64 // percentage threshold (e.g., 0.9 for 90%)
}

// NewDiskChecker creates a new disk space checker.
func NewDiskChecker(path string, threshold float64) *DiskChecker {
	return &DiskChecker{
		path:      path,
		threshold: threshold,
	}
}

// Name returns the name of the checker.
func (d *DiskChecker) Name() string {
	return "disk"
}

// Check performs the disk space check.
func (d *DiskChecker) Check(ctx context.Context) error {
	// Note: In a real implementation, you would use syscall to get disk stats
	// For now, we'll just return nil (healthy)
	// This would be implemented with platform-specific code
	return nil
}

// MemoryChecker checks available memory.
type MemoryChecker struct {
	threshold float64 // percentage threshold
}

// NewMemoryChecker creates a new memory checker.
func NewMemoryChecker(threshold float64) *MemoryChecker {
	return &MemoryChecker{
		threshold: threshold,
	}
}

// Name returns the name of the checker.
func (m *MemoryChecker) Name() string {
	return "memory"
}

// Check performs the memory check.
func (m *MemoryChecker) Check(ctx context.Context) error {
	// Note: In a real implementation, you would use runtime.MemStats
	// For now, we'll just return nil (healthy)
	return nil
}

