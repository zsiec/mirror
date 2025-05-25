package gop

import (
	"container/list"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// Buffer maintains a buffer of complete GOPs for intelligent frame management
type Buffer struct {
	streamID      string
	
	// GOP storage
	gops          *list.List // List of *GOP
	gopIndex      map[uint64]*list.Element // Quick lookup by GOP ID
	
	// Buffer limits
	maxGOPs       int
	maxBytes      int64
	maxDuration   time.Duration
	
	// Current state
	currentBytes  int64
	oldestTime    time.Time
	newestTime    time.Time
	
	// Frame index for quick lookup
	frameIndex    map[uint64]*FrameLocation // Frame ID -> location
	
	// Statistics
	totalGOPs     uint64
	droppedGOPs   uint64
	droppedFrames uint64
	
	mu            sync.RWMutex
	logger        logger.Logger
}

// FrameLocation tracks where a frame is stored
type FrameLocation struct {
	GOP      *GOP
	Position int
}

// BufferConfig configures the GOP buffer
type BufferConfig struct {
	MaxGOPs     int           // Maximum number of GOPs to buffer
	MaxBytes    int64         // Maximum buffer size in bytes
	MaxDuration time.Duration // Maximum time span of buffered content
}

// NewBuffer creates a new GOP buffer
func NewBuffer(streamID string, config BufferConfig, logger logger.Logger) *Buffer {
	return &Buffer{
		streamID:    streamID,
		gops:        list.New(),
		gopIndex:    make(map[uint64]*list.Element),
		frameIndex:  make(map[uint64]*FrameLocation),
		maxGOPs:     config.MaxGOPs,
		maxBytes:    config.MaxBytes,
		maxDuration: config.MaxDuration,
		logger:      logger.WithField("component", "gop_buffer"),
	}
}

// AddGOP adds a complete GOP to the buffer
func (b *Buffer) AddGOP(gop *GOP) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	// Don't add incomplete GOPs unless they're closed
	if !gop.IsComplete() && !gop.Closed {
		b.logger.WithField("gop_id", gop.ID).Debug("Skipping incomplete GOP")
		return
	}
	
	// Add to buffer
	elem := b.gops.PushBack(gop)
	b.gopIndex[gop.ID] = elem
	
	// Update frame index - create locations outside of loop to avoid races
	frameLocations := make([]*FrameLocation, len(gop.Frames))
	for i, frame := range gop.Frames {
		frameLocations[i] = &FrameLocation{
			GOP:      gop,
			Position: i,
		}
	}
	
	// Now update the index atomically
	for i, frame := range gop.Frames {
		b.frameIndex[frame.ID] = frameLocations[i]
	}
	
	// Update metrics
	b.currentBytes += gop.TotalSize
	b.totalGOPs++
	
	// Update time bounds
	if b.oldestTime.IsZero() || gop.StartTime.Before(b.oldestTime) {
		b.oldestTime = gop.StartTime
	}
	if gop.StartTime.After(b.newestTime) {
		b.newestTime = gop.StartTime
	}
	
	// Enforce limits
	b.enforceBufferLimits()
	
	b.logger.WithFields(map[string]interface{}{
		"gop_id":       gop.ID,
		"frame_count":  gop.FrameCount,
		"size":         gop.TotalSize,
		"buffer_gops":  b.gops.Len(),
		"buffer_bytes": b.currentBytes,
	}).Debug("GOP added to buffer")
}

// enforceBufferLimits removes old GOPs to stay within limits
func (b *Buffer) enforceBufferLimits() {
	// Check GOP count limit
	for b.gops.Len() > b.maxGOPs {
		b.removeOldestGOP()
	}
	
	// Check byte limit
	for b.currentBytes > b.maxBytes && b.gops.Len() > 1 {
		b.removeOldestGOP()
	}
	
	// Check duration limit
	if b.maxDuration > 0 && b.gops.Len() > 1 {
		cutoff := b.newestTime.Add(-b.maxDuration)
		for {
			front := b.gops.Front()
			if front == nil {
				break
			}
			
			gop := front.Value.(*GOP)
			if gop.StartTime.After(cutoff) {
				break
			}
			
			b.removeOldestGOP()
		}
	}
}

