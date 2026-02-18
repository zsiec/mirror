package rtp

import (
	"fmt"
	"sync"
	"time"
)

// PacketLossTracker tracks RTP packet loss and provides recovery information
type PacketLossTracker struct {
	mu sync.Mutex

	// Sequence tracking
	initialized     bool
	highestSeq      uint16
	baseSeq         uint16
	cycles          uint32 // Number of sequence number wraparounds
	extendedSeq     uint64 // Extended sequence number (32-bit)
	expectedPackets uint64
	receivedPackets uint64

	// Loss tracking
	lostPackets        uint64
	lossEvents         uint64
	maxConsecutiveLoss uint16
	currentLossStreak  uint16

	// Recovery tracking
	recoveredPackets  uint64
	unrecoverableLoss uint64

	// Loss patterns
	recentLosses    []uint16 // Recent lost sequence numbers (circular buffer)
	recentLossIndex int
	recentLossSize  int

	// Time-based tracking
	lastPacketTime    time.Time
	lastLossTime      time.Time
	lossWindowStart   time.Time
	lossWindowPackets uint64
	lossWindowLosses  uint64

	// Configuration
	maxRecentLosses    int
	lossWindowDuration time.Duration
}

// RecoveryStats contains statistics about packet recovery
type RecoveryStats struct {
	TotalLost          uint64
	TotalRecovered     uint64
	UnrecoverableLoss  uint64
	CurrentLossRate    float64
	AverageLossRate    float64
	MaxConsecutiveLoss uint16
	LossEvents         uint64
}

// NewPacketLossTracker creates a new packet loss tracker
func NewPacketLossTracker() *PacketLossTracker {
	return &PacketLossTracker{
		maxRecentLosses:    1000,
		recentLosses:       make([]uint16, 1000),
		lossWindowDuration: 10 * time.Second,
		lossWindowStart:    time.Now(),
	}
}

// ProcessSequence processes a sequence number and detects packet loss
func (plt *PacketLossTracker) ProcessSequence(seq uint16) error {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	now := time.Now()

	// Initialize on first packet
	if !plt.initialized {
		plt.initialized = true
		plt.baseSeq = seq
		plt.highestSeq = seq
		plt.extendedSeq = uint64(seq)
		plt.lastPacketTime = now
		plt.receivedPackets = 1
		plt.expectedPackets = 1
		return nil
	}

	// Update timing
	plt.lastPacketTime = now

	// Calculate sequence distance with wraparound handling
	distance := plt.sequenceDistance(seq, plt.highestSeq)

	// Handle different cases
	switch {
	case distance == 0:
		// Duplicate packet
		return fmt.Errorf("duplicate packet: sequence %d", seq)

	case distance > 0 && distance < 32768:
		// Normal forward progression with possible loss
		if distance > 1 {
			// Packet loss detected
			lostCount := uint64(distance - 1)
			plt.recordLoss(plt.highestSeq+1, seq-1, lostCount)
		}

		// Update highest sequence with wraparound handling
		if seq < plt.highestSeq {
			// Wraparound occurred
			plt.cycles++
		}
		plt.highestSeq = seq
		plt.extendedSeq = (uint64(plt.cycles) << 16) | uint64(seq)
		plt.receivedPackets++
		plt.expectedPackets += uint64(distance)

		// Reset loss streak on successful packet
		plt.currentLossStreak = 0

	case distance < 0 && distance > -100:
		// Out of order packet (reordering)
		// Check if this was previously marked as lost
		if plt.wasRecentlyLost(seq) {
			plt.recoveredPackets++
		}
		plt.receivedPackets++

	default:
		// Severe reordering or reset
		return fmt.Errorf("sequence reset detected: from %d to %d (distance %d)",
			plt.highestSeq, seq, distance)
	}

	// Update loss window statistics
	plt.updateLossWindow(now)

	return nil
}

// detectPacketLoss is a convenience method that processes sequence and returns lost packets
func (plt *PacketLossTracker) detectPacketLoss(seq uint16) ([]uint16, error) {
	err := plt.ProcessSequence(seq)
	if err != nil {
		return nil, err
	}

	// Return any newly detected lost packets
	plt.mu.Lock()
	defer plt.mu.Unlock()

	// Get recent losses that haven't been reported
	var lostPackets []uint16
	if plt.recentLossSize > 0 {
		// Copy recent losses
		startIdx := plt.recentLossIndex - plt.recentLossSize
		if startIdx < 0 {
			startIdx += plt.maxRecentLosses
		}

		for i := 0; i < plt.recentLossSize && i < 10; i++ { // Limit to 10 most recent
			idx := (startIdx + i) % plt.maxRecentLosses
			lostPackets = append(lostPackets, plt.recentLosses[idx])
		}
	}

	return lostPackets, nil
}

