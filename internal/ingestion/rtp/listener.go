package rtp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/ratelimit"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SessionHandler is a function that handles new RTP sessions
type SessionHandler func(*Session) error

// Listener handles RTP stream ingestion
type Listener struct {
	config           *config.RTPConfig
	codecsConfig     *config.CodecsConfig
	rtpConn          *net.UDPConn
	rtcpConn         *net.UDPConn
	registry         registry.Registry
	connLimiter      *ratelimit.ConnectionLimiter
	bandwidthManager *ratelimit.BandwidthManager
	validator        *Validator
	logger           logger.Logger
	handler          SessionHandler // Handler for new sessions
	sessions         map[string]*Session
	mu               sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup

	// Configurable for testing
	cleanupInterval time.Duration
	sessionTimeout  time.Duration
}

// NewListener creates a new RTP listener
func NewListener(cfg *config.RTPConfig, codecsCfg *config.CodecsConfig, reg registry.Registry, logger logger.Logger) *Listener {
	ctx, cancel := context.WithCancel(context.Background())

	// Create connection limiter (max 5 sessions per stream, 100 total)
	connLimiter := ratelimit.NewConnectionLimiter(5, 100)

	// Create bandwidth manager (total 1 Gbps)
	bandwidthManager := ratelimit.NewBandwidthManager(1_000_000_000) // 1 Gbps

	// Create RTP packet validator
	// Accept all dynamic payload types (96-127) per RFC 3551 Section 3
	dynamicPTs := make([]uint8, 32)
	for i := range dynamicPTs {
		dynamicPTs[i] = uint8(96 + i)
	}
	validatorConfig := &ValidatorConfig{
		AllowedPayloadTypes: dynamicPTs,
		MaxSequenceGap:      100,
		MaxTimestampJump:    90000 * 60, // 60 seconds at 90kHz - more permissive for video streams
	}
	validator := NewValidator(validatorConfig)

	l := &Listener{
		config:           cfg,
		codecsConfig:     codecsCfg,
		registry:         reg,
		connLimiter:      connLimiter,
		bandwidthManager: bandwidthManager,
		validator:        validator,
		logger:           logger.WithField("component", "rtp_listener"),
		sessions:         make(map[string]*Session),
		ctx:              ctx,
		cancel:           cancel,
		cleanupInterval:  10 * time.Second, // Default
		sessionTimeout:   10 * time.Second, // Default
	}

	// Use config session timeout if set
	if cfg.SessionTimeout > 0 {
		l.sessionTimeout = cfg.SessionTimeout
	}

	return l
}

// SetTestTimeouts sets shorter timeouts for testing
func (l *Listener) SetTestTimeouts(cleanupInterval, sessionTimeout time.Duration) {
	l.cleanupInterval = cleanupInterval
	l.sessionTimeout = sessionTimeout
}

// SetSessionHandler sets the handler for new sessions
func (l *Listener) SetSessionHandler(handler SessionHandler) {
	l.handler = handler
}

// Start starts the RTP listener
func (l *Listener) Start() error {
	// RTP socket
	rtpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", l.config.ListenAddr, l.config.Port))
	if err != nil {
		return fmt.Errorf("failed to resolve RTP address: %w", err)
	}

	rtpConn, err := net.ListenUDP("udp", rtpAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on RTP port: %w", err)
	}

	// Set buffer sizes
	if err := rtpConn.SetReadBuffer(l.config.BufferSize); err != nil {
		l.logger.WithError(err).Warn("Failed to set RTP read buffer size")
	}
	if err := rtpConn.SetWriteBuffer(l.config.BufferSize); err != nil {
		l.logger.WithError(err).Warn("Failed to set RTP write buffer size")
	}

	l.rtpConn = rtpConn

	// RTCP socket
	rtcpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", l.config.ListenAddr, l.config.RTCPPort))
	if err != nil {
		rtpConn.Close()
		return fmt.Errorf("failed to resolve RTCP address: %w", err)
	}

	rtcpConn, err := net.ListenUDP("udp", rtcpAddr)
	if err != nil {
		rtpConn.Close()
		return fmt.Errorf("failed to listen on RTCP port: %w", err)
	}

	l.rtcpConn = rtcpConn

	l.logger.WithFields(map[string]interface{}{
		"rtp_port":  l.config.Port,
		"rtcp_port": l.config.RTCPPort,
		"address":   l.config.ListenAddr,
	}).Info("RTP listener started")

	// Start packet router
	l.wg.Add(1)
	go l.routePackets()

	// Start RTCP handler
	l.wg.Add(1)
	go l.handleRTCP()

	// Start session cleanup
	l.wg.Add(1)
	go l.cleanupSessions()

	return nil
}

