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
func calculateWrapThreshold(_ types.Rational) int64 {
	// MPEG-TS PTS/DTS are always 33-bit values per ISO 13818-1 Section 2.4.3.7,
	// regardless of clock rate. At 90kHz this wraps at ~26.5 hours.
	return 1 << 33
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

// CalculatePTSDelta calculates the actual PTS delta accounting for potential wraps.
// wrapCount is the number of wraps that currentPTS has undergone since lastPTS was captured.
// lastPTS is assumed to have been captured at wrap count 0 (or its wrap count was reset
// alongside BasePTS).
func (d *PTSWrapDetector) CalculatePTSDelta(currentPTS, lastPTS int64, wrapCount int) int64 {
	// Unwrap currentPTS based on total wraps since base was captured
	unwrappedCurrent := d.UnwrapPTS(currentPTS, wrapCount)
	// lastPTS (base) was captured at wrap count 0 — no adjustment needed
	return unwrappedCurrent - lastPTS
}
