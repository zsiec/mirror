package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewParameterSetProber(t *testing.T) {
	streamID := "test-stream"
	maxFrames := 100
	duration := 30 * time.Second

	prober := NewParameterSetProber(streamID, maxFrames, duration)

	assert.NotNil(t, prober)
	assert.Equal(t, streamID, prober.streamID)
	assert.Equal(t, maxFrames, prober.maxFrameHistory)
	assert.Equal(t, duration, prober.historyDuration)
	assert.NotNil(t, prober.frameHistory)
	assert.Len(t, prober.frameHistory, 0)
	assert.NotNil(t, prober.recentExtractions)
	assert.Equal(t, 5*time.Minute, prober.cacheExpiry)
	assert.Equal(t, uint64(0), prober.probeAttempts)
	assert.Equal(t, uint64(0), prober.probeSuccesses)
}

func TestParameterSetProber_AddFrame(t *testing.T) {
	prober := NewParameterSetProber("test", 3, time.Minute)

	// Create test frames with H.264 SPS
	spsData := []byte{0x67, 0x42, 0x00, 0x1e} // H.264 SPS
	frame1 := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 7, Data: spsData[1:]}, // SPS without start code
		},
	}

	frame2 := &VideoFrame{
		ID:          2,
		CaptureTime: time.Now(),
		NALUnits:    []NALUnit{},
	}

	frame3 := &VideoFrame{
		ID:          3,
		CaptureTime: time.Now(),
		NALUnits:    []NALUnit{},
	}

	frame4 := &VideoFrame{
		ID:          4,
		CaptureTime: time.Now(),
		NALUnits:    []NALUnit{},
	}

	// Add frames and test history management
	prober.AddFrame(frame1)
	assert.Len(t, prober.frameHistory, 1)
	assert.Equal(t, uint64(1), prober.extractedSPS) // Should extract SPS

	prober.AddFrame(frame2)
	assert.Len(t, prober.frameHistory, 2)

	prober.AddFrame(frame3)
	assert.Len(t, prober.frameHistory, 3)

	// Adding 4th frame should remove the first (max is 3)
	prober.AddFrame(frame4)
	assert.Len(t, prober.frameHistory, 3)
	assert.Equal(t, uint64(2), prober.frameHistory[0].ID) // frame1 should be gone
}

func TestParameterSetProber_AddFrame_TimeBasedTrimming(t *testing.T) {
	prober := NewParameterSetProber("test", 100, 100*time.Millisecond)

	// Add old frame
	oldFrame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now().Add(-200 * time.Millisecond), // Older than history duration
		NALUnits:    []NALUnit{},
	}

	// Add recent frame
	recentFrame := &VideoFrame{
		ID:          2,
		CaptureTime: time.Now(),
		NALUnits:    []NALUnit{},
	}

	prober.AddFrame(oldFrame)
	prober.AddFrame(recentFrame)

	// Old frame should be trimmed
	assert.Len(t, prober.frameHistory, 1)
	assert.Equal(t, uint64(2), prober.frameHistory[0].ID)
}

func TestParameterSetProber_OpportunisticExtraction(t *testing.T) {
	tests := []struct {
		name     string
		nalType  uint8
		nalData  []byte
		cacheKey string
	}{
		{
			name:     "H.264 SPS",
			nalType:  7,
			nalData:  []byte{0x42, 0x00, 0x1e},
			cacheKey: "sps_7",
		},
		{
			name:     "H.264 PPS",
			nalType:  8,
			nalData:  []byte{0xce, 0x38, 0x80},
			cacheKey: "pps_8",
		},
		{
			name:     "HEVC VPS",
			nalType:  32,
			nalData:  []byte{0x40, 0x01, 0x0c, 0x01},
			cacheKey: "vps_32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create new prober for each test to avoid cumulative counters
			prober := NewParameterSetProber("test", 10, time.Minute)

			frame := &VideoFrame{
				ID:          1,
				CaptureTime: time.Now(),
				NALUnits: []NALUnit{
					{Type: tt.nalType, Data: tt.nalData},
				},
			}

			initialSPS := prober.extractedSPS
			initialPPS := prober.extractedPPS
			initialVPS := prober.extractedVPS

			prober.AddFrame(frame)

			// Check that the appropriate counter was incremented
			switch tt.nalType {
			case 7: // SPS
				assert.Equal(t, initialSPS+1, prober.extractedSPS)
				assert.Equal(t, initialPPS, prober.extractedPPS)
				assert.Equal(t, initialVPS, prober.extractedVPS)
			case 8: // PPS
				assert.Equal(t, initialSPS, prober.extractedSPS)
				assert.Equal(t, initialPPS+1, prober.extractedPPS)
				assert.Equal(t, initialVPS, prober.extractedVPS)
			case 32: // VPS
				assert.Equal(t, initialSPS, prober.extractedSPS)
				assert.Equal(t, initialPPS, prober.extractedPPS)
				assert.Equal(t, initialVPS+1, prober.extractedVPS)
			}

			// Check cache
			if tt.cacheKey != "" {
				_, exists := prober.recentExtractions[tt.cacheKey]
				assert.True(t, exists, "Expected parameter set in cache")
			}
		})
	}
}

