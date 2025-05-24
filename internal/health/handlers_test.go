package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHandler(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)
	handler := NewHandler(manager)

	assert.NotNil(t, handler)
	assert.Equal(t, manager, handler.manager)
	assert.NotZero(t, handler.startTime)
}

func TestHandleHealth(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)
	
	// Register a test checker
	manager.Register(&mockChecker{name: "test", err: nil})
	
	handler := NewHandler(manager)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	handler.HandleHealth(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache, no-store, must-revalidate", rr.Header().Get("Cache-Control"))

	var response Response
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, StatusOK, response.Status)
	assert.NotZero(t, response.Timestamp)
	assert.NotEmpty(t, response.Version)
	assert.NotEmpty(t, response.Uptime)
	assert.Contains(t, response.Checks, "test")
}

func TestHandleHealthWithFailingChecker(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)
	
	// Register failing checker
	manager.Register(&mockChecker{name: "failing", err: assert.AnError})
	
	handler := NewHandler(manager)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	handler.HandleHealth(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)

	var response Response
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, StatusDown, response.Status)
}

func TestHandleReady(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)
	
	// Register a healthy checker
	manager.Register(&mockChecker{name: "test", err: nil})
	manager.RunChecks(context.Background())
	
	handler := NewHandler(manager)

	req := httptest.NewRequest("GET", "/ready", nil)
	rr := httptest.NewRecorder()

	handler.HandleReady(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response struct {
		Status    Status    `json:"status"`
		Timestamp time.Time `json:"timestamp"`
	}
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, StatusOK, response.Status)
	assert.NotZero(t, response.Timestamp)
}

func TestHandleLive(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)
	handler := NewHandler(manager)

	req := httptest.NewRequest("GET", "/live", nil)
	rr := httptest.NewRecorder()

	handler.HandleLive(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response struct {
		Status    string    `json:"status"`
		Timestamp time.Time `json:"timestamp"`
	}
	err := json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "alive", response.Status)
	assert.NotZero(t, response.Timestamp)
}

func TestGetUptime(t *testing.T) {
	logger := logrus.New()
	manager := NewManager(logger)
	
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "seconds only",
			duration: 45 * time.Second,
			expected: "45 seconds",
		},
		{
			name:     "minutes and seconds",
			duration: 2*time.Minute + 30*time.Second,
			expected: "2 minutes 30 seconds",
		},
		{
			name:     "hours minutes seconds",
			duration: 3*time.Hour + 15*time.Minute + 45*time.Second,
			expected: "3 hours 15 minutes 45 seconds",
		},
		{
			name:     "days hours minutes seconds",
			duration: 2*24*time.Hour + 6*time.Hour + 30*time.Minute + 15*time.Second,
			expected: "2 days 6 hours 30 minutes 15 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &Handler{
				manager:   manager,
				startTime: time.Now().Add(-tt.duration),
			}
			
			uptime := handler.getUptime()
			assert.Equal(t, tt.expected, uptime)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		days     int
		hours    int
		minutes  int
		seconds  int
		expected string
	}{
		{
			name:     "all units",
			days:     1,
			hours:    2,
			minutes:  3,
			seconds:  4,
			expected: "1 day 2 hours 3 minutes 4 seconds",
		},
		{
			name:     "plural days",
			days:     3,
			hours:    0,
			minutes:  0,
			seconds:  0,
			expected: "3 days",
		},
		{
			name:     "singular units",
			days:     1,
			hours:    1,
			minutes:  1,
			seconds:  1,
			expected: "1 day 1 hour 1 minute 1 second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.days, tt.hours, tt.minutes, tt.seconds)
			assert.Equal(t, tt.expected, result)
		})
	}
}