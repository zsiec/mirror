# Contributing to Mirror

Welcome to Mirror! We're excited that you're interested in contributing to this high-performance video streaming platform. This guide will help you get started with contributing to our open source project.

## Table of Contents

- [Project Overview](#project-overview)
- [Development Setup](#development-setup)
- [Development Workflow](#development-workflow)
- [Code Standards](#code-standards)
- [Testing Guidelines](#testing-guidelines)
- [Pull Request Process](#pull-request-process)
- [Architecture Guidelines](#architecture-guidelines)
- [Getting Help](#getting-help)

## Project Overview

Mirror is a high-performance video streaming platform built in Go, designed to handle:

- 25 concurrent SRT/RTP streams at 50mbps with HEVC input
- Transcoding to HLS-compatible formats with hardware acceleration
- Broadcasting to 5,000 concurrent viewers using Low-Latency HLS (LL-HLS)
- Multi-stream viewing with up to 6 concurrent streams per viewer

### Current Status

- **Phase 1** ✅ Core infrastructure and HTTP/3 server
- **Phase 2** ✅ Stream ingestion with SRT/RTP protocols (current)
- **Phase 3** 🔄 Video processing & transcoding
- **Phase 4** 📋 HLS packaging & distribution
- **Phase 5** 📋 Multi-stream management
- **Phase 6** 📋 CDN integration
- **Phase 7** 📋 Monitoring & observability

## Development Setup

### Prerequisites

Mirror requires several system dependencies for video streaming functionality:

**Required:**
- **Go 1.23+** (strict requirement)
- **SRT library** (libsrt-openssl-dev) - for video streaming protocols
- **FFmpeg 7.1+** with SRT support - for video processing
- **Redis 7.0+** - for stream registry and state management
- **Docker & Docker Compose** - for development environment
- **pkg-config** - for CGO compilation with SRT library

### Quick Setup

We provide a one-command setup that handles all dependencies:

```bash
# Clone the repository
git clone https://github.com/zsiec/mirror.git
cd mirror

# Complete setup (handles SRT, certificates, everything)
make setup

# Verify installation
make check
```

### Platform-Specific Setup

#### Ubuntu/Debian
```bash
# Install system dependencies
sudo apt-get update
sudo apt-get install -y build-essential cmake pkg-config libssl-dev redis-server

# Setup SRT library (automated)
make srt-setup

# Generate development certificates
make generate-certs
```

#### macOS
```bash
# Install system dependencies
brew install cmake pkg-config openssl redis srt

# Setup development environment
make setup
```

### Development Environment

Start the complete development environment with monitoring:

```bash
# Start all services (Mirror, Redis, Prometheus)
make docker-compose

# View logs in real-time
make docker-compose-logs

# Stop all services
make docker-compose-down
```

This provides:
- Mirror application with hot reload
- Redis for state management
- Prometheus for metrics collection
- Volume mounts for live code changes

## Development Workflow

### Getting Started

1. **Fork and clone** the repository
2. **Create a feature branch** from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```
3. **Run the setup** to ensure your environment works:
   ```bash
   make setup && make test
   ```
4. **Make your changes** following our [code standards](#code-standards)
5. **Test thoroughly** using our [testing guidelines](#testing-guidelines)
6. **Submit a pull request** following our [PR process](#pull-request-process)

### Key Development Commands

```bash
# Run all tests with coverage
make test

# Run tests with SRT environment properly configured
make test-coverage

# Full integration test with real streams
make test-full-integration

# Format code and run all quality checks
make check

# Build the application
make build

# Clean rebuild
make clean && make build
```

## Code Standards

### Go Code Quality

We maintain high code quality standards:

- **Formatting**: Use `gofmt` and `gofumpt` (automated in `make check`)
- **Linting**: `golangci-lint` with comprehensive rules
- **Vetting**: `go vet` for suspicious code patterns
- **Testing**: Race detection enabled (`-race` flag)
- **Coverage**: Minimum 85% test coverage required

### Code Style Guidelines

**Function Signatures:**
```go
// Context should be first parameter
func ProcessStream(ctx context.Context, streamID string, opts *Options) error

// Error handling with wrapping
if err != nil {
    return fmt.Errorf("failed to process stream %s: %w", streamID, err)
}
```

**Struct Design:**
```go
// Use comprehensive struct tags
type StreamConfig struct {
    Port     int    `mapstructure:"port" validate:"required,min=1024,max=65535"`
    Protocol string `mapstructure:"protocol" validate:"required,oneof=srt rtp"`
    Bitrate  int    `mapstructure:"bitrate" validate:"min=1000,max=100000000"`
}
```

**Interface Design:**
```go
// Interfaces should be small and focused
type StreamProcessor interface {
    Process(ctx context.Context, frame *Frame) error
    Close() error
}
```

### Architecture Patterns

Follow these established patterns in the codebase:

- **Adapter Pattern**: For protocol implementations (see `internal/ingestion/srt/`)
- **Circuit Breaker**: For reliability (see `internal/ingestion/backpressure/`)
- **Buffer Pooling**: For memory efficiency (see `internal/ingestion/buffer/`)
- **Channel Communication**: For goroutine coordination
- **Context Propagation**: For cancellation and deadlines

## Testing Guidelines

### Test Coverage Requirements

- **Minimum 85% coverage** for all new code
- **Race condition testing** mandatory (all tests run with `-race`)
- **Integration tests** for protocol implementations
- **Stress tests** for performance-critical paths

### Test Types

**Unit Tests:**
```go
func TestStreamProcessor_Process(t *testing.T) {
    tests := []struct {
        name    string
        input   *Frame
        want    error
        setup   func(*StreamProcessor)
    }{
        {
            name:  "valid frame",
            input: &Frame{Data: []byte("test")},
            want:  nil,
        },
        // More test cases...
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation with proper cleanup
            defer cleanup()
            // ...
        })
    }
}
```

**Integration Tests:**
```go
func TestSRTIntegration(t *testing.T) {
    // Use real SRT connections
    ctx := context.Background()
    listener, err := srt.NewListener(ctx, ":30000")
    require.NoError(t, err)
    defer listener.Close()
    
    // Test real protocol behavior
}
```

**Full Integration Tests:**
```bash
# Rich terminal UI showing real stream metrics
make test-full-integration
```

### Benchmark Tests

For performance-critical code:

```go
func BenchmarkStreamProcessor(b *testing.B) {
    processor := NewStreamProcessor()
    frame := &Frame{Data: make([]byte, 1024)}
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        processor.Process(context.Background(), frame)
    }
}
```

## Pull Request Process

### Before Submitting

1. **Ensure tests pass**:
   ```bash
   make check
   ```

2. **Verify integration tests**:
   ```bash
   make test-full-integration
   ```

3. **Check coverage**:
   ```bash
   make test-coverage
   # Coverage should be 85%+
   ```

4. **Update documentation** if needed:
   - Add/update package README.md files
   - Update configuration examples
   - Add godoc comments for public APIs

### PR Requirements

**Title Format:**
- `feat: add new streaming protocol support`
- `fix: resolve memory leak in buffer pool`
- `docs: update configuration examples`
- `test: add integration tests for SRT adapter`

**Description Should Include:**
- **What**: Clear description of changes
- **Why**: Motivation and context
- **How**: Implementation approach
- **Testing**: How you tested the changes
- **Breaking Changes**: Any API changes

**Example PR Description:**
```markdown
## What
Add support for JPEGXS video codec in the ingestion pipeline.

## Why
JPEGXS is increasingly used for low-latency professional video applications and we've had multiple user requests for support.

## How
- Implemented JPEGXSDepacketizer following the existing codec pattern
- Added codec detection for JPEGXS streams
- Extended frame detector for JPEGXS frame boundaries

## Testing
- Unit tests with 95% coverage
- Integration tests with real JPEGXS streams
- Stress tested with 10 concurrent JPEGXS streams

## Breaking Changes
None - this is additive functionality.
```

### Review Process

1. **Automated checks** must pass (CI/CD pipeline)
2. **Code review** by maintainers
3. **Integration testing** verification
4. **Documentation review** if applicable
5. **Final approval** and merge

## Architecture Guidelines

### Adding New Features

When adding significant features:

1. **Design Document**: Create a design doc for major features
2. **Interface First**: Define interfaces before implementations
3. **Testing Strategy**: Plan testing approach upfront
4. **Configuration**: Add configuration options following existing patterns
5. **Metrics**: Add relevant Prometheus metrics
6. **Documentation**: Update package README and examples

### Code Organization

```
internal/
├── feature/              # New feature package
│   ├── README.md        # Package documentation
│   ├── feature.go       # Main implementation
│   ├── feature_test.go  # Unit tests
│   ├── integration_test.go # Integration tests
│   └── types.go         # Type definitions
```

### Error Handling

```go
// Define package-specific error types
var (
    ErrInvalidStream = errors.New("invalid stream format")
    ErrStreamClosed  = errors.New("stream connection closed")
)

// Wrap errors with context
func ProcessStream(streamID string) error {
    if err := validateStream(streamID); err != nil {
        return fmt.Errorf("stream %s validation failed: %w", streamID, err)
    }
    return nil
}
```

### Configuration

Follow the established configuration pattern:

```go
type FeatureConfig struct {
    Enabled  bool          `mapstructure:"enabled" validate:"required"`
    Timeout  time.Duration `mapstructure:"timeout" validate:"min=1s,max=30s"`
    Workers  int           `mapstructure:"workers" validate:"min=1,max=100"`
}
```

## Getting Help

### Resources

- **Documentation**: Check `docs/` directory and package README files
- **Examples**: Look at existing implementations in `internal/`
- **Tests**: Existing tests show usage patterns
- **Issues**: Search existing issues for similar problems

### Communication

- **GitHub Issues**: For bug reports and feature requests
- **GitHub Discussions**: For questions and design discussions
- **Pull Requests**: For code review and implementation discussions

### Common Issues

**SRT Setup Problems:**
```bash
# Reset SRT environment
make clean-srt
make srt-setup
```

**Test Failures:**
```bash
# Check Redis is running
redis-cli ping

# Regenerate certificates
make generate-certs

# Clean rebuild
make clean && make test
```

**Build Issues:**
```bash
# Check Go version
go version  # Should be 1.23+

# Verify SRT installation
pkg-config --modversion srt

# Check CGO environment
make check-env
```

## Contributing Areas

We welcome contributions in these areas:

### High Priority
- **Video Codec Support**: Add new codec implementations
- **Protocol Extensions**: Enhance SRT/RTP support
- **Performance Optimization**: Buffer management, memory usage
- **Testing**: More integration and stress tests

### Medium Priority
- **Documentation**: API docs, tutorials, examples
- **Monitoring**: Additional metrics and dashboards
- **Configuration**: More flexible configuration options
- **Error Handling**: Better error messages and recovery

### Future Features
- **Transcoding Pipeline**: Video processing and encoding
- **HLS Packaging**: Adaptive bitrate streaming
- **CDN Integration**: Distribution and caching
- **Multi-platform**: Windows and ARM support

## Code of Conduct

By participating in this project, you agree to maintain a welcoming and inclusive environment. We follow the [Contributor Covenant](https://www.contributor-covenant.org/) code of conduct.

---

Thank you for contributing to Mirror! Your involvement helps us build a better video streaming platform for everyone.