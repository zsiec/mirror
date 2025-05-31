package gop

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// Simple bit reader for PPS ID extraction during debugging
type simpleBitReader struct {
	data    []byte
	bytePos int
	bitPos  int
}

func newSimpleBitReader(data []byte) *simpleBitReader {
	return &simpleBitReader{data: data}
}

func (br *simpleBitReader) readBit() (uint8, error) {
	if br.bytePos >= len(br.data) {
		return 0, fmt.Errorf("end of data")
	}
	bit := (br.data[br.bytePos] >> (7 - br.bitPos)) & 1
	br.bitPos++
	if br.bitPos >= 8 {
		br.bitPos = 0
		br.bytePos++
	}
	return bit, nil
}

func (br *simpleBitReader) readBits(n int) (uint32, error) {
	var result uint32
	for i := 0; i < n; i++ {
		bit, err := br.readBit()
		if err != nil {
			return 0, err
		}
		result = (result << 1) | uint32(bit)
	}
	return result, nil
}

func (br *simpleBitReader) readUE() (uint32, error) {
	leadingZeros := 0
	for {
		bit, err := br.readBit()
		if err != nil {
			return 0, err
		}
		if bit == 1 {
			break
		}
		leadingZeros++
		if leadingZeros > 32 {
			return 0, fmt.Errorf("invalid exp-golomb")
		}
	}
	if leadingZeros == 0 {
		return 0, nil
	}
	value := uint32(0)
	for i := 0; i < leadingZeros; i++ {
		bit, err := br.readBit()
		if err != nil {
			return 0, err
		}
		value = (value << 1) | uint32(bit)
	}
	return (1 << leadingZeros) - 1 + value, nil
}

// countIFrames counts the number of I-frames in a GOP
func countIFrames(gop *types.GOP) int {
	count := 0
	for _, frame := range gop.Frames {
		if frame.IsKeyframe() {
			count++
		}
	}
	return count
}

// Buffer maintains a buffer of complete GOPs for intelligent frame management
type Buffer struct {
	streamID string

	// GOP storage
	gops     *list.List               // List of *types.GOP
	gopIndex map[uint64]*list.Element // Quick lookup by GOP ID

	// Buffer limits
	maxGOPs     int
	maxBytes    int64
	maxDuration time.Duration

	// Current state
	currentBytes int64
	oldestTime   time.Time
	newestTime   time.Time

	// Frame index for quick lookup
	frameIndex map[uint64]*FrameLocation // Frame ID -> location

	// Advanced parameter set management
	parameterContext *types.ParameterSetContext // Production-quality parameter set management

	// Callback for parameter set preservation before GOP drop
	onGOPDrop func(*types.GOP, *types.ParameterSetContext)

	// Statistics
	totalGOPs     uint64
	droppedGOPs   uint64
	droppedFrames uint64

	mu     sync.RWMutex
	logger logger.Logger
}

// FrameLocation tracks where a frame is stored
type FrameLocation struct {
	GOP      *types.GOP
	Position int
}

// BufferConfig configures the GOP buffer
type BufferConfig struct {
	MaxGOPs     int             // Maximum number of GOPs to buffer
	MaxBytes    int64           // Maximum buffer size in bytes
	MaxDuration time.Duration   // Maximum time span of buffered content
	Codec       types.CodecType // Video codec for parameter set parsing
}

// NewBuffer creates a new GOP buffer
func NewBuffer(streamID string, config BufferConfig, logger logger.Logger) *Buffer {
	return &Buffer{
		streamID:         streamID,
		gops:             list.New(),
		gopIndex:         make(map[uint64]*list.Element),
		frameIndex:       make(map[uint64]*FrameLocation),
		parameterContext: types.NewParameterSetContext(config.Codec, streamID),
		maxGOPs:          config.MaxGOPs,
		maxBytes:         config.MaxBytes,
		maxDuration:      config.MaxDuration,
		logger:           logger.WithField("component", "gop_buffer"),
		onGOPDrop:        nil, // Will be set by StreamHandler
	}
}

// SetGOPDropCallback sets the callback function called before GOPs are dropped
func (b *Buffer) SetGOPDropCallback(callback func(*types.GOP, *types.ParameterSetContext)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onGOPDrop = callback
}

