package buffer

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Buffer usage metrics
	bufferUsageBytes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "buffer_usage_bytes",
		Help: "Current buffer usage in bytes",
	}, []string{"stream_id"})

	// Buffer drops
	bufferDropsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "buffer_drops_total",
		Help: "Total number of dropped bytes due to buffer overflow",
	}, []string{"stream_id"})

	// Buffer read/write latency
	bufferReadLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "buffer_read_latency_seconds",
		Help:    "Buffer read operation latency",
		Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10), // 0.1ms to ~100ms
	}, []string{"stream_id"})

	bufferWriteLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "buffer_write_latency_seconds",
		Help:    "Buffer write operation latency",
		Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10), // 0.1ms to ~100ms
	}, []string{"stream_id"})

	// Pool metrics
	poolActiveBuffers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "buffer_pool_active_total",
		Help: "Number of active buffers in the pool",
	})

	poolFreeBuffers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "buffer_pool_free_total",
		Help: "Number of free buffers in the pool",
	})
)

// UpdateMetrics updates Prometheus metrics for a buffer
func UpdateMetrics(streamID string, stats BufferStats) {
	bufferUsageBytes.WithLabelValues(streamID).Set(float64(stats.Available))
	bufferDropsTotal.WithLabelValues(streamID).Add(float64(stats.Drops))
}

// UpdatePoolMetrics updates Prometheus metrics for the buffer pool
func UpdatePoolMetrics(stats PoolStats) {
	poolActiveBuffers.Set(float64(stats.ActiveBuffers))
	poolFreeBuffers.Set(float64(stats.FreeBuffers))
}
