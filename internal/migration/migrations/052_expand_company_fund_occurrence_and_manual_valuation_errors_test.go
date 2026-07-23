package migrations

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"monera-digital/internal/migration"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMigration052ProvenanceFailureRollsBackDDLAndRetriesFromCheckpoint051(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrator := migration.NewMigrator(db)
	migrator.Register(&ExpandCompanyFundOccurrenceAndManualValuation{})
	for attempt := 0; attempt < 2; attempt++ {
		expectMigration052RunnerStart(mock)
		expectMigration052EmptyThroughSchema(mock, nil)
		record := mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO public.migrations (version, name) VALUES ($1, $2)`)).
			WithArgs("052", (&ExpandCompanyFundOccurrenceAndManualValuation{}).Description())
		if attempt == 0 {
			record.WillReturnError(errors.New("record failed"))
			mock.ExpectRollback()
		} else {
			record.WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
		}
		mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).WithArgs(int64(8675309)).WillReturnResult(sqlmock.NewResult(0, 1))

		err = migrator.MigrateWithExpectedCeiling("052")
		if attempt == 0 && (err == nil || !strings.Contains(err.Error(), "record migration 052")) {
			t.Fatalf("first attempt = %v", err)
		}
		if attempt == 1 && err != nil {
			t.Fatalf("retry = %v", err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectMigration052RunnerStart(mock sqlmock.Sqlmock) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS public.migrations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).WithArgs(int64(8675309)).WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`)).WillReturnRows(
		sqlmock.NewRows([]string{"id", "version", "name", "executed_at"}).AddRow(51, "051", "Widen amount precision", time.Now()),
	)
}

func TestMigration052UpBeginAndCommitFailures(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))
		if err := runMigration052TestTransaction(db); err == nil || !strings.Contains(err.Error(), "begin failed") {
			t.Fatalf("Up() = %v", err)
		}
	})

	t.Run("commit", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		expectMigration052EmptyThroughSchema(mock, nil)
		mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
		mock.ExpectRollback()
		if err := runMigration052TestTransaction(db); err == nil || !strings.Contains(err.Error(), "commit failed") {
			t.Fatalf("Up() = %v", err)
		}
	})
}

