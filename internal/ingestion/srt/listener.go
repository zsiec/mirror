package srt

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/ingestion/ratelimit"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// validStreamIDRegex matches printable ASCII characters, up to 512 bytes.
// Supports Haivision SRT access control syntax (#!::key=value,key2=value2).
var validStreamIDRegex = regexp.MustCompile(`^[\x20-\x7E]{1,512}$`)

// Listener manages SRT connections using the adapter pattern
type Listener struct {
	config           *config.SRTConfig
	codecsConfig     *config.CodecsConfig
	adapter          SRTAdapter
	listener         SRTListener
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

// NewListenerWithAdapter creates a new SRT listener with the specified adapter
func NewListenerWithAdapter(cfg *config.SRTConfig, codecsCfg *config.CodecsConfig, reg registry.Registry, adapter SRTAdapter, logger logger.Logger) *Listener {
	ctx, cancel := context.WithCancel(context.Background())

	// Derive connection limits from config
	maxTotal := cfg.MaxConnections
	if maxTotal <= 0 {
		maxTotal = 100 // fallback default
	}
	connLimiter := ratelimit.NewConnectionLimiter(5, maxTotal)

	// Create bandwidth manager (total 1 Gbps)
	bandwidthManager := ratelimit.NewBandwidthManager(1_000_000_000) // 1 Gbps

	// Create codec detector
	codecDetector := codec.NewDetector()

	return &Listener{
		config:           cfg,
		codecsConfig:     codecsCfg,
		adapter:          adapter,
		registry:         reg,
		connLimiter:      connLimiter,
		bandwidthManager: bandwidthManager,
		codecDetector:    codecDetector,
		logger:           logger,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start begins listening for SRT connections
func (l *Listener) Start() error {
	// Convert config to adapter config
	adapterConfig := Config{
		Address:           l.config.ListenAddr,
		Port:              l.config.Port,
		Latency:           l.config.Latency,
		MaxBandwidth:      l.config.MaxBandwidth,
		InputBandwidth:    l.config.InputBandwidth,
		PayloadSize:       l.config.PayloadSize,
		FlowControlWindow: l.config.FlowControlWindow,
		PeerIdleTimeout:   l.config.PeerIdleTimeout,
		MaxConnections:    l.config.MaxConnections,
		Encryption: EncryptionConfig{
			Enabled:         l.config.Encryption.Enabled,
			Passphrase:      l.config.Encryption.Passphrase,
			KeyLength:       l.config.Encryption.KeyLength,
			PBKDFIterations: l.config.Encryption.PBKDFIterations,
		},
	}

	l.logger.WithFields(map[string]interface{}{
		"latency":           adapterConfig.Latency,
		"max_bandwidth":     adapterConfig.MaxBandwidth,
		"peer_idle_timeout": adapterConfig.PeerIdleTimeout,
		"payload_size":      adapterConfig.PayloadSize,
		"flow_control":      adapterConfig.FlowControlWindow,
		"input_bandwidth":   adapterConfig.InputBandwidth,
	}).Info("SRT listener configured with Haivision official bindings")

	// Create listener using adapter
	listener, err := l.adapter.NewListener(adapterConfig.Address, adapterConfig.Port, adapterConfig)
	if err != nil {
		return fmt.Errorf("failed to create SRT listener: %w", err)
	}
	l.listener = listener

	// Set listen callback for connection filtering
	err = l.listener.SetListenCallback(l.handleIncomingConnection)
	if err != nil {
		return fmt.Errorf("failed to set listen callback: %w", err)
	}

	// Start listening — backlog should accommodate burst connection attempts.
	// Use MaxConnections as a reasonable upper bound; SRT internally caps this.
	backlog := l.config.MaxConnections
	if backlog <= 0 {
		backlog = 128 // safe default for production
	}
	err = l.listener.Listen(l.ctx, backlog)
	if err != nil {
		return fmt.Errorf("failed to start SRT listener: %w", err)
	}

	l.logger.WithField("port", adapterConfig.Port).Info("SRT listener started successfully")

	// Start accept loop
	l.wg.Add(1)
	go l.acceptLoop()

	return nil
}

// Stop stops the SRT listener
func (l *Listener) Stop() error {
	l.cancel()

	// Close listener first to stop accepting new connections
	if l.listener != nil {
		l.listener.Close()
	}

	// Wait for all goroutines with a timeout to prevent shutdown hangs.
	// Connection goroutines may be blocked in srtgo Read() even after
	// context cancellation; the listener close above should unblock them,
	// but we bound the wait as a safety net.
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		l.logger.Info("SRT listener stopped")
	case <-time.After(10 * time.Second):
		l.logger.Warn("SRT listener stop timed out after 10s, some goroutines may still be running")
	}

	return nil
}

// SetHandler sets the connection handler
func (l *Listener) SetHandler(handler ConnectionHandler) {
	l.handler = handler
}

// handleIncomingConnection filters incoming SRT connections
func (l *Listener) handleIncomingConnection(socket SRTSocket, version int, addr *net.UDPAddr, streamID string) bool {
	l.logger.WithFields(map[string]interface{}{
		"remote":    addr.String(),
		"stream_id": streamID,
		"version":   version,
	}).Info("New SRT connection request")

	// Validate stream ID
	if !l.isValidStreamID(streamID) {
		l.logger.WithFields(map[string]interface{}{
			"remote":    addr.String(),
			"stream_id": streamID,
		}).Warn("Invalid stream ID format")
		socket.SetRejectReason(RejectionReasonBadRequest)
		return false
	}

	// Check connection limits
	if !l.connLimiter.TryAcquire(streamID) {
		l.logger.WithFields(map[string]interface{}{
			"remote":    addr.String(),
			"stream_id": streamID,
		}).Warn("Connection limit exceeded")
		socket.SetRejectReason(RejectionReasonResourceUnavailable)
		return false
	}

	// Check if stream already exists
	if _, exists := l.connections.Load(streamID); exists {
		l.connLimiter.Release(streamID) // Release the slot we just acquired
		l.logger.WithFields(map[string]interface{}{
			"remote":    addr.String(),
			"stream_id": streamID,
		}).Warn("Stream already active")
		socket.SetRejectReason(RejectionReasonResourceUnavailable)
		return false
	}

	return true
}

// acceptLoop continuously accepts new connections with error backoff
func (l *Listener) acceptLoop() {
	defer l.wg.Done()

	const (
		initialBackoff = 100 * time.Millisecond
		maxBackoff     = 5 * time.Second
	)
	backoff := initialBackoff

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
		}

		socket, addr, err := l.listener.Accept()
		if err != nil {
			if l.ctx.Err() != nil {
				return
			}
			l.logger.WithError(err).WithField("backoff", backoff).Error("Failed to accept SRT connection")

			// Backoff on error to avoid busy-spin
			select {
			case <-l.ctx.Done():
				return
			case <-time.After(backoff):
			}
			// Exponential backoff with cap
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Reset backoff on successful accept
		backoff = initialBackoff

		// Create connection wrapper
		srtConn, err := l.adapter.NewConnection(socket)
		if err != nil {
			l.logger.WithError(err).Error("Failed to create SRT connection wrapper")
			l.connLimiter.Release(socket.GetStreamID()) // Release slot acquired in handleIncomingConnection
			socket.Close()
			continue
		}

		streamID := socket.GetStreamID()

		l.logger.WithFields(map[string]interface{}{
			"remote":    addr.String(),
			"stream_id": streamID,
		}).Info("SRT connection accepted")

		// Create connection object
		conn := NewConnectionWithSRTConn(streamID, srtConn, addr.String(), l.registry, l.codecDetector, l.logger)
		if _, loaded := l.connections.LoadOrStore(streamID, conn); loaded {
			// Another connection with same stream ID was accepted between
			// handleIncomingConnection check and here - reject this one
			l.logger.WithField("stream_id", streamID).Warn("Duplicate stream ID detected during accept, closing connection")
			l.connLimiter.Release(streamID)
			conn.Close()
			continue
		}

		// Handle the connection
		l.wg.Add(1)
		go l.handleConnection(conn, streamID)
	}
}

// handleConnection processes a single SRT connection
func (l *Listener) handleConnection(conn *Connection, streamID string) {
	defer l.wg.Done()
	defer l.connections.Delete(streamID)
	defer l.connLimiter.Release(streamID)

	// Update metrics
	metrics.IncrementSRTConnections()
	defer metrics.DecrementSRTConnections()

	// Call handler if set
	if l.handler != nil {
		if err := l.handler(conn); err != nil {
			l.logger.WithError(err).WithField("stream_id", streamID).Error("Connection handler failed")
		}
	}

	l.logger.WithField("stream_id", streamID).Info("SRT connection finished")
}

// isValidStreamID validates the stream ID format.
// Per SRT spec, stream IDs support UTF-8 up to 512 bytes.
// We accept printable ASCII to support Haivision access control syntax (#!::).
func (l *Listener) isValidStreamID(streamID string) bool {
	if streamID == "" {
		return false
	}
	return validStreamIDRegex.MatchString(streamID)
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

// GetConnectionInfo returns information about active connections
func (l *Listener) GetConnectionInfo() map[string]interface{} {
	connections := make(map[string]interface{})
	l.connections.Range(func(key, value interface{}) bool {
		streamID := key.(string)
		conn := value.(*Connection)

		connections[streamID] = map[string]interface{}{
			"stream_id":  streamID,
			"start_time": conn.GetStartTime(),
			"stats":      conn.GetStats(),
		}
		return true
	})

	return map[string]interface{}{
		"active_count": len(connections),
		"connections":  connections,
	}
}
