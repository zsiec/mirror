package rtp

import (
	"fmt"
	"strings"
	"time"
)

// SessionDiagnostics provides detailed diagnostics for an RTP session
type SessionDiagnostics struct {
	StreamID      string
	RemoteAddr    string
	SSRC          uint32
	State         string
	Duration      time.Duration
	LastPacketAge time.Duration

	// Codec information
	CodecType   string
	CodecState  string
	PayloadType uint8
	ClockRate   uint32

	// Basic statistics
	PacketsReceived uint64
	BytesReceived   uint64
	BitrateKbps     float64
	PacketRate      float64

	// Loss and recovery
	PacketsLost        uint64
	RecoveredPackets   uint64
	UnrecoverableLoss  uint64
	AverageLossRate    float64
	CurrentLossRate    float64
	MaxConsecutiveLoss uint16
	LossEvents         uint64

	// Sequence tracking
	InitialSequence  uint16
	LastSequence     uint16
	SequenceGaps     uint64
	MaxSequenceGap   uint16
	ReorderedPackets uint64
	SequenceResets   uint64

	// Jitter statistics
	CurrentJitter float64
	MaxJitter     float64
	JitterSamples uint64

	// SSRC changes
	SSRCChanges uint64

	// Jitter buffer statistics
	JitterBufferDepth     int
	JitterBufferMaxDepth  int
	JitterBufferDelivered uint64
	JitterBufferDropped   uint64
	JitterBufferLate      uint64
	JitterBufferUnderruns uint64
	JitterBufferOverruns  uint64

	// Rate limiting
	RateLimitDrops uint64

	// Recent lost packets (for debugging)
	RecentLostPackets []uint16

	// Health assessment
	HealthScore   float64
	HealthStatus  string
	HealthReasons []string
}

// GetDiagnostics returns comprehensive diagnostics for the session
func (s *Session) GetDiagnostics() *SessionDiagnostics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()

	diag := &SessionDiagnostics{
		StreamID:      s.streamID,
		RemoteAddr:    s.remoteAddr.String(),
		SSRC:          s.ssrc,
		Duration:      now.Sub(s.firstPacketTime),
		LastPacketAge: now.Sub(s.lastPacket),
		PayloadType:   s.stats.LastPayloadType,
		ClockRate:     s.GetClockRate(),
	}

	// Codec information
	diag.CodecType = s.codecType.String()
	switch s.getCodecState() {
	case CodecStateUnknown:
		diag.CodecState = "unknown"
	case CodecStateDetecting:
		diag.CodecState = "detecting"
	case CodecStateDetected:
		diag.CodecState = "detected"
	case CodecStateError:
		diag.CodecState = "error"
	case CodecStateTimeout:
		diag.CodecState = "timeout"
	}

	// Basic statistics
	diag.PacketsReceived = s.stats.PacketsReceived
	diag.BytesReceived = s.stats.BytesReceived
	if diag.Duration.Seconds() > 0 {
		diag.BitrateKbps = float64(s.stats.BytesReceived*8) / diag.Duration.Seconds() / 1000
		diag.PacketRate = float64(s.stats.PacketsReceived) / diag.Duration.Seconds()
	}

	// Loss and recovery
	if s.packetLossTracker != nil {
		recoveryStats := s.packetLossTracker.GetStats()
		diag.PacketsLost = recoveryStats.TotalLost
		diag.RecoveredPackets = recoveryStats.TotalRecovered
		diag.UnrecoverableLoss = recoveryStats.UnrecoverableLoss
		diag.AverageLossRate = recoveryStats.AverageLossRate
		diag.CurrentLossRate = recoveryStats.CurrentLossRate
		diag.MaxConsecutiveLoss = recoveryStats.MaxConsecutiveLoss
		diag.LossEvents = recoveryStats.LossEvents
		diag.RecentLostPackets = s.packetLossTracker.GetLostPackets(10)
	}

	// Sequence tracking
	diag.InitialSequence = s.stats.InitialSequence
	diag.LastSequence = s.stats.LastSequence
	diag.SequenceGaps = s.stats.SequenceGaps
	diag.MaxSequenceGap = s.stats.MaxSequenceGap
	diag.ReorderedPackets = s.stats.ReorderedPackets
	diag.SequenceResets = s.stats.SequenceResets

	// Jitter statistics
	diag.CurrentJitter = s.stats.Jitter
	diag.MaxJitter = s.stats.MaxJitter
	diag.JitterSamples = s.stats.JitterSamples

	// SSRC changes
	diag.SSRCChanges = s.stats.SSRCChanges

	// Jitter buffer statistics
	if s.jitterBuffer != nil {
		jbStats := s.jitterBuffer.GetStats()
		diag.JitterBufferDepth = jbStats.CurrentDepth
		diag.JitterBufferMaxDepth = jbStats.MaxDepth
		diag.JitterBufferDelivered = jbStats.PacketsDelivered
		diag.JitterBufferDropped = jbStats.PacketsDropped
		diag.JitterBufferLate = jbStats.PacketsLate
		diag.JitterBufferUnderruns = jbStats.UnderrunCount
		diag.JitterBufferOverruns = jbStats.OverrunCount
	}

	// Rate limiting
	diag.RateLimitDrops = s.stats.RateLimitDrops

	// State
	if s.IsActive() {
		diag.State = "active"
	} else {
		diag.State = "inactive"
	}

	// Calculate health score and status
	diag.calculateHealth()

	return diag
}

