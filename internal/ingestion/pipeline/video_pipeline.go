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

	logger    logger.Logger
	wg        sync.WaitGroup
	closeOnce sync.Once
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
	log := logger.FromContext(ctx).WithField("stream_id", cfg.StreamID)

	// Create frame assembler
	assembler := frame.NewAssembler(cfg.StreamID, cfg.Codec, cfg.FrameBufferSize)

	// Set frame timeout if specified
	if cfg.FrameAssemblyTimeout > 0 {
		assembler.SetFrameTimeout(time.Duration(cfg.FrameAssemblyTimeout) * time.Millisecond)
	}

	// Create B-frame reorderer
	reorderer := frame.NewBFrameReorderer(
		cfg.MaxBFrameReorderDepth,
		time.Duration(cfg.MaxReorderDelay)*time.Millisecond,
		log,
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
		logger:          log,
	}

	return pipeline, nil
}

// Start starts the pipeline processing
func (p *VideoPipeline) Start() error {
	// Start frame assembler
	if err := p.frameAssembler.Start(); err != nil {
		return fmt.Errorf("failed to start frame assembler: %w", err)
	}

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

	for {
		select {
		case <-p.ctx.Done():
			return

		case pkt, ok := <-p.input:
			if !ok {
				p.logger.Debug("Input channel closed")
				return
			}

			// Process packet
			if err := p.frameAssembler.AddPacket(pkt); err != nil {
				p.errors.Add(1)
				if err != frame.ErrNoFrameContext {
					p.logger.WithError(err).Debug("Failed to add packet to assembler")
				}
			}

			p.packetsProcessed.Add(1)
		}
	}
}

// processFrames reads assembled frames and sends them to the B-frame reorderer
func (p *VideoPipeline) processFrames() {
	defer p.wg.Done()

	frameOutput := p.frameAssembler.GetOutput()

	for {
		select {
		case <-p.ctx.Done():
			return

		case frame, ok := <-frameOutput:
			if !ok {
				p.logger.Debug("Frame assembler output closed")
				return
			}

			// Send frame to B-frame reorderer
			reorderedFrames, err := p.bframeReorderer.AddFrame(frame)
			if err != nil {
				p.errors.Add(1)
				p.logger.WithError(err).Debug("Failed to add frame to reorderer")
				continue
			}

			// Output any frames that are ready
			for _, reorderedFrame := range reorderedFrames {
				select {
				case <-p.ctx.Done():
					return
				case p.output <- reorderedFrame:
					p.framesOutput.Add(1)
					p.framesReordered.Add(1)
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

// GetOutput returns the output channel for assembled frames
func (p *VideoPipeline) GetOutput() <-chan *types.VideoFrame {
	return p.output
}

// GetStats returns pipeline statistics
func (p *VideoPipeline) GetStats() PipelineStats {
	assemblerStats := p.frameAssembler.GetStats()
	reordererStats := p.bframeReorderer.GetStats()

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
