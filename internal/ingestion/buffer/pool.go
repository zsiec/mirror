package buffer

import (
	"sync"

	"github.com/sirupsen/logrus"
)

// BufferPool manages a pool of ring buffers for streams
type BufferPool struct {
	buffers    sync.Map // streamID -> *RingBuffer
	bufferSize int
	poolSize   int
	logger     *logrus.Logger

	// Pre-allocated buffers
	freeList chan *RingBuffer
	mu       sync.Mutex
}

// NewBufferPool creates a new buffer pool
func NewBufferPool(bufferSize, poolSize int, logger *logrus.Logger) *BufferPool {
	bp := &BufferPool{
		bufferSize: bufferSize,
		poolSize:   poolSize,
		logger:     logger,
		freeList:   make(chan *RingBuffer, poolSize),
	}

	// Pre-allocate some buffers
	for i := 0; i < poolSize/2; i++ {
		bp.freeList <- NewRingBuffer("", bufferSize) // Empty streamID for pool buffers
	}

	return bp
}

// Get returns a buffer for the given stream ID
func (bp *BufferPool) Get(streamID string) *RingBuffer {
	// Fast path: check if buffer already exists
	if buf, ok := bp.buffers.Load(streamID); ok {
		return buf.(*RingBuffer)
	}

	// Slow path: need to create or get a buffer
	// Use mutex to prevent multiple goroutines from creating buffers for the same stream
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Double-check after acquiring lock (another goroutine might have created it)
	if buf, ok := bp.buffers.Load(streamID); ok {
		return buf.(*RingBuffer)
	}

	// Try to get from free list
	var buffer *RingBuffer
	select {
	case buffer = <-bp.freeList:
		buffer.Reset()
		buffer.Reopen()            // Reopen the buffer for reuse
		buffer.streamID = streamID // Update streamID for reused buffer
	default:
		// Create new buffer if none available
		buffer = NewRingBuffer(streamID, bp.bufferSize)
	}

	// Store the buffer - no race condition now due to mutex
	bp.buffers.Store(streamID, buffer)

	bp.logger.WithField("stream_id", streamID).Debug("Buffer allocated")
	return buffer
}

// Put returns a buffer to the pool (called when stream ends)
func (bp *BufferPool) Put(streamID string, buffer *RingBuffer) {
	// Remove from active buffers map
	bp.buffers.Delete(streamID)

	if buffer != nil {
		// Close and reset the buffer to clean state
		buffer.Close()
		buffer.Reset()

		// Clear the stream ID to prepare for reuse
		buffer.streamID = ""

		// Try to return to free list with timeout to prevent blocking
		select {
		case bp.freeList <- buffer:
			bp.logger.WithField("stream_id", streamID).Debug("Buffer returned to pool")
		default:
			// Pool is full, let GC handle it
			bp.logger.WithField("stream_id", streamID).Debug("Buffer pool full, discarding buffer")
		}
	}
}

// Remove removes a buffer from the pool without returning it
func (bp *BufferPool) Remove(streamID string) {
	if buf, ok := bp.buffers.LoadAndDelete(streamID); ok {
		buffer := buf.(*RingBuffer)
		if buffer != nil {
			buffer.Close()
		}
		bp.logger.WithField("stream_id", streamID).Debug("Buffer removed")
	}
}

// Release is an alias for Remove
func (bp *BufferPool) Release(streamID string) {
	bp.Remove(streamID)
}

// Stats returns pool statistics
func (bp *BufferPool) Stats() PoolStats {
	count := 0
	totalSize := int64(0)
	totalWritten := int64(0)
	totalRead := int64(0)
	totalDrops := int64(0)

	bp.buffers.Range(func(key, value interface{}) bool {
		count++
		buffer := value.(*RingBuffer)
		stats := buffer.Stats()
		totalSize += stats.Size
		totalWritten += stats.Written
		totalRead += stats.Read
		totalDrops += stats.Drops
		return true
	})

	return PoolStats{
		ActiveBuffers: count,
		FreeBuffers:   len(bp.freeList),
		TotalSize:     totalSize,
		TotalWritten:  totalWritten,
		TotalRead:     totalRead,
		TotalDrops:    totalDrops,
	}
}

// PoolStats holds buffer pool statistics
type PoolStats struct {
	ActiveBuffers int
	FreeBuffers   int
	TotalSize     int64
	TotalWritten  int64
	TotalRead     int64
	TotalDrops    int64
}
