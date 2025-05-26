package logger

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// SampledLogger provides frequency-aware logging with intelligent sampling
type SampledLogger struct {
	base          Logger
	samplers      map[string]*LogSampler
	samplersMutex sync.RWMutex
}

// LogSampler handles sampling for a specific log category
type LogSampler struct {
	// Configuration
	name           string
	maxFrequency   time.Duration // Maximum frequency for this category
	burstAllowance int           // Allow burst of N messages before sampling
	sampleRate     float64       // Sample rate after burst (0.0-1.0)

	// State
	lastLogTime  int64 // Last log time (atomic)
	messageCount int64 // Total messages seen (atomic)
	droppedCount int64 // Messages dropped due to sampling (atomic)
	burstCounter int64 // Current burst counter (atomic)

	// Statistics
	totalMessages   int64 // Total messages processed (atomic)
	sampledMessages int64 // Messages that were logged (atomic)
}

// NewSampledLogger creates a new sampled logger
func NewSampledLogger(base Logger) *SampledLogger {
	return &SampledLogger{
		base:     base,
		samplers: make(map[string]*LogSampler),
	}
}

// WithSampler adds a sampler configuration for a specific category
func (s *SampledLogger) WithSampler(name string, maxFreq time.Duration, burstAllowance int, sampleRate float64) *SampledLogger {
	s.samplersMutex.Lock()
	defer s.samplersMutex.Unlock()

	s.samplers[name] = &LogSampler{
		name:           name,
		maxFrequency:   maxFreq,
		burstAllowance: burstAllowance,
		sampleRate:     sampleRate,
	}

	return s
}

// shouldLog determines if a message should be logged based on sampling rules
func (s *SampledLogger) shouldLog(category string) bool {
	s.samplersMutex.RLock()
	sampler, exists := s.samplers[category]
	s.samplersMutex.RUnlock()

	if !exists {
		// No sampler configured, always log
		return true
	}

	now := time.Now().UnixNano()
	atomic.AddInt64(&sampler.totalMessages, 1)

	// Check if we're within the minimum frequency interval
	lastLog := atomic.LoadInt64(&sampler.lastLogTime)
	if now-lastLog < sampler.maxFrequency.Nanoseconds() {
		// Within frequency limit, check burst allowance
		burstCount := atomic.LoadInt64(&sampler.burstCounter)
		if burstCount < int64(sampler.burstAllowance) {
			// Within burst allowance, allow the log
			atomic.AddInt64(&sampler.burstCounter, 1)
			atomic.StoreInt64(&sampler.lastLogTime, now)
			atomic.AddInt64(&sampler.sampledMessages, 1)
			return true
		}

		// Beyond burst allowance, apply sampling
		if sampler.sampleRate <= 0 {
			atomic.AddInt64(&sampler.droppedCount, 1)
			return false
		}

		// Simple sampling: log every 1/sampleRate messages
		msgCount := atomic.AddInt64(&sampler.messageCount, 1)
		if float64(msgCount)*sampler.sampleRate >= 1.0 {
			atomic.StoreInt64(&sampler.messageCount, 0) // Reset counter
			atomic.StoreInt64(&sampler.lastLogTime, now)
			atomic.AddInt64(&sampler.sampledMessages, 1)
			return true
		}

		atomic.AddInt64(&sampler.droppedCount, 1)
		return false
	}

	// Enough time has passed, reset burst counter and allow the log
	atomic.StoreInt64(&sampler.burstCounter, 1)
	atomic.StoreInt64(&sampler.lastLogTime, now)
	atomic.AddInt64(&sampler.sampledMessages, 1)
	return true
}

// VideoFrameLog logs video frame processing events with sampling
func (s *SampledLogger) VideoFrameLog(level logrus.Level, category string, msg string, fields map[string]interface{}) {
	if !s.shouldLog(category) {
		return
	}

	// Add sampling metadata
	s.addSamplingMetadata(category, fields)
	s.base.WithFields(fields).Log(level, msg)
}

// addSamplingMetadata adds sampling statistics to log fields
func (s *SampledLogger) addSamplingMetadata(category string, fields map[string]interface{}) {
	s.samplersMutex.RLock()
	sampler, exists := s.samplers[category]
	s.samplersMutex.RUnlock()

	if !exists {
		return
	}

	total := atomic.LoadInt64(&sampler.totalMessages)
	sampled := atomic.LoadInt64(&sampler.sampledMessages)
	dropped := atomic.LoadInt64(&sampler.droppedCount)

	if total > 0 {
		fields["_sampling_total"] = total
		fields["_sampling_logged"] = sampled
		fields["_sampling_dropped"] = dropped
		fields["_sampling_rate"] = float64(sampled) / float64(total)
	}
}

// Info logs an info message (no sampling for basic Info calls)
func (s *SampledLogger) Info(args ...interface{}) {
	s.base.Info(args...)
}

// InfoWithCategory logs an info message with sampling for the specified category
func (s *SampledLogger) InfoWithCategory(category, msg string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["category"] = category
	s.VideoFrameLog(logrus.InfoLevel, category, msg, fields)
}

// DebugWithCategory logs a debug message with sampling for the specified category
func (s *SampledLogger) DebugWithCategory(category, msg string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["category"] = category
	s.VideoFrameLog(logrus.DebugLevel, category, msg, fields)
}

