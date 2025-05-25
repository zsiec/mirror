package backpressure

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/gop"
	"github.com/zsiec/mirror/internal/logger"
)

// Controller implements video-aware backpressure control
type Controller struct {
	streamID string

	// Pressure levels
	currentPressure atomic.Value // float64
	targetPressure  float64

	// Rate control
	currentRate atomic.Int64 // bytes per second
	minRate     int64        // minimum allowed rate
	maxRate     int64        // maximum allowed rate

	// GOP-aware adjustments
	gopStats       *gop.GOPStatistics
	avgGOPSize     int64
	avgGOPDuration time.Duration

	// Adjustment parameters
	increaseRatio  float64       // How much to increase rate when pressure is low
	decreaseRatio  float64       // How much to decrease rate when pressure is high
	adjustInterval time.Duration // How often to adjust rates

	// History for smoothing
	pressureHistory []float64
	historySize     int

	// Callbacks
	onRateChange func(newRate int64)
	onDropGOP    func(gopID uint64)

	// Statistics
	adjustmentCount atomic.Uint64
	gopsDropped     atomic.Uint64
	lastAdjustment  time.Time

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.RWMutex
	logger logger.Logger
}

// Config configures the backpressure controller
type Config struct {
	MinRate        int64         // Minimum rate in bytes/sec
	MaxRate        int64         // Maximum rate in bytes/sec
	TargetPressure float64       // Target pressure level (0.0-1.0)
	IncreaseRatio  float64       // Rate increase ratio
	DecreaseRatio  float64       // Rate decrease ratio
	AdjustInterval time.Duration // Adjustment interval
	HistorySize    int           // Size of pressure history for smoothing
}

// NewController creates a new backpressure controller
func NewController(streamID string, config Config, logger logger.Logger) *Controller {
	ctx, cancel := context.WithCancel(context.Background())

	c := &Controller{
		streamID:        streamID,
		targetPressure:  config.TargetPressure,
		minRate:         config.MinRate,
		maxRate:         config.MaxRate,
		increaseRatio:   config.IncreaseRatio,
		decreaseRatio:   config.DecreaseRatio,
		adjustInterval:  config.AdjustInterval,
		historySize:     config.HistorySize,
		pressureHistory: make([]float64, 0, config.HistorySize),
		ctx:             ctx,
		cancel:          cancel,
		logger:          logger.WithField("component", "backpressure_controller"),
	}

	// Set initial rate to max
	c.currentRate.Store(config.MaxRate)
	c.currentPressure.Store(0.0)

	return c
}

// Start begins the backpressure control loop
func (c *Controller) Start() {
	go c.controlLoop()
}

// Stop stops the backpressure controller
func (c *Controller) Stop() {
	c.cancel()
}

// controlLoop runs the main control algorithm
func (c *Controller) controlLoop() {
	ticker := time.NewTicker(c.adjustInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Debug("Backpressure control loop stopped")
			return
		case <-ticker.C:
			c.adjustRate()
		}
	}
}

// UpdatePressure updates the current pressure reading
func (c *Controller) UpdatePressure(pressure float64) {
	c.currentPressure.Store(pressure)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Add to history
	c.pressureHistory = append(c.pressureHistory, pressure)
	if len(c.pressureHistory) > c.historySize {
		c.pressureHistory = c.pressureHistory[1:]
	}
}

// UpdateGOPStats updates GOP statistics for better rate calculation
func (c *Controller) UpdateGOPStats(stats *gop.GOPStatistics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.gopStats = stats

	// Calculate average GOP size if we have data
	if stats.AverageGOPSize > 0 && stats.AverageDuration > 0 {
		// Average GOP size in bytes (assuming average frame size * GOP size)
		c.avgGOPSize = int64(stats.AverageGOPSize * 1500) // Assume 1500 bytes avg frame
		c.avgGOPDuration = stats.AverageDuration
	}
}

