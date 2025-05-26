package ingestion

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/memory"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/ingestion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/queue"
)

const (
	// DefaultMaxMemory is the default total memory limit (2.5GB)
	DefaultMaxMemory = int64(2684354560)
	// DefaultMaxPerStream is the default per-stream memory limit (200MB)
	DefaultMaxPerStream = int64(209715200)
	// DefaultBitrate is the default stream bitrate (50 Mbps)
	DefaultBitrate = int64(52428800)
)

// Manager coordinates all ingestion components
type Manager struct {
	config           *config.IngestionConfig
	registry         registry.Registry
	memoryController *memory.Controller
	srtListener      *srt.ListenerAdapter
	rtpListener      *rtp.Listener
	logger           logger.Logger

	// Stream handlers with video awareness and backpressure support
	streamHandlers map[string]*StreamHandler
	handlersMu     sync.RWMutex
	handlerWg      sync.WaitGroup // Track handler goroutines

	// Stream operation locks to prevent concurrent operations on same stream
	streamOpLocks   map[string]*sync.Mutex
	streamOpLocksMu sync.Mutex

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.RWMutex
	started bool
}

// NewManager creates a new ingestion manager
func NewManager(cfg *config.IngestionConfig, logger logger.Logger) (*Manager, error) {
	// Create Redis client for registry
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Registry.RedisAddr,
		Password: cfg.Registry.RedisPassword,
		DB:       cfg.Registry.RedisDB,
	})

	// Test Redis connection
	ctx := context.Background()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis for registry: %w", err)
	}

	logrusLogger := logrus.New()
	reg := registry.NewRedisRegistry(redisClient, logrusLogger)

	// Create memory controller
	maxTotal := cfg.Memory.MaxTotal
	maxPerStream := cfg.Memory.MaxPerStream

	if maxTotal == 0 {
		maxTotal = DefaultMaxMemory
	}
	if maxPerStream == 0 {
		maxPerStream = DefaultMaxPerStream
	}

	memController := memory.NewController(maxTotal, maxPerStream)

	ctx, cancel := context.WithCancel(context.Background())

	m := &Manager{
		config:           cfg,
		registry:         reg,
		memoryController: memController,
		streamHandlers:   make(map[string]*StreamHandler),
		streamOpLocks:    make(map[string]*sync.Mutex),
		logger:           logger.WithField("component", "ingestion_manager"),
		ctx:              ctx,
		cancel:           cancel,
	}

	// Create SRT listener if enabled using Haivision adapter
	if cfg.SRT.Enabled {
		// Use new Haivision adapter for better FFmpeg compatibility
		adapter := srt.NewHaivisionAdapter()
		srtListener := srt.NewListenerWithAdapter(&cfg.SRT, &cfg.Codecs, reg, adapter, logger)

		// Set the connection handler to use proper buffering
		srtListener.SetHandler(func(conn *srt.Connection) error {
			return m.HandleSRTConnection(conn)
		})

		// Store as the old interface type for now (we'll clean this up)
		m.srtListener = &srt.ListenerAdapter{Listener: srtListener}
	}

	// Create RTP listener if enabled
	if cfg.RTP.Enabled {
		m.rtpListener = rtp.NewListener(&cfg.RTP, &cfg.Codecs, reg, logger)
		// Set the session handler to use proper buffering
		m.rtpListener.SetSessionHandler(func(session *rtp.Session) error {
			return m.HandleRTPSession(session)
		})
	}

	return m, nil
}

// Start starts all ingestion components
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("ingestion manager already started")
	}

	m.logger.Info("Starting ingestion manager")

	// Start SRT listener
	if m.srtListener != nil {
		if err := m.srtListener.Start(); err != nil {
			return fmt.Errorf("failed to start SRT listener: %w", err)
		}
		m.logger.Info("SRT listener started")
	}

	// Start RTP listener
	if m.rtpListener != nil {
		if err := m.rtpListener.Start(); err != nil {
			// Stop SRT if it was started
			if m.srtListener != nil {
				m.srtListener.Stop()
			}
			return fmt.Errorf("failed to start RTP listener: %w", err)
		}
		m.logger.Info("RTP listener started")
	}

	m.started = true
	m.logger.Info("Ingestion manager started successfully")

	return nil
}

