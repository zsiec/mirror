package rtp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/ingestion/ratelimit"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// CodecDetectionState represents the state of codec detection
type CodecDetectionState int32

const (
	CodecStateUnknown   CodecDetectionState = iota
	CodecStateDetecting                     // Detection in progress
	CodecStateDetected                      // Codec detected and depacketizer created
	CodecStateError                         // Detection failed
	CodecStateTimeout                       // Detection timed out
)

type Session struct {
	streamID        string
	remoteAddr      *net.UDPAddr
	ssrc            uint32
	registry        registry.Registry
	rateLimiter     ratelimit.RateLimiter
	logger          logger.Logger
	depacketizer    codec.Depacketizer
	codecType       codec.Type
	codecDetector   *codec.Detector
	codecFactory    *codec.DepacketizerFactory
	lastPacket      time.Time
	firstPacketTime time.Time
	stats           *SessionStats
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	mu              sync.RWMutex
	paused          int32
	timeout         time.Duration

	// Callback for sending depacketized NAL units to the adapter
	nalCallback func(nalUnits [][]byte) error

	// Codec detection results
	detectedClockRate uint32
	mediaFormat       string
	encodingName      string

	// Backpressure control
	rtcpCallback   func(nackCount int) // Callback for RTCP feedback
	backpressureMu sync.RWMutex

	// Codec detection state machine
	codecState      CodecDetectionState
	codecStateMu    sync.Mutex
	codecUpdateOnce sync.Once
	detectionCond   *sync.Cond // Condition variable for codec detection coordination
}

type SessionStats struct {
	PacketsReceived   uint64
	BytesReceived     uint64
	LastPayloadType   uint8
	PacketsLost       uint64
	RateLimitDrops    uint64
	BufferOverflows   uint64
	LastSequence      uint16
	InitialSequence   uint16
	Jitter            float64
	LastPacketTime    time.Time
	StartTime         time.Time
	LastBytesReceived uint64 // For delta calculation
	LastStatsTime     time.Time
}

func NewSession(streamID string, remoteAddr *net.UDPAddr, ssrc uint32,
	reg registry.Registry, codecsCfg *config.CodecsConfig, logger logger.Logger) (*Session, error) {

	ctx, cancel := context.WithCancel(context.Background())

	// Create codec detector and factory
	// TODO: Pass memory controller from listener/manager
	codecDetector := codec.NewDetector()
	codecFactory := codec.NewDepacketizerFactory(nil)

	session := &Session{
		streamID:      streamID,
		remoteAddr:    remoteAddr,
		ssrc:          ssrc,
		registry:      reg,
		logger:        logger.WithField("stream_id", streamID),
		codecDetector: codecDetector,
		codecFactory:  codecFactory,
		codecType:     codec.TypeUnknown, // Will be detected from first packet or SDP
		codecState:    CodecStateUnknown,
		lastPacket:    time.Now(),
		stats: &SessionStats{
			StartTime: time.Now(),
		},
		ctx:    ctx,
		cancel: cancel,
	}

	// Initialize condition variable for codec detection coordination
	session.detectionCond = sync.NewCond(&session.codecStateMu)

	// Register stream with unknown codec initially
	stream := &registry.Stream{
		ID:         streamID,
		Type:       registry.StreamTypeRTP,
		Status:     registry.StatusActive,
		SourceAddr: remoteAddr.String(),
		CreatedAt:  time.Now(),
		VideoCodec: codecsCfg.Preferred, // Use preferred codec as default
	}

	if err := reg.Register(ctx, stream); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to register stream: %w", err)
	}

	return session, nil
}

// SetRateLimiter sets the rate limiter for this session
func (s *Session) SetRateLimiter(limiter ratelimit.RateLimiter) {
	s.rateLimiter = limiter
}

// SetTimeout sets the session timeout duration
func (s *Session) SetTimeout(timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timeout = timeout
}

