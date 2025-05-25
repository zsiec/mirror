package tests

import (
	"context"
	"testing"
	
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// SetupTestRedis creates a test Redis instance using miniredis
func SetupTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	
	// Create miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)
	
	// Register cleanup
	t.Cleanup(func() {
		mr.Close()
	})
	
	// Create Redis client
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	
	// Verify connection
	ctx := context.Background()
	err = client.Ping(ctx).Err()
	require.NoError(t, err)
	
	return client
}

// CleanupRedis clears all data in Redis
func CleanupRedis(t *testing.T, client *redis.Client) {
	t.Helper()
	
	ctx := context.Background()
	err := client.FlushAll(ctx).Err()
	require.NoError(t, err)
}
