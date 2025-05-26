package logger

import (
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockLogger for testing
type MockLogger struct {
	mu    sync.RWMutex
	calls []MockLogCall
}

type MockLogCall struct {
	Level  logrus.Level
	Args   []interface{}
	Fields map[string]interface{}
	Format string
}

func NewMockLogger() *MockLogger {
	return &MockLogger{calls: make([]MockLogCall, 0)}
}

func (m *MockLogger) WithFields(fields map[string]interface{}) Logger {
	return m
}

func (m *MockLogger) WithField(key string, value interface{}) Logger {
	return m
}

func (m *MockLogger) WithError(err error) Logger {
	return m
}

func (m *MockLogger) Debug(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.DebugLevel, Args: args})
}

func (m *MockLogger) Info(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.InfoLevel, Args: args})
}

func (m *MockLogger) Warn(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.WarnLevel, Args: args})
}

func (m *MockLogger) Error(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.ErrorLevel, Args: args})
}

func (m *MockLogger) Log(level logrus.Level, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: level, Args: args})
}

func (m *MockLogger) Debugf(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.DebugLevel, Args: args, Format: format})
}

func (m *MockLogger) Infof(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.InfoLevel, Args: args, Format: format})
}

func (m *MockLogger) Warnf(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.WarnLevel, Args: args, Format: format})
}

func (m *MockLogger) Errorf(format string, args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.ErrorLevel, Args: args, Format: format})
}

func (m *MockLogger) Fatal(args ...interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, MockLogCall{Level: logrus.FatalLevel, Args: args})
}

func (m *MockLogger) GetCalls() []MockLogCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	callsCopy := make([]MockLogCall, len(m.calls))
	copy(callsCopy, m.calls)
	return callsCopy
}

func (m *MockLogger) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = make([]MockLogCall, 0)
}

// TestNewSampledLogger tests creation of a new sampled logger
func TestNewSampledLogger(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase)
	
	assert.NotNil(t, sampled)
	assert.Equal(t, mockBase, sampled.base)
	assert.NotNil(t, sampled.samplers)
	assert.Len(t, sampled.samplers, 0)
}

// TestWithSampler tests adding sampler configurations
func TestWithSampler(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase)
	
	// Add a sampler
	result := sampled.WithSampler("test_category", 100*time.Millisecond, 5, 0.5)
	
	assert.Equal(t, sampled, result, "WithSampler should return the same instance")
	assert.Len(t, sampled.samplers, 1)
	
	sampler := sampled.samplers["test_category"]
	assert.NotNil(t, sampler)
	assert.Equal(t, "test_category", sampler.name)
	assert.Equal(t, 100*time.Millisecond, sampler.maxFrequency)
	assert.Equal(t, 5, sampler.burstAllowance)
	assert.Equal(t, 0.5, sampler.sampleRate)
}

// TestShouldLog_NoSampler tests shouldLog with no sampler configured
func TestShouldLog_NoSampler(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase)
	
	// No sampler configured - should always log
	result := sampled.shouldLog("unconfigured_category")
	assert.True(t, result)
}

// TestShouldLog_WithinBurstAllowance tests shouldLog within burst allowance
func TestShouldLog_WithinBurstAllowance(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("test", 1*time.Second, 3, 0.1) // 3 burst, then 10% sampling
	
	// First 3 calls should be allowed (within burst)
	for i := 0; i < 3; i++ {
		result := sampled.shouldLog("test")
		assert.True(t, result, "Call %d should be allowed within burst", i+1)
	}
	
	// Check sampler stats
	sampler := sampled.samplers["test"]
	assert.Equal(t, int64(3), sampler.totalMessages)
	assert.Equal(t, int64(3), sampler.sampledMessages)
	assert.Equal(t, int64(3), sampler.burstCounter)
}

// TestShouldLog_SamplingAfterBurst tests sampling behavior after burst allowance
func TestShouldLog_SamplingAfterBurst(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("test", 10*time.Millisecond, 2, 0.5) // 2 burst, then 50% sampling
	
	// First 2 calls within burst
	for i := 0; i < 2; i++ {
		result := sampled.shouldLog("test")
		assert.True(t, result, "Call %d should be allowed within burst", i+1)
	}
	
	// Wait less than frequency limit to trigger sampling
	time.Sleep(5 * time.Millisecond)
	
	// Next calls should be sampled at 50% rate
	var loggedCount int
	for i := 0; i < 10; i++ {
		if sampled.shouldLog("test") {
			loggedCount++
		}
	}
	
	// With 50% sampling, we expect roughly half to be logged (but may vary due to timing)
	sampler := sampled.samplers["test"]
	assert.Greater(t, sampler.totalMessages, int64(10))
	assert.Greater(t, sampler.droppedCount, int64(0))
}

