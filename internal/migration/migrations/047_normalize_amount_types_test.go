package migrations

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPrecheck047DepositsAmountSkipsRegexForNumericColumn(t *testing.T) {
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
	defer tx.Rollback()

	mock.ExpectQuery(regexp.QuoteMeta(columnDataTypeQuery)).
		WithArgs("deposits", "amount").
		WillReturnRows(sqlmock.NewRows([]string{"data_type", "numeric_precision", "numeric_scale"}).AddRow("numeric", 65, 18))

	if err := precheck047DepositsAmount(tx); err != nil {
		t.Fatalf("precheck047DepositsAmount() error = %v, want nil for an already-numeric column", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrecheck047DepositsAmountAllowsSafeNumericWidening(t *testing.T) {
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
	defer tx.Rollback()

	mock.ExpectQuery(regexp.QuoteMeta(columnDataTypeQuery)).
		WithArgs("deposits", "amount").
		WillReturnRows(sqlmock.NewRows([]string{"data_type", "numeric_precision", "numeric_scale"}).AddRow("numeric", 32, 8))

	if err := precheck047DepositsAmount(tx); err != nil {
		t.Fatalf("precheck047DepositsAmount() error = %v, want safe NUMERIC(32,8) widening", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrecheck047DepositsAmountRejectsNumericScaleNarrowing(t *testing.T) {
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
	defer tx.Rollback()

	mock.ExpectQuery(regexp.QuoteMeta(columnDataTypeQuery)).
		WithArgs("deposits", "amount").
		WillReturnRows(sqlmock.NewRows([]string{"data_type", "numeric_precision", "numeric_scale"}).AddRow("numeric", 65, 20))

	if err := precheck047DepositsAmount(tx); err == nil {
		t.Fatal("precheck047DepositsAmount() error = nil, want unsafe scale narrowing rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrecheck047DepositsAmountRequiresExactTextConversion(t *testing.T) {
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
	defer tx.Rollback()

	mock.ExpectQuery(regexp.QuoteMeta(columnDataTypeQuery)).
		WithArgs("deposits", "amount").
		WillReturnRows(sqlmock.NewRows([]string{"data_type", "numeric_precision", "numeric_scale"}).AddRow("character varying", nil, nil))
	mock.ExpectExec(`(?s)amount::numeric\s*<>\s*amount::numeric\(65,\s*18\)`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := precheck047DepositsAmount(tx); err != nil {
		t.Fatalf("precheck047DepositsAmount() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAlterDepositsAmountUsesDigitalAssetPrecision(t *testing.T) {
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
	defer tx.Rollback()

	mock.ExpectExec(`NUMERIC\(65,\s*18\)`).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := alterDepositsAmount(tx); err != nil {
		t.Fatalf("alterDepositsAmount() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