// SetNALCallback sets the callback for receiving depacketized NAL units
func (s *Session) SetNALCallback(callback func(nalUnits [][]byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger.Info("RTP Session: Setting NAL callback")
	s.nalCallback = callback
	s.logger.Info("RTP Session: NAL callback set successfully")
}

func (s *Session) Start() {
	// Start stats reporter
	s.wg.Add(1)
	go s.reportStats()
}

func (s *Session) Stop() {
	s.cancel()
	s.wg.Wait()

	// Unregister stream
	if err := s.registry.Unregister(s.ctx, s.streamID); err != nil {
		s.logger.WithError(err).Error("Failed to unregister stream")
	}

	s.logger.Info("RTP session stopped")
}

func (s *Session) updateStats(packet *rtp.Packet, size int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update basic stats
	atomic.AddUint64(&s.stats.PacketsReceived, 1)
	atomic.AddUint64(&s.stats.BytesReceived, uint64(size))
	s.stats.LastPacketTime = time.Now()

	// Check for packet loss
	if s.stats.PacketsReceived == 1 {
		s.stats.InitialSequence = packet.SequenceNumber
		s.stats.LastSequence = packet.SequenceNumber
	} else {
		expectedSeq := s.stats.LastSequence + 1
		if packet.SequenceNumber != expectedSeq {
			// Simple packet loss detection (doesn't handle wraparound)
			if packet.SequenceNumber > expectedSeq {
				lost := uint64(packet.SequenceNumber - expectedSeq)
				atomic.AddUint64(&s.stats.PacketsLost, lost)
			}
		}
		s.stats.LastSequence = packet.SequenceNumber
	}
}

// SetSDP processes SDP to detect codec and configure the session
func (s *Session) SetSDP(sdp string) error {
	// Detect codec from SDP (may involve parsing, no lock needed)
	codecType, codecInfo, err := s.codecDetector.DetectFromSDP(sdp)
	if err != nil {
		return fmt.Errorf("failed to detect codec from SDP: %w", err)
	}

	// Use atomic state machine to prevent races
	s.codecStateMu.Lock()

	// Check current state
	switch s.codecState {
	case CodecStateDetected:
		// Already detected - verify consistency
		s.codecStateMu.Unlock()
		if s.codecType != codecType {
			return fmt.Errorf("codec mismatch: SDP indicates %s but already detected %s", codecType, s.codecType)
		}
		return nil // Already configured

	case CodecStateDetecting:
		// Another goroutine is detecting - wait for completion
		for s.codecState == CodecStateDetecting {
			s.detectionCond.Wait()
		}
		// Check result after waiting
		if s.codecState == CodecStateDetected && s.codecType == codecType {
			s.codecStateMu.Unlock()
			return nil
		} else if s.codecState == CodecStateDetected {
			s.codecStateMu.Unlock()
			return fmt.Errorf("codec mismatch: SDP indicates %s but already detected %s", codecType, s.codecType)
		}
		// Fall through to try detection again if it failed

	case CodecStateError, CodecStateTimeout:
		// Previous detection failed - reset and try again
		s.codecState = CodecStateUnknown
	}

	// Transition to detecting state
	s.codecState = CodecStateDetecting
	s.codecStateMu.Unlock()

	// Create depacketizer for detected codec (outside lock)
	depacketizer, err := s.codecFactory.Create(codecType, s.streamID)

	if err != nil {
		s.codecStateMu.Lock()
		s.codecState = CodecStateError
		s.detectionCond.Broadcast()
		s.codecStateMu.Unlock()
		return fmt.Errorf("failed to create depacketizer for codec %s: %w", codecType, err)
	}

	// Update codec state atomically - acquire locks in proper order
	s.mu.Lock()
	s.codecType = codecType
	s.depacketizer = depacketizer
	s.mu.Unlock()

	s.codecStateMu.Lock()
	s.codecState = CodecStateDetected
	s.detectionCond.Broadcast()
	s.codecStateMu.Unlock()

	// Update stream in registry (do async to avoid blocking)
	s.codecUpdateOnce.Do(func() {
		go func() {
			stream, _ := s.registry.Get(s.ctx, s.streamID)
			if stream != nil {
				stream.VideoCodec = codecType.String()
				s.registry.Update(s.ctx, stream)
			}
		}()
	})

	s.logger.WithFields(map[string]interface{}{
		"codec":   codecType,
		"profile": codecInfo.Profile,
		"level":   codecInfo.Level,
		"width":   codecInfo.Width,
		"height":  codecInfo.Height,
		"fps":     codecInfo.FrameRate,
	}).Info("Configured session from SDP")

	return nil
}

// IsTimedOut checks if the session has timed out due to inactivity
func (s *Session) IsTimedOut() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.timeout <= 0 {
		return false // No timeout configured
	}

	return time.Since(s.lastPacket) > s.timeout
}

