package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// NOTE: Per-stream metrics (keyed by stream_id) are intentionally excluded from
// Prometheus to avoid unbounded cardinality. Each new stream would create new
// time series that are never cleaned up, leading to a "cardinality bomb."
//
// For per-stream operational visibility, use structured logging or a separate
// metrics backend (e.g., an in-memory map exposed via a custom API endpoint)
// that supports stream lifecycle cleanup.

var (
	// Stream ingestion metrics (aggregate, no per-stream breakdown)
	streamsActiveTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "stream_ingestion_active_total",
		Help: "Number of active ingestion streams",
	}, []string{"protocol"})

	streamBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_bytes_total",
		Help: "Total bytes received across all streams",
	}, []string{"protocol"})

	streamPacketsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_packets_total",
		Help: "Total packets received across all streams",
	}, []string{"protocol"})

	streamPacketsLostTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_packets_lost_total",
		Help: "Total packets lost across all streams",
	}, []string{"protocol"})

	streamErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_errors_total",
		Help: "Total errors across all streams",
	}, []string{"error_type", "protocol"})

	// Connection metrics (aggregate)
	connectionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "connection_duration_seconds",
		Help:    "Connection duration in seconds",
		Buckets: prometheus.ExponentialBuckets(1, 2, 15), // 1s to ~16k seconds
	}, []string{"protocol"})

	connectionReconnectsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "connection_reconnects_total",
		Help: "Total number of reconnection attempts",
	}, []string{"protocol"})

	// SRT specific metrics (aggregate, no per-stream breakdown)
	srtPacketsLost = promauto.NewCounter(prometheus.CounterOpts{
		Name: "srt_packets_lost_total",
		Help: "Total SRT packets lost (receiver side)",
	})

	srtPacketsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "srt_packets_dropped_total",
		Help: "Total SRT packets dropped (too late to play)",
	})

	srtPacketsRetransmitted = promauto.NewCounter(prometheus.CounterOpts{
		Name: "srt_packets_retransmitted_total",
		Help: "Total SRT packets retransmitted",
	})

	// RTP specific metrics
	rtpSessionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "rtp_sessions_active_total",
		Help: "Number of active RTP sessions",
	})

	// Debug metrics
	goroutinesCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "debug_goroutines_created_total",
		Help: "Total number of goroutines created",
	}, []string{"component"})

	goroutinesDestroyed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "debug_goroutines_destroyed_total",
		Help: "Total number of goroutines destroyed",
	}, []string{"component"})

	lockContentionSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "debug_lock_contention_seconds",
		Help:    "Lock contention duration in seconds",
		Buckets: prometheus.ExponentialBuckets(0.00001, 10, 8), // 10µs to 1s
	}, []string{"component", "lock_name"})

	memoryAllocationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "debug_memory_allocations_total",
		Help: "Total memory allocations by component",
	}, []string{"component"})

	memoryAllocatedBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "debug_memory_allocated_bytes_total",
		Help: "Total bytes allocated by component",
	}, []string{"component"})

	contextCancellations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "debug_context_cancellations_total",
		Help: "Total context cancellations by reason",
	}, []string{"component", "reason"})

	activeGoroutines = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "debug_goroutines_active",
		Help: "Number of active goroutines",
	}, []string{"component"})
)

// UpdateStreamMetrics updates aggregate metrics for stream ingestion.
// The streamID parameter is accepted for caller convenience but is not used
// as a Prometheus label to avoid unbounded cardinality.
func UpdateStreamMetrics(streamID string, protocol string, bytesReceived, packetsReceived, packetsLost int64, bitrate float64) {
	streamBytesTotal.WithLabelValues(protocol).Add(float64(bytesReceived))
	streamPacketsTotal.WithLabelValues(protocol).Add(float64(packetsReceived))
	streamPacketsLostTotal.WithLabelValues(protocol).Add(float64(packetsLost))
}

// IncrementStreamError increments the error counter for a stream.
// The streamID parameter is accepted for caller convenience but is not used
// as a Prometheus label to avoid unbounded cardinality.
func IncrementStreamError(streamID, errorType, protocol string) {
	streamErrorsTotal.WithLabelValues(errorType, protocol).Inc()
}

// SetActiveStreams sets the number of active streams for a protocol
func SetActiveStreams(protocol string, count int) {
	streamsActiveTotal.WithLabelValues(protocol).Set(float64(count))
}

// RecordConnectionDuration records the duration of a connection.
// The streamID parameter is accepted for caller convenience but is not used
// as a Prometheus label to avoid unbounded cardinality.
func RecordConnectionDuration(streamID, protocol string, duration float64) {
	connectionDuration.WithLabelValues(protocol).Observe(duration)
}

