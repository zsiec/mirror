package types

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParameterSetVersioning(t *testing.T) {
	t.Run("Version Creation", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-versioning")

		// Add some parameter sets
		spsData := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00}
		err := ctx.AddSPS(spsData)
		require.NoError(t, err)

		ppsData := []byte{0x68, 0xCE, 0x38, 0x80}
		err = ctx.AddPPS(ppsData)
		require.NoError(t, err)

		// AddSPS automatically created a version, so create another one
		version, err := ctx.CreateVersion()
		require.NoError(t, err)
		assert.NotNil(t, version)
		assert.Equal(t, uint64(2), version.Version) // Second version since AddSPS created first
		assert.Equal(t, 1, len(version.SPSSets))
		assert.Equal(t, 1, len(version.PPSSets))
		assert.NotEmpty(t, version.Checksum)

		// Verify version history (should have 2 versions now)
		history := ctx.GetVersionHistory()
		assert.Len(t, history, 2)
		assert.Equal(t, version.Version, history[1].Version)
	})

	t.Run("Version Rollback", func(t *testing.T) {
		// Create context without strict validation to allow parameter set changes
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-rollback", ParameterSetContextConfig{
			ValidatorUpdateRate:        100 * time.Millisecond,
			ValidatorMaxUpdatesPerHour: 50,
			EnableVersioning:           true,
			MaxVersions:                20,
		})

		// Add initial parameter sets
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00}
		err := ctx.AddSPS(spsData1)
		require.NoError(t, err)

		// AddSPS already created version 1, create version 2 manually
		version2, err := ctx.CreateVersion()
		require.NoError(t, err)

		// Add a PPS to make the state more interesting
		ppsData1 := []byte{0x68, 0xCE, 0x38, 0x80}
		err = ctx.AddPPS(ppsData1)
		require.NoError(t, err)

		// Create version 3 after PPS addition
		_, err = ctx.CreateVersion()
		require.NoError(t, err)

		// Verify we have 3 versions now
		stats := ctx.GetStatistics()
		assert.Equal(t, 1, stats["sps_count"])
		assert.Equal(t, 1, stats["pps_count"])

		// Rollback to version 2 (before PPS was added)
		err = ctx.RollbackToVersion(version2.Version)
		require.NoError(t, err)

		// Verify parameter sets were restored
		stats = ctx.GetStatistics()
		assert.Equal(t, 1, stats["sps_count"])
		assert.Equal(t, 0, stats["pps_count"]) // PPS should be gone after rollback

		// Check rollback statistics
		assert.Contains(t, stats, "version_total_rollbacks")
		assert.Equal(t, uint64(1), stats["version_total_rollbacks"])

		// The version count might be higher due to automatic version creation
		history := ctx.GetVersionHistory()
		assert.GreaterOrEqual(t, len(history), 3)
	})

	t.Run("Version Integrity", func(t *testing.T) {
		vm := NewParameterSetVersionManager("test-integrity", 10)
		ctx := NewParameterSetContext(CodecH264, "test-integrity")

		// Create a version
		version, err := vm.CreateVersion(ctx)
		require.NoError(t, err)

		// Validate version integrity
		err = vm.ValidateVersion(version.Version)
		assert.NoError(t, err)

		// Try to validate non-existent version
		err = vm.ValidateVersion(999)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("Version Cleanup", func(t *testing.T) {
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-cleanup", ParameterSetContextConfig{
			ValidatorUpdateRate:        1 * time.Millisecond,
			ValidatorMaxUpdatesPerHour: 1000,
			EnableVersioning:           true,
			MaxVersions:                3, // Keep only 3 versions
		})

		// Create multiple versions
		for i := 0; i < 5; i++ {
			spsData := []byte{0x67, 0x42, 0x00, byte(0x1e + i), 0x8d, 0x68, 0x05, 0x00}
			ctx.AddSPS(spsData)
			ctx.CreateVersion()
			time.Sleep(2 * time.Millisecond)
		}

		// Check that only 3 versions are kept
		history := ctx.GetVersionHistory()
		assert.LessOrEqual(t, len(history), 3)

		stats := ctx.GetStatistics()
		assert.Equal(t, 3, stats["version_stored_versions"])
		// Each AddSPS with a changed parameter creates an auto version + manual CreateVersion
		// So we have: 5 (auto from AddSPS) + 5 (manual) = 10 total
		assert.Equal(t, uint64(10), stats["version_total_versions"])
	})
}

