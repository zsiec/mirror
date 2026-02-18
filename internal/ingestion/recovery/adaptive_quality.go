package recovery

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/mirror/internal/logger"
	"github.com/zsiec/mirror/internal/metrics"
)

// QualityLevel represents quality settings
type QualityLevel int

const (
	QualityLevelUltra QualityLevel = iota
	QualityLevelHigh
	QualityLevelMedium
	QualityLevelLow
	QualityLevelMinimum
)

// QualityProfile defines quality parameters
type QualityProfile struct {
	Name               string
	Level              QualityLevel
	Bitrate            int64
	Framerate          float64
	Resolution         Resolution
	GOPSize            int
	BufferSize         int
	MaxPacketLoss      float64
	ProcessingPriority int
}

// Resolution represents video resolution
type Resolution struct {
	Width  int
	Height int
}

// AdaptiveQuality manages quality adaptation based on system conditions
type AdaptiveQuality struct {
	mu       sync.RWMutex
	streamID string
	logger   logger.Logger

	// Configuration
	profiles       []QualityProfile
	currentProfile atomic.Int32
	targetProfile  int
	transitionRate float64

	// System metrics
	cpuUsage         atomic.Uint64 // Stored as percentage * 100
	memoryUsage      atomic.Uint64 // Stored as percentage * 100
	networkBandwidth atomic.Int64
	packetLoss       atomic.Uint64 // Stored as percentage * 1000

	// Quality metrics
	currentBitrate    atomic.Int64
	frameDropRate     atomic.Uint64 // Stored as percentage * 1000
	processingLatency atomic.Int64

	// Adaptation state
	lastAdaptation  time.Time
	adaptationCount uint64
	qualityScore    float64

	// ML readiness
	mlDataCollector *MLDataCollector

	// Callbacks
	onQualityChange func(old, new QualityProfile)

	// Metrics
	qualityGauge      *metrics.Gauge
	adaptationCounter *metrics.Counter
	bitrateGauge      *metrics.Gauge

	// Control
	ctx    context.Context
	cancel context.CancelFunc
}

// MLDataCollector collects data for future ML models
type MLDataCollector struct {
	mu         sync.Mutex
	dataPoints []MLDataPoint
	maxPoints  int
}

// MLDataPoint represents a data point for ML training
type MLDataPoint struct {
	Timestamp         time.Time
	CPUUsage          float64
	MemoryUsage       float64
	NetworkBandwidth  int64
	PacketLoss        float64
	FrameDropRate     float64
	ProcessingLatency time.Duration
	QualityLevel      QualityLevel
	QualityScore      float64
}

