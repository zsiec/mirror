package types

// Rational represents a rational number (numerator/denominator)
// Used for time bases in video/audio streams
type Rational struct {
	Num int // Numerator
	Den int // Denominator
}

// NewRational creates a new rational number
func NewRational(num, den int) Rational {
	if den == 0 {
		den = 1
	}
	return Rational{Num: num, Den: den}
}

// Float64 returns the floating point representation
func (r Rational) Float64() float64 {
	if r.Den == 0 {
		return 0
	}
	return float64(r.Num) / float64(r.Den)
}

// Invert returns the inverted rational (den/num)
func (r Rational) Invert() Rational {
	return Rational{Num: r.Den, Den: r.Num}
}

// Common time bases
var (
	// Video time bases
	TimeBase90kHz = Rational{Num: 1, Den: 90000}  // Standard video (RTP)
	TimeBase1kHz  = Rational{Num: 1, Den: 1000}   // Millisecond precision
	
	// Audio time bases
	TimeBase48kHz = Rational{Num: 1, Den: 48000}  // 48kHz audio
	TimeBase44kHz = Rational{Num: 1, Den: 44100}  // 44.1kHz audio
	
	// Frame rates (as rationals)
	FrameRate24   = Rational{Num: 24, Den: 1}      // 24 fps
	FrameRate25   = Rational{Num: 25, Den: 1}      // 25 fps (PAL)
	FrameRate30   = Rational{Num: 30, Den: 1}      // 30 fps
	FrameRate50   = Rational{Num: 50, Den: 1}      // 50 fps
	FrameRate60   = Rational{Num: 60, Den: 1}      // 60 fps
	
	// NTSC frame rates
	FrameRate23_976 = Rational{Num: 24000, Den: 1001}  // 23.976 fps
	FrameRate29_97  = Rational{Num: 30000, Den: 1001}  // 29.97 fps
	FrameRate59_94  = Rational{Num: 60000, Den: 1001}  // 59.94 fps
)
