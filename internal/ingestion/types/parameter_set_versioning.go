package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ParameterSnapshot represents a snapshot of parameter sets without locks
type ParameterSnapshot struct {
	SPSMap     map[uint8]*ParameterSet
	PPSMap     map[uint8]*PPSContext
	FrameCount uint64
}

// ParameterSetVersion represents a specific version of parameter sets
type ParameterSetVersion struct {
	Version       uint64                  `json:"version"`
	Timestamp     time.Time               `json:"timestamp"`
	SPSSets       map[uint8]*ParameterSet `json:"sps_sets"`
	PPSSets       map[uint8]*PPSContext   `json:"pps_sets"`
	Checksum      string                  `json:"checksum"`
	StreamID      string                  `json:"stream_id"`
	FrameCount    uint64                  `json:"frame_count"`
	IsValid       bool                    `json:"is_valid"`
	RollbackCount int                     `json:"rollback_count"`
}

// ParameterSetVersionManager manages versioning and rollback of parameter sets
type ParameterSetVersionManager struct {
	mu              sync.RWMutex
	currentVersion  uint64
	versions        map[uint64]*ParameterSetVersion
	maxVersions     int
	streamID        string
	rollbackEnabled bool

	// Metrics
	totalVersions  uint64
	totalRollbacks uint64
	lastRollback   time.Time
}

// NewParameterSetVersionManager creates a new version manager
func NewParameterSetVersionManager(streamID string, maxVersions int) *ParameterSetVersionManager {
	return &ParameterSetVersionManager{
		versions:        make(map[uint64]*ParameterSetVersion),
		maxVersions:     maxVersions,
		streamID:        streamID,
		rollbackEnabled: true,
	}
}

// CreateVersion creates a new version from current parameter sets
func (vm *ParameterSetVersionManager) CreateVersion(ctx *ParameterSetContext) (*ParameterSetVersion, error) {
	// First, get the data from context without holding vm.mu
	ctx.mu.RLock()
	spsCopy := make(map[uint8]*ParameterSet)
	ppsCopy := make(map[uint8]*PPSContext)

	for id, sps := range ctx.spsMap {
		spsCopy[id] = vm.copyParameterSet(sps)
	}
	for id, pps := range ctx.ppsMap {
		ppsCopy[id] = vm.copyPPSContext(pps)
	}
	frameCount := ctx.totalFramesProcessed
	ctx.mu.RUnlock()

	// Now acquire vm.mu after releasing ctx.mu
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Calculate checksum for version integrity
	checksum := vm.calculateVersionChecksum(spsCopy, ppsCopy)

	// Create new version
	vm.currentVersion++
	version := &ParameterSetVersion{
		Version:    vm.currentVersion,
		Timestamp:  time.Now(),
		SPSSets:    spsCopy,
		PPSSets:    ppsCopy,
		Checksum:   checksum,
		StreamID:   vm.streamID,
		FrameCount: frameCount,
		IsValid:    true,
	}

	// Store version
	vm.versions[version.Version] = version
	vm.totalVersions++

	// Cleanup old versions if necessary
	vm.cleanupOldVersions()

	return version, nil
}

// CreateVersionFromSnapshot creates a new version from a parameter snapshot
func (vm *ParameterSetVersionManager) CreateVersionFromSnapshot(snapshot *ParameterSnapshot) (*ParameterSetVersion, error) {
	// Create deep copies of parameter sets before acquiring lock
	spsCopy := make(map[uint8]*ParameterSet)
	ppsCopy := make(map[uint8]*PPSContext)

	for id, sps := range snapshot.SPSMap {
		spsCopy[id] = vm.copyParameterSet(sps)
	}
	for id, pps := range snapshot.PPSMap {
		ppsCopy[id] = vm.copyPPSContext(pps)
	}

	// Calculate checksum for version integrity (expensive operation outside lock)
	checksum := vm.calculateVersionChecksum(spsCopy, ppsCopy)

	// Now acquire lock only for the quick operations
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Create new version
	vm.currentVersion++
	version := &ParameterSetVersion{
		Version:    vm.currentVersion,
		Timestamp:  time.Now(),
		SPSSets:    spsCopy,
		PPSSets:    ppsCopy,
		Checksum:   checksum,
		StreamID:   vm.streamID,
		FrameCount: snapshot.FrameCount,
		IsValid:    true,
	}

	// Store version
	vm.versions[version.Version] = version
	vm.totalVersions++

	// Enforce version limit
	if len(vm.versions) > vm.maxVersions {
		vm.cleanupOldVersions()
	}

	return version, nil
}