// NewAdaptiveQuality creates a new adaptive quality manager
func NewAdaptiveQuality(streamID string, logger logger.Logger) *AdaptiveQuality {
	ctx, cancel := context.WithCancel(context.Background())

	aq := &AdaptiveQuality{
		streamID:       streamID,
		logger:         logger,
		transitionRate: 0.1, // 10% per step
		lastAdaptation: time.Now(),
		mlDataCollector: &MLDataCollector{
			dataPoints: make([]MLDataPoint, 0, 1000),
			maxPoints:  1000,
		},
		ctx:    ctx,
		cancel: cancel,
	}

	// Initialize default quality profiles
	aq.profiles = []QualityProfile{
		{
			Name:               "Ultra",
			Level:              QualityLevelUltra,
			Bitrate:            50_000_000, // 50 Mbps
			Framerate:          60,
			Resolution:         Resolution{3840, 2160}, // 4K
			GOPSize:            120,
			BufferSize:         10 * 1024 * 1024,
			MaxPacketLoss:      0.001,
			ProcessingPriority: 10,
		},
		{
			Name:               "High",
			Level:              QualityLevelHigh,
			Bitrate:            25_000_000, // 25 Mbps
			Framerate:          30,
			Resolution:         Resolution{1920, 1080}, // 1080p
			GOPSize:            60,
			BufferSize:         5 * 1024 * 1024,
			MaxPacketLoss:      0.005,
			ProcessingPriority: 8,
		},
		{
			Name:               "Medium",
			Level:              QualityLevelMedium,
			Bitrate:            10_000_000, // 10 Mbps
			Framerate:          30,
			Resolution:         Resolution{1280, 720}, // 720p
			GOPSize:            30,
			BufferSize:         2 * 1024 * 1024,
			MaxPacketLoss:      0.01,
			ProcessingPriority: 6,
		},
		{
			Name:               "Low",
			Level:              QualityLevelLow,
			Bitrate:            5_000_000, // 5 Mbps
			Framerate:          24,
			Resolution:         Resolution{854, 480}, // 480p
			GOPSize:            24,
			BufferSize:         1 * 1024 * 1024,
			MaxPacketLoss:      0.02,
			ProcessingPriority: 4,
		},
		{
			Name:               "Minimum",
			Level:              QualityLevelMinimum,
			Bitrate:            1_000_000, // 1 Mbps
			Framerate:          15,
			Resolution:         Resolution{640, 360}, // 360p
			GOPSize:            15,
			BufferSize:         512 * 1024,
			MaxPacketLoss:      0.05,
			ProcessingPriority: 2,
		},
	}

	// Start with high quality
	aq.currentProfile.Store(int32(QualityLevelHigh))
	aq.targetProfile = int(QualityLevelHigh)

	// Initialize metrics
	aq.qualityGauge = metrics.NewGauge("ingestion_quality_level",
		map[string]string{"stream_id": streamID})
	aq.adaptationCounter = metrics.NewCounter("ingestion_quality_adaptations",
		map[string]string{"stream_id": streamID})
	aq.bitrateGauge = metrics.NewGauge("ingestion_adaptive_bitrate",
		map[string]string{"stream_id": streamID})

	// Start adaptation loop
	go aq.adaptationLoop()

	return aq
}

// UpdateSystemMetrics updates system resource metrics
func (aq *AdaptiveQuality) UpdateSystemMetrics(cpu, memory float64, bandwidth int64) {
	aq.cpuUsage.Store(uint64(cpu * 100))
	aq.memoryUsage.Store(uint64(memory * 100))
	aq.networkBandwidth.Store(bandwidth)
}

// UpdateStreamMetrics updates stream quality metrics
func (aq *AdaptiveQuality) UpdateStreamMetrics(bitrate int64, frameDropRate, packetLoss float64, latency time.Duration) {
	aq.currentBitrate.Store(bitrate)
	aq.frameDropRate.Store(uint64(frameDropRate * 1000))
	aq.packetLoss.Store(uint64(packetLoss * 1000))
	aq.processingLatency.Store(int64(latency))
}

// GetCurrentProfile returns the current quality profile
func (aq *AdaptiveQuality) GetCurrentProfile() QualityProfile {
	idx := aq.currentProfile.Load()
	if idx >= 0 && int(idx) < len(aq.profiles) {
		return aq.profiles[idx]
	}
	return aq.profiles[QualityLevelMedium]
}

// SetTargetQuality manually sets target quality level
func (aq *AdaptiveQuality) SetTargetQuality(level QualityLevel) {
	aq.mu.Lock()
	defer aq.mu.Unlock()

	if int(level) < len(aq.profiles) {
		aq.targetProfile = int(level)
		aq.logger.WithField("target_level", level.String()).Info("Target quality set")
	}
}

// GetQualityScore returns current quality score (0-1)
func (aq *AdaptiveQuality) GetQualityScore() float64 {
	aq.mu.RLock()
	defer aq.mu.RUnlock()
	return aq.qualityScore
}

// GetMLData returns collected ML training data
func (aq *AdaptiveQuality) GetMLData() []MLDataPoint {
	return aq.mlDataCollector.GetData()
}

