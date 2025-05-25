package ingestion

import (
	"context"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/mpegts"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/ingestion/timestamp"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// SRTConnectionAdapter adapts srt.Connection to implement StreamConnection and SRTConnection interfaces
// Now also parses MPEG-TS and emits TimestampedPackets
type SRTConnectionAdapter struct {
	*srt.Connection
	
	// MPEG-TS parsing
	mpegtsParser    *mpegts.Parser
	timestampMapper *timestamp.TimestampMapper
	
	// Output channels for video-aware pipeline
	videoOutput     chan types.TimestampedPacket
	audioOutput     chan types.TimestampedPacket
	
	// Context for lifecycle
	ctx             context.Context
	cancel          context.CancelFunc
	
	// State tracking
	lastPCR         int64
	pcrWallTime     time.Time
	videoPID        uint16
	audioPID        uint16
	
	// B-frame detection and handling
	hasBFrames           bool
	frameReorderingDelay int64 // in 90kHz units
	lastFramePTS         int64 // For B-frame detection
	frameCount           int   // Count frames for detection window
	bFrameDetected       bool  // Detection complete flag
	
	logger          logger.Logger
	mu              sync.RWMutex
	wg              sync.WaitGroup
}

// Ensure it implements both interfaces
var _ StreamConnection = (*SRTConnectionAdapter)(nil)
var _ SRTConnection = (*SRTConnectionAdapter)(nil)

// NewSRTConnectionAdapter creates a new adapter
func NewSRTConnectionAdapter(conn *srt.Connection) *SRTConnectionAdapter {
	if conn == nil {
		return nil
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	adapter := &SRTConnectionAdapter{
		Connection:      conn,
		mpegtsParser:    mpegts.NewParser(),
		timestampMapper: timestamp.NewTimestampMapper(90000), // MPEG-TS uses 90kHz
		videoOutput:     make(chan types.TimestampedPacket, 1000),
		audioOutput:     make(chan types.TimestampedPacket, 1000),
		ctx:             ctx,
		cancel:          cancel,
		logger:          logger.FromContext(ctx).WithField("stream_id", conn.GetStreamID()),
	}
	
	// Start processing in background
	adapter.wg.Add(1)
	go adapter.processData()
	
	return adapter
}

// GetStreamID implements StreamConnection
func (a *SRTConnectionAdapter) GetStreamID() string {
	return a.Connection.GetStreamID()
}

// Read implements StreamConnection
func (a *SRTConnectionAdapter) Read(buf []byte) (int, error) {
	return a.Connection.Read(buf)
}

// Close implements StreamConnection
func (a *SRTConnectionAdapter) Close() error {
	a.cancel()
	a.wg.Wait()
	close(a.videoOutput)
	close(a.audioOutput)
	return a.Connection.Close()
}

// GetMaxBW returns the current max bandwidth setting
func (a *SRTConnectionAdapter) GetMaxBW() int64 {
	return a.Connection.GetMaxBW()
}

// SetMaxBW sets the max bandwidth for backpressure
func (a *SRTConnectionAdapter) SetMaxBW(bw int64) error {
	return a.Connection.SetMaxBW(bw)
}

// GetVideoOutput returns the channel of video TimestampedPackets
func (a *SRTConnectionAdapter) GetVideoOutput() <-chan types.TimestampedPacket {
	return a.videoOutput
}

// GetAudioOutput returns the channel of audio TimestampedPackets
func (a *SRTConnectionAdapter) GetAudioOutput() <-chan types.TimestampedPacket {
	return a.audioOutput
}

// processData reads from SRT connection and parses MPEG-TS
func (a *SRTConnectionAdapter) processData() {
	defer a.wg.Done()
	
	buffer := make([]byte, mpegts.PacketSize*7) // Read 7 TS packets at a time
	
	for {
		select {
		case <-a.ctx.Done():
			return
		default:
			// Read data from SRT connection
			n, err := a.Connection.Read(buffer)
			if err != nil {
				if a.ctx.Err() == nil {
					a.logger.WithError(err).Error("Failed to read from SRT connection")
				}
				return
			}
			
			// Parse MPEG-TS packets
			packets, err := a.mpegtsParser.Parse(buffer[:n])
			if err != nil {
				a.logger.WithError(err).Debug("Failed to parse MPEG-TS")
				continue
			}
			
			// Process each packet
			for _, tsPkt := range packets {
				a.processTSPacket(tsPkt)
			}
		}
	}
}

// processTSPacket converts MPEG-TS packet to TimestampedPacket
func (a *SRTConnectionAdapter) processTSPacket(tsPkt *mpegts.Packet) {
	// Update PCR for synchronization
	if tsPkt.HasPCR {
		a.mu.Lock()
		a.lastPCR = tsPkt.PCR
		a.pcrWallTime = time.Now()
		a.mu.Unlock()
	}
	
	// Only process packets with payload
	if !tsPkt.PayloadExists || len(tsPkt.Payload) == 0 {
		return
	}
	
	// Determine packet type
	packetType := types.PacketTypeData
	if a.mpegtsParser.IsVideoPID(tsPkt.PID) {
		packetType = types.PacketTypeVideo
	} else if a.mpegtsParser.IsAudioPID(tsPkt.PID) {
		packetType = types.PacketTypeAudio
	}
	
	// Skip non-media packets
	if packetType == types.PacketTypeData {
		return
	}
	
	// Create timestamped packet
	now := time.Now()
	tspkt := types.TimestampedPacket{
		Data:        tsPkt.Payload,
		CaptureTime: now,
		StreamID:    a.GetStreamID(),
		Type:        packetType,
	}
	
	// Use PTS if available, otherwise calculate from PCR
	if tsPkt.HasPTS {
		tspkt.PTS = tsPkt.PTS
		if tsPkt.HasDTS {
			tspkt.DTS = tsPkt.DTS
		} else {
			// DTS calculation with dynamic B-frame detection
			if packetType == types.PacketTypeVideo {
				// Detect B-frames during initial frames
				a.detectBFrames(tspkt.PTS)
				
				if a.hasBFrames {
					// Apply reordering delay for B-frames
					tspkt.DTS = tspkt.PTS - a.frameReorderingDelay
				} else {
					// No B-frames detected, DTS = PTS
					tspkt.DTS = tspkt.PTS
				}
			} else {
				// For audio, DTS = PTS
				tspkt.DTS = tspkt.PTS
			}
		}
	} else {
		// Estimate PTS from PCR
		a.mu.RLock()
		if a.lastPCR > 0 && !a.pcrWallTime.IsZero() {
			// Calculate elapsed time since last PCR
			elapsed := now.Sub(a.pcrWallTime)
			// Convert to 90kHz units
			elapsedPTS := int64(elapsed.Seconds() * 90000)
			tspkt.PTS = a.lastPCR + elapsedPTS
			
			// DTS calculation with dynamic B-frame detection
			if packetType == types.PacketTypeVideo {
				// Detect B-frames during initial frames
				a.detectBFrames(tspkt.PTS)
				
				if a.hasBFrames {
					// Apply reordering delay for B-frames
					tspkt.DTS = tspkt.PTS - a.frameReorderingDelay
				} else {
					// No B-frames detected, DTS = PTS
					tspkt.DTS = tspkt.PTS
				}
			} else {
				// For audio, DTS = PTS
				tspkt.DTS = tspkt.PTS
			}
		}
		a.mu.RUnlock()
	}
	
	// Detect keyframes in video packets
	if packetType == types.PacketTypeVideo && tsPkt.PayloadStart {
		// Simple keyframe detection - look for start codes
		if a.isKeyframe(tsPkt.Payload) {
			tspkt.Flags |= types.PacketFlagKeyframe
		}
		tspkt.Flags |= types.PacketFlagFrameStart
	}
	
	// Send to appropriate output channel
	var outputChan chan types.TimestampedPacket
	if packetType == types.PacketTypeVideo {
		outputChan = a.videoOutput
	} else {
		outputChan = a.audioOutput
	}
	
	select {
	case outputChan <- tspkt:
	case <-a.ctx.Done():
		return
	default:
		// Channel full, drop packet
		a.logger.Warn("Output channel full, dropping packet",
			"type", packetType.String(),
			"stream_id", a.GetStreamID())
	}
}

// isKeyframe performs simple keyframe detection
func (a *SRTConnectionAdapter) isKeyframe(data []byte) bool {
	// Look for H.264/HEVC NAL units in PES payload
	// This is simplified - real implementation would properly parse PES
	
	// Skip PES header
	if len(data) < 9 {
		return false
	}
	
	// Look for start codes and check NAL type
	for i := 0; i < len(data)-4; i++ {
		if data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			// Found start code, check NAL type
			if i+3 < len(data) {
				nalType := data[i+3] & 0x1F // H.264
				if nalType == 5 || nalType == 7 || nalType == 8 {
					return true
				}
			}
		}
	}
	
	return false
}

// SetPIDs sets the PIDs for video and audio streams
func (a *SRTConnectionAdapter) SetPIDs(videoPID, audioPID uint16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	a.videoPID = videoPID
	a.audioPID = audioPID
	
	a.mpegtsParser.SetVideoPID(videoPID)
	a.mpegtsParser.SetAudioPID(audioPID)
}

// detectBFrames analyzes PTS values to detect if stream contains B-frames
func (a *SRTConnectionAdapter) detectBFrames(currentPTS int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	// Skip if already detected
	if a.bFrameDetected {
		return
	}
	
	// Need at least 2 frames to detect
	if a.frameCount == 0 {
		a.lastFramePTS = currentPTS
		a.frameCount++
		return
	}
	
	// B-frames are present if PTS goes backwards
	// I P B B -> PTS order: 0, 3, 1, 2 (B-frames have lower PTS than previous P-frame)
	if currentPTS < a.lastFramePTS {
		a.hasBFrames = true
		a.bFrameDetected = true
		
		// Common configurations:
		// - 2 B-frames between references: delay = 2 frames
		// - 3 B-frames between references: delay = 3 frames
		// Use 2 as default (most common)
		framesDelay := int64(2)
		// At 30fps: ~3003 per frame, at 25fps: 3600 per frame
		// Use 3600 for safety
		a.frameReorderingDelay = framesDelay * 3600
		
		a.logger.WithFields(map[string]interface{}{
			"stream_id": a.GetStreamID(),
			"delay_ms":  a.frameReorderingDelay * 1000 / 90000,
		}).Info("B-frames detected in video stream")
	}
	
	a.lastFramePTS = currentPTS
	a.frameCount++
	
	// After analyzing first 30 frames, assume no B-frames if not detected
	if a.frameCount >= 30 && !a.hasBFrames {
		a.bFrameDetected = true
		a.logger.WithField("stream_id", a.GetStreamID()).Info("No B-frames detected in video stream")
	}
}
