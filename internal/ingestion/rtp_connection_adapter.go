package ingestion

import (
	"context"
	"fmt"
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
	depacketizer   codec.Depacketizer
	lastSeqNum     uint16
	lastTimestamp  uint32
	lastPacketTime time.Time

	// Output channels for video-aware pipeline
	videoOutput chan types.TimestampedPacket
	audioOutput chan types.TimestampedPacket

	// Data buffer for Read() method
	dataBuffer chan []byte
	readBuffer []byte

	// Track information
	isAudioTrack bool
	codecType    types.CodecType

	// Context for lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	logger logger.Logger
	mu     sync.RWMutex
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
		dataBuffer:   make(chan []byte, 1000),
		codecType:    codecType,
		isAudioTrack: codecType.IsAudio(),
		ctx:          ctx,
		logger:       logger.NewLogrusAdapter(logger.FromContext(ctx).WithField("stream_id", session.GetStreamID())),
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

	// Set up NAL callback to receive depacketized data from RTP session
	adapter.logger.Info("RTP Adapter: Setting up NAL callback")
	session.SetNALCallback(adapter.handleNALUnits)
	adapter.logger.Info("RTP Adapter: NAL callback set successfully")

	// Start packet processor in background
	go adapter.processPackets()

	adapter.logger.Info("RTP Adapter: Created successfully")
	return adapter
}

// handleNALUnits receives NAL units from the RTP session and converts them to TimestampedPackets
func (a *RTPConnectionAdapter) handleNALUnits(nalUnits [][]byte) error {
	a.logger.WithField("nal_count", len(nalUnits)).Info("RTP Adapter: Received NAL units")

	now := time.Now()

	for i, nalUnit := range nalUnits {
		if len(nalUnit) == 0 {
			continue
		}

		// Add start code prefix (0x00000001) for H.264/HEVC NAL units
		// This is required for proper frame boundary detection
		nalWithStartCode := make([]byte, len(nalUnit)+4)
		nalWithStartCode[0] = 0x00
		nalWithStartCode[1] = 0x00
		nalWithStartCode[2] = 0x00
		nalWithStartCode[3] = 0x01
		copy(nalWithStartCode[4:], nalUnit)

		// Buffer for legacy Read() method
		select {
		case a.dataBuffer <- nalWithStartCode:
			a.logger.WithFields(map[string]interface{}{
				"nal_index": i,
				"nal_size":  len(nalWithStartCode),
			}).Debug("RTP Adapter: NAL unit buffered for Read()")
		default:
			a.logger.Warn("RTP Adapter: Data buffer full, dropping NAL unit from Read() buffer")
		}

		// Create TimestampedPacket for video pipeline
		streamID := "rtp-stream"
		if a.Session != nil {
			streamID = a.Session.GetStreamID()
		}

		tsPkt := types.TimestampedPacket{
			Data:        nalWithStartCode,
			CaptureTime: now,
			StreamID:    streamID,
			Type:        types.PacketTypeVideo, // NAL units are always video
			Codec:       a.codecType,
			// Use session timestamp if available
			PTS: int64(now.UnixNano() / 1000), // Convert to microseconds for now
			DTS: int64(now.UnixNano() / 1000),
		}

		// Analyze NAL unit for keyframes
		a.analyzeNALUnit(&tsPkt, nalUnit)

		// Send to video output channel for stream handler
		select {
		case a.videoOutput <- tsPkt:
			a.logger.WithFields(map[string]interface{}{
				"nal_index":   i,
				"nal_size":    len(nalWithStartCode),
				"stream_id":   streamID,
				"pts":         tsPkt.PTS,
				"is_keyframe": (tsPkt.Flags & types.PacketFlagKeyframe) != 0,
			}).Info("RTP Adapter: NAL unit sent to video pipeline")
		default:
			a.logger.WithFields(map[string]interface{}{
				"nal_index": i,
				"nal_size":  len(nalWithStartCode),
				"stream_id": streamID,
			}).Warn("RTP Adapter: Video output channel full, dropping NAL unit")
		}
	}

	return nil
}

