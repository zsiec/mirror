package srt

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/haivision/srtgo"
)

func init() {
	// Initialize SRT library
	srtgo.InitSRT()
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
	// Create socket options
	options := make(map[string]string)

	// Set basic options
	if l.config.Latency > 0 {
		options["latency"] = strconv.Itoa(int(l.config.Latency.Milliseconds()))
	}
	if l.config.MaxBandwidth > 0 {
		options["maxbw"] = strconv.FormatInt(l.config.MaxBandwidth, 10)
	}
	if l.config.InputBandwidth > 0 {
		options["inputbw"] = strconv.FormatInt(l.config.InputBandwidth, 10)
	}
	if l.config.PayloadSize > 0 {
		options["payloadsize"] = strconv.Itoa(l.config.PayloadSize)
	}
	if l.config.FlowControlWindow > 0 {
		options["fc"] = strconv.Itoa(l.config.FlowControlWindow)
	}
	if l.config.PeerIdleTimeout > 0 {
		options["peeridletimeo"] = strconv.Itoa(int(l.config.PeerIdleTimeout.Milliseconds()))
	}

	// Encryption
	if l.config.Encryption.Enabled {
		options["passphrase"] = l.config.Encryption.Passphrase
		if l.config.Encryption.KeyLength > 0 {
			options["pbkeylen"] = strconv.Itoa(l.config.Encryption.KeyLength)
		}
	}

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
	return l.socket.Listen(backlog)
}

// Accept accepts a new SRT connection
func (l *HaivisionListener) Accept() (SRTSocket, *net.UDPAddr, error) {
	socket, addr, err := l.socket.Accept()
	if err != nil {
		return nil, nil, err
	}

	hvSocket := &HaivisionSocket{
		socket: socket,
		config: l.config,
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
		BytesReceived:    int64(stats.ByteRecv),
		BytesSent:        int64(stats.ByteSent),
		PacketsReceived:  int64(stats.PktRecv),
		PacketsSent:      int64(stats.PktSent),
		PacketsLost:      int64(stats.PktSndLoss),
		PacketsRetrans:   int64(stats.PktRetrans),
		RTTMs:            float64(stats.MsRTT),
		BandwidthMbps:    float64(stats.MbpsSendRate),
		DeliveryDelayMs:  float64(stats.MsRcvTsbPdDelay),
		ConnectionTimeMs: time.Duration(stats.MsTimeStamp) * time.Millisecond,
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
