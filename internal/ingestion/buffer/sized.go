package buffer

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/zsiec/mirror/internal/ingestion/memory"
)

// ProperSizedBuffer is a properly sized buffer implementation with memory sections
type ProperSizedBuffer struct {
	streamID string
	bitrate  int64 // bits per second

	// Single large buffer with sections
	data []byte
	size int

	// Section pointers (relative to start of buffer)
	hotStart  int // Last 2 seconds
	warmStart int // 2-10 seconds
	coldStart int // 10-30 seconds

	// Current positions
	writePos atomic.Int64
	readPos  atomic.Int64

	// Memory management
	memCtrl   *memory.Controller
	allocated int64

	// Synchronization
	mu        sync.RWMutex
	writeCond *sync.Cond
	readCond  *sync.Cond

	// State
	closed atomic.Bool

	// Metrics
	totalWritten atomic.Int64
	totalRead    atomic.Int64
	drops        atomic.Int64
}

// NewProperSizedBuffer creates a new properly sized buffer based on bitrate
func NewProperSizedBuffer(streamID string, bitrate int64, memCtrl *memory.Controller) (*ProperSizedBuffer, error) {
	// Calculate proper size: 30 seconds at bitrate
	totalSeconds := 30
	bytesPerSecond := bitrate / 8
	totalSize := int(bytesPerSecond * int64(totalSeconds))

	// Request memory from controller
	if err := memCtrl.RequestMemory(streamID, int64(totalSize)); err != nil {
		return nil, fmt.Errorf("failed to allocate memory for buffer: %w", err)
	}

	// Allocate single contiguous buffer
	buffer := &ProperSizedBuffer{
		streamID:  streamID,
		bitrate:   bitrate,
		data:      make([]byte, totalSize),
		size:      totalSize,
		hotStart:  0,
		warmStart: int(bytesPerSecond * 2),  // After 2 seconds
		coldStart: int(bytesPerSecond * 10), // After 10 seconds
		memCtrl:   memCtrl,
		allocated: int64(totalSize),
	}

	// Initialize condition variables
	buffer.writeCond = sync.NewCond(&buffer.mu)
	buffer.readCond = sync.NewCond(&buffer.mu)

	return buffer, nil
}

// Write writes data to the buffer
func (buffer *ProperSizedBuffer) Write(data []byte) (int, error) {
	if buffer.closed.Load() {
		return 0, ErrBufferClosed
	}

	dataLen := len(data)

	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	writePos := buffer.writePos.Load()
	readPos := buffer.readPos.Load()

	// Calculate available space
	var available int
	if writePos >= readPos {
		available = buffer.size - int(writePos) + int(readPos)
		if readPos == 0 {
			available-- // Keep one byte to distinguish full from empty
		}
	} else {
		available = int(readPos) - int(writePos) - 1
	}

	if dataLen > available {
		// Update drops metric
		buffer.drops.Add(int64(dataLen))

		pressure := 1.0 - (float64(available) / float64(buffer.size))
		return 0, &ErrBufferFullDetailed{
			StreamID:  buffer.streamID,
			Required:  dataLen,
			Available: available,
			Pressure:  pressure,
			Hint:      fmt.Sprintf("Buffer at %.1f%% capacity", pressure*100),
		}
	}

	// Write data (handle wrap-around)
	if int(writePos)+dataLen <= buffer.size {
		copy(buffer.data[writePos:], data)
	} else {
		firstPart := buffer.size - int(writePos)
		copy(buffer.data[writePos:], data[:firstPart])
		copy(buffer.data[0:], data[firstPart:])
	}

	buffer.writePos.Store((writePos + int64(dataLen)) % int64(buffer.size))
	buffer.totalWritten.Add(int64(dataLen))

	// Signal readers
	buffer.readCond.Broadcast()

	return dataLen, nil
}

// Read reads data from the buffer
func (buffer *ProperSizedBuffer) Read(data []byte) (int, error) {
	if buffer.closed.Load() && buffer.writePos.Load() == buffer.readPos.Load() {
		return 0, ErrBufferClosed
	}

	buffer.mu.Lock()
	defer buffer.mu.Unlock()

	// Wait for data
	for buffer.writePos.Load() == buffer.readPos.Load() && !buffer.closed.Load() {
		buffer.readCond.Wait()
	}

	if buffer.writePos.Load() == buffer.readPos.Load() {
		return 0, ErrBufferClosed
	}

	readPos := buffer.readPos.Load()
	writePos := buffer.writePos.Load()

	// Calculate available data
	var available int
	if writePos > readPos {
		available = int(writePos - readPos)
	} else {
		available = buffer.size - int(readPos) + int(writePos)
	}

	toRead := len(data)
	if toRead > available {
		toRead = available
	}

	// Read data (handle wrap-around)
	if int(readPos)+toRead <= buffer.size {
		copy(data, buffer.data[readPos:readPos+int64(toRead)])
	} else {
		firstPart := buffer.size - int(readPos)
		copy(data[:firstPart], buffer.data[readPos:])
		copy(data[firstPart:], buffer.data[:toRead-firstPart])
	}

	buffer.readPos.Store((readPos + int64(toRead)) % int64(buffer.size))
	buffer.totalRead.Add(int64(toRead))

	// Signal writers
	buffer.writeCond.Broadcast()

	return toRead, nil
}

