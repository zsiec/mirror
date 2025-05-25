package ingestion

import (
	"context"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	rtpPkg "github.com/zsiec/mirror/internal/ingestion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/timestamp"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// RTPConnectionAdapter adapts rtp.Session to implement StreamConnection and RTPConnection interfaces
// Now also emits TimestampedPackets for video-aware processing
type RTPConnectionAdapter struct {
	*rtpPkg.Session
	
	// Timestamp conversion (separate for audio/video)
	videoTimestampMapper *timestamp.TimestampMapper
	audioTimestampMapper *timestamp.TimestampMapper
	
	// Frame detection
	depacketizer    codec.Depacketizer
	lastSeqNum      uint16
	lastTimestamp   uint32
	lastPacketTime  time.Time
	
	// Output channels for video-aware pipeline
	videoOutput     chan types.TimestampedPacket
	audioOutput     chan types.TimestampedPacket
	
	// Track information
	isAudioTrack    bool
	codecType       types.CodecType
	
	// Context for lifecycle
	ctx             context.Context
	cancel          context.CancelFunc
	
	logger          logger.Logger
	mu              sync.RWMutex
}

// Ensure it implements both interfaces
var _ StreamConnection = (*RTPConnectionAdapter)(nil)
var _ RTPConnection = (*RTPConnectionAdapter)(nil)

// NewRTPConnectionAdapter creates a new adapter
func NewRTPConnectionAdapter(session *rtpPkg.Session, codecType types.CodecType) *RTPConnectionAdapter {
	if session == nil {
		return nil
	}
	
	ctx := context.Background()
	
	adapter := &RTPConnectionAdapter{
		Session:      session,
		videoOutput:  make(chan types.TimestampedPacket, 1000),
		audioOutput:  make(chan types.TimestampedPacket, 1000),
		codecType:    codecType,
		isAudioTrack: codecType.IsAudio(),
		ctx:          ctx,
		logger:       logger.FromContext(ctx).WithField("stream_id", session.GetStreamID()),
	}
	
	// Initialize timestamp mappers based on codec
	if adapter.isAudioTrack {
		// Audio clock rates
		switch codecType {
		case types.CodecAAC:
			adapter.audioTimestampMapper = timestamp.NewTimestampMapper(48000)
		case types.CodecOpus:
			adapter.audioTimestampMapper = timestamp.NewTimestampMapper(48000)
		case types.CodecG711:
			adapter.audioTimestampMapper = timestamp.NewTimestampMapper(8000)
		default:
			adapter.audioTimestampMapper = timestamp.NewTimestampMapper(48000) // Default audio
		}
	} else {
		// Video is typically 90kHz
		adapter.videoTimestampMapper = timestamp.NewTimestampMapper(90000)
	}
	
	// Start packet processor in background
	go adapter.processPackets()
	
	return adapter
}

// GetStreamID implements StreamConnection
func (a *RTPConnectionAdapter) GetStreamID() string {
	return a.Session.GetStreamID()
}

// Read implements StreamConnection
func (a *RTPConnectionAdapter) Read(buf []byte) (int, error) {
	// RTP sessions already implement Read
	return a.Session.Read(buf)
}

// GetBitrate returns the current bitrate
func (a *RTPConnectionAdapter) GetBitrate() int64 {
	return a.Session.GetBitrate()
}

// GetSSRC returns the SSRC of the stream
func (a *RTPConnectionAdapter) GetSSRC() uint32 {
	return a.Session.GetSSRC()
}

// SendRTCP sends an RTCP packet
func (a *RTPConnectionAdapter) SendRTCP(pkt rtcp.Packet) error {
	return a.Session.SendRTCP(pkt)
}

// Close implements StreamConnection
func (a *RTPConnectionAdapter) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	close(a.videoOutput)
	close(a.audioOutput)
	return a.Session.Close()
}

// GetVideoOutput returns the channel of video TimestampedPackets
func (a *RTPConnectionAdapter) GetVideoOutput() <-chan types.TimestampedPacket {
	return a.videoOutput
}

// GetAudioOutput returns the channel of audio TimestampedPackets
func (a *RTPConnectionAdapter) GetAudioOutput() <-chan types.TimestampedPacket {
	return a.audioOutput
}

// processPackets runs in background to convert RTP packets to TimestampedPackets
func (a *RTPConnectionAdapter) processPackets() {
	// This would hook into the Session's packet processing
	// For now, we'll add the conversion logic that would be called
	// when the Session receives packets
}

