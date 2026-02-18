package buffer

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrBufferFull   = errors.New("buffer full")
	ErrBufferClosed = errors.New("buffer closed")
	ErrTimeout      = errors.New("operation timeout")
)

// bitrateEntry tracks bytes written at a specific time
type bitrateEntry struct {
	timestamp time.Time
	bytes     int64
}

// RingBuffer is a thread-safe circular buffer implementation
type RingBuffer struct {
	streamID      string
	data          []byte
	size          int64
	maxPacketSize int64
	writePos      int64
	readPos       int64
	written       int64
	read          int64
	closed        int32

	writeCond *sync.Cond
	readCond  *sync.Cond
	mu        sync.Mutex

	// Metrics
	drops            int64
	consecutiveDrops int
	maxLatency       time.Duration
	lastDropTime     time.Time

	// Bitrate tracking
	bitrateWindow   []bitrateEntry
	bitrateIndex    int
	avgBytesPerSec  float64
	lastBitrateCalc time.Time
}

// NewRingBuffer creates a new ring buffer with the specified size
func NewRingBuffer(streamID string, size int) *RingBuffer {
	rb := &RingBuffer{
		streamID:      streamID,
		data:          make([]byte, size),
		size:          int64(size),
		maxPacketSize: 65536,                     // 64KB default max packet size
		bitrateWindow: make([]bitrateEntry, 100), // Track last 100 writes for bitrate
	}
	rb.writeCond = sync.NewCond(&rb.mu)
	rb.readCond = sync.NewCond(&rb.mu)
	return rb
}

// SetMaxPacketSize sets the maximum allowed packet size
func (rb *RingBuffer) SetMaxPacketSize(size int64) {
	rb.mu.Lock()
	rb.maxPacketSize = size
	rb.mu.Unlock()
}

// Write writes data to the buffer. Returns error instead of silently dropping data.
func (rb *RingBuffer) Write(data []byte) (int, error) {
	if atomic.LoadInt32(&rb.closed) == 1 {
		return 0, ErrBufferClosed
	}

	dataLen := int64(len(data))

	if dataLen > rb.maxPacketSize {
		// Record oversized packet metric
		bufferDropsTotal.WithLabelValues(rb.streamID).Inc()
		return 0, &ErrPacketTooLarge{
			StreamID:   rb.streamID,
			PacketSize: int(dataLen),
			MaxSize:    int(rb.maxPacketSize),
			Timestamp:  time.Now(),
		}
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	if dataLen > rb.size {
		return 0, &ErrPacketTooLarge{
			StreamID:   rb.streamID,
			PacketSize: int(dataLen),
			MaxSize:    int(rb.size),
			Timestamp:  time.Now(),
		}
	}

	// Check available space atomically to avoid race conditions
	currentWritten := rb.written
	currentRead := rb.read
	available := rb.size - (currentWritten - currentRead)
	if available < dataLen {
		pressure := float64(currentWritten-currentRead) / float64(rb.size)

		// Track consecutive drops
		now := time.Now()
		if now.Sub(rb.lastDropTime) < time.Second {
			rb.consecutiveDrops++
		} else {
			rb.consecutiveDrops = 1
		}
		rb.lastDropTime = now

		// Update metrics
		atomic.AddInt64(&rb.drops, dataLen)
		bufferDropsTotal.WithLabelValues(rb.streamID).Add(float64(dataLen))

		// Return detailed error instead of dropping silently
		return 0, &ErrBufferFullDetailed{
			StreamID:  rb.streamID,
			Required:  int(dataLen),
			Available: int(available),
			Pressure:  pressure,
			Hint:      fmt.Sprintf("Buffer at %.1f%% capacity, %d consecutive drops", pressure*100, rb.consecutiveDrops),
		}
	}

	// Write data (may wrap around)
	written := int64(0)
	for written < dataLen {
		writeSize := minInt64(dataLen-written, rb.size-rb.writePos)
		copy(rb.data[rb.writePos:rb.writePos+writeSize], data[written:written+writeSize])
		rb.writePos = (rb.writePos + writeSize) % rb.size
		written += writeSize
	}

	rb.written += dataLen
	rb.readCond.Broadcast()

	// Track bitrate
	rb.trackBitrate(dataLen)

	// Update usage metrics
	bufferUsageBytes.WithLabelValues(rb.streamID).Set(float64(rb.written - rb.read))

	return int(dataLen), nil
}

// Read reads up to len(data) bytes from the buffer
func (rb *RingBuffer) Read(data []byte) (int, error) {
	if atomic.LoadInt32(&rb.closed) == 1 && rb.written == rb.read {
		return 0, ErrBufferClosed
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Wait for data
	for rb.written == rb.read && atomic.LoadInt32(&rb.closed) == 0 {
		rb.readCond.Wait()
	}

	if rb.written == rb.read {
		return 0, ErrBufferClosed
	}

	// Read available data
	available := rb.written - rb.read
	toRead := minInt64(int64(len(data)), available)

	read := int64(0)
	for read < toRead {
		readSize := minInt64(toRead-read, rb.size-rb.readPos)
		copy(data[read:read+readSize], rb.data[rb.readPos:rb.readPos+readSize])
		rb.readPos = (rb.readPos + readSize) % rb.size
		read += readSize
	}

	rb.read += read
	rb.writeCond.Broadcast()

	return int(read), nil
}

// ReadTimeout reads with a timeout
func (rb *RingBuffer) ReadTimeout(data []byte, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		return rb.Read(data)
	}

	done := make(chan struct{})
	var n int
	var err error

	go func() {
		n, err = rb.Read(data)
		close(done)
	}()

	select {
	case <-done:
		return n, err
	case <-time.After(timeout):
		return 0, ErrTimeout
	}
}

// Close closes the buffer and wakes up any blocked readers/writers
func (rb *RingBuffer) Close() error {
	if !atomic.CompareAndSwapInt32(&rb.closed, 0, 1) {
		return errors.New("buffer already closed")
	}

	rb.mu.Lock()
	rb.readCond.Broadcast()
	rb.writeCond.Broadcast()
	rb.mu.Unlock()

	return nil
}

// Stats returns buffer statistics
func (rb *RingBuffer) Stats() BufferStats {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	return BufferStats{
		Size:       rb.size,
		Written:    rb.written,
		Read:       rb.read,
		Available:  rb.written - rb.read,
		Drops:      atomic.LoadInt64(&rb.drops),
		MaxLatency: rb.maxLatency,
	}
}

// Reset resets the buffer to empty state
func (rb *RingBuffer) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.writePos = 0
	rb.readPos = 0
	rb.written = 0
	rb.read = 0
	atomic.StoreInt64(&rb.drops, 0)
	// Note: does not reset closed state - use Reopen() for that
}