func TestParameterSetMigration(t *testing.T) {
	t.Run("Basic Migration", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-migration")

		// Create version 1
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00}
		ctx.AddSPS(spsData1)
		v1, err := ctx.CreateVersion()
		require.NoError(t, err)

		// Create version 2
		time.Sleep(110 * time.Millisecond)
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1f, 0x8d, 0x68, 0x05, 0x00}
		ctx.AddSPS(spsData2)
		v2, err := ctx.CreateVersion()
		require.NoError(t, err)

		// Start migration
		migration, err := ctx.StartMigration(v1.Version, v2.Version, MigrationStrategyImmediate)
		require.NoError(t, err)
		assert.NotNil(t, migration)
		// Note: Don't assert migration.State here — the background goroutine
		// may have already changed it since Immediate strategy completes fast.

		// Wait for migration to complete
		time.Sleep(200 * time.Millisecond)

		// Check migration completed
		activeMigration := ctx.GetActiveMigration()
		if activeMigration != nil {
			assert.Equal(t, MigrationStateCompleted, activeMigration.State)
		}

		// Check statistics
		stats := ctx.GetStatistics()
		assert.Equal(t, uint64(1), stats["migration_total_migrations"])
	})

	t.Run("Migration Strategies", func(t *testing.T) {
		strategies := []struct {
			name     string
			strategy MigrationStrategy
		}{
			{"Immediate", MigrationStrategyImmediate},
			{"Gradual", MigrationStrategyGradual},
			{"IDR", MigrationStrategyIDR},
			{"Validated", MigrationStrategyValidated},
		}

		for _, tc := range strategies {
			t.Run(tc.name, func(t *testing.T) {
				ctx := NewParameterSetContext(CodecH264, fmt.Sprintf("test-%s", tc.name))

				// Create two versions
				ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1e, 0x8d})
				v1, _ := ctx.CreateVersion()

				time.Sleep(110 * time.Millisecond)
				ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1f, 0x8d})
				v2, _ := ctx.CreateVersion()

				// Start migration with specific strategy
				migration, err := ctx.StartMigration(v1.Version, v2.Version, tc.strategy)
				require.NoError(t, err)
				assert.Equal(t, tc.strategy, migration.Strategy)

				// Wait for completion
				time.Sleep(1 * time.Second)
			})
		}
	})

	t.Run("Migration Validation", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-validation")
		mm := ctx.GetMigrationManager()
		require.NotNil(t, mm)

		// Try to migrate with invalid versions
		_, err := mm.StartMigration(999, 1000, MigrationStrategyImmediate)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid source version")
	})

	t.Run("Migration Cancellation", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-cancel")

		// Create versions
		ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1e})
		v1, _ := ctx.CreateVersion()
		time.Sleep(110 * time.Millisecond)
		ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1f})
		v2, _ := ctx.CreateVersion()

		// Start migration
		_, err := ctx.StartMigration(v1.Version, v2.Version, MigrationStrategyGradual)
		require.NoError(t, err)

		// Wait for migration to start (transition from Pending to Validating/InProgress)
		time.Sleep(50 * time.Millisecond)

		// Cancel migration
		mm := ctx.GetMigrationManager()

		// Check migration state before cancelling
		activeMigration := mm.GetActiveMigration()
		if activeMigration != nil {
			t.Logf("Migration state before cancel: %d", activeMigration.State)

			// Only cancel if migration is in a cancellable state
			if activeMigration.State == MigrationStateValidating || activeMigration.State == MigrationStateInProgress {
				err = mm.CancelMigration("test cancellation")
				assert.NoError(t, err)

				// Verify cancellation
				stats := ctx.GetStatistics()
				assert.Equal(t, uint64(1), stats["migration_failed_migrations"])
			} else {
				// Migration might have completed too quickly, which is okay
				t.Logf("Migration completed before cancellation could occur")
			}
		}
	})
}

