package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Stream ingestion metrics
	streamsActiveTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "stream_ingestion_active_total",
		Help: "Number of active ingestion streams",
	}, []string{"protocol"})

	streamBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_bytes_total",
		Help: "Total bytes received per stream",
	}, []string{"stream_id", "protocol"})

	streamPacketsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_packets_total",
		Help: "Total packets received per stream",
	}, []string{"stream_id", "protocol"})

	streamPacketsLostTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_packets_lost_total",
		Help: "Total packets lost per stream",
	}, []string{"stream_id", "protocol"})

	streamErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "stream_ingestion_errors_total",
		Help: "Total errors per stream",
	}, []string{"stream_id", "error_type", "protocol"})

	streamBitrate = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "stream_ingestion_bitrate",
		Help: "Current bitrate in bits per second",
	}, []string{"stream_id", "protocol"})

	// Connection metrics
	connectionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "connection_duration_seconds",
		Help:    "Connection duration in seconds",
		Buckets: prometheus.ExponentialBuckets(1, 2, 15), // 1s to ~16k seconds
	}, []string{"stream_id", "protocol"})

	connectionReconnectsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "connection_reconnects_total",
		Help: "Total number of reconnection attempts",
	}, []string{"stream_id", "protocol"})

	// SRT specific metrics
	srtLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "srt_latency_milliseconds",
		Help: "SRT connection latency in milliseconds",
	}, []string{"stream_id"})

	// RTP specific metrics
	rtpJitter = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "rtp_jitter_milliseconds",
		Help: "RTP stream jitter in milliseconds",
	}, []string{"stream_id"})

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

// UpdateStreamMetrics updates metrics for a stream
func UpdateStreamMetrics(streamID string, protocol string, bytesReceived, packetsReceived, packetsLost int64, bitrate float64) {
	streamBytesTotal.WithLabelValues(streamID, protocol).Add(float64(bytesReceived))
	streamPacketsTotal.WithLabelValues(streamID, protocol).Add(float64(packetsReceived))
	streamPacketsLostTotal.WithLabelValues(streamID, protocol).Add(float64(packetsLost))
	streamBitrate.WithLabelValues(streamID, protocol).Set(bitrate)
}

// IncrementStreamError increments the error counter for a stream
func IncrementStreamError(streamID, errorType, protocol string) {
	streamErrorsTotal.WithLabelValues(streamID, errorType, protocol).Inc()
}

// SetActiveStreams sets the number of active streams for a protocol
func SetActiveStreams(protocol string, count int) {
	streamsActiveTotal.WithLabelValues(protocol).Set(float64(count))
}

// RecordConnectionDuration records the duration of a connection
func RecordConnectionDuration(streamID, protocol string, duration float64) {
	connectionDuration.WithLabelValues(streamID, protocol).Observe(duration)
}

// IncrementReconnects increments the reconnection counter
func IncrementReconnects(streamID, protocol string) {
	connectionReconnectsTotal.WithLabelValues(streamID, protocol).Inc()
}

// SetSRTLatency sets the SRT connection latency
func SetSRTLatency(streamID string, latencyMs float64) {
	srtLatency.WithLabelValues(streamID).Set(latencyMs)
}

// SetRTPJitter sets the RTP stream jitter
func SetRTPJitter(streamID string, jitterMs float64) {
	rtpJitter.WithLabelValues(streamID).Set(jitterMs)
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

// UpdateSRTBytesReceived updates the SRT bytes received counter
func UpdateSRTBytesReceived(streamID string, bytes int64) {
	streamBytesTotal.WithLabelValues(streamID, "srt").Add(float64(bytes))
}

// UpdateSRTBytesSent updates the SRT bytes sent counter
func UpdateSRTBytesSent(streamID string, bytes int64) {
	streamBytesTotal.WithLabelValues(streamID, "srt").Add(float64(bytes))
}

// IncrementSRTConnections increments the SRT connection counter
func IncrementSRTConnections() {
	streamsActiveTotal.WithLabelValues("srt").Inc()
}

// DecrementSRTConnections decrements the SRT connection counter
func DecrementSRTConnections() {
	streamsActiveTotal.WithLabelValues("srt").Dec()
}
