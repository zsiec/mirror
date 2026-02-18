package registry

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, *RedisRegistry) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	registry := NewRedisRegistry(client, logger, 5*time.Minute)

	return mr, client, registry
}

func TestRedisRegistry_Register(t *testing.T) {
	mr, client, registry := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()

	stream := &Stream{
		ID:         "test-stream-1",
		Type:       StreamTypeSRT,
		Status:     StatusActive,
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "HEVC",
		CreatedAt:  time.Now(),
	}

	// Test successful registration
	err := registry.Register(ctx, stream)
	assert.NoError(t, err)

	// Verify in Redis
	key := "mirror:streams:test-stream-1"
	exists, err := client.Exists(ctx, key).Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	// Test duplicate registration (should succeed as update/heartbeat)
	originalCreatedAt := stream.CreatedAt
	time.Sleep(1 * time.Millisecond) // Ensure LastHeartbeat changes
	err = registry.Register(ctx, stream)
	assert.NoError(t, err) // Should succeed as update

	// Verify the stream was updated, not replaced
	updatedStream, err := registry.Get(ctx, stream.ID)
	assert.NoError(t, err)
	assert.Equal(t, originalCreatedAt.Unix(), updatedStream.CreatedAt.Unix()) // CreatedAt preserved
	assert.True(t, updatedStream.LastHeartbeat.After(originalCreatedAt))      // LastHeartbeat updated
}