func TestParameterSetProber_ProbeForParameterSets_CacheHit(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Add SPS to cache
	spsData := []byte{0x67, 0x42, 0x00, 0x1e}
	paramSet := &ParameterSet{
		Data:     spsData,
		ParsedAt: time.Now(),
		Size:     len(spsData),
		Valid:    true,
	}
	prober.recentExtractions["sps_7"] = paramSet

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
		MaxProbeFrames:    10,
	}

	result := prober.ProbeForParameterSets(request)

	assert.True(t, result.Success)
	assert.Len(t, result.ExtractedSets, 1)
	assert.NotNil(t, result.ExtractedSets["sps"])
	assert.Equal(t, uint64(0), result.ExtractionSource["sps"]) // Cache source
	assert.Equal(t, 0, result.FramesProbed)
	assert.Greater(t, result.ProbeLatency, time.Duration(0))
	assert.Equal(t, uint64(1), prober.probeAttempts)
	assert.Equal(t, uint64(1), prober.probeSuccesses)
}

func TestParameterSetProber_ProbeForParameterSets_FrameHistory(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Create frame with SPS
	spsData := []byte{0x42, 0x00, 0x1e}
	frame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 7, Data: spsData},
		},
	}

	// Add frame to history manually (bypass opportunistic extraction)
	prober.frameHistory = append(prober.frameHistory, frame)

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
		MaxProbeFrames:    5,
	}

	result := prober.ProbeForParameterSets(request)

	assert.True(t, result.Success)
	assert.Len(t, result.ExtractedSets, 1)
	assert.NotNil(t, result.ExtractedSets["sps"])
	assert.Equal(t, uint64(1), result.ExtractionSource["sps"])
	assert.Equal(t, 1, result.FramesProbed)
	assert.Equal(t, uint64(1), prober.probeAttempts)
	assert.Equal(t, uint64(1), prober.probeSuccesses)
}

func TestParameterSetProber_ProbeForParameterSets_SpecificSPSID(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Create frame with SPS
	spsData := []byte{0x42, 0x00, 0x1e}
	frame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 7, Data: spsData},
		},
	}

	prober.frameHistory = append(prober.frameHistory, frame)

	requiredSPSID := uint8(0)
	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
		RequiredSPSID:     &requiredSPSID,
		MaxProbeFrames:    5,
	}

	result := prober.ProbeForParameterSets(request)

	assert.True(t, result.Success)
	assert.Len(t, result.ExtractedSets, 1)
	paramSet := result.ExtractedSets["sps"]
	assert.NotNil(t, paramSet)
	assert.Equal(t, requiredSPSID, paramSet.ID)
}

func TestParameterSetProber_ProbeForParameterSets_Failure(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Create frame without SPS
	frame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 1, Data: []byte{0x00, 0x01, 0x02}}, // Not SPS
		},
	}

	prober.frameHistory = append(prober.frameHistory, frame)

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
		MaxProbeFrames:    5,
	}

	result := prober.ProbeForParameterSets(request)

	assert.False(t, result.Success)
	assert.Len(t, result.ExtractedSets, 0)
	assert.Equal(t, 1, result.FramesProbed)
	assert.Contains(t, result.FailureReason, "could not find parameter sets")
	assert.Equal(t, uint64(1), prober.probeAttempts)
	assert.Equal(t, uint64(0), prober.probeSuccesses)
}

