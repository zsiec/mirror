package memory

import (
	"sort"
	"sync"
	"time"
)

// EvictionStrategy defines how to select streams for eviction
type EvictionStrategy interface {
	SelectStreamsForEviction(streams []StreamInfo, targetBytes int64) []string
}

// StreamInfo contains information about a stream for eviction decisions
type StreamInfo struct {
	StreamID    string
	MemoryUsage int64
	LastAccess  time.Time
	Priority    int // Lower number = higher priority (less likely to evict)
	IsActive    bool
	CreatedAt   time.Time
}

// LRUEvictionStrategy evicts least recently used streams
type LRUEvictionStrategy struct{}

func (s *LRUEvictionStrategy) SelectStreamsForEviction(streams []StreamInfo, targetBytes int64) []string {
	// Sort by last access time (oldest first)
	sort.Slice(streams, func(i, j int) bool {
		// Active streams should be evicted last
		if streams[i].IsActive != streams[j].IsActive {
			return !streams[i].IsActive
		}
		// Among inactive streams, evict least recently used
		return streams[i].LastAccess.Before(streams[j].LastAccess)
	})

	var selected []string
	var totalBytes int64

	for _, stream := range streams {
		if totalBytes >= targetBytes {
			break
		}
		selected = append(selected, stream.StreamID)
		totalBytes += stream.MemoryUsage
	}

	return selected
}

// PriorityEvictionStrategy evicts based on stream priority
type PriorityEvictionStrategy struct{}

func (s *PriorityEvictionStrategy) SelectStreamsForEviction(streams []StreamInfo, targetBytes int64) []string {
	// Sort by priority (higher priority value = more likely to evict)
	sort.Slice(streams, func(i, j int) bool {
		if streams[i].Priority != streams[j].Priority {
			return streams[i].Priority > streams[j].Priority
		}
		// If same priority, use LRU
		return streams[i].LastAccess.Before(streams[j].LastAccess)
	})

	var selected []string
	var totalBytes int64

	for _, stream := range streams {
		if totalBytes >= targetBytes {
			break
		}
		selected = append(selected, stream.StreamID)
		totalBytes += stream.MemoryUsage
	}

	return selected
}

// SizeBasedEvictionStrategy evicts largest streams first
type SizeBasedEvictionStrategy struct{}

func (s *SizeBasedEvictionStrategy) SelectStreamsForEviction(streams []StreamInfo, targetBytes int64) []string {
	// Sort by memory usage (largest first)
	sort.Slice(streams, func(i, j int) bool {
		return streams[i].MemoryUsage > streams[j].MemoryUsage
	})

	var selected []string
	var totalBytes int64

	for _, stream := range streams {
		if totalBytes >= targetBytes {
			break
		}
		selected = append(selected, stream.StreamID)
		totalBytes += stream.MemoryUsage
	}

	return selected
}

// HybridEvictionStrategy combines multiple strategies
type HybridEvictionStrategy struct {
	// Weight for each factor (should sum to 1.0)
	AgeWeight      float64
	SizeWeight     float64
	PriorityWeight float64
}

func (s *HybridEvictionStrategy) SelectStreamsForEviction(streams []StreamInfo, targetBytes int64) []string {
	// Calculate a score for each stream (higher score = more likely to evict)
	type scoredStream struct {
		StreamInfo
		score float64
	}

	scored := make([]scoredStream, len(streams))
	now := time.Now()

	// Find max values for normalization
	var maxAge time.Duration
	var maxSize int64
	maxPriority := 10 // Assume priority is 0-10

	for _, stream := range streams {
		age := now.Sub(stream.LastAccess)
		if age > maxAge {
			maxAge = age
		}
		if stream.MemoryUsage > maxSize {
			maxSize = stream.MemoryUsage
		}
	}

	// Calculate scores
	for i, stream := range streams {
		scored[i].StreamInfo = stream

		// Skip active streams unless necessary
		if stream.IsActive {
			scored[i].score = -1 // Negative score to sort last
			continue
		}

		// Age score (0-1, older = higher)
		ageScore := float64(now.Sub(stream.LastAccess)) / float64(maxAge)

		// Size score (0-1, larger = higher)
		sizeScore := float64(stream.MemoryUsage) / float64(maxSize)

		// Priority score (0-1, lower priority = higher score)
		priorityScore := float64(stream.Priority) / float64(maxPriority)

		// Combined score
		scored[i].score = s.AgeWeight*ageScore +
			s.SizeWeight*sizeScore +
			s.PriorityWeight*priorityScore
	}

	// Sort by score (highest first)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var selected []string
	var totalBytes int64

	for _, stream := range scored {
		if totalBytes >= targetBytes {
			break
		}
		if stream.score < 0 { // Skip active streams if possible
			continue
		}
		selected = append(selected, stream.StreamID)
		totalBytes += stream.MemoryUsage
	}

	// If we haven't freed enough, include active streams
	if totalBytes < targetBytes {
		for _, stream := range scored {
			if totalBytes >= targetBytes {
				break
			}
			if stream.score < 0 && !containsString(selected, stream.StreamID) {
				selected = append(selected, stream.StreamID)
				totalBytes += stream.MemoryUsage
			}
		}
	}

	return selected
}

// StreamTracker tracks stream access patterns for eviction
type StreamTracker struct {
	mu      sync.RWMutex
	streams map[string]*trackedStream
}

type trackedStream struct {
	streamID   string
	lastAccess time.Time
	createdAt  time.Time
	priority   int
	isActive   bool
}

func NewStreamTracker() *StreamTracker {
	return &StreamTracker{
		streams: make(map[string]*trackedStream),
	}
}

func (t *StreamTracker) TrackStream(streamID string, priority int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.streams[streamID] = &trackedStream{
		streamID:   streamID,
		lastAccess: now,
		createdAt:  now,
		priority:   priority,
		isActive:   true,
	}
}

func (t *StreamTracker) UpdateAccess(streamID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if stream, ok := t.streams[streamID]; ok {
		stream.lastAccess = time.Now()
	}
}

func (t *StreamTracker) SetActive(streamID string, active bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if stream, ok := t.streams[streamID]; ok {
		stream.isActive = active
	}
}

func (t *StreamTracker) RemoveStream(streamID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.streams, streamID)
}

func (t *StreamTracker) GetStreamInfo(streamID string, memoryUsage int64) *StreamInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if stream, ok := t.streams[streamID]; ok {
		return &StreamInfo{
			StreamID:    stream.streamID,
			MemoryUsage: memoryUsage,
			LastAccess:  stream.lastAccess,
			Priority:    stream.priority,
			IsActive:    stream.isActive,
			CreatedAt:   stream.createdAt,
		}
	}

	// Unknown stream, return default
	return &StreamInfo{
		StreamID:    streamID,
		MemoryUsage: memoryUsage,
		LastAccess:  time.Now(),
		Priority:    5, // Medium priority
		IsActive:    true,
		CreatedAt:   time.Now(),
	}
}

func containsString(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}
