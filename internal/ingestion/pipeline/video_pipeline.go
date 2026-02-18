package pipeline

import (
	"context"
	"fmt"
	"strings"
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
	p.wg.Add(3)
	go p.processPackets()
	go p.processFrames()
	go p.processReorderedFrames()

	p.logger.Info("Video pipeline started")
	return nil
}

// Stop stops the pipeline
func (p *VideoPipeline) Stop() error {
	p.logger.Debug("Stopping video pipeline")

	p.cancel()
	p.logger.Debug("Context cancelled, waiting for goroutines to finish")

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Debug("All goroutines finished gracefully")
	case <-time.After(5 * time.Second):
		p.logger.Warn("Timeout waiting for goroutines to finish, proceeding with cleanup")
	}

	if p.frameAssembler != nil {
		if err := p.frameAssembler.Stop(); err != nil {
			p.logger.WithError(err).Error("Failed to stop frame assembler")
		} else {
			p.logger.Debug("Frame assembler stopped successfully")
		}
	}

	p.closeOnce.Do(func() {
		close(p.output)
		p.logger.Debug("Output channel closed")
	})

	var reordererStats frame.BFrameReordererStats
	if p.bframeReorderer != nil {
		reordererStats = p.bframeReorderer.GetStats()
	}

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
	defer func() {
		if r := recover(); r != nil {
			p.errors.Add(1)
			p.logger.WithField("panic", r).Error("Panic in processPackets goroutine, recovering")
		}
		p.wg.Done()
	}()

	// Validate input channel and context before processing
	if p.input == nil {
		p.logger.Error("Input channel is nil, processPackets cannot start")
		p.errors.Add(1)
		return
	}
	if p.frameAssembler == nil {
		p.logger.Error("Frame assembler is nil, processPackets cannot start")
		p.errors.Add(1)
		return
	}

	p.logger.WithFields(map[string]interface{}{
		"input_channel_nil": p.input == nil,
		"context_err":       p.ctx.Err(),
	}).Info("processPackets goroutine started")

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("processPackets: context cancelled, exiting gracefully")
			return

		case pkt, ok := <-p.input:
			if !ok {
				p.logger.Info("Input channel closed, processPackets exiting")
				return
			}

			// Validate packet data before processing
			if len(pkt.Data) == 0 {
				p.errors.Add(1)
				p.sampledLogger.ErrorWithCategory(logger.CategoryPacketProcessing, "Received empty packet data", map[string]interface{}{
					"pts": pkt.PTS,
					"dts": pkt.DTS,
				})
				continue
			}

			// Process packet with error recovery
			if err := p.frameAssembler.AddPacket(pkt); err != nil {
				p.errors.Add(1)
				p.sampledLogger.ErrorWithCategory(logger.CategoryPacketProcessing, "Frame assembler rejected packet", map[string]interface{}{
					"error":    err.Error(),
					"pts":      pkt.PTS,
					"dts":      pkt.DTS,
					"data_len": len(pkt.Data),
				})
				// Check if error indicates critical failure requiring pipeline restart
				if p.isCriticalError(err) {
					p.logger.WithError(err).Error("Critical error in frame assembler, stopping pipeline")
					p.cancel() // Trigger shutdown
					return
				}
			} else {
				p.sampledLogger.DebugWithCategory(logger.CategoryPacketProcessing, "Packet processed successfully", map[string]interface{}{
					"pts": pkt.PTS,
				})
			}

			p.packetsProcessed.Add(1)
		}
	}
}

// processFrames reads assembled frames and sends them to the B-frame reorderer
func (p *VideoPipeline) processFrames() {
	defer func() {
		if r := recover(); r != nil {
			p.errors.Add(1)
			p.logger.WithField("panic", r).Error("Panic in processFrames goroutine, recovering")
		}
		p.wg.Done()
	}()

	// Validate components before processing
	if p.frameAssembler == nil {
		p.logger.Error("Frame assembler is nil, processFrames cannot start")
		p.errors.Add(1)
		return
	}
	if p.bframeReorderer == nil {
		p.logger.Error("B-frame reorderer is nil, processFrames cannot start")
		p.errors.Add(1)
		return
	}

	frameOutput := p.frameAssembler.GetOutput()
	if frameOutput == nil {
		p.logger.Error("Frame assembler output channel is nil, processFrames cannot start")
		p.errors.Add(1)
		return
	}
	// Process frames from assembler output

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("processFrames: context cancelled, exiting gracefully")
			return

		case frame, ok := <-frameOutput:
			if !ok {
				p.logger.Debug("Frame assembler output closed, processFrames exiting")
				return
			}

			// Validate frame before processing
			if frame == nil {
				p.errors.Add(1)
				p.logger.Error("Received nil frame from assembler")
				continue
			}
			if len(frame.NALUnits) == 0 && frame.Type != types.FrameTypeSEI {
				p.errors.Add(1)
				p.logger.WithField("frame_id", frame.ID).Error("Received frame with no NAL units")
				continue
			}

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

	p.logger.Debug("processReorderedFrames: flushing remaining frames")

	remainingFrames := p.bframeReorderer.Flush()
	p.logger.WithField("frames_to_flush", len(remainingFrames)).Debug("Flushing remaining frames from reorderer")

	for i, frame := range remainingFrames {
		func() {
			defer func() {
				if r := recover(); r != nil {
					p.logger.WithFields(map[string]interface{}{
						"frame_id": frame.ID,
						"panic":    r,
					}).Warn("Recovered from panic during frame flush (channel closed)")
				}
			}()
			select {
			case p.output <- frame:
				p.framesOutput.Add(1)
				p.framesReordered.Add(1)
				p.logger.WithFields(map[string]interface{}{
					"frame_index":  i + 1,
					"total_frames": len(remainingFrames),
					"frame_id":     frame.ID,
				}).Debug("Successfully flushed frame during shutdown")
			case <-time.After(100 * time.Millisecond):
				p.logger.WithFields(map[string]interface{}{
					"frame_id":       frame.ID,
					"frames_dropped": len(remainingFrames) - i,
				}).Warn("Timeout flushing frame during shutdown, dropping remaining frames")
				return
			}
		}()
	}

	p.logger.WithField("frames_flushed", len(remainingFrames)).Debug("All remaining frames flushed successfully")
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

// isCriticalError determines if an error is critical and should stop the pipeline
func (p *VideoPipeline) isCriticalError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Check for critical error patterns
	criticalPatterns := []string{
		"out of memory",
		"memory allocation failed",
		"context deadline exceeded",
		"panic",
		"buffer overflow",
		"corrupted",
		"invalid state",
	}

	for _, pattern := range criticalPatterns {
		if strings.Contains(strings.ToLower(errStr), pattern) {
			return true
		}
	}

	return false
}