// GetLastPacketTime returns the time of the last received packet
func (s *Session) GetLastPacketTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastPacket
}

func (s *Session) reportStats() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			stats := *s.stats
			s.mu.RUnlock()

			now := time.Now()
			packetsReceived := atomic.LoadUint64(&stats.PacketsReceived)
			bytesReceived := atomic.LoadUint64(&stats.BytesReceived)
			packetsLost := atomic.LoadUint64(&stats.PacketsLost)

			// Calculate bitrate from delta
			bitrate := float64(0)
			if !stats.LastStatsTime.IsZero() {
				duration := now.Sub(stats.LastStatsTime).Seconds()
				if duration > 0 {
					// Handle counter reset or wrap-around
					if bytesReceived < stats.LastBytesReceived {
						// Counter reset detected - can't calculate accurate rate
						bitrate = 0
						s.logger.WithFields(map[string]interface{}{
							"current_bytes": bytesReceived,
							"last_bytes":    stats.LastBytesReceived,
						}).Warn("Detected counter reset in RTP statistics")
					} else {
						deltaBytes := bytesReceived - stats.LastBytesReceived
						bitrate = float64(deltaBytes*8) / duration
					}
				}
			}

			// Update last values for next calculation
			s.mu.Lock()
			s.stats.LastBytesReceived = bytesReceived
			s.stats.LastStatsTime = now
			s.mu.Unlock()

			// Update Prometheus metrics
			metrics.UpdateStreamMetrics(
				s.streamID,
				"rtp",
				int64(bytesReceived),
				int64(packetsReceived),
				int64(packetsLost),
				bitrate,
			)

			// Update jitter metric if available
			if stats.Jitter > 0 {
				metrics.SetRTPJitter(s.streamID, stats.Jitter*1000) // Convert to ms
			}

			// Calculate total duration for logging
			totalDuration := time.Since(stats.StartTime).Seconds()
			packetRate := float64(0)
			if totalDuration > 0 {
				packetRate = float64(packetsReceived) / totalDuration
			}

			s.logger.WithFields(map[string]interface{}{
				"packets_received": packetsReceived,
				"bytes_received":   bytesReceived,
				"packets_lost":     packetsLost,
				"bitrate_mbps":     bitrate / 1e6,
				"packet_rate":      packetRate,
			}).Info("RTP session statistics")

			// Update stream in registry with stats
			stream, err := s.registry.Get(s.ctx, s.streamID)
			if err != nil {
				s.logger.WithError(err).Error("Failed to get stream from registry")
				continue
			}

			// Update stream stats
			stream.PacketsReceived = int64(packetsReceived)
			stream.BytesReceived = int64(bytesReceived)
			stream.PacketsLost = int64(packetsLost)
			// Calculate bitrate in kbps
			bitrateKbps := int64(bitrate / 1000)
			_ = bitrateKbps // TODO: Add BitrateKbps field to Stream struct
			stream.LastHeartbeat = time.Now()

			// Update stream in registry by re-registering
			if err := s.registry.Register(s.ctx, stream); err != nil {
				s.logger.WithError(err).Error("Failed to update stream in registry")
			}
		}
	}
}

func (s *Session) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	timeout := s.timeout
	if timeout == 0 {
		timeout = 10 * time.Second // Default
	}
	return time.Since(s.lastPacket) < timeout
}

func (s *Session) GetStats() SessionStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.stats
}

// GetPayloadType returns the last seen RTP payload type
func (s *Session) GetPayloadType() uint8 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats.LastPayloadType
}

// GetMediaFormat returns the SDP media format if available
func (s *Session) GetMediaFormat() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mediaFormat
}

// GetEncodingName returns the encoding name if available from SDP
func (s *Session) GetEncodingName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.encodingName
}

