package monitoring

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// AlertManager manages alerts across all streams
type AlertManager struct {
	mu     sync.RWMutex
	logger logger.Logger

	// Alert storage
	activeAlerts map[string]*Alert
	alertHistory []Alert
	historySize  int

	// Configuration
	deduplicationWindow time.Duration
	escalationLevels    []EscalationLevel

	// Alert channels
	channels map[string]AlertChannel

	// Metrics
	alertsTotal   *metrics.Counter
	alertsActive  *metrics.Gauge
	alertDuration *metrics.Histogram

	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

// EscalationLevel defines when and how to escalate alerts
type EscalationLevel struct {
	Duration time.Duration
	Severity AlertSeverity
	Channels []string
}

// AlertChannel interface for alert delivery
type AlertChannel interface {
	Send(alert Alert) error
	Name() string
}

// LogChannel sends alerts to logs
type LogChannel struct {
	logger logger.Logger
}

func NewLogChannel(logger logger.Logger) *LogChannel {
	return &LogChannel{logger: logger}
}

func (c *LogChannel) Send(alert Alert) error {
	c.logger.WithFields(logger.Fields{
		"stream_id": alert.StreamID,
		"type":      alert.Type,
		"severity":  alert.Severity.String(),
		"message":   alert.Message,
	}).Error("Alert triggered")
	return nil
}

func (c *LogChannel) Name() string {
	return "log"
}

// WebhookChannel sends alerts to webhooks
type WebhookChannel struct {
	url     string
	timeout time.Duration
}

func NewWebhookChannel(url string) *WebhookChannel {
	return &WebhookChannel{
		url:     url,
		timeout: 10 * time.Second,
	}
}

func (c *WebhookChannel) Send(alert Alert) error {
	// TODO: Implement webhook sending
	return fmt.Errorf("webhook not implemented")
}

func (c *WebhookChannel) Name() string {
	return "webhook"
}

// NewAlertManager creates a new alert manager
func NewAlertManager(logger logger.Logger) *AlertManager {
	ctx, cancel := context.WithCancel(context.Background())

	am := &AlertManager{
		logger:              logger,
		activeAlerts:        make(map[string]*Alert),
		alertHistory:        make([]Alert, 0, 1000),
		historySize:         1000,
		deduplicationWindow: 5 * time.Minute,
		channels:            make(map[string]AlertChannel),
		ctx:                 ctx,
		cancel:              cancel,
	}

	// Default escalation levels
	am.escalationLevels = []EscalationLevel{
		{Duration: 0, Severity: AlertSeverityInfo, Channels: []string{"log"}},
		{Duration: 5 * time.Minute, Severity: AlertSeverityWarning, Channels: []string{"log"}},
		{Duration: 15 * time.Minute, Severity: AlertSeverityError, Channels: []string{"log", "webhook"}},
		{Duration: 30 * time.Minute, Severity: AlertSeverityCritical, Channels: []string{"log", "webhook", "pager"}},
	}

	// Initialize metrics
	am.alertsTotal = metrics.NewCounter("alerts_total", nil)
	am.alertsActive = metrics.NewGauge("alerts_active", nil)
	am.alertDuration = metrics.NewHistogram("alert_duration_seconds", nil,
		[]float64{60, 300, 900, 1800, 3600, 7200})

	// Add default channels
	am.AddChannel("log", NewLogChannel(logger))

	// Start maintenance
	go am.maintenanceLoop()

	return am
}

// AddChannel adds an alert channel
func (am *AlertManager) AddChannel(name string, channel AlertChannel) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.channels[name] = channel
}

// TriggerAlert triggers a new alert
func (am *AlertManager) TriggerAlert(alert Alert) {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Check for deduplication
	if am.isDuplicate(&alert) {
		return
	}

	// Add to active alerts
	alert.ID = am.generateAlertID(&alert)
	alert.Timestamp = time.Now()

	am.activeAlerts[alert.ID] = &alert
	am.alertsTotal.Inc()
	am.alertsActive.Set(float64(len(am.activeAlerts)))

	// Send through appropriate channels
	am.sendAlert(&alert)

	// Add to history
	am.addToHistory(alert)

	am.logger.WithFields(logger.Fields{
		"alert_id":  alert.ID,
		"stream_id": alert.StreamID,
		"type":      alert.Type,
		"severity":  alert.Severity.String(),
	}).Info("Alert triggered")
}

// ResolveAlert resolves an active alert
func (am *AlertManager) ResolveAlert(alertID string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	alert, exists := am.activeAlerts[alertID]
	if !exists {
		return
	}

	alert.Resolved = true
	alert.ResolvedAt = time.Now()

	// Update metrics
	duration := alert.ResolvedAt.Sub(alert.Timestamp)
	am.alertDuration.Observe(duration.Seconds())

	// Remove from active
	delete(am.activeAlerts, alertID)
	am.alertsActive.Set(float64(len(am.activeAlerts)))

	// Add resolved alert to history
	am.addToHistory(*alert)

	am.logger.WithFields(logger.Fields{
		"alert_id": alertID,
		"duration": duration,
	}).Info("Alert resolved")
}

