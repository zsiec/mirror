# Frame Package

The `frame` package handles video frame assembly and detection for the Mirror platform. It reconstructs complete frames from packets, detects frame boundaries, and identifies frame types for proper GOP management.

## Overview

Key components:
- **Frame Assembler**: Reconstructs frames from packets
- **Frame Detectors**: Codec-specific boundary detection
- **IDR Detector**: Identifies keyframes
- **B-frame Reorderer**: Handles B-frame reordering

## Frame Assembly

Packet-to-frame reconstruction:

```go
assembler := frame.NewAssembler(frame.Config{
    Timeout:     5 * time.Second,
    MaxSize:     10 << 20, // 10MB max frame
    ReorderSize: 100,      // Reorder buffer
})

// Add packets
for _, packet := range packets {
    frame, complete := assembler.AddPacket(packet)
    if complete {
        // Process complete frame
        processFrame(frame)
    }
}
```

## Frame Detection

Codec-specific frame boundary detection:

### H.264 Detection
```go
detector := frame.NewH264Detector()

// Analyze frame
info := detector.Analyze(frameData)

if info.IsIDR {
    // Keyframe - start of new GOP
}

fmt.Printf("Frame type: %s, Size: %d\n", info.Type, info.Size)
```

### HEVC Detection
```go
detector := frame.NewHEVCDetector()

// Check for IDR/CRA frames
if detector.IsRandomAccess(frame) {
    // Can start decoding from here
}
```

## Frame Types

```go
type FrameType int

const (
    FrameTypeI   FrameType = iota // Intra frame (keyframe)
    FrameTypeP                    // Predicted frame
    FrameTypeB                    // Bidirectional frame
    FrameTypeIDR                  // Instantaneous Decoder Refresh
    FrameTypeSPS                  // Sequence Parameter Set
    FrameTypePPS                  // Picture Parameter Set
)
```

## B-frame Reordering

Handle display order vs decode order:

```go
reorderer := frame.NewBFrameReorderer(frame.ReorderConfig{
    MaxReorderSize: 16,
    MaxDelay:       100 * time.Millisecond,
})

// Add frames in decode order
reorderer.AddFrame(frame)

// Get frames in display order
for {
    displayFrame := reorderer.GetNext()
    if displayFrame == nil {
        break
    }
    // Process in display order
}
```

## Features

- **Packet reassembly** with timeout handling
- **Frame boundary detection** for all supported codecs
- **Keyframe identification** for GOP boundaries
- **B-frame reordering** for proper display
- **Timestamp management** with PTS/DTS handling

## Related Documentation

- [Codec Support](../codec/README.md)
- [GOP Management](../gop/README.md)
- [Ingestion Overview](../README.md)
- [Main Documentation](../../../README.md)
