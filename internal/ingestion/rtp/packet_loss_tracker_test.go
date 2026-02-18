package rtp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPacketLossTracker_NormalSequence(t *testing.T) {
	plt := NewPacketLossTracker()

	// Process normal sequence
	for i := uint16(100); i < 110; i++ {
		err := plt.ProcessSequence(i)
		assert.NoError(t, err)
	}

	stats := plt.GetStats()
	assert.Equal(t, uint64(0), stats.TotalLost)
	assert.Equal(t, float64(0), stats.AverageLossRate)
}

func TestPacketLossTracker_PacketLoss(t *testing.T) {
	plt := NewPacketLossTracker()

	// Process sequence with gaps
	sequences := []uint16{100, 101, 105, 106, 110}

	for _, seq := range sequences {
		err := plt.ProcessSequence(seq)
		assert.NoError(t, err)
	}

	stats := plt.GetStats()
	assert.Equal(t, uint64(6), stats.TotalLost) // Lost: 102,103,104,107,108,109
	assert.Equal(t, uint64(2), stats.LossEvents)
	assert.Greater(t, stats.AverageLossRate, 0.0)

	// Check lost packets
	lost := plt.GetLostPackets(10)
	assert.Contains(t, lost, uint16(107))
	assert.Contains(t, lost, uint16(108))
	assert.Contains(t, lost, uint16(109))
}

func TestPacketLossTracker_SequenceWrapAround(t *testing.T) {
	plt := NewPacketLossTracker()

	// Start near wraparound
	sequences := []uint16{65533, 65534, 65535, 0, 1, 2}

	for _, seq := range sequences {
		err := plt.ProcessSequence(seq)
		assert.NoError(t, err)
	}

	stats := plt.GetStats()
	assert.Equal(t, uint64(0), stats.TotalLost)
	assert.Equal(t, uint32(1), plt.cycles) // Should detect wraparound
}

func TestPacketLossTracker_LossAcrossWrapAround(t *testing.T) {
	plt := NewPacketLossTracker()

	// Loss across wraparound boundary
	err := plt.ProcessSequence(65534)
	assert.NoError(t, err)

	err = plt.ProcessSequence(2) // Skip 65535, 0, 1
	assert.NoError(t, err)

	stats := plt.GetStats()
	assert.Equal(t, uint64(3), stats.TotalLost)

	lost := plt.GetLostPackets(5)
	assert.Contains(t, lost, uint16(65535))
	assert.Contains(t, lost, uint16(0))
	assert.Contains(t, lost, uint16(1))
}

func TestPacketLossTracker_OutOfOrder(t *testing.T) {
	plt := NewPacketLossTracker()

	// Process in order first
	for i := uint16(100); i < 105; i++ {
		err := plt.ProcessSequence(i)
		assert.NoError(t, err)
	}

	// Skip 105, process 106
	err := plt.ProcessSequence(106)
	assert.NoError(t, err)

	stats := plt.GetStats()
	assert.Equal(t, uint64(1), stats.TotalLost)

	// Now receive 105 out of order
	err = plt.ProcessSequence(105)
	assert.NoError(t, err)

	stats = plt.GetStats()
	assert.Equal(t, uint64(1), stats.TotalRecovered)
}

