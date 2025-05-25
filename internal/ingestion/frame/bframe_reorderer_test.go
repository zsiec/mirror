package frame

import (
	"container/heap"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/mirror/internal/ingestion/types"
	"github.com/zsiec/mirror/internal/logger"
)

// testLogger creates a logger suitable for tests
func testLogger() logger.Logger {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Only show errors in tests
	return log
}

func TestNewBFrameReorderer(t *testing.T) {
	tests := []struct {
		name            string
		maxReorderDepth int
		maxDelay        time.Duration
		wantDepth       int
		wantDelay       time.Duration
	}{
		{
			name:            "Valid parameters",
			maxReorderDepth: 5,
			maxDelay:        100 * time.Millisecond,
			wantDepth:       5,
			wantDelay:       100 * time.Millisecond,
		},
		{
			name:            "Zero depth uses default",
			maxReorderDepth: 0,
			maxDelay:        100 * time.Millisecond,
			wantDepth:       3,
			wantDelay:       100 * time.Millisecond,
		},
		{
			name:            "Zero delay uses default",
			maxReorderDepth: 5,
			maxDelay:        0,
			wantDepth:       5,
			wantDelay:       200 * time.Millisecond,
		},
		{
			name:            "Negative values use defaults",
			maxReorderDepth: -1,
			maxDelay:        -1,
			wantDepth:       3,
			wantDelay:       200 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewBFrameReorderer(tt.maxReorderDepth, tt.maxDelay, testLogger())
			assert.NotNil(t, r)
			assert.Equal(t, tt.wantDepth, r.maxReorderDepth)
			assert.Equal(t, tt.wantDelay, r.maxDelay)
			assert.Equal(t, 0, len(r.buffer))
		})
	}
}

func TestBFrameReorderer_SimpleSequence(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Simple GOP structure: I B B P
	// Decode order:  I P B B
	// Display order: I B B P
	frames := []*types.VideoFrame{
		{
			ID:          1,
			Type:        types.FrameTypeI,
			PTS:         1000,
			DTS:         1000,
			CaptureTime: now,
		},
		{
			ID:          4,
			Type:        types.FrameTypeP,
			PTS:         4000,
			DTS:         2000,
			CaptureTime: now.Add(33 * time.Millisecond),
		},
		{
			ID:          2,
			Type:        types.FrameTypeB,
			PTS:         2000,
			DTS:         3000,
			CaptureTime: now.Add(66 * time.Millisecond),
		},
		{
			ID:          3,
			Type:        types.FrameTypeB,
			PTS:         3000,
			DTS:         4000,
			CaptureTime: now.Add(99 * time.Millisecond),
		},
	}

	expectedOrder := []uint64{1, 4, 2, 3}
	var outputIDs []uint64

	for _, frame := range frames {
		output, err := r.AddFrame(frame)
		require.NoError(t, err)
		for _, f := range output {
			outputIDs = append(outputIDs, f.ID)
		}
	}

	// May need to flush remaining frames
	flushed := r.Flush()
	for _, f := range flushed {
		outputIDs = append(outputIDs, f.ID)
	}

	assert.Equal(t, expectedOrder, outputIDs)
}

