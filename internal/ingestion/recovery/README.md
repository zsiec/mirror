# Error Recovery Package

This package provides intelligent error recovery and resilience mechanisms for the Mirror video streaming platform. It handles various failure scenarios and implements recovery strategies to maintain stream quality and availability.

## Overview

Video streaming systems face numerous error conditions that can disrupt service. This package provides:

- **Error Detection**: Automatic identification of stream errors and corruption
- **Recovery Strategies**: Multiple approaches to handle different error types
- **Circuit Breakers**: Prevent cascading failures in distributed scenarios
- **State Management**: Track recovery attempts and success rates
- **Smart Recovery**: Learning-based recovery decisions

## Architecture

```
┌─────────────────┐
│ Error Detector  │ ← Stream Monitors
├─────────────────┤
│ • Frame Errors  │
│ • Sync Loss     │
│ • Corruption    │
│ • Timeouts      │
└─────────────────┘
         │
         ▼
┌─────────────────┐
│ Recovery Engine │
├─────────────────┤
│ • Strategy      │
│   Selection     │
│ • Action        │
│   Execution     │
│ • Success       │
│   Tracking      │
└─────────────────┘
         │
    ┌────┴─────┐
    ▼          ▼
┌─────────┐ ┌───────────┐
│Circuit  │ │Smart      │
│Breaker  │ │Recovery   │
└─────────┘ └───────────┘
```

## Error Types and Detection

### Stream Errors
```go
type ErrorType int

const (
    ErrorFrameCorruption ErrorType = iota
    ErrorSyncLoss
    ErrorPacketLoss
    ErrorTimeout
    ErrorBufferOverflow
    ErrorCodecFailure
    ErrorNetworkDisconnect
    ErrorAuthFailure
    ErrorResourceExhaustion
)

type StreamError struct {
    Type        ErrorType     `json:"type"`
    Severity    Severity      `json:"severity"`
    Message     string        `json:"message"`
    Context     ErrorContext  `json:"context"`
    Timestamp   time.Time     `json:"timestamp"`
    StreamID    string        `json:"stream_id"`
    Component   string        `json:"component"`
    Recoverable bool          `json:"recoverable"`
}
```

### Error Detection
```go
// Frame corruption detection
func (d *ErrorDetector) detectFrameCorruption(frame *Frame) *StreamError {
    // Check frame structure
    if !d.validateFrameStructure(frame) {
        return &StreamError{
            Type:      ErrorFrameCorruption,
            Severity:  SeverityMedium,
            Message:   "Invalid frame structure detected",
            Context:   ErrorContext{"frame_size": len(frame.Data)},
            Timestamp: time.Now(),
        }
    }
    
    // Codec-specific validation
    if err := d.validateCodecData(frame); err != nil {
        return &StreamError{
            Type:      ErrorFrameCorruption,
            Severity:  SeverityHigh,
            Message:   fmt.Sprintf("Codec validation failed: %v", err),
            Timestamp: time.Now(),
        }
    }
    
    return nil
}

// Sync loss detection
func (d *ErrorDetector) detectSyncLoss(stream *Stream) *StreamError {
    timeDrift := stream.GetTimeDrift()
    
    if abs(timeDrift) > d.config.SyncThreshold {
        return &StreamError{
            Type:      ErrorSyncLoss,
            Severity:  determineSyncSeverity(timeDrift),
            Message:   fmt.Sprintf("A/V sync drift: %v", timeDrift),
            Context:   ErrorContext{"drift_ms": timeDrift.Milliseconds()},
            Timestamp: time.Now(),
        }
    }
    
    return nil
}
```

## Recovery Strategies

