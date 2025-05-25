package buffer

import (
	"fmt"
	"time"
)

// ErrBufferFullDetailed provides detailed information about buffer overflow
type ErrBufferFullDetailed struct {
	StreamID  string
	Required  int
	Available int
	Pressure  float64
	Hint      string
}

func (e *ErrBufferFullDetailed) Error() string {
	return fmt.Sprintf("buffer full for stream %s: need %d bytes, have %d available (pressure: %.2f). %s",
		e.StreamID, e.Required, e.Available, e.Pressure, e.Hint)
}

// ErrPacketTooLarge indicates a packet exceeds the maximum allowed size
type ErrPacketTooLarge struct {
	StreamID   string
	PacketSize int
	MaxSize    int
	Timestamp  time.Time
}

func (e *ErrPacketTooLarge) Error() string {
	return fmt.Sprintf("packet too large for stream %s: %d bytes exceeds max %d bytes at %s",
		e.StreamID, e.PacketSize, e.MaxSize, e.Timestamp.Format(time.RFC3339))
}

// ErrDataLoss indicates data was lost due to buffer constraints
type ErrDataLoss struct {
	StreamID    string
	BytesLost   int64
	Reason      string
	Timestamp   time.Time
	Consecutive int
}

func (e *ErrDataLoss) Error() string {
	return fmt.Sprintf("data loss for stream %s: %d bytes lost due to %s at %s (consecutive: %d)",
		e.StreamID, e.BytesLost, e.Reason, e.Timestamp.Format(time.RFC3339), e.Consecutive)
}

// ErrRateLimited indicates the operation was rate limited
type ErrRateLimited struct {
	StreamID   string
	Limit      float64
	Current    float64
	RetryAfter time.Duration
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("rate limited for stream %s: current rate %.2f exceeds limit %.2f, retry after %v",
		e.StreamID, e.Current, e.Limit, e.RetryAfter)
}
