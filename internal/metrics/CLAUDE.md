# Metrics Package

This package provides Prometheus metrics collection for the Mirror application. It uses `promauto` for automatic registration and exposes package-level functions for updating metrics.

## Design Philosophy

**No per-stream Prometheus labels.** Per-stream metrics (keyed by `stream_id`) are intentionally excluded to avoid unbounded cardinality. Each new stream would create time series that are never cleaned up, leading to a "cardinality bomb." For per-stream diagnostics, use structured logging or a custom API endpoint.

## Files

- `metrics.go`: All metric definitions and update functions
- `wrappers.go`: Generic `Counter`, `Gauge`, and `Histogram` wrapper types for ad-hoc metric creation

## Defined Metrics

### Stream Ingestion (aggregate by protocol)
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `stream_ingestion_active_total` | GaugeVec | `protocol` | Number of active ingestion streams |
| `stream_ingestion_bytes_total` | CounterVec | `protocol` | Total bytes received |
| `stream_ingestion_packets_total` | CounterVec | `protocol` | Total packets received |
| `stream_ingestion_packets_lost_total` | CounterVec | `protocol` | Total packets lost |
| `stream_ingestion_errors_total` | CounterVec | `error_type`, `protocol` | Total errors |

### Connection Metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `connection_duration_seconds` | HistogramVec | `protocol` | Connection duration (buckets: 1s to ~16k s) |
| `connection_reconnects_total` | CounterVec | `protocol` | Reconnection attempts |

### SRT-Specific (aggregate, no per-stream)
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `srt_packets_lost_total` | Counter | — | SRT packets lost (receiver side) |
| `srt_packets_dropped_total` | Counter | — | SRT packets dropped (too late) |
| `srt_packets_retransmitted_total` | Counter | — | SRT packets retransmitted |

### RTP-Specific
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `rtp_sessions_active_total` | Gauge | — | Active RTP sessions |

### Debug Metrics
| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `debug_goroutines_created_total` | CounterVec | `component` | Goroutines created |
| `debug_goroutines_destroyed_total` | CounterVec | `component` | Goroutines destroyed |
| `debug_goroutines_active` | GaugeVec | `component` | Active goroutines |
| `debug_lock_contention_seconds` | HistogramVec | `component`, `lock_name` | Lock contention duration |
| `debug_memory_allocations_total` | CounterVec | `component` | Memory allocations count |
| `debug_memory_allocated_bytes_total` | CounterVec | `component` | Bytes allocated |
| `debug_context_cancellations_total` | CounterVec | `component`, `reason` | Context cancellations |

## Public Functions

### Stream Metrics
```go
// Update aggregate stream metrics (streamID accepted for caller convenience, not used as label)
UpdateStreamMetrics(streamID, protocol string, bytesReceived, packetsReceived, packetsLost int64, bitrate float64)

// Increment error counter (streamID not used as label)
IncrementStreamError(streamID, errorType, protocol string)

// Set active stream count for a protocol
SetActiveStreams(protocol string, count int)
```

### Connection Metrics
```go
// Record connection duration (streamID not used as label)
RecordConnectionDuration(streamID, protocol string, duration float64)

// Increment reconnection counter (streamID not used as label)
IncrementReconnects(streamID, protocol string)
```

### SRT Metrics
```go
// Update aggregate SRT counters (expects deltas, not cumulative values)
// Per-stream gauges (RTT, flight size, etc.) are no-ops — use structured logging
UpdateSRTStats(streamID string, rttMs float64, packetsLostDelta, packetsDroppedDelta, packetsRetransDelta int64, flightSize int, receiveRateMbps, bandwidthMbps float64, availRcvBuf int)

UpdateSRTBytesReceived(streamID string, bytes int64)
UpdateSRTBytesSent(streamID string, bytes int64)
IncrementSRTConnections()
DecrementSRTConnections()

// No-ops (per-stream gauges removed to avoid cardinality):
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

## Wrapper Types (wrappers.go)

For ad-hoc metric creation, the package provides simple wrapper types:

```go
counter := metrics.NewCounter("my_counter", map[string]string{"env": "dev"})
counter.Inc()
counter.Add(5.0)

gauge := metrics.NewGauge("my_gauge", map[string]string{"env": "dev"})
gauge.Set(42.0)
gauge.Inc()
gauge.Dec()

histogram := metrics.NewHistogram("my_histogram", nil, prometheus.DefBuckets)
histogram.Observe(0.5)
```

These handle `AlreadyRegisteredError` gracefully by reusing the existing collector.

## Best Practices

- Use the package-level functions (not wrapper types) for standard metrics
- The `streamID` parameter is accepted on many functions for caller convenience but is never used as a Prometheus label
- SRT stats functions expect **deltas**, not cumulative values
- All metrics are exposed at the `/metrics` endpoint
