package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPTSDTSValidator_BasicValidation(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	tests := []struct {
		name      string
		pts       int64
		dts       int64
		wantValid bool
		wantError string
	}{
		{
			name:      "valid PTS only",
			pts:       90000,
			dts:       0,
			wantValid: true,
		},
		{
			name:      "valid PTS and DTS",
			pts:       90000,
			dts:       90000,
			wantValid: true,
		},
		{
			name:      "PTS > DTS (valid)",
			pts:       95000,
			dts:       90000,
			wantValid: true,
		},
		{
			name:      "negative PTS",
			pts:       -1,
			dts:       0,
			wantValid: false,
			wantError: "negative PTS value: -1",
		},
		{
			name:      "negative DTS",
			pts:       90000,
			dts:       -1,
			wantValid: false,
			wantError: "negative DTS value: -1",
		},
		{
			name:      "PTS exceeds 33-bit limit",
			pts:       MaxPTSValue + 1,
			dts:       0,
			wantValid: false,
			wantError: "PTS exceeds 33-bit limit",
		},
		{
			name:      "DTS exceeds 33-bit limit",
			pts:       90000,
			dts:       MaxPTSValue + 1,
			wantValid: false,
			wantError: "DTS exceeds 33-bit limit",
		},
		{
			name:      "PTS < DTS",
			pts:       90000,
			dts:       95000,
			wantValid: false,
			wantError: "PTS < DTS: 90000 < 95000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validator.ValidateTimestamps(tt.pts, tt.dts)
			assert.Equal(t, tt.wantValid, result.Valid)
			if tt.wantError != "" {
				require.NotNil(t, result.Error)
				assert.Contains(t, result.Error.Error(), tt.wantError)
			} else {
				assert.Nil(t, result.Error)
			}
		})
	}
}

func TestPTSDTSValidator_SequentialValidation(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// First timestamp initializes the validator
	result := validator.ValidateTimestamps(1000, 1000)
	assert.True(t, result.Valid)
	assert.Nil(t, result.Error)
	assert.Empty(t, result.Warnings)

	// Sequential increase (normal case)
	result = validator.ValidateTimestamps(2000, 2000)
	assert.True(t, result.Valid)
	assert.Nil(t, result.Error)
	assert.Empty(t, result.Warnings)

	// Small backward jump in PTS only (DTS can be 0 for frames without DTS)
	result = validator.ValidateTimestamps(1500, 0)
	assert.True(t, result.Valid)
	assert.Nil(t, result.Error)
	require.NotNil(t, result.Warnings)
	assert.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "PTS went backward")

	// DTS backward jump (error) - need to set up properly
	result = validator.ValidateTimestamps(3000, 3000)
	assert.True(t, result.Valid) // This establishes lastDTS = 3000

	// Now test DTS going backward
	result = validator.ValidateTimestamps(4000, 2500)
	assert.False(t, result.Valid)
	assert.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "DTS went backward")
}

func TestPTSDTSValidator_Wraparound(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// Initialize near the wraparound point
	nearMax := int64(MaxPTSValue - 1000)
	result := validator.ValidateTimestamps(nearMax, nearMax)
	assert.True(t, result.Valid)

	// Wraparound occurs
	result = validator.ValidateTimestamps(1000, 1000)
	assert.True(t, result.Valid)
	assert.True(t, result.PTSWrapped)
	assert.True(t, result.DTSWrapped)
	assert.Len(t, result.Warnings, 2)
	assert.Contains(t, result.Warnings[0], "PTS wraparound detected")
	assert.Contains(t, result.Warnings[1], "DTS wraparound detected")

	// Verify corrected values account for wraparound
	assert.Equal(t, int64(1000)+MaxPTSValue, result.CorrectedPTS)
	assert.Equal(t, int64(1000)+MaxPTSValue, result.CorrectedDTS)
}

func TestPTSDTSValidator_LargeJump(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// Initialize
	result := validator.ValidateTimestamps(1000, 1000)
	assert.True(t, result.Valid)

	// Large forward jump (> 10 seconds at 90kHz)
	result = validator.ValidateTimestamps(1000000, 1000000)
	assert.True(t, result.Valid)
	assert.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "large PTS jump")
}

func TestPTSDTSValidator_LargePTSDTSDifference(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// PTS-DTS difference > 1 second (90000 at 90kHz)
	result := validator.ValidateTimestamps(200000, 100000)
	assert.True(t, result.Valid)
	assert.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "large PTS-DTS difference")
}

func TestPTSDTSValidator_Statistics(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// Process some timestamps
	validator.ValidateTimestamps(1000, 1000)
	validator.ValidateTimestamps(2000, 2000)
	validator.ValidateTimestamps(-1, 0) // Error

	stats := validator.GetStatistics()
	assert.Equal(t, "test-stream", stats["stream_id"])
	assert.Equal(t, true, stats["initialized"])
	assert.Equal(t, int64(2000), stats["last_pts"])
	assert.Equal(t, int64(2000), stats["last_dts"])
	assert.Equal(t, 0, stats["pts_wrap_count"])
	assert.Equal(t, 0, stats["dts_wrap_count"])
	assert.Equal(t, 1, stats["error_count"])
}

