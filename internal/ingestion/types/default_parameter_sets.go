package types

import (
	"fmt"
)

// DefaultParameterSets provides default H.264 parameter sets for streams
// that don't include them inline or in descriptors
type DefaultParameterSets struct {
	// Common H.264 profiles with typical parameter sets
	commonProfiles map[string][]byte
}

// NewDefaultParameterSets creates a new default parameter set provider
func NewDefaultParameterSets() *DefaultParameterSets {
	return &DefaultParameterSets{
		commonProfiles: initCommonProfiles(),
	}
}

// initCommonProfiles initializes common H.264 parameter sets
func initCommonProfiles() map[string][]byte {
	profiles := make(map[string][]byte)

	// Baseline Profile 720p30
	profiles["baseline_720p30"] = []byte{
		0x67, 0x42, 0x00, 0x1F, 0x95, 0xA8, 0x14, 0x01,
		0x6E, 0x02, 0x02, 0x02, 0x80, 0x00, 0x00, 0x03,
		0x00, 0x80, 0x00, 0x00, 0x1E, 0x07, 0x8B, 0x16,
		0xCB,
	}

	// Main Profile 1080p30
	profiles["main_1080p30"] = []byte{
		0x67, 0x4D, 0x40, 0x28, 0x95, 0xA0, 0x1E, 0x00,
		0x89, 0xF9, 0x70, 0x16, 0xE0, 0x20, 0x20, 0x28,
		0x00, 0x00, 0x03, 0x00, 0x08, 0x00, 0x00, 0x03,
		0x01, 0xE0, 0x78, 0xC1, 0x88, 0xB0,
	}

	// High Profile 1080p30
	profiles["high_1080p30"] = []byte{
		0x67, 0x64, 0x00, 0x28, 0xAC, 0xD9, 0x40, 0x78,
		0x02, 0x27, 0xE5, 0xC0, 0x5A, 0x80, 0x80, 0x80,
		0xA0, 0x00, 0x00, 0x03, 0x00, 0x20, 0x00, 0x00,
		0x07, 0x81, 0xE3, 0x06, 0x32, 0xC0,
	}

	return profiles
}

// GetDefaultSPS returns a default SPS based on detected stream characteristics
func (d *DefaultParameterSets) GetDefaultSPS(width, height int, fps float64) ([]byte, error) {
	// Select appropriate profile based on resolution
	var profileKey string

	if width <= 1280 && height <= 720 {
		profileKey = "baseline_720p30"
	} else if width <= 1920 && height <= 1080 {
		if fps > 30 {
			profileKey = "high_1080p30"
		} else {
			profileKey = "main_1080p30"
		}
	} else {
		// For higher resolutions, use high profile
		profileKey = "high_1080p30"
	}

	sps, exists := d.commonProfiles[profileKey]
	if !exists {
		return nil, fmt.Errorf("no default SPS for profile %s", profileKey)
	}

	// Create a copy to avoid modifying the original
	result := make([]byte, len(sps))
	copy(result, sps)

	return result, nil
}

// GetDefaultPPS returns a minimal default PPS
func (d *DefaultParameterSets) GetDefaultPPS(spsID uint8) []byte {
	// Minimal PPS that references the given SPS
	// Format: NAL header (0x68) + PPS ID (0) + SPS ID + basic parameters
	return []byte{
		0x68,                // PPS NAL header
		0x00 | (spsID << 1), // PPS ID = 0, SPS ID = spsID (simplified encoding)
		0xF0,                // Basic PPS parameters
	}
}

// GenerateMinimalParameterSets generates minimal SPS/PPS for a stream
// This is used when no parameter sets are found in the stream
func (d *DefaultParameterSets) GenerateMinimalParameterSets() (sps []byte, pps []byte) {
	// Minimal valid H.264 SPS for 1920x1080
	// This is a baseline profile SPS that should work for most streams
	sps = []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x67,             // SPS NAL header (type 7)
		0x42, 0x00, 0x1F, // profile_idc=66 (baseline), constraint_set_flags, level_idc=31
		0x8D, 0x8D, 0x40, 0x28, // seq_parameter_set_id=0, etc.
		0xEC, 0xD9, 0x40, 0x40, // More SPS data
		0x40, 0x50, // Frame cropping, VUI parameters
	}

	// Minimal PPS referencing SPS 0
	pps = []byte{
		0x00, 0x00, 0x00, 0x01, // Start code
		0x68,             // PPS NAL header (type 8)
		0xCE, 0x3C, 0x80, // pic_parameter_set_id=0, seq_parameter_set_id=0
	}

	return sps, pps
}
