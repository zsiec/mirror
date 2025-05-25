package types

import (
	"time"
)

// GOPStructure represents the structure of a GOP
type GOPStructure struct {
	Pattern      string // e.g., "IBBPBBPBBPBB"
	Size         int    // Number of frames
	OpenGOP      bool   // Whether GOP is open (refs outside)
	Hierarchical bool   // Whether B-frames use hierarchical prediction
}

// GOP represents a Group of Pictures
type GOP struct {
	// Identification
	ID          uint64 // Unique GOP ID
	StreamID    string // Stream this GOP belongs to
	SequenceNum uint32 // GOP sequence number in stream

	// Frames
	Frames   []*VideoFrame // All frames in GOP
	Keyframe *VideoFrame   // The I-frame that starts this GOP

	// Timing
	StartPTS  int64     // PTS of first frame
	EndPTS    int64     // PTS of last frame
	Duration  int64     // Total duration
	StartTime time.Time // Wall clock time of first frame
	EndTime   time.Time // Wall clock time of last frame

	// Structure
	Structure GOPStructure // GOP structure info
	Complete  bool         // All frames present
	Closed    bool         // Closed GOP (no external refs)

	// Statistics
	TotalSize    int64 // Total size in bytes
	AvgFrameSize int64 // Average frame size
	BitRate      int64 // Calculated bitrate
	IFrameSize   int64 // Size of I-frame
	PFrameCount  int   // Number of P-frames
	BFrameCount  int   // Number of B-frames

	// Quality metrics
	DroppedFrames   int     // Frames dropped from this GOP
	CorruptedFrames int     // Corrupted frames detected
	AvgQP           float64 // Average quantization parameter

	// Scene detection
	SceneChange bool    // GOP starts with scene change
	SceneScore  float64 // Scene change detection score
}

// AddFrame adds a frame to the GOP
func (g *GOP) AddFrame(frame *VideoFrame) {
	g.Frames = append(g.Frames, frame)
	frame.GOPId = g.ID
	frame.GOPPosition = len(g.Frames) - 1

	// Update statistics
	g.TotalSize += int64(frame.TotalSize)

	// Update timing
	if len(g.Frames) == 1 {
		g.StartPTS = frame.PTS
		g.StartTime = frame.CaptureTime
		if frame.Type.IsKeyframe() {
			g.Keyframe = frame
			g.IFrameSize = int64(frame.TotalSize)
		}
	}
	g.EndPTS = frame.PTS
	g.EndTime = frame.CompleteTime

	// Count frame types
	switch frame.Type {
	case FrameTypeP:
		g.PFrameCount++
	case FrameTypeB:
		g.BFrameCount++
	}

	// Check for corruption
	if frame.IsCorrupted() {
		g.CorruptedFrames++
	}
}

// CalculateStats calculates GOP statistics
func (g *GOP) CalculateStats() {
	if len(g.Frames) == 0 {
		return
	}

	// Calculate average frame size
	g.AvgFrameSize = g.TotalSize / int64(len(g.Frames))

	// Calculate duration including the last frame's duration
	g.Duration = g.EndPTS - g.StartPTS
	if len(g.Frames) > 0 {
		lastFrame := g.Frames[len(g.Frames)-1]
		if lastFrame.Duration > 0 {
			g.Duration += lastFrame.Duration
		}
	}

	// Calculate bitrate
	if g.Duration > 0 {
		// Convert to bits per second (assuming 90kHz clock)
		durationSeconds := float64(g.Duration) / 90000.0
		g.BitRate = int64(float64(g.TotalSize*8) / durationSeconds)
	}

	// Calculate average QP
	totalQP := 0
	qpCount := 0
	for _, frame := range g.Frames {
		if frame.QP > 0 {
			totalQP += frame.QP
			qpCount++
		}
	}
	if qpCount > 0 {
		g.AvgQP = float64(totalQP) / float64(qpCount)
	}

	// Update structure
	g.Structure.Size = len(g.Frames)
	g.Structure.Pattern = g.generatePattern()
}

// generatePattern generates the frame pattern string
func (g *GOP) generatePattern() string {
	pattern := ""
	for _, frame := range g.Frames {
		pattern += frame.Type.String()
	}
	return pattern
}

// IsComplete checks if GOP is complete
func (g *GOP) IsComplete() bool {
	if !g.Complete {
		return false
	}

	// Check if we have expected number of frames
	if g.Structure.Size > 0 && len(g.Frames) != g.Structure.Size {
		return false
	}

	// Check if we have a keyframe
	if g.Keyframe == nil {
		return false
	}

	return true
}

// CanDrop returns true if this GOP can be safely dropped
func (g *GOP) CanDrop() bool {
	// Don't drop incomplete GOPs as they might be building
	if !g.Complete {
		return false
	}

	// Don't drop GOPs with scene changes
	if g.SceneChange {
		return false
	}

	// Check if any frame is referenced by frames outside this GOP
	// This would indicate an open GOP that shouldn't be dropped
	if g.Structure.OpenGOP {
		return false
	}

	return true
}

// GetFrameByPosition returns frame at specified position
func (g *GOP) GetFrameByPosition(position int) *VideoFrame {
	if position < 0 || position >= len(g.Frames) {
		return nil
	}
	return g.Frames[position]
}

// GetReferenceFrames returns all reference frames in the GOP
func (g *GOP) GetReferenceFrames() []*VideoFrame {
	refs := make([]*VideoFrame, 0)
	for _, frame := range g.Frames {
		if frame.IsReference() {
			refs = append(refs, frame)
		}
	}
	return refs
}

// HasCompleteReferences checks if all frame references are within this GOP
func (g *GOP) HasCompleteReferences() bool {
	frameIDs := make(map[uint64]bool)
	for _, frame := range g.Frames {
		frameIDs[frame.ID] = true
	}

	for _, frame := range g.Frames {
		for _, refID := range frame.References {
			if !frameIDs[refID] {
				// Reference to frame outside this GOP
				g.Structure.OpenGOP = true
				return false
			}
		}
	}

	g.Closed = true
	return true
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

	// Recalculate duration including the last frame's duration
	g.Duration = g.EndPTS - g.StartPTS
	if len(g.Frames) > 0 {
		lastFrame := g.Frames[len(g.Frames)-1]
		if lastFrame.Duration > 0 {
			g.Duration += lastFrame.Duration
		}
	}

	// Update bitrate if we have duration
	if g.Duration > 0 {
		durationSeconds := float64(g.Duration) / 90000.0
		g.BitRate = int64(float64(g.TotalSize*8) / durationSeconds)
	} else {
		g.BitRate = 0
	}
}