// Reopen reopens a closed buffer for reuse
func (rb *RingBuffer) Reopen() {
	atomic.StoreInt32(&rb.closed, 0)
}

// BufferStats holds buffer statistics
type BufferStats struct {
	Size       int64
	Written    int64
	Read       int64
	Available  int64
	Drops      int64
	MaxLatency time.Duration
}

// trackBitrate updates the bitrate calculation
func (rb *RingBuffer) trackBitrate(bytes int64) {
	now := time.Now()

	// Add new entry
	rb.bitrateWindow[rb.bitrateIndex] = bitrateEntry{
		timestamp: now,
		bytes:     bytes,
	}
	rb.bitrateIndex = (rb.bitrateIndex + 1) % len(rb.bitrateWindow)

	// Calculate average bitrate every second
	if now.Sub(rb.lastBitrateCalc) >= time.Second {
		rb.calculateAverageBitrate()
		rb.lastBitrateCalc = now
	}
}

// calculateAverageBitrate calculates the average bytes per second
func (rb *RingBuffer) calculateAverageBitrate() {
	now := time.Now()
	cutoff := now.Add(-10 * time.Second) // Look at last 10 seconds

	totalBytes := int64(0)
	oldestTime := now
	newestTime := cutoff

	for _, entry := range rb.bitrateWindow {
		if entry.timestamp.After(cutoff) && !entry.timestamp.IsZero() {
			totalBytes += entry.bytes
			if entry.timestamp.Before(oldestTime) {
				oldestTime = entry.timestamp
			}
			if entry.timestamp.After(newestTime) {
				newestTime = entry.timestamp
			}
		}
	}

	duration := newestTime.Sub(oldestTime).Seconds()
	if duration > 0 {
		rb.avgBytesPerSec = float64(totalBytes) / duration
	}
}

// GetPreview returns the last N seconds of data from the buffer
func (rb *RingBuffer) GetPreview(seconds float64) ([]byte, int) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Use actual bitrate if available, otherwise use default
	bytesPerSecond := rb.avgBytesPerSec
	if bytesPerSecond <= 0 {
		// Default to 50Mbps = 6.25MB/s
		bytesPerSecond = 6250000
	}
	previewBytes := int64(seconds * bytesPerSecond)

	// Limit to available data
	available := rb.written - rb.read
	if previewBytes > available {
		previewBytes = available
	}

	// Limit to buffer size
	if previewBytes > rb.size {
		previewBytes = rb.size
	}

	// Read the preview data
	previewData := make([]byte, previewBytes)

	// Calculate read position for preview (from end of written data)
	previewReadPos := rb.written - previewBytes
	if previewReadPos < rb.read {
		previewReadPos = rb.read
		previewBytes = rb.written - rb.read
	}

	// Calculate actual position in circular buffer
	actualReadPos := (rb.readPos + (previewReadPos - rb.read)) % rb.size

	// Copy data (may wrap around)
	copied := int64(0)
	for copied < previewBytes {
		readSize := minInt64(previewBytes-copied, rb.size-actualReadPos)
		copy(previewData[copied:copied+readSize], rb.data[actualReadPos:actualReadPos+readSize])
		actualReadPos = (actualReadPos + readSize) % rb.size
		copied += readSize
	}

	// Return data and number of samples (approximate based on typical frame size)
	samples := int(previewBytes / 4096) // Rough estimate
	return previewData[:copied], samples
}

// GetBitrate returns the current average bitrate in bytes per second
func (rb *RingBuffer) GetBitrate() float64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.avgBytesPerSec
}

// minInt64 returns the minimum of two int64 values
// Named to avoid conflict with Go 1.21+ built-in min function
func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
