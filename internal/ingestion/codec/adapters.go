package codec

import (
	"time"

	"github.com/pion/rtp"
)

// Depacketizer is the interface for RTP depacketizers
type Depacketizer interface {
	Depacketize(packet *rtp.Packet) ([][]byte, error)
	Reset()
}

// NewH264Depacketizer creates a new H264 depacketizer
func NewH264Depacketizer() Depacketizer {
	return &H264Depacketizer{
		fragments:       make([][]byte, 0),
		fragmentTimeout: 5 * time.Second, // Default 5 second timeout
	}
}

// NewAV1Depacketizer creates a new AV1 depacketizer
func NewAV1Depacketizer() Depacketizer {
	return &AV1Depacketizer{
		fragments:       make([][]byte, 0),
		temporalUnitBuf: make([][]byte, 0),
	}
}

// NewJPEGXSDepacketizer creates a new JPEG XS depacketizer
func NewJPEGXSDepacketizer() Depacketizer {
	return &JPEGXSDepacketizer{
		fragments: make([][]byte, 0),
	}
}

// NewHEVCDepacketizer creates a new HEVC depacketizer
func NewHEVCDepacketizer() Depacketizer {
	return &HEVCDepacketizer{
		fragments: make([][]byte, 0),
	}
}
