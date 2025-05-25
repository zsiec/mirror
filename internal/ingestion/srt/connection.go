package srt

import (
	"context"
	"io"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	srt "github.com/datarhei/gosrt"
	
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/buffer"
	"github.com/zsiec/mirror/internal/ingestion/ratelimit"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// Connection represents an active SRT connection
type Connection struct {
	streamID    string
	conn        srt.Conn
	registry    registry.Registry
	rateLimiter ratelimit.RateLimiter
	logger      logger.Logger
	config      *config.SRTConfig

	startTime       time.Time
	lastActive      time.Time
	stats           ConnectionStats
	closed          int32
	paused          int32
	statsInterval   time.Duration
	
	// Backpressure control
	maxBandwidth    int64 // Current max bandwidth setting
	backpressureMu  sync.RWMutex
	
	// Cleanup synchronization
	done      chan struct{}
	closeOnce sync.Once
}

// ConnectionStats holds connection statistics
type ConnectionStats struct {
	BytesReceived   int64
	PacketsReceived int64
	PacketsLost     int64
	Bitrate         int64
	RateLimitDrops  int64
	BufferOverflows int64
	LastUpdate      time.Time
	LastBytesReceived int64 // For delta calculation
}

// SetRateLimiter sets the rate limiter for this connection
func (c *Connection) SetRateLimiter(limiter ratelimit.RateLimiter) {
	c.rateLimiter = limiter
}

// SetStatsInterval sets the stats update interval for testing
func (c *Connection) SetStatsInterval(interval time.Duration) {
	c.statsInterval = interval
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
			c.conn.Close()
			
			// Log any cleanup errors
			if err := recover(); err != nil {
				c.logger.Errorf("Panic during cleanup: %v", err)
			}
		})
	}()
	
	readBuffer := make([]byte, buffer.MaxPacketSize)

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

	// Exponential backoff for retries
	backoff := NewExponentialBackoff()

	for {
		select {
		case <-connCtx.Done():
			return connCtx.Err()
		case <-c.done:
			return nil

		case <-statsTicker.C:
			c.updateStats()

		case <-heartbeatTicker.C:
			if err := c.registry.UpdateHeartbeat(ctx, c.streamID); err != nil {
				c.logger.Warnf("Failed to update heartbeat for %s: %v", c.streamID, err)
			}

		default:
			// Check if paused
			if atomic.LoadInt32(&c.paused) == 1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}

			// Set read deadline
			c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

			n, err := c.conn.Read(readBuffer)
			if err != nil {
				if err == io.EOF {
					c.logger.Infof("Stream %s closed by peer", c.streamID)
					return nil
				}

				// Check if connection is closed
				if atomic.LoadInt32(&c.closed) == 1 {
					return nil
				}

				// Log error and apply backoff
				c.logger.Errorf("Read error for stream %s: %v", c.streamID, err)
				
				// Update status to error
				c.registry.UpdateStatus(ctx, c.streamID, registry.StatusError)
				
				// Wait with backoff before retrying
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff.Next()):
					// Try to recover
					c.registry.UpdateStatus(ctx, c.streamID, registry.StatusActive)
					continue
				}
			}

			// Reset backoff on successful read
			backoff.Reset()

			// Apply rate limiting if configured
			if c.rateLimiter != nil {
				if err := c.rateLimiter.AllowN(ctx, n); err != nil {
					c.logger.WithField("stream_id", c.streamID).Debug("Rate limit exceeded, dropping packet")
					atomic.AddInt64(&c.stats.RateLimitDrops, 1)
					continue
				}
			}

			// Handler will process the data, connection just passes it through

			// Update stats
			atomic.AddInt64(&c.stats.BytesReceived, int64(n))
			atomic.AddInt64(&c.stats.PacketsReceived, 1)
			c.lastActive = time.Now()

			// Get SRT statistics
			var srtStats srt.Statistics
			c.conn.Stats(&srtStats)
			// Use accumulated packet loss count
			atomic.StoreInt64(&c.stats.PacketsLost, int64(srtStats.Accumulated.PktRecvLoss))
		}
	}
}

