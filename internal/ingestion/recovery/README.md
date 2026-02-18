# Recovery Package

The `recovery` package provides multi-level error recovery for video stream ingestion, from individual frame recovery to full stream reconnection with circuit breakers.

## Subsystems

1. **Handler** (`recovery.go`) - Base error recovery with GOP-based resync
2. **SmartRecoveryHandler** (`smart_recovery.go`) - Enhanced recovery with pattern analysis and IDR detection
3. **StreamRecovery** (`stream_recovery.go`) - Stream-level recovery with circuit breaker and state preservation
4. **FrameRecovery** (`frame_recovery.go`) - Frame-level recovery (interpolation, duplication, GOP-based)
5. **AdaptiveQuality** (`adaptive_quality.go`) - Dynamic quality adaptation based on system conditions

## Handler (Base Recovery)

Manages error recovery using a GOP buffer to find recovery points.

```go
handler := recovery.NewHandler(streamID, recovery.Config{
    MaxRecoveryTime: 5 * time.Second,
    KeyframeTimeout: 2 * time.Second,
    CorruptionWindow: 10,
}, gopBuffer, logger)

handler.HandleError(recovery.ErrorTypePacketLoss, details)
handler.UpdateKeyframe(frame)
state := handler.GetState()            // RecoveryState
stats := handler.GetStatistics()       // Statistics
handler.SetCallbacks(onStart, onEnd, onKeyframe)
```

### Error Types

```go
const (
    ErrorTypePacketLoss    ErrorType = iota
    ErrorTypeCorruption
    ErrorTypeTimeout
    ErrorTypeSequenceGap
    ErrorTypeTimestampJump
    ErrorTypeCodecError
)
```

### Recovery States

```go
const (
    StateNormal    RecoveryState = iota
    StateRecovering
    StateResyncing
    StateFailed
)
```

### Statistics

```go
type Statistics struct {
    State            RecoveryState
    RecoveryCount    uint64
    CorruptionCount  uint64
    ResyncCount      uint64
    LastRecoveryTime time.Time
    IsHealthy        bool
}
```

## SmartRecoveryHandler

Enhanced recovery embedding `Handler` with error pattern analysis and IDR-based recovery.

```go
smart := recovery.NewSmartRecoveryHandler(ctx, streamID, recovery.SmartConfig{
    Config:             baseConfig,
    EnableAdaptiveGOP:  true,
    EnableFastRecovery: true,
    EnablePreemptive:   true,
    FrameBufferSize:    30,
}, gopBuffer, types.CodecH264, logger)
defer smart.Stop()

smart.HandleError(errorType, details)
smart.ProcessFrame(frame)
smart.UpdateRecoveryMetrics(duration, success)
stats := smart.GetSmartStatistics()  // SmartStatistics
```

### SmartStatistics

```go
type SmartStatistics struct {
    Statistics                  // embedded base stats
    RecoveryAttempts int
    AvgRecoveryTime  time.Duration
    SuccessRate      float64
    ErrorPatterns    map[string]interface{}
    GOPInterval      float64
    Strategy         string
}
```

## StreamRecovery

Stream-level recovery with exponential backoff, circuit breaker, and optional state preservation.

```go
sr := recovery.NewStreamRecovery(streamID, logger, recovery.DefaultRecoveryConfig())
// or: recovery.RecoveryConfig{MaxRetries: 5, InitialBackoff: 1s, MaxBackoff: 30s, ...}

sr.SetCallbacks(onRecover, onStateRestore, onRecoveryFailed)
sr.SetStateStore(store)  // optional StateStore implementation
sr.HandleError(err)
state := sr.GetState()          // StreamRecoveryState
stats := sr.GetStatistics()     // map[string]interface{}
sr.Stop()
```

### RecoveryConfig