// GetClockRate returns the RTP clock rate for this session
func (s *Session) GetClockRate() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return detected clock rate if available (from SDP)
	if s.detectedClockRate > 0 {
		return s.detectedClockRate
	}

	// Try to get clock rate from static payload type
	if clockRate := types.GetClockRateForPayloadType(s.stats.LastPayloadType); clockRate > 0 {
		return clockRate
	}

	// Default based on codec type if known
	if s.codecType != codec.TypeUnknown {
		switch s.codecType {
		case codec.TypeH264, codec.TypeHEVC, codec.TypeAV1, codec.TypeJPEGXS:
			return 90000 // Video codecs use 90kHz
		default:
			// For unknown codec types, guess based on payload type range
			if s.stats.LastPayloadType >= 96 && s.stats.LastPayloadType <= 127 {
				return 90000 // Dynamic payload types often used for video
			}
			return 8000 // Default audio clock rate
		}
	}

	return 90000 // Default video clock rate
}

// SetSDPInfo sets codec information from SDP parsing
func (s *Session) SetSDPInfo(mediaFormat, encodingName string, clockRate uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mediaFormat = mediaFormat
	s.encodingName = encodingName
	s.detectedClockRate = clockRate

	s.logger.WithFields(map[string]interface{}{
		"media_format":  mediaFormat,
		"encoding_name": encodingName,
		"clock_rate":    clockRate,
	}).Debug("Updated session with SDP info")
}

// ProcessRTCPPacket processes an RTCP packet received by the listener
func (s *Session) ProcessRTCPPacket(data []byte) {
	// Check if paused
	if atomic.LoadInt32(&s.paused) == 1 {
		return
	}

	// Update last packet time
	s.mu.Lock()
	s.lastPacket = time.Now()
	s.mu.Unlock()

	// Log RTCP packet (simplified for now - can be enhanced later)
	s.logger.WithField("size", len(data)).Debug("Received RTCP packet")

	// TODO: Parse and process RTCP packet for statistics, feedback, etc.
}

// handleCodecDetection attempts to detect codec and create depacketizer using atomic state machine
// Returns true if depacketizer is ready, false if packet should be skipped
func (s *Session) handleCodecDetection(packet *rtp.Packet) bool {
	// Fast path: check if already detected without acquiring state lock
	s.mu.RLock()
	if s.depacketizer != nil {
		s.mu.RUnlock()
		return true
	}
	s.mu.RUnlock()

	// Use atomic state machine for codec detection
	s.codecStateMu.Lock()

	switch s.codecState {
	case CodecStateDetected:
		s.codecStateMu.Unlock()
		return true

	case CodecStateDetecting:
		// Another goroutine is detecting - wait with timeout
		done := make(chan struct{})
		go func() {
			for s.codecState == CodecStateDetecting {
				s.detectionCond.Wait()
			}
			close(done)
		}()

		s.codecStateMu.Unlock()

		// Wait up to 1 second for detection to complete
		select {
		case <-done:
			s.codecStateMu.Lock()
			result := s.codecState == CodecStateDetected
			s.codecStateMu.Unlock()
			return result
		case <-time.After(time.Second):
			return false // Timeout waiting for detection
		}

	case CodecStateError, CodecStateTimeout:
		s.codecStateMu.Unlock()
		return false

	case CodecStateUnknown:
		// Check timeout first
		s.mu.Lock()
		if s.firstPacketTime.IsZero() {
			s.firstPacketTime = time.Now()
		} else if time.Since(s.firstPacketTime) > 10*time.Second {
			s.mu.Unlock()
			s.codecState = CodecStateTimeout
			s.detectionCond.Broadcast()
			s.codecStateMu.Unlock()

			s.logger.Error("Failed to detect codec within timeout period")
			go func() {
				s.registry.UpdateStatus(s.ctx, s.streamID, registry.StatusError)
				atomic.StoreInt32(&s.paused, 1)
			}()
			return false
		}
		s.mu.Unlock()

		// Transition to detecting state
		s.codecState = CodecStateDetecting
		s.codecStateMu.Unlock()

		// Perform detection outside of locks
		detectedType, err := s.codecDetector.DetectFromRTPPacket(packet)

		s.codecStateMu.Lock()
		if err != nil || detectedType == codec.TypeUnknown {
			// Detection failed - return to unknown state
			s.codecState = CodecStateUnknown
			s.detectionCond.Broadcast()
			s.codecStateMu.Unlock()

			// Log once per second to avoid spam
			s.mu.RLock()
			lastPacket := s.lastPacket
			s.mu.RUnlock()
			if time.Since(lastPacket) > time.Second {
				s.logger.Warn("Dropping RTP packets: codec not detected yet")
			}
			return false
		}

		// Create depacketizer
		depacketizer, err := s.codecFactory.Create(detectedType, s.streamID)
		if err != nil {
			s.codecState = CodecStateError
			s.detectionCond.Broadcast()
			s.codecStateMu.Unlock()

			s.logger.WithError(err).Errorf("Failed to create depacketizer for codec %s", detectedType)
			go func() {
				s.registry.UpdateStatus(s.ctx, s.streamID, registry.StatusError)
			}()
			return false
		}

		// Release codec state lock before acquiring main lock to avoid deadlock
		s.codecStateMu.Unlock()

		// Atomically update codec state
		s.mu.Lock()
		s.codecType = detectedType
		s.depacketizer = depacketizer
		s.mu.Unlock()

		// Re-acquire codec state lock to update state
		s.codecStateMu.Lock()
		s.codecState = CodecStateDetected
		s.detectionCond.Broadcast()
		s.codecStateMu.Unlock()

		// Update stream codec in registry (async)
		s.codecUpdateOnce.Do(func() {
			go func() {
				stream, _ := s.registry.Get(s.ctx, s.streamID)
				if stream != nil {
					stream.VideoCodec = detectedType.String()
					s.registry.Update(s.ctx, stream)
				}
			}()
		})

		s.logger.WithField("codec", detectedType).Info("Detected video codec")
		return true

	default:
		s.codecStateMu.Unlock()
		return false
	}
}

