package sync

import (
	"fmt"
	"math"

	"github.com/zsiec/mirror/internal/ingestion/types"
)

// TimeBaseConverter converts timestamps between different time bases
type TimeBaseConverter struct {
	from types.Rational
	to   types.Rational
	// Precomputed conversion factor to avoid repeated division
	conversionFactor float64
}

// NewTimeBaseConverter creates a new time base converter
func NewTimeBaseConverter(from, to types.Rational) (*TimeBaseConverter, error) {
	// Validate time bases
	if from.Num == 0 || from.Den == 0 {
		return nil, fmt.Errorf("invalid source time base: %v", from)
	}
	if to.Num == 0 || to.Den == 0 {
		return nil, fmt.Errorf("invalid target time base: %v", to)
	}

	// Calculate conversion factor: (to.Den * from.Num) / (to.Num * from.Den)
	factor := (float64(to.Den) * float64(from.Num)) / (float64(to.Num) * float64(from.Den))

	return &TimeBaseConverter{
		from:             from,
		to:               to,
		conversionFactor: factor,
	}, nil
}

// Convert converts a timestamp from source to target time base
func (c *TimeBaseConverter) Convert(pts int64) int64 {
	// Use precomputed factor for efficiency
	// Round to nearest integer
	return int64(math.Round(float64(pts) * c.conversionFactor))
}

// ConvertPrecise converts with higher precision for critical calculations
func (c *TimeBaseConverter) ConvertPrecise(pts int64) (int64, float64) {
	// Calculate exact value
	exact := float64(pts) * c.conversionFactor
	// Round to nearest
	rounded := int64(math.Round(exact))
	// Return both rounded and the fractional remainder
	return rounded, exact - float64(rounded)
}

// ConvertDuration converts a duration (difference between two timestamps)
func (c *TimeBaseConverter) ConvertDuration(duration int64) int64 {
	// Duration conversion is the same as timestamp conversion
	return c.Convert(duration)
}

// GetRatio returns the conversion ratio as a float64
func (c *TimeBaseConverter) GetRatio() float64 {
	return c.conversionFactor
}

// GetSourceTimeBase returns the source time base
func (c *TimeBaseConverter) GetSourceTimeBase() types.Rational {
	return c.from
}

// GetTargetTimeBase returns the target time base
func (c *TimeBaseConverter) GetTargetTimeBase() types.Rational {
	return c.to
}

// TimeBaseConverterChain allows chaining multiple converters
type TimeBaseConverterChain struct {
	converters []*TimeBaseConverter
}

// NewTimeBaseConverterChain creates a chain of converters
func NewTimeBaseConverterChain(timeBases ...types.Rational) (*TimeBaseConverterChain, error) {
	if len(timeBases) < 2 {
		return nil, fmt.Errorf("need at least 2 time bases for a chain")
	}

	chain := &TimeBaseConverterChain{
		converters: make([]*TimeBaseConverter, 0, len(timeBases)-1),
	}

	// Create converters for each adjacent pair
	for i := 0; i < len(timeBases)-1; i++ {
		converter, err := NewTimeBaseConverter(timeBases[i], timeBases[i+1])
		if err != nil {
			return nil, fmt.Errorf("failed to create converter %d: %w", i, err)
		}
		chain.converters = append(chain.converters, converter)
	}

	return chain, nil
}

// Convert applies all converters in the chain
func (c *TimeBaseConverterChain) Convert(pts int64) int64 {
	result := pts
	for _, converter := range c.converters {
		result = converter.Convert(result)
	}
	return result
}

// CommonTimeBases provides commonly used time bases
var CommonTimeBases = struct {
	Video90kHz     types.Rational
	Video48kHz     types.Rational
	Audio48kHz     types.Rational
	Audio44_1kHz   types.Rational
	NTSC29_97fps   types.Rational
	Film23_976fps  types.Rational
	Milliseconds   types.Rational
	Microseconds   types.Rational
}{
	Video90kHz:     types.Rational{Num: 1, Den: 90000},
	Video48kHz:     types.Rational{Num: 1, Den: 48000},
	Audio48kHz:     types.Rational{Num: 1, Den: 48000},
	Audio44_1kHz:   types.Rational{Num: 1, Den: 44100},
	NTSC29_97fps:   types.Rational{Num: 1001, Den: 30000},
	Film23_976fps:  types.Rational{Num: 1001, Den: 24000},
	Milliseconds:   types.Rational{Num: 1, Den: 1000},
	Microseconds:   types.Rational{Num: 1, Den: 1000000},
}

// ConvertToMilliseconds is a convenience function to convert any time base to milliseconds
func ConvertToMilliseconds(pts int64, timeBase types.Rational) int64 {
	converter, err := NewTimeBaseConverter(timeBase, CommonTimeBases.Milliseconds)
	if err != nil {
		// Fallback to direct calculation
		return (pts * 1000 * int64(timeBase.Num)) / int64(timeBase.Den)
	}
	return converter.Convert(pts)
}

// ConvertFromMilliseconds is a convenience function to convert milliseconds to any time base
func ConvertFromMilliseconds(ms int64, timeBase types.Rational) int64 {
	converter, err := NewTimeBaseConverter(CommonTimeBases.Milliseconds, timeBase)
	if err != nil {
		// Fallback to direct calculation
		return (ms * int64(timeBase.Den)) / (1000 * int64(timeBase.Num))
	}
	return converter.Convert(ms)
}
