package validation

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/security"
)

func TestFragmentValidator_ValidateFragment(t *testing.T) {
	v := NewFragmentValidator()

	tests := []struct {
		name      string
		fragment  FragmentInfo
		wantError error
	}{
		{
			name: "valid fragment",
			fragment: FragmentInfo{
				SequenceNumber: 100,
				Size:           1000,
				ReceivedAt:     time.Now(),
			},
			wantError: nil,
		},
		{
			name: "fragment too large",
			fragment: FragmentInfo{
				SequenceNumber: 101,
				Size:           security.MaxFragmentSize + 1,
				ReceivedAt:     time.Now(),
			},
			wantError: ErrFragmentTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateFragment(tt.fragment)
			if tt.wantError != nil {
				assert.ErrorIs(t, err, tt.wantError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFragmentValidator_ValidateSequence(t *testing.T) {
	v := NewFragmentValidator()

	tests := []struct {
		name        string
		setupState  func() *FragmentState
		newSeq      uint16
		wantError   error
		description string
	}{
		{
			name: "first fragment",
			setupState: func() *FragmentState {
				return NewFragmentState()
			},
			newSeq:      100,
			wantError:   nil,
			description: "First fragment should always be valid",
		},
		{
			name: "sequential fragment",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments, FragmentInfo{SequenceNumber: 100})
				state.lastSequence = 100
				return state
			},
			newSeq:      101,
			wantError:   nil,
			description: "Sequential fragments should be valid",
		},
		{
			name: "duplicate fragment",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments, FragmentInfo{SequenceNumber: 100})
				state.lastSequence = 100
				return state
			},
			newSeq:      100,
			wantError:   ErrDuplicateFragment,
			description: "Duplicate fragments should be rejected",
		},
		{
			name: "reordered fragment",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments, FragmentInfo{SequenceNumber: 100})
				state.lastSequence = 100
				return state
			},
			newSeq:      99,
			wantError:   ErrInvalidFragmentOrder,
			description: "Out-of-order fragments should be rejected with strict ordering",
		},
		{
			name: "large gap",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments, FragmentInfo{SequenceNumber: 100})
				state.lastSequence = 100
				return state
			},
			newSeq:      110,
			wantError:   ErrFragmentGapTooLarge,
			description: "Large sequence gaps should be rejected",
		},
		{
			name: "wraparound sequence",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments, FragmentInfo{SequenceNumber: 65535})
				state.lastSequence = 65535
				return state
			},
			newSeq:      0,
			wantError:   nil,
			description: "Sequence wraparound should be handled correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.setupState()
			err := v.ValidateSequence(state, tt.newSeq)

			if tt.wantError != nil {
				assert.ErrorIs(t, err, tt.wantError, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

func TestFragmentValidator_ValidateAssembly(t *testing.T) {
	v := NewFragmentValidator()

	tests := []struct {
		name       string
		setupState func() *FragmentState
		wantError  error
	}{
		{
			name: "valid assembly",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments,
					FragmentInfo{Size: 1000},
					FragmentInfo{Size: 2000},
				)
				state.totalSize = 3000
				return state
			},
			wantError: nil,
		},
		{
			name: "assembly timeout",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.startTime = time.Now().Add(-10 * time.Second)
				return state
			},
			wantError: ErrFragmentTimeout,
		},
		{
			name: "too many fragments",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				// Add more than max fragments
				for i := 0; i < 1001; i++ {
					state.fragments = append(state.fragments, FragmentInfo{Size: 10})
				}
				return state
			},
			wantError: ErrTooManyFragments,
		},
		{
			name: "assembly too large",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.totalSize = security.MaxNALUnitSize + 1
				return state
			},
			wantError: ErrFragmentTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.setupState()
			err := v.ValidateAssembly(state)

			if tt.wantError != nil {
				assert.ErrorIs(t, err, tt.wantError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFragmentValidator_IsComplete(t *testing.T) {
	v := NewFragmentValidator()

	tests := []struct {
		name         string
		setupState   func() *FragmentState
		wantComplete bool
	}{
		{
			name: "empty state",
			setupState: func() *FragmentState {
				return NewFragmentState()
			},
			wantComplete: false,
		},
		{
			name: "only start fragment",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments,
					FragmentInfo{IsStart: true},
				)
				return state
			},
			wantComplete: false,
		},
		{
			name: "only end fragment",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments,
					FragmentInfo{IsEnd: true},
				)
				return state
			},
			wantComplete: false,
		},
		{
			name: "complete assembly",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments,
					FragmentInfo{IsStart: true},
					FragmentInfo{},
					FragmentInfo{IsEnd: true},
				)
				return state
			},
			wantComplete: true,
		},
		{
			name: "single fragment with both start and end",
			setupState: func() *FragmentState {
				state := NewFragmentState()
				state.fragments = append(state.fragments,
					FragmentInfo{IsStart: true, IsEnd: true},
				)
				return state
			},
			wantComplete: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.setupState()
			complete := v.IsComplete(state)
			assert.Equal(t, tt.wantComplete, complete)
		})
	}
}