// Stop stops all ingestion components
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return nil
	}

	m.logger.Info("Stopping ingestion manager")

	// Cancel context to signal all components to stop
	if m.cancel != nil {
		m.cancel()
	}

	var errors []error

	// Stop SRT listener
	if m.srtListener != nil {
		if err := m.srtListener.Stop(); err != nil {
			errors = append(errors, fmt.Errorf("failed to stop SRT listener: %w", err))
		}
	}

	// Stop RTP listener
	if m.rtpListener != nil {
		if err := m.rtpListener.Stop(); err != nil {
			errors = append(errors, fmt.Errorf("failed to stop RTP listener: %w", err))
		}
	}

	// Stop all stream handlers
	m.handlersMu.RLock()
	handlers := make(map[string]*StreamHandler)
	for k, v := range m.streamHandlers {
		handlers[k] = v
	}
	m.handlersMu.RUnlock()

	for streamID, handler := range handlers {
		m.logger.WithField("stream_id", streamID).Info("Stopping stream handler")
		if err := handler.Stop(); err != nil {
			errors = append(errors, fmt.Errorf("failed to stop stream handler %s: %w", streamID, err))
		}
	}

	// Wait for all handler goroutines to complete
	m.logger.Info("Waiting for all stream handlers to complete")
	m.handlerWg.Wait()

	// Clear the handlers map after all have stopped
	m.handlersMu.Lock()
	m.streamHandlers = make(map[string]*StreamHandler)
	m.handlersMu.Unlock()

	// Close the registry to clean up Redis connection
	if m.registry != nil {
		if err := m.registry.Close(); err != nil {
			errors = append(errors, fmt.Errorf("failed to close registry: %w", err))
		}
	}

	m.started = false

	if len(errors) > 0 {
		return fmt.Errorf("errors during shutdown: %v", errors)
	}

	m.logger.Info("Ingestion manager stopped successfully")
	return nil
}

// GetRegistry returns the stream registry
func (m *Manager) GetRegistry() registry.Registry {
	return m.registry
}

// GetActiveStreams returns all active streams from the registry
func (m *Manager) GetActiveStreams(ctx context.Context) ([]*registry.Stream, error) {
	return m.registry.List(ctx)
}

// GetStream returns a specific stream by ID
func (m *Manager) GetStream(ctx context.Context, streamID string) (*registry.Stream, error) {
	return m.registry.Get(ctx, streamID)
}

// GetStats returns ingestion statistics
func (m *Manager) GetStats(ctx context.Context) IngestionStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := IngestionStats{
		Started: m.started,
	}

	if m.srtListener != nil {
		stats.SRTEnabled = true
	}

	if m.rtpListener != nil {
		stats.RTPEnabled = true
	}

	// Get active streams count from registry
	streams, err := m.registry.List(ctx)
	if err == nil {
		stats.TotalStreams = len(streams)

		// Count sessions by type from registry
		for _, stream := range streams {
			switch stream.Type {
			case registry.StreamTypeSRT:
				stats.SRTSessions++
			case registry.StreamTypeRTP:
				stats.RTPSessions++
			}
		}
	}

	// Get active stream handlers count safely
	m.handlersMu.RLock()
	stats.ActiveHandlers = len(m.streamHandlers)
	m.handlersMu.RUnlock()

	return stats
}

// TerminateStream terminates a stream and removes it from the registry
func (m *Manager) TerminateStream(ctx context.Context, streamID string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.started {
		return fmt.Errorf("ingestion manager not started")
	}

	// Get stream info to determine which listener to use
	stream, err := m.registry.Get(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found: %w", err)
	}

	// Terminate based on stream type
	switch stream.Type {
	case "srt":
		if m.srtListener != nil {
			if err := m.srtListener.TerminateStream(streamID); err != nil {
				return fmt.Errorf("failed to terminate SRT stream: %w", err)
			}
		}
		// Allow termination even if SRT is disabled - we want to clean up existing streams
	case "rtp":
		if m.rtpListener != nil {
			if err := m.rtpListener.TerminateStream(streamID); err != nil {
				return fmt.Errorf("failed to terminate RTP stream: %w", err)
			}
		}
		// Allow termination even if RTP is disabled - we want to clean up existing streams
	default:
		return fmt.Errorf("unknown stream type: %s", stream.Type)
	}

	// Remove from registry
	if err := m.registry.Delete(ctx, streamID); err != nil {
		m.logger.WithError(err).WithField("stream_id", streamID).Error("Failed to remove stream from registry")
	}

	// Remove stream handler
	m.RemoveStreamHandler(streamID)

	// Clean up stream operation lock
	m.streamOpLocksMu.Lock()
	delete(m.streamOpLocks, streamID)
	m.streamOpLocksMu.Unlock()

	m.logger.WithField("stream_id", streamID).Info("Stream terminated")
	return nil
}

