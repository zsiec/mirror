# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Mirror is a high-performance video streaming platform built in Go, designed to handle:
- 25 concurrent SRT/RTP streams at 50mbps with HEVC input
- Transcoding to HLS-compatible formats with hardware acceleration
- Broadcasting to 5,000 concurrent viewers using Low-Latency HLS (LL-HLS)
- Multi-stream viewing with up to 6 concurrent streams per viewer

## Current Status

### Phase 1 ✅ COMPLETED
- Go project structure with modular architecture
- Configuration management (Viper-based YAML + env)
- Structured logging with rotation (logrus)
- Custom error handling framework
- Health check system (Redis, disk, memory)
- HTTP/3 server with QUIC protocol
- Docker environment with NVIDIA CUDA support
- GitHub Actions CI/CD
- 71% test coverage

### Phase 2 ✅ COMPLETED
- Stream ingestion with SRT and RTP protocols (Haivision adapter pattern)
- Video-aware buffering and GOP management
- Automatic codec detection (H.264, HEVC, AV1, JPEGXS)
- Frame assembly and validation with B-frame reordering
- Video resolution detection from bitstream analysis
- A/V synchronization with drift correction
- Intelligent backpressure control and memory management
- Stream recovery and reconnection with circuit breakers
- Comprehensive metrics and monitoring with sampled logging
- Redis-based stream registry with race-condition-safe API
- 85%+ test coverage for ingestion components

### Next Phases
- Phase 3: Video Processing & Transcoding
- Phase 4: HLS Packaging & Distribution
- Phase 5: Multi-stream Management
- Phase 6: CDN Integration
- Phase 7: Monitoring & Observability

## URLs and Access

- Base URL (SSL on 8081): `https://localhost:8081`

(Rest of the file remains unchanged)