// calculateHealth computes a health score and status based on various metrics
func (diag *SessionDiagnostics) calculateHealth() {
	score := 100.0
	reasons := []string{}

	// Check if session is active
	if diag.State != "active" {
		score -= 50
		reasons = append(reasons, "session inactive")
	}

	// Check codec detection
	if diag.CodecState != "detected" {
		score -= 20
		reasons = append(reasons, fmt.Sprintf("codec %s", diag.CodecState))
	}

	// Check packet loss rate
	if diag.CurrentLossRate > 0.1 {
		score -= 30
		reasons = append(reasons, fmt.Sprintf("high loss rate: %.1f%%", diag.CurrentLossRate*100))
	} else if diag.CurrentLossRate > 0.05 {
		score -= 15
		reasons = append(reasons, fmt.Sprintf("moderate loss rate: %.1f%%", diag.CurrentLossRate*100))
	} else if diag.CurrentLossRate > 0.01 {
		score -= 5
		reasons = append(reasons, fmt.Sprintf("low loss rate: %.1f%%", diag.CurrentLossRate*100))
	}

	// Check for sequence resets
	if diag.SequenceResets > 0 {
		score -= 10
		reasons = append(reasons, fmt.Sprintf("%d sequence resets", diag.SequenceResets))
	}

	// Check for SSRC changes
	if diag.SSRCChanges > 0 {
		score -= 5
		reasons = append(reasons, fmt.Sprintf("%d SSRC changes", diag.SSRCChanges))
	}

	// Check jitter
	if diag.MaxJitter > 100 { // ms
		score -= 10
		reasons = append(reasons, fmt.Sprintf("high jitter: %.0fms", diag.MaxJitter))
	}

	// Check jitter buffer health
	if diag.JitterBufferUnderruns > 0 {
		score -= 5
		reasons = append(reasons, fmt.Sprintf("%d buffer underruns", diag.JitterBufferUnderruns))
	}
	if diag.JitterBufferOverruns > 0 {
		score -= 5
		reasons = append(reasons, fmt.Sprintf("%d buffer overruns", diag.JitterBufferOverruns))
	}

	// Check rate limiting
	if diag.RateLimitDrops > 0 {
		score -= 10
		reasons = append(reasons, fmt.Sprintf("%d rate limit drops", diag.RateLimitDrops))
	}

	// Ensure score doesn't go below 0
	if score < 0 {
		score = 0
	}

	diag.HealthScore = score
	diag.HealthReasons = reasons

	// Determine health status
	switch {
	case score >= 90:
		diag.HealthStatus = "excellent"
	case score >= 75:
		diag.HealthStatus = "good"
	case score >= 50:
		diag.HealthStatus = "fair"
	case score >= 25:
		diag.HealthStatus = "poor"
	default:
		diag.HealthStatus = "critical"
	}
}

