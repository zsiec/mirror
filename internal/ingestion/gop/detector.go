package gop

import (
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// Detector tracks GOP boundaries and structure
type Detector struct {
	streamID string

	// Current GOP being built
	currentGOP *types.GOP
	gopCounter uint64

	// GOP history
	recentGOPs []*types.GOP
	maxGOPs    int

	// Statistics
	avgGOPSize  float64
	avgDuration time.Duration

	mu sync.RWMutex
}

// NewDetector creates a new GOP detector
func NewDetector(streamID string) *Detector {
	return &Detector{
		streamID:   streamID,
		maxGOPs:    10, // Keep last 10 GOPs
		recentGOPs: make([]*types.GOP, 0, 10),
		gopCounter: 0,
	}
}

func (d *Detector) ProcessFrame(frame *types.VideoFrame) *types.GOP {
	d.mu.Lock()
	defer d.mu.Unlock()

	if frame.IsKeyframe() {
		var closedGOP *types.GOP
		if d.currentGOP != nil {
			d.closeCurrentGOP(frame.PTS)
			closedGOP = d.currentGOP
		}

		d.gopCounter++
		d.currentGOP = &types.GOP{
			ID:        d.gopCounter,
			StreamID:  d.streamID,
			StartPTS:  frame.PTS,
			StartTime: frame.CaptureTime,
			Keyframe:  frame,
			Frames:    []*types.VideoFrame{frame},
			Complete:  false,
			Closed:    false,
		}

		frame.GOPId = d.currentGOP.ID
		frame.GOPPosition = 0

		return closedGOP
	}

	if d.currentGOP != nil {
		d.currentGOP.Frames = append(d.currentGOP.Frames, frame)

		frame.GOPId = d.currentGOP.ID
		frame.GOPPosition = len(d.currentGOP.Frames) - 1

		switch frame.Type {
		case types.FrameTypeI, types.FrameTypeIDR:
			// I-frame count is derived from countIFrames() when needed
		case types.FrameTypeP:
			d.currentGOP.PFrameCount++
		case types.FrameTypeB:
			d.currentGOP.BFrameCount++
		}

		d.currentGOP.TotalSize += int64(frame.TotalSize)

		d.checkGOPCompletion()
	}

	return nil
}

func (d *Detector) closeCurrentGOP(nextKeyframePTS int64) {
	if d.currentGOP == nil {
		return
	}

	d.currentGOP.EndPTS = nextKeyframePTS
	if len(d.currentGOP.Frames) > 0 {
		d.currentGOP.Duration = d.currentGOP.EndPTS - d.currentGOP.StartPTS
	}
	d.currentGOP.Closed = true
	d.currentGOP.Complete = true

	d.recentGOPs = append(d.recentGOPs, d.currentGOP)
	if len(d.recentGOPs) > d.maxGOPs {
		d.recentGOPs = d.recentGOPs[1:]
	}

	d.updateStatistics()
}

func (d *Detector) updateStatistics() {
	if len(d.recentGOPs) == 0 {
		return
	}

	var totalSize int64
	var totalDuration time.Duration

	for _, gop := range d.recentGOPs {
		totalSize += int64(len(gop.Frames))
		ptsDuration := time.Duration(gop.Duration * 1000000 / 90) // Convert to nanoseconds
		totalDuration += ptsDuration
	}

	d.avgGOPSize = float64(totalSize) / float64(len(d.recentGOPs))
	d.avgDuration = totalDuration / time.Duration(len(d.recentGOPs))
}

func (d *Detector) GetCurrentGOP() *types.GOP {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentGOP
}

func (d *Detector) GetRecentGOPs() []*types.GOP {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*types.GOP, len(d.recentGOPs))
	copy(result, d.recentGOPs)
	return result
}

func (d *Detector) GetStatistics() GOPStatistics {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := GOPStatistics{
		StreamID:        d.streamID,
		TotalGOPs:       d.gopCounter,
		AverageGOPSize:  d.avgGOPSize,
		AverageDuration: d.avgDuration,
	}

	if d.currentGOP != nil {
		stats.CurrentGOPFrames = len(d.currentGOP.Frames)
		stats.CurrentGOPSize = d.currentGOP.TotalSize
	}

	if len(d.recentGOPs) > 0 {
		var totalI, totalP, totalB int
		for _, gop := range d.recentGOPs {
			totalI += countIFrames(gop)
			totalP += gop.PFrameCount
			totalB += gop.BFrameCount
		}
		total := totalI + totalP + totalB
		if total > 0 {
			stats.IFrameRatio = float64(totalI) / float64(total)
			stats.PFrameRatio = float64(totalP) / float64(total)
			stats.BFrameRatio = float64(totalB) / float64(total)
		}
	}

	return stats
}

type GOPStatistics struct {
	StreamID         string
	TotalGOPs        uint64
	CurrentGOPFrames int
	CurrentGOPSize   int64
	AverageGOPSize   float64
	AverageDuration  time.Duration
	IFrameRatio      float64
	PFrameRatio      float64
	BFrameRatio      float64
}

func (d *Detector) checkGOPCompletion() {
	if d.currentGOP == nil {
		return
	}

	// 1. Temporal duration criteria (primary completion signal)
	// Industry standard: 0.5-2 seconds for streaming applications
	if len(d.currentGOP.Frames) > 0 {
		firstFrame := d.currentGOP.Frames[0]
		lastFrame := d.currentGOP.Frames[len(d.currentGOP.Frames)-1]

		// Calculate temporal duration from PTS (90kHz clock standard)
		ptsDuration := lastFrame.PTS - firstFrame.PTS
		temporalDuration := time.Duration(ptsDuration * 1000000 / 90) // Convert to nanoseconds

		// Mark complete if GOP reaches reasonable temporal duration
		// Based on Apple HLS and WebRTC low-latency requirements
		minGOPDuration := 500 * time.Millisecond // 0.5s minimum
		maxGOPDuration := 2 * time.Second        // 2s maximum per HLS spec

		if temporalDuration >= minGOPDuration {
			d.currentGOP.Complete = true

			// Update GOP duration for bitrate calculation
			d.currentGOP.Duration = ptsDuration
		}

		// Force completion if GOP exceeds maximum duration
		// Prevents unbounded GOP growth in live streams
		if temporalDuration >= maxGOPDuration {
			d.currentGOP.Complete = true
			d.currentGOP.Closed = true // Force closure for oversized GOPs
			d.currentGOP.Duration = ptsDuration
		}
	}

	// 2. Structure-based completion for known patterns
	// If GOP structure is defined, respect it

	if d.currentGOP.Structure.Size > 0 && len(d.currentGOP.Frames) >= d.currentGOP.Structure.Size {
		d.currentGOP.Complete = true
	}

	frameCountThreshold := 120
	if len(d.currentGOP.Frames) >= frameCountThreshold {
		d.currentGOP.Complete = true
		d.currentGOP.Closed = true

		if d.currentGOP.Duration == 0 && len(d.currentGOP.Frames) > 0 {
			firstFrame := d.currentGOP.Frames[0]
			lastFrame := d.currentGOP.Frames[len(d.currentGOP.Frames)-1]
			d.currentGOP.Duration = lastFrame.PTS - firstFrame.PTS
		}
	}
}
