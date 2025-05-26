package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestIncrementGoroutineCreated(t *testing.T) {
	component := "test_component"

	// Get initial values
	initialCreated := testutil.ToFloat64(goroutinesCreated.WithLabelValues(component))
	initialActive := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))

	IncrementGoroutineCreated(component)
	IncrementGoroutineCreated(component)

	// Check created counter increased
	finalCreated := testutil.ToFloat64(goroutinesCreated.WithLabelValues(component))
	assert.Equal(t, initialCreated+2, finalCreated, "goroutines created should increase by 2")

	// Check active gauge increased
	finalActive := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))
	assert.Equal(t, initialActive+2, finalActive, "active goroutines should increase by 2")
}

func TestIncrementGoroutineDestroyed(t *testing.T) {
	component := "test_component"

	// Create some goroutines first
	IncrementGoroutineCreated(component)
	IncrementGoroutineCreated(component)
	IncrementGoroutineCreated(component)

	activeAfterCreation := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))
	initialDestroyed := testutil.ToFloat64(goroutinesDestroyed.WithLabelValues(component))

	// Destroy one goroutine
	IncrementGoroutineDestroyed(component)

	// Check destroyed counter increased
	finalDestroyed := testutil.ToFloat64(goroutinesDestroyed.WithLabelValues(component))
	assert.Equal(t, initialDestroyed+1, finalDestroyed, "goroutines destroyed should increase by 1")

	// Check active gauge decreased
	finalActive := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))
	assert.Equal(t, activeAfterCreation-1, finalActive, "active goroutines should decrease by 1")
}

func TestRecordLockContention(t *testing.T) {
	component := "test_component"
	lockName := "test_lock"
	duration1 := 0.001
	duration2 := 0.005

	// Record lock contention
	RecordLockContention(component, lockName, duration1)
	RecordLockContention(component, lockName, duration2)

	// For histograms, we can't easily test the exact values without accessing internal metrics
	// But we can verify the function executes without error
	assert.NotPanics(t, func() {
		RecordLockContention(component, lockName, 0.001)
	}, "RecordLockContention should not panic")
}

func TestIncrementMemoryAllocation(t *testing.T) {
	component := "test_component"
	bytes1 := int64(1024)
	bytes2 := int64(2048)

	// Get initial values
	initialAllocations := testutil.ToFloat64(memoryAllocationsTotal.WithLabelValues(component))
	initialBytes := testutil.ToFloat64(memoryAllocatedBytes.WithLabelValues(component))

	IncrementMemoryAllocation(component, bytes1)
	IncrementMemoryAllocation(component, bytes2)

	// Check allocations counter
	finalAllocations := testutil.ToFloat64(memoryAllocationsTotal.WithLabelValues(component))
	assert.Equal(t, initialAllocations+2, finalAllocations, "memory allocations should increase by 2")

	// Check bytes counter
	finalBytes := testutil.ToFloat64(memoryAllocatedBytes.WithLabelValues(component))
	assert.Equal(t, initialBytes+float64(bytes1+bytes2), finalBytes, "allocated bytes should increase by sum")
}

func TestIncrementContextCancellation(t *testing.T) {
	component := "test_component"
	reason := "timeout"

	// Get initial value
	initialCancellations := testutil.ToFloat64(contextCancellations.WithLabelValues(component, reason))

	IncrementContextCancellation(component, reason)
	IncrementContextCancellation(component, reason)

	// Check cancellations counter
	finalCancellations := testutil.ToFloat64(contextCancellations.WithLabelValues(component, reason))
	assert.Equal(t, initialCancellations+2, finalCancellations, "context cancellations should increase by 2")
}

func TestUpdateSRTBytesReceived(t *testing.T) {
	streamID := "srt_test_stream"
	bytes := int64(1024)

	// Get initial value
	initialBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(streamID, "srt"))

	UpdateSRTBytesReceived(streamID, bytes)

	// Check bytes counter
	finalBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(streamID, "srt"))
	assert.Equal(t, initialBytes+float64(bytes), finalBytes, "SRT bytes received should increase")
}

func TestUpdateSRTBytesSent(t *testing.T) {
	streamID := "srt_test_stream_sent"
	bytes := int64(2048)

	// Get initial value
	initialBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(streamID, "srt"))

	UpdateSRTBytesSent(streamID, bytes)

	// Check bytes counter
	finalBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(streamID, "srt"))
	assert.Equal(t, initialBytes+float64(bytes), finalBytes, "SRT bytes sent should increase")
}

func TestIncrementSRTConnections(t *testing.T) {
	// Get initial value
	initialConnections := testutil.ToFloat64(streamsActiveTotal.WithLabelValues("srt"))

	IncrementSRTConnections()
	IncrementSRTConnections()

	// Check connections gauge
	finalConnections := testutil.ToFloat64(streamsActiveTotal.WithLabelValues("srt"))
	assert.Equal(t, initialConnections+2, finalConnections, "SRT connections should increase by 2")
}

