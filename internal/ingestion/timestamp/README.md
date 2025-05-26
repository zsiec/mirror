# Timestamp Package

The timestamp package provides timestamp mapping and conversion utilities for video streaming, handling the complex relationship between RTP timestamps, PTS (Presentation Time Stamps), DTS (Decode Time Stamps), and wall clock time.

## Overview

Video streaming involves multiple timestamp domains that must be carefully synchronized:

- **RTP Timestamps**: 32-bit clock-based timestamps from network protocols
- **PTS (Presentation Time Stamps)**: When frames should be displayed
- **DTS (Decode Time Stamps)**: When frames should be decoded (differs for B-frames)
- **Wall Clock Time**: Real-world time for synchronization

## Core Components

### TimestampMapper

The `TimestampMapper` is the primary component that converts between timestamp domains with proper wrap-around handling.

```go
type TimestampMapper struct {
    clockRate    uint32    // RTP clock rate (90000 for video)
    baseRTPTime  uint32    // First RTP timestamp seen
    baseWallTime time.Time // Wall clock of first packet
    basePTS      int64     // Base PTS value
    
    // Wrap detection
    wrapCount   int    // Number of timestamp wraps
    lastRTPTime uint32 // Last RTP timestamp
    
    // Validation
    lastPTS       int64 // Last calculated PTS
    expectedDelta int64 // Expected delta between packets
}
```

## Key Features

### RTP Timestamp Conversion
Converts 32-bit RTP timestamps to 64-bit PTS values:

```go
mapper := NewTimestampMapper(90000) // 90kHz video clock
pts := mapper.ToPTS(rtpTimestamp)
```

### Wrap-Around Handling
RTP timestamps are 32-bit and wrap at 2^32. The mapper detects wraps automatically:

```go
// Detects when timestamp wraps around
if rtpTimestamp < tm.lastRTPTime {
    if (tm.lastRTPTime - rtpTimestamp) > 0x80000000 {
        tm.wrapCount++
    }
}
```

### PTS/DTS Calculation
Handles the difference between presentation and decode timestamps for B-frame videos:

```go
pts, dts := mapper.ToPTSWithDTS(rtpTimestamp, hasBFrames, frameType)
```

### Wall Clock Mapping
Converts PTS values back to real-world timestamps:

```go
wallTime := mapper.GetWallClockTime(pts)
```

## Usage Examples

### Basic RTP to PTS Conversion

```go
// Create mapper for 90kHz video clock
mapper := NewTimestampMapper(90000)

// Set expected frame rate for validation
mapper.SetExpectedFrameRate(30.0)

// Convert RTP timestamps to PTS
for _, packet := range packets {
    pts := mapper.ToPTS(packet.Timestamp)
    
    // Validate timestamp is reasonable
    if !mapper.ValidateTimestamp(packet.Timestamp) {
        log.Warn("Invalid timestamp detected")
        continue
    }
    
    // Process packet with PTS
    processPacket(packet, pts)
}
```

### B-Frame Stream Processing

```go
// Handle streams with B-frames (different PTS/DTS)
for _, frame := range frames {
    pts, dts := mapper.ToPTSWithDTS(
        frame.RTPTimestamp,
        true, // Has B-frames
        frame.Type, // "I", "P", or "B"
    )
    
    // Schedule decode at DTS, display at PTS
    decoder.Schedule(frame, dts)
    display.Schedule(frame, pts)
}
```

### Continuous Stream Handling

```go
// For continuous streams, set base PTS to maintain continuity
mapper.SetBasePTS(lastStreamPTS)

// Process new stream segment
for _, packet := range newSegment {
    pts := mapper.ToPTS(packet.Timestamp)
    // PTS will continue from lastStreamPTS
}
```

## Clock Rate Configuration

Different media types use different clock rates:

- **Video**: 90,000 Hz (90kHz) - standard for video
- **Audio**: Varies by codec (8kHz, 16kHz, 48kHz, etc.)
- **Custom**: Application-specific rates

```go
// Video stream
videoMapper := NewTimestampMapper(90000)

// Audio stream (48kHz)
audioMapper := NewTimestampMapper(48000)

// Custom application
customMapper := NewTimestampMapper(1000000) // 1MHz
```

## Validation and Error Detection

### Timestamp Validation
Validates that timestamp deltas are reasonable:

