package migrations

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUpdateWalletRequestsTable_UpBootstrapsMissingTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS wallet_creation_requests").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS idx_wallet_creation_requests_user_id").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE wallet_creation_requests").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_requests_user_product_currency").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = (&UpdateWalletRequestsTable{}).Up(db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
