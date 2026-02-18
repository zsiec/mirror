# Frame Package

The `frame` package handles video frame assembly from packets, codec-specific boundary detection, keyframe identification, and B-frame reordering.

## Architecture

```
TimestampedPackets → Assembler (boundary detection + reassembly) → VideoFrame channel
                                                                        ↓
                                                              BFrameReorderer (PTS order)
```

## Assembler

Reconstructs complete video frames from timestamped packets.

```go
assembler := frame.NewAssembler(streamID, types.CodecH264, 100) // outputBufferSize=100

assembler.Start()
defer assembler.Stop()

// Feed packets
assembler.AddPacket(pkt)

// Consume assembled frames
for frame := range assembler.GetOutput() {
    process(frame)
}

assembler.SetFrameTimeout(200 * time.Millisecond)
stats := assembler.GetStats()  // AssemblerStats
```

### AssemblerStats

```go
type AssemblerStats struct {
    FramesAssembled uint64
    FramesDropped   uint64
    PacketsReceived uint64
    PacketsDropped  uint64
}
```

### Errors

```go
var ErrNoFrameContext = errors.New("no frame context - received packet without active frame assembly")
var ErrFrameTimeout  = errors.New("frame assembly timeout")
var ErrOutputBlocked = errors.New("output channel blocked")
```

## Detector Interface

Codec-specific frame boundary detection. All detectors implement:

```go
type Detector interface {
    DetectBoundaries(pkt *types.TimestampedPacket) (isStart, isEnd bool)
    GetFrameType(nalUnits []types.NALUnit) types.FrameType
    IsKeyframe(data []byte) bool
    GetCodec() types.CodecType
}
```

### Detector Implementations

| Detector | Constructor | Codec |
|----------|-----------|-------|
| `H264Detector` | `NewH264Detector()` | H.264/AVC |
| `HEVCDetector` | `NewHEVCDetector()` | H.265/HEVC |
| `AV1Detector` | `NewAV1Detector()` | AV1 |
| `JPEGXSDetector` | `NewJPEGXSDetector()` | JPEG-XS |
| `GenericDetector` | (fallback) | Any |

### DetectorFactory

```go
factory := frame.NewDetectorFactory()
detector := factory.CreateDetector(types.CodecH264)  // returns appropriate Detector
```

## IDR Detector

Enhanced keyframe detection with GOP statistics and recovery strategy recommendation.

```go
idr := frame.NewIDRDetector(types.CodecH264, logger)

idr.IsIDRFrame(frame)                    // Check if frame is IDR
idr.GetKeyframeInterval() float64        // Average interval between keyframes
idr.PredictNextKeyframe(currentFrame)    // Predict next keyframe number
idr.NeedsKeyframe(currentFrame, hasErrors) // Whether a keyframe is needed
idr.ReportCorruption()                   // Report corruption for strategy adjustment
idr.FindRecoveryPoints(frames)           // Find RecoveryPoint candidates
idr.GetRecoveryStrategy()                // RecoveryStrategy recommendation
idr.Clone()                              // Deep copy
```

### RecoveryPoint / RecoveryStrategy

```go
type RecoveryPoint struct {
    FrameNumber uint64
    PTS         int64
    IsIDR       bool
    HasSPS      bool
    HasPPS      bool
    Confidence  float64  // 0.0 to 1.0
}

const (
    RecoveryStrategyWaitForKeyframe RecoveryStrategy = iota
    RecoveryStrategyRequestKeyframe
    RecoveryStrategyFindRecoveryPoint
    RecoveryStrategyReset
)
```

## B-Frame Reorderer

Reorders frames from decode order (DTS) to display order (PTS) using a min-heap.

```go
reorderer := frame.NewBFrameReorderer(maxReorderDepth, maxDelay, logger)

outputFrames, err := reorderer.AddFrame(frame)  // Returns frames ready for output
flushed := reorderer.Flush()                      // Drain remaining buffered frames
stats := reorderer.GetStats()                     // BFrameReordererStats
```

### BFrameReordererStats

```go
type BFrameReordererStats struct {
    FramesReordered uint64
    FramesDropped   uint64
    CurrentBuffer   int
    MaxBufferSize   int
}
```

### DTS Calculator

Estimates DTS values when not provided by the stream.

```go
calc := frame.NewDTSCalculator(frameRate, timeBase, maxBFrames)
dts := calc.CalculateDTS(pts, frameType)
```

## NAL Type Constants

The package exports NAL type constants for all supported codecs:
- `H264NALType*` (28 constants: Slice, IDR, SPS, PPS, AUD, FU-A, etc.)
- `HEVCNALType*` (26 constants: Trail, IDR, CRA, VPS, SPS, PPS, FU, etc.)
- `AV1OBUType*` (9 constants: SequenceHeader, FrameHeader, Frame, etc.)
- `JPEGXSMarker*` (12 constants: SOI, EOI, PIH, SLH, etc.)

## Files

- `assembler.go`: Assembler, AssemblerStats, GenericDetector
- `detector.go`: Detector interface, DetectorFactory
- `h264_detector.go`: H264Detector with NAL type constants
- `hevc_detector.go`: HEVCDetector with NAL type constants
- `av1_detector.go`: AV1Detector, OBU type with constants
- `jpegxs_detector.go`: JPEGXSDetector with marker constants
- `idr_detector.go`: IDRDetector, RecoveryPoint, RecoveryStrategy
- `bframe_reorderer.go`: BFrameReorderer, BFrameReordererStats, DTSCalculator
- `assembler_timeout_test.go`, `assembler_output_test.go`: Assembler tests
- `bframe_reorderer_test.go`, `bframe_reorderer_wraparound_test.go`: Reorderer tests
- `idr_detector_test.go`: IDR detector tests
- `h264_detector_security_test.go`, `h264_detector_edgecase_test.go`, `h264_detector_boundary_test.go`, `h264_detector_idr_fix_test.go`: H.264 detector tests
- `hevc_detector_security_test.go`: HEVC detector tests
- `av1_detector_security_test.go`: AV1 detector tests
