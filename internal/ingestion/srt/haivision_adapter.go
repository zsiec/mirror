package srt

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/haivision/srtgo"
)

var srtInitOnce sync.Once

func init() {
	InitSRTLibrary()
}

// InitSRTLibrary initializes the SRT library. Safe to call multiple times.
func InitSRTLibrary() {
	srtInitOnce.Do(func() {
		srtgo.InitSRT()
	})
}

// CleanupSRT cleans up the SRT library. Must be called during application shutdown.
func CleanupSRT() {
	srtgo.CleanupSRT()
}

// HaivisionAdapter implements SRTAdapter using the official Haivision srtgo library
type HaivisionAdapter struct{}

// NewHaivisionAdapter creates a new adapter using Haivision srtgo
func NewHaivisionAdapter() *HaivisionAdapter {
	return &HaivisionAdapter{}
}

// NewListener creates a new SRT listener using Haivision srtgo
func (h *HaivisionAdapter) NewListener(address string, port int, config Config) (SRTListener, error) {
	listener := &HaivisionListener{
		address: address,
		port:    uint16(port),
		config:  config,
	}
	return listener, nil
}

// NewConnection wraps a Haivision SRT socket as a connection
func (h *HaivisionAdapter) NewConnection(socket SRTSocket) (SRTConnection, error) {
	hvSocket, ok := socket.(*HaivisionSocket)
	if !ok {
		return nil, fmt.Errorf("invalid socket type for Haivision adapter")
	}

	conn := &HaivisionConnection{
		socket: hvSocket.socket,
		config: hvSocket.config,
	}

	return conn, nil
}

// Connect creates an outbound SRT connection (caller mode)
func (h *HaivisionAdapter) Connect(ctx context.Context, address string, port int, config Config) (SRTConnection, error) {
	// Check context before attempting the blocking connect
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before connect: %w", err)
	}

	options := make(map[string]string)
	populateOptions(options, config)

	socket := srtgo.NewSrtSocket(address, uint16(port), options)
	if socket == nil {
		return nil, fmt.Errorf("failed to create SRT caller socket")
	}

	// Note: srtgo.Connect() is a blocking call. Cancellation is handled by
	// SRTO_CONNTIMEO (set in populateOptions) rather than context, since the
	// underlying C library doesn't support context-based cancellation.
	if err := socket.Connect(); err != nil {
		socket.Close()
		return nil, fmt.Errorf("SRT connect failed: %w", err)
	}

	return &HaivisionConnection{
		socket: socket,
		config: config,
	}, nil
}

// populateOptions builds the SRT socket options map from config.
// Used by both Listen and Connect to ensure consistent option configuration.
func populateOptions(options map[string]string, config Config) {
	// Mandatory live mode settings
	options["transtype"] = "live"
	options["tsbpdmode"] = "1"
	options["tlpktdrop"] = "1"
	options["nakreport"] = "1"
	options["linger"] = "0"

	// Use rcvlatency/peerlatency instead of deprecated SRTO_LATENCY
	if config.Latency > 0 {
		latencyMs := strconv.Itoa(int(config.Latency.Milliseconds()))
		options["rcvlatency"] = latencyMs
		options["peerlatency"] = latencyMs
	}

	// Bandwidth settings — config stores bits/sec, SRT expects bytes/sec.
	// MaxBandwidth modes: >0 = absolute cap, -1 = auto/relative, 0 = invalid (caught by validation).
	if config.MaxBandwidth > 0 {
		// Absolute bandwidth cap
		options["maxbw"] = strconv.FormatInt(config.MaxBandwidth/8, 10) // bps → bytes/sec
	} else if config.InputBandwidth > 0 {
		// Relative mode: effective_max = inputbw * (1 + oheadbw/100)
		// maxbw=0 enables this mode so oheadbw actually takes effect
		options["maxbw"] = "0"
	}
	// Otherwise SRT defaults to maxbw=-1 (unlimited)

	if config.InputBandwidth > 0 {
		options["inputbw"] = strconv.FormatInt(config.InputBandwidth/8, 10) // bps → bytes/sec
	}
	if config.PayloadSize > 0 {
		options["payloadsize"] = strconv.Itoa(config.PayloadSize)
	}
	if config.FlowControlWindow > 0 {
		options["fc"] = strconv.Itoa(config.FlowControlWindow)
	}
	if config.PeerIdleTimeout > 0 {
		options["peeridletimeo"] = strconv.Itoa(int(config.PeerIdleTimeout.Milliseconds()))
	}

	// Buffer sizes - derive from input bandwidth and latency
	// Per SRT Configuration Guidelines: buf = bps/8 * (RTT + latency) * margin
	if config.InputBandwidth > 0 {
		latencySec := config.Latency.Seconds()
		if latencySec == 0 {
			latencySec = 0.12 // default 120ms
		}
		// InputBandwidth is in bits/sec; divide by 8 to get bytes/sec.
		// 2x safety margin covers RTT + jitter headroom.
		rcvBufSize := int(float64(config.InputBandwidth/8) * latencySec * 2)
		minBuf := 8 * 1024 * 1024 // 8MB minimum
		if rcvBufSize < minBuf {
			rcvBufSize = minBuf
		}
		options["rcvbuf"] = strconv.Itoa(rcvBufSize)
		options["sndbuf"] = strconv.Itoa(rcvBufSize)
	}

	// Overhead bandwidth percentage (only effective when maxbw=0, relative mode)
	options["oheadbw"] = "25"

	// Packet reorder tolerance — allows SRT to wait for reordered packets
	// instead of immediately triggering NAK retransmission. Critical for
	// internet-facing streams where packet reordering is common.
	options["lossmaxttl"] = "128"

	// Connection establishment timeout (not idle timeout — peeridletimeo handles that)
	options["conntimeo"] = "5000" // 5 seconds is reasonable for connection setup

	// Encryption
	if config.Encryption.Enabled {
		options["enforcedencryption"] = "1" // reject unencrypted peers
		options["passphrase"] = config.Encryption.Passphrase
		if config.Encryption.KeyLength > 0 {
			options["pbkeylen"] = strconv.Itoa(config.Encryption.KeyLength)
		}
	}
}

