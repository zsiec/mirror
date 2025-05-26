# Backpressure Control Package

This package implements intelligent backpressure control for the Mirror video streaming platform. It monitors system load and applies graduated pressure relief to maintain streaming quality under resource constraints.

## Overview

Backpressure control is essential for maintaining stable video streaming performance when system resources become constrained. This package provides:

- **Load Monitoring**: Real-time tracking of CPU, memory, and network resources
- **Graduated Response**: Progressive measures to reduce load
- **Video-Aware Dropping**: Intelligent frame dropping based on video structure
- **Performance Metrics**: Comprehensive monitoring and alerting

## Core Concepts

### Pressure Measurement
Pressure is measured as a normalized value from 0.0 to 1.0:
- **0.0-0.3**: Normal operation, no action required
- **0.3-0.7**: Moderate pressure, start optimization
- **0.7-0.9**: High pressure, begin dropping frames
- **0.9-1.0**: Critical pressure, aggressive dropping

### Response Strategies
1. **Optimization** (0.3-0.7): Reduce buffer sizes, optimize algorithms
2. **Selective Dropping** (0.7-0.9): Drop B-frames, reduce quality
3. **Aggressive Dropping** (0.9+): Drop entire GOPs, maintain keyframes

## Architecture

```
┌─────────────────┐
│ System Monitor  │ ← CPU/Memory/Network
├─────────────────┤
│ • CPU Usage     │
│ • Memory Usage  │
│ • Queue Depth   │
│ • Network Load  │
└─────────────────┘
         │
         ▼
┌─────────────────┐
│ Controller      │
├─────────────────┤
│ • Calculate     │
│   Pressure      │
│ • Determine     │
│   Actions       │
│ • Signal        │
│   Components    │
└─────────────────┘
         │
    ┌────┴────┐
    ▼         ▼
┌─────────┐ ┌──────────┐
│Frame    │ │GOP       │
│Dropper  │ │Dropper   │
└─────────┘ └──────────┘
```

## Configuration

```go
type ControllerConfig struct {
    // Pressure thresholds
    ModerateThreshold float64 // Start optimization (default: 0.3)
    HighThreshold     float64 // Start dropping frames (default: 0.7)
    CriticalThreshold float64 // Aggressive dropping (default: 0.9)
    
    // Monitoring intervals
    MonitorInterval   time.Duration // How often to check (default: 100ms)
    SampleWindow      time.Duration // Sample averaging window (default: 5s)
    
    // Response parameters
    ResponseSpeed     float64 // How quickly to respond (default: 0.1)
    RecoverySpeed     float64 // How quickly to recover (default: 0.05)
    
    // Frame dropping preferences
    PreferBFrames     bool    // Drop B-frames first (default: true)
    PreservePFrames   bool    // Try to preserve P-frames (default: true)
    MinKeyframeRate   float64 // Minimum keyframe preservation (default: 1.0)
}
```

## Usage

### Basic Controller Setup

```go
import "github.com/zsiec/mirror/internal/ingestion/backpressure"

// Create configuration
config := backpressure.ControllerConfig{
    ModerateThreshold: 0.3,
    HighThreshold:     0.7,
    CriticalThreshold: 0.9,
    MonitorInterval:   100 * time.Millisecond,
    SampleWindow:      5 * time.Second,
}

// Create controller
controller, err := backpressure.NewController(config)
if err != nil {
    return fmt.Errorf("failed to create controller: %w", err)
}

// Start monitoring
controller.Start()
defer controller.Stop()
```

### Integration with Stream Handler

```go
// Register for pressure notifications
controller.OnPressureChange(func(pressure float64, action Action) {
    switch action {
    case ActionOptimize:
        streamHandler.ReduceBuffers()
    case ActionDropBFrames:
        streamHandler.EnableBFrameDropping()
    case ActionDropGOPs:
        streamHandler.EnableGOPDropping()
    case ActionNormal:
        streamHandler.RestoreNormalOperation()
    }
})
```

## Pressure Sources

### CPU Monitoring
```go
// CPU usage tracking
func (c *Controller) updateCPUPressure() {
    usage := c.getCPUUsage()
    
    // High CPU usage contributes to pressure
    if usage > 0.8 {
        c.addPressure("cpu", usage)
    }
}
```

### Memory Monitoring
```go
// Memory pressure detection
func (c *Controller) updateMemoryPressure() {
    memStats := c.getMemoryStats()
    
    // Memory usage above threshold
    usageRatio := float64(memStats.Used) / float64(memStats.Total)
    if usageRatio > 0.85 {
        c.addPressure("memory", usageRatio)
    }
    
    // GC pressure
    if memStats.GCPressure > 0.1 {
        c.addPressure("gc", memStats.GCPressure)
    }
}
```

### Queue Depth Monitoring
```go
// Network and processing queue monitoring
func (c *Controller) updateQueuePressure() {
    for _, queue := range c.monitoredQueues {
        depth := queue.GetDepth()
        capacity := queue.GetCapacity()
        
        ratio := float64(depth) / float64(capacity)
        if ratio > 0.8 {
            c.addPressure("queue_"+queue.Name(), ratio)
        }
    }
}
```

## Response Actions

### Frame-Level Actions

```go
type FrameAction int

const (
    FrameKeep FrameAction = iota
    FrameDrop
    FrameCompress
    FrameSkip
)

// Determine action for specific frame
func (c *Controller) GetFrameAction(frame *Frame) FrameAction {
    pressure := c.GetCurrentPressure()
    
    switch {
    case pressure < 0.3:
        return FrameKeep
        
    case pressure < 0.7:
        // Drop B-frames under moderate pressure
        if frame.Type == FrameTypeB {
            return FrameDrop
        }
        return FrameKeep
        
    case pressure < 0.9:
        // Drop B-frames and some P-frames
        if frame.Type == FrameTypeB {
            return FrameDrop
        }
        if frame.Type == FrameTypeP && c.shouldDropPFrame() {
            return FrameDrop
        }
        return FrameKeep
        
    default:
        // Critical pressure: only keep essential frames
        if frame.Type == FrameTypeI {
            return FrameKeep
        }
        return FrameDrop
    }
}
```

