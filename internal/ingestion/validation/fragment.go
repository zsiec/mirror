package validation

import (
	"errors"
	"time"

	"github.com/pion/rtp"
	"github.com/zsiec/mirror/internal/ingestion/security"
)

// Fragment validation errors
var (
	ErrFragmentTooLarge     = errors.New("fragment exceeds maximum size")
	ErrFragmentTimeout      = errors.New("fragment assembly timed out")
	ErrTooManyFragments     = errors.New("too many fragments in assembly")
	ErrInvalidFragmentOrder = errors.New("invalid fragment order")
	ErrDuplicateFragment    = errors.New("duplicate fragment detected")
	ErrFragmentGapTooLarge  = errors.New("fragment sequence gap too large")
)

// FragmentValidator provides validation for RTP fragment assembly
type FragmentValidator struct {
	maxFragmentSize    int
	maxFragments       int
	maxSequenceGap     int
	fragmentTimeout    time.Duration
	maxAssemblySize    int
	allowDuplicates    bool
	enforceStrictOrder bool
}

// NewFragmentValidator creates a new fragment validator with default settings
func NewFragmentValidator() *FragmentValidator {
	return &FragmentValidator{
		maxFragmentSize:    security.MaxFragmentSize,
		maxFragments:       1000,
		maxSequenceGap:     5,
		fragmentTimeout:    5 * time.Second,
		maxAssemblySize:    security.MaxNALUnitSize,
		allowDuplicates:    false,
		enforceStrictOrder: true,
	}
}

// FragmentInfo holds information about a fragment for validation
type FragmentInfo struct {
	SequenceNumber uint16
	Timestamp      uint32
	Size           int
	ReceivedAt     time.Time
	IsStart        bool
	IsEnd          bool
}

// FragmentState tracks the state of fragment assembly for validation
type FragmentState struct {
	fragments    []FragmentInfo
	totalSize    int
	startTime    time.Time
	lastSequence uint16
	expectedSeq  uint16
}

// NewFragmentState creates a new fragment assembly state
func NewFragmentState() *FragmentState {
	return &FragmentState{
		fragments: make([]FragmentInfo, 0),
		startTime: time.Now(),
	}
}

// ValidateFragment validates a single fragment
func (v *FragmentValidator) ValidateFragment(info FragmentInfo) error {
	// Check fragment size
	if info.Size > v.maxFragmentSize {
		return ErrFragmentTooLarge
	}

	// Additional validations can be added here
	return nil
}

// ValidateSequence validates fragment sequence ordering
func (v *FragmentValidator) ValidateSequence(state *FragmentState, newSeq uint16) error {
	if len(state.fragments) == 0 {
		// First fragment, no sequence validation needed
		return nil
	}

	// Calculate sequence distance using RFC 1982 arithmetic
	gap := sequenceDistance(newSeq, state.lastSequence)

	// Check for duplicate
	if gap == 0 && !v.allowDuplicates {
		return ErrDuplicateFragment
	}

	// Check for reordering
	if gap < 0 && v.enforceStrictOrder {
		return ErrInvalidFragmentOrder
	}

	// Check for large gaps
	if gap > v.maxSequenceGap {
		return ErrFragmentGapTooLarge
	}

	return nil
}

// ValidateAssembly validates the entire fragment assembly state
func (v *FragmentValidator) ValidateAssembly(state *FragmentState) error {
	// Check timeout
	if time.Since(state.startTime) > v.fragmentTimeout {
		return ErrFragmentTimeout
	}

	// Check number of fragments
	if len(state.fragments) > v.maxFragments {
		return ErrTooManyFragments
	}

	// Check total size
	if state.totalSize > v.maxAssemblySize {
		return ErrFragmentTooLarge
	}

	return nil
}

// AddFragment adds a fragment to the state and validates
func (v *FragmentValidator) AddFragment(state *FragmentState, info FragmentInfo, packet *rtp.Packet) error {
	// Validate the fragment
	if err := v.ValidateFragment(info); err != nil {
		return err
	}

	// Validate sequence
	if err := v.ValidateSequence(state, info.SequenceNumber); err != nil {
		return err
	}

	// Add to state
	state.fragments = append(state.fragments, info)
	state.totalSize += info.Size
	state.lastSequence = info.SequenceNumber

	// Validate assembly state
	return v.ValidateAssembly(state)
}

// IsComplete checks if fragment assembly is complete
func (v *FragmentValidator) IsComplete(state *FragmentState) bool {
	if len(state.fragments) == 0 {
		return false
	}

	// Check if we have both start and end fragments
	hasStart := false
	hasEnd := false

	for _, frag := range state.fragments {
		if frag.IsStart {
			hasStart = true
		}
		if frag.IsEnd {
			hasEnd = true
		}
	}

	return hasStart && hasEnd
}

// Reset clears the fragment state
func (v *FragmentValidator) Reset(state *FragmentState) {
	state.fragments = state.fragments[:0]
	state.totalSize = 0
	state.startTime = time.Now()
	state.lastSequence = 0
	state.expectedSeq = 0
}

// sequenceDistance calculates the distance between two sequence numbers
// handling 16-bit wraparound according to RFC 1982
func sequenceDistance(s1, s2 uint16) int {
	return int(int16(s1 - s2))
}

// ValidateRTPPacket performs basic RTP packet validation
func ValidateRTPPacket(packet *rtp.Packet) error {
	if packet == nil {
		return errors.New("nil packet")
	}

	// Check version (should be 2)
	if packet.Version != 2 {
		return errors.New("invalid RTP version")
	}

	// Check payload
	if len(packet.Payload) == 0 {
		return errors.New("empty payload")
	}

	// Check payload size
	if len(packet.Payload) > security.MaxPacketSize {
		return errors.New("payload too large")
	}

	return nil
}

// FragmentMetrics holds metrics about fragment assembly
type FragmentMetrics struct {
	TotalFragments      int
	CompleteAssemblies  int
	TimeoutAssemblies   int
	DroppedFragments    int
	OutOfOrderFragments int
	DuplicateFragments  int
	LargeGaps           int
}

// UpdateMetrics updates fragment assembly metrics
func (v *FragmentValidator) UpdateMetrics(metrics *FragmentMetrics, err error) {
	if err == nil {
		return
	}

	switch err {
	case ErrFragmentTimeout:
		metrics.TimeoutAssemblies++
	case ErrDuplicateFragment:
		metrics.DuplicateFragments++
	case ErrInvalidFragmentOrder:
		metrics.OutOfOrderFragments++
	case ErrFragmentGapTooLarge:
		metrics.LargeGaps++
	default:
		metrics.DroppedFragments++
	}
}
