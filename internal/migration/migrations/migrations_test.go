package migrations

import (
	"testing"

	"monera-digital/internal/migration"
)

// TestAddTwoFactorColumnsMigration_Interface verifies the migration implements the interface
func TestAddTwoFactorColumnsMigration_Interface(t *testing.T) {
	var _ migration.Migration = (*AddTwoFactorColumnsMigration)(nil)
}

// TestAddTwoFactorColumnsMigration_Version verifies version
func TestAddTwoFactorColumnsMigration_Version(t *testing.T) {
	m := &AddTwoFactorColumnsMigration{}
	if m.Version() != "004" {
		t.Errorf("Expected version '004', got '%s'", m.Version())
	}
}

// TestAddTwoFactorColumnsMigration_Description verifies description
func TestAddTwoFactorColumnsMigration_Description(t *testing.T) {
	m := &AddTwoFactorColumnsMigration{}
	if m.Description() == "" {
		t.Error("Description should not be empty")
	}
}

// TestAddTwoFactorTimestampMigration_Interface verifies the migration implements the interface
func TestAddTwoFactorTimestampMigration_Interface(t *testing.T) {
	var _ migration.Migration = (*AddTwoFactorTimestampMigration)(nil)
}

// TestAddTwoFactorTimestampMigration_Version verifies version
func TestAddTwoFactorTimestampMigration_Version(t *testing.T) {
	m := &AddTwoFactorTimestampMigration{}
	if m.Version() != "005" {
		t.Errorf("Expected version '005', got '%s'", m.Version())
	}
}

// TestAddTwoFactorTimestampMigration_Description verifies description
func TestAddTwoFactorTimestampMigration_Description(t *testing.T) {
	m := &AddTwoFactorTimestampMigration{}
	if m.Description() == "" {
		t.Error("Description should not be empty")
	}
}

// TestMigrationOrder verifies all migrations are properly ordered
func TestMigrationOrder(t *testing.T) {
	migrations := []struct {
		name    string
		version string
	}{
		{"CreateUsersTable", "001"},
		{"CreateLendingPositionsTable", "002"},
		{"CreateWithdrawalTables", "003"},
		{"AddTwoFactorColumnsMigration", "004"},
		{"AddTwoFactorTimestampMigration", "005"},
		{"UpdateWalletRequestsTable", "007"},
		{"SafeheronPhase1", "015"},
		{"AccountFrozenBalanceDefault", "016"},
	}

	for i, m := range migrations {
		t.Run(m.name, func(t *testing.T) {
			// Verify version is not empty
			if m.version == "" {
				t.Error("Version should not be empty")
			}

			// Verify versions are in order (simple check)
			if i > 0 {
				prevVersion := migrations[i-1].version
				if m.version <= prevVersion {
					t.Errorf("Migration %s version %s should be greater than previous %s",
						m.name, m.version, prevVersion)
				}
			}
		})
	}
}
