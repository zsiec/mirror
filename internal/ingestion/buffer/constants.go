package buffer

import "time"

// Buffer size constants
const (
	// DefaultReadBufferSize is the default size for read buffers (4KB)
	DefaultReadBufferSize = 4096

	// MaxPacketSize is the maximum size for network packets (64KB)
	MaxPacketSize = 65536

	// DefaultRingBufferSize is the default ring buffer size (4MB)
	DefaultRingBufferSize = 4194304

	// DefaultPacketBufferSize is the default packet buffer size (64KB)
	DefaultPacketBufferSize = 65536
)

// Timeout constants
const (
	// DefaultSessionTimeout is the default timeout for idle sessions
	DefaultSessionTimeout = 10 * time.Second

	// DefaultPeerIdleTimeout is the default timeout for idle peers
	DefaultPeerIdleTimeout = 5 * time.Second
)

// Stream limits
const (
	// DefaultMaxConnections is the default maximum number of concurrent connections
	DefaultMaxConnections = 25

	// DefaultMaxSessions is the default maximum number of concurrent sessions
	DefaultMaxSessions = 25

	// MaxStreamIDLength is the maximum length of a stream ID
	MaxStreamIDLength = 255
)
