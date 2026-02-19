package types

import (
	"fmt"
	"sync"
	"time"
)

// MigrationStrategy defines how parameter sets should be migrated
type MigrationStrategy int

const (
	// MigrationStrategyImmediate switches immediately to new parameter sets
	MigrationStrategyImmediate MigrationStrategy = iota
	// MigrationStrategyGradual gradually transitions over multiple GOPs
	MigrationStrategyGradual
	// MigrationStrategyIDR waits for next IDR frame to switch
	MigrationStrategyIDR
	// MigrationStrategyValidated switches only after validation passes
	MigrationStrategyValidated
)

// ParameterSetMigration represents a migration operation
type ParameterSetMigration struct {
	ID              string                       `json:"id"`
	StartTime       time.Time                    `json:"start_time"`
	EndTime         time.Time                    `json:"end_time"`
	Strategy        MigrationStrategy            `json:"strategy"`
	SourceVersion   uint64                       `json:"source_version"`
	TargetVersion   uint64                       `json:"target_version"`
	State           MigrationState               `json:"state"`
	Progress        float64                      `json:"progress"`
	ValidationSteps []ParameterSetValidationStep `json:"validation_steps"`
	ErrorReason     string                       `json:"error_reason,omitempty"`
}

// MigrationState represents the current state of a migration
type MigrationState int

const (
	MigrationStatePending MigrationState = iota
	MigrationStateValidating
	MigrationStateInProgress
	MigrationStateCompleted
	MigrationStateFailed
	MigrationStateRolledBack
)

