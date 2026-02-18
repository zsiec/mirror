package reconnect

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Strategy defines the reconnection strategy interface
type Strategy interface {
	// NextDelay returns the next delay duration and whether to continue retrying
	NextDelay() (time.Duration, bool)
	// Reset resets the strategy to initial state
	Reset()
}

// ExponentialBackoff implements exponential backoff with jitter
type ExponentialBackoff struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	MaxRetries   int

	currentDelay time.Duration
	retryCount   int
	mu           sync.Mutex
}

// NewExponentialBackoff creates a new exponential backoff strategy
func NewExponentialBackoff(initialDelay, maxDelay time.Duration, multiplier float64, maxRetries int) *ExponentialBackoff {
	return &ExponentialBackoff{
		InitialDelay: initialDelay,
		MaxDelay:     maxDelay,
		Multiplier:   multiplier,
		MaxRetries:   maxRetries,
		currentDelay: initialDelay,
	}
}

// NextDelay returns the next delay with exponential backoff and jitter
func (e *ExponentialBackoff) NextDelay() (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.MaxRetries > 0 && e.retryCount >= e.MaxRetries {
		return 0, false
	}

	// Calculate next delay with jitter (±20%)
	jitterFloat := 0.8 + (0.4 * rand.Float64())
	delay := time.Duration(float64(e.currentDelay) * jitterFloat)

	// Update for next iteration
	e.currentDelay = time.Duration(float64(e.currentDelay) * e.Multiplier)
	if e.currentDelay > e.MaxDelay {
		e.currentDelay = e.MaxDelay
	}
	e.retryCount++

	return delay, true
}

// Reset resets the backoff strategy
func (e *ExponentialBackoff) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.currentDelay = e.InitialDelay
	e.retryCount = 0
}

// Manager handles reconnection logic for a connection
type Manager struct {
	strategy  Strategy
	logger    *logrus.Logger
	onConnect func(ctx context.Context) error
	onSuccess func()
	onFailure func(err error)

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

// NewManager creates a new reconnection manager
func NewManager(strategy Strategy, logger *logrus.Logger) *Manager {
	return &Manager{
		strategy: strategy,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// SetCallbacks sets the reconnection callbacks
func (m *Manager) SetCallbacks(onConnect func(context.Context) error, onSuccess func(), onFailure func(error)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.onConnect = onConnect
	m.onSuccess = onSuccess
	m.onFailure = onFailure
}

// Start begins the reconnection process
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	if m.onConnect == nil {
		m.mu.Unlock()
		m.logger.Error("Cannot start reconnection: onConnect callback is nil")
		return
	}
	m.running = true
	m.stopCh = make(chan struct{}) // Recreate channel for reuse after Stop()
	m.mu.Unlock()

	go m.reconnectLoop(ctx)
}

// Stop stops the reconnection process
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return
	}

	m.running = false
	close(m.stopCh)
}

func (m *Manager) reconnectLoop(ctx context.Context) {
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		default:
		}

		// Attempt connection
		err := m.onConnect(ctx)
		if err == nil {
			// Success - reset strategy and notify
			m.strategy.Reset()
			if m.onSuccess != nil {
				m.onSuccess()
			}
			return
		}

		// Failed - get next delay
		delay, shouldRetry := m.strategy.NextDelay()
		if !shouldRetry {
			m.logger.Error("Maximum reconnection attempts reached")
			if m.onFailure != nil {
				m.onFailure(err)
			}
			return
		}

		m.logger.WithError(err).WithField("retry_in", delay).Warn("Connection failed, retrying")

		// Wait before next attempt
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.stopCh:
			timer.Stop()
			return
		case <-timer.C:
			// Continue to next iteration
		}
	}
}

// LinearBackoff implements a simple linear backoff strategy
type LinearBackoff struct {
	Delay      time.Duration
	MaxRetries int

	retryCount int
	mu         sync.Mutex
}

// NewLinearBackoff creates a new linear backoff strategy
func NewLinearBackoff(delay time.Duration, maxRetries int) *LinearBackoff {
	return &LinearBackoff{
		Delay:      delay,
		MaxRetries: maxRetries,
	}
}

// NextDelay returns a fixed delay for linear backoff
func (l *LinearBackoff) NextDelay() (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.MaxRetries > 0 && l.retryCount >= l.MaxRetries {
		return 0, false
	}

	l.retryCount++
	return l.Delay, true
}

// Reset resets the backoff strategy
func (l *LinearBackoff) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.retryCount = 0
}

// Helper function to calculate exponential backoff with maximum
func calculateBackoff(attempt int, base time.Duration, max time.Duration) time.Duration {
	if attempt <= 0 {
		return base
	}

	backoff := base * time.Duration(math.Pow(2, float64(attempt-1)))
	if backoff > max {
		return max
	}
	return backoff
}
