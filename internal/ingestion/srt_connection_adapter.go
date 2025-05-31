package ingestion

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/mpegts"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/ingestion/timestamp"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// SRTConnectionAdapter adapts srt.Connection to implement StreamConnection and SRTConnection interfaces
// Now also parses MPEG-TS and emits TimestampedPackets with parameter set extraction
type SRTConnectionAdapter struct {
	*srt.Connection

	// MPEG-TS parsing
	mpegtsParser    *mpegts.Parser
	timestampMapper *timestamp.TimestampMapper

	// Output channels for video-aware pipeline
	videoOutput chan types.TimestampedPacket
	audioOutput chan types.TimestampedPacket

	// **NEW: Parameter set extraction**
	parameterSetCache *types.ParameterSetContext
	paramExtractorMu  sync.RWMutex

	// Context for lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// State tracking
	lastPCR     int64
	pcrWallTime time.Time
	videoPID    uint16
	audioPID    uint16

	// B-frame detection and handling
	hasBFrames           bool
	frameReorderingDelay int64 // in 90kHz units
	lastFramePTS         int64 // For B-frame detection
	frameCount           int   // Count frames for detection window
	bFrameDetected       bool  // Detection complete flag

	logger logger.Logger
	mu     sync.RWMutex
	wg     sync.WaitGroup
}

// Ensure it implements both interfaces
var (
	_ StreamConnection = (*SRTConnectionAdapter)(nil)
	_ SRTConnection    = (*SRTConnectionAdapter)(nil)
)

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// NewSRTConnectionAdapter creates a new adapter
func NewSRTConnectionAdapter(conn *srt.Connection) *SRTConnectionAdapter {
	if conn == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	adapter := &SRTConnectionAdapter{
		Connection:        conn,
		mpegtsParser:      mpegts.NewParser(),
		timestampMapper:   timestamp.NewTimestampMapper(90000), // MPEG-TS uses 90kHz
		videoOutput:       make(chan types.TimestampedPacket, 1000),
		audioOutput:       make(chan types.TimestampedPacket, 1000),
		parameterSetCache: types.NewParameterSetContext(types.CodecH264, conn.GetStreamID()), // Default to H.264
		ctx:               ctx,
		cancel:            cancel,
		logger:            logger.NewLogrusAdapter(logger.FromContext(ctx).WithField("stream_id", conn.GetStreamID())),
	}

	// Start processing in background
	adapter.wg.Add(1)
	go adapter.processData()

	adapter.logger.WithField("stream_id", conn.GetStreamID()).Debug("SRT connection adapter created successfully")
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

// GetDetectedVideoCodec returns the codec detected from MPEG-TS PMT
func (a *SRTConnectionAdapter) GetDetectedVideoCodec() types.CodecType {
	if a.mpegtsParser == nil {
		return types.CodecUnknown
	}

	streamType := a.mpegtsParser.GetVideoStreamType()
	switch streamType {
	case 0x1B: // H.264
		return types.CodecH264
	case 0x24: // HEVC
		return types.CodecHEVC
	case 0x51: // AV1
		return types.CodecAV1
	case 0x01, 0x02: // MPEG-1/2 Video
		return types.CodecMPV
	default:
		return types.CodecUnknown
	}
}

// processData reads from SRT connection and parses MPEG-TS
func (a *SRTConnectionAdapter) processData() {
	defer a.wg.Done()

	// Add panic recovery to catch any crashes
	defer func() {
		if r := recover(); r != nil {
			if a.logger != nil {
				a.logger.WithFields(map[string]interface{}{
					"panic":     r,
					"stream_id": "unknown",
				}).Error("PANIC in processData goroutine")
			}
		}
	}()

	// Cache stream ID to avoid repeated calls
	streamID := a.GetStreamID()

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   streamID,
		"context_err": a.ctx.Err(),
	}).Info("processData goroutine STARTED")

	// For Message API, use larger buffer to read complete messages
	buffer := make([]byte, 65536) // 64KB buffer for message API
	a.logger.WithField("stream_id", streamID).Info("Starting SRT data processing - MPEG-TS demuxing pipeline")

	consecutiveErrors := 0
	maxConsecutiveErrors := 10

	a.logger.WithField("stream_id", streamID).Info("About to enter SRT read loop")

	// SRT message mode read loop - simple blocking approach with exhaustive logging
	loopIteration := 0
	for {
		loopIteration++
		select {
		case <-a.ctx.Done():
			a.logger.WithField("stream_id", streamID).Info("SRT processData context cancelled")
			return
		default:
		}

		// Blocking SRT read - should wait for next message
		n, err := a.Connection.Read(buffer)
		if err != nil {
			a.logger.WithFields(map[string]interface{}{
				"stream_id":      streamID,
				"error":          err,
				"loop_iteration": loopIteration,
			}).Info("Read returned error, processing...")

			if err == io.EOF {
				a.logger.WithField("stream_id", streamID).Info("🏁 SRT stream ended normally (EOF)")
				return
			}

			consecutiveErrors++
			if consecutiveErrors >= maxConsecutiveErrors {
				a.logger.WithFields(map[string]interface{}{
					"stream_id":          streamID,
					"consecutive_errors": consecutiveErrors,
				}).Error("💥 Too many consecutive SRT read errors, terminating connection")
				return
			}

			a.logger.WithFields(map[string]interface{}{
				"error":              err,
				"stream_id":          streamID,
				"consecutive_errors": consecutiveErrors,
				"loop_iteration":     loopIteration,
			}).Warn("SRT read error, will retry after sleep")
			time.Sleep(100 * time.Millisecond)
			a.logger.WithField("stream_id", streamID).Info("⏰ Sleep completed, continuing to next iteration")
			continue
		}

		a.logger.WithFields(map[string]interface{}{
			"stream_id":      streamID,
			"bytes_read":     n,
			"loop_iteration": loopIteration,
		}).Info("Read completed without error, checking byte count")

		if n > 0 {
			consecutiveErrors = 0

			// Process the data immediately
			err = a.processMessage(buffer[:n], streamID)
			if err != nil {
				a.logger.WithError(err).WithField("stream_id", streamID).Warn("Failed to process SRT message")
			}
			continue
		} else {
			a.logger.WithFields(map[string]interface{}{
				"stream_id":      streamID,
				"loop_iteration": loopIteration,
			}).Warn("SRT read returned 0 bytes without error")
			time.Sleep(100 * time.Millisecond)
			a.logger.WithField("stream_id", streamID).Info("⏰ Zero bytes sleep completed, continuing")
			continue
		}
	}
}