// ProcessPacket converts an RTP packet to TimestampedPacket
// This should be called by the Session when it receives packets
func (a *RTPConnectionAdapter) ProcessPacket(rtpPkt *rtp.Packet) error {
	now := time.Now()
	
	// Get appropriate timestamp mapper
	var timestampMapper *timestamp.TimestampMapper
	var packetType types.PacketType
	var outputChan chan types.TimestampedPacket
	
	if a.isAudioTrack {
		timestampMapper = a.audioTimestampMapper
		packetType = types.PacketTypeAudio
		outputChan = a.audioOutput
	} else {
		timestampMapper = a.videoTimestampMapper
		packetType = types.PacketTypeVideo
		outputChan = a.videoOutput
	}
	
	// Convert RTP timestamp to PTS
	var pts int64
	if timestampMapper != nil {
		pts = timestampMapper.ToPTS(rtpPkt.Timestamp)
	} else {
		// For testing, use raw timestamp
		pts = int64(rtpPkt.Timestamp)
	}
	
	// Get stream ID - use session if available, otherwise use a default for testing
	streamID := "test-stream"
	if a.Session != nil {
		streamID = a.Session.GetStreamID()
	}

	// Create timestamped packet
	tsPkt := types.TimestampedPacket{
		Data:         rtpPkt.Payload,
		CaptureTime:  now,
		PTS:          pts,
		DTS:          pts, // Will be adjusted for B-frames later
		StreamID:     streamID,
		SSRC:         rtpPkt.SSRC,
		SeqNum:       rtpPkt.SequenceNumber,
		Type:         packetType,
		Codec:        a.codecType,
	}
	
	// Calculate arrival delta
	a.mu.Lock()
	if !a.lastPacketTime.IsZero() {
		tsPkt.ArrivalDelta = now.Sub(a.lastPacketTime).Microseconds()
	}
	a.lastPacketTime = now
	
	// Detect packet loss
	if a.lastSeqNum != 0 {
		expectedSeq := a.lastSeqNum + 1
		if rtpPkt.SequenceNumber != expectedSeq {
			// Mark potential corruption due to packet loss
			if rtpPkt.SequenceNumber > expectedSeq {
				tsPkt.Flags |= types.PacketFlagCorrupted
			}
		}
	}
	a.lastSeqNum = rtpPkt.SequenceNumber
	a.lastTimestamp = rtpPkt.Timestamp
	a.mu.Unlock()
	
	// Analyze packet for keyframes and frame boundaries
	a.analyzePacket(&tsPkt, rtpPkt)
	
	// Send to output channel
	select {
	case outputChan <- tsPkt:
		return nil
	case <-a.ctx.Done():
		return a.ctx.Err()
	default:
		// Channel full, drop packet
		a.logger.Warn("Output channel full, dropping packet",
			"type", packetType.String(),
			"stream_id", a.Session.GetStreamID())
		return nil
	}
}

// analyzePacket uses the depacketizer to extract frame information
func (a *RTPConnectionAdapter) analyzePacket(tsPkt *types.TimestampedPacket, rtpPkt *rtp.Packet) {
	// Skip analysis for audio packets
	if a.isAudioTrack {
		// Audio frames are typically smaller and don't have keyframes
		// RTP marker bit often indicates end of audio frame
		if rtpPkt.Marker {
			tsPkt.Flags |= types.PacketFlagFrameEnd
		}
		return
	}
	
	// This is simplified - real implementation would use the depacketizer
	// to properly analyze NAL units
	
	if len(rtpPkt.Payload) == 0 {
		return
	}
	
	// Check for keyframe based on codec
	switch a.codecType {
	case types.CodecH264:
		nalType := rtpPkt.Payload[0] & 0x1F
		if nalType == 5 || nalType == 7 || nalType == 8 { // IDR, SPS, PPS
			tsPkt.Flags |= types.PacketFlagKeyframe
		}
		
	case types.CodecHEVC:
		if len(rtpPkt.Payload) >= 2 {
			nalType := (rtpPkt.Payload[0] >> 1) & 0x3F
			if nalType >= 16 && nalType <= 21 { // IRAP NAL units
				tsPkt.Flags |= types.PacketFlagKeyframe
			}
		}
	}
	
	// Use RTP marker bit as potential frame end indicator
	if rtpPkt.Marker {
		tsPkt.Flags |= types.PacketFlagFrameEnd
	}
}


// SetDepacketizer sets the depacketizer for frame analysis
func (a *RTPConnectionAdapter) SetDepacketizer(depacketizer codec.Depacketizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.depacketizer = depacketizer
}
