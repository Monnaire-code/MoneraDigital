package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"monera-digital/internal/migration"
)

// Interface/Version/Description tests for the new C-2 migration 046.
// The full SQL behaviour is exercised by running `go run ./cmd/migrate`
// against a real (or test) database; sqlmock coverage of every
// statement would duplicate the integration value without catching
// additional regressions.

func TestAddPendingStatusAndActivationFields_Interface(t *testing.T) {
	var _ migration.Migration = (*AddPendingStatusAndActivationFields)(nil)
}

func TestAddPendingStatusAndActivationFields_Version(t *testing.T) {
	m := &AddPendingStatusAndActivationFields{}
	if v := m.Version(); v != "046" {
		t.Errorf("Version() = %q, want %q", v, "046")
	}
}

func TestAddPendingStatusAndActivationFields_Description(t *testing.T) {
	m := &AddPendingStatusAndActivationFields{}
	if m.Description() == "" {
		t.Error("Description should not be empty")
	}
}

func TestNormalizeAmountTypes_Interface(t *testing.T) {
	var _ migration.Migration = (*NormalizeAmountTypes)(nil)
}

func TestNormalizeAmountTypes_Version(t *testing.T) {
	m := &NormalizeAmountTypes{}
	if v := m.Version(); v != "047" {
		t.Errorf("Version() = %q, want %q", v, "047")
	}
}

func TestNormalizeAmountTypes_Description(t *testing.T) {
	m := &NormalizeAmountTypes{}
	if m.Description() == "" {
		t.Error("Description should not be empty")
	}
}

func TestAddMissingForeignKeys_Interface(t *testing.T) {
	var _ migration.Migration = (*AddMissingForeignKeys)(nil)
}

func TestAddMissingForeignKeys_Version(t *testing.T) {
	m := &AddMissingForeignKeys{}
	if v := m.Version(); v != "048" {
		t.Errorf("Version() = %q, want %q", v, "048")
	}
}

func TestAddMissingForeignKeys_Description(t *testing.T) {
	m := &AddMissingForeignKeys{}
	if m.Description() == "" {
		t.Error("Description should not be empty")
	}
}

func TestWidenAmountPrecision_InterfaceAndVersion(t *testing.T) {
	var _ migration.Migration = (*WidenAmountPrecision)(nil)
	if version := (&WidenAmountPrecision{}).Version(); version != "051" {
		t.Fatalf("Version() = %q, want 051", version)
	}
}

func TestExpandCompanyFundOccurrenceAndManualValuationInterfaceAndVersion(t *testing.T) {
	var _ migration.Migration = (*ExpandCompanyFundOccurrenceAndManualValuation)(nil)
	if version := (&ExpandCompanyFundOccurrenceAndManualValuation{}).Version(); version != "052" {
		t.Fatalf("Version() = %q, want 052", version)
	}
}

func TestEnforceSafeheronOccurrenceInterfaceAndVersion(t *testing.T) {
	var _ migration.Migration = (*EnforceSafeheronOccurrence)(nil)
	var _ migration.ControlledMigration = (*EnforceSafeheronOccurrence)(nil)
	if version := (&EnforceSafeheronOccurrence{}).Version(); version != "053" {
		t.Fatalf("Version() = %q, want 053", version)
	}
}

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

// TestMigrationOrder verifies all migrations are properly ordered and
// has a unique version per entry. Keep this list in sync with the
// registerMigrations() function in cmd/migrate/main.go — a CI
// guard in scripts/check-secrets.sh asserts both are aligned.
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
		{"CreateUserWalletsTable", "008"},
		{"AddUserWalletStatusField", "009"},
		{"AddIsPrimaryToWhitelist", "010"},
		{"CreateDepositsTable", "011"},
		{"AddUserStatus", "012"},
		{"AddFrozenUntilToWhitelist", "013"},
		{"AddEmailVerifiedStatusAndContactFields", "014"},
		{"SafeheronPhase1", "015"},
		{"AccountFrozenBalanceDefault", "016"},
		{"AddPendingStatusAndActivationFields", "046"},
		{"NormalizeAmountTypes", "047"},
		{"AddMissingForeignKeys", "048"},
		{"CreateFundReports", "049"},
		{"CreateCompanyFundLedger", "050"},
		{"WidenAmountPrecision", "051"},
		{"ExpandCompanyFundOccurrenceAndManualValuation", "052"},
		{"EnforceSafeheronOccurrence", "053"},
	}

	seen := make(map[string]bool, len(migrations))
	for i, m := range migrations {
		t.Run(m.name, func(t *testing.T) {
			if m.version == "" {
				t.Error("Version should not be empty")
			}
			if seen[m.version] {
				t.Errorf("Duplicate version %q in migration list", m.version)
			}
			seen[m.version] = true
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

func TestMigrationRunnerRegistersVersionsInOrder(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	source, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "migrate", "main.go"))
	if err != nil {
		t.Fatalf("read migration runner: %v", err)
	}

	previous := -1
	for _, registration := range []string{
		"m.Register(&migrations.AddPendingStatusAndActivationFields{})",
		"m.Register(&migrations.NormalizeAmountTypes{})",
		"m.Register(&migrations.AddMissingForeignKeys{})",
		"m.Register(&migrations.CreateFundReports{})",
		"m.Register(&migrations.CreateCompanyFundLedger{})",
		"m.Register(&migrations.WidenAmountPrecision{})",
		"m.Register(&migrations.ExpandCompanyFundOccurrenceAndManualValuation{})",
		"m.Register(&migrations.EnforceSafeheronOccurrence{})",
	} {
		position := strings.Index(string(source), registration)
		if position < 0 {
			t.Errorf("registerMigrations is missing %q", registration)
			continue
		}
		if position <= previous {
			t.Errorf("registerMigrations entry %q is not in version order", registration)
		}
		previous = position
	}
}