// GetActiveAlerts returns all active alerts
func (am *AlertManager) GetActiveAlerts() []Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	alerts := make([]Alert, 0, len(am.activeAlerts))
	for _, alert := range am.activeAlerts {
		alerts = append(alerts, *alert)
	}

	return alerts
}

// GetAlertHistory returns alert history
func (am *AlertManager) GetAlertHistory(duration time.Duration) []Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var history []Alert

	for _, alert := range am.alertHistory {
		if alert.Timestamp.After(cutoff) {
			history = append(history, alert)
		}
	}

	return history
}

// Stop stops the alert manager
func (am *AlertManager) Stop() {
	am.cancel()
}

// Private methods

func (am *AlertManager) maintenanceLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-am.ctx.Done():
			return
		case <-ticker.C:
			am.performMaintenance()
		}
	}
}

func (am *AlertManager) performMaintenance() {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()

	// Check for escalation
	for _, alert := range am.activeAlerts {
		if !alert.Resolved {
			age := now.Sub(alert.Timestamp)
			for _, level := range am.escalationLevels {
				if age >= level.Duration && alert.Severity < level.Severity {
					// Escalate
					alert.Severity = level.Severity
					am.sendAlert(alert)

					am.logger.WithFields(logger.Fields{
						"alert_id": alert.ID,
						"severity": level.Severity.String(),
						"age":      age,
					}).Warn("Alert escalated")
				}
			}
		}
	}

	// Clean up old resolved alerts
	for id, alert := range am.activeAlerts {
		if alert.Resolved && now.Sub(alert.ResolvedAt) > 1*time.Hour {
			delete(am.activeAlerts, id)
		}
	}

	am.alertsActive.Set(float64(len(am.activeAlerts)))
}

func (am *AlertManager) isDuplicate(alert *Alert) bool {
	key := am.generateDeduplicationKey(alert)

	for _, active := range am.activeAlerts {
		if !active.Resolved && am.generateDeduplicationKey(active) == key {
			if time.Since(active.Timestamp) < am.deduplicationWindow {
				return true
			}
		}
	}

	return false
}

func (am *AlertManager) generateAlertID(alert *Alert) string {
	return fmt.Sprintf("%s_%s_%d", alert.StreamID, alert.Type, time.Now().UnixNano())
}

func (am *AlertManager) generateDeduplicationKey(alert *Alert) string {
	return fmt.Sprintf("%s:%s:%s", alert.StreamID, alert.Type, alert.Message)
}

func (am *AlertManager) sendAlert(alert *Alert) {
	// Determine channels based on severity
	channelNames := []string{"log"} // Always log

	for _, level := range am.escalationLevels {
		if alert.Severity >= level.Severity {
			channelNames = level.Channels
		}
	}

	// Send through each channel
	for _, channelName := range channelNames {
		if channel, exists := am.channels[channelName]; exists {
			if err := channel.Send(*alert); err != nil {
				am.logger.WithError(err).WithField("channel", channelName).
					Error("Failed to send alert")
			}
		}
	}
}

func (am *AlertManager) addToHistory(alert Alert) {
	am.alertHistory = append(am.alertHistory, alert)

	// Trim to size
	if len(am.alertHistory) > am.historySize {
		am.alertHistory = am.alertHistory[len(am.alertHistory)-am.historySize:]
	}
}

// AlertAggregator aggregates similar alerts
type AlertAggregator struct {
	mu        sync.RWMutex
	groups    map[string]*AlertGroup
	window    time.Duration
	threshold int
}

// AlertGroup represents a group of similar alerts
type AlertGroup struct {
	Key       string
	Alerts    []Alert
	FirstSeen time.Time
	LastSeen  time.Time
	Count     int
}

// NewAlertAggregator creates a new alert aggregator
func NewAlertAggregator(window time.Duration, threshold int) *AlertAggregator {
	return &AlertAggregator{
		groups:    make(map[string]*AlertGroup),
		window:    window,
		threshold: threshold,
	}
}

// Add adds an alert to aggregation
func (aa *AlertAggregator) Add(alert Alert) *AlertGroup {
	aa.mu.Lock()
	defer aa.mu.Unlock()

	key := fmt.Sprintf("%s:%s", alert.StreamID, alert.Type)

	group, exists := aa.groups[key]
	if !exists {
		group = &AlertGroup{
			Key:       key,
			FirstSeen: alert.Timestamp,
		}
		aa.groups[key] = group
	}

	group.Alerts = append(group.Alerts, alert)
	group.LastSeen = alert.Timestamp
	group.Count++

	// Check if threshold reached
	if group.Count >= aa.threshold {
		return group
	}

	return nil
}

// GetGroups returns all alert groups
func (aa *AlertAggregator) GetGroups() []*AlertGroup {
	aa.mu.RLock()
	defer aa.mu.RUnlock()

	groups := make([]*AlertGroup, 0, len(aa.groups))
	for _, group := range aa.groups {
		groups = append(groups, group)
	}

	return groups
}

// Cleanup removes old groups
func (aa *AlertAggregator) Cleanup() {
	aa.mu.Lock()
	defer aa.mu.Unlock()

	now := time.Now()
	for key, group := range aa.groups {
		if now.Sub(group.LastSeen) > aa.window {
			delete(aa.groups, key)
		}
	}
}