// getCodecState safely returns the current codec detection state
func (s *Session) getCodecState() CodecDetectionState {
	s.codecStateMu.Lock()
	defer s.codecStateMu.Unlock()
	return s.codecState
}

func (s *Session) ProcessPacket(packet *rtp.Packet) {
	s.logger.WithFields(map[string]interface{}{
		"payload_type": packet.PayloadType,
		"ssrc":         packet.SSRC,
		"sequence":     packet.SequenceNumber,
		"timestamp":    packet.Timestamp,
		"payload_size": len(packet.Payload),
	}).Debug("RTP ProcessPacket: Entry")

	// Check if paused
	if atomic.LoadInt32(&s.paused) == 1 {
		s.logger.Debug("RTP ProcessPacket: Session paused, dropping packet")
		return
	}

	// Apply rate limiting if configured
	packetSize := len(packet.Payload) + 12 // RTP header is 12 bytes
	if s.rateLimiter != nil {
		if err := s.rateLimiter.AllowN(s.ctx, packetSize); err != nil {
			atomic.AddUint64(&s.stats.RateLimitDrops, 1)
			s.logger.WithError(err).Debug("RTP ProcessPacket: Rate limit exceeded")
			return
		}
	}

	// Handle codec detection first (without holding main mutex)
	s.logger.Debug("RTP ProcessPacket: About to handle codec detection")
	if !s.handleCodecDetection(packet) {
		s.logger.Debug("RTP ProcessPacket: Codec detection failed, dropping packet")
		return
	}

	s.logger.WithField("codec_type", s.codecType).Debug("RTP ProcessPacket: Codec detection successful")

	// Now acquire lock for stats and processing
	s.mu.Lock()

	s.logger.Debug("RTP ProcessPacket: Acquired lock, updating stats")

	// Update last packet time
	s.lastPacket = time.Now()

	// Update statistics
	s.stats.PacketsReceived++
	s.stats.BytesReceived += uint64(len(packet.Payload))
	s.stats.LastPacketTime = s.lastPacket
	s.stats.LastPayloadType = packet.PayloadType

	// Check for packet loss
	if s.stats.LastSequence != 0 {
		expectedSeq := s.stats.LastSequence + 1
		if packet.SequenceNumber != expectedSeq {
			// Calculate lost packets
			if packet.SequenceNumber > expectedSeq {
				lost := uint64(packet.SequenceNumber - expectedSeq)
				atomic.AddUint64(&s.stats.PacketsLost, lost)
			}
		}
	} else {
		// First packet
		s.stats.InitialSequence = packet.SequenceNumber
	}
	s.stats.LastSequence = packet.SequenceNumber

	// Get depacketizer and callback safely
	depacketizer := s.depacketizer
	nalCallback := s.nalCallback
	if depacketizer == nil {
		s.mu.Unlock()
		s.logger.Debug("RTP ProcessPacket: No depacketizer available")
		return
	}

	// Release lock before depacketization to avoid blocking other packets
	s.mu.Unlock()

	// Depacketize RTP payload (outside lock)
	nalUnits, err := depacketizer.Depacketize(packet)
	if err != nil {
		s.logger.WithError(err).Debug("RTP ProcessPacket: Failed to depacketize RTP payload")
		return
	}

	s.logger.WithField("nal_units_count", len(nalUnits)).Debug("RTP ProcessPacket: Depacketization successful")

	// Send NAL units to callback if set (callback was captured earlier while holding lock)
	if nalCallback != nil && len(nalUnits) > 0 {
		s.logger.WithField("nal_units_count", len(nalUnits)).Info("RTP Session: About to call NAL callback")
		if err := nalCallback(nalUnits); err != nil {
			s.logger.WithError(err).Error("RTP ProcessPacket: NAL callback failed")
		} else {
			s.logger.WithField("nal_units_sent", len(nalUnits)).Info("RTP ProcessPacket: NAL units sent to callback successfully")
		}
	} else if nalCallback == nil {
		s.logger.Debug("RTP ProcessPacket: No NAL callback set")
	} else {
		s.logger.Debug("RTP ProcessPacket: No NAL units to send")
	}
}

