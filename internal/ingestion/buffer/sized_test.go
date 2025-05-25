package buffer

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/memory"
)

func TestProperSizedBuffer_Creation(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 10*1024*1024) // 100MB total, 10MB per stream
	
	// 50Mbps stream = 6.25MB/s * 30s = 187.5MB
	bitrate := int64(50 * 1024 * 1024) // 50 Mbps
	
	// This should fail - exceeds both limits
	_, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.Error(t, err)
	// Could be either error depending on which check happens first
	assert.True(t, errors.Is(err, memory.ErrGlobalMemoryLimit) || errors.Is(err, memory.ErrStreamMemoryLimit))
	
	// Lower bitrate should work: 2Mbps = 250KB/s * 30s = 7.5MB
	bitrate = int64(2 * 1024 * 1024) // 2 Mbps
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	assert.NotNil(t, buffer)
	
	// Check buffer sections
	expectedSize := int(bitrate / 8 * 30) // 30 seconds
	assert.Equal(t, expectedSize, buffer.size)
	assert.Equal(t, int(bitrate/8*2), buffer.warmStart)  // 2 seconds
	assert.Equal(t, int(bitrate/8*10), buffer.coldStart) // 10 seconds
	
	// Cleanup
	buffer.Close()
}

func TestProperSizedBuffer_WriteRead(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 10*1024*1024)
	bitrate := int64(1 * 1024 * 1024) // 1 Mbps = 125KB/s * 30s = 3.75MB
	
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	defer buffer.Close()
	
	// Write test data
	testData := []byte("Hello, World!")
	n, err := buffer.Write(testData)
	require.NoError(t, err)
	assert.Equal(t, len(testData), n)
	
	// Read back
	readData := make([]byte, len(testData))
	n, err = buffer.Read(readData)
	require.NoError(t, err)
	assert.Equal(t, len(testData), n)
	assert.Equal(t, testData, readData)
}

func TestProperSizedBuffer_Overflow(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 10*1024*1024)
	bitrate := int64(8 * 1024) // 8 Kbps = 1KB/s * 30s = 30KB buffer
	
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	defer buffer.Close()
	
	// Fill buffer
	fillData := make([]byte, 30*1024-1) // Leave 1 byte for full/empty distinction
	n, err := buffer.Write(fillData)
	require.NoError(t, err)
	assert.Equal(t, len(fillData), n)
	
	// Try to write more
	moreData := []byte("overflow")
	n, err = buffer.Write(moreData)
	require.Error(t, err)
	assert.Equal(t, 0, n)
	
	var bufferFullErr *ErrBufferFullDetailed
	assert.ErrorAs(t, err, &bufferFullErr)
	assert.Equal(t, "test-stream", bufferFullErr.StreamID)
}

func TestProperSizedBuffer_GetHotData(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 50*1024*1024) // Increase per-stream to 50MB
	bitrate := int64(8 * 1024 * 1024) // 8 Mbps = 1MB/s = 30MB for 30s
	
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	defer buffer.Close()
	
	// Write 3 seconds of data
	secondOfData := make([]byte, 1024*1024) // 1MB = 1 second at 8Mbps
	for i := 0; i < 3; i++ {
		// Fill with different patterns
		for j := range secondOfData {
			secondOfData[j] = byte('A' + i)
		}
		_, err := buffer.Write(secondOfData)
		require.NoError(t, err)
	}
	
	// Get hot data (last 2 seconds)
	hotData, err := buffer.GetHotData()
	require.NoError(t, err)
	assert.Equal(t, 2*1024*1024, len(hotData)) // 2MB
	
	// Verify it's the most recent data
	assert.Equal(t, byte('B'), hotData[0])               // Second 'B'
	assert.Equal(t, byte('C'), hotData[len(hotData)-1]) // Third 'C'
}

func TestProperSizedBuffer_ConcurrentAccess(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 50*1024*1024) // Increase per-stream to 50MB
	bitrate := int64(8 * 1024 * 1024) // 8 Mbps = 30MB for 30s
	
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	defer buffer.Close()
	
	var wg sync.WaitGroup
	var written atomic.Int64
	var read atomic.Int64
	
	// Writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		data := make([]byte, 1024) // 1KB chunks
		for i := 0; i < 1000; i++ {
			if n, err := buffer.Write(data); err == nil {
				written.Add(int64(n))
			}
			time.Sleep(time.Microsecond)
		}
	}()
	
	// Reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		data := make([]byte, 1024)
		for read.Load() < written.Load() || written.Load() == 0 {
			if n, err := buffer.Read(data); err == nil && n > 0 {
				read.Add(int64(n))
			}
			time.Sleep(time.Microsecond)
		}
	}()
	
	wg.Wait()
	
	// Verify all written data was read
	stats := buffer.Stats()
	assert.Equal(t, written.Load(), stats.TotalWritten)
	assert.Equal(t, read.Load(), stats.TotalRead)
}

func TestProperSizedBuffer_Stats(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 10*1024*1024)
	bitrate := int64(1 * 1024 * 1024) // 1 Mbps
	
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	defer buffer.Close()
	
	// Initial stats
	stats := buffer.Stats()
	assert.Equal(t, "test-stream", stats.StreamID)
	assert.Equal(t, int64(3932160), stats.Size) // 30 seconds at 1Mbps
	assert.Equal(t, int64(0), stats.Used)
	assert.Equal(t, stats.Size, stats.Available)
	assert.Equal(t, 0.0, stats.Pressure)
	assert.Equal(t, bitrate, stats.Bitrate)
	
	// Write some data
	data := make([]byte, 1000)
	buffer.Write(data)
	
	stats = buffer.Stats()
	assert.Equal(t, int64(1000), stats.Used)
	assert.Equal(t, int64(1000), stats.TotalWritten)
	assert.Greater(t, stats.Pressure, 0.0)
}

func TestProperSizedBuffer_MemoryRelease(t *testing.T) {
	memCtrl := memory.NewController(100*1024*1024, 10*1024*1024)
	bitrate := int64(1 * 1024 * 1024) // 1 Mbps
	
	// Check initial memory
	initialPressure := memCtrl.GetPressure()
	
	buffer, err := NewProperSizedBuffer("test-stream", bitrate, memCtrl)
	require.NoError(t, err)
	
	// Memory should be allocated
	afterCreatePressure := memCtrl.GetPressure()
	assert.Greater(t, afterCreatePressure, initialPressure)
	
	// Close buffer
	err = buffer.Close()
	require.NoError(t, err)
	
	// Memory should be released
	finalPressure := memCtrl.GetPressure()
	assert.Equal(t, initialPressure, finalPressure)
}