### Strategy Selection
```go
type RecoveryStrategy interface {
    CanHandle(error *StreamError) bool
    Recover(ctx context.Context, stream *Stream, error *StreamError) RecoveryResult
    GetPriority() int
    GetSuccessRate() float64
}

type StrategySelector struct {
    strategies []RecoveryStrategy
    metrics    *StrategyMetrics
}

func (s *StrategySelector) SelectStrategy(err *StreamError) RecoveryStrategy {
    var candidates []RecoveryStrategy
    
    // Filter strategies that can handle this error
    for _, strategy := range s.strategies {
        if strategy.CanHandle(err) {
            candidates = append(candidates, strategy)
        }
    }
    
    if len(candidates) == 0 {
        return s.getDefaultStrategy()
    }
    
    // Select best strategy based on success rate and priority
    best := candidates[0]
    for _, candidate := range candidates[1:] {
        if s.isBetterStrategy(candidate, best, err) {
            best = candidate
        }
    }
    
    return best
}
```

### Frame Corruption Recovery
```go
type FrameCorruptionStrategy struct {
    gopBuffer     *gop.Buffer
    frameDropper  *FrameDropper
    successRate   float64
}

func (s *FrameCorruptionStrategy) Recover(ctx context.Context, stream *Stream, err *StreamError) RecoveryResult {
    switch err.Severity {
    case SeverityLow:
        // Try to repair frame
        if repairedFrame := s.repairFrame(stream.GetCurrentFrame()); repairedFrame != nil {
            stream.ReplaceCurrentFrame(repairedFrame)
            return RecoveryResult{Success: true, Action: "frame_repaired"}
        }
        fallthrough
        
    case SeverityMedium:
        // Drop corrupted frame and continue
        s.frameDropper.DropFrame(stream.GetCurrentFrame())
        return RecoveryResult{Success: true, Action: "frame_dropped"}
        
    case SeverityHigh:
        // Request keyframe and reset to last GOP
        if lastGOP := s.gopBuffer.GetLastValidGOP(); lastGOP != nil {
            stream.ResetToGOP(lastGOP)
            stream.RequestKeyframe()
            return RecoveryResult{Success: true, Action: "gop_reset_keyframe_requested"}
        }
        
    case SeverityCritical:
        // Full stream restart
        return s.restartStream(ctx, stream)
    }
    
    return RecoveryResult{Success: false, Action: "no_recovery_possible"}
}
```

### Sync Loss Recovery
```go
type SyncLossStrategy struct {
    driftCorrector *sync.DriftCorrector
    bufferManager  *buffer.Manager
}

func (s *SyncLossStrategy) Recover(ctx context.Context, stream *Stream, err *StreamError) RecoveryResult {
    driftMs := err.Context["drift_ms"].(int64)
    
    if abs(driftMs) < 100 {
        // Small drift - gradual correction
        s.driftCorrector.StartGradualCorrection(time.Duration(driftMs) * time.Millisecond)
        return RecoveryResult{Success: true, Action: "gradual_drift_correction"}
    }
    
    if abs(driftMs) < 1000 {
        // Medium drift - buffer adjustment
        s.bufferManager.AdjustBuffering(time.Duration(driftMs) * time.Millisecond)
        return RecoveryResult{Success: true, Action: "buffer_adjustment"}
    }
    
    // Large drift - resync from scratch
    stream.RequestResync()
    return RecoveryResult{Success: true, Action: "full_resync"}
}
```

### Network Disconnection Recovery
```go
type NetworkRecoveryStrategy struct {
    reconnector   *reconnect.Manager
    backoff       *ExponentialBackoff
    maxRetries    int
}

func (s *NetworkRecoveryStrategy) Recover(ctx context.Context, stream *Stream, err *StreamError) RecoveryResult {
    retryCount := stream.GetRetryCount()
    
    if retryCount >= s.maxRetries {
        return RecoveryResult{
            Success: false, 
            Action: "max_retries_exceeded",
            ShouldTerminate: true,
        }
    }
    
    // Wait with exponential backoff
    waitTime := s.backoff.NextDelay(retryCount)
    
    select {
    case <-time.After(waitTime):
        // Attempt reconnection
        if err := s.reconnector.Reconnect(stream); err != nil {
            stream.IncrementRetryCount()
            return RecoveryResult{
                Success: false,
                Action: "reconnect_failed",
                RetryAfter: waitTime * 2,
            }
        }
        
        // Success - reset retry count
        stream.ResetRetryCount()
        return RecoveryResult{Success: true, Action: "reconnected"}
        
    case <-ctx.Done():
        return RecoveryResult{Success: false, Action: "context_cancelled"}
    }
}
```

