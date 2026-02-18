package validation

import (
	"fmt"
	"sync"
	"time"
)

// PESValidator provides validation for PES packet assembly
type PESValidator struct {
	mu         sync.RWMutex
	streams    map[uint16]*pesStreamState
	timeout    time.Duration
	maxPESSize int
}

// pesStreamState tracks PES assembly state for a single stream
type pesStreamState struct {
	inProgress       bool
	startTime        time.Time
	bytesAccumulated int
	expectedLength   int
	hasLength        bool
	packetsReceived  int
	lastActivity     time.Time
}

// PESStats contains PES assembly statistics
type PESStats struct {
	ActiveStreams    int
	CompletedPackets int64
	TimeoutPackets   int64
	OversizePackets  int64
	MalformedPackets int64
}

// NewPESValidator creates a new PES validator
func NewPESValidator(timeout time.Duration) *PESValidator {
	return &PESValidator{
		streams:    make(map[uint16]*pesStreamState),
		timeout:    timeout,
		maxPESSize: 65536 * 3, // 192KB default max PES size
	}
}

// ValidatePESStart validates the start of a new PES packet
func (v *PESValidator) ValidatePESStart(pid uint16, data []byte) error {
	if len(data) < 6 {
		return fmt.Errorf("PES header too short: %d bytes", len(data))
	}

	// Check PES start code prefix
	if data[0] != 0x00 || data[1] != 0x00 || data[2] != 0x01 {
		return fmt.Errorf("invalid PES start code: %02X %02X %02X", data[0], data[1], data[2])
	}

	// Extract stream ID
	streamID := data[3]

	// PES packet length (can be 0 for video)
	pesLength := int(uint16(data[4])<<8 | uint16(data[5]))

	v.mu.Lock()
	defer v.mu.Unlock()

	// Check if we already have a PES in progress for this PID
	state, exists := v.streams[pid]
	if exists && state.inProgress {
		// Previous PES was not completed - this is a discontinuity
		return fmt.Errorf("incomplete PES packet on PID %d (accumulated %d bytes)", pid, state.bytesAccumulated)
	}

	// Create new state
	state = &pesStreamState{
		inProgress:       true,
		startTime:        time.Now(),
		lastActivity:     time.Now(),
		bytesAccumulated: len(data),
		packetsReceived:  1,
		hasLength:        pesLength > 0,
		expectedLength:   pesLength + 6, // Include header
	}

	// Validate length if specified
	if state.hasLength && state.expectedLength > v.maxPESSize {
		return fmt.Errorf("PES packet too large: %d bytes (max %d)", state.expectedLength, v.maxPESSize)
	}

	v.streams[pid] = state

	// Log stream ID for debugging
	_ = streamID // Prevent unused variable warning

	return nil
}

// ValidatePESContinuation validates continuation of a PES packet
func (v *PESValidator) ValidatePESContinuation(pid uint16, dataLen int) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	state, exists := v.streams[pid]
	if !exists || !state.inProgress {
		return fmt.Errorf("PES continuation without start on PID %d", pid)
	}

	// Update state
	state.bytesAccumulated += dataLen
	state.packetsReceived++
	state.lastActivity = time.Now()

	// Check size limits
	if state.bytesAccumulated > v.maxPESSize {
		state.inProgress = false
		return fmt.Errorf("PES packet exceeds maximum size on PID %d: %d bytes", pid, state.bytesAccumulated)
	}

	// Check if complete (only if length was specified)
	if state.hasLength && state.bytesAccumulated >= state.expectedLength {
		state.inProgress = false
		// Validate exact match
		if state.bytesAccumulated > state.expectedLength {
			return fmt.Errorf("PES packet overrun on PID %d: expected %d, got %d bytes",
				pid, state.expectedLength, state.bytesAccumulated)
		}
	}

	return nil
}

// CompletePES marks a PES packet as complete
func (v *PESValidator) CompletePES(pid uint16) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if state, exists := v.streams[pid]; exists {
		state.inProgress = false
	}
}

// CheckTimeouts checks for timed-out PES packets
func (v *PESValidator) CheckTimeouts() []uint16 {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := time.Now()
	var timedOutPIDs []uint16

	for pid, state := range v.streams {
		if state.inProgress && now.Sub(state.lastActivity) > v.timeout {
			state.inProgress = false
			timedOutPIDs = append(timedOutPIDs, pid)
		}
	}

	return timedOutPIDs
}

// GetStreamState returns the current state of a PES stream
func (v *PESValidator) GetStreamState(pid uint16) (inProgress bool, bytes int, packets int) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if state, exists := v.streams[pid]; exists {
		return state.inProgress, state.bytesAccumulated, state.packetsReceived
	}

	return false, 0, 0
}

// Reset clears all PES assembly state
func (v *PESValidator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.streams = make(map[uint16]*pesStreamState)
}

// ResetPID resets the PES state for a specific PID
func (v *PESValidator) ResetPID(pid uint16) {
	v.mu.Lock()
	defer v.mu.Unlock()

	delete(v.streams, pid)
}

// GetStats returns PES assembly statistics
func (v *PESValidator) GetStats() PESStats {
	v.mu.RLock()
	defer v.mu.RUnlock()

	activeCount := 0
	for _, state := range v.streams {
		if state.inProgress {
			activeCount++
		}
	}

	return PESStats{
		ActiveStreams: activeCount,
		// Other stats would be tracked during validation
	}
}
