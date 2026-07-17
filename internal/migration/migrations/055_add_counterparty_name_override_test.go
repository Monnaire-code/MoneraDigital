package migrations

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMigration055MetadataIsControlledAndForwardOnly(t *testing.T) {
	value := &AddCounterpartyNameOverride{}
	if value.Version() != "055" || value.Description() == "" {
		t.Fatal("unexpected migration 055 metadata")
	}
	if value.RequiredPreexistingVersion() != "054" || value.RequiredExpectedCeiling() != "055" {
		t.Fatal("migration 055 must require the immediately preceding controlled ceiling")
	}
	if err := value.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatal("migration 055 must reject uncontrolled Up")
	}
	if err := value.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatal("migration 055 must be forward-only")
	}
}

func TestMigration055AddsOnlyTheNullableFinanceOverrideColumn(t *testing.T) {
	if !strings.Contains(migration055AddColumnSQL, "ADD COLUMN counterparty_name_override VARCHAR(256)") {
		t.Fatal("migration 055 must add the bounded nullable override column")
	}
	for _, forbidden := range []string{
		"payer_name =", "payee_name =", "UPDATE public.company_fund_transactions",
		"DROP COLUMN", "NOT NULL", "DEFAULT",
	} {
		if strings.Contains(migration055AddColumnSQL, forbidden) {
			t.Fatalf("migration 055 contains forbidden provider/destructive SQL %q", forbidden)
		}
	}
}

func TestMigration055ExecutesInsideTheControlledRunnerTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration055TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration055AddColumnSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&AddCounterpartyNameOverride{}).UpTx(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration055ReturnsDDLFailureForRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration055TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration055AddColumnSQL)).WillReturnError(errors.New("ddl failed"))
	mock.ExpectRollback()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&AddCounterpartyNameOverride{}).UpTx(tx); err == nil {
		t.Fatal("expected migration 055 DDL failure")
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