## Circuit Breaker Pattern

### Circuit Breaker Implementation
```go
type CircuitBreaker struct {
    name          string
    maxFailures   int
    resetTimeout  time.Duration
    state         CircuitState
    failures      int
    lastFailTime  time.Time
    successCount  int
    mu            sync.RWMutex
}

type CircuitState int

const (
    StateClosed   CircuitState = iota // Normal operation
    StateOpen                         // Failing fast
    StateHalfOpen                     // Testing recovery
)

func (cb *CircuitBreaker) Execute(operation func() error) error {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    
    switch cb.state {
    case StateOpen:
        // Check if reset timeout has passed
        if time.Since(cb.lastFailTime) > cb.resetTimeout {
            cb.state = StateHalfOpen
            cb.successCount = 0
        } else {
            return ErrCircuitBreakerOpen
        }
    }
    
    // Execute operation
    err := operation()
    
    if err != nil {
        cb.onFailure()
        return err
    }
    
    cb.onSuccess()
    return nil
}

func (cb *CircuitBreaker) onFailure() {
    cb.failures++
    cb.lastFailTime = time.Now()
    
    if cb.failures >= cb.maxFailures {
        cb.state = StateOpen
    }
}

func (cb *CircuitBreaker) onSuccess() {
    if cb.state == StateHalfOpen {
        cb.successCount++
        // Require multiple successes to close circuit
        if cb.successCount >= 3 {
            cb.state = StateClosed
            cb.failures = 0
        }
    } else if cb.state == StateClosed {
        cb.failures = 0
    }
}
```

### Stream Circuit Breaker
```go
// Circuit breaker for individual streams
type StreamCircuitBreaker struct {
    streamBreakers map[string]*CircuitBreaker
    mu            sync.RWMutex
}

func (scb *StreamCircuitBreaker) ExecuteForStream(streamID string, operation func() error) error {
    scb.mu.Lock()
    breaker, exists := scb.streamBreakers[streamID]
    if !exists {
        breaker = NewCircuitBreaker(
            fmt.Sprintf("stream-%s", streamID),
            5,                     // max failures
            30 * time.Second,      // reset timeout
        )
        scb.streamBreakers[streamID] = breaker
    }
    scb.mu.Unlock()
    
    return breaker.Execute(operation)
}
```

## Smart Recovery System

### Learning-Based Recovery
```go
type SmartRecovery struct {
    strategies      []RecoveryStrategy
    successHistory  map[string][]RecoveryAttempt
    patternMatcher  *PatternMatcher
    mlPredictor     *RecoveryPredictor
}

type RecoveryAttempt struct {
    ErrorType    ErrorType     `json:"error_type"`
    Strategy     string        `json:"strategy"`
    Success      bool          `json:"success"`
    Duration     time.Duration `json:"duration"`
    Context      ErrorContext  `json:"context"`
    StreamID     string        `json:"stream_id"`
    Timestamp    time.Time     `json:"timestamp"`
}

func (sr *SmartRecovery) SelectOptimalStrategy(err *StreamError, stream *Stream) RecoveryStrategy {
    // Get historical success rates
    history := sr.getRelevantHistory(err, stream)
    
    // Pattern matching for similar scenarios
    patterns := sr.patternMatcher.FindSimilarPatterns(err, stream.GetMetadata())
    
    // ML prediction for best strategy
    prediction := sr.mlPredictor.PredictBestStrategy(err, stream, history, patterns)
    
    // Combine traditional selection with ML insights
    traditional := sr.selectTraditionalStrategy(err)
    
    if prediction.Confidence > 0.8 {
        return sr.getStrategyByName(prediction.StrategyName)
    }
    
    return traditional
}
```

