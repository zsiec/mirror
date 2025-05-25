package srt

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	srt "github.com/datarhei/gosrt"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/ingestion/ratelimit"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// ConnectionHandler is a function that handles new SRT connections
type ConnectionHandler func(*Connection) error

// Listener handles SRT stream ingestion
type Listener struct {
	config           *config.SRTConfig
	codecsConfig     *config.CodecsConfig
	listener         srt.Listener
	registry         registry.Registry
	connLimiter      *ratelimit.ConnectionLimiter
	bandwidthManager *ratelimit.BandwidthManager
	codecDetector    *codec.Detector
	logger           logger.Logger
	handler          ConnectionHandler // Handler for new connections

	connections       sync.Map // streamID -> *Connection
	wg                sync.WaitGroup
	ctx               context.Context
	cancel            context.CancelFunc
	testStatsInterval time.Duration // For testing: custom stats interval
}

// NewListener creates a new SRT listener
func NewListener(cfg *config.SRTConfig, codecsCfg *config.CodecsConfig, reg registry.Registry, logger logger.Logger) *Listener {
	ctx, cancel := context.WithCancel(context.Background())

	// Create connection limiter (max 5 connections per stream, 100 total)
	connLimiter := ratelimit.NewConnectionLimiter(5, 100)

	// Create bandwidth manager (total 1 Gbps)
	bandwidthManager := ratelimit.NewBandwidthManager(1_000_000_000) // 1 Gbps

	// Create codec detector
	codecDetector := codec.NewDetector()

	return &Listener{
		config:           cfg,
		codecsConfig:     codecsCfg,
		registry:         reg,
		connLimiter:      connLimiter,
		codecDetector:    codecDetector,
		bandwidthManager: bandwidthManager,
		logger:           logger,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// SetTestStatsInterval sets a custom stats update interval for testing
func (l *Listener) SetTestStatsInterval(interval time.Duration) {
	l.testStatsInterval = interval
}

// SetConnectionHandler sets the handler for new connections
func (l *Listener) SetConnectionHandler(handler ConnectionHandler) {
	l.handler = handler
}

// Start starts the SRT listener
func (l *Listener) Start() error {
	srtConfig := srt.DefaultConfig()
	srtConfig.FC = uint32(l.config.FlowControlWindow)
	srtConfig.InputBW = l.config.InputBandwidth
	srtConfig.MaxBW = l.config.MaxBandwidth
	srtConfig.Latency = l.config.Latency
	srtConfig.PayloadSize = uint32(l.config.PayloadSize)
	srtConfig.PeerIdleTimeout = l.config.PeerIdleTimeout

	// Configure encryption if enabled
	if l.config.Encryption.Enabled {
		if err := l.configureEncryption(&srtConfig); err != nil {
			return fmt.Errorf("failed to configure SRT encryption: %w", err)
		}
	}

	addr := fmt.Sprintf("%s:%d", l.config.ListenAddr, l.config.Port)
	listener, err := srt.Listen("srt", addr, srtConfig)
	if err != nil {
		return fmt.Errorf("failed to start SRT listener: %w", err)
	}

	l.listener = listener
	l.logger.Infof("SRT listener started on %s", addr)

	// Start accept loop
	l.wg.Add(1)
	go l.acceptLoop()

	// Start monitoring loop
	l.wg.Add(1)
	go l.monitorConnections()

	return nil
}

// Stop stops the SRT listener
func (l *Listener) Stop() error {
	l.logger.Info("Stopping SRT listener")
	l.cancel()

	if l.listener != nil {
		l.listener.Close()
	}

	// Wait for all goroutines to finish
	l.wg.Wait()

	// Close all connections
	l.connections.Range(func(key, value interface{}) bool {
		conn := value.(*Connection)
		conn.Close()
		return true
	})

	l.logger.Info("SRT listener stopped")
	return nil
}

// configureEncryption sets up SRT encryption parameters
func (l *Listener) configureEncryption(cfg *srt.Config) error {
	// Validate passphrase length (minimum 10 characters as per SRT spec)
	if len(l.config.Encryption.Passphrase) < 10 {
		return fmt.Errorf("SRT encryption passphrase must be at least 10 characters long")
	}

	// Set passphrase
	cfg.Passphrase = l.config.Encryption.Passphrase

	// Set key length if specified
	if l.config.Encryption.KeyLength > 0 {
		switch l.config.Encryption.KeyLength {
		case 128, 192, 256:
			cfg.PBKeylen = l.config.Encryption.KeyLength
		default:
			return fmt.Errorf("invalid SRT encryption key length: %d (must be 128, 192, or 256)", l.config.Encryption.KeyLength)
		}
	}
	// If KeyLength is 0, let SRT auto-select based on passphrase length

	l.logger.WithFields(map[string]interface{}{
		"key_length": l.config.Encryption.KeyLength,
		"enabled":    true,
	}).Info("SRT encryption configured")

	return nil
}

func (l *Listener) acceptLoop() {
	defer l.wg.Done()

	for {
		req, err := l.listener.Accept2()
		if err != nil {
			select {
			case <-l.ctx.Done():
				return
			default:
				l.logger.Errorf("Failed to accept SRT connection: %v", err)
				continue
			}
		}

		// Handle connection request in a new goroutine
		l.wg.Add(1)
		go l.handleConnectionRequest(req)
	}
}

func (l *Listener) handleConnectionRequest(req srt.ConnRequest) {
	defer l.wg.Done()

	// Extract stream ID from connection request
	streamID := req.StreamId()
	if streamID == "" {
		l.logger.Warn("No stream ID provided in SRT connection, rejecting")
		req.Reject(srt.REJ_PEER)
		return
	}

	// Validate stream ID format
	if err := l.validateStreamID(streamID); err != nil {
		l.logger.Errorf("Invalid stream ID '%s': %v", streamID, err)
		req.Reject(srt.REJ_PEER)
		return
	}

	// Check connection limit
	if !l.connLimiter.TryAcquire(streamID) {
		l.logger.Warnf("Connection limit exceeded for stream %s", streamID)
		req.Reject(srt.REJ_RESOURCE)
		return
	}
	// Note: We'll release the connection slot in the main defer if connection fails

	// Allocate bandwidth (50 Mbps per stream)
	rateLimiter, ok := l.bandwidthManager.AllocateBandwidth(streamID, 50_000_000)
	if !ok {
		l.logger.Warnf("Insufficient bandwidth for stream %s", streamID)
		l.connLimiter.Release(streamID)
		req.Reject(srt.REJ_RESOURCE)
		return
	}

	// Accept the connection
	conn, err := req.Accept()
	if err != nil {
		l.logger.Errorf("Failed to accept connection: %v", err)
		return
	}
	defer conn.Close()

	l.logger.WithFields(map[string]interface{}{
		"stream_id": streamID,
		"remote":    conn.RemoteAddr().String(),
	}).Info("New SRT connection")

	// Detect codec from stream ID or use default
	videoCodec := l.detectCodecFromStreamID(streamID)

	// Register stream
	stream := &registry.Stream{
		ID:         streamID,
		Type:       registry.StreamTypeSRT,
		SourceAddr: conn.RemoteAddr().String(),
		Status:     registry.StatusConnecting,
		VideoCodec: videoCodec,
		Bitrate:    l.config.InputBandwidth,
	}

	if err := l.registry.Register(l.ctx, stream); err != nil {
		l.logger.Errorf("Failed to register stream %s: %v", streamID, err)
		return
	}
	defer l.registry.Unregister(l.ctx, streamID)

	// Update status to active
	if err := l.registry.UpdateStatus(l.ctx, streamID, registry.StatusActive); err != nil {
		l.logger.Warnf("Failed to update stream status: %v", err)
	}

	// Create connection wrapper
	now := time.Now()
	connection := &Connection{
		streamID:     streamID,
		conn:         conn,
		registry:     l.registry,
		logger:       l.logger,
		config:       l.config,
		startTime:    now,
		lastActive:   now,
		done:         make(chan struct{}),
		maxBandwidth: l.config.MaxBandwidth, // Initialize with configured max
	}

	// Set rate limiter
	connection.SetRateLimiter(rateLimiter)

	// Set test stats interval if configured
	if l.testStatsInterval > 0 {
		connection.SetStatsInterval(l.testStatsInterval)
	}

	// Store connection
	l.connections.Store(streamID, connection)
	defer func() {
		l.connections.Delete(streamID)
		l.connLimiter.Release(streamID)
		l.bandwidthManager.ReleaseBandwidth(streamID)
	}()

	// Use handler if available, otherwise use direct ReadLoop
	if l.handler != nil {
		// Handler manages the connection lifecycle
		if err := l.handler(connection); err != nil {
			l.logger.Errorf("Stream %s handler error: %v", streamID, err)
			l.registry.UpdateStatus(l.ctx, streamID, registry.StatusError)
		}
	} else {
		// Fallback to direct ReadLoop (old behavior)
		if err := connection.ReadLoop(l.ctx); err != nil {
			l.logger.Errorf("Stream %s read error: %v", streamID, err)
			l.registry.UpdateStatus(l.ctx, streamID, registry.StatusError)
		}
	}
}

func (l *Listener) validateStreamID(streamID string) error {
	// Stream ID validation rules:
	// - Must be 1-64 characters long
	// - Can contain alphanumeric characters, hyphens, and underscores
	// - Must start with a letter or number
	// - Cannot contain spaces or special characters

	if len(streamID) == 0 {
		return fmt.Errorf("stream ID cannot be empty")
	}

	if len(streamID) > 64 {
		return fmt.Errorf("stream ID too long (max 64 characters)")
	}

	// Check format with regex
	validPattern := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	if !validPattern.MatchString(streamID) {
		return fmt.Errorf("stream ID must start with alphanumeric and contain only alphanumeric, hyphen, or underscore characters")
	}

	return nil
}

func (l *Listener) monitorConnections() {
	defer l.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			activeCount := 0
			var toRemove []string

			l.connections.Range(func(key, value interface{}) bool {
				streamID := key.(string)
				conn := value.(*Connection)

				// Check if connection is dead (2x idle timeout for safety)
				if time.Since(conn.lastActive) > 2*l.config.PeerIdleTimeout {
					toRemove = append(toRemove, streamID)

					// Force close the connection
					conn.closeOnce.Do(func() {
						close(conn.done)
						conn.conn.Close()
					})

					l.logger.Warnf("Force closing dead connection: %s", streamID)
				} else if time.Since(conn.lastActive) > l.config.PeerIdleTimeout {
					// Normal idle timeout
					l.logger.Warnf("Connection %s idle for too long, closing", conn.streamID)
					conn.Close()
					toRemove = append(toRemove, streamID)
				} else {
					activeCount++
				}

				return true
			})

			// Remove dead connections
			for _, id := range toRemove {
				l.connections.Delete(id)
				l.connLimiter.Release(id)
				l.bandwidthManager.ReleaseBandwidth(id)
			}

			// Update active streams metric
			metrics.SetActiveStreams("srt", activeCount)

			l.logger.WithField("active_connections", activeCount).Debug("SRT connections status")
		}
	}
}

