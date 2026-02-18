package integrity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

func TestStreamValidator(t *testing.T) {
	t.Run("ValidateFrame", func(t *testing.T) {
		sv := NewStreamValidator("test-stream", logger.NewNullLogger())

		// Valid frame
		frame := &types.VideoFrame{
			PTS:       1000,
			DTS:       900,
			TotalSize: len([]byte("test frame data")),
			Type:      types.FrameTypeI,
			NALUnits: []types.NALUnit{
				{Data: []byte("test frame data")},
			},
		}
		frame.SetFlag(types.FrameFlagKeyframe)

		err := sv.ValidateFrame(frame)
		assert.NoError(t, err)
		assert.Equal(t, uint64(1), sv.frameCount.Load())

		// Invalid timestamp
		badFrame := &types.VideoFrame{
			PTS:       500,
			DTS:       600, // DTS > PTS
			TotalSize: len([]byte("test")),
			NALUnits: []types.NALUnit{
				{Data: []byte("test")},
			},
		}

		err = sv.ValidateFrame(badFrame)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "DTS > PTS")

		// Empty frame
		emptyFrame := &types.VideoFrame{
			PTS:       1000,
			DTS:       900,
			TotalSize: 0,
			NALUnits:  []types.NALUnit{},
		}

		err = sv.ValidateFrame(emptyFrame)
		assert.NoError(t, err) // Size validation is warning, not error

		// Nil frame
		err = sv.ValidateFrame(nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nil frame")
	})

	t.Run("ValidateGOP", func(t *testing.T) {
		sv := NewStreamValidator("test-stream", logger.NewNullLogger())

		// Valid GOP
		keyframe := &types.VideoFrame{
			PTS: 1000, DTS: 900, Type: types.FrameTypeIDR,
			TotalSize: 100,
		}
		keyframe.SetFlag(types.FrameFlagKeyframe)

		gop := &types.GOP{
			Frames: []*types.VideoFrame{
				keyframe,
				{PTS: 1100, DTS: 1000, Type: types.FrameTypeP, TotalSize: 50},
				{PTS: 1200, DTS: 1100, Type: types.FrameTypeB, TotalSize: 30},
			},
			Keyframe: keyframe,
			Closed:   true,
		}

		err := sv.ValidateGOP(gop)
		assert.NoError(t, err)

		// GOP not starting with IDR
		badGOP := &types.GOP{
			Frames: []*types.VideoFrame{
				{PTS: 1000, DTS: 900, Type: types.FrameTypeP},
			},
		}

		err = sv.ValidateGOP(badGOP)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must start with IDR")

		// Non-monotonic PTS
		idrFrame := &types.VideoFrame{PTS: 1000, DTS: 900, Type: types.FrameTypeIDR}
		idrFrame.SetFlag(types.FrameFlagKeyframe)
		badOrderGOP := &types.GOP{
			Frames: []*types.VideoFrame{
				idrFrame,
				{PTS: 900, DTS: 800, Type: types.FrameTypeP}, // PTS goes backward
			},
		}

		err = sv.ValidateGOP(badOrderGOP)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "non-monotonic PTS")

		// Missing parameter sets - GOP without keyframe
		noParamsGOP := &types.GOP{
			Frames: []*types.VideoFrame{
				{PTS: 1000, DTS: 900, Type: types.FrameTypeP, TotalSize: 100},
				{PTS: 1100, DTS: 1000, Type: types.FrameTypeB, TotalSize: 50},
			},
			Keyframe: nil, // No keyframe with parameter sets
		}

		err = sv.ValidateGOP(noParamsGOP)
		assert.Error(t, err)
		// The frames are P and B frames, so it should complain about not starting with IDR
		assert.Contains(t, err.Error(), "must start with IDR")
	})

	t.Run("Checksum Validation", func(t *testing.T) {
		sv := NewStreamValidator("test-stream", logger.NewNullLogger())
		sv.checksumInterval = 1 // Check every frame for testing

		frame := &types.VideoFrame{
			PTS:       1000,
			DTS:       900,
			TotalSize: len([]byte("test frame data")),
			NALUnits: []types.NALUnit{
				{Data: []byte("test frame data")},
			},
			CodecData: make(map[string]interface{}),
		}

		// First validation adds checksum
		err := sv.ValidateFrame(frame)
		assert.NoError(t, err)
		// Check if checksum was added to codec data
		if codecData, ok := frame.CodecData.(map[string]interface{}); ok {
			assert.Contains(t, codecData, "checksum")
		}

		// Second validation verifies checksum
		err = sv.ValidateFrame(frame)
		assert.NoError(t, err)

		// Corrupt data
		if len(frame.NALUnits) > 0 && len(frame.NALUnits[0].Data) > 0 {
			frame.NALUnits[0].Data[0] = 'X'
		}
		err = sv.ValidateFrame(frame)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")
	})

	t.Run("Health Score Calculation", func(t *testing.T) {
		sv := NewStreamValidator("test-stream", logger.NewNullLogger())

		// Add some frames
		for i := 0; i < 100; i++ {
			frame := &types.VideoFrame{
				PTS:       int64(i * 1000),
				DTS:       int64(i * 900),
				TotalSize: len([]byte("test frame")),
				NALUnits: []types.NALUnit{
					{Data: []byte("test frame")},
				},
			}
			sv.ValidateFrame(frame)
		}

		// Record some drops
		for i := 0; i < 5; i++ {
			sv.RecordDroppedFrame()
		}

		result := sv.GetValidationResult()
		assert.Equal(t, "test-stream", result.StreamID)
		assert.Equal(t, uint64(100), result.FrameCount)
		assert.Equal(t, uint64(5), result.DroppedFrames)
		assert.True(t, result.HealthScore > 0 && result.HealthScore <= 1)

		// With high drop rate, health should be lower
		for i := 0; i < 20; i++ {
			sv.RecordDroppedFrame()
		}

		result2 := sv.GetValidationResult()
		assert.Less(t, result2.HealthScore, result.HealthScore)
	})

	t.Run("Validation Callbacks", func(t *testing.T) {
		sv := NewStreamValidator("test-stream", logger.NewNullLogger())

		var detectedIssue ValidationIssue

		sv.SetCallbacks(
			func(result ValidationResult) {
				// Callback invoked when validation completes
			},
			func(issue ValidationIssue) {
				detectedIssue = issue
			},
		)

		// Trigger corruption detection
		badFrame := &types.VideoFrame{
			PTS:       500,
			DTS:       600, // DTS > PTS
			TotalSize: len([]byte("test")),
			NALUnits: []types.NALUnit{
				{Data: []byte("test")},
			},
		}

		sv.ValidateFrame(badFrame)

		assert.Equal(t, "timestamp_error", detectedIssue.Type)
		assert.Equal(t, SeverityError, detectedIssue.Severity)

		// Record dropped frame
		sv.RecordDroppedFrame()
		assert.Equal(t, "frame_drop", detectedIssue.Type)
		assert.Equal(t, SeverityWarning, detectedIssue.Severity)
	})
}