func TestBFrameReorderer_ComplexGOP(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Complex GOP: I B B P B B P B B I
	// This tests multiple reference frames and longer sequences
	frames := []*types.VideoFrame{
		// First I-frame
		{ID: 1, Type: types.FrameTypeI, PTS: 0, DTS: 0, CaptureTime: now},
		// First P-frame (displayed after 2 B-frames)
		{ID: 2, Type: types.FrameTypeP, PTS: 3000, DTS: 1000, CaptureTime: now.Add(33 * time.Millisecond)},
		// B-frames between I1 and P1
		{ID: 3, Type: types.FrameTypeB, PTS: 1000, DTS: 2000, CaptureTime: now.Add(66 * time.Millisecond)},
		{ID: 4, Type: types.FrameTypeB, PTS: 2000, DTS: 3000, CaptureTime: now.Add(99 * time.Millisecond)},
		// Second P-frame
		{ID: 5, Type: types.FrameTypeP, PTS: 6000, DTS: 4000, CaptureTime: now.Add(132 * time.Millisecond)},
		// B-frames between P1 and P2
		{ID: 6, Type: types.FrameTypeB, PTS: 4000, DTS: 5000, CaptureTime: now.Add(165 * time.Millisecond)},
		{ID: 7, Type: types.FrameTypeB, PTS: 5000, DTS: 6000, CaptureTime: now.Add(198 * time.Millisecond)},
		// B-frames before next I-frame
		{ID: 8, Type: types.FrameTypeB, PTS: 7000, DTS: 7000, CaptureTime: now.Add(231 * time.Millisecond)},
		{ID: 9, Type: types.FrameTypeB, PTS: 8000, DTS: 8000, CaptureTime: now.Add(264 * time.Millisecond)},
		// Next I-frame
		{ID: 10, Type: types.FrameTypeI, PTS: 9000, DTS: 9000, CaptureTime: now.Add(297 * time.Millisecond)},
	}

	var output []*types.VideoFrame
	for _, frame := range frames {
		frames, err := r.AddFrame(frame)
		require.NoError(t, err)
		output = append(output, frames...)
	}

	// Flush remaining
	output = append(output, r.Flush()...)

	// Verify DTS order
	for i := 1; i < len(output); i++ {
		assert.GreaterOrEqual(t, output[i].DTS, output[i-1].DTS,
			"DTS must be monotonically increasing: %s (DTS=%d) < %s (DTS=%d)",
			output[i].ID, output[i].DTS, output[i-1].ID, output[i-1].DTS)
	}

	// Verify we got all frames
	assert.Equal(t, len(frames), len(output))
}

func TestBFrameReorderer_DTSBackwards(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Add several frames to trigger output
	frames := []*types.VideoFrame{
		{
			ID:          1,
			Type:        types.FrameTypeI,
			PTS:         1000,
			DTS:         1000,
			CaptureTime: now,
		},
		{
			ID:          2,
			Type:        types.FrameTypeP,
			PTS:         2000,
			DTS:         2000,
			CaptureTime: now.Add(33 * time.Millisecond),
		},
		{
			ID:          3,
			Type:        types.FrameTypeP,
			PTS:         3000,
			DTS:         3000,
			CaptureTime: now.Add(66 * time.Millisecond),
		},
	}

	// Add frames and collect output
	var output []*types.VideoFrame
	for _, frame := range frames {
		out, err := r.AddFrame(frame)
		require.NoError(t, err)
		output = append(output, out...)
	}

	// Now add a frame with backwards DTS - this should be dropped when we try to output it
	frameBackwards := &types.VideoFrame{
		ID:          4,
		Type:        types.FrameTypeP,
		PTS:         4000,
		DTS:         500, // DTS goes backwards!
		CaptureTime: now.Add(99 * time.Millisecond),
	}
	_, err := r.AddFrame(frameBackwards)
	// If frames have been output (which they likely have due to buffer pressure),
	// then lastOutputDTS will be set and this will error
	if err != nil {
		assert.Contains(t, err.Error(), "DTS went backwards")
		// Check stats show dropped frame
		stats := r.GetStats()
		assert.Equal(t, uint64(1), stats.FramesDropped)
	} else {
		// If no frames have been output yet, the frame will be added to buffer
		// but dropped when we try to output it
		_ = r.Flush()
		stats := r.GetStats()
		assert.Greater(t, stats.FramesDropped, uint64(0), "Should have dropped the frame with backwards DTS")
	}
}

