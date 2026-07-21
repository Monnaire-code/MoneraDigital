package migrations

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"monera-digital/internal/migration"
)

func TestMigration060MetadataIsControlledAndForwardOnly(t *testing.T) {
	value := &AddManualTransactionVoidColumns{}
	var controlled migration.ControlledMigration = value
	if controlled.Version() != "060" || value.Description() == "" {
		t.Fatal("unexpected migration 060 metadata")
	}
	if value.RequiredPreexistingVersion() != "059" || value.RequiredExpectedCeiling() != "060" {
		t.Fatal("migration 060 must require the immediately preceding controlled ceiling")
	}
	if err := value.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatal("migration 060 must reject uncontrolled Up")
	}
	if err := value.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatal("migration 060 must be forward-only")
	}
}

func TestMigration060AddsNullableVoidMetadataColumnsOnly(t *testing.T) {
	sqlText := migration060AddColumnsSQL
	for _, required := range []string{
		"ADD COLUMN voided_at TIMESTAMPTZ",
		"ADD COLUMN voided_by BIGINT",
		"ADD COLUMN void_reason TEXT",
	} {
		if !strings.Contains(sqlText, required) {
			t.Fatalf("migration 060 must add %q", required)
		}
	}
	for _, forbidden := range []string{
		"NOT NULL",
		"DEFAULT",
		"DROP COLUMN",
		"DELETE FROM",
		"FOREIGN KEY",
		"REFERENCES",
		"UPDATE public.company_fund_transactions",
	} {
		if strings.Contains(sqlText, forbidden) {
			t.Fatalf("migration 060 contains forbidden SQL %q", forbidden)
		}
	}
}

func TestMigration060ExecutesInsideTheControlledRunnerTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration060TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration060AddColumnsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&AddManualTransactionVoidColumns{}).UpTx(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration060ReturnsDDLFailureForRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration060TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration060AddColumnsSQL)).WillReturnError(errors.New("ddl failed"))
	mock.ExpectRollback()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&AddManualTransactionVoidColumns{}).UpTx(tx); err == nil {
		t.Fatal("expected migration 060 DDL failure")
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
