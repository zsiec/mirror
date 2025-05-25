# Metrics Package

This package provides a centralized metrics collection and export system using Prometheus client libraries. It defines all application metrics and provides thread-safe methods for updating them.

## Overview

The metrics system follows Prometheus best practices:
- All metrics are pre-registered at startup
- Consistent naming conventions
- Appropriate metric types for each use case
- Meaningful labels for cardinality control

## Metric Types

### Counters
Monotonically increasing values, used for:
- Total requests processed
- Errors encountered
- Packets/frames/GOPs processed
- Bytes transferred

### Gauges
Values that can go up or down, used for:
- Active connections
- Current memory usage
- Queue depths
- Stream counts

### Histograms
Distribution of values, used for:
- Request latencies
- Processing times
- Frame sizes
- Queue wait times

### Summaries
Similar to histograms but with quantiles, used for:
- P50/P90/P99 latencies
- Size distributions

## Defined Metrics

### HTTP Metrics
```go
// Request duration by endpoint and method
httpRequestDuration = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name: "mirror_http_request_duration_seconds",
        Help: "HTTP request latencies in seconds.",
        Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
    },
    []string{"method", "endpoint", "status"},
)

// Total requests by endpoint
httpRequestsTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_http_requests_total",
        Help: "Total number of HTTP requests.",
    },
    []string{"method", "endpoint", "status"},
)

// Response sizes
httpResponseSize = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name: "mirror_http_response_size_bytes",
        Help: "HTTP response sizes in bytes.",
        Buckets: prometheus.ExponentialBuckets(100, 10, 8),
    },
    []string{"method", "endpoint"},
)
```

### Stream Metrics
```go
// Active streams by protocol
activeStreams = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_active_streams",
        Help: "Number of active streams.",
    },
    []string{"protocol", "codec"},
)

// Stream data rates
streamBitrate = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_stream_bitrate_bps",
        Help: "Current stream bitrate in bits per second.",
    },
    []string{"stream_id", "direction"},
)

// Packets processed
packetsProcessed = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_packets_processed_total",
        Help: "Total number of packets processed.",
    },
    []string{"stream_id", "protocol"},
)

// Frames assembled
framesAssembled = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_frames_assembled_total",
        Help: "Total number of frames assembled.",
    },
    []string{"stream_id", "codec"},
)

// GOPs buffered
gopsBuffered = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_gops_buffered",
        Help: "Number of GOPs currently buffered.",
    },
    []string{"stream_id"},
)
```

### Buffer Metrics
```go
// Buffer usage
bufferUsage = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_buffer_usage_bytes",
        Help: "Current buffer usage in bytes.",
    },
    []string{"stream_id", "buffer_type"},
)

// Buffer capacity
bufferCapacity = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_buffer_capacity_bytes",
        Help: "Total buffer capacity in bytes.",
    },
    []string{"stream_id", "buffer_type"},
)

// Backpressure events
backpressureEvents = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_backpressure_events_total",
        Help: "Total number of backpressure events.",
    },
    []string{"stream_id", "severity"},
)
```

### Memory Metrics
```go
// Memory allocations
memoryAllocated = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_memory_allocated_bytes",
        Help: "Memory allocated per component.",
    },
    []string{"component"},
)

// Memory limits
memoryLimit = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_memory_limit_bytes",
        Help: "Memory limits per component.",
    },
    []string{"component"},
)

// Memory pressure
memoryPressure = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_memory_pressure_ratio",
        Help: "Memory pressure ratio (0-1).",
    },
    []string{"component"},
)
```

### Error Metrics
```go
// Stream errors
streamErrors = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_stream_errors_total",
        Help: "Total number of stream errors.",
    },
    []string{"stream_id", "error_type"},
)

// Recovery attempts
recoveryAttempts = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_recovery_attempts_total",
        Help: "Total number of recovery attempts.",
    },
    []string{"stream_id", "recovery_type"},
)

// Dropped frames
droppedFrames = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_dropped_frames_total",
        Help: "Total number of dropped frames.",
    },
    []string{"stream_id", "reason"},
)
```

### Sync Metrics
```go
// A/V sync drift
syncDrift = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "mirror_sync_drift_ms",
        Help: "Audio/Video synchronization drift in milliseconds.",
    },
    []string{"stream_id"},
)

// Sync corrections
syncCorrections = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "mirror_sync_corrections_total",
        Help: "Total number of sync corrections applied.",
    },
    []string{"stream_id", "correction_type"},
)
```

