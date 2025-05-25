# Metrics Package

The `metrics` package provides comprehensive monitoring and observability for the Mirror platform using Prometheus. It offers pre-defined metrics, automatic collection, and efficient export with minimal performance impact.

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Metric Types](#metric-types)
- [Usage](#usage)
- [Pre-defined Metrics](#pre-defined-metrics)
- [Custom Metrics](#custom-metrics)
- [Best Practices](#best-practices)
- [Performance](#performance)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The metrics package provides:

- **Prometheus integration** with standard metric types
- **Pre-defined metrics** for common operations
- **Automatic collection** with minimal code changes
- **Efficient export** with batching and compression
- **Thread-safe operations** for concurrent access
- **Cardinality control** to prevent explosion
- **Performance optimized** with minimal overhead

## Features

### Core Capabilities

- **Standard metric types**: Counter, Gauge, Histogram, Summary
- **Label support** for dimensional metrics
- **Metric families** for logical grouping
- **Push gateway support** for batch jobs
- **Custom collectors** for complex metrics
- **Exemplar support** for trace correlation
- **Native histograms** for efficient storage

### Integration Features

- **HTTP handler** for `/metrics` endpoint
- **Middleware** for automatic HTTP metrics
- **gRPC interceptors** for RPC metrics
- **Runtime metrics** for Go runtime stats
- **Process metrics** for system resources
- **Custom registries** for isolation

## Metric Types

### Counter
Monotonically increasing value (e.g., total requests)

```go
var (
    requestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "mirror",
            Subsystem: "http",
            Name:      "requests_total",
            Help:      "Total number of HTTP requests",
        },
        []string{"method", "endpoint", "status"},
    )
)

// Usage
requestsTotal.WithLabelValues("GET", "/streams", "200").Inc()
```

### Gauge
Value that can go up or down (e.g., active connections)

```go
var (
    activeStreams = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: "mirror",
            Subsystem: "ingestion",
            Name:      "active_streams",
            Help:      "Number of active streams",
        },
        []string{"protocol", "codec"},
    )
)

// Usage
activeStreams.WithLabelValues("srt", "hevc").Set(25)
activeStreams.WithLabelValues("srt", "hevc").Inc()
activeStreams.WithLabelValues("srt", "hevc").Dec()
```

### Histogram
Distribution of values (e.g., request latency)

```go
var (
    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "mirror",
            Subsystem: "http",
            Name:      "request_duration_seconds",
            Help:      "HTTP request latency",
            Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
        },
        []string{"method", "endpoint"},
    )
)

// Usage
start := time.Now()
// ... handle request ...
requestDuration.WithLabelValues("GET", "/streams").Observe(time.Since(start).Seconds())
```

### Summary
Similar to histogram but with quantiles

```go
var (
    frameSizes = prometheus.NewSummaryVec(
        prometheus.SummaryOpts{
            Namespace:  "mirror",
            Subsystem:  "video",
            Name:       "frame_size_bytes",
            Help:       "Size of video frames",
            Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
        },
        []string{"stream_id", "frame_type"},
    )
)

// Usage
frameSizes.WithLabelValues("stream_123", "I").Observe(float64(frameSize))
```

## Usage

### Basic Setup

```go
import (
    "github.com/zsiec/mirror/internal/metrics"
    "github.com/prometheus/client_golang/prometheus"
)

// Initialize metrics
metricsManager := metrics.NewManager(metrics.Config{
    Namespace: "mirror",
    Subsystem: "streaming",
})

// Register metrics
metricsManager.Register()

// Start metrics server
go metricsManager.ListenAndServe(":9090")
```

### HTTP Middleware

```go
// Automatic HTTP metrics collection
func MetricsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        
        // Wrap response writer
        wrapped := &responseWriter{ResponseWriter: w}
        
        // Handle request
        next.ServeHTTP(wrapped, r)
        
        // Record metrics
        duration := time.Since(start).Seconds()
        
        httpRequestsTotal.WithLabelValues(
            r.Method,
            r.URL.Path,
            strconv.Itoa(wrapped.status),
        ).Inc()
        
        httpRequestDuration.WithLabelValues(
            r.Method,
            r.URL.Path,
        ).Observe(duration)
        
        httpRequestSize.WithLabelValues(
            r.Method,
            r.URL.Path,
        ).Observe(float64(r.ContentLength))
        
        httpResponseSize.WithLabelValues(
            r.Method,
            r.URL.Path,
        ).Observe(float64(wrapped.bytes))
    })
}
```

## Pre-defined Metrics

### HTTP Metrics

```go
// Request metrics
mirror_http_requests_total{method,endpoint,status}
mirror_http_request_duration_seconds{method,endpoint}
mirror_http_request_size_bytes{method,endpoint}
mirror_http_response_size_bytes{method,endpoint}
mirror_http_active_connections{}

// Connection metrics
mirror_http_connections_total{protocol}
mirror_http_connection_duration_seconds{protocol}
mirror_http_connection_errors_total{protocol,error}
```

### Stream Ingestion Metrics

```go
// Stream metrics
mirror_ingestion_active_streams{protocol,codec}
mirror_ingestion_stream_duration_seconds{stream_id}
mirror_ingestion_bytes_received_total{stream_id,protocol}
mirror_ingestion_packets_received_total{stream_id,protocol}
mirror_ingestion_packets_lost_total{stream_id,protocol}
mirror_ingestion_errors_total{stream_id,error_type}

// Frame metrics
mirror_video_frames_assembled_total{stream_id,frame_type}
mirror_video_frames_dropped_total{stream_id,reason}
mirror_video_frame_size_bytes{stream_id,frame_type}
mirror_video_gop_size{stream_id}
mirror_video_keyframe_interval_seconds{stream_id}

// Buffer metrics
mirror_buffer_usage_bytes{stream_id,buffer_type}
mirror_buffer_usage_ratio{stream_id,buffer_type}
mirror_buffer_overflows_total{stream_id}
mirror_buffer_underflows_total{stream_id}
```

### System Metrics

```go
// Resource metrics
mirror_cpu_usage_percent{}
mirror_memory_usage_bytes{type}
mirror_goroutines_active{}
mirror_gc_duration_seconds{}
mirror_gc_collections_total{}

// Process metrics
process_cpu_seconds_total{}
process_resident_memory_bytes{}
process_virtual_memory_bytes{}
process_open_fds{}
```

## Custom Metrics

### Creating Custom Metrics

```go
// Define custom metric
type VideoQualityCollector struct {
    streamManager *ingestion.Manager
    
    // Metrics
    bitrateDesc    *prometheus.Desc
    framerateDesc  *prometheus.Desc
    resolutionDesc *prometheus.Desc
}

func NewVideoQualityCollector(mgr *ingestion.Manager) prometheus.Collector {
    return &VideoQualityCollector{
        streamManager: mgr,
        bitrateDesc: prometheus.NewDesc(
            "mirror_video_bitrate_bps",
            "Current video bitrate in bits per second",
            []string{"stream_id", "codec"},
            nil,
        ),
        framerateDesc: prometheus.NewDesc(
            "mirror_video_framerate_fps",
            "Current video framerate",
            []string{"stream_id"},
            nil,
        ),
    }
}

// Implement Collector interface
func (c *VideoQualityCollector) Describe(ch chan<- *prometheus.Desc) {
    ch <- c.bitrateDesc
    ch <- c.framerateDesc
}

func (c *VideoQualityCollector) Collect(ch chan<- prometheus.Metric) {
    streams := c.streamManager.GetActiveStreams()
    
    for _, stream := range streams {
        stats := stream.GetStats()
        
        ch <- prometheus.MustNewConstMetric(
            c.bitrateDesc,
            prometheus.GaugeValue,
            float64(stats.Bitrate),
            stream.ID,
            stream.Codec,
        )
        
        ch <- prometheus.MustNewConstMetric(
            c.framerateDesc,
            prometheus.GaugeValue,
            stats.Framerate,
            stream.ID,
        )
    }
}
```

### Registering Custom Collectors

```go
// Register custom collector
videoCollector := NewVideoQualityCollector(streamManager)
prometheus.MustRegister(videoCollector)

// Or with custom registry
registry := prometheus.NewRegistry()
registry.MustRegister(videoCollector)
```

## Best Practices

### 1. Label Cardinality

```go
// DO: Use bounded label values
httpRequestsTotal.WithLabelValues(
    r.Method,              // Limited values: GET, POST, etc.
    normalizedPath(r.URL), // Normalized: /streams/{id}
    statusClass(code),     // Classes: 2xx, 3xx, 4xx, 5xx
).Inc()

// DON'T: Use unbounded labels
httpRequestsTotal.WithLabelValues(
    r.Header.Get("User-Agent"), // Infinite variety!
    r.URL.String(),             // Includes query params!
    clientIP,                   // Thousands of IPs!
).Inc()

// Helper functions
func normalizedPath(u *url.URL) string {
    // Replace IDs with placeholders
    path := pathRegex.ReplaceAllString(u.Path, "{id}")
    return path
}

func statusClass(code int) string {
    return fmt.Sprintf("%dxx", code/100)
}
```

### 2. Metric Naming

```go
// DO: Follow Prometheus naming conventions
var (
    // Units in name
    requestDurationSeconds = prometheus.NewHistogram(...)
    responseSizeBytes      = prometheus.NewHistogram(...)
    
    // Clear descriptive names
    streamIngestionBytesTotal = prometheus.NewCounter(...)
    videoFramesDroppedTotal   = prometheus.NewCounter(...)
    
    // Proper suffixes
    errorsTotal        = prometheus.NewCounter(...) // _total for counters
    temperatureCelsius = prometheus.NewGauge(...)   // unit suffix for gauges
)

// DON'T: Use unclear or incorrect names
var (
    requestTime   = prometheus.NewHistogram(...) // What unit?
    streamData    = prometheus.NewCounter(...)   // What data?
    videoFrames   = prometheus.NewCounter(...)   // Total? Current?
)
```

### 3. Histogram Buckets

```go
// DO: Choose appropriate buckets for your use case
var (
    // API latency (milliseconds to seconds)
    apiLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
        Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
    })
    
    // File sizes (bytes to gigabytes)
    fileSizes = prometheus.NewHistogram(prometheus.HistogramOpts{
        Buckets: prometheus.ExponentialBuckets(1024, 2, 20), // 1KB to 1GB
    })
    
    // Percentages (0 to 100)
    cpuUsage = prometheus.NewHistogram(prometheus.HistogramOpts{
        Buckets: prometheus.LinearBuckets(0, 10, 11), // 0, 10, 20, ..., 100
    })
)
```

### 4. Performance Optimization

```go
// Cache label values for hot paths
type MetricsCache struct {
    requests map[string]prometheus.Counter
    mu       sync.RWMutex
}

func (c *MetricsCache) RecordRequest(method, path, status string) {
    key := fmt.Sprintf("%s:%s:%s", method, path, status)
    
    // Try read lock first
    c.mu.RLock()
    counter, ok := c.requests[key]
    c.mu.RUnlock()
    
    if ok {
        counter.Inc()
        return
    }
    
    // Create new counter
    c.mu.Lock()
    counter = httpRequestsTotal.WithLabelValues(method, path, status)
    c.requests[key] = counter
    c.mu.Unlock()
    
    counter.Inc()
}
```

## Performance

### Minimal Overhead

```go
// Use atomic operations for counters
type FastCounter struct {
    value int64
}

func (c *FastCounter) Inc() {
    atomic.AddInt64(&c.value, 1)
}

func (c *FastCounter) Get() float64 {
    return float64(atomic.LoadInt64(&c.value))
}

// Batch updates
type BatchedMetrics struct {
    updates chan metricUpdate
}

func (m *BatchedMetrics) Record(name string, value float64) {
    select {
    case m.updates <- metricUpdate{name, value}:
    default:
        // Buffer full, drop update
    }
}
```

### Sampling High-Frequency Metrics

```go
// Sample only a percentage of events
type SampledHistogram struct {
    histogram  prometheus.Histogram
    sampleRate float64
}

func (h *SampledHistogram) Observe(value float64) {
    if rand.Float64() < h.sampleRate {
        h.histogram.Observe(value)
    }
}

// Adaptive sampling based on rate
type AdaptiveSampler struct {
    histogram   prometheus.Histogram
    rate        int64
    sampleEvery int64
}

func (s *AdaptiveSampler) Observe(value float64) {
    count := atomic.AddInt64(&s.rate, 1)
    if count%s.sampleEvery == 0 {
        s.histogram.Observe(value)
    }
}
```

## Testing

### Unit Tests

```go
func TestMetricsCollection(t *testing.T) {
    // Create test registry
    reg := prometheus.NewRegistry()
    
    // Register metric
    requests := prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "test_requests_total",
        },
        []string{"method"},
    )
    reg.MustRegister(requests)
    
    // Increment counter
    requests.WithLabelValues("GET").Inc()
    requests.WithLabelValues("POST").Add(5)
    
    // Gather metrics
    mfs, err := reg.Gather()
    require.NoError(t, err)
    
    // Verify
    require.Len(t, mfs, 1)
    require.Equal(t, "test_requests_total", *mfs[0].Name)
    
    // Check values
    for _, m := range mfs[0].Metric {
        switch m.Label[0].GetValue() {
        case "GET":
            assert.Equal(t, 1.0, m.Counter.GetValue())
        case "POST":
            assert.Equal(t, 5.0, m.Counter.GetValue())
        }
    }
}
```

### Integration Tests

```go
func TestMetricsEndpoint(t *testing.T) {
    // Setup server with metrics
    srv := setupTestServer(t)
    defer srv.Close()
    
    // Make some requests to generate metrics
    for i := 0; i < 10; i++ {
        resp, err := http.Get(srv.URL + "/api/test")
        require.NoError(t, err)
        resp.Body.Close()
    }
    
    // Get metrics
    resp, err := http.Get(srv.URL + "/metrics")
    require.NoError(t, err)
    defer resp.Body.Close()
    
    // Parse metrics
    parser := expfmt.TextParser{}
    mfs, err := parser.TextToMetricFamilies(resp.Body)
    require.NoError(t, err)
    
    // Verify expected metrics exist
    _, ok := mfs["http_requests_total"]
    assert.True(t, ok)
    
    // Check values
    for _, m := range mfs["http_requests_total"].Metric {
        if getLabelValue(m.Label, "endpoint") == "/api/test" {
            assert.Equal(t, float64(10), m.Counter.GetValue())
        }
    }
}
```

## Examples

### Complete Metrics Setup

```go
// metrics/setup.go
func SetupMetrics(config Config) (*Manager, error) {
    // Create manager
    manager := NewManager(config)
    
    // Register standard metrics
    manager.RegisterHTTPMetrics()
    manager.RegisterStreamMetrics()
    manager.RegisterSystemMetrics()
    
    // Register custom collectors
    manager.RegisterCollector(
        NewVideoQualityCollector(streamManager),
    )
    manager.RegisterCollector(
        NewBufferUsageCollector(bufferManager),
    )
    
    // Start metrics server
    go func() {
        if err := manager.ListenAndServe(":9090"); err != nil {
            log.Fatalf("Metrics server failed: %v", err)
        }
    }()
    
    return manager, nil
}
```

### Stream Processing Metrics

```go
// Comprehensive stream metrics
type StreamMetrics struct {
    // Counters
    packetsReceived   *prometheus.CounterVec
    bytesReceived     *prometheus.CounterVec
    framesAssembled   *prometheus.CounterVec
    framesDropped     *prometheus.CounterVec
    
    // Gauges
    activeStreams     *prometheus.GaugeVec
    bitrate          *prometheus.GaugeVec
    bufferUsage      *prometheus.GaugeVec
    
    // Histograms
    frameLatency     *prometheus.HistogramVec
    gopSize          *prometheus.HistogramVec
    processingTime   *prometheus.HistogramVec
}

func NewStreamMetrics(reg prometheus.Registerer) *StreamMetrics {
    m := &StreamMetrics{
        packetsReceived: prometheus.NewCounterVec(
            prometheus.CounterOpts{
                Namespace: "mirror",
                Subsystem: "stream",
                Name:      "packets_received_total",
                Help:      "Total packets received",
            },
            []string{"stream_id", "protocol"},
        ),
        // ... initialize other metrics
    }
    
    // Register all metrics
    reg.MustRegister(
        m.packetsReceived,
        m.bytesReceived,
        // ... register others
    )
    
    return m
}

// Usage in stream handler
func (h *StreamHandler) HandlePacket(packet Packet) error {
    // Record packet
    h.metrics.packetsReceived.WithLabelValues(
        h.streamID,
        h.protocol,
    ).Inc()
    
    h.metrics.bytesReceived.WithLabelValues(
        h.streamID,
        h.protocol,
    ).Add(float64(len(packet.Data)))
    
    // Process with timing
    start := time.Now()
    frame, err := h.processPacket(packet)
    duration := time.Since(start)
    
    h.metrics.processingTime.WithLabelValues(
        h.streamID,
    ).Observe(duration.Seconds())
    
    if err != nil {
        h.metrics.framesDropped.WithLabelValues(
            h.streamID,
            "processing_error",
        ).Inc()
        return err
    }
    
    // Record frame
    h.metrics.framesAssembled.WithLabelValues(
        h.streamID,
        frame.Type.String(),
    ).Inc()
    
    return nil
}
```

### Dashboard Query Examples

```promql
# Active streams by protocol
sum by (protocol) (mirror_ingestion_active_streams)

# Total bandwidth usage
sum(rate(mirror_ingestion_bytes_received_total[5m])) * 8

# Frame drop rate
sum(rate(mirror_video_frames_dropped_total[5m])) / 
sum(rate(mirror_video_frames_assembled_total[5m]))

# 99th percentile API latency
histogram_quantile(0.99, 
  sum(rate(mirror_http_request_duration_seconds_bucket[5m])) by (le, endpoint)
)

# Stream health score (custom)
(1 - (rate(mirror_video_frames_dropped_total[5m]) / 
      rate(mirror_video_frames_assembled_total[5m]))) * 
(1 - (rate(mirror_ingestion_errors_total[5m]) / 
      rate(mirror_ingestion_packets_received_total[5m])))
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Prometheus Documentation](https://prometheus.io/docs/) - Official Prometheus docs
- [Grafana Dashboards](../../dashboards/) - Pre-built dashboards
- [Alerting Rules](../../alerts/) - Prometheus alerting configuration
- [Performance Guide](../../docs/performance.md) - Performance optimization