func TestMigration052RunStepFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(sqlmock.Sqlmock)
		want  string
	}{
		{name: "preflight query", want: "preflight", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration052TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(migration052PreflightQuery)).WillReturnError(errors.New("query failed"))
		}},
		{name: "add columns", want: "add nullable", setup: func(mock sqlmock.Sqlmock) {
			expectMigration052Preflight(mock)
			mock.ExpectExec(regexp.QuoteMeta(migration052AddOccurrenceColumnsSQL)).WillReturnError(errors.New("alter failed"))
		}},
		{name: "backfill query", want: "query Safeheron", setup: func(mock sqlmock.Sqlmock) {
			expectMigration052ThroughSchema(mock, nil)
			mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).WillReturnError(errors.New("rows failed"))
		}},
		{name: "missing validation", want: "missing Safeheron", setup: func(mock sqlmock.Sqlmock) {
			expectMigration052ThroughEmptyBackfill(mock)
			mock.ExpectQuery(regexp.QuoteMeta(migration052MissingAliasQuery)).WillReturnError(errors.New("missing failed"))
		}},
		{name: "missing aliases", want: "left 1 missing", setup: func(mock sqlmock.Sqlmock) {
			expectMigration052ThroughEmptyBackfill(mock)
			mock.ExpectQuery(regexp.QuoteMeta(migration052MissingAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
		}},
		{name: "duplicate validation", want: "validate duplicate", setup: func(mock sqlmock.Sqlmock) {
			expectMigration052ThroughMissingValidation(mock)
			mock.ExpectQuery(regexp.QuoteMeta(migration052DuplicateAliasQuery)).WillReturnError(errors.New("duplicates failed"))
		}},
		{name: "schema", want: "install occurrence", setup: func(mock sqlmock.Sqlmock) {
			expectMigration052ThroughSchema(mock, errors.New("ddl failed"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			tc.setup(mock)
			if err := runMigration052(tx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runMigration052() = %v", err)
			}
			mock.ExpectRollback()
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration052BackfillRowFailures(t *testing.T) {
	cases := []struct {
		name         string
		row          *sqlmock.Rows
		expectUpdate func(sqlmock.Sqlmock)
		want         string
	}{
		{name: "scan", row: sqlmock.NewRows(migration052TestColumns()).AddRow("bad-id", "tx", "PRINCIPAL", "ETH", "a", "b", "1", "SINGLE", 0, "EVM"), want: "scan Safeheron"},
		{name: "amount", row: migration052TestRow("not-a-number", "PRINCIPAL", "EVM", "a", "b"), want: "parse Safeheron"},
		{name: "source", row: migration052TestRow("1", "PRINCIPAL", "", "a", "b"), want: "normalize Safeheron"},
		{name: "destination", row: migration052TestRow("1", "PRINCIPAL", "EVM", "a", " "), want: "destination"},
		{name: "canonical", row: migration052TestRow("1", "UNKNOWN", "EVM", "a", "b"), want: "canonicalize"},
		{name: "update", row: migration052TestRow("1", "PRINCIPAL", "EVM", "a", "b"), want: "backfill Safeheron", expectUpdate: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration052BackfillUpdate)).WillReturnError(errors.New("update failed"))
		}},
		{name: "affected error", row: migration052TestRow("1", "PRINCIPAL", "EVM", "a", "b"), want: "inspect Safeheron", expectUpdate: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration052BackfillUpdate)).WillReturnResult(sqlmock.NewErrorResult(errors.New("affected failed")))
		}},
		{name: "affected count", row: migration052TestRow("1", "PRINCIPAL", "EVM", "a", "b"), want: "updated 0 rows", expectUpdate: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration052BackfillUpdate)).WillReturnResult(sqlmock.NewResult(0, 0))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).WillReturnRows(tc.row)
			if tc.expectUpdate != nil {
				tc.expectUpdate(mock)
			}
			if err := migration052Backfill(tx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("backfill() = %v", err)
			}
			mock.ExpectRollback()
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration052BackfillReportsRowsIterationFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).WillReturnRows(migration052TestRow("1", "PRINCIPAL", "EVM", "a", "b").
		AddRow(2, "tx", "PRINCIPAL", "ETH", "a", "b", "1", "SINGLE", 1, "EVM").
		RowError(1, errors.New("iteration failed")))
	mock.ExpectExec(regexp.QuoteMeta(migration052BackfillUpdate)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := migration052Backfill(tx); err == nil || !strings.Contains(err.Error(), "iterate Safeheron") {
		t.Fatalf("backfill() = %v", err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
}

func TestMigration052ReadBackfillRowsClosesBeforeReturning(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).
		WillReturnRows(migration052TestRow("1", "PRINCIPAL", "EVM", "a", "b")).
		RowsWillBeClosed()
	rows, err := db.Query(migration052BackfillQuery)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readMigration052BackfillRows(rows)
	if err != nil || len(got) != 1 {
		t.Fatalf("read rows = %d, %v", len(got), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration052ReadBackfillRowsReportsCloseFailure(t *testing.T) {
	if _, err := readMigration052BackfillRows(migration052CloseErrorRows{}); err == nil || !strings.Contains(err.Error(), "close") {
		t.Fatalf("readMigration052BackfillRows() = %v", err)
	}
}

type migration052CloseErrorRows struct{}

func (migration052CloseErrorRows) Next() bool        { return false }
func (migration052CloseErrorRows) Scan(...any) error { return nil }
func (migration052CloseErrorRows) Err() error        { return nil }
func (migration052CloseErrorRows) Close() error      { return errors.New("close failed") }

func expectMigration052Preflight(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta(migration052TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration052PreflightQuery)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

func expectMigration052ThroughColumns(mock sqlmock.Sqlmock) {
	expectMigration052Preflight(mock)
	mock.ExpectExec(regexp.QuoteMeta(migration052AddOccurrenceColumnsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectMigration052ThroughEmptyBackfill(mock sqlmock.Sqlmock) {
	expectMigration052ThroughSchema(mock, nil)
	mock.ExpectQuery(regexp.QuoteMeta(migration052BackfillQuery)).WillReturnRows(sqlmock.NewRows(migration052TestColumns()))
}

func expectMigration052ThroughMissingValidation(mock sqlmock.Sqlmock) {
	expectMigration052ThroughEmptyBackfill(mock)
	mock.ExpectQuery(regexp.QuoteMeta(migration052MissingAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

func expectMigration052EmptyThroughValidation(mock sqlmock.Sqlmock) {
	expectMigration052ThroughMissingValidation(mock)
	mock.ExpectQuery(regexp.QuoteMeta(migration052DuplicateAliasQuery)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

func expectMigration052EmptyThroughSchema(mock sqlmock.Sqlmock, schemaErr error) {
	mock.ExpectBegin()
	expectMigration052EmptyThroughValidation(mock)
}

func expectMigration052ThroughSchema(mock sqlmock.Sqlmock, schemaErr error) {
	expectMigration052ThroughColumns(mock)
	expect := mock.ExpectExec(regexp.QuoteMeta(migration052SchemaDDL))
	if schemaErr == nil {
		expect.WillReturnResult(sqlmock.NewResult(0, 0))
	} else {
		expect.WillReturnError(schemaErr)
	}
}

func migration052TestColumns() []string {
	return []string{"id", "provider_transaction_id", "movement_kind", "provider_asset_key", "from_address_or_account", "to_address_or_account", "amount", "transfer_mode", "movement_index", "network_family"}
}

func migration052TestRow(amount, kind, family, from, to string) *sqlmock.Rows {
	return sqlmock.NewRows(migration052TestColumns()).AddRow(1, "tx", kind, "ETH", from, to, amount, "SINGLE", 0, family)
}
