package recovery

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// StreamRecoveryStrategy defines how to recover from errors
type StreamRecoveryStrategy int

const (
	StreamRecoveryStrategyImmediate StreamRecoveryStrategy = iota
	StreamRecoveryStrategyBackoff
	StreamRecoveryStrategyCircuitBreaker
	StreamRecoveryStrategyAdaptive
)

// StreamRecoveryState represents the current recovery state
type StreamRecoveryState int

const (
	StreamRecoveryStateHealthy StreamRecoveryState = iota
	StreamRecoveryStateRecovering
	StreamRecoveryStateFailed
	StreamRecoveryStateDegraded
)

// RecoveryConfig contains recovery configuration
type RecoveryConfig struct {
	MaxRetries              int
	InitialBackoff          time.Duration
	MaxBackoff              time.Duration
	BackoffMultiplier       float64
	CircuitBreakerThreshold int
	CircuitBreakerTimeout   time.Duration
	HealthCheckInterval     time.Duration
	EnableStatePreservation bool
}

// DefaultRecoveryConfig returns default recovery configuration
func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		MaxRetries:              5,
		InitialBackoff:          1 * time.Second,
		MaxBackoff:              30 * time.Second,
		BackoffMultiplier:       2.0,
		CircuitBreakerThreshold: 3,
		CircuitBreakerTimeout:   60 * time.Second,
		HealthCheckInterval:     10 * time.Second,
		EnableStatePreservation: true,
	}
}

// StreamRecovery manages stream recovery operations
type StreamRecovery struct {
	mu       sync.RWMutex
	streamID string
	logger   logger.Logger
	config   RecoveryConfig

	// State
	state          atomic.Int32
	failures       atomic.Int32
	recoveries     atomic.Int32
	lastError      error
	lastRecovery   time.Time
	circuitBreaker *CircuitBreaker

	// Stream state preservation
	preservedState *PreservedState
	stateStore     StateStore

	// Callbacks
	onRecover        func() error
	onStateRestore   func(*PreservedState) error
	onRecoveryFailed func(error)

	// Metrics
	recoveryCounter  *metrics.Counter
	failureCounter   *metrics.Counter
	stateGauge       *metrics.Gauge
	recoveryDuration *metrics.Histogram

	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

// PreservedState contains preserved stream state
type PreservedState struct {
	StreamID         string
	LastSequence     uint16
	LastTimestamp    uint32
	LastFrameNumber  uint64
	ParameterSets    map[string][]byte
	BufferedFrames   []*types.VideoFrame
	Metadata         map[string]interface{}
	PreservationTime time.Time
}

// StateStore interface for state persistence
type StateStore interface {
	Save(state *PreservedState) error
	Load(streamID string) (*PreservedState, error)
	Delete(streamID string) error
}

// NewStreamRecovery creates a new stream recovery manager
func NewStreamRecovery(streamID string, logger logger.Logger, config RecoveryConfig) *StreamRecovery {
	ctx, cancel := context.WithCancel(context.Background())

	sr := &StreamRecovery{
		streamID:       streamID,
		logger:         logger,
		config:         config,
		lastRecovery:   time.Now(),
		circuitBreaker: NewCircuitBreaker(config.CircuitBreakerThreshold, config.CircuitBreakerTimeout),
		ctx:            ctx,
		cancel:         cancel,
	}

	sr.state.Store(int32(StreamRecoveryStateHealthy))

	// Initialize metrics
	sr.recoveryCounter = metrics.NewCounter("ingestion_recovery_total",
		map[string]string{"stream_id": streamID})
	sr.failureCounter = metrics.NewCounter("ingestion_recovery_failures",
		map[string]string{"stream_id": streamID})
	sr.stateGauge = metrics.NewGauge("ingestion_recovery_state",
		map[string]string{"stream_id": streamID})
	sr.recoveryDuration = metrics.NewHistogram("ingestion_recovery_duration_seconds",
		map[string]string{"stream_id": streamID}, []float64{0.1, 0.5, 1, 5, 10, 30})

	// Start health monitoring
	go sr.monitorHealth()

	return sr
}

// SetCallbacks sets recovery callbacks
func (sr *StreamRecovery) SetCallbacks(
	onRecover func() error,
	onStateRestore func(*PreservedState) error,
	onRecoveryFailed func(error)) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.onRecover = onRecover
	sr.onStateRestore = onStateRestore
	sr.onRecoveryFailed = onRecoveryFailed
}

// SetStateStore sets the state store for persistence
func (sr *StreamRecovery) SetStateStore(store StateStore) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.stateStore = store
}

// HandleError handles a stream error and attempts recovery
func (sr *StreamRecovery) HandleError(err error) {
	if err == nil {
		return
	}

	sr.mu.Lock()
	sr.lastError = err
	sr.mu.Unlock()

	sr.failures.Add(1)
	sr.failureCounter.Inc()

	sr.logger.WithError(err).Error("Stream error detected, initiating recovery")

	// Check circuit breaker
	if !sr.circuitBreaker.Allow() {
		sr.setState(StreamRecoveryStateFailed)
		sr.logger.Error("Circuit breaker open, recovery blocked")
		if sr.onRecoveryFailed != nil {
			sr.onRecoveryFailed(fmt.Errorf("circuit breaker open"))
		}
		return
	}

	// Preserve state if enabled
	if sr.config.EnableStatePreservation {
		if err := sr.preserveState(); err != nil {
			sr.logger.WithError(err).Error("Failed to preserve state")
		}
	}

	// Start recovery
	go sr.recover()
}

