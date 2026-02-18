package ingestion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/srt"
	"github.com/zsiec/mirror/internal/ingestion/validation"
)

// MockSRTConnection implements the SRTConnection interface for testing
type MockSRTConnection struct {
	data      []byte
	readPos   int
	streamID  string
	maxBW     int64
	closed    bool
	readDelay time.Duration
	stats     srt.ConnectionStats
}

func NewMockSRTConnection(streamID string, data []byte) *MockSRTConnection {
	return &MockSRTConnection{
		streamID: streamID,
		data:     data,
		maxBW:    1000000, // 1 Mbps default
	}
}

func (m *MockSRTConnection) Read(b []byte) (int, error) {
	if m.readDelay > 0 {
		time.Sleep(m.readDelay)
	}

	if m.readPos >= len(m.data) {
		return 0, nil // No more data
	}

	n := copy(b, m.data[m.readPos:])
	m.readPos += n
	m.stats.BytesReceived += int64(n)
	m.stats.PacketsReceived++
	return n, nil
}

func (m *MockSRTConnection) Write(b []byte) (int, error) {
	m.stats.BytesSent += int64(len(b))
	m.stats.PacketsSent++
	return len(b), nil
}

func (m *MockSRTConnection) GetStreamID() string {
	return m.streamID
}

func (m *MockSRTConnection) GetStats() srt.ConnectionStats {
	return m.stats
}

func (m *MockSRTConnection) GetMaxBW() int64 {
	return m.maxBW
}

func (m *MockSRTConnection) SetMaxBW(bw int64) error {
	m.maxBW = bw
	return nil
}

func (m *MockSRTConnection) Close() error {
	m.closed = true
	return nil
}

// Test helper functions

func createValidTSPacket(pid uint16, continuity uint8, hasPayload bool) []byte {
	packet := make([]byte, 188)
	packet[0] = 0x47 // Sync byte
	packet[1] = byte(pid >> 8 & 0x1F)
	packet[2] = byte(pid)

	// Set payload flag
	if hasPayload {
		packet[3] = 0x10 | (continuity & 0x0F)
	} else {
		packet[3] = continuity & 0x0F
	}

	// Fill with dummy payload
	for i := 4; i < 188; i++ {
		packet[i] = byte(i)
	}
	return packet
}

func createPESPacket(streamID byte, length uint16, hasPTS bool) []byte {
	pes := make([]byte, 0, 9+int(length))

	// PES start code
	pes = append(pes, 0x00, 0x00, 0x01, streamID)

	// PES packet length
	pes = append(pes, byte(length>>8), byte(length))

	// PES header flags
	if hasPTS {
		pes = append(pes, 0x80, 0x80, 0x05) // PTS present, header length 5
		// Add PTS (5 bytes)
		pes = append(pes, 0x21, 0x00, 0x01, 0x00, 0x01)

		// Add remaining payload
		for i := 14; i < 9+int(length); i++ {
			pes = append(pes, byte(i))
		}
	} else {
		pes = append(pes, 0x00, 0x00, 0x00) // No PTS
		// Add payload
		for i := 9; i < 9+int(length); i++ {
			pes = append(pes, byte(i))
		}
	}

	return pes
}

// Tests

func TestSRTProtocolBoundary_AlignedPackets(t *testing.T) {
	// Create perfectly aligned MPEG-TS packets
	var data []byte
	for i := 0; i < 10; i++ {
		data = append(data, createValidTSPacket(256, uint8(i), true)...)
	}

	// Test alignment validator directly
	alignmentValidator := validation.NewAlignmentValidator()

	// First validate alignment
	completePackets, remainder, err := alignmentValidator.ValidateMPEGTSAlignment(data)
	assert.NoError(t, err)
	assert.Equal(t, 10, completePackets)
	assert.Nil(t, remainder)

	// Then process with alignment
	alignedData, err := alignmentValidator.ProcessWithAlignment(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), len(alignedData)) // Should be same since perfectly aligned

	// Verify statistics
	stats := alignmentValidator.GetStats()
	assert.Equal(t, int64(10), stats.PacketsProcessed)
	assert.Equal(t, int64(0), stats.PartialPackets)
	assert.Equal(t, int64(0), stats.AlignmentErrors)
}