// GetActiveConnections returns the number of active connections
func (l *Listener) GetActiveConnections() int {
	count := 0
	l.connections.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// GetActiveSessions returns the number of active SRT sessions (alias for GetActiveConnections)
func (l *Listener) GetActiveSessions() int {
	return l.GetActiveConnections()
}

// TerminateStream terminates a specific stream connection
func (l *Listener) TerminateStream(streamID string) error {
	value, ok := l.connections.Load(streamID)
	if !ok {
		return fmt.Errorf("stream %s not found", streamID)
	}

	conn := value.(*Connection)
	conn.Close()

	// Remove from connections map
	l.connections.Delete(streamID)

	// Release resources
	l.connLimiter.Release(streamID)
	l.bandwidthManager.ReleaseBandwidth(streamID)

	l.logger.WithField("stream_id", streamID).Info("Stream terminated")
	return nil
}

// PauseStream pauses data ingestion for a stream
func (l *Listener) PauseStream(streamID string) error {
	value, ok := l.connections.Load(streamID)
	if !ok {
		return fmt.Errorf("stream %s not found", streamID)
	}

	conn := value.(*Connection)
	conn.Pause()

	l.logger.WithField("stream_id", streamID).Info("Stream paused")
	return nil
}

// ResumeStream resumes data ingestion for a paused stream
func (l *Listener) ResumeStream(streamID string) error {
	value, ok := l.connections.Load(streamID)
	if !ok {
		return fmt.Errorf("stream %s not found", streamID)
	}

	conn := value.(*Connection)
	conn.Resume()

	l.logger.WithField("stream_id", streamID).Info("Stream resumed")
	return nil
}

// detectCodecFromStreamID attempts to detect codec from stream ID format
// Format examples: "stream-name:codec=h264", "stream-name-h264", "stream-name"
func (l *Listener) detectCodecFromStreamID(streamID string) string {
	// First try explicit codec parameter
	if idx := strings.Index(streamID, ":codec="); idx != -1 {
		codecStr := streamID[idx+7:]
		codecType := codec.ParseType(codecStr)
		if codecType.IsValid() && l.isCodecSupported(codecType) {
			return codecType.String()
		}
	}

	// Try codec suffix
	streamLower := strings.ToLower(streamID)
	for _, supported := range l.codecsConfig.Supported {
		codecType := codec.ParseType(supported)
		suffix := "-" + strings.ToLower(codecType.String())
		if strings.HasSuffix(streamLower, suffix) && l.isCodecSupported(codecType) {
			return codecType.String()
		}
	}

	// Default to preferred codec
	return l.codecsConfig.Preferred
}

// isCodecSupported checks if a codec is in the supported list
func (l *Listener) isCodecSupported(codecType codec.Type) bool {
	for _, supported := range l.codecsConfig.Supported {
		if strings.EqualFold(supported, codecType.String()) {
			return true
		}
	}
	return false
}
