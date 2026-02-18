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

	startTime      time.Time
	lastActiveNano atomic.Int64 // stores time.UnixNano() for thread-safe access
	stats          ConnectionStats
	closed         int32
	paused         int32
	statsInterval  time.Duration

	// Delta tracking for Prometheus counters (stats are cumulative, metrics need deltas)
	lastReportedBytesRecv  int64
	lastReportedBytesSent  int64
	lastReportedPktsLost   int64
	lastReportedPktsDropd  int64
	lastReportedPktsRetran int64

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
		statsInterval: 5 * time.Second,
		done:          make(chan struct{}),
	}
	connection.lastActiveNano.Store(time.Now().UnixNano())

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
		c.lastActiveNano.Store(time.Now().UnixNano())
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
		c.lastActiveNano.Store(time.Now().UnixNano())
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

// GetLastActive returns the time of last activity on this connection
func (c *Connection) GetLastActive() time.Time {
	return time.Unix(0, c.lastActiveNano.Load())
}

// GetStats returns current connection statistics
func (c *Connection) GetStats() ConnectionStats {
	// Get stats from underlying connection if available
	if c.conn != nil {
		return c.conn.GetStats()
	}

	// Fallback to local stats
	return ConnectionStats{
		BytesReceived:   atomic.LoadInt64(&c.stats.BytesReceived),
		BytesSent:       atomic.LoadInt64(&c.stats.BytesSent),
		PacketsReceived: atomic.LoadInt64(&c.stats.PacketsReceived),
		PacketsSent:     atomic.LoadInt64(&c.stats.PacketsSent),
	}
}

// SetStatsInterval sets the stats update interval for testing
func (c *Connection) SetStatsInterval(interval time.Duration) {
	c.statsInterval = interval
}

// SetRateLimiter sets the rate limiter for the connection
func (c *Connection) SetRateLimiter(limiter ratelimit.RateLimiter) {
	c.rateLimiter = limiter
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

// readResult carries the result of a read operation from the reader goroutine
type readResult struct {
	n   int
	err error
}

// ReadLoop reads data from the SRT connection and writes to the buffer.
// Uses a dedicated reader goroutine to avoid busy-waiting and ticker starvation.
func (c *Connection) ReadLoop(ctx context.Context) error {
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Recover from panics and ensure cleanup via Close() which handles
	// registry unregistration, setting closed flag, and closing the connection.
	defer func() {
		if err := recover(); err != nil {
			c.logger.WithField("panic", err).Error("Panic during ReadLoop cleanup")
		}
		c.Close()
	}()

	readBuffer := make([]byte, 65536) // 64KB buffer for SRT messages

	statsInterval := c.statsInterval
	if statsInterval == 0 {
		statsInterval = 5 * time.Second
	}
	statsTicker := time.NewTicker(statsInterval)
	defer statsTicker.Stop()

	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()

	// Dedicated read goroutine to avoid busy-wait in select default case
	readCh := make(chan readResult, 1)
	go func() {
		for {
			if atomic.LoadInt32(&c.paused) == 1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			n, err := c.conn.Read(readBuffer)
			select {
			case readCh <- readResult{n: n, err: err}:
			case <-connCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

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

		case result := <-readCh:
			if result.err != nil {
				if result.err == io.EOF {
					c.logger.Info("SRT connection ended normally")
					return nil
				}
				if atomic.LoadInt32(&c.closed) == 1 {
					return nil
				}
				c.logger.WithError(result.err).Error("SRT read error")
				return result.err
			}

			if result.n > 0 {
				atomic.AddInt64(&c.stats.BytesReceived, int64(result.n))
				atomic.AddInt64(&c.stats.PacketsReceived, 1)
				c.lastActiveNano.Store(time.Now().UnixNano())

				c.logger.WithFields(map[string]interface{}{
					"bytes_read":  result.n,
					"total_bytes": atomic.LoadInt64(&c.stats.BytesReceived),
				}).Debug("SRT data received")
			}
		}
	}
}

// updateStats updates internal statistics and exports to Prometheus
func (c *Connection) updateStats() {
	stats := c.GetStats()

	// Stats are cumulative (Total fields); compute deltas for Prometheus counters
	recvDelta := stats.BytesReceived - c.lastReportedBytesRecv
	sentDelta := stats.BytesSent - c.lastReportedBytesSent
	if recvDelta > 0 {
		metrics.UpdateSRTBytesReceived(c.streamID, recvDelta)
	}
	if sentDelta > 0 {
		metrics.UpdateSRTBytesSent(c.streamID, sentDelta)
	}
	c.lastReportedBytesRecv = stats.BytesReceived
	c.lastReportedBytesSent = stats.BytesSent

	// Compute deltas for loss/drop/retransmission counters
	lostDelta := stats.PacketsReceiveLost - c.lastReportedPktsLost
	dropDelta := stats.PacketsDropped - c.lastReportedPktsDropd
	retransDelta := stats.PacketsRetrans - c.lastReportedPktsRetran
	c.lastReportedPktsLost = stats.PacketsReceiveLost
	c.lastReportedPktsDropd = stats.PacketsDropped
	c.lastReportedPktsRetran = stats.PacketsRetrans

	// Export all SRT-specific stats (gauges + counter deltas)
	metrics.UpdateSRTStats(
		c.streamID,
		stats.RTTMs,
		lostDelta,
		dropDelta,
		retransDelta,
		stats.PacketsFlightSize,
		stats.ReceiveRateMbps,
		stats.EstimatedLinkCapacityMbps,
		stats.AvailableRcvBuf,
	)

	c.logger.WithFields(map[string]interface{}{
		"bytes_received":   stats.BytesReceived,
		"bytes_sent":       stats.BytesSent,
		"packets_received": stats.PacketsReceived,
		"packets_sent":     stats.PacketsSent,
		"rtt_ms":           stats.RTTMs,
		"bandwidth_mbps":   stats.BandwidthMbps,
		"packets_lost":     stats.PacketsReceiveLost,
		"packets_dropped":  stats.PacketsDropped,
		"flight_size":      stats.PacketsFlightSize,
		"avail_rcv_buf":    stats.AvailableRcvBuf,
	}).Debug("SRT connection stats")
}

// SetMaxBandwidth sets maximum bandwidth for backpressure control.
// NOTE: SRTO_MAXBW only limits outbound (sending) bandwidth. For a receiver
// application, this has no effect on inbound data rate. True inbound
// backpressure requires application-level flow control (e.g., slowing reads
// or pausing the connection).
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
		return c.conn.GetMaxBW()
	}

	return c.maxBandwidth
}

// GetMaxBW is an alias for GetMaxBandwidth for compatibility
func (c *Connection) GetMaxBW() int64 {
	return c.GetMaxBandwidth()
}

// SetMaxBW is an alias for SetMaxBandwidth for compatibility
func (c *Connection) SetMaxBW(bw int64) error {
	return c.SetMaxBandwidth(bw)
}
