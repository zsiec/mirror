package types

import (
	"testing"
	"time"
)

func TestFallbackParameterManager_Creation(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	if fpm == nil {
		t.Fatal("Expected fallback parameter manager to be created")
	}

	if fpm.codec != CodecH264 {
		t.Errorf("Expected codec H264, got %v", fpm.codec)
	}

	if fpm.streamID != "test-stream" {
		t.Errorf("Expected streamID 'test-stream', got '%s'", fpm.streamID)
	}

	// Check that generic parameter sets were initialized for H.264
	if fpm.genericSPS == nil {
		t.Error("Generic SPS should be initialized for H.264")
	}

	if fpm.genericPPS == nil {
		t.Error("Generic PPS should be initialized for H.264")
	}
}

func TestFallbackParameterManager_H264GenericFallback(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	requiredSets := map[string]uint32{
		"sps": 0,
		"pps": 0,
	}

	result := fpm.FindFallbackParameterSets(requiredSets, nil, FallbackGeneric)

	if !result.Found {
		t.Error("Generic fallback should find parameter sets for H.264")
	}

	if result.Strategy != FallbackGeneric {
		t.Errorf("Expected strategy FallbackGeneric, got %v", result.Strategy)
	}

	if len(result.ParameterSets) != 2 {
		t.Errorf("Expected 2 parameter sets, got %d", len(result.ParameterSets))
	}

	if _, hasSPS := result.ParameterSets["sps"]; !hasSPS {
		t.Error("Result should include SPS")
	}

	if _, hasPPS := result.ParameterSets["pps"]; !hasPPS {
		t.Error("Result should include PPS")
	}

	if result.QualityScore <= 0 {
		t.Error("Quality score should be positive for generic fallback")
	}
}

func TestFallbackParameterManager_HEVCGenericFallback(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecHEVC, "test-stream")

	requiredSets := map[string]uint32{
		"vps": 0,
		"sps": 0,
		"pps": 0,
	}

	result := fpm.FindFallbackParameterSets(requiredSets, nil, FallbackGeneric)

	if !result.Found {
		t.Error("Generic fallback should find parameter sets for HEVC")
	}

	if len(result.ParameterSets) != 3 {
		t.Errorf("Expected 3 parameter sets for HEVC, got %d", len(result.ParameterSets))
	}

	if _, hasVPS := result.ParameterSets["vps"]; !hasVPS {
		t.Error("Result should include VPS for HEVC")
	}
}

func TestFallbackParameterManager_ApproximateMatch(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Create some available parameter sets
	availableSets := map[string]*ParameterSet{
		"sps_1": {
			ID:       1,
			Data:     []byte{0x67, 0x42, 0x00, 0x1f}, // Valid H.264 SPS
			Valid:    true,
			ParsedAt: time.Now(),
		},
		"pps_1": {
			ID:       1,
			Data:     []byte{0x68, 0xce, 0x38, 0x80}, // Valid H.264 PPS
			Valid:    true,
			ParsedAt: time.Now(),
		},
	}

	requiredSets := map[string]uint32{
		"sps": 0, // Different ID than available
		"pps": 0,
	}

	result := fpm.FindFallbackParameterSets(requiredSets, availableSets, FallbackApproximate)

	if !result.Found {
		t.Error("Approximate fallback should find suitable parameter sets")
	}

	if result.Strategy != FallbackApproximate {
		t.Errorf("Expected strategy FallbackApproximate, got %v", result.Strategy)
	}

	if result.QualityScore <= 0 {
		t.Error("Quality score should be positive for approximate match")
	}
}

func TestFallbackParameterManager_CompatibleMatch(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Create parameter sets that should be compatible
	availableSets := map[string]*ParameterSet{
		"sps_compatible": {
			ID:         0,
			Data:       []byte{0x67, 0x42, 0x00, 0x1f},
			Valid:      true,
			ProfileIDC: uint8Ptr(66), // Baseline profile
			LevelIDC:   uint8Ptr(31), // Level 3.1
			ParsedAt:   time.Now(),
		},
		"pps_compatible": {
			ID:       0,
			Data:     []byte{0x68, 0xce, 0x38, 0x80},
			Valid:    true,
			ParsedAt: time.Now(),
		},
	}

	requiredSets := map[string]uint32{
		"sps": 0,
		"pps": 0,
	}

	result := fpm.FindFallbackParameterSets(requiredSets, availableSets, FallbackCompatible)

	if result.Strategy != FallbackCompatible {
		t.Errorf("Expected strategy FallbackCompatible, got %v", result.Strategy)
	}

	// Even if no exact compatibility rules match, it should handle gracefully
	if result.Found && result.QualityScore <= 0 {
		t.Error("Quality score should be positive if compatible match found")
	}
}

func TestFallbackParameterManager_NoFallback(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	requiredSets := map[string]uint32{
		"sps": 0,
		"pps": 0,
	}

	result := fpm.FindFallbackParameterSets(requiredSets, nil, FallbackNone)

	if result.Found {
		t.Error("No fallback strategy should not find parameter sets")
	}

	if result.Strategy != FallbackNone {
		t.Errorf("Expected strategy FallbackNone, got %v", result.Strategy)
	}
}