// getStreamOpLock gets or creates a lock for stream operations
func (m *Manager) getStreamOpLock(streamID string) *sync.Mutex {
	m.streamOpLocksMu.Lock()
	defer m.streamOpLocksMu.Unlock()

	if lock, exists := m.streamOpLocks[streamID]; exists {
		return lock
	}

	lock := &sync.Mutex{}
	m.streamOpLocks[streamID] = lock
	return lock
}

// PauseStream pauses data ingestion for a stream
func (m *Manager) PauseStream(ctx context.Context, streamID string) error {
	// Get stream-specific lock to prevent concurrent operations
	streamLock := m.getStreamOpLock(streamID)
	streamLock.Lock()
	defer streamLock.Unlock()

	// Check if manager is started (read lock is sufficient)
	m.mu.RLock()
	if !m.started {
		m.mu.RUnlock()
		return fmt.Errorf("ingestion manager not started")
	}
	m.mu.RUnlock()

	// Get stream info
	stream, err := m.registry.Get(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found: %w", err)
	}

	// Check stream status
	if stream.Status == "paused" {
		return nil // Already paused
	}

	if stream.Status != "active" {
		return fmt.Errorf("stream is not active (status: %s)", stream.Status)
	}

	// Pause based on stream type
	// Need read lock to access listeners
	m.mu.RLock()
	switch stream.Type {
	case "srt":
		if m.srtListener == nil {
			m.mu.RUnlock()
			return fmt.Errorf("SRT is not enabled")
		}
		if err := m.srtListener.PauseStream(streamID); err != nil {
			m.mu.RUnlock()
			return fmt.Errorf("failed to pause SRT stream: %w", err)
		}
	case "rtp":
		if m.rtpListener == nil {
			m.mu.RUnlock()
			return fmt.Errorf("RTP is not enabled")
		}
		if err := m.rtpListener.PauseStream(streamID); err != nil {
			m.mu.RUnlock()
			return fmt.Errorf("failed to pause RTP stream: %w", err)
		}
	default:
		m.mu.RUnlock()
		return fmt.Errorf("unknown stream type: %s", stream.Type)
	}
	m.mu.RUnlock()

	// Save original status for rollback
	originalStatus := stream.Status

	// Update registry status
	stream.Status = "paused"
	if err := m.registry.Update(ctx, stream); err != nil {
		m.logger.WithError(err).WithField("stream_id", streamID).Error("Failed to update stream status in registry")

		// Rollback the pause operation
		m.logger.WithField("stream_id", streamID).Info("Rolling back pause operation")

		// Attempt to resume the stream to restore original state
		m.mu.RLock()
		var rollbackErr error
		switch stream.Type {
		case "srt":
			if m.srtListener != nil {
				rollbackErr = m.srtListener.ResumeStream(streamID)
			}
		case "rtp":
			if m.rtpListener != nil {
				rollbackErr = m.rtpListener.ResumeStream(streamID)
			}
		}
		m.mu.RUnlock()

		if rollbackErr != nil {
			m.logger.WithError(rollbackErr).WithField("stream_id", streamID).Error("Failed to rollback pause operation")
			return fmt.Errorf("failed to update registry and rollback failed: %w", err)
		}

		// Restore original status in memory
		stream.Status = originalStatus
		return fmt.Errorf("failed to update stream status in registry: %w", err)
	}

	m.logger.WithField("stream_id", streamID).Info("Stream paused")
	return nil
}

