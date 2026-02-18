package types

// CleanNALHeader fixes NAL headers with forbidden bit set or other issues
// Returns a cleaned NAL header byte
func CleanNALHeader(header byte) byte {
	// Clear forbidden bit (bit 7) - it must always be 0
	header &= 0x7F

	// Preserve NAL ref IDC (bits 5-6) and NAL type (bits 0-4)
	return header
}

// CleanNALUnitData creates a clean copy of NAL unit data with fixed header
// This is necessary because some encoders produce invalid NAL headers
func CleanNALUnitData(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	// Create a copy to avoid modifying original
	cleaned := make([]byte, len(data))
	copy(cleaned, data)

	// Fix the NAL header
	cleaned[0] = CleanNALHeader(cleaned[0])

	return cleaned
}

// IsValidNALHeader checks if a NAL header is valid according to H.264 spec
func IsValidNALHeader(header byte) bool {
	// Check forbidden bit (must be 0)
	if (header & 0x80) != 0 {
		return false
	}

	// Check NAL type (must be 1-31)
	nalType := header & 0x1F
	if nalType == 0 {
		return false
	}

	return true
}
