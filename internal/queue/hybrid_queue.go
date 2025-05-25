package queue

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

var (
	// ErrRateLimited indicates the operation was rate limited
	ErrRateLimited = errors.New("rate limited")

	// ErrQueueClosed indicates the queue is closed
	ErrQueueClosed = errors.New("queue closed")

	// ErrCorruptedData indicates corrupted data in disk file
	ErrCorruptedData = errors.New("corrupted data in disk file")
)

// HybridQueue provides in-memory queue with disk overflow capability
type HybridQueue struct {
	streamID string

	// In-memory channel queue
	memQueue chan []byte
	memSize  int

	// Disk overflow
	diskPath   string
	diskFile   *os.File
	diskWriter *bufio.Writer
	diskReader *bufio.Reader
	diskMu     sync.Mutex

	// Read tracking
	readFile   *os.File
	readOffset int64

	// Metrics
	depth     atomic.Int64
	diskBytes atomic.Int64
	memCount  atomic.Int64

	// Flow control
	rateLimiter *rate.Limiter

	// State
	closed  atomic.Bool
	closeCh chan struct{}

	// Wait group for background tasks
	wg sync.WaitGroup
}

// NewHybridQueue creates a new hybrid queue with memory and disk storage
func NewHybridQueue(streamID string, memSize int, diskPath string) (*HybridQueue, error) {
	// Ensure disk path exists
	if err := os.MkdirAll(diskPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create disk path: %w", err)
	}

	// Create disk file for overflow
	filename := filepath.Join(diskPath, streamID+".overflow")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create overflow file: %w", err)
	}

	// Create read file handle
	readFile, err := os.OpenFile(filename, os.O_RDONLY, 0644)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to open read handle: %w", err)
	}

	q := &HybridQueue{
		streamID:    streamID,
		memQueue:    make(chan []byte, memSize),
		memSize:     memSize,
		diskPath:    diskPath,
		diskFile:    file,
		diskWriter:  bufio.NewWriterSize(file, 64*1024), // 64KB buffer
		readFile:    readFile,
		diskReader:  bufio.NewReaderSize(readFile, 64*1024),
		rateLimiter: rate.NewLimiter(rate.Limit(10000), 1000), // 10k ops/sec, burst 1000
		closeCh:     make(chan struct{}),
	}

	// Start background disk reader goroutine
	// This goroutine will be properly cleaned up in Close() via:
	// 1. Signaling via closeCh
	// 2. Waiting via q.wg.Wait()
	q.wg.Add(1)
	go q.diskToMemoryPump()

	return q, nil
}

// Enqueue adds data to the queue
func (q *HybridQueue) Enqueue(data []byte) error {
	if q.closed.Load() {
		return ErrQueueClosed
	}

	// Check rate limit
	if !q.rateLimiter.Allow() {
		return ErrRateLimited
	}

	// Make a copy to avoid data races
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	// Try memory queue first (non-blocking)
	select {
	case q.memQueue <- dataCopy:
		q.depth.Add(1)
		q.memCount.Add(1)
		return nil
	default:
		// Memory full, write to disk
		return q.writeToDisk(dataCopy)
	}
}

// writeToDisk writes data to disk overflow
func (q *HybridQueue) writeToDisk(data []byte) error {
	q.diskMu.Lock()
	defer q.diskMu.Unlock()

	// Write length-prefixed message
	length := uint32(len(data))
	if err := binary.Write(q.diskWriter, binary.BigEndian, length); err != nil {
		return fmt.Errorf("failed to write length: %w", err)
	}

	if _, err := q.diskWriter.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	q.depth.Add(1)
	q.diskBytes.Add(int64(len(data) + 4))

	// Flush always for now to ensure data is available for reading
	if err := q.diskWriter.Flush(); err != nil {
		return fmt.Errorf("failed to flush: %w", err)
	}

	return nil
}

// Dequeue removes and returns data from the queue
func (q *HybridQueue) Dequeue() ([]byte, error) {
	return q.DequeueTimeout(0) // Block indefinitely
}

// DequeueTimeout removes and returns data from the queue with timeout
func (q *HybridQueue) DequeueTimeout(timeout time.Duration) ([]byte, error) {
	if q.closed.Load() && q.depth.Load() == 0 {
		return nil, ErrQueueClosed
	}

	// Create timeout channel if needed
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	// Try memory queue with timeout
	select {
	case data := <-q.memQueue:
		q.depth.Add(-1)
		q.memCount.Add(-1)
		return data, nil
	case <-timeoutCh:
		// Before timing out, check if there's disk data we can read directly
		if q.HasDiskData() && q.memCount.Load() == 0 {
			// Try to read directly from disk
			data, err := q.readFromDisk()
			if err == nil {
				q.depth.Add(-1) // Adjust depth since readFromDisk doesn't
				return data, nil
			}
		}
		return nil, errors.New("dequeue timeout")
	case <-q.closeCh:
		// Check one more time after close signal
		select {
		case data := <-q.memQueue:
			q.depth.Add(-1)
			q.memCount.Add(-1)
			return data, nil
		default:
			if q.depth.Load() == 0 {
				return nil, ErrQueueClosed
			}
			return nil, errors.New("queue closing")
		}
	}
}