// removeOldestGOP removes the oldest GOP from the buffer
func (b *Buffer) removeOldestGOP() {
	front := b.gops.Front()
	if front == nil {
		return
	}
	
	gop := front.Value.(*GOP)
	
	// Remove from indices
	delete(b.gopIndex, gop.ID)
	for _, frame := range gop.Frames {
		delete(b.frameIndex, frame.ID)
	}
	
	// Update metrics
	b.currentBytes -= gop.TotalSize
	b.droppedGOPs++
	b.droppedFrames += uint64(gop.FrameCount)
	
	// Remove from list
	b.gops.Remove(front)
	
	// Update oldest time
	if b.gops.Len() > 0 {
		b.oldestTime = b.gops.Front().Value.(*GOP).StartTime
	} else {
		b.oldestTime = time.Time{}
	}
	
	b.logger.WithFields(map[string]interface{}{
		"gop_id":      gop.ID,
		"frame_count": gop.FrameCount,
		"reason":      "buffer_limit",
	}).Debug("GOP removed from buffer")
}

// GetGOP retrieves a GOP by ID
func (b *Buffer) GetGOP(gopID uint64) *GOP {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	if elem, exists := b.gopIndex[gopID]; exists {
		return elem.Value.(*GOP)
	}
	return nil
}

// GetFrame retrieves a specific frame by ID
func (b *Buffer) GetFrame(frameID uint64) *types.VideoFrame {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	if loc, exists := b.frameIndex[frameID]; exists {
		if loc.GOP != nil && loc.Position < len(loc.GOP.Frames) {
			return loc.GOP.Frames[loc.Position]
		}
	}
	return nil
}

// GetRecentGOPs returns the most recent GOPs up to limit
func (b *Buffer) GetRecentGOPs(limit int) []*GOP {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	result := make([]*GOP, 0, limit)
	
	// Iterate from newest to oldest
	for elem := b.gops.Back(); elem != nil && len(result) < limit; elem = elem.Prev() {
		result = append(result, elem.Value.(*GOP))
	}
	
	// Reverse to get chronological order
	for i := 0; i < len(result)/2; i++ {
		result[i], result[len(result)-1-i] = result[len(result)-1-i], result[i]
	}
	
	return result
}

// DropFramesForPressure implements intelligent frame dropping based on pressure
func (b *Buffer) DropFramesForPressure(pressure float64) []*types.VideoFrame {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	var droppedFrames []*types.VideoFrame
	
	// No dropping needed at low pressure
	if pressure < 0.5 {
		return droppedFrames
	}
	
	// Strategy based on pressure level
	if pressure < 0.7 {
		// Drop B frames from oldest GOPs
		droppedFrames = b.dropBFrames(2) // Drop from 2 oldest GOPs
	} else if pressure < 0.85 {
		// Drop all B frames and some P frames
		droppedFrames = b.dropBFrames(-1) // Drop all B frames
		pDropped := b.dropPFrames(1)     // Drop P frames from oldest GOP
		droppedFrames = append(droppedFrames, pDropped...)
	} else if pressure < 0.95 {
		// Aggressive dropping - drop entire old GOPs except keyframes
		droppedFrames = b.dropOldGOPs(1, false) // Keep keyframes
	} else {
		// Extreme pressure - drop entire old GOPs
		if b.gops.Len() > 1 { // Keep at least one GOP
			droppedFrames = b.dropOldGOPs(1, true) // Drop everything
		} else {
			// Drop all non-keyframes from current GOP
			droppedFrames = b.dropAllNonKeyframes()
		}
	}
	
	b.droppedFrames += uint64(len(droppedFrames))
	
	if len(droppedFrames) > 0 {
		b.logger.WithFields(map[string]interface{}{
			"pressure":       pressure,
			"dropped_frames": len(droppedFrames),
		}).Info("Dropped frames due to pressure")
	}
	
	return droppedFrames
}