func TestChecksummer(t *testing.T) {
	t.Run("Basic Checksum", func(t *testing.T) {
		c := NewChecksummer()

		data := []byte("test data")
		checksum := c.Calculate(data)
		assert.NotEqual(t, uint32(0), checksum)

		// Same data should give same checksum
		checksum2 := c.Calculate(data)
		assert.Equal(t, checksum, checksum2)

		// Different data should give different checksum
		data2 := []byte("different data")
		checksum3 := c.Calculate(data2)
		assert.NotEqual(t, checksum, checksum3)

		// Empty data
		empty := c.Calculate([]byte{})
		assert.Equal(t, uint32(0), empty)
	})

	t.Run("Verify", func(t *testing.T) {
		c := NewChecksummer()

		data := []byte("test data")
		checksum := c.Calculate(data)

		assert.True(t, c.Verify(data, checksum))
		assert.False(t, c.Verify(data, checksum+1))
		assert.False(t, c.Verify([]byte("wrong data"), checksum))
	})

	t.Run("Incremental Checksum", func(t *testing.T) {
		c := NewChecksummer()

		chunks := [][]byte{
			[]byte("part1"),
			[]byte("part2"),
			[]byte("part3"),
		}

		incremental := c.CalculateIncremental(chunks)

		// Should match single calculation
		combined := append(append(chunks[0], chunks[1]...), chunks[2]...)
		single := c.Calculate(combined)

		assert.Equal(t, single, incremental)
	})

	t.Run("Cache Performance", func(t *testing.T) {
		c := NewChecksummer()

		data := make([]byte, 1024)
		for i := range data {
			data[i] = byte(i % 256)
		}

		// First calculation
		start := time.Now()
		checksum1 := c.Calculate(data)
		firstTime := time.Since(start)

		// Cached calculation should be faster
		start = time.Now()
		checksum2 := c.Calculate(data)
		cachedTime := time.Since(start)

		assert.Equal(t, checksum1, checksum2)
		assert.Less(t, cachedTime, firstTime)
	})

	t.Run("Different Checksum Types", func(t *testing.T) {
		c := NewChecksummer()
		data := []byte("test data")

		// CRC32 (default)
		crc32Sum := c.Calculate(data)

		// MD5
		c.SetType(ChecksumMD5)
		md5Sum := c.Calculate(data)

		assert.NotEqual(t, crc32Sum, md5Sum)
	})

	t.Run("StreamChecksum", func(t *testing.T) {
		sc := NewStreamChecksum()

		// Add data incrementally
		sc.Update([]byte("part1"))
		sc.Update([]byte("part2"))
		sc.Update([]byte("part3"))

		sum := sc.Sum()
		assert.NotEqual(t, uint32(0), sum)
		assert.Equal(t, uint64(15), sc.Size()) // 5+5+5

		// Reset
		sc.Reset()
		assert.Equal(t, uint32(0), sc.Sum())
		assert.Equal(t, uint64(0), sc.Size())
	})
}

