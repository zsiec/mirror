# Mirror Streaming Platform - Part 1: Overview and Architecture

## Table of Contents
1. [Project Overview](#project-overview)
2. [System Architecture](#system-architecture)
3. [Technology Stack](#technology-stack)
4. [Project Structure](#project-structure)
5. [Data Flow](#data-flow)
6. [Performance Targets](#performance-targets)

## Project Overview

Mirror is a high-performance streaming platform designed to:
- Ingest 25 concurrent 50Mbps HEVC streams via SRT/RTP
- Transcode to H.264 400kbps streams for multi-view consumption
- Deliver via Low-Latency HLS (LL-HLS) with sub-second latency
- Support 5,000 concurrent viewers
- Provide DVR functionality for recorded content
- Offer real-time monitoring and admin controls

### Key Design Principles
- **Single Binary Architecture**: All components in one deployable unit
- **Hardware Acceleration**: NVIDIA NVENC for efficient transcoding
- **Ultra-Low Latency**: CMAF chunks with HTTP/3 push
- **Operational Simplicity**: Minimal external dependencies

## System Architecture

### High-Level Architecture
```
┌─────────────────┐     ┌─────────────────────────────────────────┐     ┌─────────────┐
│  Stream Sources │     │           Mirror Core Binary            │     │   Viewers   │
│  (SRT/RTP)      │────▶│  Ingestion → Transcode → Package → API │────▶│  (LL-HLS)   │
└─────────────────┘     └──────────────┬──────────────────────────┘     └─────────────┘
                                       │
                        ┌──────────────┴──────────────┐
                        │     Supporting Services      │
                        ├──────────────────────────────┤
                        │ • Redis (Session State)     │
                        │ • S3 (Segments/DVR)         │
                        │ • Prometheus (Metrics)      │
                        └──────────────────────────────┘
```

### Component Architecture
```
mirror (single binary)
├── Stream Ingestion Service
│   ├── SRT Listener (port 6000)
│   ├── RTP Listener (port 5004)
│   └── Stream Registry
├── Transcoding Service
│   ├── NVENC Pipeline Manager
│   ├── HEVC → H.264 Transcoder
│   └── GPU Resource Pool
├── HLS Packaging Service
│   ├── CMAF Segmenter
│   ├── Playlist Generator
│   └── HTTP/3 Push Manager
├── Storage Service
│   ├── S3 Uploader
│   ├── DVR Recorder
│   └── Segment Cache
├── Admin API Service
│   ├── Stream Management
│   ├── Metrics Endpoint
│   └── Control Interface
└── HTTP/3 Server
    ├── LL-HLS Endpoint
    ├── Admin Dashboard
    └── Prometheus Metrics
```

## Technology Stack

### Core Libraries
```go
// Stream Ingestion
github.com/datarhei/gosrt      // SRT protocol
github.com/pion/rtp            // RTP handling

// Video Processing
github.com/asticode/go-astiav  // FFmpeg bindings with NVENC
github.com/edsrzf/mmap-go      // Memory-mapped files

// HLS Generation
github.com/etherlabsio/go-m3u8 // HLS playlist generation
github.com/Eyevinn/mp4ff       // CMAF/fMP4 manipulation

// HTTP/3 Server
github.com/quic-go/quic-go     // HTTP/3 implementation

// Storage
github.com/minio/minio-go/v7   // S3-compatible storage

// State Management
github.com/redis/go-redis/v9   // Redis client

// Monitoring
github.com/prometheus/client_golang // Metrics

// Utilities
github.com/rs/zerolog          // Structured logging
github.com/spf13/viper         // Configuration
github.com/spf13/cobra         // CLI framework
```

## Project Structure

```
github.com/zsiec/mirror/
├── cmd/
│   └── mirror/
│       └── main.go              # Application entry point
├── internal/                    # Private application code
│   ├── config/
│   │   └── config.go           # Configuration management
│   ├── ingestion/
│   │   ├── srt.go              # SRT stream ingestion
│   │   ├── rtp.go              # RTP stream ingestion
│   │   └── registry.go         # Stream registry
│   ├── transcoding/
│   │   ├── pipeline.go         # Transcoding pipeline
│   │   ├── nvenc.go            # NVIDIA encoder wrapper
│   │   └── pool.go             # GPU resource pool
│   ├── packaging/
│   │   ├── segmenter.go        # CMAF segmentation
│   │   ├── playlist.go         # HLS playlist generation
│   │   └── push.go             # HTTP/3 push logic
│   ├── storage/
│   │   ├── s3.go               # S3 operations
│   │   ├── dvr.go              # DVR recording
│   │   └── cache.go            # Local segment cache
│   ├── api/
│   │   ├── admin.go            # Admin API handlers
│   │   ├── streaming.go        # Streaming endpoints
│   │   └── metrics.go          # Prometheus metrics
│   └── server/
│       ├── http3.go            # HTTP/3 server
│       └── middleware.go       # Common middleware
├── pkg/                        # Public packages
│   ├── models/
│   │   └── stream.go           # Shared data models
│   └── utils/
│       ├── buffer_pool.go      # Memory management
│       └── errors.go           # Error types
├── web/                        # Web assets
│   ├── admin/                  # Admin dashboard
│   └── player/                 # Example player
├── deployments/
│   ├── docker/
│   │   └── Dockerfile          # Single container build
│   ├── k8s/                    # Kubernetes manifests
│   └── terraform/              # AWS infrastructure
├── configs/
│   └── mirror.yaml             # Default configuration
├── scripts/
│   ├── build.sh                # Build script
│   └── test-stream.sh          # Test streaming setup
├── go.mod
├── go.sum
└── README.md
```

## Data Flow

### 1. Stream Ingestion Flow
```
SRT/RTP Input (50 Mbps HEVC)
    ↓
Stream Ingestion Service
    ├── Connection validation
    ├── Stream registration
    └── Buffer management
    ↓
In-Memory Ring Buffer (per stream)
    ↓
Transcoding Service
```

### 2. Transcoding Flow
```
HEVC Input Stream
    ↓
GPU Resource Allocation
    ↓
NVENC Hardware Encoder
    ├── Decode HEVC
    ├── Scale to 640x360
    └── Encode H.264 @ 400kbps
    ↓
Output Buffer
    ↓
Packaging Service
```

### 3. Packaging Flow
```
H.264 Stream
    ↓
CMAF Segmenter
    ├── 500ms segments
    └── 100ms chunks (5 per segment)
    ↓
Parallel Operations:
    ├── S3 Upload (async)
    ├── Local Cache (mmap)
    ├── DVR Recording
    └── Playlist Update
    ↓
HTTP/3 Server
```

### 4. Delivery Flow
```
Client Request
    ↓
HTTP/3 Server
    ├── Check local cache
    ├── Serve playlist/segment
    └── Push next segments
    ↓
Client Buffer
```

## Performance Targets

### Latency Breakdown
- **Ingestion Buffer**: 100-200ms
- **Transcoding**: 50-100ms
- **Segmentation**: 10-20ms
- **Network Delivery**: 200-500ms
- **Total Glass-to-Glass**: 500-800ms

### Resource Requirements
- **CPU**: 16 cores (stream management, API)
- **RAM**: 32GB (25 streams × 1GB buffer + overhead)
- **GPU**: 2× NVIDIA RTX 4090 or 1× A6000
- **Network**: 10 Gbps (1.25 Gbps in + 2 Gbps out)
- **Storage**: 1TB NVMe for cache + S3 for archive

### Scaling Metrics
- **Streams**: 25 concurrent @ 50 Mbps input
- **Viewers**: 5,000 concurrent
- **Segments**: 50,000/minute generated
- **Bandwidth**: 2.4 Mbps total for 6-stream view

## Next Steps

Continue to:
- [Part 2: Core Implementation](plan-2.md) - Main application structure
- [Part 3: Stream Ingestion](plan-3.md) - SRT/RTP implementation
- [Part 4: HLS Packaging](plan-4.md) - LL-HLS and CMAF details
- [Part 5: Admin API](plan-5.md) - Control and monitoring
- [Part 6: Infrastructure](plan-6.md) - AWS deployment
- [Part 7: Development](plan-7.md) - Local setup and CI/CD