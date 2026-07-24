package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"monera-digital/internal/migration"
)

func TestExactVersionPrintVersionsRegistersOnlyRequestedMigration(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "run", ".", "-print-versions", "-exact-version", "050")
	cmd.Env = append(os.Environ(), "APP_ENV=production")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exact-version CLI failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != `["050"]` {
		t.Fatalf("exact-version registration = %s, want [\"050\"]", got)
	}
}

func TestExactVersionPrintCeilingMatchesSelection(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "run", ".", "-print-ceiling", "-exact-version", "051")
	cmd.Env = append(os.Environ(), "APP_ENV=production")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exact-version ceiling CLI failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "051" {
		t.Fatalf("exact-version ceiling = %s, want 051", got)
	}
}

func TestExactVersionRejectsRollbackBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "run", ".", "-rollback", "-exact-version", "050")
	cmd.Env = append(os.Environ(), "APP_ENV=production", "DATABASE_URL=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rollback with exact-version unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "exact-version cannot be combined with rollback") {
		t.Fatalf("rollback rejection = %s", out)
	}
}

func TestExactVersionRequiresMatchingCeilingBeforeOpeningDatabase(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "run", ".", "-dry-run", "-exact-version", "050")
	cmd.Env = append(os.Environ(), "APP_ENV=production", "DATABASE_URL=", "EXPECTED_MIGRATION_CEILING=")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("exact-version without ceiling unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "requires EXPECTED_MIGRATION_CEILING=050") {
		t.Fatalf("ceiling rejection = %s", out)
	}
}

func TestExactMigrationOptionsRequireImmediatePredecessor(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		version     string
		predecessor string
	}{
		{version: "050", predecessor: "049"},
		{version: "051", predecessor: "050"},
		{version: "052", predecessor: "051"},
		{version: "053", predecessor: "052"},
		{version: "054", predecessor: "053"},
		{version: "055", predecessor: "054"},
		{version: "056", predecessor: "055"},
		{version: "057", predecessor: "056"},
		{version: "058", predecessor: "057"},
		{version: "059", predecessor: "058"},
		{version: "060", predecessor: "059"},
	} {
		got, err := validateExactMigrationOptions(testCase.version, testCase.version, false)
		if err != nil {
			t.Fatalf("validate %s: %v", testCase.version, err)
		}
		if got != testCase.predecessor {
			t.Fatalf("migration %s predecessor = %s, want %s", testCase.version, got, testCase.predecessor)
		}
	}
	if _, err := validateExactMigrationOptions("049", "049", false); err == nil {
		t.Fatal("historical migration accepted by exact production mode")
	}
	if predecessor, err := validateExactMigrationOptions("", "", false); err != nil || predecessor != "" {
		t.Fatalf("default migration mode changed: predecessor=%q err=%v", predecessor, err)
	}
}

func TestExactMigrationOptionsRejectRollbackAndMismatchedCeiling(t *testing.T) {
	t.Parallel()
	if _, err := validateExactMigrationOptions("050", "050", true); err == nil || !strings.Contains(err.Error(), "cannot be combined with rollback") {
		t.Fatalf("rollback rejection = %v", err)
	}
	if _, err := validateExactMigrationOptions("050", "051", false); err == nil || !strings.Contains(err.Error(), "requires EXPECTED_MIGRATION_CEILING=050") {
		t.Fatalf("ceiling rejection = %v", err)
	}
}

