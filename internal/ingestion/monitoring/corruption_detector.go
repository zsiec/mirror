package monitoring

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// CorruptionType represents different types of corruption
type CorruptionType int

const (
	CorruptionTypeNone CorruptionType = iota
	CorruptionTypeChecksum
	CorruptionTypeTimestamp
	CorruptionTypeSequence
	CorruptionTypeFrameStructure
	CorruptionTypeParameterSet
	CorruptionTypeGOPStructure
	CorruptionTypeSyncDrift
)

// CorruptionEvent represents a detected corruption
type CorruptionEvent struct {
	StreamID    string
	Type        CorruptionType
	Severity    CorruptionSeverity
	Timestamp   time.Time
	FrameNumber uint64
	Description string
	Data        map[string]interface{}
}

// CorruptionSeverity levels
type CorruptionSeverity int

const (
	SeverityLow CorruptionSeverity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// CorruptionPattern represents a pattern of corruption
type CorruptionPattern struct {
	Type      CorruptionType
	Frequency float64 // Events per second
	LastSeen  time.Time
	Count     uint64
}

// CorruptionDetector detects various types of stream corruption
type CorruptionDetector struct {
	mu       sync.RWMutex
	streamID string
	logger   logger.Logger
	stopCh   chan struct{}

	// Detection state
	patterns        map[CorruptionType]*CorruptionPattern
	recentEvents    []CorruptionEvent
	eventBufferSize int

	// Detection thresholds
	checksumThreshold    float64
	timestampThreshold   time.Duration
	sequenceGapThreshold int
	syncDriftThreshold   time.Duration

	// Statistics
	totalEvents    atomic.Uint64
	criticalEvents atomic.Uint64
	lastChecksum   uint32
	lastSequence   uint16
	seqInitialized bool // Whether lastSequence has been set (seq 0 is valid per RFC 3550)
	lastTimestamp  uint32
	tsInitialized  bool // Whether lastTimestamp has been set

	// Callbacks
	onCorruption func(event CorruptionEvent)
	onPattern    func(pattern CorruptionPattern)

	// Metrics
	corruptionCounter *metrics.Counter
	corruptionGauge   *metrics.Gauge
	patternCounter    *metrics.Counter
	detectionLatency  *metrics.Histogram
}

// NewCorruptionDetector creates a new corruption detector
func NewCorruptionDetector(streamID string, logger logger.Logger) *CorruptionDetector {
	cd := &CorruptionDetector{
		streamID:             streamID,
		logger:               logger,
		stopCh:               make(chan struct{}),
		patterns:             make(map[CorruptionType]*CorruptionPattern),
		recentEvents:         make([]CorruptionEvent, 0, 100),
		eventBufferSize:      100,
		checksumThreshold:    0.001, // 0.1% error rate
		timestampThreshold:   100 * time.Millisecond,
		sequenceGapThreshold: 5,
		syncDriftThreshold:   200 * time.Millisecond,
	}

	// Initialize metrics
	cd.corruptionCounter = metrics.NewCounter("ingestion_corruption_events",
		map[string]string{"stream_id": streamID})
	cd.corruptionGauge = metrics.NewGauge("ingestion_corruption_rate",
		map[string]string{"stream_id": streamID})
	cd.patternCounter = metrics.NewCounter("ingestion_corruption_patterns",
		map[string]string{"stream_id": streamID})
	cd.detectionLatency = metrics.NewHistogram("ingestion_corruption_detection_latency_seconds",
		map[string]string{"stream_id": streamID}, []float64{0.0001, 0.0005, 0.001, 0.005, 0.01})

	// Start pattern detection
	go cd.patternDetectionLoop()

	return cd
}

// CheckFrame checks a frame for corruption
func (cd *CorruptionDetector) CheckFrame(frame *types.VideoFrame) error {
	if frame == nil {
		return fmt.Errorf("nil frame")
	}

	start := time.Now()
	defer func() {
		cd.detectionLatency.Observe(time.Since(start).Seconds())
	}()

	// Check various corruption types
	cd.checkFrameStructure(frame)
	cd.checkTimestamp(frame)
	// Additional checks would be implemented based on frame metadata

	return nil
}

// CheckPacket checks a packet for corruption
func (cd *CorruptionDetector) CheckPacket(packet *Packet) error {
	if packet == nil {
		return fmt.Errorf("nil packet")
	}

	start := time.Now()
	defer func() {
		cd.detectionLatency.Observe(time.Since(start).Seconds())
	}()

	// Check sequence numbers
	cd.checkSequence(packet.SequenceNumber)

	// Check timestamp continuity
	if packet.Timestamp > 0 {
		cd.checkPacketTimestamp(packet.Timestamp)
	}

	return nil
}

// CheckGOP checks a GOP for structural corruption
func (cd *CorruptionDetector) CheckGOP(gop *types.GOP) error {
	if gop == nil {
		return fmt.Errorf("nil GOP")
	}

	// Check GOP structure
	if len(gop.Frames) == 0 {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeGOPStructure,
			Severity:    SeverityHigh,
			Timestamp:   time.Now(),
			Description: "Empty GOP",
		})
		return fmt.Errorf("empty GOP")
	}

	// Check first frame is IDR
	if !gop.Frames[0].IsKeyframe() {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeGOPStructure,
			Severity:    SeverityMedium,
			Timestamp:   time.Now(),
			Description: "GOP does not start with IDR frame",
		})
	}

	// Check frame ordering
	var lastPTS int64 = -1
	for i, frame := range gop.Frames {
		if frame.PTS <= lastPTS {
			cd.recordEvent(CorruptionEvent{
				StreamID:    cd.streamID,
				Type:        CorruptionTypeGOPStructure,
				Severity:    SeverityHigh,
				Timestamp:   time.Now(),
				FrameNumber: frame.FrameNumber,
				Description: fmt.Sprintf("Non-monotonic PTS at frame %d", i),
				Data: map[string]interface{}{
					"current_pts": frame.PTS,
					"last_pts":    lastPTS,
				},
			})
		}
		lastPTS = frame.PTS
	}

	return nil
}

