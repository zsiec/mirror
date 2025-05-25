package timestamp

import (
	"sync"
	"time"
)

// TimestampMapper converts RTP timestamps to PTS/DTS with wrap handling
type TimestampMapper struct {
	clockRate       uint32       // RTP clock rate (90000 for video, varies for audio)
	baseRTPTime     uint32       // First RTP timestamp seen
	baseWallTime    time.Time    // Wall clock of first packet
	basePTS         int64        // Base PTS value (usually 0)
	
	// Wrap detection
	wrapCount       int          // Number of timestamp wraps detected
	lastRTPTime     uint32       // Last RTP timestamp seen
	
	// For calculating expected timestamps
	lastPTS         int64        // Last calculated PTS
	expectedDelta   int64        // Expected timestamp delta between packets
	
	mu              sync.RWMutex
}

// NewTimestampMapper creates a new timestamp mapper
func NewTimestampMapper(clockRate uint32) *TimestampMapper {
	if clockRate == 0 {
		clockRate = 90000 // Default to video clock rate
	}
	
	return &TimestampMapper{
		clockRate:     clockRate,
		expectedDelta: int64(clockRate / 30), // Assume 30fps as default
	}
}

// SetExpectedFrameRate sets the expected frame rate for validation
func (tm *TimestampMapper) SetExpectedFrameRate(fps float64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if fps > 0 {
		tm.expectedDelta = int64(float64(tm.clockRate) / fps)
	}
}

// ToPTS converts an RTP timestamp to PTS
func (tm *TimestampMapper) ToPTS(rtpTimestamp uint32) int64 {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	// Initialize on first packet
	if tm.baseWallTime.IsZero() {
		tm.baseRTPTime = rtpTimestamp
		tm.baseWallTime = time.Now()
		tm.lastRTPTime = rtpTimestamp
		return tm.basePTS
	}
	
	// Detect timestamp wrap
	// RTP timestamps are 32-bit and wrap around at 2^32
	if rtpTimestamp < tm.lastRTPTime {
		// Check if this is a wrap (large backward jump)
		if (tm.lastRTPTime - rtpTimestamp) > 0x80000000 {
			tm.wrapCount++
		}
	}
	
	// Calculate PTS accounting for wraps
	// Use 64-bit arithmetic to handle wrap correctly
	pts := int64(tm.wrapCount) * (int64(1) << 32) // Wrapped portion
	pts += int64(rtpTimestamp)                      // Current timestamp
	pts -= int64(tm.baseRTPTime)                   // Subtract base
	
	// Add base PTS
	pts += tm.basePTS
	
	// Update state
	tm.lastRTPTime = rtpTimestamp
	tm.lastPTS = pts
	
	return pts
}

// ToPTSWithDTS converts RTP timestamp to both PTS and DTS
// For streams with B-frames, DTS may differ from PTS
func (tm *TimestampMapper) ToPTSWithDTS(rtpTimestamp uint32, hasBFrames bool, frameType string) (pts, dts int64) {
	pts = tm.ToPTS(rtpTimestamp)
	
	// For streams without B-frames, DTS equals PTS
	if !hasBFrames {
		return pts, pts
	}
	
	// For streams with B-frames, we need more complex logic
	// This is a simplified version - real implementation would need frame reordering info
	dts = pts
	
	// B-frames have DTS < PTS
	if frameType == "B" {
		// B-frames are typically displayed 2-3 frames after decode
		dts = pts - (2 * tm.expectedDelta)
	}
	
	return pts, dts
}

// GetWallClockTime converts a PTS to wall clock time
func (tm *TimestampMapper) GetWallClockTime(pts int64) time.Time {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	if tm.baseWallTime.IsZero() {
		return time.Time{}
	}
	
	// Calculate PTS delta from base
	ptsDelta := pts - tm.basePTS
	
	// Convert to nanoseconds
	// PTS is in clock rate units, convert to nanoseconds
	nsDelta := (ptsDelta * int64(time.Second)) / int64(tm.clockRate)
	
	// Add to base wall time
	return tm.baseWallTime.Add(time.Duration(nsDelta))
}

// Reset resets the mapper state
func (tm *TimestampMapper) Reset() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	tm.baseRTPTime = 0
	tm.baseWallTime = time.Time{}
	tm.basePTS = 0
	tm.wrapCount = 0
	tm.lastRTPTime = 0
	tm.lastPTS = 0
}

// GetClockRate returns the current clock rate
func (tm *TimestampMapper) GetClockRate() uint32 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.clockRate
}

// SetBasePTS sets the base PTS value (useful for continuous streams)
func (tm *TimestampMapper) SetBasePTS(basePTS int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.basePTS = basePTS
}

// GetWrapCount returns the number of timestamp wraps detected
func (tm *TimestampMapper) GetWrapCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.wrapCount
}

// ValidateTimestamp checks if a timestamp is reasonable
func (tm *TimestampMapper) ValidateTimestamp(rtpTimestamp uint32) bool {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	// First timestamp is always valid
	if tm.lastRTPTime == 0 {
		return true
	}
	
	// Calculate delta
	var delta int64
	if rtpTimestamp >= tm.lastRTPTime {
		delta = int64(rtpTimestamp - tm.lastRTPTime)
	} else {
		// Handle potential wrap
		delta = int64(uint64(rtpTimestamp) + (1 << 32) - uint64(tm.lastRTPTime))
	}
	
	// Check if delta is reasonable (not more than 10 seconds)
	maxDelta := int64(tm.clockRate * 10)
	return delta < maxDelta
}

// TimestampStats contains timestamp statistics
type TimestampStats struct {
	BaseRTPTime     uint32
	LastRTPTime     uint32
	WrapCount       int
	LastPTS         int64
	ClockRate       uint32
	BaseWallTime    time.Time
}

// GetStats returns current timestamp statistics
func (tm *TimestampMapper) GetStats() TimestampStats {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	return TimestampStats{
		BaseRTPTime:  tm.baseRTPTime,
		LastRTPTime:  tm.lastRTPTime,
		WrapCount:    tm.wrapCount,
		LastPTS:      tm.lastPTS,
		ClockRate:    tm.clockRate,
		BaseWallTime: tm.baseWallTime,
	}
}
