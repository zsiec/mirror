package health

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/zsiec/mirror/pkg/version"
)

// Response represents the health check response.
type Response struct {
	Status    Status            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Version   string            `json:"version"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]*Check `json:"checks,omitempty"`
}

// Handler handles health check HTTP endpoints.
type Handler struct {
	manager   *Manager
	startTime time.Time
}

// NewHandler creates a new health check handler.
func NewHandler(manager *Manager) *Handler {
	return &Handler{
		manager:   manager,
		startTime: time.Now(),
	}
}

// HandleHealth handles the /health endpoint.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	// Run health checks with request context
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	checks := h.manager.RunChecks(ctx)
	overallStatus := h.manager.GetOverallStatus()

	response := Response{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Version:   version.Version,
		Uptime:    h.getUptime(),
		Checks:    checks,
	}

	// Set appropriate status code
	statusCode := http.StatusOK
	if overallStatus == StatusDegraded {
		statusCode = http.StatusOK // Still return 200 for degraded
	} else if overallStatus == StatusDown {
		statusCode = http.StatusServiceUnavailable
	}

	h.writeJSON(w, statusCode, response)
}

// HandleReady handles the /ready endpoint (simplified health check).
func (h *Handler) HandleReady(w http.ResponseWriter, r *http.Request) {
	overallStatus := h.manager.GetOverallStatus()

	response := struct {
		Status    Status    `json:"status"`
		Timestamp time.Time `json:"timestamp"`
	}{
		Status:    overallStatus,
		Timestamp: time.Now(),
	}

	statusCode := http.StatusOK
	if overallStatus == StatusDown {
		statusCode = http.StatusServiceUnavailable
	}

	h.writeJSON(w, statusCode, response)
}

// HandleLive handles the /live endpoint (basic liveness check).
func (h *Handler) HandleLive(w http.ResponseWriter, r *http.Request) {
	response := struct {
		Status    string    `json:"status"`
		Timestamp time.Time `json:"timestamp"`
	}{
		Status:    "alive",
		Timestamp: time.Now(),
	}

	h.writeJSON(w, http.StatusOK, response)
}

// getUptime calculates the service uptime.
func (h *Handler) getUptime() string {
	uptime := time.Since(h.startTime)
	days := int(uptime.Hours() / 24)
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	seconds := int(uptime.Seconds()) % 60

	switch {
	case days > 0:
		return formatDuration(days, hours, minutes, seconds)
	case hours > 0:
		return formatDuration(0, hours, minutes, seconds)
	case minutes > 0:
		return formatDuration(0, 0, minutes, seconds)
	default:
		return formatDuration(0, 0, 0, seconds)
	}
}

// formatDuration formats duration in a human-readable way.
func formatDuration(days, hours, minutes, seconds int) string {
	result := ""
	if days > 0 {
		result += formatUnit(days, "day")
	}
	if hours > 0 {
		if result != "" {
			result += " "
		}
		result += formatUnit(hours, "hour")
	}
	if minutes > 0 {
		if result != "" {
			result += " "
		}
		result += formatUnit(minutes, "minute")
	}
	if seconds > 0 || result == "" {
		if result != "" {
			result += " "
		}
		result += formatUnit(seconds, "second")
	}
	return result
}

// formatUnit formats a unit with proper pluralization
func formatUnit(value int, unit string) string {
	if value == 1 {
		return "1 " + unit
	}
	return formatInt(value) + " " + unit + "s"
}

// formatInt formats an integer
func formatInt(n int) string {
	return strconv.Itoa(n)
}

// writeJSON writes a JSON response
func (h *Handler) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.manager.logger.WithError(err).Error("Failed to encode health response")
	}
}
