package gop

import (
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// GOP represents a Group of Pictures
type GOP struct {
	ID        uint64
	StartPTS  int64
	EndPTS    int64
	StartTime time.Time
	Frames    []*types.VideoFrame
	Keyframe  *types.VideoFrame // The I/IDR frame that starts this GOP
	Closed    bool              // Whether this GOP is closed (ended with next keyframe)

	// Statistics
	FrameCount int
	IFrames    int
	PFrames    int
	BFrames    int
	TotalSize  int64
	Duration   time.Duration
	BitRate    int64 // bits per second
}

// UpdateDuration recalculates the GOP duration based on remaining frames
func (g *GOP) UpdateDuration() {
	if len(g.Frames) == 0 {
		g.Duration = 0
		g.StartPTS = 0
		g.EndPTS = 0
		return
	}

	// Find the actual start and end PTS from remaining frames
	g.StartPTS = g.Frames[0].PTS
	g.EndPTS = g.Frames[len(g.Frames)-1].PTS

	// Recalculate duration (assuming 90kHz clock)
	ptsDiff := g.EndPTS - g.StartPTS
	g.Duration = time.Duration(ptsDiff * 1000000 / 90) // Convert to nanoseconds

	// Update bitrate if we have duration
	if g.Duration > 0 {
		g.BitRate = int64(float64(g.TotalSize*8) / g.Duration.Seconds())
	} else {
		g.BitRate = 0
	}
}

// Detector tracks GOP boundaries and structure
type Detector struct {
	streamID string

	// Current GOP being built
	currentGOP *GOP
	gopCounter uint64

	// GOP history
	recentGOPs []*GOP
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
		recentGOPs: make([]*GOP, 0, 10),
		gopCounter: 0,
	}
}

// ProcessFrame processes a frame and updates GOP tracking
func (d *Detector) ProcessFrame(frame *types.VideoFrame) *GOP {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if this is a keyframe that starts a new GOP
	if frame.IsKeyframe() {
		// Close current GOP if exists
		var closedGOP *GOP
		if d.currentGOP != nil {
			d.closeCurrentGOP(frame.PTS)
			closedGOP = d.currentGOP
		}

		// Start new GOP
		d.gopCounter++
		d.currentGOP = &GOP{
			ID:         d.gopCounter,
			StartPTS:   frame.PTS,
			StartTime:  frame.CaptureTime,
			Keyframe:   frame,
			Frames:     []*types.VideoFrame{frame},
			FrameCount: 1,
			IFrames:    1,
		}

		// Update frame's GOP info
		frame.GOPId = d.currentGOP.ID
		frame.GOPPosition = 0

		return closedGOP
	}

	// Add frame to current GOP
	if d.currentGOP != nil {
		d.currentGOP.Frames = append(d.currentGOP.Frames, frame)

		// Update frame's GOP info
		frame.GOPId = d.currentGOP.ID
		frame.GOPPosition = len(d.currentGOP.Frames) - 1

		// Update GOP statistics
		switch frame.Type {
		case types.FrameTypeI, types.FrameTypeIDR:
			d.currentGOP.IFrames++
		case types.FrameTypeP:
			d.currentGOP.PFrames++
		case types.FrameTypeB:
			d.currentGOP.BFrames++
		}

		d.currentGOP.FrameCount = len(d.currentGOP.Frames)
		d.currentGOP.TotalSize += int64(frame.TotalSize)
	}

	return nil
}

// closeCurrentGOP closes the current GOP and adds it to history
func (d *Detector) closeCurrentGOP(nextKeyframePTS int64) {
	if d.currentGOP == nil {
		return
	}

	// Calculate GOP duration
	d.currentGOP.EndPTS = nextKeyframePTS
	if len(d.currentGOP.Frames) > 0 {
		lastFrame := d.currentGOP.Frames[len(d.currentGOP.Frames)-1]
		d.currentGOP.Duration = lastFrame.CaptureTime.Sub(d.currentGOP.StartTime)
	}
	d.currentGOP.Closed = true

	// Add to history
	d.recentGOPs = append(d.recentGOPs, d.currentGOP)
	if len(d.recentGOPs) > d.maxGOPs {
		d.recentGOPs = d.recentGOPs[1:]
	}

	// Update statistics
	d.updateStatistics()
}

// updateStatistics updates running statistics
func (d *Detector) updateStatistics() {
	if len(d.recentGOPs) == 0 {
		return
	}

	var totalSize int64
	var totalDuration time.Duration

	for _, gop := range d.recentGOPs {
		totalSize += int64(gop.FrameCount)
		totalDuration += gop.Duration
	}

	d.avgGOPSize = float64(totalSize) / float64(len(d.recentGOPs))
	d.avgDuration = totalDuration / time.Duration(len(d.recentGOPs))
}

// GetCurrentGOP returns the current GOP being built
func (d *Detector) GetCurrentGOP() *GOP {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentGOP
}

// GetRecentGOPs returns recent closed GOPs
func (d *Detector) GetRecentGOPs() []*GOP {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*GOP, len(d.recentGOPs))
	copy(result, d.recentGOPs)
	return result
}

// GetStatistics returns GOP statistics
func (d *Detector) GetStatistics() GOPStatistics {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := GOPStatistics{
		StreamID:        d.streamID,
		TotalGOPs:       d.gopCounter,
		AverageGOPSize:  d.avgGOPSize,
		AverageDuration: d.avgDuration,
	}

	// Calculate current GOP info
	if d.currentGOP != nil {
		stats.CurrentGOPFrames = d.currentGOP.FrameCount
		stats.CurrentGOPSize = d.currentGOP.TotalSize
	}

	// Calculate pattern from recent GOPs
	if len(d.recentGOPs) > 0 {
		var totalI, totalP, totalB int
		for _, gop := range d.recentGOPs {
			totalI += gop.IFrames
			totalP += gop.PFrames
			totalB += gop.BFrames
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

// GOPStatistics contains GOP detection statistics
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

// IsComplete returns true if the GOP has all expected frames
func (g *GOP) IsComplete() bool {
	// A GOP is complete if it's closed or has reasonable number of frames
	// Most GOPs are 1-2 seconds at 30fps = 30-60 frames
	return g.Closed || g.FrameCount >= 30
}

// CanDropFrame returns true if a frame at given position can be dropped
func (g *GOP) CanDropFrame(position int) bool {
	if position < 0 || position >= len(g.Frames) {
		return false
	}

	frame := g.Frames[position]

	// Never drop keyframes
	if frame.IsKeyframe() {
		return false
	}

	// B frames can always be dropped
	if frame.Type == types.FrameTypeB {
		return true
	}

	// P frames can be dropped if no B frames depend on them
	if frame.Type == types.FrameTypeP {
		// Check if any B frames after this P frame might depend on it
		for i := position + 1; i < len(g.Frames); i++ {
			if g.Frames[i].Type == types.FrameTypeB {
				// Conservative: assume B frame depends on previous P frame
				return false
			}
			if g.Frames[i].Type == types.FrameTypeP || g.Frames[i].IsKeyframe() {
				// Reached next reference frame, safe to drop
				break
			}
		}
		return true
	}

	return false
}
