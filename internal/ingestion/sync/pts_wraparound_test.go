package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestCalculateWrapThreshold(t *testing.T) {
	tests := []struct {
		name      string
		timeBase  types.Rational
		expected  int64
	}{
		{
			name:     "Standard video 90kHz",
			timeBase: types.Rational{Num: 1, Den: 90000},
			expected: 1 << 32, // 2^32
		},
		{
			name:     "Audio 48kHz",
			timeBase: types.Rational{Num: 1, Den: 48000},
			expected: 1 << 33, // 2^33
		},
		{
			name:     "Audio 44.1kHz",
			timeBase: types.Rational{Num: 1, Den: 44100},
			expected: 1 << 33, // 2^33
		},
		{
			name:     "Custom time base 30fps",
			timeBase: types.Rational{Num: 1001, Den: 30000},
			// Should calculate based on 26 hours
			// 26 * 3600 * 30000 / 1001 ≈ 2,805,194
			expected: 2805194,
		},
		{
			name:     "High frequency time base",
			timeBase: types.Rational{Num: 1, Den: 1000000},
			// 26 * 3600 * 1000000 / 1 = 93,600,000,000
			expected: 93600000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateWrapThreshold(tt.timeBase)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPTSWrapDetector_DetectWrap(t *testing.T) {
	// Create detector for standard 90kHz video
	detector := NewPTSWrapDetector(types.Rational{Num: 1, Den: 90000})
	
	tests := []struct {
		name        string
		currentPTS  int64
		lastPTS     int64
		expectWrap  bool
	}{
		{
			name:       "Normal increment",
			currentPTS: 1000,
			lastPTS:    900,
			expectWrap: false,
		},
		{
			name:       "Normal decrement (reordering)",
			currentPTS: 900,
			lastPTS:    1000,
			expectWrap: false,
		},
		{
			name:       "Wrap from near max to near zero",
			currentPTS: 100,
			lastPTS:    (1 << 32) - 100, // Near max 32-bit
			expectWrap: true,
		},
		{
			name:       "Large backward jump (not wrap)",
			currentPTS: 1000000,
			lastPTS:    2000000,
			expectWrap: false,
		},
		{
			name:       "Edge case at half threshold",
			currentPTS: 0,
			lastPTS:    (1 << 31), // Exactly half
			expectWrap: false,
		},
		{
			name:       "Just over half threshold",
			currentPTS: 0,
			lastPTS:    (1 << 31) + 1,
			expectWrap: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectWrap(tt.currentPTS, tt.lastPTS)
			assert.Equal(t, tt.expectWrap, result)
		})
	}
}

func TestPTSWrapDetector_UnwrapPTS(t *testing.T) {
	detector := NewPTSWrapDetector(types.Rational{Num: 1, Den: 90000})
	wrapThreshold := detector.GetWrapThreshold()
	
	tests := []struct {
		name      string
		pts       int64
		wrapCount int
		expected  int64
	}{
		{
			name:      "No wraps",
			pts:       12345,
			wrapCount: 0,
			expected:  12345,
		},
		{
			name:      "One wrap",
			pts:       12345,
			wrapCount: 1,
			expected:  12345 + wrapThreshold,
		},
		{
			name:      "Multiple wraps",
			pts:       12345,
			wrapCount: 3,
			expected:  12345 + 3*wrapThreshold,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.UnwrapPTS(tt.pts, tt.wrapCount)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPTSWrapDetector_IsLikelyDiscontinuity(t *testing.T) {
	detector := NewPTSWrapDetector(types.Rational{Num: 1, Den: 90000})
	
	tests := []struct {
		name                 string
		currentPTS          int64
		lastPTS             int64
		expectDiscontinuity bool
	}{
		{
			name:                "Small increment (normal)",
			currentPTS:          90100,
			lastPTS:             90000,
			expectDiscontinuity: false,
		},
		{
			name:                "Exactly 1 second jump",
			currentPTS:          180000, // 2 seconds
			lastPTS:             90000,  // 1 second
			expectDiscontinuity: false, // Exactly 1 second, not > 1 second
		},
		{
			name:                "1.5 second jump",
			currentPTS:          225000, // 2.5 seconds
			lastPTS:             90000,  // 1 second
			expectDiscontinuity: true,
		},
		{
			name:                "10 second jump",
			currentPTS:          990000, // 11 seconds
			lastPTS:             90000,  // 1 second
			expectDiscontinuity: true,
		},
		{
			name:                "Very large jump (likely wrap)",
			currentPTS:          100,
			lastPTS:             (1 << 32) - 100,
			expectDiscontinuity: false, // This is a wrap, not discontinuity
		},
		{
			name:                "Quarter threshold jump",
			currentPTS:          (1 << 30), // Quarter of 32-bit
			lastPTS:             0,
			expectDiscontinuity: false, // Too large for discontinuity
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.IsLikelyDiscontinuity(tt.currentPTS, tt.lastPTS)
			assert.Equal(t, tt.expectDiscontinuity, result)
		})
	}
}

func TestPTSWrapDetector_CalculatePTSDelta(t *testing.T) {
	detector := NewPTSWrapDetector(types.Rational{Num: 1, Den: 90000})
	
	tests := []struct {
		name       string
		currentPTS int64
		lastPTS    int64
		wrapCount  int
		expected   int64
	}{
		{
			name:       "Simple forward delta",
			currentPTS: 1000,
			lastPTS:    900,
			wrapCount:  0,
			expected:   100,
		},
		{
			name:       "Backward delta (reordering)",
			currentPTS: 900,
			lastPTS:    1000,
			wrapCount:  0,
			expected:   -100,
		},
		{
			name:       "Delta across wrap boundary",
			currentPTS: 100,
			lastPTS:    (1 << 32) - 100,
			wrapCount:  0,
			expected:   200, // Should detect wrap and adjust
		},
		{
			name:       "Delta with existing wrap count",
			currentPTS: 1000,
			lastPTS:    900,
			wrapCount:  1,
			expected:   100, // Normal delta, both already wrapped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.CalculatePTSDelta(tt.currentPTS, tt.lastPTS, tt.wrapCount)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPTSWrapDetector_AudioTimeBase(t *testing.T) {
	// Test with 48kHz audio time base
	detector := NewPTSWrapDetector(types.Rational{Num: 1, Den: 48000})
	
	// Verify it uses 33-bit threshold
	assert.Equal(t, int64(1<<33), detector.GetWrapThreshold())
	
	// Test wrap detection with 33-bit values
	tests := []struct {
		name       string
		currentPTS int64
		lastPTS    int64
		expectWrap bool
	}{
		{
			name:       "Normal audio timestamp increment",
			currentPTS: 48000, // 1 second
			lastPTS:    0,
			expectWrap: false,
		},
		{
			name:       "Wrap at 33-bit boundary",
			currentPTS: 100,
			lastPTS:    (1 << 33) - 100,
			expectWrap: true,
		},
		{
			name:       "No wrap at 32-bit boundary (audio uses 33-bit)",
			currentPTS: 100,
			lastPTS:    (1 << 32) - 100,
			expectWrap: false, // Should not wrap at 32-bit for audio
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.DetectWrap(tt.currentPTS, tt.lastPTS)
			assert.Equal(t, tt.expectWrap, result)
		})
	}
}

// BenchmarkPTSWrapDetection benchmarks wrap detection performance
func BenchmarkPTSWrapDetection(b *testing.B) {
	detector := NewPTSWrapDetector(types.Rational{Num: 1, Den: 90000})
	
	// Simulate timestamps that occasionally wrap
	timestamps := make([]int64, 1000)
	for i := range timestamps {
		if i%100 == 99 {
			// Simulate wrap
			timestamps[i] = int64(i * 3000) % (1 << 32)
		} else {
			timestamps[i] = int64(i * 3000)
		}
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		for j := 1; j < len(timestamps); j++ {
			detector.DetectWrap(timestamps[j], timestamps[j-1])
		}
	}
}
