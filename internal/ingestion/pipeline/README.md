# Video Pipeline Package

This package implements the core video processing pipeline for the Mirror streaming platform. It provides a comprehensive video-aware processing chain that handles frame assembly, GOP management, A/V synchronization, and output delivery.

## Overview

The video pipeline processes video data through multiple stages:

1. **Packet Input**: Receives raw packets from protocol adapters
2. **Frame Assembly**: Reconstructs complete frames from packets
3. **B-frame Reordering**: Handles display order correction
4. **GOP Buffering**: Groups frames into Groups of Pictures
5. **A/V Synchronization**: Ensures audio/video timing alignment
6. **Output Queue**: Delivers processed data to consumers

## Architecture

```
Protocol Layer (SRT/RTP)
    ↓
┌─────────────────┐
│ Video Pipeline  │
├─────────────────┤
│ ┌─────────────┐ │
│ │Frame        │ │ ← Packet Input
│ │Assembler    │ │
│ └─────────────┘ │
│         ↓       │
│ ┌─────────────┐ │
│ │B-frame      │ │ ← Display Order
│ │Reorderer    │ │
│ └─────────────┘ │
│         ↓       │
│ ┌─────────────┐ │
│ │GOP Buffer   │ │ ← Group Management
│ │             │ │
│ └─────────────┘ │
│         ↓       │
│ ┌─────────────┐ │
│ │Output Queue │ │ ← Delivery
│ │             │ │
│ └─────────────┘ │
└─────────────────┘
    ↓
Output Consumers
```

## Key Components

### Frame Assembler
- **Purpose**: Reconstructs complete frames from packet fragments
- **Features**: Timeout handling, packet ordering, codec-aware assembly
- **Integration**: Uses codec-specific depacketizers

### B-frame Reorderer  
- **Purpose**: Corrects display order for B-frames in H.264/HEVC
- **Features**: Configurable depth, timeout-based frame release
- **Performance**: Minimal latency for real-time streaming

### GOP Buffer
- **Purpose**: Maintains complete Groups of Pictures for clean switching
- **Features**: Keyframe detection, duration tracking, memory management
- **Usage**: Critical for adaptive bitrate and stream switching

### Output Queue
- **Purpose**: Thread-safe delivery of processed frames
- **Features**: Backpressure handling, memory limits, flow control
- **Integration**: Connects to HLS packager and other consumers

## Configuration

```go
type PipelineConfig struct {
    // Frame assembly configuration
    FrameTimeout    time.Duration  // Maximum time to wait for complete frame
    MaxFrameSize    int           // Maximum frame size in bytes
    
    // B-frame reordering
    BFrameDepth     int           // Maximum B-frame reorder depth
    ReorderTimeout  time.Duration // Timeout for frame reordering
    
    // GOP management
    GOPBufferSize   int           // Number of GOPs to buffer
    MaxGOPDuration  time.Duration // Maximum GOP duration
    
    // Memory management
    MaxMemoryUsage  int64         // Maximum memory usage per pipeline
    
    // Performance tuning
    WorkerCount     int           // Number of processing workers
}
```

## Usage

### Basic Pipeline Creation

```go
import (
    "github.com/zsiec/mirror/internal/ingestion/pipeline"
    "github.com/zsiec/mirror/internal/ingestion/types"
)

// Create pipeline configuration
config := pipeline.PipelineConfig{
    FrameTimeout:   5 * time.Second,
    MaxFrameSize:   1024 * 1024, // 1MB
    BFrameDepth:    3,
    ReorderTimeout: 100 * time.Millisecond,
    GOPBufferSize:  3,
    MaxMemoryUsage: 100 * 1024 * 1024, // 100MB
}

// Create video source (from protocol adapter)
source := &VideoSource{
    PacketChannel: packetChan,
    Codec:        types.CodecH264,
}

// Create pipeline
pipeline, err := pipeline.NewVideoPipeline(ctx, config, source)
if err != nil {
    return fmt.Errorf("failed to create pipeline: %w", err)
}

// Start processing
if err := pipeline.Start(); err != nil {
    return fmt.Errorf("failed to start pipeline: %w", err)
}
```

### Processing Flow

```go
// Input: Packets from protocol layer
packet := &types.Packet{
    Data:      packetData,
    Timestamp: pts,
    Codec:     types.CodecH264,
    IsKey:     isKeyframe,
}

// Pipeline processes automatically:
// 1. Frame assembly
// 2. B-frame reordering  
// 3. GOP buffering
// 4. Output delivery

// Output: Complete frames in display order
frame := <-pipeline.OutputChannel()
```

