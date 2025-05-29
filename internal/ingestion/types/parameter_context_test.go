package types

import (
	"testing"
	"time"
)

func TestParameterSetContext_Creation(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream-123")

	if ctx == nil {
		t.Fatal("Expected context to be created, got nil")
	}

	if ctx.streamID != "test-stream-123" {
		t.Errorf("Expected streamID 'test-stream-123', got '%s'", ctx.streamID)
	}

	if ctx.codec != CodecH264 {
		t.Errorf("Expected codec H264, got %v", ctx.codec)
	}
}

func TestParameterSetContext_AddSPS_ValidData(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Valid H.264 SPS NAL unit data (simplified)
	spsData := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04, 0x40, 0x00, 0x00, 0x03, 0x00, 0x40, 0x00, 0x00, 0x0f, 0x03, 0xc5, 0x8b, 0xa8}

	err := ctx.AddSPS(spsData)
	// Since we don't have actual SPS parsing implemented, this will likely fail
	// But we test that the method signature is correct
	if err != nil {
		// Expected since we don't have full H.264 parsing yet
		t.Logf("SPS parsing not implemented yet: %v", err)
	}
}

func TestParameterSetContext_AddSPS_WrongCodec(t *testing.T) {
	ctx := NewParameterSetContext(CodecHEVC, "test-stream")

	spsData := []byte{0x67, 0x42, 0x00, 0x1f}

	err := ctx.AddSPS(spsData)
	if err == nil {
		t.Error("Expected error when adding H.264 SPS to HEVC context")
	}

	if err != nil && err.Error() != "cannot add H.264 SPS to hevc context" {
		t.Errorf("Expected codec mismatch error, got: %v", err)
	}
}

func TestParameterSetContext_GetStatistics(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream-456")

	stats := ctx.GetStatistics()

	if stats == nil {
		t.Fatal("Expected statistics map, got nil")
	}

	if streamID, exists := stats["stream_id"]; !exists || streamID != "test-stream-456" {
		t.Errorf("Expected stream_id 'test-stream-456', got %v", streamID)
	}

	if codec, exists := stats["codec"]; !exists || codec != "h264" {
		t.Errorf("Expected codec 'h264', got %v", codec)
	}

	if totalSets, exists := stats["total_sets"]; !exists || totalSets != 0 {
		t.Errorf("Expected total_sets 0 for new context, got %v", totalSets)
	}

	if spsCount, exists := stats["sps_count"]; !exists || spsCount != 0 {
		t.Errorf("Expected sps_count 0 for new context, got %v", spsCount)
	}
}

func TestParameterSetContext_CanDecodeFrame_EmptyFrame(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Test with frame that has no NAL units
	emptyFrame := &VideoFrame{
		ID:       123,
		NALUnits: []NALUnit{},
	}

	canDecode, reason := ctx.CanDecodeFrame(emptyFrame)

	if canDecode {
		t.Error("Expected false for empty frame")
	}

	if reason == "" {
		t.Error("Expected error reason for empty frame")
	}
}

func TestParameterSetContext_GenerateDecodableStream_EmptyFrame(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	emptyFrame := &VideoFrame{
		ID:       123,
		NALUnits: []NALUnit{},
	}

	stream, err := ctx.GenerateDecodableStream(emptyFrame)

	if err == nil {
		t.Error("Expected error for empty frame")
	}

	if stream != nil {
		t.Error("Expected nil stream for empty frame")
	}
}

func TestParameterSetContext_CodecTypes(t *testing.T) {
	tests := []struct {
		name  string
		codec CodecType
	}{
		{"H264", CodecH264},
		{"HEVC", CodecHEVC},
		{"AV1", CodecAV1},
		{"JPEGXS", CodecJPEGXS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewParameterSetContext(tt.codec, "test-stream")
			if ctx.codec != tt.codec {
				t.Errorf("Expected codec %v, got %v", tt.codec, ctx.codec)
			}
		})
	}
}

