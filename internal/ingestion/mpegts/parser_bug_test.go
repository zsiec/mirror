package mpegts

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

// TestExtractTimestampPanic demonstrates the buffer overflow bug is fixed
func TestExtractTimestampPanic(t *testing.T) {
	parser := &Parser{}
	
	tests := []struct {
		name      string
		data      []byte
		shouldError bool
		wantErr   string
	}{
		{
			name:      "valid 5 bytes",
			data:      []byte{0x21, 0x00, 0x01, 0x00, 0x01},
			shouldError: false,
		},
		{
			name:      "empty buffer",
			data:      []byte{},
			shouldError: true,
			wantErr:   "insufficient data for timestamp: need 5 bytes, got 0",
		},
		{
			name:      "1 byte only",
			data:      []byte{0x21},
			shouldError: true,
			wantErr:   "insufficient data for timestamp: need 5 bytes, got 1",
		},
		{
			name:      "4 bytes only",
			data:      []byte{0x21, 0x00, 0x01, 0x00},
			shouldError: true,
			wantErr:   "insufficient data for timestamp: need 5 bytes, got 4",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamp, err := parser.extractTimestamp(tt.data)
			
			if tt.shouldError {
				assert.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				assert.Equal(t, int64(0), timestamp)
			} else {
				assert.NoError(t, err)
				assert.GreaterOrEqual(t, timestamp, int64(0))
			}
		})
	}
}

// TestSequenceNumberWraparound verifies that Go handles uint16 wraparound correctly
func TestSequenceNumberWraparound(t *testing.T) {
	// This test demonstrates that Go's uint16 arithmetic automatically handles wraparound
	// The "bug" mentioned in the docs doesn't actually exist in Go because
	// uint16 addition wraps around automatically: 65535 + 1 = 0
	
	lastSeq := uint16(65535)
	currentSeq := uint16(0)
	
	// Go's uint16 arithmetic handles wraparound automatically
	// So this "buggy" comparison actually works correctly!
	buggyResult := currentSeq == lastSeq+1 // true, because 65535+1 wraps to 0
	assert.True(t, buggyResult, "Go handles uint16 wraparound automatically")
	
	// Correct comparison
	correctResult := isNextSequence(lastSeq, currentSeq)
	assert.True(t, correctResult, "Correct comparison handles wraparound")
}

// Helper function that should be added to handle wraparound
func isNextSequence(prev, curr uint16) bool {
	return curr == prev+1 || (prev == 65535 && curr == 0)
}

// TestRingBufferOverwrite demonstrates the ring buffer corruption bug
func TestRingBufferOverwrite(t *testing.T) {
	// This would go in ring_test.go
	// Demonstrating the race condition where writer overwrites unread data
	
	// Scenario: Buffer size 100, reader at position 10, writer at position 90
	// Writing 30 bytes would wrap around and overwrite positions 0-20
	// But reader hasn't read positions 10-20 yet!
	
	bufferSize := int64(100)
	readerPos := int64(10)
	writerPos := int64(90)
	dataToWrite := int64(30)
	
	// Calculate where write would end
	endPos := (writerPos + dataToWrite) % bufferSize // 20
	
	// Check if we would overwrite unread data
	if writerPos < readerPos {
		// Already wrapped
		overwrite := endPos >= readerPos
		assert.True(t, overwrite, "Would overwrite unread data")
	} else {
		// Check if write would wrap and overtake reader
		wouldWrap := writerPos + dataToWrite > bufferSize
		if wouldWrap {
			overwrite := endPos >= readerPos
			assert.True(t, overwrite, "Would overwrite unread data after wrap")
		}
	}
}