// String returns a formatted string representation of the diagnostics
func (diag *SessionDiagnostics) String() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("RTP Session Diagnostics for %s\n", diag.StreamID))
	b.WriteString(strings.Repeat("=", 50) + "\n")

	// Connection info
	b.WriteString(fmt.Sprintf("Remote: %s, SSRC: 0x%08X\n", diag.RemoteAddr, diag.SSRC))
	b.WriteString(fmt.Sprintf("State: %s, Duration: %v, Last packet: %v ago\n",
		diag.State, diag.Duration.Round(time.Second), diag.LastPacketAge.Round(time.Second)))

	// Codec info
	b.WriteString(fmt.Sprintf("Codec: %s (%s), PT: %d, Clock: %dHz\n",
		diag.CodecType, diag.CodecState, diag.PayloadType, diag.ClockRate))

	// Traffic stats
	b.WriteString(fmt.Sprintf("\nTraffic Statistics:\n"))
	b.WriteString(fmt.Sprintf("  Packets: %d (%.1f pps)\n", diag.PacketsReceived, diag.PacketRate))
	b.WriteString(fmt.Sprintf("  Bytes: %d (%.1f kbps)\n", diag.BytesReceived, diag.BitrateKbps))

	// Loss stats
	b.WriteString(fmt.Sprintf("\nPacket Loss:\n"))
	b.WriteString(fmt.Sprintf("  Lost: %d, Recovered: %d, Unrecoverable: %d\n",
		diag.PacketsLost, diag.RecoveredPackets, diag.UnrecoverableLoss))
	b.WriteString(fmt.Sprintf("  Average loss: %.2f%%, Current loss: %.2f%%\n",
		diag.AverageLossRate*100, diag.CurrentLossRate*100))
	b.WriteString(fmt.Sprintf("  Max consecutive: %d, Loss events: %d\n",
		diag.MaxConsecutiveLoss, diag.LossEvents))

	// Sequence stats
	b.WriteString(fmt.Sprintf("\nSequence Tracking:\n"))
	b.WriteString(fmt.Sprintf("  Range: %d -> %d\n", diag.InitialSequence, diag.LastSequence))
	b.WriteString(fmt.Sprintf("  Gaps: %d (max: %d), Reordered: %d, Resets: %d\n",
		diag.SequenceGaps, diag.MaxSequenceGap, diag.ReorderedPackets, diag.SequenceResets))

	// Jitter stats
	b.WriteString(fmt.Sprintf("\nJitter Statistics:\n"))
	b.WriteString(fmt.Sprintf("  Current: %.2fms, Max: %.2fms, Samples: %d\n",
		diag.CurrentJitter/1000, diag.MaxJitter/1000, diag.JitterSamples))

	// Jitter buffer stats
	b.WriteString(fmt.Sprintf("\nJitter Buffer:\n"))
	b.WriteString(fmt.Sprintf("  Depth: %d/%d, Delivered: %d, Dropped: %d\n",
		diag.JitterBufferDepth, diag.JitterBufferMaxDepth,
		diag.JitterBufferDelivered, diag.JitterBufferDropped))
	b.WriteString(fmt.Sprintf("  Late: %d, Underruns: %d, Overruns: %d\n",
		diag.JitterBufferLate, diag.JitterBufferUnderruns, diag.JitterBufferOverruns))

	// Other issues
	if diag.SSRCChanges > 0 || diag.RateLimitDrops > 0 {
		b.WriteString(fmt.Sprintf("\nOther Issues:\n"))
		if diag.SSRCChanges > 0 {
			b.WriteString(fmt.Sprintf("  SSRC changes: %d\n", diag.SSRCChanges))
		}
		if diag.RateLimitDrops > 0 {
			b.WriteString(fmt.Sprintf("  Rate limit drops: %d\n", diag.RateLimitDrops))
		}
	}

	// Recent lost packets
	if len(diag.RecentLostPackets) > 0 {
		b.WriteString(fmt.Sprintf("\nRecent Lost Packets: %v\n", diag.RecentLostPackets))
	}

	// Health assessment
	b.WriteString(fmt.Sprintf("\nHealth Assessment:\n"))
	b.WriteString(fmt.Sprintf("  Score: %.0f/100 (%s)\n", diag.HealthScore, diag.HealthStatus))
	if len(diag.HealthReasons) > 0 {
		b.WriteString(fmt.Sprintf("  Issues: %s\n", strings.Join(diag.HealthReasons, ", ")))
	}

	return b.String()
}