func TestValidateRTPPacket(t *testing.T) {
	tests := []struct {
		name      string
		packet    *rtp.Packet
		wantError bool
		errorMsg  string
	}{
		{
			name:      "nil packet",
			packet:    nil,
			wantError: true,
			errorMsg:  "nil packet",
		},
		{
			name: "invalid version",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version: 1,
				},
				Payload: []byte{1, 2, 3},
			},
			wantError: true,
			errorMsg:  "invalid RTP version",
		},
		{
			name: "empty payload",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version: 2,
				},
				Payload: []byte{},
			},
			wantError: true,
			errorMsg:  "empty payload",
		},
		{
			name: "valid packet",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					SequenceNumber: 100,
				},
				Payload: []byte{1, 2, 3, 4, 5},
			},
			wantError: false,
		},
		{
			name: "payload too large",
			packet: &rtp.Packet{
				Header: rtp.Header{
					Version: 2,
				},
				Payload: make([]byte, security.MaxPacketSize+1),
			},
			wantError: true,
			errorMsg:  "payload too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRTPPacket(tt.packet)

			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestFragmentValidator_UpdateMetrics(t *testing.T) {
	v := NewFragmentValidator()

	tests := []struct {
		name          string
		err           error
		expectedField string
		expectedDelta int
	}{
		{
			name:          "no error",
			err:           nil,
			expectedField: "",
			expectedDelta: 0,
		},
		{
			name:          "timeout error",
			err:           ErrFragmentTimeout,
			expectedField: "TimeoutAssemblies",
			expectedDelta: 1,
		},
		{
			name:          "duplicate error",
			err:           ErrDuplicateFragment,
			expectedField: "DuplicateFragments",
			expectedDelta: 1,
		},
		{
			name:          "order error",
			err:           ErrInvalidFragmentOrder,
			expectedField: "OutOfOrderFragments",
			expectedDelta: 1,
		},
		{
			name:          "gap error",
			err:           ErrFragmentGapTooLarge,
			expectedField: "LargeGaps",
			expectedDelta: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := &FragmentMetrics{}
			v.UpdateMetrics(metrics, tt.err)

			// Check the specific field was incremented
			switch tt.expectedField {
			case "TimeoutAssemblies":
				assert.Equal(t, tt.expectedDelta, metrics.TimeoutAssemblies)
			case "DuplicateFragments":
				assert.Equal(t, tt.expectedDelta, metrics.DuplicateFragments)
			case "OutOfOrderFragments":
				assert.Equal(t, tt.expectedDelta, metrics.OutOfOrderFragments)
			case "LargeGaps":
				assert.Equal(t, tt.expectedDelta, metrics.LargeGaps)
			}
		})
	}
}

func TestFragmentValidator_CustomSettings(t *testing.T) {
	v := NewFragmentValidator()

	// Test with custom settings
	v.maxSequenceGap = 10
	v.allowDuplicates = true
	v.enforceStrictOrder = false

	state := NewFragmentState()
	state.fragments = append(state.fragments, FragmentInfo{SequenceNumber: 100})
	state.lastSequence = 100

	// Test duplicate is allowed
	err := v.ValidateSequence(state, 100)
	assert.NoError(t, err, "Duplicates should be allowed with custom settings")

	// Test reordering is allowed
	err = v.ValidateSequence(state, 99)
	assert.NoError(t, err, "Reordering should be allowed with custom settings")

	// Test larger gap is allowed
	err = v.ValidateSequence(state, 109)
	assert.NoError(t, err, "Gap of 9 should be allowed with maxSequenceGap=10")

	// Test gap exceeding limit
	err = v.ValidateSequence(state, 111)
	assert.ErrorIs(t, err, ErrFragmentGapTooLarge, "Gap of 11 should exceed maxSequenceGap=10")
}

func TestSequenceDistance(t *testing.T) {
	tests := []struct {
		s1       uint16
		s2       uint16
		expected int
	}{
		{100, 99, 1},
		{100, 101, -1},
		{0, 65535, 1},
		{65535, 0, -1},
		{32768, 0, -32768},
		{0, 32768, -32768},
	}

	for _, tt := range tests {
		result := sequenceDistance(tt.s1, tt.s2)
		assert.Equal(t, tt.expected, result,
			"sequenceDistance(%d, %d) = %d, want %d",
			tt.s1, tt.s2, result, tt.expected)
	}
}
