package types

import (
	"fmt"
)

// ValidatePPSData validates that PPS data can be parsed correctly
func ValidatePPSData(data []byte) (ppsID uint8, spsID uint8, err error) {
	if len(data) < 2 {
		return 0, 0, fmt.Errorf("PPS data too short: %d bytes", len(data))
	}

	// Check NAL header
	nalHeader := data[0]
	nalType := nalHeader & 0x1F
	forbiddenBit := (nalHeader & 0x80) != 0

	// PPS NAL type is 8 (0x08). Since nalType = nalHeader & 0x1F, the max value is 31.
	if nalType == 8 {
		// Has NAL header - extract RBSP
		rbspData, err := ExtractRBSPFromNALUnit(data)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to extract RBSP: %w", err)
		}
		return parsePPSIDs(rbspData)
	}

	// Check if this looks like a valid NAL header
	isValidNALHeader := !forbiddenBit && nalType > 0 && nalType <= 31

	// Special handling for MPEG-TS descriptor data
	// NAL types 10-23 are reserved and unlikely to be real NAL headers
	// These are more likely to be raw PPS data that happens to look like NAL headers
	if nalType >= 10 && nalType <= 23 {
		// Try parsing as raw data
		ppsID, spsID, err := validateRawPPSData(data)
		if err == nil {
			return ppsID, spsID, nil
		}
		// If it fails as raw data too, report the error
		return 0, 0, fmt.Errorf("failed to parse as PPS (looks like reserved NAL type %d): %w", nalType, err)
	}

	// If this has a valid non-PPS NAL type, reject it
	if isValidNALHeader && nalType != 8 && nalType != 40 {
		// Known non-PPS NAL types
		knownNonPPSTypes := []uint8{1, 5, 6, 7, 9}
		for _, t := range knownNonPPSTypes {
			if nalType == t {
				return 0, 0, fmt.Errorf("invalid PPS NAL type: %d (header: 0x%02x)", nalType, nalHeader)
			}
		}
	}

	// For other cases, try parsing as raw data
	ppsID, spsID, rawErr := validateRawPPSData(data)
	if rawErr == nil {
		return ppsID, spsID, nil
	}

	// If raw parsing failed and this looks like it should have a NAL header, report that
	if isValidNALHeader && nalType != 8 && nalType != 40 {
		return 0, 0, fmt.Errorf("invalid PPS NAL type: %d (header: 0x%02x)", nalType, nalHeader)
	}

	// Return the raw parsing error
	return 0, 0, rawErr
}

// validateRawPPSData validates PPS data without NAL header
func validateRawPPSData(data []byte) (ppsID uint8, spsID uint8, err error) {
	if len(data) < 1 {
		return 0, 0, fmt.Errorf("PPS data too short for raw parsing")
	}

	// Try to parse as raw RBSP data
	rbspData, err := ExtractRBSPFromPayload(data)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to extract RBSP from raw data: %w", err)
	}

	return parsePPSIDs(rbspData)
}

// parsePPSIDs extracts PPS ID and referenced SPS ID from RBSP data
func parsePPSIDs(rbspData []byte) (ppsID uint8, spsID uint8, err error) {
	if len(rbspData) < 1 {
		return 0, 0, fmt.Errorf("RBSP data too short")
	}

	br := NewBitReader(rbspData)

	// Parse pic_parameter_set_id
	ppsIDVal, err := br.ReadUE()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read PPS ID: %w", err)
	}
	if ppsIDVal > 255 {
		return 0, 0, fmt.Errorf("PPS ID %d out of range (0-255)", ppsIDVal)
	}
	ppsID = uint8(ppsIDVal)

	// Parse seq_parameter_set_id
	spsIDVal, err := br.ReadUE()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read SPS ID: %w", err)
	}
	if spsIDVal > 31 {
		return 0, 0, fmt.Errorf("SPS ID %d out of range (0-31)", spsIDVal)
	}
	spsID = uint8(spsIDVal)

	return ppsID, spsID, nil
}

// ValidateSPSData validates that SPS data can be parsed correctly
func ValidateSPSData(data []byte) (spsID uint8, err error) {
	if len(data) < 4 {
		return 0, fmt.Errorf("SPS data too short: %d bytes", len(data))
	}

	// Check NAL header
	nalHeader := data[0]
	nalType := nalHeader & 0x1F

	// Removed debug logging

	// Additional validation: Check if this might be profile_idc without NAL header
	// Common profile_idc values: 66 (Baseline), 77 (Main), 88 (Extended), 100 (High), etc.
	commonProfiles := []uint8{66, 77, 88, 100, 110, 122, 244}
	isLikelyProfileIDC := false
	for _, profile := range commonProfiles {
		if data[0] == profile {
			isLikelyProfileIDC = true
			break
		}
	}

	if isLikelyProfileIDC && nalType != 7 && nalType != 15 {
		// This is likely raw SPS data without NAL header
		return parseSPSIDFromRawData(data)
	}

	// SPS NAL type should be 7 (0x07) or 15 (0x0F) for subset SPS
	if nalType == 7 || nalType == 15 {
		// Has NAL header - parse with NAL header included
		return parseSPSIDFromNALUnit(data)
	}

	// Try parsing as raw data first, since any byte could look like a NAL header
	spsID, err = parseSPSIDFromRawData(data)
	if err == nil {
		// Successfully parsed as raw data
		return spsID, nil
	}

	// If raw parsing failed, check if this might be a wrong NAL type
	forbiddenBit := (nalHeader & 0x80) != 0
	if !forbiddenBit && nalType > 0 && nalType <= 31 && nalType != 7 && nalType != 15 {
		// This looks like a valid NAL header but wrong type
		return 0, fmt.Errorf("invalid SPS NAL type: %d (header: 0x%02x)", nalType, nalHeader)
	}

	// Return the raw parsing error
	return 0, err
}

