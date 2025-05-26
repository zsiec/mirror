package types

import (
	"testing"
	"time"
)

func TestEncoderSessionManager_Creation(t *testing.T) {
	config := CacheConfig{
		MaxParameterSets: 50,
		ParameterSetTTL:  2 * time.Minute,
		MaxSessions:      5,
	}

	esm := NewEncoderSessionManager("test-stream-session", config)

	if esm == nil {
		t.Fatal("Expected encoder session manager to be created, got nil")
	}

	if esm.streamID != "test-stream-session" {
		t.Errorf("Expected streamID 'test-stream-session', got '%s'", esm.streamID)
	}

	if esm.maxSessionHistory != 5 {
		t.Errorf("Expected maxSessionHistory 5, got %d", esm.maxSessionHistory)
	}

	// Verify initial statistics
	stats := esm.GetStatistics()
	if stats == nil {
		t.Error("Expected statistics to be available")
	}
}

func TestEncoderSessionManager_OnParameterSetChange(t *testing.T) {
	t.Run("tracks_parameter_set_changes", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 10, ParameterSetTTL: time.Minute, MaxSessions: 3}
		esm := NewEncoderSessionManager("test-stream", config)

		initialStats := esm.GetStatistics()
		initialChangeCount := getSessionChangeCountFromStats(initialStats)

		// Notify of parameter set change
		spsData := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01}
		esm.OnParameterSetChange("sps", 0, spsData)

		finalStats := esm.GetStatistics()
		finalChangeCount := getSessionChangeCountFromStats(finalStats)

		if finalChangeCount <= initialChangeCount {
			t.Errorf("Expected session change count to increase from %d to >%d", initialChangeCount, finalChangeCount)
		}
	})

	t.Run("handles_multiple_parameter_types", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 10, ParameterSetTTL: time.Minute, MaxSessions: 3}
		esm := NewEncoderSessionManager("test-stream", config)

		initialStats := esm.GetStatistics()
		initialChangeCount := getSessionChangeCountFromStats(initialStats)

		// Add SPS and PPS changes
		esm.OnParameterSetChange("sps", 0, []byte{0x67, 0x42, 0x00, 0x1f})
		esm.OnParameterSetChange("pps", 0, []byte{0x68, 0xce, 0x3c, 0x80})

		finalStats := esm.GetStatistics()
		finalChangeCount := getSessionChangeCountFromStats(finalStats)

		if finalChangeCount != initialChangeCount+2 {
			t.Errorf("Expected session change count to increase by 2, got %d->%d", initialChangeCount, finalChangeCount)
		}
	})

	t.Run("concurrent_parameter_set_changes", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 20, ParameterSetTTL: time.Minute, MaxSessions: 5}
		esm := NewEncoderSessionManager("test-concurrent", config)

		// Test concurrent access
		done := make(chan bool, 10)

		for i := 0; i < 10; i++ {
			go func(id int) {
				spsData := []byte{0x67, 0x42, byte(id), 0x1f}
				esm.OnParameterSetChange("sps", uint8(id), spsData)
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		stats := esm.GetStatistics()
		changeCount := getSessionChangeCountFromStats(stats)

		if changeCount != 10 {
			t.Errorf("Expected 10 session changes, got %d", changeCount)
		}
	})
}

