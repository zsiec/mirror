# Sync Package

The `sync` package provides audio/video synchronization for the Mirror platform. It handles timestamp management, drift detection and correction, PTS/DTS handling, and maintains perfect A/V alignment across different stream types.

## Overview

Key components:
- **Sync Manager**: Coordinates A/V synchronization
- **Track Sync**: Per-track timing management
- **Drift Detector**: Identifies sync drift
- **PTS Wraparound**: Handles timestamp wraparound
- **Timebase Converter**: Converts between timebases

## Sync Manager

Central synchronization coordinator:

```go
manager := sync.NewManager(sync.Config{
    DriftThreshold:   100 * time.Millisecond,
    CorrectionRate:   0.1, // 10% per second
    MaxDrift:         1 * time.Second,
})

// Register tracks
manager.RegisterTrack("video", sync.TrackTypeVideo)
manager.RegisterTrack("audio", sync.TrackTypeAudio)

// Process frames
syncedFrame := manager.ProcessFrame(frame, "video")
if syncedFrame != nil {
    // Frame is ready for output
}
```

## Track Synchronization

Per-track timing management:

```go
track := sync.NewTrackSync(sync.TrackConfig{
    Type:      sync.TrackTypeVideo,
    Timebase:  90000, // 90kHz for video
})

// Update timestamps
track.UpdateTimestamp(pts, dts)

// Get current position
position := track.GetPosition()

// Check sync status
if track.IsSynced() {
    // Track is synchronized
}
```

## Drift Detection and Correction

Automatic drift handling:

```go
// Detect drift between tracks
drift := manager.CalculateDrift("video", "audio")
if drift > threshold {
    // Apply correction
    manager.ApplyCorrection("audio", drift)
}

// Monitor drift over time
driftHistory := manager.GetDriftHistory()
for _, point := range driftHistory {
    fmt.Printf("Time: %v, Drift: %v\n", point.Time, point.Drift)
}
```

## PTS Wraparound Handling

Handle 33-bit PTS wraparound:

```go
wrapper := sync.NewPTSWrapper()

// Process PTS values
unwrappedPTS := wrapper.Unwrap(pts)

// Detect wraparound
if wrapper.HasWrapped() {
    log.Info("PTS wraparound detected")
}

// Get continuous timestamp
continuous := wrapper.GetContinuousTime()
```

## Timebase Conversion

Convert between different timebases:

```go
converter := sync.NewTimebaseConverter(
    90000,  // Source: 90kHz
    48000,  // Target: 48kHz audio
)

// Convert timestamp
targetTS := converter.Convert(sourceTS)

// Convert duration
targetDur := converter.ConvertDuration(sourceDur)
```

## Features

- **Multi-track synchronization** for complex streams
- **Automatic drift correction** with configurable rates
- **PTS/DTS management** with wraparound handling
- **Timebase conversion** for different clock rates
- **Sync metrics** for quality monitoring

## Synchronization Strategies

### Immediate Sync
```go
// Drop/duplicate frames for immediate sync
manager.SetStrategy(sync.StrategyImmediate)
```

### Gradual Sync
```go
// Adjust playback speed gradually
manager.SetStrategy(sync.StrategyGradual)
manager.SetCorrectionRate(0.05) // 5% speed adjustment
```

### Buffer-based Sync
```go
// Use buffering to absorb jitter
manager.SetStrategy(sync.StrategyBuffer)
manager.SetBufferDepth(500 * time.Millisecond)
```

## Related Documentation

- [Frame Processing](../frame/README.md)
- [GOP Management](../gop/README.md)
- [Ingestion Overview](../README.md)
- [Main Documentation](../../../README.md)
