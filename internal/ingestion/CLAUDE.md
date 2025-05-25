# Stream Ingestion System

This package implements a comprehensive video stream ingestion system supporting SRT and RTP protocols with advanced video processing capabilities.

## Architecture Overview

The ingestion system follows a layered architecture:

```
Protocol Layer (SRT/RTP)
    ↓
Connection Adapter Layer
    ↓
Buffer Management Layer
    ↓
Video Processing Pipeline
    ├── Codec Detection
    ├── Frame Assembly
    ├── GOP Management
    └── A/V Synchronization
    ↓
Output Queue Layer
```

## Core Components

### Manager (`manager.go`)
Central orchestrator for all ingestion operations:
- Manages protocol listeners (SRT, RTP)
- Handles stream lifecycle (creation, deletion, pause/resume)
- Coordinates between components
- Enforces connection limits
- Provides system-wide statistics

### Stream Handler (`stream_handler.go`)
Manages individual stream processing:
- Coordinates video pipeline components
- Handles backpressure signals
- Manages memory allocation
- Provides per-stream statistics
- Implements recovery mechanisms

### Video Pipeline (`pipeline/video_pipeline.go`)
Processes video data through stages:
1. **Packet Input**: Receives raw packets from protocol layer
2. **Frame Assembly**: Reconstructs complete frames
3. **GOP Building**: Groups frames into GOPs
4. **Output Queue**: Delivers processed data

## Protocol Implementations

### SRT (Secure Reliable Transport)
- **Listener** (`srt/listener.go`): Accepts incoming SRT connections
- **Connection** (`srt/connection.go`): Handles individual SRT streams
- **Features**:
  - MPEG-TS parsing
  - Automatic stream ID extraction
  - Bitrate monitoring
  - Connection recovery

### RTP (Real-time Transport Protocol)
- **Listener** (`rtp/listener.go`): UDP listener for RTP packets
- **Session** (`rtp/session.go`): Manages RTP stream state
- **Validator** (`rtp/validator.go`): Packet validation and sequencing
- **Features**:
  - SSRC-based stream identification
  - Sequence number validation
  - Jitter buffer management
  - RTCP support (future)

## Video Processing Components

### Buffer Management (`buffer/`)
Provides efficient, video-aware buffering:

#### Ring Buffer (`ring.go`)
- Thread-safe circular buffer
- Zero-copy operations where possible
- Automatic size management
- Bitrate calculation

#### Sized Buffer (`sized.go`)
- Size-limited buffer wrapper
- Backpressure signaling
- Overflow protection

#### Buffer Pool (`pool.go`)
- Reusable buffer allocation
- Memory efficiency
- Reduced GC pressure

### Codec Support (`codec/`)
Handles multiple video codecs with automatic detection:

#### Supported Codecs
- **H.264/AVC**: Most common, wide compatibility
- **H.265/HEVC**: Higher efficiency, 4K/8K support
- **AV1**: Next-gen codec, better compression
- **JPEG-XS**: Low-latency, high-quality

#### Depacketizers
Each codec has a specialized depacketizer:
- Handles codec-specific packetization
- Reconstructs access units
- Extracts timing information
- Preserves codec parameters

### Frame Assembly (`frame/`)
Reconstructs complete frames from packets:

#### Frame Assembler (`assembler.go`)
- Packet ordering and buffering
- Timeout handling for missing packets
- Frame boundary detection
- Codec-agnostic interface

#### Frame Detectors
Codec-specific frame boundary detection:
- NAL unit parsing (H.264/HEVC)
- OBU parsing (AV1)
- Marker bit detection (JPEG-XS)

### GOP Management (`gop/`)
Groups frames into GOPs for efficient processing:

#### GOP Buffer (`buffer.go`)
- Maintains complete GOPs in memory
- Indexed frame access
- Duration tracking
- Smart frame dropping

#### GOP Detector (`detector.go`)
- Identifies GOP boundaries
- Tracks GOP structure
- Handles open/closed GOPs

### A/V Synchronization (`sync/`)
Ensures audio and video alignment:

#### Sync Manager (`manager.go`)
- Tracks multiple streams
- Calculates drift
- Applies corrections

#### Track Sync (`track_sync.go`)
- Per-track timing management
- PTS/DTS handling
- Drift detection algorithms

#### Drift Correction
- Threshold-based detection
- Gradual correction
- Frame dropping/duplication

## Flow Control

### Backpressure (`backpressure/`)
Prevents system overload:
- Monitor downstream capacity
- Signal upstream to slow down
- GOP-aware frame dropping
- Maintains video quality

### Memory Management (`memory/`)
Controls memory usage:
- Global memory limits
- Per-stream allocations
- Memory pressure monitoring
- Graceful degradation

### Rate Limiting (`ratelimit/`)
Controls connection and bandwidth:
- Connection rate limiting
- Bandwidth throttling
- Fair resource allocation

## Error Handling and Recovery

### Recovery System (`recovery/`)
Handles errors gracefully:
- Automatic retry logic
- Exponential backoff
- State preservation
- Metric tracking

### Reconnection (`reconnect/`)
Manages connection failures:
- Automatic reconnection
- Configuration preservation
- Seamless stream resumption

## Metrics and Monitoring

The ingestion system provides comprehensive metrics:

### Stream Metrics
- Active stream count
- Packets/frames/GOPs processed
- Bitrate (input/output)
- Codec distribution
- Error rates

### Performance Metrics
- Processing latency
- Memory usage
- CPU utilization
- Queue depths

### Quality Metrics
- Frame drops
- Sync drift
- Recovery attempts
- Connection stability

## API Endpoints

### Stream Management
- `GET /api/v1/streams` - List all streams
- `GET /api/v1/streams/{id}` - Get stream details
- `DELETE /api/v1/streams/{id}` - Stop a stream
- `POST /api/v1/streams/{id}/pause` - Pause ingestion
- `POST /api/v1/streams/{id}/resume` - Resume ingestion

### Statistics
- `GET /api/v1/stats` - System statistics
- `GET /api/v1/stats/streams/{id}` - Stream statistics

### Video Operations
- `GET /api/v1/video/preview/{id}` - Get preview data
- `GET /api/v1/video/buffer/{id}` - Access frame buffer

### Synchronization
- `GET /api/v1/sync/status/{id}` - Sync status
- `POST /api/v1/sync/adjust/{id}` - Manual sync adjustment

## Configuration

Key configuration parameters:

```yaml
ingestion:
  srt_port: 30000              # SRT listener port
  rtp_port: 5004               # RTP listener port
  max_connections: 25          # Maximum concurrent streams
  buffer_size: 1048576         # Ring buffer size (1MB)
  gop_buffer_size: 3           # Number of GOPs to buffer
  frame_timeout: 5s            # Frame assembly timeout
  sync_threshold: 100ms        # A/V sync threshold
  memory:
    global_limit: 8GB          # Total memory limit
    per_stream_limit: 400MB    # Per-stream limit
```

## Testing

The package includes comprehensive tests:

### Unit Tests
- Component isolation
- Mock interfaces
- Edge case coverage
- Concurrent operations

### Integration Tests
- Protocol testing (SRT/RTP)
- Pipeline validation
- Memory limit enforcement
- Recovery scenarios

### Stress Tests
- High packet rates
- Memory pressure
- Concurrent streams
- Network failures

## Best Practices

### Stream Handling
1. Always check stream state before operations
2. Handle backpressure signals promptly
3. Monitor memory usage continuously
4. Log with stream context

### Error Recovery
1. Use exponential backoff for retries
2. Preserve stream state during recovery
3. Emit metrics for all failures
4. Implement circuit breakers

### Performance
1. Use buffer pools for allocations
2. Minimize lock contention
3. Batch operations where possible
4. Profile regularly

## Future Enhancements

### Protocol Support
- WebRTC ingestion
- RTMP fallback
- Custom protocols

### Video Features
- B-frame reordering
- Advanced sync algorithms
- ML-based quality optimization
- Real-time analytics

### Scalability
- Distributed ingestion
- Stream migration
- Load balancing
- Horizontal scaling