// CheckSyncDrift checks for A/V sync drift
func (cd *CorruptionDetector) CheckSyncDrift(audioPTS, videoPTS int64) {
	drift := time.Duration(float64(audioPTS-videoPTS) / 90.0 * float64(time.Millisecond))
	if drift < 0 {
		drift = -drift
	}

	if drift > cd.syncDriftThreshold {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeSyncDrift,
			Severity:    cd.getSyncDriftSeverity(drift),
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("A/V sync drift: %v", drift),
			Data: map[string]interface{}{
				"audio_pts": audioPTS,
				"video_pts": videoPTS,
				"drift_ms":  drift.Milliseconds(),
			},
		})
	}
}

// SetCallbacks sets event callbacks
func (cd *CorruptionDetector) SetCallbacks(
	onCorruption func(event CorruptionEvent),
	onPattern func(pattern CorruptionPattern)) {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.onCorruption = onCorruption
	cd.onPattern = onPattern
}

// GetRecentEvents returns recent corruption events
func (cd *CorruptionDetector) GetRecentEvents() []CorruptionEvent {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	events := make([]CorruptionEvent, len(cd.recentEvents))
	copy(events, cd.recentEvents)
	return events
}

// GetPatterns returns detected corruption patterns
func (cd *CorruptionDetector) GetPatterns() map[CorruptionType]*CorruptionPattern {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	patterns := make(map[CorruptionType]*CorruptionPattern)
	for k, v := range cd.patterns {
		pattern := *v
		patterns[k] = &pattern
	}
	return patterns
}

// GetStatistics returns corruption statistics
func (cd *CorruptionDetector) GetStatistics() map[string]interface{} {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return map[string]interface{}{
		"total_events":     cd.totalEvents.Load(),
		"critical_events":  cd.criticalEvents.Load(),
		"pattern_count":    len(cd.patterns),
		"recent_events":    len(cd.recentEvents),
		"corruption_types": cd.getCorruptionTypeCounts(),
	}
}