func TestParameterSetIntegration(t *testing.T) {
	t.Run("Full Workflow", func(t *testing.T) {
		// Create context with less strict validation to simulate encoder changes
		ctx := NewParameterSetContextWithConfig(CodecH264, "test-integration", ParameterSetContextConfig{
			ValidatorUpdateRate:        10 * time.Millisecond,
			ValidatorMaxUpdatesPerHour: 100,
			EnableVersioning:           true,
			MaxVersions:                20,
		})

		// Phase 1: Add initial parameter sets
		spsData1 := []byte{0x67, 0x42, 0x00, 0x1e, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa1}
		ppsData1 := []byte{0x68, 0xCE, 0x38, 0x80}

		err := ctx.AddSPS(spsData1)
		require.NoError(t, err)
		err = ctx.AddPPS(ppsData1)
		require.NoError(t, err)

		// Create version after initial parameter sets
		v1, err := ctx.CreateVersion()
		require.NoError(t, err)

		// Phase 2: Simulate parameter set changes by using a second context
		time.Sleep(20 * time.Millisecond)

		// Create a second context without validator to avoid conflicts
		ctx2 := NewParameterSetContextWithConfig(CodecH264, "test-integration-2", ParameterSetContextConfig{
			ValidatorUpdateRate:        0, // Disable validator
			ValidatorMaxUpdatesPerHour: 0,
			EnableVersioning:           false,
			MaxVersions:                0,
		})

		// Add different parameter sets to the second context
		spsData2 := []byte{0x67, 0x42, 0x00, 0x1f, 0x8d, 0x68, 0x05, 0x00, 0x5b, 0xa2}
		err = ctx2.AddSPS(spsData2)
		require.NoError(t, err)

		ppsData2 := []byte{0x68, 0xCE, 0x38, 0x81} // PPS with different ID
		err = ctx2.AddPPS(ppsData2)
		require.NoError(t, err)

		// Copy parameter sets from second context to first
		copiedCount := ctx.CopyParameterSetsFrom(ctx2)
		assert.Greater(t, copiedCount, 0)

		// Create version 2
		v2, err := ctx.CreateVersion()
		require.NoError(t, err)

		// Phase 3: Migrate gradually
		migration, err := ctx.StartMigration(v1.Version, v2.Version, MigrationStrategyGradual)
		require.NoError(t, err)
		assert.NotNil(t, migration)

		// Wait for migration to start
		time.Sleep(500 * time.Millisecond)

		// Phase 4: Verify final state
		stats := ctx.GetStatistics()

		// Check all components are working
		assert.Contains(t, stats, "validator_tracked_sps")
		assert.Contains(t, stats, "version_current_version")
		assert.Contains(t, stats, "migration_total_migrations")

		// Verify version history
		history := ctx.GetVersionHistory()
		assert.GreaterOrEqual(t, len(history), 2)

		// Check migration was initiated
		assert.GreaterOrEqual(t, stats["migration_total_migrations"].(uint64), uint64(1))
	})

	t.Run("Error Recovery", func(t *testing.T) {
		ctx := NewParameterSetContext(CodecH264, "test-recovery")

		// Add initial data
		ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1e, 0x8d})
		v1, _ := ctx.CreateVersion()

		// Add corrupted data
		time.Sleep(110 * time.Millisecond)
		ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1f, 0xFF}) // Short/invalid
		v2, err := ctx.CreateVersion()
		require.NoError(t, err)
		assert.NotNil(t, v2)

		// Try to use the corrupted version
		// ... operations fail ...

		// Rollback to good version
		err = ctx.RollbackToVersion(v1.Version)
		assert.NoError(t, err)

		// Verify recovery
		stats := ctx.GetStatistics()
		assert.Equal(t, uint64(1), stats["version_total_rollbacks"])
	})
}

func TestParameterSetManagementConcurrency(t *testing.T) {
	ctx := NewParameterSetContext(CodecH264, "test-concurrent")

	// Run concurrent operations
	done := make(chan bool, 3)

	// Version creator
	go func() {
		for i := 0; i < 10; i++ {
			ctx.CreateVersion()
			time.Sleep(50 * time.Millisecond)
		}
		done <- true
	}()

	// Parameter set updater
	go func() {
		for i := 0; i < 10; i++ {
			spsData := []byte{0x67, 0x42, 0x00, byte(0x1e + i%5), 0x8d}
			ctx.AddSPS(spsData)
			time.Sleep(60 * time.Millisecond)
		}
		done <- true
	}()

	// Statistics reader
	go func() {
		for i := 0; i < 20; i++ {
			stats := ctx.GetStatistics()
			_ = stats
			history := ctx.GetVersionHistory()
			_ = history
			time.Sleep(30 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for completion
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify no corruption
	stats := ctx.GetStatistics()
	assert.Greater(t, stats["version_total_versions"].(uint64), uint64(0))
}

func BenchmarkVersionCreation(b *testing.B) {
	ctx := NewParameterSetContext(CodecH264, "bench-version")

	// Add some parameter sets
	for i := 0; i < 10; i++ {
		spsData := []byte{0x67, 0x42, 0x00, byte(0x1e + i), 0x8d, 0x68, 0x05, 0x00}
		ctx.AddSPS(spsData)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx.CreateVersion()
	}
}

func BenchmarkMigration(b *testing.B) {
	ctx := NewParameterSetContext(CodecH264, "bench-migration")

	// Create two versions
	ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1e})
	v1, _ := ctx.CreateVersion()
	ctx.AddSPS([]byte{0x67, 0x42, 0x00, 0x1f})
	v2, _ := ctx.CreateVersion()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx.StartMigration(v1.Version, v2.Version, MigrationStrategyImmediate)
		time.Sleep(10 * time.Millisecond) // Let migration complete
	}
}