// TestShouldLog_ZeroSampleRate tests behavior with zero sample rate
func TestShouldLog_ZeroSampleRate(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("test", 10*time.Millisecond, 1, 0.0) // 1 burst, then 0% sampling
	
	// First call within burst
	result := sampled.shouldLog("test")
	assert.True(t, result)
	
	// Wait less than frequency limit
	time.Sleep(5 * time.Millisecond)
	
	// Subsequent calls should be dropped (0% sampling)
	for i := 0; i < 5; i++ {
		result := sampled.shouldLog("test")
		assert.False(t, result, "Call %d should be dropped with 0%% sampling", i+1)
	}
	
	sampler := sampled.samplers["test"]
	assert.Equal(t, int64(5), sampler.droppedCount)
}

// TestShouldLog_FrequencyReset tests burst counter reset after frequency interval
func TestShouldLog_FrequencyReset(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("test", 50*time.Millisecond, 2, 0.1)
	
	// Use up burst allowance
	for i := 0; i < 2; i++ {
		result := sampled.shouldLog("test")
		assert.True(t, result)
	}
	
	// Wait for frequency interval to pass
	time.Sleep(60 * time.Millisecond)
	
	// Should reset burst counter and allow logging again
	result := sampled.shouldLog("test")
	assert.True(t, result, "Should allow logging after frequency interval reset")
	
	sampler := sampled.samplers["test"]
	assert.Equal(t, int64(1), sampler.burstCounter, "Burst counter should reset to 1")
}

// TestVideoFrameLog tests the VideoFrameLog method
func TestVideoFrameLog(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("test_category", 100*time.Millisecond, 2, 1.0)
	
	fields := map[string]interface{}{"frame_id": 123}
	
	// Should log the first call
	sampled.VideoFrameLog(logrus.InfoLevel, "test_category", "test message", fields)
	
	calls := mockBase.GetCalls()
	assert.Len(t, calls, 1)
	assert.Equal(t, logrus.InfoLevel, calls[0].Level)
	
	// Check that sampling metadata was added to fields
	assert.Contains(t, fields, "_sampling_total")
	assert.Contains(t, fields, "_sampling_logged")
	assert.Contains(t, fields, "_sampling_dropped")
	assert.Contains(t, fields, "_sampling_rate")
}

// TestAddSamplingMetadata tests the sampling metadata addition
func TestAddSamplingMetadata(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("test", 100*time.Millisecond, 1, 0.5)
	
	// Generate some activity
	sampled.shouldLog("test") // Should log (within burst)
	sampled.shouldLog("test") // May be sampled
	
	fields := make(map[string]interface{})
	sampled.addSamplingMetadata("test", fields)
	
	assert.Contains(t, fields, "_sampling_total")
	assert.Contains(t, fields, "_sampling_logged")
	assert.Contains(t, fields, "_sampling_dropped")
	assert.Contains(t, fields, "_sampling_rate")
	
	// Check that values are reasonable
	total := fields["_sampling_total"].(int64)
	logged := fields["_sampling_logged"].(int64)
	assert.GreaterOrEqual(t, total, logged)
}

// TestInfoWithCategory tests InfoWithCategory method
func TestInfoWithCategory(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("info_test", 100*time.Millisecond, 1, 1.0)
	
	fields := map[string]interface{}{"key": "value"}
	sampled.InfoWithCategory("info_test", "test message", fields)
	
	assert.Contains(t, fields, "category")
	assert.Equal(t, "info_test", fields["category"])
}

// TestDebugWithCategory tests DebugWithCategory method
func TestDebugWithCategory(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("debug_test", 100*time.Millisecond, 1, 1.0)
	
	fields := map[string]interface{}{"key": "value"}
	sampled.DebugWithCategory("debug_test", "debug message", fields)
	
	assert.Contains(t, fields, "category")
	assert.Equal(t, "debug_test", fields["category"])
}

// TestWarnWithCategory tests WarnWithCategory method
func TestWarnWithCategory(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("warn_test", 100*time.Millisecond, 1, 1.0)
	
	fields := map[string]interface{}{"key": "value"}
	sampled.WarnWithCategory("warn_test", "warning message", fields)
	
	assert.Contains(t, fields, "category")
	assert.Equal(t, "warn_test", fields["category"])
}

// TestErrorWithCategory tests ErrorWithCategory method (no sampling)
func TestErrorWithCategory(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase)
	
	fields := map[string]interface{}{"key": "value"}
	sampled.ErrorWithCategory("error_test", "error message", fields)
	
	// Errors should always be logged regardless of sampling
	calls := mockBase.GetCalls()
	assert.Len(t, calls, 1)
	assert.Equal(t, logrus.ErrorLevel, calls[0].Level)
	
	assert.Contains(t, fields, "category")
	assert.Equal(t, "error_test", fields["category"])
}

// TestGetSamplerStats tests getting sampler statistics
func TestGetSamplerStats(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("stats_test", 100*time.Millisecond, 2, 0.5)
	
	// Generate some activity
	sampled.shouldLog("stats_test") // Should log
	sampled.shouldLog("stats_test") // Should log (within burst)
	sampled.shouldLog("stats_test") // May be sampled
	
	stats := sampled.GetSamplerStats()
	require.Contains(t, stats, "stats_test")
	
	samplerStats := stats["stats_test"]
	assert.Equal(t, "stats_test", samplerStats.Name)
	assert.GreaterOrEqual(t, samplerStats.TotalMessages, int64(3))
	assert.GreaterOrEqual(t, samplerStats.SampledMessages, int64(1))
	assert.GreaterOrEqual(t, samplerStats.CurrentRate, 0.0)
	assert.LessOrEqual(t, samplerStats.CurrentRate, 1.0)
}