## Video-Aware Features

### Codec-Specific Processing
- **H.264/HEVC**: NAL unit handling, B-frame reordering, SPS/PPS tracking
- **AV1**: OBU processing, temporal scalability
- **JPEG-XS**: Low-latency frame assembly

### Timing Management
- **PTS/DTS Handling**: Proper presentation/decode timestamp management
- **Drift Correction**: Automatic timing drift detection and correction
- **Synchronization**: A/V sync maintenance with configurable thresholds

### Memory Management
- **Adaptive Sizing**: Buffer sizes adjust based on content complexity
- **Pressure Monitoring**: Backpressure signaling when limits approached
- **Garbage Collection**: Efficient memory reuse and cleanup

## Performance Characteristics

### Throughput
- **H.264 1080p**: 60 FPS with 2ms average latency
- **HEVC 4K**: 30 FPS with 5ms average latency  
- **AV1**: Variable based on complexity

### Memory Usage
- **Frame Buffers**: ~10MB per 1080p stream
- **GOP Buffers**: ~30MB per stream (3 GOP default)
- **Working Memory**: ~5MB per stream

### CPU Usage
- **Single Stream**: 5-10% CPU (modern hardware)
- **Multiple Streams**: Scales linearly with worker count

## Error Handling

### Frame Assembly Errors
- **Missing Packets**: Timeout-based frame completion
- **Corruption**: Frame validation and dropping
- **Sequence Gaps**: Automatic resynchronization

### Memory Pressure
- **Frame Dropping**: Intelligent B-frame dropping first
- **Buffer Reduction**: Dynamic buffer size adjustment
- **Flow Control**: Backpressure to upstream components

### Timing Issues
- **Clock Drift**: Automatic correction algorithms
- **Timestamp Jumps**: Detection and recovery
- **Sync Loss**: A/V resynchronization procedures

## Monitoring and Metrics

### Pipeline Statistics
```go
stats := pipeline.GetStats()
fmt.Printf("Frames processed: %d\n", stats.FramesProcessed)
fmt.Printf("B-frames reordered: %d\n", stats.BFramesReordered)
fmt.Printf("GOP count: %d\n", stats.GOPCount)
fmt.Printf("Memory usage: %d bytes\n", stats.MemoryUsage)
```

### Key Metrics
- **Frame assembly rate**: Frames per second processed
- **Reorder depth**: Average B-frame reordering depth
- **Buffer utilization**: GOP buffer usage percentage
- **Error rates**: Frame drops, timeouts, corruption

## Integration Points

### Stream Handler Integration
```go
// Pipeline provides data to stream handler
pipeline.SetOutputHandler(func(frame *types.Frame) {
    streamHandler.ProcessFrame(frame)
})
```

### Transcoding Integration
```go
// Pipeline outputs feed into transcoder
transcoder.SetInputSource(pipeline.OutputChannel())
```

### HLS Packaging Integration
```go
// GOP boundaries used for segment creation
pipeline.SetGOPHandler(func(gop *types.GOP) {
    hlsPackager.CreateSegment(gop)
})
```

## Testing

### Unit Tests
```bash
# Test individual components
go test ./internal/ingestion/pipeline/...

# Test with race detection
go test -race ./internal/ingestion/pipeline/...
```

### Integration Tests
```bash
# Test full pipeline with real data
go test -tags=integration ./internal/ingestion/pipeline/...
```

### Performance Tests
```bash
# Benchmark pipeline performance
go test -bench=. ./internal/ingestion/pipeline/...
```

## Best Practices

### Configuration Guidelines
1. **Frame Timeout**: 5-10 seconds for live streams
2. **B-frame Depth**: Match encoder settings (typically 2-4)
3. **GOP Buffer**: 2-3 GOPs for smooth switching
4. **Memory Limits**: 100-400MB per stream depending on resolution

### Error Recovery
1. Always implement timeout handling
2. Monitor memory usage continuously
3. Implement graceful degradation under pressure
4. Log errors with context for debugging

### Performance Optimization
1. Use worker pools for CPU-intensive operations
2. Implement memory pooling for frequent allocations
3. Monitor and tune buffer sizes based on content
4. Consider NUMA awareness for multi-socket systems

## Future Enhancements

- **GPU Acceleration**: Hardware-accelerated frame processing
- **ML Integration**: Intelligent quality assessment
- **Advanced Sync**: Multi-stream synchronization
- **Cloud Integration**: Distributed pipeline processing