// Private methods

func (cd *CorruptionDetector) checkFrameStructure(frame *types.VideoFrame) {
	// Check for invalid frame data
	if frame.TotalSize == 0 {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeFrameStructure,
			Severity:    SeverityHigh,
			Timestamp:   time.Now(),
			FrameNumber: frame.FrameNumber,
			Description: "Empty frame data",
		})
		return
	}

	// Check frame size anomalies
	const maxFrameSize = 50 * 1024 * 1024 // 50MB
	if frame.TotalSize > maxFrameSize {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeFrameStructure,
			Severity:    SeverityMedium,
			Timestamp:   time.Now(),
			FrameNumber: frame.FrameNumber,
			Description: fmt.Sprintf("Abnormally large frame: %d bytes", frame.TotalSize),
			Data: map[string]interface{}{
				"size": frame.TotalSize,
			},
		})
	}
}

func (cd *CorruptionDetector) checkTimestamp(frame *types.VideoFrame) {
	// Check for invalid timestamps
	if frame.PTS == 0 && frame.DTS == 0 {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeTimestamp,
			Severity:    SeverityMedium,
			Timestamp:   time.Now(),
			FrameNumber: frame.FrameNumber,
			Description: "Zero timestamps",
		})
		return
	}

	// Check DTS > PTS
	if frame.DTS > frame.PTS {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeTimestamp,
			Severity:    SeverityHigh,
			Timestamp:   time.Now(),
			FrameNumber: frame.FrameNumber,
			Description: "DTS greater than PTS",
			Data: map[string]interface{}{
				"pts": frame.PTS,
				"dts": frame.DTS,
			},
		})
	}
}

func (cd *CorruptionDetector) checkSequence(sequence uint16) {
	cd.mu.Lock()
	lastSeq := cd.lastSequence
	initialized := cd.seqInitialized
	cd.lastSequence = sequence
	cd.seqInitialized = true
	cd.mu.Unlock()

	if !initialized {
		return // First packet
	}

	// Check for sequence gaps
	expected := (lastSeq + 1) & 0xFFFF
	if sequence != expected {
		gap := int(sequence) - int(expected)
		if gap < 0 {
			gap += 65536
		}

		if gap > cd.sequenceGapThreshold {
			// Calculate actual gap (missing packets)
			actualGap := gap - 1 // Don't count the current packet
			cd.recordEvent(CorruptionEvent{
				StreamID:    cd.streamID,
				Type:        CorruptionTypeSequence,
				Severity:    cd.getSequenceGapSeverity(actualGap),
				Timestamp:   time.Now(),
				Description: fmt.Sprintf("Sequence gap detected: %d packets", actualGap),
				Data: map[string]interface{}{
					"expected": expected,
					"received": sequence,
					"gap":      actualGap,
				},
			})
		}
	}
}

func (cd *CorruptionDetector) checkPacketTimestamp(timestamp uint32) {
	cd.mu.Lock()
	lastTS := cd.lastTimestamp
	initialized := cd.tsInitialized
	cd.lastTimestamp = timestamp
	cd.tsInitialized = true
	cd.mu.Unlock()

	if !initialized {
		return // First packet
	}

	// Check for timestamp jumps
	diff := int64(timestamp) - int64(lastTS)
	if diff < 0 {
		// Possible wraparound
		diff += (1 << 32)
	}

	// Convert to duration (assuming 90kHz clock)
	duration := time.Duration(diff*1000/90) * time.Microsecond

	if duration > cd.timestampThreshold {
		cd.recordEvent(CorruptionEvent{
			StreamID:    cd.streamID,
			Type:        CorruptionTypeTimestamp,
			Severity:    SeverityMedium,
			Timestamp:   time.Now(),
			Description: fmt.Sprintf("Large timestamp jump: %v", duration),
			Data: map[string]interface{}{
				"last_timestamp":    lastTS,
				"current_timestamp": timestamp,
				"jump_ms":           duration.Milliseconds(),
			},
		})
	}
}

