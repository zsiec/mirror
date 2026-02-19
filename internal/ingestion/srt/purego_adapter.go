package srt

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	gosrt "github.com/zsiec/srtgo"
)

// InitSRTLibrary is a no-op for the pure Go implementation (no C library to init).
func InitSRTLibrary() {}

// CleanupSRT is a no-op for the pure Go implementation (no C library to clean up).
func CleanupSRT() {}

// PureGoAdapter implements SRTAdapter using the pure Go SRT library.
type PureGoAdapter struct{}

// NewPureGoAdapter creates a new adapter using the pure Go SRT library.
func NewPureGoAdapter() *PureGoAdapter {
	return &PureGoAdapter{}
}

// NewListener creates a new SRT listener.
func (a *PureGoAdapter) NewListener(address string, port int, config Config) (SRTListener, error) {
	return &PureGoListener{
		address: address,
		port:    port,
		config:  config,
	}, nil
}

// NewConnection wraps a PureGoSocket as an SRTConnection.
func (a *PureGoAdapter) NewConnection(socket SRTSocket) (SRTConnection, error) {
	pgSocket, ok := socket.(*PureGoSocket)
	if !ok {
		return nil, fmt.Errorf("invalid socket type for PureGo adapter")
	}
	return &PureGoConnection{
		conn:   pgSocket.conn,
		config: pgSocket.config,
	}, nil
}

// Connect creates an outbound SRT connection (caller mode).
func (a *PureGoAdapter) Connect(ctx context.Context, address string, port int, config Config) (SRTConnection, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before connect: %w", err)
	}

	cfg := buildPureGoConfig(config)
	addr := fmt.Sprintf("%s:%d", address, port)

	conn, err := gosrt.Dial(addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("SRT connect failed: %w", err)
	}

	return &PureGoConnection{
		conn:   conn,
		config: config,
	}, nil
}

// buildPureGoConfig converts mirror's Config to a gosrt.Config.
func buildPureGoConfig(config Config) gosrt.Config {
	cfg := gosrt.DefaultConfig()

	// Latency — use both RecvLatency and PeerLatency (matching the old rcvlatency/peerlatency options)
	if config.Latency > 0 {
		cfg.RecvLatency = config.Latency
		cfg.PeerLatency = config.Latency
	}

	// Bandwidth — mirror stores bits/sec, pure Go lib expects bytes/sec
	if config.MaxBandwidth > 0 {
		cfg.MaxBW = config.MaxBandwidth / 8
	}
	if config.InputBandwidth > 0 {
		cfg.InputBW = config.InputBandwidth / 8
		// When InputBW is set with maxbw=0, SRT uses relative mode
		if config.MaxBandwidth <= 0 {
			cfg.MaxBW = 0
		}
	}

	cfg.OverheadBW = 25

	if config.PayloadSize > 0 {
		cfg.PayloadSize = config.PayloadSize
	}
	if config.FlowControlWindow > 0 {
		cfg.FC = config.FlowControlWindow
	}
	if config.PeerIdleTimeout > 0 {
		cfg.PeerIdleTimeout = config.PeerIdleTimeout
	}

	cfg.ConnTimeout = 5 * time.Second

	cfg.LossMaxTTL = 128

	// Buffer sizes — derive from input bandwidth and latency (same logic as haivision_adapter)
	if config.InputBandwidth > 0 {
		latencySec := config.Latency.Seconds()
		if latencySec == 0 {
			latencySec = 0.12 // default 120ms
		}
		rcvBufBytes := int(float64(config.InputBandwidth/8) * latencySec * 2)
		minBuf := 8 * 1024 * 1024 // 8MB minimum
		if rcvBufBytes < minBuf {
			rcvBufBytes = minBuf
		}
		// Pure Go lib uses packet count for buffer sizes
		mss := cfg.MSS
		if mss <= 0 {
			mss = 1500
		}
		bufPackets := rcvBufBytes / mss
		if bufPackets < gosrt.MinBufSize {
			bufPackets = gosrt.MinBufSize
		}
		cfg.SendBufSize = bufPackets
		cfg.RecvBufSize = bufPackets
	}

	// Encryption
	if config.Encryption.Enabled {
		cfg.Passphrase = config.Encryption.Passphrase
		if config.Encryption.KeyLength > 0 {
			cfg.KeyLength = config.Encryption.KeyLength
		}
		enforced := true
		cfg.EnforcedEncryption = &enforced
	}

	return cfg
}

// ---------------------------------------------------------------------------
// PureGoListener
// ---------------------------------------------------------------------------