// HaivisionListener implements SRTListener using srtgo
type HaivisionListener struct {
	address  string
	port     uint16
	config   Config
	socket   *srtgo.SrtSocket
	callback ListenCallback
}

// Listen starts listening for SRT connections
func (l *HaivisionListener) Listen(ctx context.Context, backlog int) error {
	options := make(map[string]string)
	populateOptions(options, l.config)

	// Create the socket
	l.socket = srtgo.NewSrtSocket(l.address, l.port, options)
	if l.socket == nil {
		return fmt.Errorf("failed to create SRT socket")
	}

	// Set listen callback if provided
	if l.callback != nil {
		l.socket.SetListenCallback(func(socket *srtgo.SrtSocket, version int, addr *net.UDPAddr, streamid string) bool {
			hvSocket := &HaivisionSocket{
				socket:   socket,
				config:   l.config,
				streamID: streamid,
			}
			return l.callback(hvSocket, version, addr, streamid)
		})
	}

	// Start listening
	if err := l.socket.Listen(backlog); err != nil {
		l.socket.Close()
		l.socket = nil
		return fmt.Errorf("SRT listen failed: %w", err)
	}
	return nil
}

// Accept accepts a new SRT connection
func (l *HaivisionListener) Accept() (SRTSocket, *net.UDPAddr, error) {
	if l.socket == nil {
		return nil, nil, fmt.Errorf("listener socket is closed")
	}
	socket, addr, err := l.socket.Accept()
	if err != nil {
		return nil, nil, err
	}

	// Eagerly fetch streamID to avoid extra syscall later
	streamID, _ := socket.GetSockOptString(srtgo.SRTO_STREAMID)

	hvSocket := &HaivisionSocket{
		socket:   socket,
		config:   l.config,
		streamID: streamID,
	}

	return hvSocket, addr, nil
}

// SetListenCallback sets the callback for incoming connections
func (l *HaivisionListener) SetListenCallback(callback ListenCallback) error {
	l.callback = callback
	return nil
}

// Close closes the listener
func (l *HaivisionListener) Close() error {
	if l.socket != nil {
		l.socket.Close()
		l.socket = nil
	}
	return nil
}

// GetPort returns the port the listener is bound to
func (l *HaivisionListener) GetPort() int {
	return int(l.port)
}

// HaivisionSocket implements SRTSocket using srtgo
type HaivisionSocket struct {
	socket   *srtgo.SrtSocket
	config   Config
	streamID string
}

// GetStreamID returns the stream ID
func (s *HaivisionSocket) GetStreamID() string {
	if s.streamID != "" {
		return s.streamID
	}
	// Try to get from socket if not cached
	if s.socket != nil {
		streamID, err := s.socket.GetSockOptString(srtgo.SRTO_STREAMID)
		if err == nil {
			s.streamID = streamID
		}
	}
	return s.streamID
}