// processMessage handles a single SRT message
func (a *SRTConnectionAdapter) processMessage(data []byte, streamID string) error {
	// Parse MPEG-TS packets
	a.logger.WithFields(map[string]interface{}{
		"stream_id":      streamID,
		"bytes_to_parse": len(data),
	}).Info("About to parse MPEG-TS data")

	// **NEW: Use enhanced MPEG-TS parsing with parameter set extraction**
	a.logger.WithFields(map[string]interface{}{
		"stream_id": streamID,
		"data_size": len(data),
	}).Info("🔍 TRANSPORT STREAM: About to call ParseWithExtractor")

	packets, err := a.mpegtsParser.ParseWithExtractor(data, a.parameterSetExtractor)

	a.logger.WithFields(map[string]interface{}{
		"stream_id":      streamID,
		"bytes_parsed":   len(data),
		"packets_parsed": len(packets),
		"error":          err,
	}).Info("MPEG-TS parsing result")

	if err != nil {
		a.logger.WithError(err).WithField("stream_id", streamID).Warn("Failed to parse MPEG-TS data")
		return err
	}

	// Process each packet
	for _, tsPkt := range packets {
		a.processTSPacket(tsPkt)
	}

	return nil
}

// processTSPacket converts MPEG-TS packet to TimestampedPacket
func (a *SRTConnectionAdapter) processTSPacket(tsPkt *mpegts.Packet) {
	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"pid":         tsPkt.PID,
		"has_payload": tsPkt.PayloadExists,
		"payload_len": len(tsPkt.Payload),
		"has_pcr":     tsPkt.HasPCR,
		"has_pts":     tsPkt.HasPTS,
	}).Info("Processing MPEG-TS packet")
	// Update PCR for synchronization
	if tsPkt.HasPCR {
		a.mu.Lock()
		a.lastPCR = tsPkt.PCR
		a.pcrWallTime = time.Now()
		a.mu.Unlock()
	}

	// Only process packets with payload
	if !tsPkt.PayloadExists || len(tsPkt.Payload) == 0 {
		a.logger.WithFields(map[string]interface{}{
			"stream_id":      a.GetStreamID(),
			"pid":            tsPkt.PID,
			"payload_exists": tsPkt.PayloadExists,
			"payload_len":    len(tsPkt.Payload),
		}).Info("Skipping packet - no payload")
		return
	}

	// Determine packet type
	packetType := types.PacketTypeData
	if a.mpegtsParser.IsVideoPID(tsPkt.PID) {
		packetType = types.PacketTypeVideo
	} else if a.mpegtsParser.IsAudioPID(tsPkt.PID) {
		packetType = types.PacketTypeAudio
	}

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"pid":         tsPkt.PID,
		"packet_type": packetType.String(),
		"is_video":    a.mpegtsParser.IsVideoPID(tsPkt.PID),
		"is_audio":    a.mpegtsParser.IsAudioPID(tsPkt.PID),
		"video_pid":   a.mpegtsParser.GetVideoPID(),
		"audio_pid":   a.mpegtsParser.GetAudioPID(),
		"pmt_pid":     a.mpegtsParser.GetPMTPID(),
	}).Info("Packet type determined")

	// Skip non-media packets
	if packetType == types.PacketTypeData {
		a.logger.WithFields(map[string]interface{}{
			"stream_id": a.GetStreamID(),
			"pid":       tsPkt.PID,
		}).Info("Skipping non-media packet")
		return
	}

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"pid":         tsPkt.PID,
		"packet_type": packetType.String(),
		"has_pts":     tsPkt.HasPTS,
		"pts":         tsPkt.PTS,
	}).Info("About to send packet to output channel")

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"packet_type": packetType.String(),
	}).Info("SRT ADAPTER: About to create TimestampedPacket")

	// Extract video bitstream data from PES packet
	videoData := a.extractVideoBitstream(tsPkt)

	// Create timestamped packet with extracted video data
	now := time.Now()
	tspkt := types.TimestampedPacket{
		Data:        videoData,
		CaptureTime: now,
		StreamID:    a.GetStreamID(),
		Type:        packetType,
	}

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"packet_type": packetType.String(),
		"data_len":    len(tspkt.Data),
	}).Info("SRT ADAPTER: TimestampedPacket created successfully")

	a.logger.WithFields(map[string]interface{}{
		"stream_id": a.GetStreamID(),
		"has_pts":   tsPkt.HasPTS,
		"pts_value": tsPkt.PTS,
	}).Info("SRT ADAPTER: About to process PTS/DTS")

	// Use PTS if available, otherwise calculate from PCR
	if tsPkt.HasPTS {
		a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: Using provided PTS")
		tspkt.PTS = tsPkt.PTS
		if tsPkt.HasDTS {
			a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: Using provided DTS")
			tspkt.DTS = tsPkt.DTS
		} else {
			a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: Calculating DTS from PTS")
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
		a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: PTS path completed")
	} else {
		a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: No PTS, estimating from PCR")
		// Estimate PTS from PCR
		a.mu.RLock()
		a.logger.WithFields(map[string]interface{}{
			"stream_id":          a.GetStreamID(),
			"last_pcr":           a.lastPCR,
			"pcr_wall_time_zero": a.pcrWallTime.IsZero(),
		}).Info("SRT ADAPTER: Checking PCR availability")
		if a.lastPCR > 0 && !a.pcrWallTime.IsZero() {
			// Calculate elapsed time since last PCR
			elapsed := now.Sub(a.pcrWallTime)
			// Convert to 90kHz units
			elapsedPTS := int64(elapsed.Seconds() * 90000)
			tspkt.PTS = a.lastPCR + elapsedPTS
		}
		a.mu.RUnlock()
		a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: Released PCR read lock")

		// DTS calculation with dynamic B-frame detection (done after releasing lock)
		if packetType == types.PacketTypeVideo && tspkt.PTS > 0 {
			a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: About to call detectBFrames")
			// Detect B-frames during initial frames
			a.detectBFrames(tspkt.PTS)
			a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: detectBFrames completed")

			a.mu.RLock()
			if a.hasBFrames {
				// Apply reordering delay for B-frames
				tspkt.DTS = tspkt.PTS - a.frameReorderingDelay
			} else {
				// No B-frames detected, DTS = PTS
				tspkt.DTS = tspkt.PTS
			}
			a.mu.RUnlock()
			a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: DTS calculation completed")
		} else {
			// For audio or if no PTS calculated, DTS = PTS
			tspkt.DTS = tspkt.PTS
			a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: No video PTS, using DTS = PTS")
		}

		a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: PCR estimation path completed")
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

	// Use completely non-blocking send to prevent any hanging
	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"packet_type": packetType.String(),
		"pts":         tspkt.PTS,
	}).Info("SRT ADAPTER: About to enter select statement")

	select {
	case outputChan <- tspkt:
		a.logger.WithFields(map[string]interface{}{
			"stream_id":          a.GetStreamID(),
			"packet_type":        packetType.String(),
			"pts":                tspkt.PTS,
			"data_len":           len(tspkt.Data),
			"channel_buffer_len": len(outputChan),
			"channel_buffer_cap": cap(outputChan),
		}).Info("SRT ADAPTER: Packet sent successfully")
	case <-a.ctx.Done():
		a.logger.WithField("stream_id", a.GetStreamID()).Info("SRT ADAPTER: Context cancelled")
		return
	default:
		// Channel is full, drop packet and continue
		a.logger.WithFields(map[string]interface{}{
			"stream_id":          a.GetStreamID(),
			"packet_type":        packetType.String(),
			"pts":                tspkt.PTS,
			"channel_buffer_len": len(outputChan),
			"channel_buffer_cap": cap(outputChan),
		}).Warn("SRT ADAPTER: Packet dropped - channel full")
	}

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   a.GetStreamID(),
		"packet_type": packetType.String(),
		"pts":         tspkt.PTS,
	}).Info("SRT ADAPTER: Finished channel send operation")
}

