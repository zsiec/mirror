package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
)

func TestNewTimeBaseConverter(t *testing.T) {
	tests := []struct {
		name    string
		from    types.Rational
		to      types.Rational
		wantErr bool
	}{
		{
			name:    "Valid conversion",
			from:    types.Rational{Num: 1, Den: 90000},
			to:      types.Rational{Num: 1, Den: 48000},
			wantErr: false,
		},
		{
			name:    "Invalid source numerator",
			from:    types.Rational{Num: 0, Den: 90000},
			to:      types.Rational{Num: 1, Den: 48000},
			wantErr: true,
		},
		{
			name:    "Invalid source denominator",
			from:    types.Rational{Num: 1, Den: 0},
			to:      types.Rational{Num: 1, Den: 48000},
			wantErr: true,
		},
		{
			name:    "Invalid target numerator",
			from:    types.Rational{Num: 1, Den: 90000},
			to:      types.Rational{Num: 0, Den: 48000},
			wantErr: true,
		},
		{
			name:    "Invalid target denominator",
			from:    types.Rational{Num: 1, Den: 90000},
			to:      types.Rational{Num: 1, Den: 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter, err := NewTimeBaseConverter(tt.from, tt.to)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, converter)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, converter)
			}
		})
	}
}

func TestTimeBaseConverter_Convert(t *testing.T) {
	tests := []struct {
		name     string
		from     types.Rational
		to       types.Rational
		input    int64
		expected int64
	}{
		{
			name:     "90kHz to 48kHz",
			from:     types.Rational{Num: 1, Den: 90000},
			to:       types.Rational{Num: 1, Den: 48000},
			input:    90000, // 1 second at 90kHz
			expected: 48000, // 1 second at 48kHz
		},
		{
			name:     "48kHz to 90kHz",
			from:     types.Rational{Num: 1, Den: 48000},
			to:       types.Rational{Num: 1, Den: 90000},
			input:    48000, // 1 second at 48kHz
			expected: 90000, // 1 second at 90kHz
		},
		{
			name:     "90kHz to milliseconds",
			from:     types.Rational{Num: 1, Den: 90000},
			to:       types.Rational{Num: 1, Den: 1000},
			input:    90000, // 1 second
			expected: 1000,  // 1000 milliseconds
		},
		{
			name:     "NTSC frame to 90kHz",
			from:     types.Rational{Num: 1001, Den: 30000}, // NTSC timebase
			to:       types.Rational{Num: 1, Den: 90000},
			input:    1,    // 1 unit in NTSC timebase
			expected: 3003, // 1 * 90000 * 1001 / (1 * 30000) = 3003
		},
		{
			name:     "Film frame to milliseconds",
			from:     types.Rational{Num: 1001, Den: 24000}, // Film timebase
			to:       types.Rational{Num: 1, Den: 1000},
			input:    24,   // 24 units
			expected: 1001, // 24 * 1000 * 1001 / (1 * 24000) = 1001
		},
		{
			name:     "Identity conversion",
			from:     types.Rational{Num: 1, Den: 90000},
			to:       types.Rational{Num: 1, Den: 90000},
			input:    12345,
			expected: 12345,
		},
		{
			name:     "Fractional time base",
			from:     types.Rational{Num: 2, Den: 90000},
			to:       types.Rational{Num: 1, Den: 45000},
			input:    90000,
			expected: 90000, // Should maintain the same value
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter, err := NewTimeBaseConverter(tt.from, tt.to)
			require.NoError(t, err)

			result := converter.Convert(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTimeBaseConverter_ConvertPrecise(t *testing.T) {
	// Test case where conversion has fractional remainder
	converter, err := NewTimeBaseConverter(
		types.Rational{Num: 1, Den: 90000},
		types.Rational{Num: 1, Den: 48000},
	)
	require.NoError(t, err)

	// 90001 at 90kHz doesn't convert evenly to 48kHz
	rounded, remainder := converter.ConvertPrecise(90001)

	// Expected: 90001 * 48000 / 90000 = 48000.533...
	assert.Equal(t, int64(48001), rounded)
	assert.InDelta(t, -0.466667, remainder, 0.0001)

	// Test exact conversion
	rounded2, remainder2 := converter.ConvertPrecise(90000)
	assert.Equal(t, int64(48000), rounded2)
	assert.InDelta(t, 0.0, remainder2, 0.0001)
}

func TestTimeBaseConverter_ConvertDuration(t *testing.T) {
	converter, err := NewTimeBaseConverter(
		types.Rational{Num: 1, Den: 90000},
		types.Rational{Num: 1, Den: 1000}, // milliseconds
	)
	require.NoError(t, err)

	// 3000 units at 90kHz = 33.33ms
	duration := converter.ConvertDuration(3000)
	assert.Equal(t, int64(33), duration) // Rounded to 33ms
}

func TestTimeBaseConverter_GetRatio(t *testing.T) {
	converter, err := NewTimeBaseConverter(
		types.Rational{Num: 1, Den: 90000},
		types.Rational{Num: 1, Den: 48000},
	)
	require.NoError(t, err)

	ratio := converter.GetRatio()
	// 48000/90000 = 0.5333...
	assert.InDelta(t, 0.5333333, ratio, 0.0001)
}

func TestTimeBaseConverterChain(t *testing.T) {
	tests := []struct {
		name      string
		timeBases []types.Rational
		input     int64
		expected  int64
		wantErr   bool
	}{
		{
			name: "Three stage conversion",
			timeBases: []types.Rational{
				{Num: 1, Den: 90000}, // 90kHz
				{Num: 1, Den: 48000}, // 48kHz
				{Num: 1, Den: 1000},  // milliseconds
			},
			input:    90000, // 1 second at 90kHz
			expected: 1000,  // 1000 milliseconds
			wantErr:  false,
		},
		{
			name: "Not enough time bases",
			timeBases: []types.Rational{
				{Num: 1, Den: 90000},
			},
			wantErr: true,
		},
		{
			name: "Invalid time base in chain",
			timeBases: []types.Rational{
				{Num: 1, Den: 90000},
				{Num: 0, Den: 48000}, // Invalid
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain, err := NewTimeBaseConverterChain(tt.timeBases...)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			result := chain.Convert(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCommonTimeBases(t *testing.T) {
	// Verify common time bases are correctly defined
	assert.Equal(t, types.Rational{Num: 1, Den: 90000}, CommonTimeBases.Video90kHz)
	assert.Equal(t, types.Rational{Num: 1, Den: 48000}, CommonTimeBases.Audio48kHz)
	assert.Equal(t, types.Rational{Num: 1, Den: 44100}, CommonTimeBases.Audio44_1kHz)
	assert.Equal(t, types.Rational{Num: 1001, Den: 30000}, CommonTimeBases.NTSC29_97fps)
	assert.Equal(t, types.Rational{Num: 1001, Den: 24000}, CommonTimeBases.Film23_976fps)
	assert.Equal(t, types.Rational{Num: 1, Den: 1000}, CommonTimeBases.Milliseconds)
	assert.Equal(t, types.Rational{Num: 1, Den: 1000000}, CommonTimeBases.Microseconds)
}

func TestConvertToMilliseconds(t *testing.T) {
	tests := []struct {
		name     string
		pts      int64
		timeBase types.Rational
		expected int64
	}{
		{
			name:     "90kHz to ms",
			pts:      90000,
			timeBase: types.Rational{Num: 1, Den: 90000},
			expected: 1000,
		},
		{
			name:     "48kHz to ms",
			pts:      48000,
			timeBase: types.Rational{Num: 1, Den: 48000},
			expected: 1000,
		},
		{
			name:     "NTSC frame to ms",
			pts:      1,
			timeBase: types.Rational{Num: 1001, Den: 30000},
			expected: 33, // 1 frame at 29.97fps ≈ 33.37ms
		},
		{
			name:     "Invalid time base fallback",
			pts:      1000,
			timeBase: types.Rational{Num: 0, Den: 1000}, // Invalid, but fallback should work
			expected: 0,                                 // Division by zero in fallback
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertToMilliseconds(tt.pts, tt.timeBase)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertFromMilliseconds(t *testing.T) {
	tests := []struct {
		name     string
		ms       int64
		timeBase types.Rational
		expected int64
	}{
		{
			name:     "ms to 90kHz",
			ms:       1000,
			timeBase: types.Rational{Num: 1, Den: 90000},
			expected: 90000,
		},
		{
			name:     "ms to 48kHz",
			ms:       1000,
			timeBase: types.Rational{Num: 1, Den: 48000},
			expected: 48000,
		},
		{
			name:     "ms to NTSC",
			ms:       33,
			timeBase: types.Rational{Num: 1001, Den: 30000},
			expected: 1, // 33ms ≈ 1 frame at 29.97fps
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertFromMilliseconds(tt.ms, tt.timeBase)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTimeBaseConverterAccuracy(t *testing.T) {
	// Test accuracy over long durations
	converter, err := NewTimeBaseConverter(
		types.Rational{Num: 1001, Den: 30000}, // NTSC 29.97fps
		types.Rational{Num: 1, Den: 90000},    // 90kHz
	)
	require.NoError(t, err)

	// 1 hour in NTSC time base units
	// With NTSC timebase Num=1001, Den=30000, 1 second = 30000/1001 ≈ 29.97 units
	oneHourPTS := int64(3600 * 30000 / 1001) // ~107892 units

	converted := converter.Convert(oneHourPTS)

	// Should be exactly 1 hour in 90kHz units
	expectedPTS := int64(3600 * 90000) // 324,000,000

	// Allow small rounding error (less than 1 frame worth at 90kHz)
	diff := abs(converted - expectedPTS)
	assert.Less(t, diff, int64(3003), "Conversion error over 1 hour should be less than 1 frame")
}

// BenchmarkTimeBaseConverter benchmarks conversion performance
func BenchmarkTimeBaseConverter(b *testing.B) {
	converter, _ := NewTimeBaseConverter(
		types.Rational{Num: 1, Den: 90000},
		types.Rational{Num: 1, Den: 48000},
	)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		converter.Convert(int64(i))
	}
}

// BenchmarkTimeBaseConverterChain benchmarks chain conversion
func BenchmarkTimeBaseConverterChain(b *testing.B) {
	chain, _ := NewTimeBaseConverterChain(
		types.Rational{Num: 1, Den: 90000},
		types.Rational{Num: 1, Den: 48000},
		types.Rational{Num: 1, Den: 1000},
	)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		chain.Convert(int64(i))
	}
}
