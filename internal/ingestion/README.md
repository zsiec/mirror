# Ingestion Package

The `ingestion` package implements a comprehensive video stream ingestion system for the Mirror platform. It supports multiple protocols (SRT, RTP), automatic codec detection, intelligent buffering, and advanced video processing capabilities.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Features](#features)
- [Protocols](#protocols)
- [Video Processing Pipeline](#video-processing-pipeline)
- [Buffer Management](#buffer-management)
- [API Endpoints](#api-endpoints)
- [Configuration](#configuration)
- [Best Practices](#best-practices)
- [Testing](#testing)
- [Examples](#examples)

## Overview

The ingestion system provides:

- **Multi-protocol support** (SRT, RTP) with unified interface
- **Automatic codec detection** (H.264, HEVC, AV1, JPEG-XS)
- **Video-aware buffering** with GOP management
- **Frame-perfect synchronization** for A/V alignment
- **Intelligent backpressure** control
- **Automatic recovery** and reconnection
- **Comprehensive metrics** and monitoring

## Architecture

### System Architecture

```
┌─────────────────┐     ┌─────────────────┐
│   SRT Client    │     │   RTP Client    │
└────────┬────────┘     └────────┬────────┘
         │                       │
    ┌────▼────────┐         ┌───▼─────────┐
    │ SRT Listener│         │ RTP Listener│
    └────┬────────┘         └───┬─────────┘
         │                      │
    ┌────▼──────────────────────▼────────┐
    │      Connection Adapter Layer      │
    └────────────────┬───────────────────┘
                     │
    ┌────────────────▼───────────────────┐
    │      Stream Handler Manager        │
    └────────────────┬───────────────────┘
                     │
    ┌────────────────▼───────────────────┐
    │      Video Processing Pipeline     │
    ├────────────────────────────────────┤
    │ • Codec Detection                  │
    │ • Frame Assembly                   │
    │ • GOP Buffering                    │
    │ • A/V Synchronization              │
    └────────────────┬───────────────────┘
                     │
    ┌────────────────▼───────────────────┐
    │         Output Queue               │
    └────────────────────────────────────┘
```

### Component Overview

- **Manager**: Central orchestrator for all ingestion operations
- **Protocol Listeners**: SRT and RTP protocol implementations
- **Stream Handler**: Manages individual stream processing
- **Video Pipeline**: Processes video through multiple stages
- **Buffer System**: Efficient memory management with backpressure
- **Registry**: Tracks active streams with Redis backend

## Features

### Stream Management

- **Automatic stream detection** from protocol metadata
- **Connection limiting** with configurable maximums
- **Stream lifecycle management** (create, pause, resume, terminate)
- **Health monitoring** with heartbeat tracking
- **Graceful shutdown** with stream draining

### Video Processing

- **Codec-agnostic design** with pluggable depacketizers
- **Frame boundary detection** for clean cuts
- **GOP structure preservation** for quality switching
- **B-frame reordering** support
- **Timestamp management** with wraparound handling

### Reliability

- **Automatic reconnection** with exponential backoff
- **Circuit breaker pattern** for failing streams
- **Memory protection** with global and per-stream limits
- **Packet loss recovery** for RTP streams
- **Graceful degradation** under load

## Protocols

### SRT (Secure Reliable Transport)

```go
// SRT configuration (from config.SRTConfig)
type SRTConfig struct {
    Enabled           bool
    Port              int           // Listen port (default: 30000)
    Latency           time.Duration // Target latency (default: 120ms)
    MaxBandwidth      int64         // Max bandwidth per stream
    PayloadSize       int           // MTU-friendly packet size
    FlowControlWindow int           // Flow control window
    PeerIdleTimeout   time.Duration // Disconnect idle peers
    MaxConnections    int           // Max concurrent connections
}

// Usage — the manager creates the listener internally via the adapter pattern
adapter := srt.NewPureGoAdapter()
srtListener := srt.NewListenerWithAdapter(&cfg.SRT, &cfg.Codecs, reg, adapter, logger)
srtListener.SetHandler(connectionHandler)
if err := srtListener.Start(); err != nil {
    log.Fatal("Failed to start SRT listener:", err)
}
```

#### SRT Features

- **Built-in encryption** with passphrase support
- **Bandwidth estimation** and congestion control
- **Stream ID routing** for multi-stream support
- **Low latency mode** for live streaming
- **Connection statistics** and monitoring

### RTP (Real-time Transport Protocol)

```go
// RTP configuration (from config.RTPConfig)
type RTPConfig struct {
    Enabled        bool
    Port           int           // RTP port (default: 5004)
    RTCPPort       int           // RTCP port (default: 5005)
    BufferSize     int           // UDP buffer size
    MaxSessions    int           // Max concurrent sessions
    SessionTimeout time.Duration // Idle session timeout (default: 10s)
}

// Usage — the manager creates the listener internally
rtpListener := rtp.NewListener(&cfg.RTP, &cfg.Codecs, reg, logger)
rtpListener.SetSessionHandler(sessionHandler)
if err := rtpListener.Start(); err != nil {
    log.Fatal("Failed to start RTP listener:", err)
}
```

#### RTP Features

- **SSRC-based stream identification**
- **Sequence number validation**
- **Jitter buffer management**
- **RTCP support** (planned)
- **Multi-codec support** via payload type

## Video Processing Pipeline

### Pipeline Stages

```go
// Video pipeline processes data through stages
type VideoPipeline struct {
    codec      codec.Detector
    assembler  frame.Assembler
    detector   frame.Detector
    gopBuffer  gop.Buffer
    syncMgr    sync.Manager
    output     chan<- interface{}
}

// Processing flow
func (p *VideoPipeline) Process(packet Packet) error {
    // 1. Detect codec
    codecType := p.codec.Detect(packet)
    
    // 2. Assemble frame
    frame, complete := p.assembler.AddPacket(packet)
    if !complete {
        return nil // Wait for more packets
    }
    
    // 3. Detect frame boundaries
    frameInfo := p.detector.Analyze(frame)
    
    // 4. Buffer in GOP
    p.gopBuffer.AddFrame(frame, frameInfo)
    
    // 5. Apply A/V sync
    if synced := p.syncMgr.ProcessFrame(frame); synced != nil {
        p.output <- synced
    }
    
    return nil
}
```

### Codec Support

#### H.264/AVC
```go
// H.264 specific handling
type H264Depacketizer struct {
    // NAL unit assembly
    nalBuffer []byte
    // SPS/PPS tracking
    sps, pps []byte
}

// Features:
// - Annex B and AVCC format support
// - SPS/PPS extraction and caching
// - Access unit boundary detection
// - SEI message parsing
```

#### HEVC/H.265
```go
// HEVC specific handling
type HEVCDepacketizer struct {
    // VPS/SPS/PPS tracking
    vps, sps, pps []byte
    // Temporal layer support
    temporalID int
}

// Features:
// - Multi-layer support
// - HDR metadata preservation
// - Temporal scalability
// - Efficient NAL unit handling
```

#### AV1
```go
// AV1 specific handling
type AV1Depacketizer struct {
    // OBU parsing
    sequenceHeader []byte
    // Temporal unit assembly
    temporalUnit [][]byte
}

// Features:
// - OBU (Open Bitstream Unit) parsing
// - Scalability support
// - Film grain synthesis data
// - Screen content coding tools
```

## Buffer Management

### Ring Buffer System

```go
// Ring buffer for efficient streaming
type RingBuffer struct {
    data     []byte
    size     int64
    writePos int64
    readPos  int64
    
    // Metrics
    drops    int64
    latency  time.Duration
}

// Features:
// - Zero-copy operations where possible
// - Automatic overflow handling
// - Configurable size limits
// - Real-time metrics
```

### Buffer Pool

```go
// Reusable buffer allocation
type BufferPool struct {
    buffers sync.Pool
    size    int
    
    // Metrics
    allocated int64
    reused    int64
}

// Usage
buffer := pool.Get()
defer pool.Put(buffer)

// Write data
n, err := buffer.Write(data)

// Read data
data, err := buffer.Read(size)
```

### Backpressure Control

```go
// Backpressure controller
type BackpressureController struct {
    highWatermark float64 // Trigger backpressure
    lowWatermark  float64 // Release backpressure
    
    // Current state
    pressure     float64
    dropping     bool
}

// Decision making
func (c *BackpressureController) ShouldDrop(priority int) bool {
    if c.pressure > c.highWatermark {
        c.dropping = true
    } else if c.pressure < c.lowWatermark {
        c.dropping = false
    }
    
    // Drop low priority frames first
    return c.dropping && priority < CriticalPriority
}
```

## API Endpoints

### Stream Management

#### List Streams - `GET /api/v1/streams`
```json
{
  "streams": [
    {
      "id": "stream_123",
      "type": "srt",
      "source_addr": "192.168.1.100:42000",
      "status": "active",
      "video_codec": "hevc",
      "resolution": "1920x1080",
      "bitrate": 5000000,
      "frame_rate": 30.0
    }
  ],
  "count": 1,
  "time": "2024-01-20T15:30:45Z"
}
```

#### Get Stream - `GET /api/v1/streams/{id}`
```json
{
  "id": "stream_123",
  "type": "srt",
  "source_addr": "192.168.1.100:42000",
  "status": "active",
  "created_at": "2024-01-20T15:25:00Z",
  "last_heartbeat": "2024-01-20T15:30:40Z",
  "video_codec": "hevc",
  "resolution": "1920x1080",
  "bitrate": 5000000,
  "frame_rate": 30.0,
  "stats": {
    "bytes_received": 150000000,
    "packets_received": 100000,
    "packets_lost": 5,
    "bitrate": 5000000
  }
}
```

#### Stream Control

- **Pause Stream** - `POST /api/v1/streams/{id}/pause`
- **Resume Stream** - `POST /api/v1/streams/{id}/resume`
- **Delete Stream** - `DELETE /api/v1/streams/{id}`

### Statistics

#### System Stats - `GET /api/v1/stats`
```json
{
  "total_streams": 25,
  "active_streams": 23,
  "total_bitrate": 125000000,
  "cpu_usage": 45.2,
  "memory_usage": 3221225472,
  "uptime": 86400
}
```

#### Video Operations
- `GET /api/v1/video/preview/{id}` - Get preview data for a stream
- `GET /api/v1/video/buffer/{id}` - Access frame buffer for a stream

## Configuration

### Complete Configuration Example

```yaml
ingestion:
  # Connection limits
  max_connections: 25
  stream_timeout: 30s
  reconnect_interval: 5s
  
  # SRT configuration
  srt:
    enabled: true
    port: 30000
    latency: 120ms
    max_bandwidth: 60000000    # 60 Mbps
    payload_size: 1316         # MTU-friendly
    flow_window: 25600
    peer_idle_timeout: 30s
    
  # RTP configuration
  rtp:
    enabled: true
    port: 5004
    rtcp_port: 5005
    buffer_size: 2097152       # 2MB
    max_sessions: 30
    session_timeout: 30s
    
  # Buffer configuration
  buffer:
    ring_size: 4194304         # 4MB per stream
    pool_size: 30              # Pre-allocate for 30 streams
    write_timeout: 100ms
    read_timeout: 100ms
    
  # Memory management
  memory:
    max_total: 2684354560      # 2.5GB total (default)
    max_per_stream: 209715200  # 200MB per stream (default)
    gc_interval: 1m
    
  # Backpressure control (watermark-based)
  backpressure:
    enabled: true
    low_watermark: 0.25        # 25% — normal operation
    medium_watermark: 0.50     # 50% — start monitoring
    high_watermark: 0.75       # 75% — begin frame dropping
    critical_watermark: 0.90   # 90% — aggressive dropping
    
  # Frame processing
  frame:
    assembly_timeout: 5s
    max_frame_size: 10485760   # 10MB
    reorder_buffer: 100        # packets
    
  # GOP management
  gop:
    buffer_count: 3            # Keep 3 GOPs
    max_gop_size: 300          # frames
    force_idr_interval: 10s    # Force IDR if missing
    
  # A/V synchronization
  sync:
    enabled: true
    max_audio_drift: 40ms      # Max drift before correction
    correction_factor: 0.1     # 10% correction per iteration
    max_correction_step: 5ms   # Max single correction step
```

## Best Practices

### 1. Stream Handling

```go
// DO: Use context for cancellation
func (h *StreamHandler) Start(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case packet := <-h.input:
            if err := h.process(packet); err != nil {
                h.logger.WithError(err).Error("Processing failed")
                // Continue processing other packets
            }
        }
    }
}

// DON'T: Block on processing
func (h *StreamHandler) BadStart() error {
    for packet := range h.input {
        // This can't be cancelled!
        h.process(packet)
    }
}
```

### 2. Memory Management

```go
// DO: Use buffer pools
func (h *Handler) ProcessFrame(data []byte) error {
    buf := h.bufferPool.Get()
    defer h.bufferPool.Put(buf)
    
    // Use buffer
    buf.Write(data)
    return h.pipeline.Process(buf)
}

// DON'T: Allocate per frame
func (h *Handler) BadProcessFrame(data []byte) error {
    // Allocates new buffer every time!
    buf := make([]byte, len(data))
    copy(buf, data)
    return h.pipeline.Process(buf)
}
```

### 3. Error Recovery

```go
// DO: Implement circuit breaker
type StreamCircuitBreaker struct {
    failures    int
    maxFailures int
    resetAfter  time.Duration
    lastFailure time.Time
    state       State
}

func (cb *StreamCircuitBreaker) Call(fn func() error) error {
    if cb.state == Open {
        if time.Since(cb.lastFailure) > cb.resetAfter {
            cb.state = HalfOpen
        } else {
            return ErrCircuitOpen
        }
    }
    
    err := fn()
    if err != nil {
        cb.recordFailure()
    } else {
        cb.reset()
    }
    
    return err
}
```

### 4. Monitoring

```go
// DO: Track comprehensive metrics
type StreamMetrics struct {
    // Counters
    packetsReceived  prometheus.Counter
    framesAssembled  prometheus.Counter
    framesDropped    prometheus.Counter
    
    // Gauges
    activeStreams    prometheus.Gauge
    bufferUsage      prometheus.Gauge
    
    // Histograms
    frameLatency     prometheus.Histogram
    processingTime   prometheus.Histogram
}

// Update metrics
func (m *StreamMetrics) RecordFrame(frame Frame, duration time.Duration) {
    m.framesAssembled.Inc()
    m.frameLatency.Observe(frame.Latency.Seconds())
    m.processingTime.Observe(duration.Seconds())
}
```

## Testing

### Unit Tests

```go
func TestFrameAssembler(t *testing.T) {
    assembler := frame.NewAssembler(frame.Config{
        Timeout: 5 * time.Second,
        MaxSize: 1024 * 1024,
    })
    
    // Test single packet frame
    packet := &rtp.Packet{
        Header: rtp.Header{
            Marker:        true,
            SequenceNumber: 1,
        },
        Payload: []byte{0x01, 0x02, 0x03},
    }
    
    frame, complete := assembler.AddPacket(packet)
    assert.True(t, complete)
    assert.Equal(t, []byte{0x01, 0x02, 0x03}, frame.Data)
    
    // Test multi-packet frame
    packets := createMultiPacketFrame(t)
    var finalFrame *Frame
    
    for _, pkt := range packets {
        frame, complete = assembler.AddPacket(pkt)
        if complete {
            finalFrame = frame
            break
        }
    }
    
    assert.NotNil(t, finalFrame)
    assert.Equal(t, expectedData, finalFrame.Data)
}
```

### Integration Tests

```go
func TestSRTIngestion(t *testing.T) {
    // Start test SRT server
    manager := setupTestManager(t)
    defer manager.Stop()
    
    // Connect SRT client
    client, err := srt.Dial("srt://localhost:30000", srt.Config{
        StreamID: "test_stream",
        Latency:  120 * time.Millisecond,
    })
    require.NoError(t, err)
    defer client.Close()
    
    // Send test data
    testData := generateTestMPEGTS(t)
    _, err = client.Write(testData)
    require.NoError(t, err)
    
    // Wait for processing
    time.Sleep(100 * time.Millisecond)
    
    // Verify stream registered
    stream, err := manager.GetStream(context.Background(), "test_stream")
    require.NoError(t, err)
    assert.Equal(t, "active", stream.Status)
    assert.Equal(t, "h264", stream.VideoCodec)
}
```

### Stress Tests

```go
func TestIngestionStress(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping stress test")
    }
    
    manager := setupTestManager(t)
    defer manager.Stop()
    
    // Start multiple streams
    const numStreams = 25
    clients := make([]*srt.Client, numStreams)
    
    for i := 0; i < numStreams; i++ {
        client, err := srt.Dial("srt://localhost:30000", srt.Config{
            StreamID: fmt.Sprintf("stream_%d", i),
        })
        require.NoError(t, err)
        clients[i] = client
        
        // Start sending data
        go sendContinuousData(t, client, 50*1024*1024/8) // 50 Mbps
    }
    
    // Run for 1 minute
    time.Sleep(1 * time.Minute)
    
    // Check metrics
    stats := manager.GetStats(context.Background())
    assert.Equal(t, numStreams, stats.ActiveStreams)
    assert.Zero(t, stats.FailedStreams)
    
    // Clean up
    for _, client := range clients {
        client.Close()
    }
}
```

## Examples

### Complete Ingestion Setup

```go
func setupIngestion(cfg *config.Config, logger logger.Logger) (*ingestion.Manager, error) {
    // Create ingestion manager (creates Redis client internally from config)
    manager, err := ingestion.NewManager(&cfg.Ingestion, logger)
    if err != nil {
        return nil, fmt.Errorf("failed to create ingestion manager: %w", err)
    }

    // Start ingestion
    if err := manager.Start(); err != nil {
        return nil, fmt.Errorf("failed to start ingestion: %w", err)
    }

    return manager, nil
}
```

### Custom Stream Handler

```go
// Implement custom processing logic
type CustomStreamHandler struct {
    *ingestion.StreamHandler
    
    // Custom fields
    transcoder *ffmpeg.Transcoder
    analyzer   *video.Analyzer
}

func (h *CustomStreamHandler) ProcessFrame(frame *Frame) error {
    // Analyze frame
    analysis := h.analyzer.Analyze(frame)
    
    // Make transcoding decisions
    if analysis.SceneChange {
        h.transcoder.ForceKeyframe()
    }
    
    // Forward to base handler
    return h.StreamHandler.ProcessFrame(frame)
}

// Register custom handler
manager.RegisterHandlerFactory(func(config StreamConfig) StreamHandler {
    return &CustomStreamHandler{
        StreamHandler: ingestion.NewStreamHandler(config),
        transcoder:    ffmpeg.NewTranscoder(config),
        analyzer:      video.NewAnalyzer(),
    }
})
```

## Related Documentation

- [Main README](../../README.md) - Project overview
- [Buffer Management](buffer/README.md) - Buffer subsystem details
- [Codec Support](codec/README.md) - Codec-specific implementations
- [Frame Processing](frame/README.md) - Frame assembly and detection
- [GOP Management](gop/README.md) - GOP buffering strategies
- [A/V Synchronization](sync/README.md) - Synchronization algorithms
- [Phase 2 Documentation](../../docs/phase-2.md) - Implementation details
