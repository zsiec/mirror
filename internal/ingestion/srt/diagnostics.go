package srt

import (
	"sync"
	"time"
)

// Diagnostics provides detailed diagnostics for SRT connections
type Diagnostics struct {
	mu sync.RWMutex

	// Connection info
	streamID  string
	startTime time.Time

	// Message statistics
	messagesReceived int64
	bytesReceived    int64
	lastMessageTime  time.Time
	lastMessageSize  int
	avgMessageSize   float64

	// MPEG-TS alignment statistics
	alignedPackets  int64
	partialPackets  int64
	alignmentErrors int64
	syncBytesFound  int64
	syncBytesLost   int64

	// PES assembly statistics
	pesPacketsStarted   int64
	pesPacketsCompleted int64
	pesPacketsTimedOut  int64
	pesAssemblyErrors   int64

	// Continuity statistics
	continuityErrors int64
	duplicatePackets int64

	// Codec detection
	detectedVideoCodec string
	detectedAudioCodec string
	videoPID           uint16
	audioPID           uint16

	// Performance metrics
	processingLatencyMs  float64
	maxProcessingLatency int64
	minProcessingLatency int64
}

// NewDiagnostics creates a new diagnostics instance
func NewDiagnostics(streamID string) *Diagnostics {
	return &Diagnostics{
		streamID:             streamID,
		startTime:            time.Now(),
		minProcessingLatency: int64(^uint64(0) >> 1), // Max int64
	}
}

// RecordMessage records statistics about a received SRT message
func (d *Diagnostics) RecordMessage(size int, processingTime time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.messagesReceived++
	d.bytesReceived += int64(size)
	d.lastMessageTime = time.Now()
	d.lastMessageSize = size

	// Update average message size
	d.avgMessageSize = float64(d.bytesReceived) / float64(d.messagesReceived)

	// Update processing latency
	latencyMs := processingTime.Milliseconds()
	d.processingLatencyMs = (d.processingLatencyMs*float64(d.messagesReceived-1) + float64(latencyMs)) / float64(d.messagesReceived)

	if latencyMs > d.maxProcessingLatency {
		d.maxProcessingLatency = latencyMs
	}
	if latencyMs < d.minProcessingLatency {
		d.minProcessingLatency = latencyMs
	}
}

// RecordAlignmentStats updates MPEG-TS alignment statistics
func (d *Diagnostics) RecordAlignmentStats(aligned, partial, errors int64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.alignedPackets += aligned
	d.partialPackets += partial
	d.alignmentErrors += errors
}

// RecordSyncByte records sync byte statistics
func (d *Diagnostics) RecordSyncByte(found bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if found {
		d.syncBytesFound++
	} else {
		d.syncBytesLost++
	}
}

// RecordPESEvent records PES assembly events
func (d *Diagnostics) RecordPESEvent(event string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch event {
	case "started":
		d.pesPacketsStarted++
	case "completed":
		d.pesPacketsCompleted++
	case "timeout":
		d.pesPacketsTimedOut++
	case "error":
		d.pesAssemblyErrors++
	}
}

// RecordContinuityError records a continuity counter error
func (d *Diagnostics) RecordContinuityError() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.continuityErrors++
}

// RecordDuplicatePacket records a duplicate packet
func (d *Diagnostics) RecordDuplicatePacket() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.duplicatePackets++
}

// SetCodecInfo sets detected codec information
func (d *Diagnostics) SetCodecInfo(videoCodec, audioCodec string, videoPID, audioPID uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.detectedVideoCodec = videoCodec
	d.detectedAudioCodec = audioCodec
	d.videoPID = videoPID
	d.audioPID = audioPID
}

// GetSnapshot returns a snapshot of current diagnostics
func (d *Diagnostics) GetSnapshot() DiagnosticsSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	uptime := time.Since(d.startTime)
	timeSinceLastMessage := time.Duration(0)
	if !d.lastMessageTime.IsZero() {
		timeSinceLastMessage = time.Since(d.lastMessageTime)
	}

	// Calculate rates
	messageRate := float64(0)
	dataRate := float64(0)
	if uptime.Seconds() > 0 {
		messageRate = float64(d.messagesReceived) / uptime.Seconds()
		dataRate = float64(d.bytesReceived) / uptime.Seconds()
	}

	// Calculate error rates
	alignmentErrorRate := float64(0)
	continuityErrorRate := float64(0)
	totalPackets := d.alignedPackets + d.partialPackets
	if totalPackets > 0 {
		alignmentErrorRate = float64(d.alignmentErrors) / float64(totalPackets) * 100
		continuityErrorRate = float64(d.continuityErrors) / float64(totalPackets) * 100
	}

	return DiagnosticsSnapshot{
		StreamID:             d.streamID,
		Uptime:               uptime,
		MessagesReceived:     d.messagesReceived,
		BytesReceived:        d.bytesReceived,
		MessageRate:          messageRate,
		DataRate:             dataRate,
		LastMessageTime:      d.lastMessageTime,
		TimeSinceLastMessage: timeSinceLastMessage,
		AvgMessageSize:       d.avgMessageSize,
		AlignedPackets:       d.alignedPackets,
		PartialPackets:       d.partialPackets,
		AlignmentErrors:      d.alignmentErrors,
		AlignmentErrorRate:   alignmentErrorRate,
		SyncBytesFound:       d.syncBytesFound,
		SyncBytesLost:        d.syncBytesLost,
		PESPacketsStarted:    d.pesPacketsStarted,
		PESPacketsCompleted:  d.pesPacketsCompleted,
		PESPacketsTimedOut:   d.pesPacketsTimedOut,
		PESAssemblyErrors:    d.pesAssemblyErrors,
		ContinuityErrors:     d.continuityErrors,
		ContinuityErrorRate:  continuityErrorRate,
		DuplicatePackets:     d.duplicatePackets,
		DetectedVideoCodec:   d.detectedVideoCodec,
		DetectedAudioCodec:   d.detectedAudioCodec,
		VideoPID:             d.videoPID,
		AudioPID:             d.audioPID,
		AvgProcessingLatency: d.processingLatencyMs,
		MaxProcessingLatency: d.maxProcessingLatency,
		MinProcessingLatency: d.minProcessingLatency,
	}
}

// DiagnosticsSnapshot represents a point-in-time snapshot of diagnostics
type DiagnosticsSnapshot struct {
	// Basic info
	StreamID string
	Uptime   time.Duration

	// Message statistics
	MessagesReceived     int64
	BytesReceived        int64
	MessageRate          float64 // messages/sec
	DataRate             float64 // bytes/sec
	LastMessageTime      time.Time
	TimeSinceLastMessage time.Duration
	AvgMessageSize       float64

	// Alignment statistics
	AlignedPackets     int64
	PartialPackets     int64
	AlignmentErrors    int64
	AlignmentErrorRate float64 // percentage
	SyncBytesFound     int64
	SyncBytesLost      int64

	// PES statistics
	PESPacketsStarted   int64
	PESPacketsCompleted int64
	PESPacketsTimedOut  int64
	PESAssemblyErrors   int64

	// Continuity statistics
	ContinuityErrors    int64
	ContinuityErrorRate float64 // percentage
	DuplicatePackets    int64

	// Codec info
	DetectedVideoCodec string
	DetectedAudioCodec string
	VideoPID           uint16
	AudioPID           uint16

	// Performance
	AvgProcessingLatency float64 // milliseconds
	MaxProcessingLatency int64   // milliseconds
	MinProcessingLatency int64   // milliseconds
}