func TestBFrameReorderer_NilFrame(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	_, err := r.AddFrame(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil frame")
}

func TestBFrameReorderer_AutoDTSAssignment(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Frame with PTS but no DTS
	frame := &types.VideoFrame{
		ID:          1,
		Type:        types.FrameTypeI,
		PTS:         1000,
		DTS:         0, // No DTS set
		CaptureTime: now,
	}

	output, err := r.AddFrame(frame)
	require.NoError(t, err)
	
	// When we eventually get the frame out, DTS should equal PTS
	flushed := r.Flush()
	allFrames := append(output, flushed...)
	
	require.Len(t, allFrames, 1)
	assert.Equal(t, int64(1000), allFrames[0].DTS)
}

func TestBFrameReorderer_BufferOverflow(t *testing.T) {
	r := NewBFrameReorderer(2, 200*time.Millisecond, testLogger()) // Small buffer
	now := time.Now()

	var totalOutput int
	
	// Add more frames than buffer can hold
	for i := 0; i < 5; i++ {
		frame := &types.VideoFrame{
			ID:          uint64(i + 1),
			Type:        types.FrameTypeP,
			PTS:         int64(i * 1000),
			DTS:         int64(i * 1000),
			CaptureTime: now.Add(time.Duration(i*33) * time.Millisecond),
		}
		output, err := r.AddFrame(frame)
		require.NoError(t, err)
		totalOutput += len(output)
	}

	// Should have output some frames due to buffer pressure
	assert.Greater(t, totalOutput, 0)
	
	stats := r.GetStats()
	assert.LessOrEqual(t, stats.CurrentBuffer, 2)
}

func TestBFrameReorderer_DelayTimeout(t *testing.T) {
	r := NewBFrameReorderer(5, 50*time.Millisecond, testLogger()) // Short delay
	now := time.Now()

	// Add first frame
	frame1 := &types.VideoFrame{
		ID:          1,
		Type:        types.FrameTypeI,
		PTS:         1000,
		DTS:         1000,
		CaptureTime: now,
	}
	output1, err := r.AddFrame(frame1)
	require.NoError(t, err)
	
	// Add frame with much later capture time
	frame2 := &types.VideoFrame{
		ID:          2,
		Type:        types.FrameTypeP,
		PTS:         2000,
		DTS:         2000,
		CaptureTime: now.Add(100 * time.Millisecond), // Beyond max delay
	}
	output2, err := r.AddFrame(frame2)
	require.NoError(t, err)
	
	// Should have forced output due to delay
	totalOutput := len(output1) + len(output2)
	assert.Greater(t, totalOutput, 0)
}

func TestBFrameReorderer_IFrameHandling(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Add I-frame
	iframe := &types.VideoFrame{
		ID:          1,
		Type:        types.FrameTypeI,
		PTS:         1000,
		DTS:         1000,
		CaptureTime: now,
	}
	output1, err := r.AddFrame(iframe)
	require.NoError(t, err)
	
	// Add another frame to trigger I-frame output
	pframe := &types.VideoFrame{
		ID:          2,
		Type:        types.FrameTypeP,
		PTS:         2000,
		DTS:         2000,
		CaptureTime: now.Add(33 * time.Millisecond),
	}
	output2, err := r.AddFrame(pframe)
	require.NoError(t, err)
	
	// I-frame should be output once we have the next frame
	totalOutput := append(output1, output2...)
	assert.GreaterOrEqual(t, len(totalOutput), 1)
	
	foundIFrame := false
	for _, f := range totalOutput {
		if f.ID == 1 {
			foundIFrame = true
			break
		}
	}
	assert.True(t, foundIFrame, "I-frame should be output")
}

func TestBFrameReorderer_Stats(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Initial stats
	stats := r.GetStats()
	assert.Equal(t, uint64(0), stats.FramesReordered)
	assert.Equal(t, uint64(0), stats.FramesDropped)
	assert.Equal(t, 0, stats.CurrentBuffer)
	assert.Equal(t, 0, stats.MaxBufferSize)

	// Add frames
	for i := 0; i < 5; i++ {
		frame := &types.VideoFrame{
			ID:          uint64(i + 1),
			Type:        types.FrameTypeP,
			PTS:         int64(i * 1000),
			DTS:         int64(i * 1000),
			CaptureTime: now.Add(time.Duration(i*33) * time.Millisecond),
		}
		_, err := r.AddFrame(frame)
		require.NoError(t, err)
	}

	// Check updated stats
	stats = r.GetStats()
	assert.Greater(t, stats.FramesReordered, uint64(0))
	assert.GreaterOrEqual(t, stats.MaxBufferSize, 1)
}

func TestFrameHeap(t *testing.T) {
	h := &frameHeap{}
	heap.Init(h)

	// Add frames in random DTS order
	frames := []*types.VideoFrame{
		{ID: 3, DTS: 3000},
		{ID: 1, DTS: 1000},
		{ID: 4, DTS: 4000},
		{ID: 2, DTS: 2000},
	}

	for _, f := range frames {
		heap.Push(h, f)
	}

	// Pop frames - should come out in DTS order
	expectedOrder := []uint64{1, 2, 3, 4}
	for i := 0; i < len(expectedOrder); i++ {
		frame := heap.Pop(h).(*types.VideoFrame)
		assert.Equal(t, expectedOrder[i], frame.ID)
	}
}

func TestDTSCalculator_NoBFrames(t *testing.T) {
	calc := NewDTSCalculator(30.0, types.Rational{Num: 1, Den: 90000}, 0)

	// Without B-frames, DTS should always equal PTS
	testCases := []struct {
		pts       int64
		frameType types.FrameType
	}{
		{90000, types.FrameTypeI},
		{93003, types.FrameTypeP},
		{96006, types.FrameTypeP},
		{99009, types.FrameTypeI},
	}

	for _, tc := range testCases {
		dts := calc.CalculateDTS(tc.pts, tc.frameType)
		assert.Equal(t, tc.pts, dts, "Without B-frames, DTS should equal PTS")
	}
}

func TestDTSCalculator_WithBFrames(t *testing.T) {
	calc := NewDTSCalculator(30.0, types.Rational{Num: 1, Den: 90000}, 2)

	// Frame duration at 30fps with 90kHz clock = 3000
	frameDuration := int64(3000)

	testCases := []struct {
		name      string
		pts       int64
		frameType types.FrameType
		wantDTS   int64
	}{
		{
			name:      "First I-frame",
			pts:       90000,
			frameType: types.FrameTypeI,
			wantDTS:   90000 - 2*frameDuration, // DTS = PTS - (maxBFrames * duration)
		},
		{
			name:      "First P-frame",
			pts:       93000,
			frameType: types.FrameTypeP,
			wantDTS:   93000 - 2*frameDuration,
		},
		{
			name:      "B-frame",
			pts:       91000,
			frameType: types.FrameTypeB,
			wantDTS:   90000 - 2*frameDuration + frameDuration, // Previous DTS + duration
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dts := calc.CalculateDTS(tc.pts, tc.frameType)
			// Allow some flexibility in calculation
			assert.InDelta(t, tc.wantDTS, dts, float64(frameDuration),
				"DTS calculation for %s frame", tc.frameType)
		})
	}
}