// adjustRate adjusts the ingestion rate based on pressure
func (c *Controller) adjustRate() {
	currentPressure := c.GetPressure()
	smoothedPressure := c.GetSmoothedPressure()
	currentRate := c.currentRate.Load()

	// Use current pressure for immediate responsiveness, but consider smoothed for stability
	pressure := currentPressure

	// If there's a significant difference between current and smoothed pressure,
	// use a weighted blend favoring immediate response to rapid changes
	pressureDiff := currentPressure - smoothedPressure
	if pressureDiff > 0.1 || pressureDiff < -0.1 {
		// Rapid change detected, favor immediate pressure for responsiveness
		pressure = currentPressure*0.7 + smoothedPressure*0.3
	} else {
		// Stable conditions, use smoothed pressure
		pressure = smoothedPressure
	}

	var newRate int64

	if pressure < c.targetPressure*0.9 {
		// Low pressure - increase rate
		newRate = c.calculateIncreaseRate(currentRate, pressure)
	} else if pressure > c.targetPressure*1.1 {
		// High pressure - decrease rate
		newRate = c.calculateDecreaseRate(currentRate, pressure)
	} else {
		// Within target range - minor adjustments
		newRate = c.calculateStableRate(currentRate, pressure)
	}

	// Apply GOP-aware adjustments
	newRate = c.applyGOPAdjustments(newRate, pressure)

	// Enforce limits
	newRate = c.enforceRateLimits(newRate)

	// Apply new rate if changed at all (even small changes matter for responsiveness)
	if newRate != currentRate {
		c.currentRate.Store(newRate)
		c.adjustmentCount.Add(1)
		c.mu.Lock()
		c.lastAdjustment = time.Now()
		c.mu.Unlock()

		if c.onRateChange != nil {
			c.onRateChange(newRate)
		}

		c.logger.WithFields(map[string]interface{}{
			"old_rate":          currentRate,
			"new_rate":          newRate,
			"pressure":          pressure,
			"current_pressure":  currentPressure,
			"smoothed_pressure": smoothedPressure,
			"target":            c.targetPressure,
		}).Info("Rate adjusted")
	}
}

// calculateIncreaseRate calculates rate increase for low pressure
func (c *Controller) calculateIncreaseRate(currentRate int64, pressure float64) int64 {
	// More aggressive increase when pressure is very low
	multiplier := c.increaseRatio
	if pressure < c.targetPressure*0.5 {
		multiplier *= 1.5
	}

	return int64(float64(currentRate) * multiplier)
}

// calculateDecreaseRate calculates rate decrease for high pressure
func (c *Controller) calculateDecreaseRate(currentRate int64, pressure float64) int64 {
	// More aggressive decrease when pressure is very high
	multiplier := c.decreaseRatio
	if pressure > 0.9 {
		multiplier *= 0.5 // Cut rate more aggressively
	} else if pressure > 0.8 {
		multiplier *= 0.7
	}

	return int64(float64(currentRate) * multiplier)
}

// calculateStableRate makes minor adjustments when near target
func (c *Controller) calculateStableRate(currentRate int64, pressure float64) int64 {
	// Small proportional adjustment
	error := c.targetPressure - pressure
	adjustment := 1.0 + (error * 0.1) // 10% adjustment per unit error

	return int64(float64(currentRate) * adjustment)
}