// PureGoListener implements SRTListener using the pure Go SRT library.
type PureGoListener struct {
	address  string
	port     int
	config   Config
	listener *gosrt.Listener
	callback ListenCallback
}

// Listen starts listening for SRT connections.
func (l *PureGoListener) Listen(_ context.Context, _ int) error {
	cfg := buildPureGoConfig(l.config)

	addr := fmt.Sprintf("%s:%d", l.address, l.port)
	listener, err := gosrt.Listen(addr, cfg)
	if err != nil {
		return fmt.Errorf("SRT listen failed: %w", err)
	}
	l.listener = listener

	// Bridge mirror's ListenCallback to the pure Go accept/reject function.
	if l.callback != nil {
		cb := l.callback // capture
		l.listener.SetAcceptRejectFunc(func(req gosrt.ConnRequest) gosrt.RejectReason {
			// Parse the remote address into *net.UDPAddr
			var udpAddr *net.UDPAddr
			if req.RemoteAddr != nil {
				if ua, ok := req.RemoteAddr.(*net.UDPAddr); ok {
					udpAddr = ua
				} else {
					// Fallback: parse string representation
					udpAddr, _ = net.ResolveUDPAddr("udp", req.RemoteAddr.String())
				}
			}
			if udpAddr == nil {
				udpAddr = &net.UDPAddr{}
			}

			// Create a callback socket that captures rejection reasons
			cbSocket := &PureGoCallbackSocket{
				streamID: req.StreamID,
			}

			accepted := cb(cbSocket, int(req.HSVersion), udpAddr, req.StreamID)
			if !accepted {
				return cbSocket.rejectReason
			}
			return 0 // 0 = accept
		})
	}

	return nil
}

// Accept accepts a new SRT connection.
func (l *PureGoListener) Accept() (SRTSocket, *net.UDPAddr, error) {
	if l.listener == nil {
		return nil, nil, fmt.Errorf("listener socket is closed")
	}

	conn, err := l.listener.Accept()
	if err != nil {
		return nil, nil, err
	}

	// Extract *net.UDPAddr from the connection's remote address
	var udpAddr *net.UDPAddr
	if ra := conn.RemoteAddr(); ra != nil {
		if ua, ok := ra.(*net.UDPAddr); ok {
			udpAddr = ua
		} else {
			udpAddr, _ = net.ResolveUDPAddr("udp", ra.String())
		}
	}
	if udpAddr == nil {
		udpAddr = &net.UDPAddr{}
	}

	socket := &PureGoSocket{
		conn:     conn,
		config:   l.config,
		streamID: conn.StreamID(),
	}

	return socket, udpAddr, nil
}

// SetListenCallback stores the callback (used before Listen is called).
func (l *PureGoListener) SetListenCallback(callback ListenCallback) error {
	l.callback = callback
	return nil
}

// Close closes the listener.
func (l *PureGoListener) Close() error {
	if l.listener != nil {
		err := l.listener.Close()
		l.listener = nil
		return err
	}
	return nil
}

// GetPort returns the port the listener is bound to.
func (l *PureGoListener) GetPort() int {
	return l.port
}

// ---------------------------------------------------------------------------
// PureGoSocket (wraps an accepted *srt.Conn)
// ---------------------------------------------------------------------------

// PureGoSocket implements SRTSocket wrapping an accepted connection.
type PureGoSocket struct {
	conn     *gosrt.Conn
	config   Config
	streamID string
}

// GetStreamID returns the stream ID.
func (s *PureGoSocket) GetStreamID() string {
	if s.streamID != "" {
		return s.streamID
	}
	if s.conn != nil {
		s.streamID = s.conn.StreamID()
	}
	return s.streamID
}

// Close closes the underlying connection.
func (s *PureGoSocket) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// SetRejectReason is a no-op on post-accept sockets (rejection happens during handshake).
func (s *PureGoSocket) SetRejectReason(_ RejectionReason) error {
	return nil
}

// ---------------------------------------------------------------------------
// PureGoCallbackSocket (used inside the listen callback to capture rejection)
// ---------------------------------------------------------------------------

// PureGoCallbackSocket implements SRTSocket for use inside the listen callback.
// It captures the rejection reason set by mirror's callback so we can return it
// to the pure Go library's accept/reject function.
type PureGoCallbackSocket struct {
	streamID     string
	rejectReason gosrt.RejectReason
}

// GetStreamID returns the stream ID.
func (s *PureGoCallbackSocket) GetStreamID() string {
	return s.streamID
}

