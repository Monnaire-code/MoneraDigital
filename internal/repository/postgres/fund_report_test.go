// internal/repository/postgres/fund_report_test.go
package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
)

// pg42P01 is a 42P01 PgError (relation does not exist). The helper is
// factored into a single literal so the test reads as intent, not SQLSTATE
// trivia.
func pg42P01() *pgconn.PgError {
	return &pgconn.PgError{
		Severity: "ERROR",
		Code:     "42P01",
		Message:  `relation "fund_reports" does not exist`,
	}
}

func TestIsUndefinedTable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated error", errors.New("connection refused"), false},
		{"wrapped 42P01", pg42P01(), true},
		{"double-wrapped 42P01 via fmt.Errorf %w", errWrap(errWrap(pg42P01(), "layer 1"), "layer 2"), true},
		{"different SQLSTATE", &pgconn.PgError{Code: "23505", Message: "unique violation"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isUndefinedTable(tc.err))
		})
	}
}

func errWrap(err error, msg string) error {
	return wrapErr{msg: msg, err: err}
}

type wrapErr struct {
	msg string
	err error
}

func (w wrapErr) Error() string { return w.msg + ": " + w.err.Error() }
func (w wrapErr) Unwrap() error { return w.err }

func TestFundReportRepository_GetLatest_UndefinedTableReturnsErrFundNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("FROM fund_reports").
		WillReturnError(pg42P01())

	repo := NewFundReportRepository(db)
	_, err = repo.GetLatest(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFundNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFundReportRepository_GetLatest_OtherErrorStaysGeneric(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("FROM fund_reports").
		WillReturnError(errors.New("connection refused"))

	repo := NewFundReportRepository(db)
	_, err = repo.GetLatest(context.Background())
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrFundNotFound), "non-PG-undefined error must not be mapped to not-found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFundReportRepository_GetTrend_UndefinedTableReturnsErrFundNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("FROM fund_reports").
		WithArgs(5).
		WillReturnError(pg42P01())

	repo := NewFundReportRepository(db)
	_, err = repo.GetTrend(context.Background(), 5)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFundNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFundReportRepository_GetAllocationsByReportID_UndefinedTableReturnsErrFundNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("FROM fund_asset_allocations").
		WithArgs(int64(5)).
		WillReturnError(pg42P01())

	repo := NewFundReportRepository(db)
	_, err = repo.GetAllocationsByReportID(context.Background(), 5)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrFundNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFundReportRepository_GetLatest_NoRowsStillReturnsErrFundNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery("FROM fund_reports").
		WillReturnRows(sqlmock.NewRows([]string{"id"})) // empty result set

	repo := NewFundReportRepository(db)
	_, err = repo.GetLatest(context.Background())
	assert.ErrorIs(t, err, ErrFundNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}
