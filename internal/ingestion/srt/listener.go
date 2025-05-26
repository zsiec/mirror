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

// NewListener creates a new SRT listener using the adapter pattern
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

	// Create connection limiter (max 5 connections per stream, 100 total)
	connLimiter := ratelimit.NewConnectionLimiter(5, 100)

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

	// Start listening
	err = l.listener.Listen(l.ctx, 1)
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

	// Close listener
	if l.listener != nil {
		l.listener.Close()
	}

	// Wait for all goroutines to finish
	l.wg.Wait()

	l.logger.Info("SRT listener stopped")
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
		l.logger.WithFields(map[string]interface{}{
			"remote":    addr.String(),
			"stream_id": streamID,
		}).Warn("Stream already active")
		socket.SetRejectReason(RejectionReasonResourceUnavailable)
		return false
	}

	return true
}

// acceptLoop continuously accepts new connections
func (l *Listener) acceptLoop() {
	defer l.wg.Done()

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
			socket, addr, err := l.listener.Accept()
			if err != nil {
				if l.ctx.Err() != nil {
					// Context cancelled, normal shutdown
					return
				}
				l.logger.WithError(err).Error("Failed to accept SRT connection")
				continue
			}

			// Create connection wrapper
			srtConn, err := l.adapter.NewConnection(socket)
			if err != nil {
				l.logger.WithError(err).Error("Failed to create SRT connection wrapper")
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
			l.connections.Store(streamID, conn)

			// Handle the connection
			l.wg.Add(1)
			go l.handleConnection(conn, streamID)
		}
	}
}

// handleConnection processes a single SRT connection
func (l *Listener) handleConnection(conn *Connection, streamID string) {
	defer l.wg.Done()
	defer l.connections.Delete(streamID)

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

// isValidStreamID validates the stream ID format
func (l *Listener) isValidStreamID(streamID string) bool {
	if streamID == "" {
		return false
	}

	// Basic validation: alphanumeric, underscores, hyphens, max 64 chars
	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	return validPattern.MatchString(streamID)
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
