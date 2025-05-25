package buffer

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingBuffer_WriteRead(t *testing.T) {
	tests := []struct {
		name       string
		bufferSize int
		writeData  [][]byte
		readSize   int
		expected   []byte
	}{
		{
			name:       "simple write and read",
			bufferSize: 1024,
			writeData:  [][]byte{[]byte("hello world")},
			readSize:   11,
			expected:   []byte("hello world"),
		},
		{
			name:       "multiple writes",
			bufferSize: 1024,
			writeData:  [][]byte{[]byte("hello "), []byte("world")},
			readSize:   11,
			expected:   []byte("hello world"),
		},
		{
			name:       "partial read",
			bufferSize: 1024,
			writeData:  [][]byte{[]byte("hello world")},
			readSize:   5,
			expected:   []byte("hello"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := NewRingBuffer("test-stream", tt.bufferSize)

			// Write data
			for _, data := range tt.writeData {
				n, err := rb.Write(data)
				require.NoError(t, err)
				assert.Equal(t, len(data), n)
			}

			// Read data
			buf := make([]byte, tt.readSize)
			n, err := rb.Read(buf)
			require.NoError(t, err)
			assert.Equal(t, len(tt.expected), n)
			assert.Equal(t, tt.expected, buf[:n])
		})
	}
}

func TestRingBuffer_Wraparound(t *testing.T) {
	rb := NewRingBuffer("test-stream", 10)

	// Write enough to wrap around
	data1 := []byte("12345678")
	n, err := rb.Write(data1)
	require.NoError(t, err)
	assert.Equal(t, 8, n)

	// Read partial
	buf := make([]byte, 5)
	n, err = rb.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, []byte("12345"), buf)

	// Write more (should wrap)
	data2 := []byte("abcdef")
	n, err = rb.Write(data2)
	require.NoError(t, err)
	assert.Equal(t, 6, n)

	// Read all remaining
	buf = make([]byte, 20)
	n, err = rb.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 9, n)
	assert.Equal(t, []byte("678abcdef"), buf[:n])
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer("test-stream", 10)

	// Fill buffer
	data1 := []byte("1234567890")
	n, err := rb.Write(data1)
	require.NoError(t, err)
	assert.Equal(t, 10, n)

	// Write more (should return error now instead of dropping)
	data2 := []byte("abcd")
	n, err = rb.Write(data2)
	require.Error(t, err)
	assert.Equal(t, 0, n)

	// Check error type
	var bufferFullErr *ErrBufferFullDetailed
	assert.ErrorAs(t, err, &bufferFullErr)
	assert.Equal(t, "test-stream", bufferFullErr.StreamID)
	assert.Equal(t, 4, bufferFullErr.Required)
	assert.Equal(t, 0, bufferFullErr.Available)
	assert.Equal(t, 1.0, bufferFullErr.Pressure)

	// Check drops metric was updated
	stats := rb.Stats()
	assert.Equal(t, int64(4), stats.Drops) // Should count the attempted bytes

	// Read should get the original data (nothing was overwritten)
	buf := make([]byte, 20)
	n, err = rb.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, []byte("1234567890"), buf[:n])
}

func TestRingBuffer_PacketTooLarge(t *testing.T) {
	rb := NewRingBuffer("test-stream", 1024)
	rb.SetMaxPacketSize(100) // Set max packet size to 100 bytes

	// Try to write packet larger than max
	largeData := make([]byte, 200)
	n, err := rb.Write(largeData)
	require.Error(t, err)
	assert.Equal(t, 0, n)

	// Check error type
	var pktErr *ErrPacketTooLarge
	assert.ErrorAs(t, err, &pktErr)
	assert.Equal(t, "test-stream", pktErr.StreamID)
	assert.Equal(t, 200, pktErr.PacketSize)
	assert.Equal(t, 100, pktErr.MaxSize)

	// Normal sized packet should work
	normalData := []byte("normal")
	n, err = rb.Write(normalData)
	require.NoError(t, err)
	assert.Equal(t, 6, n)
}

func TestRingBuffer_ConcurrentAccess(t *testing.T) {
	rb := NewRingBuffer("test-stream", 1024)
	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			data := bytes.Repeat([]byte{byte(i)}, 10)
			_, err := rb.Write(data)
			assert.NoError(t, err)
			time.Sleep(time.Microsecond)
		}
	}()

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 10)
		totalRead := 0
		for totalRead < 1000 {
			n, err := rb.Read(buf)
			if err == nil {
				totalRead += n
			}
			time.Sleep(time.Microsecond)
		}
	}()

	wg.Wait()
}