func TestParameterSetProber_ProbeForParameterSets_MaxFrameLimit(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Add multiple frames
	for i := 1; i <= 5; i++ {
		frame := &VideoFrame{
			ID:          uint64(i),
			CaptureTime: time.Now(),
			NALUnits:    []NALUnit{},
		}
		prober.frameHistory = append(prober.frameHistory, frame)
	}

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
		MaxProbeFrames:    3, // Limit to 3 frames
	}

	result := prober.ProbeForParameterSets(request)

	assert.False(t, result.Success)
	assert.Equal(t, 3, result.FramesProbed) // Should only probe 3 frames
}

func TestParameterSetProber_CheckRecentExtractions(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Add parameter set to cache
	paramSet := &ParameterSet{
		Data:     []byte{0x67, 0x42, 0x00, 0x1e},
		ParsedAt: time.Now(),
		Valid:    true,
	}
	prober.recentExtractions["sps"] = paramSet

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
	}
	result := &ProbeResult{
		ExtractedSets:    make(map[string]*ParameterSet),
		ExtractionSource: make(map[string]uint64),
	}

	found := prober.checkRecentExtractions(request, result)

	assert.True(t, found)
	assert.Len(t, result.ExtractedSets, 1)
	assert.NotNil(t, result.ExtractedSets["sps"])
}

func TestParameterSetProber_CheckRecentExtractions_Expired(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)
	prober.cacheExpiry = 100 * time.Millisecond

	// Add expired parameter set
	paramSet := &ParameterSet{
		Data:     []byte{0x67, 0x42, 0x00, 0x1e},
		ParsedAt: time.Now().Add(-200 * time.Millisecond), // Expired
		Valid:    true,
	}
	prober.recentExtractions["sps"] = paramSet

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
	}
	result := &ProbeResult{
		ExtractedSets:    make(map[string]*ParameterSet),
		ExtractionSource: make(map[string]uint64),
	}

	found := prober.checkRecentExtractions(request, result)

	assert.False(t, found)
	assert.Len(t, result.ExtractedSets, 0)
}

func TestParameterSetProber_ExtractParameterSetsFromFrame(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	frame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 7, Data: []byte{0x42, 0x00, 0x1e}}, // H.264 SPS
			{Type: 8, Data: []byte{0xce, 0x38, 0x80}}, // H.264 PPS
			{Type: 1, Data: []byte{0x00, 0x01, 0x02}}, // Non-parameter set
		},
	}

	request := &ProbeRequest{
		MissingParameters: []string{"sps", "pps"},
	}

	extracted := prober.extractParameterSetsFromFrame(frame, request)

	assert.Len(t, extracted, 2)
	assert.NotNil(t, extracted["sps"])
	assert.NotNil(t, extracted["pps"])

	// Check SPS data includes start code
	spsParam := extracted["sps"]
	assert.Equal(t, byte(0x67), spsParam.Data[0])

	// Check PPS data includes start code
	ppsParam := extracted["pps"]
	assert.Equal(t, byte(0x68), ppsParam.Data[0])
}

func TestParameterSetProber_HelperFunctions(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Test isInList
	list := []string{"sps", "pps", "vps"}
	assert.True(t, prober.isInList("sps", list))
	assert.True(t, prober.isInList("pps", list))
	assert.False(t, prober.isInList("unknown", list))

	// Test hasAllRequiredParameters
	request := &ProbeRequest{
		MissingParameters: []string{"sps", "pps"},
	}

	extracted := map[string]*ParameterSet{
		"sps": {Data: []byte{0x67}, Valid: true},
	}
	assert.False(t, prober.hasAllRequiredParameters(request, extracted))

	extracted["pps"] = &ParameterSet{Data: []byte{0x68}, Valid: true}
	assert.True(t, prober.hasAllRequiredParameters(request, extracted))

	// Test findMissingParameters
	partialExtracted := map[string]*ParameterSet{
		"sps": {Data: []byte{0x67}, Valid: true},
	}
	missing := prober.findMissingParameters(request, partialExtracted)
	assert.Len(t, missing, 1)
	assert.Contains(t, missing, "pps")
}

