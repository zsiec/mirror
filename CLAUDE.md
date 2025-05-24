# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go-based backend project for high-performance video streaming, designed to handle:
- 25 concurrent SRT/RTP streams at 50mbps with HEVC input
- Transcoding to HLS-compatible formats with hardware acceleration
- Broadcasting to 5,000 concurrent viewers using Low-Latency HLS (LL-HLS)
- Multi-stream viewing with up to 6 concurrent streams per viewer

## Architecture

### Core Technology Stack
- **Stream Ingestion**: `datarhei/gosrt` for SRT protocol, `pion/rtp` for RTP
- **Video Processing**: `go-astiav` with FFmpeg bindings and NVIDIA NVENC hardware acceleration
- **HLS Generation**: `Eyevinn/hls-m3u8` and `Eyevinn/mp4ff` for LL-HLS with CMAF chunks
- **HTTP/3 Server**: `quic-go/quic-go` for low-latency delivery
- **State Management**: Redis for session management
- **CDN/Storage**: S3-compatible storage (MinIO) + CloudFront

### Service Architecture
```
Ingestion Service → Transcoding Service → HLS Packager → CDN Distribution
       ↓                    ↓                   ↓              ↓
    Redis (Session Management)                           5,000 Viewers
```

## Key Implementation Details

### Stream Ingestion
- Optimized for 50mbps streams with proper flow control
- SRT configuration: 120ms latency buffer, MTU-friendly payload size
- Concurrent connection handling for 25 streams

### Transcoding Strategy
- Hardware-accelerated using NVIDIA NVENC (h264_nvenc)
- Single bitrate output (640x360 @ 400kbps) for multi-view scenarios
- Fixed 500ms HLS segments with 100ms CMAF chunks for ultra-low latency

### LL-HLS Implementation
- EXT-X-SERVER-CONTROL and EXT-X-PART-INF tags for low latency
- HTTP/3 Push for predictive segment delivery
- Blocking playlist reload support (_HLS_msn parameter)
- 2-3 second glass-to-glass latency target

## Infrastructure Requirements

### Hardware
- **GPU**: 2x NVIDIA RTX 4090 or A6000 for HEVC transcoding
- **Memory**: 16-32GB RAM (200-500MB per stream + viewer overhead)
- **Network**: 10Gbps connection recommended

### AWS Deployment (from deploy.md)
- **GPU Instances**: G5 instances with A10G GPUs (g5.2xlarge recommended)
- **Storage**: S3 Express One Zone for live edge segments
- **CDN**: CloudFront with HTTP/3 enabled
- **Container Orchestration**: Amazon ECS with GPU-optimized AMIs

## Development Notes

### Performance Optimization
- Memory-mapped files for zero-copy segment handling
- Buffer pooling to reduce GC pressure
- GOMEMLIMIT set to 30GiB for large-scale deployments
- Connection pooling for all external services

### Cost Optimization
- Single low-bitrate stream per feed (400kbps) for multi-view
- CDN caching reduces origin bandwidth by 80%+
- Tiered storage: S3 Express One Zone for live edge, S3 Standard for recent content
- Spot instances for batch transcoding workloads

### Security Considerations
- Never include sensitive information (API keys, tokens) in code
- Use AWS Secrets Manager for credential storage
- Implement CloudFront signed URLs for premium content
- Deploy instances in private subnets with security groups

## Repository Structure

```
mirror/
├── docs/           # Architecture and planning documentation
│   ├── plan-*.md   # Detailed implementation plans
│   ├── deploy.md   # AWS deployment strategies
│   └── libs.md     # Go library recommendations
├── CLAUDE.md       # This file - Claude Code guidance
└── README.md       # Project overview
```

## Common Development Tasks

Since this is a planning/documentation repository without actual Go code implementation yet, there are no specific build, test, or lint commands. When the implementation begins, this section should be updated with:
- Go module initialization commands
- Build commands for different services
- Test execution commands
- Docker build and deployment commands
- Performance benchmarking scripts