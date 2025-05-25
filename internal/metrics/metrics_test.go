package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

func TestUpdateStreamMetrics(t *testing.T) {
	// Test updating stream metrics
	streamID := "test-stream-1"
	protocol := "srt"

	// Initial metrics update
	UpdateStreamMetrics(streamID, protocol, 1024, 100, 5, 5000.0)

	// Verify bytes counter
	bytesCounter := streamBytesTotal.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(1024), testutil.ToFloat64(bytesCounter))

	// Verify packets counter
	packetsCounter := streamPacketsTotal.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(100), testutil.ToFloat64(packetsCounter))

	// Verify lost packets counter
	lostCounter := streamPacketsLostTotal.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(5), testutil.ToFloat64(lostCounter))

	// Verify bitrate gauge
	bitrateGauge := streamBitrate.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(5000), testutil.ToFloat64(bitrateGauge))

	// Update metrics again to test accumulation
	UpdateStreamMetrics(streamID, protocol, 2048, 200, 10, 6000.0)

	// Counters should accumulate
	assert.Equal(t, float64(3072), testutil.ToFloat64(bytesCounter))
	assert.Equal(t, float64(300), testutil.ToFloat64(packetsCounter))
	assert.Equal(t, float64(15), testutil.ToFloat64(lostCounter))

	// Gauge should be set to new value
	assert.Equal(t, float64(6000), testutil.ToFloat64(bitrateGauge))
}

func TestIncrementStreamError(t *testing.T) {
	streamID := "test-stream-2"
	errorType := "decode_error"
	protocol := "rtp"

	// Get initial value
	errorCounter := streamErrorsTotal.WithLabelValues(streamID, errorType, protocol)
	initialValue := testutil.ToFloat64(errorCounter)

	// Increment error
	IncrementStreamError(streamID, errorType, protocol)

	// Verify increment
	assert.Equal(t, initialValue+1, testutil.ToFloat64(errorCounter))

	// Increment multiple times
	IncrementStreamError(streamID, errorType, protocol)
	IncrementStreamError(streamID, errorType, protocol)

	// Verify total increments
	assert.Equal(t, initialValue+3, testutil.ToFloat64(errorCounter))
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
	streamID := "test-stream-3"
	protocol := "srt"

	// Record multiple durations
	durations := []float64{10.5, 30.2, 60.0, 120.5, 300.0}

	for _, duration := range durations {
		RecordConnectionDuration(streamID, protocol, duration)
	}

	// Get histogram
	histogram := connectionDuration.WithLabelValues(streamID, protocol).(prometheus.Histogram)

	// Create a DTO to inspect the histogram
	var dto dto.Metric
	histogram.Write(&dto)

	// Verify count
	assert.Equal(t, uint64(len(durations)), dto.Histogram.GetSampleCount())

	// Verify sum is correct
	expectedSum := 0.0
	for _, d := range durations {
		expectedSum += d
	}
	assert.InDelta(t, expectedSum, dto.Histogram.GetSampleSum(), 0.01)
}

func TestIncrementReconnects(t *testing.T) {
	streamID := "test-stream-4"
	protocol := "rtp"

	// Get counter
	reconnectCounter := connectionReconnectsTotal.WithLabelValues(streamID, protocol)
	initialValue := testutil.ToFloat64(reconnectCounter)

	// Increment reconnects
	IncrementReconnects(streamID, protocol)
	assert.Equal(t, initialValue+1, testutil.ToFloat64(reconnectCounter))

	// Increment multiple times
	for i := 0; i < 5; i++ {
		IncrementReconnects(streamID, protocol)
	}
	assert.Equal(t, initialValue+6, testutil.ToFloat64(reconnectCounter))
}

func TestSetSRTLatency(t *testing.T) {
	streamID := "test-srt-stream"
	latencies := []float64{20.5, 30.0, 25.5, 40.0}

	for _, latency := range latencies {
		SetSRTLatency(streamID, latency)

		gauge := srtLatency.WithLabelValues(streamID)
		assert.Equal(t, latency, testutil.ToFloat64(gauge))
	}
}

func TestSetRTPJitter(t *testing.T) {
	streamID := "test-rtp-stream"
	jitters := []float64{5.5, 10.0, 7.5, 12.0}

	for _, jitter := range jitters {
		SetRTPJitter(streamID, jitter)

		gauge := rtpJitter.WithLabelValues(streamID)
		assert.Equal(t, jitter, testutil.ToFloat64(gauge))
	}
}

func TestSetActiveRTPSessions(t *testing.T) {
	counts := []int{0, 5, 10, 3, 0}

	for _, count := range counts {
		SetActiveRTPSessions(count)
		assert.Equal(t, float64(count), testutil.ToFloat64(rtpSessionsActive))
	}
}

func TestMultipleStreamsMetrics(t *testing.T) {
	// Test that metrics work correctly with multiple streams
	streams := []struct {
		id       string
		protocol string
		bytes    int64
		packets  int64
		lost     int64
		bitrate  float64
	}{
		{"stream-a", "srt", 1000, 100, 2, 5000},
		{"stream-b", "srt", 2000, 200, 5, 6000},
		{"stream-c", "rtp", 3000, 300, 8, 7000},
		{"stream-d", "rtp", 4000, 400, 10, 8000},
	}

	// Update metrics for all streams
	for _, s := range streams {
		UpdateStreamMetrics(s.id, s.protocol, s.bytes, s.packets, s.lost, s.bitrate)
	}

	// Verify each stream has independent metrics
	for _, s := range streams {
		bytesCounter := streamBytesTotal.WithLabelValues(s.id, s.protocol)
		assert.Equal(t, float64(s.bytes), testutil.ToFloat64(bytesCounter))

		packetsCounter := streamPacketsTotal.WithLabelValues(s.id, s.protocol)
		assert.Equal(t, float64(s.packets), testutil.ToFloat64(packetsCounter))

		lostCounter := streamPacketsLostTotal.WithLabelValues(s.id, s.protocol)
		assert.Equal(t, float64(s.lost), testutil.ToFloat64(lostCounter))

		bitrateGauge := streamBitrate.WithLabelValues(s.id, s.protocol)
		assert.Equal(t, s.bitrate, testutil.ToFloat64(bitrateGauge))
	}
}

func TestConcurrentMetricsUpdates(t *testing.T) {
	// Test that metrics are thread-safe
	streamID := "concurrent-stream"
	protocol := "srt"

	// Run concurrent updates
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				UpdateStreamMetrics(streamID, protocol, 10, 1, 0, 1000)
				IncrementStreamError(streamID, "test_error", protocol)
				IncrementReconnects(streamID, protocol)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final values
	bytesCounter := streamBytesTotal.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(10000), testutil.ToFloat64(bytesCounter))

	packetsCounter := streamPacketsTotal.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(1000), testutil.ToFloat64(packetsCounter))

	errorCounter := streamErrorsTotal.WithLabelValues(streamID, "test_error", protocol)
	assert.Equal(t, float64(1000), testutil.ToFloat64(errorCounter))

	reconnectCounter := connectionReconnectsTotal.WithLabelValues(streamID, protocol)
	assert.Equal(t, float64(1000), testutil.ToFloat64(reconnectCounter))
}
