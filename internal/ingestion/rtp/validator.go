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
	return &ValidatorConfig{
		AllowedPayloadTypes: []uint8{96, 97, 98, 99}, // Dynamic payload types
		MaxSequenceGap:      100,
		MaxTimestampJump:    90000 * 10, // 10 seconds at 90kHz
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

	// Validate padding if present
	if packet.Padding && len(packet.Raw) > 0 {
		paddingLen := packet.Raw[len(packet.Raw)-1]
		if int(paddingLen) > len(packet.Raw) {
			return ErrInvalidPadding
		}
	}

	// Track and validate sequence/timestamp
	v.mu.Lock()
	tracker, exists := v.ssrcState[packet.SSRC]
	if !exists {
		tracker = &ssrcTracker{
			lastSequence:  packet.SequenceNumber,
			lastTimestamp: packet.Timestamp,
			packetsCount:  1,
		}
		v.ssrcState[packet.SSRC] = tracker
		v.mu.Unlock()
		return nil
	}

	// Validate sequence number
	expectedSeq := (tracker.lastSequence + 1) & 0xFFFF
	if packet.SequenceNumber != expectedSeq {
		// Calculate the gap
		var gap int
		if packet.SequenceNumber > tracker.lastSequence {
			gap = int(packet.SequenceNumber - tracker.lastSequence)
		} else {
			// Handle wraparound
			gap = int(packet.SequenceNumber) + (0xFFFF - int(tracker.lastSequence)) + 1
		}

		if gap > v.config.MaxSequenceGap {
			v.mu.Unlock()
			return ErrSequenceGap
		}
	}

	// Validate timestamp progression
	if tracker.packetsCount > 0 {
		var timestampDiff uint32
		if packet.Timestamp >= tracker.lastTimestamp {
			timestampDiff = packet.Timestamp - tracker.lastTimestamp
		} else {
			// Handle wraparound
			timestampDiff = (0xFFFFFFFF - tracker.lastTimestamp) + packet.Timestamp + 1
		}

		if timestampDiff > v.config.MaxTimestampJump {
			v.mu.Unlock()
			return ErrTimestampJump
		}
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

// Reset clears all SSRC tracking state
func (v *Validator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.ssrcState = make(map[uint32]*ssrcTracker)
}