// GetStreamID implements StreamConnection
func (a *RTPConnectionAdapter) GetStreamID() string {
	return a.Session.GetStreamID()
}

// Read implements StreamConnection
func (a *RTPConnectionAdapter) Read(buf []byte) (int, error) {
	a.logger.WithField("buffer_size", len(buf)).Info("RTP Adapter: Read() called")

	// If we have leftover data from previous read, use it first
	if len(a.readBuffer) > 0 {
		n := copy(buf, a.readBuffer)
		a.readBuffer = a.readBuffer[n:]
		a.logger.WithFields(map[string]interface{}{
			"bytes_from_buffer": n,
			"remaining_buffer":  len(a.readBuffer),
		}).Debug("RTP Adapter: Read from existing buffer")
		return n, nil
	}

	// Try to get new data from the channel
	select {
	case nalData := <-a.dataBuffer:
		// Copy as much as we can to the provided buffer
		n := copy(buf, nalData)

		// If there's leftover data, store it for the next read
		if n < len(nalData) {
			a.readBuffer = nalData[n:]
		}

		a.logger.WithFields(map[string]interface{}{
			"bytes_read":      n,
			"nal_data_size":   len(nalData),
			"leftover_buffer": len(a.readBuffer),
		}).Debug("RTP Adapter: Read new NAL data")

		return n, nil

	case <-time.After(100 * time.Millisecond):
		// Timeout to prevent blocking indefinitely
		a.logger.Debug("RTP Adapter: Read timeout, no data available")
		return 0, fmt.Errorf("no data available")

	case <-a.ctx.Done():
		a.logger.Debug("RTP Adapter: Read cancelled due to context")
		return 0, a.ctx.Err()
	}
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
		Data:        rtpPkt.Payload,
		CaptureTime: now,
		PTS:         pts,
		DTS:         pts, // Will be adjusted for B-frames later
		StreamID:    streamID,
		SSRC:        rtpPkt.SSRC,
		SeqNum:      rtpPkt.SequenceNumber,
		Type:        packetType,
		Codec:       a.codecType,
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

// analyzeNALUnit analyzes a NAL unit for keyframe detection
func (a *RTPConnectionAdapter) analyzeNALUnit(tsPkt *types.TimestampedPacket, nalUnit []byte) {
	if len(nalUnit) == 0 {
		return
	}

	// Analyze based on codec type
	switch a.codecType {
	case types.CodecH264:
		// H.264 NAL unit type is in the first 5 bits
		nalType := nalUnit[0] & 0x1F
		switch nalType {
		case 5: // IDR slice
			tsPkt.Flags |= types.PacketFlagKeyframe
			tsPkt.Flags |= types.PacketFlagFrameStart
		case 7: // SPS
			tsPkt.Flags |= types.PacketFlagKeyframe
		case 8: // PPS
			tsPkt.Flags |= types.PacketFlagKeyframe
		case 1, 2, 3, 4: // Non-IDR slices
			tsPkt.Flags |= types.PacketFlagFrameStart
		}

	case types.CodecHEVC:
		// HEVC NAL unit type is in bits 1-6 of the first byte
		if len(nalUnit) >= 2 {
			nalType := (nalUnit[0] >> 1) & 0x3F
			if nalType >= 16 && nalType <= 21 { // IRAP NAL units (IDR, CRA, BLA)
				tsPkt.Flags |= types.PacketFlagKeyframe
				tsPkt.Flags |= types.PacketFlagFrameStart
			} else if nalType >= 32 && nalType <= 34 { // VPS, SPS, PPS
				tsPkt.Flags |= types.PacketFlagKeyframe
			} else if nalType <= 9 { // VCL NAL units
				tsPkt.Flags |= types.PacketFlagFrameStart
			}
		}

	case types.CodecAV1:
		// AV1 OBU analysis would be more complex
		// For now, mark all as potential frame starts
		tsPkt.Flags |= types.PacketFlagFrameStart
	}
}

// SetDepacketizer sets the depacketizer for frame analysis
func (a *RTPConnectionAdapter) SetDepacketizer(depacketizer codec.Depacketizer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.depacketizer = depacketizer
}
