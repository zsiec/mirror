package rtp

import (
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/config"
	"github.com/zsiec/mirror/internal/ingestion/codec"
	"github.com/zsiec/mirror/internal/ingestion/registry"
	"github.com/zsiec/mirror/internal/logger"
)

func TestSessionDiagnostics_Basic(t *testing.T) {
	// Create mock registry
	reg := registry.NewMockRegistry()

	// Create test session
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5000")
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	session, err := NewSession("test-stream", addr, 0x12345678, reg, codecsCfg, log)
	require.NoError(t, err)

	// Simulate some activity
	session.codecType = codec.TypeH264
	session.codecState = CodecStateDetected
	session.stats.PacketsReceived = 1000
	session.stats.BytesReceived = 1500000
	session.stats.LastPayloadType = 96
	session.firstPacketTime = time.Now().Add(-10 * time.Second)
	session.lastPacket = time.Now().Add(-100 * time.Millisecond)

	// Get diagnostics
	diag := session.GetDiagnostics()

	// Verify basic info
	assert.Equal(t, "test-stream", diag.StreamID)
	assert.Equal(t, "127.0.0.1:5000", diag.RemoteAddr)
	assert.Equal(t, uint32(0x12345678), diag.SSRC)
	assert.Equal(t, "H264", diag.CodecType)
	assert.Equal(t, "detected", diag.CodecState)
	assert.Equal(t, uint8(96), diag.PayloadType)
	assert.Equal(t, uint32(90000), diag.ClockRate)

	// Verify statistics
	assert.Equal(t, uint64(1000), diag.PacketsReceived)
	assert.Equal(t, uint64(1500000), diag.BytesReceived)
	assert.Greater(t, diag.BitrateKbps, 0.0)
	assert.Greater(t, diag.PacketRate, 0.0)

	// Verify health
	assert.Equal(t, "excellent", diag.HealthStatus)
	assert.Greater(t, diag.HealthScore, 80.0)
}

func TestSessionDiagnostics_WithPacketLoss(t *testing.T) {
	// Create mock registry
	reg := registry.NewMockRegistry()

	// Create test session
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5000")
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	session, err := NewSession("test-stream", addr, 0x12345678, reg, codecsCfg, log)
	require.NoError(t, err)

	// Set up basic state
	session.codecType = codec.TypeH264
	session.codecState = CodecStateDetected
	session.firstPacketTime = time.Now().Add(-10 * time.Second)
	session.lastPacket = time.Now()

	// Simulate packet loss
	session.packetLossTracker = NewPacketLossTracker()
	session.packetLossTracker.ProcessSequence(100)
	session.packetLossTracker.ProcessSequence(110) // Lost 101-109

	// Get diagnostics
	diag := session.GetDiagnostics()

	// Verify loss statistics
	assert.Equal(t, uint64(9), diag.PacketsLost)
	assert.Greater(t, diag.AverageLossRate, 0.0)
	assert.Equal(t, uint64(1), diag.LossEvents)
	assert.Equal(t, uint16(9), diag.MaxConsecutiveLoss)

	// Verify health is degraded due to loss
	assert.NotEqual(t, "excellent", diag.HealthStatus)
	assert.Less(t, diag.HealthScore, 100.0)
	assert.Contains(t, diag.HealthReasons, "high loss rate: 900.0%")
}

func TestSessionDiagnostics_UnhealthySession(t *testing.T) {
	// Create mock registry
	reg := registry.NewMockRegistry()

	// Create test session
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5000")
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	session, err := NewSession("test-stream", addr, 0x12345678, reg, codecsCfg, log)
	require.NoError(t, err)

	// Set up unhealthy state
	session.codecState = CodecStateError
	session.firstPacketTime = time.Now().Add(-60 * time.Second)
	session.lastPacket = time.Now().Add(-35 * time.Second) // Inactive
	session.stats.SequenceResets = 5
	session.stats.SSRCChanges = 3
	session.stats.RateLimitDrops = 100
	session.stats.MaxJitter = 200000 // 200ms in nanoseconds

	// Get diagnostics
	diag := session.GetDiagnostics()

	// Verify unhealthy status
	assert.Equal(t, "inactive", diag.State)
	assert.Equal(t, "error", diag.CodecState)
	assert.Equal(t, uint64(5), diag.SequenceResets)
	assert.Equal(t, uint64(3), diag.SSRCChanges)
	assert.Equal(t, uint64(100), diag.RateLimitDrops)

	// Verify health assessment
	assert.Equal(t, "critical", diag.HealthStatus)
	assert.Less(t, diag.HealthScore, 25.0)

	// Check health reasons
	assert.Contains(t, diag.HealthReasons, "session inactive")
	assert.Contains(t, diag.HealthReasons, "codec error")
	assert.Contains(t, diag.HealthReasons, "5 sequence resets")
	assert.Contains(t, diag.HealthReasons, "3 SSRC changes")
	assert.Contains(t, diag.HealthReasons, "100 rate limit drops")
}

