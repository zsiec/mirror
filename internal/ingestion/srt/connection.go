package srt

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/ingestion/ratelimit"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// Connection represents an active SRT connection using the adapter pattern
type Connection struct {
	streamID      string
	conn          SRTConnection
	registry      registry.Registry
	codecDetector *codec.Detector
	rateLimiter   ratelimit.RateLimiter
	logger        logger.Logger

	// Remote address information
	remoteAddr string

	startTime     time.Time
	lastActive    time.Time
	stats         ConnectionStats
	closed        int32
	paused        int32
	statsInterval time.Duration

	// Backpressure control
	maxBandwidth   int64 // Current max bandwidth setting
	backpressureMu sync.RWMutex

	// Cleanup synchronization
	done      chan struct{}
	closeOnce sync.Once
}

// NewConnectionWithSRTConn creates a new connection with an SRTConnection and remote address
func NewConnectionWithSRTConn(streamID string, conn SRTConnection, remoteAddr string, registry registry.Registry, codecDetector *codec.Detector, logger logger.Logger) *Connection {
	connection := &Connection{
		streamID:      streamID,
		conn:          conn,
		registry:      registry,
		codecDetector: codecDetector,
		logger:        logger.WithField("stream_id", streamID),
		remoteAddr:    remoteAddr,
		startTime:     time.Now(),
		lastActive:    time.Now(),
		statsInterval: 5 * time.Second,
		done:          make(chan struct{}),
	}

	// TODO: Register stream in registry (similar to RTP streams)
	// This needs to be implemented after fixing the SRT interface to provide remote address info
	_ = registry // Prevent unused variable warning

	return connection
}

// Read implements io.Reader interface
func (c *Connection) Read(b []byte) (int, error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return 0, io.EOF
	}

	n, err := c.conn.Read(b)
	if n > 0 {
		atomic.AddInt64(&c.stats.BytesReceived, int64(n))
		atomic.AddInt64(&c.stats.PacketsReceived, 1)
		c.lastActive = time.Now()
	}

	return n, err
}

// Write implements io.Writer interface
func (c *Connection) Write(b []byte) (int, error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return 0, io.EOF
	}

	n, err := c.conn.Write(b)
	if n > 0 {
		atomic.AddInt64(&c.stats.BytesSent, int64(n))
		atomic.AddInt64(&c.stats.PacketsSent, 1)
		c.lastActive = time.Now()
	}

	return n, err
}

// Close closes the connection
func (c *Connection) Close() error {
	c.closeOnce.Do(func() {
		atomic.StoreInt32(&c.closed, 1)

		// Update registry
		if c.registry != nil {
			c.registry.Unregister(context.Background(), c.streamID)
		}

		// Close underlying connection
		if c.conn != nil {
			c.conn.Close()
		}

		// Signal completion
		close(c.done)

		c.logger.Info("SRT connection closed")
	})

	return nil
}

// GetStreamID returns the stream ID
func (c *Connection) GetStreamID() string {
	return c.streamID
}

// GetRemoteAddr returns the remote address
func (c *Connection) GetRemoteAddr() string {
	return c.remoteAddr
}

// GetStartTime returns when the connection was established
func (c *Connection) GetStartTime() time.Time {
	return c.startTime
}

// GetStats returns current connection statistics
func (c *Connection) GetStats() ConnectionStats {
	// Get stats from underlying connection if available
	if c.conn != nil {
		connStats := c.conn.GetStats()
		return ConnectionStats{
			BytesReceived:    int64(connStats.BytesReceived),
			BytesSent:        int64(connStats.BytesSent),
			PacketsReceived:  int64(connStats.PacketsReceived),
			PacketsSent:      int64(connStats.PacketsSent),
			PacketsLost:      int64(connStats.PacketsLost),
			PacketsRetrans:   int64(connStats.PacketsRetrans),
			RTTMs:            connStats.RTTMs,
			BandwidthMbps:    connStats.BandwidthMbps,
			DeliveryDelayMs:  connStats.DeliveryDelayMs,
			ConnectionTimeMs: connStats.ConnectionTimeMs,
		}
	}

	// Fallback to local stats
	return ConnectionStats{
		BytesReceived:   atomic.LoadInt64(&c.stats.BytesReceived),
		BytesSent:       atomic.LoadInt64(&c.stats.BytesSent),
		PacketsReceived: atomic.LoadInt64(&c.stats.PacketsReceived),
		PacketsSent:     atomic.LoadInt64(&c.stats.PacketsSent),
	}
}

// SetRateLimiter sets the rate limiter for this connection
func (c *Connection) SetRateLimiter(limiter ratelimit.RateLimiter) {
	c.rateLimiter = limiter
}

// SetStatsInterval sets the stats update interval for testing
func (c *Connection) SetStatsInterval(interval time.Duration) {
	c.statsInterval = interval
}