// ResumeStream resumes data ingestion for a paused stream
func (m *Manager) ResumeStream(ctx context.Context, streamID string) error {
	// Get stream-specific lock to prevent concurrent operations
	streamLock := m.getStreamOpLock(streamID)
	streamLock.Lock()
	defer streamLock.Unlock()

	// Check if manager is started (read lock is sufficient)
	m.mu.RLock()
	if !m.started {
		m.mu.RUnlock()
		return fmt.Errorf("ingestion manager not started")
	}
	m.mu.RUnlock()

	// Get stream info
	stream, err := m.registry.Get(ctx, streamID)
	if err != nil {
		return fmt.Errorf("stream not found: %w", err)
	}

	// Check if not paused
	if stream.Status != "paused" {
		return fmt.Errorf("stream is not paused (status: %s)", stream.Status)
	}

	// Resume based on stream type
	// Need read lock to access listeners
	m.mu.RLock()
	switch stream.Type {
	case "srt":
		if m.srtListener == nil {
			m.mu.RUnlock()
			return fmt.Errorf("SRT is not enabled")
		}
		if err := m.srtListener.ResumeStream(streamID); err != nil {
			m.mu.RUnlock()
			return fmt.Errorf("failed to resume SRT stream: %w", err)
		}
	case "rtp":
		if m.rtpListener == nil {
			m.mu.RUnlock()
			return fmt.Errorf("RTP is not enabled")
		}
		if err := m.rtpListener.ResumeStream(streamID); err != nil {
			m.mu.RUnlock()
			return fmt.Errorf("failed to resume RTP stream: %w", err)
		}
	default:
		m.mu.RUnlock()
		return fmt.Errorf("unknown stream type: %s", stream.Type)
	}
	m.mu.RUnlock()

	// Save original status for rollback
	originalStatus := stream.Status

	// Update registry status
	stream.Status = "active"
	if err := m.registry.Update(ctx, stream); err != nil {
		m.logger.WithError(err).WithField("stream_id", streamID).Error("Failed to update stream status in registry")

		// Rollback the resume operation
		m.logger.WithField("stream_id", streamID).Info("Rolling back resume operation")

		// Attempt to pause the stream again to restore original state
		m.mu.RLock()
		var rollbackErr error
		switch stream.Type {
		case "srt":
			if m.srtListener != nil {
				rollbackErr = m.srtListener.PauseStream(streamID)
			}
		case "rtp":
			if m.rtpListener != nil {
				rollbackErr = m.rtpListener.PauseStream(streamID)
			}
		}
		m.mu.RUnlock()

		if rollbackErr != nil {
			m.logger.WithError(rollbackErr).WithField("stream_id", streamID).Error("Failed to rollback resume operation")
			return fmt.Errorf("failed to update registry and rollback failed: %w", err)
		}

		// Restore original status in memory
		stream.Status = originalStatus
		return fmt.Errorf("failed to update stream status in registry: %w", err)
	}

	m.logger.WithField("stream_id", streamID).Info("Stream resumed")
	return nil
}

// CreateStreamHandler creates a new stream handler with backpressure support
func (m *Manager) CreateStreamHandler(streamID string, conn StreamConnection) (*StreamHandler, error) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()

	// Check if handler already exists
	if _, exists := m.streamHandlers[streamID]; exists {
		return nil, fmt.Errorf("stream handler already exists for %s", streamID)
	}

	// Create hybrid queue with disk overflow
	diskDir := m.config.QueueDir
	if diskDir == "" {
		diskDir = "/tmp/mirror/queue" // Default fallback
	}
	hybridQueue, err := queue.NewHybridQueue(streamID, 10000, diskDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create hybrid queue: %w", err)
	}

	// Create stream handler with manager's context
	handler := NewStreamHandler(m.ctx, streamID, conn, hybridQueue, m.memoryController, m.logger)

	// Store handler
	m.streamHandlers[streamID] = handler

	// Start handler with tracking
	m.handlerWg.Add(1)
	go func() {
		defer m.handlerWg.Done()
		handler.Start()
	}()

	return handler, nil
}

// RemoveStreamHandler removes a stream handler
func (m *Manager) RemoveStreamHandler(streamID string) {
	m.handlersMu.Lock()
	defer m.handlersMu.Unlock()

	if handler, exists := m.streamHandlers[streamID]; exists {
		handler.Stop()
		delete(m.streamHandlers, streamID)
	}
}

// GetStreamHandler returns a stream handler by ID
func (m *Manager) GetStreamHandler(streamID string) (*StreamHandler, bool) {
	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock()

	handler, exists := m.streamHandlers[streamID]
	return handler, exists
}

// GetStreamHandlerWithStats atomically gets handler stats to prevent race conditions
func (m *Manager) GetStreamHandlerWithStats(streamID string) (StreamStats, bool) {
	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock()

	handler, exists := m.streamHandlers[streamID]
	if !exists {
		return StreamStats{}, false
	}

	// Get stats while holding the lock - eliminates race condition
	stats := handler.GetStats()
	return stats, true
}

// GetStreamHandlerAndStats atomically gets both handler and stats to prevent race conditions
func (m *Manager) GetStreamHandlerAndStats(streamID string) (*StreamHandler, StreamStats, bool) {
	m.handlersMu.RLock()
	defer m.handlersMu.RUnlock()

	handler, exists := m.streamHandlers[streamID]
	if !exists {
		return nil, StreamStats{}, false
	}

	// Get stats while holding the lock - eliminates race condition
	stats := handler.GetStats()
	return handler, stats, true
}

