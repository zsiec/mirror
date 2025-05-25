package frame

import (
	"container/heap"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// BFrameReorderer handles reordering of frames with B-frames
type BFrameReorderer struct {
	// Configuration
	maxReorderDepth int           // Maximum B-frame depth
	maxDelay        time.Duration // Maximum time to wait for reordering

	// State
	buffer        frameHeap // Priority queue ordered by DTS
	lastOutputPTS int64     // Last output PTS for order checking
	lastOutputDTS int64     // Last output DTS
	baseTime      time.Time // Base time for delay calculations

	// Metrics
	framesReordered uint64
	framesDropped   uint64
	maxBufferSize   int

	logger logger.Logger
	mu     sync.Mutex
}

// NewBFrameReorderer creates a new B-frame reorderer
func NewBFrameReorderer(maxReorderDepth int, maxDelay time.Duration, logger logger.Logger) *BFrameReorderer {
	if maxReorderDepth <= 0 {
		maxReorderDepth = 3 // Default to 3 B-frames
	}
	if maxDelay <= 0 {
		maxDelay = 200 * time.Millisecond // Default 200ms
	}

	reorderer := &BFrameReorderer{
		maxReorderDepth: maxReorderDepth,
		maxDelay:        maxDelay,
		buffer:          make(frameHeap, 0, maxReorderDepth+1),
		lastOutputPTS:   -1,
		lastOutputDTS:   -1,
		logger:          logger,
	}

	heap.Init(&reorderer.buffer)
	return reorderer
}

// AddFrame adds a frame to the reorderer
func (r *BFrameReorderer) AddFrame(frame *types.VideoFrame) ([]*types.VideoFrame, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate frame
	if err := r.validateFrame(frame); err != nil {
		r.framesDropped++
		return nil, err
	}

	// Initialize base time on first frame
	if r.baseTime.IsZero() {
		r.baseTime = frame.CaptureTime
	}

	// Add frame to buffer
	heap.Push(&r.buffer, frame)

	// Update max buffer size metric
	if len(r.buffer) > r.maxBufferSize {
		r.maxBufferSize = len(r.buffer)
	}

	// Check if we can output frames
	return r.checkOutput(), nil
}

// Flush returns all remaining frames in order
func (r *BFrameReorderer) Flush() []*types.VideoFrame {
	r.mu.Lock()
	defer r.mu.Unlock()

	output := make([]*types.VideoFrame, 0, len(r.buffer))

	// Output all frames in DTS order
	for len(r.buffer) > 0 {
		frame := heap.Pop(&r.buffer).(*types.VideoFrame)
		if r.isValidOutput(frame) {
			output = append(output, frame)
			r.updateOutputState(frame)
		}
	}

	return output
}

// GetStats returns reorderer statistics
func (r *BFrameReorderer) GetStats() BFrameReordererStats {
	r.mu.Lock()
	defer r.mu.Unlock()

	return BFrameReordererStats{
		FramesReordered: r.framesReordered,
		FramesDropped:   r.framesDropped,
		CurrentBuffer:   len(r.buffer),
		MaxBufferSize:   r.maxBufferSize,
	}
}

// validateFrame checks if a frame is valid for reordering
func (r *BFrameReorderer) validateFrame(frame *types.VideoFrame) error {
	if frame == nil {
		return fmt.Errorf("nil frame")
	}

	// DTS must be set for proper reordering
	if frame.DTS == 0 && frame.PTS != 0 {
		// If no DTS, assume DTS = PTS (no B-frames)
		frame.DTS = frame.PTS
	}

	// Check for DTS discontinuity
	if r.lastOutputDTS >= 0 && frame.DTS < r.lastOutputDTS {
		return fmt.Errorf("DTS went backwards: %d < %d", frame.DTS, r.lastOutputDTS)
	}

	return nil
}

// checkOutput determines which frames can be output
func (r *BFrameReorderer) checkOutput() []*types.VideoFrame {
	output := make([]*types.VideoFrame, 0)

	// Continue outputting frames while conditions are met
	for len(r.buffer) > 0 {
		// Peek at the frame with lowest DTS
		nextFrame := r.buffer[0]

		// Check if we should output this frame
		shouldOutput := false

		// Condition 1: Buffer is full
		if len(r.buffer) > r.maxReorderDepth {
			shouldOutput = true
		}

		// Condition 2: Next frame is too old (prevents excessive delay)
		if !r.baseTime.IsZero() {
			frameAge := time.Since(nextFrame.CaptureTime)
			if frameAge > r.maxDelay {
				shouldOutput = true
			}
		}

		// Condition 3: We have enough frames to ensure ordering
		// For B-frames, we need to see frames with higher DTS
		if len(r.buffer) >= r.maxReorderDepth {
			shouldOutput = true
		}

		// Condition 4: Check if this is a keyframe (I-frame)
		// I-frames can be output once we have the next frame
		if nextFrame.Type == types.FrameTypeI && len(r.buffer) > 1 {
			shouldOutput = true
		}

		// Condition 5: Check if this is a P-frame with proper dependencies
		// P-frames can be output early if:
		// - We have at least one frame after it in the buffer
		// - The next frame has a higher DTS (ensuring decode order)
		// - We're not waiting for potential B-frames that depend on this P-frame
		if nextFrame.Type == types.FrameTypeP && len(r.buffer) > 1 {
			// Check if the next frame in DTS order has significantly higher DTS
			// This suggests no B-frames depend on this P-frame
			secondFrame := r.buffer[1]

			// If next frame is I or P with DTS gap > expected frame duration
			if secondFrame.Type == types.FrameTypeI || secondFrame.Type == types.FrameTypeP {
				// Estimate frame duration based on DTS difference
				// If gap is > 2x normal frame duration, likely no B-frames between
				dtsDiff := secondFrame.DTS - nextFrame.DTS
				expectedFrameDuration := r.estimateFrameDuration(nextFrame, secondFrame)

				if dtsDiff > 2*expectedFrameDuration {
					shouldOutput = true
				}
			}

			// Also output P-frames if we have reached half the max reorder depth
			// This balances latency vs. proper B-frame handling
			if len(r.buffer) >= (r.maxReorderDepth+1)/2 {
				shouldOutput = true
			}
		}

		if !shouldOutput {
			break
		}

		// Remove and output the frame
		frame := heap.Pop(&r.buffer).(*types.VideoFrame)

		// Validate output order
		if r.isValidOutput(frame) {
			output = append(output, frame)
			r.updateOutputState(frame)
			r.framesReordered++
		} else {
			r.framesDropped++
			r.logger.WithFields(map[string]interface{}{
				"frame_id": frame.ID,
				"pts":      frame.PTS,
				"dts":      frame.DTS,
				"type":     frame.Type,
			}).Warn("Dropping frame due to invalid output order")
		}
	}

	return output
}

// isValidOutput checks if a frame can be output without breaking order
func (r *BFrameReorderer) isValidOutput(frame *types.VideoFrame) bool {
	// First frame is always valid
	if r.lastOutputPTS < 0 {
		return true
	}

	// DTS must be monotonically increasing
	if frame.DTS < r.lastOutputDTS {
		return false
	}

	// For non-B-frames, PTS should be >= last output PTS
	// B-frames can have PTS < last output PTS
	if frame.Type != types.FrameTypeB && frame.PTS < r.lastOutputPTS {
		return false
	}

	return true
}

// updateOutputState updates the last output timestamps
func (r *BFrameReorderer) updateOutputState(frame *types.VideoFrame) {
	r.lastOutputPTS = frame.PTS
	r.lastOutputDTS = frame.DTS
}

// estimateFrameDuration estimates frame duration from two consecutive frames
func (r *BFrameReorderer) estimateFrameDuration(frame1, frame2 *types.VideoFrame) int64 {
	// Calculate DTS difference
	dtsDiff := frame2.DTS - frame1.DTS

	// Sanity check: duration should be positive and reasonable
	if dtsDiff <= 0 || dtsDiff > 10000 { // Arbitrary upper limit
		// Fallback to a reasonable default (assuming 30fps at 90kHz timebase)
		return 3000
	}

	return dtsDiff
}

// BFrameReordererStats contains reorderer statistics
type BFrameReordererStats struct {
	FramesReordered uint64
	FramesDropped   uint64
	CurrentBuffer   int
	MaxBufferSize   int
}

// frameHeap implements heap.Interface for frames ordered by DTS
type frameHeap []*types.VideoFrame

func (h frameHeap) Len() int           { return len(h) }
func (h frameHeap) Less(i, j int) bool { return h[i].DTS < h[j].DTS }
func (h frameHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *frameHeap) Push(x interface{}) {
	*h = append(*h, x.(*types.VideoFrame))
}

func (h *frameHeap) Pop() interface{} {
	old := *h
	n := len(old)
	frame := old[n-1]
	*h = old[0 : n-1]
	return frame
}

// DTSCalculator helps calculate DTS from PTS for streams without DTS
type DTSCalculator struct {
	frameRate   float64
	timeBase    types.Rational
	maxBFrames  int
	lastPTS     int64
	lastDTS     int64
	frameBuffer []*frameInfo
	frameIndex  int
}

// frameInfo stores minimal frame information for DTS calculation
type frameInfo struct {
	pts       int64
	frameType types.FrameType
}

// NewDTSCalculator creates a new DTS calculator
func NewDTSCalculator(frameRate float64, timeBase types.Rational, maxBFrames int) *DTSCalculator {
	return &DTSCalculator{
		frameRate:   frameRate,
		timeBase:    timeBase,
		maxBFrames:  maxBFrames,
		lastPTS:     -1,
		lastDTS:     -1,
		frameBuffer: make([]*frameInfo, 0, maxBFrames+2),
	}
}

// CalculateDTS estimates DTS from PTS based on frame type and GOP structure
func (c *DTSCalculator) CalculateDTS(pts int64, frameType types.FrameType) int64 {
	// For streams without B-frames, DTS = PTS
	if c.maxBFrames == 0 {
		c.lastDTS = pts
		return pts
	}

	// Add frame to buffer
	c.frameBuffer = append(c.frameBuffer, &frameInfo{
		pts:       pts,
		frameType: frameType,
	})

	// If we don't have enough frames yet, estimate DTS
	if len(c.frameBuffer) < c.maxBFrames+1 {
		// For I and P frames at start, DTS = PTS - (maxBFrames * frame_duration)
		if frameType != types.FrameTypeB {
			frameDuration := c.calculateFrameDuration()
			dts := pts - int64(c.maxBFrames)*frameDuration
			if dts < 0 {
				dts = 0
			}
			c.lastDTS = dts
			return dts
		}
		// For B-frames, use last DTS + frame_duration
		c.lastDTS += c.calculateFrameDuration()
		return c.lastDTS
	}

	// We have enough frames to properly calculate DTS
	// Sort frames by PTS to find display order
	// Then assign DTS in decode order

	// For now, simple approximation:
	// I/P frames: DTS = PTS - (num_b_frames * frame_duration)
	// B frames: DTS = last_DTS + frame_duration

	frameDuration := c.calculateFrameDuration()

	if frameType == types.FrameTypeB {
		c.lastDTS += frameDuration
	} else {
		// Count B-frames between this and previous I/P frame
		bFrameCount := 0
		for i := len(c.frameBuffer) - 2; i >= 0; i-- {
			if c.frameBuffer[i].frameType == types.FrameTypeB {
				bFrameCount++
			} else {
				break
			}
		}
		c.lastDTS = pts - int64(bFrameCount)*frameDuration
	}

	// Remove old frames from buffer
	if len(c.frameBuffer) > c.maxBFrames*2 {
		c.frameBuffer = c.frameBuffer[1:]
	}

	return c.lastDTS
}

// calculateFrameDuration calculates the duration of one frame in timebase units
func (c *DTSCalculator) calculateFrameDuration() int64 {
	// duration = timebase.Den / (framerate * timebase.Num)
	return int64(float64(c.timeBase.Den) / (c.frameRate * float64(c.timeBase.Num)))
}
