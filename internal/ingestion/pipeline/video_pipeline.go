package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/frame"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// VideoPipeline represents the video-aware processing pipeline
type VideoPipeline struct {
	streamID string
	codec    types.CodecType

	// Pipeline stages
	frameAssembler  *frame.Assembler
	bframeReorderer *frame.BFrameReorderer

	// Input/Output
	input  <-chan types.TimestampedPacket
	output chan *types.VideoFrame

	// Context for lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	// Metrics (using atomic for thread-safety)
	packetsProcessed atomic.Uint64
	framesOutput     atomic.Uint64
	errors           atomic.Uint64
	framesReordered  atomic.Uint64

	logger        logger.Logger
	sampledLogger *logger.SampledLogger
	wg            sync.WaitGroup
	closeOnce     sync.Once
}

// Config holds pipeline configuration
type Config struct {
	StreamID              string
	Codec                 types.CodecType
	FrameBufferSize       int
	FrameAssemblyTimeout  int // milliseconds
	MaxBFrameReorderDepth int // Maximum B-frame reorder depth
	MaxReorderDelay       int // Maximum reorder delay in milliseconds
}

// NewVideoPipeline creates a new video processing pipeline
func NewVideoPipeline(ctx context.Context, cfg Config, input <-chan types.TimestampedPacket) (*VideoPipeline, error) {
	if cfg.StreamID == "" {
		return nil, fmt.Errorf("stream ID required")
	}

	if cfg.FrameBufferSize <= 0 {
		cfg.FrameBufferSize = 100
	}

	// Default B-frame reorder settings
	if cfg.MaxBFrameReorderDepth <= 0 {
		cfg.MaxBFrameReorderDepth = 3 // Default to 3 B-frames
	}
	if cfg.MaxReorderDelay <= 0 {
		cfg.MaxReorderDelay = 200 // Default 200ms
	}

	ctx, cancel := context.WithCancel(ctx)

	// Create logger
	logEntry := logger.FromContext(ctx).WithField("stream_id", cfg.StreamID)
	baseLogger := logger.NewLogrusAdapter(logEntry)

	// Create sampled logger for high-frequency video processing events
	sampledLogger := logger.NewVideoLogger(baseLogger)

	// Create frame assembler
	assembler := frame.NewAssembler(cfg.StreamID, cfg.Codec, cfg.FrameBufferSize)

	// Set frame timeout if specified
	if cfg.FrameAssemblyTimeout > 0 {
		assembler.SetFrameTimeout(time.Duration(cfg.FrameAssemblyTimeout) * time.Millisecond)
	}

	// Create B-frame reorderer with sampled logger
	reorderer := frame.NewBFrameReorderer(
		cfg.MaxBFrameReorderDepth,
		time.Duration(cfg.MaxReorderDelay)*time.Millisecond,
		sampledLogger,
	)

	pipeline := &VideoPipeline{
		streamID:        cfg.StreamID,
		codec:           cfg.Codec,
		frameAssembler:  assembler,
		bframeReorderer: reorderer,
		input:           input,
		output:          make(chan *types.VideoFrame, cfg.FrameBufferSize),
		ctx:             ctx,
		cancel:          cancel,
		logger:          baseLogger,
		sampledLogger:   sampledLogger,
	}

	return pipeline, nil
}

// Start starts the pipeline processing
func (p *VideoPipeline) Start() error {
	// Start frame assembler
	if err := p.frameAssembler.Start(); err != nil {
		return fmt.Errorf("failed to start frame assembler: %w", err)
	}

	p.logger.WithFields(map[string]interface{}{
		"input_channel_nil": p.input == nil,
		"context_done":      p.ctx.Err() != nil,
	}).Info("Starting pipeline workers")

	// Start pipeline workers
	p.wg.Add(4)
	go p.processPackets()
	go p.processFrames()
	go p.processReorderedFrames()
	go p.drainFramesToDevNull() // Temporary drain until transcoder is implemented

	p.logger.Info("Video pipeline started")
	return nil
}