func TestSessionDiagnostics_String(t *testing.T) {
	// Create mock registry
	reg := registry.NewMockRegistry()

	// Create test session
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5000")
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	session, err := NewSession("test-stream", addr, 0x12345678, reg, codecsCfg, log)
	require.NoError(t, err)

	// Set up some data
	session.codecType = codec.TypeH264
	session.codecState = CodecStateDetected
	session.stats.PacketsReceived = 1000
	session.stats.BytesReceived = 1500000
	session.firstPacketTime = time.Now().Add(-10 * time.Second)
	session.lastPacket = time.Now()

	// Get diagnostics string
	diag := session.GetDiagnostics()
	diagStr := diag.String()

	// Verify string contains key information
	assert.Contains(t, diagStr, "RTP Session Diagnostics for test-stream")
	assert.Contains(t, diagStr, "Remote: 127.0.0.1:5000")
	assert.Contains(t, diagStr, "SSRC: 0x12345678")
	assert.Contains(t, diagStr, "Codec: H264 (detected)")
	assert.Contains(t, diagStr, "Traffic Statistics:")
	assert.Contains(t, diagStr, "Packet Loss:")
	assert.Contains(t, diagStr, "Health Assessment:")
	assert.Contains(t, diagStr, "Score:")
	assert.Contains(t, diagStr, "excellent")
}

func TestSessionDiagnostics_JitterBufferStats(t *testing.T) {
	// Create mock registry
	reg := registry.NewMockRegistry()

	// Create test session
	addr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:5000")
	codecsCfg := &config.CodecsConfig{Preferred: "h264"}
	log := logger.NewLogrusAdapter(logrus.NewEntry(logrus.New()))

	session, err := NewSession("test-stream", addr, 0x12345678, reg, codecsCfg, log)
	require.NoError(t, err)

	// Simulate jitter buffer activity
	session.jitterBuffer = &JitterBuffer{
		stats: JitterBufferStats{
			CurrentDepth:     5,
			MaxDepth:         10,
			PacketsDelivered: 950,
			PacketsDropped:   30,
			PacketsLate:      20,
			UnderrunCount:    2,
			OverrunCount:     1,
		},
	}

	// Get diagnostics
	diag := session.GetDiagnostics()

	// Verify jitter buffer stats
	assert.Equal(t, 5, diag.JitterBufferDepth)
	assert.Equal(t, 10, diag.JitterBufferMaxDepth)
	assert.Equal(t, uint64(950), diag.JitterBufferDelivered)
	assert.Equal(t, uint64(30), diag.JitterBufferDropped)
	assert.Equal(t, uint64(20), diag.JitterBufferLate)
	assert.Equal(t, uint64(2), diag.JitterBufferUnderruns)
	assert.Equal(t, uint64(1), diag.JitterBufferOverruns)

	// Verify health impact
	assert.Less(t, diag.HealthScore, 100.0) // Should be penalized for underruns/overruns
	assert.Contains(t, diag.HealthReasons, "2 buffer underruns")
	assert.Contains(t, diag.HealthReasons, "1 buffer overruns")
}

func TestSessionDiagnostics_HealthScoring(t *testing.T) {
	tests := []struct {
		name           string
		setupFunc      func(*SessionDiagnostics)
		expectedStatus string
		minScore       float64
		maxScore       float64
	}{
		{
			name: "Perfect health",
			setupFunc: func(d *SessionDiagnostics) {
				d.State = "active"
				d.CodecState = "detected"
				d.CurrentLossRate = 0.0
			},
			expectedStatus: "excellent",
			minScore:       90,
			maxScore:       100,
		},
		{
			name: "Minor packet loss",
			setupFunc: func(d *SessionDiagnostics) {
				d.State = "active"
				d.CodecState = "detected"
				d.CurrentLossRate = 0.02 // 2% loss
			},
			expectedStatus: "excellent",
			minScore:       85,
			maxScore:       95,
		},
		{
			name: "Moderate issues",
			setupFunc: func(d *SessionDiagnostics) {
				d.State = "active"
				d.CodecState = "detected"
				d.CurrentLossRate = 0.07 // 7% loss
				d.SequenceResets = 1
			},
			expectedStatus: "good",
			minScore:       60,
			maxScore:       80,
		},
		{
			name: "Severe issues",
			setupFunc: func(d *SessionDiagnostics) {
				d.State = "active"
				d.CodecState = "timeout"
				d.CurrentLossRate = 0.15 // 15% loss
				d.SequenceResets = 3
				d.SSRCChanges = 2
			},
			expectedStatus: "poor",
			minScore:       20,
			maxScore:       40,
		},
		{
			name: "Critical state",
			setupFunc: func(d *SessionDiagnostics) {
				d.State = "inactive"
				d.CodecState = "error"
				d.CurrentLossRate = 0.5 // 50% loss
			},
			expectedStatus: "critical",
			minScore:       0,
			maxScore:       20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diag := &SessionDiagnostics{}
			tt.setupFunc(diag)
			diag.calculateHealth()

			assert.Equal(t, tt.expectedStatus, diag.HealthStatus)
			assert.GreaterOrEqual(t, diag.HealthScore, tt.minScore)
			assert.LessOrEqual(t, diag.HealthScore, tt.maxScore)
		})
	}
}
