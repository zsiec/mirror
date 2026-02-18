package monitoring

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/integrity"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// HealthStatus represents overall health status
type HealthStatus int

const (
	HealthStatusHealthy HealthStatus = iota
	HealthStatusDegraded
	HealthStatusUnhealthy
	HealthStatusCritical
)

// StreamHealth contains health information for a stream
type StreamHealth struct {
	StreamID        string
	Status          HealthStatus
	Score           float64
	LastUpdate      time.Time
	Metrics         HealthMetrics
	Issues          []HealthIssue
	Recommendations []string
}

// HealthMetrics contains detailed health metrics
type HealthMetrics struct {
	// Performance metrics
	ProcessingLatency time.Duration
	ThroughputMbps    float64
	CPUUsage          float64
	MemoryUsage       float64

	// Quality metrics
	FrameDropRate   float64
	PacketLossRate  float64
	BitrateVariance float64
	TimestampDrift  time.Duration

	// Stability metrics
	UptimeMinutes     float64
	RecoveryCount     int
	ErrorRate         float64
	BufferUtilization float64

	// Corruption metrics
	CorruptionEvents int
	LastCorruption   time.Time
}

// HealthIssue represents a specific health problem
type HealthIssue struct {
	Type        string
	Severity    IssueSeverity
	Description string
	Timestamp   time.Time
	Impact      string
}

// IssueSeverity levels
type IssueSeverity int

const (
	IssueSeverityInfo IssueSeverity = iota
	IssueSeverityWarning
	IssueSeverityError
	IssueSeverityCritical
)

// HealthMonitor provides real-time health monitoring
type HealthMonitor struct {
	mu       sync.RWMutex
	streamID string
	logger   logger.Logger

	// Components
	validator          *integrity.StreamValidator
	corruptionDetector *CorruptionDetector

	// Configuration
	updateInterval    time.Duration
	degradedThreshold float64
	criticalThreshold float64

	// State
	currentHealth atomic.Pointer[StreamHealth]
	healthHistory []StreamHealth
	historySize   int

	// Alerting
	alertThresholds map[string]float64
	activeAlerts    map[string]*Alert
	alertCooldown   time.Duration

	// Callbacks
	onHealthChange func(old, new StreamHealth)
	onAlert        func(alert Alert)

	// Metrics
	healthGauge  *metrics.Gauge
	statusGauge  *metrics.Gauge
	issueCounter *metrics.Counter
	alertCounter *metrics.Counter

	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

// Alert represents a health alert
type Alert struct {
	ID         string
	StreamID   string
	Type       string
	Severity   AlertSeverity
	Message    string
	Details    map[string]interface{}
	Timestamp  time.Time
	Resolved   bool
	ResolvedAt time.Time
}

// AlertSeverity levels
type AlertSeverity int

const (
	AlertSeverityInfo AlertSeverity = iota
	AlertSeverityWarning
	AlertSeverityError
	AlertSeverityCritical
)

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(streamID string, logger logger.Logger) *HealthMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	hm := &HealthMonitor{
		streamID:          streamID,
		logger:            logger,
		updateInterval:    5 * time.Second,
		degradedThreshold: 0.7,
		criticalThreshold: 0.5,
		healthHistory:     make([]StreamHealth, 0, 100),
		historySize:       100,
		alertThresholds:   make(map[string]float64),
		activeAlerts:      make(map[string]*Alert),
		alertCooldown:     5 * time.Minute,
		ctx:               ctx,
		cancel:            cancel,
	}

	// Set default alert thresholds
	hm.alertThresholds = map[string]float64{
		"frame_drop_rate":    0.05,  // 5%
		"packet_loss_rate":   0.02,  // 2%
		"cpu_usage":          0.85,  // 85%
		"memory_usage":       0.90,  // 90%
		"processing_latency": 100.0, // 100ms
		"error_rate":         0.01,  // 1%
	}

	// Initialize metrics
	hm.healthGauge = metrics.NewGauge("ingestion_health_score",
		map[string]string{"stream_id": streamID})
	hm.statusGauge = metrics.NewGauge("ingestion_health_status",
		map[string]string{"stream_id": streamID})
	hm.issueCounter = metrics.NewCounter("ingestion_health_issues",
		map[string]string{"stream_id": streamID})
	hm.alertCounter = metrics.NewCounter("ingestion_health_alerts",
		map[string]string{"stream_id": streamID})

	// Initialize current health
	initialHealth := &StreamHealth{
		StreamID:   streamID,
		Status:     HealthStatusHealthy,
		Score:      1.0,
		LastUpdate: time.Now(),
		Metrics:    HealthMetrics{},
		Issues:     []HealthIssue{},
	}
	hm.currentHealth.Store(initialHealth)

	// Start monitoring
	go hm.monitorLoop()

	return hm
}

