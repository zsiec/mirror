package types

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRational(t *testing.T) {
	tests := []struct {
		name     string
		num      int
		den      int
		expected Rational
	}{
		{
			name:     "Normal rational",
			num:      1,
			den:      2,
			expected: Rational{Num: 1, Den: 2},
		},
		{
			name:     "Zero numerator",
			num:      0,
			den:      5,
			expected: Rational{Num: 0, Den: 5},
		},
		{
			name:     "Zero denominator gets corrected to 1",
			num:      5,
			den:      0,
			expected: Rational{Num: 5, Den: 1},
		},
		{
			name:     "Negative numerator",
			num:      -1,
			den:      2,
			expected: Rational{Num: -1, Den: 2},
		},
		{
			name:     "Negative denominator",
			num:      1,
			den:      -2,
			expected: Rational{Num: 1, Den: -2},
		},
		{
			name:     "Both negative",
			num:      -3,
			den:      -4,
			expected: Rational{Num: -3, Den: -4},
		},
		{
			name:     "Large numbers",
			num:      1000000,
			den:      999999,
			expected: Rational{Num: 1000000, Den: 999999},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewRational(tt.num, tt.den)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRational_Float64(t *testing.T) {
	tests := []struct {
		name     string
		rational Rational
		expected float64
	}{
		{
			name:     "Simple fraction",
			rational: Rational{Num: 1, Den: 2},
			expected: 0.5,
		},
		{
			name:     "Zero numerator",
			rational: Rational{Num: 0, Den: 5},
			expected: 0.0,
		},
		{
			name:     "Zero denominator",
			rational: Rational{Num: 5, Den: 0},
			expected: 0.0,
		},
		{
			name:     "Whole number",
			rational: Rational{Num: 10, Den: 1},
			expected: 10.0,
		},
		{
			name:     "Negative fraction",
			rational: Rational{Num: -1, Den: 2},
			expected: -0.5,
		},
		{
			name:     "Negative denominator",
			rational: Rational{Num: 1, Den: -2},
			expected: -0.5,
		},
		{
			name:     "Both negative",
			rational: Rational{Num: -3, Den: -4},
			expected: 0.75,
		},
		{
			name:     "Repeating decimal",
			rational: Rational{Num: 1, Den: 3},
			expected: 1.0 / 3.0,
		},
		{
			name:     "Large values",
			rational: Rational{Num: 1000000, Den: 999999},
			expected: 1000000.0 / 999999.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rational.Float64()
			if math.IsNaN(tt.expected) {
				assert.True(t, math.IsNaN(result))
			} else {
				assert.InDelta(t, tt.expected, result, 1e-10)
			}
		})
	}
}

func TestRational_Invert(t *testing.T) {
	tests := []struct {
		name     string
		rational Rational
		expected Rational
	}{
		{
			name:     "Simple fraction",
			rational: Rational{Num: 1, Den: 2},
			expected: Rational{Num: 2, Den: 1},
		},
		{
			name:     "Zero numerator",
			rational: Rational{Num: 0, Den: 5},
			expected: Rational{Num: 5, Den: 0},
		},
		{
			name:     "Zero denominator",
			rational: Rational{Num: 5, Den: 0},
			expected: Rational{Num: 0, Den: 5},
		},
		{
			name:     "Whole number",
			rational: Rational{Num: 10, Den: 1},
			expected: Rational{Num: 1, Den: 10},
		},
		{
			name:     "Negative numerator",
			rational: Rational{Num: -3, Den: 4},
			expected: Rational{Num: 4, Den: -3},
		},
		{
			name:     "Negative denominator",
			rational: Rational{Num: 3, Den: -4},
			expected: Rational{Num: -4, Den: 3},
		},
		{
			name:     "Both negative",
			rational: Rational{Num: -3, Den: -4},
			expected: Rational{Num: -4, Den: -3},
		},
		{
			name:     "Identity (1/1)",
			rational: Rational{Num: 1, Den: 1},
			expected: Rational{Num: 1, Den: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rational.Invert()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRational_InvertTwiceIsOriginal(t *testing.T) {
	// Test property: inverting twice should return original
	testCases := []Rational{
		{Num: 1, Den: 2},
		{Num: 3, Den: 7},
		{Num: -5, Den: 8},
		{Num: 10, Den: 1},
		{Num: 0, Den: 1}, // Special case
	}

	for _, rational := range testCases {
		t.Run("", func(t *testing.T) {
			result := rational.Invert().Invert()
			assert.Equal(t, rational, result)
		})
	}
}

func TestPredefinedTimeBasesAndFrameRates(t *testing.T) {
	// Test that predefined constants have expected values
	tests := []struct {
		name     string
		rational Rational
		expected float64
	}{
		{
			name:     "TimeBase90kHz",
			rational: TimeBase90kHz,
			expected: 1.0 / 90000.0,
		},
		{
			name:     "TimeBase1kHz",
			rational: TimeBase1kHz,
			expected: 1.0 / 1000.0,
		},
		{
			name:     "TimeBase48kHz",
			rational: TimeBase48kHz,
			expected: 1.0 / 48000.0,
		},
		{
			name:     "TimeBase44kHz",
			rational: TimeBase44kHz,
			expected: 1.0 / 44100.0,
		},
		{
			name:     "FrameRate24",
			rational: FrameRate24,
			expected: 24.0,
		},
		{
			name:     "FrameRate25",
			rational: FrameRate25,
			expected: 25.0,
		},
		{
			name:     "FrameRate30",
			rational: FrameRate30,
			expected: 30.0,
		},
		{
			name:     "FrameRate50",
			rational: FrameRate50,
			expected: 50.0,
		},
		{
			name:     "FrameRate60",
			rational: FrameRate60,
			expected: 60.0,
		},
		{
			name:     "FrameRate23_976",
			rational: FrameRate23_976,
			expected: 24000.0 / 1001.0, // ~23.976
		},
		{
			name:     "FrameRate29_97",
			rational: FrameRate29_97,
			expected: 30000.0 / 1001.0, // ~29.97
		},
		{
			name:     "FrameRate59_94",
			rational: FrameRate59_94,
			expected: 60000.0 / 1001.0, // ~59.94
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.rational.Float64()
			assert.InDelta(t, tt.expected, result, 1e-10)
		})
	}
}

func TestPredefinedConstantsStructure(t *testing.T) {
	// Test that predefined constants have the expected structure
	assert.Equal(t, Rational{Num: 1, Den: 90000}, TimeBase90kHz)
	assert.Equal(t, Rational{Num: 1, Den: 1000}, TimeBase1kHz)
	assert.Equal(t, Rational{Num: 1, Den: 48000}, TimeBase48kHz)
	assert.Equal(t, Rational{Num: 1, Den: 44100}, TimeBase44kHz)

	assert.Equal(t, Rational{Num: 24, Den: 1}, FrameRate24)
	assert.Equal(t, Rational{Num: 25, Den: 1}, FrameRate25)
	assert.Equal(t, Rational{Num: 30, Den: 1}, FrameRate30)
	assert.Equal(t, Rational{Num: 50, Den: 1}, FrameRate50)
	assert.Equal(t, Rational{Num: 60, Den: 1}, FrameRate60)

	assert.Equal(t, Rational{Num: 24000, Den: 1001}, FrameRate23_976)
	assert.Equal(t, Rational{Num: 30000, Den: 1001}, FrameRate29_97)
	assert.Equal(t, Rational{Num: 60000, Den: 1001}, FrameRate59_94)
}

func TestRational_RealWorldVideoScenarios(t *testing.T) {
	// Test rational operations with real-world video scenarios
	t.Run("Convert 90kHz timestamp to seconds", func(t *testing.T) {
		timestamp := 270000 // 3 seconds at 90kHz
		timeInSeconds := float64(timestamp) * TimeBase90kHz.Float64()
		assert.InDelta(t, 3.0, timeInSeconds, 1e-10)
	})

	t.Run("Calculate frame duration for 30fps", func(t *testing.T) {
		frameDuration := FrameRate30.Invert().Float64()
		assert.InDelta(t, 1.0/30.0, frameDuration, 1e-10)
	})

	t.Run("Calculate frame duration for 23.976fps", func(t *testing.T) {
		frameDuration := FrameRate23_976.Invert().Float64()
		expectedDuration := 1001.0 / 24000.0
		assert.InDelta(t, expectedDuration, frameDuration, 1e-10)
	})

	t.Run("Audio sample period for 48kHz", func(t *testing.T) {
		samplePeriod := TimeBase48kHz.Float64()
		assert.InDelta(t, 1.0/48000.0, samplePeriod, 1e-10)
	})
}

func TestRational_EdgeCases(t *testing.T) {
	t.Run("Zero divided by zero", func(t *testing.T) {
		r := NewRational(0, 0)
		assert.Equal(t, Rational{Num: 0, Den: 1}, r)
		assert.Equal(t, 0.0, r.Float64())
	})

	t.Run("Large numbers don't overflow", func(t *testing.T) {
		r := NewRational(int(1e9), int(1e6))
		assert.Equal(t, 1000.0, r.Float64())
	})

	t.Run("Invert zero numerator", func(t *testing.T) {
		r := Rational{Num: 0, Den: 5}
		inverted := r.Invert()
		assert.Equal(t, Rational{Num: 5, Den: 0}, inverted)
		assert.Equal(t, 0.0, inverted.Float64()) // Should handle division by zero
	})
}