// HandleSRTConnection handles a new SRT connection with proper backpressure
func (m *Manager) HandleSRTConnection(conn *srt.Connection) error {
	streamID := conn.GetStreamID()

	// Register stream in registry (similar to RTP streams)
	if m.registry != nil {
		stream := &registry.Stream{
			ID:            streamID,
			Type:          registry.StreamTypeSRT,
			Status:        registry.StatusActive,
			SourceAddr:    conn.GetRemoteAddr(), // Get actual remote address from SRT connection
			CreatedAt:     time.Now(),
			LastHeartbeat: time.Now(),
			VideoCodec:    "Unknown", // Will be updated when codec is detected
		}

		if err := m.registry.Register(context.Background(), stream); err != nil {
			m.logger.WithError(err).Error("Failed to register SRT stream in registry", "stream_id", streamID)
		} else {
			m.logger.Info("SRT stream registered successfully", "stream_id", streamID)
		}
	}

	// Create adapter
	adapter := NewSRTConnectionAdapter(conn)
	if adapter == nil {
		return fmt.Errorf("failed to create SRT connection adapter")
	}

	// Create and start stream handler
	handler, err := m.CreateStreamHandler(streamID, adapter)
	if err != nil {
		// Cleanup adapter on failure
		if closeErr := adapter.Close(); closeErr != nil {
			m.logger.WithError(closeErr).Warn("Failed to close adapter after handler creation failure")
		}
		// Unregister from registry on failure
		if m.registry != nil {
			m.registry.Unregister(context.Background(), streamID)
		}
		return fmt.Errorf("failed to create stream handler: %w", err)
	}

	// Handler is already started, just wait for completion
	<-handler.ctx.Done()

	// Cleanup
	m.RemoveStreamHandler(streamID)
	// Unregister from registry
	if m.registry != nil {
		if err := m.registry.Unregister(context.Background(), streamID); err != nil {
			m.logger.WithError(err).Error("Failed to unregister SRT stream from registry", "stream_id", streamID)
		}
	}
	return nil
}

// HandleRTPSession handles a new RTP session with proper backpressure
func (m *Manager) HandleRTPSession(session *rtp.Session) error {
	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: Entry")

	// Detect codec from session
	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: About to call DetectCodecFromRTPSession")
	codecType := DetectCodecFromRTPSession(session)
	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: DetectCodecFromRTPSession completed")
	if codecType == types.CodecUnknown {
		// Default to H.264 for video sessions
		m.logger.Warn("Could not detect codec, defaulting to H.264")
		codecType = types.CodecH264
	}

	m.logger.WithFields(map[string]interface{}{
		"stream_id":    session.GetStreamID(),
		"codec":        codecType.String(),
		"payload_type": session.GetPayloadType(),
	}).Info("HandleRTPSession: Detected codec for RTP session")

	// Create adapter
	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: Creating RTP connection adapter")
	adapter := NewRTPConnectionAdapter(session, codecType)
	if adapter == nil {
		m.logger.WithField("stream_id", session.GetStreamID()).Error("HandleRTPSession: Failed to create RTP connection adapter")
		return fmt.Errorf("failed to create RTP connection adapter")
	}

	// Create and start stream handler
	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: Creating stream handler")
	handler, err := m.CreateStreamHandler(session.GetStreamID(), adapter)
	if err != nil {
		// Cleanup adapter on failure
		if closeErr := adapter.Close(); closeErr != nil {
			m.logger.WithError(closeErr).Warn("Failed to close adapter after handler creation failure")
		}
		m.logger.WithError(err).WithField("stream_id", session.GetStreamID()).Error("HandleRTPSession: Failed to create stream handler")
		return fmt.Errorf("failed to create stream handler: %w", err)
	}

	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: Handler created successfully, waiting for completion")

	// Handler is already started, just wait for completion
	<-handler.ctx.Done()

	m.logger.WithField("stream_id", session.GetStreamID()).Info("HandleRTPSession: Handler completed, cleaning up")

	// Cleanup
	m.RemoveStreamHandler(session.GetStreamID())
	return nil
}

// IngestionStats holds ingestion statistics
type IngestionStats struct {
	Started        bool `json:"started"`
	SRTEnabled     bool `json:"srt_enabled"`
	RTPEnabled     bool `json:"rtp_enabled"`
	SRTSessions    int  `json:"srt_sessions"`
	RTPSessions    int  `json:"rtp_sessions"`
	TotalStreams   int  `json:"total_streams"`
	ActiveHandlers int  `json:"active_handlers"` // Number of active stream handlers
}