// TryDequeue attempts to dequeue without blocking
func (q *HybridQueue) TryDequeue() ([]byte, error) {
	if q.closed.Load() && q.depth.Load() == 0 {
		return nil, ErrQueueClosed
	}

	select {
	case data := <-q.memQueue:
		q.depth.Add(-1)
		q.memCount.Add(-1)
		return data, nil
	default:
		if q.depth.Load() == 0 {
			return nil, errors.New("queue empty")
		}
		return nil, errors.New("no data available in memory")
	}
}

// diskToMemoryPump pumps data from disk to memory queue
// diskToMemoryPump is a background goroutine that moves data from disk to memory
// when space becomes available. It properly handles shutdown via closeCh.
func (q *HybridQueue) diskToMemoryPump() {
	defer q.wg.Done()

	for {
		select {
		case <-q.closeCh:
			return
		default:
		}

		// Check if we should pump data
		if q.memCount.Load() >= int64(q.memSize) || !q.HasDiskData() {
			// Memory full or no disk data, wait
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-q.closeCh:
				timer.Stop()
				return
			case <-timer.C:
				continue
			}
		}

		// Try to read from disk
		data, err := q.readFromDisk()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// End of current disk data, wait for more
				timer := time.NewTimer(50 * time.Millisecond)
				select {
				case <-q.closeCh:
					timer.Stop()
					return
				case <-timer.C:
					continue
				}
			}
			// Other error, wait and retry
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// Try to put in memory queue (blocking)
		select {
		case q.memQueue <- data:
			q.memCount.Add(1)
			// Successfully delivered to memory
		case <-q.closeCh:
			// Queue closing, data was read but not delivered
			// Don't need to adjust counters since depth wasn't decremented in readFromDisk
			return
		}
	}
}

// readFromDisk reads one message from disk
func (q *HybridQueue) readFromDisk() ([]byte, error) {
	q.diskMu.Lock()
	defer q.diskMu.Unlock()

	// Check if we've caught up with writes
	fileInfo, err := q.diskFile.Stat()
	if err != nil {
		return nil, err
	}

	if q.readOffset >= fileInfo.Size() {
		// No more data to read
		return nil, io.EOF
	}

	// Ensure we're at the right position
	if _, err := q.readFile.Seek(q.readOffset, 0); err != nil {
		return nil, err
	}

	// Read length prefix
	var length uint32
	if err := binary.Read(q.readFile, binary.BigEndian, &length); err != nil {
		if err == io.EOF {
			// Not enough data yet
			return nil, io.EOF
		}
		return nil, err
	}

	// Sanity check
	if length > 10*1024*1024 { // 10MB max message size
		return nil, ErrCorruptedData
	}

	// Check if full message is available
	if q.readOffset+4+int64(length) > fileInfo.Size() {
		// Full message not written yet
		return nil, io.EOF
	}

	// Read data
	data := make([]byte, length)
	if _, err := io.ReadFull(q.readFile, data); err != nil {
		return nil, err
	}

	q.readOffset += int64(4 + length)
	q.diskBytes.Add(-int64(4 + length))
	// Don't adjust depth here - it will be adjusted when dequeued from memory

	return data, nil
}

// HasDiskData returns true if there's data on disk
func (q *HybridQueue) HasDiskData() bool {
	return q.diskBytes.Load() > 0
}

// GetDepth returns the total queue depth
func (q *HybridQueue) GetDepth() int64 {
	return q.depth.Load()
}

// GetPressure returns queue pressure (0.0 to 1.0+)
func (q *HybridQueue) GetPressure() float64 {
	memPressure := float64(q.memCount.Load()) / float64(q.memSize)
	// Add disk pressure (>1.0 indicates overflow to disk)
	if q.HasDiskData() {
		diskPressure := float64(q.diskBytes.Load()) / float64(1024*1024*100) // 100MB as reference
		return memPressure + diskPressure
	}
	return memPressure
}

// Close closes the queue and cleans up resources
func (q *HybridQueue) Close() error {
	if !q.closed.CompareAndSwap(false, true) {
		return errors.New("queue already closed")
	}

	// Signal close
	close(q.closeCh)

	// Wait for background tasks
	q.wg.Wait()

	// Flush any remaining disk data
	q.diskMu.Lock()
	if q.diskWriter != nil {
		q.diskWriter.Flush()
	}
	q.diskMu.Unlock()

	// Close files
	var errs []error
	if q.diskFile != nil {
		if err := q.diskFile.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close write file: %w", err))
		}
	}

	if q.readFile != nil {
		if err := q.readFile.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close read file: %w", err))
		}
	}

	// Remove overflow file
	filename := filepath.Join(q.diskPath, q.streamID+".overflow")
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed to remove overflow file: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}

	return nil
}

// Stats returns queue statistics
func (q *HybridQueue) Stats() QueueStats {
	return QueueStats{
		StreamID:    q.streamID,
		Depth:       q.depth.Load(),
		MemoryItems: q.memCount.Load(),
		DiskBytes:   q.diskBytes.Load(),
		Pressure:    q.GetPressure(),
	}
}

// QueueStats holds queue statistics
type QueueStats struct {
	StreamID    string
	Depth       int64
	MemoryItems int64
	DiskBytes   int64
	Pressure    float64
}