// Stop stops the adaptive quality manager
func (aq *AdaptiveQuality) Stop() {
	aq.cancel()
}

// Private methods

func (aq *AdaptiveQuality) adaptationLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-aq.ctx.Done():
			return
		case <-ticker.C:
			aq.evaluateAndAdapt()
		}
	}
}

func (aq *AdaptiveQuality) evaluateAndAdapt() {
	// Calculate quality score
	score := aq.calculateQualityScore()

	aq.mu.Lock()
	aq.qualityScore = score
	aq.mu.Unlock()

	// Determine target profile based on score and resources
	newTarget := aq.determineTargetProfile(score)

	aq.mu.Lock()
	aq.targetProfile = newTarget
	aq.mu.Unlock()

	// Gradually transition to target
	current := int(aq.currentProfile.Load())
	if current != newTarget {
		aq.transitionQuality(current, newTarget)
	}

	// Collect ML data
	aq.collectMLData(score)

	// Update metrics
	aq.qualityGauge.Set(float64(aq.currentProfile.Load()))
	aq.bitrateGauge.Set(float64(aq.currentBitrate.Load()))
}

func (aq *AdaptiveQuality) calculateQualityScore() float64 {
	// Get current metrics
	cpu := float64(aq.cpuUsage.Load()) / 100
	memory := float64(aq.memoryUsage.Load()) / 100
	frameDrops := float64(aq.frameDropRate.Load()) / 1000
	packetLoss := float64(aq.packetLoss.Load()) / 1000
	latency := time.Duration(aq.processingLatency.Load())

	// Calculate component scores
	cpuScore := 1.0 - math.Min(cpu/0.8, 1.0) // Penalty above 80%
	memoryScore := 1.0 - math.Min(memory/0.8, 1.0)
	dropScore := 1.0 - math.Min(frameDrops/0.05, 1.0) // Penalty above 5%
	lossScore := 1.0 - math.Min(packetLoss/0.02, 1.0) // Penalty above 2%

	// Latency score (penalty above 100ms)
	latencyScore := 1.0
	if latency > 100*time.Millisecond {
		latencyScore = math.Max(0, 1.0-float64(latency-100*time.Millisecond)/float64(400*time.Millisecond))
	}

	// Weighted average
	weights := map[string]float64{
		"cpu":     0.25,
		"memory":  0.20,
		"drops":   0.25,
		"loss":    0.20,
		"latency": 0.10,
	}

	score := cpuScore*weights["cpu"] +
		memoryScore*weights["memory"] +
		dropScore*weights["drops"] +
		lossScore*weights["loss"] +
		latencyScore*weights["latency"]

	return score
}

func (aq *AdaptiveQuality) determineTargetProfile(score float64) int {
	// Get resource constraints
	cpu := float64(aq.cpuUsage.Load()) / 100
	memory := float64(aq.memoryUsage.Load()) / 100
	bandwidth := aq.networkBandwidth.Load()

	// Start from current and adjust based on score
	current := int(aq.currentProfile.Load())
	target := current

	if score > 0.8 && cpu < 0.6 && memory < 0.6 {
		// System is healthy, can increase quality (move toward Ultra/index 0)
		if current > 0 {
			// Check if bandwidth supports higher quality
			nextProfile := aq.profiles[current-1]
			if bandwidth > nextProfile.Bitrate*120/100 { // 20% headroom
				target = current - 1
			}
		}
	} else if score < 0.5 || cpu > 0.85 || memory > 0.85 {
		// System is struggling, decrease quality (move toward Minimum/higher index)
		if current < len(aq.profiles)-1 {
			target = current + 1
		}
	} else if score < 0.3 || cpu > 0.95 || memory > 0.95 {
		// Emergency quality reduction (move toward Minimum/higher index)
		target = min(len(aq.profiles)-1, current+2)
	}

	return target
}