// isKeyframe performs simple keyframe detection
func (a *SRTConnectionAdapter) isKeyframe(data []byte) bool {
	// Look for H.264/HEVC NAL units in PES payload

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

	// B-frames are present if PTS goes backwards (but not due to wraparound)
	// I P B B -> PTS order: 0, 3, 1, 2 (B-frames have lower PTS than previous P-frame)
	//
	// MPEG-TS uses 33-bit PCR values that wrap at 2^33 (8,589,934,592) in 90kHz units
	// Wraparound detection: if difference is > 2^32, it's likely wraparound
	const maxPTSWrap = int64(1) << 32 // 2^32 = 4,294,967,296
	ptsDiff := a.lastFramePTS - currentPTS

	isWraparound := ptsDiff > maxPTSWrap
	isPTSBackwards := currentPTS < a.lastFramePTS && !isWraparound

	if isPTSBackwards {
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
			"stream_id":   a.GetStreamID(),
			"delay_ms":    a.frameReorderingDelay * 1000 / 90000,
			"current_pts": currentPTS,
			"last_pts":    a.lastFramePTS,
			"pts_diff":    ptsDiff,
		}).Info("B-frames detected in video stream")
	} else if isWraparound {
		// Handle PTS wraparound - continue without marking as B-frames
		a.logger.WithFields(map[string]interface{}{
			"stream_id":   a.GetStreamID(),
			"current_pts": currentPTS,
			"last_pts":    a.lastFramePTS,
			"pts_diff":    ptsDiff,
		}).Info("PTS wraparound detected, continuing normal operation")
	}

	a.lastFramePTS = currentPTS
	a.frameCount++

	// After analyzing first 30 frames, assume no B-frames if not detected
	if a.frameCount >= 30 && !a.hasBFrames {
		a.bFrameDetected = true
		a.logger.WithField("stream_id", a.GetStreamID()).Info("No B-frames detected in video stream")
	}
}