// TestNewVideoLogger tests the pre-configured video logger
func TestNewVideoLogger(t *testing.T) {
	mockBase := NewMockLogger()
	videoLogger := NewVideoLogger(mockBase)
	
	assert.NotNil(t, videoLogger)
	
	// Check that all expected categories are configured
	expectedCategories := []string{
		CategoryFrameProcessing,
		CategoryFrameReordering,
		CategoryPacketProcessing,
		CategoryCodecDetection,
		CategoryBufferManagement,
		CategorySyncAdjustment,
		CategoryBackpressure,
		CategoryMetrics,
	}
	
	for _, category := range expectedCategories {
		sampler, exists := videoLogger.samplers[category]
		assert.True(t, exists, "Category %s should be configured", category)
		assert.NotNil(t, sampler)
		assert.Equal(t, category, sampler.name)
	}
	
	// CategoryRecovery should NOT be configured (always logs)
	_, exists := videoLogger.samplers[CategoryRecovery]
	assert.False(t, exists, "CategoryRecovery should not be configured")
}

// TestLoggerInterfaceMethods tests that SampledLogger implements Logger interface correctly
func TestLoggerInterfaceMethods(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase)
	
	// Test WithFields
	withFields := sampled.WithFields(map[string]interface{}{"key": "value"})
	assert.NotNil(t, withFields)
	sampledWithFields, ok := withFields.(*SampledLogger)
	assert.True(t, ok)
	assert.Equal(t, sampled.samplers, sampledWithFields.samplers)
	
	// Test WithField
	withField := sampled.WithField("key", "value")
	assert.NotNil(t, withField)
	sampledWithField, ok := withField.(*SampledLogger)
	assert.True(t, ok)
	assert.Equal(t, sampled.samplers, sampledWithField.samplers)
	
	// Test WithError
	withError := sampled.WithError(assert.AnError)
	assert.NotNil(t, withError)
	sampledWithError, ok := withError.(*SampledLogger)
	assert.True(t, ok)
	assert.Equal(t, sampled.samplers, sampledWithError.samplers)
	
	// Test logging methods delegate to base logger
	sampled.Debug("debug")
	sampled.Info("info")
	sampled.Warn("warn")
	sampled.Error("error")
	sampled.Log(logrus.InfoLevel, "log")
	sampled.Debugf("debugf %s", "test")
	sampled.Infof("infof %s", "test")
	sampled.Warnf("warnf %s", "test")
	sampled.Errorf("errorf %s", "test")
	sampled.Fatal("fatal")
	
	calls := mockBase.GetCalls()
	assert.Len(t, calls, 10) // All methods should have been called
}

// TestCategoryConstantsExist tests that all category constants are defined
func TestCategoryConstantsExist(t *testing.T) {
	categories := []string{
		CategoryFrameProcessing,
		CategoryFrameReordering,
		CategoryPacketProcessing,
		CategoryCodecDetection,
		CategoryBufferManagement,
		CategorySyncAdjustment,
		CategoryBackpressure,
		CategoryRecovery,
		CategoryMetrics,
	}
	
	for _, category := range categories {
		assert.NotEmpty(t, category, "Category constant should not be empty")
	}
}

// TestConcurrentAccess tests thread-safety of sampled logger
func TestConcurrentAccess(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("concurrent_test", 10*time.Millisecond, 5, 0.5)
	
	// Run multiple goroutines concurrently
	const numGoroutines = 10
	const logsPerGoroutine = 100
	
	done := make(chan bool, numGoroutines)
	
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < logsPerGoroutine; j++ {
				sampled.shouldLog("concurrent_test")
				sampled.InfoWithCategory("concurrent_test", "concurrent message", nil)
			}
			done <- true
		}()
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
	
	// Get stats to verify no race conditions
	stats := sampled.GetSamplerStats()
	require.Contains(t, stats, "concurrent_test")
	
	samplerStats := stats["concurrent_test"]
	assert.Greater(t, samplerStats.TotalMessages, int64(0))
	assert.GreaterOrEqual(t, samplerStats.TotalMessages, samplerStats.SampledMessages)
}

// TestNilFieldsHandling tests handling of nil fields maps
func TestNilFieldsHandling(t *testing.T) {
	mockBase := NewMockLogger()
	sampled := NewSampledLogger(mockBase).
		WithSampler("nil_test", 100*time.Millisecond, 1, 1.0)
	
	// These should not panic with nil fields
	sampled.InfoWithCategory("nil_test", "info message", nil)
	sampled.DebugWithCategory("nil_test", "debug message", nil)
	sampled.WarnWithCategory("nil_test", "warn message", nil)
	sampled.ErrorWithCategory("nil_test", "error message", nil)
	
	// Should have created fields maps internally
	calls := mockBase.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1)
}