func (aq *AdaptiveQuality) transitionQuality(current, target int) {
	if current == target {
		return
	}

	// Determine direction
	step := 1
	if target < current {
		step = -1
	}

	// Move one step towards target
	newProfile := current + step
	if newProfile >= 0 && newProfile < len(aq.profiles) {
		oldProfile := aq.profiles[current]
		aq.currentProfile.Store(int32(newProfile))
		aq.adaptationCount++
		aq.adaptationCounter.Inc()

		aq.mu.Lock()
		aq.lastAdaptation = time.Now()
		aq.mu.Unlock()

		// Notify callback
		if aq.onQualityChange != nil {
			aq.onQualityChange(oldProfile, aq.profiles[newProfile])
		}

		aq.logger.WithFields(logger.Fields{
			"old_level": oldProfile.Name,
			"new_level": aq.profiles[newProfile].Name,
			"score":     aq.qualityScore,
		}).Info("Quality adapted")
	}
}

func (aq *AdaptiveQuality) collectMLData(score float64) {
	point := MLDataPoint{
		Timestamp:         time.Now(),
		CPUUsage:          float64(aq.cpuUsage.Load()) / 100,
		MemoryUsage:       float64(aq.memoryUsage.Load()) / 100,
		NetworkBandwidth:  aq.networkBandwidth.Load(),
		PacketLoss:        float64(aq.packetLoss.Load()) / 1000,
		FrameDropRate:     float64(aq.frameDropRate.Load()) / 1000,
		ProcessingLatency: time.Duration(aq.processingLatency.Load()),
		QualityLevel:      QualityLevel(aq.currentProfile.Load()),
		QualityScore:      score,
	}

	aq.mlDataCollector.AddPoint(point)
}

// SetCallback sets quality change callback
func (aq *AdaptiveQuality) SetCallback(onChange func(old, new QualityProfile)) {
	aq.mu.Lock()
	defer aq.mu.Unlock()
	aq.onQualityChange = onChange
}

// GetStatistics returns adaptation statistics
func (aq *AdaptiveQuality) GetStatistics() map[string]interface{} {
	aq.mu.RLock()
	defer aq.mu.RUnlock()

	current := aq.GetCurrentProfile()

	return map[string]interface{}{
		"current_level":    current.Name,
		"target_level":     aq.profiles[aq.targetProfile].Name,
		"quality_score":    aq.qualityScore,
		"adaptation_count": aq.adaptationCount,
		"last_adaptation":  aq.lastAdaptation,
		"current_bitrate":  aq.currentBitrate.Load(),
		"cpu_usage":        float64(aq.cpuUsage.Load()) / 100,
		"memory_usage":     float64(aq.memoryUsage.Load()) / 100,
		"frame_drop_rate":  float64(aq.frameDropRate.Load()) / 1000,
		"packet_loss":      float64(aq.packetLoss.Load()) / 1000,
	}
}

// String returns string representation of QualityLevel
func (q QualityLevel) String() string {
	switch q {
	case QualityLevelUltra:
		return "ultra"
	case QualityLevelHigh:
		return "high"
	case QualityLevelMedium:
		return "medium"
	case QualityLevelLow:
		return "low"
	case QualityLevelMinimum:
		return "minimum"
	default:
		return "unknown"
	}
}

// MLDataCollector methods

// AddPoint adds a new data point
func (c *MLDataCollector) AddPoint(point MLDataPoint) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.dataPoints = append(c.dataPoints, point)

	// Trim to max size
	if len(c.dataPoints) > c.maxPoints {
		c.dataPoints = c.dataPoints[len(c.dataPoints)-c.maxPoints:]
	}
}

// GetData returns all collected data points
func (c *MLDataCollector) GetData() []MLDataPoint {
	c.mu.Lock()
	defer c.mu.Unlock()

	data := make([]MLDataPoint, len(c.dataPoints))
	copy(data, c.dataPoints)
	return data
}