// GetState returns the current recovery state
func (sr *StreamRecovery) GetState() StreamRecoveryState {
	return StreamRecoveryState(sr.state.Load())
}

// GetStatistics returns recovery statistics
func (sr *StreamRecovery) GetStatistics() map[string]interface{} {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	return map[string]interface{}{
		"state":           sr.GetState().String(),
		"failures":        sr.failures.Load(),
		"recoveries":      sr.recoveries.Load(),
		"last_error":      sr.lastError,
		"last_recovery":   sr.lastRecovery,
		"circuit_breaker": sr.circuitBreaker.State(),
	}
}

// Stop stops the recovery manager
func (sr *StreamRecovery) Stop() {
	sr.cancel()
}

// Private methods

func (sr *StreamRecovery) recover() {
	start := time.Now()
	sr.setState(StreamRecoveryStateRecovering)

	// Snapshot callbacks under lock to avoid races with SetCallbacks
	sr.mu.RLock()
	onRecover := sr.onRecover
	onStateRestore := sr.onStateRestore
	onRecoveryFailed := sr.onRecoveryFailed
	sr.mu.RUnlock()

	backoff := sr.config.InitialBackoff

	for attempt := 1; attempt <= sr.config.MaxRetries; attempt++ {
		sr.logger.WithField("attempt", attempt).Info("Attempting recovery")

		// Try to restore state first
		if sr.config.EnableStatePreservation && sr.preservedState != nil {
			if onStateRestore != nil {
				if err := onStateRestore(sr.preservedState); err != nil {
					sr.logger.WithError(err).Warn("Failed to restore state")
				} else {
					sr.logger.Info("State restored successfully")
				}
			}
		}

		// Execute recovery callback
		if onRecover != nil {
			if err := onRecover(); err != nil {
				sr.logger.WithError(err).Warn("Recovery attempt failed")
				sr.circuitBreaker.RecordFailure()

				// Exponential backoff
				time.Sleep(backoff)
				backoff = time.Duration(float64(backoff) * sr.config.BackoffMultiplier)
				if backoff > sr.config.MaxBackoff {
					backoff = sr.config.MaxBackoff
				}
				continue
			}
		}

		// Recovery successful
		sr.recoveries.Add(1)
		sr.recoveryCounter.Inc()
		sr.recoveryDuration.Observe(time.Since(start).Seconds())
		sr.circuitBreaker.RecordSuccess()

		sr.mu.Lock()
		sr.lastRecovery = time.Now()
		sr.lastError = nil
		sr.mu.Unlock()

		sr.setState(StreamRecoveryStateHealthy)
		sr.logger.Info("Recovery successful")

		// Clean up preserved state
		if sr.stateStore != nil && sr.preservedState != nil {
			sr.stateStore.Delete(sr.streamID)
		}

		return
	}

	// All attempts failed
	sr.setState(StreamRecoveryStateFailed)
	if onRecoveryFailed != nil {
		onRecoveryFailed(fmt.Errorf("recovery failed after %d attempts", sr.config.MaxRetries))
	}
}

func (sr *StreamRecovery) preserveState() error {
	if sr.stateStore == nil {
		return nil
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Create preserved state
	// This is a simplified version - real implementation would gather actual state
	sr.preservedState = &PreservedState{
		StreamID:         sr.streamID,
		PreservationTime: time.Now(),
		Metadata:         make(map[string]interface{}),
	}

	// Save to store
	return sr.stateStore.Save(sr.preservedState)
}

func (sr *StreamRecovery) setState(state StreamRecoveryState) {
	sr.state.Store(int32(state))
	sr.stateGauge.Set(float64(state))

	sr.logger.WithField("state", state.String()).Info("Recovery state changed")
}

func (sr *StreamRecovery) monitorHealth() {
	ticker := time.NewTicker(sr.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sr.ctx.Done():
			return
		case <-ticker.C:
			sr.checkHealth()
		}
	}
}

func (sr *StreamRecovery) checkHealth() {
	state := sr.GetState()

	switch state {
	case StreamRecoveryStateFailed:
		// Check if we should retry
		sr.mu.RLock()
		timeSinceFailure := time.Since(sr.lastRecovery)
		sr.mu.RUnlock()

		if timeSinceFailure > sr.config.HealthCheckInterval {
			sr.logger.Info("Retrying after health check interval")
			sr.circuitBreaker.Reset()
			go sr.recover()
		}

	case StreamRecoveryStateDegraded:
		// Check if we can return to healthy
		// This would involve actual health checks
		sr.logger.Debug("Health check in degraded state")
	}
}

// String returns string representation of RecoveryState
func (s StreamRecoveryState) String() string {
	switch s {
	case StreamRecoveryStateHealthy:
		return "healthy"
	case StreamRecoveryStateRecovering:
		return "recovering"
	case StreamRecoveryStateFailed:
		return "failed"
	case StreamRecoveryStateDegraded:
		return "degraded"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements circuit breaker pattern
type CircuitBreaker struct {
	mu              sync.Mutex
	failures        int
	lastFailureTime time.Time
	threshold       int
	timeout         time.Duration
	state           string
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		timeout:   timeout,
		state:     "closed",
	}
}

// Allow checks if request is allowed
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "open" {
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = "half-open"
		} else {
			return false
		}
	}

	return true
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = "closed"
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailureTime = time.Now()

	if cb.failures >= cb.threshold {
		cb.state = "open"
	}
}

// State returns the current state
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = "closed"
}
