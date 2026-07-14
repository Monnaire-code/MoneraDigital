// internal/migration/migrations/047_normalize_amount_types.go
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// NormalizeAmountTypes converts two VARCHAR amount columns to NUMERIC(65, 18):
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
	return "Normalize deposits.amount and coin_chains.min_deposit_amount from VARCHAR to NUMERIC(65, 18) (H-1)"
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

const columnDataTypeQuery = `
SELECT data_type, numeric_precision, numeric_scale
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = $1
  AND column_name = $2`

func precheck047DepositsAmount(tx *sql.Tx) error {
	textual, err := precheck047ColumnRequiresTextValidation(tx, "deposits", "amount")
	if err != nil {
		return fmt.Errorf("inspect deposits.amount type: %w", err)
	}
	if !textual {
		return nil
	}
	stmt := fmt.Sprintf(`
DO $$
DECLARE bad_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO bad_count
    FROM deposits
    WHERE amount IS NOT NULL
      AND (
          amount !~ %s
          OR CASE
              WHEN amount ~ %s THEN amount::numeric <> amount::numeric(65, 18)
              ELSE FALSE
          END
      );
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'H-1: cannot migrate deposits.amount — %% rows are not exact NUMERIC(65,18) literals', bad_count;
    END IF;
END $$;`, quoteString(numericLiteralRegex), quoteString(numericLiteralRegex))
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("precheck deposits.amount: %w", err)
	}
	return nil
}

func precheck047CoinChainsMinDeposit(tx *sql.Tx) error {
	textual, err := precheck047ColumnRequiresTextValidation(tx, "coin_chains", "min_deposit_amount")
	if err != nil {
		return fmt.Errorf("inspect coin_chains.min_deposit_amount type: %w", err)
	}
	if !textual {
		return nil
	}
	stmt := fmt.Sprintf(`
DO $$
DECLARE bad_count INTEGER;
BEGIN
    SELECT COUNT(*) INTO bad_count
    FROM coin_chains
    WHERE min_deposit_amount IS NOT NULL
      AND (
          min_deposit_amount !~ %s
          OR CASE
              WHEN min_deposit_amount ~ %s THEN min_deposit_amount::numeric <> min_deposit_amount::numeric(65, 18)
              ELSE FALSE
          END
      );
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'H-1: cannot migrate coin_chains.min_deposit_amount — %% rows are not exact NUMERIC(65,18) literals', bad_count;
    END IF;
END $$;`, quoteString(numericLiteralRegex), quoteString(numericLiteralRegex))
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("precheck coin_chains.min_deposit_amount: %w", err)
	}
	return nil
}

func precheck047ColumnRequiresTextValidation(tx *sql.Tx, tableName, columnName string) (bool, error) {
	var (
		dataType  string
		precision sql.NullInt64
		scale     sql.NullInt64
	)
	if err := tx.QueryRowContext(context.Background(), columnDataTypeQuery, tableName, columnName).Scan(&dataType, &precision, &scale); err != nil {
		return false, err
	}
	switch dataType {
	case "character varying", "character", "text":
		return true, nil
	case "numeric":
		if !precision.Valid || !scale.Valid {
			return false, fmt.Errorf("%s.%s numeric precision and scale must be explicit", tableName, columnName)
		}
		integerDigits := precision.Int64 - scale.Int64
		if scale.Int64 > 18 || integerDigits > 47 || scale.Int64 < 0 || integerDigits < 1 {
			return false, fmt.Errorf("%s.%s NUMERIC(%d,%d) cannot be widened losslessly to NUMERIC(65,18)", tableName, columnName, precision.Int64, scale.Int64)
		}
		return false, nil
	default:
		return false, fmt.Errorf("%s.%s has unsupported data type %q", tableName, columnName, dataType)
	}
}

func alterDepositsAmount(tx *sql.Tx) error {
	const stmt = `ALTER TABLE deposits
		ALTER COLUMN amount TYPE NUMERIC(65, 18) USING amount::numeric;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("alter deposits.amount: %w", err)
	}
	return nil
}

func alterCoinChainsMinDepositAmount(tx *sql.Tx) error {
	const stmt = `ALTER TABLE coin_chains
		ALTER COLUMN min_deposit_amount TYPE NUMERIC(65, 18) USING min_deposit_amount::numeric;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("alter coin_chains.min_deposit_amount: %w", err)
	}
	return nil
}

func quoteString(s string) string {
	return "'" + s + "'"
}