func TestPTSDTSValidator_Reset(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// Process some timestamps
	validator.ValidateTimestamps(1000, 1000)
	validator.ValidateTimestamps(2000, 2000)

	// Reset
	validator.Reset()

	stats := validator.GetStatistics()
	assert.Equal(t, false, stats["initialized"])
	assert.Equal(t, int64(0), stats["last_pts"])
	assert.Equal(t, int64(0), stats["last_dts"])
	assert.Equal(t, 0, stats["error_count"])
}

func TestPTSDTSValidationService(t *testing.T) {
	service := NewPTSDTSValidationService()

	// Get validator for stream1
	validator1 := service.GetValidator("stream1")
	assert.NotNil(t, validator1)

	// Get same validator again
	validator1Again := service.GetValidator("stream1")
	assert.Equal(t, validator1, validator1Again)

	// Get validator for stream2
	validator2 := service.GetValidator("stream2")
	assert.NotNil(t, validator2)
	assert.NotEqual(t, validator1, validator2)

	// Process some timestamps
	validator1.ValidateTimestamps(1000, 1000)
	validator2.ValidateTimestamps(2000, 2000)

	// Get all statistics
	allStats := service.GetAllStatistics()
	assert.Len(t, allStats, 2)
	assert.Equal(t, int64(1000), allStats["stream1"]["last_pts"])
	assert.Equal(t, int64(2000), allStats["stream2"]["last_pts"])

	// Remove validator
	service.RemoveValidator("stream1")
	allStats = service.GetAllStatistics()
	assert.Len(t, allStats, 1)
	assert.NotContains(t, allStats, "stream1")
}

func TestValidatePTSRange(t *testing.T) {
	tests := []struct {
		name    string
		pts     int64
		wantErr bool
	}{
		{"valid PTS", 90000, false},
		{"zero PTS", 0, false},
		{"max PTS", MaxPTSValue, false},
		{"negative PTS", -1, true},
		{"exceeds max", MaxPTSValue + 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePTSRange(tt.pts)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDTSRange(t *testing.T) {
	tests := []struct {
		name    string
		dts     int64
		wantErr bool
	}{
		{"valid DTS", 90000, false},
		{"zero DTS", 0, false},
		{"max DTS", MaxPTSValue, false},
		{"negative DTS", -1, true},
		{"exceeds max", MaxPTSValue + 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDTSRange(tt.dts)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePTSDTSOrder(t *testing.T) {
	tests := []struct {
		name    string
		pts     int64
		dts     int64
		wantErr bool
	}{
		{"PTS > DTS", 100, 90, false},
		{"PTS = DTS", 100, 100, false},
		{"DTS is 0", 100, 0, false},
		{"PTS < DTS", 90, 100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePTSDTSOrder(tt.pts, tt.dts)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCalculateTimeDuration(t *testing.T) {
	tests := []struct {
		name      string
		timestamp int64
		clockRate int64
		want      time.Duration
	}{
		{"1 second at 90kHz", 90000, 90000, time.Second},
		{"0.5 second at 90kHz", 45000, 90000, 500 * time.Millisecond},
		{"1 second at 48kHz", 48000, 48000, time.Second},
		{"zero timestamp", 0, 90000, 0},
		{"zero clock rate", 90000, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateTimeDuration(tt.timestamp, tt.clockRate)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsLikelyWraparound(t *testing.T) {
	tests := []struct {
		name  string
		oldTS int64
		newTS int64
		want  bool
	}{
		{"normal forward", 1000, 2000, false},
		{"small backward", 2000, 1000, false},
		{"near max to small", MaxPTSValue - 1000, 1000, true},
		{"large backward jump", MaxPTSValue - 1000000, 1000, true}, // Much larger backward jump
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsLikelyWraparound(tt.oldTS, tt.newTS)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandleWraparound(t *testing.T) {
	tests := []struct {
		name      string
		timestamp int64
		wrapCount int
		want      int64
	}{
		{"no wrap", 1000, 0, 1000},
		{"one wrap", 1000, 1, 1000 + MaxPTSValue + 1},
		{"two wraps", 1000, 2, 1000 + 2*(MaxPTSValue+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HandleWraparound(tt.timestamp, tt.wrapCount)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPTSDTSValidator_ConcurrentAccess(t *testing.T) {
	validator := NewPTSDTSValidator("test-stream")

	// Run multiple goroutines concurrently
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(offset int64) {
			for j := 0; j < 100; j++ {
				pts := offset*10000 + int64(j)*1000
				validator.ValidateTimestamps(pts, pts)
			}
			done <- true
		}(int64(i))
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify we can still get statistics
	stats := validator.GetStatistics()
	assert.NotNil(t, stats)
	assert.True(t, stats["initialized"].(bool))
}
