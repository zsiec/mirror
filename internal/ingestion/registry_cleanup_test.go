package ingestion

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

// mockRedisClient tracks whether Close was called
type mockRedisClient struct {
	*redis.Client
	closeCalled bool
}

func (m *mockRedisClient) Close() error {
	m.closeCalled = true
	return nil
}

// trackableRegistry wraps RedisRegistry to track Close calls
type trackableRegistry struct {
	*registry.RedisRegistry
	closeCalled bool
	closeErr    error
}

func (t *trackableRegistry) Close() error {
	t.closeCalled = true
	if t.closeErr != nil {
		return t.closeErr
	}
	return t.RedisRegistry.Close()
}

func TestManager_RegistryCleanup(t *testing.T) {
	// Create a mock registry that tracks Close calls
	mockReg := &trackableRegistry{
		RedisRegistry: &registry.RedisRegistry{},
		closeCalled:   false,
	}

	// Create manager with mock registry
	cfg := &config.IngestionConfig{
		SRT: config.SRTConfig{
			Enabled: false,
		},
		RTP: config.RTPConfig{
			Enabled: false,
		},
	}

	manager := &Manager{
		config:         cfg,
		registry:       mockReg,
		streamHandlers: make(map[string]*StreamHandler),
		streamOpLocks:  make(map[string]*sync.Mutex),
		logger:         logger.Logger(logrus.New()),
		started:        true,
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx
	manager.cancel = cancel

	// Stop the manager
	err := manager.Stop()
	require.NoError(t, err)

	// Verify registry was closed
	assert.True(t, mockReg.closeCalled, "Registry Close() should have been called")
}

func TestManager_RegistryCleanupError(t *testing.T) {
	// Create a mock registry that returns an error on Close
	mockReg := &trackableRegistry{
		RedisRegistry: &registry.RedisRegistry{},
		closeCalled:   false,
		closeErr:      assert.AnError,
	}

	// Create manager with mock registry
	cfg := &config.IngestionConfig{
		SRT: config.SRTConfig{
			Enabled: false,
		},
		RTP: config.RTPConfig{
			Enabled: false,
		},
	}

	manager := &Manager{
		config:         cfg,
		registry:       mockReg,
		streamHandlers: make(map[string]*StreamHandler),
		streamOpLocks:  make(map[string]*sync.Mutex),
		logger:         logger.Logger(logrus.New()),
		started:        true,
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx
	manager.cancel = cancel

	// Stop the manager
	err := manager.Stop()

	// Should get an error that includes the registry close error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to close registry")
	assert.True(t, mockReg.closeCalled, "Registry Close() should have been called even if it errors")
}

// TestRedisRegistry_Close tests the actual RedisRegistry Close method
func TestRedisRegistry_Close(t *testing.T) {
	// Skip if Redis is not available
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available, skipping test")
	}
	client.Close()

	// Create a real Redis registry
	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	reg := registry.NewRedisRegistry(redisClient, logrus.New())

	// Close the registry
	err := reg.Close()
	assert.NoError(t, err)

	// Verify the client is closed by trying to ping
	// This should fail because the connection is closed
	err = redisClient.Ping(context.Background()).Err()
	assert.Error(t, err)
}

// TestRedisRegistry_CloseNilClient tests closing with nil client
func TestRedisRegistry_CloseNilClient(t *testing.T) {
	reg := &registry.RedisRegistry{}

	// Should not panic with nil client
	err := reg.Close()
	assert.NoError(t, err)
}