// ParameterSetValidationStep represents a validation step in migration
type ParameterSetValidationStep struct {
	Name      string    `json:"name"`
	Completed bool      `json:"completed"`
	Passed    bool      `json:"passed"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ParameterSetMigrationManager handles parameter set migrations
type ParameterSetMigrationManager struct {
	mu               sync.RWMutex
	activeMigration  *ParameterSetMigration
	migrationHistory []*ParameterSetMigration
	versionManager   *ParameterSetVersionManager
	validator        *ParameterSetValidator
	maxHistorySize   int

	// Callbacks
	onMigrationStart    func(*ParameterSetMigration)
	onMigrationComplete func(*ParameterSetMigration)
	onMigrationFailed   func(*ParameterSetMigration, error)

	// Metrics
	totalMigrations      uint64
	successfulMigrations uint64
	failedMigrations     uint64
}

// NewParameterSetMigrationManager creates a new migration manager
func NewParameterSetMigrationManager(versionManager *ParameterSetVersionManager, validator *ParameterSetValidator) *ParameterSetMigrationManager {
	return &ParameterSetMigrationManager{
		migrationHistory: make([]*ParameterSetMigration, 0),
		versionManager:   versionManager,
		validator:        validator,
		maxHistorySize:   100,
	}
}

// StartMigration begins a new parameter set migration
func (mm *ParameterSetMigrationManager) StartMigration(sourceVersion, targetVersion uint64, strategy MigrationStrategy) (*ParameterSetMigration, error) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Check if migration is already in progress
	if mm.activeMigration != nil && mm.activeMigration.State == MigrationStateInProgress {
		return nil, fmt.Errorf("migration already in progress: %s", mm.activeMigration.ID)
	}

	// Validate versions exist
	if err := mm.versionManager.ValidateVersion(sourceVersion); err != nil {
		return nil, fmt.Errorf("invalid source version %d: %w", sourceVersion, err)
	}
	if err := mm.versionManager.ValidateVersion(targetVersion); err != nil {
		return nil, fmt.Errorf("invalid target version %d: %w", targetVersion, err)
	}

	// Create migration
	migration := &ParameterSetMigration{
		ID:            fmt.Sprintf("mig_%d_%d_%d", sourceVersion, targetVersion, time.Now().Unix()),
		StartTime:     time.Now(),
		Strategy:      strategy,
		SourceVersion: sourceVersion,
		TargetVersion: targetVersion,
		State:         MigrationStatePending,
		Progress:      0.0,
		ValidationSteps: []ParameterSetValidationStep{
			{Name: "version_compatibility", Completed: false},
			{Name: "dependency_check", Completed: false},
			{Name: "integrity_verification", Completed: false},
			{Name: "performance_impact", Completed: false},
		},
	}

	mm.activeMigration = migration
	mm.totalMigrations++

	// Notify callback
	if mm.onMigrationStart != nil {
		mm.onMigrationStart(migration)
	}

	// Start migration process based on strategy
	go mm.executeMigration(migration)

	return migration, nil
}

// GetActiveMigration returns the currently active migration
func (mm *ParameterSetMigrationManager) GetActiveMigration() *ParameterSetMigration {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.activeMigration
}

// GetMigrationHistory returns migration history
func (mm *ParameterSetMigrationManager) GetMigrationHistory() []*ParameterSetMigration {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	history := make([]*ParameterSetMigration, len(mm.migrationHistory))
	copy(history, mm.migrationHistory)
	return history
}

// CancelMigration cancels an active migration
func (mm *ParameterSetMigrationManager) CancelMigration(reason string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if mm.activeMigration == nil {
		return fmt.Errorf("no active migration to cancel")
	}

	if mm.activeMigration.State != MigrationStateInProgress &&
		mm.activeMigration.State != MigrationStateValidating {
		return fmt.Errorf("migration in state %d cannot be cancelled", mm.activeMigration.State)
	}

	mm.activeMigration.State = MigrationStateFailed
	mm.activeMigration.ErrorReason = fmt.Sprintf("cancelled: %s", reason)
	mm.activeMigration.EndTime = time.Now()

	mm.failedMigrations++
	mm.addToHistory(mm.activeMigration)

	if mm.onMigrationFailed != nil {
		mm.onMigrationFailed(mm.activeMigration, fmt.Errorf("%s", reason))
	}

	mm.activeMigration = nil
	return nil
}

// GetStatistics returns migration statistics
func (mm *ParameterSetMigrationManager) GetStatistics() map[string]interface{} {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	return map[string]interface{}{
		"total_migrations":      mm.totalMigrations,
		"successful_migrations": mm.successfulMigrations,
		"failed_migrations":     mm.failedMigrations,
		"history_size":          len(mm.migrationHistory),
		"active_migration":      mm.activeMigration != nil,
	}
}

// SetCallbacks sets migration event callbacks
func (mm *ParameterSetMigrationManager) SetCallbacks(
	onStart func(*ParameterSetMigration),
	onComplete func(*ParameterSetMigration),
	onFailed func(*ParameterSetMigration, error)) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	mm.onMigrationStart = onStart
	mm.onMigrationComplete = onComplete
	mm.onMigrationFailed = onFailed
}

// Private methods

func (mm *ParameterSetMigrationManager) executeMigration(migration *ParameterSetMigration) {
	// Start validation phase
	mm.updateMigrationState(migration, MigrationStateValidating)

	// Run validation steps
	if err := mm.runValidationSteps(migration); err != nil {
		mm.failMigration(migration, fmt.Errorf("validation failed: %w", err))
		return
	}

	// Start actual migration based on strategy
	mm.updateMigrationState(migration, MigrationStateInProgress)

	switch migration.Strategy {
	case MigrationStrategyImmediate:
		mm.executeImmediateMigration(migration)
	case MigrationStrategyGradual:
		mm.executeGradualMigration(migration)
	case MigrationStrategyIDR:
		mm.executeIDRMigration(migration)
	case MigrationStrategyValidated:
		mm.executeValidatedMigration(migration)
	default:
		mm.failMigration(migration, fmt.Errorf("unknown migration strategy: %d", migration.Strategy))
	}
}

func (mm *ParameterSetMigrationManager) runValidationSteps(migration *ParameterSetMigration) error {
	// Version compatibility check
	if err := mm.validateVersionCompatibility(migration); err != nil {
		return fmt.Errorf("version compatibility: %w", err)
	}
	mm.updateValidationStep(migration, 0, true, nil)

	// Dependency check
	if err := mm.validateDependencies(migration); err != nil {
		return fmt.Errorf("dependency check: %w", err)
	}
	mm.updateValidationStep(migration, 1, true, nil)

	// Integrity verification
	if err := mm.validateIntegrity(migration); err != nil {
		return fmt.Errorf("integrity verification: %w", err)
	}
	mm.updateValidationStep(migration, 2, true, nil)

	// Performance impact assessment
	if err := mm.assessPerformanceImpact(migration); err != nil {
		return fmt.Errorf("performance impact: %w", err)
	}
	mm.updateValidationStep(migration, 3, true, nil)

	return nil
}

func (mm *ParameterSetMigrationManager) validateVersionCompatibility(migration *ParameterSetMigration) error {
	// Check if versions are compatible
	if migration.TargetVersion <= migration.SourceVersion {
		return fmt.Errorf("target version must be newer than source version")
	}
	return nil
}

func (mm *ParameterSetMigrationManager) validateDependencies(migration *ParameterSetMigration) error {
	// Validate parameter set dependencies are maintained
	if mm.validator != nil {
		return mm.validator.ValidateCrossReferences()
	}
	return nil
}

func (mm *ParameterSetMigrationManager) validateIntegrity(migration *ParameterSetMigration) error {
	// Verify integrity of both versions
	if err := mm.versionManager.ValidateVersion(migration.SourceVersion); err != nil {
		return fmt.Errorf("source version integrity: %w", err)
	}
	if err := mm.versionManager.ValidateVersion(migration.TargetVersion); err != nil {
		return fmt.Errorf("target version integrity: %w", err)
	}
	return nil
}

func (mm *ParameterSetMigrationManager) assessPerformanceImpact(migration *ParameterSetMigration) error {
	// Assess potential performance impact
	// This is a placeholder - real implementation would analyze the differences
	return nil
}

func (mm *ParameterSetMigrationManager) executeImmediateMigration(migration *ParameterSetMigration) {
	// Immediate switch - just update progress to 100%
	mm.updateMigrationProgress(migration, 1.0)
	mm.completeMigration(migration)
}

func (mm *ParameterSetMigrationManager) executeGradualMigration(migration *ParameterSetMigration) {
	// Gradual migration over time
	steps := 10
	for i := 0; i <= steps; i++ {
		progress := float64(i) / float64(steps)
		mm.updateMigrationProgress(migration, progress)
		time.Sleep(100 * time.Millisecond) // Simulate gradual transition
	}
	mm.completeMigration(migration)
}

func (mm *ParameterSetMigrationManager) executeIDRMigration(migration *ParameterSetMigration) {
	// Wait for IDR frame (simulated)
	mm.updateMigrationProgress(migration, 0.5)
	time.Sleep(500 * time.Millisecond) // Simulate waiting for IDR
	mm.updateMigrationProgress(migration, 1.0)
	mm.completeMigration(migration)
}

func (mm *ParameterSetMigrationManager) executeValidatedMigration(migration *ParameterSetMigration) {
	// Run additional validation during migration
	mm.updateMigrationProgress(migration, 0.25)
	time.Sleep(200 * time.Millisecond)

	// Simulate validation
	if mm.validator != nil {
		if err := mm.validator.ValidateCrossReferences(); err != nil {
			mm.failMigration(migration, fmt.Errorf("validation during migration failed: %w", err))
			return
		}
	}

	mm.updateMigrationProgress(migration, 1.0)
	mm.completeMigration(migration)
}

func (mm *ParameterSetMigrationManager) updateMigrationState(migration *ParameterSetMigration, state MigrationState) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	migration.State = state
}

func (mm *ParameterSetMigrationManager) updateMigrationProgress(migration *ParameterSetMigration, progress float64) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	migration.Progress = progress
}

func (mm *ParameterSetMigrationManager) updateValidationStep(migration *ParameterSetMigration, index int, passed bool, err error) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if index < len(migration.ValidationSteps) {
		step := &migration.ValidationSteps[index]
		step.Completed = true
		step.Passed = passed
		step.Timestamp = time.Now()
		if err != nil {
			step.Error = err.Error()
		}
	}
}

func (mm *ParameterSetMigrationManager) completeMigration(migration *ParameterSetMigration) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	migration.State = MigrationStateCompleted
	migration.EndTime = time.Now()
	migration.Progress = 1.0

	mm.successfulMigrations++
	mm.addToHistory(migration)

	if mm.onMigrationComplete != nil {
		mm.onMigrationComplete(migration)
	}

	if mm.activeMigration == migration {
		mm.activeMigration = nil
	}
}

func (mm *ParameterSetMigrationManager) failMigration(migration *ParameterSetMigration, err error) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	migration.State = MigrationStateFailed
	migration.EndTime = time.Now()
	migration.ErrorReason = err.Error()

	mm.failedMigrations++
	mm.addToHistory(migration)

	if mm.onMigrationFailed != nil {
		mm.onMigrationFailed(migration, err)
	}

	if mm.activeMigration == migration {
		mm.activeMigration = nil
	}
}

func (mm *ParameterSetMigrationManager) addToHistory(migration *ParameterSetMigration) {
	mm.migrationHistory = append(mm.migrationHistory, migration)

	// Cleanup old history if needed
	if len(mm.migrationHistory) > mm.maxHistorySize {
		mm.migrationHistory = mm.migrationHistory[len(mm.migrationHistory)-mm.maxHistorySize:]
	}
}