// GetHotData returns data from the hot section (last 2 seconds)
func (buffer *ProperSizedBuffer) GetHotData() ([]byte, error) {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()

	writePos := buffer.writePos.Load()
	bytesPerSecond := buffer.bitrate / 8
	hotBytes := int64(2 * bytesPerSecond) // 2 seconds of data

	// Calculate start position for hot data
	startPos := writePos - hotBytes
	if startPos < 0 {
		startPos += int64(buffer.size)
	}

	// Check if we have enough data
	totalData := buffer.totalWritten.Load() - buffer.totalRead.Load()
	if totalData < hotBytes {
		hotBytes = totalData
		startPos = buffer.readPos.Load()
	}

	// Extract hot data
	result := make([]byte, hotBytes)
	if startPos+hotBytes <= int64(buffer.size) {
		copy(result, buffer.data[startPos:startPos+hotBytes])
	} else {
		firstPart := int64(buffer.size) - startPos
		copy(result[:firstPart], buffer.data[startPos:])
		copy(result[firstPart:], buffer.data[:hotBytes-firstPart])
	}

	return result, nil
}

// Close closes the buffer and releases memory
func (buffer *ProperSizedBuffer) Close() error {
	if !buffer.closed.CompareAndSwap(false, true) {
		return errors.New("buffer already closed")
	}

	buffer.mu.Lock()
	buffer.readCond.Broadcast()
	buffer.writeCond.Broadcast()
	buffer.mu.Unlock()

	// Release memory
	if buffer.memCtrl != nil {
		buffer.memCtrl.ReleaseMemory(buffer.streamID, buffer.allocated)
	}

	return nil
}

// Stats returns buffer statistics
func (buffer *ProperSizedBuffer) Stats() ProperBufferStats {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()

	writePos := buffer.writePos.Load()
	readPos := buffer.readPos.Load()

	var used int64
	if writePos >= readPos {
		used = writePos - readPos
	} else {
		used = int64(buffer.size) - readPos + writePos
	}

	return ProperBufferStats{
		StreamID:     buffer.streamID,
		Size:         int64(buffer.size),
		Used:         used,
		Available:    int64(buffer.size) - used,
		Pressure:     float64(used) / float64(buffer.size),
		TotalWritten: buffer.totalWritten.Load(),
		TotalRead:    buffer.totalRead.Load(),
		Drops:        buffer.drops.Load(),
		Bitrate:      buffer.bitrate,
	}
}

// GetPreview returns the last N seconds of data from the buffer
func (buffer *ProperSizedBuffer) GetPreview(seconds float64) ([]byte, int) {
	buffer.mu.RLock()
	defer buffer.mu.RUnlock()

	// Calculate how many bytes to read
	bytesPerSecond := buffer.bitrate / 8
	previewBytes := int64(seconds * float64(bytesPerSecond))

	// Get current positions
	writePos := buffer.writePos.Load()
	readPos := buffer.readPos.Load()

	// Calculate available data
	var available int64
	if writePos >= readPos {
		available = writePos - readPos
	} else {
		available = int64(buffer.size) - readPos + writePos
	}

	// Limit preview to available data
	if previewBytes > available {
		previewBytes = available
	}

	// Calculate preview start position (from end of written data)
	previewStartPos := writePos - previewBytes
	if previewStartPos < 0 {
		previewStartPos += int64(buffer.size)
	}

	// If preview would go before read position, adjust
	if writePos >= readPos && previewStartPos < readPos {
		previewStartPos = readPos
		previewBytes = writePos - readPos
	} else if writePos < readPos && previewStartPos < readPos && previewStartPos >= writePos {
		previewStartPos = readPos
		previewBytes = int64(buffer.size) - readPos + writePos
	}

	// Read the preview data
	previewData := make([]byte, previewBytes)
	pos := previewStartPos
	for i := int64(0); i < previewBytes; i++ {
		previewData[i] = buffer.data[pos]
		pos = (pos + 1) % int64(buffer.size)
	}

	// Return data and estimated number of samples (frames)
	// Rough estimate: assume 4KB per frame
	samples := int(previewBytes / 4096)
	return previewData, samples
}

// ProperBufferStats holds statistics for the properly sized buffer
type ProperBufferStats struct {
	StreamID     string
	Size         int64
	Used         int64
	Available    int64
	Pressure     float64
	TotalWritten int64
	TotalRead    int64
	Drops        int64
	Bitrate      int64
}