func TestEncoderSessionManager_ProcessFrame(t *testing.T) {
	t.Run("processes_frame_with_parameter_sets", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 10, ParameterSetTTL: time.Minute, MaxSessions: 3}
		esm := NewEncoderSessionManager("test-process", config)

		// Create test frame
		frame := &VideoFrame{
			ID:       123,
			PTS:      1000,
			Type:     FrameTypeI,
			NALUnits: []NALUnit{{Type: 5, Data: []byte{0x65, 0x88, 0x84}}},
		}

		// Create parameter sets
		paramSets := map[string]*ParameterSet{
			"sps": {ID: 0, Valid: true, Data: []byte{0x67, 0x42, 0x00, 0x1f}},
			"pps": {ID: 0, Valid: true, Data: []byte{0x68, 0xce, 0x3c, 0x80}},
		}

		initialStats := esm.GetStatistics()
		initialFrameCount := getTotalFramesFromStats(initialStats)

		// Process frame
		analysis, err := esm.ProcessFrame(frame, paramSets)
		if err != nil {
			t.Fatalf("Failed to process frame: %v", err)
		}

		if analysis == nil {
			t.Error("Expected context analysis to be returned")
		}

		finalStats := esm.GetStatistics()
		finalFrameCount := getTotalFramesFromStats(finalStats)

		if finalFrameCount != initialFrameCount+1 {
			t.Errorf("Expected frame count to increase by 1, got %d->%d", initialFrameCount, finalFrameCount)
		}
	})

	t.Run("detects_session_changes", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 10, ParameterSetTTL: time.Minute, MaxSessions: 3}
		esm := NewEncoderSessionManager("test-session-detect", config)

		frame1 := &VideoFrame{ID: 1, PTS: 1000, Type: FrameTypeI}
		frame2 := &VideoFrame{ID: 2, PTS: 2000, Type: FrameTypeI}

		// First set of parameter sets
		paramSets1 := map[string]*ParameterSet{
			"sps": {ID: 0, Valid: true, Data: []byte{0x67, 0x42, 0x00, 0x1f}},
		}

		// Different set of parameter sets (different profile)
		paramSets2 := map[string]*ParameterSet{
			"sps": {ID: 0, Valid: true, Data: []byte{0x67, 0x64, 0x00, 0x1f}}, // Different profile_idc
		}

		// Process first frame
		_, err := esm.ProcessFrame(frame1, paramSets1)
		if err != nil {
			t.Fatalf("Failed to process first frame: %v", err)
		}

		initialSessionCount := getSessionCountFromStats(esm.GetStatistics())

		// Process second frame with different parameters
		_, err = esm.ProcessFrame(frame2, paramSets2)
		if err != nil {
			t.Fatalf("Failed to process second frame: %v", err)
		}

		finalSessionCount := getSessionCountFromStats(esm.GetStatistics())

		if finalSessionCount <= initialSessionCount {
			t.Errorf("Expected session count to increase from %d to >%d", initialSessionCount, finalSessionCount)
		}
	})
}

func TestEncoderSessionManager_Statistics(t *testing.T) {
	t.Run("provides_comprehensive_statistics", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 10, ParameterSetTTL: time.Minute, MaxSessions: 3}
		esm := NewEncoderSessionManager("test-stats", config)

		stats := esm.GetStatistics()

		// Verify expected statistics fields
		expectedFields := []string{"total_frames", "decodable_frames", "session_changes", "context_switches"}
		for _, field := range expectedFields {
			if _, exists := stats[field]; !exists {
				t.Errorf("Expected statistics field '%s' to exist", field)
			}
		}

		// Add some data and verify stats update
		frame := &VideoFrame{ID: 1, PTS: 1000, Type: FrameTypeI}
		paramSets := map[string]*ParameterSet{
			"sps": {ID: 0, Valid: true, Data: []byte{0x67, 0x42, 0x00, 0x1f}},
		}

		esm.ProcessFrame(frame, paramSets)
		esm.OnParameterSetChange("sps", 0, []byte{0x67, 0x42, 0x00, 0x1f})

		updatedStats := esm.GetStatistics()

		if getTotalFramesFromStats(updatedStats) == 0 {
			t.Error("Expected total_frames to be > 0 after processing frame")
		}

		if getSessionChangeCountFromStats(updatedStats) == 0 {
			t.Error("Expected session_changes to be > 0 after parameter set change")
		}
	})
}