// AddGOP adds a complete GOP to the buffer
func (b *Buffer) AddGOP(gop *types.GOP) {
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

	// Update frame index atomically - create and assign locations in single operation
	// to prevent race conditions with frame index corruption
	for i, frame := range gop.Frames {
		b.frameIndex[frame.ID] = &FrameLocation{
			GOP:      gop,
			Position: i,
		}
	}

	// Extract and cache parameter sets from this GOP
	b.logger.WithFields(map[string]interface{}{
		"stream_id":  b.streamID,
		"gop_id":     gop.ID,
		"gop_frames": len(gop.Frames),
		"total_nals": b.countTotalNALUnits(gop),
	}).Info("🔬 Extracting parameter sets using parsing")

	// Extract parameter sets using unified approach
	b.extractParameterSetsFromGOP(gop, b.parameterContext)

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
		"frame_count":  len(gop.Frames),
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

			gop := front.Value.(*types.GOP)
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

	gop := front.Value.(*types.GOP)

	if b.onGOPDrop != nil {
		b.onGOPDrop(gop, b.parameterContext)
	}

	// Remove from indices
	delete(b.gopIndex, gop.ID)
	for _, frame := range gop.Frames {
		delete(b.frameIndex, frame.ID)
	}

	// Update metrics
	b.currentBytes -= gop.TotalSize
	b.droppedGOPs++
	b.droppedFrames += uint64(len(gop.Frames))

	// Remove from list
	b.gops.Remove(front)

	// Update oldest time
	if b.gops.Len() > 0 {
		b.oldestTime = b.gops.Front().Value.(*types.GOP).StartTime
	} else {
		b.oldestTime = time.Time{}
	}

	b.logger.WithFields(map[string]interface{}{
		"gop_id":      gop.ID,
		"frame_count": len(gop.Frames),
		"reason":      "buffer_limit",
	}).Debug("GOP removed from buffer")
}

