package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"monera-digital/internal/migration"
)

func TestMigrationBArtifactCeilingIs053(t *testing.T) {
	t.Parallel()
	migrator := migration.NewMigrator(nil)
	registerMigrations(migrator)
	if got := migrator.Ceiling(); got != "053" {
		t.Fatalf("registered migration ceiling = %q, want 053", got)
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
	if artifactMigrationCeiling != "053" {
		t.Fatalf("current checkpoint B tree compiled ceiling = %q", artifactMigrationCeiling)
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