// recordLoss records packet loss information
func (plt *PacketLossTracker) recordLoss(startSeq, endSeq uint16, count uint64) {
	plt.lostPackets += count
	plt.lossEvents++
	plt.lastLossTime = time.Now()
	plt.lossWindowLosses += count

	// Update consecutive loss tracking
	plt.currentLossStreak += uint16(count)
	if plt.currentLossStreak > plt.maxConsecutiveLoss {
		plt.maxConsecutiveLoss = plt.currentLossStreak
	}

	// Record individual lost sequences (up to limit)
	for seq := startSeq; seq != endSeq+1 && plt.recentLossSize < plt.maxRecentLosses; seq++ {
		plt.recentLosses[plt.recentLossIndex] = seq
		plt.recentLossIndex = (plt.recentLossIndex + 1) % plt.maxRecentLosses
		if plt.recentLossSize < plt.maxRecentLosses {
			plt.recentLossSize++
		}
	}
}

// wasRecentlyLost checks if a sequence number was recently marked as lost
func (plt *PacketLossTracker) wasRecentlyLost(seq uint16) bool {
	for i := 0; i < plt.recentLossSize; i++ {
		idx := (plt.recentLossIndex - 1 - i + plt.maxRecentLosses) % plt.maxRecentLosses
		if plt.recentLosses[idx] == seq {
			return true
		}
	}
	return false
}

// updateLossWindow updates the sliding window loss statistics
func (plt *PacketLossTracker) updateLossWindow(now time.Time) {
	if now.Sub(plt.lossWindowStart) > plt.lossWindowDuration {
		// Reset window
		plt.lossWindowStart = now
		plt.lossWindowPackets = 0
		plt.lossWindowLosses = 0
	}
	plt.lossWindowPackets++
}

// GetStats returns current recovery statistics
func (plt *PacketLossTracker) GetStats() RecoveryStats {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	stats := RecoveryStats{
		TotalLost:          plt.lostPackets,
		TotalRecovered:     plt.recoveredPackets,
		UnrecoverableLoss:  plt.lostPackets - plt.recoveredPackets,
		MaxConsecutiveLoss: plt.maxConsecutiveLoss,
		LossEvents:         plt.lossEvents,
	}

	// Calculate average loss rate
	if plt.expectedPackets > 0 {
		stats.AverageLossRate = float64(plt.lostPackets) / float64(plt.expectedPackets)
	}

	// Calculate current loss rate (sliding window)
	if plt.lossWindowPackets > 0 {
		stats.CurrentLossRate = float64(plt.lossWindowLosses) / float64(plt.lossWindowPackets)
	}

	return stats
}

// GetLostPackets returns a list of currently lost packet sequence numbers
func (plt *PacketLossTracker) GetLostPackets(maxCount int) []uint16 {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	var lost []uint16
	count := plt.recentLossSize
	if count > maxCount {
		count = maxCount
	}

	for i := 0; i < count; i++ {
		idx := (plt.recentLossIndex - 1 - i + plt.maxRecentLosses) % plt.maxRecentLosses
		lost = append(lost, plt.recentLosses[idx])
	}

	return lost
}

// sequenceDistance calculates the distance between two sequence numbers
// handling 16-bit wraparound according to RFC 1982
func (plt *PacketLossTracker) sequenceDistance(s1, s2 uint16) int {
	return int(int16(s1 - s2))
}

// Reset resets the tracker to initial state
func (plt *PacketLossTracker) Reset() {
	plt.mu.Lock()
	defer plt.mu.Unlock()

	plt.initialized = false
	plt.highestSeq = 0
	plt.baseSeq = 0
	plt.cycles = 0
	plt.extendedSeq = 0
	plt.expectedPackets = 0
	plt.receivedPackets = 0
	plt.lostPackets = 0
	plt.lossEvents = 0
	plt.maxConsecutiveLoss = 0
	plt.currentLossStreak = 0
	plt.recoveredPackets = 0
	plt.unrecoverableLoss = 0
	plt.recentLossIndex = 0
	plt.recentLossSize = 0
	plt.lossWindowStart = time.Now()
	plt.lossWindowPackets = 0
	plt.lossWindowLosses = 0
}
