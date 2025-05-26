package types

import (
	"testing"
)

func TestParameterSetCopying(t *testing.T) {
	// Create source context with parameter sets
	sourceCtx := NewParameterSetContext(CodecH264, "source-stream")

	// Add test SPS
	spsData := []byte{0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05}
	err := sourceCtx.AddSPS(spsData)
	if err != nil {
		t.Fatalf("Failed to add SPS to source context: %v", err)
	}

	// Add test PPS
	ppsData := []byte{0x68, 0xCE, 0x3C, 0x80}
	err = sourceCtx.AddPPS(ppsData)
	if err != nil {
		t.Fatalf("Failed to add PPS to source context: %v", err)
	}

	// Create destination context
	destCtx := NewParameterSetContext(CodecH264, "dest-stream")

	// Verify destination is empty
	destStats := destCtx.GetStatistics()
	if destStats["sps_count"].(int) != 0 || destStats["pps_count"].(int) != 0 {
		t.Errorf("Destination context should be empty initially")
	}

	// Copy parameter sets
	copiedCount := destCtx.CopyParameterSetsFrom(sourceCtx)

	// Verify copy results
	if copiedCount != 2 {
		t.Errorf("Expected 2 parameter sets copied, got %d", copiedCount)
	}

	// Verify destination has parameter sets
	destStatsAfter := destCtx.GetStatistics()
	if destStatsAfter["sps_count"].(int) != 1 || destStatsAfter["pps_count"].(int) != 1 {
		t.Errorf("Expected 1 SPS and 1 PPS in destination, got %d SPS and %d PPS",
			destStatsAfter["sps_count"], destStatsAfter["pps_count"])
	}

	// Verify data integrity by getting raw parameter sets
	allSets := destCtx.GetAllParameterSets()

	if spsBytes, exists := allSets["sps"][0]; exists {
		if len(spsBytes) != len(spsData) {
			t.Errorf("SPS data length mismatch: expected %d, got %d", len(spsData), len(spsBytes))
		}
		for i, b := range spsData {
			if spsBytes[i] != b {
				t.Errorf("SPS data mismatch at byte %d: expected %02x, got %02x", i, b, spsBytes[i])
			}
		}
	} else {
		t.Errorf("SPS not found in copied parameter sets")
	}

	if ppsBytes, exists := allSets["pps"][0]; exists {
		if len(ppsBytes) != len(ppsData) {
			t.Errorf("PPS data length mismatch: expected %d, got %d", len(ppsData), len(ppsBytes))
		}
		for i, b := range ppsData {
			if ppsBytes[i] != b {
				t.Errorf("PPS data mismatch at byte %d: expected %02x, got %02x", i, b, ppsBytes[i])
			}
		}
	} else {
		t.Errorf("PPS not found in copied parameter sets")
	}
}

func TestParameterSetCopyingWithNilSource(t *testing.T) {
	destCtx := NewParameterSetContext(CodecH264, "dest-stream")

	// Try to copy from nil source
	copiedCount := destCtx.CopyParameterSetsFrom(nil)

	if copiedCount != 0 {
		t.Errorf("Expected 0 parameter sets copied from nil source, got %d", copiedCount)
	}
}

func TestParameterSetCopyingEmptySource(t *testing.T) {
	sourceCtx := NewParameterSetContext(CodecH264, "source-stream")
	destCtx := NewParameterSetContext(CodecH264, "dest-stream")

	// Copy from empty source
	copiedCount := destCtx.CopyParameterSetsFrom(sourceCtx)

	if copiedCount != 0 {
		t.Errorf("Expected 0 parameter sets copied from empty source, got %d", copiedCount)
	}
}

func TestParameterSetDataRetrieval(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Add test SPS
	spsData := []byte{0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05}
	err := ctx.AddSPS(spsData)
	if err != nil {
		t.Fatalf("Failed to add SPS: %v", err)
	}

	// Retrieve SPS data
	retrievedData, found := ctx.GetParameterSetData("sps", 0)
	if !found {
		t.Errorf("SPS 0 should be found")
	}

	if len(retrievedData) != len(spsData) {
		t.Errorf("Retrieved SPS data length mismatch: expected %d, got %d", len(spsData), len(retrievedData))
	}

	// Verify data integrity
	for i, b := range spsData {
		if retrievedData[i] != b {
			t.Errorf("SPS data mismatch at byte %d: expected %02x, got %02x", i, b, retrievedData[i])
		}
	}

	// Test non-existent parameter set
	_, found = ctx.GetParameterSetData("sps", 1)
	if found {
		t.Errorf("SPS 1 should not be found")
	}

	// Test invalid parameter type
	_, found = ctx.GetParameterSetData("invalid", 0)
	if found {
		t.Errorf("Invalid parameter type should not be found")
	}
}

