package rtp

import (
	"errors"
	"sync"

	"github.com/pion/rtp"
)

var (
	// Validation errors
	ErrInvalidRTPVersion  = errors.New("invalid RTP version")
	ErrInvalidPayloadType = errors.New("invalid payload type")
	ErrPacketTooSmall     = errors.New("packet too small")
	ErrInvalidPadding     = errors.New("invalid padding")
	ErrSequenceGap        = errors.New("sequence number gap detected")
	ErrTimestampJump      = errors.New("timestamp jump detected")
)

// ValidatorConfig holds configuration for RTP packet validation
type ValidatorConfig struct {
	// AllowedPayloadTypes lists the valid payload types
	AllowedPayloadTypes []uint8
	// MaxSequenceGap is the maximum allowed gap in sequence numbers
	MaxSequenceGap int
	// MaxTimestampJump is the maximum allowed jump in timestamps (in RTP units)
	MaxTimestampJump uint32
}

// DefaultValidatorConfig returns a default validator configuration
func DefaultValidatorConfig() *ValidatorConfig {
	// Accept all dynamic payload types (96-127) per RFC 3551 Section 3
	dynamicPTs := make([]uint8, 32)
	for i := range dynamicPTs {
		dynamicPTs[i] = uint8(96 + i)
	}
	return &ValidatorConfig{
		AllowedPayloadTypes: dynamicPTs,
		MaxSequenceGap:      100,
		MaxTimestampJump:    90000 * 60, // 60 seconds at 90kHz - permissive for video streams
	}
}

// Validator validates RTP packets
type Validator struct {
	config              *ValidatorConfig
	allowedPayloadTypes map[uint8]bool

	// Track per-SSRC state
	mu        sync.RWMutex
	ssrcState map[uint32]*ssrcTracker
}

// ssrcTracker tracks state for a single SSRC
type ssrcTracker struct {
	lastSequence  uint16
	lastTimestamp uint32
	packetsCount  uint64

	// B-frame aware timestamp tracking
	timestampWindow []uint32 // Recent timestamps for trend analysis
	lastMonotonicTS uint32   // Last monotonically increasing timestamp
	dtsEstimate     uint32   // Estimated DTS for B-frame validation
	frameDuration   uint32   // Estimated frame duration
	reorderDepth    int      // Maximum reorder depth observed
}

// NewValidator creates a new RTP packet validator
func NewValidator(config *ValidatorConfig) *Validator {
	if config == nil {
		config = DefaultValidatorConfig()
	}

	v := &Validator{
		config:              config,
		allowedPayloadTypes: make(map[uint8]bool),
		ssrcState:           make(map[uint32]*ssrcTracker),
	}

	// Build payload type lookup map
	for _, pt := range config.AllowedPayloadTypes {
		v.allowedPayloadTypes[pt] = true
	}

	return v
}

// ValidatePacket validates an RTP packet
func (v *Validator) ValidatePacket(packet *rtp.Packet) error {
	// Basic validation
	if packet == nil {
		return ErrPacketTooSmall
	}

	// Check RTP version (must be 2)
	if packet.Version != 2 {
		return ErrInvalidRTPVersion
	}

	// Validate payload type
	if !v.allowedPayloadTypes[packet.PayloadType] {
		return ErrInvalidPayloadType
	}

	// Validate padding if present (RFC 3550 Section 5.1)
	if packet.Padding && len(packet.Raw) > 0 {
		paddingLen := packet.Raw[len(packet.Raw)-1]
		// Padding count includes itself, so minimum is 1. A value of 0 is invalid.
		if paddingLen == 0 || int(paddingLen) > len(packet.Raw) {
			return ErrInvalidPadding
		}
	}

	// Track and validate sequence/timestamp
	v.mu.Lock()
	tracker, exists := v.ssrcState[packet.SSRC]
	if !exists {
		tracker = &ssrcTracker{
			lastSequence:    packet.SequenceNumber,
			lastTimestamp:   packet.Timestamp,
			lastMonotonicTS: packet.Timestamp,
			dtsEstimate:     packet.Timestamp,
			packetsCount:    1,
			timestampWindow: []uint32{packet.Timestamp},
			frameDuration:   3000, // Default 30fps at 90kHz
			reorderDepth:    0,
		}
		v.ssrcState[packet.SSRC] = tracker
		v.mu.Unlock()
		return nil
	}

	// Validate sequence number using signed 16-bit arithmetic for proper wraparound handling
	seqDelta := int16(packet.SequenceNumber - tracker.lastSequence)
	absGap := int(seqDelta)
	if absGap < 0 {
		absGap = -absGap
	}
	if absGap > 1 && absGap > v.config.MaxSequenceGap {
		v.mu.Unlock()
		return ErrSequenceGap
	}

	// Validate timestamp with B-frame awareness
	if err := v.validateTimestamp(packet, tracker); err != nil {
		v.mu.Unlock()
		return err
	}

	// Update tracker
	tracker.lastSequence = packet.SequenceNumber
	tracker.lastTimestamp = packet.Timestamp
	tracker.packetsCount++
	v.mu.Unlock()

	return nil
}

// GetSSRCStats returns statistics for a specific SSRC
func (v *Validator) GetSSRCStats(ssrc uint32) (packetsCount uint64, exists bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if tracker, ok := v.ssrcState[ssrc]; ok {
		return tracker.packetsCount, true
	}
	return 0, false
}

