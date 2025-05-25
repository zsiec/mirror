package rtp

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

func TestSessionStats_Update(t *testing.T) {
	stats := &SessionStats{
		StartTime: time.Now(),
	}
	
	// Create test packets
	packets := []rtp.Packet{
		{Header: rtp.Header{SequenceNumber: 100}},
		{Header: rtp.Header{SequenceNumber: 101}},
		{Header: rtp.Header{SequenceNumber: 102}},
		{Header: rtp.Header{SequenceNumber: 104}}, // Skip 103 to simulate loss
		{Header: rtp.Header{SequenceNumber: 105}},
	}
	
	// Simulate receiving packets
	for i, packet := range packets {
		// First packet
		if i == 0 {
			stats.InitialSequence = packet.SequenceNumber
			stats.LastSequence = packet.SequenceNumber
			stats.PacketsReceived = 1
			stats.BytesReceived = 100
		} else {
			// Check for loss
			expectedSeq := stats.LastSequence + 1
			if packet.SequenceNumber != expectedSeq && packet.SequenceNumber > expectedSeq {
				lost := uint64(packet.SequenceNumber - expectedSeq)
				stats.PacketsLost += lost
			}
			stats.LastSequence = packet.SequenceNumber
			stats.PacketsReceived++
			stats.BytesReceived += 100
		}
		stats.LastPacketTime = time.Now()
	}
	
	assert.Equal(t, uint64(5), stats.PacketsReceived)
	assert.Equal(t, uint64(500), stats.BytesReceived)
	assert.Equal(t, uint64(1), stats.PacketsLost) // Lost packet 103
	assert.Equal(t, uint16(100), stats.InitialSequence)
	assert.Equal(t, uint16(105), stats.LastSequence)
}

func TestSession_IsActive(t *testing.T) {
	tests := []struct {
		name       string
		lastPacket time.Time
		expected   bool
	}{
		{
			name:       "active - recent packet",
			lastPacket: time.Now(),
			expected:   true,
		},
		{
			name:       "active - within threshold",
			lastPacket: time.Now().Add(-5 * time.Second),
			expected:   true,
		},
		{
			name:       "inactive - exceeded threshold",
			lastPacket: time.Now().Add(-15 * time.Second),
			expected:   false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &Session{
				lastPacket: tt.lastPacket,
			}
			assert.Equal(t, tt.expected, session.IsActive())
		})
	}
}

func TestSession_GetStats(t *testing.T) {
	originalStats := SessionStats{
		PacketsReceived: 100,
		BytesReceived:   10000,
		PacketsLost:     5,
		LastSequence:    200,
		InitialSequence: 100,
		StartTime:       time.Now(),
		LastPacketTime:  time.Now(),
	}
	
	session := &Session{
		stats: &originalStats,
	}
	
	// Get stats should return a copy
	stats := session.GetStats()
	
	assert.Equal(t, originalStats.PacketsReceived, stats.PacketsReceived)
	assert.Equal(t, originalStats.BytesReceived, stats.BytesReceived)
	assert.Equal(t, originalStats.PacketsLost, stats.PacketsLost)
	assert.Equal(t, originalStats.LastSequence, stats.LastSequence)
	assert.Equal(t, originalStats.InitialSequence, stats.InitialSequence)
	
	// Modify the returned stats - should not affect original
	stats.PacketsReceived = 200
	assert.Equal(t, uint64(100), session.stats.PacketsReceived)
}