// parameterSetExtractor processes parameter sets extracted from MPEG-TS PMT and PES
func (a *SRTConnectionAdapter) parameterSetExtractor(parameterSets [][]byte, streamType uint8) {
	a.paramExtractorMu.Lock()
	defer a.paramExtractorMu.Unlock()

	streamID := a.GetStreamID()

	a.logger.WithFields(map[string]interface{}{
		"stream_id":   streamID,
		"stream_type": streamType,
		"param_sets":  len(parameterSets),
	}).Info("🔍 TRANSPORT STREAM: Parameter set extractor called")

	if a.parameterSetCache == nil {
		// Initialize parameter set cache based on detected stream type
		var codec types.CodecType
		switch streamType {
		case 0x1B:
			codec = types.CodecH264
		case 0x24:
			codec = types.CodecHEVC
		case 0x51:
			codec = types.CodecAV1
		default:
			codec = types.CodecH264 // Default fallback
		}
		a.parameterSetCache = types.NewParameterSetContext(codec, streamID)
		a.logger.WithFields(map[string]interface{}{
			"stream_id": streamID,
			"codec":     codec.String(),
		}).Info("🔍 TRANSPORT STREAM: Initialized parameter set cache")
	}

	// Process each parameter set
	for i, paramSet := range parameterSets {
		if len(paramSet) < 5 {
			a.logger.WithFields(map[string]interface{}{
				"stream_id":  streamID,
				"set_index":  i,
				"param_size": len(paramSet),
			}).Debug("🔍 TRANSPORT STREAM: Skipping parameter set - too small")
			continue
		}

		// Determine NAL unit type based on stream type
		var nalType uint8
		var nalStart int

		// Find start code and extract NAL type
		if paramSet[0] == 0x00 && paramSet[1] == 0x00 && paramSet[2] == 0x00 && paramSet[3] == 0x01 {
			nalStart = 4
		} else if paramSet[0] == 0x00 && paramSet[1] == 0x00 && paramSet[2] == 0x01 {
			nalStart = 3
		} else {
			a.logger.WithFields(map[string]interface{}{
				"stream_id":   streamID,
				"set_index":   i,
				"first_bytes": fmt.Sprintf("%02x %02x %02x %02x", paramSet[0], paramSet[1], paramSet[2], paramSet[3]),
			}).Debug("🔍 TRANSPORT STREAM: Invalid start code - skipping parameter set")
			continue
		}

		if nalStart >= len(paramSet) {
			continue
		}

		switch streamType {
		case 0x1B: // H.264
			nalType = paramSet[nalStart] & 0x1F
			a.logger.WithFields(map[string]interface{}{
				"stream_id":   streamID,
				"set_index":   i,
				"nal_type":    nalType,
				"param_size":  len(paramSet),
				"first_bytes": fmt.Sprintf("%02x", paramSet[nalStart:nalStart+min(8, len(paramSet)-nalStart)]),
			}).Info("🔍 TRANSPORT STREAM: Processing H.264 NAL unit")

			if nalType == 7 { // SPS
				if err := a.parameterSetCache.AddSPS(paramSet); err != nil {
					a.logger.WithError(err).WithFields(map[string]interface{}{
						"stream_id":  streamID,
						"set_index":  i,
						"param_size": len(paramSet),
					}).Warn("🔍 TRANSPORT STREAM: Failed to add H.264 SPS")
				} else {
					a.logger.WithFields(map[string]interface{}{
						"stream_id":  streamID,
						"set_index":  i,
						"param_size": len(paramSet),
					}).Info("🔍 TRANSPORT STREAM: ✅ Successfully extracted H.264 SPS")
				}
			} else if nalType == 8 { // PPS
				if err := a.parameterSetCache.AddPPS(paramSet); err != nil {
					a.logger.WithError(err).WithFields(map[string]interface{}{
						"stream_id":  streamID,
						"set_index":  i,
						"param_size": len(paramSet),
					}).Warn("🔍 TRANSPORT STREAM: Failed to add H.264 PPS")
				} else {
					a.logger.WithFields(map[string]interface{}{
						"stream_id":  streamID,
						"set_index":  i,
						"param_size": len(paramSet),
					}).Info("🔍 TRANSPORT STREAM: ✅ Successfully extracted H.264 PPS")
				}
			} else {
				a.logger.WithFields(map[string]interface{}{
					"stream_id": streamID,
					"set_index": i,
					"nal_type":  nalType,
				}).Debug("🔍 TRANSPORT STREAM: Non-parameter NAL unit (not SPS/PPS)")
			}

		case 0x24: // HEVC
			nalType = (paramSet[nalStart] >> 1) & 0x3F
			// HEVC parameter set handling would go here
			a.logger.WithFields(map[string]interface{}{
				"stream_id": streamID,
				"nal_type":  nalType,
			}).Debug("🔍 TRANSPORT STREAM: HEVC parameter set detected (handling not implemented)")

		case 0x51: // AV1
			// AV1 parameter set handling would go here
			a.logger.WithField("stream_id", streamID).
				Debug("🔍 TRANSPORT STREAM: AV1 parameter set detected (handling not implemented)")
		}
	}

	// Log current parameter set status with detailed breakdown
	if a.parameterSetCache != nil {
		stats := a.parameterSetCache.GetStatistics()

		// Get detailed parameter set inventory
		allSets := a.parameterSetCache.GetAllParameterSets()
		var spsIDs, ppsIDs []uint8

		if spsMaps, exists := allSets["sps"]; exists {
			for id := range spsMaps {
				spsIDs = append(spsIDs, id)
			}
		}
		if ppsMaps, exists := allSets["pps"]; exists {
			for id := range ppsMaps {
				ppsIDs = append(ppsIDs, id)
			}
		}

		a.logger.WithFields(map[string]interface{}{
			"stream_id":    streamID,
			"sps_count":    stats["sps_count"],
			"pps_count":    stats["pps_count"],
			"total_sets":   stats["total_sets"],
			"sps_ids":      spsIDs,
			"pps_ids":      ppsIDs,
			"last_updated": stats["last_updated"],
		}).Info("🔍 TRANSPORT STREAM: Parameter set cache status after update")
	}
}