func TestFallbackParameterManager_Statistics(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Initial statistics
	stats := fpm.GetStatistics()
	if stats["total_requests"].(uint64) != 0 {
		t.Error("Initial total requests should be 0")
	}

	// Perform some fallback operations
	requiredSets := map[string]uint32{"sps": 0, "pps": 0}

	fpm.FindFallbackParameterSets(requiredSets, nil, FallbackGeneric)
	fpm.FindFallbackParameterSets(requiredSets, nil, FallbackGeneric)

	stats = fpm.GetStatistics()

	if stats["total_requests"].(uint64) != 2 {
		t.Errorf("Expected 2 total requests, got %v", stats["total_requests"])
	}

	if stats["fallbacks_used"].(uint64) != 2 {
		t.Errorf("Expected 2 fallbacks used, got %v", stats["fallbacks_used"])
	}

	if stats["generic_hits"].(uint64) != 2 {
		t.Errorf("Expected 2 generic hits, got %v", stats["generic_hits"])
	}

	fallbackRate := stats["fallback_rate"].(float64)
	if fallbackRate != 1.0 {
		t.Errorf("Expected fallback rate 1.0, got %v", fallbackRate)
	}
}

func TestFallbackParameterManager_ParameterSetIdentification(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Test SPS identification
	spsParam := &ParameterSet{
		Data: []byte{0x67, 0x42, 0x00, 0x1f}, // H.264 SPS NAL type 7
	}

	if !fpm.isLikelySPS(spsParam) {
		t.Error("Should identify valid SPS parameter set")
	}

	// Test PPS identification
	ppsParam := &ParameterSet{
		Data: []byte{0x68, 0xce, 0x38, 0x80}, // H.264 PPS NAL type 8
	}

	if !fpm.isLikelyPPS(ppsParam) {
		t.Error("Should identify valid PPS parameter set")
	}

	// Test invalid parameter set
	invalidParam := &ParameterSet{
		Data: []byte{0x61, 0x00, 0x00, 0x00}, // Not SPS or PPS
	}

	if fpm.isLikelySPS(invalidParam) {
		t.Error("Should not identify non-SPS as SPS")
	}

	if fpm.isLikelyPPS(invalidParam) {
		t.Error("Should not identify non-PPS as PPS")
	}
}

func TestFallbackParameterManager_HEVCParameterSetIdentification(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecHEVC, "test-stream")

	// Test HEVC VPS identification (NAL type 32)
	vpsParam := &ParameterSet{
		Data: []byte{0x40, 0x01}, // HEVC VPS NAL type 32
	}

	if !fpm.isLikelyHEVCParameterSet(vpsParam, "vps") {
		t.Error("Should identify valid HEVC VPS parameter set")
	}

	// Test HEVC SPS identification (NAL type 33)
	spsParam := &ParameterSet{
		Data: []byte{0x42, 0x01}, // HEVC SPS NAL type 33
	}

	if !fpm.isLikelyHEVCParameterSet(spsParam, "sps") {
		t.Error("Should identify valid HEVC SPS parameter set")
	}

	// Test HEVC PPS identification (NAL type 34)
	ppsParam := &ParameterSet{
		Data: []byte{0x44, 0x01}, // HEVC PPS NAL type 34
	}

	if !fpm.isLikelyHEVCParameterSet(ppsParam, "pps") {
		t.Error("Should identify valid HEVC PPS parameter set")
	}
}

func TestFallbackParameterManager_CompatibilityRules(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Test updating compatibility rules
	rule := CompatibilityRule{
		ProfileCompatible:   []uint8{100}, // High profile only
		LevelCompatible:     []uint8{50},  // Level 5.0 only
		ResolutionTolerance: 0.05,         // 5% tolerance
		QualityScore:        0.95,
	}

	fpm.UpdateCompatibilityRule("custom_sps_0", rule)

	stats := fpm.GetStatistics()
	if stats["rules_count"].(int) <= 0 {
		t.Error("Should have compatibility rules after update")
	}
}

func TestFallbackParameterManager_ThreadSafety(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Test concurrent access
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			requiredSets := map[string]uint32{"sps": 0, "pps": 0}
			fpm.FindFallbackParameterSets(requiredSets, nil, FallbackGeneric)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic and should have correct statistics
	stats := fpm.GetStatistics()
	if stats["total_requests"].(uint64) != 10 {
		t.Errorf("Expected 10 total requests, got %v", stats["total_requests"])
	}
}

func TestFallbackParameterManager_Close(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Should not panic
	fpm.Close()

	// Should be able to call close multiple times
	fpm.Close()
}

func TestFallbackParameterManager_EmptyRequiredSets(t *testing.T) {
	fpm := NewFallbackParameterManager(CodecH264, "test-stream")

	// Test with empty required sets
	requiredSets := map[string]uint32{}

	result := fpm.FindFallbackParameterSets(requiredSets, nil, FallbackGeneric)

	if !result.Found {
		t.Error("Should succeed with empty required sets")
	}

	if len(result.ParameterSets) != 0 {
		t.Errorf("Expected 0 parameter sets, got %d", len(result.ParameterSets))
	}
}

// Helper function for tests
func uint8Ptr(v uint8) *uint8 {
	return &v
}
