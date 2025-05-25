package registry

import (
	"fmt"
	"sync"
	"time"
)

// StreamType represents the type of stream protocol
type StreamType string

const (
	StreamTypeSRT StreamType = "srt"
	StreamTypeRTP StreamType = "rtp"
)

// StreamStatus represents the current status of a stream
type StreamStatus string

const (
	StatusConnecting StreamStatus = "connecting"
	StatusActive     StreamStatus = "active"
	StatusPaused     StreamStatus = "paused"
	StatusError      StreamStatus = "error"
	StatusClosed     StreamStatus = "closed"
)

// Stream represents an active streaming session
type Stream struct {
	ID            string       `json:"id"`
	Type          StreamType   `json:"type"`
	SourceAddr    string       `json:"source_addr"`
	Status        StreamStatus `json:"status"`
	CreatedAt     time.Time    `json:"created_at"`
	LastHeartbeat time.Time    `json:"last_heartbeat"`

	// Stream metadata
	VideoCodec string  `json:"video_codec"` // HEVC
	Resolution string  `json:"resolution"`  // 1920x1080
	Bitrate    int64   `json:"bitrate"`     // bits per second
	FrameRate  float64 `json:"frame_rate"`

	// Statistics
	BytesReceived   int64 `json:"bytes_received"`
	PacketsReceived int64 `json:"packets_received"`
	PacketsLost     int64 `json:"packets_lost"`

	// Internal fields (not serialized)
	buffer interface{}  `json:"-"` // Will be *RingBuffer
	mu     sync.RWMutex `json:"-"`
}

// StreamStats holds stream statistics
type StreamStats struct {
	BytesReceived   int64
	PacketsReceived int64
	PacketsLost     int64
	Bitrate         int64
}

// GenerateStreamID creates a unique stream ID with a readable format
func GenerateStreamID(streamType StreamType, sourceAddr string) string {
	// Format: type_date_time_counter
	// Example: srt_20240115_143052_001
	now := time.Now()
	counter := getNextCounter()
	return fmt.Sprintf("%s_%s_%03d", streamType, now.Format("20060102_150405"), counter)
}

var (
	streamCounter uint64
	counterMu     sync.Mutex
)

func getNextCounter() uint64 {
	counterMu.Lock()
	defer counterMu.Unlock()
	streamCounter++
	if streamCounter > 999 {
		streamCounter = 1
	}
	return streamCounter
}

// UpdateStats updates the stream statistics
func (s *Stream) UpdateStats(stats *StreamStats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.BytesReceived = stats.BytesReceived
	s.PacketsReceived = stats.PacketsReceived
	s.PacketsLost = stats.PacketsLost
	s.Bitrate = stats.Bitrate
}

// UpdateHeartbeat updates the last heartbeat time
func (s *Stream) UpdateHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastHeartbeat = time.Now()
}

// SetStatus updates the stream status
func (s *Stream) SetStatus(status StreamStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

// GetStatus returns the current stream status
func (s *Stream) GetStatus() StreamStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status
}

// IsActive checks if the stream is in an active state
func (s *Stream) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Status == StatusActive || s.Status == StatusConnecting
}