// dropBFrames drops B frames from the oldest GOPs
func (b *Buffer) dropBFrames(gopCount int) []*types.VideoFrame {
	var dropped []*types.VideoFrame
	count := 0
	
	for elem := b.gops.Front(); elem != nil && (gopCount < 0 || count < gopCount); elem = elem.Next() {
		gop := elem.Value.(*GOP)
		
		// Collect B frames to drop first to avoid modifying slice while iterating
		var bFramesToDrop []int
		for i, frame := range gop.Frames {
			if frame.Type == types.FrameTypeB {
				bFramesToDrop = append(bFramesToDrop, i)
			}
		}
		
		// Drop B frames from highest index to lowest to maintain slice integrity
		for i := len(bFramesToDrop) - 1; i >= 0; i-- {
			frameIdx := bFramesToDrop[i]
			frame := gop.Frames[frameIdx]
			dropped = append(dropped, frame)
			
			// Remove from GOP
			gop.Frames = append(gop.Frames[:frameIdx], gop.Frames[frameIdx+1:]...)
			gop.BFrames--
			gop.FrameCount--
			gop.TotalSize -= int64(frame.TotalSize)
			
			// Remove from index
			delete(b.frameIndex, frame.ID)
		}
		
		// Update GOP duration after dropping frames
		if len(bFramesToDrop) > 0 {
			gop.UpdateDuration()
		}
		
		count++
	}
	
	return dropped
}

// dropPFrames drops P frames that have no dependencies
func (b *Buffer) dropPFrames(gopCount int) []*types.VideoFrame {
	var dropped []*types.VideoFrame
	count := 0
	
	for elem := b.gops.Front(); elem != nil && count < gopCount; elem = elem.Next() {
		gop := elem.Value.(*GOP)
		
		// Drop P frames that can be safely removed
		for i := len(gop.Frames) - 1; i >= 0; i-- {
			if gop.CanDropFrame(i) && gop.Frames[i].Type == types.FrameTypeP {
				frame := gop.Frames[i]
				dropped = append(dropped, frame)
				
				// Remove from GOP
				gop.Frames = append(gop.Frames[:i], gop.Frames[i+1:]...)
				gop.PFrames--
				gop.FrameCount--
				gop.TotalSize -= int64(frame.TotalSize)
				
				// Remove from index
				delete(b.frameIndex, frame.ID)
			}
		}
		
		// Update GOP duration after dropping frames
		gop.UpdateDuration()
		
		count++
	}
	
	return dropped
}

// dropOldGOPs drops entire GOPs
func (b *Buffer) dropOldGOPs(count int, includeKeyframes bool) []*types.VideoFrame {
	var dropped []*types.VideoFrame
	
	for i := 0; i < count && b.gops.Len() > 1; i++ {
		front := b.gops.Front()
		if front == nil {
			break
		}
		
		gop := front.Value.(*GOP)
		
		if includeKeyframes {
			// Drop entire GOP
			dropped = append(dropped, gop.Frames...)
			b.removeOldestGOP()
		} else {
			// Drop all except keyframe
			for _, frame := range gop.Frames {
				if !frame.IsKeyframe() {
					dropped = append(dropped, frame)
					delete(b.frameIndex, frame.ID)
				}
			}
			
			// Update GOP to only contain keyframe
			if gop.Keyframe != nil {
				gop.Frames = []*types.VideoFrame{gop.Keyframe}
				gop.FrameCount = 1
				gop.PFrames = 0
				gop.BFrames = 0
				gop.TotalSize = int64(gop.Keyframe.TotalSize)
			}
		}
	}
	
	return dropped
}

