# Backpressure Package

The `backpressure` package provides a rate-based backpressure controller with GOP-aware adjustments for video stream ingestion.

## Architecture

```
UpdatePressure(0.0-1.0) → pressure history (weighted smoothing)
                                    ↓
                         controlLoop (periodic ticker)
                                    ↓
                         adjustRate() → increase / decrease / stable
                                    ↓
                         applyGOPAdjustments() → snap to GOP boundaries
                                    ↓
                         enforceRateLimits() → clamp to [MinRate, MaxRate]
                                    ↓
                         onRateChange callback
```

## Usage

```go
ctrl := backpressure.NewController("stream-123", backpressure.Config{
    MinRate:        1000000,          // 1 MB/s minimum
    MaxRate:        52428800,         // 50 MB/s maximum
    TargetPressure: 0.7,             // Target 70% utilization
    IncreaseRatio:  1.1,             // 10% increase per adjustment
    DecreaseRatio:  0.9,             // 10% decrease per adjustment
    AdjustInterval: 100 * time.Millisecond,
    HistorySize:    10,
}, logger)

ctrl.Start()   // Starts background control loop
defer ctrl.Stop()

// Feed pressure readings from downstream
ctrl.UpdatePressure(0.8)

// Query state
rate := ctrl.GetCurrentRate()          // Current rate in bytes/sec
pressure := ctrl.GetPressure()         // Current raw pressure
smoothed := ctrl.GetSmoothedPressure() // Weighted average pressure
shouldDrop := ctrl.ShouldDropGOP(0.95) // Whether to drop entire GOPs
```

## Config

```go
type Config struct {
    MinRate        int64         // Minimum rate in bytes/sec
    MaxRate        int64         // Maximum rate in bytes/sec
    TargetPressure float64       // Target pressure level (0.0-1.0)
    IncreaseRatio  float64       // Rate increase multiplier (e.g., 1.1 = +10%)
    DecreaseRatio  float64       // Rate decrease multiplier (e.g., 0.9 = -10%)
    AdjustInterval time.Duration // How often to adjust rates
    HistorySize    int           // Pressure history window for smoothing
}
```

## API

```go
// Lifecycle
ctrl.Start()
ctrl.Stop()

// Pressure input
ctrl.UpdatePressure(pressure float64)
ctrl.UpdateGOPStats(stats *gop.GOPStatistics)

// State queries
ctrl.GetPressure() float64           // Raw current pressure
ctrl.GetSmoothedPressure() float64   // Weighted average over history
ctrl.GetCurrentRate() int64          // Current rate in bytes/sec
ctrl.ShouldDropGOP(pressure float64) bool

// Callbacks
ctrl.SetRateChangeCallback(func(newRate int64))
ctrl.SetGOPDropCallback(func(gopID uint64))

// Statistics
ctrl.GetStatistics() Statistics
ctrl.IncrementGOPsDropped()
ctrl.ClearPressureHistory()          // For testing
```

## Rate Adjustment Algorithm

The control loop runs every `AdjustInterval` and adjusts the rate based on pressure:

1. **Blend pressure**: Uses 70/30 current/smoothed when rapid changes detected, otherwise uses smoothed
2. **Calculate new rate**:
   - `pressure < target * 0.9` → increase (more aggressive if pressure < target * 0.5)
   - `pressure > target * 1.1` → decrease (more aggressive above 0.8 and 0.9)
   - Within range → small proportional correction (10% per unit error)
3. **GOP-aware adjustments**: If rate drops below 1 GOP/sec, snaps to GOP-aligned boundaries
4. **Enforce limits**: Clamps to `[MinRate, MaxRate]`

## GOP Drop Logic

`ShouldDropGOP(pressure)` returns true when:
- Pressure >= 0.9, AND
- Either at minimum rate, or below 3 GOPs/sec, or pressure >= 0.95

## Statistics

```go
type Statistics struct {
    CurrentRate      int64
    CurrentPressure  float64
    SmoothedPressure float64
    AdjustmentCount  uint64
    GOPsDropped      uint64
    LastAdjustment   time.Time
    TargetPressure   float64
    MinRate          int64
    MaxRate          int64
    GOPsPerSecond    float64
}
```

## Files

- `controller.go`: Controller, Config, Statistics, rate adjustment algorithm
- `controller_test.go`: Core tests
- `controller_stop_test.go`: Lifecycle/shutdown tests
- `controller_responsiveness_test.go`: Rate responsiveness tests
