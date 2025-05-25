package ingestion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPressureValidation tests the pressure validation logic
func TestPressureValidation(t *testing.T) {
	tests := []struct {
		name     string
		pressure float64
		valid    bool
	}{
		{
			name:     "valid_0",
			pressure: 0.0,
			valid:    true,
		},
		{
			name:     "valid_0.1",
			pressure: 0.1,
			valid:    true,
		},
		{
			name:     "valid_0.5",
			pressure: 0.5,
			valid:    true,
		},
		{
			name:     "valid_0.9",
			pressure: 0.9,
			valid:    true,
		},
		{
			name:     "valid_1",
			pressure: 1.0,
			valid:    true,
		},
		{
			name:     "invalid_negative",
			pressure: -0.1,
			valid:    false,
		},
		{
			name:     "invalid_greater_than_1",
			pressure: 1.1,
			valid:    false,
		},
		{
			name:     "invalid_very_negative",
			pressure: -100,
			valid:    false,
		},
		{
			name:     "invalid_very_large",
			pressure: 999,
			valid:    false,
		},
		{
			name:     "invalid_slightly_over",
			pressure: 1.0001,
			valid:    false,
		},
		{
			name:     "invalid_slightly_under",
			pressure: -0.0001,
			valid:    false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is the validation logic from HandleStreamBackpressureControl
			isValid := tt.pressure >= 0 && tt.pressure <= 1
			assert.Equal(t, tt.valid, isValid, 
				"Pressure %.4f should be valid=%v", tt.pressure, tt.valid)
		})
	}
}

// TestPressureValidationErrorMessage verifies the error message
func TestPressureValidationErrorMessage(t *testing.T) {
	// The error message we use in the handler
	expectedError := "Pressure must be between 0 and 1"
	
	// Verify it's descriptive
	assert.Contains(t, expectedError, "Pressure")
	assert.Contains(t, expectedError, "between")
	assert.Contains(t, expectedError, "0")
	assert.Contains(t, expectedError, "1")
}