// Stop stops the RTP listener
func (l *Listener) Stop() error {
	l.logger.Info("Stopping RTP listener")
	l.cancel()

	// Close connections
	if l.rtpConn != nil {
		l.rtpConn.Close()
	}
	if l.rtcpConn != nil {
		l.rtcpConn.Close()
	}

	// Wait for goroutines
	l.wg.Wait()

	// Stop all sessions
	l.mu.Lock()
	for _, session := range l.sessions {
		session.Stop()
	}
	l.sessions = make(map[string]*Session)
	l.mu.Unlock()

	l.logger.Info("RTP listener stopped")
	return nil
}

func (l *Listener) routePackets() {
	defer l.wg.Done()

	// Use maximum UDP datagram size, not MTU. RTP packets can be larger than
	// a single Ethernet MTU when IP fragmentation/reassembly occurs at the OS level.
	buf := make([]byte, 65535)

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
			// Set read deadline
			l.rtpConn.SetReadDeadline(time.Now().Add(time.Second))

			n, addr, err := l.rtpConn.ReadFromUDP(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if l.ctx.Err() != nil {
					return
				}
				l.logger.WithError(err).Error("Failed to read RTP packet")
				continue
			}

			// Log small packets for debugging
			if n < 12 { // RTP header minimum size
				l.logger.WithFields(map[string]interface{}{
					"size":      n,
					"source":    addr.String(),
					"raw_bytes": fmt.Sprintf("%x", buf[:n]),
				}).Debug("Received packet too small for RTP")
				continue
			}

			// Parse RTP packet
			packet := &rtp.Packet{}
			if err := packet.Unmarshal(buf[:n]); err != nil {
				l.logger.WithError(err).WithFields(map[string]interface{}{
					"size":      n,
					"source":    addr.String(),
					"raw_bytes": fmt.Sprintf("%x", buf[:min(n, 16)]), // Log first 16 bytes
				}).Debug("Failed to parse RTP packet")
				continue
			}

			// Validate RTP packet
			if err := l.validator.ValidatePacket(packet); err != nil {
				l.logger.WithError(err).WithFields(map[string]interface{}{
					"ssrc":         packet.SSRC,
					"payload_type": packet.PayloadType,
					"sequence":     packet.SequenceNumber,
				}).Debug("Invalid RTP packet")
				continue
			}

			// Get or create session for this source
			sessionKey := fmt.Sprintf("%s_%d", addr.String(), packet.SSRC)

			l.mu.RLock()
			session, exists := l.sessions[sessionKey]
			l.mu.RUnlock()

			if !exists {
				// Create new session
				streamID := registry.GenerateStreamID(registry.StreamTypeRTP, addr.String())

				// Check connection limit
				if !l.connLimiter.TryAcquire(streamID) {
					l.logger.Warnf("Connection limit exceeded for stream %s", streamID)
					continue
				}

				// Allocate bandwidth (50 Mbps per stream)
				rateLimiter, ok := l.bandwidthManager.AllocateBandwidth(streamID, 50_000_000)
				if !ok {
					l.logger.Warnf("Insufficient bandwidth for stream %s", streamID)
					l.connLimiter.Release(streamID)
					continue
				}

				newSession, err := NewSession(streamID, addr, packet.SSRC,
					l.registry, l.codecsConfig, l.logger)
				if err != nil {
					l.logger.WithError(err).Error("Failed to create RTP session")
					l.connLimiter.Release(streamID)
					l.bandwidthManager.ReleaseBandwidth(streamID)
					continue
				}

				// Set rate limiter
				newSession.SetRateLimiter(rateLimiter)

				// Set session timeout
				newSession.SetTimeout(l.sessionTimeout)

				l.mu.Lock()
				// Check again in case another goroutine created it
				session, exists := l.sessions[sessionKey]
				if exists {
					l.mu.Unlock()
					newSession.Stop()
					l.connLimiter.Release(streamID)
					l.bandwidthManager.ReleaseBandwidth(streamID)
				} else {
					l.sessions[sessionKey] = newSession
					session = newSession
					l.mu.Unlock()

					// Use handler if available
					if l.handler != nil {
						// Handler will manage the session
						go func() {
							// Create a channel to signal handler completion
							done := make(chan struct{})
							var handlerErr error

							// Run handler in a separate goroutine
							go func() {
								handlerErr = l.handler(newSession)
								close(done)
							}()

							// Wait for either handler completion or context cancellation
							select {
							case <-done:
								if handlerErr != nil {
									l.logger.WithError(handlerErr).WithField("stream_id", streamID).Error("Session handler error")
								}
							case <-l.ctx.Done():
								// Listener is stopping, stop the session
								newSession.Stop()
								l.logger.WithField("stream_id", streamID).Info("Handler cancelled due to listener shutdown")
							}

							// Clean up when handler returns or context is cancelled
							l.mu.Lock()
							delete(l.sessions, sessionKey)
							l.mu.Unlock()
							l.connLimiter.Release(streamID)
							l.bandwidthManager.ReleaseBandwidth(streamID)
						}()
					} else {
						// Default behavior - just start the session
						session.Start()
					}

					l.logger.WithFields(map[string]interface{}{
						"stream_id":   streamID,
						"remote_addr": addr.String(),
						"ssrc":        packet.SSRC,
					}).Info("New RTP session created")
				}
			} else {
				l.mu.RLock()
				session = l.sessions[sessionKey]
				l.mu.RUnlock()
			}

			// Forward packet to session for processing
			if session != nil {
				session.ProcessPacket(packet)
			}
		}
	}
}