```go
if !mapper.ValidateTimestamp(rtpTimestamp) {
    // Handle invalid timestamp:
    // - Skip packet
    // - Reset mapper
    // - Use interpolated value
}
```

### Wrap Detection
Automatically detects and handles 32-bit wraparound:

```go
stats := mapper.GetStats()
if stats.WrapCount > 0 {
    log.Info("Handled %d timestamp wraps", stats.WrapCount)
}
```

## Statistics and Monitoring

### Timestamp Statistics
Access mapper state for monitoring:

```go
type TimestampStats struct {
    BaseRTPTime  uint32    // Initial RTP timestamp
    LastRTPTime  uint32    // Most recent timestamp
    WrapCount    int       // Number of wraps handled
    LastPTS      int64     // Most recent PTS
    ClockRate    uint32    // Current clock rate
    BaseWallTime time.Time // Initial wall clock time
}

stats := mapper.GetStats()
fmt.Printf("Processed %d wraps, current PTS: %d", 
    stats.WrapCount, stats.LastPTS)
```

## Thread Safety

The `TimestampMapper` is fully thread-safe using read-write mutexes:

```go
func (tm *TimestampMapper) ToPTS(rtpTimestamp uint32) int64 {
    tm.mu.Lock()
    defer tm.mu.Unlock()
    // Thread-safe conversion
}
```

Multiple goroutines can safely use the same mapper instance.

## Performance Considerations

### Memory Efficiency
- Minimal state maintained (< 100 bytes per mapper)
- No dynamic allocations during normal operation
- Efficient 64-bit arithmetic for wrap handling

### CPU Efficiency
- O(1) timestamp conversion
- Minimal lock contention with RWMutex
- Pre-calculated expected deltas for validation

## Error Handling

### Common Issues and Solutions

1. **Clock Rate Mismatch**
   ```go
   // Detect via validation failures
   if !mapper.ValidateTimestamp(ts) {
       // Check if wrong clock rate configured
   }
   ```

2. **Discontinuous Timestamps**
   ```go
   // Reset mapper on stream restart
   mapper.Reset()
   mapper.SetBasePTS(newBasePTS)
   ```

3. **Excessive Drift**
   ```go
   // Monitor wall clock correlation
   expectedWall := mapper.GetWallClockTime(pts)
   actualWall := time.Now()
   drift := actualWall.Sub(expectedWall)
   ```

## Integration with Video Pipeline

The timestamp package integrates with the broader video processing pipeline:

### Frame Processing
```go
// In frame assembler
for _, packet := range framePackets {
    pts := timestampMapper.ToPTS(packet.Timestamp)
    frame.PTS = pts
}
```

### A/V Synchronization
```go
// In sync manager
videoPTS := videoMapper.ToPTS(videoPacket.Timestamp)
audioPTS := audioMapper.ToPTS(audioPacket.Timestamp)
drift := videoPTS - audioPTS
```

### Output Scheduling
```go
// In output queue
wallTime := mapper.GetWallClockTime(frame.PTS)
scheduler.ScheduleAt(frame, wallTime)
```

## Testing

The package includes comprehensive tests for:

- RTP timestamp conversion accuracy
- 32-bit wrap-around handling
- PTS/DTS calculation for B-frames
- Wall clock time mapping
- Validation edge cases
- Thread safety under concurrent access

## Configuration

Timestamp mapping can be configured via the main application config:

```yaml
ingestion:
  timestamp:
    video_clock_rate: 90000      # 90kHz for video
    audio_clock_rate: 48000      # 48kHz for audio
    validation_threshold: 10     # Max seconds between packets
    expected_fps: 30             # Default frame rate assumption
```

## Best Practices

1. **Clock Rate Selection**: Use standard rates (90kHz for video) unless required otherwise
2. **Validation**: Always validate timestamps before processing
3. **Reset Handling**: Reset mappers on stream discontinuities
4. **Monitoring**: Track wrap counts and validation failures
5. **Base PTS**: Set appropriate base PTS for continuous streams
6. **Thread Safety**: Use single mapper per stream for best performance

## Future Enhancements

Potential improvements for the timestamp package:

- **Adaptive Clock Rate**: Automatic detection of clock rate from stream
- **Jitter Smoothing**: Timestamp jitter detection and smoothing
- **Drift Correction**: Automatic drift correction based on reference clocks
- **RTCP Integration**: Use RTCP sender reports for improved accuracy
- **Multiple Timebases**: Support for multiple concurrent time references
