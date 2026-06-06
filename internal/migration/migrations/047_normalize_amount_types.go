// internal/migration/migrations/047_normalize_amount_types.go
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// NormalizeAmountTypes converts two VARCHAR amount columns to NUMERIC(32, 8):
// deposits.amount and coin_chains.min_deposit_amount. The application
// reads both via decimal.NewFromString, so a numeric column gives us
// server-side precision guarantees and arithmetic correctness
// (today, `deposits.amount > 0` is a lexicographic compare that returns
// wrong results for any value < 1).
//
// Idempotency contract: pre-check aborts if any row is not a numeric
// literal, preventing a silent `USING col::numeric` that would mask
// bad data. A re-run against an already-NUMERIC column is a no-op.
type NormalizeAmountTypes struct{}

func (m *NormalizeAmountTypes) Version() string { return "047" }

func (m *NormalizeAmountTypes) Description() string {
	return "Normalize deposits.amount and coin_chains.min_deposit_amount from VARCHAR to NUMERIC(32, 8) (H-1)"
}

func (m *NormalizeAmountTypes) Up(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.Tx) error
	}{
		{"precheck_deposits_amount", precheck047DepositsAmount},
		{"precheck_coin_chains_min_deposit", precheck047CoinChainsMinDeposit},
		{"alter_deposits_amount", alterDepositsAmount},
		{"alter_coin_chains_min_deposit_amount", alterCoinChainsMinDepositAmount},
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, s := range steps {
		if err := s.fn(tx); err != nil {
			return fmt.Errorf("step %s failed: %w", s.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func (m *NormalizeAmountTypes) Down(db *sql.DB) error {
	// A NUMERIC→VARCHAR rollback loses precision and is not a safe
	// migration; refuse loudly instead of corrupting data.
	return fmt.Errorf("047: Down is intentionally not implemented; numeric→varchar reversal is destructive. Open a manual cleanup migration if needed")
}

var _ migration.Migration = (*NormalizeAmountTypes)(nil)

// numericLiteralRegex matches integer or decimal literals. No thousand
// separators, no exponent, no currency suffix — those are the cases
// we want the pre-check to fail on.
const numericLiteralRegex = `^-?[0-9]+(\.[0-9]+)?$`

func precheck047DepositsAmount(tx *sql.Tx) error {
	stmt := fmt.Sprintf(`
DO $$
DECLARE bad_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO bad_count
    FROM deposits
    WHERE amount IS NOT NULL
      AND amount !~ %s;
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'H-1: cannot migrate deposits.amount — %% rows are not numeric literals', bad_count;
    END IF;
END $$;`, quoteString(numericLiteralRegex))
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("precheck deposits.amount: %w", err)
	}
	return nil
}

func precheck047CoinChainsMinDeposit(tx *sql.Tx) error {
	stmt := fmt.Sprintf(`
DO $$
DECLARE bad_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO bad_count
    FROM coin_chains
    WHERE min_deposit_amount IS NOT NULL
      AND min_deposit_amount !~ %s;
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'H-1: cannot migrate coin_chains.min_deposit_amount — %% rows are not numeric literals', bad_count;
    END IF;
END $$;`, quoteString(numericLiteralRegex))
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("precheck coin_chains.min_deposit_amount: %w", err)
	}
	return nil
}

func alterDepositsAmount(tx *sql.Tx) error {
	const stmt = `ALTER TABLE deposits
		ALTER COLUMN amount TYPE NUMERIC(32, 8) USING amount::numeric;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("alter deposits.amount: %w", err)
	}
	return nil
}

func alterCoinChainsMinDepositAmount(tx *sql.Tx) error {
	const stmt = `ALTER TABLE coin_chains
		ALTER COLUMN min_deposit_amount TYPE NUMERIC(32, 8) USING min_deposit_amount::numeric;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("alter coin_chains.min_deposit_amount: %w", err)
	}
	return nil
}

func quoteString(s string) string {
	return "'" + s + "'"
}