func TestPacketLossTracker_DuplicatePacket(t *testing.T) {
	plt := NewPacketLossTracker()

	err := plt.ProcessSequence(100)
	assert.NoError(t, err)

	// Send duplicate
	err = plt.ProcessSequence(100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestPacketLossTracker_SequenceReset(t *testing.T) {
	plt := NewPacketLossTracker()

	// Normal sequence
	for i := uint16(1000); i < 1010; i++ {
		err := plt.ProcessSequence(i)
		assert.NoError(t, err)
	}

	// Large backward jump (reset)
	err := plt.ProcessSequence(100)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sequence reset")
}

func TestPacketLossTracker_ConsecutiveLoss(t *testing.T) {
	plt := NewPacketLossTracker()

	// Process with large gap
	err := plt.ProcessSequence(100)
	assert.NoError(t, err)

	err = plt.ProcessSequence(120) // 19 consecutive losses
	assert.NoError(t, err)

	stats := plt.GetStats()
	assert.Equal(t, uint64(19), stats.TotalLost)
	assert.Equal(t, uint16(19), stats.MaxConsecutiveLoss)
}

func TestPacketLossTracker_LossRate(t *testing.T) {
	plt := NewPacketLossTracker()

	// Simulate 10% loss rate
	for i := uint16(0); i < 100; i++ {
		if i%10 != 5 { // Skip every 10th packet (at position 5)
			err := plt.ProcessSequence(i)
			assert.NoError(t, err)
		}
	}

	stats := plt.GetStats()
	assert.InDelta(t, 0.1, stats.AverageLossRate, 0.01)
}

func TestPacketLossTracker_LossRecoveryPattern(t *testing.T) {
	plt := NewPacketLossTracker()

	// Simulate burst loss and recovery
	sequences := []struct {
		seq      uint16
		expected string
	}{
		{100, "init"},
		{101, "normal"},
		{105, "loss detected"}, // Lost 102,103,104
		{102, "recovered"},     // Out of order recovery
		{103, "recovered"},     // Out of order recovery
		{106, "normal"},
	}

	for _, s := range sequences {
		err := plt.ProcessSequence(s.seq)
		if s.expected == "init" || s.expected == "normal" || s.expected == "loss detected" {
			assert.NoError(t, err, "Sequence %d: %s", s.seq, s.expected)
		}
	}

	stats := plt.GetStats()
	assert.Equal(t, uint64(3), stats.TotalLost)
	assert.Equal(t, uint64(2), stats.TotalRecovered)
	assert.Equal(t, uint64(1), stats.UnrecoverableLoss) // 104 still missing
}

func TestPacketLossTracker_RecentLosses(t *testing.T) {
	plt := NewPacketLossTracker()
	plt.maxRecentLosses = 5 // Limit for testing

	// Create losses that exceed the limit
	err := plt.ProcessSequence(100)
	assert.NoError(t, err)

	err = plt.ProcessSequence(110) // Lost 101-109
	assert.NoError(t, err)

	lost := plt.GetLostPackets(10)
	assert.LessOrEqual(t, len(lost), 5) // Should be limited

	// Most recent losses should be kept (101-105 because we limited to 5)
	assert.Contains(t, lost, uint16(101))
	assert.Contains(t, lost, uint16(102))
	assert.Contains(t, lost, uint16(103))
	assert.Contains(t, lost, uint16(104))
	assert.Contains(t, lost, uint16(105))
}

func TestPacketLossTracker_WindowedLossRate(t *testing.T) {
	plt := NewPacketLossTracker()
	plt.lossWindowDuration = 100 * time.Millisecond

	// First window - 20% loss
	for i := uint16(0); i < 10; i++ {
		if i%5 != 0 { // Skip every 5th
			err := plt.ProcessSequence(i)
			assert.NoError(t, err)
		}
	}

	stats := plt.GetStats()
	assert.InDelta(t, 0.2, stats.CurrentLossRate, 0.1) // Allow more tolerance for window calculation

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Second window - no loss
	for i := uint16(10); i < 20; i++ {
		err := plt.ProcessSequence(i)
		assert.NoError(t, err)
	}

	stats = plt.GetStats()
	assert.Equal(t, float64(0), stats.CurrentLossRate)
}

func TestPacketLossTracker_Reset(t *testing.T) {
	plt := NewPacketLossTracker()

	// Add some data
	for i := uint16(0); i < 10; i++ {
		if i != 5 {
			err := plt.ProcessSequence(i)
			assert.NoError(t, err)
		}
	}

	stats := plt.GetStats()
	assert.Greater(t, stats.TotalLost, uint64(0))

	// Reset
	plt.Reset()

	stats = plt.GetStats()
	assert.Equal(t, uint64(0), stats.TotalLost)
	assert.Equal(t, uint64(0), stats.TotalRecovered)
	assert.Equal(t, uint64(0), stats.LossEvents)
}

func TestPacketLossTracker_DetectPacketLoss(t *testing.T) {
	plt := NewPacketLossTracker()

	// Process initial sequence
	lost, err := plt.detectPacketLoss(100)
	require.NoError(t, err)
	assert.Empty(t, lost)

	// Create gap
	lost, err = plt.detectPacketLoss(103)
	require.NoError(t, err)
	assert.Len(t, lost, 2)
	assert.Contains(t, lost, uint16(101))
	assert.Contains(t, lost, uint16(102))
}
