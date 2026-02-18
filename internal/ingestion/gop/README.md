# GOP Package

The `gop` package provides GOP (Group of Pictures) buffering and boundary detection for the stream ingestion system.

## Architecture

```
VideoFrames → Detector (boundary detection) → closed GOP
                                                  ↓
                                           Buffer (indexed storage)
                                                  ↓
                                     Pressure-based frame dropping
```

## Detector

The `Detector` tracks GOP boundaries by watching for keyframes.

```go
detector := gop.NewDetector(streamID)

// Returns the *closed* previous GOP when a new keyframe starts a new GOP.
// Returns nil for non-keyframe frames.
closedGOP := detector.ProcessFrame(frame)

currentGOP := detector.GetCurrentGOP()
recentGOPs := detector.GetRecentGOPs()    // last 10 GOPs (copy)
stats := detector.GetStatistics()          // GOPStatistics
```

### GOP Completion Criteria

A GOP is marked complete when any of:
1. **Temporal duration** reaches 0.5s-2s (PTS-based, 90kHz clock)
2. **Structure size** is met (if `GOP.Structure.Size` is set)
3. **Frame count** reaches 120 (safety threshold, also forces closure)

A GOP is **closed** when the next keyframe arrives (via `ProcessFrame`).

### GOPStatistics

```go
type GOPStatistics struct {
    StreamID         string
    TotalGOPs        uint64
    CurrentGOPFrames int
    CurrentGOPSize   int64
    AverageGOPSize   float64
    AverageDuration  time.Duration
    IFrameRatio      float64
    PFrameRatio      float64
    BFrameRatio      float64
}
```

## Buffer

The `Buffer` maintains an indexed collection of complete GOPs with configurable limits.

```go
buf := gop.NewBuffer(streamID, gop.BufferConfig{
    MaxGOPs:     15,
    MaxBytes:    100 * 1024 * 1024,  // 100MB
    MaxDuration: 5 * time.Second,
    Codec:       types.CodecH264,
}, logger)
```

### BufferConfig

```go
type BufferConfig struct {
    MaxGOPs     int             // Maximum number of GOPs to buffer
    MaxBytes    int64           // Maximum buffer size in bytes
    MaxDuration time.Duration   // Maximum time span of buffered content
    Codec       types.CodecType // Video codec for parameter set parsing
}
```

### Buffer API

```go
buf.AddGOP(gop)                               // Add a complete/closed GOP
buf.GetGOP(gopID uint64) *types.GOP            // Lookup by GOP ID
buf.GetFrame(frameID uint64) *types.VideoFrame // Lookup by frame ID
buf.GetRecentGOPs(limit int) []*types.GOP      // Most recent GOPs (chronological)
buf.GetLatestIFrame() *types.VideoFrame        // Most recent keyframe
buf.Clear()                                     // Remove all GOPs

// Pressure-based frame dropping
dropped := buf.DropFramesForPressure(pressure float64)
dropped := buf.DropFramesFromGOP(gopID uint64, startIndex int)

// Callbacks
buf.SetGOPDropCallback(func(*types.GOP, *types.ParameterSetContext))

// Statistics
stats := buf.GetStatistics()  // BufferStatistics
```

### Pressure-Based Dropping Strategy

`DropFramesForPressure(pressure)` uses tiered strategies:

| Pressure | Action |
|----------|--------|
| < 0.5 | No dropping |
| 0.5-0.7 | Drop B-frames from 2 oldest GOPs |
| 0.7-0.85 | Drop all B-frames + P-frames from oldest GOP |
| 0.85-0.95 | Drop entire old GOPs (keep keyframes) |
| >= 0.95 | Drop entire old GOPs including keyframes (keep at least 1 GOP) |

### BufferStatistics

```go
type BufferStatistics struct {
    StreamID      string
    GOPCount      int
    FrameCount    int
    IFrames       int
    PFrames       int
    BFrames       int
    TotalBytes    int64
    Duration      time.Duration
    OldestTime    time.Time
    NewestTime    time.Time
    TotalGOPs     uint64
    DroppedGOPs   uint64
    DroppedFrames uint64
}
```

### Buffer Limits Enforcement

When a GOP is added, limits are enforced in order:
1. GOP count (`MaxGOPs`)
2. Byte size (`MaxBytes`) - keeps at least 1 GOP
3. Duration (`MaxDuration`) - removes GOPs older than cutoff

Before dropping a GOP, the `onGOPDrop` callback fires (used for parameter set preservation).

## Parameter Set Extraction

The buffer automatically extracts H.264 parameter sets (SPS/PPS) from NAL units in each added GOP. These are cached in a `types.ParameterSetContext` for use during stream recovery.

### Exported Utilities

```go
gop.ExtractNALTypeFromData(data []byte) uint8  // Extract NAL type, handling start codes
buf.ExtractParameterSetFromNAL(paramContext, nalUnit, nalType, gopID) bool
```

## FrameLocation

```go
type FrameLocation struct {
    GOP      *types.GOP
    Position int
}
```

Used internally for O(1) frame lookups by frame ID.

## Files

- `buffer.go`: Buffer, BufferConfig, BufferStatistics, FrameLocation, parameter set extraction
- `detector.go`: Detector, GOPStatistics, GOP completion logic
- `buffer_test.go`, `detector_test.go`: Core tests
- `buffer_duration_test.go`: Duration limit tests
- `buffer_frame_index_test.go`: Frame index integrity tests
- `buffer_nil_safety_test.go`: Nil safety tests
- `gop_duration_test.go`: GOP duration calculation tests
