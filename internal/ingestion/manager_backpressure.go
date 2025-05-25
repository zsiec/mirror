package ingestion

import (
	"github.com/pion/rtcp"
)

// StreamConnection is a common interface for SRT and RTP connections
type StreamConnection interface {
	GetStreamID() string
	Read([]byte) (int, error)
	Close() error
}

// SRTConnection wraps srt.Connection to implement StreamConnection
type SRTConnection interface {
	StreamConnection
	GetMaxBW() int64
	SetMaxBW(int64) error
}

// RTPConnection wraps rtp.Session to implement StreamConnection
type RTPConnection interface {
	StreamConnection
	GetBitrate() int64
	GetSSRC() uint32
	SendRTCP(rtcp.Packet) error
}