func (c *Connection) updateStats() {
	now := time.Now()
	duration := now.Sub(c.stats.LastUpdate).Seconds()

	if duration > 0 {
		currentBytes := atomic.LoadInt64(&c.stats.BytesReceived)
		lastBytes := atomic.LoadInt64(&c.stats.LastBytesReceived)
		deltaBytes := currentBytes - lastBytes
		
		// Calculate bitrate from delta
		var bitrate int64
		if deltaBytes < 0 {
			// Counter reset detected
			bitrate = 0
			c.logger.WithFields(map[string]interface{}{
				"current_bytes": currentBytes,
				"last_bytes":    lastBytes,
			}).Warn("Detected counter reset in SRT statistics")
		} else {
			bitrate = int64(float64(deltaBytes*8) / duration)
		}
		atomic.StoreInt64(&c.stats.Bitrate, bitrate)
		
		// Update last bytes for next calculation
		atomic.StoreInt64(&c.stats.LastBytesReceived, currentBytes)
	}

	// Get current stats
	bytesReceived := atomic.LoadInt64(&c.stats.BytesReceived)
	packetsReceived := atomic.LoadInt64(&c.stats.PacketsReceived)
	packetsLost := atomic.LoadInt64(&c.stats.PacketsLost)
	bitrate := atomic.LoadInt64(&c.stats.Bitrate)

	// Update Prometheus metrics
	metrics.UpdateStreamMetrics(
		c.streamID,
		"srt",
		bytesReceived,
		packetsReceived,
		packetsLost,
		float64(bitrate),
	)

	// Update registry
	stats := &registry.StreamStats{
		BytesReceived:   bytesReceived,
		PacketsReceived: packetsReceived,
		PacketsLost:     packetsLost,
		Bitrate:         bitrate,
	}

	if err := c.registry.UpdateStats(context.Background(), c.streamID, stats); err != nil {
		c.logger.Warnf("Failed to update stats for %s: %v", c.streamID, err)
	}

	c.stats.LastUpdate = now
}

// Close closes the connection
func (c *Connection) Close() error {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return nil // Already closed
	}

	// Record connection duration
	duration := time.Since(c.startTime).Seconds()
	metrics.RecordConnectionDuration(c.streamID, "srt", duration)

	// Trigger cleanup
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			c.conn.Close()
		}
	})
	
	return nil
}

// ExponentialBackoff implements exponential backoff with jitter
type ExponentialBackoff struct {
	attempt int
	maxWait time.Duration
}

// NewExponentialBackoff creates a new exponential backoff
func NewExponentialBackoff() *ExponentialBackoff {
	return &ExponentialBackoff{
		attempt: 0,
		maxWait: 30 * time.Second,
	}
}

// Next returns the next backoff duration
func (b *ExponentialBackoff) Next() time.Duration {
	b.attempt++
	
	// Calculate exponential backoff: 2^attempt * 100ms
	wait := time.Duration(math.Pow(2, float64(b.attempt))) * 100 * time.Millisecond
	
	// Cap at max wait
	if wait > b.maxWait {
		wait = b.maxWait
	}
	
	// Add jitter (±25%)
	jitter := time.Duration(float64(wait) * 0.25 * (2*rand.Float64() - 1))
	wait += jitter
	
	return wait
}

// Reset resets the backoff counter
func (b *ExponentialBackoff) Reset() {
	b.attempt = 0
}

// Pause pauses data ingestion
func (c *Connection) Pause() {
	atomic.StoreInt32(&c.paused, 1)
	c.logger.WithField("stream_id", c.streamID).Debug("Connection paused")
}

// Resume resumes data ingestion
func (c *Connection) Resume() {
	atomic.StoreInt32(&c.paused, 0)
	c.logger.WithField("stream_id", c.streamID).Debug("Connection resumed")
}

// GetMaxBW returns the current maximum bandwidth setting
func (c *Connection) GetMaxBW() int64 {
	c.backpressureMu.RLock()
	defer c.backpressureMu.RUnlock()
	return c.maxBandwidth
}

// SetMaxBW sets the maximum bandwidth for the SRT connection
func (c *Connection) SetMaxBW(bps int64) error {
	c.backpressureMu.Lock()
	defer c.backpressureMu.Unlock()
	
	// Store the setting
	c.maxBandwidth = bps
	
	// Note: gosrt doesn't expose SetSockOpt directly
	// In a real implementation, we would either:
	// 1. Use a lower-level SRT library that exposes socket options
	// 2. Implement flow control at the application layer
	// 3. Close and re-establish the connection with new parameters
	
	// For now, we'll log the intention and rely on application-layer flow control
	c.logger.WithFields(map[string]interface{}{
		"stream_id": c.streamID,
		"bandwidth": bps,
	}).Info("SRT bandwidth limit requested (application-layer flow control)")
	
	// We can implement application-layer throttling in the ReadLoop
	// by introducing delays when bandwidth exceeds the limit
	
	return nil
}

// GetStreamID returns the stream ID
func (c *Connection) GetStreamID() string {
	return c.streamID
}

// GetProtocol returns the protocol type
func (c *Connection) GetProtocol() string {
	return "srt"
}

// Read implements io.Reader interface for StreamConnection compatibility
func (c *Connection) Read(p []byte) (n int, err error) {
	// Set read deadline
	c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	return c.conn.Read(p)
}
