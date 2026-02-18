# Metrics Package

The `metrics` package provides Prometheus metrics collection for the Mirror application. All metrics are registered via `promauto` and exposed through package-level functions.

## Design Philosophy

**No per-stream Prometheus labels.** Per-stream metrics (keyed by `stream_id`) are intentionally excluded to avoid unbounded cardinality. Each new stream would create time series that are never cleaned up, leading to a "cardinality bomb." For per-stream diagnostics, use structured logging or a custom API endpoint.

## Defined Metrics

### Stream Ingestion (aggregate by protocol)

| Metric | Type | Labels |
|---|---|---|
| `stream_ingestion_active_total` | GaugeVec | `protocol` |
| `stream_ingestion_bytes_total` | CounterVec | `protocol` |
| `stream_ingestion_packets_total` | CounterVec | `protocol` |
| `stream_ingestion_packets_lost_total` | CounterVec | `protocol` |
| `stream_ingestion_errors_total` | CounterVec | `error_type`, `protocol` |

### Connection Metrics

| Metric | Type | Labels |
|---|---|---|
| `connection_duration_seconds` | HistogramVec | `protocol` |
| `connection_reconnects_total` | CounterVec | `protocol` |

### SRT-Specific (aggregate)

| Metric | Type |
|---|---|
| `srt_packets_lost_total` | Counter |
| `srt_packets_dropped_total` | Counter |
| `srt_packets_retransmitted_total` | Counter |

### RTP-Specific

| Metric | Type |
|---|---|
| `rtp_sessions_active_total` | Gauge |

### Debug Metrics

| Metric | Type | Labels |
|---|---|---|
| `debug_goroutines_created_total` | CounterVec | `component` |
| `debug_goroutines_destroyed_total` | CounterVec | `component` |
| `debug_goroutines_active` | GaugeVec | `component` |
| `debug_lock_contention_seconds` | HistogramVec | `component`, `lock_name` |
| `debug_memory_allocations_total` | CounterVec | `component` |
| `debug_memory_allocated_bytes_total` | CounterVec | `component` |
| `debug_context_cancellations_total` | CounterVec | `component`, `reason` |

## Public Functions

### Stream Metrics

```go
UpdateStreamMetrics(streamID, protocol string, bytesReceived, packetsReceived, packetsLost int64, bitrate float64)
IncrementStreamError(streamID, errorType, protocol string)
SetActiveStreams(protocol string, count int)
```

### Connection Metrics

```go
RecordConnectionDuration(streamID, protocol string, duration float64)
IncrementReconnects(streamID, protocol string)
```

### SRT Metrics

```go
UpdateSRTStats(streamID string, rttMs float64, packetsLostDelta, packetsDroppedDelta, packetsRetransDelta int64, flightSize int, receiveRateMbps, bandwidthMbps float64, availRcvBuf int)
UpdateSRTBytesReceived(streamID string, bytes int64)
UpdateSRTBytesSent(streamID string, bytes int64)
IncrementSRTConnections()
DecrementSRTConnections()

// No-ops (per-stream gauges removed):
SetSRTLatency(streamID string, latencyMs float64)
SetRTPJitter(streamID string, jitterMs float64)
```

### RTP Metrics

```go
SetActiveRTPSessions(count int)
```

### Debug Metrics

```go
IncrementGoroutineCreated(component string)
IncrementGoroutineDestroyed(component string)
RecordLockContention(component, lockName string, duration float64)
IncrementMemoryAllocation(component string, bytes int64)
IncrementContextCancellation(component, reason string)
```

Note: Many functions accept a `streamID` parameter for caller convenience, but it is **never** used as a Prometheus label. SRT stats functions expect **deltas**, not cumulative values.

## Wrapper Types (`wrappers.go`)

For ad-hoc metric creation:

```go
counter := metrics.NewCounter("my_counter", map[string]string{"env": "dev"})
counter.Inc()
counter.Add(5.0)

gauge := metrics.NewGauge("my_gauge", nil)
gauge.Set(42.0)
gauge.Inc()
gauge.Dec()
gauge.Add(10.0)
gauge.Sub(3.0)

histogram := metrics.NewHistogram("my_histogram", nil, prometheus.DefBuckets)
histogram.Observe(0.5)
```

These handle `AlreadyRegisteredError` gracefully by reusing the existing collector.

## Files

- `metrics.go`: All metric definitions and update functions
- `wrappers.go`: Counter, Gauge, Histogram wrapper types
- `metrics_test.go`, `wrappers_test.go`: Tests

## Dashboard Query Examples

```promql
# Active streams by protocol
stream_ingestion_active_total

# Total ingestion bandwidth (bits/sec)
sum(rate(stream_ingestion_bytes_total[5m])) * 8

# Packet loss rate
sum(rate(stream_ingestion_packets_lost_total[5m])) /
sum(rate(stream_ingestion_packets_total[5m]))

# SRT retransmission rate
rate(srt_packets_retransmitted_total[5m])
```
