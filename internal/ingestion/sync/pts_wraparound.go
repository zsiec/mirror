package sync

import (
	"github.com/zsiec/mirror/internal/ingestion/types"
)

// PTSWrapDetector handles PTS wraparound detection for different time bases
type PTSWrapDetector struct {
	timeBase      types.Rational
	wrapThreshold int64
	halfThreshold int64
}

// NewPTSWrapDetector creates a new PTS wraparound detector
func NewPTSWrapDetector(timeBase types.Rational) *PTSWrapDetector {
	// Calculate wrap threshold based on time base
	wrapThreshold := calculateWrapThreshold(timeBase)

	return &PTSWrapDetector{
		timeBase:      timeBase,
		wrapThreshold: wrapThreshold,
		halfThreshold: wrapThreshold / 2,
	}
}

// calculateWrapThreshold determines the appropriate wrap threshold based on time base
func calculateWrapThreshold(timeBase types.Rational) int64 {
	// Default to 32-bit for most video streams (90kHz)
	if timeBase.Den == 90000 && timeBase.Num == 1 {
		return 1 << 32 // 2^32 for standard video
	}

	// 33-bit for 48kHz audio
	if timeBase.Den == 48000 && timeBase.Num == 1 {
		return 1 << 33 // 2^33 for 48kHz audio
	}

	// 33-bit for 44.1kHz audio
	if timeBase.Den == 44100 && timeBase.Num == 1 {
		return 1 << 33 // 2^33 for 44.1kHz audio
	}

	// For other time bases, calculate based on expected duration
	// Assume we want to wrap after ~26 hours (typical for 32-bit at 90kHz)
	hoursBeforeWrap := int64(26)
	secondsBeforeWrap := hoursBeforeWrap * 3600

	// Calculate how many time base units in the target duration
	// units = seconds * den / num
	return (secondsBeforeWrap * int64(timeBase.Den)) / int64(timeBase.Num)
}

// DetectWrap checks if PTS has wrapped around
func (d *PTSWrapDetector) DetectWrap(currentPTS, lastPTS int64) bool {
	// If current is less than last and the difference is more than half the threshold,
	// it's likely a wrap
	if currentPTS < lastPTS {
		diff := lastPTS - currentPTS
		if diff > d.halfThreshold {
			return true
		}
	}

	return false
}

// UnwrapPTS adjusts PTS value accounting for wrap count
func (d *PTSWrapDetector) UnwrapPTS(pts int64, wrapCount int) int64 {
	// Add the wrap threshold for each wrap
	return pts + int64(wrapCount)*d.wrapThreshold
}

// GetWrapThreshold returns the wrap threshold for this detector
func (d *PTSWrapDetector) GetWrapThreshold() int64 {
	return d.wrapThreshold
}

// IsLikelyDiscontinuity checks if the PTS difference indicates a discontinuity rather than wrap
func (d *PTSWrapDetector) IsLikelyDiscontinuity(currentPTS, lastPTS int64) bool {
	// Calculate absolute difference
	diff := abs(currentPTS - lastPTS)

	// If the difference is less than quarter of wrap threshold but more than
	// a reasonable frame interval, it's likely a discontinuity
	quarterThreshold := d.wrapThreshold / 4

	// Assume max reasonable frame interval is 1 second
	oneSecondInPTS := int64(d.timeBase.Den) / int64(d.timeBase.Num)

	return diff > oneSecondInPTS && diff < quarterThreshold
}

// CalculatePTSDelta calculates the actual PTS delta accounting for potential wraps
func (d *PTSWrapDetector) CalculatePTSDelta(currentPTS, lastPTS int64, wrapCount int) int64 {
	// Unwrap both PTS values
	unwrappedCurrent := d.UnwrapPTS(currentPTS, wrapCount)
	unwrappedLast := d.UnwrapPTS(lastPTS, wrapCount)

	// If we detect a wrap between these two values, adjust
	if d.DetectWrap(currentPTS, lastPTS) {
		unwrappedCurrent += d.wrapThreshold
	}

	return unwrappedCurrent - unwrappedLast
}