// SetComponents sets monitoring components
func (hm *HealthMonitor) SetComponents(validator *integrity.StreamValidator, detector *CorruptionDetector) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.validator = validator
	hm.corruptionDetector = detector
}

// UpdateMetrics updates health metrics
func (hm *HealthMonitor) UpdateMetrics(metrics HealthMetrics) {
	health := hm.currentHealth.Load()
	if health == nil {
		return
	}

	// Create new health with updated metrics
	newHealth := *health
	oldHealth := newHealth // Store copy for comparison
	newHealth.Metrics = metrics
	newHealth.LastUpdate = time.Now()

	// Recalculate health
	hm.calculateHealth(&newHealth)

	// Store updated health
	hm.currentHealth.Store(&newHealth)

	// Add to history
	hm.addToHistory(newHealth)

	// Notify if health changed
	if oldHealth.Status != newHealth.Status && hm.onHealthChange != nil {
		hm.onHealthChange(oldHealth, newHealth)
	}

	// Check for alerts
	hm.checkAlerts(&newHealth)
}

// GetCurrentHealth returns current health status
func (hm *HealthMonitor) GetCurrentHealth() StreamHealth {
	health := hm.currentHealth.Load()
	if health == nil {
		return StreamHealth{
			StreamID: hm.streamID,
			Status:   HealthStatusHealthy,
			Score:    1.0,
		}
	}
	return *health
}

// GetHealthHistory returns health history
func (hm *HealthMonitor) GetHealthHistory(duration time.Duration) []StreamHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var history []StreamHealth

	for _, h := range hm.healthHistory {
		if h.LastUpdate.After(cutoff) {
			history = append(history, h)
		}
	}

	return history
}

// GetActiveAlerts returns active alerts
func (hm *HealthMonitor) GetActiveAlerts() []Alert {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	alerts := make([]Alert, 0, len(hm.activeAlerts))
	for _, alert := range hm.activeAlerts {
		if !alert.Resolved {
			alerts = append(alerts, *alert)
		}
	}

	return alerts
}

// SetCallbacks sets monitoring callbacks
func (hm *HealthMonitor) SetCallbacks(
	onHealthChange func(old, new StreamHealth),
	onAlert func(alert Alert)) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.onHealthChange = onHealthChange
	hm.onAlert = onAlert
}

// SetAlertThreshold sets a custom alert threshold
func (hm *HealthMonitor) SetAlertThreshold(metric string, threshold float64) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.alertThresholds[metric] = threshold
}

// Stop stops the health monitor
func (hm *HealthMonitor) Stop() {
	hm.cancel()
}

// Private methods

func (hm *HealthMonitor) monitorLoop() {
	ticker := time.NewTicker(hm.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.ctx.Done():
			return
		case <-ticker.C:
			hm.performHealthCheck()
		}
	}
}