// ResetSSRC resets tracking for a specific SSRC
func (v *Validator) ResetSSRC(ssrc uint32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.ssrcState, ssrc)
}

// validateTimestamp performs B-frame aware timestamp validation
func (v *Validator) validateTimestamp(packet *rtp.Packet, tracker *ssrcTracker) error {
	currentTS := packet.Timestamp

	// Handle 32-bit timestamp wraparound
	isWraparound := v.isTimestampWraparound(currentTS, tracker.lastTimestamp)
	if isWraparound {
		// On wraparound, reset the monotonic tracker so validation works correctly.
		// The new timestamp is a valid forward progression despite being numerically smaller.
		tracker.lastMonotonicTS = currentTS
	}

	// Update timestamp window (keep last 10 timestamps for analysis)
	tracker.timestampWindow = append(tracker.timestampWindow, currentTS)
	if len(tracker.timestampWindow) > 10 {
		tracker.timestampWindow = tracker.timestampWindow[1:]
	}

	// Update frame duration estimate
	if len(tracker.timestampWindow) >= 2 {
		tracker.frameDuration = v.estimateFrameDuration(tracker.timestampWindow)
	}

	// For the first few packets, be permissive, but still check for extreme jumps
	if tracker.packetsCount < 3 {
		// Still reject extreme jumps even early on
		if tracker.lastTimestamp > 0 && currentTS > tracker.lastTimestamp {
			jump := currentTS - tracker.lastTimestamp
			if jump > v.config.MaxTimestampJump {
				return ErrTimestampJump
			}
		}
		tracker.lastMonotonicTS = maxUint32(tracker.lastMonotonicTS, currentTS)
		return nil
	}

	// Calculate expected DTS progression
	// For minimum DTS, allow going back by frame duration for B-frames, but not too far
	expectedMinDTS := tracker.lastMonotonicTS - (tracker.frameDuration * 5) // Allow 5 frames back for B-frames
	if tracker.lastMonotonicTS < tracker.frameDuration*5 {
		expectedMinDTS = 0 // Prevent underflow for early packets
	}
	expectedMaxDTS := tracker.lastMonotonicTS + v.config.MaxTimestampJump

	// Check for reasonable bounds
	if currentTS > expectedMaxDTS {
		// Large forward jump - always reject
		return ErrTimestampJump
	}

	if currentTS < expectedMinDTS {
		// Check if this could be a B-frame (timestamp going backwards but within reasonable bounds)
		if v.isPossibleBFrame(currentTS, tracker) {
			// Allow B-frame but don't update monotonic timestamp
			tracker.reorderDepth = maxInt(tracker.reorderDepth, int(tracker.lastMonotonicTS-currentTS)/int(tracker.frameDuration))
			return nil
		}
		return ErrTimestampJump
	}

	// Update DTS estimate and monotonic timestamp
	if currentTS > tracker.lastMonotonicTS {
		tracker.lastMonotonicTS = currentTS
		tracker.dtsEstimate = currentTS + tracker.frameDuration
	}

	return nil
}

// handleTimestampWraparound detects 32-bit timestamp wraparound and returns
// true if wraparound was detected. When wraparound occurs, the caller should
// treat the timestamp jump as normal forward progression.
func (v *Validator) isTimestampWraparound(currentTS, lastTS uint32) bool {
	// If the last timestamp is in the high range and current is in the low range,
	// this is a 32-bit wraparound (~13.3 hours at 90kHz clock rate)
	return lastTS > 0xF0000000 && currentTS < 0x10000000
}

// isPossibleBFrame checks if a timestamp could be from a B-frame
func (v *Validator) isPossibleBFrame(timestamp uint32, tracker *ssrcTracker) bool {
	// B-frames typically have timestamps between the last I/P frame and the next I/P frame
	// Allow timestamps to go backwards by up to 5 frame durations (conservative B-frame depth)
	maxReorderWindow := tracker.frameDuration * 5

	// Check if timestamp is within reasonable B-frame reorder window
	if tracker.lastMonotonicTS > timestamp {
		reorderDistance := tracker.lastMonotonicTS - timestamp
		return reorderDistance <= maxReorderWindow
	}

	return false
}

// estimateFrameDuration estimates frame duration from timestamp window
func (v *Validator) estimateFrameDuration(timestamps []uint32) uint32 {
	if len(timestamps) < 2 {
		return 3000 // Default 30fps at 90kHz
	}

	// Calculate differences and find the most common one (mode)
	diffs := make(map[uint32]int)
	for i := 1; i < len(timestamps); i++ {
		if timestamps[i] > timestamps[i-1] {
			diff := timestamps[i] - timestamps[i-1]
			// Only consider reasonable frame durations (10fps to 120fps)
			if diff >= 750 && diff <= 9000 {
				diffs[diff]++
			}
		}
	}

	// Find the most common difference
	var bestDiff uint32 = 3000
	maxCount := 0
	for diff, count := range diffs {
		if count > maxCount {
			maxCount = count
			bestDiff = diff
		}
	}

	return bestDiff
}

// maxUint32 returns the maximum of two uint32 values
func maxUint32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

// maxInt returns the maximum of two int values
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Reset clears all SSRC tracking state
func (v *Validator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ssrcState = make(map[uint32]*ssrcTracker)
}