// Stop stops the pipeline
func (p *VideoPipeline) Stop() error {
	// Cancel context to signal all goroutines to stop
	p.cancel()

	// Wait for all goroutines to finish
	p.wg.Wait()

	// Stop frame assembler
	if err := p.frameAssembler.Stop(); err != nil {
		p.logger.WithError(err).Error("Failed to stop frame assembler")
	}

	// Close output channel safely with sync.Once
	p.closeOnce.Do(func() {
		close(p.output)
	})

	// Get final stats
	reordererStats := p.bframeReorderer.GetStats()

	p.logger.WithFields(map[string]interface{}{
		"packets_processed": p.packetsProcessed.Load(),
		"frames_output":     p.framesOutput.Load(),
		"frames_reordered":  p.framesReordered.Load(),
		"frames_dropped":    reordererStats.FramesDropped,
		"errors":            p.errors.Load(),
	}).Info("Video pipeline stopped")

	return nil
}

// processPackets reads packets and sends them to the frame assembler
func (p *VideoPipeline) processPackets() {
	defer p.wg.Done()

	p.logger.WithFields(map[string]interface{}{
		"input_channel_nil": p.input == nil,
		"context_err":       p.ctx.Err(),
	}).Info("processPackets goroutine started")

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("processPackets: context cancelled")
			return

		case pkt, ok := <-p.input:
			if !ok {
				p.logger.Info("Input channel closed, processPackets exiting")
				return
			}

			p.logger.WithFields(map[string]interface{}{
				"stream_id":   p.streamID,
				"packet_type": pkt.Type.String(),
				"data_len":    len(pkt.Data),
				"pts":         pkt.PTS,
				"dts":         pkt.DTS,
			}).Info("🎬 DEBUG: Video pipeline received packet")

			// Process packet
			if err := p.frameAssembler.AddPacket(pkt); err != nil {
				p.errors.Add(1)
				p.sampledLogger.ErrorWithCategory(logger.CategoryPacketProcessing, "Frame assembler rejected packet", map[string]interface{}{
					"error": err.Error(),
					"pts":   pkt.PTS,
				})
				p.logger.WithFields(map[string]interface{}{
					"stream_id": p.streamID,
					"error":     err.Error(),
					"data_len":  len(pkt.Data),
				}).Info("❌ DEBUG: Frame assembler rejected packet")
			} else {
				p.sampledLogger.DebugWithCategory(logger.CategoryPacketProcessing, "Packet processed successfully", map[string]interface{}{
					"pts": pkt.PTS,
				})
				p.logger.WithFields(map[string]interface{}{
					"stream_id": p.streamID,
					"data_len":  len(pkt.Data),
				}).Info("✅ DEBUG: Frame assembler processed packet successfully")
			}

			p.packetsProcessed.Add(1)
		}
	}
}

// processFrames reads assembled frames and sends them to the B-frame reorderer
func (p *VideoPipeline) processFrames() {
	defer p.wg.Done()

	frameOutput := p.frameAssembler.GetOutput()
	// Process frames from assembler output

	for {
		select {
		case <-p.ctx.Done():
			return

		case frame, ok := <-frameOutput:
			if !ok {
				p.logger.Debug("Frame assembler output closed")
				return
			}

			p.logger.WithFields(map[string]interface{}{
				"stream_id":   p.streamID,
				"frame_id":    frame.ID,
				"frame_type":  frame.Type.String(),
				"frame_size":  frame.TotalSize,
				"pts":         frame.PTS,
				"dts":         frame.DTS,
				"nal_units":   len(frame.NALUnits),
			}).Info("🎬 DEBUG: Video pipeline assembled frame")

			// Check if this is a metadata frame (SPS, PPS, SEI) that should bypass reordering
			if frame.Type == types.FrameTypeSPS || frame.Type == types.FrameTypePPS || frame.Type == types.FrameTypeSEI {
				// Send metadata frames directly to output
				select {
				case <-p.ctx.Done():
					return
				case p.output <- frame:
					p.framesOutput.Add(1)
					p.sampledLogger.DebugWithCategory(logger.CategoryFrameProcessing, "Metadata frame sent to output", map[string]interface{}{
						"frame_id":   frame.ID,
						"frame_type": frame.Type.String(),
					})
				}
			} else {
				// Send actual video frames to B-frame reorderer
				reorderedFrames, err := p.bframeReorderer.AddFrame(frame)
				if err != nil {
					p.errors.Add(1)
					p.sampledLogger.ErrorWithCategory(logger.CategoryFrameReordering, "Failed to add frame to reorderer", map[string]interface{}{
						"frame_id": frame.ID,
						"error":    err.Error(),
					})
					continue
				}

				p.sampledLogger.DebugWithCategory(logger.CategoryFrameReordering, "B-frame reorderer output", map[string]interface{}{
					"input_frame_id":     frame.ID,
					"output_frame_count": len(reorderedFrames),
				})

				// Output any frames that are ready
				for i, reorderedFrame := range reorderedFrames {
					p.sampledLogger.DebugWithCategory(logger.CategoryFrameProcessing, "Sending reordered frame to output", map[string]interface{}{
						"frame_id":     reorderedFrame.ID,
						"frame_index":  i + 1,
						"total_frames": len(reorderedFrames),
					})
					select {
					case <-p.ctx.Done():
						p.sampledLogger.InfoWithCategory(logger.CategoryFrameProcessing, "Context cancelled during frame output", nil)
						return
					case p.output <- reorderedFrame:
						p.framesOutput.Add(1)
						p.framesReordered.Add(1)
						p.sampledLogger.DebugWithCategory(logger.CategoryFrameProcessing, "Successfully sent reordered frame", map[string]interface{}{
							"frame_id": reorderedFrame.ID,
						})
					}
				}
			}
		}
	}
}