```go
type RecoveryConfig struct {
    MaxRetries               int
    InitialBackoff           time.Duration
    MaxBackoff               time.Duration
    BackoffMultiplier        float64
    CircuitBreakerThreshold  int
    CircuitBreakerTimeout    time.Duration
    HealthCheckInterval      time.Duration
    EnableStatePreservation  bool
}
```

### CircuitBreaker

```go
cb := recovery.NewCircuitBreaker(threshold, timeout)
cb.Allow() bool           // Check if operation is allowed
cb.RecordSuccess()
cb.RecordFailure()
cb.State() string          // "closed", "open", "half-open"
cb.Reset()
```

### PreservedState / StateStore

```go
type PreservedState struct {
    StreamID        string
    LastSequence    uint16
    LastTimestamp    uint32
    LastFrameNumber uint64
    ParameterSets   map[string][]byte
    BufferedFrames  []*types.VideoFrame
    Metadata        map[string]interface{}
    PreservationTime time.Time
}

type StateStore interface {
    Save(state *PreservedState) error
    Load(streamID string) (*PreservedState, error)
    Delete(streamID string) error
}
```

## FrameRecovery

Individual frame-level recovery with multiple strategies.

```go
fr := recovery.NewFrameRecovery(streamID, logger)
fr.SetStrategy(recovery.FrameRecoveryInterpolation)

fr.RecordFrame(frame)
fr.RecordGOP(gop)

recovered, err := fr.RecoverFrame(frameNumber, frameType)
recoveredGOP, err := fr.RecoverPartialGOP(partialGOP)
metrics := fr.GetMetrics()  // RecoveryMetrics
```

### Frame Recovery Strategies

```go
const (
    FrameRecoveryNone          FrameRecoveryStrategy = iota
    FrameRecoveryInterpolation // Average surrounding frames
    FrameRecoveryDuplication   // Copy previous frame of same type
    FrameRecoveryGOPBased      // Find frame in buffered GOPs
    FrameRecoveryMLPrediction  // Placeholder for future ML
)
```

## AdaptiveQuality

Dynamic quality adaptation across 5 quality levels (Ultra 4K/60fps to Minimum 360p/15fps).

```go
aq := recovery.NewAdaptiveQuality(streamID, logger)
defer aq.Stop()

aq.UpdateSystemMetrics(cpuUsage, memoryUsage, bandwidth)
aq.UpdateStreamMetrics(bitrate, frameDropRate, packetLoss, latency)

profile := aq.GetCurrentProfile()  // QualityProfile
score := aq.GetQualityScore()      // 0.0-1.0
aq.SetTargetQuality(recovery.QualityLevelHigh)
aq.SetCallback(func(old, new QualityProfile) { ... })
data := aq.GetMLData()             // []MLDataPoint for future ML
stats := aq.GetStatistics()        // map[string]interface{}
```

### Quality Levels

```go
const (
    QualityLevelUltra   QualityLevel = iota  // 4K, 60fps, 50Mbps
    QualityLevelHigh                          // 1080p, 30fps, 10Mbps
    QualityLevelMedium                        // 720p, 30fps, 5Mbps
    QualityLevelLow                           // 480p, 24fps, 2Mbps
    QualityLevelMinimum                       // 360p, 15fps, 1Mbps
)
```

## Files

- `recovery.go`: Handler, Config, ErrorType, RecoveryState, Statistics
- `smart_recovery.go`: SmartRecoveryHandler, SmartConfig, SmartStatistics, ErrorEvent
- `stream_recovery.go`: StreamRecovery, RecoveryConfig, PreservedState, StateStore, CircuitBreaker
- `frame_recovery.go`: FrameRecovery, FrameRecoveryStrategy, FrameInfo, RecoveryMetrics
- `adaptive_quality.go`: AdaptiveQuality, QualityLevel, QualityProfile, Resolution, MLDataCollector
- `recovery_test.go`, `recovery_drop_frames_test.go`: Base recovery tests
- `stream_recovery_test.go`: Stream recovery tests
- `smart_recovery_test.go`: Smart recovery tests