// GetParameterSetCache returns the parameter set cache for external access
func (a *SRTConnectionAdapter) GetParameterSetCache() *types.ParameterSetContext {
	a.paramExtractorMu.RLock()
	defer a.paramExtractorMu.RUnlock()
	return a.parameterSetCache
}

// extractVideoBitstream extracts the actual video bitstream data from MPEG-TS packet
func (a *SRTConnectionAdapter) extractVideoBitstream(tsPkt *mpegts.Packet) []byte {
	// For non-video packets, return original payload
	if tsPkt.PID != a.mpegtsParser.GetVideoPID() || !tsPkt.PayloadExists || len(tsPkt.Payload) == 0 {
		return tsPkt.Payload
	}

	// If this is not a PES packet start, return the payload as-is (continuation data)
	if !tsPkt.PayloadStart {
		return tsPkt.Payload
	}

	// Parse PES header to extract video bitstream
	payload := tsPkt.Payload

	// Get stream ID safely for logging
	streamID := "unknown"
	if a.Connection != nil {
		streamID = a.GetStreamID()
	}

	// Validate PES start code (0x000001)
	if len(payload) < 9 || payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x01 {
		a.logger.WithFields(map[string]interface{}{
			"stream_id":    streamID,
			"payload_size": len(payload),
			"first_bytes":  fmt.Sprintf("%02x", payload[:min(4, len(payload))]),
		}).Warn("Invalid PES start code, returning original payload")
		return payload
	}

	// Extract PES header length (byte 8)
	if len(payload) < 9 {
		a.logger.WithField("stream_id", streamID).Warn("PES packet too short for header length")
		return payload
	}

	pesHeaderLength := int(payload[8])
	pesPayloadStart := 9 + pesHeaderLength

	// Validate bounds
	if pesPayloadStart >= len(payload) {
		a.logger.WithFields(map[string]interface{}{
			"stream_id":         streamID,
			"pes_header_length": pesHeaderLength,
			"payload_start":     pesPayloadStart,
			"total_size":        len(payload),
		}).Warn("PES payload start beyond packet boundary")
		return []byte{} // Return empty slice for invalid packets
	}

	// Extract the actual video bitstream (skip PES header)
	videoBitstream := payload[pesPayloadStart:]

	a.logger.WithFields(map[string]interface{}{
		"stream_id":         streamID,
		"original_size":     len(payload),
		"pes_header_length": pesHeaderLength,
		"bitstream_size":    len(videoBitstream),
		"first_bytes":       fmt.Sprintf("%02x", videoBitstream[:min(8, len(videoBitstream))]),
	}).Debug("Extracted video bitstream from PES packet")

	return videoBitstream
}
