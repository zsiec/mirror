package types

import "time"

// NewParameterSetContextForTest creates a parameter context without rate limiting for testing
func NewParameterSetContextForTest(codec CodecType, streamID string) *ParameterSetContext {
	return NewParameterSetContextWithConfig(codec, streamID, ParameterSetContextConfig{
		ValidatorUpdateRate:        0, // Disabled
		ValidatorMaxUpdatesPerHour: 0, // Disabled
		EnableVersioning:           true,
		MaxVersions:                10,
	})
}

// NewParameterSetContextWithValidatorForTest creates a parameter context with test-friendly rate limits
func NewParameterSetContextWithValidatorForTest(codec CodecType, streamID string) *ParameterSetContext {
	return NewParameterSetContextWithConfig(codec, streamID, ParameterSetContextConfig{
		ValidatorUpdateRate:        1 * time.Millisecond, // Very short for tests
		ValidatorMaxUpdatesPerHour: 10000,                // Very high for tests
		EnableVersioning:           true,
		MaxVersions:                10,
	})
}
