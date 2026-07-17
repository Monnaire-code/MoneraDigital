package migrations

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPrecheck048OrphansGuardsLegacyFreezeLogOrderID(t *testing.T) {
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

	mock.ExpectExec(`(?s)column_name = 'order_id'.*wfl\.order_id`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := precheck048Orphans(tx); err != nil {
		t.Fatalf("precheck048Orphans() error = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAddFKWithdrawalFreezeLogGuardsLegacyOrderID(t *testing.T) {
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

	mock.ExpectExec(`(?s)column_name = 'order_id'.*ALTER TABLE withdrawal_freeze_log`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := addFKWithdrawalFreezeLog(tx); err != nil {
		t.Fatalf("addFKWithdrawalFreezeLog() error = %v, want nil", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
