package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

func TestUpdateStreamMetrics(t *testing.T) {
	protocol := "srt"

	// Get initial values
	initialBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol))
	initialPackets := testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol))
	initialLost := testutil.ToFloat64(streamPacketsLostTotal.WithLabelValues(protocol))

	// Initial metrics update (streamID is ignored for Prometheus labels)
	UpdateStreamMetrics("test-stream-1", protocol, 1024, 100, 5, 5000.0)

	// Verify bytes counter
	assert.Equal(t, initialBytes+float64(1024), testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol)))

	// Verify packets counter
	assert.Equal(t, initialPackets+float64(100), testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol)))

	// Verify lost packets counter
	assert.Equal(t, initialLost+float64(5), testutil.ToFloat64(streamPacketsLostTotal.WithLabelValues(protocol)))

	// Update metrics again to test accumulation
	UpdateStreamMetrics("test-stream-1", protocol, 2048, 200, 10, 6000.0)

	// Counters should accumulate
	assert.Equal(t, initialBytes+float64(3072), testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol)))
	assert.Equal(t, initialPackets+float64(300), testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol)))
	assert.Equal(t, initialLost+float64(15), testutil.ToFloat64(streamPacketsLostTotal.WithLabelValues(protocol)))
}

func TestIncrementStreamError(t *testing.T) {
	errorType := "decode_error"
	protocol := "rtp"

	// Get initial value
	initialValue := testutil.ToFloat64(streamErrorsTotal.WithLabelValues(errorType, protocol))

	// Increment error (streamID is not used as a label)
	IncrementStreamError("test-stream-2", errorType, protocol)

	// Verify increment
	assert.Equal(t, initialValue+1, testutil.ToFloat64(streamErrorsTotal.WithLabelValues(errorType, protocol)))

	// Increment multiple times
	IncrementStreamError("test-stream-2", errorType, protocol)
	IncrementStreamError("test-stream-2", errorType, protocol)

	// Verify total increments
	assert.Equal(t, initialValue+3, testutil.ToFloat64(streamErrorsTotal.WithLabelValues(errorType, protocol)))
}

func TestSetActiveStreams(t *testing.T) {
	tests := []struct {
		protocol string
		count    int
	}{
		{"srt", 5},
		{"rtp", 3},
		{"srt", 10}, // Update existing
		{"rtp", 0},  // Set to zero
	}

	for _, tt := range tests {
		SetActiveStreams(tt.protocol, tt.count)

		gauge := streamsActiveTotal.WithLabelValues(tt.protocol)
		assert.Equal(t, float64(tt.count), testutil.ToFloat64(gauge))
	}
}

func TestRecordConnectionDuration(t *testing.T) {
	protocol := "srt"

	// Record multiple durations (streamID is ignored for Prometheus labels)
	durations := []float64{10.5, 30.2, 60.0, 120.5, 300.0}

	for _, duration := range durations {
		RecordConnectionDuration("test-stream-3", protocol, duration)
	}

	// Get histogram
	histogram := connectionDuration.WithLabelValues(protocol).(prometheus.Histogram)

	// Create a DTO to inspect the histogram
	var dto dto.Metric
	histogram.Write(&dto)

	// Verify count includes our observations (may include others from parallel tests)
	assert.GreaterOrEqual(t, dto.Histogram.GetSampleCount(), uint64(len(durations)))
}

func TestIncrementReconnects(t *testing.T) {
	protocol := "rtp"

	// Get counter
	initialValue := testutil.ToFloat64(connectionReconnectsTotal.WithLabelValues(protocol))

	// Increment reconnects (streamID is ignored for Prometheus labels)
	IncrementReconnects("test-stream-4", protocol)
	assert.Equal(t, initialValue+1, testutil.ToFloat64(connectionReconnectsTotal.WithLabelValues(protocol)))

	// Increment multiple times
	for i := 0; i < 5; i++ {
		IncrementReconnects("test-stream-4", protocol)
	}
	assert.Equal(t, initialValue+6, testutil.ToFloat64(connectionReconnectsTotal.WithLabelValues(protocol)))
}

func TestSetSRTLatency_NoOp(t *testing.T) {
	// SetSRTLatency is now a no-op; verify it does not panic
	assert.NotPanics(t, func() {
		SetSRTLatency("test-srt-stream", 20.5)
		SetSRTLatency("test-srt-stream", 30.0)
	})
}

func TestSetRTPJitter_NoOp(t *testing.T) {
	// SetRTPJitter is now a no-op; verify it does not panic
	assert.NotPanics(t, func() {
		SetRTPJitter("test-rtp-stream", 5.5)
		SetRTPJitter("test-rtp-stream", 10.0)
	})
}

func TestSetActiveRTPSessions(t *testing.T) {
	counts := []int{0, 5, 10, 3, 0}

	for _, count := range counts {
		SetActiveRTPSessions(count)
		assert.Equal(t, float64(count), testutil.ToFloat64(rtpSessionsActive))
	}
}

func TestMultipleStreamsMetrics_Aggregate(t *testing.T) {
	// Test that metrics from multiple streams are aggregated by protocol
	protocol := "test_agg"

	initialBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol))
	initialPackets := testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol))
	initialLost := testutil.ToFloat64(streamPacketsLostTotal.WithLabelValues(protocol))

	// Update metrics for multiple streams — all aggregate under the same protocol
	UpdateStreamMetrics("stream-a", protocol, 1000, 100, 2, 5000)
	UpdateStreamMetrics("stream-b", protocol, 2000, 200, 5, 6000)
	UpdateStreamMetrics("stream-c", protocol, 3000, 300, 8, 7000)

	// Verify aggregate totals
	assert.Equal(t, initialBytes+float64(6000), testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol)))
	assert.Equal(t, initialPackets+float64(600), testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol)))
	assert.Equal(t, initialLost+float64(15), testutil.ToFloat64(streamPacketsLostTotal.WithLabelValues(protocol)))
}

func TestConcurrentMetricsUpdates(t *testing.T) {
	// Test that metrics are thread-safe
	protocol := "concurrent_test"

	initialBytes := testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol))
	initialPackets := testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol))
	initialErrors := testutil.ToFloat64(streamErrorsTotal.WithLabelValues("test_error", protocol))
	initialReconnects := testutil.ToFloat64(connectionReconnectsTotal.WithLabelValues(protocol))

	// Run concurrent updates
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				UpdateStreamMetrics("concurrent-stream", protocol, 10, 1, 0, 1000)
				IncrementStreamError("concurrent-stream", "test_error", protocol)
				IncrementReconnects("concurrent-stream", protocol)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final values
	assert.Equal(t, initialBytes+float64(10000), testutil.ToFloat64(streamBytesTotal.WithLabelValues(protocol)))
	assert.Equal(t, initialPackets+float64(1000), testutil.ToFloat64(streamPacketsTotal.WithLabelValues(protocol)))
	assert.Equal(t, initialErrors+float64(1000), testutil.ToFloat64(streamErrorsTotal.WithLabelValues("test_error", protocol)))
	assert.Equal(t, initialReconnects+float64(1000), testutil.ToFloat64(connectionReconnectsTotal.WithLabelValues(protocol)))
}