// ValidateVersion checks if a version is still valid
func (vm *ParameterSetVersionManager) ValidateVersion(version uint64) error {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	v, exists := vm.versions[version]
	if !exists {
		return fmt.Errorf("version %d not found", version)
	}

	if !v.IsValid {
		return fmt.Errorf("version %d is marked invalid", version)
	}

	// Recalculate checksum to verify integrity
	currentChecksum := vm.calculateVersionChecksum(v.SPSSets, v.PPSSets)
	if currentChecksum != v.Checksum {
		return fmt.Errorf("version %d checksum mismatch: expected %s, got %s",
			version, v.Checksum, currentChecksum)
	}

	return nil
}

// Rollback reverts to a previous version
func (vm *ParameterSetVersionManager) Rollback(ctx *ParameterSetContext, targetVersion uint64) error {
	if !vm.rollbackEnabled {
		return fmt.Errorf("rollback is disabled")
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Validate target version exists and is valid
	targetVer, exists := vm.versions[targetVersion]
	if !exists {
		return fmt.Errorf("target version %d not found", targetVersion)
	}

	if !targetVer.IsValid {
		return fmt.Errorf("target version %d is invalid", targetVersion)
	}

	// Verify checksum before rollback
	checksum := vm.calculateVersionChecksum(targetVer.SPSSets, targetVer.PPSSets)
	if checksum != targetVer.Checksum {
		return fmt.Errorf("target version %d integrity check failed", targetVersion)
	}

	// Create a backup of current state
	backupVersion, err := vm.createBackupVersion(ctx)
	if err != nil {
		return fmt.Errorf("failed to create backup before rollback: %w", err)
	}

	// Perform rollback
	ctx.mu.Lock()
	defer ctx.mu.Unlock()

	// Clear current parameter sets
	ctx.spsMap = make(map[uint8]*ParameterSet)
	ctx.ppsMap = make(map[uint8]*PPSContext)

	// Restore from target version
	for id, sps := range targetVer.SPSSets {
		ctx.spsMap[id] = vm.copyParameterSet(sps)
	}
	for id, pps := range targetVer.PPSSets {
		ctx.ppsMap[id] = vm.copyPPSContext(pps)
	}

	// Update metrics
	vm.totalRollbacks++
	vm.lastRollback = time.Now()
	targetVer.RollbackCount++

	// Mark backup as rollback source
	backupVersion.IsValid = false

	return nil
}

// GetVersionHistory returns version history
func (vm *ParameterSetVersionManager) GetVersionHistory() []*ParameterSetVersion {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	history := make([]*ParameterSetVersion, 0, len(vm.versions))
	for _, v := range vm.versions {
		history = append(history, v)
	}

	// Sort by version number
	for i := 0; i < len(history)-1; i++ {
		for j := i + 1; j < len(history); j++ {
			if history[i].Version > history[j].Version {
				history[i], history[j] = history[j], history[i]
			}
		}
	}

	return history
}

// GetStatistics returns version manager statistics
func (vm *ParameterSetVersionManager) GetStatistics() map[string]interface{} {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	return map[string]interface{}{
		"current_version":  vm.currentVersion,
		"total_versions":   vm.totalVersions,
		"stored_versions":  len(vm.versions),
		"max_versions":     vm.maxVersions,
		"total_rollbacks":  vm.totalRollbacks,
		"last_rollback":    vm.lastRollback,
		"rollback_enabled": vm.rollbackEnabled,
	}
}

// EnableRollback enables or disables rollback functionality
func (vm *ParameterSetVersionManager) EnableRollback(enabled bool) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.rollbackEnabled = enabled
}

// Private helper methods

