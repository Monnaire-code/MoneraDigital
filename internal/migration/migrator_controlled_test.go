package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type controlledTestMigration struct {
	version, prior, ceiling string
	calls                   *int
	upTx                    func(*sql.Tx) error
}

type plainTestMigration struct {
	version string
	calls   *int
	upErr   error
}

func (migration plainTestMigration) Version() string     { return migration.version }
func (migration plainTestMigration) Description() string { return migration.version }
func (migration plainTestMigration) Down(*sql.DB) error  { return nil }
func (migration plainTestMigration) Up(*sql.DB) error {
	*migration.calls++
	return migration.upErr
}

func (migration controlledTestMigration) Version() string     { return migration.version }
func (migration controlledTestMigration) Description() string { return migration.version }
func (migration controlledTestMigration) Down(*sql.DB) error  { return nil }
func (migration controlledTestMigration) RequiredPreexistingVersion() string {
	return migration.prior
}
func (migration controlledTestMigration) RequiredExpectedCeiling() string {
	return migration.ceiling
}
func (migration controlledTestMigration) Up(*sql.DB) error {
	*migration.calls++
	return nil
}
func (migration controlledTestMigration) UpTx(tx *sql.Tx) error {
	*migration.calls++
	if migration.upTx != nil {
		return migration.upTx(tx)
	}
	return nil
}