func (hm *HealthMonitor) performHealthCheck() {
	health := hm.currentHealth.Load()
	if health == nil {
		return
	}

	// Create new health for update
	newHealth := *health
	newHealth.LastUpdate = time.Now()
	newHealth.Issues = []HealthIssue{}

	// Lock for component access
	hm.mu.RLock()
	validator := hm.validator
	corruptionDetector := hm.corruptionDetector
	hm.mu.RUnlock()

	// Gather metrics from components
	if validator != nil {
		result := validator.GetValidationResult()
		newHealth.Score = result.HealthScore

		// Update metrics from validation
		if result.DroppedFrames > 0 && result.FrameCount > 0 {
			newHealth.Metrics.FrameDropRate = float64(result.DroppedFrames) / float64(result.FrameCount)
		}
	}

	if corruptionDetector != nil {
		stats := corruptionDetector.GetStatistics()
		if events, ok := stats["total_events"].(uint64); ok {
			newHealth.Metrics.CorruptionEvents = int(events)
		}

		// Check recent corruption
		recentEvents := corruptionDetector.GetRecentEvents()
		for _, event := range recentEvents {
			if time.Since(event.Timestamp) < hm.updateInterval {
				newHealth.Issues = append(newHealth.Issues, HealthIssue{
					Type:        "corruption",
					Severity:    IssueSeverity(event.Severity),
					Description: event.Description,
					Timestamp:   event.Timestamp,
					Impact:      "Potential video quality degradation",
				})
			}
		}
	}

	// Calculate overall health
	hm.calculateHealth(&newHealth)

	// Store updated health
	oldHealth := hm.currentHealth.Load()
	hm.currentHealth.Store(&newHealth)

	// Add to history
	hm.addToHistory(newHealth)

	// Update metrics
	hm.healthGauge.Set(newHealth.Score)
	hm.statusGauge.Set(float64(newHealth.Status))

	// Notify if health changed
	if oldHealth != nil && oldHealth.Status != newHealth.Status && hm.onHealthChange != nil {
		hm.onHealthChange(*oldHealth, newHealth)
	}

	// Check for alerts
	hm.checkAlerts(&newHealth)
}

func (hm *HealthMonitor) calculateHealth(health *StreamHealth) {
	// Analyze metrics and issues
	score := 1.0
	recommendations := []string{}

	// Check frame drop rate
	if health.Metrics.FrameDropRate > 0.1 {
		score *= 0.5
		health.Issues = append(health.Issues, HealthIssue{
			Type:        "frame_drops",
			Severity:    IssueSeverityError,
			Description: fmt.Sprintf("High frame drop rate: %.1f%%", health.Metrics.FrameDropRate*100),
			Timestamp:   time.Now(),
			Impact:      "Severe video quality degradation",
		})
		recommendations = append(recommendations, "Reduce stream bitrate or upgrade hardware")
	} else if health.Metrics.FrameDropRate > 0.05 {
		score *= 0.8
		health.Issues = append(health.Issues, HealthIssue{
			Type:        "frame_drops",
			Severity:    IssueSeverityWarning,
			Description: fmt.Sprintf("Moderate frame drop rate: %.1f%%", health.Metrics.FrameDropRate*100),
			Timestamp:   time.Now(),
			Impact:      "Noticeable video quality issues",
		})
	}

	// Check CPU usage
	if health.Metrics.CPUUsage > 0.9 {
		score *= 0.6
		health.Issues = append(health.Issues, HealthIssue{
			Type:        "cpu_usage",
			Severity:    IssueSeverityError,
			Description: fmt.Sprintf("Critical CPU usage: %.1f%%", health.Metrics.CPUUsage*100),
			Timestamp:   time.Now(),
			Impact:      "System overload risk",
		})
		recommendations = append(recommendations, "Reduce concurrent streams or add CPU resources")
	}

	// Check memory usage
	if health.Metrics.MemoryUsage > 0.9 {
		score *= 0.7
		health.Issues = append(health.Issues, HealthIssue{
			Type:        "memory_usage",
			Severity:    IssueSeverityError,
			Description: fmt.Sprintf("Critical memory usage: %.1f%%", health.Metrics.MemoryUsage*100),
			Timestamp:   time.Now(),
			Impact:      "Out of memory risk",
		})
		recommendations = append(recommendations, "Reduce buffer sizes or add memory")
	}

	// Check processing latency
	if health.Metrics.ProcessingLatency > 200*time.Millisecond {
		score *= 0.8
		health.Issues = append(health.Issues, HealthIssue{
			Type:        "latency",
			Severity:    IssueSeverityWarning,
			Description: fmt.Sprintf("High processing latency: %v", health.Metrics.ProcessingLatency),
			Timestamp:   time.Now(),
			Impact:      "Increased stream delay",
		})
	}

	// Check corruption events
	if health.Metrics.CorruptionEvents > 10 {
		score *= 0.7
		health.Issues = append(health.Issues, HealthIssue{
			Type:        "corruption",
			Severity:    IssueSeverityError,
			Description: fmt.Sprintf("Multiple corruption events: %d", health.Metrics.CorruptionEvents),
			Timestamp:   time.Now(),
			Impact:      "Stream integrity compromised",
		})
		recommendations = append(recommendations, "Check network stability and source encoding")
	}

	// Update health status
	health.Score = score
	health.Recommendations = recommendations

	// Determine status based on score
	switch {
	case score >= 0.9:
		health.Status = HealthStatusHealthy
	case score >= hm.degradedThreshold:
		health.Status = HealthStatusDegraded
	case score >= hm.criticalThreshold:
		health.Status = HealthStatusUnhealthy
	default:
		health.Status = HealthStatusCritical
	}

	// Count issues
	for _, issue := range health.Issues {
		hm.issueCounter.Inc()
		if issue.Severity >= IssueSeverityError {
			hm.logger.WithFields(logger.Fields{
				"type":        issue.Type,
				"severity":    issue.Severity.String(),
				"description": issue.Description,
			}).Warn("Health issue detected")
		}
	}
}

