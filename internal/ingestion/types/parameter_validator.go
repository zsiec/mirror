package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ParameterSetValidator provides enhanced validation and security for parameter sets
type ParameterSetValidator struct {
	mu sync.RWMutex

	// Cryptographic validation
	spsChecksums map[uint8]string // SPS ID -> SHA256 checksum
	ppsChecksums map[uint8]string // PPS ID -> SHA256 checksum

	// Rate limiting
	lastSPSUpdate  map[uint8]time.Time
	lastPPSUpdate  map[uint8]time.Time
	spsUpdateCount map[uint8]int
	ppsUpdateCount map[uint8]int

	// Dependency tracking
	ppsDependencies map[uint8]uint8 // PPS ID -> SPS ID
	spsReferences   map[uint8]int   // SPS ID -> reference count

	// Configuration
	maxUpdateRate      time.Duration // Maximum rate of parameter set updates
	maxUpdatesPerHour  int           // Maximum updates per hour per parameter set
	enforceConsistency bool          // Whether to enforce strict consistency

	// Statistics
	validationErrors    int
	rateLimitViolations int
	checksumMismatches  int
	lastCleanup         time.Time
}

// NewParameterSetValidator creates a new parameter set validator
func NewParameterSetValidator(maxUpdateRate time.Duration, maxUpdatesPerHour int) *ParameterSetValidator {
	return &ParameterSetValidator{
		spsChecksums:       make(map[uint8]string),
		ppsChecksums:       make(map[uint8]string),
		lastSPSUpdate:      make(map[uint8]time.Time),
		lastPPSUpdate:      make(map[uint8]time.Time),
		spsUpdateCount:     make(map[uint8]int),
		ppsUpdateCount:     make(map[uint8]int),
		ppsDependencies:    make(map[uint8]uint8),
		spsReferences:      make(map[uint8]int),
		maxUpdateRate:      maxUpdateRate,
		maxUpdatesPerHour:  maxUpdatesPerHour,
		enforceConsistency: false, // Disable strict consistency to allow flexible parameter set discovery
		lastCleanup:        time.Now(),
	}
}

// ValidateSPS validates an SPS before accepting it
func (v *ParameterSetValidator) ValidateSPS(spsID uint8, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Calculate checksum first to check for duplicates
	checksum := v.calculateChecksum(data)

	// Check if this is a duplicate
	if existingChecksum, exists := v.spsChecksums[spsID]; exists {
		if existingChecksum == checksum {
			// Identical SPS - allow but don't count as update
			return nil
		}
	}

	// Not a duplicate - check rate limiting
	if err := v.checkRateLimit(spsID, true); err != nil {
		v.rateLimitViolations++
		return fmt.Errorf("SPS rate limit exceeded: %w", err)
	}

	// Check for different SPS with same ID
	if existingChecksum, exists := v.spsChecksums[spsID]; exists && existingChecksum != checksum {
		// Different SPS with same ID - log but allow if consistency is not enforced
		v.checksumMismatches++
		if v.enforceConsistency {
			return fmt.Errorf("SPS ID %d checksum mismatch: expected %s, got %s",
				spsID, existingChecksum, checksum)
		}
		// If not enforcing consistency, allow the update (parameter sets can change during streaming)
	}

	// Validate SPS structure
	if err := v.validateSPSStructure(data); err != nil {
		v.validationErrors++
		return fmt.Errorf("invalid SPS structure: %w", err)
	}

	// Update tracking
	v.spsChecksums[spsID] = checksum
	v.lastSPSUpdate[spsID] = time.Now()
	v.spsUpdateCount[spsID]++

	// Cleanup old entries periodically
	v.cleanupIfNeeded()

	return nil
}

// ValidatePPS validates a PPS and its SPS dependency
func (v *ParameterSetValidator) ValidatePPS(ppsID uint8, spsID uint8, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Calculate checksum first to check for duplicates
	checksum := v.calculateChecksum(data)

	// Check if this is a duplicate BEFORE rate limiting
	if existingChecksum, exists := v.ppsChecksums[ppsID]; exists {
		if existingChecksum == checksum {
			// Identical PPS - allow but don't count as update
			return nil
		}
	}

	// Not a duplicate - now check rate limiting
	if err := v.checkRateLimit(ppsID, false); err != nil {
		v.rateLimitViolations++
		return fmt.Errorf("PPS rate limit exceeded: %w", err)
	}

	// Verify SPS dependency exists (but be lenient during parameter discovery)
	if _, exists := v.spsChecksums[spsID]; !exists && v.enforceConsistency {
		v.validationErrors++
		return fmt.Errorf("PPS %d references non-existent SPS %d", ppsID, spsID)
	}

	// Check if this is an update (not duplicate)
	if _, exists := v.ppsChecksums[ppsID]; exists {
		// Check if dependency changed (only enforce if consistency is required)
		if oldSPSID, hasDep := v.ppsDependencies[ppsID]; hasDep && oldSPSID != spsID && v.enforceConsistency {
			v.validationErrors++
			return fmt.Errorf("PPS %d dependency changed from SPS %d to %d",
				ppsID, oldSPSID, spsID)
		}
	}

	// Update dependency tracking
	if oldSPSID, exists := v.ppsDependencies[ppsID]; exists && oldSPSID != spsID {
		// Decrease reference count on old SPS
		if v.spsReferences[oldSPSID] > 0 {
			v.spsReferences[oldSPSID]--
		}
	}
	v.ppsDependencies[ppsID] = spsID
	v.spsReferences[spsID]++

	// Update tracking
	v.ppsChecksums[ppsID] = checksum
	v.lastPPSUpdate[ppsID] = time.Now()
	v.ppsUpdateCount[ppsID]++

	return nil
}

