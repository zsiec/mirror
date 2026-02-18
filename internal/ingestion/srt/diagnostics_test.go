package srt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDiagnostics_RecordMessage(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	// Record some messages
	diag.RecordMessage(1000, 5*time.Millisecond)
	diag.RecordMessage(2000, 10*time.Millisecond)
	diag.RecordMessage(1500, 7*time.Millisecond)

	snapshot := diag.GetSnapshot()

	assert.Equal(t, int64(3), snapshot.MessagesReceived)
	assert.Equal(t, int64(4500), snapshot.BytesReceived)
	assert.InDelta(t, 1500.0, snapshot.AvgMessageSize, 0.1)
	assert.InDelta(t, 7.33, snapshot.AvgProcessingLatency, 0.1)
	assert.Equal(t, int64(10), snapshot.MaxProcessingLatency)
	assert.Equal(t, int64(5), snapshot.MinProcessingLatency)
}

func TestDiagnostics_RecordAlignmentStats(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	// Record alignment stats
	diag.RecordAlignmentStats(100, 5, 2)
	diag.RecordAlignmentStats(50, 3, 1)

	snapshot := diag.GetSnapshot()

	assert.Equal(t, int64(150), snapshot.AlignedPackets)
	assert.Equal(t, int64(8), snapshot.PartialPackets)
	assert.Equal(t, int64(3), snapshot.AlignmentErrors)
	assert.InDelta(t, 1.896, snapshot.AlignmentErrorRate, 0.01) // 3/(150+8) * 100
}

func TestDiagnostics_RecordSyncByte(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	// Record sync bytes
	for i := 0; i < 100; i++ {
		diag.RecordSyncByte(true)
	}
	for i := 0; i < 5; i++ {
		diag.RecordSyncByte(false)
	}

	snapshot := diag.GetSnapshot()

	assert.Equal(t, int64(100), snapshot.SyncBytesFound)
	assert.Equal(t, int64(5), snapshot.SyncBytesLost)
}

func TestDiagnostics_RecordPESEvent(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	// Record various PES events
	diag.RecordPESEvent("started")
	diag.RecordPESEvent("started")
	diag.RecordPESEvent("completed")
	diag.RecordPESEvent("timeout")
	diag.RecordPESEvent("error")
	diag.RecordPESEvent("completed")

	snapshot := diag.GetSnapshot()

	assert.Equal(t, int64(2), snapshot.PESPacketsStarted)
	assert.Equal(t, int64(2), snapshot.PESPacketsCompleted)
	assert.Equal(t, int64(1), snapshot.PESPacketsTimedOut)
	assert.Equal(t, int64(1), snapshot.PESAssemblyErrors)
}

func TestDiagnostics_ContinuityTracking(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	// Record continuity issues
	diag.RecordContinuityError()
	diag.RecordContinuityError()
	diag.RecordDuplicatePacket()

	// Need some packets for rate calculation
	diag.RecordAlignmentStats(100, 0, 0)

	snapshot := diag.GetSnapshot()

	assert.Equal(t, int64(2), snapshot.ContinuityErrors)
	assert.Equal(t, int64(1), snapshot.DuplicatePackets)
	assert.InDelta(t, 2.0, snapshot.ContinuityErrorRate, 0.01) // 2/100 * 100
}

func TestDiagnostics_SetCodecInfo(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	diag.SetCodecInfo("H.264", "AAC", 256, 257)

	snapshot := diag.GetSnapshot()

	assert.Equal(t, "H.264", snapshot.DetectedVideoCodec)
	assert.Equal(t, "AAC", snapshot.DetectedAudioCodec)
	assert.Equal(t, uint16(256), snapshot.VideoPID)
	assert.Equal(t, uint16(257), snapshot.AudioPID)
}

func TestDiagnostics_Rates(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	// Simulate messages over time
	diag.RecordMessage(1000, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	diag.RecordMessage(2000, 5*time.Millisecond)

	snapshot := diag.GetSnapshot()

	// Check rates are calculated
	assert.Greater(t, snapshot.MessageRate, float64(0))
	assert.Greater(t, snapshot.DataRate, float64(0))
	assert.Greater(t, snapshot.Uptime, time.Duration(0))
	assert.Less(t, snapshot.TimeSinceLastMessage, 50*time.Millisecond)
}

func TestDiagnostics_EmptyStats(t *testing.T) {
	diag := NewDiagnostics("test-stream")

	snapshot := diag.GetSnapshot()

	// Verify initial state
	assert.Equal(t, "test-stream", snapshot.StreamID)
	assert.Equal(t, int64(0), snapshot.MessagesReceived)
	assert.Equal(t, float64(0), snapshot.MessageRate)
	assert.Equal(t, float64(0), snapshot.AlignmentErrorRate)
	assert.Equal(t, float64(0), snapshot.ContinuityErrorRate)
	assert.Equal(t, int64(^uint64(0)>>1), snapshot.MinProcessingLatency) // Max int64
}
