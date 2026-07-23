package migrations

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/migration"
)

func TestEnforceSafeheronOccurrenceMetadataAndDDLContract(t *testing.T) {
	t.Parallel()
	var _ migration.Migration = (*EnforceSafeheronOccurrence)(nil)
	var _ migration.ControlledMigration = (*EnforceSafeheronOccurrence)(nil)
	migrationB := &EnforceSafeheronOccurrence{}
	if migrationB.Version() != "053" || migrationB.Description() == "" {
		t.Fatalf("metadata = %q %q", migrationB.Version(), migrationB.Description())
	}
	if migrationB.RequiredPreexistingVersion() != "052" || migrationB.RequiredExpectedCeiling() != "053" {
		t.Fatalf("control boundary = %q / %q", migrationB.RequiredPreexistingVersion(), migrationB.RequiredExpectedCeiling())
	}
	if err := migrationB.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatalf("Down() = %v", err)
	}
	if err := migrationB.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up() = %v", err)
	}

	for _, required := range []string{
		"channel <> 'SAFEHERON'", "provider_occurrence_key IS NOT NULL", "btrim(provider_occurrence_key) <> ''",
		"provider_occurrence_algorithm_version = 'safeheron-occurrence-v1'", "NOT VALID", "VALIDATE CONSTRAINT",
	} {
		if !strings.Contains(migration053AddConstraintSQL+migration053ValidateConstraintSQL, required) {
			t.Errorf("Migration B DDL missing %q", required)
		}
	}
	if strings.Contains(migration053AddConstraintSQL, "channel = 'AIRWALLEX'") {
		t.Fatal("Migration B constrains Airwallex")
	}
	for _, required := range []string{"missing_count", "wrong_version_count", "duplicate_count", "invariant_count", "channel = 'SAFEHERON'", "safeheron-occurrence-v1:[0-9a-f]{64}"} {
		if !strings.Contains(migration053PreflightSQL, required) {
			t.Errorf("Migration B preflight missing %q", required)
		}
	}
}

func TestMigration053IntegrationGatePrecedesEnvironmentAndDatabaseOpen(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("053_enforce_safeheron_occurrence_integration_test.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	gate := strings.Index(text, `os.Getenv("RUN_COMPANY_FUND_MIGRATION_053_INTEGRATION")`)
	databaseURL := strings.Index(text, `os.Getenv("DATABASE_URL")`)
	open := strings.Index(text, `sql.Open("pgx", databaseURL)`)
	if gate < 0 || databaseURL <= gate || open <= databaseURL {
		t.Fatalf("integration gate ordering gate=%d env=%d open=%d", gate, databaseURL, open)
	}
}

func TestMigration053UpTxValidatesConstraintInsideRunnerOwnedTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	expectMigration053Success(mock)
	mock.ExpectCommit()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&EnforceSafeheronOccurrence{}).UpTx(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration053RunnerCommitsDDLAndProvenanceAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrator := migration.NewMigrator(db)
	migrationB := &EnforceSafeheronOccurrence{}
	migrator.Register(migrationB)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).WithArgs(int64(8675309)).WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "version", "name", "executed_at"}).AddRow(1, "052", "A", time.Now()),
	)
	mock.ExpectBegin()
	expectMigration053Success(mock)
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", migrationB.Description()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := migrator.MigrateWithExpectedCeiling("053"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration053PreflightRejectsEveryUnsafeStateBeforeDDL(t *testing.T) {
	for _, testCase := range []struct {
		name                                 string
		missing, wrong, duplicate, invariant int64
	}{
		{name: "missing", missing: 1},
		{name: "wrong version", wrong: 1},
		{name: "duplicate", duplicate: 1},
		{name: "invariant", invariant: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(migration053TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(migration053PreflightSQL)).WillReturnRows(migration053PreflightRows(testCase.missing, testCase.wrong, testCase.duplicate, testCase.invariant))
			mock.ExpectRollback()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := (&EnforceSafeheronOccurrence{}).UpTx(tx); err == nil || !strings.Contains(err.Error(), "preflight rejected") {
				t.Fatalf("UpTx() = %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration053EveryDDLFailureReturnsToRunnerForRollback(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		setup      func(sqlmock.Sqlmock)
	}{
		{name: "timeout", want: "timeouts", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(migration053TimeoutsSQL)).WillReturnError(errors.New("timeout failed"))
			mock.ExpectRollback()
		}},
		{name: "preflight", want: "preflight", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(migration053TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(migration053PreflightSQL)).WillReturnError(errors.New("preflight failed"))
			mock.ExpectRollback()
		}},
		{name: "add", want: "add required", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			expectMigration053Preflight(mock)
			mock.ExpectExec(regexp.QuoteMeta(migration053AddConstraintSQL)).WillReturnError(errors.New("add failed"))
			mock.ExpectRollback()
		}},
		{name: "validate", want: "validate required", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectBegin()
			expectMigration053Preflight(mock)
			mock.ExpectExec(regexp.QuoteMeta(migration053AddConstraintSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(migration053ValidateConstraintSQL)).WillReturnError(errors.New("validate failed"))
			mock.ExpectRollback()
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			testCase.setup(mock)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := (&EnforceSafeheronOccurrence{}).UpTx(tx); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("UpTx() = %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func expectMigration053Preflight(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta(migration053TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration053PreflightSQL)).WillReturnRows(migration053PreflightRows(0, 0, 0, 0))
}

func expectMigration053Success(mock sqlmock.Sqlmock) {
	expectMigration053Preflight(mock)
	mock.ExpectExec(regexp.QuoteMeta(migration053AddConstraintSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration053ValidateConstraintSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
}

func migration053PreflightRows(missing, wrong, duplicate, invariant int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"missing_count", "wrong_version_count", "duplicate_count", "invariant_count"}).AddRow(missing, wrong, duplicate, invariant)
}
