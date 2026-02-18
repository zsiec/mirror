package integrity

import (
	"math"
	"sync"
	"time"
)

// HealthMetrics contains metrics for health scoring
type HealthMetrics struct {
	FrameDropRate       float64
	TimestampDrift      time.Duration
	BufferUtilization   float64
	ErrorRate           float64
	BitrateVariance     float64
	ParameterSetChanges int
	RecoveryTime        time.Duration
	Latency             time.Duration
}

// HealthScorer calculates stream health scores
type HealthScorer struct {
	mu sync.RWMutex

	// Weights for different metrics
	weights map[string]float64

	// Historical data for trend analysis
	history     []HealthSnapshot
	historySize int

	// Thresholds
	criticalDropRate float64
	warningDropRate  float64
	maxDrift         time.Duration
	maxLatency       time.Duration
}

// HealthSnapshot represents health at a point in time
type HealthSnapshot struct {
	Timestamp time.Time
	Score     float64
	Metrics   HealthMetrics
}

// NewHealthScorer creates a new health scorer
func NewHealthScorer() *HealthScorer {
	return &HealthScorer{
		weights: map[string]float64{
			"frame_drop":    0.3,
			"timestamp":     0.2,
			"buffer":        0.15,
			"error_rate":    0.2,
			"bitrate":       0.05,
			"param_changes": 0.05,
			"latency":       0.05,
		},
		history:          make([]HealthSnapshot, 0, 100),
		historySize:      100,
		criticalDropRate: 0.1,  // 10% drops
		warningDropRate:  0.05, // 5% drops
		maxDrift:         100 * time.Millisecond,
		maxLatency:       500 * time.Millisecond,
	}
}

// CalculateScore computes health score from metrics
func (hs *HealthScorer) CalculateScore(metrics HealthMetrics) float64 {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	scores := make(map[string]float64)

	// Frame drop score (inverse of drop rate)
	scores["frame_drop"] = hs.scoreFrameDrops(metrics.FrameDropRate)

	// Timestamp drift score
	scores["timestamp"] = hs.scoreTimestampDrift(metrics.TimestampDrift)

	// Buffer utilization score (optimal is 50-80%)
	scores["buffer"] = hs.scoreBufferUtilization(metrics.BufferUtilization)

	// Error rate score
	scores["error_rate"] = hs.scoreErrorRate(metrics.ErrorRate)

	// Bitrate variance score
	scores["bitrate"] = hs.scoreBitrateVariance(metrics.BitrateVariance)

	// Parameter set stability score
	scores["param_changes"] = hs.scoreParameterSetChanges(metrics.ParameterSetChanges)

	// Latency score
	scores["latency"] = hs.scoreLatency(metrics.Latency)

	// Calculate weighted average
	totalScore := 0.0
	totalWeight := 0.0

	for metric, score := range scores {
		weight := hs.weights[metric]
		totalScore += score * weight
		totalWeight += weight
	}

	finalScore := totalScore / totalWeight

	// Store in history
	hs.addToHistory(HealthSnapshot{
		Timestamp: time.Now(),
		Score:     finalScore,
		Metrics:   metrics,
	})

	return finalScore
}

// GetTrend returns health score trend
func (hs *HealthScorer) GetTrend() (trend float64, improving bool) {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	if len(hs.history) < 2 {
		return 0, true
	}

	// Calculate trend over last 10 samples
	samples := 10
	if len(hs.history) < samples {
		samples = len(hs.history)
	}

	start := len(hs.history) - samples
	end := len(hs.history) - 1

	oldAvg := hs.averageScore(hs.history[start : start+samples/2])
	newAvg := hs.averageScore(hs.history[end-samples/2+1 : end+1])

	trend = newAvg - oldAvg
	improving = trend > 0

	return trend, improving
}

// GetHistoricalScores returns recent health scores
func (hs *HealthScorer) GetHistoricalScores(duration time.Duration) []HealthSnapshot {
	hs.mu.RLock()
	defer hs.mu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var result []HealthSnapshot

	for _, snapshot := range hs.history {
		if snapshot.Timestamp.After(cutoff) {
			result = append(result, snapshot)
		}
	}

	return result
}

// SetWeights updates scoring weights
func (hs *HealthScorer) SetWeights(weights map[string]float64) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	for k, v := range weights {
		if v >= 0 && v <= 1 {
			hs.weights[k] = v
		}
	}
}

// Private scoring methods

func (hs *HealthScorer) scoreFrameDrops(dropRate float64) float64 {
	if dropRate >= hs.criticalDropRate {
		return 0.0
	}
	if dropRate >= hs.warningDropRate {
		// Linear degradation between warning and critical
		return 0.5 * (hs.criticalDropRate - dropRate) / (hs.criticalDropRate - hs.warningDropRate)
	}
	// Exponential curve for good performance
	return 1.0 - math.Pow(dropRate/hs.warningDropRate, 2)
}

func (hs *HealthScorer) scoreTimestampDrift(drift time.Duration) float64 {
	if drift < 0 {
		drift = -drift
	}

	if drift >= hs.maxDrift {
		return 0.0
	}

	// Linear degradation
	return 1.0 - float64(drift)/float64(hs.maxDrift)
}

func (hs *HealthScorer) scoreBufferUtilization(utilization float64) float64 {
	// Optimal range is 50-80%
	if utilization < 0.3 || utilization > 0.95 {
		return 0.0
	}
	if utilization >= 0.5 && utilization <= 0.8 {
		return 1.0
	}
	if utilization < 0.5 {
		// Too low
		return (utilization - 0.3) / 0.2
	}
	// Too high
	return (0.95 - utilization) / 0.15
}

func (hs *HealthScorer) scoreErrorRate(errorRate float64) float64 {
	if errorRate >= 0.1 {
		return 0.0
	}
	if errorRate <= 0.001 {
		return 1.0
	}
	// Logarithmic scale
	return 1.0 - (math.Log10(errorRate*1000)+3)/5
}

func (hs *HealthScorer) scoreBitrateVariance(variance float64) float64 {
	// Lower variance is better
	if variance >= 0.5 {
		return 0.0
	}
	return 1.0 - variance*2
}

func (hs *HealthScorer) scoreParameterSetChanges(changes int) float64 {
	// Fewer changes is better
	if changes == 0 {
		return 1.0
	}
	if changes >= 10 {
		return 0.0
	}
	return 1.0 - float64(changes)/10.0
}

func (hs *HealthScorer) scoreLatency(latency time.Duration) float64 {
	if latency >= hs.maxLatency {
		return 0.0
	}
	if latency <= 50*time.Millisecond {
		return 1.0
	}
	// Linear degradation
	return 1.0 - float64(latency-50*time.Millisecond)/float64(hs.maxLatency-50*time.Millisecond)
}

func (hs *HealthScorer) addToHistory(snapshot HealthSnapshot) {
	hs.history = append(hs.history, snapshot)

	// Trim to size
	if len(hs.history) > hs.historySize {
		hs.history = hs.history[len(hs.history)-hs.historySize:]
	}
}

func (hs *HealthScorer) averageScore(snapshots []HealthSnapshot) float64 {
	if len(snapshots) == 0 {
		return 0
	}

	sum := 0.0
	for _, s := range snapshots {
		sum += s.Score
	}

	return sum / float64(len(snapshots))
}
