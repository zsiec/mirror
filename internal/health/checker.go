package health

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Status represents the health status of a component.
type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// Check represents a health check result.
type Check struct {
	Name        string                 `json:"name"`
	Status      Status                 `json:"status"`
	Message     string                 `json:"message,omitempty"`
	LastChecked time.Time              `json:"last_checked"`
	Duration    time.Duration          `json:"-"`
	DurationMS  float64                `json:"duration_ms"`
	Details     map[string]interface{} `json:"details,omitempty"`
}

// Checker is the interface that health checkers must implement.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

// Manager manages health checks.
type Manager struct {
	checkers []Checker
	results  map[string]*Check
	mu       sync.RWMutex
	logger   *logrus.Logger
}

// NewManager creates a new health check manager.
func NewManager(logger *logrus.Logger) *Manager {
	return &Manager{
		checkers: make([]Checker, 0),
		results:  make(map[string]*Check),
		logger:   logger,
	}
}

// Register adds a new health checker.
func (m *Manager) Register(checker Checker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkers = append(m.checkers, checker)
	m.logger.WithField("checker", checker.Name()).Debug("Registered health checker")
}

// RunChecks executes all registered health checks.
func (m *Manager) RunChecks(ctx context.Context) map[string]*Check {
	var wg sync.WaitGroup
	results := make(map[string]*Check, len(m.checkers))
	resultsChan := make(chan *Check, len(m.checkers))

	// Run all checks concurrently
	for _, checker := range m.checkers {
		wg.Add(1)
		go func(c Checker) {
			defer wg.Done()

			// Create a timeout context for individual checks
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			start := time.Now()
			err := c.Check(checkCtx)
			duration := time.Since(start)

			check := &Check{
				Name:        c.Name(),
				LastChecked: time.Now(),
				Duration:    duration,
				DurationMS:  float64(duration.Milliseconds()),
			}

			if err != nil {
				if err == context.DeadlineExceeded {
					check.Status = StatusDown
					check.Message = "Health check timed out"
				} else {
					check.Status = StatusDown
					check.Message = err.Error()
				}
				m.logger.WithFields(logrus.Fields{
					"checker":  c.Name(),
					"duration": duration,
					"error":    err,
				}).Error("Health check failed")
			} else {
				check.Status = StatusOK
				m.logger.WithFields(logrus.Fields{
					"checker":  c.Name(),
					"duration": duration,
				}).Debug("Health check passed")
			}

			resultsChan <- check
		}(checker)
	}

	// Wait for all checks to complete
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	for check := range resultsChan {
		results[check.Name] = check
		m.mu.Lock()
		m.results[check.Name] = check
		m.mu.Unlock()
	}

	return results
}

// GetResults returns the latest health check results.
func (m *Manager) GetResults() map[string]*Check {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string]*Check, len(m.results))
	for k, v := range m.results {
		// Create a copy to avoid race conditions
		checkCopy := *v
		results[k] = &checkCopy
	}
	return results
}

// GetOverallStatus returns the overall system health status.
func (m *Manager) GetOverallStatus() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.results) == 0 {
		return StatusDown
	}

	hasDown := false
	hasDegraded := false

	for _, check := range m.results {
		switch check.Status {
		case StatusDown:
			hasDown = true
		case StatusDegraded:
			hasDegraded = true
		}
	}

	if hasDown {
		return StatusDown
	}
	if hasDegraded {
		return StatusDegraded
	}
	return StatusOK
}

// StartPeriodicChecks starts running health checks periodically.
func (m *Manager) StartPeriodicChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run initial check
	m.RunChecks(ctx)

	for {
		select {
		case <-ticker.C:
			m.RunChecks(ctx)
		case <-ctx.Done():
			m.logger.Info("Stopping periodic health checks")
			return
		}
	}
}