// IncrementReconnects increments the reconnection counter.
// The streamID parameter is accepted for caller convenience but is not used
// as a Prometheus label to avoid unbounded cardinality.
func IncrementReconnects(streamID, protocol string) {
	connectionReconnectsTotal.WithLabelValues(protocol).Inc()
}

// SetSRTLatency is a no-op; per-stream SRT latency is not tracked in Prometheus
// to avoid unbounded cardinality. Use structured logging for per-stream diagnostics.
func SetSRTLatency(streamID string, latencyMs float64) {
	// Per-stream gauge removed to avoid cardinality bomb.
	// Use structured logging for per-stream SRT latency.
}

// SetRTPJitter is a no-op; per-stream RTP jitter is not tracked in Prometheus
// to avoid unbounded cardinality. Use structured logging for per-stream diagnostics.
func SetRTPJitter(streamID string, jitterMs float64) {
	// Per-stream gauge removed to avoid cardinality bomb.
	// Use structured logging for per-stream RTP jitter.
}

// SetActiveRTPSessions sets the number of active RTP sessions
func SetActiveRTPSessions(count int) {
	rtpSessionsActive.Set(float64(count))
}

// Debug metrics functions

// IncrementGoroutineCreated increments the goroutine creation counter
func IncrementGoroutineCreated(component string) {
	goroutinesCreated.WithLabelValues(component).Inc()
	activeGoroutines.WithLabelValues(component).Inc()
}

// IncrementGoroutineDestroyed increments the goroutine destruction counter
func IncrementGoroutineDestroyed(component string) {
	goroutinesDestroyed.WithLabelValues(component).Inc()
	activeGoroutines.WithLabelValues(component).Dec()
}

// RecordLockContention records lock contention duration
func RecordLockContention(component, lockName string, duration float64) {
	lockContentionSeconds.WithLabelValues(component, lockName).Observe(duration)
}

// IncrementMemoryAllocation increments memory allocation counters
func IncrementMemoryAllocation(component string, bytes int64) {
	memoryAllocationsTotal.WithLabelValues(component).Inc()
	memoryAllocatedBytes.WithLabelValues(component).Add(float64(bytes))
}

// IncrementContextCancellation increments context cancellation counter
func IncrementContextCancellation(component, reason string) {
	contextCancellations.WithLabelValues(component, reason).Inc()
}

// UpdateSRTBytesReceived updates the SRT bytes received counter (aggregate)
func UpdateSRTBytesReceived(streamID string, bytes int64) {
	streamBytesTotal.WithLabelValues("srt").Add(float64(bytes))
}

// UpdateSRTBytesSent updates the SRT bytes sent counter (aggregate)
func UpdateSRTBytesSent(streamID string, bytes int64) {
	streamBytesTotal.WithLabelValues("srt_send").Add(float64(bytes))
}

// IncrementSRTConnections increments the SRT connection counter
func IncrementSRTConnections() {
	streamsActiveTotal.WithLabelValues("srt").Inc()
}

// DecrementSRTConnections decrements the SRT connection counter
func DecrementSRTConnections() {
	streamsActiveTotal.WithLabelValues("srt").Dec()
}

// UpdateSRTStats updates aggregate SRT-specific counters.
// The streamID parameter is accepted for caller convenience but is not used
// as a Prometheus label to avoid unbounded cardinality.
// Counters (lost, dropped, retransmitted) expect deltas, not cumulative values.
// Per-stream gauges (RTT, flight size, receive rate, bandwidth, available buffer)
// have been removed to avoid cardinality issues; use structured logging instead.
func UpdateSRTStats(streamID string, rttMs float64, packetsLostDelta, packetsDroppedDelta, packetsRetransDelta int64, flightSize int, receiveRateMbps, bandwidthMbps float64, availRcvBuf int) {
	if packetsLostDelta > 0 {
		srtPacketsLost.Add(float64(packetsLostDelta))
	}
	if packetsDroppedDelta > 0 {
		srtPacketsDropped.Add(float64(packetsDroppedDelta))
	}
	if packetsRetransDelta > 0 {
		srtPacketsRetransmitted.Add(float64(packetsRetransDelta))
	}
	// Per-stream gauges (RTT, flight size, receive rate, bandwidth, rcv buffer)
	// are intentionally not tracked in Prometheus to avoid cardinality bomb.
	// Use structured logging for per-stream SRT diagnostics.
}