func (vm *ParameterSetVersionManager) copyParameterSet(ps *ParameterSet) *ParameterSet {
	if ps == nil {
		return nil
	}

	copy := &ParameterSet{
		ID:          ps.ID,
		Data:        append([]byte(nil), ps.Data...),
		ParsedAt:    ps.ParsedAt,
		Size:        ps.Size,
		Valid:       ps.Valid,
		ErrorReason: ps.ErrorReason,
	}

	if ps.ProfileIDC != nil {
		profileCopy := *ps.ProfileIDC
		copy.ProfileIDC = &profileCopy
	}
	if ps.LevelIDC != nil {
		levelCopy := *ps.LevelIDC
		copy.LevelIDC = &levelCopy
	}
	if ps.Width != nil {
		widthCopy := *ps.Width
		copy.Width = &widthCopy
	}
	if ps.Height != nil {
		heightCopy := *ps.Height
		copy.Height = &heightCopy
	}

	return copy
}

func (vm *ParameterSetVersionManager) copyPPSContext(pps *PPSContext) *PPSContext {
	if pps == nil {
		return nil
	}

	return &PPSContext{
		ParameterSet:    vm.copyParameterSet(pps.ParameterSet),
		ReferencedSPSID: pps.ReferencedSPSID,
	}
}

func (vm *ParameterSetVersionManager) calculateVersionChecksum(sps map[uint8]*ParameterSet, pps map[uint8]*PPSContext) string {
	h := sha256.New()

	// Get sorted keys for consistent hashing
	var spsKeys []uint8
	for k := range sps {
		spsKeys = append(spsKeys, k)
	}
	// Sort keys for consistent hash
	for i := 0; i < len(spsKeys); i++ {
		for j := i + 1; j < len(spsKeys); j++ {
			if spsKeys[i] > spsKeys[j] {
				spsKeys[i], spsKeys[j] = spsKeys[j], spsKeys[i]
			}
		}
	}

	// Hash SPS sets in order
	for _, i := range spsKeys {
		if s := sps[i]; s != nil {
			h.Write([]byte{i})
			h.Write(s.Data)
		}
	}

	// Get sorted PPS keys
	var ppsKeys []uint8
	for k := range pps {
		ppsKeys = append(ppsKeys, k)
	}
	// Sort keys for consistent hash
	for i := 0; i < len(ppsKeys); i++ {
		for j := i + 1; j < len(ppsKeys); j++ {
			if ppsKeys[i] > ppsKeys[j] {
				ppsKeys[i], ppsKeys[j] = ppsKeys[j], ppsKeys[i]
			}
		}
	}

	// Hash PPS sets in order
	for _, i := range ppsKeys {
		if p := pps[i]; p != nil {
			h.Write([]byte{i})
			h.Write(p.Data)
			h.Write([]byte{p.ReferencedSPSID})
		}
	}

	return hex.EncodeToString(h.Sum(nil))
}

func (vm *ParameterSetVersionManager) cleanupOldVersions() {
	if len(vm.versions) <= vm.maxVersions {
		return
	}

	// Find oldest versions to remove
	var oldestVersion uint64
	for v := range vm.versions {
		if oldestVersion == 0 || v < oldestVersion {
			oldestVersion = v
		}
	}

	// Remove oldest versions until we're under the limit
	for len(vm.versions) > vm.maxVersions && oldestVersion < vm.currentVersion {
		delete(vm.versions, oldestVersion)
		oldestVersion++
	}
}

func (vm *ParameterSetVersionManager) createBackupVersion(ctx *ParameterSetContext) (*ParameterSetVersion, error) {
	// Similar to CreateVersion but without incrementing version counter
	spsCopy := make(map[uint8]*ParameterSet)
	ppsCopy := make(map[uint8]*PPSContext)

	ctx.mu.RLock()
	for id, sps := range ctx.spsMap {
		spsCopy[id] = vm.copyParameterSet(sps)
	}
	for id, pps := range ctx.ppsMap {
		ppsCopy[id] = vm.copyPPSContext(pps)
	}
	frameCount := ctx.totalFramesProcessed
	ctx.mu.RUnlock()

	checksum := vm.calculateVersionChecksum(spsCopy, ppsCopy)

	vm.currentVersion++
	version := &ParameterSetVersion{
		Version:    vm.currentVersion,
		Timestamp:  time.Now(),
		SPSSets:    spsCopy,
		PPSSets:    ppsCopy,
		Checksum:   checksum,
		StreamID:   vm.streamID,
		FrameCount: frameCount,
		IsValid:    true,
	}

	vm.versions[version.Version] = version
	return version, nil
}
