# Stream Ingestion System

This package implements a comprehensive video stream ingestion system supporting SRT and RTP protocols with advanced video processing capabilities.

## Architecture Overview

The ingestion system follows a layered architecture:

```
Protocol Layer (SRT/RTP)
    |
Connection Adapter Layer
    |
Buffer Management Layer
    |
Video Processing Pipeline
    ├── Codec Detection
    ├── Frame Assembly
    ├── GOP Management
    └── A/V Synchronization
    |
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
- Backpressure integration (`manager_backpressure.go`)

### Stream Handler (`stream_handler.go`)
Manages individual stream processing:
- Coordinates video pipeline components
- Handles backpressure signals
- Manages memory allocation
- Provides per-stream statistics
- Implements recovery mechanisms

### Connection Adapters
- `srt_connection_adapter.go`: Adapts SRT connections to common interface
- `rtp_connection_adapter.go`: Adapts RTP connections to common interface

### Codec Detection (`codec_detection.go`)
Automatic codec identification from raw packet data.

## Subdirectories

### Protocol Implementations

#### SRT (`srt/`)
- **Listener** (`listener.go`): Accepts incoming SRT connections
- **Connection** (`connection.go`): Handles individual SRT streams
- MPEG-TS parsing, stream ID extraction, bitrate monitoring, connection recovery

#### RTP (`rtp/`)
- **Listener** (`listener.go`): UDP listener for RTP packets
- **Session** (`session.go`): Manages RTP stream state
- **Validator** (`validator.go`): Packet validation and sequencing
- SSRC-based stream identification, sequence number validation, jitter buffering

### Video Processing

#### Buffer Management (`buffer/`)
- Ring buffer (`ring.go`): Thread-safe circular buffer with bitrate calculation
- Sized buffer (`sized.go`): Size-limited buffer with backpressure signaling
- Buffer pool (`pool.go`): Reusable buffer allocation for reduced GC pressure

#### Codec Support (`codec/`)
Depacketizers for each supported codec:
- **H.264/AVC**: NAL unit reassembly, STAP-A/FU-A handling
- **H.265/HEVC**: NAL unit reassembly with layer support
- **AV1**: OBU parsing and reassembly
- **JPEG-XS**: Low-latency codec support

#### Frame Assembly (`frame/`)
- Frame assembler (`assembler.go`): Packet ordering, timeout handling, boundary detection
- Codec-specific frame boundary detectors (NAL unit parsing, OBU parsing, marker bits)

#### GOP Management (`gop/`)
- GOP buffer (`buffer.go`): Complete GOPs in memory, indexed access, duration tracking
- GOP detector (`detector.go`): Boundary detection, open/closed GOP handling

#### A/V Synchronization (`sync/`)
- Sync manager (`manager.go`): Multi-stream tracking, drift calculation, corrections
- Track sync (`track_sync.go`): Per-track PTS/DTS handling, drift detection

#### Resolution Detection (`resolution/`)
- Video resolution detection from bitstream analysis
- H.264 SPS parsing (`h264_parser.go`) and HEVC SPS parsing (`hevc_parser.go`)
- Exp-Golomb decoding (`golomb.go`)

#### MPEG-TS Parsing (`mpegts/`)
- MPEG-TS packet parser for SRT streams
- PES extraction, parameter set detection
- Timestamp safety validation

#### Types (`types/`)
Shared type definitions used across the ingestion system:
- Codec types and detection (`codec.go`)
- Frame types (`frame.go`, `simplified_frame.go`)
- GOP types (`gop.go`)
- Packet types (`packet.go`)
- Parameter set management (`parameter_context.go`, `parameter_cache.go`, `parameter_validation.go`)
- H.264 bitstream parsing (`h264_parser.go`, `rbsp.go`, `nal_header_fix.go`)
- Encoder session tracking (`encoder_session.go`)
- Rational number type for timebase math (`rational.go`)

#### Validation (`validation/`)
Stream integrity validation:
- PES validation (`pes.go`)
- PTS/DTS validation (`pts_dts_validator.go`)
- Continuity counter checking (`continuity.go`)
- Alignment validation (`alignment.go`)
- Fragment validation (`fragment.go`)

#### Timestamp Mapping (`timestamp/`)
- Timestamp mapper for timebase conversion between protocols

### Flow Control

#### Backpressure (`backpressure/`)
Watermark-based backpressure control:
- Monitor downstream capacity
- Signal upstream to slow down
- GOP-aware frame dropping
- Configurable watermarks (25/50/75/90%)

#### Memory Management (`memory/`)
Controls memory usage:
- Global memory limits (8GB default)
- Per-stream allocations (400MB default)
- Memory pressure monitoring and eviction

#### Rate Limiting (`ratelimit/`)
Connection and bandwidth rate limiting.

### Error Handling and Recovery

#### Recovery (`recovery/`)
- Automatic retry with exponential backoff (`recovery.go`)
- Smart recovery with state awareness (`smart_recovery.go`)
- Stream-level recovery (`stream_recovery.go`)
- Frame recovery (`frame_recovery.go`)
- Adaptive quality degradation (`adaptive_quality.go`)

#### Reconnection (`reconnect/`)
Automatic reconnection with configuration preservation.

### Monitoring and Integrity

#### Monitoring (`monitoring/`)
- Health monitor (`health_monitor.go`)
- Alert definitions (`alerts.go`)
- Corruption detection (`corruption_detector.go`)
- Type definitions (`types.go`)

#### Integrity (`integrity/`)
- Checksum validation (`checksum.go`)
- Health scoring (`health_scorer.go`)
- Stream validation (`stream_validator.go`)

#### Security (`security/`)
- LEB128 parsing with safety limits (`leb128.go`)
- Size limit enforcement (`limits.go`)

#### Diagnostics (`diagnostics/`)
Stream diagnostics (currently empty, placeholder for future use).

### Pipeline (`pipeline/`)
Video processing pipeline orchestration — coordinates the flow from packet input through frame assembly, GOP building, and output.

### Test Support

#### Test Data (`testdata/`)
- Test helpers (`helpers.go`)
- HEVC test data generator (`hevc_generator.go`)

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

Key configuration parameters (in `configs/default.yaml` under `ingestion:`):

```yaml
ingestion:
  queue_dir: "/tmp/mirror/queue"
  srt:
    port: 1234
    max_connections: 25
    latency: 120ms
    max_bandwidth: 52428800  # 50 Mbps
  rtp:
    port: 5004
    max_sessions: 25
    session_timeout: 10s
  buffer:
    ring_size: 4194304       # 4MB per stream
    pool_size: 50
  memory:
    max_total: 8589934592    # 8GB
    max_per_stream: 419430400  # 400MB
  stream_handling:
    frame_assembly_timeout: 200ms
    gop_buffer_size: 15
    max_gop_age: 5s
  backpressure:
    low_watermark: 0.25
    high_watermark: 0.75
    critical_watermark: 0.9
```

## Testing

The package includes comprehensive tests at multiple levels:
- Unit tests per component
- Integration tests (`backpressure_integration_test.go`)
- Race condition tests (`stream_handler_backpressure_race_test.go`, `frame_copy_race_test.go`)
- Memory tests (`stream_handler_memory_test.go`)
- Corruption tests (`stream_handler_corruption_test.go`)
- PTS wraparound tests (`pts_wraparound_test.go`)
- B-frame reordering tests (`srt_bframe_dts_test.go`, `srt_bframe_simple_test.go`)