func TestHealthScorer(t *testing.T) {
	t.Run("Score Calculation", func(t *testing.T) {
		hs := NewHealthScorer()

		// Perfect metrics
		perfect := HealthMetrics{
			FrameDropRate:       0.0,
			TimestampDrift:      0,
			BufferUtilization:   0.65, // Optimal range
			ErrorRate:           0.0,
			BitrateVariance:     0.0,
			ParameterSetChanges: 0,
			Latency:             30 * time.Millisecond,
		}

		score := hs.CalculateScore(perfect)
		assert.Greater(t, score, 0.95) // Should be nearly perfect

		// Poor metrics
		poor := HealthMetrics{
			FrameDropRate:       0.15, // 15% drops
			TimestampDrift:      200 * time.Millisecond,
			BufferUtilization:   0.98, // Too high
			ErrorRate:           0.2,
			BitrateVariance:     0.6,
			ParameterSetChanges: 15,
			Latency:             600 * time.Millisecond,
		}

		poorScore := hs.CalculateScore(poor)
		assert.Less(t, poorScore, 0.3) // Should be very low

		// Moderate metrics
		moderate := HealthMetrics{
			FrameDropRate:       0.02,
			TimestampDrift:      50 * time.Millisecond,
			BufferUtilization:   0.7,
			ErrorRate:           0.01,
			BitrateVariance:     0.2,
			ParameterSetChanges: 2,
			Latency:             150 * time.Millisecond,
		}

		modScore := hs.CalculateScore(moderate)
		assert.Greater(t, modScore, 0.6)
		assert.Less(t, modScore, 0.9)
	})

	t.Run("Individual Score Components", func(t *testing.T) {
		hs := NewHealthScorer()

		// Test frame drop scoring
		assert.Equal(t, 1.0, hs.scoreFrameDrops(0.0))
		assert.Greater(t, hs.scoreFrameDrops(0.02), 0.5)
		assert.Less(t, hs.scoreFrameDrops(0.08), 0.5)
		assert.Equal(t, 0.0, hs.scoreFrameDrops(0.15))

		// Test buffer utilization scoring
		assert.Equal(t, 1.0, hs.scoreBufferUtilization(0.65))
		assert.Greater(t, hs.scoreBufferUtilization(0.4), 0.0)
		assert.Equal(t, 0.0, hs.scoreBufferUtilization(0.99))

		// Test latency scoring
		assert.Equal(t, 1.0, hs.scoreLatency(30*time.Millisecond))
		assert.Greater(t, hs.scoreLatency(200*time.Millisecond), 0.5)
		assert.Equal(t, 0.0, hs.scoreLatency(600*time.Millisecond))
	})

	t.Run("Trend Analysis", func(t *testing.T) {
		hs := NewHealthScorer()

		// Add improving scores
		for i := 0; i < 10; i++ {
			metrics := HealthMetrics{
				FrameDropRate:     0.1 - float64(i)*0.01,
				BufferUtilization: 0.5 + float64(i)*0.03,
			}
			hs.CalculateScore(metrics)
			time.Sleep(10 * time.Millisecond)
		}

		trend, improving := hs.GetTrend()
		assert.True(t, improving)
		assert.Greater(t, trend, 0.0)

		// Add declining scores
		for i := 0; i < 10; i++ {
			metrics := HealthMetrics{
				FrameDropRate:     float64(i) * 0.01,
				ErrorRate:         float64(i) * 0.02,
				BufferUtilization: 0.7,
			}
			hs.CalculateScore(metrics)
			time.Sleep(10 * time.Millisecond)
		}

		trend2, improving2 := hs.GetTrend()
		assert.False(t, improving2)
		assert.Less(t, trend2, 0.0)
	})

	t.Run("Historical Scores", func(t *testing.T) {
		hs := NewHealthScorer()

		// Add scores over time
		for i := 0; i < 5; i++ {
			metrics := HealthMetrics{
				FrameDropRate:     float64(i) * 0.01,
				BufferUtilization: 0.65,
			}
			hs.CalculateScore(metrics)
			time.Sleep(100 * time.Millisecond)
		}

		// Get last 300ms of history
		history := hs.GetHistoricalScores(300 * time.Millisecond)
		assert.GreaterOrEqual(t, len(history), 2)
		assert.LessOrEqual(t, len(history), 3)

		// Verify chronological order
		for i := 1; i < len(history); i++ {
			assert.True(t, history[i].Timestamp.After(history[i-1].Timestamp))
		}
	})

	t.Run("Custom Weights", func(t *testing.T) {
		hs := NewHealthScorer()

		metrics := HealthMetrics{
			FrameDropRate:     0.05,
			BufferUtilization: 0.65,
			ErrorRate:         0.01,
		}

		score1 := hs.CalculateScore(metrics)

		// Change weights to emphasize frame drops
		hs.SetWeights(map[string]float64{
			"frame_drop": 0.8,
			"buffer":     0.1,
			"error_rate": 0.1,
		})

		score2 := hs.CalculateScore(metrics)

		// Score should be different with different weights
		assert.NotEqual(t, score1, score2)
	})
}

func BenchmarkStreamValidation(b *testing.B) {
	sv := NewStreamValidator("bench-stream", logger.NewNullLogger())

	frame := &types.VideoFrame{
		PTS:       1000,
		DTS:       900,
		TotalSize: 1024,
		NALUnits: []types.NALUnit{
			{Data: make([]byte, 1024)},
		},
		CodecData: make(map[string]interface{}),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sv.ValidateFrame(frame)
	}
}

func BenchmarkChecksumCalculation(b *testing.B) {
	c := NewChecksummer()
	data := make([]byte, 4096)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Calculate(data)
	}
}

func BenchmarkHealthScoring(b *testing.B) {
	hs := NewHealthScorer()

	metrics := HealthMetrics{
		FrameDropRate:       0.02,
		TimestampDrift:      50 * time.Millisecond,
		BufferUtilization:   0.7,
		ErrorRate:           0.01,
		BitrateVariance:     0.2,
		ParameterSetChanges: 2,
		Latency:             150 * time.Millisecond,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hs.CalculateScore(metrics)
	}
}
