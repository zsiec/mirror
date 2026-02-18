# Documentation

This directory contains project documentation, architecture decisions, and implementation plans.

## Phase Documents

### phase-1.md ✅ COMPLETED
Core Foundation and Project Setup:
- Project structure and configuration
- Logging and error handling
- HTTP/3 server setup
- Health checks and monitoring
- Docker and CI/CD

### phase-2.md ✅ COMPLETED
Stream Ingestion Service:
- SRT protocol implementation using datarhei/gosrt
- RTP protocol support using pion/rtp
- Video-aware buffering with GOP management
- Automatic codec detection (H.264, HEVC, AV1, JPEGXS)
- Frame assembly and validation
- A/V synchronization with drift correction
- Backpressure control and memory management
- Stream recovery and reconnection
- Comprehensive metrics and monitoring
- Redis-based stream registry

### phase-3.md
Video Processing and Transcoding:
- FFmpeg integration with go-astiav
- NVIDIA hardware acceleration
- Codec support (HEVC input, H.264 output)
- Frame buffering and synchronization
- Quality presets and adaptive bitrate

### phase-4.md
HLS Packaging and Distribution:
- LL-HLS implementation
- CMAF packaging
- Manifest generation
- Segment management
- HTTP/3 delivery optimization

### phase-5.md
Multi-Stream Management:
- Stream multiplexing
- Viewer session management
- Stream switching logic
- Bandwidth optimization
- Layout templates

### phase-6.md
CDN Integration and Storage:
- S3 integration for segments
- CloudFront configuration
- Storage lifecycle policies
- Cache optimization
- Failover strategies

### phase-7.md
Monitoring and Observability:
- Prometheus metrics
- Custom dashboards
- Log aggregation
- Distributed tracing
- Alerting rules

## Architecture Decisions

### Why HTTP/3?
- Reduced latency through 0-RTT connections
- Better performance over unreliable networks
- Stream multiplexing without head-of-line blocking
- Native support for server push

### Why Go?
- Excellent concurrency primitives
- Low memory footprint
- Fast compilation and deployment
- Strong ecosystem for networking
- Good FFmpeg bindings

### Why Redis?
- Fast in-memory operations
- Pub/Sub for real-time updates
- Built-in expiration for sessions
- Clustering support for scaling
- Proven reliability

### Why SRT Primary Protocol?
- Built for live video streaming
- Packet loss recovery
- Low latency with reliability
- Encryption built-in
- Better than raw RTP for internet delivery

### Video Pipeline Design (Phase 2)
- Packet → Frame → GOP abstraction
- Video-aware buffering prevents corruption
- GOP boundaries for clean switching
- Memory pools for efficiency
- Backpressure at every stage

## Implementation Guidelines

### Code Organization
- `internal/`: Private packages not for external use
- `pkg/`: Public packages that could be imported
- `cmd/`: Executable entry points
- Keep packages focused and cohesive

### Testing Strategy
- Unit tests for all business logic
- Integration tests for external dependencies
- End-to-end tests for critical paths
- Benchmark tests for performance-critical code
- Minimum 80% code coverage target

### Error Handling
- Use custom error types for better context
- Wrap errors with additional information
- Map errors to appropriate HTTP status codes
- Log errors with trace IDs for debugging

### Performance Considerations
- Profile before optimizing
- Use buffer pools for memory efficiency
- Implement proper backpressure
- Monitor goroutine counts
- Set appropriate timeouts

## Deployment Strategy

### Development
- Docker Compose for local environment
- Hot reload for rapid iteration
- Local Redis and MinIO instances
- Self-signed certificates

### Staging
- Kubernetes deployment
- Horizontal pod autoscaling
- Shared Redis cluster
- S3-compatible storage

### Production
- Multi-region deployment
- GPU-enabled instances
- CloudFront CDN
- Auto-scaling groups
- Blue-green deployments

## API Documentation

### OpenAPI Specifications
The `openapi/` subdirectory contains:
- `server.yaml`: Main API specification
- `ingestion.yaml`: Stream ingestion API spec
- `index.html`: Interactive API documentation

View the documentation:
```bash
# Open in browser
open docs/openapi/index.html

# Or serve locally
python -m http.server 8000 -d docs/openapi
```

## Future Considerations

### Scaling Strategies
- Horizontal scaling for ingestion
- GPU pooling for transcoding
- Edge caching for distribution
- Database sharding for metadata

### Feature Roadmap
- WebRTC support for ultra-low latency
- AI-based quality optimization
- Dynamic ad insertion
- Analytics and reporting
- Mobile SDK development

### Security Enhancements
- DRM integration
- Geo-blocking capabilities
- Token-based authentication
- Encrypted segment delivery
- Audit logging
