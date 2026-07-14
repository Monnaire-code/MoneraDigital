package migrations

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestWidenAmountPrecisionUpSafelyUpgradesPreviouslyApplied047Schema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	for _, column := range []struct {
		table string
		name  string
	}{
		{table: "deposits", name: "amount"},
		{table: "coin_chains", name: "min_deposit_amount"},
	} {
		mock.ExpectQuery(regexp.QuoteMeta(columnDataTypeQuery)).
			WithArgs(column.table, column.name).
			WillReturnRows(sqlmock.NewRows([]string{"data_type", "numeric_precision", "numeric_scale"}).AddRow("numeric", 32, 8))
	}
	mock.ExpectExec(`ALTER TABLE deposits(?s).*NUMERIC\(65,\s*18\)`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`ALTER TABLE coin_chains(?s).*NUMERIC\(65,\s*18\)`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	if err := (&WidenAmountPrecision{}).Up(db); err != nil {
		t.Fatalf("WidenAmountPrecision.Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