// Close closes the socket
func (s *HaivisionSocket) Close() error {
	if s.socket != nil {
		s.socket.Close()
	}
	return nil
}

// SetRejectReason sets the rejection reason for the connection
func (s *HaivisionSocket) SetRejectReason(reason RejectionReason) error {
	if s.socket == nil {
		return fmt.Errorf("socket is nil")
	}

	var srtReason int
	switch reason {
	case RejectionReasonUnauthorized:
		srtReason = srtgo.RejectionReasonUnauthorized
	case RejectionReasonResourceUnavailable:
		srtReason = srtgo.RejectionReasonOverload
	case RejectionReasonBadRequest:
		srtReason = srtgo.RejectionReasonBadRequest
	case RejectionReasonForbidden:
		srtReason = srtgo.RejectionReasonForbidden
	case RejectionReasonNotFound:
		srtReason = srtgo.RejectionReasonNotFound
	case RejectionReasonBadMode:
		srtReason = srtgo.RejectionReasonBadMode
	case RejectionReasonUnacceptable:
		srtReason = srtgo.RejectionReasonUnacceptable
	default:
		srtReason = srtgo.RejectionReasonPredefined
	}

	return s.socket.SetRejectReason(srtReason)
}

// HaivisionConnection implements SRTConnection using srtgo
type HaivisionConnection struct {
	socket *srtgo.SrtSocket
	config Config
}

// Read reads data from the SRT connection
func (c *HaivisionConnection) Read(b []byte) (int, error) {
	if c.socket == nil {
		return 0, fmt.Errorf("socket is nil")
	}
	return c.socket.Read(b)
}

// Write writes data to the SRT connection
func (c *HaivisionConnection) Write(b []byte) (int, error) {
	if c.socket == nil {
		return 0, fmt.Errorf("socket is nil")
	}
	return c.socket.Write(b)
}

// Close closes the connection
func (c *HaivisionConnection) Close() error {
	if c.socket != nil {
		c.socket.Close()
		c.socket = nil
	}
	return nil
}

// GetStreamID returns the stream ID
func (c *HaivisionConnection) GetStreamID() string {
	if c.socket == nil {
		return ""
	}
	streamID, _ := c.socket.GetSockOptString(srtgo.SRTO_STREAMID)
	return streamID
}

// GetStats returns connection statistics
func (c *HaivisionConnection) GetStats() ConnectionStats {
	if c.socket == nil {
		return ConnectionStats{}
	}

	stats, err := c.socket.Stats()
	if err != nil {
		return ConnectionStats{}
	}

	return ConnectionStats{
		// Use Total fields — local fields are cleared on each Stats() call
		// (srtgo calls srt_bstats with clear=1)
		BytesReceived:    int64(stats.ByteRecvTotal),
		BytesSent:        int64(stats.ByteSentTotal),
		PacketsReceived:  int64(stats.PktRecvTotal),
		PacketsSent:      int64(stats.PktSentTotal),
		PacketsLost:      int64(stats.PktRcvLossTotal),
		PacketsRetrans:   int64(stats.PktRetransTotal),
		RTTMs:            float64(stats.MsRTT),
		BandwidthMbps:    float64(stats.MbpsSendRate),
		DeliveryDelayMs:  float64(stats.MsRcvTsbPdDelay),
		ConnectionTimeMs: time.Duration(stats.MsTimeStamp) * time.Millisecond,
		// Extended stats
		PacketsDropped:            int64(stats.PktRcvDropTotal),
		PacketsReceiveLost:        int64(stats.PktRcvLossTotal),
		PacketsFlightSize:         stats.PktFlightSize,
		NegotiatedLatencyMs:       stats.MsRcvTsbPdDelay,
		ReceiveRateMbps:           float64(stats.MbpsRecvRate),
		EstimatedLinkCapacityMbps: float64(stats.MbpsBandwidth),
		AvailableRcvBuf:           stats.ByteAvailRcvBuf,
	}
}

// SetMaxBW sets the maximum bandwidth
func (c *HaivisionConnection) SetMaxBW(bw int64) error {
	if c.socket == nil {
		return fmt.Errorf("socket is nil")
	}
	return c.socket.SetSockOptInt(srtgo.SRTO_MAXBW, int(bw))
}

// GetMaxBW gets the maximum bandwidth
func (c *HaivisionConnection) GetMaxBW() int64 {
	if c.socket == nil {
		return 0
	}
	bw, err := c.socket.GetSockOptInt(srtgo.SRTO_MAXBW)
	if err != nil {
		return 0
	}
	return int64(bw)
}