// parseSPSID extracts SPS ID from RBSP data
func parseSPSID(rbspData []byte) (spsID uint8, err error) {
	if len(rbspData) < 4 {
		return 0, fmt.Errorf("RBSP data too short for SPS parsing")
	}

	// Check for known bad patterns
	if len(rbspData) >= 4 && rbspData[0] == 0x27 && rbspData[1] == 0x4f {
		// This pattern (274f...) has been seen causing issues
		return 0, fmt.Errorf("detected corrupted SPS data pattern (starts with 274f)")
	}

	// Validate that the first byte looks like profile_idc
	if len(rbspData) > 0 {
		profileIDC := rbspData[0]
		validProfiles := map[uint8]bool{
			66: true, 77: true, 88: true, 100: true, 110: true,
			122: true, 244: true, 44: true, 83: true, 86: true,
			118: true, 128: true, 138: true,
		}
		if !validProfiles[profileIDC] {
			// Unknown profile but might be valid
		}
	}

	br := NewBitReader(rbspData)

	// Skip profile_idc (8 bits)
	_, err = br.ReadBits(8)
	if err != nil {
		return 0, fmt.Errorf("failed to read profile_idc: %w", err)
	}

	// Skip constraint flags (8 bits)
	_, err = br.ReadBits(8)
	if err != nil {
		return 0, fmt.Errorf("failed to read constraint flags: %w", err)
	}

	// Skip level_idc (8 bits)
	_, err = br.ReadBits(8)
	if err != nil {
		return 0, fmt.Errorf("failed to read level_idc: %w", err)
	}

	// Parse seq_parameter_set_id
	spsIDVal, err := br.ReadUE()
	if err != nil {
		return 0, fmt.Errorf("failed to read SPS ID: %w", err)
	}
	if spsIDVal > 31 {
		return 0, fmt.Errorf("SPS ID %d out of range (0-31)", spsIDVal)
	}

	return uint8(spsIDVal), nil
}

// parseSPSIDFromNALUnit extracts SPS ID from data with NAL header
func parseSPSIDFromNALUnit(data []byte) (spsID uint8, err error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("SPS data too short for NAL unit parsing")
	}

	// Extract RBSP data
	rbspData, err := ExtractRBSPFromNALUnit(data)
	if err != nil {
		return 0, fmt.Errorf("failed to extract RBSP: %w", err)
	}

	return parseSPSID(rbspData)
}

// parseSPSIDFromRawData extracts SPS ID from raw data (no NAL header)
func parseSPSIDFromRawData(data []byte) (spsID uint8, err error) {
	if len(data) < 3 {
		return 0, fmt.Errorf("SPS data too short for raw parsing")
	}

	// Extract RBSP from payload
	rbspData, err := ExtractRBSPFromPayload(data)
	if err != nil {
		return 0, fmt.Errorf("failed to extract RBSP from raw data: %w", err)
	}

	return parseSPSID(rbspData)
}

// ValidateAndFixNALUnit validates a NAL unit and attempts to fix common issues
func ValidateAndFixNALUnit(data []byte, expectedType uint8) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty NAL unit data")
	}

	nalHeader := data[0]
	nalType := nalHeader & 0x1F

	// Check if NAL type matches expected
	if nalType == expectedType {
		// Validate based on type
		switch expectedType {
		case 7: // SPS
			if _, err := ValidateSPSData(data); err != nil {
				return nil, fmt.Errorf("SPS validation failed: %w", err)
			}
		case 8: // PPS
			if _, _, err := ValidatePPSData(data); err != nil {
				return nil, fmt.Errorf("PPS validation failed: %w", err)
			}
		}
		return data, nil
	}

	// Try to fix by adding NAL header
	// For SPS: if first byte looks like profile_idc (common values: 66, 77, 88, 100, etc.)
	// For PPS: if first byte doesn't look like a valid NAL header
	shouldTryFix := false

	switch expectedType {
	case 7: // SPS
		// Common profile_idc values that indicate missing NAL header
		commonProfiles := []uint8{66, 77, 88, 100, 110, 122, 244}
		for _, profile := range commonProfiles {
			if data[0] == profile {
				shouldTryFix = true
				break
			}
		}
	case 8: // PPS
		// For PPS, try to fix if NAL type doesn't make sense
		// or if the data could be raw PPS (exp-Golomb encoded)
		forbiddenBit := (nalHeader & 0x80) != 0
		if forbiddenBit || nalType > 23 || nalType == 0 {
			shouldTryFix = true
		}
	}

	if shouldTryFix {
		// Try adding NAL header
		fixed := make([]byte, len(data)+1)
		fixed[0] = 0x60 | expectedType // Reference IDC = 3, NAL type = expectedType
		copy(fixed[1:], data)

		// Validate the fixed data
		switch expectedType {
		case 7: // SPS
			if _, err := ValidateSPSData(fixed); err != nil {
				return nil, fmt.Errorf("fixed SPS validation failed: %w", err)
			}
		case 8: // PPS
			if _, _, err := ValidatePPSData(fixed); err != nil {
				return nil, fmt.Errorf("fixed PPS validation failed: %w", err)
			}
		}
		return fixed, nil
	}

	return nil, fmt.Errorf("NAL type mismatch: expected %d, got %d", expectedType, nalType)
}