func TestRequireAppliedMigrationRejectsSparsePredecessorGap(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs("049").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	if err := requireAppliedMigration(db, "049"); err == nil || !strings.Contains(err.Error(), "must be applied") {
		t.Fatalf("missing predecessor error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRequireAppliedMigrationAcceptsRecordedPredecessor(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs("049").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	if err := requireAppliedMigration(db, "049"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRequireAppliedMigrationPropagatesLookupFailure(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(`SELECT EXISTS`).WithArgs("049").WillReturnError(errors.New("database unavailable"))
	if err := requireAppliedMigration(db, "049"); err == nil || !strings.Contains(err.Error(), "query migration 049 provenance") {
		t.Fatalf("lookup failure = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestExactMigrationRegistrationContainsOnlyRequestedVersion(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"050", "051", "052", "053", "054", "055", "056", "057", "058", "059", "060"} {
		migrator := migration.NewMigrator(nil)
		if err := registerSelectedMigrations(migrator, version); err != nil {
			t.Fatalf("register %s: %v", version, err)
		}
		got := migrator.RegisteredVersions()
		if len(got) != 1 || got[0] != version {
			t.Fatalf("exact migration %s registered %v", version, got)
		}
	}
}

func TestExactMigrationRegistrationRejectsHistoricalAndUnknownVersions(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"049", "061", "latest"} {
		if err := registerSelectedMigrations(migration.NewMigrator(nil), version); err == nil {
			t.Fatalf("exact migration %q accepted", version)
		}
	}
}

func TestDefaultMigrationSelectionRegistersCurrentArtifact(t *testing.T) {
	t.Parallel()
	migrator := migration.NewMigrator(nil)
	if err := registerSelectedMigrations(migrator, ""); err != nil {
		t.Fatal(err)
	}
	if got := migrator.Ceiling(); got != artifactMigrationCeiling {
		t.Fatalf("default migration ceiling = %q, want %q", got, artifactMigrationCeiling)
	}
}

func TestCurrentArtifactCeilingIs060(t *testing.T) {
	t.Parallel()
	migrator := migration.NewMigrator(nil)
	registerMigrations(migrator)
	if got := migrator.Ceiling(); got != "060" {
		t.Fatalf("registered migration ceiling = %q, want 060", got)
	}
}

func TestArtifactMigrationCeilingControlsRegistrationAndCannotBeRuntimeExpanded(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		ceiling string
		want    string
	}{
		{ceiling: "052", want: "052"},
		{ceiling: "053", want: "053"},
		{ceiling: "054", want: "054"},
		{ceiling: "055", want: "055"},
		{ceiling: "056", want: "056"},
		{ceiling: "057", want: "057"},
		{ceiling: "058", want: "058"},
		{ceiling: "059", want: "059"},
		{ceiling: "060", want: "060"},
	} {
		migrator := migration.NewMigrator(nil)
		if err := registerMigrationsForArtifact(migrator, testCase.ceiling); err != nil {
			t.Fatalf("register %s: %v", testCase.ceiling, err)
		}
		if got := migrator.Ceiling(); got != testCase.want {
			t.Fatalf("artifact %s registered ceiling %s", testCase.ceiling, got)
		}
	}
	if err := registerMigrationsForArtifact(migration.NewMigrator(nil), "051"); err == nil {
		t.Fatal("unsupported artifact migration ceiling accepted")
	}
	if artifactMigrationCeiling != "060" {
		t.Fatalf("current tree compiled ceiling = %q", artifactMigrationCeiling)
	}
}

func TestArtifactMigrationRegistrationManifestIsCompleteOrderedAndImmutable(t *testing.T) {
	wantA := []string{"001", "002", "003", "004", "005", "007", "008", "009", "010", "011", "012", "013", "014", "015", "016", "046", "047", "048", "049", "050", "051", "052"}
	for _, testCase := range []struct {
		ceiling string
		want    []string
	}{
		{ceiling: "052", want: wantA},
		{ceiling: "053", want: append(append([]string(nil), wantA...), "053")},
		{ceiling: "054", want: append(append([]string(nil), wantA...), "053", "054")},
		{ceiling: "055", want: append(append([]string(nil), wantA...), "053", "054", "055")},
		{ceiling: "056", want: append(append([]string(nil), wantA...), "053", "054", "055", "056")},
		{ceiling: "057", want: append(append([]string(nil), wantA...), "053", "054", "055", "056", "057")},
		{ceiling: "058", want: append(append([]string(nil), wantA...), "053", "054", "055", "056", "057", "058")},
		{ceiling: "059", want: append(append([]string(nil), wantA...), "053", "054", "055", "056", "057", "058", "059")},
		{ceiling: "060", want: append(append([]string(nil), wantA...), "053", "054", "055", "056", "057", "058", "059", "060")},
	} {
		migrator := migration.NewMigrator(nil)
		if err := registerMigrationsForArtifact(migrator, testCase.ceiling); err != nil {
			t.Fatal(err)
		}
		got := migrator.RegisteredVersions()
		if fmt.Sprint(got) != fmt.Sprint(testCase.want) {
			t.Fatalf("ceiling %s versions = %v, want %v", testCase.ceiling, got, testCase.want)
		}
		got[0] = "mutated"
		if migrator.RegisteredVersions()[0] != "001" {
			t.Fatal("RegisteredVersions exposed mutable internal state")
		}
	}
}

func TestMigrationBIsIndependentFromCheckpointA(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("..", "..", "internal", "migration", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	foundA, foundB := false, false
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		foundA = foundA || strings.HasPrefix(name, "052_expand_company_fund_occurrence")
		foundB = foundB || strings.HasPrefix(name, "053_enforce_safeheron_occurrence")
	}
	if !foundA || !foundB {
		t.Fatalf("independent migration files A=%t B=%t", foundA, foundB)
	}
}

func TestMigrationFailureExitCodeIsDedicatedOnlyToIndeterminateControlledCommit(t *testing.T) {
	commitFailure := errors.New("connection lost while committing")
	indeterminate := &migration.ControlledCommitOutcomeIndeterminateError{Version: "053", Err: commitFailure}
	if got := migrationFailureExitCode(fmt.Errorf("migration failed: %w", indeterminate)); got != controlledCommitOutcomeIndeterminateExitCode {
		t.Fatalf("indeterminate exit = %d", got)
	}
	if got := migrationFailureExitCode(errors.New("preflight rejected")); got != 1 {
		t.Fatalf("ordinary failure exit = %d", got)
	}
}

func TestMigrateCLI_RejectsPoolerMigrationURL(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "run", ".", "-dry-run")
	cmd.Env = append(os.Environ(),
		"APP_ENV=production",
		"MIGRATION_DATABASE_URL=postgresql://u:secret-pass@ep-foo-pooler.example.com/db?sslmode=require",
		"DATABASE_URL=",
		"EXPECTED_MIGRATION_CEILING=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected pooler reject, got success:\n%s", out)
	}
	msg := string(out)
	if !strings.Contains(strings.ToLower(msg), "pooler") {
		t.Fatalf("expected pooler wording in output:\n%s", msg)
	}
	if strings.Contains(msg, "secret-pass") {
		t.Fatalf("password leaked in CLI output:\n%s", msg)
	}
}

func TestMigrateCLI_RequiresDedicatedURLOnProduction(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "run", ".", "-dry-run")
	cmd.Env = append(os.Environ(),
		"APP_ENV=production",
		"MIGRATION_DATABASE_URL=",
		"DATABASE_URL=postgresql://u:p@localhost:5432/db",
		"EXPECTED_MIGRATION_CEILING=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing MIGRATION_DATABASE_URL failure:\n%s", out)
	}
	if !strings.Contains(string(out), "MIGRATION_DATABASE_URL") {
		t.Fatalf("output should mention required var:\n%s", out)
	}
}