func TestDTSCalculator_FrameDuration(t *testing.T) {
	testCases := []struct {
		name          string
		frameRate     float64
		timeBase      types.Rational
		wantDuration  int64
	}{
		{
			name:         "30fps with 90kHz",
			frameRate:    30.0,
			timeBase:     types.Rational{Num: 1, Den: 90000},
			wantDuration: 3000, // 90000 / 30
		},
		{
			name:         "25fps with 90kHz",
			frameRate:    25.0,
			timeBase:     types.Rational{Num: 1, Den: 90000},
			wantDuration: 3600, // 90000 / 25
		},
		{
			name:         "60fps with 90kHz",
			frameRate:    60.0,
			timeBase:     types.Rational{Num: 1, Den: 90000},
			wantDuration: 1500, // 90000 / 60
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			calc := NewDTSCalculator(tc.frameRate, tc.timeBase, 2)
			duration := calc.calculateFrameDuration()
			assert.Equal(t, tc.wantDuration, duration)
		})
	}
}

func TestBFrameReorderer_ConcurrentAccess(t *testing.T) {
	r := NewBFrameReorderer(3, 200*time.Millisecond, testLogger())
	now := time.Now()

	// Test concurrent access doesn't cause races
	done := make(chan bool)
	
	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			frame := &types.VideoFrame{
				ID:          uint64(i + 1),
				Type:        types.FrameTypeP,
				PTS:         int64(i * 1000),
				DTS:         int64(i * 1000),
				CaptureTime: now.Add(time.Duration(i*33) * time.Millisecond),
			}
			_, _ = r.AddFrame(frame)
			time.Sleep(time.Microsecond)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 50; i++ {
			_ = r.GetStats()
			time.Sleep(time.Microsecond * 2)
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done

	// Final flush
	frames := r.Flush()
	assert.NotNil(t, frames)
}