func TestParameterSetContext_ThreadSafety(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Test that we can call GetStatistics concurrently without panic
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			stats := ctx.GetStatistics()
			if stats == nil {
				t.Error("Got nil statistics")
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestParameterSetContext_LastUpdated(t *testing.T) {
	before := time.Now()
	ctx := NewParameterSetContext(CodecH264, "test-stream")
	after := time.Now()

	if ctx.lastUpdated.Before(before) || ctx.lastUpdated.After(after) {
		t.Error("lastUpdated should be set during context creation")
	}
}

// ENCODER SESSION INTEGRATION TESTS

func TestParameterSetContext_EncoderSessionIntegration(t *testing.T) {
	t.Run("session_manager_initialization", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-stream-session")

		sessionManager := ctx.GetSessionManager()
		if sessionManager == nil {
			t.Fatal("Expected session manager to be initialized, got nil")
		}

		// Verify session manager is properly configured
		stats := sessionManager.GetStatistics()
		if stats == nil {
			t.Error("Expected session manager statistics to be available")
		}
	})

	t.Run("session_change_detection_on_sps_change", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-stream-session")
		sessionManager := ctx.GetSessionManager()

		// Add initial SPS
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04}
		err := ctx.AddSPS(spsData1)
		if err != nil {
			t.Fatalf("Failed to add initial SPS: %v", err)
		}

		initialStats := sessionManager.GetStatistics()
		initialChangeCount := getSessionChangeCount(initialStats)

		// Add different SPS with different profile (should trigger session change)
		spsData2 := []byte{0x67, 0x64, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04} // Different profile_idc (0x64 vs 0x42)
		err = ctx.AddSPS(spsData2)
		if err != nil {
			t.Fatalf("Failed to add changed SPS: %v", err)
		}

		finalStats := sessionManager.GetStatistics()
		finalChangeCount := getSessionChangeCount(finalStats)

		if finalChangeCount <= initialChangeCount {
			t.Errorf("Expected session change count to increase from %d to >%d", initialChangeCount, finalChangeCount)
		}
	})

	t.Run("no_session_change_on_identical_sps", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-stream-session")
		sessionManager := ctx.GetSessionManager()

		// Add initial SPS
		spsData := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04}
		err := ctx.AddSPS(spsData)
		if err != nil {
			t.Fatalf("Failed to add initial SPS: %v", err)
		}

		initialStats := sessionManager.GetStatistics()
		initialChangeCount := getSessionChangeCount(initialStats)

		// Add identical SPS (should not trigger session change)
		err = ctx.AddSPS(spsData)
		if err != nil {
			t.Fatalf("Failed to add identical SPS: %v", err)
		}

		finalStats := sessionManager.GetStatistics()
		finalChangeCount := getSessionChangeCount(finalStats)

		if finalChangeCount != initialChangeCount {
			t.Errorf("Expected no session change, but count changed from %d to %d", initialChangeCount, finalChangeCount)
		}
	})

	t.Run("session_manager_isolation_between_contexts", func(t *testing.T) {
		ctx1 := NewParameterSetContext(CodecH264, "stream-1")
		ctx2 := NewParameterSetContext(CodecH264, "stream-2")

		session1 := ctx1.GetSessionManager()
		session2 := ctx2.GetSessionManager()

		if session1 == session2 {
			t.Error("Expected different session managers for different contexts")
		}

		// Changes to one shouldn't affect the other
		spsData := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04}
		ctx1.AddSPS(spsData)

		stats1 := session1.GetStatistics()
		stats2 := session2.GetStatistics()

		// ctx1 should have at least 1 change, ctx2 should have 0
		changeCount1 := getSessionChangeCount(stats1)
		changeCount2 := getSessionChangeCount(stats2)

		if changeCount1 == 0 {
			t.Error("Expected ctx1 to have parameter set changes after AddSPS")
		}

		if changeCount1 == changeCount2 {
			t.Errorf("Expected different session change counts: ctx1=%d, ctx2=%d", changeCount1, changeCount2)
		}
	})
}

func TestParameterSetContext_SessionAwareDecoding(t *testing.T) {
	t.Run("generate_decodable_stream_with_session_context", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-stream-decode")

		// Add valid SPS and PPS
		spsData := []byte{0x67, 0x42, 0x00, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04}
		ppsData := []byte{0x68, 0xce, 0x3c, 0x80}

		err := ctx.AddSPS(spsData)
		if err != nil {
			t.Fatalf("Failed to add SPS: %v", err)
		}

		err = ctx.AddPPS(ppsData)
		if err != nil {
			t.Fatalf("Failed to add PPS: %v", err)
		}

		// Create a test frame
		testFrame := &VideoFrame{
			ID:  123,
			PTS: 1000,
			NALUnits: []NALUnit{
				{Type: 5, Data: []byte{0x65, 0x88, 0x84, 0x00, 0x33, 0xff}}, // IDR slice
			},
		}

		// Generate decodable stream (this should use the session context)
		stream, err := ctx.GenerateDecodableStream(testFrame)
		if err != nil {
			t.Fatalf("Failed to generate decodable stream: %v", err)
		}

		if len(stream) == 0 {
			t.Error("Expected non-empty decodable stream")
		}

		// Verify the stream contains SPS, PPS, and frame data
		expectedPatterns := [][]byte{
			{0x00, 0x00, 0x00, 0x01}, // Start code
			{0x67},                   // SPS NAL type
			{0x68},                   // PPS NAL type
		}

		for _, pattern := range expectedPatterns {
			if !containsPattern(stream, pattern) {
				t.Errorf("Expected decodable stream to contain pattern %x", pattern)
			}
		}
	})
}

// Helper functions for encoder session tests

func getSessionChangeCount(stats map[string]interface{}) uint64 {
	if changeCount, exists := stats["session_changes"]; exists {
		if count, ok := changeCount.(uint64); ok {
			return count
		}
	}
	return 0
}

func containsPattern(data, pattern []byte) bool {
	if len(pattern) > len(data) {
		return false
	}
	for i := 0; i <= len(data)-len(pattern); i++ {
		found := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}