// ProcessRTCP handles RTCP packets for this session
func (s *Session) ProcessRTCP(data []byte) {
	// For now, just log that we received RTCP
	// In a full implementation, we would parse SR/RR packets and send feedback
	s.logger.WithField("size", len(data)).Debug("Received RTCP packet")

	// TODO: Parse RTCP packets and extract useful information:
	// - Sender Reports (SR) for synchronization
	// - Receiver Reports (RR) for feedback
	// - Source Description (SDES) for metadata
	// - Feedback messages for congestion control
}

// Pause pauses data ingestion for this session
func (s *Session) Pause() {
	atomic.StoreInt32(&s.paused, 1)
	s.logger.Debug("Session paused")
}

// Resume resumes data ingestion for this session
func (s *Session) Resume() {
	atomic.StoreInt32(&s.paused, 0)
	s.logger.Debug("Session resumed")
}

// SetRTCPCallback sets the callback for RTCP feedback
func (s *Session) SetRTCPCallback(callback func(nackCount int)) {
	s.backpressureMu.Lock()
	defer s.backpressureMu.Unlock()
	s.rtcpCallback = callback
}

// SendRTCPFeedback sends RTCP feedback (NACK)
func (s *Session) SendRTCPFeedback(nackCount int) error {
	s.backpressureMu.RLock()
	callback := s.rtcpCallback
	s.backpressureMu.RUnlock()

	if callback != nil {
		callback(nackCount)
	}

	// In a real implementation, we would send actual RTCP NACK packets here
	s.logger.WithFields(map[string]interface{}{
		"stream_id":  s.streamID,
		"nack_count": nackCount,
	}).Debug("Sending RTCP feedback")

	return nil
}

// GetStreamID returns the stream ID
func (s *Session) GetStreamID() string {
	return s.streamID
}

// GetProtocol returns the protocol type
func (s *Session) GetProtocol() string {
	return "rtp"
}

// GetBitrate returns the current bitrate in bits per second
func (s *Session) GetBitrate() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Calculate bitrate from stats
	now := time.Now()
	duration := now.Sub(s.firstPacketTime).Seconds()
	if duration <= 0 {
		return 0
	}

	bytesReceived := atomic.LoadUint64(&s.stats.BytesReceived)
	return int64(float64(bytesReceived*8) / duration)
}

// GetSSRC returns the SSRC of the session
func (s *Session) GetSSRC() uint32 {
	return s.ssrc
}

// SendRTCP sends an RTCP packet
func (s *Session) SendRTCP(packet rtcp.Packet) error {
	// TODO: Implement actual RTCP packet sending
	// This would require access to the UDP connection
	s.logger.WithField("type", packet.DestinationSSRC()).Debug("Would send RTCP packet")
	return nil
}

// Read implements io.Reader interface for StreamConnection compatibility
func (s *Session) Read(p []byte) (n int, err error) {
	// RTP doesn't use a traditional read interface
	// This is here for interface compatibility
	return 0, errors.New("RTP session does not support direct reading")
}

// Close closes the RTP session
func (s *Session) Close() error {
	s.Stop()
	return nil
}
