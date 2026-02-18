package rtp

import (
	"container/heap"
	"sync"
	"time"

	"github.com/pion/rtp"
)

// JitterBuffer implements a buffer to smooth out network jitter in RTP streams
type JitterBuffer struct {
	mu              sync.Mutex
	packets         *packetHeap
	seqSeen         map[uint16]bool // Track seen sequence numbers for duplicate detection
	maxSize         int
	targetDelay     time.Duration
	clockRate       uint32
	baseTime        time.Time
	baseTS          uint32
	hasBaseTime     bool
	lastGetWasEmpty bool // Track empty-to-empty transitions for underrun counting
	stats           JitterBufferStats
}

// JitterBufferStats tracks jitter buffer performance
type JitterBufferStats struct {
	PacketsBuffered  uint64
	PacketsDelivered uint64
	PacketsDropped   uint64
	PacketsLate      uint64
	CurrentDepth     int
	MaxDepth         int
	UnderrunCount    uint64
	OverrunCount     uint64
}

// packetEntry represents a packet in the jitter buffer
type packetEntry struct {
	packet      *rtp.Packet
	arrivalTime time.Time
	index       int // heap index
}

// packetHeap implements heap.Interface for packet ordering
type packetHeap []*packetEntry

func (h packetHeap) Len() int { return len(h) }
func (h packetHeap) Less(i, j int) bool {
	// Sort by sequence number with wraparound handling
	seqI, seqJ := h[i].packet.SequenceNumber, h[j].packet.SequenceNumber

	// Handle 16-bit wraparound
	diff := int32(seqI) - int32(seqJ)
	if diff > 32768 {
		diff -= 65536
	} else if diff < -32768 {
		diff += 65536
	}

	return diff < 0
}
func (h packetHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *packetHeap) Push(x interface{}) {
	n := len(*h)
	entry := x.(*packetEntry)
	entry.index = n
	*h = append(*h, entry)
}

func (h *packetHeap) Pop() interface{} {
	old := *h
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil // avoid memory leak
	entry.index = -1
	*h = old[0 : n-1]
	return entry
}

// NewJitterBuffer creates a new jitter buffer
func NewJitterBuffer(maxSize int, targetDelay time.Duration, clockRate uint32) *JitterBuffer {
	h := make(packetHeap, 0, maxSize)
	return &JitterBuffer{
		packets:     &h,
		seqSeen:     make(map[uint16]bool, maxSize),
		maxSize:     maxSize,
		targetDelay: targetDelay,
		clockRate:   clockRate,
	}
}

// Add inserts a packet into the jitter buffer
func (jb *JitterBuffer) Add(packet *rtp.Packet) error {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	// Set base time on first packet
	if !jb.hasBaseTime {
		jb.baseTime = time.Now()
		jb.baseTS = packet.Timestamp
		jb.hasBaseTime = true
	}

	// Discard duplicate packets per RFC 3550
	if jb.seqSeen[packet.SequenceNumber] {
		return nil
	}
	jb.seqSeen[packet.SequenceNumber] = true

	// Check if buffer is full
	if jb.packets.Len() >= jb.maxSize {
		// Drop oldest packet
		oldest := heap.Pop(jb.packets).(*packetEntry)
		delete(jb.seqSeen, oldest.packet.SequenceNumber)
		jb.stats.PacketsDropped++
		jb.stats.OverrunCount++
	}

	// Add new packet
	entry := &packetEntry{
		packet:      packet,
		arrivalTime: time.Now(),
	}
	heap.Push(jb.packets, entry)
	jb.stats.PacketsBuffered++

	// Update stats
	jb.stats.CurrentDepth = jb.packets.Len()
	if jb.stats.CurrentDepth > jb.stats.MaxDepth {
		jb.stats.MaxDepth = jb.stats.CurrentDepth
	}

	return nil
}

// Get retrieves packets that are ready for playout
func (jb *JitterBuffer) Get() ([]*rtp.Packet, error) {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	if jb.packets.Len() == 0 {
		// Only count underrun when transitioning from non-empty to empty,
		// not on every poll when already empty
		if !jb.lastGetWasEmpty && jb.stats.PacketsDelivered > 0 {
			jb.stats.UnderrunCount++
		}
		jb.lastGetWasEmpty = true
		return nil, nil
	}

	now := time.Now()
	var ready []*rtp.Packet

	// Calculate playout time based on target delay
	for jb.packets.Len() > 0 {
		entry := (*jb.packets)[0]

		// Calculate expected playout time with 32-bit timestamp wraparound handling.
		// Cast to int32 first for proper signed wraparound arithmetic per RFC 3550.
		tsDiff := int64(int32(entry.packet.Timestamp - jb.baseTS))
		expectedTime := jb.baseTime.Add(time.Duration(tsDiff * 1e9 / int64(jb.clockRate)))
		playoutTime := expectedTime.Add(jb.targetDelay)

		// Check if packet is ready for playout
		if now.Before(playoutTime) {
			break // Not ready yet
		}

		// Remove from heap
		heap.Pop(jb.packets)

		// Check if packet is too late
		if now.Sub(playoutTime) > jb.targetDelay {
			jb.stats.PacketsLate++
			delete(jb.seqSeen, entry.packet.SequenceNumber) // Prevent seqSeen memory leak
			continue                                        // Skip late packet
		}

		ready = append(ready, entry.packet)
		delete(jb.seqSeen, entry.packet.SequenceNumber)
		jb.stats.PacketsDelivered++
	}

	// Update current depth and empty tracking
	jb.stats.CurrentDepth = jb.packets.Len()
	if len(ready) > 0 {
		jb.lastGetWasEmpty = false
	}

	return ready, nil
}

// Flush removes all packets from the buffer
func (jb *JitterBuffer) Flush() {
	jb.mu.Lock()
	defer jb.mu.Unlock()

	// Clear the heap
	*jb.packets = (*jb.packets)[:0]
	jb.seqSeen = make(map[uint16]bool, jb.maxSize)
	jb.stats.CurrentDepth = 0
	jb.hasBaseTime = false
}

// GetStats returns current jitter buffer statistics
func (jb *JitterBuffer) GetStats() JitterBufferStats {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.stats
}

// SetTargetDelay updates the target playout delay
func (jb *JitterBuffer) SetTargetDelay(delay time.Duration) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	jb.targetDelay = delay
}

// GetCurrentDepth returns the number of packets currently buffered
func (jb *JitterBuffer) GetCurrentDepth() int {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	return jb.packets.Len()
}