func TestDecrementSRTConnections(t *testing.T) {
	// Set up initial connections
	IncrementSRTConnections()
	IncrementSRTConnections()
	IncrementSRTConnections()

	connections := testutil.ToFloat64(streamsActiveTotal.WithLabelValues("srt"))

	DecrementSRTConnections()

	// Check connections gauge
	finalConnections := testutil.ToFloat64(streamsActiveTotal.WithLabelValues("srt"))
	assert.Equal(t, connections-1, finalConnections, "SRT connections should decrease by 1")
}

func TestSRTBytesReceivedAndSent(t *testing.T) {
	streamID := "srt_bidirectional_stream"
	receivedBytes := int64(1000)
	sentBytes := int64(500)

	// Test receiving bytes
	UpdateSRTBytesReceived(streamID, receivedBytes)
	bytesAfterReceive := testutil.ToFloat64(streamBytesTotal.WithLabelValues(streamID, "srt"))

	// Test sending bytes (should add to the same counter)
	UpdateSRTBytesSent(streamID, sentBytes)
	bytesAfterSend := testutil.ToFloat64(streamBytesTotal.WithLabelValues(streamID, "srt"))

	// Total bytes should be sum of received and sent
	assert.Equal(t, float64(receivedBytes), bytesAfterReceive, "received bytes should be tracked")
	assert.Equal(t, float64(receivedBytes+sentBytes), bytesAfterSend, "total bytes should be sum of received and sent")
}

func TestGoroutineLifecycle(t *testing.T) {
	component := "lifecycle_test"

	// Start with clean slate
	initialCreated := testutil.ToFloat64(goroutinesCreated.WithLabelValues(component))
	initialDestroyed := testutil.ToFloat64(goroutinesDestroyed.WithLabelValues(component))
	initialActive := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))

	// Create 3 goroutines
	IncrementGoroutineCreated(component)
	IncrementGoroutineCreated(component)
	IncrementGoroutineCreated(component)

	activeAfterCreation := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))
	assert.Equal(t, initialActive+3, activeAfterCreation, "active count should reflect 3 new goroutines")

	// Destroy 2 goroutines
	IncrementGoroutineDestroyed(component)
	IncrementGoroutineDestroyed(component)

	finalCreated := testutil.ToFloat64(goroutinesCreated.WithLabelValues(component))
	finalDestroyed := testutil.ToFloat64(goroutinesDestroyed.WithLabelValues(component))
	finalActive := testutil.ToFloat64(activeGoroutines.WithLabelValues(component))

	assert.Equal(t, initialCreated+3, finalCreated, "created counter should be 3")
	assert.Equal(t, initialDestroyed+2, finalDestroyed, "destroyed counter should be 2")
	assert.Equal(t, initialActive+1, finalActive, "active count should be 1 (3 created - 2 destroyed)")
}

func TestMemoryAllocationAccumulation(t *testing.T) {
	component := "memory_test"
	allocSizes := []int64{512, 1024, 2048, 4096}

	initialAllocations := testutil.ToFloat64(memoryAllocationsTotal.WithLabelValues(component))
	initialBytes := testutil.ToFloat64(memoryAllocatedBytes.WithLabelValues(component))

	totalBytes := int64(0)
	for _, size := range allocSizes {
		IncrementMemoryAllocation(component, size)
		totalBytes += size
	}

	finalAllocations := testutil.ToFloat64(memoryAllocationsTotal.WithLabelValues(component))
	finalBytes := testutil.ToFloat64(memoryAllocatedBytes.WithLabelValues(component))

	assert.Equal(t, initialAllocations+float64(len(allocSizes)), finalAllocations, "allocations should equal number of calls")
	assert.Equal(t, initialBytes+float64(totalBytes), finalBytes, "bytes should equal sum of all allocations")
}

func TestContextCancellationReasons(t *testing.T) {
	component := "context_test"
	reasons := []string{"timeout", "deadline", "manual", "parent_cancelled"}

	for i, reason := range reasons {
		// Increment each reason multiple times
		for j := 0; j <= i; j++ {
			IncrementContextCancellation(component, reason)
		}
	}

	// Verify each reason has the correct count
	for i, reason := range reasons {
		count := testutil.ToFloat64(contextCancellations.WithLabelValues(component, reason))
		expectedCount := float64(i + 1) // 1, 2, 3, 4
		assert.Equal(t, expectedCount, count, "reason '%s' should have count %v", reason, expectedCount)
	}
}

func TestLockContentionDifferentLocks(t *testing.T) {
	component := "lock_test"
	locks := map[string]float64{
		"mutex_a":    0.001,
		"mutex_b":    0.005,
		"rwmutex_c":  0.010,
		"channel_d":  0.002,
	}

	for lockName, duration := range locks {
		// Record contention twice for each lock
		RecordLockContention(component, lockName, duration)
		RecordLockContention(component, lockName, duration*2)

		// For histograms, just verify no panic occurs
		assert.NotPanics(t, func() {
			RecordLockContention(component, lockName, duration)
		}, "lock contention recording should not panic for lock '%s'", lockName)
	}
}