// applyGOPAdjustments applies GOP-aware rate adjustments
func (c *Controller) applyGOPAdjustments(rate int64, pressure float64) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.avgGOPSize == 0 || c.avgGOPDuration == 0 {
		return rate
	}

	// Calculate GOPs per second we can handle at this rate
	gopsPerSecond := float64(rate) / float64(c.avgGOPSize)
	if gopsPerSecond < 0.1 {
		gopsPerSecond = 0.1 // Minimum 0.1 GOPs per second
	}
	gopInterval := time.Duration(float64(time.Second) / gopsPerSecond)

	// If we're dropping below 1 GOP per second, snap to GOP boundaries
	if gopInterval > time.Second {
		// Round to nearest GOP rate
		if pressure > 0.8 {
			// Under high pressure, drop to exactly 1 GOP per 2 seconds
			rate = c.avgGOPSize / 2
		} else {
			// Otherwise, maintain at least 1 GOP per second
			rate = c.avgGOPSize
		}

		c.logger.WithFields(map[string]interface{}{
			"gops_per_sec":  gopsPerSecond,
			"gop_interval":  gopInterval,
			"adjusted_rate": rate,
		}).Debug("Applied GOP boundary adjustment")
	}

	return rate
}

// enforceRateLimits ensures rate stays within configured bounds
func (c *Controller) enforceRateLimits(rate int64) int64 {
	if rate < c.minRate {
		return c.minRate
	}
	if rate > c.maxRate {
		return c.maxRate
	}
	return rate
}

// GetSmoothedPressure returns averaged pressure over history
func (c *Controller) GetSmoothedPressure() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.pressureHistory) == 0 {
		return c.currentPressure.Load().(float64)
	}

	// Calculate weighted average (recent values weighted more)
	var sum, weightSum float64
	for i, p := range c.pressureHistory {
		weight := float64(i + 1) // More recent = higher weight
		sum += p * weight
		weightSum += weight
	}

	return sum / weightSum
}

// GetCurrentRate returns the current ingestion rate
func (c *Controller) GetCurrentRate() int64 {
	return c.currentRate.Load()
}

// GetPressure returns the current pressure
func (c *Controller) GetPressure() float64 {
	return c.currentPressure.Load().(float64)
}

// ShouldDropGOP determines if we should drop an entire GOP
func (c *Controller) ShouldDropGOP(pressure float64) bool {
	// Drop entire GOPs only under extreme pressure
	if pressure < 0.9 {
		return false
	}

	// Check if we're already at minimum rate
	currentRate := c.currentRate.Load()
	if currentRate <= c.minRate {
		return true
	}

	// Check if we're below 1 GOP per second
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.avgGOPSize > 0 {
		gopsPerSecond := float64(currentRate) / float64(c.avgGOPSize)
		// Under extreme pressure, drop GOPs if rate is low relative to GOP size
		return gopsPerSecond < 3.0 || pressure >= 0.95
	}

	return false
}

// SetRateChangeCallback sets the callback for rate changes
func (c *Controller) SetRateChangeCallback(callback func(newRate int64)) {
	c.onRateChange = callback
}

// SetGOPDropCallback sets the callback for GOP drops
func (c *Controller) SetGOPDropCallback(callback func(gopID uint64)) {
	c.onDropGOP = callback
}

// GetStatistics returns controller statistics
func (c *Controller) GetStatistics() Statistics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := Statistics{
		CurrentRate:      c.currentRate.Load(),
		CurrentPressure:  c.currentPressure.Load().(float64),
		SmoothedPressure: c.GetSmoothedPressure(),
		AdjustmentCount:  c.adjustmentCount.Load(),
		GOPsDropped:      c.gopsDropped.Load(),
		LastAdjustment:   c.lastAdjustment,
		TargetPressure:   c.targetPressure,
		MinRate:          c.minRate,
		MaxRate:          c.maxRate,
	}

	if c.gopStats != nil {
		stats.GOPsPerSecond = float64(c.currentRate.Load()) / float64(c.avgGOPSize)
	}

	return stats
}

// Statistics contains backpressure controller statistics
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

// IncrementGOPsDropped increments the GOPs dropped counter
func (c *Controller) IncrementGOPsDropped() {
	c.gopsDropped.Add(1)
}

// ClearPressureHistory clears the pressure history (for testing)
func (c *Controller) ClearPressureHistory() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pressureHistory = c.pressureHistory[:0]
}