### GOP-Level Actions

```go
// GOP dropping for severe pressure
func (c *Controller) ShouldDropGOP(gop *GOP) bool {
    pressure := c.GetCurrentPressure()
    
    // Never drop GOP with keyframe
    if gop.HasKeyframe() {
        return false
    }
    
    // Drop GOPs under critical pressure
    if pressure > 0.95 {
        return c.dropDecision.ShouldDrop(gop)
    }
    
    return false
}
```

## Statistics and Monitoring

### Pressure Metrics
```go
type Statistics struct {
    // Current state
    CurrentPressure   float64   `json:"current_pressure"`
    PressureHistory   []float64 `json:"pressure_history"`
    
    // Pressure sources
    CPUPressure       float64   `json:"cpu_pressure"`
    MemoryPressure    float64   `json:"memory_pressure"`
    QueuePressure     float64   `json:"queue_pressure"`
    NetworkPressure   float64   `json:"network_pressure"`
    
    // Actions taken
    FramesDropped     uint64    `json:"frames_dropped"`
    GOPsDropped       uint64    `json:"gops_dropped"`
    ActionsOptimize   uint64    `json:"actions_optimize"`
    ActionsCritical   uint64    `json:"actions_critical"`
    
    // Response timing
    AverageResponse   time.Duration `json:"avg_response_time"`
    LastActionTime    time.Time     `json:"last_action_time"`
}
```

### Performance Tracking
```go
// Get current controller statistics
stats := controller.GetStatistics()

fmt.Printf("Current pressure: %.2f\n", stats.CurrentPressure)
fmt.Printf("Frames dropped: %d\n", stats.FramesDropped)
fmt.Printf("CPU pressure: %.2f\n", stats.CPUPressure)
fmt.Printf("Memory pressure: %.2f\n", stats.MemoryPressure)
```

## Advanced Features

### Adaptive Thresholds
```go
// Thresholds adjust based on stream characteristics
func (c *Controller) adaptThresholds(streamStats *StreamStats) {
    // Lower thresholds for high-motion content
    if streamStats.MotionLevel > 0.8 {
        c.config.HighThreshold *= 0.9
    }
    
    // Adjust for stream priority
    if streamStats.Priority == HighPriority {
        c.config.CriticalThreshold *= 1.1
    }
}
```

### Predictive Control
```go
// Predict future pressure based on trends
func (c *Controller) predictPressure(horizon time.Duration) float64 {
    history := c.pressureHistory
    if len(history) < 3 {
        return c.currentPressure
    }
    
    // Simple linear extrapolation
    recent := history[len(history)-3:]
    trend := (recent[2] - recent[0]) / 2.0
    
    // Project trend forward
    steps := float64(horizon / c.config.MonitorInterval)
    return c.currentPressure + (trend * steps)
}
```

### Recovery Strategies
```go
// Gradual recovery when pressure decreases
func (c *Controller) handleRecovery() {
    if c.currentPressure < c.lastPressure {
        // Gradually restore normal operation
        recoveryRate := c.config.RecoverySpeed
        
        // Faster recovery if pressure drops significantly
        if c.lastPressure - c.currentPressure > 0.2 {
            recoveryRate *= 2.0
        }
        
        c.restoreOperation(recoveryRate)
    }
}
```

## Integration Examples

### With Video Pipeline
```go
// Pipeline queries controller for frame decisions
pipeline.SetFrameFilter(func(frame *Frame) bool {
    action := controller.GetFrameAction(frame)
    return action == FrameKeep
})
```

### With Memory Manager
```go
// Memory manager reports pressure
memoryManager.OnPressure(func(pressure float64) {
    controller.AddPressureSource("memory", pressure)
})
```

### With Network Monitor
```go
// Network congestion contributes to pressure
networkMonitor.OnCongestion(func(congestion float64) {
    controller.AddPressureSource("network", congestion)
})
```

## Testing

### Unit Tests
```bash
# Test backpressure algorithms
go test ./internal/ingestion/backpressure/...

# Test with different pressure scenarios
go test -run TestPressureResponse ./internal/ingestion/backpressure/...
```

### Performance Tests
```bash
# Benchmark pressure calculation
go test -bench=BenchmarkPressure ./internal/ingestion/backpressure/...

# Test responsiveness
go test -run TestResponseTime ./internal/ingestion/backpressure/...
```

### Integration Tests
```bash
# Test with real stream data
go test -tags=integration ./internal/ingestion/backpressure/...
```

## Best Practices

1. **Monitor Continuously**: Never disable backpressure monitoring
2. **Tune Thresholds**: Adjust based on hardware and content characteristics  
3. **Preserve Quality**: Always prioritize keyframes and important P-frames
4. **Gradual Response**: Avoid abrupt changes that cause quality oscillation
5. **Log Actions**: Track all backpressure actions for debugging

## Troubleshooting

### High Pressure Scenarios
- **Cause**: Insufficient hardware resources
- **Solution**: Scale hardware or reduce stream count

### Oscillating Pressure  
- **Cause**: Thresholds too close together
- **Solution**: Increase threshold separation

### Slow Response
- **Cause**: MonitorInterval too large
- **Solution**: Reduce interval (balance with CPU usage)

### Over-Aggressive Dropping
- **Cause**: ResponseSpeed too high
- **Solution**: Reduce response speed for smoother control