// processReorderedFrames handles flushing the reorderer on shutdown
func (p *VideoPipeline) processReorderedFrames() {
	defer p.wg.Done()

	// Wait for context cancellation
	<-p.ctx.Done()

	// Flush any remaining frames from the reorderer
	remainingFrames := p.bframeReorderer.Flush()
	for _, frame := range remainingFrames {
		select {
		case p.output <- frame:
			p.framesOutput.Add(1)
			p.framesReordered.Add(1)
		default:
			// If output channel is full, drop the frame
			p.logger.Debug("Dropping frame during shutdown flush")
		}
	}
}

// drainFramesToDevNull consumes frames from output channel and discards them
func (p *VideoPipeline) drainFramesToDevNull() {
	defer p.wg.Done()

	p.logger.Info("Started frame drain worker (temporary until transcoder implementation)")

	frameCount := uint64(0)
	for {
		select {
		case <-p.ctx.Done():
			p.logger.WithFields(map[string]interface{}{
				"frames_drained": frameCount,
			}).Info("Frame drain worker stopped")
			return
		case frame, ok := <-p.output:
			if !ok {
				p.logger.WithFields(map[string]interface{}{
					"frames_drained": frameCount,
				}).Info("Output channel closed, drain worker exiting")
				return
			}

			frameCount++

			// Log periodically to show progress
			if frameCount%100 == 0 {
				p.logger.WithFields(map[string]interface{}{
					"frames_drained": frameCount,
					"frame_id":       frame.ID,
					"frame_size":     frame.TotalSize,
					"frame_type":     frame.Type.String(),
					"is_keyframe":    frame.IsKeyframe(),
				}).Info("Frame drain progress (every 100 frames)")
			} else {
				p.logger.WithFields(map[string]interface{}{
					"frame_id":    frame.ID,
					"frame_size":  frame.TotalSize,
					"frame_type":  frame.Type.String(),
					"is_keyframe": frame.IsKeyframe(),
				}).Debug("Frame drained to /dev/null")
			}

			// Frame is now "consumed" and will be garbage collected
		}
	}
}

// GetOutput returns the output channel for assembled frames
func (p *VideoPipeline) GetOutput() <-chan *types.VideoFrame {
	return p.output
}

// GetStats returns pipeline statistics
func (p *VideoPipeline) GetStats() PipelineStats {
	if p == nil {
		return PipelineStats{}
	}

	var assemblerStats frame.AssemblerStats
	var reordererStats frame.BFrameReordererStats

	if p.frameAssembler != nil {
		assemblerStats = p.frameAssembler.GetStats()
	}
	if p.bframeReorderer != nil {
		reordererStats = p.bframeReorderer.GetStats()
	}

	return PipelineStats{
		PacketsProcessed: p.packetsProcessed.Load(),
		FramesOutput:     p.framesOutput.Load(),
		FramesReordered:  p.framesReordered.Load(),
		Errors:           p.errors.Load(),
		AssemblerStats:   assemblerStats,
		ReordererStats:   reordererStats,
	}
}

// PipelineStats contains pipeline statistics
type PipelineStats struct {
	PacketsProcessed uint64
	FramesOutput     uint64
	FramesReordered  uint64
	Errors           uint64
	AssemblerStats   frame.AssemblerStats
	ReordererStats   frame.BFrameReordererStats
}
