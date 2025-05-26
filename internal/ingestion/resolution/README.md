# Resolution Detection Package

This package provides video resolution detection capabilities for the Mirror video streaming platform. It automatically detects video resolution from various video codecs without full frame decoding.

## Features

- **Multi-codec Support**: H.264, HEVC, AV1, JPEG-XS
- **Parser-based Detection**: Uses specialized parsers for H.264/HEVC SPS parsing
- **Pattern Matching**: Falls back to pattern recognition for unsupported formats
- **Performance Optimized**: Minimal CPU overhead, designed for real-time streams
- **Validation**: Ensures detected resolutions are valid and reasonable

## Supported Codecs

### H.264/AVC
- Parses Sequence Parameter Set (SPS) NAL units
- Extracts `pic_width_in_mbs_minus1` and `pic_height_in_map_units_minus1`
- Handles frame cropping parameters
- Supports all standard H.264 profiles

### HEVC/H.265
- Parses Sequence Parameter Set (SPS) NAL units
- Extracts `pic_width_in_luma_samples` and `pic_height_in_luma_samples`
- Handles conformance window parameters
- Supports Main, Main10, and other profiles

### AV1
- Parses Open Bitstream Units (OBUs)
- Extracts frame size from sequence headers
- Handles bit-packed frame dimensions
- Falls back to pattern matching for complex streams

### JPEG-XS
- Uses pattern matching approach
- Searches for common resolution patterns in frame data
- Validates aspect ratios and size constraints

## Architecture

```
┌─────────────────┐
│   Detector      │
├─────────────────┤
│ • DetectFromFrame
│ • DetectFromNALUnits
│ • isValidResolution
└─────────────────┘
         │
    ┌────┴────┐
    │         │
┌───▼───┐ ┌──▼──┐
│H.264  │ │HEVC │
│Parser │ │Parser│
└───────┘ └─────┘
    │         │
┌───▼─────────▼───┐
│  Golomb Decoder │
│ • ReadUE()      │
│ • ReadSE()      │
│ • Bit Reader    │
└─────────────────┘
```

## Usage

### Basic Detection

```go
import "github.com/zsiec/mirror/internal/ingestion/resolution"

detector := resolution.NewDetector()

// Detect from complete frame data
res := detector.DetectFromFrame(frameData, types.CodecH264)
if res.Width > 0 && res.Height > 0 {
    fmt.Printf("Detected resolution: %dx%d\n", res.Width, res.Height)
}
```

### NAL Unit Based Detection

```go
// Extract NAL units from H.264/HEVC frame
nalUnits := detector.extractNALUnits(frameData, types.CodecH264)

// Detect from extracted NAL units
res := detector.DetectFromNALUnits(nalUnits, types.CodecH264)
```

## Resolution Validation

The detector validates all detected resolutions:

- **Minimum size**: 64x64 pixels
- **Maximum size**: 7680x4320 pixels (8K)
- **Aspect ratio**: Must be reasonable (not extreme)
- **Standard formats**: Prefers common video resolutions

## Performance

- **H.264/HEVC**: ~50μs per frame (SPS parsing)
- **AV1**: ~100μs per frame (pattern matching)
- **JPEG-XS**: ~200μs per frame (full pattern search)
- **Memory**: < 1KB per detection

## Integration

### Video Pipeline Integration

The resolution detector is integrated into the video processing pipeline:

```go
// In video pipeline
if frame.IsKeyframe() {
    resolution := detector.DetectFromFrame(frame.Data, frame.Codec)
    if resolution.Width > 0 {
        pipeline.UpdateResolution(resolution)
    }
}
```

### Stream Handler Integration

Stream handlers use resolution detection for:
- Stream metadata updates
- Adaptive bitrate decisions
- Client capability matching

## Error Handling

The detector is designed to be resilient:

- **Invalid data**: Returns empty resolution (0x0)
- **Parsing errors**: Falls back to pattern matching
- **Unsupported codecs**: Graceful failure
- **Memory safety**: Bounds checking on all operations

## Testing

Comprehensive test suite includes:

- **Unit tests**: All parsers and detection methods
- **Security tests**: Malformed data handling
- **Performance tests**: Benchmarks for all codecs
- **Integration tests**: Real-world video samples

```bash
# Run resolution detection tests
go test ./internal/ingestion/resolution/...

# Run with coverage
go test -coverprofile=coverage.out ./internal/ingestion/resolution/...
```

## Configuration

No configuration required. The detector works with default settings suitable for most use cases.

## Limitations

- **H.264/HEVC**: Requires SPS NAL units for accurate detection
- **AV1**: Pattern matching may not work with all encoders
- **JPEG-XS**: Limited to common resolution patterns
- **Frame Accuracy**: Detection is frame-based, not field-based

## Future Enhancements

- **More Codecs**: VP9, AV2 support
- **Field Detection**: Interlaced video support
- **Encoder Detection**: Identify encoder type from bitstream
- **Quality Metrics**: Detect compression quality indicators