// Close is a no-op for callback sockets.
func (s *PureGoCallbackSocket) Close() error {
	return nil
}

// SetRejectReason captures the rejection reason for the accept/reject callback.
func (s *PureGoCallbackSocket) SetRejectReason(reason RejectionReason) error {
	s.rejectReason = mapRejectReason(reason)
	return nil
}

// mapRejectReason converts mirror's RejectionReason to the pure Go library's reject codes.
func mapRejectReason(reason RejectionReason) gosrt.RejectReason {
	switch reason {
	case RejectionReasonUnauthorized:
		return gosrt.RejXUnauthorized
	case RejectionReasonResourceUnavailable:
		return gosrt.RejXOverloaded
	case RejectionReasonBadRequest:
		return gosrt.RejXBadRequest
	case RejectionReasonForbidden:
		return gosrt.RejXForbidden
	case RejectionReasonNotFound:
		return gosrt.RejXNotFound
	case RejectionReasonBadMode:
		return gosrt.RejXBadMode
	case RejectionReasonUnacceptable:
		return gosrt.RejXUnacceptable
	default:
		return gosrt.RejPeer
	}
}

// ---------------------------------------------------------------------------
// PureGoConnection
// ---------------------------------------------------------------------------

// PureGoConnection implements SRTConnection using the pure Go SRT library.
type PureGoConnection struct {
	conn   *gosrt.Conn
	config Config
	mu     sync.Mutex
	maxBW  int64 // track last-set value since the lib has no getter
}

// Read reads data from the SRT connection.
func (c *PureGoConnection) Read(b []byte) (int, error) {
	if c.conn == nil {
		return 0, fmt.Errorf("connection is nil")
	}
	return c.conn.Read(b)
}

// Write writes data to the SRT connection.
func (c *PureGoConnection) Write(b []byte) (int, error) {
	if c.conn == nil {
		return 0, fmt.Errorf("connection is nil")
	}
	return c.conn.Write(b)
}

// Close closes the connection.
func (c *PureGoConnection) Close() error {
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// GetStreamID returns the stream ID.
func (c *PureGoConnection) GetStreamID() string {
	if c.conn == nil {
		return ""
	}
	return c.conn.StreamID()
}

// GetStats returns connection statistics.
func (c *PureGoConnection) GetStats() ConnectionStats {
	if c.conn == nil {
		return ConnectionStats{}
	}

	// clear=false → cumulative (Total) stats, matching haivision adapter behavior
	stats := c.conn.Stats(false)

	return ConnectionStats{
		BytesReceived:    int64(stats.RecvUniqueBytes),
		BytesSent:        int64(stats.SentUniqueBytes),
		PacketsReceived:  int64(stats.RecvUniquePackets),
		PacketsSent:      int64(stats.SentUniquePackets),
		PacketsLost:      int64(stats.RecvLoss),
		PacketsRetrans:   int64(stats.Retransmits),
		RTTMs:            float64(stats.RTT.Milliseconds()),
		BandwidthMbps:    stats.MbpsSendRate,
		DeliveryDelayMs:  float64(stats.MsRcvTsbPdDelay.Milliseconds()),
		ConnectionTimeMs: stats.Duration,

		// Extended stats
		PacketsDropped:            int64(stats.RecvDropped),
		PacketsReceiveLost:        int64(stats.RecvLoss),
		PacketsFlightSize:         stats.FlightSize,
		NegotiatedLatencyMs:       int(stats.MsRcvTsbPdDelay.Milliseconds()),
		ReceiveRateMbps:           stats.MbpsRecvRate,
		EstimatedLinkCapacityMbps: float64(stats.EstimatedBandwidth),
		AvailableRcvBuf:          stats.RecvBufAvailable * c.getMSS(), // convert packets → bytes
	}
}

// getMSS returns the MSS for buffer size conversions.
func (c *PureGoConnection) getMSS() int {
	if c.config.PayloadSize > 0 {
		return c.config.PayloadSize
	}
	return 1500
}

// SetMaxBW sets the maximum bandwidth.
func (c *PureGoConnection) SetMaxBW(bw int64) error {
	if c.conn == nil {
		return fmt.Errorf("connection is nil")
	}
	c.conn.SetMaxBW(bw)
	c.mu.Lock()
	c.maxBW = bw
	c.mu.Unlock()
	return nil
}

// GetMaxBW returns the last-set maximum bandwidth.
func (c *PureGoConnection) GetMaxBW() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxBW
}
