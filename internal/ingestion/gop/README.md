# GOP Package

The `gop` package manages Group of Pictures (GOP) buffering and processing for the Mirror platform. It maintains complete GOPs in memory for clean stream switching, quality adaptation, and efficient transcoding decisions.

## Overview

Key components:
- **GOP Buffer**: Maintains multiple complete GOPs
- **GOP Detector**: Identifies GOP boundaries
- **GOP Analytics**: Provides GOP statistics

## GOP Buffer

Efficient GOP management:

```go
// Create buffer for 3 GOPs
gopBuffer := gop.NewBuffer(gop.Config{
    MaxGOPs:      3,
    MaxGOPSize:   300,  // frames
    MaxDuration:  10 * time.Second,
})

// Add frames
gopBuffer.AddFrame(frame, frameInfo)

// Get current GOP
currentGOP := gopBuffer.GetCurrent()

// Get all buffered GOPs
allGOPs := gopBuffer.GetAll()

// Get specific GOP by index
gop := gopBuffer.GetGOP(0)
```

## GOP Detection

Identify GOP boundaries:

```go
detector := gop.NewDetector()

// Check if frame starts new GOP
if detector.IsGOPStart(frame) {
    // New GOP boundary
    gopBuffer.StartNewGOP()
}

// Get GOP structure
structure := detector.AnalyzeGOP(frames)
fmt.Printf("GOP size: %d, I: %d, P: %d, B: %d\n",
    structure.Size,
    structure.IFrames,
    structure.PFrames,
    structure.BFrames,
)
```

## GOP Types

```go
type GOPType int

const (
    GOPTypeClosed GOPType = iota // Closed GOP (independent)
    GOPTypeOpen                  // Open GOP (references previous)
)

type GOP struct {
    ID        string
    Type      GOPType
    Frames    []*Frame
    Duration  time.Duration
    StartPTS  int64
    EndPTS    int64
    Keyframe  *Frame
}
```

## GOP Analytics

Analyze GOP characteristics:

```go
analytics := gop.NewAnalytics(gopBuffer)

// Get average GOP size
avgSize := analytics.AverageGOPSize()

// Get keyframe interval
interval := analytics.KeyframeInterval()

// Check for irregular GOPs
irregular := analytics.FindIrregularGOPs()
for _, g := range irregular {
    log.Warnf("Irregular GOP: %s, size: %d", g.ID, len(g.Frames))
}
```

## Features

- **Complete GOP buffering** for clean switching
- **GOP boundary detection** across all codecs
- **Duration tracking** for timing accuracy
- **Memory efficient** with configurable limits
- **Analytics support** for quality monitoring

## Usage Patterns

### Stream Switching
```go
// Wait for GOP boundary before switching
if detector.IsGOPStart(frame) {
    // Safe to switch streams
    switcher.SwitchStream(newStreamID)
}
```

### Quality Adaptation
```go
// Buffer GOPs for quality decisions
if gopBuffer.Count() >= 2 {
    // Analyze GOPs for quality adaptation
    quality := analyzer.DetermineQuality(gopBuffer.GetAll())
    transcoder.SetQuality(quality)
}
```

## Related Documentation

- [Frame Processing](../frame/README.md)
- [Codec Support](../codec/README.md)
- [Ingestion Overview](../README.md)
- [Main Documentation](../../../README.md)
