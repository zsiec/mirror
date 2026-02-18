package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPESValidator_ValidatePESStart(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "too short",
			data:    []byte{0x00, 0x00, 0x01},
			wantErr: true,
			errMsg:  "PES header too short",
		},
		{
			name:    "invalid start code",
			data:    []byte{0xFF, 0x00, 0x01, 0xE0, 0x00, 0x00},
			wantErr: true,
			errMsg:  "invalid PES start code",
		},
		{
			name:    "valid PES start with length",
			data:    []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x64}, // 100 bytes
			wantErr: false,
		},
		{
			name:    "valid PES start without length (video)",
			data:    []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00}, // 0 length
			wantErr: false,
		},
		{
			name:    "PES too large",
			data:    []byte{0x00, 0x00, 0x01, 0xE0, 0xFF, 0xFF}, // 65535 bytes
			wantErr: false,                                      // Should be under default limit
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset state for clean test
			validator.ResetPID(pid)

			err := validator.ValidatePESStart(pid, tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPESValidator_IncompletePES(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	// Start first PES
	pesHeader1 := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x64}
	err := validator.ValidatePESStart(pid, pesHeader1)
	assert.NoError(t, err)

	// Try to start another PES without completing the first
	pesHeader2 := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x50}
	err = validator.ValidatePESStart(pid, pesHeader2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "incomplete PES packet")
}

func TestPESValidator_ValidatePESContinuation(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	// Start PES with 100 byte length
	pesHeader := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x64}
	validator.ValidatePESStart(pid, pesHeader)

	// Continue with data
	err := validator.ValidatePESContinuation(pid, 50)
	assert.NoError(t, err)

	// Check state
	inProgress, bytes, packets := validator.GetStreamState(pid)
	assert.True(t, inProgress)
	assert.Equal(t, 56, bytes)  // 6 header + 50 data
	assert.Equal(t, 2, packets) // start + continuation

	// Complete the PES (need 50 more bytes to reach 106 total)
	err = validator.ValidatePESContinuation(pid, 50)
	assert.NoError(t, err)

	// Should be complete now
	inProgress, _, _ = validator.GetStreamState(pid)
	assert.False(t, inProgress)
}

func TestPESValidator_ContinuationWithoutStart(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	// Try continuation without start
	err := validator.ValidatePESContinuation(pid, 100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "continuation without start")
}

func TestPESValidator_Overrun(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	// Start PES with 50 byte length
	pesHeader := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x32} // 50 bytes
	validator.ValidatePESStart(pid, pesHeader)

	// Add 45 bytes (not complete yet)
	err := validator.ValidatePESContinuation(pid, 45)
	assert.NoError(t, err)

	// Try to add 10 more bytes - should cause overrun (51 + 10 > 56)
	err = validator.ValidatePESContinuation(pid, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "overrun")
}

func TestPESValidator_ContinuationAfterComplete(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	// Start PES with 50 byte length
	pesHeader := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x32} // 50 bytes
	validator.ValidatePESStart(pid, pesHeader)

	// Add exactly 50 bytes (should complete at 56 total)
	err := validator.ValidatePESContinuation(pid, 50)
	assert.NoError(t, err)

	// PES is now complete, try to add more data
	err = validator.ValidatePESContinuation(pid, 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "continuation without start")
}

func TestPESValidator_ExceedsMaxSize(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	validator.maxPESSize = 1000 // Set small limit for testing
	pid := uint16(256)

	// Start PES without length
	pesHeader := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00}
	validator.ValidatePESStart(pid, pesHeader)

	// Add data up to limit
	err := validator.ValidatePESContinuation(pid, 994) // 6 + 994 = 1000
	assert.NoError(t, err)

	// Exceed limit
	err = validator.ValidatePESContinuation(pid, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum size")
}

func TestPESValidator_Timeout(t *testing.T) {
	validator := NewPESValidator(100 * time.Millisecond) // Short timeout for test
	pid1 := uint16(256)
	pid2 := uint16(257)

	// Start two PES packets
	validator.ValidatePESStart(pid1, []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00})
	validator.ValidatePESStart(pid2, []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00})

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Check timeouts
	timedOut := validator.CheckTimeouts()
	assert.Len(t, timedOut, 2)
	assert.Contains(t, timedOut, pid1)
	assert.Contains(t, timedOut, pid2)

	// Verify they're no longer in progress
	inProgress1, _, _ := validator.GetStreamState(pid1)
	inProgress2, _, _ := validator.GetStreamState(pid2)
	assert.False(t, inProgress1)
	assert.False(t, inProgress2)
}

func TestPESValidator_CompletePES(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)
	pid := uint16(256)

	// Start PES
	validator.ValidatePESStart(pid, []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00})

	// Verify it's in progress
	inProgress, _, _ := validator.GetStreamState(pid)
	assert.True(t, inProgress)

	// Complete it manually
	validator.CompletePES(pid)

	// Verify it's no longer in progress
	inProgress, _, _ = validator.GetStreamState(pid)
	assert.False(t, inProgress)
}

func TestPESValidator_Reset(t *testing.T) {
	validator := NewPESValidator(5 * time.Second)

	// Start multiple PES packets
	validator.ValidatePESStart(256, []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00})
	validator.ValidatePESStart(257, []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x00})

	// Reset all
	validator.Reset()

	// Verify all cleared
	stats := validator.GetStats()
	assert.Equal(t, 0, stats.ActiveStreams)
}