// GetGOP retrieves a GOP by ID
func (b *Buffer) GetGOP(gopID uint64) *types.GOP {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if elem, exists := b.gopIndex[gopID]; exists {
		return elem.Value.(*types.GOP)
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
func (b *Buffer) GetRecentGOPs(limit int) []*types.GOP {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]*types.GOP, 0, limit)

	// Iterate from newest to oldest
	for elem := b.gops.Back(); elem != nil && len(result) < limit; elem = elem.Prev() {
		result = append(result, elem.Value.(*types.GOP))
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
		pDropped := b.dropPFrames(1)      // Drop P frames from oldest GOP
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
		gop := elem.Value.(*types.GOP)

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

			// Remove from index first to prevent corruption
			delete(b.frameIndex, frame.ID)

			// Remove from GOP
			gop.Frames = append(gop.Frames[:frameIdx], gop.Frames[frameIdx+1:]...)
			gop.BFrameCount--
			// FrameCount is now len(gop.Frames) - automatically updated
			gop.TotalSize -= int64(frame.TotalSize)

			// Update frame indices for remaining frames after removal
			b.updateFrameIndicesAfterRemoval(gop, frameIdx)
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
		gop := elem.Value.(*types.GOP)

		// Drop P frames that can be safely removed
		for i := len(gop.Frames) - 1; i >= 0; i-- {
			if gop.CanDropFrame(i) && gop.Frames[i].Type == types.FrameTypeP {
				frame := gop.Frames[i]
				dropped = append(dropped, frame)

				// Remove from index first to prevent corruption
				delete(b.frameIndex, frame.ID)

				// Remove from GOP
				gop.Frames = append(gop.Frames[:i], gop.Frames[i+1:]...)
				gop.PFrameCount--
				// FrameCount is now len(gop.Frames) - automatically updated
				gop.TotalSize -= int64(frame.TotalSize)

				// Update frame indices for remaining frames after removal
				b.updateFrameIndicesAfterRemoval(gop, i)
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

		gop := front.Value.(*types.GOP)

		if includeKeyframes {
			// Drop entire GOP
			dropped = append(dropped, gop.Frames...)
			b.removeOldestGOP()
		} else {
			// Drop all except keyframe - collect and remove atomically
			var newFrames []*types.VideoFrame
			for _, frame := range gop.Frames {
				if frame.IsKeyframe() {
					newFrames = append(newFrames, frame)
				} else {
					dropped = append(dropped, frame)
					delete(b.frameIndex, frame.ID)
				}
			}

			// Update GOP atomically
			gop.Frames = newFrames
			gop.PFrameCount = 0
			gop.BFrameCount = 0
			if len(newFrames) > 0 {
				gop.TotalSize = int64(newFrames[0].TotalSize)
				// Update frame index for the remaining keyframe
				b.frameIndex[newFrames[0].ID] = &FrameLocation{
					GOP:      gop,
					Position: 0,
				}
			} else {
				gop.TotalSize = 0
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
		gop := elem.Value.(*types.GOP)
		stats.FrameCount += len(gop.Frames)
		stats.IFrames += countIFrames(gop)
		stats.PFrames += gop.PFrameCount
		stats.BFrames += gop.BFrameCount
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
		gop := elem.Value.(*types.GOP)

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

		// Update GOP atomically
		gop.Frames = newFrames
		// FrameCount is now len(gop.Frames) - automatically updated
		gop.PFrameCount = 0
		gop.BFrameCount = 0
		if len(newFrames) > 0 {
			gop.TotalSize = int64(newFrames[0].TotalSize)
			// Update frame index for remaining keyframes
			for i, frame := range newFrames {
				b.frameIndex[frame.ID] = &FrameLocation{
					GOP:      gop,
					Position: i,
				}
			}
		} else {
			gop.TotalSize = 0
		}
	}

	return dropped
}

// GetLatestIFrame returns the most recent iframe from the buffer
func (b *Buffer) GetLatestIFrame() *types.VideoFrame {
	b.mu.RLock()
	defer b.mu.RUnlock()

	b.logger.WithFields(map[string]interface{}{
		"stream_id":     b.streamID,
		"gop_count":     b.gops.Len(),
		"current_bytes": b.currentBytes,
		"total_gops":    b.totalGOPs,
	}).Info("🔍 Searching for latest iframe in GOP buffer")

	gopCount := 0
	frameCount := 0

	// Search from newest to oldest GOP
	for elem := b.gops.Back(); elem != nil; elem = elem.Prev() {
		gop := elem.Value.(*types.GOP)
		gopCount++

		b.logger.WithFields(map[string]interface{}{
			"stream_id":    b.streamID,
			"gop_id":       gop.ID,
			"gop_frames":   len(gop.Frames),
			"gop_size":     gop.TotalSize,
			"gop_position": gopCount,
			"i_frames":     countIFrames(gop),
			"p_frames":     gop.PFrameCount,
			"b_frames":     gop.BFrameCount,
		}).Debug("🔎 Examining GOP for iframe")

		// Find the iframe (keyframe) in this GOP
		for frameIdx, frame := range gop.Frames {
			frameCount++

			b.logger.WithFields(map[string]interface{}{
				"stream_id":      b.streamID,
				"gop_id":         gop.ID,
				"frame_id":       frame.ID,
				"frame_type":     frame.Type.String(),
				"frame_position": frameIdx,
				"is_keyframe":    frame.IsKeyframe(),
				"frame_size":     frame.TotalSize,
				"nal_units":      len(frame.NALUnits),
				"pts":            frame.PTS,
			}).Debug("🎞️  Checking frame")

			if frame.IsKeyframe() {
				b.logger.WithFields(map[string]interface{}{
					"stream_id":       b.streamID,
					"found_frame_id":  frame.ID,
					"found_gop_id":    gop.ID,
					"gops_searched":   gopCount,
					"frames_searched": frameCount,
					"frame_type":      frame.Type.String(),
					"frame_size":      frame.TotalSize,
					"nal_units":       len(frame.NALUnits),
					"pts":             frame.PTS,
					"capture_time":    frame.CaptureTime,
					"complete_time":   frame.CompleteTime,
				}).Info("✅ Found latest iframe")
				return frame
			}
		}
	}

	b.logger.WithFields(map[string]interface{}{
		"stream_id":       b.streamID,
		"gops_searched":   gopCount,
		"frames_searched": frameCount,
		"total_gops":      b.gops.Len(),
	}).Warn("❌ No iframe found in GOP buffer")

	return nil
}

// extractVideoContext attempts to extract video context from frame and parameter sets
func (b *Buffer) extractVideoContext(frame *types.VideoFrame, codec types.CodecType) *types.VideoContext {
	// For now, return basic context - in the future we could parse SPS to get resolution, etc.
	return &types.VideoContext{
		FrameRate: types.Rational{Num: 30, Den: 1},    // Default to 30fps
		TimeBase:  types.Rational{Num: 1, Den: 90000}, // 90kHz timebase
		Profile:   "main",                             // Default profile
	}
}

// extractHEVCParameterSets extracts HEVC parameter sets from NAL units
func (b *Buffer) extractHEVCParameterSets(paramContext *types.ParameterSetContext, nalUnit types.NALUnit, nalType uint8) {
	hevcNalType := (nalUnit.Data[0] >> 1) & 0x3F
	switch hevcNalType {
	case 32: // VPS
		// HEVC VPS handling would go here
		b.logger.Debug("Found HEVC VPS - handling not implemented yet")
	case 33: // SPS
		// HEVC SPS handling would go here
		b.logger.Debug("Found HEVC SPS - handling not implemented yet")
	case 34: // PPS
		// HEVC PPS handling would go here
		b.logger.Debug("Found HEVC PPS - handling not implemented yet")
	}
}

// getGOPPosition returns the position of a GOP element in the list (0-based from front)
func (b *Buffer) getGOPPosition(elem *list.Element) int {
	pos := 0
	for e := b.gops.Front(); e != nil; e = e.Next() {
		if e == elem {
			return pos
		}
		pos++
	}
	return -1 // Not found
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

	gop := elem.Value.(*types.GOP)
	if startIndex < 0 || startIndex >= len(gop.Frames) {
		return nil
	}

	// Collect frames to drop
	droppedFrames := make([]*types.VideoFrame, len(gop.Frames[startIndex:]))
	copy(droppedFrames, gop.Frames[startIndex:])

	// Remove frames from GOP
	gop.Frames = gop.Frames[:startIndex]

	// Update frame index - remove dropped frames and update positions atomically
	for _, frame := range droppedFrames {
		delete(b.frameIndex, frame.ID)
		b.currentBytes -= int64(frame.TotalSize)
	}

	// Update frame indices for remaining frames
	for i, frame := range gop.Frames {
		b.frameIndex[frame.ID] = &FrameLocation{
			GOP:      gop,
			Position: i,
		}
	}

	// Update GOP statistics
	// FrameCount is now len(gop.Frames) - automatically updated
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
			// IFrames count is derived from countIFrames(gop)
		case types.FrameTypeP:
			gop.PFrameCount--
		case types.FrameTypeB:
			gop.BFrameCount--
		}
	}

	b.droppedFrames += uint64(len(droppedFrames))

	return droppedFrames
}

// minInt returns the minimum of two integers
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// countTotalNALUnits counts NAL units across all frames in a GOP
func (b *Buffer) countTotalNALUnits(gop *types.GOP) int {
	total := 0
	for _, frame := range gop.Frames {
		total += len(frame.NALUnits)
	}
	return total
}

// extractParameterSetsFromGOP extracts parameter sets from a single GOP using unified approach
func (b *Buffer) extractParameterSetsFromGOP(gop *types.GOP, paramContext *types.ParameterSetContext) {
	for _, frame := range gop.Frames {
		for _, nalUnit := range frame.NALUnits {
			if len(nalUnit.Data) == 0 {
				continue
			}

			nalType := nalUnit.Type
			if nalType == 0 && len(nalUnit.Data) > 0 {
				nalType = nalUnit.Data[0] & 0x1F
			}

			// Use the unified extraction method
			b.ExtractParameterSetFromNAL(paramContext, nalUnit, nalType, gop.ID)
		}
	}
}

// updateFrameIndicesAfterRemoval updates frame indices after a frame is removed from a GOP
// This prevents frame index corruption when frame positions change
func (b *Buffer) updateFrameIndicesAfterRemoval(gop *types.GOP, removedIndex int) {
	// Update positions for all frames after the removed index
	for i := removedIndex; i < len(gop.Frames); i++ {
		frame := gop.Frames[i]
		if location, exists := b.frameIndex[frame.ID]; exists {
			location.Position = i
		}
	}
}

// ExtractParameterSetFromNAL extracts parameter sets from a single NAL unit (unified method)
// Made public for reuse by StreamHandler session cache
func (b *Buffer) ExtractParameterSetFromNAL(paramContext *types.ParameterSetContext, nalUnit types.NALUnit, nalType uint8, gopID uint64) bool {
	switch nalType {
	case 7: // H.264 SPS
		// Construct proper NAL unit with header if needed
		var spsData []byte
		if len(nalUnit.Data) > 0 && nalUnit.Data[0] != 0x67 {
			// Add NAL header if missing
			spsData = make([]byte, len(nalUnit.Data)+1)
			spsData[0] = 0x67 // H.264 SPS NAL header
			copy(spsData[1:], nalUnit.Data)
		} else {
			// Use data as-is if header already present
			spsData = nalUnit.Data
		}

		if err := paramContext.AddSPS(spsData); err != nil {
			// **CRITICAL DEBUG: Log SPS addition failures with detailed context**
			maxBytes := len(spsData)
			if maxBytes > 20 {
				maxBytes = 20
			}
			b.logger.WithFields(map[string]interface{}{
				"stream_id":  b.streamID,
				"gop_id":     gopID,
				"error":      err.Error(),
				"nal_size":   len(spsData),
				"raw_bytes":  fmt.Sprintf("%x", spsData[:maxBytes]),
				"has_header": len(spsData) > 0 && spsData[0] == 0x67,
				"issue":      "SPS_ADDITION_FAILED",
			}).Error("💥 CRITICAL: Failed to add H.264 SPS - this will prevent iframe generation")
			return false
		}

		// **ENHANCED DEBUGGING: Extract and log the SPS ID**
		spsID := uint8(255) // Default invalid ID
		if len(spsData) >= 2 {
			// Parse SPS ID from the data to aid debugging
			spsPayload := spsData[1:] // Skip NAL header
			if len(spsPayload) > 0 {
				// Simple parsing to extract SPS ID
				bitReader := newSimpleBitReader(spsPayload)
				// Skip profile_idc (8 bits), constraint flags (8 bits), level_idc (8 bits)
				if _, err := bitReader.readBits(24); err == nil {
					if id, err := bitReader.readUE(); err == nil && id <= 31 {
						spsID = uint8(id)
					}
				}
			}
		}

		b.logger.WithFields(map[string]interface{}{
			"stream_id": b.streamID,
			"gop_id":    gopID,
			"nal_size":  len(spsData),
			"sps_id":    spsID,
		}).Debug("Successfully added H.264 SPS")
		return true

	case 8: // H.264 PPS
		// Construct proper NAL unit with header if needed
		var ppsData []byte
		if len(nalUnit.Data) > 0 && nalUnit.Data[0] != 0x68 {
			// Add NAL header if missing
			ppsData = make([]byte, len(nalUnit.Data)+1)
			ppsData[0] = 0x68 // H.264 PPS NAL header
			copy(ppsData[1:], nalUnit.Data)
		} else {
			// Use data as-is if header already present
			ppsData = nalUnit.Data
		}

		// **ENHANCED DEBUGGING: Extract and log the PPS ID**
		ppsID := uint8(255) // Default invalid ID
		parseError := ""
		if len(ppsData) >= 2 {
			// Parse PPS ID from the data to aid debugging
			ppsPayload := ppsData[1:] // Skip NAL header
			if len(ppsPayload) > 0 {
				// Simple parsing to extract PPS ID (first few bits)
				bitReader := newSimpleBitReader(ppsPayload)
				if id, err := bitReader.readUE(); err == nil && id <= 255 {
					ppsID = uint8(id)
				} else {
					parseError = fmt.Sprintf("PPS ID parse failed: %v", err)
				}
			}
		}

		// **DEBUG: Log all PPS attempts, especially for IDs we're missing**
		maxBytes := len(ppsData)
		if maxBytes > 10 {
			maxBytes = 10
		}
		b.logger.WithFields(map[string]interface{}{
			"stream_id":   b.streamID,
			"gop_id":      gopID,
			"nal_size":    len(ppsData),
			"pps_id":      ppsID,
			"parse_error": parseError,
			"raw_bytes":   fmt.Sprintf("%x", ppsData[:maxBytes]),
		}).Debug("🔍 Processing PPS NAL unit")

		if err := paramContext.AddPPS(ppsData); err != nil {
			b.logger.WithFields(map[string]interface{}{
				"stream_id": b.streamID,
				"gop_id":    gopID,
				"error":     err.Error(),
				"nal_size":  len(ppsData),
				"pps_id":    ppsID,
			}).Warn("Failed to add H.264 PPS - this may cause decoding issues")
			return false
		}

		b.logger.WithFields(map[string]interface{}{
			"stream_id": b.streamID,
			"gop_id":    gopID,
			"nal_size":  len(ppsData),
			"pps_id":    ppsID,
		}).Debug("Successfully added H.264 PPS")
		return true
	}
	return false
}