// GetStatistics returns buffer statistics
func (b *Buffer) GetStatistics() BufferStatistics {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	stats := BufferStatistics{
		StreamID:      b.streamID,
		GOPCount:      b.gops.Len(),
		FrameCount:    0,
		TotalBytes:    b.currentBytes,
		OldestTime:    b.oldestTime,
		NewestTime:    b.newestTime,
		TotalGOPs:     b.totalGOPs,
		DroppedGOPs:   b.droppedGOPs,
		DroppedFrames: b.droppedFrames,
	}
	
	// Count frames and types
	for elem := b.gops.Front(); elem != nil; elem = elem.Next() {
		gop := elem.Value.(*GOP)
		stats.FrameCount += gop.FrameCount
		stats.IFrames += gop.IFrames
		stats.PFrames += gop.PFrames
		stats.BFrames += gop.BFrames
	}
	
	if !stats.OldestTime.IsZero() && !stats.NewestTime.IsZero() {
		stats.Duration = stats.NewestTime.Sub(stats.OldestTime)
	}
	
	return stats
}

// BufferStatistics contains GOP buffer statistics
type BufferStatistics struct {
	StreamID      string
	GOPCount      int
	FrameCount    int
	IFrames       int
	PFrames       int
	BFrames       int
	TotalBytes    int64
	Duration      time.Duration
	OldestTime    time.Time
	NewestTime    time.Time
	TotalGOPs     uint64
	DroppedGOPs   uint64
	DroppedFrames uint64
}

// dropAllNonKeyframes drops all non-keyframes from all GOPs
func (b *Buffer) dropAllNonKeyframes() []*types.VideoFrame {
	var dropped []*types.VideoFrame
	
	for elem := b.gops.Front(); elem != nil; elem = elem.Next() {
		gop := elem.Value.(*GOP)
		
		// Drop all non-keyframes
		newFrames := make([]*types.VideoFrame, 0, 1)
		for _, frame := range gop.Frames {
			if frame.IsKeyframe() {
				newFrames = append(newFrames, frame)
			} else {
				dropped = append(dropped, frame)
				delete(b.frameIndex, frame.ID)
			}
		}
		
		// Update GOP
		gop.Frames = newFrames
		gop.FrameCount = len(newFrames)
		gop.PFrames = 0
		gop.BFrames = 0
		if len(newFrames) > 0 {
			gop.TotalSize = int64(newFrames[0].TotalSize)
		}
	}
	
	return dropped
}

// Clear removes all GOPs from the buffer
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	b.gops.Init()
	b.gopIndex = make(map[uint64]*list.Element)
	b.frameIndex = make(map[uint64]*FrameLocation)
	b.currentBytes = 0
	b.oldestTime = time.Time{}
	b.newestTime = time.Time{}
}

// DropFramesFromGOP removes frames from a specific GOP starting at the given index
// This method properly updates the frame index and GOP statistics
func (b *Buffer) DropFramesFromGOP(gopID uint64, startIndex int) []*types.VideoFrame {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	// Find the GOP
	elem, exists := b.gopIndex[gopID]
	if !exists {
		return nil
	}
	
	gop := elem.Value.(*GOP)
	if startIndex < 0 || startIndex >= len(gop.Frames) {
		return nil
	}
	
	// Collect frames to drop
	droppedFrames := make([]*types.VideoFrame, len(gop.Frames[startIndex:]))
	copy(droppedFrames, gop.Frames[startIndex:])
	
	// Remove frames from GOP
	gop.Frames = gop.Frames[:startIndex]
	
	// Update frame index - remove dropped frames
	for _, frame := range droppedFrames {
		delete(b.frameIndex, frame.ID)
		b.currentBytes -= int64(frame.TotalSize)
	}
	
	// Update GOP statistics
	gop.FrameCount = len(gop.Frames)
	newTotalSize := int64(0)
	for _, f := range gop.Frames {
		newTotalSize += int64(f.TotalSize)
	}
	gop.TotalSize = newTotalSize
	gop.UpdateDuration()
	
	// Update frame counts
	for _, frame := range droppedFrames {
		switch frame.Type {
		case types.FrameTypeI, types.FrameTypeIDR:
			gop.IFrames--
		case types.FrameTypeP:
			gop.PFrames--
		case types.FrameTypeB:
			gop.BFrames--
		}
	}
	
	b.droppedFrames += uint64(len(droppedFrames))
	
	return droppedFrames
}