func (l *Listener) cleanupSessions() {
	defer l.wg.Done()

	ticker := time.NewTicker(l.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			activeCount := 0
			for key, session := range l.sessions {
				if !session.IsActive() {
					l.logger.WithField("stream_id", session.streamID).Info("RTP session timed out")
					session.Stop()
					delete(l.sessions, key)
					// Release resources
					l.connLimiter.Release(session.streamID)
					l.bandwidthManager.ReleaseBandwidth(session.streamID)
				} else {
					activeCount++
				}
			}
			l.mu.Unlock()

			// Update active sessions metric
			metrics.SetActiveRTPSessions(activeCount)
			metrics.SetActiveStreams("rtp", activeCount)
		}
	}
}

// GetActiveSessions returns the number of active RTP sessions
func (l *Listener) GetActiveSessions() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.sessions)
}

// GetSessionStats returns statistics for all active sessions
func (l *Listener) GetSessionStats() map[string]SessionStats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	stats := make(map[string]SessionStats)
	for _, session := range l.sessions {
		stats[session.streamID] = session.GetStats()
	}
	return stats
}

// handleRTCP processes RTCP packets
func (l *Listener) handleRTCP() {
	defer l.wg.Done()

	buf := make([]byte, 65535) // Maximum UDP datagram size

	for {
		select {
		case <-l.ctx.Done():
			return
		default:
			// Set read deadline
			l.rtcpConn.SetReadDeadline(time.Now().Add(time.Second))

			n, addr, err := l.rtcpConn.ReadFromUDP(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if l.ctx.Err() != nil {
					return
				}
				l.logger.WithError(err).Debug("Failed to read RTCP packet")
				continue
			}

			// Validate RTCP version (RFC 3550 Section 6.1: version must be 2)
			if n < 4 {
				l.logger.WithField("size", n).Debug("RTCP packet too short")
				continue
			}
			rtcpVersion := (buf[0] >> 6) & 0x03
			if rtcpVersion != 2 {
				l.logger.WithField("version", rtcpVersion).Debug("Invalid RTCP version")
				continue
			}

			// Find the session for this RTCP packet
			// Parse SSRC from RTCP header (bytes 4-7) for more precise routing
			var rtcpSSRC uint32
			rtcpSSRCParsed := false
			if n >= 8 {
				rtcpSSRC = uint32(buf[4])<<24 | uint32(buf[5])<<16 | uint32(buf[6])<<8 | uint32(buf[7])
				rtcpSSRCParsed = true
			}

			l.mu.RLock()
			var targetSession *Session
			for _, session := range l.sessions {
				if session.remoteAddr.IP.Equal(addr.IP) {
					// If we have an SSRC, prefer exact match (SSRC 0 is valid per RFC 3550)
					if rtcpSSRCParsed && session.ssrc == rtcpSSRC {
						targetSession = session
						break
					}
					// Fall back to IP-only match if no SSRC parsed
					if !rtcpSSRCParsed {
						targetSession = session
						break
					}
				}
			}
			l.mu.RUnlock()

			if targetSession != nil {
				// Forward RTCP packet to session
				targetSession.ProcessRTCPPacket(buf[:n])
			}
		}
	}
}

// TerminateStream terminates a specific stream session
func (l *Listener) TerminateStream(streamID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Find the session with this stream ID
	var sessionKey string
	var session *Session
	for key, s := range l.sessions {
		if s.streamID == streamID {
			sessionKey = key
			session = s
			break
		}
	}

	if session == nil {
		return fmt.Errorf("stream %s not found", streamID)
	}

	// Stop the session
	session.Stop()
	delete(l.sessions, sessionKey)

	// Release resources
	l.connLimiter.Release(streamID)
	l.bandwidthManager.ReleaseBandwidth(streamID)

	l.logger.WithField("stream_id", streamID).Info("Stream terminated")
	return nil
}

// PauseStream pauses data ingestion for a stream
func (l *Listener) PauseStream(streamID string) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Find the session with this stream ID
	var session *Session
	for _, s := range l.sessions {
		if s.streamID == streamID {
			session = s
			break
		}
	}

	if session == nil {
		return fmt.Errorf("stream %s not found", streamID)
	}

	session.Pause()
	l.logger.WithField("stream_id", streamID).Info("Stream paused")
	return nil
}

// ResumeStream resumes data ingestion for a paused stream
func (l *Listener) ResumeStream(streamID string) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Find the session with this stream ID
	var session *Session
	for _, s := range l.sessions {
		if s.streamID == streamID {
			session = s
			break
		}
	}

	if session == nil {
		return fmt.Errorf("stream %s not found", streamID)
	}

	session.Resume()
	l.logger.WithField("stream_id", streamID).Info("Stream resumed")
	return nil
}
