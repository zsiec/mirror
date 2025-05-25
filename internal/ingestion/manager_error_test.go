package ingestion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/backpressure"
	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

// mockFailingConnection simulates a connection that fails to close
type mockFailingConnection struct {
	VideoAwareConnection
	closeError error
}

func (m *mockFailingConnection) Close() error {
	if m.closeError != nil {
		return m.closeError
	}
	return m.VideoAwareConnection.Close()
}

// mockFailingRegistry simulates a registry that fails to close
type mockFailingRegistry struct {
	registry.Registry
	closeError error
}

func (m *mockFailingRegistry) Close() error {
	return m.closeError
}

// TestManager_StopErrorCollection tests that Stop collects all errors
func TestManager_StopErrorCollection(t *testing.T) {
	// Create manager with mock components
	cfg := &config.IngestionConfig{
		SRT: config.SRTConfig{
			Enabled: false,
		},
		RTP: config.RTPConfig{
			Enabled: false,
		},
	}

	// Create a failing registry
	failingReg := &mockFailingRegistry{
		Registry:   &mockRegistry{streams: make(map[string]*registry.Stream)},
		closeError: errors.New("registry close failed"),
	}

	manager := &Manager{
		config:           cfg,
		registry:         failingReg,
		memoryController: memory.NewController(100*1024*1024, 50*1024*1024),
		streamHandlers:   make(map[string]*StreamHandler),
		streamOpLocks:    make(map[string]*sync.Mutex),
		logger:           logger.Logger(logrus.New()),
		started:          true,
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx
	manager.cancel = cancel

	// Don't create actual handlers - the test is about error collection from Stop()

	// Stop the manager
	err := manager.Stop()

	// Should get error from registry
	require.Error(t, err)
	errStr := err.Error()

	// Check that registry error is collected
	assert.Contains(t, errStr, "registry close failed")
}

// TestManager_StopNoErrors tests Stop with no errors
func TestManager_StopNoErrors(t *testing.T) {
	manager, mr := setupTestManager(t)
	defer mr.Close()

	err := manager.Start()
	require.NoError(t, err)

	// Create a working stream handler
	stream := &registry.Stream{
		ID:         "test-stream",
		Type:       "srt",
		SourceAddr: "192.168.1.100:1234",
		VideoCodec: "H264",
	}
	err = manager.GetRegistry().Register(context.Background(), stream)
	require.NoError(t, err)

	mockConn := createTestConnection("test-stream", []byte("test data"))
	handler, err := manager.CreateStreamHandler("test-stream", mockConn)
	require.NoError(t, err)
	require.NotNil(t, handler)

	// Stop should succeed without errors
	err = manager.Stop()
	assert.NoError(t, err)
}

// TestStreamHandler_StopErrorCollection tests StreamHandler error collection
func TestStreamHandler_StopErrorCollection(t *testing.T) {
	// Create a failing connection
	mockConn := &mockFailingConnection{
		VideoAwareConnection: createTestConnection("test-stream", nil),
		closeError:          errors.New("connection close failed"),
	}

	// Create handler with proper backpressure controller
	bpConfig := backpressure.Config{
		MinRate:        1 * 1024 * 1024,  // 1 Mbps
		MaxRate:        50 * 1024 * 1024, // 50 Mbps
		TargetPressure: 0.7,
		IncreaseRatio:  1.1,
		DecreaseRatio:  0.7,
		AdjustInterval: 100 * time.Millisecond,
		HistorySize:    10,
	}
	bpController := backpressure.NewController("test-stream", bpConfig, logger.Logger(logrus.New()))
	
	handler := &StreamHandler{
		streamID:         "test-stream",
		conn:             mockConn,
		ctx:              context.Background(),
		cancel:           func() {},
		memoryController: memory.NewController(100*1024*1024, 50*1024*1024),
		logger:           logger.Logger(logrus.New()),
		started:          true,
		bpController:     bpController,
	}

	// Stop should return error
	err := handler.Stop()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection close failed")
}

// Helper to get unique stream IDs
func getStreamID(index int) string {
	return "stream-" + string(rune('a'+index))
}