func TestControlledMigrationRequiresExactArtifactCeilingBeforeDatabaseAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	migrator := NewMigrator(db)
	migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: &calls})
	if err := migrator.MigrateWithExpectedCeiling("052"); err == nil || !strings.Contains(err.Error(), "registered ceiling") {
		t.Fatalf("MigrateWithExpectedCeiling() = %v", err)
	}
	if calls != 0 {
		t.Fatalf("controlled migration ran %d times", calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledMigrationCannotApplyWithCheckpointInSameInvocation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	aCalls, bCalls := 0, 0
	migrator := NewMigrator(db)
	migrator.Register(plainTestMigration{version: "052", calls: &aCalls})
	migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: &bCalls})
	expectMigratorStart(mock, nil)
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("052", "052").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := migrator.MigrateWithExpectedCeiling("053"); err == nil || !strings.Contains(err.Error(), "pre-exist") {
		t.Fatalf("MigrateWithExpectedCeiling() = %v", err)
	}
	if aCalls != 1 || bCalls != 0 {
		t.Fatalf("migration calls A=%d B=%d", aCalls, bCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledMigrationRequiresExplicitCeilingButAllowsExactCheckpointedRun(t *testing.T) {
	for _, testCase := range []struct {
		name, expected string
		wantCalls      int
		wantErr        bool
	}{
		{name: "standard blocked", wantErr: true},
		{name: "exact migration-only", expected: "053", wantCalls: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			calls := 0
			migrator := NewMigrator(db)
			migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: &calls})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			if !testCase.wantErr {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", "053").WillReturnResult(sqlmock.NewResult(2, 1))
				mock.ExpectCommit()
			}
			mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))
			err = migrator.MigrateWithExpectedCeiling(testCase.expected)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("MigrateWithExpectedCeiling() = %v", err)
			}
			if calls != testCase.wantCalls {
				t.Fatalf("controlled migration calls = %d", calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControlledMigrationAlreadyAppliedIsSafeUnderStandardRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	migrator := NewMigrator(db)
	migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: &calls})
	expectMigratorStart(mock, []MigrationRecord{
		{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()},
		{ID: 2, Version: "053", Name: "B", ExecutedAt: time.Now()},
	})
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))

	if err := migrator.Migrate(); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	if calls != 0 {
		t.Fatalf("already applied controlled migration ran %d times", calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledMigrationRecordFailureRollsBackDDLAndProvenanceTogether(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	migrator := NewMigrator(db)
	migrator.Register(controlledTestMigration{
		version: "053", prior: "052", ceiling: "053", calls: &calls,
		upTx: func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE controlled ADD CONSTRAINT migration_b CHECK (true)`)
			return err
		},
	})
	expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`ALTER TABLE controlled ADD CONSTRAINT migration_b CHECK (true)`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", "053").WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))

	if err := migrator.MigrateWithExpectedCeiling("053"); err == nil || !strings.Contains(err.Error(), "record migration 053") {
		t.Fatalf("MigrateWithExpectedCeiling() = %v", err)
	}
	if calls != 1 {
		t.Fatalf("controlled migration calls = %d", calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedMigrationRunnerFailsClosedAtEverySessionAndTransactionBoundary(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		setup      func(*Migrator, sqlmock.Sqlmock, *int)
		closedDB   bool
	}{
		{name: "session", want: "acquire migration session", closedDB: true},
		{name: "init", want: "create migrations table", setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnError(sql.ErrConnDone)
		}},
		{name: "lock", want: "acquire migration lock", setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_lock($1)`)).WithArgs(int64(8675309)).WillReturnError(sql.ErrConnDone)
		}},
		{name: "applied", want: "query migrations", setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			expectInitAndLock(mock)
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`)).WillReturnError(sql.ErrConnDone)
			expectUnlock(mock, nil)
		}},
		{name: "plain Up", want: "migration 052 failed", setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(plainTestMigration{version: "052", calls: calls, upErr: sql.ErrConnDone})
			expectMigratorStart(mock, nil)
			expectUnlock(mock, nil)
		}},
		{name: "plain record", want: "record migration 052", setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(plainTestMigration{version: "052", calls: calls})
			expectMigratorStart(mock, nil)
			mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("052", "052").WillReturnError(sql.ErrConnDone)
			expectUnlock(mock, nil)
		}},
		{name: "controlled begin", want: "begin controlled", setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: calls})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			mock.ExpectBegin().WillReturnError(sql.ErrConnDone)
			expectUnlock(mock, nil)
		}},
		{name: "controlled UpTx", want: "controlled up failed", setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: calls, upTx: func(*sql.Tx) error { return errors.New("controlled up failed") }})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			mock.ExpectBegin()
			mock.ExpectRollback()
			expectUnlock(mock, nil)
		}},
		{name: "controlled commit", want: "outcome indeterminate", setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: calls})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", "053").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit().WillReturnError(sql.ErrConnDone)
			expectUnlock(mock, nil)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			migrator := NewMigrator(db)
			if testCase.closedDB {
				db.Close()
			} else {
				defer db.Close()
				testCase.setup(migrator, mock, &calls)
			}
			expected := ""
			if testCase.name == "controlled begin" || testCase.name == "controlled UpTx" || testCase.name == "controlled commit" {
				expected = "053"
			}
			err = migrator.MigrateWithExpectedCeiling(expected)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("MigrateWithExpectedCeiling() = %v", err)
			}
			if !testCase.closedDB {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPinnedMigrationRunnerLogsUnlockFailureAfterSuccessfulControlledCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	migrator := NewMigrator(db)
	migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: &calls})
	expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", "053").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectUnlock(mock, sql.ErrConnDone)
	if err := migrator.MigrateWithExpectedCeiling("053"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledMigrationRecordFailureRetriesCleanlyFromCheckpointA(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	migrator := NewMigrator(db)
	migrator.Register(controlledTestMigration{version: "053", prior: "052", ceiling: "053", calls: &calls})
	for attempt := 0; attempt < 2; attempt++ {
		expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
		mock.ExpectBegin()
		if attempt == 0 {
			mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", "053").WillReturnError(sql.ErrConnDone)
			mock.ExpectRollback()
		} else {
			mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).WithArgs("053", "053").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
		}
		expectUnlock(mock, nil)
		err = migrator.MigrateWithExpectedCeiling("053")
		if attempt == 0 && (err == nil || !strings.Contains(err.Error(), "record migration 053")) {
			t.Fatalf("first attempt = %v", err)
		}
		if attempt == 1 && err != nil {
			t.Fatalf("retry = %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("controlled migration calls = %d", calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledCommitOutcomeIndeterminateErrorPreservesTypedCause(t *testing.T) {
	cause := sql.ErrConnDone
	err := &ControlledCommitOutcomeIndeterminateError{Version: "053", Err: cause}
	wrapped := fmt.Errorf("migration 053 failed: %w", err)
	if !IsControlledCommitOutcomeIndeterminate(wrapped) || !errors.Is(wrapped, cause) {
		t.Fatalf("typed commit error was not preserved: %v", wrapped)
	}
	if !strings.Contains(err.Error(), "053") || IsControlledCommitOutcomeIndeterminate(errors.New("ordinary")) {
		t.Fatalf("typed commit classification failed: %v", err)
	}
}

func TestGetAppliedMigrationsRejectsScanError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "version", "name", "executed_at"}).AddRow("not-an-id", "053", "B", time.Now()),
	)
	if _, err := getAppliedMigrations(context.Background(), db); err == nil || !strings.Contains(err.Error(), "scan migration record") {
		t.Fatalf("scan error = %v", err)
	}
}

func expectInitAndLock(mock sqlmock.Sqlmock) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_lock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectUnlock(mock sqlmock.Sqlmock, failure error) {
	expectation := mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309))
	if failure != nil {
		expectation.WillReturnError(failure)
	} else {
		expectation.WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func expectMigratorStart(mock sqlmock.Sqlmock, applied []MigrationRecord) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_lock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows([]string{"id", "version", "name", "executed_at"})
	for _, record := range applied {
		rows.AddRow(record.ID, record.Version, record.Name, record.ExecutedAt)
	}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`)).WillReturnRows(rows)
}