func (hm *HealthMonitor) checkAlerts(health *StreamHealth) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Check each metric against thresholds
	alerts := []Alert{}

	// Frame drop rate
	if threshold, ok := hm.alertThresholds["frame_drop_rate"]; ok {
		if health.Metrics.FrameDropRate > threshold {
			alerts = append(alerts, Alert{
				ID:       fmt.Sprintf("frame_drops_%s", hm.streamID),
				StreamID: hm.streamID,
				Type:     "frame_drops",
				Severity: AlertSeverityError,
				Message: fmt.Sprintf("Frame drop rate %.1f%% exceeds threshold %.1f%%",
					health.Metrics.FrameDropRate*100, threshold*100),
				Details: map[string]interface{}{
					"current":   health.Metrics.FrameDropRate,
					"threshold": threshold,
				},
				Timestamp: time.Now(),
			})
		}
	}

	// CPU usage
	if threshold, ok := hm.alertThresholds["cpu_usage"]; ok {
		if health.Metrics.CPUUsage > threshold {
			alerts = append(alerts, Alert{
				ID:       fmt.Sprintf("cpu_usage_%s", hm.streamID),
				StreamID: hm.streamID,
				Type:     "cpu_usage",
				Severity: AlertSeverityCritical,
				Message: fmt.Sprintf("CPU usage %.1f%% exceeds threshold %.1f%%",
					health.Metrics.CPUUsage*100, threshold*100),
				Details: map[string]interface{}{
					"current":   health.Metrics.CPUUsage,
					"threshold": threshold,
				},
				Timestamp: time.Now(),
			})
		}
	}

	// Process alerts
	for _, alert := range alerts {
		existing, exists := hm.activeAlerts[alert.ID]
		if !exists || (existing.Resolved && time.Since(existing.ResolvedAt) > hm.alertCooldown) {
			// New alert or cooldown expired
			alert := alert // Copy for closure
			hm.activeAlerts[alert.ID] = &alert
			hm.alertCounter.Inc()

			if hm.onAlert != nil {
				hm.onAlert(alert)
			}
		}
	}

	// Resolve alerts that are no longer active
	for id, alert := range hm.activeAlerts {
		if !alert.Resolved {
			stillActive := false
			for _, newAlert := range alerts {
				if newAlert.ID == id {
					stillActive = true
					break
				}
			}

			if !stillActive {
				alert.Resolved = true
				alert.ResolvedAt = time.Now()
			}
		}
	}
}

func (hm *HealthMonitor) addToHistory(health StreamHealth) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.healthHistory = append(hm.healthHistory, health)

	// Trim to size
	if len(hm.healthHistory) > hm.historySize {
		hm.healthHistory = hm.healthHistory[len(hm.healthHistory)-hm.historySize:]
	}
}

// String methods

func (s HealthStatus) String() string {
	switch s {
	case HealthStatusHealthy:
		return "healthy"
	case HealthStatusDegraded:
		return "degraded"
	case HealthStatusUnhealthy:
		return "unhealthy"
	case HealthStatusCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func (s IssueSeverity) String() string {
	switch s {
	case IssueSeverityInfo:
		return "info"
	case IssueSeverityWarning:
		return "warning"
	case IssueSeverityError:
		return "error"
	case IssueSeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

func (s AlertSeverity) String() string {
	switch s {
	case AlertSeverityInfo:
		return "info"
	case AlertSeverityWarning:
		return "warning"
	case AlertSeverityError:
		return "error"
	case AlertSeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}
