package frame

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

var (
	// ErrNoFrameContext indicates packets received without frame context
	ErrNoFrameContext = errors.New("no frame context - received packet without active frame assembly (possible fragmented/incomplete frame start)")

	// ErrFrameTimeout indicates frame assembly timeout
	ErrFrameTimeout = errors.New("frame assembly timeout")

	// ErrOutputBlocked indicates output channel is blocked
	ErrOutputBlocked = errors.New("output channel blocked")
)

// Assembler assembles complete frames from packets
type Assembler struct {
	streamID string
	codec    types.CodecType

	// Current frame being assembled
	currentFrame *types.VideoFrame
	framePackets []types.TimestampedPacket
	nalBuffer    []byte
	frameTimeout time.Duration

	// Frame detection
	frameDetector Detector

	// Output
	output chan *types.VideoFrame

	// Context
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics
	framesAssembled uint64
	framesDropped   uint64
	packetsReceived uint64
	packetsDropped  uint64

	// Frame ID generation
	nextFrameID uint64

	logger    logger.Logger
	mu        sync.Mutex
	closeOnce sync.Once
}

// NewAssembler creates a new frame assembler
func NewAssembler(streamID string, codec types.CodecType, outputBufferSize int) *Assembler {
	if outputBufferSize <= 0 {
		outputBufferSize = 100
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create appropriate frame detector
	factory := NewDetectorFactory()
	detector := factory.CreateDetector(codec)
	if detector == nil {
		// Fallback to generic detector
		detector = &GenericDetector{codec: codec}
	}

	return &Assembler{
		streamID:      streamID,
		codec:         codec,
		frameTimeout:  200 * time.Millisecond,
		frameDetector: detector,
		output:        make(chan *types.VideoFrame, outputBufferSize),
		ctx:           ctx,
		cancel:        cancel,
		logger:        logger.NewLogrusAdapter(logger.FromContext(ctx).WithField("stream_id", streamID)),
		nextFrameID:   1,
	}
}

// Start starts the assembler
func (a *Assembler) Start() error {
	a.logger.Info("Frame assembler started")
	return nil
}

// Stop stops the assembler
func (a *Assembler) Stop() error {
	a.cancel()

	// Close output channel safely with sync.Once
	a.closeOnce.Do(func() {
		close(a.output)
	})

	// Get stats with proper locking
	a.mu.Lock()
	stats := map[string]interface{}{
		"frames_assembled": a.framesAssembled,
		"frames_dropped":   a.framesDropped,
		"packets_received": a.packetsReceived,
		"packets_dropped":  a.packetsDropped,
	}
	a.mu.Unlock()

	a.logger.WithFields(stats).Info("Frame assembler stopped")

	return nil
}

// AddPacket adds a packet to the assembler
func (a *Assembler) AddPacket(pkt types.TimestampedPacket) error {
	// Check if context is cancelled first (before acquiring lock)
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	default:
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.packetsReceived++

	// Debug log incoming packet
	a.logger.WithFields(map[string]interface{}{
		"packet_num":      a.packetsReceived,
		"pts":             pkt.PTS,
		"dts":             pkt.DTS,
		"data_len":        len(pkt.Data),
		"flags":           pkt.Flags,
		"is_keyframe":     pkt.IsKeyframe(),
		"has_frame_start": pkt.HasFlag(types.PacketFlagFrameStart),
		"has_frame_end":   pkt.HasFlag(types.PacketFlagFrameEnd),
		"first_16_bytes":  fmt.Sprintf("%02x", pkt.Data[:min(16, len(pkt.Data))]),
	}).Debug("FRAME ASSEMBLER: AddPacket called")

	// Detect frame boundaries
	isStart, isEnd := a.frameDetector.DetectBoundaries(&pkt)

	a.logger.WithFields(map[string]interface{}{
		"is_start":    isStart,
		"is_end":      isEnd,
		"has_current": a.currentFrame != nil,
	}).Debug("FRAME ASSEMBLER: Boundary detection result")

	// Handle frame start
	if isStart {
		// Complete current frame if exists
		if a.currentFrame != nil {
			a.logger.WithFields(map[string]interface{}{
				"current_frame_id": a.currentFrame.ID,
				"current_pts":      a.currentFrame.PTS,
				"nal_buffer_size":  len(a.nalBuffer),
			}).Debug("FRAME ASSEMBLER: Completing previous frame due to new start")

			if err := a.completeFrame(); err != nil {
				a.logger.WithError(err).Debug("Failed to complete frame on new start")
				// Note: buffers are already cleared by defer in completeFrame()
			}
		}

		// Always start new frame, regardless of previous frame completion
		a.currentFrame = &types.VideoFrame{
			ID:          a.nextFrameID,
			StreamID:    a.streamID,
			FrameNumber: a.nextFrameID, // Will be updated if known
			PTS:         pkt.PTS,
			DTS:         pkt.DTS,
			CaptureTime: pkt.CaptureTime,
			NALUnits:    make([]types.NALUnit, 0),
		}
		a.nextFrameID++

		// Set keyframe flag if detected
		if pkt.IsKeyframe() {
			a.currentFrame.SetFlag(types.FrameFlagKeyframe)
		}

		a.framePackets = []types.TimestampedPacket{pkt}
		a.nalBuffer = make([]byte, len(pkt.Data))
		copy(a.nalBuffer, pkt.Data)

		a.logger.WithFields(map[string]interface{}{
			"frame_id":    a.currentFrame.ID,
			"pts":         a.currentFrame.PTS,
			"dts":         a.currentFrame.DTS,
			"is_keyframe": pkt.IsKeyframe(),
			"data_len":    len(pkt.Data),
		}).Debug("FRAME ASSEMBLER: Started new frame")

	} else if a.currentFrame != nil {
		// Add to current frame
		a.framePackets = append(a.framePackets, pkt)

		oldBufferSize := len(a.nalBuffer)
		a.nalBuffer = append(a.nalBuffer, pkt.Data...)

		// Update frame timing if needed
		if pkt.PTS > a.currentFrame.PTS {
			// Calculate duration
			a.currentFrame.Duration = pkt.PTS - a.currentFrame.PTS
		}

		a.logger.WithFields(map[string]interface{}{
			"frame_id":        a.currentFrame.ID,
			"packet_count":    len(a.framePackets),
			"old_buffer_size": oldBufferSize,
			"new_buffer_size": len(a.nalBuffer),
			"data_appended":   len(pkt.Data),
			"frame_duration":  a.currentFrame.Duration,
		}).Debug("FRAME ASSEMBLER: Added packet to current frame")

	} else {
		// No frame context - we're starting mid-stream
		// Instead of erroring, silently wait for the next frame start (keyframe)
		// This is normal when joining a live stream
		a.packetsDropped++
		a.logger.WithFields(map[string]interface{}{
			"pts":           pkt.PTS,
			"is_keyframe":   pkt.IsKeyframe(),
			"total_dropped": a.packetsDropped,
		}).Debug("FRAME ASSEMBLER: Dropping packet while waiting for frame start (normal for mid-stream join)")
		return nil // Return nil instead of ErrNoFrameContext to avoid error logging
	}

	// Handle frame end
	if isEnd && a.currentFrame != nil {
		a.logger.WithFields(map[string]interface{}{
			"frame_id":     a.currentFrame.ID,
			"packet_count": len(a.framePackets),
			"buffer_size":  len(a.nalBuffer),
		}).Debug("FRAME ASSEMBLER: Frame end detected, completing frame")
		return a.completeFrame()
	}

	// Check for timeout
	if a.currentFrame != nil && time.Since(a.currentFrame.CaptureTime) > a.frameTimeout {
		a.currentFrame.SetFlag(types.FrameFlagCorrupted)
		a.logger.WithFields(map[string]interface{}{
			"frame_id":   a.currentFrame.ID,
			"age_ms":     time.Since(a.currentFrame.CaptureTime).Milliseconds(),
			"timeout_ms": a.frameTimeout.Milliseconds(),
		}).Warn("FRAME ASSEMBLER: Frame assembly timeout")
		// Try to complete the frame but don't propagate errors
		// The defer in completeFrame ensures cleanup happens
		_ = a.completeFrame()
	}

	return nil
}

// completeFrame finalizes and outputs the current frame
func (a *Assembler) completeFrame() error {
	if a.currentFrame == nil {
		return nil
	}

	// Ensure buffers are cleared even on error
	defer func() {
		a.currentFrame = nil
		a.framePackets = nil
		a.nalBuffer = nil
	}()

	// Parse NAL units from buffer
	nalUnits := a.parseNALUnits(a.nalBuffer)
	a.currentFrame.NALUnits = nalUnits

	// Determine frame type
	a.currentFrame.Type = a.frameDetector.GetFrameType(nalUnits)

	// Update flags based on frame type
	if a.currentFrame.Type.IsKeyframe() {
		a.currentFrame.SetFlag(types.FrameFlagKeyframe)
	}
	if a.currentFrame.Type.IsReference() {
		a.currentFrame.SetFlag(types.FrameFlagReference)
	}

	// Calculate total size
	for _, nal := range nalUnits {
		a.currentFrame.TotalSize += len(nal.Data)
	}

	// Set completion time
	a.currentFrame.CompleteTime = time.Now()

	// Calculate presentation time if not set
	if a.currentFrame.PresentationTime.IsZero() && a.currentFrame.PTS > 0 {
		// This would be calculated based on stream time base
		// For now, use capture time as approximation
		a.currentFrame.PresentationTime = a.currentFrame.CaptureTime
	}

	// Send to output with proper timeout handling
	return a.sendFrameToOutput(a.currentFrame)
}

// sendFrameToOutput sends a frame to the output channel with proper timeout handling
func (a *Assembler) sendFrameToOutput(frame *types.VideoFrame) error {
	// First try non-blocking send
	select {
	case a.output <- frame:
		a.framesAssembled++
		return nil
	case <-a.ctx.Done():
		return a.ctx.Err()
	default:
		// Channel is blocked, try with timeout
	}

	// Create timer for output timeout - use frame timeout duration
	outputTimeout := a.frameTimeout
	if outputTimeout < 50*time.Millisecond {
		outputTimeout = 50 * time.Millisecond // Minimum timeout
	}
	if outputTimeout > 500*time.Millisecond {
		outputTimeout = 500 * time.Millisecond // Maximum timeout
	}

	timer := time.NewTimer(outputTimeout)
	defer timer.Stop()

	select {
	case a.output <- frame:
		a.framesAssembled++
		return nil
	case <-timer.C:
		a.framesDropped++
		a.logger.WithFields(map[string]interface{}{
			"frame_id":       frame.ID,
			"output_timeout": outputTimeout,
		}).Warn("Frame dropped due to output channel timeout")
		return ErrOutputBlocked
	case <-a.ctx.Done():
		return a.ctx.Err()
	}
}

// parseNALUnits extracts NAL units from buffer
func (a *Assembler) parseNALUnits(data []byte) []types.NALUnit {
	nalUnits := make([]types.NALUnit, 0)

	a.logger.WithFields(map[string]interface{}{
		"codec":       a.codec.String(),
		"data_len":    len(data),
		"first_bytes": fmt.Sprintf("%02x", data[:min(32, len(data))]),
	}).Debug("FRAME ASSEMBLER: Parsing NAL units from buffer")

	// Different parsing based on codec
	switch a.codec {
	case types.CodecH264, types.CodecHEVC:
		// Look for start codes
		units := a.findStartCodeUnits(data)
		a.logger.WithField("unit_count", len(units)).Debug("FRAME ASSEMBLER: Found start code units")

		for i, unitData := range units {
			if len(unitData) > 0 {
				nalType := a.getNALType(unitData)
				nalUnit := types.NALUnit{
					Type: nalType,
					Data: unitData,
				}

				a.logger.WithFields(map[string]interface{}{
					"unit_index":  i,
					"nal_type":    nalType,
					"unit_size":   len(unitData),
					"first_bytes": fmt.Sprintf("%02x", unitData[:min(8, len(unitData))]),
				}).Debug("FRAME ASSEMBLER: Parsed NAL unit")

				nalUnits = append(nalUnits, nalUnit)
			}
		}

	case types.CodecAV1:
		// Parse OBUs
		// Simplified - would use AV1 detector's parsing
		nalUnits = append(nalUnits, types.NALUnit{
			Type: 0,
			Data: data,
		})
		a.logger.Debug("FRAME ASSEMBLER: AV1 codec - treating as single unit")

	default:
		// Unknown codec, treat as single unit
		if len(data) > 0 {
			nalUnits = append(nalUnits, types.NALUnit{
				Type: 0,
				Data: data,
			})
			a.logger.Debug("FRAME ASSEMBLER: Unknown codec - treating as single unit")
		}
	}

	a.logger.WithField("total_nal_units", len(nalUnits)).Debug("FRAME ASSEMBLER: NAL unit parsing complete")
	return nalUnits
}

// findStartCodeUnits finds NAL units using start codes
func (a *Assembler) findStartCodeUnits(data []byte) [][]byte {
	units := make([][]byte, 0)

	i := 0
	for i < len(data)-3 {
		// Look for start code (0x00 0x00 0x01 or 0x00 0x00 0x00 0x01)
		if data[i] == 0 && data[i+1] == 0 {
			startCodeLen := 0
			if data[i+2] == 1 {
				startCodeLen = 3
			} else if i < len(data)-4 && data[i+2] == 0 && data[i+3] == 1 {
				startCodeLen = 4
			}

			if startCodeLen > 0 {
				// Found start code
				unitStart := i + startCodeLen
				unitEnd := len(data)

				// Find next start code
				for j := unitStart; j < len(data)-3; j++ {
					if data[j] == 0 && data[j+1] == 0 &&
						(data[j+2] == 1 || (j < len(data)-4 && data[j+2] == 0 && data[j+3] == 1)) {
						unitEnd = j
						break
					}
				}

				if unitEnd > unitStart {
					unitData := data[unitStart:unitEnd]
					units = append(units, unitData)

				}

				i = unitEnd
				continue
			}
		}
		i++
	}

	// If no start codes found, treat entire buffer as one unit
	if len(units) == 0 && len(data) > 0 {
		units = append(units, data)
	}

	return units
}

// getNALType extracts NAL unit type based on codec
func (a *Assembler) getNALType(data []byte) uint8 {
	if len(data) == 0 {
		return 0
	}

	switch a.codec {
	case types.CodecH264:
		return data[0] & 0x1F
	case types.CodecHEVC:
		return (data[0] >> 1) & 0x3F
	default:
		return 0
	}
}

// GetOutput returns the output channel
func (a *Assembler) GetOutput() <-chan *types.VideoFrame {
	return a.output
}

// SetFrameTimeout sets the frame assembly timeout
func (a *Assembler) SetFrameTimeout(timeout time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.frameTimeout = timeout
}

// GetStats returns assembler statistics
func (a *Assembler) GetStats() AssemblerStats {
	a.mu.Lock()
	defer a.mu.Unlock()

	return AssemblerStats{
		FramesAssembled: a.framesAssembled,
		FramesDropped:   a.framesDropped,
		PacketsReceived: a.packetsReceived,
		PacketsDropped:  a.packetsDropped,
	}
}

// AssemblerStats contains assembler statistics
type AssemblerStats struct {
	FramesAssembled uint64
	FramesDropped   uint64
	PacketsReceived uint64
	PacketsDropped  uint64
}

// GenericDetector is a fallback detector for unknown codecs
type GenericDetector struct {
	codec types.CodecType
}

func (g *GenericDetector) DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool) {
	// Use packet flags as hints
	isStart = pkt.HasFlag(types.PacketFlagFrameStart)
	isEnd = pkt.HasFlag(types.PacketFlagFrameEnd)
	return
}

func (g *GenericDetector) GetFrameType(nalUnits []types.NALUnit) types.FrameType {
	return types.FrameTypeP // Default
}

func (g *GenericDetector) IsKeyframe(data []byte) bool {
	return false
}

func (g *GenericDetector) GetCodec() types.CodecType {
	return g.codec
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