func TestParameterSetCopyingWithInvalidData(t *testing.T) {
	sourceCtx := NewParameterSetContext(CodecH264, "source-stream")

	// Try to add invalid SPS (too short - only NAL header)
	invalidSPS := []byte{0x67}
	err := sourceCtx.AddSPS(invalidSPS)

	// Check if the SPS was marked as invalid
	sourceStats := sourceCtx.GetStatistics()

	// The parser might accept short SPS but mark it as invalid
	// Check if it was either rejected or marked invalid
	if err != nil {
		// SPS was rejected - verify source is empty
		if sourceStats["sps_count"].(int) != 0 {
			t.Errorf("Source should have 0 SPS after rejected add")
		}
	} else {
		// SPS was accepted but should be marked invalid
		// Check if valid SPS count is 0
		if sourceStats["valid_sps_count"].(int) != 0 {
			t.Errorf("Source should have 0 valid SPS after invalid add")
		}
	}

	destCtx := NewParameterSetContext(CodecH264, "dest-stream")

	// Copy should only copy valid parameter sets
	copiedCount := destCtx.CopyParameterSetsFrom(sourceCtx)
	if copiedCount != 0 {
		t.Errorf("Expected 0 parameter sets copied (only valid sets are copied), got %d", copiedCount)
	}
}

func TestParameterSetCopyingConcurrency(t *testing.T) {
	sourceCtx := NewParameterSetContext(CodecH264, "source-stream")

	// Add test parameter sets
	spsData := []byte{0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05}
	ppsData := []byte{0x68, 0xCE, 0x3C, 0x80}

	err := sourceCtx.AddSPS(spsData)
	if err != nil {
		t.Fatalf("Failed to add SPS: %v", err)
	}

	err = sourceCtx.AddPPS(ppsData)
	if err != nil {
		t.Fatalf("Failed to add PPS: %v", err)
	}

	destCtx := NewParameterSetContext(CodecH264, "dest-stream")

	// Test concurrent copying (this should be thread-safe)
	done := make(chan bool, 2)

	go func() {
		copiedCount := destCtx.CopyParameterSetsFrom(sourceCtx)
		if copiedCount != 2 {
			t.Errorf("Goroutine 1: Expected 2 parameter sets copied, got %d", copiedCount)
		}
		done <- true
	}()

	go func() {
		// Also test concurrent reads from source
		stats := sourceCtx.GetStatistics()
		if stats["sps_count"].(int) != 1 || stats["pps_count"].(int) != 1 {
			t.Errorf("Goroutine 2: Source stats incorrect during concurrent access")
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// Verify final state
	destStats := destCtx.GetStatistics()
	if destStats["sps_count"].(int) != 1 || destStats["pps_count"].(int) != 1 {
		t.Errorf("Final destination stats incorrect after concurrent operations")
	}
}

func TestGetAllParameterSets(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-stream")

	// Add multiple parameter sets
	spsData1 := []byte{0x67, 0x42, 0x00, 0x1E, 0x8D, 0x84, 0x04, 0x05}
	spsData2 := []byte{0x67, 0x42, 0x00, 0x1F, 0x8D, 0x84, 0x04, 0x05}
	ppsData := []byte{0x68, 0xCE, 0x3C, 0x80}

	// These should get different IDs (0 and 1 for SPS)
	err := ctx.AddSPS(spsData1)
	if err != nil {
		t.Fatalf("Failed to add SPS 1: %v", err)
	}

	err = ctx.AddSPS(spsData2)
	if err != nil {
		t.Fatalf("Failed to add SPS 2: %v", err)
	}

	err = ctx.AddPPS(ppsData)
	if err != nil {
		t.Fatalf("Failed to add PPS: %v", err)
	}

	// Get all parameter sets
	allSets := ctx.GetAllParameterSets()

	// Verify structure
	if len(allSets) != 2 {
		t.Errorf("Expected 2 parameter set types (sps, pps), got %d", len(allSets))
	}

	spsMap, hasSPS := allSets["sps"]
	if !hasSPS {
		t.Errorf("SPS map should be present")
	}

	ppsMap, hasPPS := allSets["pps"]
	if !hasPPS {
		t.Errorf("PPS map should be present")
	}

	// Verify we have the right number of each type
	if len(spsMap) != 1 {
		t.Errorf("Expected 1 SPS entries, got %d", len(spsMap))
	}

	if len(ppsMap) != 1 {
		t.Errorf("Expected 1 PPS entry, got %d", len(ppsMap))
	}
}
