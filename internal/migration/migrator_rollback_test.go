package migration

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type rollbackTestMigration struct {
	version string
	downErr error
	calls   *int
}

func (migration rollbackTestMigration) Version() string     { return migration.version }
func (migration rollbackTestMigration) Description() string { return migration.version }
func (migration rollbackTestMigration) Up(*sql.DB) error    { return nil }
func (migration rollbackTestMigration) Down(*sql.DB) error {
	*migration.calls++
	return migration.downErr
}

func TestPinnedRollbackCoversEverySessionAndMutationBoundary(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		setup      func(*Migrator, sqlmock.Sqlmock, *int)
		closedDB   bool
		wantErr    bool
	}{
		{name: "session", want: "acquire rollback session", closedDB: true, wantErr: true},
		{name: "init", want: "create migrations table", wantErr: true, setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnError(sql.ErrConnDone)
		}},
		{name: "lock", want: "acquire migration lock", wantErr: true, setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).WithArgs(int64(8675309)).WillReturnError(sql.ErrConnDone)
		}},
		{name: "applied", want: "query migrations", wantErr: true, setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			expectInitAndLock(mock)
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`)).WillReturnError(sql.ErrConnDone)
			expectUnlock(mock, nil)
		}},
		{name: "empty", setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			expectMigratorStart(mock, nil)
			expectUnlock(mock, nil)
		}},
		{name: "missing migration", want: "migration 999 not found", wantErr: true, setup: func(_ *Migrator, mock sqlmock.Sqlmock, _ *int) {
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "999", Name: "missing", ExecutedAt: time.Now()}})
			expectUnlock(mock, nil)
		}},
		{name: "Down", want: "rollback of migration 052 failed", wantErr: true, setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(rollbackTestMigration{version: "052", downErr: errors.New("down failed"), calls: calls})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			expectUnlock(mock, nil)
		}},
		{name: "delete", want: "remove migration record 052", wantErr: true, setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(rollbackTestMigration{version: "052", calls: calls})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM public.migrations WHERE version = $1`)).WithArgs("052").WillReturnError(sql.ErrConnDone)
			expectUnlock(mock, nil)
		}},
		{name: "unlock", setup: func(migrator *Migrator, mock sqlmock.Sqlmock, calls *int) {
			migrator.Register(rollbackTestMigration{version: "052", calls: calls})
			expectMigratorStart(mock, []MigrationRecord{{ID: 1, Version: "052", Name: "A", ExecutedAt: time.Now()}})
			mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM public.migrations WHERE version = $1`)).WithArgs("052").WillReturnResult(sqlmock.NewResult(0, 1))
			expectUnlock(mock, sql.ErrConnDone)
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
			err = migrator.Rollback()
			if (err != nil) != testCase.wantErr || err != nil && !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Rollback() = %v", err)
			}
			if !testCase.closedDB {
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