func TestParameterSetProber_ExtractSPSID(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Test with empty data
	id := prober.extractSPSID([]byte{})
	assert.Equal(t, uint8(0), id)

	// Test with short data
	id = prober.extractSPSID([]byte{0x01, 0x02})
	assert.Equal(t, uint8(0), id)

	// Test with valid data (simplified - always returns 0)
	id = prober.extractSPSID([]byte{0x42, 0x00, 0x1e, 0x3c})
	assert.Equal(t, uint8(0), id)
}

func TestParameterSetProber_GetStatistics(t *testing.T) {
	prober := NewParameterSetProber("test-stream", 10, time.Minute)

	// Add some test data
	prober.probeAttempts = 5
	prober.probeSuccesses = 3
	prober.extractedSPS = 2
	prober.extractedPPS = 1
	prober.extractedVPS = 1

	frame := &VideoFrame{ID: 1, CaptureTime: time.Now()}
	prober.frameHistory = append(prober.frameHistory, frame)

	prober.recentExtractions["test"] = &ParameterSet{Data: []byte{0x67}}

	stats := prober.GetStatistics()

	assert.Equal(t, "test-stream", stats["stream_id"])
	assert.Equal(t, 1, stats["frame_history_size"])
	assert.Equal(t, 10, stats["max_frame_history"])
	assert.Equal(t, uint64(5), stats["probe_attempts"])
	assert.Equal(t, uint64(3), stats["probe_successes"])
	assert.Equal(t, 0.6, stats["success_rate"])
	assert.Equal(t, uint64(2), stats["extracted_sps"])
	assert.Equal(t, uint64(1), stats["extracted_pps"])
	assert.Equal(t, uint64(1), stats["extracted_vps"])
	assert.Equal(t, 1, stats["cached_extractions"])
}

func TestParameterSetProber_GetStatistics_ZeroAttempts(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	stats := prober.GetStatistics()

	assert.Equal(t, float64(0), stats["success_rate"])
}

func TestParameterSetProber_ConcurrentAccess(t *testing.T) {
	prober := NewParameterSetProber("test", 100, time.Minute)

	// Test concurrent AddFrame and ProbeForParameterSets
	frame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 7, Data: []byte{0x42, 0x00, 0x1e}},
		},
	}

	request := &ProbeRequest{
		MissingParameters: []string{"sps"},
		MaxProbeFrames:    10,
	}

	// Run operations concurrently
	done := make(chan bool)
	go func() {
		for i := 0; i < 50; i++ {
			prober.AddFrame(frame)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 50; i++ {
			_ = prober.ProbeForParameterSets(request)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 50; i++ {
			_ = prober.GetStatistics()
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify final state is consistent
	stats := prober.GetStatistics()
	require.NotNil(t, stats)
	assert.GreaterOrEqual(t, stats["probe_attempts"].(uint64), uint64(50))
}

func TestParameterSetProber_MultipleParameterTypes(t *testing.T) {
	prober := NewParameterSetProber("test", 10, time.Minute)

	// Create frame with multiple parameter sets
	frame := &VideoFrame{
		ID:          1,
		CaptureTime: time.Now(),
		NALUnits: []NALUnit{
			{Type: 7, Data: []byte{0x42, 0x00, 0x1e}},  // H.264 SPS
			{Type: 8, Data: []byte{0xce, 0x38, 0x80}},  // H.264 PPS
			{Type: 32, Data: []byte{0x40, 0x01, 0x0c}}, // HEVC VPS
		},
	}

	prober.frameHistory = append(prober.frameHistory, frame)

	request := &ProbeRequest{
		MissingParameters: []string{"sps", "pps", "vps"},
		MaxProbeFrames:    5,
	}

	result := prober.ProbeForParameterSets(request)

	assert.True(t, result.Success)
	assert.Len(t, result.ExtractedSets, 3)
	assert.NotNil(t, result.ExtractedSets["sps"])
	assert.NotNil(t, result.ExtractedSets["pps"])
	assert.NotNil(t, result.ExtractedSets["vps"])
	assert.Equal(t, 1, result.FramesProbed)
}