func TestRedisRegistry_Get(t *testing.T) {
	mr, _, registry := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Register a stream first
	stream := &Stream{
		ID:         "test-stream-2",
		Type:       StreamTypeRTP,
		Status:     StatusActive,
		SourceAddr: "192.168.1.101:5004",
		VideoCodec: "HEVC",
		Resolution: "1920x1080",
		Bitrate:    5000000,
	}

	err := registry.Register(ctx, stream)
	require.NoError(t, err)

	// Test successful get
	retrieved, err := registry.Get(ctx, "test-stream-2")
	assert.NoError(t, err)
	assert.Equal(t, stream.ID, retrieved.ID)
	assert.Equal(t, stream.Type, retrieved.Type)
	assert.Equal(t, stream.Status, retrieved.Status)
	assert.Equal(t, stream.VideoCodec, retrieved.VideoCodec)

	// Test get non-existent
	_, err = registry.Get(ctx, "non-existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRedisRegistry_UpdateStatus(t *testing.T) {
	mr, _, registry := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Register a stream first
	stream := &Stream{
		ID:         "test-stream-3",
		Type:       StreamTypeSRT,
		Status:     StatusConnecting,
		SourceAddr: "192.168.1.102:1234",
	}

	err := registry.Register(ctx, stream)
	require.NoError(t, err)

	// Update status
	err = registry.UpdateStatus(ctx, stream.ID, StatusActive)
	assert.NoError(t, err)

	// Verify update
	retrieved, err := registry.Get(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, retrieved.Status)
}

func TestRedisRegistry_Unregister(t *testing.T) {
	mr, client, registry := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()

	// Register a stream first
	stream := &Stream{
		ID:         "test-stream-4",
		Type:       StreamTypeRTP,
		Status:     StatusActive,
		SourceAddr: "192.168.1.103:5004",
	}

	err := registry.Register(ctx, stream)
	require.NoError(t, err)

	// Unregister
	err = registry.Unregister(ctx, stream.ID)
	assert.NoError(t, err)

	// Verify removed from Redis
	key := "mirror:streams:test-stream-4"
	exists, err := client.Exists(ctx, key).Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Test unregister non-existent
	err = registry.Unregister(ctx, "non-existent")
	assert.Error(t, err) // Should error on non-existent
}

func TestRedisRegistry_List(t *testing.T) {
	mr, _, registry := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Register multiple streams
	streams := []*Stream{
		{
			ID:         "test-stream-5",
			Type:       StreamTypeSRT,
			Status:     StatusActive,
			SourceAddr: "192.168.1.104:1234",
		},
		{
			ID:         "test-stream-6",
			Type:       StreamTypeRTP,
			Status:     StatusActive,
			SourceAddr: "192.168.1.105:5004",
		},
		{
			ID:         "test-stream-7",
			Type:       StreamTypeSRT,
			Status:     StatusPaused,
			SourceAddr: "192.168.1.106:1234",
		},
	}

	for _, s := range streams {
		err := registry.Register(ctx, s)
		require.NoError(t, err)
	}

	// List all streams
	list, err := registry.List(ctx)
	assert.NoError(t, err)
	assert.Len(t, list, 3)

	// Verify all streams are in the list
	streamMap := make(map[string]*Stream)
	for _, s := range list {
		streamMap[s.ID] = s
	}

	for _, expected := range streams {
		actual, ok := streamMap[expected.ID]
		assert.True(t, ok)
		assert.Equal(t, expected.Type, actual.Type)
		assert.Equal(t, expected.Status, actual.Status)
	}
}

func TestRedisRegistry_UpdateHeartbeat(t *testing.T) {
	mr, _, registry := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Register a stream
	stream := &Stream{
		ID:         "test-stream-8",
		Type:       StreamTypeSRT,
		Status:     StatusActive,
		SourceAddr: "192.168.1.107:1234",
	}

	err := registry.Register(ctx, stream)
	require.NoError(t, err)

	// Get initial heartbeat time
	retrieved1, err := registry.Get(ctx, stream.ID)
	require.NoError(t, err)
	initialHeartbeat := retrieved1.LastHeartbeat

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Update heartbeat
	err = registry.UpdateHeartbeat(ctx, stream.ID)
	assert.NoError(t, err)

	// Verify heartbeat updated
	retrieved2, err := registry.Get(ctx, stream.ID)
	require.NoError(t, err)
	assert.True(t, retrieved2.LastHeartbeat.After(initialHeartbeat))
}

func TestRedisRegistry_UpdateStats(t *testing.T) {
	mr, _, registry := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Register a stream
	stream := &Stream{
		ID:         "test-stream-stats",
		Type:       StreamTypeSRT,
		Status:     StatusActive,
		SourceAddr: "192.168.1.108:1234",
	}

	err := registry.Register(ctx, stream)
	require.NoError(t, err)

	// Update stats
	stats := &StreamStats{
		BytesReceived:   1024000,
		PacketsReceived: 1000,
		PacketsLost:     5,
		Bitrate:         5000000,
	}

	err = registry.UpdateStats(ctx, stream.ID, stats)
	assert.NoError(t, err)

	// Verify stats updated
	retrieved, err := registry.Get(ctx, stream.ID)
	require.NoError(t, err)
	assert.Equal(t, stats.BytesReceived, retrieved.BytesReceived)
	assert.Equal(t, stats.PacketsReceived, retrieved.PacketsReceived)
	assert.Equal(t, stats.PacketsLost, retrieved.PacketsLost)
	assert.Equal(t, stats.Bitrate, retrieved.Bitrate)

	// Test update stats for non-existent stream
	err = registry.UpdateStats(ctx, "non-existent", stats)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRedisRegistry_Delete(t *testing.T) {
	mr, client, registry := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()

	// Register a stream first
	stream := &Stream{
		ID:         "test-stream-delete",
		Type:       StreamTypeRTP,
		Status:     StatusActive,
		SourceAddr: "192.168.1.109:5004",
	}

	err := registry.Register(ctx, stream)
	require.NoError(t, err)

	// Delete using Delete method (should be alias for Unregister)
	err = registry.Delete(ctx, stream.ID)
	assert.NoError(t, err)

	// Verify removed from Redis
	key := "mirror:streams:test-stream-delete"
	exists, err := client.Exists(ctx, key).Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), exists)

	// Verify stream is gone
	_, err = registry.Get(ctx, stream.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRedisRegistry_Update(t *testing.T) {
	mr, _, registry := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Register a stream first
	originalStream := &Stream{
		ID:         "test-stream-update",
		Type:       StreamTypeSRT,
		Status:     StatusActive,
		SourceAddr: "192.168.1.110:1234",
		VideoCodec: "HEVC",
		Resolution: "1920x1080",
		Bitrate:    5000000,
	}

	err := registry.Register(ctx, originalStream)
	require.NoError(t, err)

	// Update the stream with new values
	updatedStream := &Stream{
		ID:              "test-stream-update",
		Type:            StreamTypeSRT,
		Status:          StatusActive,
		SourceAddr:      "192.168.1.110:1234",
		VideoCodec:      "HEVC",
		Resolution:      "3840x2160", // Changed
		Bitrate:         10000000,    // Changed
		BytesReceived:   2048000,     // Added stats
		PacketsReceived: 2000,
		PacketsLost:     10,
	}

	err = registry.Update(ctx, updatedStream)
	assert.NoError(t, err)

	// Verify updates
	retrieved, err := registry.Get(ctx, updatedStream.ID)
	require.NoError(t, err)
	assert.Equal(t, updatedStream.Resolution, retrieved.Resolution)
	assert.Equal(t, updatedStream.Bitrate, retrieved.Bitrate)
	assert.Equal(t, updatedStream.BytesReceived, retrieved.BytesReceived)
	assert.Equal(t, updatedStream.PacketsReceived, retrieved.PacketsReceived)

	// Test update non-existent stream (should fail since Update uses XX flag)
	nonExistentStream := &Stream{
		ID:         "non-existent",
		Type:       StreamTypeSRT,
		Status:     StatusActive,
		SourceAddr: "192.168.1.111:1234",
	}
	err = registry.Update(ctx, nonExistentStream)
	assert.Error(t, err) // Update only updates existing streams, won't create new ones
}