### Pattern Recognition
```go
type PatternMatcher struct {
    patterns []ErrorPattern
}

type ErrorPattern struct {
    ErrorSequence   []ErrorType           `json:"error_sequence"`
    StreamMetadata  map[string]interface{} `json:"stream_metadata"`
    OptimalStrategy string                `json:"optimal_strategy"`
    SuccessRate     float64               `json:"success_rate"`
    SampleSize      int                   `json:"sample_size"`
}

func (pm *PatternMatcher) FindSimilarPatterns(err *StreamError, metadata StreamMetadata) []ErrorPattern {
    var matches []ErrorPattern
    
    for _, pattern := range pm.patterns {
        similarity := pm.calculateSimilarity(err, metadata, pattern)
        if similarity > 0.7 {
            matches = append(matches, pattern)
        }
    }
    
    // Sort by similarity and success rate
    sort.Slice(matches, func(i, j int) bool {
        return matches[i].SuccessRate > matches[j].SuccessRate
    })
    
    return matches
}
```

## Recovery Handler Integration

### Stream Handler Integration
```go
// Recovery handler integrated with stream processing
type StreamHandler struct {
    recoveryHandler *recovery.Handler
    // ... other fields
}

func (h *StreamHandler) processFrame(frame *Frame) error {
    // Process frame with recovery wrapper
    return h.recoveryHandler.ExecuteWithRecovery(func() error {
        return h.actuallyProcessFrame(frame)
    })
}

func (h *StreamHandler) handleError(err error) {
    streamError := h.convertToStreamError(err)
    
    // Attempt recovery
    result := h.recoveryHandler.Recover(h.ctx, h.stream, streamError)
    
    if result.Success {
        h.logger.Info("Recovery successful", 
            "action", result.Action,
            "error_type", streamError.Type)
    } else {
        h.logger.Error("Recovery failed",
            "error", err,
            "attempted_action", result.Action)
        
        if result.ShouldTerminate {
            h.terminateStream()
        }
    }
}
```

## Monitoring and Metrics

### Recovery Metrics
```go
type RecoveryMetrics struct {
    // Recovery attempts
    RecoveryAttempts   *prometheus.CounterVec   // by error_type, strategy
    RecoverySuccesses  *prometheus.CounterVec   // by error_type, strategy
    RecoveryDuration   *prometheus.HistogramVec // by strategy
    
    // Circuit breaker metrics
    CircuitBreakerState *prometheus.GaugeVec    // by stream_id
    CircuitBreakerTrips *prometheus.CounterVec  // by stream_id
    
    // Error metrics
    ErrorsDetected     *prometheus.CounterVec   // by error_type, severity
    ErrorRecoveryRate  *prometheus.GaugeVec     // by error_type
    
    // Smart recovery metrics
    MLPredictionAccuracy prometheus.Gauge
    PatternMatchCount    *prometheus.CounterVec // by pattern_type
}

func (rm *RecoveryMetrics) RecordRecoveryAttempt(err *StreamError, strategy string, success bool, duration time.Duration) {
    labels := prometheus.Labels{
        "error_type": err.Type.String(),
        "strategy":   strategy,
    }
    
    rm.RecoveryAttempts.With(labels).Inc()
    
    if success {
        rm.RecoverySuccesses.With(labels).Inc()
    }
    
    rm.RecoveryDuration.With(prometheus.Labels{"strategy": strategy}).Observe(duration.Seconds())
}
```

