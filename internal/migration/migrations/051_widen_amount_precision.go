package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// WidenAmountPrecision upgrades databases that already recorded the original
// 047 migration at NUMERIC(32,8). Fresh databases reach the same target in 047;
// repeating the ALTER against NUMERIC(65,18) is semantically idempotent.
type WidenAmountPrecision struct{}

func (m *WidenAmountPrecision) Version() string { return "051" }

func (m *WidenAmountPrecision) Description() string {
	return "Widen deposits.amount and coin_chains.min_deposit_amount to NUMERIC(65,18)"
}

func (m *WidenAmountPrecision) Up(db *sql.DB) error {
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

	for _, step := range steps {
		if err := step.fn(tx); err != nil {
			return fmt.Errorf("step %s failed: %w", step.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func (m *WidenAmountPrecision) Down(*sql.DB) error {
	return fmt.Errorf("051: Down is intentionally not implemented; reducing numeric precision is destructive")
}

var _ migration.Migration = (*WidenAmountPrecision)(nil)