// ValidateCrossReferences validates that all PPS/SPS dependencies are satisfied
func (v *ParameterSetValidator) ValidateCrossReferences() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	var errors []error

	// Check all PPS dependencies
	for ppsID, spsID := range v.ppsDependencies {
		if _, exists := v.spsChecksums[spsID]; !exists {
			errors = append(errors, fmt.Errorf("PPS %d references missing SPS %d", ppsID, spsID))
		}
	}

	// Check for orphaned SPS (no PPS references)
	for spsID := range v.spsChecksums {
		if refs, exists := v.spsReferences[spsID]; !exists || refs == 0 {
			// Warning only - orphaned SPS is allowed but suspicious
			// Could log this for monitoring
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cross-reference validation failed: %v", errors)
	}

	return nil
}

// GetDependencies returns the dependency graph
func (v *ParameterSetValidator) GetDependencies() map[uint8]uint8 {
	v.mu.RLock()
	defer v.mu.RUnlock()

	deps := make(map[uint8]uint8)
	for k, v := range v.ppsDependencies {
		deps[k] = v
	}
	return deps
}

// checkRateLimit checks if parameter set updates are within allowed rate
func (v *ParameterSetValidator) checkRateLimit(id uint8, isSPS bool) error {
	now := time.Now()

	var lastUpdate time.Time
	var updateCount int

	if isSPS {
		lastUpdate = v.lastSPSUpdate[id]
		updateCount = v.spsUpdateCount[id]
	} else {
		lastUpdate = v.lastPPSUpdate[id]
		updateCount = v.ppsUpdateCount[id]
	}

	// Check minimum time between updates
	if !lastUpdate.IsZero() && now.Sub(lastUpdate) < v.maxUpdateRate {
		return fmt.Errorf("update too frequent: %v since last update", now.Sub(lastUpdate))
	}

	// Check hourly rate limit
	if updateCount > v.maxUpdatesPerHour {
		return fmt.Errorf("exceeded hourly update limit: %d updates", updateCount)
	}

	return nil
}

// calculateChecksum computes SHA256 checksum of data
func (v *ParameterSetValidator) calculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// validateSPSStructure performs basic structural validation
func (v *ParameterSetValidator) validateSPSStructure(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("SPS too short: %d bytes", len(data))
	}

	// Check NAL unit type only if it looks like a NAL header
	// NAL headers have forbidden_zero_bit = 0 (bit 7 must be 0)
	if data[0]&0x80 == 0 {
		// Looks like a NAL header
		nalType := data[0] & 0x1f
		if nalType != 7 && nalType != 15 { // 7 = H.264 SPS, 15 = subset SPS
			return fmt.Errorf("not an SPS NAL unit: type %d", nalType)
		}
	}
	// Otherwise assume it's raw SPS data without header

	// Could add more structural checks here
	// - Profile/level validation
	// - Resolution sanity checks

	return nil
}

// cleanupIfNeeded performs periodic cleanup of old entries
func (v *ParameterSetValidator) cleanupIfNeeded() {
	if time.Since(v.lastCleanup) < time.Hour {
		return
	}

	now := time.Now()
	v.lastCleanup = now

	// Reset hourly counters
	for id := range v.spsUpdateCount {
		if v.lastSPSUpdate[id].Before(now.Add(-time.Hour)) {
			v.spsUpdateCount[id] = 0
		}
	}

	for id := range v.ppsUpdateCount {
		if v.lastPPSUpdate[id].Before(now.Add(-time.Hour)) {
			v.ppsUpdateCount[id] = 0
		}
	}
}

// GetStatistics returns validation statistics
func (v *ParameterSetValidator) GetStatistics() map[string]interface{} {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return map[string]interface{}{
		"validation_errors":     v.validationErrors,
		"rate_limit_violations": v.rateLimitViolations,
		"checksum_mismatches":   v.checksumMismatches,
		"tracked_sps":           len(v.spsChecksums),
		"tracked_pps":           len(v.ppsChecksums),
		"pps_dependencies":      len(v.ppsDependencies),
	}
}

// Reset clears all validation state
func (v *ParameterSetValidator) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.spsChecksums = make(map[uint8]string)
	v.ppsChecksums = make(map[uint8]string)
	v.lastSPSUpdate = make(map[uint8]time.Time)
	v.lastPPSUpdate = make(map[uint8]time.Time)
	v.spsUpdateCount = make(map[uint8]int)
	v.ppsUpdateCount = make(map[uint8]int)
	v.ppsDependencies = make(map[uint8]uint8)
	v.spsReferences = make(map[uint8]int)
	v.validationErrors = 0
	v.rateLimitViolations = 0
	v.checksumMismatches = 0
}