## Usage Examples

### Recording HTTP Metrics
```go
// Record request duration
start := time.Now()
defer func() {
    duration := time.Since(start).Seconds()
    metrics.RecordHTTPRequest(method, endpoint, status, duration)
}()

// Record response size
metrics.RecordHTTPResponse(method, endpoint, size)
```

### Recording Stream Metrics
```go
// Update active streams
metrics.UpdateActiveStreams(protocol, codec, 1)
defer metrics.UpdateActiveStreams(protocol, codec, -1)

// Record bitrate
metrics.UpdateStreamBitrate(streamID, "input", bitrate)

// Record packets
metrics.IncrementPacketsProcessed(streamID, protocol)

// Record frames
metrics.IncrementFramesAssembled(streamID, codec)
```

### Recording Buffer Metrics
```go
// Update buffer usage
metrics.UpdateBufferUsage(streamID, "ring", currentSize)
metrics.UpdateBufferCapacity(streamID, "ring", maxSize)

// Record backpressure
metrics.IncrementBackpressure(streamID, "high")
```

### Recording Errors
```go
// Record stream error
metrics.IncrementStreamError(streamID, "decode_failure")

// Record recovery attempt
metrics.IncrementRecoveryAttempt(streamID, "reconnect")

// Record dropped frame
metrics.IncrementDroppedFrames(streamID, "buffer_full")
```

## Best Practices

### Label Cardinality
- Keep label cardinality low (<100 unique values per label)
- Use predefined enums for label values
- Avoid high-cardinality labels like user IDs

### Metric Naming
- Use `mirror_` prefix for all metrics
- Use underscores to separate words
- Include units in metric names (_seconds, _bytes, _total)
- Use standard suffixes (_total for counters)

### Performance
- Pre-compute label values when possible
- Use atomic operations for counters/gauges
- Batch updates when appropriate
- Avoid metrics in hot paths

### Testing
```go
// Example test for metrics
func TestStreamMetrics(t *testing.T) {
    // Create test registry
    reg := prometheus.NewRegistry()
    metrics := NewMetrics(reg)
    
    // Record metric
    metrics.UpdateActiveStreams("srt", "h264", 1)
    
    // Verify metric
    dto := &dto.Metric{}
    metric, _ := reg.Gather()
    // Assert on metric values
}
```

## Metric Queries

Example Prometheus queries:

### Request Rate
```promql
rate(mirror_http_requests_total[5m])
```

### P95 Latency
```promql
histogram_quantile(0.95, rate(mirror_http_request_duration_seconds_bucket[5m]))
```

### Active Streams by Protocol
```promql
sum by (protocol) (mirror_active_streams)
```

### Stream Bitrate
```promql
mirror_stream_bitrate_bps{direction="input"}
```

### Error Rate
```promql
rate(mirror_stream_errors_total[5m])
```

### Memory Usage
```promql
mirror_memory_allocated_bytes / mirror_memory_limit_bytes
```

## Dashboard Guidelines

When creating Grafana dashboards:

### Overview Dashboard
- Total active streams
- Aggregate bitrate (in/out)
- Error rates
- Memory usage
- Request rates

### Stream Dashboard
- Per-stream bitrate
- Frame/GOP counts
- Buffer utilization
- Sync drift
- Error details

### Performance Dashboard
- Request latencies
- Processing times
- Queue depths
- CPU/Memory usage
- GC metrics

## Alerts

Example alert rules:

```yaml
groups:
  - name: mirror_alerts
    rules:
      - alert: HighErrorRate
        expr: rate(mirror_stream_errors_total[5m]) > 0.05
        for: 5m
        
      - alert: HighMemoryUsage
        expr: mirror_memory_pressure_ratio > 0.9
        for: 10m
        
      - alert: StreamBackpressure
        expr: rate(mirror_backpressure_events_total[5m]) > 10
        for: 5m
        
      - alert: SyncDrift
        expr: abs(mirror_sync_drift_ms) > 500
        for: 5m
```

## Integration

The metrics package integrates with:
- HTTP middleware for automatic request tracking
- Stream handlers for video metrics
- Buffer implementations for usage tracking
- Error handlers for failure tracking

All metrics are exposed at the `/metrics` endpoint for Prometheus scraping.
