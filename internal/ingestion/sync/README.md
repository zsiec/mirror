# Sync Package

The `sync` package provides audio/video synchronization for the Mirror platform. It handles per-stream drift detection and correction, PTS/DTS management with wraparound handling, and timebase conversion between different clock rates.

## Architecture

```
┌─────────────────────────────────────────┐
│   Manager (per-stream)                  │
│  • Drift window (100 samples)           │
│  • Auto-correction (gradual)            │
│  • Audio offset tracking                │
├───────────────┬─────────────────────────┤
│ TrackSyncMgr  │  TrackSyncMgr           │
│   (video)     │    (audio)              │
│ • Base PTS    │  • Base PTS             │
│ • Wrap detect │  • Wrap detect          │
│ • Wall clock  │  • Wall clock           │
└───────────────┴─────────────────────────┘
```

## Core Components

### Manager

Central A/V synchronization coordinator for a single stream.

```go
manager := sync.NewManager("stream-123", nil, logger) // nil = default config

// Initialize tracks with their timebases
manager.InitializeVideo(types.TimeBase90kHz) // 1/90000
manager.InitializeAudio(types.TimeBase48kHz) // 1/48000

// Process frames — sets PresentationTime on each
manager.ProcessVideoFrame(frame) // frame.PresentationTime is set
manager.ProcessAudioPacket(packet) // packet.PresentationTime is set (with offset)

// Check sync status
status := manager.GetSyncStatus()
fmt.Printf("In sync: %v, drift: %v\n", status.InSync, status.CurrentDrift)
```

#### Constructor

```go
func NewManager(streamID string, config *SyncConfig, log logger.Logger) *Manager
```

Takes stream ID, optional config (nil for defaults), and logger. Initializes drift window capacity to 100 and corrections capacity to 50.

#### Key Methods

| Method | Signature | Description |
|--------|-----------|-------------|
| `InitializeVideo` | `(timeBase types.Rational) error` | Initialize video track sync |
| `InitializeAudio` | `(timeBase types.Rational) error` | Initialize audio track sync |
| `ProcessVideoFrame` | `(frame *types.VideoFrame) error` | Process video frame, measure drift |
| `ProcessAudioPacket` | `(packet *types.TimestampedPacket) error` | Process audio packet with offset |
| `GetSyncStatus` | `() *SyncStatus` | Current sync state snapshot |
| `GetStatistics` | `() map[string]interface{}` | Detailed stats map |
| `SetAudioOffset` | `(offset time.Duration)` | Manual audio offset adjustment |
| `Reset` | `()` | Reset all sync state |
| `ReportVideoDropped` | `(count uint64)` | Report dropped video frames |
| `ReportAudioDropped` | `(count uint64)` | Report dropped audio samples |

### TrackSyncManager

Per-track timing management with PTS wrap detection and wall-clock correlation.

```go
track := sync.NewTrackSyncManager(sync.TrackTypeVideo, "stream-123",
    types.Rational{Num: 1, Den: 90000}, nil)

track.ProcessTimestamp(pts, dts, time.Now())
presentationTime := track.GetPresentationTime(pts)
```

#### Constructor

```go
func NewTrackSyncManager(trackType TrackType, streamID string,
    timeBase types.Rational, config *SyncConfig) *TrackSyncManager
```

Guards against zero timebase values (defaults `Num` to 1, `Den` to 90000). Creates an internal `PTSWrapDetector`.

#### Key Methods

| Method | Description |
|--------|-------------|
| `ProcessTimestamp(pts, dts int64, wallTime time.Time) error` | Updates timing, detects wraps/jumps |
| `GetPresentationTime(pts int64) time.Time` | PTS to wall-clock (overflow-safe integer math) |
| `GetDecodeTime(dts int64) time.Time` | DTS to wall-clock |
| `GetDriftFromWallClock(pts int64, actualWallTime time.Time) time.Duration` | Measure drift |
| `GetSyncState() *TrackSync` | Current state copy |
| `GetStatistics() map[string]interface{}` | Track stats |
| `Reset()` | Reset track state |

### PTSWrapDetector

Handles 33-bit PTS wraparound detection (per ISO 13818-1, wraps at ~26.5 hours at 90kHz).