func TestEncoderSessionManager_Close(t *testing.T) {
	t.Run("cleans_up_resources", func(t *testing.T) {
		config := CacheConfig{MaxParameterSets: 10, ParameterSetTTL: time.Minute, MaxSessions: 3}
		esm := NewEncoderSessionManager("test-close", config)

		// Add some data
		frame := &VideoFrame{ID: 1, PTS: 1000, Type: FrameTypeI}
		paramSets := map[string]*ParameterSet{
			"sps": {ID: 0, Valid: true, Data: []byte{0x67, 0x42, 0x00, 0x1f}},
		}
		esm.ProcessFrame(frame, paramSets)

		// Close should not panic
		esm.Close()

		// Verify we can still get stats after close (though they may be empty)
		stats := esm.GetStatistics()
		if stats == nil {
			t.Error("Expected statistics to still be available after close")
		}
	})
}

// INTEGRATION TESTS WITH PARAMETER CONTEXT

func TestEncoderSessionManager_Integration(t *testing.T) {
	t.Run("integration_with_parameter_context", func(t *testing.T) {
		// Create parameter context with encoder session manager
		ctx := NewParameterSetContext(CodecH264, "test-integration")
		sessionManager := ctx.GetSessionManager()

		if sessionManager == nil {
			t.Fatal("Expected session manager to be available from parameter context")
		}

		// Add parameter sets and verify session tracking
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16}
		err := ctx.AddSPS(spsData1)
		if err != nil {
			t.Fatalf("Failed to add SPS: %v", err)
		}

		initialStats := sessionManager.GetStatistics()
		initialChangeCount := getSessionChangeCountFromStats(initialStats)

		// Add different SPS (should trigger session change detection)
		spsData2 := []byte{0x67, 0x64, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16} // Different profile
		err = ctx.AddSPS(spsData2)
		if err != nil {
			t.Fatalf("Failed to add different SPS: %v", err)
		}

		finalStats := sessionManager.GetStatistics()
		finalChangeCount := getSessionChangeCountFromStats(finalStats)

		if finalChangeCount <= initialChangeCount {
			t.Errorf("Expected encoder session to detect parameter set change: %d -> %d",
				initialChangeCount, finalChangeCount)
		}
	})

	t.Run("session_persistence_across_parameter_updates", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-persistence")
		sessionManager := ctx.GetSessionManager()

		// Add multiple parameter sets over time
		parameterSets := [][]byte{
			{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16}, // Profile 66
			{0x67, 0x4d, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16}, // Profile 77
			{0x67, 0x64, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16}, // Profile 100
		}

		sessionCounts := make([]uint64, len(parameterSets))

		for i, spsData := range parameterSets {
			err := ctx.AddSPS(spsData)
			if err != nil {
				t.Fatalf("Failed to add SPS %d: %v", i, err)
			}

			stats := sessionManager.GetStatistics()
			sessionCounts[i] = getSessionChangeCountFromStats(stats)

			// Small delay to ensure different timestamps
			time.Sleep(1 * time.Millisecond)
		}

		// Verify session change count increased with each different parameter set
		for i := 1; i < len(sessionCounts); i++ {
			if sessionCounts[i] <= sessionCounts[i-1] {
				t.Errorf("Expected session change count to increase at step %d: %v", i, sessionCounts)
			}
		}
	})
}

// Helper functions for encoder session tests

func getSessionChangeCountFromStats(stats map[string]interface{}) uint64 {
	if changeCount, exists := stats["session_changes"]; exists {
		if count, ok := changeCount.(uint64); ok {
			return count
		}
	}
	return 0
}

func getTotalFramesFromStats(stats map[string]interface{}) uint64 {
	if frameCount, exists := stats["total_frames"]; exists {
		if count, ok := frameCount.(uint64); ok {
			return count
		}
	}
	return 0
}

func getSessionCountFromStats(stats map[string]interface{}) int {
	// This would need to be implemented based on the actual statistics structure
	// For now, we use session_changes as a proxy
	return int(getSessionChangeCountFromStats(stats))
}
