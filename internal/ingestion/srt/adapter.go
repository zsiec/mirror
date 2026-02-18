package srt

import (
	"context"
	"net"
	"time"
)

// SRTAdapter abstracts the SRT library implementation
// This allows us to switch between different SRT libraries without breaking existing code
type SRTAdapter interface {
	// Configuration and lifecycle
	NewListener(address string, port int, config Config) (SRTListener, error)
	NewConnection(socket SRTSocket) (SRTConnection, error)
	// Caller mode: connect to a remote SRT listener
	Connect(ctx context.Context, address string, port int, config Config) (SRTConnection, error)
}

// SRTListener represents an SRT listener that can accept connections
type SRTListener interface {
	Listen(ctx context.Context, backlog int) error
	Accept() (SRTSocket, *net.UDPAddr, error)
	SetListenCallback(callback ListenCallback) error
	Close() error
	GetPort() int
}

// SRTSocket represents a low-level SRT socket
type SRTSocket interface {
	GetStreamID() string
	Close() error
	SetRejectReason(reason RejectionReason) error
}

// SRTConnection represents an established SRT connection for data transfer
type SRTConnection interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	Close() error
	GetStreamID() string
	GetStats() ConnectionStats
	SetMaxBW(bw int64) error
	GetMaxBW() int64
}

// ListenCallback is called when a new connection is requested
type ListenCallback func(socket SRTSocket, version int, addr *net.UDPAddr, streamID string) bool

// RejectionReason represents why a connection was rejected
type RejectionReason int

const (
	RejectionReasonUnauthorized        RejectionReason = iota // SRT 401
	RejectionReasonResourceUnavailable                        // SRT 402 (Overload)
	RejectionReasonBadRequest                                 // SRT 400
	RejectionReasonForbidden                                  // SRT 403
	RejectionReasonNotFound                                   // SRT 404
	RejectionReasonBadMode                                    // SRT 405
	RejectionReasonUnacceptable                               // SRT 406
)

// ConnectionStats provides statistics about an SRT connection
type ConnectionStats struct {
	BytesReceived    int64
	BytesSent        int64
	PacketsReceived  int64
	PacketsSent      int64
	PacketsLost      int64
	PacketsRetrans   int64
	RTTMs            float64
	BandwidthMbps    float64
	DeliveryDelayMs  float64
	ConnectionTimeMs time.Duration

	// Extended stats for spec compliance
	PacketsDropped            int64   // Too-late-to-play dropped packets (receiver)
	PacketsReceiveLost        int64   // Packets lost on receiver side
	PacketsFlightSize         int     // Packets currently in flight
	NegotiatedLatencyMs       int     // Negotiated TSBPD latency
	ReceiveRateMbps           float64 // Receiving rate in Mbps
	EstimatedLinkCapacityMbps float64 // Estimated link bandwidth in Mbps
	AvailableRcvBuf           int     // Available receiver buffer in bytes
}

// Config represents SRT configuration options
type Config struct {
	// Server configuration
	Address           string
	Port              int
	Latency           time.Duration
	MaxBandwidth      int64
	InputBandwidth    int64
	PayloadSize       int
	FlowControlWindow int
	PeerIdleTimeout   time.Duration
	MaxConnections    int

	// Security
	Encryption EncryptionConfig
}

// EncryptionConfig holds SRT encryption settings
type EncryptionConfig struct {
	Enabled         bool
	Passphrase      string
	KeyLength       int
	PBKDFIterations int
}

// ConnectionHandler is a function that handles new connections
type ConnectionHandler func(conn *Connection) error