func TestSRTProtocolBoundary_MisalignedPackets(t *testing.T) {
	// Create misaligned data - partial packet at end
	var data []byte
	for i := 0; i < 5; i++ {
		data = append(data, createValidTSPacket(256, uint8(i), true)...)
	}
	// Add partial packet (100 bytes)
	partial := createValidTSPacket(256, 5, true)
	data = append(data, partial[:100]...)

	// Test alignment validator
	alignmentValidator := validation.NewAlignmentValidator()

	// Process first message with partial
	alignedData1, err := alignmentValidator.ProcessWithAlignment(data)
	assert.NoError(t, err)
	assert.Equal(t, 5*188, len(alignedData1)) // Only complete packets
	assert.True(t, alignmentValidator.HasPartialPacket())

	// Process second message with remainder
	data2 := partial[100:]
	alignedData2, err := alignmentValidator.ProcessWithAlignment(data2)
	assert.NoError(t, err)
	assert.Equal(t, 188, len(alignedData2)) // The completed packet

	// Check alignment was handled correctly
	assert.False(t, alignmentValidator.HasPartialPacket())
}

func TestSRTProtocolBoundary_ContinuityCounter(t *testing.T) {
	// Create packets with continuity counter sequence
	pid := uint16(256)
	continuityValidator := validation.NewContinuityValidator()

	// Normal sequence
	for i := 0; i < 5; i++ {
		err := continuityValidator.ValidateContinuity(pid, uint8(i), true, false)
		assert.NoError(t, err)
	}

	// Skip counter 5 - create discontinuity
	err := continuityValidator.ValidateContinuity(pid, 6, true, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected 5, got 6")

	// Check continuity stats
	contStats := continuityValidator.GetStats()
	assert.Equal(t, int64(1), contStats.TotalDiscontinuities)
	assert.Equal(t, int64(6), contStats.PacketsValidated)
}

func TestSRTProtocolBoundary_PESAssembly(t *testing.T) {
	// Test PES assembly with proper chunking
	pid := uint16(256)
	pesValidator := validation.NewPESValidator(5 * time.Second)

	// Create PES header with 100 byte payload length
	pesHeader := []byte{
		0x00, 0x00, 0x01, 0xE0, // Start code + stream ID
		0x00, 0x64, // PES length = 100
		0x80, 0x80, 0x05, // Flags + header length
		0x21, 0x00, 0x01, 0x00, 0x01, // PTS
	}

	// Start PES packet
	err := pesValidator.ValidatePESStart(pid, pesHeader)
	assert.NoError(t, err)

	// Add payload data in chunks
	// The PES packet expects 100 bytes total after the 6-byte PES header
	// We already gave it 14 bytes (which includes 8 bytes of optional header)
	// So we need: 100 - 8 = 92 more bytes
	err = pesValidator.ValidatePESContinuation(pid, 50)
	assert.NoError(t, err)

	err = pesValidator.ValidatePESContinuation(pid, 42) // 92 - 50 = 42
	assert.NoError(t, err)

	// Check PES state - should be complete
	inProgress, bytes, packets := pesValidator.GetStreamState(pid)
	assert.False(t, inProgress)
	assert.Equal(t, 106, bytes) // 6 (PES header prefix) + 100 (PES length)
	assert.Equal(t, 3, packets) // 1 start + 2 continuations
}

func TestSRTProtocolBoundary_LostSyncByte(t *testing.T) {
	// Create data with lost sync bytes
	var data []byte

	// Good packet
	data = append(data, createValidTSPacket(256, 0, true)...)

	// Corrupt sync byte
	badPacket := createValidTSPacket(256, 1, true)
	badPacket[0] = 0xFF // Corrupt sync byte
	data = append(data, badPacket...)

	// Another good packet
	data = append(data, createValidTSPacket(256, 2, true)...)

	// Test alignment validator's sync recovery
	alignmentValidator := validation.NewAlignmentValidator()

	// Try to validate - should find sync byte issue
	completePackets, remainder, err := alignmentValidator.ValidateMPEGTSAlignment(data)
	assert.Error(t, err)                // Should error on first bad sync byte
	assert.Equal(t, 1, completePackets) // Got first good packet before error

	t.Logf("ValidateMPEGTSAlignment: %d complete packets, remainder: %v, err: %v",
		completePackets, remainder != nil, err)

	// Check stats after ValidateMPEGTSAlignment
	stats1 := alignmentValidator.GetStats()
	t.Logf("After ValidateMPEGTSAlignment: PacketsProcessed=%d, AlignmentErrors=%d",
		stats1.PacketsProcessed, stats1.AlignmentErrors)

	// Process with alignment should recover
	alignedData, err := alignmentValidator.ProcessWithAlignment(data)
	assert.NoError(t, err)

	// Should have recovered and found good packets
	assert.GreaterOrEqual(t, len(alignedData), 188)

	// Check stats
	stats := alignmentValidator.GetStats()
	// ValidateMPEGTSAlignment returns early on sync error before incrementing PacketsProcessed
	assert.Equal(t, int64(0), stats.PacketsProcessed) // No packets were processed due to early return
	assert.Equal(t, int64(1), stats.AlignmentErrors)  // From ValidateMPEGTSAlignment line 44
}

func TestSRTProtocolBoundary_MultipleMessages(t *testing.T) {
	// Simulate multiple SRT messages with various alignment scenarios
	messages := [][]byte{
		// Message 1: 2.5 packets
		append(createValidTSPacket(256, 0, true),
			append(createValidTSPacket(256, 1, true),
				createValidTSPacket(256, 2, true)[:94]...)...),
		// Message 2: Remainder + 1 full packet
		append(createValidTSPacket(256, 2, true)[94:],
			createValidTSPacket(256, 3, true)...),
		// Message 3: Exactly 3 packets
		append(createValidTSPacket(256, 4, true),
			append(createValidTSPacket(256, 5, true),
				createValidTSPacket(256, 6, true)...)...),
	}

	// Test alignment validator across messages
	alignmentValidator := validation.NewAlignmentValidator()
	totalProcessed := 0

	// Process all messages
	for i, msg := range messages {
		t.Logf("Message %d: %d bytes", i, len(msg))
		alignedData, err := alignmentValidator.ProcessWithAlignment(msg)
		assert.NoError(t, err, "Message %d failed", i)

		// Count packets in aligned data
		if alignedData != nil {
			packets := len(alignedData) / 188
			t.Logf("Message %d: Returned %d packets (%d bytes)", i, packets, len(alignedData))
			totalProcessed += packets
		}

		// Check if validator has partial packet
		if alignmentValidator.HasPartialPacket() {
			partial := alignmentValidator.GetPartialPacket()
			t.Logf("Message %d: Has partial packet of %d bytes", i, len(partial))
		}
	}

	// Verify all packets were processed correctly
	// Message 1: ProcessWithAlignment returns 2 complete packets, saves 94 bytes
	// Message 2: 94 bytes (partial) + 282 bytes = 376 bytes = 2 complete packets
	// Message 3: 564 bytes = 3 complete packets
	// Total: 2 + 2 + 3 = 7
	assert.Equal(t, 7, totalProcessed)

	// Note: ProcessWithAlignment doesn't update the internal stats
	// Only ValidateMPEGTSAlignment updates PacketsProcessed and PartialPackets

	// Alternative test: Use ValidateMPEGTSAlignment separately
	validator2 := validation.NewAlignmentValidator()
	totalValidated := 0

	for i, msg := range messages {
		complete, remainder, err := validator2.ValidateMPEGTSAlignment(msg)
		totalValidated += complete
		t.Logf("ValidateMPEGTSAlignment - Message %d: %d bytes, %d complete packets, remainder: %v (len=%d), err: %v",
			i, len(msg), complete, remainder != nil, len(msg)%188, err)

		// Get stats after each message
		tmpStats := validator2.GetStats()
		t.Logf("After message %d: PacketsProcessed=%d, PartialPackets=%d",
			i, tmpStats.PacketsProcessed, tmpStats.PartialPackets)
	}

	// ValidateMPEGTSAlignment doesn't handle cross-message state
	// Message 0: 2 complete packets (470 bytes = 2*188 + 94) - has remainder, partialPackets++
	// Message 1: 0 complete packets (sync error at offset 0) - error returned, partialPackets NOT incremented
	// Message 2: 3 complete packets (564 bytes = 3*188) - no remainder
	assert.Equal(t, 5, totalValidated)

	stats := validator2.GetStats()
	assert.Equal(t, int64(5), stats.PacketsProcessed)
	assert.Equal(t, int64(1), stats.PartialPackets) // Only message 0 increments (message 1 errors out)
}

func TestSRTProtocolBoundary_PESTimeout(t *testing.T) {
	// Test PES timeout directly
	pesValidator := validation.NewPESValidator(100 * time.Millisecond)
	pid := uint16(256)

	// Start a PES packet but don't complete it
	pesHeader := []byte{0x00, 0x00, 0x01, 0xE0, 0x00, 0x64} // 100 byte PES
	err := pesValidator.ValidatePESStart(pid, pesHeader)
	assert.NoError(t, err)

	// Verify it's in progress
	inProgress, _, _ := pesValidator.GetStreamState(pid)
	assert.True(t, inProgress)

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Check for timeouts
	timedOut := pesValidator.CheckTimeouts()
	assert.Len(t, timedOut, 1)
	assert.Equal(t, pid, timedOut[0])

	// Verify it's no longer in progress
	inProgress, _, _ = pesValidator.GetStreamState(pid)
	assert.False(t, inProgress)
}

func TestSRTProtocolBoundary_Diagnostics(t *testing.T) {
	// Test diagnostics directly
	diagnostics := srt.NewDiagnostics("test-stream")

	// Record some messages
	for i := 0; i < 5; i++ {
		diagnostics.RecordMessage(1000+i*200, time.Duration(i+1)*time.Millisecond)
	}

	// Record alignment stats
	diagnostics.RecordAlignmentStats(100, 5, 2)

	// Record PES events
	diagnostics.RecordPESEvent("started")
	diagnostics.RecordPESEvent("completed")

	// Set codec info
	diagnostics.SetCodecInfo("H.264", "AAC", 256, 257)

	// Get snapshot
	diag := diagnostics.GetSnapshot()

	// Verify diagnostics
	assert.Equal(t, "test-stream", diag.StreamID)
	assert.Equal(t, int64(5), diag.MessagesReceived)
	assert.Equal(t, int64(7000), diag.BytesReceived) // 1000+1200+1400+1600+1800
	assert.Equal(t, int64(100), diag.AlignedPackets)
	assert.Equal(t, int64(5), diag.PartialPackets)
	assert.Equal(t, int64(2), diag.AlignmentErrors)
	assert.Equal(t, "H.264", diag.DetectedVideoCodec)
	assert.Equal(t, "AAC", diag.DetectedAudioCodec)
	assert.InDelta(t, 1400.0, diag.AvgMessageSize, 0.1) // 7000/5
}

// Benchmark tests

func BenchmarkSRTProtocolBoundary_AlignedProcessing(b *testing.B) {
	// Create aligned data
	var data []byte
	for i := 0; i < 100; i++ {
		data = append(data, createValidTSPacket(256, uint8(i%16), true)...)
	}

	alignmentValidator := validation.NewAlignmentValidator()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alignmentValidator.ProcessWithAlignment(data)
		alignmentValidator.Reset() // Reset for next iteration
	}
}

func BenchmarkSRTProtocolBoundary_MisalignedProcessing(b *testing.B) {
	// Create misaligned data (not multiple of 188)
	var data []byte
	for i := 0; i < 100; i++ {
		data = append(data, createValidTSPacket(256, uint8(i%16), true)...)
	}
	data = data[:len(data)-50] // Remove 50 bytes to misalign

	alignmentValidator := validation.NewAlignmentValidator()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alignmentValidator.ProcessWithAlignment(data)
		alignmentValidator.Reset() // Reset for next iteration
	}
}