// Pause pauses the connection
func (c *Connection) Pause() {
	atomic.StoreInt32(&c.paused, 1)
	c.logger.Info("SRT connection paused")
}

// Resume resumes the connection
func (c *Connection) Resume() {
	atomic.StoreInt32(&c.paused, 0)
	c.logger.Info("SRT connection resumed")
}

// IsPaused returns whether the connection is paused
func (c *Connection) IsPaused() bool {
	return atomic.LoadInt32(&c.paused) == 1
}

// IsClosed returns whether the connection is closed
func (c *Connection) IsClosed() bool {
	return atomic.LoadInt32(&c.closed) == 1
}

// ReadLoop reads data from the SRT connection and writes to the buffer
func (c *Connection) ReadLoop(ctx context.Context) error {
	// Create a child context that we control
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Ensure cleanup happens
	defer func() {
		c.closeOnce.Do(func() {
			close(c.done)
			if c.conn != nil {
				c.conn.Close()
			}

			// Log any cleanup errors
			if err := recover(); err != nil {
				c.logger.WithField("panic", err).Error("Panic during cleanup")
			}
		})
	}()

	readBuffer := make([]byte, 65536) // 64KB buffer for SRT messages

	// Stats ticker - use configurable interval or default to 5 seconds
	statsInterval := c.statsInterval
	if statsInterval == 0 {
		statsInterval = 5 * time.Second
	}
	statsTicker := time.NewTicker(statsInterval)
	defer statsTicker.Stop()

	// Heartbeat ticker
	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-connCtx.Done():
			return connCtx.Err()
		case <-c.done:
			return nil

		case <-statsTicker.C:
			c.updateStats()

		case <-heartbeatTicker.C:
			if c.registry != nil {
				if err := c.registry.UpdateHeartbeat(ctx, c.streamID); err != nil {
					c.logger.WithError(err).Warn("Failed to update heartbeat")
				}
			}

		default:
			// Check if paused
			if atomic.LoadInt32(&c.paused) == 1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			n, err := c.conn.Read(readBuffer)
			if err != nil {
				if err == io.EOF {
					c.logger.Info("SRT connection ended normally")
					return nil
				}

				// Check if connection is closed
				if atomic.LoadInt32(&c.closed) == 1 {
					return nil
				}

				c.logger.WithError(err).Error("SRT read error")
				return err
			}

			if n > 0 {
				// Update stats
				atomic.AddInt64(&c.stats.BytesReceived, int64(n))
				atomic.AddInt64(&c.stats.PacketsReceived, 1)
				c.lastActive = time.Now()

				// Process the data (this would integrate with the existing pipeline)
				c.logger.WithFields(map[string]interface{}{
					"bytes_read":  n,
					"total_bytes": atomic.LoadInt64(&c.stats.BytesReceived),
				}).Debug("SRT data received")
			}
		}
	}
}

// updateStats updates internal statistics
func (c *Connection) updateStats() {
	stats := c.GetStats()

	// Update metrics
	metrics.UpdateSRTBytesReceived(c.streamID, stats.BytesReceived)
	metrics.UpdateSRTBytesSent(c.streamID, stats.BytesSent)

	c.logger.WithFields(map[string]interface{}{
		"bytes_received":   stats.BytesReceived,
		"bytes_sent":       stats.BytesSent,
		"packets_received": stats.PacketsReceived,
		"packets_sent":     stats.PacketsSent,
		"rtt_ms":           stats.RTTMs,
		"bandwidth_mbps":   stats.BandwidthMbps,
	}).Debug("SRT connection stats")
}

// SetMaxBandwidth sets maximum bandwidth for backpressure control
func (c *Connection) SetMaxBandwidth(bw int64) error {
	c.backpressureMu.Lock()
	defer c.backpressureMu.Unlock()

	c.maxBandwidth = bw

	// Apply to underlying connection if supported
	if c.conn != nil {
		return c.conn.SetMaxBW(bw)
	}

	return nil
}

// GetMaxBandwidth returns the current maximum bandwidth setting
func (c *Connection) GetMaxBandwidth() int64 {
	c.backpressureMu.RLock()
	defer c.backpressureMu.RUnlock()

	if c.conn != nil {
		// Try to get from underlying SRT connection if it supports it
		if srtConn, ok := c.conn.(interface{ GetMaxBW() int64 }); ok {
			return srtConn.GetMaxBW()
		}
	}

	return c.maxBandwidth
}

// GetMaxBW is an alias for GetMaxBandwidth for compatibility
func (c *Connection) GetMaxBW() int64 {
	c.backpressureMu.RLock()
	defer c.backpressureMu.RUnlock()
	return c.maxBandwidth
}

// SetMaxBW is an alias for SetMaxBandwidth for compatibility
func (c *Connection) SetMaxBW(bw int64) error {
	return c.SetMaxBandwidth(bw)
}