```go
detector := sync.NewPTSWrapDetector(types.TimeBase90kHz)
if detector.DetectWrap(currentPTS, lastPTS) {
    wrapCount++
}
unwrapped := detector.UnwrapPTS(pts, wrapCount)
```

#### Methods

| Method | Description |
|--------|-------------|
| `DetectWrap(currentPTS, lastPTS int64) bool` | True if wrap detected (backward jump > half threshold) |
| `UnwrapPTS(pts int64, wrapCount int) int64` | Adjust PTS by `wrapCount * (1 << 33)` |
| `GetWrapThreshold() int64` | Returns `1 << 33` |
| `IsLikelyDiscontinuity(currentPTS, lastPTS int64) bool` | Distinguishes discontinuity from wrap |
| `CalculatePTSDelta(currentPTS, lastPTS int64, wrapCount int) int64` | Delta accounting for wraps |

### TimeBaseConverter

Converts timestamps between different time bases using a precomputed conversion factor.

```go
converter, err := sync.NewTimeBaseConverter(
    types.Rational{Num: 1, Den: 90000}, // from: 90kHz video
    types.Rational{Num: 1, Den: 48000}, // to: 48kHz audio
)
targetPTS := converter.Convert(sourcePTS)
```

#### Constructor

```go
func NewTimeBaseConverter(from, to types.Rational) (*TimeBaseConverter, error)
```

Returns error if either timebase has zero Num or Den.

#### Methods

| Method | Description |
|--------|-------------|
| `Convert(pts int64) int64` | Convert timestamp (rounds to nearest) |
| `ConvertPrecise(pts int64) (int64, float64)` | Convert with fractional remainder |
| `ConvertDuration(duration int64) int64` | Convert duration (same as Convert) |
| `GetRatio() float64` | Conversion ratio |

#### Convenience Functions

```go
sync.ConvertToMilliseconds(pts, timeBase)   // Any timebase -> milliseconds
sync.ConvertFromMilliseconds(ms, timeBase)  // Milliseconds -> any timebase
```

#### TimeBaseConverterChain

Chains multiple converters for multi-step conversion:

```go
chain, err := sync.NewTimeBaseConverterChain(tb1, tb2, tb3) // tb1 -> tb2 -> tb3
result := chain.Convert(pts)
```

#### CommonTimeBases

```go
sync.CommonTimeBases.Video90kHz    // {1, 90000}
sync.CommonTimeBases.Audio48kHz    // {1, 48000}
sync.CommonTimeBases.Audio44_1kHz  // {1, 44100}
sync.CommonTimeBases.NTSC29_97fps  // {1001, 30000}
sync.CommonTimeBases.Film23_976fps // {1001, 24000}
sync.CommonTimeBases.Milliseconds  // {1, 1000}
sync.CommonTimeBases.Microseconds  // {1, 1000000}
```

## Configuration

```go
type SyncConfig struct {
    MaxAudioDrift        time.Duration // Default: 40ms
    MaxVideoDrift        time.Duration // Default: 40ms
    CorrectionInterval   time.Duration // Default: 100ms
    EnableAutoCorrect    bool          // Default: true
    CorrectionFactor     float64       // Default: 0.1 (10% per iteration)
    MaxCorrectionStep    time.Duration // Default: 5ms
    MultiStreamTolerance time.Duration // Default: 50ms
    MasterClockSource    string        // Default: "system"
    EnableDriftLogging   bool          // Default: false
    DriftLogInterval     time.Duration // Default: 1s
}
```

## Key Types

```go
type SyncStatus struct {
    InSync         bool
    MaxDrift       time.Duration
    CurrentDrift   time.Duration
    VideoSync      *TrackSync
    AudioSync      *TrackSync
    DriftWindow    []DriftSample
    Corrections    []DriftCorrection
    AvgDrift       time.Duration
    DriftVariance  float64
    CorrectionRate float64
}

type DriftSample struct {
    Timestamp     time.Time
    VideoPTS      int64
    AudioPTS      int64
    Drift         time.Duration // positive = video ahead
    PTSDrift      time.Duration
    ProcessingLag time.Duration
}
```

## Testing

```bash
source scripts/srt-env.sh && go test ./internal/ingestion/sync/...
```