// WarnWithCategory logs a warning message with sampling for the specified category
func (s *SampledLogger) WarnWithCategory(category, msg string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["category"] = category
	s.VideoFrameLog(logrus.WarnLevel, category, msg, fields)
}

// ErrorWithCategory logs an error message (always logged, no sampling for errors)
func (s *SampledLogger) ErrorWithCategory(category, msg string, fields map[string]interface{}) {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["category"] = category
	s.base.WithFields(fields).Error(msg)
}

// GetSamplerStats returns statistics for all samplers
func (s *SampledLogger) GetSamplerStats() map[string]SamplerStats {
	s.samplersMutex.RLock()
	defer s.samplersMutex.RUnlock()

	stats := make(map[string]SamplerStats)
	for name, sampler := range s.samplers {
		stats[name] = SamplerStats{
			Name:            name,
			TotalMessages:   atomic.LoadInt64(&sampler.totalMessages),
			SampledMessages: atomic.LoadInt64(&sampler.sampledMessages),
			DroppedMessages: atomic.LoadInt64(&sampler.droppedCount),
			CurrentRate:     float64(atomic.LoadInt64(&sampler.sampledMessages)) / float64(atomic.LoadInt64(&sampler.totalMessages)),
		}
	}

	return stats
}

// SamplerStats holds statistics for a log sampler
type SamplerStats struct {
	Name            string  `json:"name"`
	TotalMessages   int64   `json:"total_messages"`
	SampledMessages int64   `json:"sampled_messages"`
	DroppedMessages int64   `json:"dropped_messages"`
	CurrentRate     float64 `json:"current_rate"`
}

// Common video pipeline log categories
const (
	CategoryFrameProcessing  = "frame_processing"
	CategoryFrameReordering  = "frame_reordering"
	CategoryPacketProcessing = "packet_processing"
	CategoryCodecDetection   = "codec_detection"
	CategoryBufferManagement = "buffer_management"
	CategorySyncAdjustment   = "sync_adjustment"
	CategoryBackpressure     = "backpressure"
	CategoryRecovery         = "recovery"
	CategoryMetrics          = "metrics"
)

// NewVideoLogger creates a pre-configured sampled logger for video processing
func NewVideoLogger(base Logger) *SampledLogger {
	return NewSampledLogger(base).
		// High-frequency frame processing: max 10/sec, burst 5, then 10% sampling
		WithSampler(CategoryFrameProcessing, 100*time.Millisecond, 5, 0.1).
		// Frame reordering: max 5/sec, burst 3, then 20% sampling
		WithSampler(CategoryFrameReordering, 200*time.Millisecond, 3, 0.2).
		// Very high-frequency packet processing: max 20/sec, burst 10, then 5% sampling
		WithSampler(CategoryPacketProcessing, 50*time.Millisecond, 10, 0.05).
		// Codec detection: max 2/sec, burst 2, then 50% sampling
		WithSampler(CategoryCodecDetection, 500*time.Millisecond, 2, 0.5).
		// Buffer management: max 5/sec, burst 3, then 30% sampling
		WithSampler(CategoryBufferManagement, 200*time.Millisecond, 3, 0.3).
		// Sync adjustments: max 1/sec, burst 2, then 100% sampling (important)
		WithSampler(CategorySyncAdjustment, 1*time.Second, 2, 1.0).
		// Backpressure: max 2/sec, burst 3, then 100% sampling (important)
		WithSampler(CategoryBackpressure, 500*time.Millisecond, 3, 1.0).
		// Recovery events: no sampling (always important)
		// CategoryRecovery intentionally not configured - always logs
		// Metrics: max 1/sec, burst 1, then 100% sampling
		WithSampler(CategoryMetrics, 1*time.Second, 1, 1.0)
}

// Implement Logger interface for SampledLogger
func (s *SampledLogger) WithFields(fields map[string]interface{}) Logger {
	return &SampledLogger{
		base:     s.base.WithFields(fields),
		samplers: s.samplers,
	}
}

func (s *SampledLogger) WithField(key string, value interface{}) Logger {
	return &SampledLogger{
		base:     s.base.WithField(key, value),
		samplers: s.samplers,
	}
}

func (s *SampledLogger) WithError(err error) Logger {
	return &SampledLogger{
		base:     s.base.WithError(err),
		samplers: s.samplers,
	}
}

func (s *SampledLogger) Debug(args ...interface{}) {
	s.base.Debug(args...)
}

func (s *SampledLogger) Warn(args ...interface{}) {
	s.base.Warn(args...)
}

func (s *SampledLogger) Error(args ...interface{}) {
	s.base.Error(args...)
}

func (s *SampledLogger) Log(level logrus.Level, args ...interface{}) {
	s.base.Log(level, args...)
}

// Printf-style methods for backward compatibility
func (s *SampledLogger) Debugf(format string, args ...interface{}) {
	s.base.Debugf(format, args...)
}

func (s *SampledLogger) Infof(format string, args ...interface{}) {
	s.base.Infof(format, args...)
}

func (s *SampledLogger) Warnf(format string, args ...interface{}) {
	s.base.Warnf(format, args...)
}

func (s *SampledLogger) Errorf(format string, args ...interface{}) {
	s.base.Errorf(format, args...)
}

func (s *SampledLogger) Fatal(args ...interface{}) {
	s.base.Fatal(args...)
}