func (cd *CorruptionDetector) recordEvent(event CorruptionEvent) {
	cd.totalEvents.Add(1)
	cd.corruptionCounter.Inc()

	if event.Severity == SeverityCritical {
		cd.criticalEvents.Add(1)
	}

	// Add to buffer with proper index management
	cd.mu.Lock()
	cd.recentEvents = append(cd.recentEvents, event)
	if len(cd.recentEvents) > cd.eventBufferSize {
		// Keep only the most recent events
		cd.recentEvents = cd.recentEvents[len(cd.recentEvents)-cd.eventBufferSize:]
	}

	// Update patterns
	pattern, exists := cd.patterns[event.Type]
	if !exists {
		pattern = &CorruptionPattern{
			Type: event.Type,
		}
		cd.patterns[event.Type] = pattern
	}
	pattern.Count++
	pattern.LastSeen = event.Timestamp
	cd.mu.Unlock()

	// Notify callback
	if cd.onCorruption != nil {
		cd.onCorruption(event)
	}

	cd.logger.WithFields(logger.Fields{
		"type":        event.Type.String(),
		"severity":    event.Severity.String(),
		"description": event.Description,
	}).Warn("Corruption detected")
}

// Stop stops the corruption detector's background goroutine
func (cd *CorruptionDetector) Stop() {
	select {
	case <-cd.stopCh:
		// Already stopped
	default:
		close(cd.stopCh)
	}
}

func (cd *CorruptionDetector) patternDetectionLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cd.analyzePatterns()
		case <-cd.stopCh:
			return
		}
	}
}

func (cd *CorruptionDetector) analyzePatterns() {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	now := time.Now()
	windowDuration := 60 * time.Second

	for _, pattern := range cd.patterns {
		// Calculate frequency
		if pattern.LastSeen.After(now.Add(-windowDuration)) {
			pattern.Frequency = float64(pattern.Count) / windowDuration.Seconds()

			// Notify if frequency is high
			if pattern.Frequency > 1.0 && cd.onPattern != nil {
				cd.patternCounter.Inc()
				cd.onPattern(*pattern)
			}
		}
	}

	// Update corruption rate metric
	totalRate := float64(cd.totalEvents.Load()) / time.Since(now.Add(-windowDuration)).Seconds()
	cd.corruptionGauge.Set(totalRate)
}

func (cd *CorruptionDetector) getSequenceGapSeverity(gap int) CorruptionSeverity {
	switch {
	case gap > 100:
		return SeverityCritical
	case gap > 50:
		return SeverityHigh
	case gap > 10:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func (cd *CorruptionDetector) getSyncDriftSeverity(drift time.Duration) CorruptionSeverity {
	switch {
	case drift > 1*time.Second:
		return SeverityCritical
	case drift > 500*time.Millisecond:
		return SeverityHigh
	case drift > 200*time.Millisecond:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func (cd *CorruptionDetector) getCorruptionTypeCounts() map[string]uint64 {
	counts := make(map[string]uint64)
	for typ, pattern := range cd.patterns {
		counts[typ.String()] = pattern.Count
	}
	return counts
}

// String methods for types

func (t CorruptionType) String() string {
	switch t {
	case CorruptionTypeNone:
		return "none"
	case CorruptionTypeChecksum:
		return "checksum"
	case CorruptionTypeTimestamp:
		return "timestamp"
	case CorruptionTypeSequence:
		return "sequence"
	case CorruptionTypeFrameStructure:
		return "frame_structure"
	case CorruptionTypeParameterSet:
		return "parameter_set"
	case CorruptionTypeGOPStructure:
		return "gop_structure"
	case CorruptionTypeSyncDrift:
		return "sync_drift"
	default:
		return "unknown"
	}
}

func (s CorruptionSeverity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}