func TestRingBuffer_Close(t *testing.T) {
	rb := NewRingBuffer("test-stream", 1024)

	// Write some data
	_, err := rb.Write([]byte("test"))
	require.NoError(t, err)

	// Close buffer
	err = rb.Close()
	require.NoError(t, err)

	// Write should fail
	_, err = rb.Write([]byte("fail"))
	assert.ErrorIs(t, err, ErrBufferClosed)

	// Read remaining data should work
	buf := make([]byte, 10)
	n, err := rb.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 4, n)

	// Further reads should fail
	_, err = rb.Read(buf)
	assert.ErrorIs(t, err, ErrBufferClosed)
}

func TestRingBuffer_ReadTimeout(t *testing.T) {
	rb := NewRingBuffer("test-stream", 1024)
	buf := make([]byte, 10)

	// Read with timeout should timeout
	start := time.Now()
	n, err := rb.ReadTimeout(buf, 100*time.Millisecond)
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, ErrTimeout)
	assert.Equal(t, 0, n)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.Less(t, elapsed, 200*time.Millisecond)
}

func TestRingBuffer_Stats(t *testing.T) {
	rb := NewRingBuffer("test-stream", 1024)

	// Initial stats
	stats := rb.Stats()
	assert.Equal(t, int64(1024), stats.Size)
	assert.Equal(t, int64(0), stats.Written)
	assert.Equal(t, int64(0), stats.Read)
	assert.Equal(t, int64(0), stats.Available)
	assert.Equal(t, int64(0), stats.Drops)

	// Write and check stats
	rb.Write([]byte("hello"))
	stats = rb.Stats()
	assert.Equal(t, int64(5), stats.Written)
	assert.Equal(t, int64(5), stats.Available)

	// Read and check stats
	buf := make([]byte, 3)
	rb.Read(buf)
	stats = rb.Stats()
	assert.Equal(t, int64(5), stats.Written)
	assert.Equal(t, int64(3), stats.Read)
	assert.Equal(t, int64(2), stats.Available)
}

func TestRingBuffer_GetPreview(t *testing.T) {
	tests := []struct {
		name       string
		bufferSize int
		writeData  [][]byte
		writeDelay time.Duration
		seconds    int
		wantMinLen int
		wantMaxLen int
	}{
		{
			name:       "get last 1 second",
			bufferSize: 1024,
			writeData:  [][]byte{[]byte("old data"), []byte("recent data")},
			writeDelay: 500 * time.Millisecond,
			seconds:    1,
			wantMinLen: 11, // "recent data"
			wantMaxLen: 19, // both strings
		},
		{
			name:       "get all data when time exceeds buffer age",
			bufferSize: 1024,
			writeData:  [][]byte{[]byte("data1"), []byte("data2"), []byte("data3")},
			writeDelay: 0,
			seconds:    60,
			wantMinLen: 15, // all data
			wantMaxLen: 15,
		},
		{
			name:       "empty buffer",
			bufferSize: 1024,
			writeData:  [][]byte{},
			writeDelay: 0,
			seconds:    10,
			wantMinLen: 0,
			wantMaxLen: 0,
		},
		{
			name:       "partial time window",
			bufferSize: 1024,
			writeData:  [][]byte{[]byte("first"), []byte("second"), []byte("third")},
			writeDelay: 200 * time.Millisecond,
			seconds:    1,
			wantMinLen: 5,  // at least "third"
			wantMaxLen: 16, // all data
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := NewRingBuffer("test-stream", tt.bufferSize)

			// Write data with delays
			for i, data := range tt.writeData {
				if i > 0 && tt.writeDelay > 0 {
					time.Sleep(tt.writeDelay)
				}
				_, err := rb.Write(data)
				require.NoError(t, err)
			}

			// Get preview
			preview, _ := rb.GetPreview(float64(tt.seconds))

			// Check length is within expected range
			assert.GreaterOrEqual(t, len(preview), tt.wantMinLen)
			assert.LessOrEqual(t, len(preview), tt.wantMaxLen)

			// If we have data, just verify we got some preview data
			if len(tt.writeData) > 0 && tt.wantMinLen > 0 {
				assert.Greater(t, len(preview), 0, "Expected to get preview data")
			}
		})
	}
}

func TestRingBuffer_GetPreview_WithWraparound(t *testing.T) {
	rb := NewRingBuffer("test-stream", 50) // Buffer large enough for all data

	// Write data to fill buffer
	for i := 0; i < 5; i++ {
		data := bytes.Repeat([]byte{byte('A' + i)}, 10)
		n, err := rb.Write(data)
		require.NoError(t, err)
		assert.Equal(t, 10, n)

		// Read some data to make room for next write (simulating consumption)
		if i < 4 {
			readBuf := make([]byte, 10)
			_, err = rb.Read(readBuf)
			require.NoError(t, err)
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Get preview of last second
	preview, _ := rb.GetPreview(1.0)

	// Should have recent data
	assert.Greater(t, len(preview), 0)
}
