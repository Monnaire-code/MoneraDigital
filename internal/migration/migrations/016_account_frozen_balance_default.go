package migrations

import (
	"database/sql"
	"fmt"
)

// AccountFrozenBalanceDefault adds DEFAULT 0 to account.frozen_balance.
// The column was created NOT NULL without a default, causing FindOrCreateAccountForUpdate
// to fail for first-time depositors when the INSERT omitted the column.
type AccountFrozenBalanceDefault struct{}

func (m *AccountFrozenBalanceDefault) Version() string { return "016" }
func (m *AccountFrozenBalanceDefault) Name() string    { return "account_frozen_balance_default" }
func (m *AccountFrozenBalanceDefault) Description() string {
	return "Add DEFAULT 0 to account.frozen_balance"
}

func (m *AccountFrozenBalanceDefault) Up(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE account ALTER COLUMN frozen_balance SET DEFAULT 0`)
	if err != nil {
		return fmt.Errorf("set frozen_balance default: %w", err)
	}
	return nil
}

func (m *AccountFrozenBalanceDefault) Down(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE account ALTER COLUMN frozen_balance DROP DEFAULT`)
	if err != nil {
		return fmt.Errorf("drop frozen_balance default: %w", err)
	}
	return nil
}