### Recovery Statistics
```go
type RecoveryStatistics struct {
    TotalAttempts      uint64                    `json:"total_attempts"`
    SuccessfulAttempts uint64                    `json:"successful_attempts"`
    SuccessRate        float64                   `json:"success_rate"`
    
    // By error type
    ErrorTypeStats     map[ErrorType]ErrorStats  `json:"error_type_stats"`
    
    // By strategy
    StrategyStats      map[string]StrategyStats  `json:"strategy_stats"`
    
    // Recent activity
    RecentAttempts     []RecoveryAttempt         `json:"recent_attempts"`
    
    // Performance
    AverageRecoveryTime time.Duration            `json:"avg_recovery_time"`
    FastestRecovery     time.Duration            `json:"fastest_recovery"`
    SlowestRecovery     time.Duration            `json:"slowest_recovery"`
}

type ErrorStats struct {
    Count       uint64  `json:"count"`
    SuccessRate float64 `json:"success_rate"`
    LastSeen    time.Time `json:"last_seen"`
}

type StrategyStats struct {
    Usage       uint64  `json:"usage"`
    SuccessRate float64 `json:"success_rate"`
    AvgDuration time.Duration `json:"avg_duration"`
}
```

## Configuration

### Recovery Configuration
```go
type RecoveryConfig struct {
    // Error detection
    ErrorDetectionEnabled bool          `yaml:"error_detection_enabled"`
    SyncThreshold        time.Duration `yaml:"sync_threshold"`
    CorruptionThreshold  float64       `yaml:"corruption_threshold"`
    
    // Recovery strategies
    EnabledStrategies    []string      `yaml:"enabled_strategies"`
    MaxRecoveryAttempts  int           `yaml:"max_recovery_attempts"`
    RecoveryTimeout      time.Duration `yaml:"recovery_timeout"`
    
    // Circuit breaker
    CircuitBreakerConfig CircuitBreakerConfig `yaml:"circuit_breaker"`
    
    // Smart recovery
    MLEnabled            bool          `yaml:"ml_enabled"`
    PatternMatchingEnabled bool        `yaml:"pattern_matching_enabled"`
    LearningRate         float64       `yaml:"learning_rate"`
    
    // Performance
    MaxConcurrentRecoveries int        `yaml:"max_concurrent_recoveries"`
    RecoveryWorkers        int         `yaml:"recovery_workers"`
}

type CircuitBreakerConfig struct {
    MaxFailures   int           `yaml:"max_failures"`
    ResetTimeout  time.Duration `yaml:"reset_timeout"`
    Enabled       bool          `yaml:"enabled"`
}
```

## Testing

### Recovery Testing
```bash
# Test recovery mechanisms
go test ./internal/ingestion/recovery/...

# Test with fault injection
go test -tags=fault_injection ./internal/ingestion/recovery/...

# Test smart recovery
go test -run TestSmartRecovery ./internal/ingestion/recovery/...
```

### Fault Injection Testing
```go
// Fault injection for testing recovery
type FaultInjector struct {
    errorProbability float64
    errorTypes       []ErrorType
}

func (fi *FaultInjector) InjectError() *StreamError {
    if rand.Float64() < fi.errorProbability {
        errorType := fi.errorTypes[rand.Intn(len(fi.errorTypes))]
        return &StreamError{
            Type:      errorType,
            Severity:  SeverityMedium,
            Message:   "Injected error for testing",
            Timestamp: time.Now(),
        }
    }
    return nil
}
```

## Best Practices

1. **Layered Recovery**: Implement multiple recovery strategies for different error types
2. **Circuit Breakers**: Prevent cascading failures in distributed systems
3. **Gradual Recovery**: Use gentle correction methods before aggressive ones
4. **Learn from History**: Track success rates and adapt strategies
5. **Monitor Continuously**: Track recovery metrics and adjust thresholds
6. **Test Failure Scenarios**: Regularly test recovery mechanisms with fault injection
7. **Graceful Degradation**: Ensure system continues operating even with partial failures

## Integration Examples

### Pipeline Integration
```go
pipeline.SetErrorHandler(func(err error) {
    streamError := convertToStreamError(err)
    recoveryHandler.HandleError(streamError)
})
```

### Network Integration
```go
networkClient.SetCircuitBreaker(
    recovery.NewCircuitBreaker("network", 5, 30*time.Second)
)
```
